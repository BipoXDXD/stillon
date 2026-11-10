#!/bin/sh
# Ensaio do piso de rollback (SP1, PR 2): o binário publicado do PR 1 tem de
# ler e operar as linhas que o schema novo das faixas permite (período e
# tolerância de até 365 dias, descrição de até 10000 caracteres). Uso, da raiz
# do repositório:
#
#   STILLON_TAG=<tag-do-branch> docker compose build
#   sh deploy/ci/ensaiar-piso.sh <tag-do-piso> <tag-do-branch>
#
# O ensaio não constrói nada (`up --no-build`): sem isso, uma tag de piso
# errada ou privada faria o compose construir o código deste branch com a tag
# do piso, e o ensaio passaria testando o código errado. Por isso a imagem do
# branch (app e -tools) tem de ser construída antes, com o build acima, e a do
# piso tem de existir no ghcr.io ou no Docker local. Tag que é um SHA de 40
# caracteres só vale com o label org.opencontainers.image.revision igual a ele,
# como as imagens que o CI publica.
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
# O compose lê estas do ambiente e, na falta, do .env da raiz, onde o dono guarda
# a chave real do Resend. Vazias no ambiente, elas vencem o .env: o ensaio não
# envia e-mail nem sonda URLs de fora.
export RESEND_API_KEY='' EMAIL_FROM='' DEMO_TARGETS_EXTRA=''
BASE=http://127.0.0.1:$STILLON_PORT
# O usuário de demonstração do seed (deploy/compose/seed.sh), o mesmo do
# checar-compose.sh. A senha é pública no compose.yaml.
EMAIL=demo@stillon.local
SENHA=demo-stillon-2026
# 365 dias, o novo teto de período e de tolerância (migration heartbeat_limits).
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

# exigir_imagem TAG: a imagem existe localmente ou vem do registro; se a tag
# é um SHA, o label de revisão confere com ele. Sem isso, o --no-build do up
# falharia sem dizer qual imagem faltou.
exigir_imagem() {
    imagem=ghcr.io/bipoxdxd/stillon:$1
    docker image inspect "$imagem" >/dev/null 2>&1 ||
        docker pull "$imagem" >/dev/null ||
        falhar "imagem $1 inexistente (a do branch se constrói com STILLON_TAG=$1 docker compose build)"
    case "$1" in
        *[!0-9a-f]*) return 0 ;;
    esac
    [ "${#1}" -eq 40 ] || return 0
    revisao=$(docker image inspect -f '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$imagem") ||
        falhar "não deu para ler os labels da imagem $1"
    [ "$revisao" = "$1" ] || falhar "a imagem $1 traz a revisão '$revisao', não o próprio SHA"
}

sql() { docker compose exec -T db psql -U postgres -d stillon -Atq -c "$1"; }

# Guarda o access token em TOKEN, sem ecoá-lo.
entrar() {
    codigo=$(curl -s -o "$CORPO" -w '%{http_code}' -X POST "$BASE/api/v1/auth/login" \
        -H 'Content-Type: application/json' -H 'X-Requested-With: stillon' \
        -d "{\"email\":\"$EMAIL\",\"password\":\"$SENHA\"}") ||
        falhar "o login não respondeu em $BASE"
    [ "$codigo" = 200 ] || falhar "login do usuário de demonstração deu $codigo"
    TOKEN=$(jq -r '.access_token // empty' "$CORPO") || falhar "o login não devolveu JSON"
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
    exigir_imagem "$1"
    # --no-deps: o rollback troca só o binário do app; o seed e o banco ficam.
    # shellcheck disable=SC2086 # UP_ARGS são várias flags, de propósito
    STILLON_TAG=$1 docker compose up -d --wait --no-deps --no-build ${UP_ARGS:-} app >&2 ||
        falhar "o app com a tag $1 não ficou saudável"
}

# 1. O branch sobe a pilha (aplica a migration nova) e grava linhas que só o
#    schema novo aceita.
passo "1. branch $BRANCH: pilha, tarefa de um ano e cópia"
exigir_imagem "$BRANCH"
exigir_imagem "$BRANCH-tools"
docker compose down -v --remove-orphans >/dev/null 2>&1 || true
# shellcheck disable=SC2086 # UP_ARGS são várias flags, de propósito
STILLON_TAG=$BRANCH docker compose up -d --wait --no-build ${UP_ARGS:-} >&2 ||
    falhar "docker compose up --wait com a tag $BRANCH"
