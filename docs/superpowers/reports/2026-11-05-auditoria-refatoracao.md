# Auditoria para a refatoração geral (2026-11-05)

Base: `main` em `8d9b90b`. Só auditoria: nada de código mudou. Substitui, como ordem de trabalho, as
Ondas do [backlog de 2026-10-14](2026-10-14-backlog-refatoracao.md), que continua valendo para o
detalhe de mecânica e para os Descartes e Defeitos de lá. Os ids `R…` são daquele backlog; `N…` é
item novo. Critério: `~/.claude/knowledge/code-quality.md`, seções A e B, com a B7 como filtro.

Legenda: custo **P** (até 2 h), **M** (até 1 dia), **G** (mais de 1 dia), estimado sem medir. Risco
**B**, **M**, **A**. Um PR por item, testes verdes antes e depois, teste novo visto falhar.

## Medidas

Tudo medido nesta auditoria, em `8d9b90b`, salvo o que está marcado.

| Medida | Valor |
|---|---|
| Go de produção / de teste | 16.847 / 38.537 linhas; 636 funções (sem o `storage/db` gerado) |
| Painel de produção / de teste | 9.668 / 13.226 linhas |
| Maiores funções Go (ciclomática, linhas) | `config.Load` 48, 247; `backup.run` 37, 144; `runUser` 33, 127; `validate` 22, 66; `ChangePassword` 22, 88; `ConfirmRotation` 22, 95 |
| Maiores componentes (linhas, contagem por `awk`) | `PaginaConfiguracoes` 223; `Formulario` da status page 205; `PaginaCanais` 201; `DetalheDoAlvo` 196; `FormularioAlvo` 190 |
| Argon2id da produção (64 MiB, t=3, p=2), arm64 | 0,09 s sem `-race`; **1,2 a 2,5 s com `-race`** (cerca de 20×), 3 amostras nesta máquina |
| Argon2id barato (8 MiB, t=1, p=1), arm64, `-race` | 0,04 s |
| `internal/httpapi` | 233 funções `Test`, 216 chamadas de `newAPI*`, **zero `t.Parallel`**; cada `newAPI` faz 1 hash real e 1 `container.Restore` |
| CI (run `42131340507`) | `test` 5 min 44 s por perna; `vps` 6 min 17 s; `e2e` 5 min 44 s |
| Último `deploy.yml` | 2026-10-14. O `backup.yml` roda todo dia e passa |

Conta que explica o item N1: 216 fixtures × cerca de 1,9 s dá uns 410 s em hash de fixture, perto de
40% dos 1063 s informados. Os 1063 s não foram reproduzidos, e as 91 chamadas de helper de login
somam verificações reais por cima. A divisão fina depende do `go test -json` pedido no N7.

## O que aconteceu com o backlog antigo

| Estado | Itens |
|---|---|
| **Resolvidos** | R0.3 (o limitador usa `clock.Clock`; o prazo do semáforo é injetável), R1.1 (`lib/estado.ts`; as funções do spark ficaram puras e testadas em `Minigrafico.tsx:54`, sem o `lib/spark.ts` previsto), R1.2, R1.3 |
| **Parciais** | R0.1 (só em `account`: ver N3), R0.2 (só em `account`: ver N2), R1.4 (`duracaoDoIncidente` feito; faltam `normalizarCodigo` e tabela no `navegadorDe`), R1.5 (constantes feitas; faltam `destinoSeguro` e `baixarTexto`), R1.8 (`pedir<T>` feito; faltam `repetirComTokenNovo` e o `ApiError` de 6 posicionais), R1.11 (`LIMITES` só nos heartbeats), R6.3 (o helper Go valida a URI, mas o awk segue no script) |
| **Abertos, listados nas ondas abaixo** | R0.4, R0.5, R1.6, R1.7, R1.9, R1.10, R2.1 a R2.13, R3.1 a R3.10, R4.1 a R4.5, R5.1 a R5.5, R6.1, R6.2, R6.4, R6.5, R6.6 |
| **Pioraram desde outubro** | R2.1 (+1 `falhou`), R2.12 (paginação e cadeias, com os heartbeats), R5.4 (7 → 10 `SELECT *`), R6.6 (1201 → 1422 linhas), R5.5 (27 chamadas de SQL inline) |

Decisões antigas: a recomendação de D2 a D5 foi aceita pelo dono em 2026-10-14, e a D1 foi feita
(itens da Onda 1 dentro do redesign). A numeração de migrations mudou: o texto do R5 fala na 00014, e
hoje a última é a 00018, com a 00015 vaga.

## Onda 0: testes rápidos e determinísticos

Vem primeiro: as outras ondas rodam a suíte dezenas de vezes.

| Id | Local | Smell | Refactoring | Custo | Risco | Rede |
|---|---|---|---|---|---|---|
| N1 | `httpapi/auth_integration_test.go:88` | Slow Tests, fixture repetida | **Extract Function** com `sync.OnceValue`: um hash real por processo, reaproveitado | P | B | Suíte; `internal/auth` continua provando os parâmetros reais. Medir antes e depois com `go test -race ./internal/httpapi` |
| N2 (resto do R0.2) | `httpapi/authhandlers.go:126`, `totphandlers.go:283`, `authhandlers.go:47` | Dependência oculta | **Replace Inline Code with Function Call** para um hasher no `AuthHandlers`. Hash fraco na fixture sozinho não basta: o `needsRehash` do login gasta um hash real. Fazer só se o N1 não bastar | M | M | `login_race_integration_test` (CAS do rehash); teste de que o hasher injetado é usado |
| N3 (resto do R0.1) | `httpapi/login_race_integration_test.go:104` (prazo fixo de 5 s), `auth/session_race_test.go:47`, `notify/reminders_test.go:162`, `auth/totp_prova_test.go:283`, `prober/sonda_velha_test.go:179`; 17 chamadas | Teste não determinístico, Duplicated Code | **Substitute Algorithm** + **Move Function**: `esperarPresa` (`account/account_test.go:245`) vira helper do `testdb`, com corrida contra o fim da goroutine e limite em `t.Context()`, filtrando a trava esperada | M | B | Sabotar a trava de cada teste e ver falhar sempre, com e sem carga |
| N4 | `config/scripts_test.go:155` | Teste não determinístico | **Substitute Algorithm**: prazo por `t.Context()` e `cmd.WaitDelay`, como o teto de 60 s da `:262`; 3 s de parede para um `sh` com 11 casos falha sob carga | P | B | Os 11 casos da tabela, com o helper sabotado para travar |
| N5 | `internal/config/{scripts,backup_snapshot,compose,seed,repositorio}_test.go` | Divergent Change: `config` testa scripts de deploy, 635 linhas só no snapshot | **Move Function** para um pacote de testes de deploy | M | B | Mesmos testes, antes e depois; job `test` |
| R0.4 | `INSERT INTO users` em 30 arquivos | Duplicated fixture, Mystery Guest | **Extract Function** `testdb.NovoUsuario`, migrando só onde a onda tocar | P, depois incremental | B | Suíte |
| R0.5 | Esperas por relógio em pelo menos 40 arquivos de teste; o maior é `config/backup_snapshot_test.go` (11) | Teste não determinístico | Triagem: teto em `select` fica, decisão pelo relógio vira item | M | B | Nenhuma: é levantamento |
| N6 | `web/.oxlintrc.json`; sem `.golangci.yml` no repo | Falta de fitness function | Ligar `complexity` e `max-lines-per-function` no oxlint (o R1 adiou até o fim do redesign, que acabou) e `gocyclo` em modo ratchet, com a lista atual de exceções | P | B | O próprio lint; uma função sabotada acima do teto |
| N7 | `testdb/testdb.go:75`; 216 `newAPI` em série | Slow Tests | Primeiro medir com `go test -json`. Depois decidir **D6** | G | M | Suíte com `-race -count=3` sem falha |

