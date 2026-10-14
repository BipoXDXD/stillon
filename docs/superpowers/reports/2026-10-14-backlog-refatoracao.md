# Backlog de refatoração do StillOn (2026-10-14)

Consolida as quatro auditorias de refatoração (A: `httpapi`, `webui`, `cmd`; B: sondador, avisos,
página pública, backup; C: storage, targets, account, auth, config; D: painel e scripts), feitas
sobre a `main` em `909cdc4`/`c1c7f57`. A base é `~/.claude/knowledge/code-quality.md`, seções A e B,
com a B7 como filtro. Nada aqui é código pronto: é a ordem de trabalho proposta, para o dono decidir.

Convenções: os ids das auditorias aparecem entre colchetes (`[A1]` é o achado 1 do relatório A,
`[D-A2]` é o A2 do grupo (a) do relatório D, `[S6]` é o script S6). Risco e ganho: B (baixo),
M (médio), A (alto). "Rede antes" é o teste que precisa existir e estar verde antes do primeiro
passo; quando a cobertura falta, é um characterization test.

Regra para todas as ondas: um PR por linha de tabela (ou por grupo indicado), refatoração sem
mudança de comportamento (R cap. 2, as duas "hats"), suíte verde antes e depois de cada passo, e
cada teste novo visto falhar com o código sabotado.

---

## Defeitos

**Nenhum defeito confirmado.** O possível bug do relatório C ([C2]: `Disable` e `ResetTOTP`
desligam o 2FA limpando conjuntos de colunas diferentes) foi conferido no código e no schema e
**não causa comportamento errado**. As diferenças são intencionais ou inertes:

| Coluna ou efeito | `TOTP.Disable` (`auth/totp.go:296`) | `ResetTOTP` + `ResetTwoFactor` (`users.sql:87`, `account.go:380`) | Efeito da diferença |
|---|---|---|---|
| `totp_secret_enc`, `totp_confirmed_at` | zera | zera | igual |
| par pendente (`totp_pending_secret_enc`, `totp_pending_family_id`) | zera os dois | zera os dois | igual; o CHECK `users_totp_pending_pair` (00011) recusaria meio par |
| `totp_recovery_codes` | `DELETE` inline | `DeleteRecoveryCodes` | igual |
| `totp_last_timestep` | **fica** | zera | inerte, ver abaixo |
| `failed_attempts`, `locked_until` | **ficam** | zera | intencional, documentado em `users.sql:83-86` |
| sessões | ficam | `RevokeAllFamilies` | intencional: o CLI é o caminho de recuperação de conta comprometida |
| `totp_challenge_mark` | fica | fica | igual, documentado em `users.sql:80-81` |

Cenários testados contra o código, todos sem resultado errado:

1. **Segredo pendente que sobra.** Estado: troca de autenticador em andamento → `Disable` → o par
   pendente cai no mesmo `UPDATE` (comentário em `totp.go:316-318`). Um novo `Enroll` grava só
   `totp_secret_enc`; o QR abandonado não confirma nada.
2. **Código de recuperação que continua valendo.** Os dois caminhos apagam a tabela na mesma
   transação. Mesmo que sobrasse um, o `Confirm` de um recadastro chama `substituirCodigos`.
3. **Par pendente que reativa o 2FA.** `ConfirmRotation` exige o par inteiro e a família; os dois
   caminhos zeram o par, e a 00011 impede meio par.
4. **`totp_last_timestep` velho depois do `Disable`.** Estado: login consumiu o timestep T →
   `Disable` (não grava T de novo, por decisão escrita em `totp.go:291-292`) → `Enroll` + `Confirm`
   com outro app no mesmo período. O `Confirm` grava `totp_last_timestep = step` **sem guarda**
   (`totp.go:156`), então o valor velho é sobrescrito antes de qualquer verificação. Entre o
   `Disable` e o `Confirm` a coluna não é lida: `consumirTimestep` só roda com fator confirmado e
   ainda exige `totp_secret_enc = $3`. Sem recusa indevida e sem replay (o segredo é outro).
5. **Bloqueio que fica depois do `Disable`.** O bloqueio é estado do login, não do fator. Quem
   chama o `Disable` está autenticado e acabou de provar senha e fator; sem 2FA, o próximo login
   certo zera o contador como em qualquer conta. O `ResetTOTP` zera porque o operador provou a
   identidade fora da API e o dono costuma chegar travado pelos próprios chutes (A6 do Plano 10).

O que sobra é um smell, não um bug: **Shotgun Surgery** no conhecimento "o que é desligar o 2FA",
com duas fontes em duas camadas (SQL inline e sqlc). Uma coluna de 2FA nova precisa entrar nas
duas, e nada falha se uma esquecer. Vai para o backlog como **R2.6**, com o teste de
caracterização que pega o esquecimento.

---

## Onda 0: testes que dão rede de segurança

Vem primeiro porque refatorar com teste instável não é seguro (R cap. 4): um vermelho intermitente
esconde o vermelho de verdade.

