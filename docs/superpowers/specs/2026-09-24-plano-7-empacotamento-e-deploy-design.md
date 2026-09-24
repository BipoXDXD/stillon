# Vigilume — Plano 7: Empacotamento, Compose, CI, Deploy e README (design)

- **Data:** 2026-09-24
- **Status:** rascunho para revisão do usuário (junto com as specs dos Planos 5 e 6)
- **Contrato:** `docs/superpowers/specs/2026-09-24-planos-5-6-7-contrato.md`
- **Spec base:** `docs/superpowers/specs/2026-09-22-vigilume-design.md` (§1.1, §3.1, §5.7, §5.9, §13, §14, §15)
- **Pendências absorvidas:** `PENDENCIAS-PARA-O-PLANO-5.md`, seção "Já valendo em produção" (itens 1 a 6), e
  os detalhes do papel do Postgres em `PENDENCIAS-PARA-O-PLANO-3.md` (item 2)

> **Achado que muda o escopo.** A pendência mais antiga do projeto diz que o `TRUSTED_PROXIES` é
> "configuração, não código". **Não é.** A documentação atual do Fly diz que o elemento **mais à
> direita** do `X-Forwarded-For` é o IP **do app** (o anycast em que o cliente bateu), e o
> `ClientIP` de hoje pega justamente o último salto. Reproduzido num spike: com
> `TRUSTED_PROXIES` preenchido e o `X-Forwarded-For` no formato do Fly, oito clientes diferentes
> dividiram um balde de login e levaram 429 a partir do sexto pedido (seção 10.4). Configurar só
> a variável deixaria o problema igual e com cara de resolvido. A correção é código: ler o
> `Fly-Client-IP` (D22).

> **Não há deploy nenhum hoje.** Não existe `fly.toml`, `Dockerfile` nem compose no histórico do
> git. Os itens "já valendo em produção" das pendências valem, na prática, para o **primeiro**
> deploy. O runbook da seção 6 parte de um banco vazio. O apêndice 6.6 cobre o caso de já haver
> tabelas com outro dono.

---

## 1. Objetivo

Fazer o Vigilume **rodar em qualquer máquina com um comando só**, **passar por um pipeline que
dá para mostrar a alguém** e **poder ir para o Fly.io com um runbook que não falha no primeiro
boot**. O plano também fecha as pendências de deploy herdadas do Plano 1 ao 4: IP real do
cliente, papel do Postgres sem `BYPASSRLS`, métricas, e-mail, `PUBLIC_BASE_URL` e primeira
manutenção.

Critérios de pronto, em ordem de prioridade:

1. `git clone … && docker compose up --wait` deixa o painel aberto em `http://localhost:8080`,
   com um usuário de demonstração pronto para login, dentro de **30 s** numa máquina e numa rede
   razoáveis (spec §15, requisito não negociável). Medido em 10.2.
2. A imagem é multi-stage, termina em `distroless/static:nonroot` e passa no Trivy sem achado
   HIGH ou CRITICAL corrigível.
3. O CI roda, em PR, tudo o que a spec §15 lista, mais o E2E do Playwright contra o **mesmo
   compose** que o avaliador usa, em ~5 min de relógio.
4. `fly.toml` e um runbook manual. O deploy real acontece **só com confirmação do usuário**
   (contrato, decisão 6).
5. Um README que um avaliador de vaga backend/fullstack Go entende em 2 minutos (spec §1.1).

## 2. Escopo e fora de escopo

### 2.1 Dentro

| Item | Onde |
|---|---|
| `Dockerfile` multi-stage (web → build → runtime) e o alvo `tools` para o seed | raiz |
| `.dockerignore` | raiz |
| `compose.yaml` e o SQL de init do Postgres | raiz, `deploy/compose/` |
| Subcomandos `vigilume healthcheck` e `vigilume version`, e `var version` com `-X` | `cmd/vigilume` |
| **Correção do IP do cliente atrás do Fly** (`CLIENT_IP_HEADER`, D22) | `internal/config`, `internal/httpapi/middleware/clientip.go` |
| Imagem do Postgres de teste parametrizável e matriz 17/18 no CI (D27) | `internal/storage/testdb`, CI |
| CI estendido: web, imagem, Trivy, E2E, publicação no GHCR, deploy manual | `.github/workflows/` |
| `dependabot.yml` (spec §5.9 manda, e ainda não existe) | `.github/` |
| `fly.toml` | raiz |
| Runbook de deploy e de papel do Postgres | `docs/deploy/fly.md` |
| README | raiz |
| Alvos novos no `Makefile` (`image`, `up`, `down`, `e2e`) | `Makefile` |

### 2.2 Fora

| Item | Motivo |
|---|---|
| Executar o deploy, criar app, banco, usuário ou segredo no Fly | Ação externa na conta do usuário (contrato, decisão 6). O plano entrega os artefatos e o runbook |
| Tornar o repositório ou o pacote do GHCR públicos | Decisão do usuário (seção 12) |
| Domínio próprio, TLS próprio | Fora do escopo da spec (§2.2); `*.fly.dev` basta |
| Várias máquinas, `--role=api` e `--role=prober` separados em produção | O desenho aguenta (spec §3.1), mas custa o dobro e não soma sinal ao portfólio agora |
| Raspagem de métricas pelo Fly | Exige relaxar a D10 do Plano 2 (seção 6.4, D24) |
| Backups além dos do Postgres gerenciado | O MPG já tem backup; restauração testada fica para depois |
| Testes Playwright propriamente ditos | São do Plano 6. O Plano 7 os roda no CI contra o compose |
| R2 (auth → sqlc) | Plano próprio (contrato, decisão 5) |

---

## 3. Imagem

### 3.1 Forma

```
web     FROM --platform=$BUILDPLATFORM node:24-alpine@sha256:…
        npm ci  →  npm run build  →  /src/internal/webui/dist
build   FROM --platform=$BUILDPLATFORM golang:1.26-alpine@sha256:…
        go mod download (cache mount)  →  COPY cmd internal  →  COPY --from=web dist
        CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH
        go build -trimpath -buildvcs=false -ldflags="-s -w -X main.version=$VERSION"
tools   FROM gcr.io/distroless/static-debian13:debug-nonroot@sha256:…   (só o compose usa)
runtime FROM gcr.io/distroless/static-debian13:nonroot@sha256:…         (último estágio, padrão)
        COPY /vigilume ; USER nonroot:nonroot ; EXPOSE 8080
        ENTRYPOINT ["/vigilume"] ; CMD ["--role=all"]
```

O painel sai do Vite direto em `internal/webui/dist/`, que o `//go:embed all:dist` do Plano 6
embute (contrato, decisão 2). O `vite.config.ts` precisa de `build.outDir: '../internal/webui/dist'`
e de `emptyOutDir: true`, que o Vite exige explícito quando o destino fica fora da raiz do
projeto web. O spike usou esses dois valores (10.1).

### 3.2 Decisões

