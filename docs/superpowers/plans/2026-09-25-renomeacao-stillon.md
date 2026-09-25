# StillOn — renomeação do Vigilume

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Trocar o nome do projeto de Vigilume para **StillOn** no repositório inteiro (código, painel, deploy, CI e docs), apagar a documentação histórica dos planos já executados e deixar no CI uma checagem que reprova qualquer volta do nome antigo. O comportamento não muda, exceto nos identificadores que carregam o nome: cookies, travas, rótulos criptográficos, papéis do Postgres, variáveis de ambiente, imagem e caminhos da VPS. Não há produção nem dado real, e por isso todos mudam agora, sem período de transição.

**Architecture:** Uma tarefa sequencial para o módulo Go (T1). Depois, quatro áreas em paralelo, cada uma na sua worktree, com arquivos **disjuntos**: (a) backend Go, (b) painel `web/`, (c) deploy, compose, workflows, config e CI, (d) docs. Os pontos de contato entre áreas não são negociados pelos agentes: estão fixados na tabela de mapeamento abaixo, e cada lado aplica a sua metade. Os testes que atravessam áreas (compose com o binário, e2e, imagem, VPS) só rodam depois da integração (T7). A checagem do nome antigo entra por último (T8), quando o repositório já está limpo.

**Tech Stack:** Go 1.26, PostgreSQL 18, `pgx/v5`, `sqlc` v1.31.1, `goose` v3, `testcontainers-go`. Painel: React 19, TypeScript, Vite, Vitest, Playwright, `oxlint`. Deploy: Docker e compose, Caddy, systemd, `age`, GitHub Actions, `actionlint`, `shellcheck`, `gitleaks` v8.30.1.

**Base:** `main` depois do merge do Plano 11. O inventário deste plano foi feito na branch `plano-11-hospedagem` em `77b785d`, que já contém o Plano 11 inteiro: 243 arquivos versionados com o nome (1 `go.mod`, 88 `.go` só com import, 154 com outras ocorrências). O `levantamento.md` (`.superpowers/sdd/renomeacao/`) tem o porquê de cada item. Onde ele e este plano divergem, vale este plano (veja "Desvios do levantamento").

**Commits:** cada commit termina com a linha
`Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>`. O histórico do git **não** é reescrito.

---

## Global Constraints

### Grafia

- **StillOn** é a marca, usada em texto para gente: painel, e-mail, HTML, User-Agent, emissor do TOTP, comentários de pacote e docs.
- **stillon** vale para identificadores: módulo, binário, imagem, papéis, cookies, chaves de trava, rótulos, compose, arquivos e caminhos.
- **STILLON_** é o prefixo de variáveis de ambiente e do marcador `STILLON_CSP_NONCE`.
- Na troca mecânica de texto, aplique **primeiro** os casos especiais da tabela ("Domínios de teste" e "Chaves de demo") e só depois o genérico, nesta ordem:
  `s/VIGILUME/STILLON/g; s/Vigilume/StillOn/g; s/vigilume/stillon/g`.
  O sed genérico **nunca** entra em `vigi`, `vigia`, `sentinela` nem `Fly`. Revise o diff linha a linha.

### Tabela de mapeamento (vinculante; nenhum agente muda um valor daqui)

**Módulo, binário e imagem**

| Velho | Novo | Onde |
|---|---|---|
| `github.com/BipoXDXD/vigilume` | `github.com/BipoXDXD/stillon` | `go.mod`, imports (T1) |
| `cmd/vigilume/` | `cmd/stillon/` (`git mv`) | T1 |
| `bin/vigilume`, `/vigilume` (na imagem), `/out/vigilume` | `bin/stillon`, `/stillon`, `/out/stillon` | Makefile, Dockerfile, composes, scripts, pilha.ts |
| `.gitignore` `/vigilume`, `.dockerignore` `vigilume` | `/stillon`, `stillon` | (c) |
| `vigilume user …`, `vigilume version`, `vigilume healthcheck`, `flag.NewFlagSet("vigilume user "+sub)` | `stillon user …`, `stillon version`, `stillon healthcheck`, `"stillon user "+sub` | (a) |
| emissor TOTP `totpIssuer = "Vigilume"` | `"StillOn"` | `cmd/stillon/main.go` (a) |
| `ghcr.io/bipoxdxd/vigilume` (e `…-tools`) | `ghcr.io/bipoxdxd/stillon` | ci.yml `IMAGEM`, deploy.yml `ORIGEM`, compose.yaml |
| tags locais `vigilume:dev`, `vigilume:$(VERSION)`, `vigilume:tools` | `stillon:dev`, `stillon:$(VERSION)`, `stillon:tools` | Makefile, comentários do Dockerfile |
| VPS: `IMAGEM=vigilume`, `image: vigilume:${VIGILUME_SHA:-atual}` | `IMAGEM=stillon`, `image: stillon:${STILLON_SHA:-atual}` | `stillon-entrada`, `compose.prod.yaml` |
| imagem e projeto de teste `vigilume-teste-vps` | `stillon-teste-vps` | `deploy/ci/vps/compose.teste.yaml`, `checar-vps.sh` |
| guarda do publish `github.repository == 'BipoXDXD/vigilume'` | `github.repository == 'BipoXDXD/stillon'` | ci.yml (c) |
| nome do pacote npm `vigilume-painel` | `stillon-painel` | `web/package.json`, lock regenerado (b) |

**Compose e rede**

| Velho | Novo |
|---|---|
| `name: vigilume` (compose.yaml e compose.prod.yaml) | `name: stillon` (o volume vira `stillon_pgdata`) |
| rede `vigilume` do compose.prod.yaml (172.30.0.0/24) e `networks: vigilume:` dos serviços | rede `stillon`, com a mesma sub-rede e os mesmos IPs |
| `…/docker-entrypoint-initdb.d/10-vigilume.sql` e `10-vigilume.sh` | `10-stillon.sql` e `10-stillon.sh` |

**Variáveis de ambiente e marcadores** (as variáveis de config sem prefixo, como `DATABASE_URL` e `MASTER_KEY`, **não** mudam)

| Velho | Novo |
|---|---|
| `VIGILUME_PORT`, `VIGILUME_TAG`, `VIGILUME_BIN`, `VIGILUME_EXIGIR_DIST` | `STILLON_PORT`, `STILLON_TAG`, `STILLON_BIN`, `STILLON_EXIGIR_DIST` |
| `VIGILUME_PASSWORD` (teste negativo em `user_test.go`) | `STILLON_PASSWORD` |
| variável de shell `VIGILUME` no `seed.sh` | `STILLON` |
| `VIGILUME_DOMINIO`, `VIGILUME_APP_PASSWORD`, `VIGILUME_BACKUP_PASSWORD`, `VIGILUME_SHA`, `VIGILUME_CADDY_TESTE` | `STILLON_DOMINIO`, `STILLON_APP_PASSWORD`, `STILLON_BACKUP_PASSWORD`, `STILLON_SHA`, `STILLON_CADDY_TESTE` |
| marcador CSP `VIGILUME_CSP_NONCE` (`webui.Marcador` e `cspNonce` do Vite) | `STILLON_CSP_NONCE` (os dois lados na área b) |

**Postgres**

| Velho | Novo | Onde |
|---|---|---|
| papel `vigilume_app` (senha local `vigilume_app`) | `stillon_app` (senha local `stillon_app`) | initdb.sql, compose.yaml, verificar.sql, checar-compose.sh, testdb.go, initdb.sh, compose.prod.yaml, checar-vps.sh |
| papel `vigilume_backup` | `stillon_backup` | backup.sh, initdb.sh, stillon-entrada, checar-vps.sh |
| banco `vigilume` | `stillon` | idem, mais backup.sh (`-d stillon`), `web/e2e/init.sql`, `pilha.ts` |
| testcontainers: banco `vigilume_test`, admin `vigilume`/`vigilume` | `stillon_test`, `stillon`/`stillon` | testdb.go |
| prefixos temporários `vigilume_vazio_%d`, `vigilume_bypass_` | `stillon_vazio_%d`, `stillon_bypass_` | testdb.go, role_test.go |
| papel do e2e `vigilume` (senha `vigilume`), banco `vigilume` | `stillon`, `stillon`, `stillon` | `web/e2e/init.sql`, `pilha.ts` |

**Travas advisory** (as migrations são editadas **no lugar**, sem migration nova)

| Velho | Novo | Onde |
|---|---|---|
| `'vigilume.sessions:' \|\| user_id` | `'stillon.sessions:'` | `queries/sessions.sql` + `sqlc generate`; e o recálculo em `account_test.go`, `auth/totp_prova_test.go`, `auth/totp_rotation_test.go`, `httpapi/totp_troca_integration_test.go`, `httpapi/totp_prova_integration_test.go` |
| `'vigilume.targets:' \|\| user_id` | `'stillon.targets:'` | `queries/targets.sql` + `sqlc generate` |
| `'vigilume.user_create'` | `'stillon.user_create'` | `queries/users.sql` + `sqlc generate` |
| `'vigilume.ensure_checks_partition'` | `'stillon.ensure_checks_partition'` | `migrations/00005_…sql:150` (em `ensure_checks_partition`), `migrations/00006_…sql:149` (em `drop_checks_partitions_before`), `storage/notify_rls_test.go:341` |

**Rótulos criptográficos**

| Velho | Novo | Onde |
|---|---|---|
| `"vigilume.dispositivo.v1"` (`info` do HKDF da chave do MAC do cookie de dispositivo) | `"stillon.dispositivo.v1"` | `internal/auth/dispositivo.go` `rotuloDispositivo` |
| `"vigilume.rotacao.v1"` (prefixo de domínio do id de rotação) | `"stillon.rotacao.v1"`, numa constante nova `rotuloRotacao` | `internal/auth/totp_rotation.go` `rotacaoDe` |
| AAD do `crypto.Box` (`tabela:coluna:id`) | **não muda** (não contém a marca) | — |

**HTTP, navegador e textos**

| Velho | Novo |
|---|---|
| cookie `vigilume_refresh` | `stillon_refresh` (authhandlers.go, testes Go, seed.sh, fluxo.spec.ts, README) |
| cookie `vigilume_dispositivo` | `stillon_dispositivo` |
| `X-Requested-With: vigilume` | `X-Requested-With: stillon` (http.ts e api-falsa.ts juntos; testes Go, seed.sh, checar-compose.sh, checar-vps.sh, fluxo.spec.ts, README) |
| User-Agent `Vigilume/1.0 (+monitor de disponibilidade)`, teste `Vigilume/teste` | `StillOn/1.0 (+monitor de disponibilidade)`, `StillOn/teste` |
| localStorage `vigilume.tema` | `stillon.tema` (tema.tsx e tema.js juntos) |
| Web Lock `vigilume:refresh`, BroadcastChannel `vigilume:sessao` | `stillon:refresh`, `stillon:sessao` |
| download `vigilume-codigos-de-recuperacao.txt` | `stillon-codigos-de-recuperacao.txt` |
| plugin do Vite `'vigilume:gitkeep'`; função do e2e `vigilumeViolacao` | `'stillon:gitkeep'`; `stillonViolacao` |
| assuntos `[Vigilume] …`, textos do Discord, do Resend e das páginas de verificação, rodapé `· Vigilume` do status.html | `[StillOn] …` etc. |
| `otpauth://totp/Vigilume:…&issuer=Vigilume` nos mocks do painel | `otpauth://totp/StillOn:…&issuer=StillOn` |

**Domínios de teste e contas de demo** (aplicar **antes** do sed genérico)