| Id | Smell | Locais | Refactoring e mecânica | Risco | Ganho | Rede antes |
|---|---|---|---|---|---|---|
| R0.1 | Teste não determinístico (prazo de parede) [C1, nota 1] | `account/account_test.go:196` `esperarAlguemNaTrava` (3 s), `:540` `esperarBloqueados` (5 s); `account/reset2fa_test.go:187` | **Substitute Algorithm** no helper: corrida entre dois eventos (a goroutine fecha `fim`; o polling devolve quando há waiter), sem prazo próprio, limitado só por `t.Context()`. Filtrar o waiter pela advisory lock **deste** usuário (`pg_locks.objid`), não por qualquer backend | B | A | sabotar a trava em `ChangePassword` e ver o teste novo falhar sempre, com e sem carga |
| R0.2 | Dependência oculta (hasher real onde não é o assunto) [C1, C14] | `account.go:164` (`auth.VerifyPassword` direto), `CreateUser` e `ResetPassword` (`auth.HashPassword` direto) | **Replace Inline Code with Function Call** para o hasher já injetável (`SetPasswordHasher`), acrescentando o verificador. Testes que não tratam de Argon2id passam um hasher barato | B | A | os testes atuais de `account` verdes antes; um teste que prova que o hasher injetado é usado nos três caminhos |
| R0.3 | Global Data, relógio real [C1, nota 1] | `httpapi/middleware/ratelimit.go:172` (`time.Now`, `Allow`); `auth/argon2.go:38` `var esperaArgon2`; teste `httpapi/account_integration_test.go:373` | **Replace Global Data** por dependência: `rateLimit` recebe o `clock.Clock` e chama `AllowN(clk.Now(), 1)`; a espera do semáforo do Argon2id vira campo injetado. O `newAPI` dos testes passa o `clock.Fake` | M (toca o limitador de produção) | A | teste do limitador com relógio parado: 5 fichas, a 6ª recebe 429; avançar 6 s devolve uma |
| R0.4 | Duplicated fixture + Mystery Guest [C17] | `INSERT INTO users` cru em ~20 arquivos de teste; `reset2fa_test.go` com `"dono@exemplo.com"` literal; `storage/helpers_test.go` privado | **Extract Function** para `testdb.NovoUsuario(t, pool, testdb.Usuario{...})` (test data builder); o teste lê o e-mail do retorno. Migrar arquivo a arquivo, só onde for tocado nas ondas seguintes | B | M | nenhuma além da suíte; não é preciso migrar os 20 de uma vez |
| R0.5 | Triagem de esperas por relógio | `grep` acha `time.Sleep`/`time.After(`/`Eventually` em 31 arquivos de teste (`cmd/stillon/*_e2e_test.go`, `prober`, `notify`, `config/backup_snapshot_test.go` com 5) | Classificar cada ocorrência: **teto de segurança** num `select` (fica) × **decisão pelo relógio** (vira espera por evento ou `clock.Fake`). Só as do segundo tipo viram item. A contagem é do `grep`; nenhuma foi auditada uma a uma | B | M | nenhuma (é levantamento) |

---

## Onda 1: preparatório para o redesign do painel

São os itens do grupo (a) do relatório D: lógica e dados que sobrevivem ao redesign e hoje moram
dentro de telas que o redesign vai reescrever. Todos são refatorações puras do `web/`, com a suíte
do vitest (`npx vitest run`), `tsc -b` e `oxlint` verdes entre os passos.

