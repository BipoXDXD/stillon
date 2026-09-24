# Vigilume — Plano 7: Empacotamento, Compose, CI, Deploy e README

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fazer o Vigilume rodar em qualquer máquina com `docker compose up --wait`, passar por um pipeline que dá para mostrar (Go na matriz 17/18, painel, imagem com Trivy, E2E contra o compose do avaliador, varredura de segredos) e poder ir para o Fly.io com um runbook que não falha no primeiro boot, fechando as pendências de deploy herdadas dos Planos 1 a 6.

**Architecture:** Duas mudanças de produto, as duas por causa do deploy: o IP do cliente passa a vir de `Fly-Client-IP` quando `CLIENT_IP_HEADER` está preenchido (o `X-Forwarded-For` do Fly termina no IP do app, igual para todos), e o boot recusa as chaves de demonstração do compose com `PUBLIC_BASE_URL` https. O binário ganha `healthcheck` e `version` no despacho de subcomandos que o Plano 5 criou. A imagem é multi-stage (Vite → Go com o painel embutido → distroless nonroot, sem shell), com um alvo `tools` (a variante debug, com busybox) que só o seed do compose usa. O compose sobe db → seed → app; o seed migra, cria o usuário de demonstração pela senha no stdin e cadastra o alvo `https://example.com/` (e os de `DEMO_TARGETS_EXTRA`) por uma API temporária em loopback. O CI roda o E2E do Plano 6 contra esse mesmo compose, com a imagem que acabou de passar no Trivy; o `publish` reconstrói do mesmo código e bases para as duas arquiteturas, sobe só pelo digest, reescaneia e só então dá as tags; o deploy, manual e só da `main`, copia esse digest para o registry do Fly. Produção: dois apps no Fly (o binário, e o Postgres 18 oficial num volume, sem gerência), com backup lógico diário restaurado e conferido num workflow do GitHub.

**Tech Stack:** Go 1.26, Node 24 (build do painel), Docker/BuildKit, `gcr.io/distroless/static-debian13` (`nonroot` e `debug-nonroot`), Docker Compose v5, PostgreSQL 18 (`postgres:18-alpine`, por digest), GitHub Actions (actions por SHA), Trivy 0.74, gitleaks 8.30, actionlint 1.7, Dependabot, Fly.io (`flyctl`), `age` 1.3.

**Spec:** `docs/superpowers/specs/2026-09-24-plano-7-empacotamento-e-deploy-design.md` (D1–D37), `docs/superpowers/specs/2026-09-24-planos-5-6-7-contrato.md` (com a seção **Reconciliação**, que prevalece) e `docs/superpowers/specs/2026-09-22-vigilume-design.md` (§1.1, §5.9, §13, §14, §15).

