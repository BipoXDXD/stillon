# O Postgres de produção da VPS: a imagem OFICIAL, a mesma (tag e digest) do
# deploy/vps/compose.prod.yaml. Este arquivo não se constrói: é o ponto fixo
# do digest para o Dependabot (ecossistema docker) e para quem precisa da
# imagem num script (sed -n 's/^FROM //p'). O MESMO digest está no
# compose.prod.yaml, no compose.yaml, na perna 18 do .github/workflows/ci.yml e
# no internal/storage/testdb; o TestPostgres18DigestIsTheSameEverywhere fica
# vermelho até todos andarem juntos.
FROM postgres:18-alpine@sha256:123b17998ccaf47e53b502e8c6a1e79afd8f579c6f3eec83c563da026f957395
