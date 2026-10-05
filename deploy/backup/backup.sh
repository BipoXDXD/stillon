#!/bin/sh
# Backup lógico do StillOn com restauração conferida (D32 do Plano 7).
#
# Roda DENTRO de postgres:18-alpine (o cliente tem de ser da mesma versão
# maior do servidor; o pg_dump do runner pode ser mais velho e recusar).
#
#   ORIGEM_URL   URI PostgreSQL com usuário e senha na autoridade, como stillon_backup
#                (NOSUPERUSER BYPASSRLS + pg_read_all_data). O dono das
#                tabelas NÃO serve: com FORCE ROW LEVEL SECURITY, o pg_dump
#                dele falha.
#   PGHOST, PGPORT, PGUSER, PGPASSWORD
#                o Postgres DESCARTÁVEL da restauração, como superusuário.
#   SAIDA        caminho do dump (formato custom).
#   VOLUME_BYTES opcional: tamanho do volume de produção, para o aviso de disco.
#
# Códigos: 0 = dump restaurado e conferido; 3 = uma checagem da cópia falhou
# (o dump não serve); 4 = dump restaurado e conferido, MAS o disco de produção
# passou de 85%: o dump é bom e deve ser guardado, e quem chama falha depois.
# Qualquer outro código (1 do pg_dump, por exemplo) = não houve backup.
#
# O dump só é dado como bom depois de restaurado e conferido. Texto claro
# nunca sai daqui: quem chama cifra o arquivo antes de guardá-lo.
set -eu
umask 077

: "${ORIGEM_URL:?defina ORIGEM_URL}"
: "${SAIDA:?defina SAIDA}"
: "${PGHOST:?defina PGHOST (o Postgres da restauração)}"
AQUI=$(cd "$(dirname "$0")" && pwd)

# O URI chega pelo ambiente do contêiner, mas não deve virar argv do psql
# nem do pg_dump (visível pelo ps do host). Separar a senha preserva o URI
# público e os parâmetros de conexão; só as chamadas à origem recebem sua
# PGPASSWORD, sem mudar a senha do Postgres descartável da restauração.
case "$ORIGEM_URL" in
    postgres://* | postgresql://*) ;;
    *) echo "backup: ORIGEM_URL precisa ser um URI PostgreSQL" >&2; exit 1 ;;