entrar
descricao=$(printf '%10000s' '' | tr ' ' 'a')
corpo=$(jq -n --arg d "$descricao" --argjson s "$UM_ANO" \
    '{name: "ensaio do piso", description: $d, schedule: {kind: "period", period_sec: $s}, grace_sec: $s}')
codigo=$(api POST /api/v1/heartbeats "$corpo") || falhar "a API não respondeu em $BASE"
[ "$codigo" = 201 ] || falhar "criar a tarefa de um ano deu $codigo"
ID=$(jq -r '.id' "$CORPO") || falhar "a criação não devolveu JSON"
PING_URL=$(jq -r '.ping_url' "$CORPO") || falhar "a criação não devolveu JSON"
case "$PING_URL" in
    http*/ping/*) ;;
    *) falhar "a criação não devolveu uma ping_url" ;;
esac
codigo=$(api POST "/api/v1/heartbeats/$ID/clone") || falhar "a API não respondeu em $BASE"
[ "$codigo" = 201 ] || falhar "duplicar a tarefa deu $codigo"
COPIA=$(jq -r '.id' "$CORPO") || falhar "a cópia não devolveu JSON"
codigo=$(api POST "/api/v1/heartbeats/$COPIA/events/clear") || falhar "a API não respondeu em $BASE"
[ "$codigo" = 204 ] || falhar "limpar os eventos da cópia deu $codigo"

# 2. O rollback: só o app volta ao piso, sobre o mesmo banco.
passo "2. piso $PISO: troca só o app"
trocar_app "$PISO"
entrar

# 3. O piso lê e opera as linhas novas, e recusa gravar acima das suas faixas
#    sem rebaixar o que já está no banco.
passo "3. piso $PISO: lista, ping e PATCH"
codigo=$(api GET /api/v1/heartbeats) || falhar "a API não respondeu em $BASE"
[ "$codigo" = 200 ] || falhar "GET /api/v1/heartbeats no piso deu $codigo"
anuais=$(jq --argjson s "$UM_ANO" --arg a "$ID" --arg b "$COPIA" \
    '[.items[] | select((.id == $a or .id == $b) and .schedule.period_sec == $s)] | length' "$CORPO") ||
    falhar "a lista do piso não veio em JSON"
[ "$anuais" = 2 ] || falhar "a lista do piso trouxe $anuais das 2 tarefas com period_sec $UM_ANO"

codigo=$(curl -s -o "$CORPO" -w '%{http_code}' -X POST "$PING_URL") ||
    falhar "a ping_url não respondeu"
[ "$codigo" = 200 ] || falhar "POST na ping_url no piso deu $codigo"

codigo=$(api PATCH "/api/v1/heartbeats/$ID" '{"name":"ensaio do piso, renomeada"}') ||
    falhar "a API não respondeu em $BASE"
[ "$codigo" = 200 ] || falhar "PATCH de name no piso deu $codigo"

codigo=$(api PATCH "/api/v1/heartbeats/$ID" \
    "{\"schedule\":{\"kind\":\"period\",\"period_sec\":$UM_ANO}}") ||
    falhar "a API não respondeu em $BASE"
[ "$codigo" = 400 ] || falhar "PATCH reenviando period_sec $UM_ANO no piso deu $codigo, queria 400"
grep -q '"out_of_range"' "$CORPO" || falhar "o 400 do PATCH no piso não traz out_of_range"
periodo=$(sql "SELECT period_sec FROM heartbeats WHERE id = '$ID'") ||
    falhar "não deu para ler o period_sec no banco"
[ "$periodo" = "$UM_ANO" ] || falhar "o period_sec no banco virou '$periodo' depois do PATCH recusado"

# 4. De volta ao branch, que lê o que o piso deixou. A pilha cai no trap.
passo "4. branch $BRANCH: volta e relê"
trocar_app "$BRANCH"
entrar
codigo=$(api GET "/api/v1/heartbeats/$ID") || falhar "a API não respondeu em $BASE"
[ "$codigo" = 200 ] || falhar "GET da tarefa de volta ao branch deu $codigo"
[ "$(jq -r '.schedule.period_sec' "$CORPO")" = "$UM_ANO" ] ||
    falhar "de volta ao branch, a tarefa perdeu o period_sec $UM_ANO"

echo "piso $PISO ensaiado sobre o schema de $BRANCH"
