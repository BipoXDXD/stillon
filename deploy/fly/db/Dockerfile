# Postgres de produção do Vigilume: a imagem OFICIAL, a mesma (tag e digest)
# do compose e do workflow de backup (D28, D29 do Plano 7). Este arquivo
# não se constrói: existe para o Dependabot enxergar o digest (ele não lê o
# fly.toml), e o deploy (--image) e o backup.yml leem a imagem da linha FROM.
# O MESMO digest está fixado em quatro lugares: este Dockerfile, o
# compose.yaml, o .github/workflows/ci.yml e o internal/storage/testdb.
# Atualize os quatro juntos.
FROM postgres:18-alpine@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873