## Onda 1: `httpapi`, a duplicação que cresceu

Toda feature nova copiou o mesmo esqueleto; o heartbeat confirmou a Rule of Three. Nenhum item muda
contrato HTTP.

| Id | Local | Smell | Refactoring | Custo | Risco | Rede |
|---|---|---|---|---|---|---|
| R2.12a | `heartbeathandlers.go:367` `paginaDe`, `targethandlers.go:165-186`, `incidenthandlers.go:70-89`; cursor em `targethandlers.go:198`, `incidenthandlers.go:110`, `heartbeathandlers.go:206,292,327` | Duplicated Code | **Move Function** de `paginaDe` para um arquivo comum, parametrizado pelo teto; **Extract Function** `proximoCursor` (genérica sobre o id). Os três pacotes têm os mesmos 50 e 100 | P | B | `limit` e `cursor` on/off point em `targets_`, `incidents_` e `heartbeats_integration_test` |
| N8 | `ReasonRequired/Invalid/OutOfRange` em `targets/validate.go:19`, `heartbeat/heartbeat.go:48`, `statuspage/statuspage.go:33`, `notify/service.go:44`; 4 `ValidationError` | Duplicated Code, Alternative Classes with Different Interfaces | **Extract Class** num pacote pequeno de validação, sem dependências, com `Fields []FieldError`; cada domínio usa o mesmo tipo. `incidenthandlers.go:67-95` deixa de importar `targets` só pelos motivos | M | M | `entrada_estrita_integration_test`; JSON de erro idêntico, campo a campo |
| R2.1 | 5 `falhou` (`targethandlers.go:341`, `incidenthandlers.go:133`, `channelhandlers.go:161`, `heartbeathandlers.go:394`, `statuspagehandlers.go:141`); `authhandlers.go:385` = `accounthandlers.go:221` | Duplicated Code | **Extract Function** `erroInterno(w, r, op, err)`, com o desvio do Argon2 ocupado. Depois do N8, um `falhouValidacao` comum. Os switches por domínio ficam | P | B | `canario_integration_test`; teste do 429 do Argon2 ocupado |
| R2.12b | `routes.go:220-234`, `:322-328`, `publicpage.go:57-61`, `pinghandlers.go:91-96` | Duplicated Code de ordem | **Extract Function** `externa(h, prazo)` com o porquê da ordem | P | B | Testes de `trace_id` e cabeçalhos nas 4 cadeias |
| N9 | `usuario` + `pathID` em 9 handlers (ex.: `targethandlers.go:143-150`) × `heartbeathandlers.go:356` `donoEID` | Duplicated Code | **Move Function** de `donoEID` para junto de `usuario` e `pathID`, e **Replace Inline Code with Function Call** nos alvos | P | B | `idor_integration_test` |
| R2.3 | `accounthandlers.go:100,204`, `authhandlers.go:379`, `provas.go:57` | Duplicated Code de invariante | **Extract Function** `metaDe(r, px)` | P | B | `ip_hash` gravado = IP do limitador |
| R2.5 | `authhandlers.go:316-371` | Duplicated Code, Magic Literal | **Extract Function** `cookieDeAuth` + `const pathDeAuth` | P | B | Atributos do cookie posto e apagado |
| R2.2 | `totphandlers.go:30-45, 88-103, 310-329, 380-399, 461-479`, `accounthandlers.go:82-96` | Duplicated Code, Data Clumps | **Extract Function** `(p *provasDaFamilia) abrir` e **Extract Class** `provasDoFator` | M | M, a ordem é contrato de segurança | Sabotar a ordem das provas e ver `totp_*` e `provas_familia_*` quebrarem |
| R3.10 | `authhandlers.go:260`, `:300` | Flag Argument | **Remove Flag Argument** | M | M | "Assina antes do Set-Cookie" sabotado |
| R3.9 | `targethandlers.go:271-336` | Long Function | **Split Phase** `lerIntervalo(q, agora)` | P | B | `window`, `from`, `to` em `targets_integration_test` |
| N10 | `authhandlers.go:72` `Login` (104 linhas, cyc 15), `totphandlers.go:155` | Long Function | Só o trecho do rehash (R3.5): **Replace Nested Conditional with Guard Clauses** em `regravarHash`. O resto segue a B7 | P | M | `login_race_integration_test` |

## Onda 2: vocabulário de incidentes e avisos

| Id | Local | Smell | Refactoring | Custo | Risco | Rede |
|---|---|---|---|---|---|---|
| N11 | `notify/notify.go:41` `TargetName`, `notify/dispatcher.go:369,399`, `notifications.sql:17-23,82,92-95` `target_enabled`; 14 usos em `message.go` | Mysterious Name: o nome mente desde os heartbeats | **Rename Field** para `Monitor…`, com alias no SQL. A coluna `notifications.target_name` fica por ora (renomear é expand/contract) | P | B | `notify/*_test`, reminders com heartbeat; `sqlc generate` |
| R3.6 + N12 | `prober/scheduler.go:414` `avaliar`; `:462-468`, `:473-479`, `prober/heartbeat.go:122-128`, `:139-145` | Duplicated Code (4 cópias de marcar + enfileirar), Long Function | **Extract Function** `avisarAbertura` e `avisarFechamento`; `avaliar` fica com a histerese | M | M, roda na transação do check | Abertura e fechamento com `n == 1` e `n == 0`; `heartbeat_test` |
| R3.7 | `prober/scheduler.go:320` (arquivo de 597 linhas) | Divergent Change, Large Class | **Extract Class** `gravador`. Depois do R3.6 | M | M | Testes de corrida com `-count=20` |
| R2.10 | `notify/dispatcher.go:291,337,383,448`; 14 literais de `status` | Primitive Obsession, Duplicated Code | **Replace Primitive with Object** `statusNotificacao`, `case failed` explícito e `default` que falha; depois **Extract Function** `registrarDesfecho` | M | M | Tentativas: 429 perdoado, `MaxAttempts`, backoff |
| R2.7 | `auth/store.go:187`, `account/account.go:556`, `notify/dispatcher.go:487`; 11+ ações literais | Duplicated Code, Primitive Obsession | **Substitute Algorithm** sobre uma query só; `type AuditAction` | M | M | Teste por ação lendo `audit_log` |
| R2.11 | `notify/message.go:13`, `prober/scheduler.go:486` × `targetstate.go:22`, `routes.go:123,149,158,190`, `cmd/stillon/ops.go` | Magic Number, Shotgun Surgery | **Replace Magic Literal**; `causas` por `prober.ErrorKind`, com teste que cobre todo valor | M | B | Teste de `causas` visto falhar com valor novo |
| R4.4 | `notify/notify.go:40` | Mysterious Name, Primitive Obsession | **Rename Field** `Event.Kind` → `Evento` | P | B | Compilador e `notify` |
| N13 | `prober/scheduler.go:543`, `notify/dispatcher.go:502`, `heartbeat/ping.go:103` | Duplicated Code | **Move Function** de `valorOuZero` para um lugar só | P | B | Compilador |