| # | Decisão | Alternativa | Custo se errada |
|---|---|---|---|
| D1 | Base final **`gcr.io/distroless/static-debian13:nonroot`**, fixada por **digest** (a tag vem no comentário); o Dependabot atualiza o digest | `static-debian12`; fixar só pela tag | Com a tag solta, o mesmo commit gera imagens diferentes em dias diferentes, e um CVE novo aparece "do nada" no Trivy. As duas versões do Debian existem (conferido); a 13 é a estável atual |
| D2 | Estágios `web` e `build` em **`$BUILDPLATFORM`**, com cross-compile por `GOOS/GOARCH`. Publicação **multi-arch (amd64 + arm64)** | Só amd64 | Um avaliador num Mac com Apple Silicon rodaria a imagem amd64 emulada: mais lento e com avisos. Medido: amd64 + arm64 juntos em 13 s com cache quente, sem QEMU (10.1) |
| D3 | `-trimpath -buildvcs=false -ldflags "-s -w -X main.version=…"`. `VERSION` vem de `git describe --tags --always --dirty` no Makefile e do `github.sha` no CI | Carimbo VCS automático (`debug.ReadBuildInfo`) | O carimbo automático exige o `.git` dentro do contexto do build, o que engorda o contexto e embute o estado da árvore. O `-s -w` tira DWARF e tabela de símbolos, mas stack trace de panic continua legível (o `pclntab` fica) e o Trivy continua lendo as dependências do binário (conferido: `gobinary` escaneado) |
| D4 | `var version = "dev"` em `main`. Aparece no log `iniciando` e em `vigilume version`, e **não** vai para nenhuma resposta HTTP | Expor em `/healthz` ou num header | Expor a versão facilita o *fingerprinting* de uma versão vulnerável, e o avaliador vê a versão no log e no README |
| D5 | **Sem `HEALTHCHECK` no Dockerfile.** Subcomando `vigilume healthcheck` (GET em `http://127.0.0.1:<porta de HTTP_ADDR>/readyz`, prazo de 2 s, sai 0 ou 1), usado pelo compose | `HEALTHCHECK` na imagem; imagem com `curl`/`wget` | O Fly ignora o `HEALTHCHECK` do Docker e faz as próprias checagens (6.3). Pôr `wget` na imagem final quebra o "sem shell, sem ferramenta" da §5.9. O custo é um subcomando pequeno |
| D6 | Alvo **`tools`** = `distroless/static-debian13:debug-nonroot` + o mesmo binário. Só o seed do compose usa; **nunca** vai para o Fly | Senha do usuário demo por variável de ambiente | O contrato fixou a senha pelo stdin, e o compose não tem como alimentar stdin sem shell: o pipe precisa do `busybox sh` da variante `debug`. Senha por env seria uma divergência do contrato e um caminho a mais para vazar senha em `docker inspect` |
| D7 | `.dockerignore` deixa de fora `.git`, `.idea`, `.superpowers`, `bin`, `web/node_modules`, `internal/webui/dist/*` (menos o `.gitkeep`), `**/*_test.go` e `coverage.out` | Sem `.dockerignore` | Sem ele, um `dist` velho da máquina entra no build em vez do que o estágio `web` gerou, e os testes entram no contexto |
| D8 | Cache mounts do BuildKit para `/go/pkg/mod`, `/root/.cache/go-build` e `/root/.npm`. No CI, cache `type=gha` do buildx | Camadas simples | Sem cache, cada build do CI refaz `npm ci` e `go mod download`. O custo é pequeno: 5 s cada nesta máquina, mais num runner |
| D9 | **Reprodutibilidade do binário**, não da imagem. Dois builds `--no-cache` geraram o **mesmo SHA-256** de binário, com painel embutido (10.1). O digest da imagem muda a cada build por causa dos timestamps | `SOURCE_DATE_EPOCH` + `rewrite-timestamp=true` para o digest também bater | Nenhum para o portfólio. Se um dia valer a pena, são duas linhas no workflow |

### 3.3 Números medidos (spike, seção 10)

| Medida | Valor |
|---|---|
| Binário (com painel de exemplo embutido, `-s -w`) | **18,2 MB** |
| Imagem `runtime` descomprimida (camadas: binário + distroless) | **~20 MB** (o `docker images` mostra 31,5 MB porque o store do containerd soma blob comprimido e snapshot) |
| Imagem `runtime` comprimida (o que se baixa) | **7,5 MB** por arquitetura |
| Trivy, HIGH/CRITICAL corrigíveis | **0** (Debian 13.7 e o `gobinary`) |
| Build frio (sem cache nem imagens-base) | **27 s** nesta máquina (16 núcleos) |
| Rebuild depois de mudar Go, com cache | **2,6 s** |

---

## 4. docker compose

### 4.1 Forma

```
db    postgres:18-alpine        volume em /var/lib/postgresql (layout do 18, ver D11)
      init: deploy/compose/initdb.sql cria o papel vigilume_app e o banco vigilume (dono: o papel)
      healthcheck: pg_isready -h 127.0.0.1 -U vigilume_app -d vigilume
seed  alvo tools  depends_on: db healthy
      sh -c 'printf "%s\n" "$DEMO_PASSWORD" | vigilume user create --email demo@vigilume.local
             rc=$?; [ $rc -eq 3 ] && exit 0      # 3 = já existe usuário (Plano 5)
             [ $rc -eq 0 ] || exit $rc
             <alvos de exemplo, só neste caminho: D15>'
app   alvo runtime  --role=all  depends_on: db healthy, seed completed_successfully
      healthcheck: ["CMD", "/vigilume", "healthcheck"]  (start_interval 500ms)
      ports: 127.0.0.1:8080:8080
```

Cada serviço do app declara `image: ghcr.io/bipoxdxd/vigilume:<tag>` **e** `build:` com o
`target` certo. Comportamento conferido no Compose v5.5: com a imagem publicada, ele **puxa**; se
o pull é negado (pacote privado ou tag inexistente), ele **constrói** do código (10.3).

### 4.2 Decisões

