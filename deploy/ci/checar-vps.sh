#!/usr/bin/env bash
# Checagens da produção na VPS (Plano 11, spec §8), sem VPS: sobe o
# deploy/vps/compose.prod.yaml com o Caddy em modo local (certificado da CA
# interna, domínio vigilume.teste) e confere o que a revisão exigiu da
# hospedagem. O job "vps" do CI roda; localmente, da raiz do repositório:
#
#   bash deploy/ci/checar-vps.sh <imagem do app>     (ex.: vigilume:dev)
#
# Precisa de docker (com o image store containerd, como a VPS), jq, curl,
# openssl, python3 (monta os tars maliciosos) e do age/age-keygen no PATH.
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
# As ferramentas, antes de subir qualquer coisa: sem uma delas, o teste
# morreria no meio (127) com a pilha de pé.
for f in docker jq curl openssl python3 age age-keygen awk sed tar sha1sum base64 od flock; do
    command -v "$f" >/dev/null || { echo "checar-vps.sh: falta '$f' no PATH (veja o cabeçalho)" >&2; exit 2; }
done
docker compose version >/dev/null 2>&1 || { echo "checar-vps.sh: falta o plugin docker compose" >&2; exit 2; }
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
# "ok" só quando o bloco que ele fecha não falhou.
falhas_antes=0
ok() {
    [ "$falhas" = "$falhas_antes" ] && echo "ok: $*"
    falhas_antes=$falhas
}