esac
origem_prefixo=${ORIGEM_URL%%://*}://
origem_resto=${ORIGEM_URL#*://}
origem_autoridade=${origem_resto%%/*}
case "$origem_autoridade" in
    *:*@*) ;;
    *) echo "backup: ORIGEM_URL precisa de usuário e senha na autoridade" >&2; exit 1 ;;
esac
origem_usuario=${origem_autoridade%%:*}
origem_credencial=${origem_autoridade#*:}
origem_senha_uri=${origem_credencial%%@*}
origem_host=${origem_credencial#*@}
case "$origem_host" in
    *@* | "") echo "backup: autoridade inválida em ORIGEM_URL" >&2; exit 1 ;;
esac
# A libpq decodifica percent-encoding de senha no URI. Reproduzir somente
# essa decodificação, por stdin (sem segredo no argv do awk), sem interpretar
# barras como escapes de shell. O restante continua sendo lido pela libpq.
origem_senha=$(printf '%s\n' "$origem_senha_uri" | LC_ALL=C awk '
    NR == 1 { senha = $0 }
    END {
        if (NR != 1) exit 1
        hex = "0123456789abcdef"
        for (i = 1; i <= length(senha); i++) {
            c = substr(senha, i, 1)
            if (c == "%") {
                a = index(hex, tolower(substr(senha, i + 1, 1)))
                b = index(hex, tolower(substr(senha, i + 2, 1)))
                if (i + 2 > length(senha) || a == 0 || b == 0) exit 1
                n = (a - 1) * 16 + b - 1
                if (n == 0 || n == 10 || n == 13) exit 1
                printf "%c", n
                i += 2
            } else printf "%s", c
        }
    }') || { echo "backup: senha inválida em ORIGEM_URL" >&2; exit 1; }
origem_url_publica=$origem_prefixo$origem_usuario@$origem_host${origem_resto#"$origem_autoridade"}
# Credenciais em query escapariam em argv. Opções reservadas poderiam mudar
# conexão, identidade ou prazos da sessão; recusar seus nomes após decoding,
# sem alterar os demais parâmetros libpq aceitos pelo chamador.
printf '%s\n' "$origem_url_publica" | LC_ALL=C awk '
    NR == 1 { uri = $0 }
    END {
        if (NR != 1) exit 1
        if (!index(uri, "?")) exit 0
        query = substr(uri, index(uri, "?") + 1)
        n = split(query, params, "&")
        hex = "0123456789abcdef"
        for (p = 1; p <= n; p++) {
            split(params[p], par, "=")
            key = ""
            for (i = 1; i <= length(par[1]); i++) {
                c = substr(par[1], i, 1)
                if (c == "%") {
                    a = index(hex, tolower(substr(par[1], i + 1, 1)))
                    b = index(hex, tolower(substr(par[1], i + 2, 1)))
                    if (i + 2 > length(par[1]) || a == 0 || b == 0) exit 1
                    c = sprintf("%c", (a - 1) * 16 + b - 1)
                    i += 2
                }
                key = key tolower(c)
            }
            if (key == "password" || key == "sslpassword" || key == "passfile" ||
                key == "oauth_client_secret" || key == "scram_client_key" || key == "scram_server_key" ||
                key == "options" || key == "service" || key == "application_name" ||
                key == "fallback_application_name" || key == "connect_timeout") exit 1
        }
    }' || { echo "backup: credencial ou parâmetro inválido na query de ORIGEM_URL" >&2; exit 1; }

# A URI secreta não precisa ser herdada pelo helper nem pelas ferramentas.
unset ORIGEM_URL POSTGRES_PASSWORD

# A consulta final de disco também é exclusiva da origem. env -i retira PG*
# do restauro (inclusive PGSERVICE/PGOPTIONS); a senha entra por stdin, não
# por argumento NAME=valor do env. A senha já recusou LF/CR no parser acima.
origem() {
    printf '%s\n' "$origem_senha" |
        env -i PATH="$PATH" PGCONNECT_TIMEOUT=10 \
            PGOPTIONS='-c statement_timeout=30000 -c client_connection_check_interval=1000' \
            sh -c 'IFS= read -r PGPASSWORD || exit 1
                export PGPASSWORD
                exec psql --no-password --no-psqlrc -X -Atq -v ON_ERROR_STOP=1 "$@"' \
            origem "$origem_url_publica" -c "$1"
}

echo "== origem"
echo "== dump"
# Gosu faz exec no mesmo PID, depois de reduzir UID/GID. O helper arma sua
# proteção de morte do pai após essa redução. A chamada é foreground direta:
# $(helper) criaria um subshell intermediário e enfraqueceria esse contrato.
meta=$(mktemp "${SAIDA%/*}/.stillon-snapshot.XXXXXX")
trap 'rm -f "$meta"' 0
if PGPASSWORD=$origem_senha STILLON_ORIGEM_PUBLICA=$origem_url_publica SAIDA=$SAIDA \
    /usr/local/bin/gosu 65532:65532 /stillon backup-snapshot > "$meta"; then
    :
else
    codigo=$?
    case "$codigo" in
        130 | 143) exit "$codigo" ;;
        *) echo "backup: snapshot ou dump não concluído" >&2; exit 1 ;;
    esac
fi
# Só depois de exit 0: uma linha terminada por LF, quatro campos, sem
# resultado parcial ou avaliação de shell. O protocolo não contém segredos.
if ! { [ "$(wc -c < "$meta")" -le 512 ] && [ "$(tail -c 1 "$meta" | wc -l)" -eq 1 ] &&
    LC_ALL=C awk -F '|' '
        NR == 1 {
            valido = NF == 4 && $1 == "stillon-backup-snapshot-v1"
            for (i = 2; i <= 4; i++) if ($i !~ /^[0-9]+$/) valido = 0
        }
        END { if (NR != 1 || !valido) exit 1 }' "$meta"; }; then
        echo "backup: manifesto do snapshot inválido" >&2; exit 1
fi
IFS='|' read -r _protocolo versao usuarios alvos < "$meta"
rm -f "$meta"
trap - 0
if ! { [ -f "$SAIDA" ] && [ ! -L "$SAIDA" ] && [ -s "$SAIDA" ]; }; then
    echo "backup: snapshot sem dump regular" >&2; exit 1;
fi
echo "schema $versao, $usuarios usuário(s), $alvos alvo(s)"
ls -l "$SAIDA"

echo "== restauração"
# O mesmo SQL do compose e de produção: papel comum, dono do banco.
psql --no-psqlrc -X -q -v ON_ERROR_STOP=1 -d postgres -f "$AQUI/../compose/initdb.sql"
# Como superusuário e PRESERVANDO os donos do dump: restaurar como o papel da
# aplicação esbarra no FORCE ROW LEVEL SECURITY, e --no-owner deixaria as
# tabelas com o dono errado (medido no spike do Plano 7).
pg_restore --exit-on-error --no-password -d stillon "$SAIDA"

echo "== checagens"
# Uma checagem que falha sai com 3 (o código do psql com ON_ERROR_STOP).
psql --no-psqlrc -X -q -d stillon -v versao="$versao" -v usuarios="$usuarios" -v alvos="$alvos" \
    -f "$AQUI/verificar.sql"
echo "backup conferido"

echo "== disco"
ocupado=$(origem "SELECT sum(pg_database_size(datname)) FROM pg_database")
echo "bancos ocupam $ocupado bytes"
if [ -n "${VOLUME_BYTES:-}" ]; then
    pct=$((ocupado * 100 / VOLUME_BYTES))
    resumo="Postgres: ${pct}% do volume (${ocupado} de ${VOLUME_BYTES} bytes, sem contar o WAL)"
    echo "$resumo"
    if [ "$pct" -ge 85 ]; then
        # O backup acima já está conferido; 4 (e não 3) diz a quem chama que
        # ele deve ser cifrado e guardado mesmo assim.
        echo "ERRO: acima de 85%: libere espaço ou aumente o disco" >&2
        exit 4
    elif [ "$pct" -ge 70 ]; then
        echo "AVISO: acima de 70% do volume"
    fi
fi