| Velho | Novo |
|---|---|
| `vigilume.exemplo.com.br`, `vigilume.exemplo.com` | `stillon.example` |
| `vigilume.dev`, `alertas@vigilume.dev` | `stillon.example`, `alertas@stillon.example` (**nunca** `stillon.dev`, que é de terceiros) |
| `vigilume.teste` como domínio (`DOMINIO=` e `dono@vigilume.teste` em checar-vps.sh) | `stillon.example`, `dono@stillon.example` |
| rótulo Docker `LABEL vigilume.teste=a…d` (checar-vps.sh) | `LABEL stillon.teste=a…d` (é chave de rótulo, não domínio) |
| `e2e@vigilume.test` (pilha.ts) | `e2e@stillon.example` |
| `vigilume.example`, `app.vigilume.example`, `localhost.vigilume.example` | `stillon.example`, `app.stillon.example`, `localhost.stillon.example` |
| `vigilume-e2e.invalid` | `stillon-e2e.invalid` (continua `.invalid`) |
| `postgres://localhost/vigilume` (config_test) | `postgres://localhost/stillon` |
| usuário de demo `demo@vigilume.local` / `demo-vigilume-2026` | `demo@stillon.local` / `demo-stillon-2026` |

Se dois casos viram o mesmo valor dentro de uma mesma tabela de teste (chave duplicada num literal de map ou subteste repetido), use `outro.stillon.example` no segundo.

**Chaves de demo** (32 bytes cada; um sed ingênuo daria 31 e o boot as recusaria)

| Variável | Texto novo (conferido com `printf %s … \| wc -c` = 32) | base64 novo (compose.yaml e `.gitleaks.toml`) |
|---|---|---|
| `MASTER_KEY` | `demo-stillon-MASTER_KEY-naouse!!` | `ZGVtby1zdGlsbG9uLU1BU1RFUl9LRVktbmFvdXNlISE=` |
| `JWT_SECRET` | `demo-stillon-JWT_SECRET-naouse!!` | `ZGVtby1zdGlsbG9uLUpXVF9TRUNSRVQtbmFvdXNlISE=` |
| `IP_HASH_KEY` | `demo-stillon-IP_HASH_KEY-naouse!` | `ZGVtby1zdGlsbG9uLUlQX0hBU0hfS0VZLW5hb3VzZSE=` |

As três chaves antigas **continuam recusadas**:
- Em `chavesDeDemo`, elas vêm **depois** das novas (o `TestComposeUsesTheDemoKeys` compara pelo índice 0..2). Ficam escritas em base64, decodificadas na inicialização, e não como texto. Os valores são `ZGVtby12aWdpbHVtZS1NQVNURVJfS0VZLW5hb3VzZSE=`, `ZGVtby12aWdpbHVtZS1KV1RfU0VDUkVULW5hb3VzZSE=` e `ZGVtby12aWdpbHVtZS1JUF9IQVNIX0tFWS1uYW91c2U=`.
- O allowlist do `.gitleaks.toml` fica com as **seis** regex ancoradas: as três antigas, porque o gitleaks do CI varre o histórico inteiro, e as três novas.

**VPS: caminhos, unidades e arquivos**

| Velho | Novo |
|---|---|
| `/opt/vigilume/` (`.env`, `Caddyfile`, `age-recipient`, `deploy/`) | `/opt/stillon/` |
| `/usr/local/sbin/vigilume-entrada`, `/usr/local/sbin/vigilume-vigia` | `/usr/local/sbin/stillon-entrada`, `/usr/local/sbin/stillon-vigia` |
| `/etc/sudoers.d/vigilume-entrada`, `command="sudo /usr/local/sbin/vigilume-entrada"` | `/etc/sudoers.d/stillon-entrada`, `command="sudo /usr/local/sbin/stillon-entrada"` |
| unidades `vigilume-vigia.service` / `.timer`, `Unit=vigilume-vigia.service`, `Description=Vigilume: …` | `stillon-vigia.service` / `.timer`, `Unit=stillon-vigia.service`, `Description=StillOn: …` |
| `LoadCredential=vigilume-env:/opt/vigilume/.env`, `$CREDENTIALS_DIRECTORY/vigilume-env` | `LoadCredential=stillon-env:/opt/stillon/.env`, `…/stillon-env` |
| temporários `vigilume-entrada.XXXXXX`, `vigilume-backup.XXXXXX`, contêiner `vigilume-restauro-$$` | `stillon-entrada.XXXXXX`, `stillon-backup.XXXXXX`, `stillon-restauro-$$` |
| dumps `vigilume.dump`, `vigilume-AAAAMMDD.dump.age`, `vigilume-20260925.dump.age`, `vigilume-20260925.sql.age` | `stillon.dump`, `stillon-AAAAMMDD.dump.age`, `stillon-20260925.dump.age`, `stillon-20260925.sql.age` |
| chave do age `~/vigilume-backup.key`; artifact `vigilume-backup-${{ github.run_id }}`, `vigilume-*.dump.age` | `~/stillon-backup.key`; `stillon-backup-${{ github.run_id }}`, `stillon-*.dump.age` |
| runbook: `~/.ssh/vigilume_admin`, `vigilume_deploy`, `Host vigilume-vps`, `00-vigilume.conf`, `52vigilume-reinicio`, `vigilume-arquivos`, `vigilume_pgdata` etc. | o mesmo com `stillon` (sed genérico em `docs/deploy/vps.md`) |

**Arquivos renomeados no repositório (`git mv`)**

| Velho | Novo | Tarefa |
|---|---|---|
| `cmd/vigilume/` (9 arquivos) | `cmd/stillon/` | T1 |
| `deploy/vps/vigilume-entrada` | `deploy/vps/stillon-entrada` | T4 |
| `deploy/vps/sudoers-vigilume-entrada` | `deploy/vps/sudoers-stillon-entrada` | T4 |
| `deploy/vps/vigilume-vigia.service` | `deploy/vps/stillon-vigia.service` | T4 |
| `deploy/vps/vigilume-vigia.timer` | `deploy/vps/stillon-vigia.timer` | T4 |
| `docs/superpowers/specs/2026-09-22-vigilume-design.md` | `docs/superpowers/specs/2026-09-22-stillon-design.md` | T5 |

**O que NÃO muda:**
- o histórico do git e as mensagens de commit;
- as métricas (`pgxpool_*`, `probe_duration_seconds`);
- as variáveis de config sem prefixo;
- a AAD do Box;
- "é uma sentinela" (`internal/crypto/box_test.go`), as palavras "vigia"/"vigia externo" e `vigia.sh`/`checar-vigia.sh`;
- o cabeçalho `Fly-Client-IP`;
- `.superpowers/` (ledgers e spikes, fora do git);
- os domínios genéricos sem a marca (`exemplo.com`, `voce@exemplo.com`, `dono%40exemplo.com`).

### Referências à documentação apagada

- Uma referência a **arquivo** apagado (caminho, link, ou nome como `PENDENCIAS-PARA-O-PLANO-5` e "PENDENCIAS do Plano 6") é reescrita. Nos comentários de código, ela vira o identificador "pendência do Plano N". Nas docs que ficam, vira "(apagado em 2026-09-25; ver README, Histórico)".
- Identificadores de decisão nos comentários ficam como estão: "D32 do Plano 7", "spec do Plano 10, §4", "banco S1 da revisão", "A7 do Plano 10" (são cerca de 600 linhas). O README ganha a seção **Histórico**, que diz como lê-los: `git show <BASE>:docs/superpowers/...`, com o `<BASE>` registrado na T0.
- "spec §N" sem número de plano é a spec do sistema, que fica (renomeada).

### Exceções à checagem do nome antigo (lista fechada)

1. `README.md`, linha 1, exatamente `# StillOn (até 2026-09-25, Vigilume)`.

Não há outra exceção. Estes lugares ficam sem o texto antigo:
- as chaves antigas: base64 em `config.go` e no `.gitleaks.toml`, e o base64 não contém a marca;
- os comentários do `.gitleaks.toml` e do `config.go`, reescritos ("as chaves do nome anterior do projeto");
- as regras do `.gitignore`: os globs `*.key`, `*.dump` e `*.age` já cobrem os nomes antigos, e a linha `/vigilume` vira `/stillon`;
- o próprio `deploy/ci/checar-nome.sh`, que monta o nome em partes.

## Execução e ordem

```
T0 (controlador) → T1 (sozinha) → T2 ∥ T3 ∥ T4 ∥ T5 (worktrees) → T6 (4 revisores ∥)
   → T7 (integração + gate cruzado) → T8 (checagem no CI + sabotagens) → T9 (gates finais, revisão final, PR)
   → Passos do dono (renomear o repo ANTES do merge) → merge → Passos do dono (depois do merge)
```

- **Dono de cada ocorrência.** A T1 é dona só das linhas de import `github.com/BipoXDXD/vigilume` (em todos os `.go`), do `go.mod` e do `git mv cmd/vigilume cmd/stillon`. Depois da T1, cada arquivo que ainda contém o nome pertence a **exatamente uma** das áreas T2 a T5 (listas abaixo, conferidas por `sort | uniq -d` vazio). Algumas tarefas sequenciais depois mexem em arquivos das áreas sem tocar no nome: a T8 põe um passo no `ci.yml` e apaga a cópia deste plano, e a T7 só integra. Isso não fere a disjunção, porque nada roda em paralelo com elas.
- **Worktrees.** T2..T5 rodam cada uma em `.superpowers/renomeacao-wt/{a,b,c,d}`, branches `renomear-stillon-{a,b,c,d}`, todas criadas a partir do commit da T1. Um agente só edita arquivos da sua lista e os novos que a tarefa declara.
- **Integração (T7).** Cherry-pick na ordem a → b → c → d, sobre `renomear-stillon`. Como os arquivos são disjuntos, não pode haver conflito. Se houver, **pare**: é defeito do plano, e não se resolve na mão.
- **Testes de fronteira.** Os testes que atravessam áreas ficam para a T7, porque numa worktree isolada ficariam vermelhos, ou pior, **vacuamente verdes**. São eles: `checar-compose.sh` (o `seed.sh` da área c extrai o cookie do binário da área a), Playwright contra o compose, `checar-imagem.sh` com o painel e `checar-vps.sh`.

## Review Focus

- **Pares que só funcionam juntos**, um lado em cada área:
  - `STILLON_EXIGIR_DIST` no `ci.yml` (c) e no `webui_test.go` (b). Se divergirem, o teste do dist embutido passa vacuamente no CI.
  - O cookie `stillon_refresh`: `authhandlers.go` (a), `seed.sh` (c) e `fluxo.spec.ts` (b). O `not.toContain` do e2e fica vacuamente verde com o nome errado.
  - O healthcheck `/stillon` no compose (c) e o binário na imagem (c/T1).
  - O guarda do publish (c) e o nome do repositório no GitHub (dono).
- **Trava advisory.** A string da query, a das migrations e a que os testes recalculam são **iguais**. Os testes de `pg_locks` ficam vermelhos quando só um lado muda (sabotagens S-a1, S-a2).
- **Chaves de demo.**
  - As três novas têm 32 bytes.
  - As três antigas continuam recusadas com `PUBLIC_BASE_URL` https.
  - O gitleaks passa sobre o histórico inteiro.
- **Domínios.** Nenhum `stillon.dev`. Os e-mails de teste são `@stillon.example`, e os de demo, `@stillon.local`.
- **Nada de `vigi`/`vigia`/`sentinela` alterado por engano.** Revise com `git diff <BASE> -- | grep -n '^[-+].*vigia'`.
- **Docs.**
  - Nenhum link ou caminho aponta para arquivo apagado.
  - Os `.md` que ficam estão coerentes com o Plano 11 (VPS, e não Fly) só no que a renomeação toca. Não reescreva a spec.