| # | Decisão | Alternativa | Custo se errada |
|---|---|---|---|
| D10 | Ordem **db → seed → app**. O seed aplica as migrations (o `user create` do Plano 5 faz `CheckRole` → `Migrate` antes de tudo, D17 dele) e cria o usuário; o app só sobe depois de o seed sair com 0 | Seed depois do app `healthy` (a ordem óbvia) | Medido: com o seed por último, `docker compose up --wait` devolve **código 1** mesmo com o seed saindo 0, porque o `--wait` trata como falha o contêiner que terminou. Com o seed como dependência `service_completed_successfully`, devolve 0 (10.2). O custo é o seed precisar migrar |
| D11 | **Postgres 18** no compose (`postgres:18-alpine`), com o volume em **`/var/lib/postgresql`**, não em `…/data` | `postgres:16-alpine`, como o testdb de hoje | Conferido na documentação da imagem oficial: a partir do 18, `PGDATA=/var/lib/postgresql/18/docker` e o `VOLUME` declarado é `/var/lib/postgresql`. Montar em `…/data` no 18 grava num volume anônimo, e os dados somem ao recriar o contêiner. Medido: com a montagem nova, os dados sobreviveram a um `down` + `up` (10.2) |
| D12 | Papel da aplicação no compose igual ao de produção: `vigilume_app` com `NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE`, e **`CREATE DATABASE vigilume OWNER vigilume_app`** | `GRANT CREATE ON DATABASE` + `GRANT CREATE ON SCHEMA public`, como o testdb | Desde o PG15 o schema `public` pertence a `pg_database_owner`, então quem é dono do banco é dono do `public` e dispensa os dois GRANTs. Conferido: as 17 tabelas e o `citext` ficaram com `vigilume_app` como dono, e o boot aceitou o papel (`rolsuper=f`, `rolbypassrls=f`). O superusuário `postgres` só existe para o init |
| D13 | **Chaves de demonstração fixas** no `compose.yaml` (`MASTER_KEY`, `JWT_SECRET`, `IP_HASH_KEY`), base64 de um texto que diz "demo, não use em produção", com um comentário gritando isso | Gerar chaves no primeiro `up` | Gerar exige mais um contêiner com shell e um volume compartilhado, e o segundo `up` precisa reler as mesmas chaves (senão os segredos cifrados ficam ilegíveis). O risco da fixa é alguém copiar o compose para produção: ver D14 |
| D14 | O boot **recusa** as três chaves de demonstração quando `PUBLIC_BASE_URL` é `https://` | Só o comentário | Sem a recusa, um deploy copiado do compose sobe com chaves públicas, e qualquer um forja JWT e decifra os segredos. São ~10 linhas em `config.Load`, com teste. **Para o usuário revisar** (seção 12) |
| D15 | Seed cria o usuário `demo@vigilume.local` (senha de demo com ≥ 15 caracteres, a política do Plano 5) e, com `DEMO_TARGETS=1` (padrão no compose), **três alvos públicos** pela própria API (login + `POST /targets` com `wget` do busybox). Os alvos só são criados quando o `user create` devolveu **0**; com **3** (usuário já existe), o seed sai 0 sem tocar em nada, e é isso que torna o segundo `up` idempotente | Só o usuário; um subcomando `target create` | Um dashboard vazio na primeira tela vende mal o projeto. Os alvos **precisam** ser públicos: um serviço falso dentro do compose tem IP privado e o `safedial` o recusa, **por desenho** (spec §5.1). Vale uma linha no README. Sem internet, os alvos ficam `down`, e isso também é demonstração. O parser de JSON em `sed` é frágil; o E2E do CI o cobre |
| D16 | `PUBLIC_BASE_URL=http://localhost:8080`, `CORS_ORIGIN=http://localhost:5173` (o `vite dev` do Plano 6), `METRICS_ADDR=off`, porta só em `127.0.0.1` | Porta em `0.0.0.0` | Com a porta aberta em todas as interfaces, uma rede de café veria o painel de demo com senha conhecida. Com http, o cookie sai sem `Secure` (lógica existente em `routes.go`), e é o que o desenvolvimento local precisa |
| D17 | O avaliador tem **dois caminhos**: pull da imagem publicada (padrão, rápido) ou build do código (fallback automático) | Só build | O build frio levou 27 s nesta máquina de 16 núcleos. Num notebook comum passa de 30 s com folga. Só o caminho do pull cumpre a §15, e ele depende de o pacote do GHCR ser público (seção 12) |

### 4.3 Os 30 segundos

| Cenário | Medido | Observação |
|---|---|---|
| Imagens locais, volumes novos, `up --wait` até app `healthy` e seed concluído | **3,5 s** (PG16) / **4,0 s** (PG18), três rodadas cada | O seed leva 85 a 200 ms, quase tudo Argon2id |
| Pull de um Postgres alpine que não estava na máquina | **5,1 s** | `postgres:17-alpine` como amostra; o 16-alpine tem ~115 MB comprimidos |
| Pull da imagem do app | não medido (o pacote não existe) | 7,5 MB comprimidos; menos de 1 s nesta rede |
| Build do código, frio | **27 s** + 4 s de `up` | Fica no limite aqui e passa em máquina mais lenta |

**Conclusão:** com a imagem publicada, a conta é ~123 MB de download + 4 s. Cabe em 30 s numa
conexão a partir de ~40 Mbit/s. Em rede lenta, o gargalo é o Postgres, não o Vigilume. O README
diz isso e sugere `docker compose pull` antes do `up`. O CI mede o `up --wait` a cada PR e
imprime o tempo (seção 9).

---

## 5. CI

### 5.1 Forma

```
                ┌── go (matriz pg 17 / pg 18) ──────────────┐
PR e main ──────┼── web (typecheck, lint, vitest, build) ───┼──► ok
                └── image (build amd64, Trivy) ──► e2e ─────┘
main (depois do verde) ──► publish (multi-arch, GHCR, digest)
workflow_dispatch + environment "production" ──► deploy (flyctl deploy --image …@digest)
```

| Job | Passos | Tempo estimado |
|---|---|---|
| `go` | O de hoje (`sqlc diff`, `vet`, `staticcheck`, `gosec`, `govulncheck`, `test -race`), com as **ferramentas em versão fixa** e `TEST_POSTGRES_IMAGE` da matriz | ~3 min (a última execução real: 108 s de teste, 186 s no total) |
| `web` | `npm ci`, `npm run typecheck`, `npm run lint`, `vitest run`, `npm run build` | ~1–1,5 min |
| `image` | `docker/build-push-action` com `load: true`, só amd64, cache `gha`; Trivy (`--severity HIGH,CRITICAL --ignore-unfixed --exit-code 1`); checagens da imagem (9.3) | ~1,5–2 min |
| `e2e` | Recebe a imagem do `image` (artifact `docker save`), `docker compose up --wait` com `pull_policy: never`, imprime o tempo, `npx playwright install --with-deps chromium`, `npx playwright test`, relatório como artifact em falha | ~2–3 min |
| `publish` | Só em `push` para `main`: build multi-arch, push para `ghcr.io/bipoxdxd/vigilume:{sha,latest}` e `:{sha}-tools`, e o digest como saída | ~2 min |
| `deploy` | Só `workflow_dispatch`, `environment: production` com revisor obrigatório: `flyctl deploy --image ghcr.io/…@sha256:… --strategy rolling` | ~1–2 min |

Relógio de um PR: **~5 min** (o caminho mais longo é `image` → `e2e`, rodando em paralelo com
`go`). Hoje são ~3 min.

### 5.2 Decisões

| # | Decisão | Alternativa | Custo se errada |
|---|---|---|---|
| D18 | O **E2E roda contra o `compose.yaml` do avaliador**, com a imagem que acabou de passar no Trivy | Servidor Go + Vite num `webServer` do Playwright | Um E2E feito por fora poderia ficar verde com o compose quebrado. Assim, **o requisito dos 30 s vira teste de regressão**: se o seed, o healthcheck, o init do Postgres ou o painel embutido quebrarem, o PR fica vermelho |
| D19 | E2E **em todo PR**, só no Chromium | Só na `main`; os três navegadores | Na `main`, um E2E vermelho aparece depois do merge. Três navegadores triplicam o tempo, e o fluxo crítico (spec §14) não tem nada específico de navegador |
| D20 | **Actions fixadas por SHA completo** (a tag vem no comentário) e **ferramentas Go com versão** (`staticcheck@<v>`, `gosec@<v>`, `govulncheck@<v>`), mais Dependabot para `github-actions`, `gomod`, `npm` (em `/web`) e `docker` | `@v5`, `@latest` | Com tag móvel, um push forçado na tag de uma action roda código alheio com o `GITHUB_TOKEN` (o caso `tj-actions/changed-files`, de 2025). `@latest` faz o CI de hoje mudar de resultado sem commit nenhum. A §5.9 já pede o Dependabot |
| D21 | `permissions: contents: read` no workflow inteiro. `packages: write` só no `publish`. Deploy com token **do app** (`fly tokens create deploy`) guardado no **environment** `production`, não no repositório. `concurrency` cancela o PR anterior do mesmo branch; o deploy nunca é cancelado | Token pessoal do Fly nos secrets do repositório | Um token de organização vazado mexe em tudo o que o usuário tem no Fly; o de deploy só faz deploy deste app. No environment, um PR de fork nem enxerga o token |

---

## 6. Deploy no Fly.io

### 6.1 Topologia

Uma máquina `shared-cpu-1x` com 512 MB, `--role=all`, na mesma região do Postgres gerenciado
(Fly **Managed Postgres**, MPG). A API, o sondador, o dispatcher e a manutenção ficam no mesmo
processo (spec §3.1).

