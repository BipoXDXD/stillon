#!/usr/bin/env bash
# Checagens da produção na VPS (Plano 11, spec §8), sem VPS: sobe o
# deploy/vps/compose.prod.yaml com o Caddy em modo local (certificado da CA
# interna, domínio vigilume.teste) e confere o que a revisão exigiu da
# hospedagem. O job "vps" do CI roda; localmente, da raiz do repositório:
#
#   bash deploy/ci/checar-vps.sh <imagem do app>     (ex.: vigilume:dev)
#
# O projeto do compose é "vigilume-teste-vps" e as portas do Caddy vão para o
# loopback (TESTE_VPS_HTTP, padrão 18580; TESTE_VPS_HTTPS, padrão 18543):
# deploy/ci/vps/compose.teste.yaml. A rede é a de produção (172.30.0.0/24):
# não rode numa máquina que já tenha o Vigilume de produção de pé.
# Derruba tudo no fim (TESTE_VPS_MANTER=1 deixa de pé, para investigar).
# O script escreve outros scripts e procura ${...} em arquivos: muita string
# com $ literal entre aspas simples, de propósito.
# shellcheck disable=SC2016
set -euo pipefail

IMAGEM_APP=${1:?uso: checar-vps.sh <imagem do app>}
RAIZ=$(cd "$(dirname "$0")/../.." && pwd)
HTTP=${TESTE_VPS_HTTP:-18580}
HTTPS=${TESTE_VPS_HTTPS:-18543}
DOMINIO=vigilume.teste
PROJETO=vigilume-teste-vps
REPO_TESTE=vigilume-teste-vps
EMAIL=dono@vigilume.teste
SENHA=senha-do-teste-da-vps-2026

falhas=0
falhar() { echo "FALHOU: $*" >&2; falhas=$((falhas + 1)); }
ok() { echo "ok: $*"; }

# O diretório que faz o papel de /opt/vigilume.
DIR=$(mktemp -d)
limpar() {
    if [ "${TESTE_VPS_MANTER:-}" = 1 ]; then
        echo "de pé em $DIR (projeto $PROJETO)" >&2
        return
    fi
    dc down -v --remove-orphans >/dev/null 2>&1 || true
    for r in "$REPO_TESTE" "$REPO_TESTE-fonte"; do
        docker image ls --format '{{.Repository}}:{{.Tag}}' "$r" | xargs -r docker rmi >/dev/null 2>&1 || true
    done
    rm -rf "$DIR" "$TARS"
}
trap limpar EXIT

cp "$RAIZ/deploy/vps/compose.prod.yaml" "$RAIZ/deploy/vps/Caddyfile" "$RAIZ/deploy/vps/initdb.sh" "$DIR/"
cp "$RAIZ/deploy/ci/vps/compose.teste.yaml" "$DIR/"
b64() { openssl rand -base64 32; }
IP_HASH_KEY=$(b64)
cat > "$DIR/.env" <<EOF
VIGILUME_DOMINIO=$DOMINIO
POSTGRES_PASSWORD=$(openssl rand -hex 32)
VIGILUME_APP_PASSWORD=$(openssl rand -hex 32)
VIGILUME_BACKUP_PASSWORD=$(openssl rand -hex 32)
MASTER_KEY=$(b64)
JWT_SECRET=$(b64)
IP_HASH_KEY=$IP_HASH_KEY
RESEND_API_KEY=
EMAIL_FROM=
HC_URL=
VIGILUME_CADDY_TESTE=local_certs
TESTE_VPS_HTTP=$HTTP
TESTE_VPS_HTTPS=$HTTPS
EOF
chmod 600 "$DIR/.env"

# O compose como a entrada o chama: o .env do diretório do projeto para a
# interpolação, mais o arquivo do teste por cima.
dc() { COMPOSE_FILE="$DIR/compose.prod.yaml:$DIR/compose.teste.yaml" docker compose --project-directory "$DIR" "$@"; }
sql() { dc exec -T db psql -U postgres -d vigilume -Atq -c "$1"; }

