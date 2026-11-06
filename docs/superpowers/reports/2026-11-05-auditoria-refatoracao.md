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
