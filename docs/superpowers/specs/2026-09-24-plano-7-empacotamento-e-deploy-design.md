# Vigilume — Plano 7: Empacotamento, Compose, CI, Deploy e README (design)

- **Data:** 2026-09-24 (revisada no mesmo dia com as decisões do usuário)
- **Status:** aprovada pelo usuário; revisão com as decisões de 2026-09-24 (contrato, "Reconciliação", item 9)
- **Contrato:** `docs/superpowers/specs/2026-09-24-planos-5-6-7-contrato.md` (inclusive a seção "Reconciliação")
- **Spec base:** `docs/superpowers/specs/2026-09-22-vigilume-design.md` (§1.1, §3.1, §5.7, §5.9, §13, §14, §15)
- **Pendências absorvidas:** `PENDENCIAS-PARA-O-PLANO-5.md`, seção "Já valendo em produção" (itens 1 a 6), e
  os detalhes do papel do Postgres em `PENDENCIAS-PARA-O-PLANO-3.md` (item 2)

> **Decisões do usuário nesta revisão.** (1) Postgres de produção **sem gerência** no Fly, e não o
> Managed Postgres; (2) repositório e imagem do GHCR **públicos**, com varredura de segredos no
> histórico antes; (3) README **em português**; (4) **sem blocklist** de senhas, só o mínimo de 15.
> O Postgres 18 em desenvolvimento e testes já está integrado (PR #6, `TEST_POSTGRES_IMAGE`).

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
> deploy. O runbook da seção 6 parte de um banco vazio. O apêndice 6.9 cobre o caso de já haver
> tabelas com outro dono.

---

## 1. Objetivo

Fazer o Vigilume **rodar em qualquer máquina com um comando só**, **passar por um pipeline que
dá para mostrar a alguém** e **poder ir para o Fly.io com um runbook que não falha no primeiro
boot**. O plano também fecha as pendências de deploy herdadas do Plano 1 ao 4: IP real do
cliente, papel do Postgres sem `BYPASSRLS`, métricas, e-mail, `PUBLIC_BASE_URL` e primeira
manutenção. Com o Postgres sem gerência, também entrega o que o gerenciado daria de graça:
**backup com restauração testada, upgrade e vigilância de disco**.

Critérios de pronto, em ordem de prioridade:

1. `git clone … && docker compose up --wait` deixa o painel aberto em `http://localhost:8080`,
   com um usuário de demonstração pronto para login, dentro de **30 s** numa máquina e numa rede
   razoáveis (spec §15, requisito não negociável). Medido em 10.2.
2. A imagem é multi-stage, termina em `distroless/static:nonroot` e passa no Trivy sem achado
   HIGH ou CRITICAL corrigível.
3. O CI roda, em PR, tudo o que a spec §15 lista, mais o E2E do Playwright contra o **mesmo
   compose** que o avaliador usa e a varredura de segredos, em ~5 min de relógio.
4. `fly.toml` do app e do banco, e um runbook manual. O deploy real acontece **só com
   confirmação do usuário** (contrato, decisão 6).
5. Backup diário do Postgres, **restaurado a cada execução** num banco descartável, e cópia
   cifrada guardada fora do Fly.
6. Um README em português que um avaliador de vaga backend/fullstack Go entende em 2 minutos
   (spec §1.1).

## 2. Escopo e fora de escopo

### 2.1 Dentro

| Item | Onde |
|---|---|
| `Dockerfile` multi-stage (web → build → runtime) e o alvo `tools` para o seed | raiz |
| `.dockerignore` | raiz |
| `compose.yaml` e o SQL de init do Postgres | raiz, `deploy/compose/` |
| Subcomandos `vigilume healthcheck` e `vigilume version`, e `var version` com `-X` | `cmd/vigilume` |
| **Correção do IP do cliente atrás do Fly** (`CLIENT_IP_HEADER`, D22) | `internal/config`, `internal/httpapi/middleware/clientip.go` |
| Matriz Postgres 17/18 no CI com `TEST_POSTGRES_IMAGE` (contrato, Reconciliação 7) | CI |
| CI estendido: web, imagem, Trivy, E2E, gitleaks, publicação no GHCR, deploy manual | `.github/workflows/` |
| Workflow de backup diário com teste de restauração e checagem de disco (D32, D35) | `.github/workflows/backup.yml`, `deploy/backup/` |
| `dependabot.yml` (spec §5.9 manda, e ainda não existe) | `.github/` |
| `fly.toml` do app e `deploy/fly/db.toml` do Postgres | raiz, `deploy/fly/` |
| Runbook de deploy, de papel do Postgres, de backup/restauração, de upgrade e de publicação | `docs/deploy/fly.md` |
| `.gitleaksignore` com os falsos positivos conferidos (10.7) | raiz |
| README em português | raiz |
| Alvos novos no `Makefile` (`image`, `up`, `down`, `e2e`) | `Makefile` |

### 2.2 Fora

| Item | Motivo |
|---|---|
| Executar o deploy, criar app, volume, banco, papel ou segredo no Fly | Ação externa na conta do usuário (contrato, decisão 6). O plano entrega os artefatos e o runbook |
| Tornar o repositório e o pacote do GHCR públicos | **Ação manual do usuário**, depois da varredura de segredos (6.7) |
| Domínio próprio, TLS próprio | Fora do escopo da spec (§2.2); `*.fly.dev` basta |
| Várias máquinas, `--role=api` e `--role=prober` separados em produção | O desenho aguenta (spec §3.1), mas custa o dobro e não soma sinal ao portfólio agora |
| Postgres com réplica e failover | Um nó só; queda do host = restauração do snapshot (6.8). Aceito para o portfólio |
| Recuperação a um instante qualquer (PITR, arquivamento de WAL) | O backup lógico diário + snapshots diários bastam para o volume de dados do projeto (D32) |
| Raspagem de métricas pelo Fly | Exige relaxar a D10 do Plano 2 (D33) |
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
| D3 | `-trimpath -buildvcs=false -ldflags "-s -w -X main.version=…"`. `VERSION` vem de `git describe --tags --always --dirty` no Makefile e do `github.sha` (ou da tag) no CI | Carimbo VCS automático (`debug.ReadBuildInfo`) | O carimbo automático exige o `.git` dentro do contexto do build, o que engorda o contexto e embute o estado da árvore. O `-s -w` tira DWARF e tabela de símbolos, mas stack trace de panic continua legível (o `pclntab` fica) e o Trivy continua lendo as dependências do binário (conferido: `gobinary` escaneado) |
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
db    postgres:18-alpine@sha256:…   volume em /var/lib/postgresql (layout do 18, ver D11)
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

O `initdb.sql` do compose é o **mesmo SQL** que o runbook roda no Postgres de produção (6.5,
passo 4). Desenvolvimento, testes e produção usam a mesma imagem oficial `postgres:18-alpine`,
com o mesmo papel (D29).

### 4.2 Decisões

| # | Decisão | Alternativa | Custo se errada |
|---|---|---|---|
| D10 | Ordem **db → seed → app**. O seed aplica as migrations (o `user create` do Plano 5 faz `CheckRole` → `Migrate` antes de tudo, D17 dele) e cria o usuário; o app só sobe depois de o seed sair com 0 | Seed depois do app `healthy` (a ordem óbvia) | Medido: com o seed por último, `docker compose up --wait` devolve **código 1** mesmo com o seed saindo 0, porque o `--wait` trata como falha o contêiner que terminou. Com o seed como dependência `service_completed_successfully`, devolve 0 (10.2). O custo é o seed precisar migrar |
| D11 | **Postgres 18** no compose (`postgres:18-alpine`), com o volume em **`/var/lib/postgresql`**, não em `…/data` | `postgres:16-alpine` | Conferido na documentação da imagem oficial: a partir do 18, `PGDATA=/var/lib/postgresql/18/docker` e o `VOLUME` declarado é `/var/lib/postgresql`. Montar em `…/data` no 18 grava num volume anônimo, e os dados somem ao recriar o contêiner. Medido: com a montagem nova, os dados sobreviveram a um `down` + `up` (10.2) |
| D12 | Papel da aplicação no compose igual ao de produção: `vigilume_app` com `NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE`, e **`CREATE DATABASE vigilume OWNER vigilume_app`** | `GRANT CREATE ON DATABASE` + `GRANT CREATE ON SCHEMA public`, como o testdb | Desde o PG15 o schema `public` pertence a `pg_database_owner`, então quem é dono do banco é dono do `public` e dispensa os dois GRANTs. Conferido: as 17 tabelas e o `citext` ficaram com `vigilume_app` como dono, e o boot aceitou o papel (`rolsuper=f`, `rolbypassrls=f`). O superusuário `postgres` só existe para o init |
| D13 | **Chaves de demonstração fixas** no `compose.yaml` (`MASTER_KEY`, `JWT_SECRET`, `IP_HASH_KEY`), base64 de um texto que diz "demo, não use em produção", com um comentário gritando isso | Gerar chaves no primeiro `up` | Gerar exige mais um contêiner com shell e um volume compartilhado, e o segundo `up` precisa reler as mesmas chaves (senão os segredos cifrados ficam ilegíveis). O risco da fixa é alguém copiar o compose para produção: ver D14 |
| D14 | O boot **recusa** as três chaves de demonstração quando `PUBLIC_BASE_URL` é `https://` | Só o comentário | Sem a recusa, um deploy copiado do compose sobe com chaves públicas, e qualquer um forja JWT e decifra os segredos. São ~10 linhas em `config.Load`, com teste. As chaves ficam públicas **de propósito** num repositório público: é por isso que a recusa importa. **Para o usuário revisar** (seção 12) |
| D15 | Seed cria o usuário `demo@vigilume.local` (senha de demo com **≥ 15 caracteres**, a política do Plano 5, sem blocklist) e, com `DEMO_TARGETS=1` (padrão no compose), **três alvos públicos** pela própria API (login + `POST /targets` com `wget` do busybox). Os alvos só são criados quando o `user create` devolveu **0**; com **3** (usuário já existe), o seed sai 0 sem tocar em nada, e é isso que torna o segundo `up` idempotente | Só o usuário; um subcomando `target create` | Um dashboard vazio na primeira tela vende mal o projeto. Os alvos **precisam** ser públicos: um serviço falso dentro do compose tem IP privado e o `safedial` o recusa, **por desenho** (spec §5.1). Vale uma linha no README. Sem internet, os alvos ficam `down`, e isso também é demonstração. O parser de JSON em `sed` é frágil; o E2E do CI o cobre |
| D16 | `PUBLIC_BASE_URL=http://localhost:8080`, `CORS_ORIGIN=http://localhost:5173` (o `vite dev` do Plano 6), `METRICS_ADDR=off`, porta só em `127.0.0.1` | Porta em `0.0.0.0` | Com a porta aberta em todas as interfaces, uma rede de café veria o painel de demo com senha conhecida. Com http, o cookie sai sem `Secure` (lógica existente em `routes.go`), e é o que o desenvolvimento local precisa |
| D17 | O avaliador tem **dois caminhos**: pull da imagem publicada (padrão, rápido) ou build do código (fallback automático) | Só build | O build frio levou 27 s nesta máquina de 16 núcleos. Num notebook comum passa de 30 s com folga. Só o caminho do pull cumpre a §15. **Decidido:** o pacote do GHCR é público |

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
                ├── web (typecheck, lint, vitest, build) ───┤
PR e main ──────┼── secrets (gitleaks, histórico inteiro) ──┼──► ok
                └── image (build amd64, Trivy) ──► e2e ─────┘
push em main, tag v* (depois do verde) ──► publish (multi-arch, GHCR, digest)
workflow_dispatch + environment "production" ──► deploy (flyctl deploy --image …@digest)
schedule diário + workflow_dispatch, environment "backup" ──► backup (dump, restauração, disco)
```

| Job | Passos | Tempo estimado |
|---|---|---|
| `go` | O de hoje (`sqlc diff`, `vet`, `staticcheck`, `gosec`, `govulncheck`, `test -race`), com as **ferramentas em versão fixa** e `TEST_POSTGRES_IMAGE` da matriz (`postgres:17-alpine`, `postgres:18-alpine`) | ~3 min (a última execução real: 108 s de teste, 186 s no total) |
| `web` | `npm ci`, `npm run typecheck`, `npm run lint`, `vitest run`, `npm run build` | ~1–1,5 min |
| `secrets` | `gitleaks git` no **histórico inteiro** (`fetch-depth: 0`), binário ou contêiner fixado por versão/digest, com o `.gitleaksignore` | < 30 s (96 commits em 0,9 s localmente) |
| `image` | `docker/build-push-action` com `load: true`, só amd64, cache `gha`; Trivy (`--severity HIGH,CRITICAL --ignore-unfixed --exit-code 1`); checagens da imagem (9.2) | ~1,5–2 min |
| `e2e` | Recebe a imagem do `image` (artifact `docker save`), `docker compose up --wait` com `pull_policy: never`, imprime o tempo, `npx playwright install --with-deps chromium`, `npx playwright test` com `E2E_BASE_URL`, `E2E_EMAIL` e `E2E_PASSWORD` apontando para o compose (contrato, Reconciliação 4), relatório como artifact em falha | ~2–3 min |
| `publish` | Em `push` para `main`: build multi-arch, push para `ghcr.io/bipoxdxd/vigilume:{sha,latest}` e `:{sha}-tools`. Em tag `v*`: também `:{versão}` e `:{versão}-tools`. O digest sai como saída do job | ~2 min |
| `deploy` | Só `workflow_dispatch`, `environment: production` com revisor obrigatório: `flyctl deploy --image ghcr.io/…@sha256:… --strategy rolling` | ~1–2 min |
| `backup` | Workflow próprio (`backup.yml`), diário: proxy para o banco, `pg_dump`, **restauração num Postgres descartável com checagens**, checagem de disco, cifra com `age`, artifact de 30 dias (D32, D35) | ~2 min |

Relógio de um PR: **~5 min** (o caminho mais longo é `image` → `e2e`, rodando em paralelo com
`go`). Hoje são ~3 min. Com o repositório público, os minutos de Actions deixam de ser cobrados.

### 5.2 Decisões

| # | Decisão | Alternativa | Custo se errada |
|---|---|---|---|
| D18 | O **E2E roda contra o `compose.yaml` do avaliador**, com a imagem que acabou de passar no Trivy | Servidor Go + Vite num `webServer` do Playwright | Um E2E feito por fora poderia ficar verde com o compose quebrado. Assim, **o requisito dos 30 s vira teste de regressão**: se o seed, o healthcheck, o init do Postgres ou o painel embutido quebrarem, o PR fica vermelho |
| D19 | E2E **em todo PR**, só no Chromium | Só na `main`; os três navegadores | Na `main`, um E2E vermelho aparece depois do merge. Três navegadores triplicam o tempo, e o fluxo crítico (spec §14) não tem nada específico de navegador |
| D20 | **Actions fixadas por SHA completo** (a tag vem no comentário) e **ferramentas Go com versão** (`staticcheck@<v>`, `gosec@<v>`, `govulncheck@<v>`), mais Dependabot para `github-actions`, `gomod`, `npm` (em `/web`) e `docker` (inclusive o digest do Postgres em `deploy/fly/db.toml` e no compose) | `@v5`, `@latest` | Com tag móvel, um push forçado na tag de uma action roda código alheio com o `GITHUB_TOKEN` (o caso `tj-actions/changed-files`, de 2025). `@latest` faz o CI de hoje mudar de resultado sem commit nenhum. A §5.9 já pede o Dependabot |
| D21 | `permissions: contents: read` no workflow inteiro. `packages: write` só no `publish`. Tokens do Fly **por app** (`fly tokens create deploy -a <app>`), cada um no seu **environment** (`production` para o deploy, `backup` para o backup), não no repositório. `concurrency` cancela o PR anterior do mesmo branch; deploy e backup nunca são cancelados | Token pessoal do Fly nos secrets do repositório | Um token de organização vazado mexe em tudo o que o usuário tem no Fly; o de app só alcança aquele app. No environment, um PR de fork nem enxerga o token. Com o repositório público, isso deixa de ser zelo e vira necessidade |
| D37 | **Varredura de segredos (gitleaks) em todo PR**, sobre o **histórico inteiro**, com falsos positivos conferidos à mão e listados por *fingerprint* no `.gitleaksignore` | Só antes de publicar; `gitleaks-action` | Um segredo commitado depois da publicação fica público na hora. A varredura só antes de abrir o repositório pega o passado, não o futuro. O binário fixado evita depender de uma action de terceiro com licença e permissões próprias. Custo: menos de 30 s por PR |

---

## 6. Deploy no Fly.io

### 6.1 Topologia

Dois apps no Fly, na mesma organização e na mesma região:

| App | O que roda | Máquina | Rede |
|---|---|---|---|
| `vigilume` | O binário, `--role=all`: API, painel, sondador, dispatcher e manutenção no mesmo processo (spec §3.1) | `shared-cpu-1x`, 512 MB | Serviço HTTP público pelo proxy do Fly |
| `vigilume-db` | Imagem **oficial** `postgres:18-alpine` (fixada por digest), volume `pgdata` em `/var/lib/postgresql` | `shared-cpu-1x`, 512 MB, volume de 3 GB | **Sem serviço público**; só a rede privada (6PN), em `vigilume-db.internal:5432` |

O Postgres **não tem gerência do Fly**: backup, upgrade, disco e recuperação ficam com o
operador (6.8). É uma decisão consciente do usuário, pelo custo (D28).

**Custo aproximado** (tabela de preços do Fly consultada hoje): máquina `shared-cpu-1x` de
256 MB = US$ 2,47/mês e de 1 GB = US$ 7,42/mês, então cada uma de 512 MB fica em torno de
**US$ 4–5**; volume a US$ 0,15/GB/mês (3 GB = US$ 0,45); snapshots de volume a US$ 0,08/GB/mês
com os **primeiros 10 GB grátis** (este banco cabe na faixa grátis). Total na casa de
**US$ 9–11/mês**, contra US$ 38 + armazenamento só do banco no Managed Postgres. Conferir a
página de preços no dia de criar.

### 6.2 Configuração (trechos)

`fly.toml` (app):

```toml
app = "vigilume"                 # nome a confirmar
primary_region = "gru"           # a confirmar em `fly platform regions`; a mesma do banco
kill_signal = "SIGTERM"
kill_timeout = "25s"

[env]
  PUBLIC_BASE_URL  = "https://vigilume.fly.dev"
  CORS_ORIGIN      = "https://vigilume.fly.dev"
  CLIENT_IP_HEADER = "Fly-Client-IP"
  TRUSTED_PROXIES  = "…"          # ver 6.5, passo 9
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

`deploy/fly/db.toml` (Postgres):

```toml
app = "vigilume-db"
primary_region = "gru"
kill_signal = "SIGINT"           # "fast shutdown" do Postgres; é o STOPSIGNAL da imagem oficial
kill_timeout = "60s"

[build]
  image = "postgres:18-alpine@sha256:…"   # o Dependabot atualiza (D20)

[mounts]
  source = "pgdata"
  destination = "/var/lib/postgresql"    # layout do 18 (D11)

[[vm]]
  size = "shared-cpu-1x"
  memory = "512mb"

# Sem [http_service] e sem [[services]]: nada do banco passa pelo proxy público.
# POSTGRES_PASSWORD (superusuário) é secret do app vigilume-db.
```

### 6.3 Decisões

| # | Decisão | Alternativa | Custo se errada |
|---|---|---|---|
| D22 | **IP do cliente pelo `Fly-Client-IP`**, com `CLIENT_IP_HEADER` novo na config: vazio (padrão) mantém o comportamento de hoje (último salto do XFF); preenchido, o `ClientIP` lê **esse header, e só ele**, quando a conexão vem de `TRUSTED_PROXIES`. Header ausente ou ilegível cai no `RemoteAddr`. Com `CLIENT_IP_HEADER` preenchido, o header chegando de origem **fora** de `TRUSTED_PROXIES` gera um aviso no log com o `remote_addr`, no máximo um por minuto: é o sinal de configuração errada, e é por ele que se descobre o CIDR do proxy (6.5, passo 9) | Pegar o **penúltimo** salto do XFF; `TRUSTED_HOPS=N` | A documentação do Fly diz que o último elemento do XFF é o IP do app e recomenda o `Fly-Client-IP` quando não há outro proxy na frente. "Penúltimo" depende de um detalhe que o Fly não promete. O header dedicado tem um valor só, que o proxy escreve. Os três limitadores (API, `/verify/`, `/p/`) e o `ip_hash` da auditoria passam pelo mesmo `ClientIP` e são corrigidos juntos |
| D23 | `auto_stop_machines = "off"` e `min_machines_running = 1` | O padrão do `fly launch` (`"stop"` e 0) | **Crítico.** Com o padrão, o Fly desliga a máquina sem tráfego, e **o monitor para de sondar em silêncio**: nenhum incidente abre, nenhuma notificação sai. O `fly launch` gera o padrão, e por isso o `fly.toml` é escrito à mão e o runbook usa `fly apps create`, não `fly launch`. O banco não tem serviço, e o proxy nunca o desliga |
| D24 | `kill_timeout = "25s"`, `kill_signal = "SIGTERM"` explícito no app; no banco, `SIGINT` e 60 s | Os padrões | O padrão do Fly é **5 s**, e o `ShutdownGrace` do app é 15 s (spec §6.2): o SIGKILL chegaria no meio do shutdown gracioso. A documentação do Fly se contradiz sobre o sinal padrão (SIGINT numa página, SIGTERM noutra). O app trata os dois, mas explícito é melhor. No Postgres, SIGKILL no meio do checkpoint força recuperação por WAL no boot seguinte |
| D25 | Checagem de serviço em **`/healthz`**, não em `/readyz` | `/readyz` | O servidor HTTP só escuta **depois** de `CheckRole` e `Migrate`, então um `/healthz` 200 já prova boot completo. Com `/readyz`, um soluço do banco tira a máquina única da rota, e a status page vira o erro genérico do proxy do Fly em vez do 503 em HTML que ela mesma sabe dar (D21 do Plano 4) |
| D26 | Estratégia **`rolling`** (padrão). Com uma máquina, cada deploy fica alguns segundos fora | `bluegreen` | O desenho aguenta `bluegreen`: dois sondadores juntos são seguros (`SKIP LOCKED`), e as migrations se serializam (advisory lock). Mas o código velho rodaria alguns segundos contra o schema novo, e toda migration teria de ser compatível para trás. Fica como passo seguinte, junto com a regra "migration só aditiva" |
| D27 | **Migrations no boot** (spec §15), sem `release_command` | `release_command = "/vigilume migrate"` | Com `release_command`, uma migration quebrada aborta o deploy **antes** de trocar a máquina. No boot, vira crash em loop até alguém voltar a imagem anterior (runbook 6.5, passo 14). Com uma máquina, a diferença é pequena, e a spec escolheu o boot |
| D28 | **Postgres sem gerência: um app próprio com a imagem oficial `postgres:18-alpine` e um volume** | (a) `fly postgres create` com a imagem `flyio/postgres-flex` (o "Fly Postgres" antigo); (b) Managed Postgres | Evidência: `flyio/postgres-flex` tem as tags `18` e `18.1` (publicadas em 2026-08-31, conferido no Docker Hub), e o `fly postgres create --image-ref` a aceitaria, com o açúcar do `flyctl` (`fly pg connect`, `--enable-backups` com WAL no Tigris). Mas o 18.1 está **cinco versões menores atrás** da oficial (a `postgres:18-alpine` local é 18.6), roda processos a mais (repmgr, agente) na mesma memória, e o próprio `flyctl` avisa que o Fly Postgres sem gerência "não tem suporte do Fly". A imagem oficial tem os patches em dia, é **a mesma** do compose e dos testes, e o SQL do papel é o mesmo do compose. O custo: backup, conexão administrativa e upgrade saem do `flyctl` e vão para o runbook (6.8). O MPG saiu por custo (decisão do usuário) |
| D29 | **Postgres 18 em produção também**, com a **mesma imagem** (tag e digest) de desenvolvimento e testes. A matriz 17/18 do CI (contrato, Reconciliação 7) continua | Produção no 17 | A imagem oficial do 18 existe e é a que o repositório já testa (PR #6). Com o MPG fora, o motivo para o 17 em produção sumiu. A perna do 17 na matriz vira seguro barato: mantém o código compatível com um 17, caso seja preciso restaurar num Postgres gerenciado que não tenha o 18. Ver a seção 12 sobre removê-la |
| D30 | **Papel da aplicação criado à mão pelo superusuário**, com o **mesmo SQL** do compose: `CREATE ROLE vigilume_app LOGIN PASSWORD … NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE` e `CREATE DATABASE vigilume OWNER vigilume_app`. O `vigilume_app` é **dono das tabelas** e aplica as migrations no boot | Rodar o app como `postgres`; separar papel de migração e de runtime | Como `postgres`, o boot recusa (D8 do Plano 2), e com razão: superusuário atravessa a RLS. Papel de runtime não-dono não funciona: a manutenção faz DDL (`CREATE`/`DROP` de partição). Com a imagem oficial, o superusuário é nosso, e a incerteza do "Schema Admin" do MPG desaparece |
| D31 | Conexão pela **rede privada**: `vigilume-db.internal:5432`, `sslmode=disable`. O banco **não** tem serviço público nem Flycast | TLS no Postgres com certificado próprio; expor por Flycast | A 6PN é a malha WireGuard do Fly: o tráfego entre hosts já vai cifrado. Pôr TLS no Postgres exige gerar e rodar certificado a mais para cifrar de novo o que já está cifrado. Sem serviço, não há porta do banco na internet. O custo, se um dia o banco sair do Fly, é voltar a `sslmode=require` |
| D32 | **Backup em duas camadas.** (1) **Snapshots diários do volume** (padrão do Fly), retenção ajustada para **14 dias**: é a volta rápida de uma queda de host. (2) **Dump lógico diário** num workflow agendado do GitHub Actions: `fly proxy` para o banco, `pg_dump -Fc` com o papel **`vigilume_backup`** (`NOSUPERUSER BYPASSRLS` + `pg_read_all_data`), **restauração a cada execução** num `postgres:18-alpine` descartável com checagens, e cópia **cifrada com `age`** (chave pública no repositório, privada só com o usuário) guardada como artifact por 30 dias | Só snapshots; `pg_dump` numa máquina agendada do Fly para um bucket Tigris; WAL contínuo (PITR) | O snapshot é do mesmo provedor e é "consistente como depois de um crash": sozinho, um erro de conta ou do Fly leva tudo junto. O dump sai do Fly, prova que restaura **todo dia**, e custa zero num repositório público. O papel de backup precisa de `BYPASSRLS`: medido no spike, o `pg_dump` como dono **falha** por causa do `FORCE ROW LEVEL SECURITY`, e com `vigilume_backup` funciona (10.6). O boot recusa esse papel, e o `DATABASE_URL` nunca pode apontar para ele (falha fechada). Com o repositório público, o artifact é baixável por qualquer um, e por isso a cifra é obrigatória. PITR fica de fora (2.2): perde-se até um dia de checks no pior caso |
| D33 | `METRICS_ADDR=off` em produção | Liberar bind em `fly-local-6pn` e configurar `[metrics]` no `fly.toml` | O raspador do Fly entra pela rede privada, e a config de hoje só aceita loopback (D10 do Plano 2). Liberar a 6PN expõe o mapa operacional a toda máquina da organização. O custo de `off`: produção só tem logs. Fica registrado como passo seguinte |
| D34 | Imagem vem do **GHCR por digest** (`--image ghcr.io/…@sha256:…`), a mesma que passou no Trivy e no E2E. O pacote é **público** (decisão do usuário), e o Fly puxa sem credencial | `fly deploy` com build remoto no Fly; `registry.fly.io` | O build remoto gera uma imagem que nenhum teste viu |
| D35 | **Disco vigiado pelo workflow de backup**: a cada execução, `pg_database_size` e o uso do volume comparados com o tamanho dele. Acima de 70%, aviso no resumo do job; acima de 85%, o job **falha** (e o GitHub manda e-mail ao usuário). O volume cresce com `fly volumes extend`, com o banco no ar | Só o painel de métricas do Fly | Painel ninguém olha. Um e-mail de job vermelho chega. A estimativa: 10 alvos a cada 60 s dão ~430 mil checks em 30 dias, ~100 MB com índices; 3 GB sobram por muito tempo. É o descuido que derruba um banco sem gerência |
| D36 | **Upgrades do Postgres:** versão menor (18.x) pelo Dependabot, que muda o digest em `deploy/fly/db.toml`; o usuário aplica com `fly deploy -c deploy/fly/db.toml` (10–30 s fora). Versão maior (18 → 19) por **dump e restauração num app novo** (`vigilume-db19`), trocando o `DATABASE_URL` e mantendo o antigo 7 dias | `pg_upgrade --link` no mesmo volume | O layout novo do 18 foi feito para facilitar o `pg_upgrade --link`, mas ele exige uma imagem com os dois binários e não tem volta depois de iniciar o banco novo. Com um banco de centenas de MB, dump e restauração levam segundos, reusam o caminho que o backup já testa todo dia e deixam o banco antigo intacto para voltar |

### 6.4 O que exige confirmação do usuário

Cada item abaixo é ação externa, na conta do usuário. Nenhum agente o executa sem um "sim"
explícito naquele momento:

1. Varredura de segredos do histórico e o que fazer com cada achado (6.7).
2. **Tornar o repositório e o pacote do GHCR públicos** (6.7). Ação manual do usuário.
3. Criar os apps `vigilume` e `vigilume-db` no Fly, o volume e o segredo do superusuário. **Tem
   custo recorrente** (6.1).
4. Criar os papéis `vigilume_app` e `vigilume_backup` e o banco `vigilume`.
5. Gravar os segredos do app (`fly secrets set`).
6. O primeiro deploy do banco e o primeiro deploy do app.
7. Criar os environments `production` e `backup` no GitHub, com os tokens de app e a chave
   pública do `age`.
8. Cada deploy pelo CI (o revisor aprova no GitHub).
9. Mudar `TRUSTED_PROXIES` depois do teste de dois pontos de rede (6.5, passo 9).
10. Upgrades do Postgres e restaurações (6.8).

### 6.5 Runbook de deploy (vira `docs/deploy/fly.md`)

Pré-requisitos: `flyctl` instalado e autenticado **pelo usuário**; repositório e pacote do GHCR
já públicos (6.7); a imagem publicada pelo job `publish`; `age` instalado para gerar a chave de
backup.

1. **Apps.** `fly apps create vigilume` e `fly apps create vigilume-db` (sem `fly launch`: ele
   gera `auto_stop_machines`, D23).
2. **Volume.** `fly volumes create pgdata -a vigilume-db --region <região> --size 3 --snapshot-retention 14`.
3. **Banco no ar.** `fly secrets set -a vigilume-db --stage POSTGRES_PASSWORD="$(openssl rand -base64 32)"`
   (guardar num gerenciador de senhas) e `fly deploy -c deploy/fly/db.toml`. Esperado no
   `fly logs -a vigilume-db`: `database system is ready to accept connections`.
4. **Papéis e banco.** `fly ssh console -a vigilume-db` (a imagem oficial tem shell) e, em
   `psql -U postgres`, o **mesmo** SQL de `deploy/compose/initdb.sql`, com senhas novas:
   ```sql
   CREATE ROLE vigilume_app LOGIN PASSWORD '…' NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
   CREATE DATABASE vigilume OWNER vigilume_app;
   CREATE ROLE vigilume_backup LOGIN PASSWORD '…' NOSUPERUSER BYPASSRLS NOCREATEDB NOCREATEROLE;
   GRANT pg_read_all_data TO vigilume_backup;
   ```
5. **Pré-checagem do papel** (o que o boot vai conferir), no mesmo console:
   ```sql
   SET ROLE vigilume_app;
   SELECT rolsuper, rolbypassrls FROM pg_roles WHERE rolname = current_user;   -- f | f
   \c vigilume
   SELECT has_schema_privilege('vigilume_app', 'public', 'CREATE');             -- t
   ```
6. **Segredos do app**, sem reiniciar nada (`--stage`), gerados na máquina do usuário:
   ```sh
   fly secrets set -a vigilume --stage \
     DATABASE_URL='postgres://vigilume_app:<senha>@vigilume-db.internal:5432/vigilume?sslmode=disable' \
     MASTER_KEY="$(openssl rand -base64 32)" JWT_SECRET="$(openssl rand -base64 32)" \
     IP_HASH_KEY="$(openssl rand -base64 32)"
   ```
   **Guardar a `MASTER_KEY` fora do Fly.** Perdê-la torna ilegíveis todos os headers de alvo e
   todas as configurações de canal cifrados (spec §5.4), e **nenhum backup devolve isso**: o
   dump guarda o texto cifrado, não a chave.
   E-mail é opcional: `RESEND_API_KEY` e `EMAIL_FROM` juntas, com o domínio do remetente já
   verificado no Resend (senão todo envio volta 403). Sem elas, só o Discord funciona.
7. **Primeiro deploy do app.** `fly deploy --image ghcr.io/bipoxdxd/vigilume@sha256:<digest do publish>`.
   Esperado no `fly logs`: `iniciando` com a versão, `migrations aplicadas`, `sondador ativo`,
   `servindo`. Com `TRUSTED_PROXIES` ainda vazio, o aviso de "TRUSTED_PROXIES vazio com
   PUBLIC_BASE_URL https" aparece, e é esperado nesta etapa.
8. **Conferência do dono.** No console do banco: `SELECT tableowner, count(*) FROM pg_tables
   WHERE schemaname = 'public' GROUP BY 1;` dá só `vigilume_app`.
9. **IP do cliente (a pendência de sempre).** A documentação do Fly não diz de qual endereço o
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
   - O aviso do passo 9 some do log.
10. **Usuário real.** `fly proxy 15432:5432 -a vigilume-db` num terminal; no outro,
    `DATABASE_URL=postgres://vigilume_app:…@127.0.0.1:15432/vigilume?sslmode=disable vigilume user create --email <e>`
    lê a senha do stdin, sem eco (contrato, decisão 1). Serve o binário compilado na máquina do
    usuário ou a própria imagem com TTY (`docker run --rm -it --network host -e DATABASE_URL=… ghcr.io/…:<tag> user create --email <e>`).
    Sem `-t`, o terminal mostraria a senha.
11. **Conferência pública.** `https://<app>.fly.dev/healthz` e `/readyz` dão 200. Login no painel.
    `curl -sI` mostra `Strict-Transport-Security`, CSP com nonce e `X-Content-Type-Options`.
    Criar a status page e abrir `/p/{slug}` numa aba anônima.
12. **Primeira manutenção** (pendência 6). Num banco novo, a primeira rodada não tem nada a
    consolidar. Ela só pesa num banco importado com 62 dias de checks. Nada a fazer aqui.
13. **CI de deploy e de backup.** `fly tokens create deploy -a vigilume` → `FLY_API_TOKEN` no
    environment `production`, com o usuário como revisor obrigatório.
    `fly tokens create deploy -a vigilume-db` → `FLY_API_TOKEN` no environment `backup`, com a
    senha do `vigilume_backup` (`BACKUP_DATABASE_PASSWORD`) e a chave **pública** do `age`
    (`age-keygen -o vigilume-backup.key`; a privada fica **fora** do GitHub e do Fly).
    Rodar o `backup.yml` à mão uma vez e conferir que ficou verde (6.8).
14. **Voltar uma versão.** `fly releases` → `fly deploy --image ghcr.io/…@sha256:<digest anterior>`.
    Só é seguro se as migrations aplicadas pela versão nova forem aditivas (D26). O `goose` não
    roda `Down` sozinho, e o plano não automatiza isso.

### 6.6 Depuração sem shell

O contêiner do app não tem shell (§5.9), e `fly ssh console -a vigilume` não serve para nada.
O que serve: `fly logs`, os subcomandos do próprio binário (`version`, `healthcheck`, `user`) e o
console do **banco**, que tem shell por ser a imagem oficial.

### 6.7 Publicação do repositório e da imagem

Antes de tornar o repositório público (ação **manual** do usuário):

1. **Varredura do histórico inteiro:** `gitleaks git . --redact -v` (ou pelo contêiner
   `zricethezav/gitleaks`, fixado por digest). Feita neste spike (10.7): **96 commits, 1 achado**,
   um token de teste fixo em `internal/httpapi/middleware/middleware_test.go` (commit `ae726caf`),
   **falso positivo**. Vai para o `.gitleaksignore` pela *fingerprint*, com um comentário.
2. **Se aparecer um segredo real:**
   1. **Considerá-lo vazado e rotacioná-lo primeiro** (Resend, Discord, Fly, o que for), antes de
      qualquer limpeza. Reescrever a história não "desvaza" o que já saiu.
   2. Tirá-lo do histórico com `git filter-repo --replace-text` (ou `--invert-paths` para um
      arquivo inteiro), num clone novo; `git push --force` de todos os branches e tags; pedir ao
      suporte do GitHub a limpeza de caches e de PRs antigos, se houver.
   3. Rodar a varredura de novo até zerar, e só então publicar.
3. **E-mail de autor.** O histórico tem commits com o e-mail pessoal do autor (97 dos 103), que
   fica visível ao tornar o repositório público. Opções: aceitar, ou reescrever com
   `git filter-repo --mailmap` para o endereço `noreply` do GitHub (muda todos os hashes; os
   hashes citados nos documentos deixam de bater). Decisão do usuário (seção 12).
4. `.idea/` não está no `.gitignore` e aparece como não rastreado: entra no `.gitignore` antes de
   publicar, para não ser commitado por engano.
5. Tornar o repositório público (Settings → General → Danger Zone). O pacote do GHCR tem
   visibilidade **própria**: tornar público em Package settings → Change visibility. Conferir de
   uma máquina sem login: `docker pull ghcr.io/bipoxdxd/vigilume:latest`.

A partir daí, o job `secrets` (D37) varre o histórico em todo PR, e o `publish` publica a imagem
em `main` e nas tags `v*`.

### 6.8 Operação do Postgres (responsabilidade do usuário)

Sem gerência, **quatro coisas ficam com o operador**. O plano entrega o mecanismo de cada uma; o
operador precisa manter o hábito.

| Responsabilidade | Mecanismo | Frequência |
|---|---|---|
| **Backup** | Snapshots diários do volume (14 dias) + `backup.yml` diário (dump, restauração, cifra, 30 dias) | Automático; conferir que o `backup.yml` segue verde |
| **Teste de restauração** | Automático em cada `backup.yml` (restaura num Postgres descartável e confere). Manual, **trimestral**: baixar o artifact, decifrar com a chave privada do `age`, restaurar num `docker run postgres:18-alpine` local, subir o `vigilume` contra ele e fazer login | Diário (automático) e trimestral (manual) |
| **Disco** | Checagem no `backup.yml` (D35). Crescer com `fly volumes extend <id> -s <GB>` | Quando o job avisar |
| **Upgrade** | Menor: PR do Dependabot + `fly deploy -c deploy/fly/db.toml`. Maior: dump e restauração num app novo (D36) | Menor: quando o Dependabot abrir; maior: ~1 vez por ano |

**O que o `backup.yml` faz, em ordem:**

1. `flyctl proxy 15432:5432 -a vigilume-db` em segundo plano, com o token do environment `backup`.
2. `pg_dump -Fc` como `vigilume_backup`, com o **cliente da mesma versão maior do servidor**
   (roda dentro de `postgres:18-alpine`, não o `pg_dump` do runner, que pode ser mais velho e
   recusar o servidor).
3. **Restauração num serviço `postgres:18-alpine` descartável:** cria `vigilume_app` e o banco
   com o `initdb.sql` do compose, e `pg_restore` como **superusuário, preservando os donos**
   (medido no spike: restaurar como `vigilume_app` esbarra na RLS com `FORCE`; como superusuário
   com os donos do dump, as tabelas voltam com `vigilume_app` como dono e com o `FORCE` ligado).
4. Checagens na cópia restaurada, que **falham o job**: todas as tabelas de `public` pertencem a
   `vigilume_app`; `relforcerowsecurity` ligado nas tabelas com RLS; `max(version_id)` de
   `goose_db_version` igual à maior migration do repositório; `users` com pelo menos uma linha.
5. Disco (D35).
6. `age -r "$AGE_RECIPIENT"` e `actions/upload-artifact` com `retention-days: 30`. O texto claro
   nunca sai do runner.

**Recuperação de uma queda do host** (máquina única, sem réplica): `fly volumes snapshots list
<volume>` → `fly volumes create pgdata --snapshot-id <id> -s 3 -a vigilume-db` → `fly deploy -c
deploy/fly/db.toml`. O Postgres faz a recuperação de crash sozinho. Se o snapshot também falhar,
restaurar o último dump do artifact num banco novo (passo trimestral acima, apontando para o Fly).

### 6.9 Apêndice: banco que já tem tabelas

Não é o caso hoje (ver a nota no topo). Se um dia o app subir contra um banco cujas tabelas
pertencem a outro papel (por exemplo, criadas pelo `postgres`), o boot passa pelo `CheckRole` e
**falha no `Migrate`** ou na manutenção. A correção é transferir a posse **antes** do deploy:
`ALTER TABLE … OWNER TO vigilume_app` em toda tabela, **incluindo `goose_db_version`** (a
sequência acompanha a tabela), mais as funções das migrations 00005 e 00006, e as partições de
`checks` (detalhes em `PENDENCIAS-PARA-O-PLANO-3.md`, item 2). Com o superusuário na mão,
`REASSIGN OWNED BY postgres TO vigilume_app` **não** serve: ele também tentaria mover objetos do
sistema. Vai tabela a tabela.

---

## 7. Configuração de produção

| Variável | Onde | Valor em produção | Valor no compose | Observação |
|---|---|---|---|---|
| `ROLE` | `CMD` da imagem | `all` (`--role=all`) | `all` | Sobreposto pela flag |
| `HTTP_ADDR` | padrão | `:8080` | `:8080` | Casa com `internal_port` |
| `METRICS_ADDR` | `[env]` | `off` | `off` | D33 |
| `DATABASE_URL` | **secret** | `postgres://vigilume_app:…@vigilume-db.internal:5432/vigilume?sslmode=disable` | `postgres://vigilume_app:…@db:5432/vigilume?sslmode=disable` | D30, D31. O boot recusa superusuário e `BYPASSRLS` (e, portanto, o `vigilume_backup`) |
| `PUBLIC_BASE_URL` | `[env]` | `https://<app>.fly.dev` | `http://localhost:8080` | Host da API **e** do painel (mesma origem). Base de `/verify/{token}` e do `public_url` da status page |
| `CORS_ORIGIN` | `[env]` | igual a `PUBLIC_BASE_URL` | `http://localhost:5173` | Obrigatória na config. Com o painel na mesma origem, em produção não é usada de fato |
| `MASTER_KEY` | **secret** | 32 bytes aleatórios, base64 | chave de demo (D13) | **Guardar fora do Fly.** O boot recusa a de demo com https (D14) |
| `JWT_SECRET` | **secret** | idem | chave de demo | Distinta das outras duas |
| `IP_HASH_KEY` | **secret** | idem | chave de demo | Distinta das outras duas |
| `TRUSTED_PROXIES` | `[env]` | CIDR do proxy do Fly, **medido** no passo 9 de 6.5 | vazio | Sem ele, o header é ignorado e a falha fica fechada (todos dividem um balde) |
| `CLIENT_IP_HEADER` | `[env]` | `Fly-Client-IP` | vazio | **Nova** (D22). Vazio mantém o comportamento de hoje |
| `LOG_LEVEL` | `[env]` | `info` | `info` | |
| `RESEND_API_KEY` | **secret** | opcional | vazio | Vai junto com `EMAIL_FROM`, ou nenhuma das duas. Domínio verificado no Resend |
| `EMAIL_FROM` | secret ou `[env]` | opcional | vazio | idem |
| `DEMO_PASSWORD`, `DEMO_TARGETS` | só o seed | — | senha de demo com ≥ 15 caracteres, `1` | Nunca em produção |
| `POSTGRES_PASSWORD` | **secret do `vigilume-db`** | aleatória | `postgres` (só o compose) | Superusuário; só para o console administrativo |
| `FLY_API_TOKEN`, `BACKUP_DATABASE_PASSWORD`, `AGE_RECIPIENT` | environment `backup` do GitHub | token do app `vigilume-db`, senha do `vigilume_backup`, chave pública | — | D21, D32 |

---

## 8. README (estrutura, não o texto)

**Em português** (decisão do usuário), como o resto do repositório. O leitor é um avaliador
técnico com 2 minutos. O README responde na ordem em que ele pergunta: "funciona?", "roda
aqui?", "o que tem de difícil?", "está testado?".

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
   próprio; IP real atrás do proxy com falha fechada; imagem distroless nonroot com Trivy e
   varredura de segredos.
6. **Concorrência:** semáforo do sondador, `errgroup`, shutdown gracioso com prazo, *single-flight*
   do refresh. Links.
7. **Testes:** onde ficam, como rodar (`make test`, que precisa de Docker; `make test-short`, que
   não precisa), Postgres real via testcontainers (**sem mock de banco**), `-race`, matriz 17/18,
   E2E Playwright contra o compose, backup restaurado todo dia, e a prática de **prova por
   sabotagem** em uma frase com um exemplo real.
8. **Stack** numa tabela curta (spec §3.3 e §11.1).
9. **Desenvolvimento:** `make` targets, `vite dev` com proxy, `sqlc generate`.
10. **Deploy e operação:** um parágrafo (dois apps no Fly, Postgres sem gerência, backup
    restaurado todo dia) e o link para `docs/deploy/fly.md`.
11. **O que ficou de fora e por quê** (spec §2.2), com link para as specs e planos. Mostra
    critério, não esquecimento.
12. **Licença.**

Regras: nada de texto de marketing; cada afirmação de segurança aponta para um arquivo; o
README não repete a spec, aponta para ela. Os identificadores do código continuam como estão
(o código mistura português e inglês); o texto é português.

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
- Playwright (os testes do Plano 6), com `E2E_BASE_URL`, `E2E_EMAIL` e `E2E_PASSWORD`: login →
  cadastrar alvo (`https://vigilume-e2e.invalid/`) → ver o alvo (spec §14; contrato,
  Reconciliação 4).

### 9.4 Segredos (job `secrets`)

- `gitleaks` sobre o histórico inteiro sai com 0, com o `.gitleaksignore`.
- Sabotagem: um commit de teste com uma chave no formato de uma real (por exemplo, um token do
  Fly de mentira) num branch descartável → o job fica vermelho. Tirar a linha do
  `.gitleaksignore` → vermelho no falso positivo conhecido.

### 9.5 Backup (workflow `backup`)

- As checagens do passo 4 de 6.8 **falham** o job. Sabotagem, ensaiada no spike e repetida no
  plano: (a) fazer o dump como `vigilume_app` → o `pg_dump` falha pela RLS (a razão certa);
  (b) restaurar com `--no-owner` → a checagem de dono fica vermelha; (c) apontar a checagem de
  versão para uma migration que não existe → vermelho.
- Ensaiável sem o Fly: o mesmo script roda contra o compose local (`BACKUP_TARGET=compose`), e é
  assim que o plano o valida.

### 9.6 Deploy (manual, no runbook)

Os passos 5, 7, 8, 9, 11 e 13 de 6.5 **são** a verificação de produção. O mais importante é o
teste de dois pontos de rede do passo 9: é o único que prova a D22 no ambiente real.

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
documentação do Fly, não de um deploy real. O passo 9 do runbook o confirma em produção.

### 10.5 Efeitos colaterais na máquina

O `docker builder prune -af` do build frio apagou **todo** o cache do BuildKit da máquina (ele se
reconstrói sozinho no próximo build), e os pulls de amostra foram apagados em seguida. As imagens
e volumes do spike foram removidos. O contêiner `plano5-spike`, de outro agente, não foi tocado.
O plano não repete a limpeza global: limpa só os próprios artefatos.

### 10.6 Backup e restauração (PG18, com dados reais)

Com o compose do spike, um alvo cadastrado pela API (com header cifrado) e um check gravado:

| Tentativa | Resultado |
|---|---|
| `pg_dump -Fc` como `vigilume_app` (dono, `FORCE ROW LEVEL SECURITY`) | **Falha**: `query would be affected by row-level security policy`, em `channel_health`, com a dica `ALTER TABLE NO FORCE ROW LEVEL SECURITY` |
| `pg_dump -Fc` como `vigilume_backup` (`NOSUPERUSER BYPASSRLS` + `pg_read_all_data`) | OK, 57 KB |
| `pg_restore` como `postgres` (superusuário), **com** os donos do dump, num banco novo `OWNER vigilume_app` | OK. 17 tabelas com dono `vigilume_app`, 1 alvo, 1 check, `goose_db_version` = 6, `relforcerowsecurity = t` em `targets` |
| `SELECT count(*) FROM targets` como `vigilume_app`, sem escopo, no banco restaurado | **0**: a RLS continua valendo depois da restauração |

### 10.7 Varredura de segredos

`gitleaks git` (contêiner `zricethezav/gitleaks:latest`, só leitura) sobre o repositório: **96
commits, ~2,2 MB, 0,9 s, 1 achado**: `generic-api-key` em
`internal/httpapi/middleware/middleware_test.go:87`, commit `ae726caf`, a constante
`tok-9f8e…` de `TestRecoverDoesNotLogThePath`. É um token de teste que nunca existiu fora do
teste: **falso positivo**, vai para o `.gitleaksignore`. Nenhum arquivo com nome de segredo
(`.env`, `.pem`, `.key`) está rastreado. O histórico tem o e-mail pessoal do autor em 97
commits (6.7, item 3).

---

## 11. Riscos

| Risco | Probabilidade | Mitigação |
|---|---|---|
| Postgres sem gerência: disco cheio, backup quebrado sem ninguém ver, upgrade esquecido | Média | `backup.yml` diário com restauração e checagem de disco que falha o job (e-mail do GitHub); Dependabot no digest do Postgres; 6.8 lista o que é do operador |
| Queda do host do banco (máquina única, sem réplica) | Baixa | Snapshot diário (14 dias) + dump fora do Fly. Recuperação em 6.8. Perde-se até um dia no pior caso |
| O token de app do Fly não permitir `fly proxy` no workflow de backup | Média (a doc não diz o escopo exato) | Conferir no passo 13 de 6.5, rodando o `backup.yml` à mão. Se não bastar, um token de organização só no environment `backup`, com revisor |
| O endereço de origem do proxy do Fly não está em nenhum CIDR óbvio | Média | O aviso da D22 imprime o `remote_addr` real. O teste de dois pontos de rede fecha a questão |
| O Fly não sobrescreve um `Fly-Client-IP` enviado pelo cliente | Baixa (o header existe para isso) | O terceiro item do teste de dois pontos de rede (6.5, passo 9) prova. Se falhar, voltar ao XFF com "penúltimo salto" |
| Perder a `MASTER_KEY` ou a chave privada do `age` | Baixa | O runbook manda guardar as duas fora do Fly e do GitHub. Sem a primeira, os segredos cifrados somem; sem a segunda, os dumps são inúteis (os snapshots continuam) |
| Máquina única do app: um deploy ou uma falha do host para o monitor | Certa | Aceito para o portfólio. O D12 do Plano 2 garante que o deploy não abre incidente falso. Separar os papéis é mudar a flag. Quem vigia o monitor fica fora do escopo |
| Container sem shell: `fly ssh console` não serve no app | Certa | Por desenho (§5.9). Depuração em 6.6 |
| Migration quebrada em produção vira crash em loop | Baixa | CI com a matriz e o E2E contra o compose, que migra do zero. O `backup.yml` restaura e confere a versão do schema. Voltar pelo digest anterior (6.5, passo 14) |
| Alvos de exemplo do seed dependem da internet | Média | O E2E usa o alvo `.invalid` da Reconciliação 4 e não depende do estado. O seed tolera falha de rede nos alvos (usuário primeiro, alvos depois) |
| Compose copiado para produção com as chaves de demo (públicas no repositório) | Baixa | D14 (recusa no boot) + comentário + README |
| Segredo commitado depois de o repositório ficar público | Baixa | Job `secrets` em todo PR (D37). Se acontecer: rotacionar primeiro (6.7) |
| Action de terceiro comprometida | Baixa | D20 (SHA completo, Dependabot), D21 (permissões mínimas, tokens por app em environments) |
| Domínio do Resend não verificado: todo e-mail volta 403 | Média | O runbook trata o e-mail como opcional e diz o pré-requisito |

---

## 12. Decisões para o usuário revisar

Já decidido pelo usuário e fora desta lista: Postgres sem gerência, repositório e imagem públicos
com varredura antes, README em português, sem blocklist de senhas, Postgres 18 em
desenvolvimento e testes.

O que ainda decidi sozinho e merece um segundo olhar, da mais cara para a mais barata:

1. **Postgres com a imagem oficial num app próprio (D28)**, em vez do `fly postgres create` com a
   `postgres-flex`. A oficial tem patches em dia e é a mesma do compose e dos testes; a `flex`
   traria `fly pg connect` e backup por WAL prontos, mas está cinco versões menores atrás.
2. **Postgres 18 também em produção (D29).** Com o MPG fora, nada prende a produção no 17. A
   perna do 17 da matriz fica como seguro; dá para removê-la se você preferir um CI mais curto
   (a reconciliação a pôs, e eu a mantive).
3. **Backup pelo GitHub Actions, cifrado com `age`, 30 dias (D32).** Custo zero, sai do Fly e
   restaura todo dia. Exige guardar uma chave privada fora de tudo. A alternativa (bucket Tigris
   com máquina agendada no Fly) custa centavos, mas fica no mesmo provedor.
4. **E-mail pessoal no histórico (6.7, item 3).** Aceitar, ou reescrever com `--mailmap` antes
   de publicar (muda todos os hashes, inclusive os citados nos documentos).
5. **A correção do IP é código no Plano 7 (D22).** A pendência dizia "configuração". O spike
   mostrou que não basta.
6. **Recusar as chaves de demo no boot com https (D14).** Com o repositório público, as chaves de
   demo são públicas de fato.
7. **Alvos de exemplo no seed (D15).** Melhoram a primeira impressão, mas dependem da internet e
   de um parser de JSON em `sed`.
8. **E2E em todo PR (D19)**, levando o PR de ~3 para ~5 min.
9. **Deploy só manual, com revisor (D21, 6.4).**
10. **`METRICS_ADDR=off` em produção (D33).**
11. **Estratégia `rolling`, alguns segundos fora por deploy (D26).**
12. **Região `gru`**, a confirmar em `fly platform regions` no momento de criar.

---

## 13. Divergências com o contrato

Nenhuma decisão do contrato muda. Estas são adições ou consequências que precisam de
reconciliação na revisão.

1. **`vigilume user create`: o que o Plano 7 depende, já coberto pela spec do Plano 5**
   (contrato, Reconciliação 5): (a) o subcomando **aplica as migrations** antes de criar
   (`CheckRole` → `Migrate`, D17 do Plano 5), o que viabiliza a ordem da D10; (b) sai com
   **código 3** quando já existe usuário, sem alterar nada, e o seed trata 3 como sucesso. A
   senha de demo do compose respeita a política do Plano 5 (mínimo de 15 caracteres, sem
   blocklist). O spike usou uma ferramenta falsa, não o CLI real.
2. **"Aceita pipe, para o compose" (decisão 1) exige um shell**, e a imagem final não tem nenhum
   (§5.9). A consequência é o alvo `tools` (D6), que só o compose usa. Não muda o contrato, mas
   quem ler o contrato espera que o pipe "simplesmente funcione".
3. **Subcomandos novos no binário, do Plano 7:** `healthcheck` e `version` (contrato,
   Reconciliação 6). O despacho nasce no Plano 5 (`main` olha `os.Args[1]` antes do
   `flag.Parse` global e desvia para uma função que devolve o código de saída). O Plano 7
   acrescenta os dois no mesmo despacho e na mesma forma testável.
4. **O Plano 7 mexe em código Go de produto** (`CLIENT_IP_HEADER`, D22; e, se aprovada, a D14).
   O contrato descreve o Plano 7 como empacotamento. As duas mudanças só existem por causa do
   deploy, e por isso ficam aqui.
5. **Postgres em produção: 18, não 17.** A Reconciliação 7 do contrato diz "17 em produção",
   escrito quando a produção ainda seria o Managed Postgres (que só oferece 16 e 17). Com a
   decisão do usuário pelo Postgres sem gerência, a produção usa a imagem oficial do 18 (D28,
   D29). A matriz 17/18 do CI continua, como a Reconciliação pede. **Precisa de reconciliação:**
   trocar "17 em produção" por "18 em produção, 17 mantido compatível pela matriz".
6. **`.gitignore`:** o Plano 6 troca `/web/dist/` por `internal/webui/dist/*` com exceção do
   `.gitkeep` (Reconciliação 8); o Plano 7 espelha no `.dockerignore` (D7) e acrescenta `.idea/`
   ao `.gitignore` antes da publicação (6.7, item 4).
7. **E2E:** os testes Playwright são do Plano 6, e o Plano 7 os roda contra o compose (D18) com
   `E2E_BASE_URL=http://localhost:8080`, `E2E_EMAIL=demo@vigilume.local` e a senha de demo
   (Reconciliação 4). O alvo do teste é `https://vigilume-e2e.invalid/`, e o teste não depende do
   estado dele.