# O diretório que faz o papel de /opt/vigilume.
DIR=$(mktemp -d)
limpar() {
    if [ "${TESTE_VPS_MANTER:-}" = 1 ]; then
        echo "de pé em $DIR (projeto $PROJETO)" >&2
        return
    fi
    # Um postgres descartável esquecido pela entrada seguraria a rede.
    docker ps -aq --filter name=vigilume-restauro- | xargs -r docker rm -f >/dev/null 2>&1 || true
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
trocar 'SHM_BASE=/dev/shm' "SHM_BASE=$TARS"
# O age do teste pode não estar num diretório do PATH fixo da entrada (na
# VPS ele vem do apt, em /usr/bin).
trocar 'export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin' \
    "export PATH=$(dirname "$(command -v age)"):/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
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
# no_ar <sha>: a versão gravada e a imagem do app são as desse SHA.
no_ar() { [ "$(versao)" = "$1" ] && [ "$(imagem_do_app)" = "$REPO_TESTE:$1" ]; }

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
if ! { [ "$gomem_b" -gt 0 ] && [ "$gomem_b" -lt "$limite" ]; }; then
    falhar "GOMEMLIMIT '$gomem' não está abaixo do mem_limit do app ($limite bytes)"
fi
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
# Nenhuma chave ou senha escrita no compose (obrigação 5): na config resolvida
# com o .env do teste, cada segredo é o valor gerado acima. Um literal
# qualquer (POSTGRES_PASSWORD: postgres, uma MASTER_KEY fixa) fica diferente.
env_do_teste() { sed -n "s/^$1=//p" "$DIR/.env"; }
for par in app:MASTER_KEY app:JWT_SECRET app:IP_HASH_KEY db:POSTGRES_PASSWORD db:VIGILUME_APP_PASSWORD db:VIGILUME_BACKUP_PASSWORD; do
    s=${par%%:*} v=${par#*:}
    [ "$(q ".services.$s.environment.$v // empty")" = "$(env_do_teste "$v")" ] ||
        falhar "o $v do $s não é o do .env (valor escrito no compose.prod.yaml?)"
done
[ "$(q '.services.app.environment.DATABASE_URL')" = "postgres://vigilume_app:$(env_do_teste VIGILUME_APP_PASSWORD)@172.30.0.4:5432/vigilume?sslmode=disable" ] ||
    falhar "a DATABASE_URL do app não usa a senha do .env: $(q '.services.app.environment.DATABASE_URL' | sed 's/:[^:@]*@/:***@/')"
# Sem trusted_proxies no Caddyfile (obrigação 1), fora dos comentários.
[ "$(sed 's/#.*//' "$RAIZ/deploy/vps/Caddyfile" | grep -c trusted_proxies || true)" = 0 ] ||
    falhar "o Caddyfile tem trusted_proxies: um cliente escolheria o próprio IP"
# O env.exemplo é o modelo do .env: sem valor nenhum, e com toda variável que
# o compose.prod.yaml lê (menos VIGILUME_SHA, da entrada, e o modo de teste).
grep -Ev '^(#|$)' "$RAIZ/deploy/vps/env.exemplo" | grep -v '=$' && falhar "o env.exemplo tem valor preenchido"
while read -r v; do
    case "$v" in VIGILUME_SHA | VIGILUME_CADDY_TESTE) continue ;; esac
    grep -q "^$v=$" "$RAIZ/deploy/vps/env.exemplo" || falhar "o env.exemplo não tem $v"
done < <(grep -o '\${[A-Z_]*' "$RAIZ/deploy/vps/compose.prod.yaml" | sed 's/^\${//' | sort -u)
# E o contrário: o env.exemplo só tem o que o compose lê (a HC_URL do vigia
# fica no vigia.env, longe dos segredos).
while read -r v; do
    grep -q "\${${v}[:}-]" "$RAIZ/deploy/vps/compose.prod.yaml" || falhar "o env.exemplo tem $v, que o compose.prod.yaml não lê"
done < <(grep -Eo '^[A-Z_]+=' "$RAIZ/deploy/vps/env.exemplo" | tr -d =)
ok "compose.prod.yaml: portas, restart, mem_limit, GOMEMLIMIT, IPs fixos, IPv6, variáveis, segredos do .env; Caddyfile sem trusted_proxies"

# A entrada e o compose falam da mesma imagem.
if ! { grep -qx 'IMAGEM=vigilume' "$RAIZ/deploy/vps/vigilume-entrada" &&
    [ "$(q '.services.app.image')" = "vigilume:atual" ]; }; then
    falhar "o IMAGEM do vigilume-entrada e o image do app no compose.prod.yaml não casam"
fi

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
# O cliente do host chega à rede pelo gateway fixo: é esse o IP que o app vê.
gateway=$(q '.networks.vigilume.ipam.config[0].gateway')
[ "$real" = "$(hmac "$gateway")" ] || falhar "o app não viu o IP do cliente ($gateway, o gateway da rede)"
[ "$(entrar -H 'X-Real-IP: 1.2.3.4' -H 'Fly-Client-IP: 1.2.3.5' -H 'CF-Connecting-IP: 1.2.3.6' -H 'X-Forwarded-For: 1.2.3.7')" = 200 ] ||
    falhar "login com cabeçalhos forjados não deu 200"
forjado=$(ultimo_hash)
[ "$forjado" = "$real" ] || falhar "cabeçalhos do cliente mudaram o IP visto pelo app"
for ip in 1.2.3.4 1.2.3.5 1.2.3.6 1.2.3.7; do
    [ "$forjado" = "$(hmac "$ip")" ] && falhar "o app acreditou no IP forjado $ip"
done
# O aviso da B3 (X-Forwarded-For de fora de TRUSTED_PROXIES) não aparece.
# Em arquivo, e não num pipe: sob pipefail, o grep -q sairia no acerto e o
# SIGPIPE do `logs` deixaria a pipeline falsa (a asserção de ausência nunca
# falharia).
dc logs app > "$TARS/app.log" 2>&1 || falhar "docker compose logs app falhou"
grep -q 'não vem de TRUSTED_PROXIES' "$TARS/app.log" && falhar "o app avisou que o X-Forwarded-For não vem de TRUSTED_PROXIES"
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
recusa "   " "só espaços"
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
no_ar "$SHA_A" || falhar "um pedido recusado mexeu na versão"
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
# Os desvios da revisão (I1): tars que o docker load (containerd) lê com
# nome e que uma checagem por texto deixaria passar. O nome pedido é o do
# app no ar ($REPO_TESTE:atual): se o load acontecesse, a etiqueta atual
# passaria a apontar para b, sem healthcheck e sem historico. Cada um sai 3,
# e nenhuma etiqueta muda. O pedido traz o ID certo de b: só o tar é o
# problema.
etiquetas() {
    docker image ls --no-trunc --format '{{.Repository}}:{{.Tag}} {{.ID}}' |
        grep -E "^($REPO_TESTE|$REPO_TESTE-fonte):" | sort || true
}
rm -rf "$TARS/bx" && mkdir "$TARS/bx"
tar -xf "$TARS/b.tar" -C "$TARS/bx"
NOME_ATUAL=docker.io/library/$REPO_TESTE:atual
jq -c --arg n "$NOME_ATUAL" '.manifests[0].annotations = {"io.containerd.image.name": $n}' "$TARS/bx/index.json" > "$TARS/index-nomeado.json"
# O mesmo nome com escape JSON na chave ("\u002e" é o "."), que o
# encoding/json do containerd decodifica igual.
jq -c --arg n "$NOME_ATUAL" '.manifests[0].annotations = {"CHAVE": $n}' "$TARS/bx/index.json" |
    sed 's/"CHAVE"/"io\\u002econtainerd\\u002eimage\\u002ename"/' > "$TARS/index-escapado.json"
grep -q 'u002e' "$TARS/index-escapado.json" || falhar "controle: o index.json escapado não tem o escape"
# O nome no começo e depois 1 MB de espaço em branco (JSON válido).
{
    jq -c --arg n "$NOME_ATUAL" '{manifests: [{annotations: {"io.containerd.image.name": $n}} + .manifests[0]], schemaVersion, mediaType}' "$TARS/bx/index.json"
    head -c 1048576 /dev/zero | tr '\0' '\n'
} > "$TARS/index-grande.json"
# O manifest.json do formato v1.2 (sem oci-layout), com o nome no RepoTags
# e espaço depois dos dois-pontos.
man=$(jq -r '.manifests[0].digest | sub(":"; "/")' "$TARS/bx/index.json")
jq -c --arg n "$REPO_TESTE:atual" '[{Config: ("blobs/" + (.config.digest | sub(":"; "/"))), RepoTags: [$n],
    Layers: [.layers[].digest | "blobs/" + sub(":"; "/")]}]' "$TARS/bx/blobs/$man" |
    sed 's/"RepoTags":\[/"RepoTags": [/' > "$TARS/manifest-v12.json"
