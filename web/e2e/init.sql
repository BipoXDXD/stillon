-- O papel comum do StillOn: dono do banco, sem superusuário nem BYPASSRLS.
-- O binário recusa subir com um papel que ignore a RLS (storage.CheckRole,
-- D8 do Plano 2). Roda uma vez, quando o container cria o banco.
CREATE ROLE stillon LOGIN PASSWORD 'stillon' NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
CREATE DATABASE stillon OWNER stillon;