| Id | Smell | Locais | Refactoring e mecânica | Risco | Ganho | Rede antes | PR |
|---|---|---|---|---|---|---|---|
| R1.1 | Feature Envy / Divergent Change [D-A1] | `components/SeloDeEstado.tsx:7-25` (consumido por `painel/transicoes.ts:1` e 3 telas); `components/Minigrafico.tsx:26-30,57-76` | **Move Function** de `EstadoExibido`, `estadoExibido`, `textoDoEstado` para `lib/estado.ts`, reexportando do selo no primeiro passo. **Extract Function** de `horasDesenhaveis`, `estadoDaHora`, `resumoDasHoras` para `lib/spark.ts`, com `UPTIME_NO_AR = 0.999` e `UPTIME_DEGRADADO = 0.99` | B | A | `Minigrafico.test.tsx` verde antes; testes de tabela das funções puras antes de cortar o SVG | 1a |
| R1.2 | Duplicated Code [D-A11] | `painel/PaginaPainel.tsx:15-32` e `:97-102`; plural em `PaginaPainel.tsx`, `Minigrafico.tsx:63-75`, `mensagens.ts:30-35` | **Extract Function** `contarPorEstado(itens)` em `lib/estado.ts` e `plural(n, um, varios)` em `lib/format.ts` | B | M | `painel.test.tsx` | 1a |
| R1.3 | Regra de negócio no JSX [D-A12] | `painel/PaginaPainel.tsx:66-93` `AvisoDeCanais` (ciclomática 11) | **Extract Function** + **Move Function** `avisosDosCanais(consulta): string[]` pura, com teste por tabela (nenhum canal, pausados 1/n, nenhum verificado, falhando 1/n, erro) | B | A | os testes de tela do aviso verdes antes | 1a |
| R1.4 | Duplicated Code, Primitive Obsession leve [D-A6, D-A13, D-A14, D-A15] | `alvos/esquema.ts:83-87` e `statuspage/PaginaStatusPage.tsx:25-42` (rótulo curto 3×); `incidentes/PaginaIncidentes.tsx:39,48-50,80-81` e `alvos/PaginaDetalhe.tsx:110-112` (duração, filtro); `.replace(/\s+/g,'')` 4×; `lib/format.ts:72-95` `navegadorDe` | **Extract Function** `rotuloCurto(max)` em `lib/rotulo.ts` (com `pontosDeCodigo`), `duracaoDoIncidente`, `type FiltroDeIncidentes` + guarda `filtroDe`, `normalizarCodigo`; **Replace Conditional with table** no `navegadorDe`. A divergência `min(6)` (login) × `min(1)` (configurações) do `esquemaCodigo` ganha um comentário com o porquê, sem mudar | B | M | `esquema.test.ts`, `format.test.ts`, `format.cobertura.test.ts` | 1a |
| R1.5 | Magic Number, lógica de `lib` na tela [D-A16] | `lib/api/consultas.ts:25,35,43,79,86`; `lib/api/sessao.ts:70`; `login/PaginaLogin.tsx:20-30` `destinoSeguro`; `configuracoes/PaginaConfiguracoes.tsx:329-337` | Constantes nomeadas dos intervalos (e `useAbertos` passa a usar `INTERVALO_DOS_INCIDENTES_MS`); **Move Function** de `destinoSeguro` para `lib/navegacao.ts` com o teste junto (é a defesa contra open redirect); **Extract Function** `baixarTexto` | B | M | teste do `destinoSeguro` com os casos de redirect hostil, verde antes de mover | 1a |
| R1.6 | Shotgun Surgery, literal mágico [D-A5] | 11 chaves de cache escritas à mão em 5 telas, contra `lib/api/consultas.ts:5-16` | Passo 1: prefixos em `chaves` e **Replace Inline Code with Function Call**. Passo 2: **Move Function** das mutações para hooks em `lib/api/mutacoes.ts` (`useApagarAlvo`, `useSalvarPagina`...) | B | A | os testes de tela que conferem a invalidação (lista atualizada depois de apagar) | 1b |
| R1.7 | Duplicated Code de política [D-A4] | `configuracoes/PaginaConfiguracoes.tsx:78-90` × `lib/api/cliente.ts:90-96` | **Move Function** para `sessao.renovarOuEncerrar()` com a guarda `estado() === 'autenticado'` num lugar só | B | M | `cliente.test.ts` e `sessao.test.ts` verdes | 1b |
| R1.8 | Long Function, `as T` [D-A9, D-A10] | `lib/api/cliente.ts:82-110` `pedir`; `lib/api/http.ts:24-31` `ApiError` (6 posicionais) | **Extract Function** `repetirComTokenNovo`; **Change Function Declaration** em `pedir<T>(p, esquema)` e `pedirSemCorpo(p)`; **Introduce Parameter Object** no `ApiError` | M (o 401 é denso) | M | `cliente.test.ts` (534 linhas) verde antes de cada passo | 1b |
| R1.9 | Duplicated Code, abstração inconsistente [D-A3] | `PaginaConfiguracoes.tsx:28-48, 204-227, 467-482, 613-629`; `instanceof ApiError &&` 22× | **Extract Function** + **Move Function** para `configuracoes/credencial.ts`; predicados `temCodigo(e, code)`/`comStatus(e, n)` no `ApiError`; `classificarFalhaDoConfirm(e)` devolve uma união | B | A | os testes atuais da tela de configurações | 1c |
| R1.10 | Mutable Data, flags combinadas [D-A2] | `PaginaConfiguracoes.tsx:753-848` (7 `useState`, 3 resets copiados, portão em ternário) | **Extract Function** de um reducer puro `proximoEstadoDo2fa(estado, evento)` em `configuracoes/dialogo2fa.ts`, usado por `useReducer`; **Replace Derived Variable with Query** para `preso`/`portao`; `avisoAoFechar(estado)` pura. Depende de R1.9 | M | A | o teste de 1350 linhas da tela verde antes; tabela de transições do reducer escrita antes de trocar o `useState` | 1c |
| R1.11 | Long Function, Magic Number espelhando o servidor [D-A7, D-A8] | `alvos/esquema.ts:34-77` `urlPermitida` (≥ 27), `:127-176` superRefine (16), literais `30`, `86400`, `1000`, `30000`, `2000`...; `alvos/FormularioAlvo.tsx:36-59` × `statuspage/PaginaStatusPage.tsx:117-125` | **Decompose Conditional** (`ipv4Interno`, `ipv6Interno`, `rotuloUnico`); **Extract Function** por regra do superRefine; objeto `LIMITES` citando `validate.go`; **Extract Function** `errosDosCampos(err, mapear)` pura | B | M | `esquema.test.ts` (243 linhas); um teste por limite on/off point se faltar | 1d |

**Fora da Onda 1, de propósito:** o grupo (b) do relatório D (arquivo de 970 linhas, foco
imperativo, mapas estado → cor, tabelas e segmentos à mão, testes que leem o SVG) não é
refatoração: é lição para o próprio redesign evitar, e fica com as tasks dele. Ligar
`eslint/complexity` e `max-lines-per-function` no `.oxlintrc.json` também espera o fim do redesign
(hoje seriam ~70 violações em telas que vão sumir).

**Passos do plano do redesign afetados** (`docs/superpowers/plans/2026-10-14-redesign-painel-parte1.md`,
não editado):

- **Task 6** (linhas 1455-1614) muda de arquivo e de conteúdo por causa do R1.1: o Step 3 reescreve
  `SeloDeEstado.tsx` com `estadoExibido` e `textoDoEstado` dentro (código em ~1564); depois do
  R1.1 eles moram em `lib/estado.ts`, e o selo só os importa. Mudam junto as "Interfaces" da Task 6
  (1462-1475) e a seção "Interfaces publicadas" (2742-2746).