grep -q '"RepoTags": \[' "$TARS/manifest-v12.json" || falhar "controle: o manifest.json não tem o espaço"
# montar <saida> <nome no tar>=<arquivo> ...: um tar com esses membros, na
# ordem, com os nomes exatamente como dados (o GNU tar os normalizaria). Em
# vez de um arquivo: @symlink:<alvo>, @hard:<alvo>, @fifo ou @dir.
montar() {
    python3 - "$@" <<'PY'
import sys, tarfile
tipos = {"@fifo": tarfile.FIFOTYPE, "@dir": tarfile.DIRTYPE}
with tarfile.open(sys.argv[1], "w", format=tarfile.PAX_FORMAT) as t:
    for par in sys.argv[2:]:
        nome, arq = par.split("=", 1)
        info = tarfile.TarInfo(nome)
        info.mode = 0o644
        if arq.startswith("@symlink:") or arq.startswith("@hard:"):
            tipo, alvo = arq[1:].split(":", 1)
            info.type = tarfile.SYMTYPE if tipo == "symlink" else tarfile.LNKTYPE
            info.linkname = alvo
            t.addfile(info)
        elif arq in tipos:
            info.type = tipos[arq]
            info.mode = 0o755 if arq == "@dir" else 0o644
            t.addfile(info)
        else:
            with open(arq, "rb") as f:
                dados = f.read()
            info.size = len(dados)
            with open(arq, "rb") as f:
                t.addfile(info, f)
PY
}
blobs=()
for f in "$TARS"/bx/blobs/sha256/*; do blobs+=("blobs/sha256/${f##*/}=$f"); done
montar "$TARS/m-ponto-barra.tar" "oci-layout=$TARS/bx/oci-layout" ".//index.json=$TARS/index-nomeado.json" "${blobs[@]}"
montar "$TARS/m-ponto-ponto.tar" "oci-layout=$TARS/bx/oci-layout" "x/../index.json=$TARS/index-nomeado.json" "${blobs[@]}"
montar "$TARS/m-duplicado.tar" "oci-layout=$TARS/bx/oci-layout" "index.json=$TARS/bx/index.json" "${blobs[@]}" "./index.json=$TARS/index-nomeado.json"
montar "$TARS/m-escape.tar" "oci-layout=$TARS/bx/oci-layout" "index.json=$TARS/index-escapado.json" "${blobs[@]}"
montar "$TARS/m-repotags.tar" "manifest.json=$TARS/manifest-v12.json" "${blobs[@]}"
montar "$TARS/m-grande.tar" "oci-layout=$TARS/bx/oci-layout" "index.json=$TARS/index-grande.json" "${blobs[@]}"
# Dois valores JSON no index.json, o nomeado e depois o limpo: o jq sem -s
# julgaria só o último, e o containerd lê o primeiro.
cat "$TARS/index-nomeado.json" "$TARS/bx/index.json" > "$TARS/index-dois.json"
montar "$TARS/m-dois-valores.tar" "oci-layout=$TARS/bx/oci-layout" "index.json=$TARS/index-dois.json" "${blobs[@]}"
# Os tipos e caminhos que o GNU tar trataria de outro jeito: o index.json
# nomeado escondido num "blob" e alcançado por link; nomes absoluto e com
# ..; um FIFO; o index.json como diretório; e um layout sem blobs.
FALSO=blobs/sha256/$(printf 'f%.0s' {1..64})
montar "$TARS/m-symlink.tar" "oci-layout=$TARS/bx/oci-layout" "$FALSO=$TARS/index-nomeado.json" "index.json=@symlink:$FALSO" "${blobs[@]}"
montar "$TARS/m-hardlink.tar" "oci-layout=$TARS/bx/oci-layout" "$FALSO=$TARS/index-nomeado.json" "index.json=@hard:$FALSO" "${blobs[@]}"
montar "$TARS/m-absoluto.tar" "oci-layout=$TARS/bx/oci-layout" "/index.json=$TARS/index-nomeado.json" "${blobs[@]}"
montar "$TARS/m-sobe.tar" "oci-layout=$TARS/bx/oci-layout" "../index.json=$TARS/index-nomeado.json" "${blobs[@]}"
montar "$TARS/m-fifo.tar" "oci-layout=$TARS/bx/oci-layout" "index.json=$TARS/bx/index.json" "$FALSO=@fifo" "${blobs[@]}"
montar "$TARS/m-index-dir.tar" "oci-layout=$TARS/bx/oci-layout" "index.json=@dir" "${blobs[@]}"
montar "$TARS/m-sem-blobs.tar" "oci-layout=$TARS/bx/oci-layout" "index.json=$TARS/bx/index.json"
# blobs ou blobs/sha256 como ARQUIVO, com os blobs depois: o tar -x falharia
# (e não seria culpa da máquina).
montar "$TARS/m-blobs-arquivo.tar" "oci-layout=$TARS/bx/oci-layout" "index.json=$TARS/bx/index.json" "blobs=$TARS/bx/oci-layout" "${blobs[@]}"
montar "$TARS/m-sha256-arquivo.tar" "oci-layout=$TARS/bx/oci-layout" "index.json=$TARS/bx/index.json" "blobs/sha256=$TARS/bx/oci-layout" "${blobs[@]}"
antes=$(etiquetas)
for m in ponto-barra ponto-ponto duplicado escape repotags grande dois-valores symlink hardlink absoluto sobe fifo index-dir sem-blobs blobs-arquivo sha256-arquivo; do
    c=$(entrada "deploy $SHA_B $ID_B" < "$TARS/m-$m.tar")
    [ "$c" = 3 ] || falhar "tar malicioso '$m' saiu $c, queria 3: $(tail -1 "$TARS/erro")"
    [ "$(etiquetas)" = "$antes" ] || falhar "tar malicioso '$m' mexeu nas etiquetas: $(diff <(echo "$antes") <(etiquetas) | tr '\n' ' ')"
    if [ "$(docker image inspect -f '{{.Id}}' "$REPO_TESTE:atual")" != "$ID_A" ]; then
        falhar "tar malicioso '$m' reetiquetou $REPO_TESTE:atual"
        # De volta, para o caso seguinte ser julgado sozinho.
        docker tag "$ID_A" "$REPO_TESTE:atual"
    fi
    antes=$(etiquetas)