**Base:** o estado final do Plano 6 (`docs/superpowers/plans/2026-09-24-painel-react.md`), escrito sobre a `main` com o Plano 5 integrado (PR #8, `e3aec43`, inclusive a Task 11: `Issue` rechecando a senha sob a trava do usuário, leitura de senha abortável no CLI) (`docs/superpowers/plans/2026-09-24-api-do-painel.md`): o painel em `web/`, embutido por `internal/webui`; o despacho `subcomandos` com `vigilume user create` e `user set-password`; as rotas da conta (`internal/httpapi/accounthandlers.go`); o E2E Playwright que aceita `E2E_BASE_URL`, `E2E_EMAIL` e `E2E_PASSWORD`. Os diffs aplicam sobre esse estado.

**Validação prévia.** Todo o conteúdo deste plano rodou numa cópia do repositório antes de ser entregue: `go test -race -count=1 ./...` verde (PostgreSQL 18 real), `gofmt`, `vet`, `staticcheck` v0.8.1, `gosec` v2.29.0 e `govulncheck` v1.8.0 limpos; `actionlint` v1.7.12 limpo nos três workflows; a imagem construída, com Trivy (0 HIGH/CRITICAL corrigíveis, Debian 13.7 e o binário) e as checagens de `deploy/ci/checar-imagem.sh`; o compose de pé com `deploy/ci/checar-compose.sh` verde e o **E2E do Plano 6 contra ele: 6 de 6**; o job `e2e` simulado com a imagem passando por `docker save`/`docker load` e `--no-build --pull never`; os passos do `backup.yml` repetidos localmente, com `age`, e o **texto exato** do passo principal rodado sob os dois shells do GitHub; o `publish` ensaiado contra um registry local (sobe só pelo digest, Trivy nas duas arquiteturas, tags depois, digest preservado); as flags do `flyctl` conferidas no `--help` da versão 0.4.108 (sem conta no Fly: nada foi executado contra ele). Além disso, **o estado da árvore depois de cada tarefa** foi reconstruído a partir deste plano e conferido (compila, `vet`, `gofmt`, e o que a tarefa entrega: testes Go, `actionlint`, `gitleaks` no histórico real, build da imagem e Trivy, compose e Playwright, ensaio do backup, links do README); o estado depois da Task 9 é idêntico, byte a byte, à cópia validada. Cada sabotagem pedida nos passos foi feita e ficou vermelha **pela razão certa**; as que ficaram verdes, ou vermelhas por outra razão, estão ditas no passo. Se um passo não se comportar como o plano diz, isso é informação: pare e reporte, não force.

**Como ler os diffs.** Arquivo novo aparece inteiro. Arquivo modificado aparece como diff unificado contra o estado da tarefa anterior; o contexto basta para aplicar à mão, ou com `git apply` depois de salvar o bloco num arquivo.

**O que roda fora da máquina de quem executa, e o que não roda nunca.** Os passos fazem `docker pull` das imagens-base (Docker Hub, `gcr.io`), `go run` de ferramentas com versão fixa e `npx playwright install`. **Nenhum passo** publica imagem, cria nada no Fly, grava segredo ou muda configuração do GitHub: o job `publish` só roda no GitHub depois do merge, e o deploy e tudo o que o runbook lista são do dono do projeto, com confirmação explícita (contrato, decisão 6).

---

## Global Constraints

Valores copiados da spec do Plano 7, do contrato e da reconciliação. Valem para todas as tarefas, inclusive as que não os repetem.

- **Go 1.26**, módulo `github.com/BipoXDXD/vigilume`. Código Go novo segue o estilo do repositório: nomes de teste em inglês, helpers e comentários em português.
- **Repositório privado por enquanto** (decisão do usuário). O pacote do GHCR segue a visibilidade do repositório. Nada neste plano torna algo público.
- **Tudo fixado**: imagens-base e Postgres por **digest** (a tag no comentário), actions por **SHA completo** (a tag no comentário), ferramentas Go por **versão**. O Dependabot atualiza (D1, D20).
- **A imagem final não tem shell nem ferramenta** (spec §5.9): `distroless/static-debian13:nonroot`, `USER nonroot:nonroot`, sem `HEALTHCHECK`. O alvo `tools` (com busybox) **nunca** vai para o Fly (D5, D6).
- **A versão** vem de `-ldflags "-X main.version=…"`, aparece no log `iniciando` e em `vigilume version`, e **nunca** numa resposta HTTP (D3, D4).
- **Postgres 18** no compose, nos testes e em produção, com a **mesma** imagem e digest: `postgres:18-alpine@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873`. Volume em `/var/lib/postgresql`, não em `…/data` (D11, D29). A matriz do CI mantém o 17 compatível.
- **O papel da aplicação é comum**: `vigilume_app`, `NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE`, dono do banco; o **mesmo SQL** no compose, na restauração do backup e em produção (D12, D30).
- **Ordem do compose: db → seed → app** (D10). O código **3** de `vigilume user create` é sucesso no seed (Reconciliação 5). A senha de demonstração tem **≥ 15** code points (sem blocklist, Reconciliação 9).
- **O compose escuta só em `127.0.0.1`**, com `PUBLIC_BASE_URL=http://localhost:8080` e `CORS_ORIGIN=http://localhost:5173` (D16). O painel exige **contexto seguro** (HTTPS ou `localhost`): o README, o compose e o runbook dizem para abrir por `localhost`.
- **E2E contra o compose** com `E2E_BASE_URL`, `E2E_EMAIL=demo@vigilume.local` e `E2E_PASSWORD` (Reconciliação 4; D18), só no Chromium, em todo PR (D19).
- **Permissões mínimas no CI**: `contents: read` no workflow; `packages: write` só no `publish`; tokens do Fly **por app** (`FLY_DEPLOY_TOKEN`, `FLY_BACKUP_TOKEN`), como secrets **de repositório**: o GitHub Free com repositório privado não tem environments (desvio da D21). Todo workflow declara `shell: bash` (`-eo pipefail`). Input de `workflow_dispatch` chega ao shell só por variável de ambiente.
- **Deploy real só com confirmação do usuário** (contrato, decisão 6). O plano entrega os artefatos e o runbook. O workflow `deploy` só roda da `main`, disparado à mão por quem tem acesso de escrita (hoje, só o dono); não há revisor obrigatório no GitHub Free privado, e nenhum texto do plano diz que há.
- **`go test -race -count=1 ./...` verde ao fim de toda tarefa** que mexe em Go.
- **Prova por sabotagem é obrigatória** para toda asserção de ausência ("não é root", "não tem shell", "o cabeçalho forjado não vale", "o segundo `up` não duplica"). O implementador quebra a propriedade de propósito, vê o teste ficar vermelho **pela razão certa** (uma sabotagem que não compila não prova nada: o build falha, e isso não é o teste falhando), restaura, e registra no relatório.

### Desvios registrados em relação à spec do Plano 7

| Spec | Aqui | Motivo |
|---|---|---|
| D15: três alvos públicos (example.com, iana.org, wikipedia.org) | Só `https://example.com/`, mais os de `DEMO_TARGETS_EXTRA` | Decisão do controlador: o domínio reservado para exemplos, sem sondar sites de terceiros a cada 60 s (D11 deste plano) |
| D15: o seed cria os alvos "pela própria API" (login + `POST /targets` com `wget`) | O seed sobe **uma instância só-API em loopback, dentro do próprio contêiner** (`--role=api` em `127.0.0.1:18080`), cadastra os alvos por ela e a derruba | Com a ordem db → seed → app (D10), o app ainda não existe quando o seed roda. Assim a API usada é a mesma, com as mesmas validações, e a ordem que faz o `up --wait` sair 0 continua |
| 9.3: sabotagem "volume em `/var/lib/postgresql/data` → o usuário some" | O Postgres 18.6 **nem sobe**: o entrypoint detecta a montagem em `…/data` e sai com erro; o `checar-compose.sh` fica vermelho no primeiro `up` | A imagem oficial ganhou a própria trava. A checagem de persistência (down + up) continua, como guarda contra outras regressões |
| 10.2: seed depois do app → o `up --wait` devolve **1** | No Compose v5.5.1, o **primeiro** `up` devolve 0; o **segundo** devolve 1 ("container seed exited (0)") | Medido na validação. A ordem errada continua vermelha no `checar-compose.sh`, no passo de idempotência |
| 9.2: "Painel embutido: `GET /` do contêiner dá o `index.html`" no job `image` | No job `image`, o binário é tirado da imagem (`docker create` + `docker cp`) e procurado pela referência ao bundle (`src="/assets/index-`); o `GET /` real fica no `checar-compose.sh` (job `e2e`) | O `GET /` precisa de um Postgres; o job `e2e` já sobe um. As duas checagens existem, uma em cada job |
| 5.1: `deploy` como job do `ci.yml` | Workflow próprio, `.github/workflows/deploy.yml`, só `workflow_dispatch`, com o digest como input | No `ci.yml`, um `workflow_dispatch` rodaria também lint, testes e E2E a cada deploy. O runbook já dizia "Actions → deploy → Run workflow" |
| 4.1: `image: ghcr.io/bipoxdxd/vigilume:<tag>` | `:${VIGILUME_TAG:-latest}` e `:${VIGILUME_TAG:-latest}-tools`; o `publish` também empurra `:latest-tools` | O job `e2e` usa a imagem do artifact com `VIGILUME_TAG=ci`; o seed precisa de uma tag estável da variante `tools` |
| 3.1: `npm ci → npm run build` | Igual, e o `# syntax=` do Dockerfile **não** é usado | O frontend embutido do BuildKit do Docker 29 já tem cache mounts; a linha `# syntax=docker/dockerfile:1` baixaria um frontend por tag solta a cada build |
| D21: tokens do Fly em **environments** (`production` com revisor obrigatório, `backup`) | Secrets **de repositório** (`FLY_DEPLOY_TOKEN`, `FLY_BACKUP_TOKEN`); o `deploy` só roda da `main`; sem revisor | O GitHub Free não tem environments, secrets por environment nem revisor em repositório privado (decisão do controlador: ficar no Free e privado). A trava que existe é quem pode disparar (acesso de escrita). O runbook diz como pôr o environment com revisor quando o repositório abrir, e que o Pro (~US$ 4/mês) daria secrets por environment mas não o revisor |
| D34 e 5.1: o deploy usa "a **mesma** imagem que passou no Trivy e no E2E" | O `publish` **reconstrói** (mesmo código, mesmas bases por digest, o cache do `image`) para amd64 e arm64, sobe só pelo digest, **reescaneia** com o Trivy e só então dá as tags; `provenance: false` | O E2E roda a imagem amd64 do `image`; promover exatamente ela exigiria construir as duas arquiteturas já no PR. O binário é reprodutível, a imagem não (D9 da spec). Decisão do controlador. README e runbook dizem "mesmo código e bases fixadas, reescaneado" |
| 6.2: `deploy/fly/db/fly.toml` com `[build] dockerfile = "Dockerfile"` | Sem `[build]`: `fly deploy -c deploy/fly/db/fly.toml --ha=false --image "$(sed -n 's/^FROM //p' deploy/fly/db/Dockerfile)"` | Sem build remoto e sem depender de como o `flyctl` resolve um caminho relativo ao `-c`. O Dockerfile de uma linha continua, para o Dependabot. Não ensaiado no Fly (sem conta na validação) |
| 6.2 e D23: `auto_start_machines = false` | `auto_start_machines = true` e `[[restart]] policy = "always"`; `--ha=false` em todo deploy | O proxy nunca desliga a máquina (`auto_stop_machines = "off"`, D23 mantida), mas liga de novo uma que parou; `on-failure` (o padrão) não reinicia um processo que saiu com 0. Sem `--ha=false`, o primeiro deploy cria duas máquinas: notificações em dobro |
| 5.1: `backup` diário, sempre | Só com a variável `BACKUP_ENABLED=1` | Antes do primeiro deploy não há banco: o agendamento falharia todo dia. Com a variável, termina em "skipped" |
| 9.2: "`{{.Config.User}}` dá `nonroot:nonroot`" | Qualquer usuário não root (D5 deste plano) | A base `:nonroot` já traz o 65532; exigir a string faria a sabotagem "sem `USER`" ficar vermelha por um motivo falso |
| §8 da spec: README com GIF e badges de CI, Go e licença | Só os badges de CI e Go; sem GIF, sem licença | Não há deploy para gravar o GIF nem licença escolhida (decisão pendente do dono) |
| §8 e D17: "~31 s" de build frio no README | "cerca de 40 s" | O build frio da validação foi ~31 s, mas o avaliador também puxa as bases e o Postgres e sobe o compose: ~40 s é o número honesto de ponta a ponta nesta máquina |

---

## Decisões tomadas neste plano

As decisões de desenho estão na spec do Plano 7 (D1–D37). Estas são as do plano, achadas ou tomadas na validação, com o custo de estarem erradas.

| # | Decisão | Alternativa | Custo se errada |
|---|---|---|---|
| D1 | `healthcheck` e `version` entram no mapa `subcomandos` do Plano 5 como funções `runHealthcheck(ctx, stderr, getenv)` e `runVersion(stdout)`, testadas diretamente; um teste confere que o mapa tem as três chaves e não tem `--role=all` nem `serve` | Um segundo despacho próprio antes do `flag.Parse` (a primeira versão deste trabalho) | Dois despachos divergem com o tempo: um subcomando novo entraria num e não no outro. O teste do mapa pega o subcomando que sumiu |
| D2 | As rotas da conta do Plano 5 (`/auth/password`, `/sessions`) recebem `middleware.Proxies` do `Routes`, como o login; teste de integração próprio (`TestPasswordChangeBehindFlyAuditsTheClient`) | Confiar no teste do login | Cada grupo de handlers recebe os proxies por um caminho próprio. Sem o teste, trocar só o do login deixaria a auditoria da troca de senha gravando o hash do proxy, e a sabotagem disso ficava verde |
| D3 | `TestComposeUsesTheDemoKeys`: um teste Go lê o `compose.yaml` e confere que as três chaves são as que o boot recusa | Só o comentário no compose | Trocar uma chave no compose sem trocar em `config.go` desligaria a D14 em silêncio: o compose copiado para produção subiria com uma chave pública |
| D4 | As checagens da imagem e do compose são **scripts do repositório** (`deploy/ci/checar-imagem.sh`, `deploy/ci/checar-compose.sh`) que o CI chama | Passos inline no YAML | Inline, só dá para testar empurrando para o GitHub. Como script, roda na máquina de quem executa, e as sabotagens deste plano rodaram assim |
| D5 | A checagem de usuário aceita qualquer usuário **não root** (recusa vazio, `root`, `0`, `root:*`, `0:*`) | Exigir a string `nonroot:nonroot` | A base `:nonroot` já traz o usuário 65532, e o `USER` do Dockerfile repete. Exigir a string faria "tirar o `USER`" ficar vermelho por um motivo falso (65532 **é** não-root). O script diz que são duas guardas |
| D6 | Teto da imagem comprimida em **15 MB** medido com `docker save \| gzip` | `docker images` | O `docker images` do store do containerd soma blob e snapshot (32 MB aqui, para 7,6 MB comprimidos) |
| D7 | O seed **falha** se o cadastro de um alvo falhar | Avisar e sair 0 | Cadastrar alvo não usa rede (a validação não resolve DNS), então uma falha ali é defeito, e o E2E do CI precisa ver. O custo: se o seed cair depois de criar o usuário, o `up` seguinte vê o código 3 e não tenta os alvos de novo (registrado nas pendências) |
| D8 | O job `web` roda `go test ./internal/webui/` **depois** do `npm run build`, com `VIGILUME_EXIGIR_DIST=1`, e confere que o build não sujou a árvore (`git status --porcelain` vazio) | Só o job `test`, com o dist vazio | No job `test` o dist só tem o `.gitkeep`, e o teste de MIME do dist real passaria sem conferir nada (Plano 6). Com a variável, dist vazio é falha |
| D9 | Trivy pelo contêiner oficial, fixado por digest num **Dockerfile de uma linha** (`deploy/ci/trivy/Dockerfile`), que o CI lê (`sed -n 's/^FROM //p'`); o `backup.yml` lê do mesmo jeito a imagem do Postgres de `deploy/fly/db/Dockerfile` | `aquasecurity/trivy-action`; o digest no `env` do workflow | Uma action de terceiro a mais, com permissões próprias, para rodar um binário que o contêiner já dá. No `env`, o Dependabot não vê o digest, e ele envelheceria à mão. Com o Dockerfile, o Dependabot abre o PR, e o backup usa **por construção** a imagem de produção |
| D10 | `compose.yaml` com `name: vigilume` e `VIGILUME_PORT` (padrão 8080) | Nome derivado do diretório; porta fixa | Com o nome fixo, `docker compose down -v` de qualquer clone acha o mesmo projeto. A porta variável deixa rodar ao lado de outra coisa na 8080 (e foi assim que a validação rodou, em portas altas) |
| D11 | Seed com **um** alvo, `https://example.com/`, e `DEMO_TARGETS_EXTRA` (URLs separadas por espaço) para quem quiser mais; o `checar-compose.sh` confere 1 + o número de extras, e que o example.com está lá | Três alvos públicos de terceiros (spec D15: example.com, iana.org, wikipedia.org) | O compose de um avaliador esquecido de pé sondaria sites de terceiros a cada 60 s. O `example.com` é reservado para exemplos (RFC 2606 e 6761). Decisão do controlador na revisão |
| D12 | `concurrency` do `ci.yml` com `cancel-in-progress: true` por ref, também na `main` | Cancelar só em PR | Com o repositório privado, cada execução superada gasta cota de Actions à toa. Na `main`, o `publish` da execução nova substitui o da cancelada. O E2E continua em todo PR (D19); o runbook explica a conta e o primeiro corte |
| D13 | `defaults: run: shell: bash` nos três workflows | O padrão do GitHub | O padrão é `bash -e`, sem `pipefail`: o `backup.sh \| tee` do `backup.yml` ficava **verde** com a checagem ou o disco falhando (achado na revisão; provado com o texto exato do passo: código 0 sem, 3 com) |
| D14 | `--ha=false` em todo `fly deploy`, e o `deploy` confere `fly machines list -q` = 1 | Confiar no padrão | O primeiro deploy de um app cria duas máquinas: dois `--role=all` mandariam cada notificação em dobro, e o banco teria uma máquina sem volume |
| D15 | O `deploy` recusa `TRUSTED_PROXIES` vazio no `fly.toml`; o valor medido entra por PR (runbook, passo 9) | Confiar no runbook | Um deploy pelo workflow com o `fly.toml` do repositório apagaria o valor medido à mão, e todo cliente voltaria a dividir um balde |
| D16 | O seed faz logout no fim; o `checar-compose.sh` confere que nenhuma sessão do seed (`Wget`) ficou viva | Deixar a sessão expirar | Um refresh de 7 dias parado no banco é uma credencial a mais. Custo: quatro linhas de `sed` |
| D17 | `dependabot.yml` ignora versão **maior** de `postgres` e `node` | Deixar o PR vir | Um PR "postgres 18 → 19" verde no CI seria um desastre no merge: a maior é dump e restauração (D36) |

---

## Review Focus

As cinco entradas que a spec implica e que mais provavelmente morderiam quem usa ou opera, com o que uma pessoa razoável esperaria. Cada uma tem trava na tarefa dona.

1. **Oito pessoas diferentes erram a senha no painel de produção ao mesmo tempo.** Espera-se que cada uma tenha o próprio limite, e não que todas levem 429 a partir da sexta tentativa. → Task 1, `TestRateLimitSeparatesClientsBehindFly` e `TestSessionsBehindFlyRecordDistinctOrigins`; no Fly, o teste de dois pontos de rede do runbook (Task 8).
2. **Alguém copia o `compose.yaml` para um servidor com https.** Espera-se que o boot recuse, e não que suba com chaves públicas. → Task 2, `TestLoadRejectsDemoKeysOverHTTPS`; Task 6, `TestComposeUsesTheDemoKeys`.
3. **O avaliador roda `docker compose up --wait` duas vezes, ou para e sobe de novo.** Espera-se sair 0 as duas vezes, sem duplicar usuário nem alvos, e com os dados lá. → Task 6, `checar-compose.sh` (idempotência e persistência).
4. **O painel quebra no build da imagem, mas o Go compila.** Espera-se CI vermelho, e não uma imagem publicada que responde 503 na raiz. → Task 5, `checar-imagem.sh` (painel dentro do binário); Task 7, o E2E contra o compose.
5. **O backup de produção para de funcionar em silêncio.** Espera-se um job vermelho no dia (o GitHub manda e-mail), e não descobrir no dia de restaurar. → Task 8, `backup.sh` + `verificar.sql` (dono, RLS com `FORCE`, versão, contagens, partições, disco) e o `shell: bash` do `backup.yml` (sem `pipefail`, o `| tee` deixava o job verde).

---

## Mapa de arquivos

| Arquivo | Responsabilidade |
|---|---|
| `internal/config/config.go` | `CLIENT_IP_HEADER`; recusa das chaves de demonstração com https. |
| `internal/httpapi/middleware/clientip.go` | `Proxies{Trusted, Header}`; `Fly-Client-IP` só de origem confiável; aviso limitado a um por minuto. |
| `internal/httpapi/middleware/ratelimit.go`, `internal/httpapi/{routes,publicpage,authhandlers,accounthandlers}.go` | Os limitadores e os handlers recebem `Proxies`. |
| `cmd/vigilume/ops.go`, `cmd/vigilume/main.go` | `vigilume healthcheck`, `vigilume version`, `var version`; o despacho. |
| `Dockerfile`, `.dockerignore` | Web → build → `tools` / `runtime`. |
| `compose.yaml`, `deploy/compose/initdb.sql`, `deploy/compose/seed.sh` | A pilha do avaliador. |
| `deploy/ci/checar-imagem.sh`, `deploy/ci/checar-compose.sh` | As checagens que o CI roda, e que rodam localmente. |
| `Makefile` | `build` com a versão; `image`, `up`, `down`, `e2e`. |
| `.github/workflows/ci.yml` | lint, test (17/18), secrets, web, image, e2e, publish. |
| `.github/workflows/deploy.yml`, `.github/workflows/backup.yml` | Deploy manual por digest; backup diário com restauração. |
| `.github/dependabot.yml`, `.gitleaksignore` | Atualizações; falsos positivos do gitleaks. |
| `deploy/ci/trivy/Dockerfile` | O Trivy por digest, numa linha que o Dependabot vê e o CI lê. |
| `fly.toml`, `deploy/fly/db/{fly.toml,Dockerfile}` | Os dois apps do Fly. |
| `deploy/backup/{backup.sh,verificar.sql}` | Dump, restauração e checagens. |
| `docs/deploy/fly.md` | O runbook. |
| `README.md`, `.gitignore`, `docs/superpowers/PENDENCIAS-PARA-O-PLANO-8.md`, roadmap | Fechamento. |

---

## Ordem das tarefas e seu motivo

1. **IP do cliente**, 2. **chaves de demonstração**, 3. **`healthcheck` e `version`:** o código Go que o empacotamento precisa, com teste, antes de qualquer arquivo de infraestrutura depender dele.
4. **CI dos jobs Go e da varredura de segredos:** ferramentas fixadas e a matriz 17/18 valem desde já para as tarefas seguintes.
5. **Imagem**, 6. **compose:** o critério de pronto número 1; o compose usa a imagem e o `healthcheck`.
7. **CI da imagem, do E2E e da publicação:** só faz sentido com a imagem e o compose prontos.
8. **Fly e backup:** os artefatos de produção e o runbook; nada roda sozinho.
9. **README e pendências:** o que o avaliador lê, apontando para o que já existe.

---

### Task 1: IP do cliente atrás do proxy do Fly (`CLIENT_IP_HEADER`)

A pendência mais antiga do projeto dizia que o `TRUSTED_PROXIES` no Fly era "configuração, não código". Não é: o proxy do Fly **anexa** ao `X-Forwarded-For` o IP do próprio app (o anycast em que o cliente bateu), e o `ClientIP` de hoje pega o último salto. Com `TRUSTED_PROXIES` preenchido, todos os clientes caem no mesmo balde de rate limit e no mesmo `ip_hash` (spec do Plano 7, D22 e 10.4). A correção: uma variável nova, `CLIENT_IP_HEADER`; preenchida (`Fly-Client-IP`), vale esse cabeçalho, e só ele, quando a conexão vem de `TRUSTED_PROXIES`. Vazia, nada muda.

**Files:**
- Modify: `internal/config/config.go`, `internal/config/config_test.go`
- Modify: `internal/httpapi/middleware/clientip.go`, `internal/httpapi/middleware/clientip_test.go`
- Create: `internal/httpapi/middleware/clientip_internal_test.go`
- Modify: `internal/httpapi/middleware/ratelimit.go`, `internal/httpapi/middleware/ratelimit_internal_test.go`, `internal/httpapi/middleware/middleware_test.go`
- Modify: `internal/httpapi/routes.go`, `internal/httpapi/publicpage.go`, `internal/httpapi/authhandlers.go`, `internal/httpapi/accounthandlers.go`
- Modify: `internal/httpapi/auth_integration_test.go`

**Interfaces:**
- Consumes: `config.Load`, `middleware.ClientIP`, `middleware.RateLimit`, `NewAuthHandlers`, `NewAccountHandlers` (Plano 5), o helper de teste `newAPIWith(t, ajustar)`.
- Produces:
  - `config.Config.ClientIPHeader string` (canônico, `http.CanonicalHeaderKey`); env `CLIENT_IP_HEADER`, vazia por padrão.
  - `type middleware.Proxies struct { Trusted []netip.Prefix; Header string }`.
  - `func middleware.ClientIP(r *http.Request, p middleware.Proxies) string` e `func middleware.RateLimit(ctx, perMinute, burst int, p middleware.Proxies)` (antes recebiam `[]netip.Prefix`).
  - `NewAuthHandlers(…, proxies middleware.Proxies)` e `NewAccountHandlers(svc, proxies middleware.Proxies, secureCookie)`.
  - `func proxiesDe(cfg *config.Config) middleware.Proxies` em `routes.go`.

**Contexto para quem implementa.** Os três limitadores (API, `/verify/`, `/p/`), o do login (que o Plano 5 passou a dividir com `/auth/password`) e o `ip_hash` das sessões e da auditoria passam todos pelo mesmo `ClientIP`, e são corrigidos juntos. Mas cada grupo de handlers recebe os proxies por um caminho próprio em `routes.go`; por isso há um teste de integração para o login (`TestSessionsBehindFlyRecordDistinctOrigins`) e outro para as rotas da conta (`TestPasswordChangeBehindFlyAuditsTheClient`). O aviso de "cabeçalho chegou de origem não confiável" é o que, em produção, revela o endereço do proxy do Fly (a documentação dele não diz), e por isso sai com o `remote_addr`, no máximo uma vez por minuto e só com `CLIENT_IP_HEADER` preenchido.

- [ ] **Step 1: Escrever os testes (falham)**

Aplique em `internal/config/config_test.go`:

```diff
--- a/internal/config/config_test.go
+++ b/internal/config/config_test.go
@@ -248,3 +248,36 @@
 		t.Errorf("log = %s", buf.String())
 	}
 }
+
+func TestLoadParsesTheClientIPHeader(t *testing.T) {
+	env := validEnv()
+	env["CLIENT_IP_HEADER"] = "fly-client-ip"
+	cfg, err := Load(getenvFrom(env))
+	if err != nil {
+		t.Fatal(err)
+	}
+	if cfg.ClientIPHeader != "Fly-Client-Ip" {
+		t.Errorf("ClientIPHeader = %q, queria a forma canônica Fly-Client-Ip", cfg.ClientIPHeader)
+	}
+}
+
+func TestClientIPHeaderDefaultsToEmpty(t *testing.T) {
+	cfg, err := Load(getenvFrom(validEnv()))
+	if err != nil {
+		t.Fatal(err)
+	}
+	if cfg.ClientIPHeader != "" {
+		t.Errorf("sem CLIENT_IP_HEADER veio %q, queria vazio (último salto do XFF)", cfg.ClientIPHeader)
+	}
+}
+
+func TestLoadRejectsABadClientIPHeader(t *testing.T) {
+	for _, v := range []string{"Fly Client IP", "Fly-Client-IP:", "X-Forwarded-For", "x-forwarded-for"} {
+		env := validEnv()
+		env["CLIENT_IP_HEADER"] = v
+		_, err := Load(getenvFrom(env))
+		if err == nil || !strings.Contains(err.Error(), "CLIENT_IP_HEADER") {
+			t.Errorf("CLIENT_IP_HEADER=%q: erro = %v, queria recusa citando a variável", v, err)
+		}
+	}
+}
```

Aplique em `internal/httpapi/middleware/clientip_test.go`:

```diff
--- a/internal/httpapi/middleware/clientip_test.go
+++ b/internal/httpapi/middleware/clientip_test.go
@@ -39,25 +39,26 @@
 func TestClientIPIgnoresForwardedForFromAnUntrustedOrigin(t *testing.T) {
 	r := requisicaoDe("203.0.113.9:4444", "1.2.3.4")
 
-	if got := middleware.ClientIP(r, nil); got != "203.0.113.9" {
+	if got := middleware.ClientIP(r, middleware.Proxies{}); got != "203.0.113.9" {
 		t.Errorf("sem proxy confiável, ClientIP = %q, queria o RemoteAddr 203.0.113.9", got)
 	}
 
 	confiaveis := prefixos(t, "10.0.0.0/8")
-	if got := middleware.ClientIP(r, confiaveis); got != "203.0.113.9" {
+	if got := middleware.ClientIP(r, middleware.Proxies{Trusted: confiaveis}); got != "203.0.113.9" {
 		t.Errorf("origem fora da lista confiável, ClientIP = %q, queria 203.0.113.9", got)
 	}
 }
 
 // TestClientIPTakesTheLastHopFromATrustedProxy: o último salto, e não o
-// primeiro. No Fly.io o proxy ANEXA o IP real ao fim da cadeia, então o
-// último elemento é o único que o proxy escreveu e o cliente não controla.
-// Os anteriores foram enviados pelo cliente e são tão confiáveis quanto ele.
+// primeiro. Um proxy que ANEXA o IP observado ao fim da cadeia escreveu só o
+// último elemento; os anteriores foram enviados pelo cliente e são tão
+// confiáveis quanto ele. (O Fly anexa o IP do próprio app, e por isso lá se
+// usa o cabeçalho dedicado: ver os testes de Fly-Client-IP.)
 func TestClientIPTakesTheLastHopFromATrustedProxy(t *testing.T) {
 	r := requisicaoDe("10.1.2.3:5555", "9.9.9.9, 8.8.8.8, 198.51.100.7")
 	confiaveis := prefixos(t, "10.0.0.0/8")
 
-	if got := middleware.ClientIP(r, confiaveis); got != "198.51.100.7" {
+	if got := middleware.ClientIP(r, middleware.Proxies{Trusted: confiaveis}); got != "198.51.100.7" {
 		t.Errorf("ClientIP = %q, queria o último salto 198.51.100.7", got)
 	}
 }
@@ -71,7 +72,7 @@
 		if xff == "" {
 			r.Header.Del("X-Forwarded-For")
 		}
-		if got := middleware.ClientIP(r, confiaveis); got != "10.1.2.3" {
+		if got := middleware.ClientIP(r, middleware.Proxies{Trusted: confiaveis}); got != "10.1.2.3" {
 			t.Errorf("com XFF %q, ClientIP = %q, queria o fallback 10.1.2.3", xff, got)
 		}
 	}
@@ -83,7 +84,7 @@
 func TestRateLimitSeparatesClientsBehindATrustedProxy(t *testing.T) {
 	ctx := t.Context()
 	confiaveis := prefixos(t, "10.0.0.0/8")
-	h := middleware.RateLimit(ctx, 60, 1, confiaveis)(http.HandlerFunc(
+	h := middleware.RateLimit(ctx, 60, 1, middleware.Proxies{Trusted: confiaveis})(http.HandlerFunc(
 		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
 
 	for i, xff := range []string{"198.51.100.1", "198.51.100.2", "198.51.100.3"} {
@@ -95,3 +96,98 @@
 		}
 	}
 }
+
+// Formato do Fly (documentação de request headers): o X-Forwarded-For
+// termina no IP do APP, e o cliente vem em Fly-Client-IP.
+const ipDoApp = "66.241.124.63"
+
+func requisicaoDoFly(remoto, cliente string) *http.Request {
+	r := requisicaoDe(remoto, cliente+", "+ipDoApp)
+	r.Header.Set("Fly-Client-IP", cliente)
+	return r
+}
+
+func fly(t *testing.T) middleware.Proxies {
+	return middleware.Proxies{Trusted: prefixos(t, "172.16.0.0/12"), Header: "Fly-Client-IP"}
+}
+
+// TestClientIPReadsFlyClientIPFromATrustedProxy é o caminho feliz da D22 do
+// Plano 7.
+func TestClientIPReadsFlyClientIPFromATrustedProxy(t *testing.T) {
+	r := requisicaoDoFly("172.19.0.5:5555", "198.51.100.7")
+	if got := middleware.ClientIP(r, fly(t)); got != "198.51.100.7" {
+		t.Errorf("ClientIP = %q, queria o Fly-Client-IP 198.51.100.7", got)
+	}
+}
+
+// TestClientIPWithoutTheHeaderSettingCollapsesBehindFly documenta o defeito
+// que o CLIENT_IP_HEADER corrige: no formato do Fly, o último salto do XFF é
+// o IP do app, igual para todos os clientes. O padrão continua o mesmo (e
+// fechado); é a configuração que muda.
+func TestClientIPWithoutTheHeaderSettingCollapsesBehindFly(t *testing.T) {
+	semHeader := middleware.Proxies{Trusted: prefixos(t, "172.16.0.0/12")}
+	for _, cliente := range []string{"198.51.100.1", "203.0.113.9"} {
+		r := requisicaoDoFly("172.19.0.5:5555", cliente)
+		if got := middleware.ClientIP(r, semHeader); got != ipDoApp {
+			t.Errorf("sem CLIENT_IP_HEADER, ClientIP = %q; o formato do Fly daria o IP do app %s", got, ipDoApp)
+		}
+	}
+}
+
+// TestClientIPIgnoresAForgedFlyClientIP: o cabeçalho vale só vindo da lista.
+// Direto da internet (ou de outra máquina fora da lista), é o cliente
+// escolhendo o próprio balde.
+func TestClientIPIgnoresAForgedFlyClientIP(t *testing.T) {
+	r := requisicaoDoFly("203.0.113.50:4444", "1.2.3.4")
+	if got := middleware.ClientIP(r, fly(t)); got != "203.0.113.50" {
+		t.Errorf("origem fora da lista, ClientIP = %q, queria o RemoteAddr 203.0.113.50", got)
+	}
+	vazia := middleware.Proxies{Header: "Fly-Client-IP"}
+	if got := middleware.ClientIP(r, vazia); got != "203.0.113.50" {
+		t.Errorf("TRUSTED_PROXIES vazio, ClientIP = %q, queria o RemoteAddr 203.0.113.50", got)
+	}
+}
+
+// Com o cabeçalho configurado, o X-Forwarded-For não conta mais nada, nem
+// quando é o único que veio.
+func TestClientIPWithTheHeaderSettingIgnoresForwardedFor(t *testing.T) {
+	r := requisicaoDe("172.19.0.5:5555", "198.51.100.7")
+	if got := middleware.ClientIP(r, fly(t)); got != "172.19.0.5" {
+		t.Errorf("sem Fly-Client-IP, ClientIP = %q, queria o RemoteAddr 172.19.0.5", got)
+	}
+}
+
+// Um Fly-Client-IP ilegível não pode virar chave de balde nem hash de origem.
+func TestClientIPFallsBackWhenFlyClientIPIsUnusable(t *testing.T) {
+	for _, valor := range []string{"nao-e-um-ip", "1.2.3.4, 5.6.7.8", "fe80::1%eth0", " "} {
+		r := requisicaoDe("172.19.0.5:5555", "")
+		r.Header.Set("Fly-Client-IP", valor)
+		if got := middleware.ClientIP(r, fly(t)); got != "172.19.0.5" {
+			t.Errorf("com Fly-Client-IP %q, ClientIP = %q, queria o fallback 172.19.0.5", valor, got)
+		}
+	}
+}
+
+// IPv4 mapeado em IPv6 vira o IPv4: o mesmo cliente não ganha dois baldes.
+func TestClientIPUnmapsFlyClientIP(t *testing.T) {
+	r := requisicaoDoFly("172.19.0.5:5555", "::ffff:198.51.100.7")
+	if got := middleware.ClientIP(r, fly(t)); got != "198.51.100.7" {
+		t.Errorf("ClientIP = %q, queria 198.51.100.7", got)
+	}
+}
+
+// TestRateLimitSeparatesClientsBehindFly é o dano real da pendência mais
+// antiga do projeto: no formato do Fly, sem o cabeçalho dedicado, todos os
+// clientes caem no balde do IP do app.
+func TestRateLimitSeparatesClientsBehindFly(t *testing.T) {
+	h := middleware.RateLimit(t.Context(), 60, 1, fly(t))(http.HandlerFunc(
+		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
+
+	for i, cliente := range []string{"198.51.100.1", "198.51.100.2", "198.51.100.3"} {
+		w := httptest.NewRecorder()
+		h.ServeHTTP(w, requisicaoDoFly("172.19.0.5:5555", cliente))
+		if w.Code != http.StatusOK {
+			t.Errorf("cliente %d (%s) recebeu %d: gastou o balde de outro cliente", i, cliente, w.Code)
+		}
+	}
+}
```

Crie `internal/httpapi/middleware/clientip_internal_test.go`:

```go
package middleware

import (
	"bytes"
	"log/slog"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// capturarAvisos troca o logger padrão e o relógio do aviso pelo tempo do
// teste. Não roda em paralelo: os dois são globais.
func capturarAvisos(t *testing.T, agora *time.Time) *bytes.Buffer {
	t.Helper()
	var log bytes.Buffer
	antigoLog, antigoAviso := slog.Default(), avisoDeProxy
	slog.SetDefault(slog.New(slog.NewTextHandler(&log, nil)))
	avisoDeProxy = &aviso{agora: func() time.Time { return *agora }, intervalo: time.Minute}
	t.Cleanup(func() { slog.SetDefault(antigoLog); avisoDeProxy = antigoAviso })
	return &log
}

// TestUntrustedHeaderWarnsAtMostOncePerMinute: com TRUSTED_PROXIES errado,
// TODA requisição cai no aviso. Sem o limite, o log vira só isso.
func TestUntrustedHeaderWarnsAtMostOncePerMinute(t *testing.T) {
	agora := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	log := capturarAvisos(t, &agora)
	px := Proxies{Trusted: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}, Header: "Fly-Client-IP"}

	pedir := func() {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "172.19.0.5:5555"
		r.Header.Set("Fly-Client-IP", "198.51.100.7")
		ClientIP(r, px)
	}
	for range 100 {
		pedir()
	}
	if n := strings.Count(log.String(), "cabeçalho de IP do cliente ignorado"); n != 1 {
		t.Fatalf("%d avisos em 100 requisições no mesmo minuto, queria 1", n)
	}
	if !strings.Contains(log.String(), "remote_addr=172.19.0.5") {
		t.Errorf("o aviso não traz o remote_addr, que é o que falta para configurar: %s", log.String())
	}

	agora = agora.Add(time.Minute)
	pedir()
	if n := strings.Count(log.String(), "cabeçalho de IP do cliente ignorado"); n != 2 {
		t.Errorf("%d avisos depois de um minuto, queria 2", n)
	}
}

// Sem CLIENT_IP_HEADER (desenvolvimento, compose), um X-Forwarded-For de
// origem qualquer não gera aviso: seria ruído em toda máquina de dev.
func TestNoWarningWithoutTheHeaderSetting(t *testing.T) {
	agora := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	log := capturarAvisos(t, &agora)

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.9:4444"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	r.Header.Set("Fly-Client-IP", "1.2.3.4")
	ClientIP(r, Proxies{})
	if log.Len() != 0 {
		t.Errorf("sem CLIENT_IP_HEADER saiu log: %s", log.String())
	}
}
```

Os testes que já existiam passam a montar `Proxies`. Aplique em `internal/httpapi/middleware/middleware_test.go`:

```diff
--- a/internal/httpapi/middleware/middleware_test.go
+++ b/internal/httpapi/middleware/middleware_test.go
@@ -130,7 +130,7 @@
 func TestRateLimitBlocksAfterBurst(t *testing.T) {
 	ctx, cancel := context.WithCancel(context.Background())
 	defer cancel()
-	h := middleware.RateLimit(ctx, 60, 3, nil)(http.HandlerFunc(ok))
+	h := middleware.RateLimit(ctx, 60, 3, middleware.Proxies{})(http.HandlerFunc(ok))
 
 	var last int
 	for i := 0; i < 10; i++ {
@@ -148,7 +148,7 @@
 func TestRateLimitIsPerClient(t *testing.T) {
 	ctx, cancel := context.WithCancel(context.Background())
 	defer cancel()
-	h := middleware.RateLimit(ctx, 60, 3, nil)(http.HandlerFunc(ok))
+	h := middleware.RateLimit(ctx, 60, 3, middleware.Proxies{})(http.HandlerFunc(ok))
 
 	for i := 0; i < 10; i++ {
 		req := httptest.NewRequest(http.MethodGet, "/", nil)
```

Aplique em `internal/httpapi/middleware/ratelimit_internal_test.go`:

```diff
--- a/internal/httpapi/middleware/ratelimit_internal_test.go
+++ b/internal/httpapi/middleware/ratelimit_internal_test.go
@@ -20,7 +20,7 @@
 // Por isso os dois saem juntos.
 func TestRateLimitCapsTheNumberOfBuckets(t *testing.T) {
 	const teto = 8
-	h := rateLimit(t.Context(), porMinuto(60), 5, porIP(nil), teto)(http.HandlerFunc(
+	h := rateLimit(t.Context(), porMinuto(60), 5, porIP(Proxies{}), teto)(http.HandlerFunc(
 		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
 
 	for i := range 500 {
@@ -38,7 +38,7 @@
 // bloco que um único assinante recebe, então dar um balde por endereço
 // entrega ao atacante bilhões de baldes de graça.
 func TestIPv6ClientsShareABucketPerSlash64(t *testing.T) {
-	h := rateLimit(t.Context(), porMinuto(60), 1, porIP(nil), 1000)(http.HandlerFunc(
+	h := rateLimit(t.Context(), porMinuto(60), 1, porIP(Proxies{}), 1000)(http.HandlerFunc(
 		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
 
 	var negados int
```

Aplique em `internal/httpapi/auth_integration_test.go`:

```diff
--- a/internal/httpapi/auth_integration_test.go
+++ b/internal/httpapi/auth_integration_test.go
@@ -881,6 +881,92 @@
 	}
 }
 
+// TestSessionsBehindFlyRecordDistinctOrigins é a mesma trava no formato do
+// Fly (D22 do Plano 7): o X-Forwarded-For termina no IP do app, igual para
+// todos, e o cliente vem em Fly-Client-IP. Passa pelo wiring inteiro:
+// config → Routes → AuthHandlers → ClientIP.
+func TestSessionsBehindFlyRecordDistinctOrigins(t *testing.T) {
+	a := newAPIWith(t, func(cfg *config.Config) {
+		cfg.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
+		cfg.ClientIPHeader = "Fly-Client-IP"
+	})
+
+	entrarComOrigem := func(origem string) {
+		t.Helper()
+		req, err := http.NewRequest(http.MethodPost, a.srv.URL+"/api/v1/auth/login",
+			jsonBody(t, map[string]string{"email": testEmail, "password": testPass}))
+		if err != nil {
+			t.Fatal(err)
+		}
+		req.Header.Set("Content-Type", "application/json")
+		req.Header.Set("X-Requested-With", "vigilume")
+		req.Header.Set("X-Forwarded-For", origem+", 66.241.124.63")
+		req.Header.Set("Fly-Client-IP", origem)
+		resp, err := http.DefaultClient.Do(req)
+		if err != nil {
+			t.Fatal(err)
+		}
+		defer resp.Body.Close()
+		if resp.StatusCode != http.StatusOK {
+			t.Fatalf("login devolveu %d", resp.StatusCode)
+		}
+	}
+
+	entrarComOrigem("198.51.100.1")
+	entrarComOrigem("203.0.113.42")
+
+	var distintos int
+	if err := a.pool.QueryRow(context.Background(),
+		`SELECT count(DISTINCT ip_hash) FROM sessions WHERE user_id = $1`, a.userID).
+		Scan(&distintos); err != nil {
+		t.Fatal(err)
+	}
+	if distintos != 2 {
+		t.Errorf("%d origens distintas gravadas em sessions.ip_hash, queria 2: "+
+			"no formato do Fly, o último salto do XFF é o app, e todo mundo vira um só", distintos)
+	}
+}
+
+// TestPasswordChangeBehindFlyAuditsTheClient: as rotas da conta (Plano 5)
+// também gravam ip_hash, na auditoria. Cada grupo de handlers recebe os
+// proxies do Routes por um caminho próprio; o teste do login não trava este.
+func TestPasswordChangeBehindFlyAuditsTheClient(t *testing.T) {
+	a := newAPIWith(t, func(cfg *config.Config) {
+		cfg.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
+		cfg.ClientIPHeader = "Fly-Client-IP"
+	})
+	_, token := entrar(t, a)
+
+	req, err := http.NewRequest(http.MethodPost, a.srv.URL+"/api/v1/auth/password",
+		jsonBody(t, map[string]string{"current_password": testPass, "new_password": "outra-senha-bem-comprida"}))
+	if err != nil {
+		t.Fatal(err)
+	}
+	req.Header.Set("Content-Type", "application/json")
+	req.Header.Set("X-Requested-With", "vigilume")
+	req.Header.Set("Authorization", "Bearer "+token)
+	req.Header.Set("X-Forwarded-For", "198.51.100.9, 66.241.124.63")
+	req.Header.Set("Fly-Client-IP", "198.51.100.9")
+	resp, err := http.DefaultClient.Do(req)
+	if err != nil {
+		t.Fatal(err)
+	}
+	resp.Body.Close()
+	if resp.StatusCode != http.StatusNoContent {
+		t.Fatalf("troca devolveu %d, queria 204", resp.StatusCode)
+	}
+
+	var gravado []byte
+	if err := a.pool.QueryRow(context.Background(),
+		`SELECT ip_hash FROM audit_log WHERE user_id = $1 AND action = 'user.password_changed'`,
+		a.userID).Scan(&gravado); err != nil {
+		t.Fatal(err)
+	}
+	if !bytes.Equal(gravado, crypto.HashIP(testIPHashKey, "198.51.100.9")) {
+		t.Error("a auditoria da troca de senha não gravou o hash do Fly-Client-IP")
+	}
+}
+
 // TestTheMiddlewareChainIsActuallyWired trava o wiring, e não os middlewares.
 //
 // Os testes de middleware montam cadeias À MÃO, então provam cada peça e
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/config/ ./internal/httpapi/...`
Expected: FAIL de compilação: `cfg.ClientIPHeader undefined (type *Config has no field or method ClientIPHeader)` e `undefined: Proxies`.

- [ ] **Step 3: A configuração**

Aplique em `internal/config/config.go`:

```diff
--- a/internal/config/config.go
+++ b/internal/config/config.go
@@ -10,6 +10,7 @@
 	"fmt"
 	"log/slog"
 	"net"
+	"net/http"
 	"net/mail"
 	"net/netip"
 	"strings"
@@ -43,6 +44,10 @@
 	JWTSecret      []byte
 	IPHashKey      []byte
 	TrustedProxies []netip.Prefix
+	// ClientIPHeader é o cabeçalho de um valor só em que o proxy da
+	// plataforma escreve o IP do cliente (no Fly, "Fly-Client-IP"). Vazio,
+	// vale o último salto do X-Forwarded-For. Só é lido de TRUSTED_PROXIES.
+	ClientIPHeader string
 	LogLevel       slog.Level
 	ShutdownGrace  time.Duration
 
@@ -142,6 +147,25 @@
 		return ""
 	}
 
+	// CLIENT_IP_HEADER é um nome de cabeçalho HTTP, e de um valor só. O
+	// X-Forwarded-For é recusado aqui porque tem vários valores e já é o
+	// comportamento padrão (variável vazia).
+	clientIPHeader := func(name string) string {
+		raw := strings.TrimSpace(getenv(name))
+		if raw == "" {
+			return ""
+		}
+		if !nomeDeCabecalho(raw) {
+			errs = append(errs, fmt.Errorf("%s %q não é um nome de cabeçalho HTTP", name, raw))
+			return ""
+		}
+		if strings.EqualFold(raw, "X-Forwarded-For") {
+			errs = append(errs, fmt.Errorf("%s: deixe vazio para usar o X-Forwarded-For; a variável é para um cabeçalho de um valor só, como Fly-Client-IP", name))
+			return ""
+		}
+		return http.CanonicalHeaderKey(raw)
+	}
+
 	cfg := &Config{
 		HTTPAddr:       opt("HTTP_ADDR", ":8080"),
 		MetricsAddr:    metricsAddr("METRICS_ADDR"),
@@ -152,6 +176,7 @@
 		JWTSecret:      key("JWT_SECRET"),
 		IPHashKey:      key("IP_HASH_KEY"),
 		TrustedProxies: proxies("TRUSTED_PROXIES"),
+		ClientIPHeader: clientIPHeader("CLIENT_IP_HEADER"),
 		ShutdownGrace:  15 * time.Second,
 		ResendAPIKey:   strings.TrimSpace(getenv("RESEND_API_KEY")),
 		EmailFrom:      strings.TrimSpace(getenv("EMAIL_FROM")),
@@ -206,6 +231,18 @@
 	return cfg, nil
 }
 
+// nomeDeCabecalho aceita só os caracteres de "token" da RFC 9110 que fazem
+// sentido num nome de cabeçalho: letras, dígitos e hífen.
+func nomeDeCabecalho(s string) bool {
+	for _, c := range s {
+		ok := c == '-' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
+		if !ok {
+			return false
+		}
+	}
+	return true
+}
+
 // LogValue garante que registrar a configuração nunca imprime material de
 // chave nem a string de conexão (que embute a senha do banco). É a mesma
 // estratégia do tipo crypto.Secret: a proteção é do tipo, não da disciplina.
@@ -216,6 +253,7 @@
 		slog.String("metrics_addr", c.MetricsAddr),
 		slog.String("public_base_url", c.PublicBaseURL),
 		slog.String("cors_origin", c.CORSOrigin),
+		slog.String("client_ip_header", c.ClientIPHeader),
 		slog.String("log_level", c.LogLevel.String()),
 		slog.String("database_url", "[REDACTED]"),
 		slog.String("master_key", "[REDACTED]"),
```

- [ ] **Step 4: O `ClientIP` e os limitadores**

Aplique em `internal/httpapi/middleware/clientip.go`:

```diff
--- a/internal/httpapi/middleware/clientip.go
+++ b/internal/httpapi/middleware/clientip.go
@@ -1,41 +1,59 @@
 package middleware
 
 import (
+	"log/slog"
 	"net"
 	"net/http"
 	"net/netip"
 	"strings"
+	"sync"
+	"time"
 )
 
-// ClientIP devolve o endereço do cliente, considerando X-Forwarded-For
-// APENAS quando a requisição chega de um proxy confiável.
+// Proxies diz em quem confiar e qual cabeçalho ler para achar o cliente.
 //
-// A lista de confiáveis não é burocracia: X-Forwarded-For é um cabeçalho de
-// REQUISIÇÃO, escolhido por quem a envia. Acreditar nele sem saber de quem
-// veio seria pior que ignorá-lo. Hoje, atrás do proxy, a falha é FECHADA —
-// todo mundo divide um balde, o que é ruim mas simétrico e visível. Com XFF
-// cego a falha seria ABERTA: o atacante escolheria um balde novo por
-// requisição, o limitador viraria zero, e o ip_hash da auditoria passaria a
-// atribuir origem falsa a terceiros.
+// Header vazio mantém o comportamento original: o último salto do
+// X-Forwarded-For. Preenchido (no Fly, "Fly-Client-IP"), vale esse cabeçalho,
+// e só ele. O motivo é o formato do Fly: o proxy dele ANEXA ao X-Forwarded-For
+// o IP do próprio app (o anycast em que o cliente bateu), e o último salto
+// passa a ser o mesmo para todo mundo. Com o cabeçalho dedicado, o proxy
+// escreve um valor só: o do cliente.
+type Proxies struct {
+	Trusted []netip.Prefix
+	Header  string
+}
+
+// ClientIP devolve o endereço do cliente, considerando os cabeçalhos de proxy
+// APENAS quando a requisição chega de um proxy confiável.
 //
-// Quando a origem é confiável, vale o ÚLTIMO salto, não o primeiro. O proxy
-// da plataforma ANEXA o endereço observado ao fim da cadeia, então o último
-// elemento é o único que ele escreveu; tudo antes dele veio do cliente e vale
-// tanto quanto o cliente.
-func ClientIP(r *http.Request, trusted []netip.Prefix) string {
+// A lista de confiáveis não é burocracia: X-Forwarded-For e Fly-Client-IP são
+// cabeçalhos de REQUISIÇÃO, escolhidos por quem a envia. Acreditar neles sem
+// saber de quem veio seria pior que ignorá-los. Sem a lista, a falha é
+// FECHADA — todo mundo divide um balde, o que é ruim mas simétrico e
+// visível. Com o cabeçalho cego a falha seria ABERTA: o atacante escolheria
+// um balde novo por requisição, o limitador viraria zero, e o ip_hash da
+// auditoria passaria a atribuir origem falsa a terceiros.
+func ClientIP(r *http.Request, p Proxies) string {
 	direto, _, err := net.SplitHostPort(r.RemoteAddr)
 	if err != nil {
 		direto = r.RemoteAddr
 	}
-	if len(trusted) == 0 {
+	if p.Header != "" {
+		return peloCabecalho(r, p, direto)
+	}
+	if len(p.Trusted) == 0 {
 		return direto
 	}
 
 	origem, err := netip.ParseAddr(direto)
-	if err != nil || !confiavel(origem, trusted) {
+	if err != nil || !confiavel(origem, p.Trusted) {
 		return direto
 	}
 
+	// Quando a origem é confiável, vale o ÚLTIMO salto, não o primeiro. Um
+	// proxy que anexa o endereço observado ao fim da cadeia escreveu só o
+	// último elemento; tudo antes dele veio do cliente e vale tanto quanto o
+	// cliente. (No Fly o último é o IP do app: por isso lá se usa o Header.)
 	cadeia := r.Header.Get("X-Forwarded-For")
 	if cadeia == "" {
 		return direto
@@ -50,6 +68,27 @@
 	return ultimo
 }
 
+// peloCabecalho lê o cabeçalho de cliente de um valor só (Fly-Client-IP).
+func peloCabecalho(r *http.Request, p Proxies, direto string) string {
+	valor := strings.TrimSpace(r.Header.Get(p.Header))
+	if valor == "" {
+		return direto
+	}
+	origem, err := netip.ParseAddr(direto)
+	if err != nil || !confiavel(origem, p.Trusted) {
+		// O cabeçalho chegou, mas não de um proxy da lista. Ou é alguém
+		// forjando, ou é TRUSTED_PROXIES errado; o aviso diz qual é o
+		// endereço que a conexão tem, que é o que falta para configurar.
+		avisoDeProxy.avisar(direto, p.Header)
+		return direto
+	}
+	ip, err := netip.ParseAddr(valor)
+	if err != nil || ip.Zone() != "" {
+		return direto
+	}
+	return ip.Unmap().String()
+}
+
 func confiavel(ip netip.Addr, trusted []netip.Prefix) bool {
 	ip = ip.Unmap()
 	for _, p := range trusted {
@@ -59,3 +98,30 @@
 	}
 	return false
 }
+
+// avisoDeProxy é global de propósito: o aviso é por processo, não por
+// cadeia de rotas. Quatro limitadores com um aviso cada inundariam o log com
+// a mesma frase.
+var avisoDeProxy = &aviso{agora: time.Now, intervalo: time.Minute}
+
+// aviso limita o log de "cabeçalho de proxy ignorado" a um por intervalo.
+// Com TRUSTED_PROXIES errado, TODA requisição cairia aqui.
+type aviso struct {
+	mu        sync.Mutex
+	ultimo    time.Time
+	agora     func() time.Time
+	intervalo time.Duration
+}
+
+func (a *aviso) avisar(remoto, cabecalho string) {
+	a.mu.Lock()
+	agora := a.agora()
+	if !a.ultimo.IsZero() && agora.Sub(a.ultimo) < a.intervalo {
+		a.mu.Unlock()
+		return
+	}
+	a.ultimo = agora
+	a.mu.Unlock()
+	slog.Warn("cabeçalho de IP do cliente ignorado: a conexão não vem de TRUSTED_PROXIES",
+		"cabecalho", cabecalho, "remote_addr", remoto)
+}
```

Aplique em `internal/httpapi/middleware/ratelimit.go`:

```diff
--- a/internal/httpapi/middleware/ratelimit.go
+++ b/internal/httpapi/middleware/ratelimit.go
@@ -30,8 +30,8 @@
 // um jeito explícito de terminar; amarrar isso ao contexto do servidor faz
 // a goroutine morrer no shutdown gracioso junto com o resto, em vez de
 // vazar para sempre (R32).
-func RateLimit(ctx context.Context, perMinute, burst int, trusted []netip.Prefix) func(http.Handler) http.Handler {
-	return rateLimit(ctx, porMinuto(perMinute), burst, porIP(trusted), maxBuckets)
+func RateLimit(ctx context.Context, perMinute, burst int, px Proxies) func(http.Handler) http.Handler {
+	return rateLimit(ctx, porMinuto(perMinute), burst, porIP(px), maxBuckets)
 }
 
 // RateLimitUser limita por usuário autenticado, a segunda camada da spec
@@ -52,9 +52,9 @@
 
 func porMinuto(n int) rate.Limit { return rate.Limit(float64(n) / 60.0) }
 
-func porIP(trusted []netip.Prefix) chaveFn {
+func porIP(px Proxies) chaveFn {
 	return func(r *http.Request) (string, bool) {
-		return chaveDe(ClientIP(r, trusted)), true
+		return chaveDe(ClientIP(r, px)), true
 	}
 }
 
```

- [ ] **Step 5: O wiring**

Aplique em `internal/httpapi/routes.go`:

```diff
--- a/internal/httpapi/routes.go
+++ b/internal/httpapi/routes.go
@@ -66,12 +66,12 @@
 	// simplesmente não o guardaria, e o desenvolvimento local pararia. Em
 	// produção PUBLIC_BASE_URL é https, e o refresh nunca trafega em claro.
 	secureCookie := strings.HasPrefix(cfg.PublicBaseURL, "https://")
-	h := NewAuthHandlers(d.Store, d.Sessions, d.TOTP, d.Clock, cfg.JWTSecret, secureCookie, cfg.TrustedProxies)
+	h := NewAuthHandlers(d.Store, d.Sessions, d.TOTP, d.Clock, cfg.JWTSecret, secureCookie, proxiesDe(cfg))
 	th := NewTargetHandlers(d.Targets, d.Clock)
 	ih := NewIncidentHandlers(d.Incidents)
 	ch := NewChannelHandlers(d.Channels)
 	sp := NewStatusPageHandlers(d.StatusPages, cfg.PublicBaseURL)
-	ah := NewAccountHandlers(d.Accounts, cfg.TrustedProxies, secureCookie)
+	ah := NewAccountHandlers(d.Accounts, proxiesDe(cfg), secureCookie)
 	authn := middleware.Authn(cfg.JWTSecret, d.Clock)
 
 	mux := http.NewServeMux()
@@ -96,7 +96,7 @@
 	// login (D5 do Plano 5; contrato dos Planos 5–7, "mesmo limite por IP da
 	// rota de login"). Com instâncias separadas, quem adivinha senha dobraria
 	// o orçamento alternando as duas rotas.
-	limiteLogin := middleware.RateLimit(ctx, 10, 5, cfg.TrustedProxies)
+	limiteLogin := middleware.RateLimit(ctx, 10, 5, proxiesDe(cfg))
 	mux.Handle("POST /api/v1/auth/login", limiteLogin(http.HandlerFunc(h.Login)))
 	mux.Handle("POST /api/v1/auth/password", limiteLogin(authn(http.HandlerFunc(ah.ChangePassword))))
 	mux.Handle("POST /api/v1/auth/refresh", http.HandlerFunc(h.Refresh))
@@ -115,7 +115,7 @@
 	// se aplica — a senha já foi acertada, o que resta é adivinhar seis
 	// dígitos.
 	mux.Handle("POST /api/v1/auth/totp/verify",
-		middleware.RateLimit(ctx, 20, 5, cfg.TrustedProxies)(http.HandlerFunc(h.TOTPVerify)))
+		middleware.RateLimit(ctx, 20, 5, proxiesDe(cfg))(http.HandlerFunc(h.TOTPVerify)))
 
 	// Rotas de recurso: Authn primeiro, depois o limite por USUÁRIO (spec
 	// §5.7), que precisa do usuário que Authn põe no contexto. Uma instância
@@ -178,7 +178,7 @@
 	// CORS continuam por fora do limitador, em middleware.CORS, para que o
 	// 429 também os leve (R33).
 	handler = middleware.Preflight()(handler)
-	handler = middleware.RateLimit(ctx, 120, 30, cfg.TrustedProxies)(handler)
+	handler = middleware.RateLimit(ctx, 120, 30, proxiesDe(cfg))(handler)
 	handler = middleware.CORS(cfg.CORSOrigin)(handler)
 	handler = middleware.SecurityHeaders(handler)
 	handler = middleware.Recover(handler)
@@ -222,6 +222,13 @@
 // Routes() em quase todo teste.
 var painelEmbutido = sync.OnceValue(func() *webui.Painel { return webui.New(webui.Dist()) })
 
+// proxiesDe junta as duas variáveis que decidem de onde vem o IP do cliente.
+// Todo limitador por IP e o ip_hash da auditoria passam por aqui: um lugar
+// só para errar ou acertar.
+func proxiesDe(cfg *config.Config) middleware.Proxies {
+	return middleware.Proxies{Trusted: cfg.TrustedProxies, Header: cfg.ClientIPHeader}
+}
+
 // readyzTimeout limita o ping: um banco que não responde tem de virar 503
 // rápido, e não segurar o health check do orquestrador.
 const readyzTimeout = 2 * time.Second
@@ -260,7 +267,7 @@
 
 	var h http.Handler = mux
 	h = middleware.BodyLimit(verifyBodyBytes)(h)
-	h = middleware.RateLimit(ctx, 20, 10, cfg.TrustedProxies)(h)
+	h = middleware.RateLimit(ctx, 20, 10, proxiesDe(cfg))(h)
 	h = middleware.SecurityHeaders(h)
 	h = middleware.Recover(h)
 	h = middleware.RequestID(h)
```

Aplique em `internal/httpapi/publicpage.go`:

```diff
--- a/internal/httpapi/publicpage.go
+++ b/internal/httpapi/publicpage.go
@@ -50,7 +50,7 @@
 	mux.HandleFunc("GET /p/{slug}", (&PublicPageHandlers{svc: svc}).Page)
 
 	var h http.Handler = mux
-	h = middleware.RateLimit(ctx, publicPorMinuto, publicBurst, cfg.TrustedProxies)(h)
+	h = middleware.RateLimit(ctx, publicPorMinuto, publicBurst, proxiesDe(cfg))(h)
 	h = middleware.SecurityHeaders(h)
 	h = middleware.Recover(h)
 	h = middleware.RequestID(h)
```

Aplique em `internal/httpapi/authhandlers.go`:

```diff
--- a/internal/httpapi/authhandlers.go
+++ b/internal/httpapi/authhandlers.go
@@ -6,7 +6,6 @@
 	"io"
 	"log/slog"
 	"net/http"
-	"net/netip"
 	"time"
 
 	"github.com/google/uuid"
@@ -27,14 +26,14 @@
 	clk          clock.Clock
 	jwtSecret    []byte
 	secureCookie bool
-	// trustedProxies decide se o X-Forwarded-For desta requisição vale. Ver
+	// proxies decide se os cabeçalhos de proxy desta requisição valem. Ver
 	// middleware.ClientIP: sem a lista, acreditar no cabeçalho seria pior que
 	// ignorá-lo.
-	trustedProxies []netip.Prefix
+	proxies middleware.Proxies
 }
 
-func NewAuthHandlers(users auth.UserStore, sessions *auth.Manager, totp *auth.TOTP, clk clock.Clock, jwtSecret []byte, secureCookie bool, trustedProxies []netip.Prefix) *AuthHandlers {
-	return &AuthHandlers{users: users, sessions: sessions, totp: totp, clk: clk, jwtSecret: jwtSecret, secureCookie: secureCookie, trustedProxies: trustedProxies}
+func NewAuthHandlers(users auth.UserStore, sessions *auth.Manager, totp *auth.TOTP, clk clock.Clock, jwtSecret []byte, secureCookie bool, proxies middleware.Proxies) *AuthHandlers {
+	return &AuthHandlers{users: users, sessions: sessions, totp: totp, clk: clk, jwtSecret: jwtSecret, secureCookie: secureCookie, proxies: proxies}
 }
 
 type loginRequest struct {
@@ -275,7 +274,7 @@
 // contrário do rate limit, esse dano não tem volta: linha já gravada com o
 // hash errado não se recupera.
 func (h *AuthHandlers) meta(r *http.Request) auth.Meta {
-	return auth.Meta{IP: middleware.ClientIP(r, h.trustedProxies), UserAgent: r.UserAgent()}
+	return auth.Meta{IP: middleware.ClientIP(r, h.proxies), UserAgent: r.UserAgent()}
 }
 
 // internal registra o erro real e devolve ao cliente apenas o envelope
```

Aplique em `internal/httpapi/accounthandlers.go`:

```diff
--- a/internal/httpapi/accounthandlers.go
+++ b/internal/httpapi/accounthandlers.go
@@ -4,7 +4,6 @@
 	"errors"
 	"log/slog"
 	"net/http"
-	"net/netip"
 	"time"
 
 	"github.com/google/uuid"
@@ -19,15 +18,15 @@
 // AccountHandlers são as rotas da conta do dono (Plano 5): GET /me, POST
 // /auth/password, GET /sessions e DELETE /sessions/{id}.
 type AccountHandlers struct {
-	svc            *account.Service
-	trustedProxies []netip.Prefix
+	svc     *account.Service
+	proxies middleware.Proxies
 	// secureCookie: o mesmo critério do AuthHandlers, para apagar o cookie de
 	// refresh quando a sessão encerrada é a atual.
 	secureCookie bool
 }
 
-func NewAccountHandlers(svc *account.Service, trustedProxies []netip.Prefix, secureCookie bool) *AccountHandlers {
-	return &AccountHandlers{svc: svc, trustedProxies: trustedProxies, secureCookie: secureCookie}
+func NewAccountHandlers(svc *account.Service, proxies middleware.Proxies, secureCookie bool) *AccountHandlers {
+	return &AccountHandlers{svc: svc, proxies: proxies, secureCookie: secureCookie}
 }
 
 // meResponse é construído campo a campo: nada de hash, bloqueio ou segredo.
@@ -87,7 +86,7 @@
 
 	err := h.svc.ChangePassword(r.Context(), userID, familyID, account.ChangePasswordInput{
 		Current: req.CurrentPassword, New: req.NewPassword, TOTPCode: req.TOTPCode,
-	}, auth.Meta{IP: middleware.ClientIP(r, h.trustedProxies), UserAgent: r.UserAgent()})
+	}, auth.Meta{IP: middleware.ClientIP(r, h.proxies), UserAgent: r.UserAgent()})
 	var pe account.PolicyError
 	switch {
 	case errors.As(err, &pe):
@@ -189,7 +188,7 @@
 		return
 	}
 	err := h.svc.RevokeSession(r.Context(), userID, alvo,
-		auth.Meta{IP: middleware.ClientIP(r, h.trustedProxies), UserAgent: r.UserAgent()})
+		auth.Meta{IP: middleware.ClientIP(r, h.proxies), UserAgent: r.UserAgent()})
 	if errors.Is(err, account.ErrSessionNotFound) {
 		apierr.WriteError(w, r, apierr.CodeNotFound)
 		return
```

- [ ] **Step 6: Rodar e ver passar**

Run: `gofmt -l internal/ && go vet ./... && go test -race -count=1 ./internal/config/ ./internal/httpapi/...`
Expected: PASS, e o `gofmt -l` sem saída.

Run: `go test -race -count=1 ./...`
Expected: PASS.

- [ ] **Step 7: Sabotagem**

Uma de cada vez; rode o teste indicado; restaure. Todas compilam (uma sabotagem que não compila não prova nada).
1. Em `peloCabecalho`, troque `if err != nil || !confiavel(origem, p.Trusted) {` por `if err != nil || !confiavel(origem, p.Trusted) && p.Header == "" {` (o `Header` nunca é vazio ali: é tirar a checagem de origem). Run: `go test -count=1 ./internal/httpapi/middleware/`. Expected: FAIL em `TestClientIPIgnoresAForgedFlyClientIP` (e no teste do aviso, que deixa de disparar).
2. Em `ClientIP`, troque `if p.Header != "" {` por `if p.Header == "-" {`. Expected: FAIL em `TestClientIPReadsFlyClientIPFromATrustedProxy`, `TestRateLimitSeparatesClientsBehindFly` e mais três.
3. Em `peloCabecalho`, no `if err != nil || ip.Zone() != "" {`, troque `return direto` por `return valor`. Expected: FAIL em `TestClientIPFallsBackWhenFlyClientIPIsUnusable`.
4. Troque `return ip.Unmap().String()` por `return ip.String()`. Expected: FAIL em `TestClientIPUnmapsFlyClientIP`.
5. Em `aviso.avisar`, troque `agora.Sub(a.ultimo) < a.intervalo` por `agora.Sub(a.ultimo) < 0`. Expected: FAIL em `TestUntrustedHeaderWarnsAtMostOncePerMinute`.
6. Em `proxiesDe`, troque `middleware.Proxies{Trusted: cfg.TrustedProxies, Header: cfg.ClientIPHeader}` por `middleware.Proxies{Trusted: cfg.TrustedProxies}`. Run: `go test -count=1 -run BehindFly ./internal/httpapi/`. Expected: FAIL em `TestSessionsBehindFlyRecordDistinctOrigins` e `TestPasswordChangeBehindFlyAuditsTheClient`.
7. Em `Routes`, troque `NewAccountHandlers(d.Accounts, proxiesDe(cfg), secureCookie)` por `NewAccountHandlers(d.Accounts, middleware.Proxies{Trusted: cfg.TrustedProxies}, secureCookie)`. Expected: FAIL **só** em `TestPasswordChangeBehindFlyAuditsTheClient` (o do login continua verde: é por isso que o teste da conta existe).
8. Em `config.go`, troque `if strings.EqualFold(raw, "X-Forwarded-For") {` por `if strings.EqualFold(raw, "-") {`. Run: `go test -count=1 ./internal/config/`. Expected: FAIL em `TestLoadRejectsABadClientIPHeader`.

`TestClientIPWithoutTheHeaderSettingCollapsesBehindFly` não tem sabotagem: ele documenta o defeito (sem a variável, o formato do Fly vira um balde só) e prova que o padrão não mudou.

- [ ] **Step 8: Commit**

```bash
git add internal/config/ internal/httpapi/
git commit -m "fix(httpapi): IP do cliente pelo Fly-Client-IP, só de TRUSTED_PROXIES (CLIENT_IP_HEADER)"
```

---

### Task 2: O boot recusa as chaves de demonstração com https

O `compose.yaml` (Task 6) publica no repositório três chaves fixas, de propósito: gerar chaves no primeiro `up` exigiria outro contêiner com shell e um volume compartilhado (D13 da spec). O risco é alguém copiar o compose para produção e subir com chaves que todo mundo com acesso ao repositório conhece, e com elas forjar JWT e decifrar os segredos gravados. A trava: com `PUBLIC_BASE_URL` https (o sinal de produção), o boot recusa qualquer uma das três (D14).

**Files:**
- Modify: `internal/config/config.go`, `internal/config/config_test.go`

**Interfaces:**
- Consumes: `config.Load`, `keyLen` (32 bytes).
- Produces: `var chavesDeDemo [][]byte` (as três chaves, em texto: `demo-vigilume-MASTER_KEY-naouse!`, `demo-vigilume-JWT_SECRET-naouse!`, `demo-vigilume-IP_HASH_KEY-naouse`); a Task 6 as usa em base64 no compose, e `TestComposeUsesTheDemoKeys` amarra os dois lados.

**Contexto para quem implementa.** A recusa compara **qualquer** das três chaves com **cada** variável: trocar as chaves de lugar no compose não escapa. As três têm exatamente 32 bytes; senão o compose nem subiria, e a recusa nunca seria exercida por quem copiou o arquivo.

- [ ] **Step 1: Escrever os testes (falham)**

Aplique em `internal/config/config_test.go`:

```diff
--- a/internal/config/config_test.go
+++ b/internal/config/config_test.go
@@ -281,3 +281,38 @@
 		}
 	}
 }
+
+// As chaves de demonstração do compose.yaml são públicas no repositório. Com
+// elas em produção, qualquer um forja JWT e decifra os segredos.
+func TestLoadRejectsDemoKeysOverHTTPS(t *testing.T) {
+	for i, nome := range []string{"MASTER_KEY", "JWT_SECRET", "IP_HASH_KEY"} {
+		env := validEnv()
+		// Cada variável com a chave de demo de OUTRA: embaralhar não escapa.
+		env[nome] = base64.StdEncoding.EncodeToString(chavesDeDemo[(i+1)%len(chavesDeDemo)])
+		_, err := Load(getenvFrom(env))
+		if err == nil || !strings.Contains(err.Error(), nome) || !strings.Contains(err.Error(), "demonstração") {
+			t.Errorf("%s com chave de demo e https: erro = %v, queria recusa", nome, err)
+		}
+	}
+}
+
+func TestLoadAcceptsDemoKeysOverHTTP(t *testing.T) {
+	env := validEnv()
+	env["PUBLIC_BASE_URL"] = "http://localhost:8080"
+	env["MASTER_KEY"] = base64.StdEncoding.EncodeToString(chavesDeDemo[0])
+	env["JWT_SECRET"] = base64.StdEncoding.EncodeToString(chavesDeDemo[1])
+	env["IP_HASH_KEY"] = base64.StdEncoding.EncodeToString(chavesDeDemo[2])
+	if _, err := Load(getenvFrom(env)); err != nil {
+		t.Errorf("chaves de demo com http (o compose) deveriam subir: %v", err)
+	}
+}
+
+// As chaves de demo têm de ter o tamanho certo: senão o compose nem sobe, e
+// a recusa acima nunca seria exercida por quem copiou o arquivo.
+func TestDemoKeysHaveTheRequiredLength(t *testing.T) {
+	for i, k := range chavesDeDemo {
+		if len(k) != keyLen {
+			t.Errorf("chave de demo %d tem %d bytes, queria %d", i, len(k), keyLen)
+		}
+	}
+}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/config/`
Expected: FAIL de compilação: `undefined: chavesDeDemo`.

- [ ] **Step 3: A recusa**

Aplique em `internal/config/config.go`:

```diff
--- a/internal/config/config.go
+++ b/internal/config/config.go
@@ -5,6 +5,7 @@
 package config
 
 import (
+	"bytes"
 	"encoding/base64"
 	"errors"
 	"fmt"
@@ -29,6 +30,15 @@
 func (r Role) RunsAPI() bool    { return r == RoleAll || r == RoleAPI }
 func (r Role) RunsProber() bool { return r == RoleAll || r == RoleProber }
 
+// chavesDeDemo são as chaves que o compose.yaml publica, de propósito, no
+// repositório. Com elas, qualquer um forja JWT e decifra os segredos. O boot
+// as recusa quando PUBLIC_BASE_URL é https, que é o sinal de produção.
+var chavesDeDemo = [][]byte{
+	[]byte("demo-vigilume-MASTER_KEY-naouse!"),
+	[]byte("demo-vigilume-JWT_SECRET-naouse!"),
+	[]byte("demo-vigilume-IP_HASH_KEY-naouse"),
+}
+
 // keyLen é o tamanho exigido para toda chave simétrica: 32 bytes (AES-256,
 // HMAC-SHA256).
 const keyLen = 32
@@ -205,6 +215,18 @@
 			"colapsam num valor único para todos os clientes")
 	}
 
+	if strings.HasPrefix(cfg.PublicBaseURL, "https://") {
+		for _, k := range []struct {
+			nome  string
+			valor []byte
+		}{{"MASTER_KEY", cfg.MasterKey}, {"JWT_SECRET", cfg.JWTSecret}, {"IP_HASH_KEY", cfg.IPHashKey}} {
+			if chaveDeDemo(k.valor) {
+				errs = append(errs, fmt.Errorf("%s é uma chave de demonstração do compose.yaml, pública no repositório; "+
+					"gere uma com `openssl rand -base64 32`", k.nome))
+			}
+		}
+	}
+
 	switch role := Role(opt("ROLE", string(RoleAll))); role {
 	case RoleAll, RoleAPI, RoleProber:
 		cfg.Role = role
@@ -231,6 +253,15 @@
 	return cfg, nil
 }
 
+func chaveDeDemo(k []byte) bool {
+	for _, d := range chavesDeDemo {
+		if bytes.Equal(k, d) {
+			return true
+		}
+	}
+	return false
+}
+
 // nomeDeCabecalho aceita só os caracteres de "token" da RFC 9110 que fazem
 // sentido num nome de cabeçalho: letras, dígitos e hífen.
 func nomeDeCabecalho(s string) bool {
```

- [ ] **Step 4: Rodar e ver passar**

Run: `gofmt -l internal/ && go vet ./internal/config/ && go test -race -count=1 ./internal/config/`
Expected: PASS.

- [ ] **Step 5: Sabotagem**

Uma de cada vez; restaure:
1. Troque `if strings.HasPrefix(cfg.PublicBaseURL, "https://") {` (o da recusa, antes do `for`) por `if strings.HasPrefix(cfg.PublicBaseURL, "https://nunca") {`. Expected: FAIL em `TestLoadRejectsDemoKeysOverHTTPS`.
2. Troque o mesmo `"https://"` por `"http"` (recusa também em http). Expected: FAIL em `TestLoadAcceptsDemoKeysOverHTTP`: o compose deixaria de subir.

- [ ] **Step 6: Commit**

```bash
git add internal/config/
git commit -m "feat(config): recusa as chaves de demonstração do compose quando PUBLIC_BASE_URL é https"
```

---

### Task 3: `vigilume healthcheck` e `vigilume version`

A imagem final não tem shell nem `curl` (spec §5.9), e o compose precisa saber quando o app está pronto: o próprio binário vira a ferramenta (D5). `healthcheck` faz GET no `/readyz` do próprio processo, em loopback, com prazo de 2 s, e sai 0 ou 1. `version` imprime a versão carimbada no build (`-X main.version`), que também passa a sair no log `iniciando` e em nenhuma resposta HTTP (D4). Os dois entram no mapa `subcomandos` que o Plano 5 criou para o `user` (contrato, Reconciliação 6).

**Files:**
- Create: `cmd/vigilume/ops.go`, `cmd/vigilume/ops_test.go`
- Modify: `cmd/vigilume/main.go`

**Interfaces:**
- Consumes: `subcomandos` e o `main` do Plano 5 (o primeiro argumento escolhe o subcomando antes do `flag.Parse`; o código de saída volta da função).
- Produces:
  - `var version = "dev"` (package `main`), carimbada por `-ldflags "-X main.version=…"` (Tasks 5 e 7).
  - `func runVersion(stdout io.Writer) int`.
  - `func runHealthcheck(ctx context.Context, stderr io.Writer, getenv func(string) string) int` — lê só `HTTP_ADDR` (padrão `:8080`).
  - `func urlDeProntidao(httpAddr string) (string, error)`, `func checar(ctx, url string, prazo time.Duration) error`.

**Contexto para quem implementa.** O `/readyz` (e não o `/healthz`) porque é ele que olha o banco: o compose não deve marcar `healthy` um app sem banco. O host do `HTTP_ADDR` não interessa: o healthcheck roda dentro do contêiner, e o servidor escuta em todas as interfaces. `TestHealthcheckGivesUpOnASilentServer` tem **teto próprio** (1 s para um prazo de 100 ms): na primeira versão deste trabalho, a sabotagem "sem prazo" **travou a suíte** em vez de ficar vermelha — o teste esperava o servidor desistir, e o servidor esperava o cliente. Um teste de espera precisa de um teto que não dependa do código testado.

- [ ] **Step 1: Escrever os testes (falham)**

Crie `cmd/vigilume/ops_test.go`:

```go
package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func servidorDeProntidao(t *testing.T, h http.HandlerFunc) (httpAddr string) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	_, porta, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return ":" + porta
}

func healthcheck(t *testing.T, httpAddr string) (int, string) {
	t.Helper()
	var errb bytes.Buffer
	env := map[string]string{"HTTP_ADDR": httpAddr}
	codigo := runHealthcheck(context.Background(), &errb, func(k string) string { return env[k] })
	return codigo, errb.String()
}

func TestHealthcheckPassesOnlyWithReadyzOK(t *testing.T) {
	var caminho string
	addr := servidorDeProntidao(t, func(w http.ResponseWriter, r *http.Request) {
		caminho = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	if codigo, errb := healthcheck(t, addr); codigo != 0 {
		t.Fatalf("readyz 200: código %d (%s), queria 0", codigo, errb)
	}
	if caminho != "/readyz" {
		t.Errorf("o healthcheck bateu em %q, queria /readyz (o que olha o banco)", caminho)
	}
}

func TestHealthcheckFailsWhenNotReady(t *testing.T) {
	addr := servidorDeProntidao(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	codigo, errb := healthcheck(t, addr)
	if codigo != 1 || !strings.Contains(errb, "503") {
		t.Errorf("readyz 503: código %d, stderr %q; queria 1 citando o 503", codigo, errb)
	}
}

func TestHealthcheckFailsWhenNothingListens(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, porta, _ := net.SplitHostPort(ln.Addr().String())
	ln.Close()
	if codigo, _ := healthcheck(t, ":"+porta); codigo != 1 {
		t.Errorf("porta fechada: código %d, queria 1", codigo)
	}
}

// Um servidor que aceita e não responde não pode segurar o healthcheck além
// do prazo, nem passar por pronto. O teste tem teto próprio: um checar sem
// prazo tem de FALHAR aqui, e não travar a suíte.
func TestHealthcheckGivesUpOnASilentServer(t *testing.T) {
	solta := make(chan struct{})
	addr := servidorDeProntidao(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-solta:
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	defer close(solta)
	url, err := urlDeProntidao(addr)
	if err != nil {
		t.Fatal(err)
	}
	fim := make(chan error, 1)
	go func() { fim <- checar(context.Background(), url, 100*time.Millisecond) }()
	select {
	case err := <-fim:
		if err == nil {
			t.Fatal("servidor mudo passou por pronto")
		}
	case <-time.After(time.Second):
		t.Fatal("checar não desistiu em 1s com prazo de 100ms")
	}
}

func TestReadyzURLUsesLoopbackAndThePortOfHTTPAddr(t *testing.T) {
	for addr, queria := range map[string]string{
		"":             "http://127.0.0.1:8080/readyz",
		":9000":        "http://127.0.0.1:9000/readyz",
		"0.0.0.0:8081": "http://127.0.0.1:8081/readyz",
		"[::]:8082":    "http://127.0.0.1:8082/readyz",
	} {
		got, err := urlDeProntidao(addr)
		if err != nil || got != queria {
			t.Errorf("urlDeProntidao(%q) = %q, %v; queria %q", addr, got, err, queria)
		}
	}
	if _, err := urlDeProntidao("8080"); err == nil {
		t.Error("HTTP_ADDR sem ':' deveria ser erro")
	}
}

func TestVersionPrintsTheStampedVersion(t *testing.T) {
	antiga := version
	version = "v9.9.9-teste"
	t.Cleanup(func() { version = antiga })
	var out bytes.Buffer
	codigo := runVersion(&out)
	if codigo != 0 || strings.TrimSpace(out.String()) != "v9.9.9-teste" {
		t.Errorf("version: código %d, saída %q; queria 0 e a versão carimbada", codigo, out.String())
	}
}

// Os dois subcomandos de operação passam pelo mesmo despacho do `user` (Plano
// 5), e o servidor continua sendo o padrão: um argumento que não está no mapa
// (a flag --role, um nome qualquer) segue para o flag.Parse de main.
func TestOperationSubcommandsShareTheDispatch(t *testing.T) {
	for _, nome := range []string{"user", "healthcheck", "version"} {
		if _, ok := subcomandos[nome]; !ok {
			t.Errorf("%q fora do mapa de subcomandos", nome)
		}
	}
	for _, arg := range []string{"--role=all", "-role", "serve", ""} {
		if _, ok := subcomandos[arg]; ok {
			t.Errorf("%q foi tratado como subcomando; o servidor é o padrão", arg)
		}
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./cmd/vigilume/`
Expected: FAIL de compilação: `undefined: runHealthcheck`, `undefined: urlDeProntidao`, `undefined: checar`.

- [ ] **Step 3: Os subcomandos**

Crie `cmd/vigilume/ops.go`:

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// version é carimbada no build: -ldflags "-X main.version=…" (Dockerfile,
// Makefile, CI). Sai no log de boot e em `vigilume version`, e em nenhuma
// resposta HTTP: expor a versão facilita achar uma vulnerável.
var version = "dev"

// healthcheckPrazo é o teto do `vigilume healthcheck`. O compose checa a
// cada 2 s; um app que não responde em 2 s não está pronto.
const healthcheckPrazo = 2 * time.Second

// runVersion é `vigilume version`: imprime a versão carimbada no build.
func runVersion(stdout io.Writer) int {
	fmt.Fprintln(stdout, version)
	return 0
}

// runHealthcheck é `vigilume healthcheck`: 0 se o /readyz do próprio processo
// responde 200 dentro do prazo, 1 em qualquer outro caso.
//
// A imagem final não tem shell nem curl (spec §5.9): o próprio binário é a
// ferramenta do healthcheck do compose.
func runHealthcheck(ctx context.Context, stderr io.Writer, getenv func(string) string) int {
	url, err := urlDeProntidao(getenv("HTTP_ADDR"))
	if err != nil {
		fmt.Fprintln(stderr, "healthcheck:", err)
		return 1
	}
	if err := checar(ctx, url, healthcheckPrazo); err != nil {
		fmt.Fprintln(stderr, "healthcheck:", err)
		return 1
	}
	return 0
}

// urlDeProntidao monta o /readyz em loopback a partir do HTTP_ADDR do
// processo. O host do HTTP_ADDR não interessa: o healthcheck roda dentro do
// mesmo contêiner, e o servidor escuta em todas as interfaces.
func urlDeProntidao(httpAddr string) (string, error) {
	if strings.TrimSpace(httpAddr) == "" {
		httpAddr = ":8080"
	}
	_, porta, err := net.SplitHostPort(httpAddr)
	if err != nil || porta == "" {
		return "", fmt.Errorf("HTTP_ADDR %q sem porta", httpAddr)
	}
	return "http://" + net.JoinHostPort("127.0.0.1", porta) + "/readyz", nil
}

// checar exige 200 do /readyz dentro do prazo. Qualquer outra coisa (503 com
// o banco fora, conexão recusada, servidor mudo) é "não pronto".
func checar(ctx context.Context, url string, prazo time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, prazo)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
	if resp.StatusCode != http.StatusOK {
		return errors.New("readyz respondeu " + resp.Status)
	}
	return nil
}
```

Aplique em `cmd/vigilume/main.go`:

```diff
--- a/cmd/vigilume/main.go
+++ b/cmd/vigilume/main.go
@@ -34,11 +34,17 @@
 
 // subcomandos são os modos do binário que não sobem o servidor. O primeiro
 // argumento escolhe um deles; sem subcomando, o processo sobe o servidor
