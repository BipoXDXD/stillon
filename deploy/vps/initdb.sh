#!/bin/sh
# Papéis e banco do Vigilume em produção (Plano 11, spec §2). O entrypoint do
# postgres:18-alpine roda este arquivo UMA vez, na primeira subida (volume
# vazio), como o superusuário, com as senhas do /opt/vigilume/.env que o
# compose.prod.yaml passa ao serviço db. Nada de senha no repositório.
#
# - vigilume_app: comum (NOSUPERUSER NOBYPASSRLS), dono do banco. O boot
#   recusa um papel que atravesse a row-level security (storage.CheckRole).
# - vigilume_backup: BYPASSRLS + pg_read_all_data, só para o pg_dump (o do
#   dono falha com FORCE ROW LEVEL SECURITY). O DATABASE_URL do app nunca
#   aponta para ele.
#
# O entrypoint faz `source` de um .sh sem bit de execução: por isso nada de
# `set -u` aqui (ele valeria para o resto do entrypoint). As guardas abaixo
# fazem o papel dele.
: "${VIGILUME_APP_PASSWORD:?defina VIGILUME_APP_PASSWORD no /opt/vigilume/.env}"
: "${VIGILUME_BACKUP_PASSWORD:?defina VIGILUME_BACKUP_PASSWORD no /opt/vigilume/.env}"

# As senhas entram como variáveis do psql (:'app'), citadas por ele: nem
# aspas na senha quebram o SQL, nem a senha aparece na linha de comando.
psql -v ON_ERROR_STOP=1 --no-psqlrc --username "${POSTGRES_USER:-postgres}" --dbname postgres \
    -v app="$VIGILUME_APP_PASSWORD" -v backup="$VIGILUME_BACKUP_PASSWORD" <<'SQL'
CREATE ROLE vigilume_app LOGIN PASSWORD :'app'
    NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
CREATE DATABASE vigilume OWNER vigilume_app;
CREATE ROLE vigilume_backup LOGIN PASSWORD :'backup'
    NOSUPERUSER BYPASSRLS NOCREATEDB NOCREATEROLE;
GRANT pg_read_all_data TO vigilume_backup;
SQL