## Onda 3: série horária

Todos pedem a mesma rede: borda do corte com hora cheia e rollup ausente.

| Id | Local | Smell | Refactoring | Custo | Risco | Rede |
|---|---|---|---|---|---|---|
| R5.1, passo 1 | `maintenance.sql:9-15`, `targets.sql:114-121`, `statuspages.sql:83-114` | Duplicated Code, Comments | Characterization test: rollup consolidado = leitura bruta da mesma hora. Não achei teste que faça essa comparação | M | B | É a própria rede |
| R2.8 | `targets/service.go:334`, `targets/summary.go:83`, `statuspage/public.go:156` | Data Clumps | **Introduce Parameter Object** `rollupcut.Janela`. A terceira cópia já chegou | M | M | `rollupcut_test`, resumo e página pública |
| N14 | `targets/summary.go:97-104` × `targets/service.go:362` `horaBruta` | Duplicated Code | **Replace Inline Code with Function Call** | P | B | `TestSummarySparkMixesRollupsAndRaw` |
| R2.9 | `statuspage/public.go:219,225`, `targets/summary.go:134,146`, `public.go:26-35` | Duplicated Code | **Extract Function** `uptime`; `string(targetstate.Down)`; alias de `State` | P | B | `uptime` com total 0, ok 0, ok = total |
| R3.3 | `targets/summary.go:68` (87 linhas) | Long Function | **Extract Function** `sparkPorAlvo`, `horaCorrentePorAlvo`, `resumoDe` | M | B | `summary_test` |
| R3.2 | `statuspage/public.go:131` | Long Function | **Split Phase**: leitura e `montarView` pura | M | B | `public_test`; depois `montarView` sem banco |

## Onda 4: Long Function do backend e o `auth`

| Id | Local | Smell | Refactoring | Custo | Risco | Rede |
|---|---|---|---|---|---|---|
| R3.4 | `config/config.go:119` (cyc 48) | Long Function | **Combine Functions into Class** `leitor`; guardar a URL parseada | M | B | `config_test`, `config_complementar_test` |
| R3.1 | `cmd/stillon/user.go:122`, `:271` | Long Function, Repeated Switches (`:198` e `:222`) | **Split Phase** + tabela de subcomandos | M | B | `user_test`, `reset2fa_test` |
| R2.4 | `account/account.go:203-215, 345-354, 387-397, 521-530`; `auth/totp_rotation.go:78, 204` | Duplicated Code de invariante de concorrência | **Extract Function** `travarFamiliaViva` e `travarUsuarioPorEmail` | M | M | "Rechecks the family under the lock" |
| R3.5 | `account/account.go:144` | Long Function | **Extract Function** `provarSegundoFator`, `gravarTroca` | M | M | `account_test`, depois do R2.4 |
| N15 | `auth/totp_rotation.go:156` `ConfirmRotation` (95 linhas, cyc 22), `:58` | Long Function; `userID, familyID, rotation, code` são trocáveis | **Extract Function** das fases; **Introduce Parameter Object** só se o `FamilyID` (R4.5) não bastar | M | M | `totp_rotation_test`, corrida e RLS |
| R2.6 + R5.5 | `auth/totp.go:320-336, 535`, `users.sql:87`; 27 chamadas de SQL inline em `auth/` (`totp.go` 11, `store.go` 7, `user.go` 6, `totp_rotation.go` 3) | Shotgun Surgery, Divergent Change | **Move Statements** para `queries/*.sql`, uma query por PR, começando por `DisableTOTP` | G | M | Corrida e RLS de cada query, antes e depois |
| R3.8 | `statuspage/service.go:85` | Long Function | **Extract Function** `aplicar(patch, atual)` pura | P | B | PATCH: ausente não altera, `null` limpa |

## Onda 5: painel

| Id | Local | Smell | Refactoring | Custo | Risco | Rede |
|---|---|---|---|---|---|---|
| N16 (cresce R1.11) | `alvos/FormularioAlvo.tsx:43-58`, `heartbeats/FormularioHeartbeat.tsx:42-51`, `statuspage/PaginaStatusPage.tsx:121-130`, `canais/PaginaCanais.tsx:73`, `configuracoes/PaginaConfiguracoes.tsx:157` | Duplicated Code | **Extract Function** `errosDosCampos(err, mapear)` pura, com teste por tabela; o seletor de intervalo fica no chamador | P | B | Testes de tela do 400 com `fields` |
| N17 | `incidentes/PaginaIncidentes.tsx:222-238`, `alvos/FaixaDeEstado.tsx:72-86`, `heartbeats/IncidentesDoHeartbeat.tsx:32-50` | Duplicated Code (ack com 404, invalidação e reentrância) | **Extract Function** hook `useReconhecer`. Os avisos diferem: parametrizar por callback | P | B | `incidentes.test`, `detalhe.test` |
| R1.6 | 12 `queryKey` literais em `features/`, contra `lib/api/consultas.ts:7`; sem `mutacoes.ts` | Shotgun Surgery | Prefixos em `chaves`; depois **Move Function** das mutações | M | B | Testes de invalidação |
| R1.9 + R1.10 | `configuracoes/PaginaConfiguracoes.tsx` (960 linhas, 25 `useState`, 11 `instanceof ApiError`; `:738-960`) | Large Class, Mutable Data | **Extract Function** `credencial.ts`; reducer puro `proximoEstadoDo2fa`; **Move Function** de `Sessoes` (`:643`), `TrocaDeSenha` (`:110`) e dos diálogos para arquivos próprios | G | M | As 1372 linhas de `configuracoes.test`; tabela de transições antes de trocar o `useState` |
| R1.8 (resto) | `lib/api/cliente.ts:111-139`, `lib/api/http.ts:24-31` | Long Function, Long Parameter List | **Extract Function** `repetirComTokenNovo`; **Introduce Parameter Object** no `ApiError` | M | M, o 401 é denso | `cliente.test` |
| R1.7 | `PaginaConfiguracoes.tsx:85-88` × `lib/api/cliente.ts:120-125` | Duplicated Code de política | **Move Function** `sessao.renovarOuEncerrar()` | P | B | `sessao.test` |
| R1.11 (resto) | `alvos/esquema.ts:35`, `:94`, `:129`, `:106-112`, `:136-137`, `:216-237` | Long Function, Magic Number | **Decompose Conditional**; objeto `LIMITES` como o de `heartbeats/esquema.ts:12` | M | B | `esquema.test`, on/off point por limite |
| N18 | `features/*/Pagina*`: `Formulario` da status page (205 linhas), `PaginaCanais` (201), `DetalheDoAlvo` (196) | Long Function | **Extract Function** por seção, só onde o nome ficar mais abstrato que o JSX | M | B | Testes de tela; `a11y.spec` |
| R1.4, R1.5 (restos) | `.replace(/\s+/g, '')` em `PaginaLogin.tsx:121`, `PaginaConfiguracoes.tsx:144,283,378`; `lib/format.ts:98`; `PaginaLogin.tsx:21`; `PaginaConfiguracoes.tsx:333` | Duplicated Code, lógica de `lib` na tela | **Extract Function** `normalizarCodigo` e `baixarTexto`; **Move Function** de `destinoSeguro`; tabela no `navegadorDe` | P | B | Teste de redirect hostil antes de mover |

## Onda 6: tipos e SQL

