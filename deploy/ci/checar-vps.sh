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
    docker image ls --format '{{.Repository}}:{{.Tag}}' "$REPO_TESTE" | xargs -r docker rmi >/dev/null 2>&1 || true
    rm -rf "$DIR"
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
# shellcheck disable=SC2016 # o ${ é literal: é o que se procura
while read -r v; do
    case "$v" in VIGILUME_SHA | VIGILUME_CADDY_TESTE) continue ;; esac
    grep -q "^$v=$" "$RAIZ/deploy/vps/env.exemplo" || falhar "o env.exemplo não tem $v"
done < <(grep -o '\${[A-Z_]*' "$RAIZ/deploy/vps/compose.prod.yaml" | sed 's/^\${//' | sort -u)
ok "compose.prod.yaml: portas, restart, mem_limit, GOMEMLIMIT, IPs fixos, IPv6, variáveis"

# ---------------------------------------------------------------------------
# 2. A pilha de pé, com o Caddy em modo local.
# ---------------------------------------------------------------------------
docker tag "$IMAGEM_APP" "$REPO_TESTE:t1"
if ! VIGILUME_SHA=t1 dc up -d --wait --wait-timeout 120 >&2; then
    dc logs >&2
    echo "FALHOU: a pilha não subiu" >&2
    exit 1
fi

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
    if ! COMPOSE_FILE="$DIR/compose.prod.yaml:$DIR/compose.teste.yaml:$DIR/compose.cabecalho.yaml" VIGILUME_SHA=t1 \
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
VIGILUME_SHA=t1 dc up -d --wait --wait-timeout 120 app >&2 || falhar "o app não voltou à configuração de produção"
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

if [ "$falhas" -gt 0 ]; then
    dc logs >&2
    exit 1
fi
echo "vps conferida"
