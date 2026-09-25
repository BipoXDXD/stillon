# Seed do compose (D10 e D15 do Plano 7). Roda no alvo tools da imagem, que
# tem o busybox: a imagem de produção não tem shell (spec §5.9).
#
# 1. O usuário de demonstração, com a senha pelo stdin (contrato, decisão 1).
#    O `user create` aplica as migrations antes (CheckRole → Migrate), e é por
#    isso que o seed pode vir antes do app. Código 3 = já existe usuário, nada
#    mudou (Reconciliação 5): é o que torna o segundo `up` idempotente.
# 2. Com DEMO_TARGETS=1, o alvo https://example.com/, pela própria API, e mais
#    as URLs de DEMO_TARGETS_EXTRA (separadas por espaço), se houver. O
#    example.com é reservado para exemplo (RFC 2606 e 6761): o seed não sonda
#    o site de ninguém a cada 60 s. O app ainda não subiu (ele espera o
#    seed), então o seed sobe uma instância só-API em loopback, dentro deste
#    contêiner, e a derruba no fim. Os alvos precisam ser públicos: um serviço
#    dentro do compose tem IP privado, e o safedial o recusa de propósito
#    (spec §5.1).
set -u

printf '%s\n' "$DEMO_PASSWORD" | /vigilume user create --email "$DEMO_EMAIL"
rc=$?
if [ "$rc" -eq 3 ]; then
    echo "seed: já existe usuário; nada muda"
    exit 0
fi
[ "$rc" -eq 0 ] || exit "$rc"
[ "${DEMO_TARGETS:-0}" = 1 ] || exit 0

API=127.0.0.1:18080
HTTP_ADDR=$API /vigilume --role=api >/tmp/seed-api.log 2>&1 &
pid=$!
trap 'kill "$pid" 2>/dev/null; wait "$pid" 2>/dev/null' EXIT

tentativas=0
until HTTP_ADDR=$API /vigilume healthcheck 2>/dev/null; do
    tentativas=$((tentativas + 1))
    if [ "$tentativas" -ge 60 ]; then
        echo "seed: a API temporária não ficou pronta" >&2
        cat /tmp/seed-api.log >&2
        exit 1
    fi
    sleep 0.5
done

# O busybox não tem parser de JSON. O token é base64url com pontos, sem
# aspas; o E2E do CI pega uma mudança no formato da resposta.
postar() {
    wget -q -O - --header 'Content-Type: application/json' --header 'X-Requested-With: vigilume' "$@"
}
# -S manda os cabeçalhos da resposta para o stderr: o cookie de refresh vem
# de lá, para o logout do fim.
resposta=$(postar -S --post-data "{\"email\":\"$DEMO_EMAIL\",\"password\":\"$DEMO_PASSWORD\"}" \
    "http://$API/api/v1/auth/login" 2>&1)
token=$(printf '%s\n' "$resposta" | sed -n 's/.*"access_token":"\([^"]*\)".*/\1/p')
refresh=$(printf '%s\n' "$resposta" | sed -n 's/.*[Ss]et-[Cc]ookie: *\(vigilume_refresh=[^;]*\).*/\1/p')
if [ -z "$token" ] || [ -z "$refresh" ]; then
    echo "seed: o login do usuário de demonstração falhou" >&2
    exit 1
fi

n=0
for url in https://example.com/ ${DEMO_TARGETS_EXTRA:-}; do
    # O nome é o host da URL: https://www.exemplo.org/x → www.exemplo.org.
    nome=${url#*://}
    nome=${nome%%/*}
    corpo="{\"name\":\"$nome\",\"url\":\"$url\",\"method\":\"GET\",\"interval_sec\":60,\"timeout_ms\":10000,\"expected_status\":200}"
    if ! postar --header "Authorization: Bearer $token" --post-data "$corpo" \
        "http://$API/api/v1/targets" >/dev/null; then
        echo "seed: falhou ao cadastrar o alvo $url" >&2
        exit 1
    fi
    n=$((n + 1))
done
# A sessão do seed não fica viva no banco: logout revoga a família. Se ele
# falhar, um refresh de 7 dias fica parado no banco: é falha do seed, dita
# aqui, e não só uma contagem estranha no checar-compose.sh.
if ! postar --header "Cookie: $refresh" --post-data '' "http://$API/api/v1/auth/logout" >/dev/null; then
    echo "seed: o logout da sessão do seed falhou; a sessão ficou viva no banco" >&2
    exit 1
fi
echo "seed: usuário $DEMO_EMAIL e $n alvo(s) de exemplo"