# A entrada do teste: a de produção com as constantes trocadas (o diretório,
# o nome da imagem, o compose do teste por cima, uma espera menor). Cada troca
# tem de casar exatamente uma linha, ou o teste rodaria outra coisa.
TARS=$(mktemp -d)
ENTRADA=$TARS/vigilume-entrada
trocar() {
    [ "$(grep -cxF -- "$1" "$ENTRADA")" = 1 ] || { echo "FALHOU: a linha '$1' não está (uma vez) no vigilume-entrada" >&2; exit 1; }
    awk -v de="$1" -v para="$2" '$0 == de { print para; next } { print }' "$ENTRADA" > "$ENTRADA.novo" && mv "$ENTRADA.novo" "$ENTRADA"
}
cp "$RAIZ/deploy/vps/vigilume-entrada" "$ENTRADA"
trocar 'DIR=/opt/vigilume' "DIR=$DIR"
trocar 'IMAGEM=vigilume' "IMAGEM=$REPO_TESTE"
trocar 'ARQUIVOS_COMPOSE="$DIR/compose.prod.yaml"' 'ARQUIVOS_COMPOSE="$DIR/compose.prod.yaml:$DIR/compose.teste.yaml"'
trocar 'ESPERA_S=90' 'ESPERA_S=45'
trocar 'TMP_BASE=/var/tmp' "TMP_BASE=$TARS"
# Como o sudo a roda: ambiente limpo, só o pedido do SSH. A saída padrão e a
# de erro ficam em $TARS/saida e $TARS/erro; devolve o código.
entrada() {
    local pedido=$1 codigo=0
    shift
    env -i PATH="$PATH" HOME="$HOME" SSH_ORIGINAL_COMMAND="$pedido" bash "$ENTRADA" "$@" \
        > "$TARS/saida" 2> "$TARS/erro" || codigo=$?
    echo "$codigo"
}
# O tar como o deploy.yml o manda (crane pull --format oci): layout OCI com o
# manifesto linux/amd64 da imagem, SEM nome. Imprime o digest do manifesto,
# que é o image-id do protocolo.
oci_tar() {
    local img=$1 saida=$2 x y d f m
    x=$(mktemp -d "$TARS/x.XXXX")
    y=$(mktemp -d "$TARS/y.XXXX")
    docker save "$img" | tar -xf - -C "$x"
    d=$(jq -r '.manifests[0].digest' "$x/index.json")
    while :; do
        f="$x/blobs/${d/://}"
        case "$(jq -r '.mediaType // empty' "$f")" in
            *index* | *manifest.list*)
                d=$(jq -r '[.manifests[] | select(.platform.os == "linux" and .platform.architecture == "amd64")][0].digest' "$f") ;;
            *) break ;;
        esac
    done
    m=$d
    mkdir -p "$y/blobs/sha256"
    cp "$x/oci-layout" "$y/"
    for b in "$m" $(jq -r '.config.digest, .layers[].digest' "$x/blobs/${m/://}"); do
        cp "$x/blobs/${b/://}" "$y/blobs/sha256/"
    done
    printf '{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"mediaType":"%s","digest":"%s","size":%s}]}' \
        "$(jq -r .mediaType "$x/blobs/${m/://}")" "$m" "$(stat -c %s "$x/blobs/${m/://}")" > "$y/index.json"
    tar -cf "$saida" -C "$y" .
    rm -rf "$x" "$y"
    echo "$m"
}
# Imagens diferentes do mesmo app (um LABEL a mais), e uma que nunca fica
# saudável (o boot recusa ARGON2_MAX_CONCURRENT fora de 1–64; o compose não
# define essa variável, e o ENV da imagem vale).
fonte() {
    local nome=$1 extra=$2
    printf 'FROM %s\n%s\n' "$IMAGEM_APP" "$extra" | docker build -q -t "$REPO_TESTE-fonte:$nome" - >/dev/null
    oci_tar "$REPO_TESTE-fonte:$nome" "$TARS/$nome.tar"
}
sha_de() { printf %s "$1" | sha1sum | cut -c1-40; }
versao() { cat "$DIR/versao" 2>/dev/null || echo -; }
imagem_do_app() { docker inspect -f '{{.Config.Image}}' "$(dc ps -q app)"; }

# ---------------------------------------------------------------------------
# 1. O compose.prod.yaml sozinho (o que vai para a VPS), pela config resolvida.
# ---------------------------------------------------------------------------
cfg=$(cd "$DIR" && docker compose -f compose.prod.yaml config --format json)
q() { jq -r "$1" <<<"$cfg"; }

[ "$(q '[.services | to_entries[] | select(.value.ports != null and (.value.ports | length) > 0) | .key] | join(",")')" = caddy ] ||
    falhar "algum serviço além do caddy publica porta"
[ "$(q '[.services.caddy.ports[] | "\(.host_ip)|\(.published)|\(.target)|\(.protocol)"] | sort | join(" ")')" = "0.0.0.0|443|443|tcp 0.0.0.0|80|80|tcp" ] ||
    falhar "o caddy não publica exatamente 80 e 443 (tcp, IPv4): $(q '.services.caddy.ports')"
[ "$(q '.services | keys | sort | join(",")')" = app,caddy,db ] ||
    falhar "serviços do compose.prod: $(q '.services | keys | join(",")'), queria app, caddy e db"