### 6.2 `fly.toml` (trechos)

```toml
app = "vigilume"                 # nome a confirmar
primary_region = "gru"           # a confirmar contra as regiões do MPG
kill_signal = "SIGTERM"
kill_timeout = "25s"

[env]
  PUBLIC_BASE_URL  = "https://vigilume.fly.dev"
  CORS_ORIGIN      = "https://vigilume.fly.dev"
  CLIENT_IP_HEADER = "Fly-Client-IP"
  TRUSTED_PROXIES  = "…"          # ver 6.5, passo 8
  METRICS_ADDR     = "off"
  LOG_LEVEL        = "info"

[http_service]
  internal_port = 8080
  force_https = true
  auto_stop_machines = "off"
  auto_start_machines = false
  min_machines_running = 1

  [[http_service.checks]]
    method = "GET"
    path = "/healthz"
    interval = "15s"
    timeout = "2s"
    grace_period = "20s"

[[vm]]
  size = "shared-cpu-1x"
  memory = "512mb"
```

### 6.3 Decisões

| # | Decisão | Alternativa | Custo se errada |
|---|---|---|---|
| D22 | **IP do cliente pelo `Fly-Client-IP`**, com `CLIENT_IP_HEADER` novo na config: vazio (padrão) mantém o comportamento de hoje (último salto do XFF); preenchido, o `ClientIP` lê **esse header, e só ele**, quando a conexão vem de `TRUSTED_PROXIES`. Header ausente ou ilegível cai no `RemoteAddr`. Com `CLIENT_IP_HEADER` preenchido, o header chegando de origem **fora** de `TRUSTED_PROXIES` gera um aviso no log com o `remote_addr`, no máximo um por minuto: é o sinal de configuração errada, e é por ele que se descobre o CIDR do proxy (6.5, passo 8) | Pegar o **penúltimo** salto do XFF; `TRUSTED_HOPS=N` | A documentação do Fly diz que o último elemento do XFF é o IP do app e recomenda o `Fly-Client-IP` quando não há outro proxy na frente. "Penúltimo" depende de um detalhe que o Fly não promete. O header dedicado tem um valor só, que o proxy escreve. Os três limitadores (API, `/verify/`, `/p/`) e o `ip_hash` da auditoria passam pelo mesmo `ClientIP` e são corrigidos juntos |
| D23 | `auto_stop_machines = "off"` e `min_machines_running = 1` | O padrão do `fly launch` (`"stop"` e 0) | **Crítico.** Com o padrão, o Fly desliga a máquina sem tráfego, e **o monitor para de sondar em silêncio**: nenhum incidente abre, nenhuma notificação sai. O `fly launch` gera o padrão, e por isso o `fly.toml` é escrito à mão e o runbook usa `fly apps create`, não `fly launch` |
| D24 | `kill_timeout = "25s"`, `kill_signal = "SIGTERM"` explícito | Os padrões | O padrão do Fly é **5 s**, e o `ShutdownGrace` do app é 15 s (spec §6.2): o SIGKILL chegaria no meio do shutdown gracioso. A documentação do Fly se contradiz sobre o sinal padrão (SIGINT numa página, SIGTERM noutra). O app trata os dois, mas explícito é melhor |
| D25 | Checagem de serviço em **`/healthz`**, não em `/readyz` | `/readyz` | O servidor HTTP só escuta **depois** de `CheckRole` e `Migrate`, então um `/healthz` 200 já prova boot completo. Com `/readyz`, um soluço do banco tira a máquina única da rota, e a status page vira o erro genérico do proxy do Fly em vez do 503 em HTML que ela mesma sabe dar (D21 do Plano 4) |
| D26 | Estratégia **`rolling`** (padrão). Com uma máquina, cada deploy fica alguns segundos fora | `bluegreen` | O desenho aguenta `bluegreen`: dois sondadores juntos são seguros (`SKIP LOCKED`), e as migrations se serializam (advisory lock). Mas o código velho rodaria alguns segundos contra o schema novo, e toda migration teria de ser compatível para trás. Fica como passo seguinte, junto com a regra "migration só aditiva" |
| D27 | **Migrations no boot** (spec §15), sem `release_command` | `release_command = "/vigilume migrate"` | Com `release_command`, uma migration quebrada aborta o deploy **antes** de trocar a máquina. No boot, vira crash em loop até alguém voltar a imagem anterior (runbook 6.5, passo 13). Com uma máquina, a diferença é pequena, e a spec escolheu o boot |
| D28 | **Postgres: Fly MPG, PostgreSQL 17**. O MPG oferece **16 e 17** (padrão 16, `--pg-major-version`); **não oferece o 18** (conferido na documentação do `fly mpg create`). **Piso de produção: 17.** Compose e testes no 18, CI com matriz 17/18 | Postgres sem gerência do Fly (`fly postgres create`, imagem `postgres-flex`); Neon ou Supabase | Ver D29 sobre o descompasso de versões. **Custo:** o plano Basic do MPG sai por **US$ 38/mês** + US$ 0,28/GB. Para um portfólio, é a maior decisão de dinheiro do projeto (seção 12). As alternativas mais baratas precisam passar pelo mesmo `CheckRole`, e cada provedor tem as próprias regras de papel |
| D29 | **Nenhuma feature exclusiva do 18 nas migrations e queries** enquanto produção for 17 (por exemplo `uuidv7()` nativo, `RETURNING OLD/NEW`, colunas geradas virtuais). A matriz do CI prova: a suíte roda contra `postgres:17-alpine` e `postgres:18-alpine` | Testar só no 18 | Uma migration com `uuidv7()` passa no compose e no CI só-18 e **quebra o boot de produção**. A matriz custa minutos de runner, não relógio |
| D30 | O papel da aplicação no MPG é **`vigilume_app`**, criado com `fly mpg users create --role schema_admin`. Ele é **dono das tabelas** e aplica as migrations no boot. Antes do primeiro deploy, o runbook confere `rolsuper` e `rolbypassrls` à mão | Usar o `fly-user`; separar papel de migração e papel de runtime | O `fly-user` é o admin do cluster, e a aplicação não deve rodar com credencial de admin. Papel de runtime não-dono (`writer`) não funciona: a manutenção faz DDL (`CREATE`/`DROP` de partição) e precisa ser dona. **Incerteza:** a documentação do MPG diz que o Schema Admin é "o mais perto de superusuário", mas não diz se tem `BYPASSRLS`. Se tiver, o boot **recusa** (falha fechada, D8 do Plano 2), e o runbook tem o plano B (6.5, passo 5) |
| D31 | **Conexão direta** (`direct.<cluster>.flympg.net`), não pelo PgBouncer | URL do pooler | Com uma máquina e um pool de 10 conexões, o pooler não traz nada. O `goose` usa advisory lock **de sessão**, que o PgBouncer em modo transação quebra (o MPG vem em modo sessão, mas alguém pode trocar no painel). O `set_config(..., true)` dos escopos de RLS funcionaria nos dois modos |
| D32 | **Não usar `fly mpg attach`.** O `DATABASE_URL` entra à mão por `fly secrets set`, com o usuário `vigilume_app` | `attach` | O `attach` grava um `DATABASE_URL` com credencial que o plano não controla (pode ser a do admin) e reinicia o app |
| D33 | `METRICS_ADDR=off` em produção | Liberar bind em `fly-local-6pn` e configurar `[metrics]` no `fly.toml` | O raspador do Fly entra pela rede privada, e a config de hoje só aceita loopback (D10 do Plano 2). Liberar a 6PN expõe o mapa operacional a toda máquina da organização. O custo de `off`: produção só tem logs. Fica registrado como passo seguinte |
| D34 | Imagem vem do **GHCR por digest** (`--image ghcr.io/…@sha256:…`), a mesma que passou no Trivy e no E2E | `fly deploy` com build remoto no Fly | O build remoto gera uma imagem que nenhum teste viu. **Se o pacote do GHCR ficar privado**, o Fly precisa de credencial para puxar, e a saída é publicar também em `registry.fly.io/<app>` (seção 12) |