| Id | Local | Smell | Refactoring | Custo | Risco | Rede |
|---|---|---|---|---|---|---|
| R5.4 | 10 `SELECT *` em `queries/` (3 novos em `heartbeats.sql:15,43,133`) | Regra de SQL | Listar colunas | P | B | `sqlc generate` sem diff no Go |
| R5.2 | `users.sql:27`, `:56` | Duplicated Code de política | **Parameterize Function** | M | M | Set-password e troca, com CAS |
| R5.3 | `targets.sql:69-70,90`, `maintenance.sql:30`, `statuspages.sql:54-55,69`; `summary.go:131` | Valor sentinela | Tirar o `coalesce`; o Go lê `nil` | M | M | Alvo sem check e sem rollup |
| R4.1 | `targets/validate.go:86` (cyc 22), `:157`, `summary.go:123`, `public.go:205`, `scheduler.go:308` | Primitive Obsession | **Replace Primitive with Object** `parseProbe` | G | M | `validate_test`, URL validada = URL sondada |
| R4.5 | `account.ChangePassword`, `TOTP.StartRotation`, `TOTP.ConfirmRotation` | Primitive Obsession | `type FamilyID` primeiro | M | M | Compilador |
| R4.2 | `crypto/hash.go:29`, `auth/session.go:68-78`, `account.go:200` | Primitive Obsession | **Replace Primitive with Object** `crypto.IPHasher` | M | M | Boot sem chave e com chave curta |
| R4.3 | `account/account.go:75-77` | Temporary Field | **Extract Class** `Operador` e `Service` | M | B | `user_test`, `account_test` |
| N19 | `routes.go:80-84`, `auth/argon2.go:59` | Global Data: `Routes` muta o semáforo do processo | **Replace Global Data** por dependência do `AuthHandlers` e do `account` | M | M | `argon2_integration_test` |
| R2.13 | 36 comentários "revisão"/"Review Focus"; 306 linhas de produção citam Plano, D*n*, R*n* etc.; `middleware/secheaders.go:7-9` diz que o painel é servido à parte, e é o mesmo binário | Comments | Apagar o id, manter o porquê; reescrever a CSP. Por arquivo, ao tocar | P por arquivo | B | Nenhuma |

## Onda 7: deploy e scripts

Depende da **D7**: a produção é Coolify, e o verbo `deploy` do `stillon-entrada` não roda desde
2026-10-14. O verbo `backup` segue vivo (`coolify/release/stillon-backup-entrada.sh:6`).

| Id | Local | Smell | Refactoring | Custo | Risco | Rede |
|---|---|---|---|---|---|---|
| N20 | `.github/workflows/deploy.yml`, `deploy/vps/stillon-entrada:308` `deploy`, `deploy/vps/compose.prod.yaml`, `deploy/ci/vps.sh`, `deploy/ci/checar-vps.sh`, `docs/deploy/vps.md` | Speculative Generality, Remove Dead Code: 1422 linhas e 6 min de CI protegem um caminho que a produção não usa | **Remove Dead Code**, depois de separar `backup` em entrada própria | G | A, o backup depende dele | Backup real com restauro; job `vps` reduzido ao `backup` |
| R6.3 | `deploy/backup/backup.sh:55-104` (hex em awk 2×) | Duplicated Code | **Move Function** para `internal/backup` (`validateURI`, `backup.go:47`, já recusa parte) | M | M | URI hostil, antes de mover |
| R6.1 | `falhar` em 8 scripts de `deploy/ci/` | Duplicated Code | **Extract Function** para `deploy/ci/lib/checagem.sh` | M | B | Cada `checar-*` sabotado |
| R6.2 | `85` em `stillon-entrada:666`, `backup.sh:181`, `ci/vps.sh:197`; `65532` 4× | Magic Number | Constantes nomeadas | P | B | `checar-vps.sh` |
| R6.5 | `checar-vps.sh:101,120,238,429`; credencial de demo em `Makefile:53`, `compose.yaml:86-88`, `checar-compose.sh:16` | Mysterious Name, dado duplicado | **Rename Function**; `DEMO_*` no Makefile | P | B | Checadores |
| R6.4, R6.6 | `stillon-entrada:308,561`, `checar-vps.sh` | Long Function | Só depois da D7: se o N20 entrar, a maior parte some | M a G | A | Paridade caso a caso |
| N21 | `coolify/release/README.md:25-27` × `receiver-release.json` (2.1.1) | Comments que mentem | Atualizar a versão citada | P | B | Nenhuma; `[skip ci]` |

## Descartes novos

- **`deploy/coolify/release/receiver.py`** (968 linhas; `validate_oci` cyc 26): compartilhado com Duora e GoblinShift, por hash. Refatorar só aqui quebra a igualdade de bytes.
- **`Patch.apply` e `updateParams`** (cyc 17 e 12): sequência plana de `if campo != nil`; a B7 aceita.
- **`depoisDeContar` e similares** (6 ganchos em `targets`, `heartbeat`, `notify`, `statuspage`, `account`): é o seam dos testes de concorrência; custa um campo nil.
- **Unir `incidentResponse` e `heartbeatIncidentResponse`, ou os dois `Ack`**: contratos separados de propósito (D3 da spec de heartbeats), e são dois casos, abaixo da Rule of Three.
- **Unir as duas `PaginasDoFormulario`**: o heartbeat carrega a URL de ping, e o alvo, o aviso dos headers; o que sobra em comum é pequeno.
- **Contar e inserir sob trava** (`targets/service.go:68`, `heartbeat/service.go:143`, `notify/service.go:229`): três cópias curtas com tipos diferentes; um genérico esconderia mais do que ganha.

## Decisões para o dono

**D6. Paralelizar o `internal/httpapi`.** (a) só o N1 e o N2; (b) banco por teste a partir de um
template, com `t.Parallel`; (c) dividir o pacote. **Recomendo (a), e decidir (b) com o número do
`go test -json` na mão.** O N1 custa 2 h e tira da ordem de 40% do tempo estimado. A (b) exige mexer
no `testdb` e rever o limitador por teste; a (c) não acelera e espalha helpers.

**D7. Aposentar a trilha de deploy antiga.** (a) remover `deploy.yml`, o verbo `deploy` e o `compose.prod.yaml`,
mantendo o `backup` numa entrada própria; (b) manter tudo como rollback; (c) só reduzir o job `vps`.
**Recomendo (a).** Ganho: uns 6 min por CI e o R6.4 e o R6.6 quase somem. Custo: perder o caminho
manual de volta e reescrever o `docs/deploy/vps.md`; é a parte que mexe no backup de produção.

**D8. Nome único para "o que é monitorado".** (a) `Monitor` no Go e no SQL novo, sem tocar no
contrato HTTP; (b) deixar `Target` e documentar. **Recomendo (a)**, o painel já diz "monitores". O
custo é o N11, e o contrato `target_name` de `/incidents` fica como está.

## O que ficou sem auditar

- Nenhuma suíte rodou: os 1063 s e os prazos de 3 s e 5 s vêm do pedido. Frequência de falha não foi medida.
- Dos 5 `ci` vermelhos nos últimos 60 runs, só li o da `main` (`govulncheck`, sem relação com teste). Os outros (`e2e`, `web`, `lint`, `test` no 18, `workflows`) não foram abertos.
- Só por métrica, sem leitura linha a linha: `internal/crypto`, `observability`, `webui`, `prober/safedial`, `incident/history`, `auth/session.go`, `token.go`, `dispositivo.go`, `httpapi/middleware` e `backup/backup.go`.
- Sem auditoria: qualidade interna dos testes além das esperas, `web/e2e`, CSS, `web/src/test/api-falsa.ts`, `scripts/`, `docs/deploy/`, e scripts Python de `deploy/ci`.
- Plano de migração de cada item: as ondas dão ordem e rede, não o passo a passo.