- **Task 4** (942, 1149-1163, 1189) cita linhas exatas de `FormularioAlvo.tsx:183`,
  `PaginaDetalhe.tsx:231`, `PaginaCanais.tsx:273`, `PaginaStatusPage.tsx:177,184,190`,
  `PaginaLogin.tsx:210,215` e `PaginaConfiguracoes.tsx:499,702,876`. R1.4, R1.5, R1.6, R1.9, R1.10
  e R1.11 deslocam essas linhas. A contagem por arquivo do teste do vermelho (1067) continua
  valendo: nenhum item tira o `DesativarTotp` de `PaginaConfiguracoes.tsx`.
- **Task 5** (1209, 1415-1427) cita `FormularioAlvo.tsx:140-143`, `PaginaStatusPage.tsx:157-160` e
  `PaginaConfiguracoes.tsx:342-345`: deslocam pelos mesmos itens.
- Tasks 1-3, 7-12 não são afetadas (a Task 11 importa `useAbertos`, cujo nome não muda).

A ordem entre a Onda 1 e o redesign é a decisão **D1** abaixo.

---

## Onda 2: backend, Duplicated Code e Shotgun Surgery

Ordenada por ganho/risco. Nenhum item muda contrato HTTP, SQL ou migration; os testes de
integração provam que os valores de `reason` e os cabeçalhos ficam iguais.

| Id | Smell | Locais | Refactoring e mecânica | Risco | Ganho | Rede antes |
|---|---|---|---|---|---|---|
| R2.1 | Duplicated Code, Shotgun Surgery (envelope do 500) [A2] | `default` de `falhou` em `targethandlers.go:349`, `incidenthandlers.go:141`, `channelhandlers.go:178`, `statuspagehandlers.go:160`; `authhandlers.go:385-392` = `accounthandlers.go:220-227`; laço de campos em `targethandlers.go:343-347` = `statuspagehandlers.go:148-152` | **Extract Function** `erroInterno(w, r, op, err)` (com o desvio do Argon2 ocupado) e `camposDe(fields)`. Os switches por domínio ficam (B7) | B | A | teste "sem stack trace no corpo" para cada handler; teste do 429 do Argon2 ocupado |
| R2.2 | Duplicated Code (prólogo do 2FA, 6 cópias), Data Clumps [A1, A12] | `totphandlers.go:30-45, 88-103, 310-329, 380-399, 461-479`; `accounthandlers.go:82-96`; struct `{Password, Code, RecoveryCode}` em `totphandlers.go:317-343, 387-413` | **Extract Function** `(p *provasDaFamilia) abrir(w, r, dst)` que faz 401 → decode → reservar e devolve a reserva para o `defer res.Release()`; `senhaDoDono(...)`; **Extract Class** `provasDoFator` com `segunda()` | M (a ordem das provas é contrato de segurança do Plano 10) | A | sabotar a ordem (reservar depois do Argon2id; decodificar depois de reservar) e confirmar que `totp_*` e `provas_familia_*` quebram; se não quebrarem, escrever o teste antes |
| R2.3 | Duplicated Code de invariante (auditoria de IP) [A6] | `authhandlers.go:378-380`, `accounthandlers.go:100, 203`, `provas.go:57` | **Extract Function** `metaDe(r, px) auth.Meta`; o IP sai da mesma função que o limitador usa | B | M | teste que confere `ip_hash` gravado = IP do limitador atrás de proxy confiável |
| R2.4 | Duplicated Code de invariante de concorrência [C3] | `account.go:149-155, 199-211, 514-526`; `totp_rotation.go:78-87, 204-215`; par e-mail → trava em `account.go:341-352, 383-395` | **Extract Function** `travarFamiliaViva(ctx, q, user, family, now) error` com sentinela única traduzida por quem chama; `travarUsuarioPorEmail` | M | A | os testes de "rechecks the family under the lock" (depois do R0.1, determinísticos) |
| R2.5 | Duplicated Code + Magic Literal (atributos de cookie) [A10] | `authhandlers.go:316-371` | **Extract Function** `cookieDeAuth(nome, valor, secure)`; `const pathDeAuth`; um `#nosec` só | B | M | os testes que conferem atributos do cookie posto **e** do apagado |
| R2.6 | Shotgun Surgery ("desligar o 2FA" em duas camadas) [C2, ver Defeitos] | `auth/totp.go:320-330` (SQL inline) × `users.sql:87-95` `ResetTOTP` + `DeleteRecoveryCodes` | Duas queries sqlc nomeadas, `DisableTOTP` e `ResetTOTP`, no mesmo arquivo e lado a lado, com a diferença do bloqueio e do timestep escrita no comentário; o `DELETE` de códigos (3 cópias) usa só `DeleteRecoveryCodes`. Depende de **D2** | M | M | characterization test: depois de `Disable` e de `ResetTwoFactor`, as colunas de fator e de troca pendente têm o mesmo valor, e o recadastro no mesmo timestep confirma |
| R2.7 | Duplicated Code + Primitive Obsession (auditoria) [C5] | `auth/store.go:187` `insertAudit` × `account.go:552` `audit`; 12 ações em literais | **Substitute Algorithm** sobre `InsertAuditEntry`; `type AuditAction string` com constantes num pacote só | M | M | teste por ação que lê `audit_log` (ação e `entity`) |
| R2.8 | Data Clumps, Duplicated Code (janela do rollup) [B2, C4 parte] | `statuspage/public.go:132-164`; `targets/summary.go:64-78`; a série do detalhe será a 3ª | **Introduce Parameter Object** `rollupcut.Janela{Desde, Corte, Ate}` via `rollupcut.Nova(agora, desde, marca)`. Fazer no PR da série (Rule of Three) ou logo antes | B-M | A | testes do `rollupcut`; teste de borda do corte (hora cheia, rollup ausente) no resumo e na página pública |
| R2.9 | Duplicated Code de vocabulário de estado [B7, B15, C11 parte] | `uptime` em `public.go:219, 225` e `summary.go:131`; literal `"down"` em `summary.go:120`; `statuspage.State` copia `targetstate.State` (`public.go:26-35`) | **Extract Function** `uptime(ok, total) *float64` em `rollupcut`; `string(targetstate.Down)`; alias `type State = targetstate.State` | B | M | teste de `uptime` com total 0, ok 0, ok = total |
| R2.10 | Duplicated Code + Shotgun Surgery no notify [B6, B8] | `notify/dispatcher.go:291` `adiar`, `:337` `entregar`, `:382` `tentar`; 25 literais de status, `persistir` (:455) sem `case failed` | Passo 1: **Replace Primitive with Object** `type statusNotificacao` com `case failed` explícito e `default` que falha (Prática 7). Passo 2: **Extract Function** `decidirAposFalhaTransitoria` e `registrarDesfecho` | M | A | testes de tentativas: 429 perdoado, `MaxAttempts`, backoff; o `default` novo visto falhar |
| R2.11 | Magic Number / Shotgun Surgery (constantes com duas fontes) [A8, A13, B9, B15, B16, C12 parte] | `routes.go:117,143,152,210,304` (limites); `cmd/stillon/ops.go` e `backup_snapshot.go` (`return 0/1`, "interrompido" 3×); `notify/message.go:12` `causas` × `prober.ErrorKind`; `fatorLacuna` × `StaleIntervals`; `notify.go:140,144` × `safedial`; Argon2 em `config.go` × `auth` | **Replace Magic Literal** com os comentários junto da constante; códigos de saída num arquivo comum do `main`; `causas` indexado por `prober.ErrorKind` com teste que cobre todos os valores; constantes do Argon2 num pacote sem dependências | B | M | teste de `causas` (todo `ErrorKind` tem texto) visto falhar com um valor novo |
| R2.12 | Duplicated Code de ordem de middleware, paginação [A9, A4] | `routes.go:211-215, 305-308`, `publicpage.go:54-57`; `targethandlers.go:160-194` × `incidenthandlers.go:70-111` (+ `targets.Reason*` usado por incidentes) | **Extract Function** `externa(h)` com o porquê da ordem; `lerPagina` e `proximoCursor`; **Move Function** dos motivos de parâmetro para o `httpapi` | B | M | testes de trace_id e cabeçalhos nas 3 cadeias; limite e cursor em `targets_` e `incidents_integration_test` |
| R2.13 | Comments (ids de revisão, comentário que mente), layout [B11, A16, A17, B17 parte] | 36 comentários com "C6 da revisão", "Review Focus 4"...; `middleware/secheaders.go:7-9`; `backup_snapshot.go` (goimports); `backup.go:36,361,370,385` (linhas de 200 colunas) | Apagar o id e manter o porquê; reescrever o comentário da CSP; `goimports -w`; um campo por linha | B | B-M | nenhuma (comentário e formatação) |

