#!/usr/bin/env bash
# Checagens do vigia externo (Plano 11, spec §6): deploy/vps/vigia.sh e as
# unidades vigilume-vigia.service/.timer. O job "vps" do CI roda; localmente,
# da raiz do repositório:
#
#   bash deploy/ci/checar-vigia.sh
#
# O curl é trocado por um falso (primeiro no PATH) que anota cada URL pedida e
# responde conforme o cenário: nada sai para a rede.
set -euo pipefail

RAIZ=$(cd "$(dirname "$0")/../.." && pwd)
VIGIA=$RAIZ/deploy/vps/vigia.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT

falhas=0
falhar() { echo "FALHOU: $*" >&2; falhas=$((falhas + 1)); }

mkdir -p "$T/bin" "$T/cred"
cat > "$T/bin/curl" <<'EOF'
#!/bin/sh
# O último argumento é a URL; os outros, as opções.
for url; do :; done
echo "$*" >> "$REGISTRO"
case "$url" in
    */healthz) [ "${SAUDE:-ok}" = ok ] ;;
    *) exit 0 ;;
esac
EOF
chmod +x "$T/bin/curl"

HC=https://hc-ping.com/0000-teste
# roda <cenário de saúde> <conteúdo do vigia.env>; devolve o código do vigia.
roda() {
    local c=0
    printf '%s\n' "$2" > "$T/cred/vigilume-vigia"
    : > "$T/registro"
    env -i PATH="$T/bin:/usr/bin:/bin" CREDENTIALS_DIRECTORY="$T/cred" REGISTRO="$T/registro" SAUDE="$1" \
        sh "$VIGIA" 2> "$T/erro" || c=$?
    echo "$c"
}
urls() { awk '{ print $NF }' "$T/registro" | tr '\n' ' '; }
ENV_OK="VIGILUME_DOMINIO=vigilume.exemplo.com
HC_URL=$HC"

# 1. Saudável: pede o /healthz público e pinga a HC_URL.
c=$(roda ok "$ENV_OK")
[ "$c" = 0 ] || falhar "saudável: o vigia saiu $c"
[ "$(urls)" = "https://vigilume.exemplo.com/healthz $HC " ] || falhar "saudável: pediu '$(urls)'"

# 2. Fora do ar: pinga HC_URL/fail e sai com erro (o systemd registra).
c=$(roda falha "$ENV_OK")
[ "$c" != 0 ] || falhar "fora do ar: o vigia saiu 0"
[ "$(urls)" = "https://vigilume.exemplo.com/healthz $HC/fail " ] || falhar "fora do ar: pediu '$(urls)'"