---

## T0: preparação (controlador)

**Files:**
- Create: `docs/superpowers/plans/2026-09-25-renomeacao-stillon.md` (cópia deste plano; **apagada na T8**)

- [ ] **Passo 1.** Pré-condições:
  - o Plano 11 está integrado ao `main`, com CI verde;
  - os passos P1 do dono foram feitos (GoLand fechado; worktrees do `.superpowers/plano-11-spike/` removidas com `git worktree remove` e `git worktree prune`, depois de confirmar que não têm trabalho fora do `main`);
  - ninguém mexeu na VPS.
- [ ] **Passo 2.** Crie a branch e registre a base:
  ```
  git switch main && git pull --ff-only
  git switch -c renomear-stillon
  BASE=$(git rev-parse HEAD); echo "$BASE" > .superpowers/sdd/renomeacao/BASE
  ```
- [ ] **Passo 3.** Refaça o inventário e compare com o do plano. As contagens esperadas são 243 arquivos, 131 `.go` com o import e 339 linhas de import:
  ```
  git ls-files -z | xargs -0 grep -Ili vigilume | sort > /tmp/inv.txt; wc -l < /tmp/inv.txt
  git grep -l 'github.com/BipoXDXD/vigilume' -- '*.go' | wc -l
  git grep -n 'github.com/BipoXDXD/vigilume' -- '*.go' | wc -l
  ```
  Todo arquivo do inventário que não estiver no Apêndice A, nem for um `.go` só com import, nem estiver na lista de apagados, **para a execução**. Atribua o arquivo a uma área neste plano antes de seguir.
- [ ] **Passo 4.** Apague as sobras locais de build fora do git: `rm -f ./vigilume bin/vigilume web/e2e/.tmp/vigilume`.
- [ ] **Passo 5.** Copie este plano para `docs/superpowers/plans/2026-09-25-renomeacao-stillon.md` e commite:
  `docs(plano): renomeação do Vigilume para StillOn`.

## T1: módulo Go e `cmd/stillon` (sozinha)

**Files:**
- Modify: `go.mod` (linha 1)
- Modify: as linhas de import em **todos** os `.go` que têm `github.com/BipoXDXD/vigilume` (131 arquivos, 339 linhas; o conjunto é o de `git grep -l 'github.com/BipoXDXD/vigilume' -- '*.go'`)
- Rename: `cmd/vigilume/` → `cmd/stillon/` (9 arquivos: `incident_e2e_test.go`, `main.go`, `main_test.go`, `ops.go`, `ops_test.go`, `reset2fa_test.go`, `statuspage_e2e_test.go`, `user.go`, `user_test.go`)

O **conteúdo** de `cmd/stillon/*` além dos imports é das áreas: `ops_test.go` fica com a T4, e o resto com a T2.

- [ ] **Passo 1.** Aplique a troca:
  ```
  go mod edit -module github.com/BipoXDXD/stillon
  git grep -lz 'github.com/BipoXDXD/vigilume' -- '*.go' | xargs -0 sed -i 's#github.com/BipoXDXD/vigilume#github.com/BipoXDXD/stillon#g'
  git mv cmd/vigilume cmd/stillon
  gofmt -w $(git ls-files '*.go' 'cmd/stillon/*.go')
  ```
- [ ] **Passo 2.** Aceite:
  ```
  test "$(head -1 go.mod)" = "module github.com/BipoXDXD/stillon"
  git grep -n 'BipoXDXD/vigilume' -- '*.go' go.mod          # vazio
  git grep -l 'github.com/BipoXDXD/stillon' -- '*.go' | wc -l   # 131
  git ls-files cmd/stillon | wc -l                            # 9
  test -z "$(gofmt -l .)"
  go build ./... && go vet ./... && staticcheck ./...
  go test -race -count=1 ./...
  ```
  Até a T4, o `make build`, o `docker build` e o `pilha.ts` ficam **quebrados** (ainda apontam para `./cmd/vigilume`). Isso é esperado, e só se confere na T7.
- [ ] **Passo 3.** Commit: `refactor: módulo github.com/BipoXDXD/stillon e cmd/stillon`.
- [ ] **Passo 4.** Crie as 4 worktrees a partir deste commit:
  ```
  T1=$(git rev-parse HEAD)
  for x in a b c d; do git worktree add -b renomear-stillon-$x .superpowers/renomeacao-wt/$x "$T1"; done
  ```

---

## T2 (área a): backend Go

**Files** (55 com o nome depois da T1, mais 2 sem o nome):
- Modify: os 55 arquivos da lista **A** do Apêndice A
- Modify: `internal/httpapi/publicpage.go` (linha 40: `PENDENCIAS-PARA-O-PLANO-5` → "pendência do Plano 5"; o arquivo não tem mais o nome depois da T1)
- Regenerate: `internal/storage/db/{sessions,targets,users}.sql.go` (via `sqlc generate`, **nunca** à mão)
- Create: `internal/auth/rotulos_test.go`

**O que fazer:**
1. **CLI** (`cmd/stillon/{main,main_test,ops,reset2fa_test,user,user_test}.go`):
   - textos de uso, `flag.NewFlagSet("stillon user "+sub, …)`, `stillon user: interrompido`, `uso: stillon version` e `uso: stillon healthcheck …`;
   - "outro processo do StillOn";
   - `totpIssuer = "StillOn"`;
   - `STILLON_PASSWORD` no teste negativo;
   - o `reset2fa_test.go` espera `stillon user reset-2fa --email <e-mail>`.
2. **Travas.**
   - Edite `queries/{sessions,targets,users}.sql` conforme a tabela, inclusive os comentários `` `vigilume user create` `` e `vigilume user reset-2fa`. Depois rode `sqlc generate` (sqlc v1.31.1).
   - Edite as migrations `00005` (linha 150) e `00006` (linha 149) no lugar.
   - Troque a chave nos 5 testes que a recalculam e em `notify_rls_test.go`.
3. **Rótulos.**
   - `rotuloDispositivo = "stillon.dispositivo.v1"`.
   - Em `totp_rotation.go`, crie `const rotuloRotacao = "stillon.rotacao.v1"` e use-o em `rotacaoDe`.
   - Crie `internal/auth/rotulos_test.go` (`package auth`) com `TestRotulosFixados`, que prova três coisas:
     - (i) `rotuloDispositivo == "stillon.dispositivo.v1"` e `rotuloRotacao == "stillon.rotacao.v1"`;
     - (ii) a chave de `NewDevices(k, clk)` é igual a `hkdf.Key(sha256.New, k, nil, "stillon.dispositivo.v1", 32)` para uma `k` fixa de 32 bytes (use o relógio que o `dispositivo_test.go` já usa);
     - (iii) `rotacaoDe(p)` é igual a `hex.EncodeToString(sha256("stillon.rotacao.v1" || p)[:16])`, calculado no teste.
   - Os valores esperados vêm do **literal novo** no teste, e não da constante.
4. **Testdb e papéis.** `testdb.go`: `stillon_test`, `stillon`/`stillon`, `stillon_app`/`stillon_app`, `stillon_vazio_%d`. `role_test.go`: `stillon_bypass_`.
5. **HTTP.**
   - Cookies em `authhandlers.go` e nos testes.
   - `X-Requested-With` "stillon" nos testes Go (o servidor só exige valor não vazio: nada muda nele).
   - `verifyhandlers.go`: 4 títulos e textos com "StillOn".
   - `templates/status.html`: rodapé `· StillOn`.
   - Comentários de pacote em `server.go`, `middleware/requestid.go`, `apierr/errors.go` e `crypto/secret.go`: "do StillOn".
   - Domínios de teste conforme a tabela.
6. **Notificações.**
   - `message.go` (assuntos `[StillOn] …`), `resend.go` (corpo e assunto), `discord.go` e comentários do `dispatcher.go`;
   - `userAgent` em `notify.go`;
   - testes: `message_test.go`, `dispatcher_test.go`, `senders_test.go` (`from: "StillOn <alertas@stillon.example>"`) e `service_test.go` (`https://stillon.example/verify/` e `https://stillon.example/`).
7. **Sondador.** `probe.go` (`userAgent`), `probe_test.go` (prefixo `StillOn/`), `safedial/safedial.go` (comentário) e `safedial/transport_test.go` (`StillOn/teste`).
8. **Comentários.** `account/account.go`, `auth/password.go` e `db/users.sql.go` (regenerado a partir do `.sql`): `stillon user …`.

**Aceite** (na worktree `a`):
```
test -z "$(gofmt -l .)" && go vet ./... && staticcheck ./...
sqlc diff                                   # limpo
go test -race -count=1 ./...                # verde: config, webui e ops_test ainda são os antigos e coerentes entre si
git grep -nIi vigilume -- $(cat <lista A>) internal/httpapi/publicpage.go internal/auth/rotulos_test.go   # vazio
git grep -n 'stillon\.dev' -- .             # vazio
git diff --name-only <T1>.. | sort          # ⊆ lista A + publicpage.go + rotulos_test.go
```

**Sabotagens** (cada uma: aplique, rode, veja **vermelho** pela razão certa, desfaça com `git checkout -- <arq>` e anote a saída no relatório). Use o valor `sabotagem` e **não** o nome antigo:
- **S-a1:** em `queries/sessions.sql`, troque `stillon.sessions:` por `sabotagem.sessions:` e rode `sqlc generate`. Vermelho em `go test -count=1 -timeout 5m -run TestConfirmRotationTakesTheUserLock ./internal/auth/` e em `-run TestRotateRechecksTheFamilyUnderTheLock ./internal/httpapi/`. Desfaça com `git checkout -- internal/storage/queries internal/storage/db`.
- **S-a2:** em `00005_incidents_notifications.sql:150`, troque a chave. Vermelho em `go test -count=1 -timeout 5m -run TestPartitionCreationWaitsForTheAdvisoryLock ./internal/storage/`. Repita só com a `00006:149`. Se **nenhum** teste ficar vermelho, anote como lacuna, sem criar teste nesta tarefa: o grep e a revisão cobrem.
- **S-a3:** `refreshCookieName = "sabotagem_refresh"`. Vermelho em `go test -count=1 ./internal/httpapi/`.
- **S-a4:** `rotuloDispositivo = "sabotagem.v1"` → vermelho em `go test -count=1 -run TestRotulosFixados ./internal/auth/`. Depois, o mesmo com `rotuloRotacao`.
- **S-a5:** `userAgent` do `probe.go` = `"Sabotagem/1.0"` → vermelho em `-run TestProbeSendsConfiguredHeadersAndUserAgent ./internal/prober/`.
- **S-a6:** o assunto `[StillOn]` do `message.go` = `[Sabotagem]` → vermelho em `-run TestMessageForOpenedAndResolved ./internal/notify/`.

**Commit:** `refactor(backend): StillOn no código Go — travas, rótulos, cookies, textos e testes`

## T3 (área b): painel `web/` e o marcador CSP

**Files:** os 31 arquivos da lista **B** do Apêndice A (`web/**` com o nome, mais `internal/webui/webui.go`, `internal/webui/webui_test.go` e `internal/httpapi/painel_integration_test.go`).