---

## Onda 3: backend, Long Function e Flag Argument

Só o que passa do gatilho por motivo além de `if err != nil` (B7). Cada item extrai por clareza,
com o nome mais abstrato que o código, e mantém a ordem dos passos visível em quem chama.

| Id | Smell | Locais | Refactoring e mecânica | Risco | Ganho | Rede antes |
|---|---|---|---|---|---|---|
| R3.1 | Long Function + Repeated Switches [A3, A14] | `cmd/stillon/user.go:122-248` `runUser` (cyc 33, cog 43); `lerSenha` (`:271-331`) | **Split Phase** (ambiente → operação); tabela `map[string]subcomandoUser`; mensagem repetida vira constante; `lerSenha` só despacha para `lerDoTerminal`/`lerDoPipe` | B-M | A | `user_test.go` (14) e `reset2fa_test.go` (4) conferem códigos de saída |
| R3.2 | Long Function, Split Phase ausente [B1] | `statuspage/public.go:131` `projetar` (cog 25) | **Split Phase**: leitura para `leituraPublica` dentro da transação; `montarView(leitura, desde, agora)` pura. Junto com R2.8 | B | A | teste da página pública com dia sem dados, hora corrente, alvo pausado; depois, testes de `montarView` sem banco |
| R3.3 | Long Function [C11] | `targets/summary.go:63-140` `Summary` (cog 34) | **Extract Function**: `sparkPorAlvo`, `horaCorrentePorAlvo`, `resumoDe`. Depois de R2.8 e R2.9 | B | M | testes do resumo com alvo sem check e hora corrente |
| R3.4 | Long Function [C12] | `config/config.go:119-365` `Load` (cyc 48, cog 73, 7 closures sobre `errs`) | **Combine Functions into Class**: `type leitor struct{ getenv; errs }` com um método por variável; guardar `PublicBaseURL` parseada (some o reparse em `EhHTTPS`) | B | M | os testes de `config` verdes; um teste por variável que agora fica testável sozinha |
| R3.5 | Long Function [C13, A11] | `account.go:140-227` `ChangePassword` (cyc 22); `authhandlers.go:122-135` rehash no `Login` | **Extract Function** `provarSegundoFator` e `gravarTroca`; `regravarHash` com guard clauses. O log do erro hoje descartado de `auth.HashPassword` é mudança de comportamento: commit separado | M | M | R0.1 e R0.2 feitos; `login_race_integration_test` (CAS) |
| R3.6 | Long Function, Duplicated Code [B5] | `prober/scheduler.go:410` `avaliar` (cyc 15) | **Extract Function** `abrirIncidente` e `fecharIncidente`; `avaliar` fica com a histerese | M (dentro da transação do check) | M-A | testes de abertura e fechamento com `n == 1` e `n == 0` no enfileiramento |
| R3.7 | Divergent Change, Large Class [B4] | `prober/scheduler.go` (593 linhas): despacho × gravação | **Extract Class** `gravador` em `gravacao.go`; o `Scheduler` chama `gravador.gravar(a, em, res)`. Depois de R3.6 | M | A | testes de corrida do scheduler verdes 20× seguidas (`-count=20`) antes |
| R3.8 | Long Function [B10] | `statuspage/service.go:85` `Update` (cog 30) | **Extract Function** `aplicar(patch, atual) Input` pura | B | M | testes do PATCH: ausente não altera, `null` limpa |
| R3.9 | Long Function, Data Clumps [A7, C10] | `targethandlers.go:262-327` `Checks`; literal `Target{…}` 3× em `targets/service.go` | **Split Phase** `lerIntervalo(q, agora)` com `const janelaPadrao`; **Extract Function** `targetDe`, `fieldsDe` | B | M | `targets_integration_test` (window, from, to) |
| R3.10 | Flag Argument [A5] | `authhandlers.go:260-278` `issue`, `:300-314` `grantSession` | **Remove Flag Argument**: `issue` recebe a função que emite; `grantSession` vira `entregarSessao` + `entregarLogin` | M | M | sabotar "assina antes do Set-Cookie" e ver `auth_` e `dispositivo_integration_test` quebrarem |