## Auditoria complementar (2026-11-06)

Base: `main` em `27b1005`. Cobre o que a lista acima deixou de fora e o que entrou de #165 a #176. O PR
#177, aberto, trata o domingo do cron e não foi auditado. Só auditoria: nenhum código mudou, e nenhuma
suíte rodou. A `/security-review` não foi usada: ela revisa um diff, e aqui não há diff de código.

**Separação.** Defeito real (`F…`) tem cenário concreto. Smell (`N…`) não quebra nada hoje. Legenda de
custo e risco igual à do início. O sinal † marca item de leitura delegada que não reconferi linha a
linha; o resto foi lido por mim, e cada fato de produção vem de comando citado.

### Fatos de produção medidos

| Fato | Prova | Por que importa |
|---|---|---|
| A **Cloudflare** está na frente de `stillon.tech` | `curl -I https://stillon.tech/` devolve `server: cloudflare`, `cf-ray` (GRU), `cf-cache-status: DYNAMIC` e dois `via: 1.1 Caddy` | `git grep -i cloudflare` só acha `CF-Connecting-IP`: README, specs e runbooks não citam a Cloudflare |
| O ping público atravessa a Cloudflare sem desafio | `POST /ping/<token inexistente>` responde 404 em JSON do app | D6 e o `curl -f` da tarefa seguem valendo; descarta o bloqueio por bot, hoje |
| Backups diários existem | artifact `stillon-backup-42233497757`, de 2026-11-06 14:23, 30 dias de validade | Não há perda de dados à vista |
| A revalidação de restauro falha desde 2026-11-04 | runs `41879675778` e `42021172944`; o último verde é `41739202525`, de 2026-11-03 | Ver F1 |
| O agendamento do backup deriva | marcado para 06:17 UTC, rodou 14:23 em 11-06 e 06:35 em 10-30 | Atraso do GitHub; ver N22 |

### Defeitos reais

Nenhum defeito provado de segurança ou de perda de dados **nos dados de hoje**. F1 e F2 ficam no topo
porque tiram duas provas em que o projeto confia.

| Id | Gravidade | Local | Cenário | Correção | Custo | Risco | Teste que protege |
|---|---|---|---|---|---|---|---|
| F1 | Alta | branch `audit/forwarding-review-20261023`, `deploy/backup/restore-stored-audit.py:25-27` | A API lista só os 100 artifacts mais novos, e o repositório tem 963. Entre o backup de 11-05 14:46 e a tentativa de 11-06 05:43 nasceram ao menos 290 artifacts de CI, então nenhum `stillon-backup-*` entra na página e `max([])` dá `ValueError`. Com o `main` ativo, a falha se repete a cada PR grande. A prova recorrente de que o backup restaura parou há 3 dias, e o dono não foi avisado | **Substitute Algorithm**: `?name=` por artifact ou paginação; mesmo critério de idade. Decidir D9 antes | P | B | Rodar com mais de 100 artifacts novos; caso de lista vazia com erro nomeado |
| F2 | Alta (risco), impacto não confirmado | `deploy/coolify/app.json:13,25`, `deploy/vps/Caddyfile:17-19,34`, `README.md:178,184`, `specs/2026-11-05-heartbeats-design.md:§6,§9` | A cadeia real é cliente → Cloudflare → Caddy → Caddy → app. O app só vê o que o último Caddy calculou, e o repositório não diz o que o primeiro faz com o IP da Cloudflare. Se ele não a tratar como proxy confiável, todo cliente da mesma borda divide o balde de login (10/min), e `ip_hash` grava a Cloudflare. **Não confirmado:** falta ver a configuração do primeiro Caddy. Confirmado: o modelo de ameaça está incompleto. Spec e README tratam o caminho como sem log, e a Cloudflare termina TLS, logo vê token de ping, token de verificação, senha e cookie em claro | Ler a configuração do primeiro salto; decidir D10; documentar a cadeia e refazer o STRIDE do ping | P para conferir, M para corrigir | M | Dois clientes de origens diferentes em buckets distintos; canário de IP em `sessions` |
| F3 | Média | `deploy/vps/Caddyfile`, `deploy/coolify/app.json:101`, `deploy/coolify/README.md` | O Coolify fixa o commit de infraestrutura, e o release só troca imagens. Um PR que corrija o Caddyfile, mesmo por segurança, passa no CI e não chega à produção, sem aviso | **Move File** para `deploy/coolify/` e passo explícito no checklist; ou um check que compare o commit fixado | M | A | Teste que falhe com commit de infraestrutura atrás do Caddyfile |
| F4 | Média | `README.md:44-48,85,239,311`, `docs/deploy/vps.md`, `docs/deploy/mac.md:6-8` | `README.md:311` diz "Deploy: sempre manual", e merge publica sozinho. O `vps.md` descreve a produção anterior inteira, sem banner; o `CLAUDE.md` manda guiar por ele; há `dropdb` em `:4241`† e `docker volume rm` em `:4157`† | Banner "legado", **Move File** para `docs/deploy/legado/` e runbook Coolify curto. Depende de D7 | M | B | `checar-docs.sh` que exija o banner e cite o Coolify no README |
| F5 | Média | `docs/deploy/vps.md:4084`† | O "upgrade menor" manda recriar o `db`. Em produção a imagem está sem tag, e `pull_policy: never` faz o recreate falhar (`deploy/coolify/README.md`) | Nota no runbook e item no checklist | P | M | Revisão; `checar-docs.sh` |
| F6 | Média | `deploy/ci/test_snapshot_banco_backup.py` | Nenhum Makefile ou workflow o chama (`grep` no repositório; o `discover` usa `-p test_docker_build.py`). Os 6 testes do snapshot do backup não protegem nada | Ligar ao job `workflows`, ou apagar com a D7 | P | B | Sabotar `snapshot_banco_backup` e ver falhar |
| F7 | Média | `web/e2e/fluxo.spec.ts:188-192` | O cookie tem `Path=/api/v1/auth` (`authhandlers.go:323`): `document.cookie` numa página do painel nunca o traz, com `HttpOnly` ou sem. O comentário "este teste prova a propriedade" é falso. O código está certo; a garantia não | `context.cookies()` conferindo `httpOnly`, `sameSite` e `path` | P | B | `HttpOnly: false` sabotado tem de quebrar a spec |
| F8 | Média | `web/src/test/api-falsa.ts:399-403` | O PATCH de `enabled` devolve `state: 'up'`. O servidor devolve `waiting` em quem nunca concluiu e `late` com incidente aberto (`heartbeat/service.go:553`). Teste de tela de "retomar" passa num estado que não existe | Espelhar `statusOf` | P | B | Retomar tarefa `waiting` e tarefa com incidente |
| F9 | Média | `web/src/test/api-falsa.ts:357` × `incidenthandlers.go` | A falsa só dá `next_cursor` se sobra item; o servidor dá com a página cheia. Com exatos 100 abertos, a tela real diz "100 ou mais" e a falsa, "100" | `cheia = pagina.length === limite` | P | B | Tela com exatamente `limit` itens |
| F10 | Média | `web/src/test/api-falsa.ts:294-308,368-386,399-405`, entre outras† | Só o `PATCH /channels` imita a entrada estrita. As demais usam `Object.assign(h, corpo)`: cliente que mande campo que o servidor recusa com 400 passa nos testes de tela. É mitigado pelos `toEqual` do corpo | **Extract Function** `exigirCampos(corpo, permitidos)` por rota | M | B | `criarAlvo({...x, extra: 1})` falha na falsa |
| F11 | Baixa | `web/src/test/api-falsa.ts:486`, `:454-456`, `:279-292`, `:121-126`† | `POST /status-pages` substitui (real: 409). Reenviar verificação de canal já verificado dá 204 (real: 409). O filtro de tag não normaliza, nem recusa `state` inválido. `forcar` aceita par status e código que o servidor não emite | Tabela tipada de erros copiada de `apierr/errors.go`, e os casos | P | B | Um teste de contrato por caso |
| F12 | Baixa | `heartbeathandlers.go:52,275`, `targethandlers.go:44` | `PATCH {"name": null}` responde 200 e não muda nada. A regra do dono separa `null` de ausente, e `description` e `tags` já seguem `optional[T]` | `optional[string]` e 400 em `null` | P | B | PATCH com `name: null` dá 400, banco intacto |
| F13 | Baixa | `.github/workflows/coolify.yml:17` | O filtro cita `publish-images.yml`, que não existe; a publicação está no `ci.yml`. Mudança nela não dispara a checagem do release | Corrigir o caminho | P | B | `actionlint` não pega: revisão |
| F14 | Baixa | `web/e2e/pilha.ts:218-224` | No macOS não há `/proc`, então o binário de uma rodada interrompida nunca é morto (o comentário assume isso) e fica com a porta aberta. O Mac é a máquina do dono | `ps -p <pid> -o command=` | P | B | `kill -9` no runner, nova rodada, `pgrep` vazio |
| F15 | Baixa | `web/e2e/pilha.ts:238-245`, `:73`, `:107`† | `docker rm -f` engole todo erro, e `rmSync` apaga o registro; `docker run` e `exec` sem prazo. Docker parado deixa container órfão sem rastro | Ignorar só "No such container", manter `pilha.json`, `timeout` | P | B | `derrubar` com docker falso que falha |
| F16 | Baixa | `README.md:391`†, `docs/deploy/vps.md:4485`†, `:163` e mais 6† | `IMAGE_TAG` não existe; "os workflows não usam `environment`", e `coolify.yml:91` usa; `~/Projetos/Go/StillOn` falha no Mac do dono | Corrigir no bloco do F4 | P | B | `checar-docs.sh` |
| F17 | Baixa | `deploy/ci/diagnostico-fixture.py:358`† | `close()` itera `registry` sem trava com a thread viva; `RuntimeError` perde o `manifesto.json` | `list()` sob a trava | P | B | `close()` com thread presa |