**O que fazer:**
1. **Textos.**
   - `index.html`: `<title>StillOn</title>`.
   - `Layout.tsx`: "StillOn".
   - `ExigirSessao.tsx`: "Carregando o StillOn…".
   - `PaginaLogin.tsx`: "Entrar no StillOn".
   - `AvisoDeContexto.tsx`.
   - `Titulo.tsx`: `` `${aba} — StillOn` ``.
   - Os testes que fixam esses textos: `login.test.tsx` (10, com a regex `/^(Painel|Entrar no StillOn)$/`), `titulo.test.tsx`, `a11y.spec.ts`, `fluxo.spec.ts`, `troca.spec.ts` e `abas.spec.ts`.
2. **Identificadores do navegador.**
   - `tema.tsx` e `public/tema.js`: `stillon.tema`, no mesmo commit.
   - `sessao.ts`: `stillon:refresh` e `stillon:sessao`.
   - `http.ts` e `api-falsa.ts`: `X-Requested-With: stillon`, mais o `cliente.test.ts`.
   - `PaginaConfiguracoes.tsx`: `stillon-codigos-de-recuperacao.txt`.
   - Os mocks `otpauth://totp/StillOn:…&issuer=StillOn` em `api-falsa.ts`, `a11y.spec.ts`, `configuracoes.test.tsx` e `cliente.test.ts`.
3. **Vite e o marcador.**
   - `vite.config.ts`: plugin `'stillon:gitkeep'` e `cspNonce: 'STILLON_CSP_NONCE'`.
   - `webui.go`: `Marcador = "STILLON_CSP_NONCE"`.
   - `webui_test.go`: os 3 marcadores, a env `STILLON_EXIGIR_DIST` nas linhas 245-248 e o `console.log('stillon')`.
   - `painel_integration_test.go`: `indexFalso` com `STILLON_CSP_NONCE` e o domínio `https://stillon.example`.
4. **Pacote.** `package.json` `"name": "stillon-painel"`. Depois: `cd web && npm install --package-lock-only --no-audit --no-fund`. O diff do lock tem de ser só as 2 linhas de `name`.
5. **E2E.**
   - `pilha.ts`:
     - `EMAIL = 'e2e@stillon.example'`;
     - o binário `path.join(TMP, 'stillon')` e o build de `'./cmd/stillon'`;
     - `pg_isready -U stillon -d stillon`;
     - `DATABASE_URL` `postgres://stillon:stillon@…/stillon`;
     - `psql -d stillon`;
     - `/stillon user reset-2fa` no `compose exec`;
     - a checagem de `/proc/<pid>/cmdline`;
     - os comentários.
   - `init.sql`: o papel `stillon`, com senha `stillon`, e o banco `stillon`.
   - `fixtures.ts`: `stillonViolacao`, nas 3 ocorrências.
   - `fluxo.spec.ts`: header `stillon`, `stillon-e2e.invalid` e `not.toContain('stillon_refresh')`.
   - `alvos.test.tsx`: `stillon-e2e.invalid`.
   - `playwright.config.ts` e `troca.spec.ts`: comentários (`stillon user …`, "O StillOn tem um usuário só").
6. **Referências a arquivo apagado.** "PENDENCIAS do Plano 6" vira "pendência do Plano 6" em `PaginaConfiguracoes.tsx:39,187`, `configuracoes.test.tsx:57,421,492` e `api-falsa.ts:72`.

**Aceite** (na worktree `b`):
```
cd web && npm ci --no-audit --no-fund && npm run typecheck && npm run lint && npm test && npm run build && cd ..
STILLON_EXIGIR_DIST=1 go test -count=1 ./internal/webui/
go test -count=1 -run 'TestCadaPrefixoCaiNaCadeiaCerta|TestAMutacaoDaAPIContinuaExigindoOCabecalho' ./internal/httpapi/
git diff <T1> -- web/package-lock.json | grep -c '^[-+] .*"name"'     # 4 (2 linhas saem, 2 entram)
git grep -nIi vigilume -- web internal/webui internal/httpapi/painel_integration_test.go   # vazio
git grep -n 'PENDENCIAS' -- web                                         # vazio
git diff --name-only <T1>.. | sort                                      # ⊆ lista B
```
O Playwright roda aqui só como aviso (`cd web && npx playwright test`): o backend desta worktree ainda usa `vigilume_refresh`, e a asserção do cookie seria vacuamente verde. O gate dele é na T7.

**Sabotagens:**
- **S-b1:** header do `http.ts` = `'sabotagem'` → vermelho em `npm test -- src/lib/api` (a API falsa responde 403).
- **S-b2:** `cspNonce: 'SABOTAGEM_NONCE'`, rode `npm run build` → vermelho em `STILLON_EXIGIR_DIST=1 go test -count=1 -run TestTodoArquivoDoDistTemTipo ./internal/webui/` (o dist sem o marcador). Desfaça e rode `npm run build` de novo.
- **S-b3:** "Entrar no Sabotagem" em `PaginaLogin.tsx` → vermelho em `npm test -- src/features/login`.
- **S-b4:** `CHAVE = 'sabotagem.tema'` só no `tema.tsx` → rode `npm test`. Se nada ficar vermelho, anote como lacuna. A revisão confere as duas chaves por grep.

**Commit:** `refactor(web): StillOn no painel — textos, tema, sessão, cabeçalho, marcador CSP e e2e`

## T4 (área c): deploy, compose, workflows, config e CI

**Files:**
- Modify: os 31 arquivos da lista **C** do Apêndice A que ficam no lugar
- Rename + Modify:
  - `deploy/vps/vigilume-entrada` → `deploy/vps/stillon-entrada`
  - `deploy/vps/sudoers-vigilume-entrada` → `deploy/vps/sudoers-stillon-entrada`
  - `deploy/vps/vigilume-vigia.service` → `deploy/vps/stillon-vigia.service`
  - `deploy/vps/vigilume-vigia.timer` → `deploy/vps/stillon-vigia.timer`
- Modify, sem o nome: `deploy/ci/checar-workflows.sh`, só o comentário das linhas 108-113. Hoje ele diz que `docs/superpowers/` tem "specs, planos e pendências de planos já feitos". Depois do corte, sobram a spec do sistema, o contrato, o roadmap e as pendências correntes, que citam o Fly como história. A exclusão `':(exclude)docs/superpowers/**'` **fica**.

**O que fazer:**
1. **`compose.yaml`.**
   - Comentários, `name: stillon`, `DATABASE_URL: postgres://stillon_app:stillon_app@db:5432/stillon?sslmode=disable` e `STILLON_PORT`.
   - As três chaves novas (base64 da tabela, com comentário do texto de 32 bytes).
   - `10-stillon.sql`, `pg_isready … -U stillon_app -d stillon`, as imagens `ghcr.io/bipoxdxd/stillon:${STILLON_TAG:-latest}` e `-tools`, e `DEMO_EMAIL`/`DEMO_PASSWORD`.
   - O healthcheck `["CMD", "/stillon", "healthcheck"]`.
2. **`internal/config/config.go`.**
   - `chavesDeDemo` com as 3 novas (texto) seguidas das 3 antigas, via um helper `demoAntiga(b64 string) []byte`, que faz `base64.StdEncoding.DecodeString` e `panic` no erro.
   - Reescreva o comentário para dizer que as antigas são do nome anterior do projeto, que continuam recusadas (um `.env` copiado de um compose antigo) e que o `.gitleaks.toml` libera as seis.
   - Não escreva o texto antigo.
3. **`internal/config/*_test.go`.**
   - `compose_test.go`: `"\nname: stillon\n"` e o comentário `STILLON_PORT`.
   - `seed_test.go`: o falso chamado `stillon`, `STILLON_BIN=`, o demo e os domínios.
   - `repositorio_test.go`: `stillon-backup.key`, `deploy/backup/stillon-backup.key`, `stillon.dump` e `stillon-20260925.dump.age`.
   - `config_test.go`: os domínios e a URL do banco.
   - Novo, também em `config_test.go`: `TestLoadRejectsEveryDemoKeyOverHTTPS`. Para **cada** índice de `chavesDeDemo` (0..5), com `PUBLIC_BASE_URL=https://stillon.example` e a chave em `MASTER_KEY`, o `Load` recusa. Mais `len(chavesDeDemo) == 6`.
4. **`cmd/stillon/ops_test.go`.** A regex `` `"/stillon", "healthcheck"\]…` `` (linha 215) e o comentário da linha 175.
5. **`.gitignore`.** `/stillon` na linha 1 e o comentário das linhas 20-21 (`stillon-backup.key`, `stillon.dump`, `stillon-AAAAMMDD.dump.age`). Os globs ficam.
6. **`.dockerignore`.** `stillon` e o comentário `go build ./cmd/stillon`.
7. **`.gitleaks.toml`.**
   - Some as 3 regex novas, ancoradas (`'''^<base64>$'''`), às 3 antigas.
   - Reescreva o comentário: as seis são as chaves de demo do compose, as três de hoje e as três do nome anterior, que seguem no histórico que o CI varre.
   - Troque a referência ao arquivo da spec do Plano 7, que será apagado, por "D13 e D14 do Plano 7".
8. **`Dockerfile` e `Makefile`**, conforme a tabela:
   - Dockerfile: `/out/stillon ./cmd/stillon`, `/stillon` nos dois `COPY` e nos dois `ENTRYPOINT`, e os comentários.
   - Makefile: `bin/stillon ./cmd/stillon`, `./bin/stillon`, `stillon:$(VERSION) stillon:dev`, `STILLON_PORT` e o demo.
9. **Banco e backup.**
   - `deploy/compose/initdb.sql`: `CREATE ROLE stillon_app LOGIN PASSWORD 'stillon_app'` e `CREATE DATABASE stillon OWNER stillon_app`.
   - `deploy/backup/backup.sh`: `-d stillon` duas vezes e os comentários.
   - `deploy/backup/verificar.sql`: `stillon_app` duas vezes.
10. **`deploy/compose/seed.sh`.** `STILLON_BIN`, `STILLON=${STILLON_BIN:-/stillon}`, `"$STILLON"` nas 3 chamadas, `X-Requested-With: stillon` e a regex `\(stillon_refresh=[^;]*\)`.
11. **`deploy/ci/checar-compose.sh` e `checar-imagem.sh`.**
    - checar-compose.sh: `STILLON_PORT`/`STILLON_TAG` (comentário e código), o demo, `-d stillon`, o header e `stillon_app`.
    - checar-imagem.sh: `docker cp …:/stillon "$tmp/stillon"`, o grep no binário e as mensagens `stillon version`.
12. **`deploy/vps/*`.** O conteúdo inteiro conforme a tabela da VPS:
    - `DIR=/opt/stillon` e `IMAGEM=stillon`;
    - `STILLON_*`;
    - a rede `stillon` (a chave `networks:` do topo e a de cada serviço);
    - `name: stillon`;
    - `image: stillon:${STILLON_SHA:-atual}`;
    - `10-stillon.sh`;
    - os papéis;
    - os temporários e dumps;
    - `LoadCredential=stillon-env:/opt/stillon/.env`, `ExecStart=/usr/local/sbin/stillon-vigia`, `Unit=stillon-vigia.service` e `Description=StillOn: …`;
    - o sudoers com `/usr/local/sbin/stillon-entrada`;
    - `Caddyfile` e `env.exemplo` (o exemplo de domínio vira `stillon.tech`).