### 6.4 O que exige confirmação do usuário

Cada item abaixo é ação externa, na conta do usuário. Nenhum agente o executa sem um "sim"
explícito naquele momento:

1. Criar o app no Fly (`fly apps create`).
2. Criar o cluster MPG. **Tem custo recorrente.**
3. Criar o usuário `vigilume_app` no cluster e instalar o `citext`.
4. Gravar os segredos (`fly secrets set`).
5. O primeiro deploy.
6. Tornar público o pacote do GHCR (ou o repositório).
7. Criar o environment `production` no GitHub, com revisor, e o secret `FLY_API_TOKEN` nele.
8. Cada deploy pelo CI (o revisor aprova no GitHub).
9. Mudar `TRUSTED_PROXIES` depois do teste de dois pontos de rede (6.5, passo 8).

### 6.5 Runbook manual (vira `docs/deploy/fly.md`)

Pré-requisitos: `flyctl` instalado e autenticado **pelo usuário**, `psql` opcional, e a imagem
já publicada no GHCR pelo job `publish`.

1. **App.** `fly apps create vigilume` (sem `fly launch`: ele gera `auto_stop_machines`, D23).
2. **Banco.** `fly mpg create --name vigilume-db --region <região> --plan basic --pg-major-version 17`.
   Escolher a região na lista que o comando oferece e usar a mesma no `primary_region`.
3. **Usuário da aplicação.** `fly mpg users create <cluster> --username vigilume_app --role schema_admin`.
   Guardar a senha num gerenciador.
4. **Banco da aplicação.** Criar o database `vigilume` pelo painel do MPG (o Schema Admin não
   cria database).
5. **Pré-checagem do papel** (o que o boot vai conferir), com `fly mpg connect <cluster>` como
   `vigilume_app` no banco `vigilume`:
   ```sql
   SELECT rolsuper, rolbypassrls FROM pg_roles WHERE rolname = current_user;  -- esperado: f | f
   SELECT has_database_privilege(current_user, current_database(), 'CREATE'); -- esperado: t
   SELECT has_schema_privilege(current_user, 'public', 'CREATE');             -- esperado: t
   ```
   - Se `CREATE` no banco der `f`: como `fly-user`, rodar `CREATE EXTENSION IF NOT EXISTS citext;`
     no banco `vigilume` (a migration 00001 usa `IF NOT EXISTS` e passa adiante).
   - Se `rolbypassrls` der `t`: **parar**. O boot vai recusar. Plano B, em ordem: (a) como
     `fly-user`, `CREATE ROLE vigilume_app2 LOGIN PASSWORD … NOSUPERUSER NOBYPASSRLS` (se o
     `fly-user` tiver `CREATEROLE`) + `GRANT CREATE ON DATABASE vigilume` + `GRANT CREATE ON SCHEMA public`;
     (b) Postgres sem gerência do Fly, onde `fly postgres attach --superuser=false` cria um
     papel comum; (c) outro provedor. Voltar ao usuário antes de escolher.
6. **Segredos**, sem reiniciar nada (`--stage`), gerados na máquina do usuário:
   ```sh
   fly secrets set --stage \
     DATABASE_URL='postgres://vigilume_app:<senha>@direct.<cluster>.flympg.net:5432/vigilume?sslmode=require' \
     MASTER_KEY="$(openssl rand -base64 32)" JWT_SECRET="$(openssl rand -base64 32)" \
     IP_HASH_KEY="$(openssl rand -base64 32)"
   ```
   **Guardar a `MASTER_KEY` fora do Fly.** Perdê-la torna ilegíveis todos os headers de alvo e
   todas as configurações de canal cifrados (spec §5.4), e não há como recuperar.
   E-mail é opcional: `RESEND_API_KEY` e `EMAIL_FROM` juntas, com o domínio do remetente já
   verificado no Resend (senão todo envio volta 403). Sem elas, só o Discord funciona.
7. **Primeiro deploy.** `fly deploy --image ghcr.io/bipoxdxd/vigilume@sha256:<digest do publish>`.
   Esperado no `fly logs`: `iniciando` com a versão, `migrations aplicadas`, `sondador ativo`,
   `servindo`. Com `TRUSTED_PROXIES` ainda vazio, o aviso de "TRUSTED_PROXIES vazio com
   PUBLIC_BASE_URL https" aparece, e é esperado nesta etapa.
8. **IP do cliente (a pendência de sempre).** A documentação do Fly não diz de qual endereço o
   proxy conecta na máquina. Com `CLIENT_IP_HEADER=Fly-Client-IP` já no `[env]`, o app registra
   **um aviso por minuto** quando o header chega de uma origem fora de `TRUSTED_PROXIES`, com o
   `remote_addr` (D22, 9.1). Ler esse endereço no `fly logs`, pôr o menor CIDR que o cobre em
   `TRUSTED_PROXIES` (candidatos esperados: `172.16.0.0/12` ou `fdaa::/16`) e fazer o deploy de
   novo. Depois, o **teste de dois pontos de rede**:
   - Da rede A (casa), 6 logins errados seguidos: os 5 primeiros dão 401, o 6º dá 429.
   - Logo em seguida, da rede B (4G do celular, ou um runner do GitHub): login errado dá **401**,
     não 429. Os baldes estão separados.
   - Da rede A, com `-H 'Fly-Client-IP: 203.0.113.77' -H 'X-Forwarded-For: 203.0.113.77'`:
     continua **429**. O cabeçalho forjado pelo cliente não vale.
   - O aviso do passo 8 some do log.
9. **Usuário real.** Com o banco a partir da máquina do usuário (`fly mpg proxy`), e
   `DATABASE_URL` apontando para o proxy local:
   `vigilume user create --email <e>` lê a senha do stdin, sem eco (contrato, decisão 1).
   Serve o binário compilado na máquina do usuário ou a própria imagem com TTY
   (`docker run --rm -it --network host -e DATABASE_URL=… ghcr.io/…:<tag> user create --email <e>`).
   Sem `-t`, o terminal mostraria a senha.
10. **Conferência pública.** `https://<app>.fly.dev/healthz` e `/readyz` dão 200. Login no painel.
    `curl -sI` mostra `Strict-Transport-Security`, CSP com nonce e `X-Content-Type-Options`.
    Criar a status page e abrir `/p/{slug}` numa aba anônima.
11. **Primeira manutenção** (pendência 6). Num banco novo, a primeira rodada não tem nada a
    consolidar. Ela só pesa num banco importado com 62 dias de checks. Nada a fazer aqui.
12. **CI de deploy.** `fly tokens create deploy -a vigilume` → secret `FLY_API_TOKEN` no
    environment `production` do GitHub, com o usuário como revisor obrigatório.
13. **Voltar uma versão.** `fly releases` → `fly deploy --image ghcr.io/…@sha256:<digest anterior>`.
    Só é seguro se as migrations aplicadas pela versão nova forem aditivas (D26). O `goose` não
    roda `Down` sozinho, e o plano não automatiza isso.

### 6.6 Apêndice: banco que já tem tabelas