### Os runs vermelhos do `ci`

Os 4 que a auditoria não abriu, e os 3 que vieram depois. Nenhum afeta a `main`.

| Run | Branch | Job | Causa lida no log | Estado |
|---|---|---|---|---|
| `41902225671` | `chore/deps-131-135` | `workflows` | `testar-imagem-backup.py`: docker falso em `/run` no runner; 3 falhas e 1 erro | Corrigido em #138 |
| `42083765701` | `feat/heartbeats-dominio` | `lint` | gosec G101 na constante gerada `lockHeartbeatByTokenHash` | Query renomeada (`heartbeats.sql:12-13`) |
| `42083765701` | idem | `test` no 18 | `TestAnotherUsersChannelIs…`: canário `"6789"` casou com um id aleatório, falso alarme | Corrigido em #150 (`"webhook …6789"`) |
| `42085051717` | `denso/tabela` | `web` | `sistema.test.ts` barrou `shadow-none`: a fitness function funcionou | Corrigido no PR |
| `42123955566` | `feat/heartbeats-detalhe` | `e2e` | `a11y.spec.ts:550` esperava `Incidentes, 1 aberto`; a soma com tarefas mudou o nome da aba | Corrigido no PR; a spec hoje afirma 2 |
| `42134824502`, `42135648582` | heartbeats | `lint` | gosec G109 em `heartbeat.go:124`, `Atoi` convertido | `ParseUint` |
| `42139393822` | `test/e2e-jornada-heartbeats` | `e2e` | `Tarefas: 1 pausada` sumiu por 45 s: não é prazo. A causa provável é a tarefa pausada com incidente, corrigida por `0ae5353` no mesmo PR (inferência: a ordem dos commits casa, e o passo não foi reproduzido) | O #175 ainda ampliou prazos; ver N38 |

Padrão: nenhum foi flaky de infraestrutura. Vários são teste barrando defeito real antes do merge.

### Pacotes lidos linha a linha

**Sem defeito:** `internal/crypto`, `prober/safedial`, `webui`, `incident/detector`, `auth/session.go`,
`token.go`, `dispositivo.go`, `backup/backup.go` e os middlewares. As travas, a ordem de cancelamento e
as recusas conferem com os comentários. SSRF: o IP público da própria VPS passa no `AllowIP`, sem
importância enquanto o único usuário é o dono, criado pela CLI.

| Id | Local | Smell | Refactoring | Custo | Risco | Rede |
|---|---|---|---|---|---|---|
| N22 | `.github/workflows/backup.yml:14` | Hidden Dependency: o relógio do `schedule` do GitHub atrasa até 8 h, e a política de 26 h do F1 fica no limite | Disparo por relógio próprio, ou alargar a política com motivo escrito | P | B | Run agendado medido por uma semana |
| N23 | `backup/backup.go:25,397` | Incomplete Data: `Reference` confere usuários e alvos; heartbeats, tags e eventos ficam fora da aceitação do restauro | **Add Field** `Heartbeats`, no protocolo `v2`, com o script do restauro junto | M | M | Restauro real com heartbeat; `backup_test` |
| N24 | `backup/backup.go:36,385` | Long Line (mais de 300 colunas) | **Reformatar**; o `gofmt` aceita, mas ninguém lê | P | B | Compilador |
| N25 | `incident/history/history.go:72-75,94-97` | Duplicated Code | **Extract Function** `toIncident` | P | B | `history_test` |
| N26 | `httpapi/middleware/recover.go:21-36` | `http.ErrAbortHandler` é engolido, e `WriteError` escreve depois de resposta parcial. Sem chamador hoje | Repassar o abort e pular o envelope depois do `WriteHeader` | P | B | Teste de panic depois do `Write` |
| N27 | `clientip.go:15-25,38-44,81`, `TestRateLimitSeparatesClientsBehindFly` | Comments que mentem: citam o Fly e o Caddy como produção. Aumenta o R2.13. O modo `CLIENT_IP_HEADER` não tem consumidor† | **Rename**; o resto depende de D10 | P a M | M | `clientip_test` |
| N28 | `observability/metrics.go:1`, `trace.go:1`, `app.json:22` | Dois doc comments de pacote; `METRICS_ADDR=off`, então fila, outbox e DLQ não têm métrica lida em produção | Unificar o texto. Métrica é decisão, não refactoring | P | B | Nenhuma |
| N29 | `auth/token.go:90-91` | Comments: "o painel ainda não foi publicado" | Apagar. Aumenta o R2.13 | P | B | Nenhuma |
| N30 | `auth/dispositivo.go:186-190` | Contador em memória zera a cada merge, e merge é deploy; varredura O(n) sob trava | Aceitar: n é pequeno, o bloqueio da conta segue no banco. Registrar | P | B | Nenhuma |
| N31 | `incident/detector.go:58` | Global Data exportado e mutável | `func Default()`; pouco ganho, fazer ao tocar | P | B | `detector_test` |

