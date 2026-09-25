# Imagem do Vigilume (Plano 7): o painel (Vite) → o binário Go com o painel
# embutido → distroless nonroot, sem shell nem ferramenta nenhuma (spec §5.9).
#
#   docker build -t vigilume .                  a imagem de produção (runtime)
#   docker build --target tools -t vigilume:tools .
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
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund
COPY web/ ./
# O vite.config.ts manda o build para ../internal/webui/dist (contrato).
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS build
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
      -o /out/vigilume ./cmd/vigilume

# tools: o mesmo binário sobre a variante debug, que tem o busybox. Só o seed
# do compose a usa, para passar a senha de demo pelo stdin (D6). NUNCA vai
# para o Fly.
FROM gcr.io/distroless/static-debian13:debug-nonroot@sha256:2a581fcbda6320d4d17fd6ff4774bb96e4825d2be9c2bca59f0777b429997f51 AS tools
COPY --from=build /out/vigilume /vigilume
USER nonroot:nonroot
ENTRYPOINT ["/vigilume"]

# runtime: o último estágio, o padrão do `docker build`. Sem HEALTHCHECK: o
# Fly faz as próprias checagens, e o compose usa `vigilume healthcheck` (D5).
FROM gcr.io/distroless/static-debian13:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3 AS runtime
COPY --from=build /out/vigilume /vigilume
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/vigilume"]
CMD ["--role=all"]
