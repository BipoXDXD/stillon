# Imagem do StillOn (Plano 7): o painel (Vite) → o binário Go com o painel
# embutido → distroless nonroot, sem shell nem ferramenta nenhuma (spec §5.9).
#
#   docker build -t stillon .                  a imagem de produção (runtime)
#   docker build --target tools -t stillon:tools .
#                                               a variante com busybox, só para
#                                               o seed do compose (D6)
#
# As bases vão por digest (a tag fica no comentário): com a tag solta, o mesmo
# commit gera imagens diferentes em dias diferentes (D1). O Dependabot atualiza.

# web e build rodam na arquitetura de quem constrói, e o Go faz o
# cross-compile: amd64 e arm64 saem sem emulação (D2).
FROM --platform=$BUILDPLATFORM node:24-alpine@sha256:ebfe2f90462722a7a4de65e91990e97fe0d401c70e0e762c5b53302f905ec1c1 AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
# O build explícito abaixo continua; os hooks de instalação das dependências
# não rodam. Manter opcionais: elas entregam os binários nativos do bundler.
RUN --mount=type=cache,target=/root/.npm npm ci --ignore-scripts --no-audit --no-fund
COPY web/ ./
# O vite.config.ts manda o build para ../internal/webui/dist (contrato).
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26.10-alpine@sha256:27f2d3b83cfc5803c8078f8e730af0f7c431bf067afaec8f60a36fb763187cba AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY --from=web /src/internal/webui/dist/ internal/webui/dist/
# -buildvcs=false: o .git não entra no contexto (.dockerignore), e a versão
# vem do -X (D3). -s -w tira DWARF e símbolos; o stack trace de um panic
# continua legível. O ARG VERSION vem logo antes do único passo que o usa:
# declarado lá em cima, uma versão nova (todo commit, no CI) invalidaria o
# cache do `go mod download` também.
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -buildvcs=false -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/stillon ./cmd/stillon

# tools: o mesmo binário sobre a variante debug, que tem o busybox. Só o seed
# do compose a usa, para passar a senha de demo pelo stdin (D6). NUNCA vai
# para produção.
FROM gcr.io/distroless/static-debian13:debug-nonroot@sha256:c7b8faacc4439b8f5c41ca7ce4a3f8ff3e8520cb0c3444fae7292f687b00d0dd AS tools
COPY --from=build /out/stillon /stillon
USER nonroot:nonroot
ENTRYPOINT ["/stillon"]

# runtime: o último estágio, o padrão do `docker build`. Sem HEALTHCHECK: os
# compose (o local e o de produção) usam `stillon healthcheck` (D5).
FROM gcr.io/distroless/static-debian13:nonroot@sha256:18f4cc26c9ffa7f20a359298be7deadac5c607c8e03a02b8119f1a5fc2f82c99 AS runtime
COPY --from=build /out/stillon /stillon
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/stillon"]
CMD ["--role=all"]