for s in caddy app db; do
    [ "$(q ".services.$s.restart")" = unless-stopped ] || falhar "$s sem restart: unless-stopped"
    [ "$(q ".services.$s.mem_limit // empty")" != "" ] || falhar "$s sem mem_limit"
done
# GOMEMLIMIT abaixo do mem_limit do app (obrigação 4).
gomem=$(q '.services.app.environment.GOMEMLIMIT // empty')
limite=$(q '.services.app.mem_limit')
case "$gomem" in
    *MiB) gomem_b=$((${gomem%MiB} * 1024 * 1024)) ;;
    *GiB) gomem_b=$((${gomem%GiB} * 1024 * 1024 * 1024)) ;;
    *) gomem_b=0 ;;
esac
[ "$gomem_b" -gt 0 ] && [ "$gomem_b" -lt "$limite" ] ||
    falhar "GOMEMLIMIT '$gomem' não está abaixo do mem_limit do app ($limite bytes)"
# O IP do cliente (obrigação 2): TRUSTED_PROXIES é o IP fixo do Caddy.
caddy_ip=$(q '.services.caddy.networks.vigilume.ipv4_address')
[ "$caddy_ip" = 172.30.0.2 ] || falhar "IP do caddy é '$caddy_ip', queria 172.30.0.2"
[ "$(q '.services.app.environment.TRUSTED_PROXIES')" = "$caddy_ip/32" ] ||
    falhar "TRUSTED_PROXIES do app não é $caddy_ip/32"
[ "$(q '.services.app.environment.CLIENT_IP_HEADER')" = "" ] || falhar "CLIENT_IP_HEADER do app não é vazio"
[ "$(q '.services.app.environment.PUBLIC_BASE_URL')" = "https://$DOMINIO" ] || falhar "PUBLIC_BASE_URL não é https://<domínio>"
[ "$(q '.services.app.environment.METRICS_ADDR')" = off ] || falhar "METRICS_ADDR do app não é off"
[ "$(q '.services.app.networks.vigilume.ipv4_address')/$(q '.services.db.networks.vigilume.ipv4_address')" = 172.30.0.3/172.30.0.4 ] ||
    falhar "IPs fixos do app e do db não são 172.30.0.3 e 172.30.0.4"
[ "$(q '.networks.vigilume.enable_ipv6')" = false ] || falhar "a rede vigilume não desliga o IPv6"
[ "$(q '.networks.vigilume.ipam.config[0].subnet')" = 172.30.0.0/24 ] || falhar "sub-rede da rede vigilume não é 172.30.0.0/24"
# O Caddy não recebe segredo nenhum; o app não recebe a senha do superusuário.
[ "$(q '.services.caddy.environment | keys | sort | join(",")')" = VIGILUME_CADDY_TESTE,VIGILUME_DOMINIO ] ||
    falhar "o caddy recebe variáveis além do domínio e do modo de teste: $(q '.services.caddy.environment | keys | join(",")')"
[ "$(q '.services.app.environment | has("POSTGRES_PASSWORD") or has("VIGILUME_BACKUP_PASSWORD")')" = false ] ||
    falhar "o app recebe a senha do superusuário ou a do backup"
grep -q -- '-demo-\|ZGVtby12aWdpbHVtZS' "$RAIZ/deploy/vps/compose.prod.yaml" && falhar "o compose.prod.yaml tem chave de demonstração"
# O env.exemplo é o modelo do .env: sem valor nenhum, e com toda variável que
# o compose.prod.yaml lê (menos VIGILUME_SHA, da entrada, e o modo de teste).
grep -Ev '^(#|$)' "$RAIZ/deploy/vps/env.exemplo" | grep -v '=$' && falhar "o env.exemplo tem valor preenchido"
while read -r v; do
    case "$v" in VIGILUME_SHA | VIGILUME_CADDY_TESTE) continue ;; esac
    grep -q "^$v=$" "$RAIZ/deploy/vps/env.exemplo" || falhar "o env.exemplo não tem $v"
done < <(grep -o '\${[A-Z_]*' "$RAIZ/deploy/vps/compose.prod.yaml" | sed 's/^\${//' | sort -u)
ok "compose.prod.yaml: portas, restart, mem_limit, GOMEMLIMIT, IPs fixos, IPv6, variáveis"

# A entrada e o compose falam da mesma imagem.
grep -qx 'IMAGEM=vigilume' "$RAIZ/deploy/vps/vigilume-entrada" &&
    [ "$(q '.services.app.image')" = "vigilume:atual" ] ||
    falhar "o IMAGEM do vigilume-entrada e o image do app no compose.prod.yaml não casam"

# ---------------------------------------------------------------------------
# 2. A pilha de pé pelo vigilume-entrada, com o Caddy em modo local.
# ---------------------------------------------------------------------------
ID_A=$(fonte a 'LABEL vigilume.teste=a')
ID_DOENTE=$(fonte doente 'ENV ARGON2_MAX_CONCURRENT=999')
SHA_A=$(sha_de a)
SHA_DOENTE=$(sha_de doente)

