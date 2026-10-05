#!/bin/sh
# Papéis e banco do StillOn em produção (Plano 11, spec §2). O entrypoint do
# postgres:18-alpine roda este arquivo UMA vez, na primeira subida (volume
# vazio), como o superusuário, com as senhas do /opt/stillon/.env que o
# compose.prod.yaml passa ao serviço db. Nada de senha no repositório.
#
# - stillon_app: comum (NOSUPERUSER NOBYPASSRLS), dono do banco. O boot
#   recusa um papel que atravesse a row-level security (storage.CheckRole).
# - stillon_backup: BYPASSRLS + pg_read_all_data, só para o pg_dump (o do
#   dono falha com FORCE ROW LEVEL SECURITY). O DATABASE_URL do app nunca
#   aponta para ele.
#
# O entrypoint faz `source` de um .sh sem bit de execução: por isso nada de
# `set -u` aqui (ele valeria para o resto do entrypoint). As guardas abaixo
# fazem o papel dele.
: "${STILLON_APP_PASSWORD:?defina STILLON_APP_PASSWORD no /opt/stillon/.env}"
: "${STILLON_BACKUP_PASSWORD:?defina STILLON_BACKUP_PASSWORD no /opt/stillon/.env}"

# O psql lê as senhas do ambiente por \getenv e cita os literais (:'app'):
# aspas e barras não quebram o SQL, e nenhum segredo entra no argv.
export STILLON_APP_PASSWORD STILLON_BACKUP_PASSWORD
psql -v ON_ERROR_STOP=1 --no-psqlrc --username "${POSTGRES_USER:-postgres}" --dbname postgres \
    <<'SQL'
\getenv app STILLON_APP_PASSWORD
\getenv backup STILLON_BACKUP_PASSWORD
CREATE ROLE stillon_app LOGIN PASSWORD :'app'
    NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
CREATE DATABASE stillon OWNER stillon_app;
CREATE ROLE stillon_backup LOGIN PASSWORD :'backup'
    NOSUPERUSER BYPASSRLS NOCREATEDB NOCREATEROLE;
GRANT pg_read_all_data TO stillon_backup;
SQL