done
no_ar "$SHA_A" || falhar "uma imagem divergente mexeu na versão"
# O SHA de uma versão que já existe com OUTRA imagem (aqui, o SHA no ar com a
# imagem doente): etiquetar moveria $REPO_TESTE:<sha> para a doente, e o
# rollback, que usa o mesmo SHA, subiria a doente. Recusado com 3 antes.
c=$(entrada "deploy $SHA_A $ID_DOENTE" < "$TARS/doente.tar")
[ "$c" = 3 ] || falhar "SHA existente com outra imagem saiu $c, queria 3: $(tail -1 "$TARS/erro")"
grep -q 'já existe com outra imagem' "$TARS/erro" || falhar "SHA existente com outra imagem: não disse por quê"
[ "$(docker image inspect -f '{{.Id}}' "$REPO_TESTE:$SHA_A")" = "$ID_A" ] || falhar "a etiqueta $REPO_TESTE:$SHA_A foi movida"
no_ar "$SHA_A" || falhar "o SHA existente com outra imagem mexeu na versão"
[ "$(docker inspect -f '{{.State.Health.Status}}' "$(dc ps -q app)")" = healthy ] || falhar "depois do SHA existente com outra imagem o app não está saudável"
ok "vigilume-entrada: imagem divergente, tar com nome, os desvios de layout e o SHA já etiquetado recusados com 3"

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

# 8c'. Rollback por tempo esgotado: um app que fica de pé e nunca saudável
#      (o processo não cai; o healthcheck sempre falha). A imagem doente de
#      cima morre no boot e o --wait volta em segundos; esta prova a espera
#      de ESPERA_S inteira. A base é a imagem do db (já na máquina, com sh).
printf '%s\n' "FROM $pg" \
    "RUN printf '#!/bin/sh\\nexit 1\\n' > /vigilume && chmod 755 /vigilume" \
    'ENTRYPOINT ["/bin/sh", "-c", "trap \"exit 0\" TERM; while :; do sleep 1; done", "--"]' |
    docker build -q -t "$REPO_TESTE-fonte:lento" - >/dev/null
ID_LENTO=$(oci_tar "$REPO_TESTE-fonte:lento" "$TARS/lento.tar")
SHA_LENTO=$(sha_de lento)
inicio=$(date +%s)
c=$(entrada "deploy $SHA_LENTO $ID_LENTO" < "$TARS/lento.tar")
duracao=$(($(date +%s) - inicio))
[ "$c" = 4 ] || falhar "deploy que nunca fica saudável saiu $c, queria 4: $(tail -3 "$TARS/erro")"
grep -q 'rollback feito' "$TARS/erro" || falhar "o deploy lento não disse que fez o rollback"
[ "$duracao" -ge 45 ] || falhar "o deploy lento voltou em $duracao s, antes da espera de 45 s: não foi o tempo esgotado"
[ "$(versao)" = "$SHA_A" ] || falhar "depois do rollback por tempo, versao = $(versao)"
[ "$(imagem_do_app)" = "$REPO_TESTE:$SHA_A" ] || falhar "depois do rollback por tempo o app roda $(imagem_do_app)"
[ "$(docker inspect -f '{{.State.Health.Status}}' "$(dc ps -q app)")" = healthy ] || falhar "depois do rollback por tempo o app não está saudável"
ok "vigilume-entrada: deploy que nunca fica saudável volta à anterior depois da espera (4)"