# 2a. Primeiro deploy que não fica saudável: sem versão anterior, o app para
#     e o código é 4.
c=$(entrada "deploy $SHA_DOENTE $ID_DOENTE" < "$TARS/doente.tar")
[ "$c" = 4 ] || falhar "primeiro deploy doente saiu $c, queria 4: $(tail -3 "$TARS/erro")"
grep -q 'sem versão anterior' "$TARS/erro" || falhar "primeiro deploy doente não disse que não havia versão anterior"
[ "$(versao)" = - ] || falhar "primeiro deploy doente gravou a versão $(versao)"
[ -z "$(dc ps -q app)" ] || falhar "primeiro deploy doente deixou o app de pé"

# 2b. O deploy saudável.
c=$(entrada "deploy $SHA_A $ID_A" < "$TARS/a.tar")
if [ "$c" != 0 ]; then
    cat "$TARS/erro" >&2
    dc logs >&2
    echo "FALHOU: o deploy de $SHA_A saiu $c" >&2
    exit 1
fi
[ "$(versao)" = "$SHA_A" ] || falhar "depois do deploy, versao = $(versao)"
[ "$(imagem_do_app)" = "$REPO_TESTE:$SHA_A" ] || falhar "o app roda $(imagem_do_app), queria $REPO_TESTE:$SHA_A"
[ "$(docker image inspect -f '{{.Id}}' "$REPO_TESTE:atual")" = "$ID_A" ] || falhar "$REPO_TESTE:atual não aponta para a versão no ar"
[ ! -s "$TARS/saida" ] || falhar "o deploy escreveu na saída padrão"
ok "vigilume-entrada: primeiro deploy doente para o app (4); deploy saudável (0)"

# Só o caddy publica, e só 80 e 443 (de dentro do contêiner).
publicadas=$(dc ps --format json | jq -rs '[.[] | .Service as $s | (.Publishers // [])[] | select(.PublishedPort > 0) | "\($s):\(.TargetPort)"] | sort | unique | join(" ")')
[ "$publicadas" = "caddy:443 caddy:80" ] || falhar "portas publicadas na pilha: '$publicadas'"
for s in app db; do
    [ "$(docker inspect -f '{{json .HostConfig.PortBindings}}' "$(dc ps -q "$s")")" = "{}" ] ||
        falhar "$s tem porta publicada no host"
done
rede=$(docker inspect -f '{{range $k, $v := .NetworkSettings.Networks}}{{$k}}{{end}}' "$(dc ps -q db)")
[ "$(docker network inspect -f '{{.EnableIPv6}}' "$rede")" = false ] || falhar "a rede $rede tem IPv6"

# A CA interna do Caddy, para o curl conferir o certificado de verdade.
dc exec -T caddy cat /data/caddy/pki/authorities/local/root.crt > "$DIR/ca.crt"
cu() { curl -sS --max-time 10 --cacert "$DIR/ca.crt" --resolve "$DOMINIO:$HTTPS:127.0.0.1" "$@"; }
BASE=https://$DOMINIO:$HTTPS

# 3. /healthz pelo Caddy, com TLS conferido, chegando ao app (X-Trace-Id é do app).
resp=$(cu -D - -o /dev/null "$BASE/healthz" || true)
echo "$resp" | head -1 | grep -q ' 200' || falhar "/healthz pelo Caddy não deu 200: $(echo "$resp" | head -1)"
echo "$resp" | grep -qi '^x-trace-id:' || falhar "/healthz pelo Caddy não veio do app (sem X-Trace-Id)"
ok "/healthz pelo Caddy, com o certificado da CA interna"

# 4. Host estranho não chega ao app.
#    a) outro nome no SNI: sem certificado para ele, nada responde.
if curl -sS --max-time 10 --cacert "$DIR/ca.crt" --resolve "estranho.teste:$HTTPS:127.0.0.1" \
    -o /dev/null "https://estranho.teste:$HTTPS/healthz" 2>/dev/null; then
    falhar "https com SNI estranho.teste respondeu"
fi
#    b) o SNI do domínio com outro Host.
resp=$(cu -D - -o /dev/null -H 'Host: estranho.teste' "$BASE/healthz" || true)
echo "$resp" | grep -qi '^x-trace-id:' && falhar "Host estranho pelo https chegou ao app"
#    c) http puro, com outro Host e com Host nenhum reconhecível (o IP).
for h in estranho.teste 127.0.0.1; do
    resp=$(curl -sS --max-time 10 -D - -o /dev/null -H "Host: $h" "http://127.0.0.1:$HTTP/healthz" || true)
    echo "$resp" | grep -qi '^x-trace-id:' && falhar "Host $h pelo http chegou ao app"