### Heartbeats, painel denso e alinhamento

As regras de prazo, abertura, fechamento, retomada, cooldown, RLS e token batem com a spec nos cenários
que percorri: atraso, falha, pausa com ping, retomada com e sem sucesso, dois avaliadores e corrida com
PATCH. Nenhum defeito de domínio.

| Id | Local | Smell ou desalinhamento | Refactoring | Custo | Risco | Rede |
|---|---|---|---|---|---|---|
| N32 | `heartbeat/service.go:281-285`, `ping.go:66` | Poison Row: uma linha que a validação futura recuse (`ScheduleFromRow`) derruba a lista inteira e todo ping dela. Hoje não há linha assim | Pular e logar na lista, ou manter a validação de leitura mais larga que a de escrita | P | M | Linha inválida não derruba as outras |
| N33 | `storage/queries/heartbeats.sql:68-69` | Comments: citam `heartbeats_due_idx`, removido na 00019 | Reescrever | P | B | Nenhuma; `sqlc generate` |
| N34 | `heartbeathandlers.go:236-246` × `:414` | Inconsistent Validation: a prévia recusa parâmetro desconhecido, `paginaDe` aceita | Decidir no R2.12a; recusar nas listas é mudança de contrato | P | M | `heartbeats_integration_test` |
| N35 | `specs/2026-11-05-heartbeats-design.md:§6,§9` | A checagem de access log foi no Traefik, e `app.json:63,109` tem `traefik.enable=false`; a resposta mostra Caddy e Cloudflare | Corrigir junto do F2 | P | B | Nenhuma |
| N36 | `specs/2026-11-05-painel-denso-design.md:134` | "Nenhum alvo com esses filtros." e colunas "Alvo": a tela diz "monitor" (T6) | Atualizar a spec | P | B | Nenhuma; `[skip ci]` |
| N37 | `lib/api/consultas.ts:31`, `routes.go:158`, `web/e2e/fluxo.spec.ts:76-83,381-449` | Requisito implícito: o painel soma polls de 15 s por tela, o limite é 60/min com rajada 20, e o e2e só parou de levar 429 cadenciando o navegador a 1 req/s†. **Não confirmado** com usuário real | Medir requisições por navegação antes de mexer | P | M | Contador de requisições no e2e |

### Qualidade interna dos testes

**Go, medido:** 22 `time.Sleep` em 14 arquivos, zero mock de banco, zero `t.Parallel` no repositório
inteiro, 3 candidatos sem assert (conferidos: usam helpers, ou o não-pânico é o comportamento). A base é
boa: banco real, nome que diz o comportamento, sabotagem exigida. Os itens abaixo são o que sobra.

| Id | Local | Smell | Refactoring | Custo | Risco | Rede |
|---|---|---|---|---|---|---|
| N38 | `web/e2e/fluxo.spec.ts:228,244,321-328`† | Sleepy Test: `ESPERA_DO_POLL_MS` em espera que não é de poll; `:327` segue com o prazo padrão | **Replace Wait with Event** (`waitForResponse`) e nome certo | P | B | `--repeat-each=5` |
| N39 | `cmd/stillon/heartbeat_e2e_test.go:134` | Negative assert depois de 6 s fixos: "o papel api não abriu incidente" passa se a máquina atrasar | Provar que um tique rodou (métrica ou marca) antes de negar | P | B | Sabotar `--role=api` abrindo incidente |
| N40 | `httpapi/sessions_integration_test.go:223`, `statuspage/service_test.go:356`, `cmd/stillon/statuspage_e2e_test.go:100-123`, `incident_e2e_test.go:257` | Sleep em vez de `Clock` ou condição. Aumenta o R0.5 | Triagem do R0.5 | M | B | `-count=20` |
| N41 | `internal/httpapi/plano5_routes_test.go`, 7 `*_complementar_test.go`, `prober/spec_log_test.go`; 74 arquivos de teste citam Plano, Task ou R*n* | Mysterious Name: nome pela história, não pelo comportamento | **Rename** e **Move Function** ao tocar. Aumenta o R2.13 | M | B | Mesmos testes |
| N42 | `httpapi/idor_integration_test.go:111` | Fragile Test: canário por substring já deu falso alarme (`42083765701`) | `CANARY-<uuid>`, como manda a regra de log | P | B | Sabotar o handler |
| N43 | `heartbeats_integration_test.go` (1169 linhas), `auth_integration_test.go` (1207) | Large Class | **Move Function** por assunto, depois do N1 | M | B | Suíte |
| N44 | `web/e2e/fluxo.spec.ts:233,328`† | Weak Assertion: `ABA_COM_ABERTOS` aceita qualquer contagem, e o comentário diz que confere a soma | Número exato na pilha própria | P | B | `useTarefasQuePedemAtencao` sabotado |
| N45 | `web/e2e/fluxo.spec.ts:16-39`, `pagina-publica.spec.ts:23,50-61`, `fluxo.spec.ts:85-90`, `troca.spec.ts:25-32`† | Duplicated Code, Sleepy Test: laço de 429 copiado, comentário "sem Retry-After" falso (`ratelimit.go:171`), 42 s de pausa fixa | **Extract Function** `e2e/limite.ts`, que obedece o `Retry-After` | P | B | Fake com 429 |
| N46 | `web/e2e/fluxo.spec.ts:263-304`† | Lacuna: a pilha real não exercita `/ping/{token}/{n}`, cron, prévia, tags nem descrição | Acrescentar à jornada | M | B | A própria jornada |
| N47 | `web/e2e/abas.spec.ts:62-64`, `troca.spec.ts:28`† | Conditional Test Logic: pulam em silêncio; a conferência de reúso nunca roda no CI | Anotar o pulo, ou ler a auditoria pelo compose | P | B | Run do CI mostra a anotação |
| N48 | `playwright.config.ts`, `ci.yml`† | Sem artefato de falha: `--trace off` protege a senha, e nada o substitui. O padrão local grava a senha contra pilha externa | `screenshot: 'only-on-failure'`; `trace` condicionado | P | B | Falha forçada gera o artefato |
| N49 | `web/e2e/a11y.spec.ts` (908 linhas), `capturas.spec.ts`, `api-imitada.ts:47-107`† | Large Class, Duplicated Code: preâmbulo em uns 12 testes, login em 3 arquivos; `capturas` é script sem assert, sempre "skipped"; terceira cópia do contrato | **Extract Function** `e2e/passos.ts`; **Move File** das capturas | M | B | a11y verde |
| N50 | `pagina-publica.spec.ts:33,38,149-160`, `fluxo.spec.ts:170`, `a11y.spec.ts:44,270-279`† | Fragile Test: classes de uma letra do template, internos do Recharts, `oklch` literal | `data-testid` ou ARIA | M | B | Specs |
| N51 | `web/src/sistema.test.ts:121-133`, `tokens.test.ts:13`, `sistema.test.ts:163-168`† | `toContain` casa comentário; `acento-fora`, `acento-degradado` e `sem-acento` fora do contrato: a barra de acento some sem teste. Teste histórico do PR 6 | Ampliar a lista; apagar o histórico | P | B | Remover o `@utility` e ver falhar |
| N52 | `web/src/index.css:230-237`, `:51,113`, `tokens.test.ts:99`† | Dead Code (`prefers-reduced-motion` quase sem alvo), Speculative Generality (`--realce` só apelida `--primaria`), contraste do `apagado` sobre tons 100 fora do teste | Cobrir ou apagar; **Inline** do token | P | B | `tokens.test` |
| N53 | `cadastro.test.tsx:132`, 8 `setTimeout(res, 50)`, `monitores.test.tsx:187`, 11 `.length).toBeGreaterThan(0)`† | Sleepy Test, assert vácuo ou redundante | Fake timers; `toHaveLength(n)` | M | B | Sabotagem pontual |
| N54 | `api-falsa.ts:293,367`, `cadastro.test.tsx:18`, `detalhe.test.tsx:12`† | Sem paginação em `/targets` e `/heartbeats`; montagem repetida | **Extract Function** `montarDetalhe`, `montarPainel` | P | B | Tela com 101 itens |

