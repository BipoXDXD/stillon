# O Postgres de produção da VPS: a imagem OFICIAL, a mesma (tag e digest) do
# deploy/vps/compose.prod.yaml. Este arquivo não se constrói: é o ponto fixo
# do digest para o Dependabot (ecossistema docker) e para quem precisa da
# imagem num script (sed -n 's/^FROM //p'). O MESMO digest está no
# compose.prod.yaml, no compose.yaml, na perna 18 do .github/workflows/ci.yml e
# no internal/storage/testdb; o TestPostgres18DigestIsTheSameEverywhere fica
# vermelho até todos andarem juntos.
FROM postgres:18-alpine@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873