done
#    d) o domínio pelo http vira redirecionamento para o https, sem passar no app.
resp=$(curl -sS --max-time 10 -D - -o /dev/null -H "Host: $DOMINIO" "http://127.0.0.1:$HTTP/healthz" || true)
echo "$resp" | head -1 | grep -q ' 308' || falhar "http no domínio não redirecionou (308): $(echo "$resp" | head -1)"
ok "Host estranho não chega ao app"

# 5. O IP que o app vê é o da conexão com o Caddy, com qualquer cabeçalho que
#    o cliente mande. O observável é o ip_hash da sessão criada pelo login:
#    HMAC-SHA256(IP_HASH_KEY, ip), o mesmo cálculo de crypto.HashIP.
hmac() { printf %s "$1" | openssl dgst -sha256 -mac HMAC -macopt "hexkey:$(printf %s "$IP_HASH_KEY" | base64 -d | od -An -tx1 | tr -d ' \n')" | sed 's/^.*= //'; }
printf '%s\n' "$SENHA" | dc exec -T app /vigilume user create --email "$EMAIL" >&2 ||
    { falhar "vigilume user create falhou"; exit 1; }
papeis=$(sql "SELECT string_agg(rolname || ':' || rolsuper::int || rolbypassrls::int || pg_has_role(oid, 'pg_read_all_data', 'MEMBER')::int, ' ' ORDER BY rolname) FROM pg_roles WHERE rolname IN ('vigilume_app', 'vigilume_backup')")
[ "$papeis" = "vigilume_app:000 vigilume_backup:011" ] ||
    falhar "papéis do initdb.sh: '$papeis', queria vigilume_app:000 vigilume_backup:011 (super, bypassrls, pg_read_all_data)"
entrar() {
    cu -o /dev/null -w '%{http_code}' -X POST "$BASE/api/v1/auth/login" \
        -H 'Content-Type: application/json' -H 'X-Requested-With: vigilume' "$@" \
        -d "{\"email\":\"$EMAIL\",\"password\":\"$SENHA\"}"
}
ultimo_hash() { sql "SELECT encode(ip_hash, 'hex') FROM sessions ORDER BY created_at DESC, id DESC LIMIT 1"; }

[ "$(entrar)" = 200 ] || falhar "login pelo Caddy não deu 200"
real=$(ultimo_hash)
[ -n "$real" ] || falhar "a sessão do login não tem ip_hash"
[ "$real" = "$(hmac "$caddy_ip")" ] && falhar "o app viu o IP do Caddy: TRUSTED_PROXIES não casa, e todo cliente divide um balde"
[ "$(entrar -H 'X-Real-IP: 1.2.3.4' -H 'Fly-Client-IP: 1.2.3.5' -H 'CF-Connecting-IP: 1.2.3.6' -H 'X-Forwarded-For: 1.2.3.7')" = 200 ] ||
    falhar "login com cabeçalhos forjados não deu 200"
forjado=$(ultimo_hash)
[ "$forjado" = "$real" ] || falhar "cabeçalhos do cliente mudaram o IP visto pelo app"
for ip in 1.2.3.4 1.2.3.5 1.2.3.6 1.2.3.7; do
    [ "$forjado" = "$(hmac "$ip")" ] && falhar "o app acreditou no IP forjado $ip"
done
# O aviso da B3 (X-Forwarded-For de fora de TRUSTED_PROXIES) não aparece.
dc logs app 2>&1 | grep -q 'não vem de TRUSTED_PROXIES' && falhar "o app avisou que o X-Forwarded-For não vem de TRUSTED_PROXIES"
ok "X-Real-IP, Fly-Client-IP, CF-Connecting-IP e X-Forwarded-For do cliente não mudam o IP visto"

# 6. O Caddy tira os três cabeçalhos: com um CLIENT_IP_HEADER posto por
#    engano, o app cai no IP do Caddy (e avisa), e não no valor do cliente.
for cab in X-Real-IP Fly-Client-IP CF-Connecting-IP; do
    cat > "$DIR/compose.cabecalho.yaml" <<EOF
services:
  app:
    environment:
      CLIENT_IP_HEADER: $cab
EOF
    if ! COMPOSE_FILE="$DIR/compose.prod.yaml:$DIR/compose.teste.yaml:$DIR/compose.cabecalho.yaml" VIGILUME_SHA=$SHA_A \
        docker compose --project-directory "$DIR" up -d --wait --wait-timeout 120 app >&2; then
        falhar "o app não subiu com CLIENT_IP_HEADER=$cab"
        continue
    fi
    [ "$(entrar -H "$cab: 9.8.7.6")" = 200 ] || falhar "login com $cab não deu 200"
    h=$(ultimo_hash)
    [ "$h" = "$(hmac 9.8.7.6)" ] && falhar "o Caddy repassou o $cab do cliente"
    [ "$h" = "$(hmac "$caddy_ip")" ] || falhar "com CLIENT_IP_HEADER=$cab o app não caiu no IP do Caddy"