### Deploy, scripts e CI

`scripts/docker-build.sh` não tem defeito†. Variáveis de ambiente e caminhos dos runbooks batem com o
código†.

| Id | Local | Smell | Refactoring | Custo | Risco | Rede |
|---|---|---|---|---|---|---|
| N55 | `deploy/coolify/{app,database}.json` × `compose.prod.yaml`, `compose_test.go:42-62`† | Duplicated Code: o digest do Postgres no `database.json` e o do Caddy no `app.json` não entram no teste; o Dependabot atualiza um lado e o CI fica verde | Ampliar o teste; `TRUSTED_PROXIES` × Caddyfile junto | P | B | Sabotar o digest |
| N56 | `stillon-entrada:308,688-696`, `deploy.yml`† | Falta de guarda: o verbo `deploy` não recusa um host Coolify e usa os mesmos nomes. **Não confirmado** para onde `VPS_HOST` aponta | Guard Clause por `current.json`, ou a D7 | P | M | Saída não zero antes de qualquer `docker` |
| N57 | `deploy/ci/test_docker_build.py:105,117,139-151`, `testar-imagem-backup.py:131`† | Weak Assertion: recusa só por código não zero; `docker info` quebrado também passa | Exigir código e mensagem | P | B | Tirar a recusa de `--push` e ver falhar |
| N58 | `Makefile:9`, `ci.yml:133`, `test_docker_build.py:161`† | Divergent Change: o contrato do docker roda nas duas pernas do Postgres e no `make test`, e baixa `moby/buildkit` por digest manual | `make test-docker-build`, uma perna só | P | B | Job `test` |
| N59 | `deploy/ci/diagnostico-fixture.py:18,216`, `testar-diagnostico-fixture.py:24-45,176,186`, `test_snapshot_banco_backup.py:17,42-43`, `test_*` × `testar-*`† | Shotgun Surgery (versões do Postgres no regex), Attribute Outside Init, Speculative Generality, Global Data no import, nomes que escondem o F6 | Limpeza em um PR; rodar tudo por `deploy/ci/rodar-testes.sh` | P | B | Os próprios testes |
| N60 | `scripts/docker-build.sh:109-112`, `Makefile:55-59`† | Speculative Generality: modo `publish` sem chamador; `oci` exige builder sem dizer | Remover, ou testar contra `registry:2` | P | B | `test_docker_build` |
| N61 | `app.json:15-23,71`, `database.json:16-18`† | Misleading Message: `:?defina X no /opt/stillon/.env`, e as variáveis moram no Coolify | Trocar o texto; muda o commit de infraestrutura | P | B | Nenhuma |
| N62 | `stillon-entrada:478,521`, `ci.yml:443`† | Divergent Change: o backup vivo mora no arquivo a aposentar, e o `publish` depende do job `vps`. Reforça a ordem da D7 | **Extract Module** antes de apagar | G | A | `testar-imagem-backup.py` |

### Ordem sugerida

F1 e a conferência do F2 antes de tudo: os dois custam menos de 2 h. Depois F6, F7, F8 e F9, que dão
rede real à Onda 5. O bloco F3, F4, F5 e F16 anda com a D7. Os `N` entram nas ondas que tocam os mesmos
arquivos; N38 a N54 podem ser uma onda própria de testes, depois da Onda 0.

### Decisões novas para o dono

**D9. Onde mora a revalidação de restauro.** (a) trazê-la para a `main`, com aviso de falha; (b) mantê-la
na branch `audit/forwarding-review-20261023` e só corrigir a paginação; (c) aposentá-la e ensaiar à mão.
**Recomendo (a).** Hoje ela roda de uma branch que ninguém olha, por isso a falha durou 3 dias em
silêncio. O custo é levar para a `main` um script denso, e revisá-lo inteiro.

**Registro (2026-11-06): decidida a opção (a), no PR #181.** Com a ampliação do environment para a
`main`, o controle de acesso à chave privada do `age` passa a ser **a revisão do merge**: o que entra
na `main` pode lê-la. Hoje essa revisão não é uma barreira técnica. A `main` exige PR e 9 checks, com
0 aprovações, sem revisão de code owner, e um merge com `--admin` pula os checks. O `CODEOWNERS` do
PR só marca os donos, e o repositório tem um único colaborador. A janela entre ampliar o environment
e tirar dele a branch de auditoria, que não tem ruleset, é o momento mais exposto.

**D10. O papel da Cloudflare na cadeia.** (a) assumi-la: o primeiro Caddy confia só nas faixas dela, e o
acesso direto à origem é fechado; (b) tirar o proxy dela e ficar só com DNS; (c) manter como está e aceitar
balde compartilhado. **Recomendo (a)**, depois de conferir o F2, com o STRIDE refeito. Custa manter a lista
de faixas e aceitar um terceiro que termina TLS. A (b) tira esse terceiro, e perde o que ela absorve.

**D11. Reação a backup sem prova.** O `ci` protege o código; nada avisa o dono quando a revalidação falha.
Recomendo que a falha entre no vigia, ou num check da `main`. Depende da D9.

### O que ficou sem auditar nesta passada

- Nada rodou: suítes, Playwright, Docker. F7 e F14 são leitura, sem execução.
- A configuração do primeiro Caddy e o estado da VPS: não entrei nela, e por isso o F2, o N37 e o N56 estão
  marcados como não confirmados. Na VPS: `docker network inspect edge` e o `Caddyfile` em `/srv/edge`, se ele ainda existir.
- `docs/deploy/vps.md`: lido em trechos; os blocos 1, 2, 4.1 a 4.7, 5, 6, 7.2, 7.3 e 8 só por `grep`†.
- `deploy/coolify/release/receiver.py`: fora, pelo descarte registrado. `deploy/backup/` (fora do `backup.go`) e
  o restante do `stillon-entrada` e do `checar-vps.sh`: só os trechos citados.
- `capturas.spec.ts`, `renderizar.test.tsx`, `esperar-navegacao.test.tsx`, `fontes.test.ts`, `filtros.test.ts`,
  `grupos.test.ts`, `ordem.test.ts`, `esquema.test.ts` e `cliente.test.ts`: não lidos.
- Testes Go: métrica e amostra, não leitura integral de 38 mil linhas.
- O código de #177 e a duração real dos e2e.