-// (--role). O Plano 7 acrescenta healthcheck e version neste mesmo mapa.
+// (--role).
 var subcomandos = map[string]func(ctx context.Context, args []string) int{
 	"user": func(ctx context.Context, args []string) int {
 		return runUser(ctx, args, stdinDoProcesso(), os.Stdout, os.Stderr, os.Getenv)
 	},
+	"healthcheck": func(ctx context.Context, _ []string) int {
+		return runHealthcheck(ctx, os.Stderr, os.Getenv)
+	},
+	"version": func(context.Context, []string) int {
+		return runVersion(os.Stdout)
+	},
 }
 
 func main() {
@@ -73,7 +79,7 @@
 
 	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
 	slog.SetDefault(log)
-	log.Info("iniciando", "cfg", cfg)
+	log.Info("iniciando", "versao", version, "cfg", cfg)
 
 	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
 	defer stop()
```

- [ ] **Step 4: Rodar e ver passar**

Run: `gofmt -l cmd/ && go vet ./cmd/... && go test -race -count=1 ./cmd/...`
Expected: PASS.

Run: `go build -ldflags "-X main.version=v0.0.0-teste" -o /tmp/vigilume-t3 ./cmd/vigilume && /tmp/vigilume-t3 version && HTTP_ADDR=:1 /tmp/vigilume-t3 healthcheck; echo "código $?"`
Expected: `v0.0.0-teste`, depois `healthcheck: Get "http://127.0.0.1:1/readyz": … connection refused` e `código 1`.

- [ ] **Step 5: Sabotagem**

Uma de cada vez; restaure:
1. No começo de `runHealthcheck`, ponha `if ctx != nil { return 0 }` (devolver 0 sempre). Run: `go test -count=1 -run Healthcheck ./cmd/vigilume/`. Expected: FAIL em `TestHealthcheckFailsWhenNotReady`, `TestHealthcheckFailsWhenNothingListens` e `TestHealthcheckPassesOnlyWithReadyzOK`.
2. Em `checar`, troque `context.WithTimeout(ctx, prazo)` por `context.WithCancel(ctx)`. Expected: FAIL em `TestHealthcheckGivesUpOnASilentServer` ("checar não desistiu em 1s"), em ~1 s, **sem travar**.
3. Em `urlDeProntidao`, troque `"/readyz"` por `"/healthz"`. Expected: FAIL em `TestHealthcheckPassesOnlyWithReadyzOK`.
4. Em `main.go`, renomeie a chave `"healthcheck"` do mapa para `"healthcheck-x"`. Run: `go test -count=1 -run Operation ./cmd/vigilume/`. Expected: FAIL em `TestOperationSubcommandsShareTheDispatch`.

- [ ] **Step 6: Commit**

```bash
git add cmd/vigilume/
git commit -m "feat(cmd): subcomandos healthcheck e version no despacho do binário"
```

---

### Task 4: CI dos jobs Go, matriz 17/18 e varredura de segredos

O `ci.yml` de hoje tem um job `go` com actions por tag móvel (`@v4`, `@v5`) e ferramentas `@latest`: o CI muda de resultado sem commit nenhum, e uma tag reescrita roda código alheio com o `GITHUB_TOKEN` (o caso `tj-actions/changed-files`, 2025). Esta tarefa fixa tudo (D20), separa `lint` de `test`, põe o `test` na matriz Postgres 17/18 pelo `TEST_POSTGRES_IMAGE` que o `testdb` já lê (Reconciliação 7), e acrescenta o job `secrets`: `gitleaks` sobre o **histórico inteiro**, em todo PR, desde já (D37). Com o repositório privado, um segredo commitado hoje fica público no dia em que ele abrir.

**Files:**
- Modify: `.github/workflows/ci.yml`
- Create: `.gitleaksignore`

**Interfaces:**
- Consumes: `TEST_POSTGRES_IMAGE` de `internal/storage/testdb` (Postgres 18 por padrão).
- Produces: jobs `lint`, `test (postgres:17-alpine)`, `test (postgres:18-alpine)` e `secrets`; o `env` do workflow com as versões das ferramentas (a Task 7 acrescenta o resto).

**Contexto para quem implementa.** O `concurrency` cancela a execução anterior do mesmo ref (PR, `main` ou tag) quando chega um push novo: com o repositório privado, os minutos de Actions contam na cota, e uma execução superada não prova nada (D12). O `defaults: run: shell: bash` troca o padrão do GitHub (`bash -e`) por `bash -eo pipefail`: sem `pipefail`, um `comando | tee` que falha fica verde (D13; a prova está na Task 8, onde isso morderia o backup). A varredura do histórico deste repositório tem **um** achado, conferido à mão: a constante `tok-9f8e…` de `TestRecoverDoesNotLogThePath` (commit `ae726caf`), um token inventado para provar que o `Recover` não escreve o caminho no log. Ele entra no `.gitleaksignore` pela *fingerprint*, com o motivo. Nenhum arquivo com nome de segredo (`.env`, `.pem`, `.key`) está rastreado. O gitleaks é instalado por `go install` com versão fixa, e não pela `gitleaks-action` (uma action de terceiro a mais, com licença e permissões próprias). `fetch-depth: 0` é o que faz o checkout trazer o histórico.

- [ ] **Step 1: O workflow**

Aplique em `.github/workflows/ci.yml`:

```diff
--- a/.github/workflows/ci.yml
+++ b/.github/workflows/ci.yml
@@ -4,21 +4,49 @@
     branches: [main]
   pull_request:
 
+# Nenhum job escreve nada por padrão. Quem precisa de mais pede no job.
+permissions:
+  contents: read
+
+# shell: bash dá `bash -eo pipefail` a todo run. O padrão do GitHub é
+# `bash -e`, sem pipefail: um `comando | tee log` que falha deixaria o passo
+# verde, porque o código do pipe é o do tee.
+defaults:
+  run:
+    shell: bash
+
+# Um push novo no mesmo ref (PR, main ou tag) cancela a execução anterior:
+# com o repositório privado, os minutos de Actions contam na cota (2.000/mês),
+# e uma execução superada não prova nada. Na main, o publish da execução
+# nova substitui o da cancelada.
+concurrency:
+  group: ci-${{ github.ref }}
+  cancel-in-progress: true
+
+# Actions fixadas por SHA completo (a tag vem no comentário) e ferramentas com
+# versão: uma tag móvel roda código alheio com o GITHUB_TOKEN, e @latest muda
+# o resultado do CI sem commit nenhum (D20 do Plano 7). O Dependabot atualiza.
+env:
+  SQLC_VERSION: v1.31.1
+  STATICCHECK_VERSION: v0.8.1
+  GOSEC_VERSION: v2.29.0
+  GOVULNCHECK_VERSION: v1.8.0
+  GITLEAKS_VERSION: v8.30.1
+
 jobs:
-  go:
+  lint:
     runs-on: ubuntu-latest
     steps:
-      - uses: actions/checkout@v4
-      - uses: actions/setup-go@v5
+      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
+      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
         with:
-          go-version: '1.26'
-          cache: true
+          go-version-file: go.mod
 
       - name: sqlc diff
         # Código gerado desatualizado em relação ao SQL é o erro que o sqlc
         # existe para impedir; o CI o transforma em falha de build.
         run: |
-          go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1
+          go install github.com/sqlc-dev/sqlc/cmd/sqlc@${SQLC_VERSION}
           sqlc diff
 
       - name: vet
@@ -26,18 +54,55 @@
 
       - name: staticcheck
         run: |
-          go install honnef.co/go/tools/cmd/staticcheck@latest
+          go install honnef.co/go/tools/cmd/staticcheck@${STATICCHECK_VERSION}
           staticcheck ./...
 
       - name: gosec
         run: |
-          go install github.com/securego/gosec/v2/cmd/gosec@latest
+          go install github.com/securego/gosec/v2/cmd/gosec@${GOSEC_VERSION}
           gosec -quiet ./...
 
       - name: govulncheck
         run: |
-          go install golang.org/x/vuln/cmd/govulncheck@latest
+          go install golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}
           govulncheck ./...
 
