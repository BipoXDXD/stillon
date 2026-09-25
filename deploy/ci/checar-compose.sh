#!/bin/sh
# Checagens do compose do avaliador (spec do Plano 7, 9.3). O job "e2e" do CI
# as roda antes do Playwright; localmente, da raiz do repositório:
#
#   sh deploy/ci/checar-compose.sh
#
# Respeita COMPOSE_PROJECT_NAME, VIGILUME_PORT e VIGILUME_TAG, e passa
# UP_ARGS ao `docker compose up` (no CI: --no-build --pull never, para usar a
# imagem que acabou de passar no Trivy). Parte de volumes novos (as
# contagens do seed só valem num banco vazio). Deixa a pilha de pé no fim: o
# Playwright roda contra ela.
set -eu

PORTA=${VIGILUME_PORT:-8080}
BASE=http://127.0.0.1:$PORTA
EMAIL=demo@vigilume.local
SENHA=demo-vigilume-2026
TETO_S=90
falhas=0
falhar() { echo "FALHOU: $*" >&2; falhas=$((falhas + 1)); }

# Milissegundos desde a época. O `date +%N` é do GNU (Linux, o runner do
# CI); o do macOS devolve "N" literal, e aí a medida cai para segundos.
agora_ms() {
    ns=$(date +%s%N)
    case "$ns" in
        *N) echo $(($(date +%s) * 1000)) ;;
        *) echo $((ns / 1000000)) ;;
    esac
}
subir() {
    inicio=$(agora_ms)
    # shellcheck disable=SC2086 # UP_ARGS são várias flags, de propósito
    docker compose up --wait ${UP_ARGS:-} >&2 || return 1
    echo $(($(agora_ms) - inicio))
}
sql() { docker compose exec -T db psql -U postgres -d vigilume -Atq -c "$1"; }
entrar() {
    curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE/api/v1/auth/login" \
        -H 'Content-Type: application/json' -H 'X-Requested-With: vigilume' \
        -d "{\"email\":\"$EMAIL\",\"password\":\"$SENHA\"}"
}

# 1. Um comando, código 0, e o tempo à vista. O runner não é a máquina do
#    avaliador: o número serve para ver tendência; o teto pega o desastre.
ms=$(subir) || { docker compose logs >&2; echo "FALHOU: docker compose up --wait" >&2; exit 1; }
echo "docker compose up --wait: ${ms} ms"
[ -n "${GITHUB_STEP_SUMMARY:-}" ] && echo "docker compose up --wait: **${ms} ms**" >> "$GITHUB_STEP_SUMMARY"
[ "$ms" -le $((TETO_S * 1000)) ] || falhar "up --wait levou ${ms} ms, teto ${TETO_S} s"

# 2. O seed funcionou, e o Argon2id bate.
[ "$(entrar)" = 200 ] || falhar "login do usuário de demonstração não deu 200"

# 3. O painel está embutido e é servido na raiz, com a CSP de nonce.
pagina=$(curl -s -D - "$BASE/")
echo "$pagina" | grep -q 'src="/assets/index-' || falhar "GET / não devolveu o index.html do painel"
echo "$pagina" | grep -qi "^content-security-policy:.*'nonce-" || falhar "GET / sem CSP com nonce"

# 4. O papel da aplicação é comum e é dono das tabelas (D12).
papel=$(sql "SELECT rolsuper::text || ' ' || rolbypassrls::text FROM pg_roles WHERE rolname = 'vigilume_app'")
[ "$papel" = "false false" ] || falhar "vigilume_app com rolsuper/rolbypassrls = '$papel'"
alheias=$(sql "SELECT count(*) FROM pg_tables WHERE schemaname = 'public' AND tableowner <> 'vigilume_app'")
[ "$alheias" = 0 ] || falhar "$alheias tabela(s) sem o dono vigilume_app"

# 5. Os alvos de exemplo (D15): o example.com, reservado para isso, e os de
#    DEMO_TARGETS_EXTRA, se quem roda pediu. Nenhum outro site de terceiros.
# shellcheck disable=SC2086 # sem aspas de propósito: conta as palavras
extras=$(echo ${DEMO_TARGETS_EXTRA:-} | wc -w)
usuarios=$(sql "SELECT count(*) FROM users")
alvos=$(sql "SELECT count(*) FROM targets")
if ! { [ "$usuarios" = 1 ] && [ "$alvos" = $((1 + extras)) ]; }; then
    falhar "seed: $usuarios usuário(s) e $alvos alvo(s), queria 1 e $((1 + extras))"
fi
[ "$(sql "SELECT count(*) FROM targets WHERE url = 'https://example.com/'")" = 1 ] ||
    falhar "seed: o alvo https://example.com/ não foi cadastrado"

# 6. O seed não deixa sessão viva: ele encerra a que abriu para cadastrar os
#    alvos (um refresh de 7 dias parado no banco é uma credencial a menos
#    para vazar).
#    (A do seed é a do busybox: user agent "Wget".)
vivas=$(sql "SELECT count(*) FROM sessions WHERE revoked_at IS NULL AND user_agent LIKE 'Wget%'")
[ "$vivas" = 0 ] || falhar "o seed deixou $vivas sessão(ões) viva(s)"

# 7. Idempotência: um segundo `up` sai 0 e não duplica nada.
subir >/dev/null || falhar "o segundo up --wait falhou"
[ "$(sql "SELECT count(*) FROM users")/$(sql "SELECT count(*) FROM targets")" = "$usuarios/$alvos" ] ||
    falhar "o segundo up mudou usuários ou alvos"

# 8. Persistência no layout do 18 (D11): down sem -v, up, e o usuário continua.
docker compose down >&2
subir >/dev/null || falhar "o up depois do down falhou"
[ "$(sql "SELECT count(*) FROM users")" = 1 ] || falhar "o usuário sumiu depois de down + up"
[ "$(entrar)" = 200 ] || falhar "login depois de down + up não deu 200"

if [ "$falhas" -gt 0 ]; then
    docker compose logs >&2
    exit 1
fi
echo "compose conferido"