Não é o caso hoje (ver a nota no topo). Se um dia o app subir contra um banco cujas tabelas pertencem a outro
papel (por exemplo, criadas pelo `fly-user`), o boot passa pelo `CheckRole` e **falha no
`Migrate`** ou na manutenção. A correção é transferir a posse **antes** do deploy: `ALTER TABLE
… OWNER TO vigilume_app` em toda tabela, **incluindo `goose_db_version`** (a sequência acompanha a
tabela), mais as funções das migrations 00005 e 00006, e as partições de `checks` (detalhes em
`PENDENCIAS-PARA-O-PLANO-3.md`, item 2).

---

## 7. Configuração de produção

| Variável | Onde | Valor em produção | Valor no compose | Observação |
|---|---|---|---|---|
| `ROLE` | `CMD` da imagem | `all` (`--role=all`) | `all` | Sobreposto pela flag |
| `HTTP_ADDR` | padrão | `:8080` | `:8080` | Casa com `internal_port` |
| `METRICS_ADDR` | `[env]` | `off` | `off` | D33 |
| `DATABASE_URL` | **secret** | URL **direta** do MPG, usuário `vigilume_app`, `sslmode=require` | `postgres://vigilume_app:…@db:5432/vigilume?sslmode=disable` | D31, D32. O boot recusa superusuário e `BYPASSRLS` |
| `PUBLIC_BASE_URL` | `[env]` | `https://<app>.fly.dev` | `http://localhost:8080` | Host da API **e** do painel (mesma origem). Base de `/verify/{token}` e do `public_url` da status page |
| `CORS_ORIGIN` | `[env]` | igual a `PUBLIC_BASE_URL` | `http://localhost:5173` | Obrigatória na config. Com o painel na mesma origem, em produção não é usada de fato |
| `MASTER_KEY` | **secret** | 32 bytes aleatórios, base64 | chave de demo (D13) | **Guardar fora do Fly.** O boot recusa a de demo com https (D14) |
| `JWT_SECRET` | **secret** | idem | chave de demo | Distinta das outras duas |
| `IP_HASH_KEY` | **secret** | idem | chave de demo | Distinta das outras duas |
| `TRUSTED_PROXIES` | `[env]` | CIDR do proxy do Fly, **medido** no passo 8 de 6.5 | vazio | Sem ele, o header é ignorado e a falha fica fechada (todos dividem um balde) |
| `CLIENT_IP_HEADER` | `[env]` | `Fly-Client-IP` | vazio | **Nova** (D22). Vazio mantém o comportamento de hoje |
| `LOG_LEVEL` | `[env]` | `info` | `info` | |
| `RESEND_API_KEY` | **secret** | opcional | vazio | Vai junto com `EMAIL_FROM`, ou nenhuma das duas. Domínio verificado no Resend |
| `EMAIL_FROM` | secret ou `[env]` | opcional | vazio | idem |
| `DEMO_PASSWORD`, `DEMO_TARGETS` | só o seed | — | `demo-vigilume-123`, `1` | Nunca em produção |

---

## 8. README (estrutura, não o texto)

O leitor é um avaliador técnico com 2 minutos. O README responde na ordem em que ele pergunta:
"funciona?", "roda aqui?", "o que tem de difícil?", "está testado?".

1. **Título, uma frase e badges** (CI, Go 1.26, licença). Um GIF curto (painel → alvo cai →
   incidente → status page) e o link do deploy, se houver.
2. **Rodar em 30 segundos.** Três linhas: `git clone`, `docker compose up --wait`, abrir
   `http://localhost:8080` com `demo@vigilume.local` e a senha de demo. Uma nota: os alvos de
   exemplo precisam de internet, e um serviço local não pode ser alvo porque o `safedial` recusa
   IP privado, de propósito.
3. **O que faz**, em 5 tópicos (spec §1, §2.1).
4. **Arquitetura** num diagrama Mermaid: binário único com `--role`, Postgres como fila
   (`SKIP LOCKED`), outbox de notificações, status page em Go e painel embutido. Uma linha de
   "por que não Redis ou Kafka" com link para a spec §17.
5. **Decisões de segurança**, uma tabela de 7 a 8 linhas, cada uma com **link para o código e
   para o teste que a prova**: SSRF validada no `ControlContext` (DNS rebinding); RLS com `FORCE`
   e boot que recusa papel com `BYPASSRLS`; access token em memória e refresh em cookie
   `HttpOnly`; AES-GCM com o id da linha como AAD e o tipo `Secret`; status page com DTO
   próprio; IP real atrás do proxy com falha fechada; imagem distroless nonroot com Trivy.
6. **Concorrência:** semáforo do sondador, `errgroup`, shutdown gracioso com prazo, *single-flight*
   do refresh. Links.
7. **Testes:** onde ficam, como rodar (`make test`, que precisa de Docker; `make test-short`, que
   não precisa), Postgres real via testcontainers (**sem mock de banco**), `-race`, matriz 17/18,
   E2E Playwright contra o compose, e a prática de **prova por sabotagem** em uma frase com um
   exemplo real.
8. **Stack** numa tabela curta (spec §3.3 e §11.1).
9. **Desenvolvimento:** `make` targets, `vite dev` com proxy, `sqlc generate`.
10. **Deploy:** um parágrafo e o link para `docs/deploy/fly.md`.
11. **O que ficou de fora e por quê** (spec §2.2), com link para as specs e planos. Mostra
    critério, não esquecimento.
12. **Licença.**

Regras: nada de texto de marketing; cada afirmação de segurança aponta para um arquivo; o
README não repete a spec, aponta para ela.

---

## 9. Testes e verificação

A regra do projeto vale aqui: **toda asserção de ausência tem prova por sabotagem**, registrada
no relatório da tarefa (o teste vacuamente verde apareceu 14 vezes no Plano 1).

### 9.1 Código Go (unitário, `-race`)

| Teste | O que prova | Sabotagem |
|---|---|---|
| `ClientIP` com `CLIENT_IP_HEADER=Fly-Client-IP`, origem confiável → IP do header | Caminho feliz no formato do Fly | Voltar a ler o XFF → vermelho |
| Idem, com XFF no formato do Fly (`cliente, IP-do-app`) e **sem** `CLIENT_IP_HEADER` → IP do app | Documenta o defeito que a D22 corrige, e deixa claro que o padrão continua fechado | — |
| Origem **não** confiável + `Fly-Client-IP` forjado → `RemoteAddr` | Cliente não escolhe o balde | Remover a checagem de `confiavel` → vermelho |
| Header ilegível, vazio, com vírgula ou IPv6 com zona → `RemoteAddr` | Lixo não vira chave de balde nem `ip_hash` | Aceitar sem `ParseAddr` → vermelho |
| Rate limit separa 3 clientes atrás do proxy com `Fly-Client-IP` (como o `TestRateLimitSeparatesClientsBehindATrustedProxy` de hoje) | O dano real da pendência | Usar o `RemoteAddr` → vermelho |
| Aviso de origem não confiável sai **no máximo uma vez por minuto** e **só** com `CLIENT_IP_HEADER` preenchido | Sinal de configuração errada sem inundar o log, e sem ruído em dev | Tirar o limite → o teste conta N avisos |
| `config.Load`: `CLIENT_IP_HEADER` só aceita um nome de header válido | Falha no boot, não em runtime | — |
| `config.Load` recusa as chaves de demo com `PUBLIC_BASE_URL` https e aceita com http (se a D14 for aprovada) | Compose copiado para produção não sobe | Tirar a comparação → vermelho |
| `vigilume healthcheck`: 0 com 200, 1 com 503, 1 com conexão recusada, 1 com servidor que não responde em 2 s | O compose não marca `healthy` um app que não está pronto | Devolver 0 sempre → vermelho |
| `vigilume version` imprime o valor do `-X` | O carimbo chega ao binário | — |