+  test:
+    # Produção roda o 18; a perna do 17 mantém o código compatível com um
+    # Postgres que ainda não tenha o 18 (Reconciliação 7 do contrato). Uma
+    # migration com uuidv7() nativo, por exemplo, fica vermelha aqui.
+    strategy:
+      fail-fast: false
+      matrix:
+        postgres: ['postgres:17-alpine', 'postgres:18-alpine']
+    name: test (${{ matrix.postgres }})
+    runs-on: ubuntu-latest
+    env:
+      TEST_POSTGRES_IMAGE: ${{ matrix.postgres }}
+    steps:
+      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
+      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
+        with:
+          go-version-file: go.mod
+
       - name: test
         run: go test -race -count=1 -coverprofile=coverage.out ./...
+
+  secrets:
+    # O histórico INTEIRO, em todo PR: com o repositório privado, um segredo
+    # commitado hoje fica público no dia em que ele abrir. Os falsos positivos
+    # conferidos à mão ficam no .gitleaksignore, pela fingerprint (D37).
+    runs-on: ubuntu-latest
+    steps:
+      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
+        with:
+          fetch-depth: 0
+      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
+        with:
+          go-version-file: go.mod
+          cache: false
+
+      - name: gitleaks
+        run: |
+          go install github.com/zricethezav/gitleaks/v8@${GITLEAKS_VERSION}
+          gitleaks git . --redact --no-banner --verbose
```

- [ ] **Step 2: O falso positivo conhecido**

Crie `.gitleaksignore`:

```
# Falsos positivos do gitleaks, conferidos à mão (D37 do Plano 7).
# Uma linha por achado, pela fingerprint que o próprio gitleaks imprime
# (commit:arquivo:regra:linha). Nada entra aqui sem dizer por que não é segredo.

# Constante de TestRecoverDoesNotLogThePath: um token de verificação inventado
# para provar que o Recover não escreve o caminho no log. Nunca existiu fora
# do teste.
ae726caf892701f2b5111f59f01014d094a2cd0a:internal/httpapi/middleware/middleware_test.go:generic-api-key:87
```

- [ ] **Step 3: Conferir localmente**

Run: `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 .github/workflows/*.yml`
Expected: sem saída, código 0.

Run: `go run github.com/zricethezav/gitleaks/v8@v8.30.1 git . --redact --no-banner --verbose`
Expected: `no leaks found`, código 0 (109 commits na validação; o número cresce).

Run: `TEST_POSTGRES_IMAGE=postgres:17-alpine go test -race -count=1 ./internal/storage/`
Expected: PASS (o código continua compatível com o 17).

- [ ] **Step 4: Sabotagem**

Uma de cada vez; restaure:
1. Tire o `.gitleaksignore` da raiz (`mv .gitleaksignore /tmp/ign.bak`), rode `go run github.com/zricethezav/gitleaks/v8@v8.30.1 git . --redact --no-banner` e devolva o arquivo (`mv /tmp/ign.bak .gitleaksignore`). Expected: `leaks found: 1`, código 1: o falso positivo conhecido volta, e o job ficaria vermelho. (Não use `--gitleaks-ignore-path` com um arquivo vazio: o gitleaks lê o `.gitleaksignore` da raiz mesmo assim, e a sabotagem fica verde por vácuo.)
2. Crie `internal/storage/migrations/00008_sabotagem.sql` com `-- +goose Up`, `CREATE TABLE sabotagem (id uuid PRIMARY KEY DEFAULT uuidv7());`, `-- +goose Down`, `DROP TABLE sabotagem;`. Run: `TEST_POSTGRES_IMAGE=postgres:17-alpine go test -count=1 -run TestMigrat ./internal/storage/`. Expected: FAIL com `function uuidv7() does not exist`. O mesmo comando com `postgres:18-alpine` passa: é a perna do 17 que pega. Apague o arquivo.

A sabotagem "um token de verdade commitado num branch" (spec 9.4) foi feita num clone descartável na preparação deste plano (1 achado); não a repita no repositório.

- [ ] **Step 5: Commit**

```bash
git add .github/workflows/ci.yml .gitleaksignore
git commit -m "ci: actions por SHA, ferramentas com versão, matriz Postgres 17/18 e gitleaks no histórico inteiro"
```

---

### Task 5: A imagem

Multi-stage (D1–D9 da spec): o painel sai do Vite num estágio Node, o Go o embute e compila para a arquitetura de destino sem emulação, e o resultado vai para `distroless/static-debian13:nonroot`, sem shell nem ferramenta. Um alvo intermediário, `tools`, põe o mesmo binário sobre a variante `debug-nonroot` (que tem busybox): só o seed do compose o usa, para passar a senha pelo stdin (D6). As checagens da imagem são um script do repositório, que o CI chama e que roda na máquina de quem executa.

**Files:**
- Create: `Dockerfile`, `.dockerignore`, `deploy/ci/checar-imagem.sh`, `deploy/ci/trivy/Dockerfile`
- Modify: `Makefile`

**Interfaces:**
- Consumes: `web/` e o `vite.config.ts` do Plano 6 (o build sai em `../internal/webui/dist`); `var version` e `vigilume version` (Task 3).
- Produces:
  - Alvos `tools` e `runtime` (o padrão); `ARG VERSION` (padrão `dev`); `ENTRYPOINT ["/vigilume"]`, `CMD ["--role=all"]`, `USER nonroot:nonroot`, `EXPOSE 8080`.
  - `sh deploy/ci/checar-imagem.sh <imagem> <versão esperada>`: 0 se tudo passa, 1 com a lista do que falhou.
  - `make image`; `make build` passa a carimbar a versão (`git describe --tags --always --dirty`).
  - `deploy/ci/trivy/Dockerfile`: uma linha `FROM aquasec/trivy:<versão>@sha256:…`, que não se constrói; quem roda o Trivy lê a imagem dali (D9), e o Dependabot a atualiza (Task 7).

**Contexto para quem implementa.** As imagens-base vão por digest; a tag fica no comentário. Os estágios `web` e `build` rodam em `$BUILDPLATFORM`: amd64 e arm64 saem por cross-compile, sem QEMU. O `-buildvcs=false` é necessário porque o `.git` não entra no contexto (`.dockerignore`), e a versão vem do `-X`. O `.dockerignore` repete a regra do `.gitignore` do Plano 6 para o dist (só o `.gitkeep`): sem ela, um dist velho da máquina entraria no binário no lugar do que o estágio `web` gerou. O `tsc -b` do `npm run build` confere também o `e2e/` e o `playwright.config.ts` (o `tsconfig.node.json` do Plano 6 os inclui), por isso o `web/` entra inteiro no estágio. As checagens não podem ser vacuamente verdes: o "sem shell" só conta se o `docker run` falhou **por falta do arquivo** (um daemon fora do ar também daria código diferente de zero), e o tamanho é medido num `docker save -o` em arquivo, com piso de 1 MB (num pipe, um `save` que falha daria "0 bytes", abaixo do teto). **O build passar não prova que o painel entrou**: o `//go:embed all:dist` compila com o dist só com o `.gitkeep`, e o painel viraria um 503. Por isso a checagem 4 do script procura, dentro do binário tirado da imagem, a referência do `index.html` ao bundle com hash (`src="/assets/index-`), que nenhum código Go contém.

Números da validação (16 núcleos, Docker 29.8, rede boa): build frio num builder novo **~31 s** (37 s contando a subida do builder), rebuild com cache **~13 s** (com o Go só `~1,5 s`); binário de 18,7 MB; imagem **7,6 MB comprimida** (o `docker images` mostra ~32 MB: o store do containerd soma blob e snapshot); Trivy: **0** HIGH/CRITICAL corrigíveis no Debian 13.7 e no binário.

- [ ] **Step 1: O script de checagem (primeiro, para vê-lo falhar)**

Crie `deploy/ci/checar-imagem.sh`:

```bash
#!/bin/sh
# Checagens da imagem de produção (spec do Plano 7, 9.2). O job "image" do CI
# as roda depois do Trivy; localmente: sh deploy/ci/checar-imagem.sh <imagem> <versão>.
#
# Cada checagem afirma uma ausência (sem root, sem shell, sem inchaço) ou uma
# presença que o build sozinho não prova (o painel embutido, a versão).
set -eu

IMAGEM=${1:?uso: checar-imagem.sh <imagem> <versão esperada>}
VERSAO=${2:?uso: checar-imagem.sh <imagem> <versão esperada>}
TETO_BYTES=$((15 * 1000 * 1000))
falhas=0
falhar() { echo "FALHOU: $*" >&2; falhas=$((falhas + 1)); }

# 1. Não é root. Duas guardas dão isso: a base :nonroot já traz o usuário
#    65532, e o Dockerfile repete USER nonroot:nonroot. Tirar só uma delas
#    fica verde aqui; a checagem prova a propriedade, não cada guarda.
usuario=$(docker image inspect -f '{{.Config.User}}' "$IMAGEM")
case "$usuario" in
    "" | root | 0 | root:* | 0:*) falhar "a imagem roda como root (User='$usuario')" ;;
esac

# 2. Sem shell (spec §5.9). /busybox/sh é o da variante debug, a base do
#    alvo tools: se ela vazar para o runtime, é aqui que aparece.
#    "Sem shell" só vale se o docker run falhou POR FALTA do arquivo: um
#    daemon fora do ar ou uma imagem errada também dão código diferente de
#    zero, e passariam por "sem shell".
for sh in /bin/sh /busybox/sh; do
    if saida=$(docker run --rm --entrypoint "$sh" "$IMAGEM" -c true 2>&1); then
        falhar "a imagem tem shell: $sh"
    elif ! printf '%s' "$saida" | grep -q "no such file or directory"; then
        falhar "docker run com $sh falhou por outro motivo: $saida"
    fi
done

# 3. A versão carimbada chegou ao binário.
got=$(docker run --rm "$IMAGEM" version)
[ "$got" = "$VERSAO" ] || falhar "vigilume version = '$got', queria '$VERSAO'"

# 4. O painel está DENTRO do binário. O build passa com o dist vazio (o
#    .gitkeep basta para o go:embed), e o painel viraria um 503: por isso a
#    checagem olha o conteúdo, não o build. O index.html do Vite referencia o
#    bundle com hash; nenhum código Go tem essa string.
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
id=$(docker create "$IMAGEM")
docker cp -q "$id:/vigilume" "$tmp/vigilume"
docker rm "$id" >/dev/null
grep -aq 'src="/assets/index-' "$tmp/vigilume" || falhar "o binário não embute o index.html do painel"

# 5. Teto de tamanho: pega uma base errada ou um node_modules que vazou.
#    Em dois passos: num pipe `docker save | gzip | wc`, um save que falha
#    daria "0 bytes", abaixo do teto. O set -e pega o save que falha.
docker save -o "$tmp/imagem.tar" "$IMAGEM"
comprimido=$(gzip -c "$tmp/imagem.tar" | wc -c)
echo "imagem comprimida: $comprimido bytes"
[ "$comprimido" -gt 1000000 ] || falhar "imagem comprimida com $comprimido bytes: pequena demais, a medida falhou"
[ "$comprimido" -lt "$TETO_BYTES" ] || falhar "imagem comprimida com $comprimido bytes, teto $TETO_BYTES"

if [ "$falhas" -gt 0 ]; then
    exit 1
fi
echo "imagem conferida: $IMAGEM ($VERSAO)"
```

- [ ] **Step 2: Ver falhar**

Run: `sh deploy/ci/checar-imagem.sh vigilume:dev dev; echo "código $?"`
Expected: erro do `docker image inspect` (`No such image: vigilume:dev`) e código diferente de 0: ainda não há imagem.

- [ ] **Step 3: O Dockerfile e o contexto**

Crie `Dockerfile`:

```dockerfile
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

FROM --platform=$BUILDPLATFORM golang:1.26-alpine@sha256:8ac98ca534ac3f51e1f420a1dd2c15e74c75cfa0f23f3ad27eb5d7236c349a0c AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY --from=web /src/internal/webui/dist/ internal/webui/dist/
# -buildvcs=false: o .git não entra no contexto (.dockerignore), e a versão
# vem do -X (D3). -s -w tira DWARF e símbolos; o stack trace de um panic
# continua legível.
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
```

Crie `.dockerignore`:

```
# O contexto do build é só o que a imagem usa (D7 do Plano 7). Sem isto, um
# dist velho da máquina entraria no binário no lugar do que o estágio web
# gerou, e o .git e os testes engordariam o contexto.
.git
.github
.idea
.superpowers
bin
.env
coverage.out
**/*_test.go
docs
deploy
# A mesma regra do .gitignore (Plano 6): do dist, só o .gitkeep.
internal/webui/dist/*
!internal/webui/dist/.gitkeep
web/node_modules
web/test-results
web/playwright-report
web/e2e/.tmp
```

Crie `deploy/ci/trivy/Dockerfile`:

```dockerfile
# O Trivy que o job image do CI roda (e quem executa, localmente). Este
# arquivo não constrói nada: existe para o Dependabot enxergar o digest e
# abrir PR quando sair versão nova. O CI lê a imagem da linha FROM.
FROM aquasec/trivy:0.74.0@sha256:62b1e65e8869bc4b4c6aa4fa2b21595256c7c2f6018a9d9ad61caf87187c1969
```

Aplique em `Makefile`:

```diff
--- a/Makefile
+++ b/Makefile
@@ -1,4 +1,9 @@
-.PHONY: test test-short lint sec build run generate
+.PHONY: test test-short lint sec build run generate image
+
+# A versão carimbada no binário (vigilume version, log de boot). Fora de um
+# clone git, "dev".
+VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
+LDFLAGS := -X main.version=$(VERSION)
 
 test:
 	go test -race -count=1 ./...
@@ -15,10 +20,15 @@
 	govulncheck ./...
 
 build:
-	go build -o bin/vigilume ./cmd/vigilume
+	go build -ldflags "$(LDFLAGS)" -o bin/vigilume ./cmd/vigilume
 
 run: build
 	./bin/vigilume --role=all
 
 generate:
 	sqlc generate
+
+# A imagem de produção, com o painel embutido (Dockerfile).
+image:
+	docker build --build-arg VERSION=$(VERSION) -t vigilume:$(VERSION) -t vigilume:dev .
+
```

- [ ] **Step 4: Construir e conferir**

Run: `make image VERSION=v0.0.0-t5`
Expected: build verde; `vigilume:v0.0.0-t5` e `vigilume:dev` na lista do `docker images`.

Run: `sh deploy/ci/checar-imagem.sh vigilume:dev v0.0.0-t5`
Expected: `imagem comprimida: ~7600000 bytes` e `imagem conferida: vigilume:dev (v0.0.0-t5)`.

Run: `docker run --rm -v /var/run/docker.sock:/var/run/docker.sock "$(sed -n 's/^FROM //p' deploy/ci/trivy/Dockerfile)" image --quiet --severity HIGH,CRITICAL --ignore-unfixed --exit-code 1 --db-repository public.ecr.aws/aquasecurity/trivy-db,ghcr.io/aquasecurity/trivy-db vigilume:dev`
Expected: a tabela com `0` no `debian` e no `gobinary`, código 0. (Um achado novo aqui, dias depois, é um CVE publicado depois da validação: atualize o digest da base, não ignore.)

Run: `docker buildx build --platform linux/amd64,linux/arm64 -o type=cacheonly .`
Expected: verde, sem emulação (os estágios de build rodam na arquitetura da máquina).

- [ ] **Step 5: Sabotagem**

Cada uma num Dockerfile de sabotagem (`cp Dockerfile /tmp/Dockerfile.sab`, editar, `docker build -f /tmp/Dockerfile.sab -t vigilume:sab .`, `sh deploy/ci/checar-imagem.sh vigilume:sab dev`, `docker rmi vigilume:sab`):
1. Base do `runtime` trocada por `gcr.io/distroless/static-debian13:latest` **e** sem a linha `USER nonroot:nonroot`. Expected: `FALHOU: a imagem roda como root (User='0')`. Tirar só o `USER` fica **verde**: a base `:nonroot` já traz o usuário 65532. São duas guardas da mesma propriedade, e o script prova a propriedade, não cada uma.
2. Base do `runtime` trocada pela do `tools` (`debug-nonroot`, com o mesmo digest). Expected: `FALHOU: a imagem tem shell: /busybox/sh`.
3. Sem a linha `COPY --from=web /src/internal/webui/dist/ internal/webui/dist/`. Expected: o build passa, e o script dá `FALHOU: o binário não embute o index.html do painel`. É o caso que o build sozinho não pega.
4. Antes do `EXPOSE`, `COPY --from=web /src/web/node_modules /node_modules`. Expected: `FALHOU: imagem comprimida com ~65000000 bytes, teto 15000000`.
5. Controle positivo do Trivy: o mesmo comando do Step 4 contra `postgres:16-alpine` (se estiver na máquina) deu código 1 na validação, com 22 achados no `gosu` dela: as flags derrubam o job quando há achado.

- [ ] **Step 6: Commit**

```bash
git add Dockerfile .dockerignore Makefile deploy/ci/checar-imagem.sh deploy/ci/trivy/Dockerfile
git commit -m "build: imagem multi-stage com o painel embutido, distroless nonroot, e o alvo tools"
```

---

### Task 6: O compose do avaliador

O critério de pronto número 1 da spec: `git clone … && docker compose up --wait` deixa o painel em `http://localhost:8080`, com um usuário de demonstração pronto para login. A ordem é db → seed → app (D10): o seed usa o `vigilume user create` do Plano 5, que aplica as migrations antes de criar o usuário, e o app só sobe depois de o seed sair com 0. O seed também cadastra o alvo `https://example.com/`, para o painel não nascer vazio (D15; D11 deste plano): o domínio é reservado para exemplos, e o seed não sonda sites de terceiros. Quem quiser mais passa `DEMO_TARGETS_EXTRA`. As checagens do compose são um script, que o job `e2e` da Task 7 roda antes do Playwright.

**Files:**
- Create: `compose.yaml`, `deploy/compose/initdb.sql`, `deploy/compose/seed.sh`, `deploy/ci/checar-compose.sh`
- Create: `internal/config/compose_test.go`
- Modify: `Makefile`

**Interfaces:**
- Consumes: os alvos `tools` e `runtime` (Task 5); `vigilume healthcheck` (Task 3); as chaves de demonstração (Task 2); `vigilume user create` (código 3 = já existe usuário); `POST /api/v1/auth/login` e `POST /api/v1/targets`; o E2E do Plano 6 com `E2E_BASE_URL`, `E2E_EMAIL`, `E2E_PASSWORD`.
- Produces:
  - Serviços `db`, `seed` e `app`; volume `pgdata`. Variáveis de quem roda: `VIGILUME_TAG` (padrão `latest`), `VIGILUME_PORT` (padrão `8080`), `DEMO_TARGETS_EXTRA` (URLs públicas separadas por espaço; padrão vazio).
  - Usuário `demo@vigilume.local`, senha `demo-vigilume-2026`; o alvo `https://example.com/` (nome `example.com`), mais um por URL de `DEMO_TARGETS_EXTRA` (nome = o host).
  - `sh deploy/ci/checar-compose.sh` (respeita `COMPOSE_PROJECT_NAME`, `VIGILUME_PORT`, `VIGILUME_TAG` e `UP_ARGS`; deixa a pilha de pé).
  - `make up` (`docker compose up --wait --build`: a imagem do código que se tem), `make down`, `make e2e` (apaga o banco do compose antes, porque as checagens contam o que o seed criou num banco vazio).

**Contexto para quem implementa.** O `vigilume user create` lê a senha do stdin, e a imagem de produção não tem shell para o pipe: é por isso que o seed roda no alvo `tools` (`/busybox/sh`). O app não existe quando o seed roda, então o seed sobe **uma instância só-API em loopback dentro do próprio contêiner** (`--role=api` em `127.0.0.1:18080`), faz login, cadastra os alvos e a derruba (desvio da D15, registrado no cabeçalho). Os alvos são públicos porque um serviço dentro do compose tem IP privado, e o `safedial` o recusa de propósito; cadastrar não usa rede (a validação não resolve DNS), então o seed falha alto se o cadastro falhar. No fim, o seed faz **logout** (o cookie de refresh vem do cabeçalho `Set-Cookie` do login, que o `wget -S` mostra): a sessão dele não fica viva no banco, e o `checar-compose.sh` confere isso. O `checar-compose.sh` confere exatamente 1 + o número de URLs de `DEMO_TARGETS_EXTRA`, e que o `example.com` está entre eles: um seed que voltasse a cadastrar sites de terceiros, ou que ignorasse os extras, fica vermelho. Com o código 3 (usuário já existe) o seed sai 0 sem tocar em nada: é o que torna o segundo `up` idempotente. `image` **e** `build` em cada serviço do Vigilume: com a imagem acessível, o compose a puxa; com o pull negado (pacote privado, ou tag que não existe), ele constrói do código — conferido na validação (`error from registry: denied` → `Building`). No Postgres 18 o volume é `/var/lib/postgresql`, não `…/data` (D11). A porta escuta só em `127.0.0.1`, e o painel exige contexto seguro: abre-se por `localhost`.

Números da validação: `up --wait` com as imagens na máquina e volumes novos **4,5 s**; com o build em cache, **7,8 s**; o `checar-compose.sh` inteiro (três `up`, um `down`) **~10 s**; o E2E do Plano 6 contra o compose, **6 de 6** em ~40 s.

- [ ] **Step 1: O teste que amarra o compose às chaves recusadas (falha)**

Crie `internal/config/compose_test.go`:

```go
package config

import (
	"bytes"
	"encoding/base64"
	"os"
	"regexp"
	"testing"
)

// A recusa das chaves de demonstração (D14 do Plano 7) só protege se as
// chaves do compose.yaml forem ESTAS. Trocar uma chave no compose sem trocar
// aqui deixaria o compose copiado para produção subir com uma chave pública.
func TestComposeUsesTheDemoKeys(t *testing.T) {
	compose, err := os.ReadFile("../../compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for i, nome := range []string{"MASTER_KEY", "JWT_SECRET", "IP_HASH_KEY"} {
		m := regexp.MustCompile(`(?m)^\s+` + nome + `:\s+(\S+)`).FindSubmatch(compose)
		if m == nil {
			t.Errorf("%s ausente do compose.yaml", nome)
			continue
		}
		chave, err := base64.StdEncoding.DecodeString(string(m[1]))
		if err != nil || !bytes.Equal(chave, chavesDeDemo[i]) {
			t.Errorf("%s do compose.yaml não é a chave de demonstração %d que o boot recusa", nome, i)
		}
	}
}
```

Run: `go test -count=1 -run TestComposeUsesTheDemoKeys ./internal/config/`
Expected: FAIL: `open ../../compose.yaml: no such file or directory`.

- [ ] **Step 2: O script de checagem**

Crie `deploy/ci/checar-compose.sh`:

```bash
#!/bin/sh
# Checagens do compose do avaliador (spec do Plano 7, 9.3). O job "e2e" do CI
# as roda antes do Playwright; localmente, da raiz do repositório:
#
#   sh deploy/ci/checar-compose.sh
#
# Respeita COMPOSE_PROJECT_NAME, VIGILUME_PORT e VIGILUME_TAG, e passa
# UP_ARGS ao `docker compose up` (no CI: --no-build --pull never, para usar a
# imagem que acabou de passar no Trivy). Parte de volumes novos (as
# contagens do seed só valem num banco vazio). Deixa a pilha de pé no fim: o
# Playwright roda contra ela.
set -eu

PORTA=${VIGILUME_PORT:-8080}
BASE=http://127.0.0.1:$PORTA
EMAIL=demo@vigilume.local
SENHA=demo-vigilume-2026
TETO_S=90
falhas=0
falhar() { echo "FALHOU: $*" >&2; falhas=$((falhas + 1)); }

# Milissegundos desde a época. O `date +%N` é do GNU (Linux, o runner do
# CI); o do macOS devolve "N" literal, e aí a medida cai para segundos.
agora_ms() {
    ns=$(date +%s%N)
    case "$ns" in
        *N) echo $(($(date +%s) * 1000)) ;;
        *) echo $((ns / 1000000)) ;;
    esac
}
subir() {
    inicio=$(agora_ms)
    # shellcheck disable=SC2086 # UP_ARGS são várias flags, de propósito
    docker compose up --wait ${UP_ARGS:-} >&2 || return 1
    echo $(($(agora_ms) - inicio))
}
sql() { docker compose exec -T db psql -U postgres -d vigilume -Atq -c "$1"; }
entrar() {
    curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE/api/v1/auth/login" \
        -H 'Content-Type: application/json' -H 'X-Requested-With: vigilume' \
        -d "{\"email\":\"$EMAIL\",\"password\":\"$SENHA\"}"
}

# 1. Um comando, código 0, e o tempo à vista. O runner não é a máquina do
#    avaliador: o número serve para ver tendência; o teto pega o desastre.
ms=$(subir) || { docker compose logs >&2; echo "FALHOU: docker compose up --wait" >&2; exit 1; }
echo "docker compose up --wait: ${ms} ms"
[ -n "${GITHUB_STEP_SUMMARY:-}" ] && echo "docker compose up --wait: **${ms} ms**" >> "$GITHUB_STEP_SUMMARY"
[ "$ms" -le $((TETO_S * 1000)) ] || falhar "up --wait levou ${ms} ms, teto ${TETO_S} s"

# 2. O seed funcionou, e o Argon2id bate.
[ "$(entrar)" = 200 ] || falhar "login do usuário de demonstração não deu 200"

# 3. O painel está embutido e é servido na raiz, com a CSP de nonce.
pagina=$(curl -s -D - "$BASE/")
echo "$pagina" | grep -q 'src="/assets/index-' || falhar "GET / não devolveu o index.html do painel"
echo "$pagina" | grep -qi "^content-security-policy:.*'nonce-" || falhar "GET / sem CSP com nonce"

# 4. O papel da aplicação é comum e é dono das tabelas (D12).
papel=$(sql "SELECT rolsuper::text || ' ' || rolbypassrls::text FROM pg_roles WHERE rolname = 'vigilume_app'")
[ "$papel" = "false false" ] || falhar "vigilume_app com rolsuper/rolbypassrls = '$papel'"
alheias=$(sql "SELECT count(*) FROM pg_tables WHERE schemaname = 'public' AND tableowner <> 'vigilume_app'")
[ "$alheias" = 0 ] || falhar "$alheias tabela(s) sem o dono vigilume_app"

# 5. Os alvos de exemplo (D15): o example.com, reservado para isso, e os de
#    DEMO_TARGETS_EXTRA, se quem roda pediu. Nenhum outro site de terceiros.
extras=$(echo ${DEMO_TARGETS_EXTRA:-} | wc -w)
usuarios=$(sql "SELECT count(*) FROM users")
alvos=$(sql "SELECT count(*) FROM targets")
[ "$usuarios" = 1 ] && [ "$alvos" = $((1 + extras)) ] ||
    falhar "seed: $usuarios usuário(s) e $alvos alvo(s), queria 1 e $((1 + extras))"
[ "$(sql "SELECT count(*) FROM targets WHERE url = 'https://example.com/'")" = 1 ] ||
    falhar "seed: o alvo https://example.com/ não foi cadastrado"

# 6. O seed não deixa sessão viva: ele encerra a que abriu para cadastrar os
#    alvos (um refresh de 7 dias parado no banco é uma credencial a menos
#    para vazar).
#    (A do seed é a do busybox: user agent "Wget".)
vivas=$(sql "SELECT count(*) FROM sessions WHERE revoked_at IS NULL AND user_agent LIKE 'Wget%'")
[ "$vivas" = 0 ] || falhar "o seed deixou $vivas sessão(ões) viva(s)"

# 7. Idempotência: um segundo `up` sai 0 e não duplica nada.
subir >/dev/null || falhar "o segundo up --wait falhou"
[ "$(sql "SELECT count(*) FROM users")/$(sql "SELECT count(*) FROM targets")" = "$usuarios/$alvos" ] ||
    falhar "o segundo up mudou usuários ou alvos"

# 8. Persistência no layout do 18 (D11): down sem -v, up, e o usuário continua.
docker compose down >&2
subir >/dev/null || falhar "o up depois do down falhou"
[ "$(sql "SELECT count(*) FROM users")" = 1 ] || falhar "o usuário sumiu depois de down + up"
[ "$(entrar)" = 200 ] || falhar "login depois de down + up não deu 200"

if [ "$falhas" -gt 0 ]; then
    docker compose logs >&2
    exit 1
fi
echo "compose conferido"
```

- [ ] **Step 3: O compose, o init e o seed**

Crie `deploy/compose/initdb.sql`:

```sql
-- Papel e banco da aplicação. É o MESMO SQL em três lugares: o init do
-- Postgres do compose, o banco descartável em que o backup é restaurado todo
-- dia, e o console do Postgres de produção (docs/deploy/fly.md, passo 4, com
-- senha nova).
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
```

Crie `deploy/compose/seed.sh`:

```bash
# Seed do compose (D10 e D15 do Plano 7). Roda no alvo tools da imagem, que
# tem o busybox: a imagem de produção não tem shell (spec §5.9).
#
# 1. O usuário de demonstração, com a senha pelo stdin (contrato, decisão 1).
#    O `user create` aplica as migrations antes (CheckRole → Migrate), e é por
#    isso que o seed pode vir antes do app. Código 3 = já existe usuário, nada
#    mudou (Reconciliação 5): é o que torna o segundo `up` idempotente.
# 2. Com DEMO_TARGETS=1, o alvo https://example.com/, pela própria API, e mais
#    as URLs de DEMO_TARGETS_EXTRA (separadas por espaço), se houver. O
#    example.com é reservado para exemplo (RFC 2606 e 6761): o seed não sonda
#    o site de ninguém a cada 60 s. O app ainda não subiu (ele espera o
#    seed), então o seed sobe uma instância só-API em loopback, dentro deste
#    contêiner, e a derruba no fim. Os alvos precisam ser públicos: um serviço
#    dentro do compose tem IP privado, e o safedial o recusa de propósito
#    (spec §5.1).
set -u

printf '%s\n' "$DEMO_PASSWORD" | /vigilume user create --email "$DEMO_EMAIL"
rc=$?
if [ "$rc" -eq 3 ]; then
    echo "seed: já existe usuário; nada muda"
    exit 0
fi
[ "$rc" -eq 0 ] || exit "$rc"
[ "${DEMO_TARGETS:-0}" = 1 ] || exit 0

API=127.0.0.1:18080
HTTP_ADDR=$API /vigilume --role=api >/tmp/seed-api.log 2>&1 &
pid=$!
trap 'kill "$pid" 2>/dev/null; wait "$pid" 2>/dev/null' EXIT

tentativas=0
until HTTP_ADDR=$API /vigilume healthcheck 2>/dev/null; do
    tentativas=$((tentativas + 1))
    if [ "$tentativas" -ge 60 ]; then
        echo "seed: a API temporária não ficou pronta" >&2
        cat /tmp/seed-api.log >&2
        exit 1
    fi
    sleep 0.5
done

# O busybox não tem parser de JSON. O token é base64url com pontos, sem
# aspas; o E2E do CI pega uma mudança no formato da resposta.
postar() {
    wget -q -O - --header 'Content-Type: application/json' --header 'X-Requested-With: vigilume' "$@"
}
# -S manda os cabeçalhos da resposta para o stderr: o cookie de refresh vem
# de lá, para o logout do fim.
resposta=$(postar -S --post-data "{\"email\":\"$DEMO_EMAIL\",\"password\":\"$DEMO_PASSWORD\"}" \
    "http://$API/api/v1/auth/login" 2>&1)
token=$(printf '%s\n' "$resposta" | sed -n 's/.*"access_token":"\([^"]*\)".*/\1/p')
refresh=$(printf '%s\n' "$resposta" | sed -n 's/.*[Ss]et-[Cc]ookie: *\(vigilume_refresh=[^;]*\).*/\1/p')
if [ -z "$token" ] || [ -z "$refresh" ]; then
    echo "seed: o login do usuário de demonstração falhou" >&2
    exit 1