# 8c''. A conexão cai no meio do deploy (I1): o runner cancelado ou perdido
#       fecha o canal do SSH, e a stderr da entrada vira um pipe sem leitor. A
#       entrada termina o que começou (aqui, o rollback de um deploy doente) e
#       o log inteiro fica no entrada.log. A stderr vai para um head que lê a
#       primeira linha e sai. O entrada.log começa acima do teto: é rodado.
head -c $((1024 * 1024 + 1)) /dev/zero > "$DIR/entrada.log"
c=0
env -i PATH="$PATH" HOME="$HOME" SSH_ORIGINAL_COMMAND="deploy $SHA_DOENTE $ID_DOENTE" \
    bash "$ENTRADA" < "$TARS/doente.tar" > "$TARS/saida" 2> >(head -n 1 > "$TARS/primeira") || c=$?
[ "$c" = 4 ] || falhar "com a conexão caída no meio, o deploy doente saiu $c, queria 4 (rollback feito)"
no_ar "$SHA_A" || falhar "com a conexão caída no meio, o rollback não aconteceu: versao $(versao), app $(imagem_do_app)"
[ "$(docker inspect -f '{{.State.Health.Status}}' "$(dc ps -q app)")" = healthy ] ||
    falhar "com a conexão caída no meio, o app não ficou saudável"
grep -q 'rollback feito' "$DIR/entrada.log" || falhar "o entrada.log não registrou o rollback"
# A linha final (a que o dono lê depois de uma queda do SSH): o pedido e o código.
tail -n 1 "$DIR/entrada.log" | grep -Eq "^== [0-9TZ:-]+ fim: deploy $SHA_DOENTE $ID_DOENTE: código 4 \(" ||
    falhar "a última linha do entrada.log não é o fim do deploy com o código 4: $(tail -n 1 "$DIR/entrada.log")"
grep -q 'ARGON2_MAX_CONCURRENT' "$DIR/entrada.log" || falhar "o entrada.log não tem o log do app"
[ "$(stat -c %s "$DIR/entrada.log")" -lt 1048576 ] || falhar "o entrada.log acima do teto não foi rodado"
[ "$(stat -c %s "$DIR/entrada.log.1" 2>/dev/null || echo 0)" -gt 1048576 ] || falhar "o entrada.log rodado não ficou em entrada.log.1"
[ "$(stat -c %a "$DIR/entrada.log")" = 644 ] || falhar "o entrada.log com modo $(stat -c %a "$DIR/entrada.log"), queria 644"
rm -f "$DIR/entrada.log.1"
ok "vigilume-entrada: a conexão cai no meio e o rollback acontece; entrada.log com teto"

# 8d. Deploys seguidos: versao-anterior anda, e ficam as 3 últimas imagens
#     (as 3 últimas que ficaram no ar; a doente sai), fora a etiqueta atual.
ID_C=$(fonte c 'LABEL vigilume.teste=c')
SHA_C=$(sha_de c)
ID_D=$(fonte d 'LABEL vigilume.teste=d')
SHA_D=$(sha_de d)
# O tar de c sem o "./" na frente dos nomes (tar -C dir oci-layout …): as
# duas formas do layout OCI passam.
rm -rf "$TARS/cx" && mkdir "$TARS/cx" && tar -xf "$TARS/c.tar" -C "$TARS/cx"
tar -cf "$TARS/c.tar" -C "$TARS/cx" oci-layout index.json blobs
tar -tf "$TARS/c.tar" | grep -q '^\./' && falhar "controle: o tar de c ainda tem ./"
# Um historico longo (I2): 2000 versões antigas, como depois de anos de
# deploy. A poda não pode morrer de SIGPIPE, e o historico não cresce sem
# limite.
for i in $(seq 1 2000); do sha_de "antiga-$i"; done >> "$DIR/historico"
# O temporário de um deploy morto por SIGKILL (o trap não roda): o próximo
# deploy o apaga, já sob a trava.
mkdir -p "$DIR/.vigilume-entrada.morto"
head -c 4096 /dev/urandom > "$DIR/.vigilume-entrada.morto/imagem.tar"
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
[ -e "$DIR/.vigilume-entrada.morto" ] && falhar "o temporário de um deploy morto ficou para trás"
[ "$(wc -l < "$DIR/historico")" -le 50 ] || falhar "o historico ficou com $(wc -l < "$DIR/historico") linhas: cresce sem limite"
[ "$(tail -n 3 "$DIR/historico" | tr '\n' ' ')" = "$SHA_B $SHA_C $SHA_D " ] || falhar "o historico perdeu as últimas versões"
# O mesmo SHA de novo (redeploy) é aceito e não mexe na anterior. Com
# variáveis do docker e do compose no ambiente (que o sudo tiraria): a
# entrada não as usa.
c=0
env -i PATH="$PATH" HOME="$HOME" SSH_ORIGINAL_COMMAND="deploy $SHA_D $ID_D" LANG=C.UTF-8 \
    DOCKER_HOST=unix:///nada.sock DOCKER_CONTEXT=nada COMPOSE_PROJECT_NAME=outro COMPOSE_FILE=/nada.yaml \
    bash "$ENTRADA" < "$TARS/d.tar" > "$TARS/saida" 2> "$TARS/erro" || c=$?