done
rm -f "$DIR/compose.cabecalho.yaml"
VIGILUME_SHA=$SHA_A dc up -d --wait --wait-timeout 120 app >&2 || falhar "o app não voltou à configuração de produção"
ok "o Caddy remove X-Real-IP, Fly-Client-IP e CF-Connecting-IP"

# 7. O banco não aceita conexão de fora da rede do compose. O controle
#    positivo (de dentro da rede) prova que a sonda funciona.
pg=$(docker inspect -f '{{.Config.Image}}' "$(dc ps -q db)")
docker run --rm --network "$rede" "$pg" pg_isready -h 172.30.0.4 -t 5 >/dev/null ||
    falhar "controle: de dentro da rede, o pg_isready não alcançou o banco"
if docker run --rm "$pg" pg_isready -h 172.30.0.4 -t 5 >/dev/null 2>&1; then
    falhar "de outra rede do Docker, o banco aceitou conexão"
fi
ok "o banco só aceita conexão de dentro da rede"

# ---------------------------------------------------------------------------
# 8. vigilume-entrada: recusas, imagem divergente, rollback, as 3 últimas.
# ---------------------------------------------------------------------------
# 8a. Pedidos recusados com 2, antes de ler a entrada padrão ou mexer em
#     qualquer coisa. O tar da entrada padrão é o de a, válido: o que está
#     errado é só o pedido.
recusa() {
    local pedido=$1 motivo=$2 c
    shift 2
    c=$(entrada "$pedido" "$@" < "$TARS/a.tar")
    [ "$c" = 2 ] || falhar "pedido '$pedido' ($motivo) saiu $c, queria 2"
}
recusa "" "vazio"
recusa "reiniciar" "verbo desconhecido"
recusa "DEPLOY $SHA_A $ID_A" "verbo em maiúsculas"
recusa "deploy" "sem argumentos"
recusa "deploy $SHA_A" "sem image-id"
recusa "deploy $SHA_A $ID_A extra" "palavra a mais"
recusa "deploy ${SHA_A:0:39} $ID_A" "SHA com 39 caracteres"
recusa "deploy ${SHA_A}0 $ID_A" "SHA com 41 caracteres"
recusa "deploy $(tr a-f A-F <<<"$SHA_A") $ID_A" "SHA em maiúsculas"
recusa "deploy g${SHA_A:1} $ID_A" "SHA com letra fora do hex"
recusa "deploy $SHA_A ${ID_A#sha256:}" "image-id sem sha256:"
recusa "deploy $SHA_A ${ID_A:0:70}" "image-id curto"
recusa "deploy $SHA_A;id $ID_A" "caractere de shell"
recusa 'deploy $(id) '"$ID_A" "substituição de comando"
# Com um pedido e um tar válidos: só o argumento é recusado.
c=$(entrada "deploy $SHA_A $ID_A" backup < "$TARS/a.tar")
[ "$c" = 2 ] || falhar "argumento na linha de comando saiu $c, queria 2"
[ "$(versao)" = "$SHA_A" ] && [ "$(imagem_do_app)" = "$REPO_TESTE:$SHA_A" ] || falhar "um pedido recusado mexeu na versão"
c=$(entrada "deploy $SHA_A $ID_A" < /dev/null)
[ "$c" = 2 ] || falhar "deploy sem nada na entrada padrão saiu $c, queria 2"
ok "vigilume-entrada: pedidos malformados recusados com 2"