---

## Onda 4: Primitive Obsession e tipos

Mais larga e mais lenta: cada item atravessa pacotes. Começa pelo par mais perigoso.

| Id | Smell | Locais | Refactoring e mecânica | Risco | Ganho | Rede antes |
|---|---|---|---|---|---|---|
| R4.1 | Primitive Obsession, parse, don't validate [C9] | `targets/validate.go:85-183, 282`; `IntervalSec`/`TimeoutMS` convertidos à mão em `summary.go:113`, `statuspage/public.go:205`, `prober/scheduler.go:304` | **Replace Primitive with Object**: `parseProbe(fields) (Probe, *ValidationError)` com URL parseada e `time.Duration`; conversão só no sqlc e no JSON | M | M | `validate_test` com on/off point de cada faixa; teste que a URL validada é a sondada |
| R4.2 | Primitive Obsession (chave como `[]byte`) [C15] | `crypto/hash.go:29`, `auth/session.go:77-81`, `account.go:141, 509` | **Replace Primitive with Object** `crypto.IPHasher` criado no boot, com erro em vez de panic | M | M | teste do boot sem chave e com chave curta |
| R4.3 | Temporary Field [C14] | `account.Service` com `totp`/`ipKey` nil no CLI | **Extract Class** `account.Operador` (CLI) e `account.Service` (API) com dependências obrigatórias | B | M | `user_test` e os testes de `account` |
| R4.4 | Primitive Obsession, Mysterious Name [B9] | `notify/notify.go:38` `Event.Kind` × `notify.Kind` | **Rename Field** `Event.Kind` → `Event.Evento` com tipo `incident.Event` | B | M | compilação + testes do notify |
| R4.5 | Primitive Obsession (ids trocáveis) [C16] | `account.ChangePassword(ctx, userID, familyID…)`, `TOTP.StartRotation/ConfirmRotation`, `targets.Get/Update/Delete`, `auth.Session` | **Replace Primitive with Object** `type FamilyID` primeiro, depois `UserID`, `TargetID`, com `go_type` override no `sqlc.yaml`. Depende de **D5** | M | M | compilação; nenhum teste novo (o compilador é a rede) |

---

## Onda 5: SQL

Toda query tocada leva `EXPLAIN (ANALYZE, BUFFERS)` antes e depois, com o resultado no PR, e o
teste de RLS correspondente. Migration nova só depois da 00014 da vinculação Discord, se ela vier
antes (a numeração está reservada).

