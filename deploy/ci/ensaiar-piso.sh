#!/bin/sh
# Ensaio do piso de rollback (SP1, PR 2): o binário publicado do PR 1 tem de
# ler e operar as linhas que o schema novo das faixas permite (período e
# tolerância de até 365 dias, descrição de até 10000 caracteres). Uso, da raiz
# do repositório:
#
#   sh deploy/ci/ensaiar-piso.sh <tag-do-piso> <tag-do-branch>
#
# Sobe só a pilha local do compose.yaml, num projeto e em volumes próprios
# (nada de VPS nem de segredo real: as chaves são as de demonstração), e a
# derruba com os volumes no fim, inclusive quando um passo falha. Sai com
# código diferente de zero no primeiro passo que falhar. Não entra no CI: o
# orçamento de Actions não cobre um ensaio com duas imagens.
#
# Respeita STILLON_PORT (padrão 18089, para não colidir com um compose de pé
# na 8080) e passa UP_ARGS ao `docker compose up`.
set -eu

[ $# -eq 2 ] || { echo "uso: sh deploy/ci/ensaiar-piso.sh <tag-do-piso> <tag-do-branch>" >&2; exit 2; }
PISO=$1
BRANCH=$2

cd "$(dirname "$0")/../.."
for ferramenta in docker curl jq; do
    command -v "$ferramenta" >/dev/null || { echo "FALHOU: falta o $ferramenta" >&2; exit 1; }
done

# Projeto próprio: o `down -v` do fim só alcança os volumes deste ensaio.
COMPOSE_PROJECT_NAME=stillon-ensaio-piso
STILLON_PORT=${STILLON_PORT:-18089}
export COMPOSE_PROJECT_NAME STILLON_PORT
BASE=http://127.0.0.1:$STILLON_PORT
# O usuário de demonstração do seed (deploy/compose/seed.sh), o mesmo do
# checar-compose.sh. A senha é pública no compose.yaml.
EMAIL=demo@stillon.local
SENHA=demo-stillon-2026
# 365 dias, o novo teto de período e de tolerância (migration 00023).
UM_ANO=31536000

TMP=$(mktemp -d)
CORPO=$TMP/corpo.json
TOKEN=

limpar() {
    status=$?
    docker compose down -v --remove-orphans >/dev/null 2>&1 || true
    rm -rf "$TMP"
    exit "$status"
}
trap limpar EXIT
trap 'exit 130' INT TERM

falhar() {
    echo "FALHOU: $*" >&2
    docker compose logs app >&2 || true
    exit 1
}
passo() { echo "== $*"; }

sql() { docker compose exec -T db psql -U postgres -d stillon -Atq -c "$1"; }

# Guarda o access token em TOKEN, sem ecoá-lo.
entrar() {
    codigo=$(curl -s -o "$CORPO" -w '%{http_code}' -X POST "$BASE/api/v1/auth/login" \
        -H 'Content-Type: application/json' -H 'X-Requested-With: stillon' \
        -d "{\"email\":\"$EMAIL\",\"password\":\"$SENHA\"}")
    [ "$codigo" = 200 ] || falhar "login do usuário de demonstração deu $codigo"
    TOKEN=$(jq -r '.access_token // empty' "$CORPO")
    [ -n "$TOKEN" ] || falhar "login sem access_token na resposta"
}

# api MÉTODO CAMINHO [CORPO]: grava a resposta em $CORPO e imprime o status.
# O corpo vai pelo stdin, porque a descrição de 10000 caracteres não cabe
# com folga numa linha de comando.
api() {
    printf '%s' "${3:-}" | curl -s -o "$CORPO" -w '%{http_code}' -X "$1" "$BASE$2" \
        -H "Authorization: Bearer $TOKEN" -H 'X-Requested-With: stillon' \
        -H 'Content-Type: application/json' --data-binary @-
}

trocar_app() {
    # --no-deps: o rollback troca só o binário do app; o seed e o banco ficam.
    # shellcheck disable=SC2086 # UP_ARGS são várias flags, de propósito
    STILLON_TAG=$1 docker compose up -d --wait --no-deps ${UP_ARGS:-} app >&2 ||
        falhar "o app com a tag $1 não ficou saudável"
}

# 1. O branch sobe a pilha (aplica a migration nova) e grava linhas que só o
#    schema novo aceita.
passo "1. branch $BRANCH: pilha, tarefa de um ano e cópia"
docker compose down -v --remove-orphans >/dev/null 2>&1 || true
# shellcheck disable=SC2086 # UP_ARGS são várias flags, de propósito
STILLON_TAG=$BRANCH docker compose up -d --wait ${UP_ARGS:-} >&2 ||
    falhar "docker compose up --wait com a tag $BRANCH"
entrar
descricao=$(printf '%10000s' '' | tr ' ' 'a')
corpo=$(jq -n --arg d "$descricao" --argjson s "$UM_ANO" \
    '{name: "ensaio do piso", description: $d, schedule: {kind: "period", period_sec: $s}, grace_sec: $s}')
codigo=$(api POST /api/v1/heartbeats "$corpo")
[ "$codigo" = 201 ] || falhar "criar a tarefa de um ano deu $codigo"
ID=$(jq -r '.id' "$CORPO")
PING_URL=$(jq -r '.ping_url' "$CORPO")
case "$PING_URL" in
    http*/ping/*) ;;
    *) falhar "a criação não devolveu uma ping_url" ;;
esac
codigo=$(api POST "/api/v1/heartbeats/$ID/clone")
[ "$codigo" = 201 ] || falhar "duplicar a tarefa deu $codigo"
COPIA=$(jq -r '.id' "$CORPO")
codigo=$(api POST "/api/v1/heartbeats/$COPIA/events/clear")
[ "$codigo" = 204 ] || falhar "limpar os eventos da cópia deu $codigo"

# 2. O rollback: só o app volta ao piso, sobre o mesmo banco.
passo "2. piso $PISO: troca só o app"
trocar_app "$PISO"
entrar

# 3. O piso lê e opera as linhas novas, e recusa gravar acima das suas faixas
#    sem rebaixar o que já está no banco.
passo "3. piso $PISO: lista, ping e PATCH"
codigo=$(api GET /api/v1/heartbeats)
[ "$codigo" = 200 ] || falhar "GET /api/v1/heartbeats no piso deu $codigo"
anuais=$(jq --argjson s "$UM_ANO" --arg a "$ID" --arg b "$COPIA" \
    '[.items[] | select((.id == $a or .id == $b) and .schedule.period_sec == $s)] | length' "$CORPO")
[ "$anuais" = 2 ] || falhar "a lista do piso trouxe $anuais das 2 tarefas com period_sec $UM_ANO"

codigo=$(curl -s -o "$CORPO" -w '%{http_code}' -X POST "$PING_URL")
[ "$codigo" = 200 ] || falhar "POST na ping_url no piso deu $codigo"

codigo=$(api PATCH "/api/v1/heartbeats/$ID" '{"name":"ensaio do piso, renomeada"}')
[ "$codigo" = 200 ] || falhar "PATCH de name no piso deu $codigo"

codigo=$(api PATCH "/api/v1/heartbeats/$ID" \
    "{\"schedule\":{\"kind\":\"period\",\"period_sec\":$UM_ANO}}")
[ "$codigo" = 400 ] || falhar "PATCH reenviando period_sec $UM_ANO no piso deu $codigo, queria 400"
grep -q '"out_of_range"' "$CORPO" || falhar "o 400 do PATCH no piso não traz out_of_range"
periodo=$(sql "SELECT period_sec FROM heartbeats WHERE id = '$ID'")
[ "$periodo" = "$UM_ANO" ] || falhar "o period_sec no banco virou '$periodo' depois do PATCH recusado"

# 4. De volta ao branch, que lê o que o piso deixou. A pilha cai no trap.
passo "4. branch $BRANCH: volta e relê"
trocar_app "$BRANCH"
entrar
codigo=$(api GET "/api/v1/heartbeats/$ID")
[ "$codigo" = 200 ] || falhar "GET da tarefa de volta ao branch deu $codigo"
[ "$(jq -r '.schedule.period_sec' "$CORPO")" = "$UM_ANO" ] ||
    falhar "de volta ao branch, a tarefa perdeu o period_sec $UM_ANO"

echo "piso $PISO ensaiado sobre o schema de $BRANCH"
