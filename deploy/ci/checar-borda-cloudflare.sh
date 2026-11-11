#!/usr/bin/env bash
# O IP do cliente atrás da Cloudflare, com a cadeia real da produção em
# contêineres: um cliente fala PROXY v2 e TLS com a borda, como faz o Traefik
# do Coolify; a borda (deploy/ci/vps/borda-cloudflare, com a origem declarada
# no compose.yaml de lá) repassa ao deploy/vps/compose.prod.yaml, com o Caddy
# do StillOn e os trusted_proxies reais, o binário e o Postgres. O job "vps" do
# CI roda; localmente, da raiz do repositório:
#
#   bash deploy/ci/checar-borda-cloudflare.sh <imagem do app> [site da borda]
#
# Sem o segundo argumento, o site é o da fixture. Com ele, o teste roda contra
# outro stillon.tech.caddy: é assim que se vê a falha do site anterior.
#
# Os baldes do app ficam em memória: cada caso usa IPs que nenhum outro usa.
#
# As redes são as de produção (edge, 172.31.0.0/24, e 10.30.0.0/24): recusa
# rodar se a rede edge já existe, o que inclui um checar-vps.sh em andamento.
set -euo pipefail

IMAGEM_APP=${1:?uso: checar-borda-cloudflare.sh <imagem do app> [site da borda]}
RAIZ=$(cd "$(dirname "$0")/../.." && pwd)
FIXTURE=$RAIZ/deploy/ci/vps/borda-cloudflare
SITE=${2:-$FIXTURE/sites/stillon.tech.caddy}
[ -f "$SITE" ] || { echo "checar-borda-cloudflare.sh: sem o arquivo $SITE" >&2; exit 2; }
for f in docker openssl od base64 sed; do
    command -v "$f" >/dev/null || { echo "checar-borda-cloudflare.sh: falta '$f' no PATH" >&2; exit 2; }
done
if docker network inspect edge >/dev/null 2>&1; then
    echo "checar-borda-cloudflare.sh: a rede edge já existe; este teste precisa dela só para si" >&2
    exit 2
fi

EMAIL=dono@stillon.example
SENHA=senha-do-teste-da-borda-2026
# Teto de um cliente que tenta passar do balde de login (rajada de 5, um token
# a cada 6 s): quem chega aqui sem 429 não está sendo limitado.
TENTATIVAS_DE_LOGIN=12
# Tentativas até o 429 de quem já estava sem saldo: uma reposta e a recusa.
LOGINS_APOS_O_429=2
# Pings até o 429 (rajada de 300 no balde por IP) e o lote de conferência.
TETO_DE_PINGS=3000
LOTE_DE_PINGS=100

export COMPOSE_PROJECT_NAME=stillon-teste-cloudflare
export STILLON_SHA=cloudflare-$$
IMAGEM_TESTE=stillon-teste-vps:$STILLON_SHA
DIR=$(mktemp -d)

dc() { COMPOSE_FILE="$DIR/compose.prod.yaml:$DIR/compose.teste.yaml" docker compose --project-directory "$DIR" "$@"; }
bd() { docker compose --project-directory "$DIR/borda" -f "$DIR/borda/compose.yaml" "$@"; }

limpar() {
    dc down -v --remove-orphans >/dev/null 2>&1 || true
    bd down -v --remove-orphans >/dev/null 2>&1 || true
    docker image rm "$IMAGEM_TESTE" >/dev/null 2>&1 || true
    rm -rf "$DIR"
}
trap limpar EXIT

falhas=0
falhar() { echo "FALHOU: $*" >&2; falhas=$((falhas + 1)); }
falhas_antes=0
ok() {
    [ "$falhas" = "$falhas_antes" ] && echo "ok: $*"
    falhas_antes=$falhas
}

cp "$RAIZ/deploy/vps/compose.prod.yaml" "$RAIZ/deploy/vps/Caddyfile" "$RAIZ/deploy/ci/vps/compose.teste.yaml" "$DIR/"
# 0755 pelo mesmo motivo do checar-vps.sh: o VirtioFS do Docker Desktop.
install -m 0755 "$RAIZ/deploy/vps/initdb.sh" "$DIR/"
mkdir "$DIR/borda"
cp -R "$FIXTURE/." "$DIR/borda/"
cp "$SITE" "$DIR/borda/sites/stillon.tech.caddy"

IP_HASH_KEY=$(openssl rand -base64 32)
cat > "$DIR/.env" <<EOF
STILLON_DOMINIO=stillon.tech
POSTGRES_PASSWORD=$(openssl rand -hex 32)
STILLON_APP_PASSWORD=$(openssl rand -hex 32)
STILLON_BACKUP_PASSWORD=$(openssl rand -hex 32)
MASTER_KEY=$(openssl rand -base64 32)
JWT_SECRET=$(openssl rand -base64 32)
IP_HASH_KEY=$IP_HASH_KEY
PROBE_BLOCKED_CIDRS=192.0.2.10/32,2001:db8::/64
EOF
chmod 600 "$DIR/.env"