| Id | Smell | Locais | Refactoring e mecânica | Risco | Ganho | Rede antes |
|---|---|---|---|---|---|---|
| R5.1 | Duplicated Code, Shotgun Surgery, comentário desodorante [B3, C4] | fórmula da hora (`floor(epoch/3600)`, `status <> 'down'` 11×, `percentile_disc … FILTER`) em `maintenance.sql:9-15` (ConsolidateRollups), `targets.sql:110-127` (ListSummaryRawHours), `statuspages.sql:85-114` (ListPublicDailyUptime, ListPublicLatency); 3 variantes da marca d'água | Passo 1, sem migration: **characterization test** que compara o rollup consolidado com a leitura bruta da mesma hora, nas três consultas. Passo 2 (**D3**): **Extract Function** no banco, `hora_utc(timestamptz)` e `check_ok(text)` `IMMUTABLE`, e a agregação horária numa função SQL inlinável que deixa o filtro de `checked_at` visível para o pruning; se for view, `security_invoker = true` | M | A | o teste do passo 1; `EXPLAIN` das três consultas com a partição podada |
| R5.2 | Duplicated Code de política de segurança [C6] | `users.sql` `ReplaceUserHash` × `SwapUserHash` (mesmo `SET`) | **Parameterize Function**: `SetUserPassword` com `sqlc.narg(old_hash)` opcional na WHERE | M | M | testes do set-password e da troca: desbloqueia, derruba 2FA pendente e troca pendente, CAS do hash antigo |
| R5.3 | Valor sentinela (SQL regra 12) [C8] | `targets.sql:69-71, 90`, `maintenance.sql:30`, `statuspages.sql:54-55, 69`; Go em `summary.go:117`, `maintenance.go:122` | Tirar o `coalesce` para `''`/`'epoch'` e forçar nulidade (`sqlc.narg` ou override para `*time.Time`); o Go lê `nil` | M | M | testes com alvo sem check e sem rollup |
| R5.4 | `SELECT *` (SQL regra 1) [C18] | `targets.sql:12,18,23`, `channels.sql:44`, `incidents.sql:10,19`, `statuspages.sql:5` | Listar as colunas; o código gerado não deve mudar | B | B | `sqlc generate` + `git diff` vazio no Go gerado |
| R5.5 | Divergent Change (duas camadas de persistência) [C2] | ~22 SQL em string em `auth/totp.go` (13), `totp_rotation.go` (3), `store.go` (5), `user.go:262` | **Move Statements** para `queries/*.sql` (sqlc), uma query por PR, com os testes de RLS e de corrida. Depende de **D2**; R2.6 é o primeiro passo | M | A | o teste de cada query (corrida e RLS) verde antes de mover |

---

## Onda 6: scripts de shell

Tudo verde no `checar-vps.sh` (job `vps` do CI, que não roda nativo no Mac) entre os passos.

| Id | Smell | Locais | Refactoring e mecânica | Risco | Ganho | Rede antes |
|---|---|---|---|---|---|---|
| R6.1 | Duplicated Code, Mysterious Name [S6, S7] | `falhar`/`falhas`/`ok` em 7 `checar-*`/`testar-*`; `testar-ssh-real.sh:30-31` com outro sentido; binário falso no PATH 3× | **Extract Function** para `deploy/ci/lib/checagem.sh` (`falhar`, `ok`, `concluir`, `instalar_falso`); **Rename Function** `abortar` no `testar-ssh-real.sh`; incluir o `lib/` na exclusão do `checar-workflows.sh` | B | M | cada `checar-*` sabotado uma vez antes e depois |
| R6.2 | Magic Number, Shotgun Surgery [S5, S9, S10] | `85` em `stillon-entrada:25,115,601-605`, `backup.sh:18,181-186`, `ci/vps.sh:197`; códigos de saída descritos em 3 arquivos; `65532` 4×; `600`/"10 min" | Constantes nomeadas com os dois sentidos de "85%" separados (`LIMITE_DISCO_PCT`, `LIMITE_VOLUME_BANCO_PCT`); um comentário-contrato dos códigos; `UID_NONROOT`, `ESPERA_DA_TRAVA_S`, `hex_aleatorio` | B | M | `checar-vps.sh` confere os códigos |
| R6.3 | Duplicated Code, shell além do ponto (segurança em awk) [S2] | `deploy/backup/backup.sh:39-105` (decodificador hex 2×, allowlist da libpq) | **Move Function** do parse e da recusa de parâmetros para o helper Go (`stillon backup-snapshot`), com teste table-driven; o shell só orquestra. Mínimo: `decodificar_pct` em awk uma vez | M (contrato `ORIGEM_URL` entre script e helper) | A | casos de URI hostil (parâmetro proibido, percent-encoding inválido) no `checar-vps.sh` antes de mover |
| R6.4 | Long Function [S3, S4, S11] | `stillon-entrada:290-398` `deploy`, `:499-612` `backup`, `:351-357`; `ci/vps.sh:173-194` | **Extract Function** por fase (`receber_imagem`, `reverter`, `subir_descartavel`...), `gravar_publico`, `descartar` | M (roda como root) | M | `checar-vps.sh` e `testar-vps.sh` verdes no job `vps` |
| R6.5 | Mysterious Name, dado duplicado [S12, S13] | `checar-vps.sh` `q`, `cu`, `dc`, `b64`; credenciais de demo em `Makefile:52`, `compose.yaml:86-88`, `checar-compose.sh`, `ci.yml:359` | **Rename Function**; `DEMO_EMAIL`/`DEMO_SENHA` no Makefile; alvos `web-check` e `checar-scripts` | B | B-M | nenhuma além dos próprios checadores |
| R6.6 | Long Function no nível do arquivo [S1] | `deploy/ci/checar-vps.sh` (1201 linhas, ~45 blocos sem isolamento) | Depende de **D4**: **Split Phase** em arquivos por bloco com o `lib/` de R6.1, ou reescrita em teste Go com `t.Run` | A (é o portão da produção) | A | paridade caso a caso: cada bloco sabotado antes e depois |