[ "$c" = 0 ] || falhar "redeploy de d (com DOCKER_* e COMPOSE_* no ambiente) saiu $c: $(tail -3 "$TARS/erro")"
[ "$(cat "$DIR/versao-anterior")" = "$SHA_C" ] || falhar "o redeploy de d mudou a versao-anterior"
[ "$(imagem_do_app)" = "$REPO_TESTE:$SHA_D" ] || falhar "depois do redeploy o app roda $(imagem_do_app)"
# versao, versao-anterior e historico legíveis sem sudo (não são segredo),
# apesar do umask 077 da entrada.
for f in versao versao-anterior historico; do
    [ "$(stat -c %a "$DIR/$f")" = 644 ] || falhar "$f com modo $(stat -c %a "$DIR/$f"), queria 644"
done
ok "vigilume-entrada: versao-anterior, as 3 últimas imagens, historico longo, temporário velho, ambiente ignorado, modos 644"

# 8d'. A etiqueta de um SHA que já existe com outra imagem: recusada (3) só se
#      o SHA é um alvo do rollback (versao ou versao-anterior; o da versao é o
#      8b). Qualquer outro SHA (um publish refeito, uma tag v* refeita) troca a
#      etiqueta velha e segue.
c=$(entrada "deploy $SHA_C $ID_A" < "$TARS/a.tar")
[ "$c" = 3 ] || falhar "o SHA da versao-anterior com outra imagem saiu $c, queria 3: $(tail -1 "$TARS/erro")"
grep -q 'já existe com outra imagem' "$TARS/erro" || falhar "o SHA da versao-anterior com outra imagem: não disse por quê"
[ "$(docker image inspect -f '{{.Id}}' "$REPO_TESTE:$SHA_C")" = "$ID_C" ] || falhar "a etiqueta $REPO_TESTE:$SHA_C (versao-anterior) foi movida"
no_ar "$SHA_D" || falhar "o SHA da versao-anterior com outra imagem mexeu na versão"
c=$(entrada "deploy $SHA_B $ID_A" < "$TARS/a.tar")
[ "$c" = 0 ] || falhar "um SHA fora do rollback (b) com outra imagem saiu $c, queria 0: $(tail -3 "$TARS/erro")"
[ "$(docker image inspect -f '{{.Id}}' "$REPO_TESTE:$SHA_B")" = "$ID_A" ] || falhar "a etiqueta velha de b não foi trocada pela imagem nova"
no_ar "$SHA_B" || falhar "depois de trocar a etiqueta de b, versao $(versao), app $(imagem_do_app)"
# De volta a d (o 8e' parte dele).
c=$(entrada "deploy $SHA_D $ID_D" < "$TARS/d.tar")
if ! { [ "$c" = 0 ] && no_ar "$SHA_D"; }; then falhar "a volta para d saiu $c: $(tail -3 "$TARS/erro")"; fi
ok "vigilume-entrada: SHA etiquetado com outra imagem, 3 se é a versao-anterior, trocado se não é alvo do rollback"

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

# 8e'. Falhas da máquina, não do tar: 1, e nada etiquetado. (a) O tar não
#      extrai ou não refaz (disco cheio): um tar falso na frente do PATH que
#      falha no -x ou no -c. (b) Sem jq: um PATH com tudo de /usr/bin e /bin
#      menos ele.
mkdir -p "$TARS/bin-tar" "$TARS/semjq"
printf '#!/bin/sh\nfor a; do case "$a" in -%s*) echo "tar: sem espaço" >&2; exit 2 ;; esac; done\nexec %s "$@"\n' \
    "\${FALHAR_TAR:-nada}" "$(command -v tar)" > "$TARS/bin-tar/tar"