13. **`deploy/ci/*`.**
    - `checar-vigia.sh`: os caminhos das unidades, `stillon-env`, `STILLON_DOMINIO=stillon.example` e as URLs `https://stillon.example/healthz`.
    - `checar-vps.sh`:
      - `DOMINIO=stillon.example`, `PROJETO=`/`REPO_TESTE=stillon-teste-vps` e `EMAIL=dono@stillon.example`;
      - as linhas de `trocar 'DIR=/opt/stillon'` e `'IMAGEM=stillon'`;
      - `.networks.stillon`, `.services.*.networks.stillon` e as envs;
      - `"stillon:atual"`;
      - `LABEL stillon.teste=a..d`;
      - `/stillon user create` e `-d stillon`;
      - os papéis esperados `"stillon_app:000 stillon_backup:011"`;
      - `stillon-restauro-`, `stillon-backup.*`, os caminhos do sudoers e da entrada, e o header.
    - `testar-vps.sh`: `stillon-20260925.sql.age`.
    - `vps.sh`: o comentário.
    - `vps/compose.teste.yaml`: `name: stillon-teste-vps` e `image: stillon-teste-vps:${STILLON_SHA:-atual}`.
14. **Workflows.**
    - `ci.yml`:
      - `IMAGEM: ghcr.io/bipoxdxd/stillon`;
      - o comentário "Nome repetido à mão", atualizado para a lista real: `ci.yml` `IMAGEM`, `deploy.yml` `ORIGEM` e `compose.yaml` (duas vezes). A tag local da VPS `stillon` fica no `stillon-entrada` e no `compose.prod.yaml`;
      - `STILLON_EXIGIR_DIST: '1'` e o comentário;
      - `STILLON_TAG: ci`;
      - os comentários "a imagem do StillOn" e `` `/stillon user …` ``;
      - `E2E_EMAIL: demo@stillon.local` e `E2E_PASSWORD: demo-stillon-2026`;
      - os comentários `stillon-entrada`;
      - o guarda `if: github.event_name == 'push' && github.repository == 'BipoXDXD/stillon'`.
    - `deploy.yml`: `ORIGEM: ghcr.io/bipoxdxd/stillon`.
    - `backup.yml`: os comentários, `"$RUNNER_TEMP/stillon-$(date -u +%Y%m%d).dump.age"`, `name: stillon-backup-${{ github.run_id }}` e `path: ${{ runner.temp }}/stillon-*.dump.age`.

**Aceite** (na worktree `c`; o `cmd/stillon` já existe desde a T1):
```
for s in 'demo-stillon-MASTER_KEY-naouse!!' 'demo-stillon-JWT_SECRET-naouse!!' 'demo-stillon-IP_HASH_KEY-naouse!'; do test "$(printf %s "$s" | wc -c)" = 32; done
go test -race -count=1 ./internal/config/ ./cmd/...
make build && ./bin/stillon version
docker compose config -q
sed 's/^\([A-Z_][A-Z0-9_]*\)=$/\1=x/' deploy/vps/env.exemplo > /tmp/env.teste && docker compose --env-file /tmp/env.teste -f deploy/vps/compose.prod.yaml config -q
docker build --build-arg VERSION=teste -t ghcr.io/bipoxdxd/stillon:ci . && docker build --target tools --build-arg VERSION=teste -t ghcr.io/bipoxdxd/stillon:ci-tools .
sh deploy/ci/checar-imagem.sh ghcr.io/bipoxdxd/stillon:ci teste
actionlint && sh deploy/ci/checar-workflows.sh && sh deploy/ci/testar-vps.sh && bash deploy/ci/checar-vigia.sh
docker run --rm -v "$PWD:/mnt" -w /mnt koalaman/shellcheck:stable deploy/ci/*.sh
go run github.com/zricethezav/gitleaks/v8@v8.30.1 dir . --config .gitleaks.toml --redact --no-banner   # sem achados (árvore)
grep -c "github.repository == 'BipoXDXD/stillon'" .github/workflows/ci.yml          # 1
grep -c "STILLON_EXIGIR_DIST: '1'" .github/workflows/ci.yml                          # 1
git grep -nIi vigilume -- $(cat <lista C com os caminhos novos>) deploy/ci/checar-workflows.sh   # vazio
git ls-files | grep -i vigilume | grep -v '^docs/\|^web/\|^internal/'                # vazio
git diff --name-only -M <T1>.. | sort                                               # ⊆ lista C (caminhos novos) + checar-workflows.sh
```
Depois do commit, rode também `gitleaks git . --config .gitleaks.toml --redact --no-banner` (o histórico inteiro, como no CI).

O `checar-compose.sh` e o `checar-vps.sh` **não** são gate aqui:
- o `seed.sh` novo procura `stillon_refresh`, e o binário desta worktree ainda emite o cookie antigo;
- o `checar-vps.sh` precisa do image store do containerd.

Os dois rodam na T7.

**Sabotagens:**
- **S-c1:** o healthcheck do `compose.yaml` com `/sabotagem` → vermelho em `go test -count=1 -run TestHealthcheckDeadlinesNest ./cmd/stillon/`.
- **S-c2:** o `MASTER_KEY` do `compose.yaml` com o base64 de outra chave de 32 bytes → vermelho em `-run TestComposeUsesTheDemoKeys ./internal/config/`.
- **S-c3:** tire as 3 antigas de `chavesDeDemo` → vermelho em `-run TestLoadRejectsEveryDemoKeyOverHTTPS ./internal/config/`.
- **S-c4:** tire a regex nova do `MASTER_KEY` do `.gitleaks.toml` → o `gitleaks dir .` acusa o `compose.yaml`.
- **S-c5:** `ExecStart=/usr/local/sbin/sabotagem` no `stillon-vigia.service` → vermelho em `bash deploy/ci/checar-vigia.sh`.
- **S-c6:** `name: sabotagem` no `compose.yaml` → vermelho em `-run TestComposeHeaderSaysItIsLocalOnly ./internal/config/`.

**Commit:** `refactor(deploy): StillOn no compose, na imagem, na VPS, nos workflows e na config`

## T5 (área d): docs — renomear o que fica, apagar o histórico

**Files:**
- Delete: os 33 arquivos da seção "Arquivos a apagar" (**não** apague `docs/superpowers/plans/2026-09-25-renomeacao-stillon.md`: ele é da T8)
- Rename + Modify: `docs/superpowers/specs/2026-09-22-vigilume-design.md` → `docs/superpowers/specs/2026-09-22-stillon-design.md`
- Modify:
  - `README.md`
  - `docs/deploy/vps.md`
  - `docs/superpowers/PENDENCIAS-PARA-O-PLANO-12.md`
  - `docs/superpowers/plans/2026-09-22-roadmap.md`
  - `docs/superpowers/specs/2026-09-24-planos-5-6-7-contrato.md`

A T5 recebe o `<BASE>` da T0.

**O que fazer:**
1. `git rm` dos 33 arquivos e `git mv` da spec.
2. **`README.md`.**
   - A linha 1 passa a ser **exatamente** `# StillOn (até 2026-09-25, Vigilume)`.
   - O resto, conforme a tabela:
     - o badge e o clone `BipoXDXD/stillon`, e `cd stillon`;
     - o demo `demo@stillon.local` / `demo-stillon-2026`;
     - o diagrama "binário stillon";
     - os links da spec, para o caminho novo, 3 vezes;
     - `cmd/stillon/main.go`;
     - `bin/stillon`;
     - `stillon user …`, 3 vezes;
     - `X-Requested-With: stillon`;
     - os cookies;
     - `[stillon-entrada](deploy/vps/stillon-entrada)`;
     - "o StillOn não".
   - Troque o link "e nos [planos](docs/superpowers/plans/)" (linha 258) pela seção nova **`## Histórico`**, com quatro pontos:
     - o projeto se chamava Vigilume até 2026-09-25;
     - os planos 1 a 11, as specs de cada plano, os relatórios de revisão e as pendências antigas foram apagados nessa data;
     - para lê-los, use `git show <BASE>:docs/superpowers/plans/<arquivo>` (e `git ls-tree -r --name-only <BASE> docs/superpowers` para a lista), com o `<BASE>` literal da T0;
     - os comentários do código citam decisões por identificador ("D32 do Plano 7", "spec do Plano 10, §4", "banco S1 da revisão"), que se resolvem nesses arquivos; "spec §N" sem plano é a spec do sistema, `docs/superpowers/specs/2026-09-22-stillon-design.md`.
3. **`docs/deploy/vps.md`.**
   - Sed genérico: todas as 234 ocorrências. Revise os nomes de arquivo e de chave SSH da tabela da VPS.
   - O nome do check no healthchecks.io passa a ser "StillOn".
   - A linha 9 cita a spec do Plano 11: troque por "(a spec do Plano 11, apagada em 2026-09-25; ver README, Histórico)".
   - A linha 1354 cita `PENDENCIAS-PARA-O-PLANO-11.md`: troque por "a tabela 'Fechadas no Plano 11' de `docs/superpowers/PENDENCIAS-PARA-O-PLANO-12.md`".
4. **`PENDENCIAS-PARA-O-PLANO-12.md`.**
   - As 10 ocorrências (`/opt/stillon`, `stillon-entrada`, `stillon-vigia.*`, `~/stillon-backup.key`, a rede `stillon`, "o StillOn").
   - A frase "os anteriores ficam como histórico" vira "os anteriores foram apagados em 2026-09-25 (README, Histórico)".
   - As referências à spec do Plano 11 e a `PENDENCIAS-PARA-O-PLANO-11.md` levam a mesma nota.
   - Acrescente a seção curta **"Renomeação para StillOn (2026-09-25)"**, com o que fica para o dono:
     - apagar o pacote GHCR `vigilume` órfão, com o nome escrito como "o pacote antigo do GHCR" para não citar o nome;
     - dar o nome StillOn ao check do healthchecks.io na criação;
     - o `RESEND_FROM` `StillOn <alertas@stillon.tech>`.
5. **`plans/2026-09-22-roadmap.md`.**
   - O título `# StillOn — Roteiro de Planos de Implementação`.
   - `**Spec:**` com o caminho novo.
   - A coluna "Arquivo" das linhas 1 a 11 vira "apagado (README, Histórico)".
   - O parágrafo de 2026-09-25 e o de 2026-09-24: tire os caminhos dos relatórios e das specs apagados. O contrato **fica** e segue citado.
   - Uma linha nova abaixo da tabela: "Em 2026-09-25, depois do Plano 11, o projeto passou a se chamar StillOn (plano de renomeação executado e apagado)."
6. **`specs/2026-09-24-planos-5-6-7-contrato.md`.** As 6 ocorrências. A tabela das linhas 10 a 12 (specs 5, 6 e 7) passa a dizer "apagada em 2026-09-25 (README, Histórico)".
7. **`specs/2026-09-22-stillon-design.md`.** As 7 ocorrências. O resto do conteúdo **não** muda: é a spec do sistema.

**Aceite** (na worktree `d`):
```
git grep -nIi vigilume -- README.md docs ':(exclude)docs/superpowers/plans/2026-09-25-renomeacao-stillon.md'   # só README.md:1
test "$(sed -n 1p README.md)" = "# StillOn (até 2026-09-25, Vigilume)"
git ls-files docs | sort     # exatamente: docs/deploy/vps.md, PENDENCIAS-PARA-O-PLANO-12.md, plans/2026-09-22-roadmap.md,
                             # plans/2026-09-25-renomeacao-stillon.md, specs/2026-09-22-stillon-design.md, specs/2026-09-24-planos-5-6-7-contrato.md
for p in $(cat <lista de apagados>); do case $p in */revisao-2026-09-25/*) pat=revisao-2026-09-25 ;; *) pat=$(basename "$p") ;; esac
  git grep -nF "$pat" -- README.md docs ':(exclude)docs/superpowers/plans/2026-09-25-renomeacao-stillon.md'; done   # vazio
grep -n 'BASE\|<BASE>' README.md    # o SHA literal, sem marcador por resolver
```