# 8b. Imagem divergente: o tar de uma imagem com o ID de outra. Nada é
#     etiquetado e a versão no ar não muda.
ID_B=$(fonte b 'LABEL vigilume.teste=b')
SHA_B=$(sha_de b)
c=$(entrada "deploy $SHA_B $ID_A" < "$TARS/b.tar")
[ "$c" = 3 ] || falhar "tar de b com o ID de a saiu $c, queria 3"
docker image inspect "$REPO_TESTE:$SHA_B" >/dev/null 2>&1 && falhar "a imagem divergente foi etiquetada"
c=$(entrada "deploy $SHA_B sha256:$(printf '0%.0s' {1..64})" < "$TARS/b.tar")
[ "$c" = 3 ] || falhar "ID inexistente saiu $c, queria 3"
# Um tar COM nome (o docker save comum) é recusado antes do load: o nome
# etiquetaria outra imagem da máquina.
docker tag "$REPO_TESTE-fonte:b" "$REPO_TESTE-fonte:b-com-nome"
docker save "$REPO_TESTE-fonte:b-com-nome" -o "$TARS/b-com-nome.tar"
docker rmi "$REPO_TESTE-fonte:b-com-nome" >/dev/null
c=$(entrada "deploy $SHA_B $ID_B" < "$TARS/b-com-nome.tar")
[ "$c" = 3 ] || falhar "tar com nome saiu $c, queria 3"
docker image inspect "$REPO_TESTE-fonte:b-com-nome" >/dev/null 2>&1 && falhar "o tar com nome foi carregado (a etiqueta voltou)"
# O mesmo vale para um tar OCI sem manifest.json com o nome numa anotação do
# index.json (as duas que o containerd lê).
for anot in io.containerd.image.name org.opencontainers.image.ref.name; do
    rm -rf "$TARS/anotado" && mkdir "$TARS/anotado"
    tar -xf "$TARS/b.tar" -C "$TARS/anotado"
    jq --arg a "$anot" --arg n "docker.io/library/$REPO_TESTE-fonte:anotado" \
        '.manifests[0].annotations = {($a): $n}' "$TARS/anotado/index.json" > "$TARS/index.json"
    mv "$TARS/index.json" "$TARS/anotado/index.json"
    tar -cf "$TARS/b-anotado.tar" -C "$TARS/anotado" .
    c=$(entrada "deploy $SHA_B $ID_B" < "$TARS/b-anotado.tar")
    [ "$c" = 3 ] || falhar "tar com $anot saiu $c, queria 3"
    docker image inspect "$REPO_TESTE-fonte:anotado" >/dev/null 2>&1 && falhar "o tar com $anot foi carregado com o nome"
    docker rmi "$REPO_TESTE-fonte:anotado" >/dev/null 2>&1 || true
done
[ "$(versao)" = "$SHA_A" ] && [ "$(imagem_do_app)" = "$REPO_TESTE:$SHA_A" ] || falhar "uma imagem divergente mexeu na versão"
ok "vigilume-entrada: imagem divergente e tar com nome recusados com 3"

# 8c. Rollback: um deploy que não fica saudável volta à versão anterior.
c=$(entrada "deploy $SHA_DOENTE $ID_DOENTE" < "$TARS/doente.tar")
[ "$c" = 4 ] || falhar "deploy doente saiu $c, queria 4: $(tail -3 "$TARS/erro")"
grep -q 'rollback feito' "$TARS/erro" || falhar "o deploy doente não disse que fez o rollback"
grep -q 'ARGON2_MAX_CONCURRENT' "$TARS/erro" || falhar "o deploy doente não mostrou o log do app"
[ "$(versao)" = "$SHA_A" ] || falhar "depois do rollback, versao = $(versao)"
[ "$(imagem_do_app)" = "$REPO_TESTE:$SHA_A" ] || falhar "depois do rollback o app roda $(imagem_do_app)"
[ "$(docker image inspect -f '{{.Id}}' "$REPO_TESTE:atual")" = "$ID_A" ] || falhar "depois do rollback, atual não é a versão no ar"
[ "$(docker inspect -f '{{.State.Health.Status}}' "$(dc ps -q app)")" = healthy ] || falhar "depois do rollback o app não está saudável"
[ "$(cu -o /dev/null -w '%{http_code}' "$BASE/healthz")" = 200 ] || falhar "depois do rollback, /healthz pelo Caddy não deu 200"
ok "vigilume-entrada: deploy doente volta à versão anterior (4)"

# 8d. Deploys seguidos: versao-anterior anda, e ficam as 3 últimas imagens
#     (as 3 últimas que ficaram no ar; a doente sai), fora a etiqueta atual.
ID_C=$(fonte c 'LABEL vigilume.teste=c')
SHA_C=$(sha_de c)
ID_D=$(fonte d 'LABEL vigilume.teste=d')
SHA_D=$(sha_de d)
for par in "b $SHA_B $ID_B" "c $SHA_C $ID_C" "d $SHA_D $ID_D"; do
    read -r nome sha id <<<"$par"
    c=$(entrada "deploy $sha $id" < "$TARS/$nome.tar")
    [ "$c" = 0 ] || falhar "deploy de $nome saiu $c: $(tail -3 "$TARS/erro")"
