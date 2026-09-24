-- O papel comum do Vigilume: dono do banco, sem superusuário nem BYPASSRLS.
-- O binário recusa subir com um papel que ignore a RLS (storage.CheckRole,
-- D8 do Plano 2). Roda uma vez, quando o container cria o banco.
CREATE ROLE vigilume LOGIN PASSWORD 'vigilume' NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
CREATE DATABASE vigilume OWNER vigilume;
