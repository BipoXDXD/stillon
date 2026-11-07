# Auditoria para a refatoração geral (2026-11-05, atualizada em 2026-11-07)

Base desta versão: `main` em `7fbe42c` (2026-11-07). A primeira versão (2026-11-05) tomou `8d9b90b`, e a
complementar (2026-11-06), `27b1005`. Só auditoria: nenhum código mudou, e nenhuma suíte rodou (nem
Docker, nem `make test`). Substitui, como ordem de trabalho, as Ondas do
[backlog de 2026-10-14](2026-10-14-backlog-refatoracao.md), que continua valendo para o detalhe de
mecânica e para os Descartes e Defeitos de lá. Os ids `R…` são daquele backlog; `N…` e `F…` são desta
auditoria, e foram mantidos para as referências antigas continuarem valendo. Critério:
`~/.claude/knowledge/code-quality.md`, seções A e B, com a B7 como filtro, e `typescript.md` para o painel.

Legenda: custo **P** (até 2 h), **M** (até 1 dia), **G** (mais de 1 dia), estimado sem medir. Risco da
refatoração **B**, **M**, **A**. Um PR por item, testes verdes antes e depois, teste novo visto falhar
com o código sabotado. Onde falta teste, a coluna diz **sem teste**: o primeiro passo é escrevê-lo.

## Medidas

Tudo medido nesta versão, em `7fbe42c`, salvo o que está marcado.

| Medida | Valor |
|---|---|
| Go de produção / de teste | 17.552 / 40.005 linhas; 656 funções (sem o `storage/db` gerado, 4.168 linhas) |
| Painel de produção / de teste | 9.108 / 13.721 linhas de `.ts` e `.tsx`, sem CSS nem `e2e/` (a conta de 11-05 pode ter outro critério) |
| Go com ciclomática > 7 | 68 funções (`gocyclo`, só produção); 56 com 10 ou mais |
| Maiores funções Go (ciclomática, linhas) | `config.Load` 48, 247; `backup.run` 37, 144; `runUser` 33, 127; `targets.validate` 25, 70 (era 22); `ConfirmRotation` 22, 95; `ChangePassword` 22, 88 |
| Go com mais de 60 linhas | 23 funções. Depois do `Load`, a maior é `httpapi.Routes`, 179 linhas planas (ver Descartes) |
| Go: aninhamento e parâmetros | Nenhuma função passa de 3 níveis de `if`, `for`, `switch` ou `select` (contagem por AST). Mais de 6 parâmetros: `NewAuthHandlers` 9, `audit` 8, `novoDump` 7, `serieHoraria` 7 |
| TS (`oxlint`: `complexity` > 7, `max-depth` > 3, `max-lines-per-function` > 60 sem linhas em branco nem comentário) | 29 funções com complexidade acima de 7; 22 com mais de 60 linhas; 3 blocos acima de 3 níveis (`FormularioAlvo.tsx:48,53`, `PaginaStatusPage.tsx:125`). Maiores: `urlPermitida` 29, `FormularioAlvo` 21, `PaginaConfiguracoes` 21, `Formulario` da status page 19, `esquema.ts:129` 17, `enviar` da status page 16, `navegadorDe` 15 |
| Maiores componentes (linhas, de `function` ao `}`) | `PaginaConfiguracoes` 223; `Formulario` da status page 204; `PaginaCanais` 201; `FormularioAlvo` 190; `DetalheDoAlvo` 188; `criarSessao` 184; `PaginaLogin` 167; `DetalheDoHeartbeat` 155; `PaginaIncidentes` 145 |
| `internal/httpapi` | 266 funções `Test`; 253 linhas com `newAPI*`; zero `t.Parallel` no repositório (só um comentário em `apierr`); a fixture paga um Argon2id por processo desde o #200 |
| Suíte do `httpapi` | 488 s para 297 s, medianas informadas no #200; não reproduzi |
| Esperas por relógio nos testes Go | 19 `time.Sleep` em 11 arquivos (eram 22 em 14) |
| CI, run da `main` do #206 (`42347359921`) | `test` 4 min 07 s (Postgres 18) e 3 min 18 s (17); `vps` 5 min 50 s; `e2e` 9 min 52 s (era 5 min 44 s: a jornada de heartbeats entrou); `publish` 1 min 40 s |
| Argon2id da produção (64 MiB, t=3, p=2), arm64 | 0,09 s sem `-race`; 1,2 a 2,5 s com `-race` (de 11-05, não remedido) |

**Como medi, sem instalar nada.** Go: o `golangci-lint` 2.13.2 que já está em `~/go/bin`, só com o
linter `gocyclo` e `min-complexity: 8`, com a configuração fora do repositório; linhas, parâmetros e
aninhamento por um programa de AST em `go/ast` fora do repositório. Painel: o `oxlint` que já está em
`web/node_modules`, com as quatro regras acima e a configuração fora do repositório. O `.golangci.yml`
e as regras do oxlint não existem no repositório: é o item N6.

## O que mudou desde 2026-11-05

### Resolvido e fora do backlog

| Id | PR | O que ficou |
|---|---|---|
| N1 | #200 | `hashDaFixture` (`httpapi/auth_integration_test.go:53`), `sync.OnceValue` com os parâmetros reais; `argon2_integration_test.go:38` prova que o hash não pede rehash. O commit informa 488 s para 297 s |
| N4 | #199 | `config/scripts_test.go:126-167`: um teto de 60 s pelo `t.Context()` e `WaitDelay`, no lugar dos 3 s por caso |
| N54 (a paginação) | #188 | `paginar` em `web/src/test/api-falsa.ts:212`, usada em `/targets` e `/heartbeats` (`:449`, `:521`). A "montagem repetida" do item não foi reconferida e saiu do backlog |
| F5, F6, F7, F8, F9, F10, F16 | #185, #186, #188, #189 | Como no estado de 2026-11-06 |
| F11 | #196 | Teste de contrato da API falsa contra `internal/apierr/errors.go` |
| F12 e a D12 | #194, #205 | `httpapi/optional.go:19-46` decodifica estrito e recusa `null` em campo obrigatório de PATCH, em alvos, heartbeats e status pages: 400 `required`, antes de procurar o recurso e sem gravar |
| F13 | #192 | `deploy/ci/checar-workflows.sh` recusa filtro `paths` inexistente; o filtro do `coolify.yml` cita o `ci.yml` |
| F14, F15 | #195 | `web/e2e/pilha.ts` usa `ps` no lugar de `/proc` e guarda o rastro do container |
| F17 | #193 | `deploy/ci/diagnostico-fixture.py` itera uma cópia do registro, sob a trava |
| R0.3, R1.1, R1.2, R1.3 | antes de 11-05 | Já constavam como resolvidos |

Defeito novo, que a auditoria não tinha: o #203 recusa host cujo domínio depende da tabela do IDNA (cadastro,
PATCH e sondagem). Dele nascem os itens R4.1 (ampliado), N63 e N64.

Os smells que os PRs #168 a #177 (heartbeats, tags, painel denso, ordem da tabela) e o #203 criaram estão nos
itens marcados "(novo)", de N63 a N71. O #206 (rótulo "Abertos") não gerou item.

### Parcial: o que sobrou