done
[ "$(versao)" = "$SHA_D" ] || falhar "depois de b, c e d, versao = $(versao)"
[ "$(cat "$DIR/versao-anterior" 2>/dev/null)" = "$SHA_C" ] || falhar "versao-anterior não é a de c"
restantes=$(docker image ls "$REPO_TESTE" --format '{{.Tag}}' | grep -vx atual | sort | tr '\n' ' ')
esperadas=$(printf '%s\n' "$SHA_B" "$SHA_C" "$SHA_D" | sort | tr '\n' ' ')
[ "$restantes" = "$esperadas" ] || falhar "imagens que sobraram: '$restantes', queria as de b, c e d"
# O mesmo SHA de novo (redeploy) é aceito e não mexe na anterior.
c=$(entrada "deploy $SHA_D $ID_D" < "$TARS/d.tar")
[ "$c" = 0 ] || falhar "redeploy de d saiu $c: $(tail -3 "$TARS/erro")"
[ "$(cat "$DIR/versao-anterior")" = "$SHA_C" ] || falhar "o redeploy de d mudou a versao-anterior"
ok "vigilume-entrada: versao-anterior e as 3 últimas imagens"

# 8e. Docker sem o image store containerd: recusa com 1. Um docker falso na
#     frente do PATH responde ao `docker info` como o store clássico.
mkdir -p "$TARS/bin"
printf '#!/bin/sh\nif [ "$1" = info ]; then echo "[[Backing Filesystem extfs] [Supports d_type true]]"; exit 0; fi\nexec %s "$@"\n' \
    "$(command -v docker)" > "$TARS/bin/docker"
chmod +x "$TARS/bin/docker"
sed 's|^export PATH=.*|export PATH="$PATH"|' "$ENTRADA" > "$TARS/entrada-path"
c=0
env -i PATH="$TARS/bin:$PATH" HOME="$HOME" SSH_ORIGINAL_COMMAND="deploy $SHA_A $ID_A" \
    bash "$TARS/entrada-path" < "$TARS/a.tar" > /dev/null 2> "$TARS/erro" || c=$?
[ "$c" = 1 ] || falhar "sem o store containerd a entrada saiu $c, queria 1"
grep -q 'containerd' "$TARS/erro" || falhar "sem o store containerd a entrada não disse por quê"
ok "vigilume-entrada: recusa o Docker sem o image store containerd"

# 8f. O sudoers: sintaxe (visudo) e efeito, num Ubuntu 24.04 descartável com
#     o usuário deploy e uma entrada falsa que mostra o que recebeu.
printf '#!/bin/sh\necho "args=$# pedido=${SSH_ORIGINAL_COMMAND:-} outra=${OUTRA:-} usuario=$(id -un)"\n' > "$TARS/entrada-falsa"
cat > "$TARS/sudo.sh" <<'FIM'
set -eu
apt-get update -qq >/dev/null && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq sudo >/dev/null
useradd -m -s /bin/bash deploy
install -m 0755 /t/entrada-falsa /usr/local/sbin/vigilume-entrada
install -m 0440 /t/sudoers /etc/sudoers.d/vigilume-entrada
visudo -cf /etc/sudoers.d/vigilume-entrada
visudo -c >/dev/null
como_deploy() { su deploy -c "$1" 2>&1 || echo "negado"; }
echo "A $(como_deploy 'SSH_ORIGINAL_COMMAND="deploy abc" OUTRA=x sudo -n /usr/local/sbin/vigilume-entrada')"
echo "B $(como_deploy 'sudo -n /usr/local/sbin/vigilume-entrada backup' | tail -1)"
echo "C $(como_deploy 'sudo -n /bin/sh -c id' | tail -1)"
echo "D $(como_deploy 'sudo -n /usr/bin/id' | tail -1)"
FIM
cp "$RAIZ/deploy/vps/sudoers-vigilume-entrada" "$TARS/sudoers"
chmod 644 "$TARS/entrada-falsa" "$TARS/sudo.sh" "$TARS/sudoers"
chmod 755 "$TARS"
docker run --rm -v "$TARS:/t:ro" ubuntu:24.04 bash /t/sudo.sh > "$TARS/sudo.log" 2>&1 ||
    falhar "o teste do sudoers não rodou: $(tail -5 "$TARS/sudo.log")"
grep -qx 'A args=0 pedido=deploy abc outra= usuario=root' "$TARS/sudo.log" ||
    falhar "sudoers: o deploy não roda a entrada como root com o SSH_ORIGINAL_COMMAND (e só ele): $(grep '^A ' "$TARS/sudo.log")"
grep -qx 'B negado' "$TARS/sudo.log" || falhar "sudoers: a entrada aceitou argumento: $(grep '^B ' "$TARS/sudo.log")"
grep -qx 'C negado' "$TARS/sudo.log" || falhar "sudoers: o deploy rodou um shell como root"
grep -qx 'D negado' "$TARS/sudo.log" || falhar "sudoers: o deploy rodou outro comando como root"
ok "sudoers: só a entrada, sem argumento, com o SSH_ORIGINAL_COMMAND"

if [ "$falhas" -gt 0 ]; then
    dc logs >&2
    exit 1
fi
echo "vps conferida"