fi

n=0
for url in https://example.com/ ${DEMO_TARGETS_EXTRA:-}; do
    # O nome é o host da URL: https://www.exemplo.org/x → www.exemplo.org.
    nome=${url#*://}
    nome=${nome%%/*}
    corpo="{\"name\":\"$nome\",\"url\":\"$url\",\"method\":\"GET\",\"interval_sec\":60,\"timeout_ms\":10000,\"expected_status\":200}"
    if ! postar --header "Authorization: Bearer $token" --post-data "$corpo" \
        "http://$API/api/v1/targets" >/dev/null; then
        echo "seed: falhou ao cadastrar o alvo $url" >&2
        exit 1
    fi
    n=$((n + 1))
done
# A sessão do seed não fica viva no banco: logout revoga a família.
postar --header "Cookie: $refresh" --post-data '' "http://$API/api/v1/auth/logout" >/dev/null
echo "seed: usuário $DEMO_EMAIL e $n alvo(s) de exemplo"
```

Crie `compose.yaml`:

```yaml
# Vigilume local, com um comando (spec §15):
#
#   docker compose up --wait        e abra http://localhost:8080
#   login: demo@vigilume.local / demo-vigilume-2026
#
# Pelo localhost, e não pelo IP da máquina: o painel exige contexto seguro
# (HTTPS ou localhost). Em HTTP por outro endereço ele mostra um aviso e
# desliga a coordenação entre abas (Web Locks). A porta só escuta no
# 127.0.0.1 de qualquer jeito (D16).
#
# Ordem: db → seed → app (D10 do Plano 7). O seed migra o banco e cria o
# usuário de demonstração; o app só sobe depois de o seed sair com 0. Com o
# seed depois do app, o `up --wait` devolve 1 mesmo com o seed saindo 0.
#
# Cada serviço do Vigilume tem `image` E `build`: com a imagem publicada e
# acessível, o compose a puxa; se o pull é negado (pacote privado), ele
# constrói do código (D17).
name: vigilume

# !!! CHAVES DE DEMONSTRAÇÃO, PÚBLICAS NO REPOSITÓRIO. NÃO USE EM PRODUÇÃO. !!!
# Com elas, qualquer um forja um JWT e decifra os segredos gravados. O boot
# as recusa quando PUBLIC_BASE_URL é https (D14). Produção: docs/deploy/fly.md.
x-ambiente: &ambiente
  DATABASE_URL: postgres://vigilume_app:vigilume_app@db:5432/vigilume?sslmode=disable
  PUBLIC_BASE_URL: http://localhost:8080
  # O `vite dev` do painel (Plano 6), para quem desenvolve contra este compose.
  CORS_ORIGIN: http://localhost:5173
  METRICS_ADDR: "off"
  LOG_LEVEL: info
  MASTER_KEY: ZGVtby12aWdpbHVtZS1NQVNURVJfS0VZLW5hb3VzZSE=  # "demo-vigilume-MASTER_KEY-naouse!"
  JWT_SECRET: ZGVtby12aWdpbHVtZS1KV1RfU0VDUkVULW5hb3VzZSE=  # "demo-vigilume-JWT_SECRET-naouse!"
  IP_HASH_KEY: ZGVtby12aWdpbHVtZS1JUF9IQVNIX0tFWS1uYW91c2U= # "demo-vigilume-IP_HASH_KEY-naouse"

services:
  db:
    # A mesma imagem (tag e digest) de produção e dos testes (D29).
    image: postgres:18-alpine@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873
    environment:
      # Superusuário só para o init; a aplicação usa vigilume_app.
      POSTGRES_PASSWORD: postgres
    volumes:
      # Layout do 18: o volume é /var/lib/postgresql, não .../data (D11).
      # Em .../data os dados iriam para um volume anônimo e sumiriam.
      - pgdata:/var/lib/postgresql
      - ./deploy/compose/initdb.sql:/docker-entrypoint-initdb.d/10-vigilume.sql:ro
    healthcheck:
      # Pelo TCP: durante o init o Postgres só escuta no socket, e o banco
      # vigilume ainda não existe.
      test: ["CMD", "pg_isready", "-h", "127.0.0.1", "-U", "vigilume_app", "-d", "vigilume"]
      interval: 10s
      timeout: 3s
      retries: 5
      start_period: 30s
      start_interval: 500ms

  seed:
    image: ghcr.io/bipoxdxd/vigilume:${VIGILUME_TAG:-latest}-tools
    build:
      context: .
      target: tools
    entrypoint: ["/busybox/sh", "/seed/seed.sh"]
    environment:
      <<: *ambiente
      DEMO_EMAIL: demo@vigilume.local
      # A política do Plano 5: de 15 a 128 code points.
      DEMO_PASSWORD: demo-vigilume-2026
      # 1 = o alvo https://example.com/ (domínio reservado para exemplos), para
      # o painel não nascer vazio. Mais alvos: DEMO_TARGETS_EXTRA com URLs
      # públicas separadas por espaço, no ambiente de quem roda o compose.
      DEMO_TARGETS: "1"
      DEMO_TARGETS_EXTRA: ${DEMO_TARGETS_EXTRA:-}
    volumes:
      - ./deploy/compose/seed.sh:/seed/seed.sh:ro
    depends_on:
      db:
        condition: service_healthy

  app:
    image: ghcr.io/bipoxdxd/vigilume:${VIGILUME_TAG:-latest}
    build:
      context: .
      target: runtime
    environment: *ambiente
    ports:
      # Só no loopback: numa rede de café, o painel de demo com senha
      # conhecida ficaria aberto a todos (D16).
      - "127.0.0.1:${VIGILUME_PORT:-8080}:8080"
    healthcheck:
      # A imagem não tem curl nem shell: o próprio binário checa o /readyz (D5).
      test: ["CMD", "/vigilume", "healthcheck"]
      interval: 10s
      timeout: 3s
      retries: 3
      start_period: 30s
      start_interval: 500ms
    restart: unless-stopped
    depends_on:
      db:
        condition: service_healthy
      seed:
        condition: service_completed_successfully

volumes:
  pgdata:
```

Aplique em `Makefile`:

```diff
--- a/Makefile
+++ b/Makefile
@@ -1,4 +1,4 @@
-.PHONY: test test-short lint sec build run generate image
+.PHONY: test test-short lint sec build run generate image up down e2e
 
 # A versão carimbada no binário (vigilume version, log de boot). Fora de um
 # clone git, "dev".
@@ -32,3 +32,21 @@
 image:
 	docker build --build-arg VERSION=$(VERSION) -t vigilume:$(VERSION) -t vigilume:dev .
 
+# A pilha do avaliador (compose.yaml): db → seed → app, em http://localhost:8080.
+# --build: quem desenvolve quer a imagem do código que tem na mão, e não a
+# publicada nem uma construída antes.
+up:
+	docker compose up --wait --build
+
+down:
+	docker compose down
+
+# O que o job e2e do CI roda: as checagens do compose e o Playwright contra
+# ele, com o usuário de demonstração. APAGA o banco do compose antes (as
+# checagens contam o que o seed criou num banco vazio). Deixa a pilha de pé;
+# `make down` a tira.
+e2e:
+	docker compose down -v
+	UP_ARGS=--build sh deploy/ci/checar-compose.sh
+	cd web && E2E_BASE_URL=http://127.0.0.1:$${VIGILUME_PORT:-8080} \
+		E2E_EMAIL=demo@vigilume.local E2E_PASSWORD=demo-vigilume-2026 npx playwright test
```

- [ ] **Step 4: Rodar e ver passar**

Run: `go test -race -count=1 ./internal/config/ && docker compose config -q`
Expected: PASS, e o `config` sem saída.

Run: `docker compose down -v; UP_ARGS=--build sh deploy/ci/checar-compose.sh`
Expected: as linhas do compose; `pull` negado e `Building` nos dois serviços do Vigilume (a imagem publicada ainda não existe); `docker compose up --wait: <N> ms`; `compose conferido`. O `docker compose logs seed` mostra `usuário criado: demo@vigilume.local …` e `seed: usuário demo@vigilume.local e 1 alvo(s) de exemplo`.

Abra <http://localhost:8080>, entre com `demo@vigilume.local` / `demo-vigilume-2026`: o painel com o alvo `example.com`.

Run: `cd web && npx playwright install chromium && cd .. && make e2e`
Expected: `compose conferido` de novo e `6 passed` do Playwright, contra o compose. Depois: `docker compose down -v`.

Run: `DEMO_TARGETS_EXTRA="https://www.iana.org/ https://vigilume-extra.invalid/x" sh deploy/ci/checar-compose.sh && docker compose exec -T db psql -U postgres -d vigilume -Atc "SELECT name, url FROM targets ORDER BY url"; docker compose down -v`
Expected: `compose conferido`, e os três alvos: `example.com`, `vigilume-extra.invalid` e `www.iana.org`, cada um com a sua URL.

- [ ] **Step 5: Sabotagem**

Cada uma num compose de sabotagem na raiz do repositório (`cp compose.yaml compose.sab.yaml`, editar, `COMPOSE_FILE=compose.sab.yaml COMPOSE_PROJECT_NAME=sab VIGILUME_PORT=18081 sh deploy/ci/checar-compose.sh`, depois `COMPOSE_FILE=compose.sab.yaml COMPOSE_PROJECT_NAME=sab docker compose down -v` e apagar o arquivo):
1. Seed sem o tratamento do código 3 (um `seed.sab.sh` sem o `if [ "$rc" -eq 3 ]`, montado no lugar do `seed.sh`). Expected: o primeiro `up` passa; depois `service "seed" didn't complete successfully: exit 3`, `FALHOU: o segundo up --wait falhou` e as falhas da persistência.
2. Ordem invertida: o `seed` com `depends_on: app: condition: service_healthy`, e o `app` sem a dependência do `seed`. Expected: no Compose v5.5.1 o **primeiro** `up` sai 0 (a spec mediu 1 numa versão anterior); o segundo dá `container sab-seed-1 exited (0)` e `FALHOU: o segundo up --wait falhou`. A ordem errada fica vermelha no passo de idempotência.
3. Volume em `/var/lib/postgresql/data`. Expected: `FALHOU: docker compose up --wait`, com o log do Postgres 18.6 dizendo que há dados em `/var/lib/postgresql/data (unused mount/volume)`. A imagem oficial já recusa essa montagem; a checagem de persistência continua valendo para outras regressões.
4. Seed com os três alvos da spec (num `seed.sab.sh`, a linha do laço como `for url in https://example.com/ https://www.iana.org/ https://www.wikipedia.org/ ${DEMO_TARGETS_EXTRA:-}; do`). Expected: `FALHOU: seed: 1 usuário(s) e 3 alvo(s), queria 1 e 1`.
5. Seed que ignora os extras (o laço como `for url in https://example.com/; do`), rodado com `DEMO_TARGETS_EXTRA="https://vigilume-extra.invalid/"`. Expected: `FALHOU: seed: 1 usuário(s) e 1 alvo(s), queria 1 e 2`.
6. Seed sem a linha do logout (`grep -v 'auth/logout' deploy/compose/seed.sh > seed.sab.sh`). Expected: `FALHOU: o seed deixou 1 sessão(ões) viva(s)`.
7. Em `compose.yaml`, troque um caractere do `JWT_SECRET` (o final `ZSE=` por `ZT8=`, que ainda é base64 válido). Run: `go test -count=1 -run TestComposeUsesTheDemoKeys ./internal/config/`. Expected: FAIL: `JWT_SECRET do compose.yaml não é a chave de demonstração 1 que o boot recusa`. Restaure.

- [ ] **Step 6: Commit**

```bash
git add compose.yaml deploy/compose/ deploy/ci/checar-compose.sh internal/config/compose_test.go Makefile
git commit -m "feat(compose): db → seed → app com usuário e alvos de demonstração, e as checagens do avaliador"
```

---

### Task 7: CI da imagem, do E2E e da publicação; deploy manual; Dependabot

O CI passa a construir a imagem em todo PR, passá-la no Trivy e nas checagens da Task 5, e rodar o E2E do Plano 6 **contra o compose do avaliador com essa mesma imagem** (D18): se o seed, o healthcheck, o init do Postgres ou o painel embutido quebrarem, o PR fica vermelho. Depois do verde, na `main` ou numa tag `v*`, o `publish` constrói a imagem multi-arch do mesmo código e com as mesmas bases, sobe para o GHCR (privado, como o repositório) **só pelo digest**, reescaneia com o Trivy, e só então dá as tags. O deploy é outro workflow, só à mão e só da `main`: copia do GHCR para o registry do Fly **pelo digest** a imagem publicada, e faz o deploy dela (D34). O Dependabot passa a atualizar tudo o que o repositório fixa por digest ou SHA.

**Files:**
- Modify: `.github/workflows/ci.yml`
- Create: `.github/workflows/deploy.yml`, `.github/dependabot.yml`

**Interfaces:**
- Consumes: `Dockerfile` (alvos `runtime` e `tools`), `deploy/ci/checar-imagem.sh`, `deploy/ci/trivy/Dockerfile`, `deploy/ci/checar-compose.sh` (`VIGILUME_TAG`, `UP_ARGS`), o E2E do Plano 6, `VIGILUME_EXIGIR_DIST` do teste `TestTodoArquivoDoDistTemTipo` (Plano 6); `fly.toml` (Task 8, lido pelo `deploy`).
- Produces:
  - Jobs `web`, `image`, `e2e` (em todo PR e push) e `publish` (só em push, depois de todos os outros).
  - Tags no GHCR: `ghcr.io/bipoxdxd/vigilume:{sha completo, latest}` e as mesmas com `-tools`; numa tag `v1.2.3`, também `:1.2.3` e `:1.2.3-tools`. O `publish` imprime o digest no resumo e o expõe como saída.
  - Workflow `deploy` (`workflow_dispatch`, input `digest`), só na `main`; secret **de repositório** `FLY_DEPLOY_TOKEN`.

**Contexto para quem implementa.** O job `image` constrói só amd64, com `load: true`, e passa as duas imagens (`:ci` e `:ci-tools`) ao `e2e` por um artifact de um dia (`docker save | gzip`, ~8 MB). O `e2e` roda o compose com `VIGILUME_TAG=ci` e `UP_ARGS=--no-build --pull never`: sem isso, o compose poderia construir outra imagem, ou puxar a publicada, e o E2E provaria uma imagem que o Trivy não viu. No `web`, depois do `npm run build`, o `go test ./internal/webui/` roda com `VIGILUME_EXIGIR_DIST=1` (no job `test` o dist só tem o `.gitkeep`, e o teste de MIME do dist real passaria sem conferir nada), e `git status --porcelain` vazio prova que o build não sujou a árvore.

O `publish` **constrói de novo**, para as duas arquiteturas: mesmo código, mesmas bases por digest, o cache `gha` do `image`, mas não é byte a byte a imagem do E2E (o binário é reprodutível; a imagem não, D9 da spec). Por isso ele sobe a imagem **só pelo digest** (`push-by-digest=true`, sem tag nenhuma), roda o Trivy nesse digest para `linux/amd64` e `linux/arm64`, e só então aponta as tags para ele (`docker buildx imagetools create`, que não muda o digest). `provenance: false`: o atestado viraria uma entrada `unknown/unknown` no índice. O Trivy usa dois espelhos do banco de CVEs (`--db-repository public.ecr.aws/…,ghcr.io/…`), porque o `ghcr.io` sozinho limita downloads anônimos.

O `deploy` é a trava que o GitHub Free com repositório privado permite: **não há environments** (nem revisor obrigatório, nem secret por environment); só quem tem acesso de escrita dispara um `workflow_dispatch` (hoje, só o dono), e o job só roda da `main`. Ele **recusa** um `fly.toml` com `TRUSTED_PROXIES` vazio (o valor é medido no primeiro deploy, à mão, e entra por PR: runbook, passo 9), faz o deploy com `--ha=false` (sem ele o primeiro deploy cria duas máquinas, e dois `--role=all` mandam cada notificação em dobro) e confere que ficou **uma** máquina. O `flyctl` vai com versão fixa (0.4.108). O input vai para o shell só por variável de ambiente, e só no formato `sha256:<64 hex>`. O `dependabot.yml` põe no ecossistema `docker` o Dockerfile do app, o do Postgres de produção e o do Trivy, e ignora versão **maior** de `postgres` e `node` (a maior do Postgres é dump e restauração num app novo, D36). Os três workflows declaram `shell: bash` (Task 4). **Nada disto roda sozinho na máquina de quem executa**: os jobs rodam no GitHub depois do push, e o `deploy` só quando o dono o dispara.

Minutos de Actions (repositório privado, 2.000/mês na conta pessoal): ~15 min de runner por push somando os jobs, ~5 min de relógio; o `concurrency` da Task 4 cancela a execução superada. Se apertar, o `e2e` é o primeiro a ir para só-`main` (D19; o runbook da Task 8 explica).

- [ ] **Step 1: Os jobs novos do CI**

Aplique em `.github/workflows/ci.yml`:

```diff
--- a/.github/workflows/ci.yml
+++ b/.github/workflows/ci.yml
@@ -2,6 +2,7 @@
 on:
   push:
     branches: [main]
+    tags: ['v*']
   pull_request:
 
 # Nenhum job escreve nada por padrão. Quem precisa de mais pede no job.
@@ -32,6 +33,9 @@
   GOSEC_VERSION: v2.29.0
   GOVULNCHECK_VERSION: v1.8.0
   GITLEAKS_VERSION: v8.30.1
+  # O nome com que o job image carrega a imagem e o e2e a usa: o mesmo do
+  # compose.yaml, com VIGILUME_TAG=ci.
+  IMAGEM: ghcr.io/bipoxdxd/vigilume
 
 jobs:
   lint:
@@ -106,3 +110,246 @@
         run: |
           go install github.com/zricethezav/gitleaks/v8@${GITLEAKS_VERSION}
           gitleaks git . --redact --no-banner --verbose
+
+  web:
+    runs-on: ubuntu-latest
+    defaults:
+      run:
+        shell: bash
+        working-directory: web
+    steps:
+      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
+      - uses: actions/setup-node@820762786026740c76f36085b0efc47a31fe5020 # v7.0.0
+        with:
+          node-version: '24'
+          cache: npm
+          cache-dependency-path: web/package-lock.json
+      - run: npm ci --no-audit --no-fund
+      - run: npm run typecheck
+      - run: npm run lint
+      - run: npx vitest run
+      - run: npm run build
+      - name: o build não suja a árvore
+        # O .gitignore ignora internal/webui/dist menos o .gitkeep (Plano 6):
+        # um build que apagasse o .gitkeep, ou escrevesse fora dali, aparece aqui.
+        working-directory: .
+        run: test -z "$(git status --porcelain)"
+
+      # O teste do internal/webui contra o dist de verdade. Com o dist vazio
+      # (só o .gitkeep, como no job test) ele passaria sem conferir nada;
+      # VIGILUME_EXIGIR_DIST=1 faz o dist vazio ser falha (Plano 6).
+      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
+        with:
+          go-version-file: go.mod
+      - name: go test do painel embutido
+        working-directory: .
+        env:
+          VIGILUME_EXIGIR_DIST: '1'
+        run: go test -count=1 ./internal/webui/
+
+  image:
+    # A imagem de produção que o e2e vai usar, só amd64 (a do runner). O
+    # publish constrói as duas arquiteturas do mesmo código, com o cache daqui.
+    runs-on: ubuntu-latest
+    steps:
+      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
+      - uses: docker/setup-buildx-action@f87e5991a6d7451dcb8d9637bfbc97413f497069 # v4.4.1
+
+      - name: build (runtime)
+        uses: docker/build-push-action@c3c9e263c25d99ce0380d002d59b67737d91b0dc # v7.4.0
+        with:
+          context: .
+          target: runtime
+          platforms: linux/amd64
+          load: true
+          tags: ${{ env.IMAGEM }}:ci
+          build-args: VERSION=${{ github.sha }}
+          cache-from: type=gha,scope=imagem
+          cache-to: type=gha,scope=imagem,mode=max
+
+      - name: build (tools, só o seed do compose)
+        uses: docker/build-push-action@c3c9e263c25d99ce0380d002d59b67737d91b0dc # v7.4.0
+        with:
+          context: .
+          target: tools
+          platforms: linux/amd64
+          load: true
+          tags: ${{ env.IMAGEM }}:ci-tools
+          build-args: VERSION=${{ github.sha }}
+          cache-from: type=gha,scope=imagem
+
+      - name: trivy
+        # Achado HIGH ou CRITICAL com correção disponível derruba o job. A
+        # imagem (por digest) vem de deploy/ci/trivy/Dockerfile, que o
+        # Dependabot atualiza.
+        run: |
+          trivy=$(sed -n 's/^FROM //p' deploy/ci/trivy/Dockerfile)
+          # Dois espelhos do banco de CVEs: o ghcr.io sozinho limita downloads
+          # anônimos e derruba o job de vez em quando.
+          docker run --rm -v /var/run/docker.sock:/var/run/docker.sock "$trivy" \
+            image --quiet --severity HIGH,CRITICAL --ignore-unfixed --exit-code 1 \
+            --db-repository public.ecr.aws/aquasecurity/trivy-db,ghcr.io/aquasecurity/trivy-db "$IMAGEM:ci"
+
+      - name: checagens da imagem
+        run: sh deploy/ci/checar-imagem.sh "$IMAGEM:ci" "$GITHUB_SHA"
+
+      - name: exportar para o e2e
+        run: docker save "$IMAGEM:ci" "$IMAGEM:ci-tools" | gzip -1 > "$RUNNER_TEMP/imagens.tar.gz"
+      - uses: actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a # v7.0.1
+        with:
+          name: imagens
+          path: ${{ runner.temp }}/imagens.tar.gz
+          retention-days: 1
+          if-no-files-found: error
+
+  e2e:
+    # O compose do avaliador, com a imagem que acabou de passar no Trivy (D18):
+    # se o seed, o healthcheck, o init do Postgres ou o painel embutido
+    # quebrarem, o PR fica vermelho.
+    needs: image
+    runs-on: ubuntu-latest
+    env:
+      VIGILUME_TAG: ci
+      # Nem pull nem build: a imagem é a do artifact, ou nada.
+      UP_ARGS: --no-build --pull never
+    steps:
+      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
+      - uses: actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c # v8.0.1
+        with:
+          name: imagens
+          path: ${{ runner.temp }}
+      - run: gunzip -c "$RUNNER_TEMP/imagens.tar.gz" | docker load
+
+      - name: compose (tempo, login, papel, seed, idempotência, persistência)
+        run: sh deploy/ci/checar-compose.sh
+
+      - uses: actions/setup-node@820762786026740c76f36085b0efc47a31fe5020 # v7.0.0
+        with:
+          node-version: '24'
+          cache: npm
+          cache-dependency-path: web/package-lock.json
+      - name: playwright
+        working-directory: web
+        env:
+          E2E_BASE_URL: http://127.0.0.1:8080
+          E2E_EMAIL: demo@vigilume.local
+          E2E_PASSWORD: demo-vigilume-2026
+        run: |
+          npm ci --no-audit --no-fund
+          npx playwright install --with-deps chromium
+          npx playwright test
+
+      - name: logs do compose
+        if: failure()
+        run: docker compose logs
+      - uses: actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a # v7.0.1
+        if: failure()
+        with:
+          name: playwright
+          path: web/test-results
+          retention-days: 7
+
+  publish:
+    # Só depois de tudo verde, e só em push para a main ou tag v*. O pacote do
+    # GHCR segue a visibilidade do repositório: privado, por enquanto.
+    if: github.event_name == 'push'
+    needs: [lint, test, secrets, web, image, e2e]
+    runs-on: ubuntu-latest
+    permissions:
+      contents: read
+      packages: write
+    outputs:
+      digest: ${{ steps.runtime.outputs.digest }}
+    env:
+      VERSION: ${{ github.ref_type == 'tag' && github.ref_name || github.sha }}
+    steps:
+      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
+      - uses: docker/setup-buildx-action@f87e5991a6d7451dcb8d9637bfbc97413f497069 # v4.4.1
+      - uses: docker/login-action@dbcb813823bdd20940b903addbd779551569679f # v4.6.0
+        with:
+          registry: ghcr.io
+          username: ${{ github.actor }}
+          password: ${{ secrets.GITHUB_TOKEN }}
+
+      # :<sha> e :latest na main; :<versão> numa tag v*. O tools ganha o
+      # sufixo -tools, e o compose.yaml usa :latest-tools.
+      - id: meta
+        uses: docker/metadata-action@dc802804100637a589fabce1cb79ff13a1411302 # v6.2.0
+        with:
+          images: ${{ env.IMAGEM }}
+          tags: |
+            type=sha,format=long,prefix=
+            type=raw,value=latest,enable={{is_default_branch}}
+            type=semver,pattern={{version}}
+      - id: meta-tools
+        uses: docker/metadata-action@dc802804100637a589fabce1cb79ff13a1411302 # v6.2.0
+        with:
+          images: ${{ env.IMAGEM }}
+          flavor: suffix=-tools,onlatest=true
+          tags: |
+            type=sha,format=long,prefix=
+            type=raw,value=latest,enable={{is_default_branch}}
+            type=semver,pattern={{version}}
+
+      # O publish CONSTRÓI de novo, para as duas arquiteturas: é o mesmo
+      # código, com as mesmas bases fixadas por digest e o cache do job image,
+      # mas não é byte a byte a imagem que o e2e viu (o binário é
+      # reprodutível; a imagem não, D9 da spec). Por isso ela é reescaneada
+      # ANTES de ganhar qualquer tag: primeiro sobe só pelo digest, depois o
+      # Trivy, e só então :sha e :latest apontam para ela.
+      - id: runtime
+        uses: docker/build-push-action@c3c9e263c25d99ce0380d002d59b67737d91b0dc # v7.4.0
+        with:
+          context: .
+          target: runtime
+          platforms: linux/amd64,linux/arm64
+          outputs: type=image,name=${{ env.IMAGEM }},push-by-digest=true,name-canonical=true,push=true
+          labels: ${{ steps.meta.outputs.labels }}
+          build-args: VERSION=${{ env.VERSION }}
+          cache-from: type=gha,scope=imagem
+          # Sem atestado de proveniência: ele vira uma entrada "unknown/unknown"
+          # no índice, que o Fly e o Trivy teriam de pular.
+          provenance: false
+
+      - name: trivy do digest publicado
+        env:
+          DIGEST: ${{ steps.runtime.outputs.digest }}
+        run: |
+          trivy=$(sed -n 's/^FROM //p' deploy/ci/trivy/Dockerfile)
+          for plataforma in linux/amd64 linux/arm64; do
+            docker run --rm -v "$HOME/.docker/config.json:/root/.docker/config.json:ro" "$trivy" \
+              image --quiet --severity HIGH,CRITICAL --ignore-unfixed --exit-code 1 \
+              --db-repository public.ecr.aws/aquasecurity/trivy-db,ghcr.io/aquasecurity/trivy-db \
+              --platform "$plataforma" "$IMAGEM@$DIGEST"
+          done
+
+      - name: tags da imagem conferida
+        env:
+          DIGEST: ${{ steps.runtime.outputs.digest }}
+          TAGS: ${{ steps.meta.outputs.tags }}
+        run: |
+          args=()
+          while read -r tag; do [ -n "$tag" ] && args+=(-t "$tag"); done <<< "$TAGS"
+          docker buildx imagetools create "${args[@]}" "$IMAGEM@$DIGEST"
+
+      - uses: docker/build-push-action@c3c9e263c25d99ce0380d002d59b67737d91b0dc # v7.4.0
+        with:
+          context: .
+          target: tools
+          platforms: linux/amd64,linux/arm64
+          push: true
+          tags: ${{ steps.meta-tools.outputs.tags }}
+          labels: ${{ steps.meta-tools.outputs.labels }}
+          build-args: VERSION=${{ env.VERSION }}
+          cache-from: type=gha,scope=imagem
+          provenance: false
+
+      - name: resumo
+        env:
+          DIGEST: ${{ steps.runtime.outputs.digest }}
+        run: |
+          {
+            echo "Imagem publicada: \`$IMAGEM@$DIGEST\`"
+            echo
+            echo "Deploy: Actions → deploy → Run workflow com este digest."
+          } >> "$GITHUB_STEP_SUMMARY"
```

- [ ] **Step 2: O deploy manual**

Crie `.github/workflows/deploy.yml`:

```yaml
name: deploy
# Deploy no Fly.io, SÓ à mão (contrato, decisão 6): Actions → deploy → Run
# workflow, com o digest que o job publish do ci imprimiu, a partir da main.
#
# Quem pode disparar: só quem tem acesso de escrita ao repositório (hoje, só
# o dono). No GitHub Free com repositório privado não há environment com
# revisor obrigatório; essa é a trava que existe. Quando o repositório abrir,
# um environment "production" com revisor pode ser acrescentado (runbook,
# "Tornar o repositório público").
#
# O Fly puxa de registry.fly.io, não do GHCR (D34): o pacote do GHCR é
# privado, e dar ao Fly uma credencial do GitHub seria guardar um token
# pessoal fora do GitHub. O job copia pelo digest a imagem que o publish
# empurrou e reescaneou, sem rebuild.
on:
  workflow_dispatch:
    inputs:
      digest:
        description: 'digest da imagem publicada (sha256:…), do resumo do job publish'
        required: true
        type: string

permissions:
  contents: read
  packages: read

# Um deploy nunca é cancelado no meio por outro.
concurrency:
  group: deploy
  cancel-in-progress: false

# shell: bash dá `bash -eo pipefail` a todo run (o padrão é só `bash -e`).
defaults:
  run:
    shell: bash

jobs:
  deploy:
    # Só da main: o fly.toml do deploy é o que passou por PR.
    if: github.ref == 'refs/heads/main'
    runs-on: ubuntu-latest
    timeout-minutes: 15
    env:
      DIGEST: ${{ inputs.digest }}
      ORIGEM: ghcr.io/bipoxdxd/vigilume
      DESTINO: registry.fly.io/vigilume
    steps:
      - name: conferir o digest
        # O input vai para o shell só por variável de ambiente, e só no
        # formato de um digest: nada de texto livre na linha de comando.
        run: |
          if ! printf '%s' "$DIGEST" | grep -Eqx 'sha256:[0-9a-f]{64}'; then
            echo "digest inválido: use sha256:<64 hex>" >&2
            exit 1
          fi
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1

      - name: conferir o TRUSTED_PROXIES
        # Vazio, todo cliente divide um balde de rate limit e o mesmo ip_hash
        # (D22). O valor é medido no primeiro deploy, feito à mão (runbook,
        # passo 9), e entra no fly.toml por PR antes de o workflow ser usado.
        run: |
          if ! grep -Eq '^[[:space:]]*TRUSTED_PROXIES[[:space:]]*=[[:space:]]*"[^"[:space:]]+"' fly.toml; then
            echo "TRUSTED_PROXIES vazio no fly.toml: meça o proxy do Fly (docs/deploy/fly.md, passo 9) e faça o PR antes" >&2
            exit 1
          fi

      - uses: docker/setup-buildx-action@f87e5991a6d7451dcb8d9637bfbc97413f497069 # v4.4.1
      - uses: superfly/flyctl-actions/setup-flyctl@ed8efb33836e8b2096c7fd3ba1c8afe303ebbff1 # 1.6
        with:
          version: 0.4.108
      - uses: docker/login-action@dbcb813823bdd20940b903addbd779551569679f # v4.6.0
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      # Secret de repositório: FLY_DEPLOY_TOKEN = `fly tokens create deploy -a
      # vigilume`, que só alcança este app.
      - name: copiar para o registry do Fly, pelo digest
        env:
          FLY_API_TOKEN: ${{ secrets.FLY_DEPLOY_TOKEN }}
        run: |
          flyctl auth docker
          # Copia o índice multi-arch inteiro; o digest continua o mesmo.
          docker buildx imagetools create -t "$DESTINO:run-$GITHUB_RUN_ID" "$ORIGEM@$DIGEST"

      - name: flyctl deploy
        env:
          FLY_API_TOKEN: ${{ secrets.FLY_DEPLOY_TOKEN }}
        # rolling: com uma máquina, alguns segundos fora (D26). --ha=false:
        # sem ele, o primeiro deploy de um app cria DUAS máquinas, e dois
        # processos --role=all mandariam cada notificação em dobro.
        run: |
          flyctl deploy --image "$DESTINO@$DIGEST" --strategy rolling --ha=false
          maquinas=$(flyctl machines list -a vigilume -q | grep -c . || true)
          echo "máquinas do app: $maquinas"
          [ "$maquinas" = 1 ] || { echo "queria exatamente 1 máquina" >&2; exit 1; }
```

- [ ] **Step 3: O Dependabot**

Crie `.github/dependabot.yml`:

```yaml
# Atualizações de dependência (spec §5.9; D20 do Plano 7). Tudo o que o
# repositório fixa por versão, SHA ou digest tem de ter quem o atualize, ou a
# fixação vira atraso: um CVE corrigido na base fica na imagem para sempre.
#
# As imagens que só um workflow usa (o Trivy do ci.yml; o Postgres do
# backup.yml é o de produção) têm um Dockerfile de uma linha cada, lido pelo
# workflow: o Dependabot não lê o `env` de um workflow. Ficam à mão só as
# versões das ferramentas Go no env do ci.yml.
version: 2
updates:
  - package-ecosystem: gomod
    directory: /
    schedule:
      interval: weekly
    groups:
      go:
        patterns: ['*']

  - package-ecosystem: npm
    directory: /web
    schedule:
      interval: weekly
    groups:
      painel:
        patterns: ['*']

  - package-ecosystem: github-actions
    directory: /
    schedule:
      interval: weekly

  # O Dockerfile do app (node, golang, distroless); o do Postgres de
  # produção (que o fly.toml e o backup.yml usam: o Dependabot não lê
  # nenhum dos dois) e o do Trivy do CI.
  - package-ecosystem: docker
    directories:
      - /
      - /deploy/fly/db
      - /deploy/ci/trivy
    schedule:
      interval: weekly
    # Versão MAIOR de Postgres e Node não vem por PR: o Postgres maior é dump
    # e restauração num app novo (D36; runbook, "Upgrade maior"), e o Node
    # maior muda o que o build do painel exige. Menores e digests, sim.
    ignore:
      - dependency-name: postgres
        update-types: ['version-update:semver-major']
      - dependency-name: node
        update-types: ['version-update:semver-major']

  # O Postgres do compose.yaml, o mesmo digest do de produção.
  - package-ecosystem: docker-compose
    directory: /
    schedule:
      interval: weekly
    # Versão MAIOR de Postgres e Node não vem por PR: o Postgres maior é dump
    # e restauração num app novo (D36; runbook, "Upgrade maior"), e o Node
    # maior muda o que o build do painel exige. Menores e digests, sim.
    ignore:
      - dependency-name: postgres
        update-types: ['version-update:semver-major']
      - dependency-name: node
        update-types: ['version-update:semver-major']
```

- [ ] **Step 4: Conferir localmente**

Run: `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 .github/workflows/*.yml`
Expected: sem saída, código 0.

O job `e2e`, repetido na máquina, com a imagem passando pelo mesmo caminho do artifact:

```bash
I=ghcr.io/bipoxdxd/vigilume
docker build --target runtime --build-arg VERSION=local -t $I:ci .
docker build --target tools --build-arg VERSION=local -t $I:ci-tools .
docker save $I:ci $I:ci-tools | gzip -1 > /tmp/imagens.tar.gz
docker rmi $I:ci $I:ci-tools && gunzip -c /tmp/imagens.tar.gz | docker load
docker compose down -v
VIGILUME_TAG=ci UP_ARGS="--no-build --pull never" sh deploy/ci/checar-compose.sh
cd web && E2E_BASE_URL=http://127.0.0.1:8080 E2E_EMAIL=demo@vigilume.local \
  E2E_PASSWORD=demo-vigilume-2026 npx playwright test; cd ..
docker compose down -v && docker rmi $I:ci $I:ci-tools && rm /tmp/imagens.tar.gz
```

Expected: `Loaded image` duas vezes; `compose conferido` **sem** nenhum `Pulling` nem `Building`; `6 passed`. (Na validação: `up --wait` em 5,8 s; artifact de 8,3 MB.)

O job `web`, na máquina: `cd web && npm ci && npm run typecheck && npm run lint && npx vitest run && npm run build && cd .. && VIGILUME_EXIGIR_DIST=1 go test -count=1 ./internal/webui/ && git status --porcelain`. Expected: tudo verde e o `git status` sem saída.

O `publish`, contra um registry local no lugar do GHCR (o que dá para ensaiar sem publicar nada):

```bash
docker run -d --name registry-ensaio -p 127.0.0.1:35000:5000 registry:3
docker buildx create --name ensaio-pub --driver docker-container --driver-opt network=host
IMAGEM=localhost:35000/vigilume
docker buildx build --builder ensaio-pub --platform linux/amd64,linux/arm64 --target runtime \
  --build-arg VERSION=ensaio --provenance=false --metadata-file /tmp/meta.json \
  --output type=image,name=$IMAGEM,push-by-digest=true,name-canonical=true,push=true .
DIGEST=$(sed -n 's/.*"containerimage.digest": *"\([^"]*\)".*/\1/p' /tmp/meta.json)
curl -s http://127.0.0.1:35000/v2/vigilume/tags/list; echo
for p in linux/amd64 linux/arm64; do
  docker run --rm --network host "$(sed -n 's/^FROM //p' deploy/ci/trivy/Dockerfile)" image --quiet \
    --severity HIGH,CRITICAL --ignore-unfixed --exit-code 1 \
    --db-repository public.ecr.aws/aquasecurity/trivy-db,ghcr.io/aquasecurity/trivy-db \
    --platform "$p" "$IMAGEM@$DIGEST"
done
docker buildx imagetools create -t $IMAGEM:ensaio -t $IMAGEM:latest "$IMAGEM@$DIGEST"
docker buildx imagetools inspect $IMAGEM:latest | grep '^Digest'; echo "$DIGEST"
docker rm -f registry-ensaio && docker buildx rm ensaio-pub
```

Expected: o `tags/list` responde `NAME_UNKNOWN` (a imagem subiu **sem** tag); o Trivy dá 0 nas duas plataformas; o `inspect` de `:latest` mostra o **mesmo** digest; o índice tem só `linux/amd64` e `linux/arm64` (sem `unknown/unknown`).

A trava do `TRUSTED_PROXIES`, com o `fly.toml` da Task 8 (o comando é o do passo "conferir o TRUSTED_PROXIES"):

```bash
confere() { grep -Eq '^[[:space:]]*TRUSTED_PROXIES[[:space:]]*=[[:space:]]*"[^"[:space:]]+"' "$1" && echo passa || echo recusa; }
confere fly.toml
sed 's#^  TRUSTED_PROXIES  = .*#  TRUSTED_PROXIES  = "172.16.0.0/12"#' fly.toml > /tmp/fly.toml && confere /tmp/fly.toml
```

Expected: `recusa` (o `fly.toml` do repositório nasce vazio) e depois `passa`. (Rode este depois da Task 8, ou volte a ele: o `fly.toml` é dela.)

- [ ] **Step 5: Sabotagem**

1. No bloco do `e2e`, apague a linha `UP_ARGS: --no-build --pull never` e rode o trecho do Step 4 **sem** o `docker load` (sem a imagem `:ci` na máquina). Expected: o compose tenta o pull (`denied`) e **constrói** do código: o E2E passaria com uma imagem que o Trivy não viu. Com a linha, o mesmo cenário falha em `docker compose up` (`No such image: ghcr.io/bipoxdxd/vigilume:ci-tools`): é a trava. Restaure.
2. `VIGILUME_EXIGIR_DIST=1 go test -count=1 ./internal/webui/` com o dist só com o `.gitkeep` (`git stash -u` não serve: o dist é ignorado; mova o `internal/webui/dist` para fora, recrie só o `.gitkeep`, rode e devolva). Expected: FAIL em `TestTodoArquivoDoDistTemTipo` (a guarda do Plano 6). Sem a variável, o mesmo teste passa: é por isso que o job `web` a define.
3. No ensaio do `publish`, troque a `output` por `type=image,name=$IMAGEM:ensaio,push=true` (com tag, sem `push-by-digest`). Expected: o `tags/list` já mostra `ensaio` **antes** do Trivy: uma imagem com achado estaria com tag, e o compose de quem tem acesso a puxaria.

As sabotagens dos jobs `image` e `e2e` são as das Tasks 5 e 6: os jobs só chamam os scripts.

- [ ] **Step 6: Commit**

```bash
git add .github/
git commit -m "ci: imagem com Trivy, E2E contra o compose, publicação reescaneada no GHCR e deploy manual por digest"
```

---

### Task 8: Fly.io, backup e runbook

Os artefatos de produção: o `fly.toml` do app (escrito à mão: o `fly launch` geraria `auto_stop_machines`, e um monitor desligado por falta de tráfego para de sondar em silêncio, D23), o app do Postgres 18 oficial num volume, sem gerência do Fly (D28), o workflow de backup diário com restauração conferida e aviso de disco (D32, D35), e o runbook. **Nada aqui roda sozinho nem é executado nesta tarefa**: criar apps, volumes, papéis, segredos e os secrets e variáveis do GitHub, e cada deploy, são do dono do projeto, com confirmação explícita (contrato, decisão 6; spec 6.4).

**Files:**
- Create: `fly.toml`, `deploy/fly/db/fly.toml`, `deploy/fly/db/Dockerfile`
- Create: `deploy/backup/backup.sh`, `deploy/backup/verificar.sql`, `.github/workflows/backup.yml`
- Create: `docs/deploy/fly.md`

**Interfaces:**
- Consumes: `CLIENT_IP_HEADER` (Task 1); `deploy/compose/initdb.sql` (Task 6, o mesmo SQL do papel); o workflow `deploy` (Task 7); `vigilume user create`.
- Produces:
  - Apps `vigilume` (HTTP público, `/healthz`, `kill_timeout = "25s"`, `min_machines_running = 1`, `auto_start_machines = true`, `[[restart]] policy = "always"`) e `vigilume-db` (sem serviço público, `vigilume-db.internal:5432`, volume `pgdata` em `/var/lib/postgresql`; imagem por `--image`, lida de `deploy/fly/db/Dockerfile`).
  - `backup.sh` (roda dentro de `postgres:18-alpine`): `ORIGEM_URL` (como `vigilume_backup`), `PGHOST/PGPORT/PGUSER/PGPASSWORD` (o descartável), `SAIDA`, `VOLUME_BYTES`. Código 0 = dump restaurado e conferido; 3 = uma checagem falhou; 4 = conferido, mas o disco passou de 85% (o dump é cifrado e guardado mesmo assim, e o job fica vermelho no fim).
  - Workflow `backup` (diário às 06:17 UTC e à mão), **só com a variável `BACKUP_ENABLED=1`**; secrets de repositório `FLY_BACKUP_TOKEN`, `BACKUP_DATABASE_PASSWORD`, `AGE_RECIPIENT`; variável `PG_VOLUME_GB`. Artifact cifrado com `age`, 30 dias.

**Contexto para quem implementa.** O `pg_dump` **do dono** das tabelas falha (`query would be affected by row-level security policy`): as tabelas têm `FORCE ROW LEVEL SECURITY`. Por isso existe o papel `vigilume_backup` (`NOSUPERUSER BYPASSRLS` + `pg_read_all_data`), que o boot recusa e que o `DATABASE_URL` nunca usa. A restauração é como **superusuário, preservando os donos**: com `--no-owner` as tabelas voltariam com o dono errado, e o boot seguinte falharia no `Migrate` ou na manutenção. O `deploy/fly/db/Dockerfile` tem uma linha só: existe para o Dependabot enxergar o digest, que ele não lê no `fly.toml`; o `backup.yml` lê a imagem da mesma linha (`sed -n 's/^FROM //p'`), e assim o dump e a restauração usam, por construção, a versão do servidor de produção. O mesmo Dockerfile serve ao deploy do banco: `fly deploy --image "$(sed -n 's/^FROM //p' deploy/fly/db/Dockerfile)"`, direto do Docker Hub, sem build remoto e sem depender de como o `flyctl` resolve um caminho de Dockerfile relativo ao `-c` (não ensaiado no Fly: a validação não tem conta; as flags foram conferidas no `flyctl` 0.4.108). O `backup.yml` declara `shell: bash`: com o padrão do GitHub (`bash -e`, sem `pipefail`), o `backup.sh | tee` do passo principal deixaria o job **verde** com a checagem ou o disco falhando, porque o código do pipe é o do `tee` (achado na revisão; prova no Step 4). O job só roda com `BACKUP_ENABLED=1`: antes do primeiro deploy, o agendamento diário termina em "skipped", sem gastar minutos nem falhar. No `fly.toml` do app, `auto_start_machines = true` e `[[restart]] policy = "always"`: o proxy nunca desliga a máquina (D23), mas liga de novo uma que parou, e um processo que sai por qualquer motivo volta. As contagens de `verificar.sql` comparam com `>=`: a contagem da origem é de **antes** do dump, e um alvo cadastrado no meio entraria no dump e não na contagem. O `backup.yml` não é executável fora do GitHub (precisa do `flyctl proxy` e dos secrets); o Step 3 repete os mesmos passos localmente, com o túnel trocado por uma porta publicada.

- [ ] **Step 1: Os dois apps**

Crie `fly.toml`:

```toml
# App do Vigilume no Fly.io (Plano 7). O deploy é manual ou pelo workflow
# "deploy", sempre com a imagem testada, copiada do GHCR para o registry do
# Fly e referida pelo digest (D34; docs/deploy/fly.md):
#
#   fly deploy --image registry.fly.io/vigilume@sha256:<digest>
#
# Escrito à mão, sem `fly launch`: ele geraria auto_stop_machines, e um
# monitor desligado por falta de tráfego para de sondar em silêncio (D23).
app = "vigilume"
primary_region = "gru"

# O ShutdownGrace do app é 15 s (spec §6.2); o padrão do Fly (5 s) mataria o
# shutdown gracioso no meio (D24).
kill_signal = "SIGTERM"
kill_timeout = "25s"

[env]
  PUBLIC_BASE_URL  = "https://vigilume.fly.dev"
  CORS_ORIGIN      = "https://vigilume.fly.dev"
  # O IP do cliente vem do cabeçalho do proxy do Fly, e só de TRUSTED_PROXIES
  # (D22). TRUSTED_PROXIES começa vazio: o aviso "cabeçalho de IP do cliente
  # ignorado" no log mostra o remote_addr do proxy, e o runbook (passo 9) diz
  # como preencher.
  CLIENT_IP_HEADER = "Fly-Client-IP"
  TRUSTED_PROXIES  = ""
  METRICS_ADDR     = "off"
  LOG_LEVEL        = "info"

[http_service]
  internal_port = 8080
  force_https = true
  # O proxy nunca desliga a máquina (D23), e liga de novo uma que tenha
  # parado por outro motivo.
  auto_stop_machines = "off"
  auto_start_machines = true
  min_machines_running = 1

  # /healthz, não /readyz: o servidor só escuta depois de CheckRole e Migrate,
  # e um soluço do banco não deve tirar a máquina única da rota (D25).
  [[http_service.checks]]
    method = "GET"
    path = "/healthz"
    interval = "15s"
    timeout = "2s"
    grace_period = "20s"

# Um processo que sai (panic, OOM, erro do errgroup) volta sempre, e não só
# com código diferente de zero (o padrão on-failure): o monitor que para
# calado é o defeito que mais custa.
[[restart]]
  policy = "always"

[[vm]]
  size = "shared-cpu-1x"
  memory = "512mb"
```

Crie `deploy/fly/db/Dockerfile`:

```dockerfile
# Postgres de produção do Vigilume: a imagem OFICIAL, a mesma (tag e digest)
# do compose e do workflow de backup (D28, D29 do Plano 7). Este arquivo
# não se constrói: existe para o Dependabot enxergar o digest (ele não lê o
# fly.toml), e o deploy (--image) e o backup.yml leem a imagem da linha FROM.
FROM postgres:18-alpine@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873
```

Crie `deploy/fly/db/fly.toml`:

```toml
# Postgres de produção, SEM gerência do Fly (decisão do usuário, D28 do Plano
# 7). Backup, disco e upgrade ficam com o operador: docs/deploy/fly.md.
#
#   fly deploy -c deploy/fly/db/fly.toml --ha=false \
#     --image "$(sed -n 's/^FROM //p' deploy/fly/db/Dockerfile)"
#
# A imagem vai por --image, direto do Docker Hub e pelo digest do Dockerfile
# (que o Dependabot atualiza): nada de build remoto, e nada de depender de
# como o flyctl resolve um caminho de Dockerfile relativo a um -c.
app = "vigilume-db"
primary_region = "gru"

# "Fast shutdown" do Postgres: é o STOPSIGNAL da imagem oficial. Um SIGKILL no
# meio do checkpoint força recuperação por WAL no boot seguinte.
kill_signal = "SIGINT"
kill_timeout = "60s"

# Layout do 18: PGDATA=/var/lib/postgresql/18/docker, e o volume é o diretório
# de cima. Montar em .../data grava num volume anônimo que some.
[mounts]
  source = "pgdata"
  destination = "/var/lib/postgresql"

# Sem [http_service] e sem [[services]]: o banco não passa pelo proxy público.
# A aplicação chega em vigilume-db.internal:5432, pela rede privada (6PN).
[checks]
  [checks.postgres]
    type = "tcp"
    port = 5432
    interval = "15s"
    timeout = "2s"
    grace_period = "30s"

[[vm]]
  size = "shared-cpu-1x"
  memory = "512mb"
```

- [ ] **Step 2: O backup**

Crie `deploy/backup/backup.sh`:

```bash
#!/bin/sh
# Backup lógico do Vigilume com restauração conferida (D32 do Plano 7).
#
# Roda DENTRO de postgres:18-alpine (o cliente tem de ser da mesma versão
# maior do servidor; o pg_dump do runner pode ser mais velho e recusar).
#
#   ORIGEM_URL   conexão com o banco de produção como vigilume_backup
#                (NOSUPERUSER BYPASSRLS + pg_read_all_data). O dono das
#                tabelas NÃO serve: com FORCE ROW LEVEL SECURITY, o pg_dump
#                dele falha.
#   PGHOST, PGPORT, PGUSER, PGPASSWORD
#                o Postgres DESCARTÁVEL da restauração, como superusuário.
#   SAIDA        caminho do dump (formato custom).
#   VOLUME_BYTES opcional: tamanho do volume de produção, para o aviso de disco.
#
# O dump só é dado como bom depois de restaurado e conferido. Texto claro
# nunca sai daqui: quem chama cifra o arquivo antes de guardá-lo.
set -eu

: "${ORIGEM_URL:?defina ORIGEM_URL}"
: "${SAIDA:?defina SAIDA}"
: "${PGHOST:?defina PGHOST (o Postgres da restauração)}"
AQUI=$(cd "$(dirname "$0")" && pwd)

origem() { psql --no-psqlrc -X -Atq -v ON_ERROR_STOP=1 "$ORIGEM_URL" -c "$1"; }

echo "== origem"
versao=$(origem "SELECT max(version_id) FROM goose_db_version")
usuarios=$(origem "SELECT count(*) FROM users")
alvos=$(origem "SELECT count(*) FROM targets")
echo "schema $versao, $usuarios usuário(s), $alvos alvo(s)"

echo "== dump"
pg_dump --format=custom --no-password --file="$SAIDA" "$ORIGEM_URL"
ls -l "$SAIDA"

echo "== restauração"
# O mesmo SQL do compose e de produção: papel comum, dono do banco.
psql --no-psqlrc -X -q -v ON_ERROR_STOP=1 -d postgres -f "$AQUI/../compose/initdb.sql"
# Como superusuário e PRESERVANDO os donos do dump: restaurar como o papel da
# aplicação esbarra no FORCE ROW LEVEL SECURITY, e --no-owner deixaria as
# tabelas com o dono errado (medido no spike do Plano 7).
pg_restore --exit-on-error --no-password -d vigilume "$SAIDA"

echo "== checagens"
psql --no-psqlrc -X -q -d vigilume -v versao="$versao" -v usuarios="$usuarios" -v alvos="$alvos" \
    -f "$AQUI/verificar.sql"

echo "== disco"
ocupado=$(origem "SELECT sum(pg_database_size(datname)) FROM pg_database")
echo "bancos ocupam $ocupado bytes"
if [ -n "${VOLUME_BYTES:-}" ]; then
    pct=$((ocupado * 100 / VOLUME_BYTES))
    resumo="Postgres: ${pct}% do volume (${ocupado} de ${VOLUME_BYTES} bytes, sem contar o WAL)"
    echo "$resumo"
    if [ "$pct" -ge 85 ]; then
        echo "ERRO: acima de 85%: aumente o volume (fly volumes extend)" >&2
        exit 3
    elif [ "$pct" -ge 70 ]; then
        echo "AVISO: acima de 70% do volume"
    fi
fi
echo "backup conferido"
```

Crie `deploy/backup/verificar.sql`:

```sql
-- Checagens da cópia restaurada. Rodam no banco restaurado, como
-- superusuário, com as variáveis do psql que backup.sh lê da ORIGEM:
-- :versao (a maior versão do goose), :usuarios e :alvos (contagens).
-- Qualquer RAISE derruba o psql (ON_ERROR_STOP) e o job fica vermelho.
\set ON_ERROR_STOP on
\o /dev/null
SELECT set_config('verificar.versao', :'versao', false),
       set_config('verificar.usuarios', :'usuarios', false),
       set_config('verificar.alvos', :'alvos', false);
\o

DO $$
DECLARE
    alheias   text;
    sem_force text;
    com_rls   int;
    versao    bigint;
    n         bigint;
BEGIN
    -- 1. Dono: a aplicação só roda como dona das tabelas (D30). Restaurar com
    --    --no-owner deixaria tudo com o superusuário, e o boot seguinte
    --    falharia no Migrate ou na manutenção.
    SELECT string_agg(tablename, ', ' ORDER BY tablename) INTO alheias
      FROM pg_tables WHERE schemaname = 'public' AND tableowner <> 'vigilume_app';
    IF alheias IS NOT NULL THEN
        RAISE EXCEPTION 'tabelas sem o dono vigilume_app: %', alheias;
    END IF;

    -- 2. RLS: toda tabela com política continua com FORCE depois da volta.
    SELECT count(*) FILTER (WHERE c.relrowsecurity),
           string_agg(c.relname, ', ') FILTER (WHERE c.relrowsecurity AND NOT c.relforcerowsecurity)
      INTO com_rls, sem_force
      FROM pg_class c JOIN pg_namespace ns ON ns.oid = c.relnamespace
     WHERE ns.nspname = 'public' AND c.relkind IN ('r', 'p');
    IF com_rls = 0 THEN
        RAISE EXCEPTION 'nenhuma tabela com row-level security: a restauração perdeu as políticas';
    END IF;
    IF sem_force IS NOT NULL THEN
        RAISE EXCEPTION 'tabelas com RLS sem FORCE: %', sem_force;
    END IF;

    -- 3. Schema: a mesma versão da origem.
    SELECT max(version_id) INTO versao FROM goose_db_version;
    IF versao IS DISTINCT FROM current_setting('verificar.versao')::bigint THEN
        RAISE EXCEPTION 'versão do schema restaurado = %, origem = %', versao, current_setting('verificar.versao');
    END IF;

    -- 4. Dados: as tabelas que mudam devagar voltam inteiras. A contagem da
    --    origem é de ANTES do dump (outro instante): um alvo cadastrado no meio
    --    entra no dump e não na contagem. Por isso ">=", e não "=". (checks
    --    muda a cada minuto e não entra.)
    SELECT count(*) INTO n FROM users;
    IF n < 1 OR n < current_setting('verificar.usuarios')::bigint THEN
        RAISE EXCEPTION 'users restaurado = %, origem = %', n, current_setting('verificar.usuarios');
    END IF;
    SELECT count(*) INTO n FROM targets;
    IF n < current_setting('verificar.alvos')::bigint THEN
        RAISE EXCEPTION 'targets restaurado = %, origem = %', n, current_setting('verificar.alvos');
    END IF;

    -- 5. Partições de checks: sem elas o sondador não grava.
    SELECT count(*) INTO n FROM pg_inherits WHERE inhparent = 'public.checks'::regclass;
    IF n < 1 THEN
        RAISE EXCEPTION 'checks restaurado sem partições';
    END IF;

    RAISE NOTICE 'cópia conferida: schema %, % usuário(s), % alvo(s), % tabela(s) com RLS',
        versao, current_setting('verificar.usuarios'), current_setting('verificar.alvos'), com_rls;
END
$$;
```

Crie `.github/workflows/backup.yml`:

```yaml
name: backup
# Backup lógico diário do Postgres de produção, com restauração conferida
# (D32 do Plano 7) e aviso de disco (D35). Desligado até o dono ligar: o
# job só roda com a variável de repositório BACKUP_ENABLED=1 (runbook, passo
# 13). Antes disso, o agendamento diário termina em "skipped", sem gastar
# minutos nem falhar por falta de banco.
on:
  schedule:
    - cron: '17 6 * * *' # 06:17 UTC, fora da hora cheia em que o GitHub enfileira tudo
  workflow_dispatch:

permissions:
  contents: read

# Dois backups ao mesmo tempo disputariam o túnel e a porta; nenhum é cancelado.
concurrency:
  group: backup
  cancel-in-progress: false

# shell: bash dá `bash -eo pipefail` a todo run. O padrão do GitHub é
# `bash -e`, sem pipefail: um `comando | tee log` que falha deixaria o passo
# verde, porque o código do pipe é o do tee.
defaults:
  run:
    shell: bash

env:
  AGE_VERSION: v1.3.2

jobs:
  backup:
    if: vars.BACKUP_ENABLED == '1'
    runs-on: ubuntu-latest
    # Secrets de REPOSITÓRIO (no GitHub Free com repositório privado não há
    # environment): FLY_BACKUP_TOKEN (token de deploy do app vigilume-db, que
    # só alcança esse app), BACKUP_DATABASE_PASSWORD (papel vigilume_backup),
    # AGE_RECIPIENT (chave PÚBLICA do age; a privada fica com o dono, fora do
    # GitHub e do Fly). Variáveis: BACKUP_ENABLED, PG_VOLUME_GB.
    timeout-minutes: 20
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          sparse-checkout: deploy
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version: '1.26'
          cache: false
      - uses: superfly/flyctl-actions/setup-flyctl@ed8efb33836e8b2096c7fd3ba1c8afe303ebbff1 # 1.6
        with:
          version: 0.4.108

      - name: age
        run: go install filippo.io/age/cmd/age@${AGE_VERSION}

      - name: imagem do Postgres
        # A MESMA imagem (e digest) do Postgres de produção, lida do
        # Dockerfile dele, que o Dependabot atualiza: pg_dump e pg_restore da
        # mesma versão do servidor, e um digest só para manter.
        run: echo "POSTGRES_IMAGE=$(sed -n 's/^FROM //p' deploy/fly/db/Dockerfile)" >> "$GITHUB_ENV"

      - name: túnel para o banco
        env:
          FLY_API_TOKEN: ${{ secrets.FLY_BACKUP_TOKEN }}
        run: |
          flyctl proxy 15432:5432 -a vigilume-db > "$RUNNER_TEMP/proxy.log" 2>&1 &
          for _ in $(seq 1 60); do
            if (echo > /dev/tcp/127.0.0.1/15432) 2>/dev/null; then exit 0; fi
            sleep 1
          done
          cat "$RUNNER_TEMP/proxy.log"
          exit 1

      - name: dump, restauração e checagens
        env:
          BACKUP_DATABASE_PASSWORD: ${{ secrets.BACKUP_DATABASE_PASSWORD }}
          PG_VOLUME_GB: ${{ vars.PG_VOLUME_GB }}
        run: |
          # O Postgres descartável da restauração, só no localhost do runner.
          docker run -d --name restauro -p 127.0.0.1:5433:5432 -e POSTGRES_PASSWORD=restauro "$POSTGRES_IMAGE"
          until docker exec restauro pg_isready -h 127.0.0.1 -U postgres >/dev/null 2>&1; do sleep 1; done
          # --network host: o contêiner enxerga o túnel (15432) e o descartável
          # (5433) no localhost do runner, como o próprio runner.
          # --user: o dump sai com o dono do runner, e o passo seguinte o lê
          # e apaga sem sudo.
          docker run --rm --network host --user "$(id -u):$(id -g)" \
            -v "$PWD/deploy:/deploy:ro" -v "$RUNNER_TEMP:/saida" \
            -e ORIGEM_URL="postgres://vigilume_backup:${BACKUP_DATABASE_PASSWORD}@127.0.0.1:15432/vigilume?sslmode=disable" \
            -e SAIDA=/saida/vigilume.dump \
            -e PGHOST=127.0.0.1 -e PGPORT=5433 -e PGUSER=postgres -e PGPASSWORD=restauro \
            -e VOLUME_BYTES="$(( ${PG_VOLUME_GB:-3} * 1024 * 1024 * 1024 ))" \
            "$POSTGRES_IMAGE" sh /deploy/backup/backup.sh | tee "$RUNNER_TEMP/backup.log"
          grep '^Postgres: ' "$RUNNER_TEMP/backup.log" >> "$GITHUB_STEP_SUMMARY" || true

      - name: cifrar
        env:
          AGE_RECIPIENT: ${{ secrets.AGE_RECIPIENT }}
        run: |
          age -r "$AGE_RECIPIENT" -o "$RUNNER_TEMP/vigilume-$(date -u +%Y%m%d).dump.age" "$RUNNER_TEMP/vigilume.dump"
          # O texto claro não sai do runner: nem artifact, nem log.
          shred -u "$RUNNER_TEMP/vigilume.dump"

      - uses: actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a # v7.0.1
        with:
          name: vigilume-backup-${{ github.run_id }}
          path: ${{ runner.temp }}/vigilume-*.dump.age
          retention-days: 30
          if-no-files-found: error
```

- [ ] **Step 3: Ensaiar o backup localmente**

Um Postgres "de produção" com o papel do compose, migrado pelo binário real, com um usuário e um alvo; um Postgres descartável; e o `backup.sh` na mesma imagem, como o workflow faz. Rode da raiz do repositório (as portas 25432 e 28080 precisam estar livres):

```bash
PG=$(sed -n 's/^FROM //p' deploy/fly/db/Dockerfile)
N=ensaio-bk; docker network create $N
docker run -d --name $N-prod --network $N -e POSTGRES_PASSWORD=postgres -p 127.0.0.1:25432:5432 \
  -v "$PWD/deploy/compose/initdb.sql:/docker-entrypoint-initdb.d/10-app.sql:ro" $PG
docker run -d --name $N-descartavel --network $N -e POSTGRES_PASSWORD=restauro $PG
until docker exec $N-prod pg_isready -h 127.0.0.1 -U vigilume_app -d vigilume; do sleep 0.5; done
until docker exec $N-descartavel pg_isready -h 127.0.0.1 -U postgres; do sleep 0.5; done
docker exec $N-prod psql -U postgres -c "CREATE ROLE vigilume_backup LOGIN PASSWORD 'bk' NOSUPERUSER BYPASSRLS NOCREATEDB NOCREATEROLE; GRANT pg_read_all_data TO vigilume_backup;"
go build -o /tmp/vigilume-ensaio ./cmd/vigilume
K=$(printf 'chave-de-ensaio-com-32-bytes!!!!' | base64)
DATABASE_URL='postgres://vigilume_app:vigilume_app@127.0.0.1:25432/vigilume?sslmode=disable' \
  PUBLIC_BASE_URL=http://localhost CORS_ORIGIN=http://localhost MASTER_KEY=$K JWT_SECRET=$K IP_HASH_KEY=$K \
  HTTP_ADDR=127.0.0.1:28080 METRICS_ADDR=off /tmp/vigilume-ensaio --role=all > /tmp/vigilume-ensaio.log 2>&1 &
PID=$!; until curl -sf http://127.0.0.1:28080/healthz; do sleep 0.3; done; kill $PID
docker exec $N-prod psql -U vigilume_app -d vigilume -v ON_ERROR_STOP=1 -c "
INSERT INTO users (id,email,password_hash) VALUES ('0192f000-0000-7000-8000-000000000001','dono@exemplo.com','x');
BEGIN; SELECT set_config('app.user_id','0192f000-0000-7000-8000-000000000001',true);
INSERT INTO targets (id,user_id,name,url,method,interval_sec,timeout_ms,expected_status,enabled,next_run_at,created_at)
VALUES ('0192f000-0000-7000-8000-000000000002','0192f000-0000-7000-8000-000000000001','Exemplo','https://example.com','GET',60,5000,200,true,now(),now()); COMMIT;"
docker run --rm --network $N --user "$(id -u):$(id -g)" -v "$PWD/deploy:/deploy:ro" -v /tmp:/saida \
  -e ORIGEM_URL="postgres://vigilume_backup:bk@$N-prod:5432/vigilume?sslmode=disable" -e SAIDA=/saida/ensaio.dump \
  -e PGHOST=$N-descartavel -e PGUSER=postgres -e PGPASSWORD=restauro -e VOLUME_BYTES=3221225472 \
  $PG sh /deploy/backup/backup.sh; echo "código $?"
```

Expected: `schema 7, 1 usuário(s), 1 alvo(s)`, o `ls` do dump (~57 KB), `NOTICE: cópia conferida: schema 7, 1 usuário(s), 1 alvo(s), 10 tabela(s) com RLS`, `Postgres: 1% do volume …`, `backup conferido`, `código 0`.

Para a volta a limpo depois do Step 4: `docker rm -f $N-prod $N-descartavel && docker network rm $N && rm -f /tmp/ensaio.dump /tmp/vigilume-ensaio*`.

- [ ] **Step 4: Sabotagem**

Com a pilha do Step 3 de pé; cada uma repete só o último `docker run` do Step 3 (o do `backup.sh`), com um descartável novo antes (o `initdb.sql` cria o papel e o banco, e a segunda vez daria "já existe"):

```bash
docker rm -f $N-descartavel && docker run -d --name $N-descartavel --network $N -e POSTGRES_PASSWORD=restauro $PG
until docker exec $N-descartavel pg_isready -h 127.0.0.1 -U postgres; do sleep 0.5; done
```

1. `ORIGEM_URL` com `vigilume_app:vigilume_app` no lugar de `vigilume_backup:bk`. Expected: `pg_dump: error: query failed: ERROR: query would be affected by row-level security policy for table "channel_health"`, código 1. É a razão de existir o `vigilume_backup`.
2. Numa cópia de `backup.sh` (monte a cópia no lugar da original), `pg_restore` com `--no-owner`. Expected: `ERROR: tabelas sem o dono vigilume_app: audit_log, channel_health, …`, código 3.
3. Na cópia, `SELECT max(version_id) + 1 FROM goose_db_version` na linha da `versao`. Expected: `ERROR: versão do schema restaurado = 7, origem = 8`, código 3.
4. `VOLUME_BYTES=10000000`. Expected: `ERRO: acima de 85%: aumente o volume (fly volumes extend)`, código 3, **depois** de `cópia conferida` (o disco é conferido por último).
5. **O `shell: bash` do workflow.** O passo principal do `backup.yml` termina em `… sh /deploy/backup/backup.sh | tee "$RUNNER_TEMP/backup.log"`. O GitHub roda um `run` sem `shell:` como `bash -e {0}`, e com `shell: bash` como `bash --noprofile --norc -eo pipefail {0}`. A diferença, em uma linha:
   ```bash
   bash -e -c 'false | tee /dev/null'; echo "sem pipefail: $?"
   bash --noprofile --norc -eo pipefail -c 'false | tee /dev/null'; echo "com pipefail: $?"
   ```
   Expected: `sem pipefail: 0` e `com pipefail: 1`. Na validação, o **texto exato** do passo, extraído do YAML, rodou contra um Postgres de produção local (porta 15432) com `VOLUME_BYTES` pequeno: sob `bash -e`, código **0** com `ERRO: acima de 85%` no log (o job ficaria verde); sob `bash -eo pipefail`, código **3**. Tire as três linhas `defaults:` / `run:` / `shell: bash` do `backup.yml` e o `actionlint` continua limpo: a trava é só o `shell`.

Run: `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 .github/workflows/*.yml`
Expected: sem saída.

- [ ] **Step 5: O runbook**

Crie `docs/deploy/fly.md`:

````markdown
# Deploy e operação no Fly.io

Runbook **manual**. Nada aqui roda sozinho: cada passo mexe na conta do Fly ou do GitHub do dono
do projeto e é executado por ele. O porquê de cada escolha está na spec do Plano 7
(`docs/superpowers/specs/2026-09-24-plano-7-empacotamento-e-deploy-design.md`); as decisões são
citadas como D*n*.

## Topologia

| App | O que roda | Máquina | Rede |
|---|---|---|---|
| `vigilume` | O binário com `--role=all`: API, painel, sondador, dispatcher e manutenção | `shared-cpu-1x`, 512 MB | HTTP público pelo proxy do Fly |
| `vigilume-db` | Imagem oficial `postgres:18-alpine` (digest em `deploy/fly/db/Dockerfile`), volume `pgdata` | `shared-cpu-1x`, 512 MB, volume de 3 GB | Só a rede privada: `vigilume-db.internal:5432` |

O Postgres **não tem gerência do Fly**. Backup, disco e upgrade são do operador (seção
"Operação do Postgres"). Custo aproximado: US$ 9–11/mês (duas máquinas de 512 MB, 3 GB de volume,
snapshots dentro da faixa grátis de 10 GB). Conferir a página de preços antes de criar.

## O que você precisa antes

- `flyctl` instalado e autenticado (`fly auth login`).
- A imagem publicada no GHCR pelo job `publish` do CI (pacote **privado**, como o repositório).
  Anote o digest (`sha256:…`) do resumo do job.
- `docker` com `buildx`, logado no GHCR com um token `read:packages`
  (`docker login ghcr.io -u <usuário>`).
- `age` (<https://age-encryption.org>) para a chave do backup.
- Um gerenciador de senhas. Quatro coisas vão para ele e **não** podem ficar só no Fly: a senha
  do superusuário do Postgres, as senhas de `vigilume_app` e `vigilume_backup`, a `MASTER_KEY` e
  a chave privada do `age`.

## Primeiro deploy

Troque `gru` pela região escolhida em `fly platform regions`, nos dois `fly.toml` também.

1. **Apps.** Sem `fly launch` (ele gera `auto_stop_machines`, e um monitor desligado por falta
   de tráfego para de sondar em silêncio, D23):
   ```sh
   fly apps create vigilume
   fly apps create vigilume-db
   ```
2. **Volume do banco**, com snapshots diários guardados por 14 dias:
   ```sh
   fly volumes create pgdata -a vigilume-db --region gru --size 3 --snapshot-retention 14
   ```
3. **Banco no ar:**
   ```sh
   fly secrets set -a vigilume-db --stage POSTGRES_PASSWORD="$(openssl rand -base64 32)"
   fly deploy -c deploy/fly/db/fly.toml --ha=false --image "$(sed -n 's/^FROM //p' deploy/fly/db/Dockerfile)"
   fly machines list -a vigilume-db   # exatamente UMA máquina, com o volume pgdata
   fly logs -a vigilume-db    # esperado: "database system is ready to accept connections"
   ```
   A imagem vai por `--image`, direto do Docker Hub e pelo digest de `deploy/fly/db/Dockerfile`
   (que o Dependabot atualiza), sem build remoto. `--ha=false`: sem ele, o primeiro deploy cria
   duas máquinas, e uma segunda máquina de Postgres não teria volume. (Não ensaiado: a
   validação não tem conta no Fly. As flags foram conferidas no `flyctl` 0.4.108.)
   Guarde a `POSTGRES_PASSWORD` no gerenciador (`fly secrets` não mostra o valor depois).
4. **Papéis e banco.** A imagem oficial tem shell:
   ```sh
   fly ssh console --pty -a vigilume-db -C "psql -U postgres"
   ```
   Rode o mesmo SQL de `deploy/compose/initdb.sql`, com **senhas novas**, e o papel de backup:
   ```sql
   CREATE ROLE vigilume_app LOGIN PASSWORD '<senha nova>'
       NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
   CREATE DATABASE vigilume OWNER vigilume_app;
   CREATE ROLE vigilume_backup LOGIN PASSWORD '<outra senha nova>'
       NOSUPERUSER BYPASSRLS NOCREATEDB NOCREATEROLE;
   GRANT pg_read_all_data TO vigilume_backup;
   ```
   O `vigilume_backup` tem `BYPASSRLS` porque o `pg_dump` do dono falha com
   `FORCE ROW LEVEL SECURITY`. É exatamente o tipo de papel que o boot **recusa**: o
   `DATABASE_URL` do app nunca aponta para ele.
5. **Pré-checagem** (o que o boot vai conferir), no mesmo `psql`:
   ```sql
   SELECT rolname, rolsuper, rolbypassrls FROM pg_roles
    WHERE rolname IN ('vigilume_app', 'vigilume_backup');
   -- vigilume_app    | f | f
   -- vigilume_backup | f | t
   \c vigilume
   SELECT has_schema_privilege('vigilume_app', 'public', 'CREATE');   -- t
   ```
6. **Segredos do app**, sem reiniciar nada:
   ```sh
   fly secrets set -a vigilume --stage \
     DATABASE_URL='postgres://vigilume_app:<senha>@vigilume-db.internal:5432/vigilume?sslmode=disable' \
     MASTER_KEY="$(openssl rand -base64 32)" \
     JWT_SECRET="$(openssl rand -base64 32)" \
     IP_HASH_KEY="$(openssl rand -base64 32)"
   ```
   Gere a `MASTER_KEY` antes, guarde-a no gerenciador e só então a passe ao Fly. **Perder a
   `MASTER_KEY` torna ilegíveis os headers de alvo e as configurações de canal, e nenhum backup
   devolve isso**: o dump guarda o texto cifrado, não a chave.

   `sslmode=disable` é deliberado (D31): a rede privada do Fly é uma malha WireGuard, e o banco
   não tem porta pública.

   E-mail é opcional: `RESEND_API_KEY` e `EMAIL_FROM` **juntas**, com o domínio do remetente já
   verificado no Resend (senão todo envio volta 403). Sem elas, o Discord funciona e o cadastro
   de canal de e-mail responde que ele está indisponível.
7. **Primeiro deploy do app.** O Fly puxa de `registry.fly.io` (D34): o pacote do GHCR é privado,
   e dar ao Fly uma credencial do GitHub seria guardar um token pessoal fora do GitHub. Copie a
   imagem **testada**, pelo digest, sem rebuild:
   ```sh
   fly auth docker
   docker buildx imagetools create \
     -t registry.fly.io/vigilume:<sha do commit> \
     ghcr.io/bipoxdxd/vigilume@sha256:<digest do publish>
   fly deploy --image registry.fly.io/vigilume@sha256:<o mesmo digest> --ha=false
   fly machines list -a vigilume   # exatamente UMA máquina
   fly logs -a vigilume
   ```
   `--ha=false` importa: sem ele, o primeiro deploy cria **duas** máquinas, e dois processos
   `--role=all` mandariam cada notificação em dobro. O workflow `deploy` confere a mesma coisa.
   Esperado: `iniciando` com a versão, `migrations aplicadas`, `sondador ativo`, `servindo`. O
   aviso "TRUSTED_PROXIES vazio com PUBLIC_BASE_URL https" é esperado nesta etapa.
8. **Conferência do dono das tabelas**, no console do banco:
   ```sql
   \c vigilume
   SELECT tableowner, count(*) FROM pg_tables WHERE schemaname = 'public' GROUP BY 1;
   -- só vigilume_app
   ```
9. **IP do cliente.** O proxy do Fly põe o IP do **app** no fim do `X-Forwarded-For`; o do
   cliente vem em `Fly-Client-IP`, que o app lê (`CLIENT_IP_HEADER`) **só** de conexões vindas de
   `TRUSTED_PROXIES` (D22). A documentação do Fly não diz de qual endereço o proxy conecta, e
   por isso `TRUSTED_PROXIES` começa vazio. Abra o app no navegador e procure no log:
   ```sh
   fly logs -a vigilume | grep "cabeçalho de IP do cliente ignorado"
   ```
   O aviso (no máximo um por minuto) traz o `remote_addr` do proxy. Ponha o menor CIDR que o
   cobre em `TRUSTED_PROXIES` no `fly.toml` (candidatos esperados: `172.16.0.0/12` ou
   `fdaa::/16`) e faça o deploy de novo pelo mesmo digest (o comando do passo 7). **Leve o valor,
   e a região, para o `fly.toml` do repositório por PR** antes de usar o workflow `deploy`: ele faz
   o deploy com o `fly.toml` da `main`, e **recusa** um `TRUSTED_PROXIES` vazio. Então o **teste
   de dois pontos de rede**, com um e-mail qualquer e senha errada:
   ```sh
   for i in 1 2 3 4 5 6; do
     curl -s -o /dev/null -w '%{http_code}\n' -X POST https://vigilume.fly.dev/api/v1/auth/login \
       -H 'Content-Type: application/json' -H 'X-Requested-With: vigilume' \
       -d '{"email":"ninguem@exemplo.com","password":"errada-errada-1234"}'
   done
   ```
   - Da rede A (casa): cinco `401` e um `429`.
   - Logo depois, da rede B (4G do celular): `401`, não `429`. Os baldes estão separados.
   - De novo da rede A, com `-H 'Fly-Client-IP: 203.0.113.77' -H 'X-Forwarded-For: 203.0.113.77'`:
     continua `429`. O cabeçalho forjado não vale.
   - O aviso some do log.

   Se a rede B levar `429`, o `TRUSTED_PROXIES` não cobre o proxy: volte ao aviso.
10. **Seu usuário**, com o binário **da versão que está no ar** (o `user create` aplica as
    migrations antes: uma versão mais nova migraria o banco de produção por fora do deploy).
    `fly releases -a vigilume --image` mostra o digest em produção; `vigilume version` numa imagem
    diz o commit. Num terminal, o túnel; no outro, o subcomando (a senha é lida do terminal, sem
    eco):
    ```sh
    fly proxy 15432:5432 -a vigilume-db
    ```
    - Pela imagem em produção, **com** `-t` (sem ele o terminal mostraria a senha):
      ```sh
      fly auth docker
      docker run --rm -it --network host \
        -e DATABASE_URL='postgres://vigilume_app:<senha>@127.0.0.1:15432/vigilume?sslmode=disable' \
        registry.fly.io/vigilume@sha256:<digest em produção> user create --email <seu e-mail>
      ```
      `--network host` é do Linux. No Docker Desktop (macOS, Windows) o contêiner não vê o
      `127.0.0.1` da máquina: troque o host do `DATABASE_URL` por `host.docker.internal` e tire o
      `--network host`.
    - Ou compilando o mesmo commit: `git checkout <sha em produção> && make build`, e
      `DATABASE_URL='…@127.0.0.1:15432/…' ./bin/vigilume user create --email <seu e-mail>`
      (o painel não precisa estar construído: o `user create` não o usa).
11. **Conferência pública.** `https://vigilume.fly.dev/healthz` e `/readyz` dão 200; login no
    painel; `curl -sI https://vigilume.fly.dev/` mostra `Strict-Transport-Security`, a CSP com
    nonce e `X-Content-Type-Options`; crie a status page e abra `/p/<slug>` numa aba anônima.

    O painel exige **contexto seguro**: HTTPS, ou `localhost`. Em HTTP por outro endereço ele
    mostra um aviso e desliga a coordenação entre abas (Web Locks), e as abas passam a disputar
    o refresh. No Fly isso já está resolvido (`force_https = true`); não sirva o painel por um
    domínio próprio sem TLS nem pelo IP da máquina.
12. **Primeira manutenção.** Num banco novo não há o que consolidar. A rodada pesada (62 dias de
    checks de uma vez) só acontece num banco importado.
13. **CI de deploy e de backup** (GitHub → Settings → Secrets and variables → Actions). No
    GitHub Free com repositório privado **não há environments** (nem secrets por environment,
    nem revisor obrigatório): tudo é secret **de repositório**. A trava real é quem pode
    disparar um workflow à mão: só quem tem acesso de escrita, hoje só você. Secrets de
    repositório não chegam a PR de fork.
    - Secrets: `FLY_DEPLOY_TOKEN` = `fly tokens create deploy -a vigilume`;
      `FLY_BACKUP_TOKEN` = `fly tokens create deploy -a vigilume-db`; `BACKUP_DATABASE_PASSWORD`
      = senha do `vigilume_backup`; `AGE_RECIPIENT` = a chave **pública** de
      `age-keygen -o vigilume-backup.key` (a privada vai para o gerenciador e para mais nenhum
      lugar). Um token por app: um token vazado só alcança aquele app.
    - Variáveis: `PG_VOLUME_GB` = `3`; `BACKUP_ENABLED` = `1` (sem ela, o `backup` agendado
      termina em "skipped" todo dia, sem gastar minutos).

    Rode o workflow `backup` à mão (Actions → backup → Run workflow) e confira que ficou verde.
    Se o `flyctl proxy` falhar por permissão, o token de app não basta para o túnel: troque o
    `FLY_BACKUP_TOKEN` por um token de organização, sabendo que ele alcança todos os seus apps.
14. **Voltar uma versão.** `fly releases -a vigilume` e
    `fly deploy --image registry.fly.io/vigilume@sha256:<digest anterior> --ha=false`. Só é seguro se as
    migrations da versão nova forem aditivas: o `goose` não roda `Down` sozinho.

## Deploys seguintes

Actions → ci → o `publish` da `main` gera o digest; Actions → deploy → Run workflow **na `main`**
com esse digest (o job recusa outro branch e um `TRUSTED_PROXIES` vazio). O job copia a imagem do
GHCR para o `registry.fly.io` pelo digest e faz o deploy com `--strategy rolling` e `--ha=false`
(alguns segundos fora com uma máquina só, D26), e confere que ficou uma máquina.

A imagem publicada é construída de novo no `publish`, do **mesmo código e com as mesmas bases
fixadas por digest** da imagem que o E2E testou, e é **reescaneada** pelo Trivy antes de ganhar
tag: não é, byte a byte, a do E2E (o binário é reprodutível; a imagem não, D9 da spec).

### Minutos de Actions

Com o repositório privado, o CI gasta a cota gratuita (2.000 min/mês na conta pessoal): ~15 min de
runner por push somando os jobs, dos quais o `e2e` é o maior, e ~2 min por dia do `backup`
(~60 min/mês). Um push novo no mesmo branch cancela a execução anterior (`concurrency` do `ci.yml`),
então só a última conta inteira. Se a cota apertar, o primeiro corte é o `e2e` só na `main`
(`if: github.event_name == 'push'` no job), trocando a garantia do PR por um aviso depois do merge
(D19 da spec).

## Depuração

A imagem do app não tem shell (spec §5.9): `fly ssh console -a vigilume` não serve. O que serve:
`fly logs`, os subcomandos do binário (`vigilume version`, `vigilume healthcheck`,
`vigilume user …`) e o console do **banco**, que tem shell.

## Operação do Postgres

| Responsabilidade | Mecanismo | Frequência |
|---|---|---|
| Backup | Snapshots diários do volume (14 dias) + workflow `backup` diário: dump, restauração conferida, cifra `age`, artifact de 30 dias | Automático: conferir que o `backup` segue verde |
| Teste de restauração | Automático em cada `backup`; manual a cada trimestre (abaixo) | Diário e trimestral |
| Disco | O `backup` compara o tamanho dos bancos com `PG_VOLUME_GB`: aviso acima de 70%, **falha** acima de 85% (o GitHub manda e-mail). Depois de `fly volumes extend <id> -s <GB>`, **atualize a variável `PG_VOLUME_GB`**, senão o aviso continua contando o tamanho velho | Quando avisar |
| Upgrade menor (18.x) | PR do Dependabot em `deploy/fly/db/Dockerfile` (e o do `compose.yaml`, juntos) → merge → `fly deploy -c deploy/fly/db/fly.toml --ha=false --image "$(sed -n 's/^FROM //p' deploy/fly/db/Dockerfile)"` (10–30 s fora). O `backup` passa a usar a imagem nova sozinho | Quando o Dependabot abrir |
| Upgrade maior (18 → 19) | **Não** vem por PR: o `dependabot.yml` ignora versão maior de `postgres` (e de `node`). Dump e restauração num app novo (abaixo, D36) | ~1 vez por ano |

### Restauração manual (trimestral)

1. Baixe o artifact do último `backup` (Actions → backup → execução → Artifacts).
2. `age -d -i vigilume-backup.key -o vigilume.dump vigilume-AAAAMMDD.dump.age`
3. Num Postgres descartável, o mesmo caminho do workflow:
   ```sh
   docker run -d --name restauro -p 127.0.0.1:25433:5432 -e POSTGRES_PASSWORD=restauro \
     "$(sed -n 's/^FROM //p' deploy/fly/db/Dockerfile)"
   until docker exec restauro pg_isready -h 127.0.0.1 -U postgres; do sleep 1; done
   docker exec -i restauro psql -U postgres < deploy/compose/initdb.sql
   docker exec -i restauro pg_restore -U postgres --exit-on-error -d vigilume < vigilume.dump
   ```
4. Suba o `vigilume` contra ele **com `--role=api`** (`DATABASE_URL=postgres://vigilume_app:vigilume_app@127.0.0.1:25433/vigilume?sslmode=disable`,
   **a mesma `MASTER_KEY` de produção**, `PUBLIC_BASE_URL=http://localhost:8080`) e faça login.
   Só a API: com `--role=all`, a cópia sondaria os alvos de produção e mandaria alertas aos
   canais **reais** (Discord, e-mail), como se fosse a produção. Abra um alvo com header: se a
   `MASTER_KEY` estiver errada, é aqui que aparece.
5. `docker rm -f restauro` e apague o `vigilume.dump`.

### Queda do host do banco

Máquina única, sem réplica. A máquina velha está presa ao volume velho: destrua-a primeiro, ou o
deploy tentaria reusá-la.
```sh
fly machines list -a vigilume-db
fly machine destroy <id da máquina> -a vigilume-db --force
fly volumes list -a vigilume-db
fly volumes snapshots list <id do volume>
fly volumes create pgdata -a vigilume-db --snapshot-id <snapshot> -s 3 --region gru
fly deploy -c deploy/fly/db/fly.toml --ha=false --image "$(sed -n 's/^FROM //p' deploy/fly/db/Dockerfile)"
```
O Postgres faz a recuperação de crash sozinho. Se o snapshot não servir, restaure o último dump
(seção anterior) num banco novo, com os papéis do passo 4.

### Upgrade maior

1. `fly apps create vigilume-db19`, volume novo, `deploy/fly/db/Dockerfile` com a imagem nova.
2. Papéis do passo 4 no banco novo.
3. Janela curta: pare o app (`fly scale count 0 -a vigilume`), faça o dump do antigo como
   `vigilume_backup` (`fly proxy` + `pg_dump -Fc`) e restaure no novo como superusuário,
   preservando os donos.
4. `fly secrets set -a vigilume DATABASE_URL=…vigilume-db19.internal…`, `fly scale count 1`.
5. Mantenha o `vigilume-db` antigo parado por 7 dias antes de destruí-lo.

## Tornar o repositório público (passo futuro)

O repositório é **privado por decisão do dono**. No dia de abrir:

1. Varredura do histórico inteiro: `gitleaks git . --redact -v` (o job `secrets` do CI já faz
   isso em todo PR). O falso positivo conhecido está no `.gitleaksignore`, com o motivo.
2. **Se aparecer um segredo real:** considere-o vazado e **rotacione primeiro** (Resend, Discord,
   Fly, o que for). Depois, tire-o do histórico com `git filter-repo --replace-text` num clone
   novo, `git push --force` de todos os branches e tags, e rode a varredura de novo até zerar.
   Reescrever a história não desfaz um vazamento.
3. O e-mail pessoal do autor está no histórico e **fica** (decisão do dono).
4. Settings → General → Change visibility. O pacote do GHCR tem visibilidade própria: Package
   settings → Change visibility → Public. Confira de uma máquina sem login:
   `docker pull ghcr.io/bipoxdxd/vigilume:latest`.
5. No README, troque o parágrafo "enquanto o repositório for privado" pelo dos 30 segundos: o
   `docker compose up` passa a puxar a imagem em vez de construir, sem mudar o compose.
6. **Deploy com revisor:** num repositório público, o GitHub Free tem environments com revisor
   obrigatório. Crie o `production`, mova o `FLY_DEPLOY_TOKEN` para ele e ponha
   `environment: production` no job do `deploy.yml`. (O GitHub Pro, ~US$ 4/mês, dá secrets por
   environment num repositório privado, mas **não** o revisor obrigatório; nada aqui depende dele.)
7. **Agendamentos:** num repositório público, o GitHub **desliga** os workflows agendados depois
   de 60 dias sem atividade no repositório, e avisa por e-mail. O `backup` para junto: reative-o
   em Actions quando o aviso chegar.
````

- [ ] **Step 6: Commit**

```bash
git add fly.toml deploy/fly/ deploy/backup/ .github/workflows/backup.yml docs/deploy/fly.md
git commit -m "deploy: apps do Fly, Postgres sem gerência, backup diário restaurado e runbook"
```

---

### Task 9: README, `.gitignore` e pendências

O README é para um avaliador de vaga backend/fullstack Go com dois minutos (spec §1.1, e a estrutura na seção 8 da spec do Plano 7): funciona, roda aqui, o que tem de difícil, está testado. Em português (decisão do usuário), sem texto de marketing, cada afirmação de segurança apontando para o código e para o teste que a prova. Enquanto o repositório for privado, o README diz o número honesto do compose (constrói do código) em vez de prometer os 30 s (D17). O `.gitignore` ganha `.idea/` antes de qualquer publicação (spec 6.7, item 4). As pendências de deploy herdadas fecham num arquivo novo, que passa a ser o estado corrente.

**Files:**
- Create: `README.md`, `docs/superpowers/PENDENCIAS-PARA-O-PLANO-8.md`
- Modify: `.gitignore`, `docs/superpowers/plans/2026-09-22-roadmap.md`

**Interfaces:**
- Consumes: tudo o que as Tasks 1 a 8 criaram (os links do README apontam para arquivos que existem).
- Produces: nada que código use.

**Contexto para quem implementa.** Os números do README são os da validação: o compose com build frio ficou em ~40 s (31 s de build num builder novo, mais o pull do Postgres e o `up`), e ~5 s com as imagens na máquina. Se a sua máquina der outro número, troque pelo seu: o README não pode prometer o que não se mediu. O README não fala em "aprovação" de deploy: no GitHub Free com repositório privado não há revisor obrigatório, e a trava é quem tem acesso de escrita (Task 7). A imagem de produção é descrita como é: mesmo código e bases fixadas, reescaneada (Task 7). O README pede Docker 25+ com Compose v2.20+ (o `start_interval` do healthcheck) e ensina o `docker compose up --build` para quem mexe no código. A licença **não** foi escolhida: é decisão do dono, junto com a publicação (o README diz isso). O painel exige contexto seguro (HTTPS ou `localhost`), e o README manda abrir por `localhost`.

- [ ] **Step 1: O README**

Crie `README.md`:

````markdown
# Vigilume

Monitor de disponibilidade de serviços HTTP: sonda cada alvo no intervalo que você define, abre e
fecha incidentes sozinho, avisa pelo Discord e por e-mail, e publica uma página de status. Um
binário Go com o painel React embutido e um Postgres.

[![ci](https://github.com/BipoXDXD/vigilume/actions/workflows/ci.yml/badge.svg)](https://github.com/BipoXDXD/vigilume/actions/workflows/ci.yml)
![Go 1.26](https://img.shields.io/badge/Go-1.26-00ADD8)

## Rodar local

Precisa de Docker 25+ com Compose v2.20+ (o healthcheck usa `start_interval`).

```sh
git clone git@github.com:BipoXDXD/vigilume.git && cd vigilume
docker compose up --wait
```

Abra <http://localhost:8080> e entre com `demo@vigilume.local` / `demo-vigilume-2026`. O painel já
tem um alvo de exemplo, `https://example.com/` (domínio reservado para isso). Para cadastrar mais
no primeiro `up`: `DEMO_TARGETS_EXTRA="https://seu.site/ https://outro.site/" docker compose up --wait`.

- **Enquanto o repositório for privado**, a imagem publicada também é privada, e o compose
  **constrói do código**: cerca de 40 s numa máquina de 16 núcleos com a rede boa, mais num
  notebook comum. Quem tem acesso ao repositório pode puxar a imagem pronta
  (`docker login ghcr.io` com um token `read:packages`, depois `docker compose pull`), e o `up`
  cai para ~5 s.
- Use **`localhost`**, não o IP da máquina: o painel exige contexto seguro (HTTPS ou
  `localhost`). Em HTTP por outro endereço ele mostra um aviso e desliga a coordenação entre
  abas. A porta só escuta no `127.0.0.1`.
- Os alvos de exemplo precisam de internet. Um serviço rodando na sua máquina ou no próprio
  compose **não** pode ser alvo: o sondador recusa endereço privado, de propósito (SSRF, abaixo).
- As chaves do `compose.yaml` são de demonstração e estão no repositório; o boot as recusa
  quando `PUBLIC_BASE_URL` é `https://`.

`docker compose down` para; `docker compose down -v` apaga o banco também.

## O que faz

- **Sonda** alvos HTTP (GET, HEAD ou POST) de 30 s a 24 h, com headers cifrados, status
  esperado e limite de latência para "degradado".
- **Incidentes** abrem depois de falhas seguidas e fecham depois de sucessos seguidos, com
  histórico por alvo.
- **Notificações** pelo Discord e por e-mail (Resend), com entrega confiável: saem de uma fila
  no próprio Postgres e sobrevivem a um restart.
- **Página de status pública** em `/p/{slug}`, com os alvos que você escolher, uptime de 90 dias
  e os incidentes recentes.
- **Painel** com dashboard, gráfico de latência, 2FA (TOTP), sessões e troca de senha.

## Arquitetura

```mermaid
flowchart LR
  subgraph bin["binário vigilume (--role=all | api | prober)"]
    api["API HTTP + painel embutido<br/>+ status page"]
    sond["sondador<br/>(semáforo de sondas)"]
    det["detector de incidentes"]
    disp["dispatcher<br/>(outbox)"]
    man["manutenção<br/>(partições, rollups)"]
  end
  pg[("Postgres<br/>RLS com FORCE")]
  api --> pg
  sond -- "SELECT … FOR UPDATE SKIP LOCKED" --> pg
  sond --> det --> pg
  disp -- "fila de notificações" --> pg
  disp --> ext["Discord / Resend"]
  man --> pg
```

Um processo só, que também roda partido (`--role=api` e `--role=prober`) sem mudar código: o
Postgres é a fila (`SKIP LOCKED`), então dois sondadores nunca pegam o mesmo alvo. Sem Redis nem
Kafka, porque o volume de um monitor destes cabe com folga no banco que já existe
([spec §17](docs/superpowers/specs/2026-09-22-vigilume-design.md)).

## Decisões de segurança

| Decisão | Código | Teste que prova |
|---|---|---|
| SSRF: o IP é validado **na hora de conectar** (`ControlContext`), não só no cadastro, o que fecha o DNS rebinding; redirect revalidado a cada salto | [`safedial`](internal/prober/safedial/safedial.go), [`transport.go`](internal/prober/safedial/transport.go) | [`transport_test.go`](internal/prober/safedial/transport_test.go) (`TestRedirectToAnotherInternalAddressIsBlockedAtTheHop`) |
| Isolamento por usuário com **RLS e `FORCE`**; o boot recusa um papel com superusuário ou `BYPASSRLS` | [`00004_targets_checks.sql`](internal/storage/migrations/00004_targets_checks.sql), [`role.go`](internal/storage/role.go) | [`rls_test.go`](internal/storage/rls_test.go) (`TestRLSIsolatesTargetsEvenWithTheWrongFilter`), [`role_test.go`](internal/storage/role_test.go) |
| Access token só em memória; refresh em cookie `HttpOnly`, `SameSite=Strict`, com rotação e detecção de reúso que derruba a família | [`authhandlers.go`](internal/httpapi/authhandlers.go), [`sessao.ts`](web/src/lib/api/sessao.ts) | [`auth_integration_test.go`](internal/httpapi/auth_integration_test.go) (`TestReusedRefreshCookieRevokesTheWholeFamily`) |
| Segredos com AES-256-GCM e o id da linha como AAD (um texto cifrado não muda de linha); tipo `Secret` que não sai em log nem em JSON | [`box.go`](internal/crypto/box.go), [`secret.go`](internal/crypto/secret.go) | [`box_test.go`](internal/crypto/box_test.go) (`TestOpenRejectsCiphertextMovedToAnotherRow`), [`secret_test.go`](internal/crypto/secret_test.go) |
| Status page com DTO próprio: nada interno chega à página pública | [`public.go`](internal/statuspage/public.go) | [`public_test.go`](internal/statuspage/public_test.go) (`TestPublicViewHasNoInternalFields`) |
| IP real do cliente atrás do proxy, com falha **fechada**: cabeçalho só vale vindo de `TRUSTED_PROXIES` | [`clientip.go`](internal/httpapi/middleware/clientip.go) | [`clientip_test.go`](internal/httpapi/middleware/clientip_test.go), `TestSessionsBehindFlyRecordDistinctOrigins` |
| CSP com nonce por resposta, no painel e na status page | [`webui.go`](internal/webui/webui.go), [`publicpage.go`](internal/httpapi/publicpage.go) | E2E: [`fluxo.spec.ts`](web/e2e/fluxo.spec.ts) (zero violações, com controle positivo) |
| Imagem distroless **nonroot, sem shell**, Trivy sem achado HIGH/CRITICAL corrigível, varredura de segredos no histórico inteiro em todo PR | [`Dockerfile`](Dockerfile), [`ci.yml`](.github/workflows/ci.yml) | [`checar-imagem.sh`](deploy/ci/checar-imagem.sh) |

## Concorrência

- O sondador reivindica só o que cabe no [semáforo](internal/prober/scheduler.go) e devolve a
  vaga até em panic (`TestProbePanicIsLoggedAndReturnsTheSlot`).
- Cada subsistema roda num [`errgroup`](cmd/vigilume/main.go): se um cai, os outros encerram, e o
  processo sai com erro em vez de ficar meio vivo. Shutdown gracioso com prazo de 15 s, e sondas
  interrompidas não viram falso "fora do ar".
- No painel, o refresh do token é *single-flight* e coordenado entre abas por Web Locks
  ([`sessao.ts`](web/src/lib/api/sessao.ts)); duas abas recarregando juntas não derrubam a sessão
  ([`abas.spec.ts`](web/e2e/abas.spec.ts)).

## Testes

```sh
make test          # Go com -race; precisa de Docker (Postgres real via testcontainers)
make test-short    # sem Docker
cd web && npm test # Vitest do painel
make e2e           # o compose do avaliador + Playwright contra ele
```

- **Sem mock de banco**: RLS, `SKIP LOCKED`, partições e advisory locks só se provam num
  Postgres de verdade. O CI roda a suíte no **17 e no 18**.
- O **E2E** (Playwright, Chromium) roda no CI contra o **mesmo `compose.yaml`** deste README, com a
  imagem que acabou de passar no Trivy: o "sobe com um comando" é teste de regressão.
- O **backup** de produção é restaurado num banco descartável e conferido **todo dia**.
- **Prova por sabotagem.** Todo teste que afirma uma ausência ("o segredo não vaza", "o cliente
  não alcança") é conferido quebrando a propriedade de propósito e vendo ficar vermelho. Um caso
  real: os testes de middleware montavam a cadeia à mão, e apagar `SecurityHeaders`, `CORS` e
  `BodyLimit` de `routes.go` deixava a suíte verde; daí nasceu
  `TestTheMiddlewareChainIsActuallyWired`.

## Stack

| Camada | Escolha |
|---|---|
| Backend | Go 1.26, `net/http`, pgx, sqlc, goose, `log/slog`, Prometheus |
| Banco | PostgreSQL 18 (compatível com o 17) |
| Painel | React 19, TypeScript, Vite, TanStack Query, React Router, React Hook Form + Zod, Radix, Tailwind, Recharts |
| Testes | `testing` + testcontainers, Vitest + Testing Library + MSW, Playwright + axe |
| Entrega | Docker (distroless), GitHub Actions, GHCR, Fly.io |

## Desenvolvimento

```sh
make build            # bin/vigilume, com a versão do git describe
make generate         # sqlc generate, depois de mudar internal/storage/queries
make lint sec         # vet, staticcheck, gosec, govulncheck
make image            # a imagem de produção
make up               # docker compose up --wait --build: o compose com o código que você tem
cd web && npm run dev # o painel em http://localhost:5173, com proxy de /api para :8080
```

Depois de mudar o código, `docker compose up --build` (ou `make up`): sem o `--build`, o compose
reusa a imagem que já tem. Para o `npm run dev`, suba a API assim (o `CORS_ORIGIN` do compose já é
o do Vite). Criar ou recuperar o usuário dono: `vigilume user create --email <e>` e
`vigilume user set-password --email <e>`, com a senha pelo stdin.

## Deploy e operação

Dois apps no Fly.io: o binário (`--role=all`) e um Postgres 18 com a imagem oficial, sem gerência
do Fly, só na rede privada. A imagem de produção sai do mesmo código e das mesmas bases fixadas por
digest que o E2E testou, é reescaneada pelo Trivy antes de ganhar tag, e vai para o Fly pelo
digest. O backup lógico sai do Fly, é cifrado com `age` e é restaurado e conferido todo dia. O
deploy é sempre manual, disparado por quem tem acesso de escrita ao repositório. Runbook:
[`docs/deploy/fly.md`](docs/deploy/fly.md).

## O que ficou de fora, e por quê

- **Vários usuários, times e planos**: dobraria o prazo sem somar sinal técnico novo. É um
  sistema de um dono; a RLS isola por usuário mesmo assim.
- **Sondas TCP, ICMP e DNS, e de várias regiões**: HTTP já exercita o pipeline inteiro; várias
  regiões exigem infraestrutura distribuída.
- **Slack, Telegram, webhook genérico**: dois canais já provam a abstração de notificação.
- **SSE ou WebSocket no painel**: o painel consulta de tempos em tempos, e isso basta.
- **Domínio próprio na status page**: verificação de DNS é escopo próprio.
- **Réplica, failover e PITR do Postgres**: um nó com snapshot diário e dump diário basta para
  este volume; no pior caso, perde-se um dia de checks.
- **Métricas raspadas em produção**: exigiriam abrir o endpoint na rede privada do Fly.

Os motivos completos estão na [spec](docs/superpowers/specs/2026-09-22-vigilume-design.md) (§2.2)
e nos [planos](docs/superpowers/plans/).

## Licença

Ainda não definida: o repositório é privado. A escolha vem junto com a publicação
([runbook](docs/deploy/fly.md), "Tornar o repositório público").
````

- [ ] **Step 2: `.gitignore`, pendências e roadmap**

Aplique em `.gitignore`:

```diff
--- a/.gitignore
+++ b/.gitignore
@@ -12,3 +12,5 @@
 *.test
 coverage.out
 /.superpowers/
+# Configuração local do GoLand/Rider; não vai para o repositório.
+.idea/
```

Crie `docs/superpowers/PENDENCIAS-PARA-O-PLANO-8.md`:

```markdown
# Pendências depois do Plano 7

Este arquivo existe porque o ledger de execução vive em `.superpowers/`, fora do controle de
versão. O que está aqui é o subconjunto que não pode se perder. Ele substitui o
`PENDENCIAS-PARA-O-PLANO-6.md` como estado corrente; os anteriores ficam como histórico.

## Fechadas no Plano 7

A seção "Já valendo em produção" do `PENDENCIAS-PARA-O-PLANO-5.md` (itens 1 a 6) e as duas do
`PENDENCIAS-PARA-O-PLANO-6.md`:

| Pendência | Como fechou |
|---|---|
| 1. `TRUSTED_PROXIES` no Fly | Não era só configuração: o proxy do Fly põe o IP do **app** no fim do `X-Forwarded-For`. `CLIENT_IP_HEADER=Fly-Client-IP` (D22) nos três limitadores e no `ip_hash`, com aviso limitado no log que mostra o `remote_addr` do proxy; o CIDR é medido no passo 9 do runbook |
| 2. Papel comum do Postgres | `deploy/compose/initdb.sql`: `vigilume_app` sem superusuário nem `BYPASSRLS`, dono do banco. O mesmo SQL no compose, na restauração do backup e em produção |
| 3. `METRICS_ADDR` | `off` em produção (D33); raspar pelo Fly exigiria abrir a rede privada |
| 4. E-mail | Opcional no runbook (passo 6): `RESEND_API_KEY` e `EMAIL_FROM` juntas, domínio verificado |
| 5. `PUBLIC_BASE_URL` | `https://<app>.fly.dev` no `fly.toml`; é o host da API, do painel e da status page |
| 6. Primeira manutenção | Num banco novo não há o que consolidar (runbook, passo 12) |
| Usuário inicial | Seed do compose por pipe (código 3 = sucesso); em produção, `vigilume user create` pelo `fly proxy` (passo 10) |
| Política de senha | A senha de demonstração tem 18 caracteres |
| Compressão das respostas da API (deixada ao Plano 7 pelo Plano 6: o summary tem ~180 KB a cada 15 s com 100 alvos) | O proxy do Fly comprime (zstd, brotli ou gzip) toda resposta sem `Content-Encoding` quando o app usa `[http_service]`, que é o caso; o painel já sai com gzip próprio e passa direto. Em desenvolvimento não há compressão, e não faz falta |

## Dependem do dono do projeto (nenhum agente faz)

- Criar os apps, o volume, os papéis e os segredos no Fly, e o primeiro deploy (runbook
  `docs/deploy/fly.md`). **Tem custo recorrente** (~US$ 9–11/mês).
- Gravar os secrets de repositório (`FLY_DEPLOY_TOKEN`, `FLY_BACKUP_TOKEN`,
  `BACKUP_DATABASE_PASSWORD`, `AGE_RECIPIENT`) e as variáveis (`PG_VOLUME_GB`, `BACKUP_ENABLED=1`);
  rodar o `backup` à mão uma vez (runbook, passo 13).
- Medir o endereço do proxy do Fly, preencher `TRUSTED_PROXIES` e levá-lo ao `fly.toml` por PR;
  o teste de dois pontos de rede. O workflow `deploy` recusa o `TRUSTED_PROXIES` vazio.
- Opcional: GitHub Pro (~US$ 4/mês) dá secrets por environment num repositório privado; o plano
  não depende dele.
- Tornar o repositório e o pacote do GHCR públicos, quando quiser (runbook, última seção), e
  escolher a licença junto.

## Limites conhecidos do Plano 7

- **Os 30 s do compose dependem da imagem publicada ser pública.** Privada, o compose constrói
  do código (~40 s numa máquina boa).
- **O seed não refaz os alvos de exemplo.** Se o `user create` passou e o cadastro dos alvos
  falhou, o `up` seguinte vê o usuário (código 3) e não tenta de novo.
- **Deploy sem revisor obrigatório:** no GitHub Free com repositório privado não há
  environments. A trava é quem pode disparar o workflow (acesso de escrita: hoje, só o dono), e o
  job só roda da `main`. Com o repositório público, um environment `production` com revisor entra
  (runbook, "Tornar o repositório público").
- **A imagem publicada não é, byte a byte, a do E2E:** o `publish` reconstrói do mesmo código e
  das mesmas bases, para as duas arquiteturas, e reescaneia com o Trivy antes de dar tag.
- **Máquina única do app:** cada deploy deixa o monitor alguns segundos fora (`rolling`, D26).
- **Postgres num nó só**, sem PITR: perde-se até um dia de checks no pior caso.
- **Dois PRs do Dependabot para o mesmo Postgres:** o do `compose.yaml` (ecossistema
  `docker-compose`) e o de `deploy/fly/db/Dockerfile` (`docker`, que o `backup.yml` também lê) chegam
  separados; aceitar os dois juntos, para desenvolvimento, testes e produção seguirem na mesma imagem.
- **Versões das ferramentas Go do CI** (`staticcheck`, `gosec`, `govulncheck`, `sqlc`, `gitleaks`,
  no `env` do `ci.yml`) se atualizam à mão: o Dependabot não as vê.

## Herdadas, ainda abertas

As do `PENDENCIAS-PARA-O-PLANO-6.md`, seção "Herdadas, ainda abertas", seguem como estavam.
```

Aplique em `docs/superpowers/plans/2026-09-22-roadmap.md`:

```diff
--- a/docs/superpowers/plans/2026-09-22-roadmap.md
+++ b/docs/superpowers/plans/2026-09-22-roadmap.md
@@ -16,7 +16,7 @@
 | 4 | Status page e retenção | `2026-09-24-statuspage-e-retencao.md` | Página pública, rollups, partições | 10 | concluído |
 | 5 | API do painel | `2026-09-24-api-do-painel.md` | Conta, sessões, resumo do dashboard | 11 | concluído |
 | 6 | Painel React | `NN-painel-react.md` | Painel completo servido pelo binário | 12–13 | a executar |
-| 7 | Empacotamento e deploy | `NN-empacotamento-e-deploy.md` | Compose, CI, deploy, README | 14 | a executar |
+| 7 | Empacotamento e deploy | `2026-09-24-empacotamento-e-deploy.md` | Compose, CI, deploy, README | 14 | concluído |
 
 Em 2026-09-24 o antigo Plano 5 (painel, deploy e README) virou três, com um contrato comum:
 `docs/superpowers/specs/2026-09-24-planos-5-6-7-contrato.md`.
```

- [ ] **Step 3: Conferir**

Todo link relativo do README aponta para um arquivo que existe:

```bash
grep -o '](\([^)#]*\))' README.md | sed 's/^](//; s/)$//' | grep -v '^http' | while read -r l; do [ -e "$l" ] || echo "quebrado: $l"; done
```

Expected: sem saída.

Run: `mkdir -p .idea && git status --porcelain .idea; rmdir .idea 2>/dev/null`
Expected: sem saída (o diretório é ignorado).

Run: `go test -race -count=1 ./... && go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 .github/workflows/*.yml`
Expected: PASS e sem saída.

- [ ] **Step 4: Commit**

```bash
git add README.md .gitignore docs/superpowers/PENDENCIAS-PARA-O-PLANO-8.md docs/superpowers/plans/2026-09-22-roadmap.md
git commit -m "docs: README em português, pendências de deploy fechadas e roadmap"
```

---

## Depois da Task 9

- **O que o dono do projeto faz, e nenhum agente faz sem um "sim" explícito naquele momento** (spec 6.4): criar os apps, o volume, os papéis e os segredos no Fly (com custo recorrente, ~US$ 9–11/mês); o primeiro deploy do banco e do app; os secrets e as variáveis do repositório no GitHub (`FLY_DEPLOY_TOKEN`, `FLY_BACKUP_TOKEN`, `BACKUP_DATABASE_PASSWORD`, `AGE_RECIPIENT`, `PG_VOLUME_GB`, `BACKUP_ENABLED`); levar o `TRUSTED_PROXIES` medido ao `fly.toml` por PR; medir o endereço do proxy do Fly e preencher `TRUSTED_PROXIES`, com o teste de dois pontos de rede (runbook, passo 9); cada deploy pelo workflow; e, no futuro, tornar o repositório e o pacote públicos, depois da varredura de segredos.
- **Primeiro push depois do merge:** o `publish` roda pela primeira vez e cria o pacote `ghcr.io/bipoxdxd/vigilume`, privado. Conferir no resumo do job o digest e as tags `:latest` e `:latest-tools`; com elas, `docker login ghcr.io` + `docker compose pull` passa a dar o caminho rápido do compose para quem tem acesso.
- **Riscos que só o ambiente real fecha:** se o token de app do Fly não bastar para o `flyctl proxy` do backup, o runbook diz o que fazer (passo 13); o formato do `X-Forwarded-For` do Fly vem da documentação, e o passo 9 do runbook o confirma.
- **Fora deste plano, registrado:** `--role=api` e `--role=prober` em máquinas separadas; `bluegreen` com migrations só aditivas; raspar métricas pelo Fly (D33 da spec); PITR; Trusted Types no painel (Plano 6).