### 9.2 Imagem (job `image`)

- Trivy HIGH/CRITICAL corrigíveis: **0**, senão falha.
- Usuário **não é root**: `docker inspect -f '{{.Config.User}}'` dá `nonroot:nonroot`.
  Sabotagem: tirar o `USER` e trocar a base por `static` (sem `nonroot`) → vermelho.
- **Sem shell:** `docker run --rm --entrypoint /bin/sh <img> -c true` tem de **falhar**.
  Sabotagem: base `debug-nonroot` → o teste fica vermelho.
- **Painel embutido:** o binário contém `assets/index-*.js`. No spike, o `_ = webui.FS` foi
  descartado pelo linker e o painel **não** entrou: um teste que só olhasse o build teria
  passado vazio. Checagem real: `GET /` do contêiner dá o `index.html` do painel.
- **Teto de tamanho:** imagem comprimida < 15 MB (hoje 7,5 MB). Pega uma base errada ou um
  `node_modules` que vazou para dentro.
- `docker run <img> version` imprime o `github.sha`.

### 9.3 Compose e E2E (job `e2e`)

- `docker compose up --wait` com código 0, e o tempo **impresso** no resumo do job. Sem limite
  rígido de 30 s: o runner não é a máquina do avaliador, e a medida serve para ver tendência.
  Falha acima de 90 s.
- `POST /api/v1/auth/login` com o usuário de demo dá 200. O seed funcionou e o Argon2id bate.
- O papel da aplicação dentro do compose: `rolsuper=f`, `rolbypassrls=f`, e as tabelas são dele.
- **Idempotência:** um segundo `docker compose up --wait` sem `down -v` sai com 0 e não duplica
  usuário nem alvos.
- **Persistência no PG18:** `down` (sem `-v`) + `up`, e o usuário continua lá. Sabotagem: montar
  em `/var/lib/postgresql/data` → o usuário some (reproduzido no spike com a montagem certa, 10.2).
- Playwright (os testes do Plano 6): login → cadastrar alvo → ver status (spec §14).

### 9.4 Deploy (manual, no runbook)

Os passos 5, 7, 8 e 10 de 6.5 **são** a verificação de produção. O mais importante é o teste de
dois pontos de rede do passo 8: é o único que prova a D22 no ambiente real.

---

## 10. Resultado dos spikes

Todos **descartáveis**, numa cópia do `HEAD` em `/tmp/claude-1000/plano7-spike/` (fora do
repositório), com um painel Vite + React 19 falso que tem as dependências da spec §11.1
(React Router, TanStack Query, RHF + Zod, Recharts, Radix, Tailwind), uma ferramenta falsa que
imita `user create` (Argon2id + INSERT) e `healthcheck`, e um `internal/webui` com o embed do
contrato. Nada disso entra no repositório. Máquina: 16 núcleos, 31 GB, Docker 29.8.1, Compose
v5.5.1.

### 10.1 Imagem

- Build frio (sem cache do BuildKit e sem imagens-base): **27,2 s**. `npm ci` 5,0 s,
  `go mod download` 4,9 s, `vite build` 2,8 s, `go build` 8,6 s.
- Binário: **17,4 MB** sem painel, **18,2 MB** com o painel de exemplo. A imagem final
  comprimida tem **7,5 MB**.
- Rebuild depois de mudar um `.go`: **2,6 s**.
- Multi-arch amd64 + arm64 por cross-compile, com cache: **13,3 s**; arm64 com 6,9 MB comprimidos.
- Trivy (`aquasec/trivy` em contêiner, `--severity HIGH,CRITICAL --ignore-unfixed`): **0** na
  base Debian 13.7 e **0** no binário Go.
- Dois builds `--no-cache`: **binário idêntico** (mesmo SHA-256). O digest da imagem difere.
- **Armadilha achada:** `_ = webui.FS` em `main` **não** embute o painel (o linker descarta a
  variável). O embed só entrou quando o código **leu** o FS. No Plano 6 o handler lê, então não é
  problema real, mas é por isso que 9.2 confere o painel na imagem, e não o build.
- **Armadilha achada:** `go build` fora de um repositório git, mas com estado de VCS estranho
  acima dele, falhou em "error obtaining VCS status". O `-buildvcs=false` explícito (D3) evita
  isso no contexto do Docker.

### 10.2 Compose

- Ordem seed → app (D10): `up --wait` com **código 0** em **3,4–3,6 s** (PG16) e **3,95 s**
  (PG18), três rodadas cada, volumes novos.
- Ordem app → seed: `up --wait` com **código 1** mesmo com o seed saindo 0. Por isso a D10.
- Seed: 85–200 ms (Argon2id m=64 MiB, t=3).
- Login do usuário semeado: **200**. `/readyz`: 200.
- Dono do banco = `vigilume_app`: 17 tabelas e o `citext` dele. `rolsuper=f`, `rolbypassrls=f`,
  e o boot aceitou (D12).
- PG18: `PGDATA=/var/lib/postgresql/18/docker` e `VOLUME ["/var/lib/postgresql"]` conferidos na
  imagem `postgres:18-alpine` (18.6). Os dados sobreviveram a `down` + `up` com o volume em
  `/var/lib/postgresql`.
- Seed idempotente (`ON CONFLICT DO NOTHING` na ferramenta falsa): o segundo `up` sai 0.

### 10.3 Compose com `image` e `build` juntos

- Com a imagem existente no registry: o Compose **puxou** e **não** construiu.
- Com o pull negado (`ghcr.io/…/nao-existe`): o Compose **construiu** e subiu. Esse é o fallback
  da D17.

### 10.4 IP do cliente no formato do Fly

Com o app do compose, `TRUSTED_PROXIES=172.16.0.0/12,192.168.0.0/16` (a ponte do Docker) e login
errado repetido:

| `X-Forwarded-For` enviado | Resultado de 8 pedidos, cada um com um "cliente" diferente |
|---|---|
| `198.51.100.N, 66.241.124.63` (formato do Fly: IP do app à direita) | `401 401 401 401 401 429 429 429`: **um balde só** |
| `198.51.100.N` (cliente real à direita) | oito 401: baldes separados |

É a prova de que `TRUSTED_PROXIES` sozinho não resolve no Fly (D22). Ressalva: o formato vem da
documentação do Fly, não de um deploy real. O passo 8 do runbook o confirma em produção.

### 10.5 Efeitos colaterais na máquina

O `docker builder prune -af` do build frio apagou **todo** o cache do BuildKit da máquina (ele se
reconstrói sozinho no próximo build), e os pulls de amostra foram apagados em seguida. As imagens
e volumes do spike foram removidos. O contêiner `plano5-spike`, de outro agente, não foi tocado.

---

## 11. Riscos

