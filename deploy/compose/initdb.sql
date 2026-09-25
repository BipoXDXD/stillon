-- Papel e banco da aplicação. É o MESMO SQL em dois lugares: o init do
-- Postgres do compose e o banco descartável em que o backup é restaurado todo
-- dia. O Postgres de produção cria os papéis com deploy/vps/initdb.sh, com as
-- senhas geradas na VPS.
--
-- O papel é comum de propósito: sem SUPERUSER e sem BYPASSRLS, que
-- atravessariam a row-level security em silêncio. O boot recusa um papel
-- assim (storage.CheckRole). Ser dono do BANCO basta para tudo o que as
-- migrations fazem: desde o PostgreSQL 15 o schema public pertence a
-- pg_database_owner, e o citext é uma extensão "trusted".
--
-- A senha abaixo é de demonstração e fica pública no repositório; o banco do
-- compose só escuta na rede interna do compose.
CREATE ROLE vigilume_app LOGIN PASSWORD 'vigilume_app'
    NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
CREATE DATABASE vigilume OWNER vigilume_app;