chmod +x "$TARS/bin-tar/tar"
for d in /usr/local/bin /usr/bin /bin /usr/sbin /sbin; do
    for f in "$d"/*; do
        [ "${f##*/}" = jq ] || [ -e "$TARS/semjq/${f##*/}" ] || ln -s "$f" "$TARS/semjq/${f##*/}"
    done
done
ln -sf "$(command -v docker)" "$TARS/semjq/docker"
antes=$(etiquetas)
for caso in "x FALHAR_TAR=x $TARS/bin-tar:$PATH" "c FALHAR_TAR=c $TARS/bin-tar:$PATH" "jq NADA=1 $TARS/semjq"; do
    read -r nome var caminho <<<"$caso"
    c=0
    env -i PATH="$caminho" HOME="$HOME" SSH_ORIGINAL_COMMAND="deploy $SHA_D $ID_D" "$var" \
        bash "$TARS/entrada-path" < "$TARS/d.tar" > /dev/null 2> "$TARS/erro" || c=$?
    [ "$c" = 1 ] || falhar "deploy com falha de máquina ($nome) saiu $c, queria 1: $(tail -1 "$TARS/erro")"
    [ "$(etiquetas)" = "$antes" ] || falhar "deploy com falha de máquina ($nome) mexeu nas etiquetas"
done
grep -q 'docs/deploy/vps.md' "$TARS/erro" || falhar "sem jq, a entrada não apontou o runbook: $(tail -1 "$TARS/erro")"
no_ar "$SHA_D" || falhar "uma falha de máquina mexeu na versão"
ok "vigilume-entrada: tar que não extrai ou não refaz, e jq ausente, saem 1 sem etiquetar"

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

# ---------------------------------------------------------------------------
# 9. vigilume-entrada backup: só texto cifrado na saída padrão.
# ---------------------------------------------------------------------------
# Os scripts do backup no layout que a entrada espera ($DIR/deploy), tais e
# quais os do repositório; a chave do age gerada aqui (a privada fica em
# $TARS, só para conferir).
mkdir -p "$DIR/deploy/backup" "$DIR/deploy/compose"
cp "$RAIZ/deploy/backup/backup.sh" "$RAIZ/deploy/backup/verificar.sql" "$DIR/deploy/backup/"
cp "$RAIZ/deploy/compose/initdb.sql" "$DIR/deploy/compose/"
age-keygen -o "$TARS/chave-age" 2>/dev/null
age-keygen -y "$TARS/chave-age" > "$DIR/age-recipient"

c=$(entrada "backup extra" < /dev/null)
[ "$c" = 2 ] || falhar "backup com argumento saiu $c, queria 2"

# O que um backup morto por SIGKILL deixaria: o dump em claro na memória e o
# postgres descartável. O próximo backup apaga os dois, já sob a trava.
mkdir -p "$TARS/vigilume-backup.morto"
echo 'PGDMP em claro' > "$TARS/vigilume-backup.morto/vigilume.dump"
docker run -d --name vigilume-restauro-morto --entrypoint sleep "$pg" 600 >/dev/null
c=$(entrada backup < /dev/null)
[ -e "$TARS/vigilume-backup.morto" ] && falhar "o dump em claro de um backup morto ficou para trás"
docker inspect vigilume-restauro-morto >/dev/null 2>&1 && falhar "o postgres descartável de um backup morto ficou para trás"
if [ "$c" != 0 ]; then
    cat "$TARS/erro" >&2
    falhar "backup saiu $c, queria 0"
fi
grep -q 'backup conferido' "$TARS/erro" || falhar "o log do backup não diz 'backup conferido'"
[ "$(head -c 21 "$TARS/saida")" = "age-encryption.org/v1" ] || falhar "a saída padrão do backup não começa com o cabeçalho do age"
grep -aq 'PGDMP' "$TARS/saida" && falhar "a saída padrão do backup tem o dump em claro (PGDMP)"
grep -aqE 'vigilume_app|goose_db_version|CREATE TABLE|backup conferido' "$TARS/saida" && falhar "a saída padrão do backup tem texto em claro"
# Decifrado com a privada, é um dump do pg_dump que o pg_restore lê.
if age -d -i "$TARS/chave-age" -o "$TARS/decifrado.dump" "$TARS/saida"; then
    [ "$(head -c 5 "$TARS/decifrado.dump")" = PGDMP ] || falhar "o backup decifrado não é um dump do pg_dump"
    chmod 644 "$TARS/decifrado.dump"
    docker run --rm -v "$TARS/decifrado.dump:/d.dump:ro" "$pg" pg_restore -l /d.dump > "$TARS/lista" 2>/dev/null || true
    grep -q 'TABLE public users' "$TARS/lista" || falhar "o dump decifrado não tem a tabela users"
    rm -f "$TARS/decifrado.dump"
else
    falhar "o backup não decifra com a chave privada"
fi
# Nada sobra: nem o postgres descartável, nem o dump em claro.
[ -z "$(docker ps -aq --filter name=vigilume-restauro-)" ] || falhar "o postgres descartável do backup ficou para trás"
ls "$TARS"/vigilume-backup.* >/dev/null 2>&1 && falhar "o diretório do dump em claro ficou para trás"
ok "vigilume-entrada backup: conferido, só o cifrado na saída padrão"

# A senha do backup no .env com aspas em volta e CRLF (o compose tira as
# aspas): o backup a lê igual. Com um caractere que o compose e a URL do
# backup leriam diferente: recusa com 1, antes do dump.
cp "$DIR/.env" "$TARS/env.original"
senha_bk=$(env_do_teste VIGILUME_BACKUP_PASSWORD)
for forma in "\"$senha_bk\"\r" "'$senha_bk'"; do
    grep -v '^VIGILUME_BACKUP_PASSWORD=' "$TARS/env.original" > "$DIR/.env"
    printf 'VIGILUME_BACKUP_PASSWORD=%b\n' "$forma" >> "$DIR/.env"
    c=$(entrada backup < /dev/null)
    [ "$c" = 0 ] || falhar "backup com a senha escrita como $(printf %q "$forma") no .env saiu $c, queria 0: $(tail -2 "$TARS/erro")"
    [ "$(head -c 21 "$TARS/saida")" = "age-encryption.org/v1" ] || falhar "backup com a senha entre aspas não mandou o cifrado"
done
grep -v '^VIGILUME_BACKUP_PASSWORD=' "$TARS/env.original" > "$DIR/.env"
printf 'VIGILUME_BACKUP_PASSWORD=%s$x\n' "$senha_bk" >> "$DIR/.env"
c=$(entrada backup < /dev/null)
[ "$c" = 1 ] || falhar "backup com \$ na senha saiu $c, queria 1"
grep -q 'VIGILUME_BACKUP_PASSWORD' "$TARS/erro" || falhar "backup com \$ na senha não disse por quê: $(tail -1 "$TARS/erro")"
grep -q 'postgres descartável' "$TARS/erro" && falhar "backup com \$ na senha fez o dump antes de recusar"
[ ! -s "$TARS/saida" ] || falhar "backup com \$ na senha escreveu na saída padrão"
cp "$TARS/env.original" "$DIR/.env"
ok "vigilume-entrada backup: senha com aspas e CRLF lida igual ao compose; caractere ambíguo recusado (1)"

# Sem a chave pública: 1, e nada na saída padrão.
mv "$DIR/age-recipient" "$DIR/age-recipient.fora"
c=$(entrada backup < /dev/null)
[ "$c" = 1 ] || falhar "backup sem age-recipient saiu $c, queria 1"
[ ! -s "$TARS/saida" ] || falhar "backup sem age-recipient escreveu na saída padrão"
grep -q 'postgres descartável' "$TARS/erro" && falhar "backup sem age-recipient fez o dump antes de recusar"
mv "$DIR/age-recipient.fora" "$DIR/age-recipient"
# A cópia que não passa nas checagens: 3, e nada na saída padrão. O
# verificar.sql do diretório da entrada é trocado por um que sempre falha
# (o do repositório não muda).
cp "$DIR/deploy/backup/verificar.sql" "$TARS/verificar.sql"
printf '\\set ON_ERROR_STOP on\nDO $$ BEGIN RAISE EXCEPTION %s; END $$;\n' "'checagem do teste'" > "$DIR/deploy/backup/verificar.sql"
c=$(entrada backup < /dev/null)
[ "$c" = 3 ] || falhar "backup com a checagem falhando saiu $c, queria 3"
[ ! -s "$TARS/saida" ] || falhar "backup com a checagem falhando escreveu na saída padrão"
cp "$TARS/verificar.sql" "$DIR/deploy/backup/verificar.sql"
[ -z "$(docker ps -aq --filter name=vigilume-restauro-)" ] || falhar "o postgres descartável ficou para trás depois da falha"
ok "vigilume-entrada backup: sem chave (1) e cópia reprovada (3) não mandam nada"

# O código 4: o backup vai, cifrado, e o código avisa do disco. Um df falso na
# frente do PATH: (a) o disco da VPS acima de 85% (o df da entrada); (b) o
# banco acima de 85% do volume (o backup.sh sai 4, pelo VOLUME_BYTES que a
# entrada mede).
mkdir -p "$TARS/bin-df"
printf '#!/bin/sh\ncase "$*" in\n*--output=pcent*) [ -n "${DF_PCT:-}" ] && { echo Use%%; echo " $DF_PCT%%"; exit 0; } ;;\n*--output=size*) [ -n "${DF_TAMANHO:-}" ] && { echo 1B-blocks; echo "$DF_TAMANHO"; exit 0; } ;;\nesac\nexec %s "$@"\n' \
    "$(command -v df)" > "$TARS/bin-df/df"
chmod +x "$TARS/bin-df/df"
sed 's|^export PATH=.*|export PATH="$PATH"|' "$ENTRADA" > "$TARS/entrada-df"
for caso in "DF_PCT=91 disco da VPS" "DF_TAMANHO=1000000 volume do banco"; do
    read -r var motivo <<<"$caso"
    c=0
    env -i PATH="$TARS/bin-df:$PATH" HOME="$HOME" SSH_ORIGINAL_COMMAND=backup "$var" \
        bash "$TARS/entrada-df" < /dev/null > "$TARS/saida" 2> "$TARS/erro" || c=$?
    [ "$c" = 4 ] || falhar "backup com o $motivo acima de 85% saiu $c, queria 4: $(tail -3 "$TARS/erro")"
    grep -q '85%' "$TARS/erro" || falhar "backup com o $motivo acima de 85% não avisou"
    if age -d -i "$TARS/chave-age" -o "$TARS/decifrado.dump" "$TARS/saida" 2>/dev/null; then
        [ "$(head -c 5 "$TARS/decifrado.dump")" = PGDMP ] || falhar "backup com o $motivo cheio: o cifrado não é um dump"
        rm -f "$TARS/decifrado.dump"
    else
        falhar "backup com o $motivo cheio: a saída não decifra (o cifrado não saiu?)"
    fi
done
ok "vigilume-entrada backup: disco ou volume acima de 85% manda o cifrado e sai 4"

if [ "$falhas" -gt 0 ]; then
    dc logs >&2
    exit 1
fi
echo "vps conferida"