# 3. HC_URL com barra no fim: sem barra dupla no /fail.
c=$(roda falha "VIGILUME_DOMINIO=vigilume.exemplo.com
HC_URL=$HC/")
[ "$(urls)" = "https://vigilume.exemplo.com/healthz $HC/fail " ] || falhar "HC_URL com barra: pediu '$(urls)'"

# 4. Sem HC_URL: erro, e nenhum pedido.
c=$(roda ok "VIGILUME_DOMINIO=vigilume.exemplo.com
HC_URL=")
[ "$c" != 0 ] || falhar "sem HC_URL: o vigia saiu 0"
[ -z "$(urls)" ] || falhar "sem HC_URL: pediu '$(urls)'"
grep -q HC_URL "$T/erro" || falhar "sem HC_URL: não disse o que falta"

# 5. Sem domínio: é falha (pinga /fail), e não um pedido a https:///healthz.
c=$(roda ok "HC_URL=$HC")
[ "$c" != 0 ] || falhar "sem domínio: o vigia saiu 0"
[ "$(urls)" = "$HC/fail " ] || falhar "sem domínio: pediu '$(urls)'"

# 6. O /healthz com o certificado conferido (nada de -k/--insecure) e com
#    prazo; os pings com prazo.
roda ok "$ENV_OK" >/dev/null
grep -Eq -- '(^| )(-k|--insecure)( |$)' "$T/registro" && falhar "o vigia desliga a conferência do certificado"
[ "$(grep -c -- '--max-time' "$T/registro")" = 2 ] || falhar "algum pedido do vigia sem --max-time"

# 7. As unidades: o timer dispara o serviço a cada minuto; o serviço lê SÓ o
#    vigia.env (nunca o .env com os segredos) pelo LoadCredential, com
#    usuário dinâmico, e roda o vigia instalado.
S=$RAIZ/deploy/vps/vigilume-vigia.service
TM=$RAIZ/deploy/vps/vigilume-vigia.timer
grep -qx 'ExecStart=/usr/local/sbin/vigilume-vigia' "$S" || falhar "o serviço não roda /usr/local/sbin/vigilume-vigia"
grep -qx 'LoadCredential=vigilume-vigia:/opt/vigilume/vigia.env' "$S" || falhar "o serviço não entrega o vigia.env como vigilume-vigia"
[ "$(grep -c '^LoadCredential' "$S")" = 1 ] || falhar "o serviço entrega mais de uma credencial"
grep -v '^#' "$S" > "$T/servico-sem-comentario"
grep -Eq '/opt/vigilume/\.env|EnvironmentFile' "$T/servico-sem-comentario" && falhar "o serviço lê o .env com os segredos"
grep -qx 'DynamicUser=yes' "$S" || falhar "o serviço não usa usuário dinâmico"
grep -qx 'Type=oneshot' "$S" || falhar "o serviço não é oneshot"
grep -qx 'OnCalendar=\*-\*-\* \*:\*:00' "$TM" || falhar "o timer não dispara a cada minuto"
grep -qx 'Unit=vigilume-vigia.service' "$TM" || falhar "o timer não aponta para o serviço"
grep -qx 'WantedBy=timers.target' "$TM" || falhar "o timer não volta depois do reboot"
if command -v systemd-analyze >/dev/null; then
    # O verify confere a sintaxe e que o ExecStart existe: aponta o serviço
    # para o vigia do repositório, com os mesmos nomes de arquivo.
    mkdir -p "$T/unidades"
    sed "s|^ExecStart=.*|ExecStart=$VIGIA|" "$S" > "$T/unidades/vigilume-vigia.service"
    cp "$TM" "$T/unidades/"
    systemd-analyze verify "$T/unidades/vigilume-vigia.timer" "$T/unidades/vigilume-vigia.service" 2> "$T/verify" ||
        falhar "systemd-analyze verify: $(cat "$T/verify")"
    [ ! -s "$T/verify" ] || falhar "systemd-analyze verify avisou: $(cat "$T/verify")"
else
    echo "aviso: sem systemd-analyze, as unidades não foram verificadas" >&2
fi

# 8. Os modelos: o vigia.env.exemplo tem só o domínio e a HC_URL, sem valor;
#    a HC_URL não está mais no env.exemplo (o .env com os segredos).
[ "$(grep -Ev '^(#|$)' "$RAIZ/deploy/vps/vigia.env.exemplo" | sort | tr '\n' ' ')" = "HC_URL= VIGILUME_DOMINIO= " ] ||
    falhar "o vigia.env.exemplo não é só VIGILUME_DOMINIO= e HC_URL=: $(grep -Ev '^(#|$)' "$RAIZ/deploy/vps/vigia.env.exemplo" | tr '\n' ' ')"
grep -q '^HC_URL=' "$RAIZ/deploy/vps/env.exemplo" && falhar "a HC_URL ainda está no env.exemplo (é do vigia.env)"

# 9. O pior caso do vigia (tudo esgota o prazo) cabe no TimeoutStartSec, para
#    o /fail sair antes de o systemd matar o serviço; e o TimeoutStartSec fica
#    abaixo do intervalo do timer (60 s). A conta usa as opções de verdade de
#    cada curl do cenário "fora do ar". Sem --retry: o --max-time. Com
#    --retry, o curl respeita o Retry-After do servidor (que pode ser de
#    horas), e só o --retry-max-time o limita: o curl não começa espera nem
#    tentativa nova depois dele. O pior caso é então o --retry-max-time, mais
#    a maior espera exponencial (2^(retry-1) s), mais uma tentativa inteira
#    (--max-time). Com --retry e sem --retry-max-time, não há teto.
roda falha "$ENV_OK" >/dev/null
pior=$(awk '{
    m = 0; r = 0; t = 0
    for (i = 1; i < NF; i++) {
        if ($i == "--max-time") m = $(i + 1)
        if ($i == "--retry") r = $(i + 1)
        if ($i == "--retry-max-time") t = $(i + 1)
    }
    if (m == 0 || (r > 0 && t == 0)) sem = 1
    if (r == 0) { total += m; next }
    e = 1
    for (k = 1; k < r; k++) e *= 2
    total += t + e + m
} END { print (sem ? "sem-prazo" : total) }' "$T/registro")
teto=$(sed -n 's/^TimeoutStartSec=//p' "$S")
if [ -z "$pior" ] || [ "$pior" = sem-prazo ] || [ -z "$teto" ] || [ "$pior" -gt "$teto" ] || [ "$teto" -ge 60 ]; then
    falhar "pior caso do vigia: ${pior} s, TimeoutStartSec=${teto:-nenhum} (tem de caber, e abaixo de 60 s)"
fi

[ "$falhas" = 0 ] || exit 1
echo "vigia conferido"