| Risco | Probabilidade | Mitigação |
|---|---|---|
| O Schema Admin do MPG tem `BYPASSRLS` | Média (a doc não diz) | O boot recusa (falha fechada). A pré-checagem do passo 5 pega **antes** do deploy. Plano B no mesmo passo |
| O endereço de origem do proxy do Fly não está em nenhum CIDR óbvio | Média | O aviso da D22 imprime o `remote_addr` real. O teste de dois pontos de rede fecha a questão |
| O Fly não sobrescreve um `Fly-Client-IP` enviado pelo cliente | Baixa (o header existe para isso) | O terceiro item do teste de dois pontos de rede (6.5, passo 8) prova. Se falhar, voltar ao XFF com "penúltimo salto" |
| Custo do MPG (US$ 38/mês + armazenamento) para um portfólio | Alta | Decisão do usuário (seção 12), com as alternativas e o custo de cada uma |
| Descompasso de versão: compose e testes no 18, produção no 17 | Média | D29 e a matriz do CI |
| Máquina única: um deploy ou uma falha do host para o monitor | Certa | Aceito para o portfólio. O D12 do Plano 2 garante que o deploy não abre incidente falso. Separar os papéis é mudar a flag |
| Container sem shell: `fly ssh console` não serve para depurar | Certa | Por desenho (§5.9). Depuração por log e pelos subcomandos do próprio binário. O runbook avisa |
| Migration quebrada em produção vira crash em loop | Baixa | CI com a matriz e o E2E contra o compose, que migra do zero. Voltar pelo digest anterior (6.5, passo 13) |
| Alvos de exemplo do seed dependem da internet; o E2E pode ficar instável | Média | O E2E do Plano 6 não deve depender do **estado** do alvo externo, só do cadastro e da renderização. O seed tolera falha de rede nos alvos (usuário primeiro, alvos depois) |
| Compose copiado para produção com as chaves de demo | Baixa | D14 (recusa no boot) + comentário + README |
| Action de terceiro comprometida | Baixa | D20 (SHA completo, Dependabot), D21 (permissões mínimas, token no environment) |
| Pacote do GHCR privado: avaliador cai no build (acima de 30 s) e o Fly não puxa | Alta enquanto o repositório for privado | Decisão do usuário. O fallback de build funciona sozinho (10.3) |
| Domínio do Resend não verificado: todo e-mail volta 403 | Média | O runbook trata o e-mail como opcional e diz o pré-requisito |

---

## 12. Decisões para o usuário revisar

O que decidi sozinho e merece um segundo olhar, da mais cara para a mais barata:

1. **Postgres em produção e o dinheiro (D28).** O MPG custa US$ 38/mês no plano mais barato e
   oferece só 16 e 17. Opções: (a) MPG 17, como recomendo, porque é o "gerenciado" da spec §15;
   (b) Postgres sem gerência do Fly, mais barato, e a operação fica com você; (c) um provedor
   externo com plano gratuito, que precisa passar no `CheckRole`. Qualquer uma delas precisa de
   confirmação antes de criar.
2. **Postgres 18 como alvo: confirmado pelo usuário e já integrado** (PR #6, `7f84211`). O
   `testdb` usa `postgres:18-alpine`, e `TEST_POSTGRES_IMAGE` troca a imagem; a suíte passou no 18 e
   no 17. Esta spec mantém o compose no 18, a produção no 17 (o MPG não oferece o 18) e a matriz
   17/18 no CI (D11, D29).
3. **Visibilidade do repositório e do pacote do GHCR (D17, D34).** O repositório está
   **privado**. Sem imagem pública, o avaliador cai no build (~30 s nesta máquina, mais num
   notebook) e o Fly precisa puxar de `registry.fly.io`. Um portfólio normalmente é público.
4. **A correção do IP é código no Plano 7 (D22).** A pendência dizia "configuração". O spike
   mostrou que não basta. A mudança é pequena (config + `ClientIP` + aviso com limite de
   frequência) e fica neste plano porque só existe por causa do deploy.
5. **Recusar as chaves de demo no boot com https (D14).** Adiciona uma regra à config. Sem ela,
   a proteção é só o comentário.
6. **Alvos de exemplo no seed (D15).** Melhoram muito a primeira impressão, mas dependem da
   internet e de um parser de JSON em `sed`. A alternativa é só o usuário, com dashboard vazio.
7. **E2E em todo PR (D19).** Leva o PR de ~3 para ~5 min. Só na `main` volta aos 3 min e move a
   descoberta de quebra para depois do merge.
8. **Deploy só manual, com revisor (D21, 6.4).** Nada de deploy automático no push para `main`,
   nem depois do CI verde.
9. **`METRICS_ADDR=off` em produção (D33).** Produção fica sem Prometheus. A alternativa
   precisa relaxar a regra de loopback da D10 do Plano 2.
10. **Estratégia `rolling`, alguns segundos fora por deploy (D26).** Passar a `bluegreen` exige
    a regra "migration só aditiva" por escrito.
11. **Idioma do README.** A estrutura da seção 8 serve para os dois. Português conversa com o
    resto do repositório; inglês alcança mais vagas. Ou os dois, com o inglês como principal.
12. **Região `gru`.** A confirmar contra a lista de regiões do MPG no momento de criar.

---

## 13. Divergências com o contrato

Nenhuma decisão do contrato muda. Estas são adições ou consequências que precisam de
reconciliação na revisão.

1. **`vigilume user create`: o que o Plano 7 depende, já coberto pela spec do Plano 5** (lida
   depois de ela aparecer no repositório): (a) o subcomando **aplica as migrations** antes de
   criar (`CheckRole` → `Migrate`, D17 do Plano 5), o que viabiliza a ordem da D10; (b) sai com
   **código 3** quando já existe usuário, sem alterar nada, e o seed trata 3 como sucesso (em vez
   do `--if-not-exists` que este rascunho chegou a pedir). Duas amarras que a reconciliação
   precisa manter: o código 3 não pode mudar de sentido, e a senha de demo do compose precisa
   respeitar a política do Plano 5 (mínimo de 15 caracteres). O spike usou uma ferramenta falsa,
   não o CLI real.
2. **"Aceita pipe, para o compose" (decisão 1) exige um shell**, e a imagem final não tem nenhum
   (§5.9). A consequência é o alvo `tools` (D6), que só o compose usa. Não muda o contrato, mas
   quem ler o contrato espera que o pipe "simplesmente funcione".
3. **Subcomandos novos no binário, do Plano 7:** `healthcheck` e `version`. O despacho de
   subcomandos nasce no Plano 5 (`main` olha `os.Args[1]` antes do `flag.Parse` global e desvia
   para uma função que devolve o código de saída). O Plano 7 acrescenta os dois no mesmo
   despacho e na mesma forma testável.
4. **O Plano 7 mexe em código Go de produto** (`CLIENT_IP_HEADER`, D22; e, se aprovada, a D14).
   O contrato descreve o Plano 7 como empacotamento. As duas mudanças só existem por causa do
   deploy, e por isso ficam aqui.
5. **Postgres 18: resolvido.** Confirmado pelo usuário e integrado no PR #6: `testdb` lê
   `TEST_POSTGRES_IMAGE` (padrão `postgres:18-alpine`), e a spec §3.3 diz "18 em desenvolvimento e
   testes, 17 em produção". Falta só a matriz 17/18 do CI, que é deste plano.
6. **`.gitignore`:** hoje ignora `/web/dist/`, que deixa de existir. Com o contrato, o que se
   ignora é `internal/webui/dist/*`, com exceção do `.gitkeep`. É provável que o Plano 6 faça
   isso. O Plano 7 confere, porque o `.dockerignore` (D7) espelha a mesma regra.
7. **E2E:** os testes Playwright são do Plano 6, e o Plano 7 os roda contra o compose (D18).
   Duas restrições que o Plano 6 precisa saber: (a) a URL base é `http://localhost:8080`, com o
   usuário `demo@vigilume.local` já criado pelo seed; (b) um alvo cadastrado no teste precisa
   ser **público**, porque o `safedial` recusa endereço privado, e o teste não deve depender do
   estado dele (up ou down), só de que aparece.