---

## Descartes

- **[B13] `backup.run` com Combine Functions into Class**: a B7 aceita a rotina longa com justificativa escrita, que existe (`backup.go:116-119`); risco alto (processos filhos e sinais, ensaio só na VPS).
- **[B12] Extract Function no `Tick`**: risco médio-alto (o panic "released more than held" derruba o processo) para ganho médio; reabrir só se o despacho mudar.
- **[B14] polimorfismo sobre `notify.Kind`**: Speculative Generality antes da terceira variante; a vinculação Discord (D1-D3) é o gatilho.
- **[A15] Introduce Parameter Object no `NewAuthHandlers`**: a B7 só agrupa parâmetros que formam um conceito; segredo, cookie seguro e proxies não formam.
- **[A14, parte] Replace Type Code with Subclasses no `lerSenha`**: duas variantes, duas funções bastam (Padrões 2); a extração entrou no R3.1.
- **[C7] função SQL `sessao_viva`**: 5 ocorrências num arquivo só; a função pode atrapalhar o planejador e o ganho não paga a migration.
- **[S8] `yq` no `checar-workflows.sh`**: dependência nova no runner e no Mac para um parse que hoje passa; reabrir no primeiro falso positivo.
- **[D] decompor `criarSessao`**: módulo profundo (interface pequena, 415 linhas de teste); justificar em vez de decompor.
- **[D] unir `HoraDoSpark` e `Hora`**: espelham DTOs Go diferentes; coincidência, não DRY (PP T9).
- **[D] dividir a `api-falsa`**: só quando ela mudar de novo.
- **[A, notas] tabelas `tipos`/`comprimiveis` do `webui` e o reparse de IP no `clientip.go`**: ganho baixo, e o segundo é código de segurança com 22 testes.
- **[B, notas] mapeamento de `history.List`/`Ack`**: duas ocorrências, abaixo da Rule of Three.
- **[C, nota 3] `nomeDeCabecalho` × `validHeaderName`**: regras diferentes de propósito.
- **`TOTPVerify`, `run` do `main.go`, `Routes`, os `falhou` por handler**: já excluídos pelos relatórios pela B7 (sequência coesa, switch único por domínio).

---

## Decisões para o dono

**D1. Ordem entre a Onda 1 e o redesign.**
(a) Onda 1 inteira antes do PR 1 do redesign, regenerando as linhas citadas nas Tasks 4, 5 e 6 no
mesmo PR; (b) Onda 1 entre o PR 2 (navegação) e o PR 3 (primeira tela) do redesign, com só o R1.1
antes da Task 6; (c) só `lib/` e arquivos novos antes, e a troca das chamadas dentro de cada task do
redesign. **Recomendo (b):** os PRs 1 e 2 mexem pouco nas telas (troca de `tipo` de botão e caixa
de marcar), então as linhas citadas ainda valem quando forem executados, e as telas chegam limpas ao
PR 3. Custo: a Task 6 muda antes de começar (o selo importa `estadoExibido` de `lib/estado.ts`), e o
redesign espera uns 4 PRs pequenos entre o PR 2 e o PR 3. A (c) cria duplicação temporária entre a
tela e o `lib/`.

**D2. Migrar o SQL inline do `auth` para o sqlc (o "R2" que o código já anuncia).**
**Recomendo migrar, uma query por PR, começando pelo R2.6** ("desligar o 2FA"). Ganho: uma camada
de persistência, o `go vet`/`sqlc` vendo os tipos, fim das duas fontes da auditoria. Custo: ~22
queries de código de segurança, cada uma com o teste de corrida e de RLS rodando antes e depois;
uns 5 a 8 PRs. Alternativa: deixar como está e só documentar; o Shotgun Surgery fica.

**D3. A fórmula horária do rollup: função SQL ou só teste de equivalência.**
**Recomendo o passo 1 do R5.1 agora (teste que compara rollup e bruto, sem migration) e decidir a
função SQL depois do `EXPLAIN`.** O teste já transforma o comentário "a MESMA fórmula" em
verificação. Custo da função: uma migration, risco de o planejador perder a poda de partição, e a
RLS a conferir; ganho: uma fonte da fórmula para três consultas e para a série do detalhe que vem aí.

**D4. `checar-vps.sh`: dividir em arquivos agora ou reescrever em Go.**
**Recomendo dividir em arquivos por bloco com um `lib/` comum (R6.1 + R6.6 em shell)**, e o Go só se
o arquivo voltar a crescer. Custo da divisão: baixo, paridade fácil de conferir. Custo do Go: alto,
reescrever o portão da produção caso a caso, embora resolva o "não roda no Mac".

**D5. Tipos de id (`FamilyID`, `UserID`, `TargetID`) com override no sqlc.**
**Recomendo fazer só o `FamilyID` primeiro**, o par trocável mais perigoso (`userID, familyID` lado
a lado em `ChangePassword`, `StartRotation`, `ConfirmRotation`). Custo: mudança larga no código
gerado e nas assinaturas, difícil de reverter pela metade; ganho: trocar os dois deixa de compilar.
Se o `FamilyID` não pagar a conta, os outros não entram.