docker tag "$IMAGEM_APP" "$IMAGEM_TESTE"
bd up -d --wait --wait-timeout 60 >&2 || { echo "FALHOU: a borda não subiu" >&2; exit 1; }
dc up -d --wait --wait-timeout 120 >&2 || { dc logs >&2; echo "FALHOU: a pilha não subiu" >&2; exit 1; }
printf '%s\n' "$SENHA" | dc exec -T app /stillon user create --email "$EMAIL" >&2 ||
    { echo "FALHOU: stillon user create" >&2; exit 1; }

cliente() { bd exec -T -e "TESTE_EMAIL=$EMAIL" -e "TESTE_SENHA=$SENHA" cliente python3 /cliente.py "$@"; }
sql() { dc exec -T db psql -U postgres -d stillon -Atq -c "$1"; }
# O mesmo cálculo de crypto.HashIP.
hmac() { printf %s "$1" | openssl dgst -sha256 -mac HMAC -macopt "hexkey:$(printf %s "$IP_HASH_KEY" | base64 -d | od -An -tx1 | tr -d ' \n')" | sed 's/^.*= //'; }
ultimo_hash() { sql "SELECT encode(ip_hash, 'hex') FROM sessions ORDER BY created_at DESC, id DESC LIMIT 1"; }

# visto <caso> <IP esperado> <argumentos do cliente...>: um login que tem de
# dar certo e gravar na sessão o ip_hash do IP esperado.
visto() {
    local caso=$1 esperado=$2 codigo
    shift 2
    codigo=$(cliente login "$@")
    if [ "$codigo" != 200 ]; then
        falhar "$caso: o login deu $codigo, queria 200"
        return 0
    fi
    [ "$(ultimo_hash)" = "$(hmac "$esperado")" ] || falhar "$caso: o ip_hash gravado não é o de $esperado"
}

# logins_ate_o_429 <argumentos do cliente...>: imprime quantos logins foram
# até o primeiro 429, ou nada se o teto chegou antes.
logins_ate_o_429() {
    local i
    for i in $(seq 1 "$TENTATIVAS_DE_LOGIN"); do
        if [ "$(cliente login "$@")" = 429 ]; then
            echo "$i"
            return 0
        fi
    done
}

# A cadeia inteira responde (a borda emite o certificado depois de subir).
pronto=
for _ in $(seq 1 60); do
    if [ "$(cliente saude --origem 162.158.10.1 2>/dev/null)" = 200 ]; then
        pronto=1
        break
    fi
    sleep 1
done
[ -n "$pronto" ] || { dc logs >&2; bd logs >&2; echo "FALHOU: /healthz não respondeu pela borda" >&2; exit 1; }

# Duas bordas da Cloudflare, em blocos diferentes da lista, e clientes em faixa
# de documentação.
BORDA_CF=162.158.10.2
OUTRA_BORDA_CF=172.69.39.2

# 1. Login: atrás da mesma borda da Cloudflare, cada cliente tem o seu balde.
A=198.51.100.11
B=198.51.100.12
visto "login do cliente A" "$A" --origem "$BORDA_CF" --cf "$A"
n=$(logins_ate_o_429 --origem "$BORDA_CF" --cf "$A")
[ -n "$n" ] || falhar "login: o cliente A fez $TENTATIVAS_DE_LOGIN tentativas sem 429"
# Logo depois do 429, e ainda assim com folga: o balde repõe um token a cada
# 6 s, e um pode cair entre as duas tentativas. Um balde novo, o da borda, daria
# a rajada inteira de 5 antes do 429.
n=$(logins_ate_o_429 --origem "$OUTRA_BORDA_CF" --cf "$A")
if [ -z "$n" ] || [ "$n" -gt "$LOGINS_APOS_O_429" ]; then
	falhar "login: o cliente A por outra borda levou '$n' tentativas até o 429 (o balde é do cliente, não da borda)"
fi
visto "login do cliente B, com o A sem saldo na mesma borda" "$B" --origem "$BORDA_CF" --cf "$B"
ok "login: cada cliente tem o seu balde, atrás da mesma borda e entre bordas"

# 2. Ping: o mesmo no balde por IP. A conferência é um lote, e não um envio: o
#    balde repõe 20 tokens por segundo, e um único ping passaria de qualquer
#    jeito logo depois do 429. Pelo mesmo motivo, cada conferência começa com o
#    balde recém-esgotado: um lote depois do outro herdaria os tokens repostos
#    no tempo do primeiro, e o resultado dependeria da velocidade do runner.
C=198.51.100.21
D=198.51.100.22
BORDA_PING=162.158.10.3
OUTRA_BORDA_PING=172.69.39.3
esgotar_pings_de_c() {
    local enviados recusados
    read -r enviados recusados <<<"$(cliente ping --origem "$BORDA_PING" --cf "$C" --quantos "$TETO_DE_PINGS" --parar-no-429)"
    [ "$recusados" = 1 ] || falhar "ping: o cliente C mandou $enviados sem nenhum 429"
}
esgotar_pings_de_c
read -r _ recusados <<<"$(cliente ping --origem "$OUTRA_BORDA_PING" --cf "$C" --quantos "$LOTE_DE_PINGS")"
[ "$recusados" -gt 0 ] || falhar "ping: o cliente C por outra borda não teve recusa (o balde é do cliente, não da borda)"
esgotar_pings_de_c
read -r _ recusados <<<"$(cliente ping --origem "$BORDA_PING" --cf "$D" --quantos "$LOTE_DE_PINGS")"
[ "$recusados" = 0 ] || falhar "ping: o cliente D teve $recusados de $LOTE_DE_PINGS recusados, com o C sem saldo na mesma borda"
ok "ping: cada cliente tem o seu balde por IP"