| Id | O que falta |
|---|---|
| N3 (resto do R0.1) | `testdb.EsperarBloqueados` (`storage/testdb/testdb.go:109`, #199) tem 12 chamadas; sobra o `account` e dois laços com prazo de parede. Ver o item no grupo 3 |
| N46 | `/start`, `/fail`, tags e descrição entraram na jornada real (#168, #179); faltam `/ping/{token}/{exit_code}`, cron e a prévia |
| R0.5 | Os `Sleep` caíram de 22 para 19; o levantamento não foi refeito |
| F1 | Corrigido no #181, mas a primeira revalidação agendada (2026-11-07 02:22 UTC) falhou, como a manual de 2026-11-06 23:00; a manual de 2026-11-07 04:37 passou. A causa das duas falhas não foi aberta (o `gh` não devolveu o log) |
| F2, F4 | Ver "Defeitos que seguem abertos" |
| R1.4, R1.5, R1.8, R1.11, R6.3 | Parciais desde 11-05, com o resto no backlog |

### Do backlog antigo (ids `R…`)

Resolvidos: R0.3, R1.1, R1.2, R1.3. Parciais: R0.1 (virou N3), R0.2 (virou N2), R1.4, R1.5, R1.8, R1.11, R6.3.
Todos os outros seguem abertos e estão no backlog abaixo. **Recontagem, não piora:** o R2.1 tem 9 pontos, e
não 7 (`provas.go:59` e `pinghandlers.go:87` já existiam e não tinham sido contados). **Pioraram de fato:**
`targets.validate` (ciclomática 22 para 25, com as tags e o IDNA) e as asserções vazias do N53 (11 para 18).

## Defeitos que seguem abertos

Não são refatoração, mas tocam os mesmos arquivos e vêm antes dela.

| Id | Estado | Local | O que falta | Custo | Risco | Teste |
|---|---|---|---|---|---|---|
| F1 | Corrigido, sem prova agendada verde | `deploy/backup/revalidar_restauro.py` (#181), `.github/workflows/revalidar-backup.yml:17` | Uma execução agendada verde; abrir a causa das duas falhas | P | B | `deploy/ci/test_revalidar_backup.py` cobre o script; a prova é o run agendado |
| F2 | Parcial (#180, #182, #184, #186, #191) | `deploy/coolify/app.json:25`, `deploy/vps/Caddyfile`, `deploy/ci/checar-borda-cloudflare.sh` | A cadeia de borda tem teste e o modelo de ameaça cita a Cloudflare, que termina o TLS; falta conferir a configuração do primeiro salto e a D10, que é do dono | P a M | M | `checar-borda-cloudflare.sh`; dois clientes de origens diferentes em buckets distintos |
| F3 | Sem correção | `deploy/coolify/app.json:101-102`, `README.md:331`, `deploy/coolify/README.md:13` | O Coolify fixa o commit de infraestrutura e o release só troca imagens: uma mudança no Caddyfile ou no compose passa no CI e não chega à produção. Está só documentado | M | A | **sem teste** que falhe com o commit fixado atrás do Caddyfile |
| F4 | Parcial (#186, #189) | `docs/deploy/vps.md:4179,4263` (`docker volume rm`, `dropdb`), `docs/deploy/mac.md` | O aviso no topo existe; falta mover o runbook para `docs/deploy/legado/` e escrever o runbook curto do Coolify. Depende da D7 | M | B | **sem teste**: o `checar-docs.sh` sugerido não existe |

Fatos de produção de 2026-11-06, ainda válidos: a Cloudflare está na frente de `stillon.tech` (visto por `curl -I`
de fora); o ping público atravessa a Cloudflare sem desafio; os backups diários existem (último agendado
verde: run `42233497757`, de 2026-11-06).

## Backlog, em ordem de risco da refatoração

Cinco grupos, do mais para o menos arriscado: **1** quebra de contrato, **2** segurança, **3** concorrência,
**4** banco, **5** estilo (5a backend, 5b painel, 5c testes, 5d deploy, scripts e docs). Dentro do grupo, o
risco maior vem primeiro e, empatando, o custo menor. Cada item é um PR.

### 1. Quebra de contrato

Mudam JSON, motivo de erro, status HTTP ou nome que o painel lê.

| Id | Local | Smell | Refactoring | Custo | Risco | Teste |
|---|---|---|---|---|---|---|
| N8 | `targets/validate.go:19-21,31`, `heartbeat/heartbeat.go:51-53,59`, `statuspage/statuspage.go:33-36,85`, `notify/service.go:44-45,51`; `httpapi/incidenthandlers.go:73,77,85` importa `targets` só pelos motivos | Duplicated Code, Alternative Classes with Different Interfaces: 4 `ValidationError` e 4 jogos de `Reason…` | Extract Class: pacote de validação sem dependências, com `Fields []FieldError` | M | M; A se unificar o `reason` (D13) | `entrada_estrita_integration_test`, `targets/validate_test`, `heartbeat/heartbeat_test`, `statuspage/service_test`; JSON de erro idêntico, campo a campo |
| N66 (novo) | `targets/validate.go:95-103`, `heartbeat/service.go:120-128` (`ValidateName`), `statuspage/statuspage.go:130-143` (`texto`) | Duplicated Code, terceira cópia (vazio, comprimento, invisível). A cópia do heartbeat devolve `out_of_range` onde as outras devolvem `too_long`, e o painel mapeia cada um (`web/src/lib/api/mensagens.ts:58-59,79`) | Extract Function `rotulo.ValidarTexto(s, max)`, depois do N8 | P | M, o motivo é contrato (D13) | `targets/validate_test`, `heartbeat/heartbeat_test`, `statuspages_integration_test`; um caso por motivo |
| R2.12a + N34 | `httpapi/heartbeathandlers.go:420` `paginaDe`, `targethandlers.go:165-186`, `incidenthandlers.go:70-89` (`List`, cyc 16, 63 linhas); cursor em `targethandlers.go:197-198`, `incidenthandlers.go:109-110`, `heartbeathandlers.go:216,345,380`. N34: `heartbeathandlers.go:225-251` (a prévia recusa parâmetro desconhecido) × `:420` (aceita) | Duplicated Code, três cópias (a Rule of Three passou); Inconsistent Validation | Move Function de `paginaDe` para um arquivo comum, com o teto por parâmetro; Extract Function `proximoCursor` genérica no id. Recusar parâmetro desconhecido nas listas muda o contrato: só com decisão | P; N34 M | B; N34 M | `limit` e `cursor` on/off point em `targets_integration_test`, `incidents_integration_test` e `heartbeats_integration_test` |
| R2.1 | 9 pontos de `slog.Error` + `CodeInternal`: `targethandlers.go:348`, `incidenthandlers.go:133`, `channelhandlers.go:161`, `heartbeathandlers.go:447`, `statuspagehandlers.go:147`, `authhandlers.go:385`, `accounthandlers.go:221`, `pinghandlers.go:87`, `provas.go:59` | Duplicated Code | Extract Function `erroInterno(w, r, op, err)`, com o desvio do Argon2 ocupado; depois do N8, um `falhouValidacao` comum. Os switches por domínio ficam | P | B | `canario_integration_test`; o 429 do Argon2 ocupado em `argon2_integration_test` |
| R3.9 | `httpapi/targethandlers.go:278` `Checks` (66 linhas, cyc 14) | Long Function | Split Phase `lerIntervalo(q, agora)` | P | B | `window`, `from`, `to` em `targets_integration_test` |
| N9 | `usuario` em 21 pontos; `pathID` em 11, dez fora do `donoEID` (`heartbeathandlers.go:409`): `accounthandlers.go:199`, `channelhandlers.go:96,120,151`, `incidenthandlers.go:122`, `statuspagehandlers.go:119`, `targethandlers.go:147,208,257,283` | Duplicated Code | Move Function de `donoEID` para junto de `usuario` (`targethandlers.go:371`) e `pathID` (`:469`); Replace Inline Code with Function Call | P | B | `idor_integration_test` |
| R2.11 | `targetstate/targetstate.go:22` `StaleIntervals = 3` × `prober/scheduler.go:497` `fatorLacuna = 3` (o comentário diz que são o mesmo número, e nada os liga); `notify/message.go:12` `causas` por `string`; limites literais em `httpapi/routes.go:123,149,158,230,325` | Magic Number, Shotgun Surgery | Replace Magic Literal: uma constante exportada; `causas` por `prober.ErrorKind`, com teste que cubra todo valor | M | B | `targetstate/targetstate_test`, `notify/message_test` (visto falhar com valor novo) |
| N32 | `heartbeat/service.go:286-289` (a lista), `:535` (`ScheduleFromRow` dentro de `toHeartbeat`), `heartbeat/ping.go:66` | Poison Row: uma linha que a validação futura recuse derruba a lista inteira e todo ping dela. Hoje não há linha assim | Pular e logar na lista, ou manter a validação de leitura mais larga que a de escrita | P | M | `TestScheduleFromRow` (`heartbeat_test.go:337`) cobre a função; **sem teste** da lista com linha inválida |
| N26 | `httpapi/middleware/recover.go:21-37` | `http.ErrAbortHandler` é engolido, e `WriteError` escreve depois de resposta parcial. Sem chamador hoje | Replace Nested Conditional with Guard Clauses: repassar o abort e pular o envelope depois do `WriteHeader` | P | B | `middleware_test.go:48,84` (panic comum); **sem teste** de panic depois do `Write` |
| N11 (D8) | `notify/notify.go:41` `TargetName`, `notify/dispatcher.go:399`, `storage/queries/notifications.sql:17,22-23,82,92-95` `target_name` e `target_enabled`; 14 usos em `message.go` | Mysterious Name: o nome mente desde os heartbeats | Rename Field para `Monitor…`, com alias no SQL. A coluna `notifications.target_name` fica por ora (expand/contract); o contrato `target_name` de `/incidents` não muda | P | B | `notify/message_test`, `reminders_test`; `sqlc generate` |
| R4.4 | `notify/notify.go:40` | Mysterious Name: `Event.Kind string` colide com `type Kind` (`:28`) | Rename Field `Kind` para `Evento` | P | B | Compilador |

### 2. Segurança

Autenticação, sessão, cookie, IP do cliente, validação de URL, backup e borda. A ordem das chamadas é
contrato nestes itens.

| Id | Local | Smell | Refactoring | Custo | Risco | Teste |
|---|---|---|---|---|---|---|
| R4.1 (ampliado) | `targets/validate.go:91` `validate` (70 linhas, cyc 25), `:166` `checkURL`, `:321` `origem`; `prober/safedial/safedial.go:143` `ValidateURL`, `safedial/host.go:37-40` `HostInstavelNoIDNA`; `prober/scheduler.go:284,312` | Primitive Obsession: a URL é `string` e é relida em cada camada (`url.Parse` em `safedial.go:143`, `host.go:38` e `validate.go:322`, mais o dial). O #203 acrescentou a terceira checagem | Replace Primitive with Object `parseProbe`: host decodificado, origem e a recusa do IDNA numa vez só | G | M | `validate_test`, `TestHostInstavelNoIDNA` (`safedial_test.go:209`), `TestTargetWithSharpSKeepsProbing` (`scheduler_test.go:646`); URL validada = URL sondada |
| N63 (novo) | `targets/validate.go:78-85` (`urlGravada`), `:106`; `targets/service.go:173` | Temporary Field: vazio no cadastro e preenchido só no PATCH, para a recusa do IDNA valer só quando a URL muda | Introduce Parameter Object: `validate(f, anterior)`; deixa o R4.1 pronto | P | M | `TestUpdateOfAStoredSharpSTargetLeavesTheURLAlone` (`service_test.go:597`), `TestUpdateCannotWriteASharpSURL` (`:688`); sabotar o `f.URL != f.urlGravada` e ver falhar |
| N64 (novo) | `prober/safedial/host.go:28-40` (o literal `'ẞ'`) | Magic Literal: o caractere vem de uma comparação feita à mão entre as tabelas do IDNA, e nada reconfere quando o `golang.org/x/net` mudar | Extract Constant `caracteresInstaveisNoIDNA`; teste que grave a saída de `idna.Lookup` para os code points aceitos e falhe quando o `x/net` mudar | M | B | `TestHostInstavelNoIDNA` cobre a lista atual; **sem teste** que detecte tabela nova |
| R2.2 | `httpapi/totphandlers.go:41,99,325,395,475` e `accounthandlers.go:92` (`reservar`); `errou` em `totphandlers.go:116,289,348,354,424,486` e `accounthandlers.go:108` | Duplicated Code, Data Clumps (`userID, familyID`) | Extract Function `(p *provasDaFamilia) abrir` e Extract Class `provasDoFator` | M | M, a ordem é contrato de segurança | Sabotar a ordem das provas e ver quebrar `provas_familia_integration_test`, `totp_prova_integration_test` e `totp_integration_test` |
| R3.10 | `httpapi/authhandlers.go:260` `issue` (6 parâmetros; `prova` nil escolhe a emissão), `:300` `grantSession` (`dispositivo` nil escolhe o cookie) | Flag Argument | Remove Flag Argument: um método para o login completo e outro para a renovação | M | M | **sem teste** de "assina antes do `Set-Cookie`": nenhum caminho HTTP faz `NewAccessToken` falhar (só com família nula, `auth/token.go:37`). Injetar a assinatura e escrever o teste antes; `dispositivo_integration_test` e `login_race_integration_test` cobrem o resto |
| N10 | `httpapi/authhandlers.go:72` `Login` (104 linhas, cyc 15), trecho do rehash `:121-134`; `totphandlers.go:155` `TOTPVerify` (100 linhas, cyc 15) | Long Function | Só o rehash (R3.5 do backlog antigo): Replace Nested Conditional with Guard Clauses em `regravarHash`. O resto segue a B7 | P | M | `TestLoginRehashDoesNotUndoAPasswordReset` (`login_race_integration_test.go:280`), o CAS do rehash |
| N2 + N70 (novo) | `authhandlers.go:126` `HashPassword`, `totphandlers.go:283` `VerifyPassword`, `authhandlers.go:47` `NewAuthHandlers` (9 parâmetros) | Hidden Dependency; Long Parameter List | Replace Inline Code with Function Call (um hasher no `AuthHandlers`); Introduce Parameter Object `AuthDeps`. O hasher só se o #200 não bastar | M | M | `login_race_integration_test`; teste de que o hasher injetado é usado; compilador |
| R4.2 | `crypto/hash.go:29`; `auth/session.go:72-74` (validação da chave), `:203`, `:311`; `account/account.go:200,517` | Primitive Obsession: a chave do IP é `[]byte`, validada em cada consumidor | Replace Primitive with Object `crypto.IPHasher` | M | M | Boot sem chave e com chave curta; `auth_integration_test.go:933,979` e `account_integration_test.go:182` (o `ip_hash` gravado) |
| N19 | `httpapi/routes.go:80-84`, `auth/argon2.go:51,65` | Global Data: `Routes` muta o semáforo do processo | Replace Global Data por dependência do `AuthHandlers` e do `account` | M | M | `argon2_integration_test` |
| R6.3 | `deploy/backup/backup.sh:56,78` (decodificação em `awk`, 2×); `internal/backup/backup.go:47` `validateURI` (cyc 13) | Duplicated Code | Move Function para `internal/backup` (o helper Go já recusa parte) | M | M | URI hostil antes de mover: `backup/backup_test.go`; `deploy/ci/testar-imagem-backup.py` |
| N23 | `internal/backup/backup.go:25,94` | Incomplete Data: `Reference` confere versão, usuários e alvos; heartbeats, tags e eventos ficam fora da aceitação do restauro | Add Field `Heartbeats`, no protocolo `v2`, com o script do restauro junto | M | M | Restauro real com heartbeat: `backup_test.go`, `deploy/ci/test_revalidar_backup.py` |
| N56 | `deploy/vps/stillon-entrada:308` `deploy`, `:689-696` | Falta de guarda: o verbo `deploy` não recusa um host Coolify e usa os mesmos nomes. Não confirmado para onde `VPS_HOST` aponta | Guard Clause por `current.json`, ou a D7 | P | M | Saída não zero antes de qualquer `docker`: caso novo em `deploy/ci/checar-vps.sh` |
| R2.5 | `httpapi/authhandlers.go:316-371` (`setCookie`, `setDeviceCookie`, `clearCookie`, `apagarCookieDeRefresh`), `"/api/v1/auth"` em `:323,345,365` | Duplicated Code, Magic Literal | Extract Function `cookieDeAuth` + `const pathDeAuth` | P | B | Atributos do cookie posto e apagado: `refreshCookie` (`auth_integration_test.go:199`), `dispositivo_integration_test.go:31`; e2e `fluxo.spec.ts:194` |
| R2.3 | `httpapi/accounthandlers.go:100,204`, `authhandlers.go:379`, `provas.go:57` | Duplicated Code de invariante: o IP do `Meta` tem de ser o do limitador | Extract Function `metaDe(r, px)` | P | B | `ip_hash` gravado = IP do limitador: `auth_integration_test.go:933,979`, `account_integration_test.go:182` |
| N27 | `httpapi/middleware/clientip.go:16-24,39,81,96,138`; `clientip_test.go:184` `TestRateLimitSeparatesClientsBehindFly`; `auth_integration_test.go:947,992` | Comments que mentem: citam o Fly e o Caddy como produção. O modo `CLIENT_IP_HEADER` não tem consumidor nos arquivos de deploy (`deploy/ci/checar-vps.sh:295` exige vazio) | Rename dos testes e dos comentários; o resto depende da D10 | P a M | M | `clientip_test`, `clientip_internal_test` |
| R2.13, `secheaders` | `httpapi/middleware/secheaders.go:7-9` | Comments que mentem: diz que o painel é servido à parte, e é o mesmo binário | Reescrever o porquê da CSP | P | B | Nenhuma |

### 3. Concorrência

Travas, transações, corrida entre avaliadores e esperas de teste por trava.

| Id | Local | Smell | Refactoring | Custo | Risco | Teste |
|---|---|---|---|---|---|---|
| R2.4 | `LockUserSessions` em 7 pontos: `account/account.go:203-215,354,397,521`, `auth/totp_rotation.go:78,204`, `auth/totp.go:303` | Duplicated Code de invariante de concorrência (trava + família viva) | Extract Function `travarFamiliaViva` e `travarUsuarioPorEmail` | M | M | `TestChangePasswordRechecksTheFamilyUnderTheLock` (`account_test.go:371`), `TestConfirmRotationRechecksTheFamily` (`totp_rotation_test.go:392`), `TestStartRotationRechecksTheFamily` (`:650`), `TestRotateRechecksTheFamilyUnderTheLock` (`httpapi/totp_troca_integration_test.go:527`) |
| R3.5 | `account/account.go:144` `ChangePassword` (88 linhas, cyc 22, 5 parâmetros) | Long Function | Extract Function `provarSegundoFator` e `gravarTroca`, depois do R2.4 | M | M | `account_test`, `account_integration_test` |
| N15 | `auth/totp_rotation.go:156` `ConfirmRotation` (95 linhas, cyc 22, 6 parâmetros), `:58` `StartRotation` (63 linhas, cyc 15, 6 parâmetros) | Long Function; `userID, familyID, rotation, code` são trocáveis | Extract Function das fases; Introduce Parameter Object só se o `FamilyID` (R4.5) não bastar | M | M | `totp_rotation_test`, corrida e RLS |
| R4.5 | `account/account.go:144`, `auth/totp_rotation.go:58,156` | Primitive Obsession: `uuid.UUID` para usuário e família na mesma assinatura | `type FamilyID` primeiro | M | M | Compilador |
| R3.6 + N12 | `prober/scheduler.go:425` `avaliar` (69 linhas, cyc 15); `:473-478`, `:484-489`; `prober/heartbeat.go:122-129`, `:139-146` | Duplicated Code (4 cópias de "marcar notificado + enfileirar"), Long Function | Extract Function `avisarAbertura` e `avisarFechamento`; `avaliar` fica com a histerese | M | M, roda na transação do check | Abertura e fechamento com `n == 1` e `n == 0`: `prober/scheduler_test`, `prober/heartbeat_test` |
| R3.7 | `prober/scheduler.go:331` `gravar` (63 linhas, cyc 13; o arquivo tem 608) | Divergent Change, Large Class | Extract Class `gravador`, depois do R3.6 | M | M | Testes de corrida com `-count=20` |
| R2.10 | `notify/dispatcher.go:291,337,383,448`; 24 literais de `status` | Primitive Obsession, Duplicated Code | Replace Primitive with Object `statusNotificacao`, `case failed` explícito e `default` que falha; depois Extract Function `registrarDesfecho` | M | M | Tentativas (429 perdoado, `MaxAttempts`, backoff): `dispatcher_test`, `reminders_test`, `prazos_test` |
| N3 (resto) | `account/account_test.go:245` `esperarPresa` (2 usos, `:228` e `:593`), cópia de `testdb.EsperarBloqueados`; laços com prazo de parede em `auth/totp_rotation_test.go:461` (3 s) e `httpapi/totp_troca_integration_test.go:582` (5 s) | Teste não determinístico, Duplicated Code | Move Function e Substitute Algorithm: o `account` usa o helper do `testdb`; os dois laços viram espera por evento | M | B | Sabotar a trava de cada teste e ver falhar sempre, com e sem carga |

### 4. Banco

Queries, formas de leitura do rollup e SQL dentro do Go.

| Id | Local | Smell | Refactoring | Custo | Risco | Teste |
|---|---|---|---|---|---|---|
| R5.1, passo 1 | `storage/queries/maintenance.sql:7-17` `ConsolidateRollups`, `targets.sql:112-126` `ListRawHours`, `statuspages.sql:81-122` (`ListPublicDailyUptime`, `ListPublicLatency`) | Duplicated Code: a fórmula do rollup mora em três queries, e "rollup = leitura bruta da mesma hora" só vale pelo comentário | Characterization test: rollup consolidado = leitura bruta da mesma hora, na borda do corte, com hora cheia e com rollup ausente | M | B | **sem teste**: é o próprio passo. Parciais: `TestConsolidateRollupsAggregatesClosedHours` (`storage/maintenance_test.go:68`), `TestSummarySparkMixesRollupsAndRaw` (`targets/summary_test.go:75`) |
| R2.6 + R5.5 | `auth/totp.go:321`, `auth/totp_rotation.go:222`, `users.sql:30,59,89` (a mesma limpeza do fator pendente em 5 lugares); 26 chamadas de SQL inline em `auth/` (`totp.go` 11, `store.go` 6, `user.go` 6, `totp_rotation.go` 3) | Shotgun Surgery, Divergent Change | Move Statements para `queries/*.sql`, uma query por PR, começando por `DisableTOTP` | G | M | Corrida e RLS de cada query, antes e depois |
| R2.7 | `auth/store.go:187`, `account/account.go:556` `audit` (8 parâmetros), `notify/dispatcher.go:487`; 11 ações literais (`session.reuse_detected`, `totp.*`, `user.*`) | Duplicated Code, Primitive Obsession, Long Parameter List | Substitute Algorithm sobre uma query só; `type AuditAction` | M | M | Um teste por ação lendo `audit_log` |
| R5.2 | `users.sql:27,56` (`ReplaceUserHash`, `SwapUserHash`: o mesmo `SET` de limpeza) | Duplicated Code de política | Parameterize Function | M | M | Set-password e troca, com CAS: `login_race_integration_test`, `totp_troca_senha_integration_test` |
| R5.3 | `targets.sql:69-71,90`, `maintenance.sql:30`, `statuspages.sql:54-55,69`; `targets/summary.go:131-136` | Valor sentinela (`''`, `'epoch'`, `0`) | Tirar o `coalesce`; o Go lê `nil` | M | M | Alvo sem check e sem rollup: `targets/summary_test`, `statuspage/public_test` |
| R2.8 | `targets/service.go:328-335` `serieHoraria` (7 parâmetros), `targets/summary.go:68-83`, `statuspage/public.go:150-156` | Data Clumps (marca, início, hora, corte); a terceira cópia já chegou | Introduce Parameter Object `rollupcut.Janela` | M | M | `rollupcut/rollupcut_test`, resumo e página pública |
| R3.3 | `targets/summary.go:68` `Summary` (87 linhas, cyc 17) | Long Function | Extract Function `sparkPorAlvo`, `horaCorrentePorAlvo` e `resumoDe` | M | B | `targets/summary_test` |
| R3.2 | `statuspage/public.go:131` `projetar` (70 linhas, cyc 13) | Long Function | Split Phase: leitura e `montarView` pura | M | B | `statuspage/public_test`; depois, `montarView` sem banco |
| N14 | `targets/summary.go:97-104` × `targets/service.go:363` `horaBruta` | Duplicated Code: a regra "ok = 0 zera as latências"; os tipos diferem (`SparkHour` × `Rollup`) | Extract Function da regra | P | B | `TestSummarySparkMixesRollupsAndRaw` |
| R2.9 | `statuspage/public.go:219,225`, `targets/summary.go:134,146`, `statuspage/public.go:26-35` | Duplicated Code (`float64(ok)/float64(total)`, o literal `"down"`, alias de `State`) | Extract Function `uptime`; `string(targetstate.Down)` | P | B | `uptime` com total 0, ok 0 e ok = total |
| R5.4 | 10 `SELECT *`: `channels.sql:44`, `incidents.sql:10,19`, `heartbeats.sql:15,45,135`, `statuspages.sql:5`, `targets.sql:12,18,23` | Regra de SQL: coluna nova entra no Go sem ninguém olhar (as migrations 00020 e 00022 mexeram em `heartbeats`) | Listar colunas | P | B | `sqlc generate` sem diff no Go |
| N33 | `heartbeats.sql:68-69` | Comments que mentem: citam `heartbeats_due_idx`, que a 00019 derrubou | Reescrever | P | B | Nenhuma; `sqlc generate` |

### 5a. Estilo: backend

| Id | Local | Smell | Refactoring | Custo | Risco | Teste |
|---|---|---|---|---|---|---|
| R3.8 + N65 (novo) | `statuspage/service.go:85` `Update` (64 linhas, cyc 15), `heartbeat/service.go:304` (49 linhas, cyc 14), `targets/service.go:153` (68 linhas, cyc 13) | Long Function: a mesma sequência (validar o patch, travar, aplicar, regravar, reler) nos três serviços | Extract Function `aplicar(patch, atual)` pura em cada um; `validarPatch` no heartbeat (`:305-330`) | M | B | PATCH (ausente não altera, `null` dá 400): `patch_null_integration_test`, `heartbeats_integration_test`, `*/service_test` |
| R3.4 | `config/config.go:119` `Load` (247 linhas, cyc 48) | Long Function | Combine Functions into Class `leitor`; guardar a URL parseada | M | B | `config_test`, `config_complementar_test` |
| R3.1 | `cmd/stillon/user.go:122` `runUser` (127 linhas, cyc 33), `:271` `lerSenha` (61 linhas, cyc 14); `switch sub` repetido em `:197` e `:221` | Long Function, Repeated Switches | Split Phase + tabela de subcomandos | M | B | `cmd/stillon/user_test`, `reset2fa_test` |
| R4.3 | `account/account.go:63-66,75-77,145` | Temporary Field: `totp` e `ipKey` ficam nil no CLI, e `ChangePassword` recusa sem eles | Extract Class `Operador` e `Service` | M | B | `account_test`, `cmd/stillon/user_test` |
| R2.12b | `httpapi/routes.go:229-236`, `:325-329`; `publicpage.go:57-61`; `pinghandlers.go:110-114` | Duplicated Code de ordem: quatro cadeias RateLimit, SecurityHeaders, Recover, Deadline, RequestID | Extract Function `externa(h, prazo)`, com o porquê da ordem | P | B | `deadline_integration_test`, `publicpage_integration_test`, `ping_integration_test`, `rotas_negar_por_padrao_integration_test` |
| N13 | `prober/scheduler.go:554`, `notify/dispatcher.go:502`, `heartbeat/ping.go:115` (`valueOrZero`) | Duplicated Code | Move Function para um lugar só | P | B | Compilador |
| N25 | `incident/history/history.go:72-75,94-97` | Duplicated Code | Extract Function `toIncident` | P | B | `history_test` |
| N24 | `backup/backup.go:36,370,385` (mais de 300 colunas); `:293,361,397,454` (mais de 200) | Long Line: o `gofmt` aceita, ninguém lê | Reformatar | P | B | Compilador, `backup_test` |
| N31 | `incident/detector.go:58` | Global Data exportado e mutável | `func Default()`, ao tocar | P | B | `detector_test` |
| N28 | `observability/metrics.go:1`, `trace.go:1`, `deploy/coolify/app.json:22` | Dois doc comments de pacote; `METRICS_ADDR=off`: fila, outbox e DLQ não têm métrica lida em produção | Unificar o texto. Métrica é decisão, não refactoring | P | B | Nenhuma |
| N29 | `auth/token.go:89-91` | Comments que mentem: "o painel ainda não foi publicado" | Apagar | P | B | Nenhuma |
| N41 | `httpapi/plano5_routes_test.go`, 7 `*_complementar_test.go`, `prober/spec_log_test.go`; 84 arquivos de teste citam Plano, Task ou revisão | Mysterious Name: nome pela história, não pelo comportamento | Rename e Move Function, ao tocar | M | B | Os mesmos testes |
| R2.13 | 307 linhas de produção citam Plano, Task, D*n*, R*n* ou "revisão" (30 com "revisão" ou "Review Focus"); regex: `Plano [0-9]\|Task [0-9]\|\bD[0-9]+\b\|\bR[0-9]+\b\|revisão\|Review Focus` | Comments | Apagar o id e manter o porquê, por arquivo, ao tocar | P por arquivo | B | Nenhuma |

### 5b. Estilo: painel

| Id | Local | Smell | Refactoring | Custo | Risco | Teste |
|---|---|---|---|---|---|---|
| R1.8 (resto) | `web/src/lib/api/cliente.ts:112-139` `pedir` (cc 12), `lib/api/http.ts:24-31` `ApiError` (6 posicionais), `:75` `lerErro` (cc 12) | Long Function, Long Parameter List | Extract Function `repetirComTokenNovo`; Introduce Parameter Object no `ApiError` | M | M, o 401 é denso | `lib/api/cliente.test` |
| R1.9 + R1.10 | `features/configuracoes/PaginaConfiguracoes.tsx` (960 linhas, 25 `useState`, 11 `instanceof ApiError`): `:738` (cc 21, 191 linhas), `:643` `Sessoes`, `:526` `TrocarTotp`, `:394` `AtivarTotp`, `:110` `TrocaDeSenha` | Large Class, Mutable Data | Extract Function `credencial.ts`; reducer puro `proximoEstadoDo2fa`; Move Function de `Sessoes`, `TrocaDeSenha` e dos diálogos para arquivos próprios | G | M | As 1.372 linhas de `configuracoes.test`; tabela de transições antes de trocar o `useState` |
| R1.7 | `PaginaConfiguracoes.tsx:85-88` × `lib/api/cliente.ts:125,133` | Duplicated Code de política (renovar ou encerrar) | Move Function `sessao.renovarOuEncerrar()` | P | B | `lib/api/sessao.test` |
| R1.6 | 12 `queryKey` literais: `alvos/FaixaDeEstado.tsx:82`; `alvos/PaginaDetalhe.tsx:138,139,140,166`; `alvos/PaginasDoFormulario.tsx:27,77`; `statuspage/PaginaStatusPage.tsx:106,115,120`; `canais/PaginaCanais.tsx:171`; `incidentes/PaginaIncidentes.tsx:230`; contra `lib/api/consultas.ts:10-28` | Shotgun Surgery | Prefixos em `chaves`; depois Move Function das mutações para `mutacoes.ts` | M | B | Testes de invalidação |
| N16 | `alvos/FormularioAlvo.tsx:43-58` (`enviar`, cc 11, aninhamento 5), `heartbeats/FormularioHeartbeat.tsx:44-55`, `statuspage/PaginaStatusPage.tsx:121-130` (`enviar`, cc 16, aninhamento 4), `canais/PaginaCanais.tsx:73`, `configuracoes/PaginaConfiguracoes.tsx:157` | Duplicated Code, Long Function | Extract Function `errosDosCampos(err, mapear)` pura, com teste por tabela; o seletor de intervalo fica no chamador | P | B | Testes de tela do 400 com `fields`: `alvos.test`, `cadastro.test`, `statuspage.test`, `canais.test`, `configuracoes.test` |
| N17 | `incidentes/PaginaIncidentes.tsx:218-238`, `alvos/FaixaDeEstado.tsx:68-86`, `heartbeats/IncidentesDoHeartbeat.tsx:33-50` | Duplicated Code: ack com 404, invalidação e reentrância, 3 cópias | Extract Function: hook `useReconhecer`; os avisos diferem, então vão por callback | P | B | `incidentes.test`, `alvos/detalhe.test`, `heartbeats/detalhe.test` |
| R1.11 (resto) + N69 (novo) | `alvos/esquema.ts:35` `urlPermitida` (cc 29), `:80` `esquemaDoAlvo` (92 linhas), `:129` (cc 17), `:271` `paraAlteracao` (cc 12); números soltos em `:93,107,112,116-117,136-137,145-146,159,174,179`; `LIMITES` só em `heartbeats/esquema.ts:15` | Long Function, Magic Number; `urlPermitida` espelha a regra do servidor (`checkURL`) | Decompose Conditional (tabela de faixas IPv4 em `urlPermitida`); objeto `LIMITES` como o dos heartbeats | M | B | `alvos/esquema.test`, on/off point por limite |
| N18 | `statuspage/PaginaStatusPage.tsx:63` `Formulario` (186 linhas, cc 19), `canais/PaginaCanais.tsx:149` (178), `alvos/FormularioAlvo.tsx:12` (175, cc 21), `alvos/PaginaDetalhe.tsx:110` `DetalheDoAlvo` (165), `login/PaginaLogin.tsx:66` (144, cc 11), `heartbeats/PaginaHeartbeat.tsx:33` `DetalheDoHeartbeat` (138), `incidentes/PaginaIncidentes.tsx:168` (125) | Long Function | Extract Function por seção, só onde o nome ficar mais abstrato que o JSX | M | B | Testes de tela; `a11y.spec` |
| N68 (novo) | `painel/PaginaPainel.tsx:113` `MonitoresFiltrados` (cc 12); `painel/BarraDeFiltros.tsx:17` (77 linhas, 10 props) | Long Function; Long Parameter List (props) | Extract Function: hook `useMonitoresFiltrados(itens, tarefas)`; Introduce Parameter Object nos props da barra | M | B | `painel/filtragem.test`, `barra-de-filtros.test`, `painel.test` |
| N67 (novo) | `heartbeats/EventosDoHeartbeat.tsx:38-49`, `heartbeats/IncidentesDoHeartbeat.tsx:90-101` (idênticos); `incidentes/PaginaIncidentes.tsx:294-304` (com foco) | Duplicated Code: o botão "Carregar mais" | Extract Function: componente `CarregarMais` | P | B | `heartbeats/detalhe.test`, `incidentes.test` |
| R1.4 + R1.5 (restos) | `.replace(/\s+/g, '')` em `login/PaginaLogin.tsx:121` e `configuracoes/PaginaConfiguracoes.tsx:144,283,378`; `lib/format.ts:98` `navegadorDe` (cc 15); `PaginaLogin.tsx:21` `destinoSeguro`; `PaginaConfiguracoes.tsx:333` | Duplicated Code, lógica de `lib` na tela | Extract Function `normalizarCodigo` e `baixarTexto`; Move Function de `destinoSeguro`; tabela no `navegadorDe` | P | B | `login.test.tsx:10` (redirect hostil) antes de mover; `format.test` |
| N37 | `lib/api/consultas.ts:31-33`, `httpapi/routes.go:158`, `web/e2e/fluxo.spec.ts:76-92` | Requisito implícito: o painel soma polls de 15 s por tela, o limite é 60/min com rajada 20, e o e2e só parou de levar 429 com a pausa fixa. Não confirmado com usuário real | Medir requisições por navegação antes de mexer | P | M | Contador de requisições no e2e |

### 5c. Estilo: testes

| Id | Local | Smell | Refactoring | Custo | Risco | Teste |
|---|---|---|---|---|---|---|
| N6 | `web/.oxlintrc.json` (só `correctness` e `suspicious`); `.github/workflows/ci.yml:52-93` (job `lint`: `vet`, `staticcheck`, `gosec`; sem `gocyclo`) | Falta de fitness function | `gocyclo` pinado como o `staticcheck`, em modo ratchet com a lista atual; `complexity`, `max-depth` e `max-lines-per-function` no oxlint, com exceção por arquivo | P | B | O próprio lint; uma função sabotada acima do teto |
| N7 | `storage/testdb/testdb.go:76` `New`; 253 linhas com `newAPI*`, em série | Slow Tests | Medir com `go test -json` (a suíte do `httpapi` caiu a 297 s com o #200). Depois, decidir a D6 | G | M | Suíte com `-race -count=3` sem falha |
| N5 | `internal/config/{scripts,backup_snapshot,compose,seed,repositorio}_test.go` (2.301 linhas de teste no pacote, 635 só no snapshot) | Divergent Change: `config` testa scripts de deploy | Move Function para um pacote de testes de deploy | M | B | Os mesmos testes, antes e depois; job `test` |
| R0.4 | `INSERT INTO users` em 29 arquivos (31 ocorrências) | Duplicated Code, Mystery Guest | Extract Function `testdb.NovoUsuario`, migrando só onde a onda tocar | P, depois incremental | B | Suíte |
| R0.5 + N40 | 19 `time.Sleep` em 11 arquivos; o maior é `config/backup_snapshot_test.go` (4). Decisão pelo relógio: `httpapi/sessions_integration_test.go:223`, `statuspage/service_test.go:356`, `cmd/stillon/statuspage_e2e_test.go:100-123`, `incident_e2e_test.go:257` | Teste não determinístico | Triagem: teto em `select` fica; decisão pelo relógio vira item | M | B | `-count=20` |
| N39 | `cmd/stillon/heartbeat_e2e_test.go:134` | Negative assert depois de 6 s fixos: "o papel api não abriu incidente" passa se a máquina atrasar | Provar que um tique rodou (métrica ou marca) antes de negar | P | B | Sabotar `--role=api` abrindo incidente |
| N42 | `httpapi/idor_integration_test.go:111` | Fragile Test: o canário `"webhook …6789"` casa por substring com dado real (já deu falso alarme) | `CANARY-<uuid>`, como manda a regra de log | P | B | Sabotar o handler |
| N43 | `httpapi/heartbeats_integration_test.go` (1.209 linhas), `auth_integration_test.go` (1.217) | Large Class | Move Function por assunto | M | B | Suíte |
| N38 | `web/e2e/fluxo.spec.ts:235,251,395` | Sleepy Test: `ESPERA_DO_POLL_MS` (45 s) em espera que não é de poll | Replace Wait with Event (`waitForResponse`) e nome certo | P | B | `--repeat-each=5` |
| N44 | `web/e2e/fluxo.spec.ts:240,335` | Weak Assertion: `ABA_COM_ABERTOS` aceita qualquer contagem | Número exato na pilha própria | P | B | `useTarefasQuePedemAtencao` sabotado |
| N45 | `web/e2e/fluxo.spec.ts:16-39,76-92`, `pagina-publica.spec.ts:23,50-61`, `troca.spec.ts:24-31` | Duplicated Code, Sleepy Test: laço de 429 copiado; o comentário "sem Retry-After" é falso (`httpapi/middleware/ratelimit.go:171-174`); 21 s de pausa fixa em dois specs | Extract Function `e2e/limite.ts`, que obedece o `Retry-After` | P | B | Fake com 429 |
| N46 (resto) | `web/e2e/fluxo.spec.ts` | Lacuna: a pilha real não exercita `/ping/{token}/{exit_code}`, cron nem a prévia | Acrescentar à jornada | M | B | A própria jornada |
| N47 | `web/e2e/abas.spec.ts:73-74` | Conditional Test Logic: `if (reusos !== null)` pula a conferência em silêncio | Anotar o pulo (como `troca.spec.ts:28`), ou ler a auditoria pelo compose | P | B | O run do CI mostra a anotação |
| N48 | `web/playwright.config.ts:23`, `ci.yml:382` (`--trace off`) | Sem artefato de falha: `--trace off` protege a senha, e nada o substitui | `screenshot: 'only-on-failure'`; `trace` condicionado | P | B | Falha forçada gera o artefato |
| N49 | `web/e2e/a11y.spec.ts` (908 linhas), `capturas.spec.ts:23` (448 linhas, pulado sem `CAPTURAR`), `api-imitada.ts` (124 linhas) | Large Class, Duplicated Code: preâmbulo repetido; login em `abas`, `troca` e `pagina-publica`; `capturas` é script sem assert; terceira cópia do contrato | Extract Function `e2e/passos.ts`; Move File das capturas | M | B | `a11y` verde |
| N50 | `web/e2e/fluxo.spec.ts:171,176`, `a11y.spec.ts:44-49,509-518`, `pagina-publica.spec.ts:150-151` | Fragile Test: classes internas do Recharts, `oklch` literal | `data-testid` ou ARIA | M | B | Os specs |
| N51 | `web/src/sistema.test.ts:131,166`; `index.css:156-163` (`sem-acento`, `acento-fora`, `acento-degradado`) | `toContain` casa comentário; a barra de acento some sem teste | Ampliar a lista; apagar o teste histórico do PR 6 | P | B | Remover o `@utility` e ver falhar |
| N52 | `web/src/index.css:245`, `:51,113`, `tokens.test.ts:99` | Dead Code (`prefers-reduced-motion` quase sem alvo), Speculative Generality (`--realce` só apelida `--primaria`); o contraste do `apagado` sobre tons 100 fica fora do teste | Cobrir ou apagar; Inline do token | P | B | `tokens.test` |
| N53 | `setTimeout(res, 50)`: `canais.test.tsx:193,397`, `incidentes.test.tsx:179,226,238`, `configuracoes.test.tsx:312,579,977,996`; espera real da prévia em `heartbeats/cadastro.test.tsx:139`; 18 `.length).toBeGreaterThan(0)` (eram 11) | Sleepy Test, asserção vácua | Fake timers; `toHaveLength(n)` | M | B | Sabotagem pontual |
| N71 (novo) | `web/src/test/api-falsa.ts` (795 linhas), `web/e2e/api-imitada.ts` | Large Class: cópia do servidor que já divergiu quatro vezes (F8 a F11) | Move Function por domínio; manter `api-falsa.test.ts` (515 linhas) como contrato | M | B | `web/src/test/api-falsa.test.ts` |

### 5d. Estilo: deploy, scripts e docs

Depende da **D7**: a produção é Coolify, e o verbo `deploy` do `stillon-entrada` não roda desde 2026-10-14. O
verbo `backup` segue vivo (`deploy/coolify/release/stillon-backup-entrada.sh:6`).

| Id | Local | Smell | Refactoring | Custo | Risco | Teste |
|---|---|---|---|---|---|---|
| N20 | `.github/workflows/deploy.yml` (179 linhas), `deploy/vps/stillon-entrada:308` `deploy` (109 linhas), `deploy/vps/compose.prod.yaml` (201), `deploy/ci/vps.sh` (219), `deploy/ci/checar-vps.sh` (1.422), `docs/deploy/vps.md` (4.539) | Speculative Generality, Remove Dead Code: 5 min 50 s de CI no job `vps` protegem um caminho que a produção não usa | Remove Dead Code, depois de separar `backup` em entrada própria | G | A, o backup depende dele | Backup real com restauro; job `vps` reduzido ao `backup` |
| N62 | `deploy/vps/stillon-entrada:478,521,561` (o backup vivo), `.github/workflows/ci.yml:450` (`publish` depende do job `vps`) | Divergent Change: o backup vivo mora no arquivo a aposentar | Extract Module antes de apagar; reforça a ordem da D7 | G | A | `deploy/ci/testar-imagem-backup.py` |
| R6.4 + R6.6 | `stillon-entrada:308` `deploy` (109 linhas) e `:561` `backup` (114 linhas); `checar-vps.sh` (1.422 linhas) | Long Function | Só depois da D7: se o N20 entrar, a maior parte some | M a G | A | Paridade caso a caso |
| N55 | `deploy/coolify/database.json:37`, `deploy/coolify/app.json:73` × `internal/config/compose_test.go:42-62` | Duplicated Code: o digest do Postgres e o do Caddy no Coolify não entram no teste; o Dependabot atualiza um lado e o CI fica verde | Ampliar o teste; `TRUSTED_PROXIES` × Caddyfile junto | P | B | Sabotar o digest |
| R6.1 | `falhar` em 8 scripts: `deploy/ci/checar-imagem.sh:13`, `checar-nome.sh:18`, `checar-borda-cloudflare.sh:61`, `checar-compose.sh:20`, `checar-vps.sh:41`, `checar-vigia.sh:18`, `checar-workflows.sh:19`, `testar-vps.sh:19`; a variante com `exit 1` em `testar-ssh-real.sh:30` | Duplicated Code | Extract Function para `deploy/ci/lib/checagem.sh` | M | B | Cada `checar-*` sabotado |
| R6.2 | `85` em `stillon-entrada:666`, `deploy/backup/backup.sh:181`, `deploy/ci/vps.sh:197`; `65532` em `stillon-entrada:628,639`, `backup.sh:131`, `checar-vps.sh:1229` | Magic Number | Constantes nomeadas | P | B | `checar-vps.sh` |
| R6.5 | `checar-vps.sh:120,122,238,429` (`dc`, `bd`, `q`, `cu`); credencial de demo em `Makefile:53`, `compose.yaml:86-88`, `deploy/ci/checar-compose.sh:15-16` | Mysterious Name, dado duplicado | Rename Function; `DEMO_*` no Makefile | P | B | Os checadores |
| N22 | `.github/workflows/backup.yml:18`, `revalidar-backup.yml:17` | Hidden Dependency: o `schedule` do GitHub atrasa (a primeira revalidação agendada, marcada para 22:47, rodou às 02:22 UTC) | Disparo por relógio próprio, ou alargar a política com motivo escrito | P | B | Run agendado medido por uma semana |
| N57 | `deploy/ci/test_docker_build.py:105,117,139-141` | Weak Assertion: a recusa só confere código não zero | Exigir código e mensagem | P | B | Tirar a recusa de `--push` e ver falhar |
| N58 | `Makefile:9`, `ci.yml:133`, `deploy/ci/test_docker_build.py:161` | Divergent Change: o contrato do docker roda nas duas pernas do Postgres e no `make test` | `make test-docker-build`, uma perna só | P | B | Job `test` |
| N59 | `deploy/ci/diagnostico-fixture.py:18` (regex com `17\|18`); nomes `test_*` × `testar-*` em `deploy/ci/` | Shotgun Surgery (versão do Postgres no regex), nomes que escondem o F6 | Limpeza em um PR; rodar tudo por `deploy/ci/rodar-testes.sh` | P | B | Os próprios testes |
| N60 | `scripts/docker-build.sh:109-112` (`publish`), `Makefile:58-59` | Speculative Generality: o modo `publish` não tem chamador (o CI usa `published`); `oci` exige builder sem dizer | Remover, ou testar contra `registry:2` | P | B | `deploy/ci/test_docker_build.py` |
| N61 | `deploy/coolify/app.json:15-23,71`, `database.json:16-18` | Misleading Message: `:?defina X no /opt/stillon/.env`, e as variáveis moram no Coolify | Trocar o texto; muda o commit de infraestrutura | P | B | Nenhuma |
| N21 | `deploy/coolify/release/README.md:25-27` × `receiver-release.json:2` (versão 2.1.1) | Comments que mentem: o README cita a 2.0.2 | Atualizar a versão citada | P | B | Nenhuma; `[skip ci]` |
| N35 | `docs/superpowers/specs/2026-11-05-heartbeats-design.md:237,308` | A checagem de access log foi no Traefik, e `app.json:63,109` tem `traefik.enable=false`; a resposta mostra Caddy e Cloudflare | Corrigir junto do F2 | P | B | Nenhuma |
| N36 | `docs/superpowers/specs/2026-11-05-painel-denso-design.md:48,134` | "Alvo" e "Nenhum alvo com esses filtros.": a tela diz "monitor" | Atualizar a spec | P | B | Nenhuma; `[skip ci]` |

## Descartes

Mantidos, e os novos, pela B7.

- **`deploy/coolify/release/receiver.py`** (968 linhas; `validate_oci`, cyc 26, em `:395`): compartilhado com Duora e GoblinShift, por hash. Refatorar só aqui quebra a igualdade de bytes.
- **`Patch.apply`** (`targets/service.go:227`, cyc 17) **e `updateParams`** (`heartbeat/service.go:364`, cyc 13): sequência plana de `if campo != nil`.
- **`depoisDeContar` e similares** (`targets`, `heartbeat`, `notify`, `statuspage`, `account`): é o seam dos testes de concorrência; custa um campo nil.
- **Unir `incidentResponse` e `heartbeatIncidentResponse`, ou os dois `Ack`**: contratos separados de propósito (D3 da spec de heartbeats), e são dois casos, abaixo da Rule of Three.
- **Unir as duas `PaginasDoFormulario`**: o heartbeat carrega a URL de ping, e o alvo, o aviso dos headers; o que sobra em comum é pequeno.
- **Contar e inserir sob trava** em `targets/service.go`, `heartbeat/service.go` e `notify/service.go`: três cópias curtas com tipos diferentes; um genérico esconderia mais do que ganha.
- **`backup.run`** (`internal/backup/backup.go:120`, cyc 37, 144 linhas; e `novoDump`, 7 parâmetros): o cabeçalho já explica que os `defer` formam a ordem de encerramento e por isso ficam no mesmo corpo. Extrair quebraria a ordem. Só com `-race` e os testes do snapshot na mão.
- **`TOTP.Confirm`** (`auth/totp.go:116`, 72 linhas, cyc 13): cerca de 25 linhas são comentário de invariante; o corpo é uma sequência linear.
- **`httpapi.Routes`** (`routes.go:72`, 179 linhas): composition root plano, sem decisão.
- **`criarSessao`** (`web/src/lib/api/sessao.ts:67`, 184 linhas) **e `criarApi`** (`cliente.ts:110`, 134 linhas): fábricas com funções internas curtas e um objeto de uma linha por rota; só o `pedir` entra no R1.8.
- **Tags de alvo e de heartbeat em dois pacotes** (`targets/tags.go`, `heartbeat/service.go:221-262,354-361`): o `rotulo` já guarda as regras; sobram duas leituras e gravações, uma por tabela.
- **`auth/session.go`** (`Rotate`, `issue`, cyc 12): lido linha a linha em 11-06, sem defeito; a ordem das travas está documentada.
- **Validação e parsing sem estado** com ciclomática de 8 a 13 (`decodeHash`, `Classify`, `ClientIP`, `validHeaderName`, `nomeDeCabecalho`, `ValidateDiscordURL`): sequência de guardas, com teste de tabela.
- **N30** (`auth/dispositivo.go:184-190`): o contador em memória zera a cada merge, e merge é deploy; a varredura é O(n) sob trava. Aceito: n é pequeno, e o bloqueio da conta segue no banco.

## Decisões para o dono

**D6. Paralelizar o `internal/httpapi`.** (a) só o N1 e o N2; (b) banco por teste a partir de um template, com
`t.Parallel`; (c) dividir o pacote. O N1 foi feito (#200, 488 s para 297 s). **Recomendo seguir a (a) e decidir a (b)
com o número do `go test -json` (N7) na mão**: a (b) exige mexer no `testdb` e rever o limitador por teste; a (c) não
acelera e espalha helpers.

**D7. Aposentar a trilha de deploy antiga.** (a) remover `deploy.yml`, o verbo `deploy` e o `compose.prod.yaml`,
mantendo o `backup` numa entrada própria; (b) manter tudo como rollback; (c) só reduzir o job `vps`.
**Recomendo (a).** Ganho: uns 6 min por CI e o R6.4 e o R6.6 quase somem. Custo: perder o caminho manual de volta e
reescrever o `docs/deploy/vps.md`; é a parte que mexe no backup de produção.

**D8. Nome único para "o que é monitorado".** (a) `Monitor` no Go e no SQL novo, sem tocar no contrato HTTP; (b)
deixar `Target` e documentar. **Recomendo (a)**, o painel já diz "monitores". O custo é o N11, e o contrato
`target_name` de `/incidents` fica como está.

**D9. Onde mora a revalidação de restauro.** Decidida a opção (a), no PR #181. O texto completo da decisão está
no histórico do git deste arquivo.

**D10. O papel da Cloudflare na cadeia.** (a) assumi-la: o primeiro Caddy confia só nas faixas dela, e o acesso
direto à origem é fechado; (b) tirar o proxy dela e ficar só com DNS; (c) manter como está e aceitar balde
compartilhado. **Recomendo (a)**, depois de conferir o F2, com o STRIDE refeito. Custa manter a lista de faixas e
aceitar um terceiro que termina TLS. A (b) tira esse terceiro, e perde o que ela absorve.

**D11. Reação a backup sem prova.** Resolvida: a falha da revalidação avisa pelo `avisar_revalidacao.py` (#181).

**D12. `null` nos campos obrigatórios de PATCH.** Resolvida pela opção (a) no #205.

**D13 (nova). Motivo do nome longo demais.** Alvos e status pages devolvem `too_long`; heartbeats devolvem
`out_of_range` (`heartbeat/service.go:124-125`), e a descrição também (`:139-140`). (a) manter por domínio e o N8
só unifica o tipo; (b) alinhar o heartbeat a `too_long`, o que muda o JSON e as mensagens do painel
(`mensagens.ts:58-59,79`). **Recomendo (b)**: o mesmo erro com o mesmo motivo, e o painel já tem a mensagem. O custo
é o contrato do `POST` e do `PATCH` de heartbeats; não conferi se há cliente fora do painel.

## Ordem sugerida

F1 (uma revalidação agendada verde) e a conferência do F2 antes de tudo. Depois o N6, que dá o ratchet de
complexidade para o resto, e o N7, para decidir a D6. Os itens do grupo 1 com custo P (R2.1, R3.9, N9, R4.4, N11)
entram juntos; o N8 e o N66 esperam a D13. No grupo 2, o R3.10 só depois de escrever o teste da ordem, e o R4.1 só
depois do N63. O N20 e o N62 andam com a D7, e o F3, o F4 e o N56 também. Os itens de testes (5c) podem ser uma
onda própria, depois do N6.

## O que ficou sem auditar

- Nenhuma suíte, Playwright nem Docker rodou: o 297 s do #200, o Argon2id de 11-05 e as frequências de falha
  não foram reproduzidos. O `go test -json` do N7 não existe ainda.
- Os heartbeats em Go foram lidos pelo que mudou (`service.go`, `ping.go`, `heartbeathandlers.go`,
  `pinghandlers.go`), não linha a linha. O painel denso e a tela de heartbeats entraram só por métrica do oxlint e
  por trechos.
- Na complementar de 2026-11-06 vários itens N38 a N54 vinham de leitura delegada. Nesta versão conferi arquivo e
  linha de cada um que continua no backlog, menos: o conteúdo de `api-imitada.ts` (N49, só o tamanho), os pontos de
  N49 e N50 além das linhas citadas, e a "montagem repetida" do N54, que saiu do backlog sem reconferência.
- A configuração do primeiro Caddy e o estado da VPS: não entrei nela. O F2, o N37 e o N56 seguem não confirmados.
- `docs/deploy/vps.md` (lido em trechos), `deploy/coolify/release/receiver.py` (fora, pelo descarte),
  `deploy/backup/` fora do `backup.go`, e o restante do `stillon-entrada` e do `checar-vps.sh`: só os trechos citados.
- Testes Go: métrica e amostra, não leitura integral de 40 mil linhas. Testes do painel (`esquema.test.ts`,
  `cliente.test.ts`, entre outros): não lidos.
- Pacotes lidos linha a linha em 2026-11-06, sem defeito: `internal/crypto`, `prober/safedial`, `webui`,
  `incident/detector`, `auth/session.go`, `token.go`, `dispositivo.go`, `backup/backup.go` e os middlewares. O `safedial`
  mudou desde então (`host.go`, #203) e foi relido só nesse arquivo.
- Os 7 runs vermelhos do `ci` que a complementar de 11-06 abriu ficaram todos explicados e corrigidos, e nenhum afetou
  a `main`.