Checagem de links, válida depois da T7 (aqui, os caminhos da área c ainda não existem: confira-os contra a tabela):
```
for f in README.md docs/deploy/vps.md docs/superpowers/PENDENCIAS-PARA-O-PLANO-12.md docs/superpowers/plans/2026-09-22-roadmap.md docs/superpowers/specs/*.md; do
  grep -oE '\]\([^)#[:space:]]+' "$f" | sed 's/^](//' | grep -vE '^(https?:|mailto:|<)' | while read -r p; do
    [ -e "$p" ] || [ -e "$(dirname "$f")/$p" ] || echo "QUEBRADO: $f -> $p"; done
  grep -oE '`(docs|deploy|cmd|internal|web|\.github)/[^` ]+`' "$f" | tr -d '`' | sed 's/[:,].*$//' | while read -r p; do
    [ -e "$p" ] || echo "CAMINHO: $f -> $p"; done
done
```
Confira cada `CAMINHO:` à mão: alguns são caminhos da VPS ou globs, e isso não é erro.

**Sabotagem:**
- **S-d1:** plante `[x](docs/superpowers/plans/2026-09-24-painel-react.md)` no README → o laço acima imprime `QUEBRADO`. Desfaça.

**Commit:** `docs: StillOn no README, no runbook e nas pendências; planos, specs e relatórios executados apagados`

---

**Acréscimo da T0 (controlador), vindo da re-revisão final do Plano 11 — corrigir no `docs/deploy/vps.md` junto com a renomeação:**
1. Citar a linha que a entrada sempre grava por ÚLTIMO no `entrada.log`: `== <UTC> fim: <pedido>: código N (<sentido>)` (e a de `início`), e mandar o dono ler essa linha (não "a linha `…-entrada:` no fim") depois de uma queda de SSH.
2. Na tabela de códigos, o código 0 (`deploy de <sha> saudável`) não tem o prefixo `…-entrada:`; ajustar a coluna.
3. O `entrada.log` é 0644: lê-lo SEM `sudo` em todo o runbook.
4. O teto do log guarda a rodada anterior em `entrada.log.1`: citá-lo onde o runbook fala do teto, e no passo 2 de "A chave de deploy vazou" comparar também o `entrada.log.1`.
Confira cada ponto contra `deploy/vps/stillon-entrada` (função `fim`/`registrar`).

## T6: revisão por área (4 revisores em paralelo)

Cada revisor lê o diff da sua área (`git diff -M <T1>..renomear-stillon-{x}`), a tabela de mapeamento e o relatório do implementador (gates e sabotagens com a saída). Achados voltam ao implementador da área, na mesma worktree, antes da T7.

**Todas as áreas:**
- o diff só toca arquivos da lista;
- nenhum valor foge da tabela;
- nada de `vigia`/`sentinela`/`Fly` alterado;
- as sabotagens foram vermelhas pela razão certa e foram desfeitas (`git status` limpo).

**Área a:**
- as quatro chaves de trava iguais em query, migration e teste;
- o `db/*.sql.go` é igual ao que `sqlc generate` produz;
- o `rotulos_test.go` confere contra o literal, e não contra a constante;
- os domínios dos testes batem entre `service_test.go` e `dispatcher_test.go`;
- nenhum `stillon.dev`.

**Área b:**
- `tema.tsx` e `tema.js` com a mesma chave;
- `http.ts` e `api-falsa.ts` com o mesmo header;
- `Marcador` e `cspNonce` iguais;
- `STILLON_EXIGIR_DIST` no `webui_test.go`;
- o diff do lock tem só o `name`;
- as asserções do e2e continuam com o sentido original.

**Área c:**
- as chaves com 32 bytes e na ordem nova e depois as antigas;
- o allowlist com as 6 regex;
- o guarda do publish;
- a rede `stillon` em **todos** os serviços do `compose.prod.yaml`;
- o `stillon-entrada` e o `checar-vps.sh` coerentes: `trocar 'DIR=/opt/stillon'` e `'IMAGEM=stillon'` casam uma linha cada;
- sudoers, unidades e caminhos na mesma grafia.

**Área d:**
- a linha 1 do README;
- a seção Histórico com o SHA;
- nenhum link para arquivo apagado;
- as pendências abertas do `PENDENCIAS-PARA-O-PLANO-12.md` estão intactas, só com o nome trocado.

## T7: integração e gate cruzado (controlador)

**Files:** nenhum novo. Só cherry-picks.

- [ ] **Passo 1.** Integre, no worktree principal, na branch `renomear-stillon`:
  ```
  for x in a b c d; do git cherry-pick $(git rev-list --reverse <T1>..renomear-stillon-$x); done
  ```
  Um conflito para tudo (veja "Execução e ordem").
- [ ] **Passo 2.** Confira os pares entre áreas (cada linha tem de achar exatamente o valor):
  ```
  grep -F "cspNonce: 'STILLON_CSP_NONCE'" web/vite.config.ts && grep -F 'Marcador = "STILLON_CSP_NONCE"' internal/webui/webui.go
  grep -F "STILLON_EXIGIR_DIST: '1'" .github/workflows/ci.yml && grep -F 'STILLON_EXIGIR_DIST' internal/webui/webui_test.go
  grep -F '"stillon_refresh"' internal/httpapi/authhandlers.go && grep -F 'stillon_refresh=' deploy/compose/seed.sh && grep -F "'stillon_refresh'" web/e2e/fluxo.spec.ts
  grep -F '"/stillon", "healthcheck"' compose.yaml deploy/vps/compose.prod.yaml
  grep -F "'./cmd/stillon'" web/e2e/pilha.ts && grep -F './cmd/stillon' Makefile Dockerfile
  ```
- [ ] **Passo 3.** Gate (tudo verde):
  ```
  test -z "$(gofmt -l .)" && go build ./... && go vet ./... && staticcheck ./... && sqlc diff
  go test -race -count=1 ./...
  (cd web && npm ci --no-audit --no-fund && npm run typecheck && npm run lint && npm test && npm run build)
  STILLON_EXIGIR_DIST=1 go test -count=1 ./internal/webui/
  (cd web && npx playwright test)                     # modo isolado: pilha.ts com o binário e o init.sql novos
  docker compose -p vigilume down -v 2>/dev/null || true   # a pilha e o volume antigos, se existirem
  make e2e                                           # checar-compose.sh + Playwright contra o compose, com demo@stillon.local
  make down
  docker build --build-arg VERSION=teste -t ghcr.io/bipoxdxd/stillon:ci . && sh deploy/ci/checar-imagem.sh ghcr.io/bipoxdxd/stillon:ci teste
  actionlint && sh deploy/ci/checar-workflows.sh && sh deploy/ci/testar-vps.sh && bash deploy/ci/checar-vigia.sh
  docker run --rm -v "$PWD:/mnt" -w /mnt koalaman/shellcheck:stable deploy/ci/*.sh
  go run github.com/zricethezav/gitleaks/v8@v8.30.1 git . --config .gitleaks.toml --redact --no-banner
  ```
  O `bash deploy/ci/checar-vps.sh ghcr.io/bipoxdxd/stillon:ci` só roda com o Docker no image store do containerd (`docker info -f '{{.DriverStatus}}' | grep -q io.containerd.snapshotter.v1`). Sem ele, fica para o job `vps` do CI na T9: anote isso no PR, e não troque o store da máquina do dono sem perguntar.
- [ ] **Passo 4.** A checagem de links da T5 roda sem `QUEBRADO`. Os arquivos apagados não aparecem citados em lugar nenhum:
  ```
  for p in $(cat <lista de apagados>); do case $p in */revisao-2026-09-25/*) pat=revisao-2026-09-25 ;; *) pat=$(basename "$p") ;; esac
    git grep -nF "$pat" -- . ':(exclude)docs/superpowers/plans/2026-09-25-renomeacao-stillon.md'; done   # vazio
  git grep -n 'PENDENCIAS-PARA-O-PLANO-\([3-9]\|1[01]\)\b\|PENDENCIAS do Plano' -- . ':(exclude)docs/superpowers/plans/2026-09-25-renomeacao-stillon.md'   # vazio
  ```
- [ ] **Passo 5.** Sem commit novo, a não ser correções de integração, cada uma atribuída à área dona e revisada.

## T8: checagem do nome antigo no CI

**Files:**
- Delete: `docs/superpowers/plans/2026-09-25-renomeacao-stillon.md` (plano executado; o original segue em `.superpowers/sdd/renomeacao/plano.md`)
- Create: `deploy/ci/checar-nome.sh`
- Modify: `.github/workflows/ci.yml` (um passo no job `workflows`, logo depois de "checagens dos workflows")

- [ ] **Passo 1.** `git rm docs/superpowers/plans/2026-09-25-renomeacao-stillon.md`.
- [ ] **Passo 2.** Crie `deploy/ci/checar-nome.sh`:
  ```sh
  #!/bin/sh
  # O nome anterior do projeto não volta ao repositório (renomeação para
  # StillOn, 2026-09-25). O job "workflows" do CI roda; localmente, da raiz:
  #
  #   sh deploy/ci/checar-nome.sh
  #
  # Olha o conteúdo e os caminhos de todo arquivo versionado, e dos não
  # versionados que o .gitignore não esconde, sem exceção de pasta. A única
  # ocorrência aceita é a linha 1 do README, que conta o nome anterior. O nome
  # é montado em partes para este arquivo não ser ele mesmo uma exceção.
  set -eu

  RAIZ=$(cd "$(dirname "$0")/../.." && pwd)
  cd "$RAIZ"
  ANTIGO=$(printf '%s%s' vigi lume)
  TITULO="# StillOn (até 2026-09-25, $(printf 'V%s' "${ANTIGO#v}"))"
  falhas=0
  falhar() { echo "FALHOU: $*" >&2; falhas=$((falhas + 1)); }

  # 1. Conteúdo, em qualquer caixa; -I pula binários.
  achados=$(git grep --untracked -nIi -e "$ANTIGO" -- . || true)
  fora=$(printf '%s\n' "$achados" | grep -v '^README\.md:1:' | grep . || true)
  if [ -n "$fora" ]; then
      printf '%s\n' "$fora" >&2
      falhar "o nome anterior aparece acima"
  fi

  # 2. A exceção é exatamente a linha 1 do README.
  [ "$(sed -n 1p README.md)" = "$TITULO" ] || falhar "README.md:1 não é '$TITULO'"

  # 3. Caminhos (arquivos e pastas).
  if git ls-files --cached --others --exclude-standard | grep -i -e "$ANTIGO" >&2; then
      falhar "caminho com o nome anterior (acima)"
  fi

  if [ "$falhas" -gt 0 ]; then
      echo "$falhas falha(s): o nome anterior voltou" >&2
      exit 1
  fi
  echo "nome: tudo certo"
  ```
- [ ] **Passo 3.** No `ci.yml`, job `workflows`, depois do passo `checagens dos workflows`:
  ```yaml
      - name: nome anterior do projeto
        run: sh deploy/ci/checar-nome.sh
  ```
- [ ] **Passo 4.** Aceite:
  ```
  sh deploy/ci/checar-nome.sh                          # "nome: tudo certo", saída 0
  actionlint
  docker run --rm -v "$PWD:/mnt" -w /mnt koalaman/shellcheck:stable deploy/ci/*.sh
  grep -c 'checar-nome.sh' .github/workflows/ci.yml   # 1
  ```
- [ ] **Passo 5.** Sabotagens. Cada uma tem de sair com **1** e apontar o lugar, e depois é desfeita. Para escrever o nome sem digitá-lo, use `N=$(printf '%s%s' Vigi lume)`:
  - **S-n1:** `echo "// $N" >> internal/crypto/secret.go` (versionado, código) → vermelho. Desfaça com `git checkout -- internal/crypto/secret.go`.
  - **S-n2:** `echo "$N" > sabotagem.txt` (não versionado, fora do `.gitignore`) → vermelho. Desfaça com `rm sabotagem.txt`.
  - **S-n3:** `echo "$N" >> docs/superpowers/plans/2026-09-22-roadmap.md` (a pasta que antes era excluída) → vermelho. Desfaça com `git checkout -- …`.
  - **S-n4:** uma segunda linha no README com `$N` → vermelho. Troque a data da linha 1 → vermelho. Desfaça.
  - **S-n5:** `git mv docs/deploy/vps.md "docs/deploy/vps-$(printf '%s%s' vigi lume).md"` → vermelho (caminho). Desfaça com `git mv` de volta.
  - **S-n6:** `echo "$(printf '%s%s' VIGI LUME)_X=1" >> deploy/vps/env.exemplo` (caixa alta) → vermelho. Desfaça.
- [ ] **Passo 6.** Commit: `ci: checagem do nome anterior do projeto no repositório inteiro; plano de renomeação executado apagado`.

## T9: gates finais, revisão final e PR

- [ ] **Passo 1.** Repita o Passo 3 da T7 inteiro, mais `sh deploy/ci/checar-nome.sh`.
- [ ] **Passo 2.** Revisão final: um revisor lê `git diff -M <BASE>..renomear-stillon` com o Review Focus acima e confere o seguinte:
  - `git diff --stat -M <BASE>..` mostra 33 arquivos apagados e 14 renomeados (a cópia do plano nasce na T0 e morre na T8, sem saldo);
  - nenhum arquivo fora do Apêndice A, das listas de renomeados e apagados, dos 2 novos (`internal/auth/rotulos_test.go`, `deploy/ci/checar-nome.sh`) e dos 2 editados sem o nome (`internal/httpapi/publicpage.go`, `deploy/ci/checar-workflows.sh`) mudou além das linhas de import da T1;
  - as sabotagens de T2, T3, T4, T5 e T8 estão registradas.
- [ ] **Passo 3.** Remova as worktrees das áreas: `for x in a b c d; do git worktree remove .superpowers/renomeacao-wt/$x; git branch -D renomear-stillon-$x; done`.
- [ ] **Passo 4.** Push e PR:
  ```
  git push -u origin renomear-stillon
  gh pr create --title "StillOn: renomeação do Vigilume" --body-file <corpo>
  ```
  O corpo do PR diz quatro coisas:
  - rode `npm run build` antes de `make run` (um `dist` local velho tem o marcador antigo, e o CSP bloqueia o painel);
  - rode `docker compose -p vigilume down -v` para largar o volume antigo;
  - o `compose.yaml` aponta para `ghcr.io/bipoxdxd/stillon:latest`, que só existe depois do primeiro publish;
  - **o repositório tem de ser renomeado antes do merge**.

  Termine com `🤖 Generated with [Claude Code](https://claude.com/claude-code)`.
- [ ] **Passo 5.** CI do PR todo verde. O `publish` é pulado em PR, e isso é normal. **Não faça merge**: espere o P2 do dono.

---

## Passos do dono (externos; não são tarefa de agente)

| # | Quando | O quê |
|---|---|---|
| P1 | Antes da T0 | Plano 11 integrado. Fechar o GoLand (ele regrava listas ao fechar). Conferir que os spikes do Plano 11 não têm trabalho fora do `main` e removê-los (`git worktree remove` ×3, `git worktree prune`). **Não** provisionar a VPS até o P7. |
| P2 | Com o PR verde, **antes do merge** | GitHub → Settings → General → Repository name: `vigilume` → `stillon`. Localmente: `git remote set-url origin https://github.com/BipoXDXD/stillon.git`. Conferir com `gh repo view BipoXDXD/stillon`. |
| P3 | Merge (o controlador faz, depois do P2) | Depois do merge, conferir que o job `publish` **rodou** e não foi pulado: `gh run list --workflow ci.yml --branch main --limit 1` e `gh run view <id> --json jobs --jq '.jobs[] \| select(.name \| test("publish")) \| .conclusion'` = `success`. Se foi pulado, o repo não estava renomeado: renomear e rodar de novo. |
| P4 | Depois do publish | GitHub → Packages: o pacote novo `stillon` está **privado** e ligado ao repo. O pacote antigo fica órfão; apagar agora ou depois. |
| P5 | Depois do merge (o controlador faz com o dono, numa sessão aberta em `~`) | Remover qualquer worktree que sobrou em `.superpowers/`. Mover `~/Projetos/Go/Vigilume` → `~/Projetos/Go/StillOn`. Mover `~/.claude/projects/-home-bipo-Projetos-Go-Vigilume/memory/` → `~/.claude/projects/-home-bipo-Projetos-Go-StillOn/memory/`. Atualizar o `MEMORY.md` global e `~/.claude/rules/projetos-separados.md`, com o nome e o caminho novos e a regra **mantida**. `docker compose -p vigilume down -v` e `docker image rm` das tags antigas. Apagar `~/Projetos/Go/vigilume-wt/` (vazia). Reabrir o GoLand na pasta nova e recriar as run configs com `cmd/stillon`. |
| P6 | Primeira sessão em `~/Projetos/Go/StillOn` | `go build ./... && go test ./...`, `sh deploy/ci/checar-nome.sh`, a memória carregou, `docker compose up --wait --build` e login com `demo@stillon.local` / `demo-stillon-2026`. |
| P7 | Só depois do P6 | Guiar a VPS pelo `docs/deploy/vps.md` já renomeado: `/opt/stillon`, `stillon-entrada`, `stillon_app`, o check do healthchecks.io chamado "StillOn", o DNS de `stillon.tech` e `RESEND_FROM=StillOn <alertas@stillon.tech>`. Se a VPS tiver sido mexida com os nomes antigos: `docker compose down -v` lá e apagar `/opt/vigilume`, o sudoers e as unidades antigas. |

## Checklist do controlador

- **Depois da T0:**
  - o `BASE` está gravado;
  - o inventário bate com o Apêndice A (243 arquivos);
  - o working tree está limpo;
  - a cópia do plano está commitada.
- **Depois da T1:**
  - 131 `.go` com `BipoXDXD/stillon` e nenhum com o antigo;
  - `cmd/stillon` com 9 arquivos;
  - `go test -race ./...` verde;
  - as 4 worktrees foram criadas do commit da T1.
- **Antes de despachar as áreas:** `cat A B C D | sort | uniq -d` vazio. Cada agente recebe a sua lista, a tabela de mapeamento, os comandos de aceite e as sabotagens, e nada de outra área.
- **Ao receber cada área:**
  - `git diff --name-only -M <T1>..renomear-stillon-x` ⊆ a lista mais os novos declarados;
  - o grep da área está vazio;
  - as sabotagens vêm com a saída vermelha e o `git status` limpo depois;
  - os gates vêm com a saída real, e não com um resumo.
- **Depois da T6:** todo achado corrigido na worktree da área e revisto.
- **Depois da T7:**
  - cherry-picks sem conflito;
  - os pares do Passo 2 casam;
  - o gate está verde;
  - o `checar-vps.sh` rodou, ou ficou anotado para o CI.
- **Depois da T8:** as 6 sabotagens foram vermelhas, e o script está verde no estado final.
- **Na T9:**
  - o PR está verde;
  - **esperar o P2** antes do merge;
  - depois do merge, o P3 (o publish rodou);
  - só então o P5.

---

## Arquivos a apagar (T5: 33; T8: 1)

**Planos executados (11):**
- `docs/superpowers/plans/2026-09-22-fundacao-e-autenticacao.md`
- `docs/superpowers/plans/2026-09-24-alvos-e-sondador.md`
- `docs/superpowers/plans/2026-09-24-api-do-painel.md`
- `docs/superpowers/plans/2026-09-24-empacotamento-e-deploy.md`
- `docs/superpowers/plans/2026-09-24-incidentes-e-notificacoes.md`
- `docs/superpowers/plans/2026-09-24-painel-react.md`
- `docs/superpowers/plans/2026-09-24-statuspage-e-retencao.md`
- `docs/superpowers/plans/2026-09-25-endurecimento.md`
- `docs/superpowers/plans/2026-09-25-hospedagem.md`
- `docs/superpowers/plans/2026-09-25-operacao.md`
- `docs/superpowers/plans/2026-09-25-revisao.md`

**Specs de planos executados (7):**
- `docs/superpowers/specs/2026-09-24-plano-5-api-do-painel-design.md`
- `docs/superpowers/specs/2026-09-24-plano-6-painel-react-design.md`
- `docs/superpowers/specs/2026-09-24-plano-7-empacotamento-e-deploy-design.md`
- `docs/superpowers/specs/2026-09-25-plano-8-endurecimento-design.md`
- `docs/superpowers/specs/2026-09-25-plano-9-operacao-design.md`
- `docs/superpowers/specs/2026-09-25-plano-10-revisao-design.md`
- `docs/superpowers/specs/2026-09-25-plano-11-hospedagem-design.md`

**Pendências antigas (8):**
- `docs/superpowers/PENDENCIAS-PARA-O-PLANO-3.md`
- `docs/superpowers/PENDENCIAS-PARA-O-PLANO-4.md`
- `docs/superpowers/PENDENCIAS-PARA-O-PLANO-5.md`
- `docs/superpowers/PENDENCIAS-PARA-O-PLANO-6.md`
- `docs/superpowers/PENDENCIAS-PARA-O-PLANO-8.md`
- `docs/superpowers/PENDENCIAS-PARA-O-PLANO-9.md`
- `docs/superpowers/PENDENCIAS-PARA-O-PLANO-10.md`
- `docs/superpowers/PENDENCIAS-PARA-O-PLANO-11.md`

**Relatórios resolvidos (7):**
- `docs/superpowers/reports/2026-09-22-cobertura-plano-1.md`
- `docs/superpowers/reports/2026-09-22-revisao-das-rulings.md`
- `docs/superpowers/revisao-2026-09-25/auth.md`
- `docs/superpowers/revisao-2026-09-25/banco-e-ci.md`
- `docs/superpowers/revisao-2026-09-25/code-review-pr15.md`
- `docs/superpowers/revisao-2026-09-25/http-e-painel.md`
- `docs/superpowers/revisao-2026-09-25/sondador.md`

**Na T8 (1):**
- `docs/superpowers/plans/2026-09-25-renomeacao-stillon.md` (cópia deste plano, criada na T0)

## Arquivos que ficam

**Renomeados (velho → novo):**
- `cmd/vigilume/{incident_e2e_test,main,main_test,ops,ops_test,reset2fa_test,statuspage_e2e_test,user,user_test}.go` → `cmd/stillon/…` (T1)
- `deploy/vps/vigilume-entrada` → `deploy/vps/stillon-entrada` (T4)
- `deploy/vps/sudoers-vigilume-entrada` → `deploy/vps/sudoers-stillon-entrada` (T4)
- `deploy/vps/vigilume-vigia.service` → `deploy/vps/stillon-vigia.service` (T4)
- `deploy/vps/vigilume-vigia.timer` → `deploy/vps/stillon-vigia.timer` (T4)
- `docs/superpowers/specs/2026-09-22-vigilume-design.md` → `docs/superpowers/specs/2026-09-22-stillon-design.md` (T5)

**Docs que ficam no lugar, com o conteúdo renomeado (T5):**
- `README.md`
- `docs/deploy/vps.md`
- `docs/superpowers/PENDENCIAS-PARA-O-PLANO-12.md`
- `docs/superpowers/plans/2026-09-22-roadmap.md`
- `docs/superpowers/specs/2026-09-24-planos-5-6-7-contrato.md` (os comentários do código citam o "contrato dos Planos 5–7": `accounthandlers.go`, `routes.go`, `targethandlers.go`, `apierr/errors.go`)

---

## Desvios do levantamento

| # | Levantamento | Este plano | Por quê |
|---|---|---|---|
| 1 | §1.15: docs históricos **ficam**, e a spec fica com o nome antigo (opção A) | Os históricos são **apagados**. A spec do sistema, o contrato, o roadmap e a `PENDENCIAS-12` ficam e são renomeados. A spec vai para `…-stillon-design.md` (opção B) | Decisão do dono |
| 2 | §1.18: o grep de aceite exclui `docs/superpowers/{plans,specs,reports,revisao}` e as pendências antigas | A checagem vale para o repositório **inteiro**, com uma exceção só (README:1) e sem exclusão de pasta | Decisão do dono |
| 3 | §1.9 e T9: exceções para o allowlist do gitleaks, as `chavesDeDemo` antigas e o `.gitignore` antigo | Nenhuma dessas é exceção. O allowlist e as `chavesDeDemo` antigas estão em base64, que não contém a marca. Os comentários são reescritos. O `.gitignore` já cobre os nomes antigos pelos globs `*.key`, `*.dump` e `*.age` | Menos exceções; o valor recusado é o mesmo |
| 4 | §1.14: `vigilume.dev` → `stillon.tech` ou `stillon.example`; `.exemplo.com` → `stillon.exemplo.com` | Todo domínio de teste vira `stillon.example` (incluindo `.exemplo.com(.br)`, `vigilume.teste` e o `e2e@…test`). O demo fica `demo@stillon.local` | Decisão do dono ("domínio de teste stillon.example"). `stillon.tech` só no runbook e no `env.exemplo` |
| 5 | §3: T2 a T7 em paralelo, com 7 recortes por tema | 4 áreas por pasta. `internal/config/**` e `cmd/stillon/ops_test.go` vão para a área c, com o compose que eles leem. `internal/webui/**` e `painel_integration_test.go` vão para a área b, com o `vite.config.ts` | Decisão do dono (4 agentes). Cada teste fica na mesma área do arquivo que ele lê, para as áreas passarem sozinhas |
| 6 | §3 T1: a T1 muda também o healthcheck dos composes, Makefile, Dockerfile e `checar-imagem.sh` | A T1 só faz `go.mod`, imports e `git mv cmd`. O resto é da área c. `make build` e `docker build` ficam quebrados entre a T1 e a T7 | Um arquivo, um dono |
| 7 | §1.7: "a 00006 recria a função" `ensure_checks_partition` | A 00006:149 está em `drop_checks_partitions_before`, que toma a **mesma** trava. As duas são editadas | Conferido no arquivo |
| 8 | §1.8: teste opcional com o rótulo antigo sendo recusado | `TestRotulosFixados` confere os rótulos novos contra valores calculados no teste, sem o literal antigo | Não criar exceção à checagem; a sabotagem prova que o teste morde |
| 9 | §1.11: User-Agent opcionalmente `(+https://stillon.tech)` | Fica `StillOn/1.0 (+monitor de disponibilidade)` | Troca mínima; ver Dúvidas |
| 10 | §1.15: as `PENDENCIAS -10` e `-11` "se ainda valerem" | Apagadas. A `-12` declara que substitui a `-11` | Decisão do dono |
| 11 | §1.3 e §2: o controlador renomeia o remoto | O remoto é renomeado pelo dono (P2), **antes** do merge. O controlador só confere o `publish` (P3) | Decisão do dono |
| 12 | (não previsto) | Comentários que citam arquivo apagado (`PENDENCIAS-PARA-O-PLANO-5`, "PENDENCIAS do Plano 6", o caminho da spec do Plano 7 no `.gitleaks.toml`) são reescritos. Os identificadores "D<n> do Plano N" e "spec do Plano N §x" ficam, resolvidos pela seção Histórico do README | "Referências ajustadas" sem reescrever ~600 linhas de comentário |

## Apêndice A: listas por área (arquivos que ainda têm o nome depois da T1)

A T1 não tem lista fixa: é o conjunto de `git grep -l 'github.com/BipoXDXD/vigilume' -- '*.go'` (131), mais o `go.mod`. Os 88 `.go` que só têm o import saem da T1 sem o nome e não entram em nenhuma área.

**A: backend Go (55; mais `internal/httpapi/publicpage.go` e o novo `internal/auth/rotulos_test.go`)**
- `cmd/stillon/main.go`, `cmd/stillon/main_test.go`, `cmd/stillon/ops.go`, `cmd/stillon/reset2fa_test.go`, `cmd/stillon/user.go`, `cmd/stillon/user_test.go`
- `internal/account/account.go`, `internal/account/account_test.go`
- `internal/apierr/errors.go`
- `internal/auth/dispositivo.go`, `internal/auth/password.go`, `internal/auth/totp_prova_test.go`, `internal/auth/totp_rotation.go`, `internal/auth/totp_rotation_test.go`, `internal/auth/totp_test.go`
- `internal/crypto/secret.go`
- `internal/httpapi/authhandlers.go`, `internal/httpapi/auth_integration_test.go`, `internal/httpapi/dispositivo_integration_test.go`, `internal/httpapi/login_race_integration_test.go`, `internal/httpapi/middleware/middleware_test.go`, `internal/httpapi/middleware/requestid.go`, `internal/httpapi/plano5_routes_test.go`, `internal/httpapi/reset2fa_integration_test.go`, `internal/httpapi/server.go`, `internal/httpapi/sessions_integration_test.go`, `internal/httpapi/targets_integration_test.go`, `internal/httpapi/templates/status.html`, `internal/httpapi/totp_prova_integration_test.go`, `internal/httpapi/totp_troca_integration_test.go`, `internal/httpapi/verifyhandlers.go`
- `internal/notify/discord.go`, `internal/notify/dispatcher.go`, `internal/notify/dispatcher_test.go`, `internal/notify/message.go`, `internal/notify/message_test.go`, `internal/notify/notify.go`, `internal/notify/resend.go`, `internal/notify/senders_test.go`, `internal/notify/service_test.go`
- `internal/prober/probe.go`, `internal/prober/probe_test.go`, `internal/prober/safedial/safedial.go`, `internal/prober/safedial/transport_test.go`
- `internal/storage/db/sessions.sql.go`, `internal/storage/db/targets.sql.go`, `internal/storage/db/users.sql.go` (gerados)
- `internal/storage/migrations/00005_incidents_notifications.sql`, `internal/storage/migrations/00006_statuspage_rollups.sql`
- `internal/storage/notify_rls_test.go`, `internal/storage/queries/sessions.sql`, `internal/storage/queries/targets.sql`, `internal/storage/queries/users.sql`, `internal/storage/role_test.go`, `internal/storage/testdb/testdb.go`

**B: painel (31)**
- `internal/httpapi/painel_integration_test.go`, `internal/webui/webui.go`, `internal/webui/webui_test.go`
- `web/e2e/a11y.spec.ts`, `web/e2e/abas.spec.ts`, `web/e2e/fixtures.ts`, `web/e2e/fluxo.spec.ts`, `web/e2e/init.sql`, `web/e2e/pilha.ts`, `web/e2e/troca.spec.ts`
- `web/index.html`, `web/package.json`, `web/package-lock.json`, `web/playwright.config.ts`, `web/public/tema.js`, `web/vite.config.ts`
- `web/src/app/ExigirSessao.tsx`, `web/src/app/Layout.tsx`, `web/src/components/AvisoDeContexto.tsx`, `web/src/components/titulo.test.tsx`, `web/src/components/Titulo.tsx`
- `web/src/features/alvos/alvos.test.tsx`, `web/src/features/configuracoes/configuracoes.test.tsx`, `web/src/features/configuracoes/PaginaConfiguracoes.tsx`, `web/src/features/login/login.test.tsx`, `web/src/features/login/PaginaLogin.tsx`
- `web/src/lib/api/cliente.test.ts`, `web/src/lib/api/http.ts`, `web/src/lib/api/sessao.ts`, `web/src/lib/tema.tsx`, `web/src/test/api-falsa.ts`

**C: deploy, compose, workflows, config e CI (35; mais `deploy/ci/checar-workflows.sh`, só o comentário)**
- `cmd/stillon/ops_test.go`
- `internal/config/compose_test.go`, `internal/config/config.go`, `internal/config/config_test.go`, `internal/config/repositorio_test.go`, `internal/config/seed_test.go`
- `compose.yaml`, `Dockerfile`, `.dockerignore`, `Makefile`, `.gitignore`, `.gitleaks.toml`
- `.github/workflows/backup.yml`, `.github/workflows/ci.yml`, `.github/workflows/deploy.yml`
- `deploy/backup/backup.sh`, `deploy/backup/verificar.sql`
- `deploy/ci/checar-compose.sh`, `deploy/ci/checar-imagem.sh`, `deploy/ci/checar-vigia.sh`, `deploy/ci/checar-vps.sh`, `deploy/ci/testar-vps.sh`, `deploy/ci/vps.sh`, `deploy/ci/vps/compose.teste.yaml`
- `deploy/compose/initdb.sql`, `deploy/compose/seed.sh`
- `deploy/vps/Caddyfile`, `deploy/vps/compose.prod.yaml`, `deploy/vps/env.exemplo`, `deploy/vps/initdb.sh`, `deploy/vps/vigia.sh`, `deploy/vps/vigia.env.exemplo` (acrescentado na T0: nasceu na onda final do Plano 11)
- `deploy/vps/vigilume-entrada` → `stillon-entrada`, `deploy/vps/sudoers-vigilume-entrada` → `sudoers-stillon-entrada`, `deploy/vps/vigilume-vigia.service` → `stillon-vigia.service`, `deploy/vps/vigilume-vigia.timer` → `stillon-vigia.timer`

**D: docs (39 arquivos em `docs/` e o README; 33 apagados, 1 renomeado, 5 editados no lugar; veja as seções acima)**
- Editados: `README.md`, `docs/deploy/vps.md`, `docs/superpowers/PENDENCIAS-PARA-O-PLANO-12.md`, `docs/superpowers/plans/2026-09-22-roadmap.md`, `docs/superpowers/specs/2026-09-24-planos-5-6-7-contrato.md`
- Renomeado e editado: `docs/superpowers/specs/2026-09-22-vigilume-design.md` → `…-stillon-design.md`
- Apagados: os 33 da lista "Arquivos a apagar"

## Dúvidas para o controlador ou o dono

1. **User-Agent.** Manter `StillOn/1.0 (+monitor de disponibilidade)` ou adotar `StillOn/1.0 (+https://stillon.tech)`, a convenção de bots que o levantamento sugere? O plano mantém o texto atual. Trocar é uma linha em `probe.go` e `notify.go` e o teste do prefixo não muda.
2. **Seção Histórico do README.** Apontar para o SHA `<BASE>` resolve os ~600 identificadores "Plano N / D<n>" dos comentários sem reescrevê-los. Se o dono preferir uma tag (`git tag docs-historicos-2026-09-25 <BASE>` e push), o README cita a tag em vez do SHA. Isso é uma ação que muda o remoto, e por isso fica fora do plano sem o sim dele.
3. **Chaves de demo antigas em base64** em vez de texto. Se o dono preferir o texto legível, isso vira a segunda exceção da checagem: as três linhas de `config.go`, listadas no script.