# 3. De fora das faixas, o CF-Connecting-IP forjado não vale: conta a origem.
FORJADO=198.51.100.77
FORA=203.0.113.50
visto "forjado de fora das faixas" "$FORA" --origem "$FORA" --cf "$FORJADO" --xff "$FORJADO"
ROTACAO=203.0.113.51
for i in $(seq 1 "$TENTATIVAS_DE_LOGIN"); do
    c=$(cliente login --origem "$ROTACAO" --cf "198.51.100.$((100 + i))")
    [ "$c" = 429 ] && break
done
[ "$c" = 429 ] || falhar "forjar um CF-Connecting-IP novo a cada login escapou do balde da origem"
# As fronteiras da lista, no IPv4 e no IPv6: logo fora e logo dentro.
visto "logo depois de 173.245.48.0/20" 173.245.64.1 --origem 173.245.64.1 --cf "$FORJADO"
visto "logo antes de 172.64.0.0/13" 172.63.255.254 --origem 172.63.255.254 --cf "$FORJADO"
visto "logo depois de 2606:4700::/32" 2606:4701::1 --origem 2606:4701::1 --cf "$FORJADO"
visto "na ponta de 173.245.48.0/20" 198.51.100.31 --origem 173.245.63.254 --cf 198.51.100.31
visto "na ponta de 172.64.0.0/13" 198.51.100.32 --origem 172.64.0.1 --cf 198.51.100.32
visto "em 2606:4700::/32" 198.51.100.33 --origem 2606:4700::10 --cf 198.51.100.33
ok "de fora das faixas não se escolhe o IP, e a lista vale nas fronteiras"

# 3b. A Cloudflare manda IPv6 quando o cliente é IPv6; o app grava a forma
#     normalizada: minúsculas, e :: só para dois grupos zerados ou mais. Os dois
#     últimos casos casam, cada um, uma única alternativa da regex: oito grupos
#     e o :: entre três e quatro grupos.
visto "cliente IPv6" 2001:db8:1::77 --origem "$OUTRA_BORDA_CF" --cf 2001:DB8:1::77
visto "cliente IPv6 de oito grupos" 2001:db8:2:3:4:5:6:7 --origem "$OUTRA_BORDA_CF" --cf 2001:db8:2:3:4:5:6:7
visto "cliente IPv6 com :: no meio" 2001:db8:3:0:4:5:6:7 --origem "$OUTRA_BORDA_CF" --cf 2001:db8:3::4:5:6:7
ok "cliente IPv6 chega ao app"

# 4. Valor inválido ou duplicado é ignorado: conta a borda da Cloudflare, que é
#    quem conectou. Uma borda por caso, para o balde dela não acabar.
ultimo_octeto=100
invalido() {
    local caso=$1 borda
    shift
    ultimo_octeto=$((ultimo_octeto + 1))
    borda=162.158.20.$ultimo_octeto
    visto "CF-Connecting-IP $caso" "$borda" --origem "$borda" "$@"
}
invalido "ausente"
invalido "vazio" --cf ""
invalido "que não é IP" --cf nao-e-ip
invalido "com octeto acima de 255" --cf 999.1.1.1
invalido "com três octetos" --cf 1.2.3
invalido "com porta" --cf 1.2.3.4:5678
invalido "IPv4 mapeado em IPv6" --cf ::ffff:1.2.3.4
invalido "IPv6 com zona" --cf 'fe80::1%eth0'
invalido "lista com vírgula" --cf 1.2.3.4,5.6.7.8
invalido "lista com vírgula e espaço" --cf '1.2.3.4, 5.6.7.8'
invalido "em duas linhas iguais" --cf 1.2.3.4 --cf 1.2.3.4
invalido "em duas linhas diferentes" --cf 1.2.3.4 --cf 5.6.7.8
invalido "inválido com X-Forwarded-For forjado" --cf nao-e-ip --xff 1.2.3.4
ok "CF-Connecting-IP inválido ou duplicado é ignorado"

if [ "$falhas" != 0 ]; then
    echo "checar-borda-cloudflare.sh: $falhas falha(s)" >&2
    exit 1
fi
echo "checar-borda-cloudflare.sh: tudo certo"
