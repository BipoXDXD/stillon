#!/bin/sh
# Backup lógico do Vigilume com restauração conferida (D32 do Plano 7).
#
# Roda DENTRO de postgres:18-alpine (o cliente tem de ser da mesma versão
# maior do servidor; o pg_dump do runner pode ser mais velho e recusar).
#
#   ORIGEM_URL   conexão com o banco de produção como vigilume_backup
#                (NOSUPERUSER BYPASSRLS + pg_read_all_data). O dono das
#                tabelas NÃO serve: com FORCE ROW LEVEL SECURITY, o pg_dump
#                dele falha.
#   PGHOST, PGPORT, PGUSER, PGPASSWORD
#                o Postgres DESCARTÁVEL da restauração, como superusuário.
#   SAIDA        caminho do dump (formato custom).
#   VOLUME_BYTES opcional: tamanho do volume de produção, para o aviso de disco.
#
# O dump só é dado como bom depois de restaurado e conferido. Texto claro
# nunca sai daqui: quem chama cifra o arquivo antes de guardá-lo.
set -eu

: "${ORIGEM_URL:?defina ORIGEM_URL}"
: "${SAIDA:?defina SAIDA}"
: "${PGHOST:?defina PGHOST (o Postgres da restauração)}"
AQUI=$(cd "$(dirname "$0")" && pwd)

origem() { psql --no-psqlrc -X -Atq -v ON_ERROR_STOP=1 "$ORIGEM_URL" -c "$1"; }

echo "== origem"
versao=$(origem "SELECT max(version_id) FROM goose_db_version")
usuarios=$(origem "SELECT count(*) FROM users")
alvos=$(origem "SELECT count(*) FROM targets")
echo "schema $versao, $usuarios usuário(s), $alvos alvo(s)"

echo "== dump"
pg_dump --format=custom --no-password --file="$SAIDA" "$ORIGEM_URL"
ls -l "$SAIDA"

echo "== restauração"
# O mesmo SQL do compose e de produção: papel comum, dono do banco.
psql --no-psqlrc -X -q -v ON_ERROR_STOP=1 -d postgres -f "$AQUI/../compose/initdb.sql"
# Como superusuário e PRESERVANDO os donos do dump: restaurar como o papel da
# aplicação esbarra no FORCE ROW LEVEL SECURITY, e --no-owner deixaria as
# tabelas com o dono errado (medido no spike do Plano 7).
pg_restore --exit-on-error --no-password -d vigilume "$SAIDA"

echo "== checagens"
psql --no-psqlrc -X -q -d vigilume -v versao="$versao" -v usuarios="$usuarios" -v alvos="$alvos" \
    -f "$AQUI/verificar.sql"

echo "== disco"
ocupado=$(origem "SELECT sum(pg_database_size(datname)) FROM pg_database")
echo "bancos ocupam $ocupado bytes"
if [ -n "${VOLUME_BYTES:-}" ]; then
    pct=$((ocupado * 100 / VOLUME_BYTES))
    resumo="Postgres: ${pct}% do volume (${ocupado} de ${VOLUME_BYTES} bytes, sem contar o WAL)"
    echo "$resumo"
    if [ "$pct" -ge 85 ]; then
        echo "ERRO: acima de 85%: aumente o volume (fly volumes extend)" >&2
        exit 3
    elif [ "$pct" -ge 70 ]; then
        echo "AVISO: acima de 70% do volume"
    fi
fi
echo "backup conferido"
