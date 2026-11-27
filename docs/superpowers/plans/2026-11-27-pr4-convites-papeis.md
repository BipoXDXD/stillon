# PR 4: convites e papéis. Plano de implementação

> **Para agentes:** sub-skill OBRIGATÓRIA: use superpowers:subagent-driven-development (recomendado) ou superpowers:executing-plans para executar este plano tarefa por tarefa. Os passos usam checkbox (`- [ ]`) para acompanhamento.

**Objetivo:** tornar os papéis reais. Um projeto passa a ter membros além do titular: o owner convida por e-mail, a pessoa aceita por link, papéis mudam, membros saem ou são removidos, o projeto é renomeado e a titularidade pode ser transferida, sempre com a RLS forçada no banco e sem enumerar e-mails.

**Arquitetura:** o contexto `internal/tenancy` continua puro. Ele ganha as regras de quem pode convidar, mudar papel, remover e transferir, o teto de membros e as portas `MemberRepository` (dentro de `ProjectRepository`), `InvitationRepository` e `InvitationMailer`. O adaptador `tenancy/pgstore` aplica essas regras dentro de `storage.InProjectTx`, com o papel lido na própria transação. No banco: (1) uma FK adiável faz o titular ser sempre owner, o que já garante pelo menos um owner; (2) os colegas ficam visíveis por `app.project_id`, sem subconsulta recursiva em `project_members`; (3) a tabela nova `project_invitations` tem dois escopos, o do projeto (`app.project_id`) e o do token (`app.invitation_token_hash`), no molde de `account_tokens` (00047). O e-mail do convite sai pelo `contaemail.Enviador` do PR 3: balde por destinatário, teto global de 50/dia e remetente de conta.

**Stack:** Go 1.26.11, PostgreSQL 17/18, pgx + sqlc, goose, testcontainers; React 19, react-router 8, TanStack Query 5, zod 4, radix-ui, Vitest + MSW, Playwright + axe.

**Spec:** [`docs/superpowers/specs/2026-11-22-multiinquilino-cadastro-design.md`](../specs/2026-11-22-multiinquilino-cadastro-design.md), seções Domínio, Dados, Isolamento (RLS), API e painel, e Cotas ("3 membros por projeto" = 3 **além do titular**; a cota é do titular). Roteiro: linha "PR 4" de [`2026-11-22-etapa1-multiinquilino.md`](2026-11-22-etapa1-multiinquilino.md). Portões herdados: § "Portões para PRs futuros" de [`2026-11-22-pr2b-pertenca-projetos.md`](2026-11-22-pr2b-pertenca-projetos.md). Padrão de tokens e e-mails de conta: [`2026-11-23-pr3-cadastro-publico.md`](2026-11-23-pr3-cadastro-publico.md).

**Base:** `main` em `fb9d4f97` (PR 3a.2, #461). Última migration: **00047**. O PR 3b (telas de cadastro) está em andamento em paralelo. O PR 5 (cotas e abuso) é planejado em paralelo e entra **depois** deste.

## Restrições globais

- Go 1.26.11. **Nenhuma dependência nova** em `go.mod` nem em `web/package.json`.
- Tudo em português do Brasil: código novo, nomes de teste, comentários, textos, e-mails, commits (`tipo(escopo): descrição`) e PRs.
- Antes de dizer que algo funciona: `go build ./... && go vet ./... && go test -race -count=1 -p 1 <pacotes afetados>`. No fim de cada sub-PR, `make test` (precisa do Docker). Painel: `cd web && npm test && npm run typecheck && npm run lint`.
- Teste novo só vale **visto falhando com o código sabotado**. Cada passo "rodar e ver falhar" diz o que sabotar quando o teste já passaria.
- Busca com `git grep` / `git ls-files`, nunca `grep -r` da raiz: `.claude/` guarda outras worktrees.
- Migrations deste PR: **00048** (4.1), **00049** (4.2), **00050** (4.3), nesta ordem, só expand. A 00015 fica vaga. `storage.Migrate` recusa número menor que o aplicado: se outra frente (PR 5, SP2) publicar migration antes, **renumere em sequência antes do merge**, sem buraco.
- Cada sub-PR publica com o binário anterior rodando durante o release (expand → migrate → contract). Merge = deploy: depois de cada merge, confira o run do `Coolify release` do SHA e o `readyz`. Merge fora de 00:00–01:00 e 03:10–03:45 BRT.
- `app.project_id` com valor só por `storage.InProjectTx`. `app.invitation_token_hash` só por `storage.InInvitationTokenTx` e `storage.InInvitationAcceptTx` (as duas em `internal/storage/scope.go`).
- Contratos fechados do servidor (valem para toda rota nova):
  - código de erro novo entra em `internal/apierr/errors.go` **e** em `ERROS_DO_SERVIDOR` de `web/src/test/api-falsa.ts`;
  - handler que decodifica JSON entra em `corposEstritos()` (`internal/httpapi/dto_estrito_internal_test.go`);
  - todo `POST` que cria passa por `criacao` (trava `email_not_verified`), ou entra nas exceções de `TestContaNaoConfirmadaNaoCriaPorNenhumaRota` com o porquê;
  - arquivo novo que liga escopo entra nas listas de `internal/storage/guarda_escopo_test.go` com o motivo.
- Valores da spec e deste plano: convite vale **7 dias**, uso único, token de **256 bits** com só o SHA-256 gravado; teto de membros = **3 além do titular** (`MaxMembersPerProject = 3`), operador **100** (`MaxMembersForOperator`), contados pelo titular do projeto; papéis `owner`, `manager`, `member`, `viewer` com a matriz de `internal/tenancy/role.go` (sem mudança).
- Nunca enumerar contas: criar convite responde e envia **o mesmo** e-mail exista ou não conta com aquele endereço.
- Tarefas de tela (T10, T12–T14): carregue `frontend-design:frontend-design` (direção C "Editorial claro", Fraunces + Inter, primária índigo, dentro da spec do redesign), `design-skills:ux-writing`, `design-skills:interaction-design`, `design-skills:design-systems` (reusar `web/src/components/ui`) e `design-skills:accessibility-audit` (WCAG 2.2 AA). Detalhes visuais e textos novos são do dono: a T12 **para** até ele decidir.
- Um comando pesado por vez (Docker, Playwright); remova os contêineres que criar.

## Foco da revisão

1. **Membro removido continua agindo.** Depois da remoção, o próximo pedido dele no projeto dá 404 (o mesmo de projeto inexistente), os canais de e-mail que ele criou ali ficam desabilitados, e um convite já aceito não serve para ele voltar sozinho. O pedido que já estava em voo conclui, como na revogação de sessão. Testes: `TestRemovedMemberNextRequestIsNotFound` e `TestRemovalLetsInFlightRequestFinish` (T3), `TestRemovedMemberEmailChannelsDisabled` (T3), `TestAcceptedInvitationCannotReinsertRemovedMember` (T6).
2. **Convite aceito por quem não devia.** Outro usuário logado com o link, o mesmo link duas vezes ao mesmo tempo (clique duplo, scanner + humano), convite revogado ou vencido. Exatamente um aceite vence; conta com outro e-mail recebe 403; o resto recebe `token_invalid`. Testes: `TestAcceptByOtherAccountRefusedInDB` (T5), `TestConcurrentAcceptOnlyOneWins` e `TestRevokedOrExpiredInvitationIsTokenInvalid` (T6).
3. **Enumeração de contas pelo convite.** Status, corpo da resposta e texto do e-mail idênticos para endereço com conta e sem conta, inclusive com o balde do destinatário vazio ou o teto global esgotado. Teste: `TestInviteResponseIdenticalWithOrWithoutAccount` (T8) e `TestInvitationEmailSameForAnyRecipient` (T7).
4. **Teto de membros furado.** Dois convites simultâneos na última vaga; convite pendente que não conta; reenvio que gasta vaga nova; aceite que passa do teto. Testes: `TestConcurrentInvitesAtLimitOnlyOnePasses`, `TestPendingInvitationsCountTowardLimit` e `TestReinviteSameEmailKeepsOneSlot` (T6).
5. **Escalada de papel.** Manager convidando manager ou owner, manager mudando papel, viewer ou member mutando por uma rota esquecida, titular rebaixado ou saindo sem transferir. Testes: `TestAuthorizeInviteMatrix` e `TestAuthorizeRoleChangeAndRemoval` (T2), `TestEveryMutatingProjectRouteRefusesViewer` (T4) e `TestTitularAlwaysOwnerInDB` (T1, a FK como segunda barreira).

## Decisões

### Precisam do dono (com recomendação; nada do que depende delas começa antes)

**Decidido em 2026-11-27:** D1 = (a), os quatro papéis da spec; D2 = (a), transferência em dois passos (o 4.3 entra). D3, D4 e D5 seguem a recomendação (a). D6 continua esperando o dono, na T12.

| # | Decisão | Opções (prós / contras) | Recomendo | Bloqueia |
|---|---|---|---|---|
| D1 | Nomes dos papéis. A spec aprovada e o código (`tenancy.Role`, CHECK de `project_members`) têm **quatro**: owner, manager, member, viewer. O pedido desta tarefa fala em owner/editor/viewer. | (a) Manter os quatro: zero migração, segue a spec, manager separa "gerir canais e convidar" de "editar monitores". (b) Três (owner/editor/viewer): mais simples de explicar; exige migration de dados, mexe em toda verificação `Require` e na matriz, e perde o papel intermediário. | (a). Os rótulos em português ficam para a T12. | nada (o plano segue a) |
| D2 | Transferência de titularidade (spec: "só muda por transferência explícita para outro owner"). Ela leva a cota do projeto para quem recebe. | (a) **Em dois passos**: o titular propõe, o owner que recebe aceita no painel. Ninguém ganha cota alheia sem querer; custa uma coluna e três rotas. (b) Imediata para um owner existente: menos código, mas permite "empurrar" projetos para alguém e lotar a cota dele (e ninguém apaga projeto até o PR 6). (c) Adiar para depois do PR 6: o PR 6 (excluir conta) precisa dela quando a pessoa é titular de projeto com outros membros. | (a), no sub-PR 4.3 | T9 e a parte de transferência da T13 |
| D3 | Papel no banco (decisão 4 do 2b: "reavaliar no PR 4"). Hoje o papel é conferido no Go (`ProjectScope.Require` dentro de `InProjectTx`, 59 usos). | (a) Go + teste que enumera **toda** rota de mutação de projeto e exige 403 para viewer (e member onde cabe): barato, cobre a rota futura. (b) Restritivas por `app.project_role` em INSERT/UPDATE/DELETE das quatro tabelas pais e filhas: defesa em profundidade, mas ~15 tabelas, filhas escritas também pelo sondador, e mais um GUC de acesso para guardar. O isolamento **entre inquilinos** já está no banco; isto é autorização **dentro** do inquilino. | (a) agora; (b) só se aparecer caminho de escrita fora dos serviços | nada (o plano segue a) |
| D4 | Convite para quem ainda não tem conta, com o cadastro público desligado em produção até o PR 6 (decisão do PR 3). | (a) Aceitar convite para qualquer e-mail; quem não tem conta só consegue aceitar quando o cadastro abrir (o convite vence em 7 dias e pode ser reenviado). Sem enumeração. (b) Aceitar convite criando a conta no próprio fluxo (o token prova o e-mail): resolve já, mas abre um segundo caminho de cadastro sem Turnstile e sem o aceite de termos do PR 6. (c) Recusar convite para e-mail sem conta: **proibido**, enumera contas. | (a) | nada (o plano segue a) |
| D5 | Orçamento de e-mail dos convites dentro do teto global de 50/dia. | (a) Só o balde por usuário (rajada 3, depois 1 a cada 30 min), mais a trava de e-mail confirmado, o balde por destinatário e o teto global já existentes; o teto diário por conta é do PR 5. Simples, mas um usuário confirmado ainda pode gastar ~50/dia. (b) Subteto global de 20 convites/dia: protege cadastro e redefinição, mas exige outra contagem em `account_email_daily`. | (a), reavaliar no PR 5 | nada (o plano segue a) |
| D6 | Design e textos: página de membros, página de aceite, rótulos dos papéis, e-mail do convite. | O implementador propõe 2 opções com as skills de design (T12). | — | T13, T14 |

### Tomadas por mim (uma linha de porquê cada)

- **Quatro sub-PRs.** 4.1 (membros e papéis, 00048), 4.2 (convites, 00049), 4.3 (transferência, 00050, espera D2) e 4.4 (painel). Cada um publica sozinho; o backend de convites sobe antes da tela porque ninguém cria convite sem a UI, e a rota de aceite só é usada por um link que ainda não sai.
- **Titular sempre owner, no banco:** FK adiável `projects (id, titular_id, titular_role) → project_members (project_id, user_id, role)`, com `titular_role text NOT NULL DEFAULT 'owner' CHECK (titular_role = 'owner')` e `UNIQUE (project_id, user_id, role)` em `project_members`. Por quê: atende o portão do 2b e, como o titular é sempre owner, "pelo menos um owner" deixa de precisar de trava ou contagem. `INITIALLY DEFERRED` porque todo caminho grava o projeto antes do vínculo, na mesma transação (`CriarProjetoPadraoNaTx`, `Store.Create`).
- **Titular protegido:** não é rebaixado, não é removido e não sai; antes, transfere (4.3). Erro `tenancy.ErrTitularProtected` → 409 `titular_protected`. A regra está no domínio; a FK é a segunda barreira.
- **Colegas visíveis por `app.project_id`** (`project_members_do_projeto_select`), e não por subconsulta em `project_members`, que recursaria (42P17). Só `InProjectTx` liga esse GUC, depois de conferir a pertença.
- **Papéis e permissões:** a matriz atual fica como está. Convidar member/viewer exige `PermInviteBasic` (manager+). Convidar manager/owner, mudar papel e remover outro exigem `PermManageMembers` (owner). Renomear exige `PermManageProject` (owner). Sair do projeto vale para qualquer papel, menos para o titular. Ver membros vale para qualquer papel (`PermRead`), com e-mail, para a equipe saber quem está nela. Ver convites pendentes exige `PermInviteBasic`.
- **Toda mutação de membros e convites trava `stillon.members:<project_id>`** (advisory), inclusive mudar papel, remover, aceitar e transferir. É barato e serializa contagem e mudança.
- **Teto de membros:** contam os membros menos o titular e os convites pendentes não vencidos, sob a trava. Assim o aceite nunca passa do teto (o pendente vira membro) e não é preciso reconferir. Operador = titular com `users.is_operator`. O código do erro é `member_limit_reached` (409), no molde de `project_limit_reached`.
- **Reenviar = convidar o mesmo e-mail de novo:** o pendente anterior é apagado (o link antigo morre) e nasce outro, ocupando a mesma vaga. E-mail que já é membro dá 409 `already_member`. Não enumera: quem convida já vê a lista de membros.
- **Token do convite no molde de `account_tokens`:** 32 bytes de `crypto/rand` em base64url, SHA-256 (o mesmo de `crypto.HashToken`, com teste de paridade), link `/invite#token=…` (fragmento: fora de log e de Referer). Revogar = `DELETE`. Aceite = `UPDATE … SET accepted_at, accepted_by` num comando só (o segundo aceite simultâneo não acha linha).
- **Aceite exige sessão e o mesmo e-mail da conta.** A política de UPDATE compara `email` com o de `users` de `app.user_id`, então o banco recusa outra conta. Erro distinto `invitation_other_account` (403) só depois de o token se provar válido: quem tem o link já sabe o próprio e-mail, e o texto não mostra o e-mail do convite. O aceite **não exige** e-mail confirmado, porque o token prova a caixa de entrada, e **não marca** o e-mail como confirmado, para não misturar com o fluxo do PR 3a.2.
- **Expiração conferida no Go com o relógio da aplicação** (`expires_at > sqlc.arg(now)`), nunca com `now()` em política: os testes usam relógio falso, como em `account_tokens`.
- **Membro removido (ou que sai):** na mesma transação, `notif_channels` com `kind = 'email'` e `user_id` dele **naquele projeto** passam a `enabled = false` (decisão 5 do 2b, vinculante), e os convites aceitos por ele ali são apagados, para que a política de INSERT por convite nunca mais o admita sem convite novo. Os outros recursos que ele criou ficam (o `user_id` é "criado por").
- **Remoção concorrente:** sem `FOR SHARE` em `ActiveMembershipRole`; o pedido em voo conclui e o seguinte dá 404, a mesma semântica da revogação de sessão (prometida no plano da etapa). Fica preso por teste.
- **E-mail do convite pelo `contaemail.Enviador`:** entra no teto global de 50 e no balde por destinatário sem código novo de cota. O `Preparar` espia o convite pelo hash (revogado antes do envio não sai). O `Concluir` é nulo: um convite descartado continua pendente, aparece no painel e pode ser reenviado ou revogado. O texto não leva mensagem livre de quem convida; leva o e-mail dele e o nome do projeto, em texto puro.
- **Balde por usuário na criação de convite:** `middleware.RateLimitUserEvery(ctx, clk, 30*time.Minute, 3)` (em memória, como `porProjetoNovo`). Vai para `ip_rate_limits` no PR 5, junto com os outros baldes.
- **Projetos: `UPDATE` só de `name`.** O gatilho `guard_projects_imutavel` (00048) recusa mudar `id`, `titular_id`, `titular_role`, `is_default` e `created_at` (42501). A 00050 libera `titular_id` só para o owner pendente aceitar (4.3).
- **`project_invitations` entra em `tabelasDoProjeto`** (a varredura de isolamento a cobre). A política do token cita `app.user_id` (comparação de e-mail): ela entra numa exceção nomeada de `TestNoProjectTablePolicyNamesAppUserID`, com o motivo, em vez de afrouxar o teste.
- **Portão do 2b sobre `cota_urls_com_regra_do_titular`:** guarda estática (T4) de que só a contagem de hosts de `internal/targets` a chama e de que o resultado nunca vai para DTO.
- **`tenancy.ChangeRole` e `tenancy.RemoveMember` (fatias) saem**, com `ErrLastOwner`: sem uso em produção e substituídos pelas regras com titular (Speculative Generality).
- **Cotas do PR 5 não entram aqui**, exceto o teto de membros. Na transferência (4.3), quem recebe passa pelos tetos que já existem (projetos 3/100 e as funções de cota da 00045, com os tetos atuais); os valores do plano grátis são do PR 5.

## Roteiro

| Sub-PR | Tarefas | Migration | Binário em sobreposição no release, e por que funciona |
|---|---|---|---|
| **4.1** membros e papéis | T1, T2, T3, T4 | 00048 | PR 3a.2. A FK é adiável e todo caminho do binário anterior grava projeto e owner na mesma transação. As políticas novas só alargam dentro de `app.project_id` (que só `InProjectTx` liga), e o binário anterior não lê `project_members` nesse escopo além do próprio vínculo |
| **4.2** convites | T5, T6, T7, T8 | 00049 | 4.1. A tabela é nova e o binário anterior a ignora. A política nova de INSERT em `project_members` só vale com `app.invitation_token_hash`, que ele nunca liga |
| **4.3** transferência | T9 | 00050 | 4.2. A coluna nova é anulável e o gatilho afrouxado só aceita a troca quando ela casa com o pendente. **Espera D2** |
| **4.4** painel | T10, T11, T12, T13, T14 | — | 4.2 (e 4.3 para a seção de transferência). O painel novo só chama rotas que já estão no ar. Rebase provável sobre o 3b (`rotas.tsx`, `Layout.tsx`, `mensagens.ts`). **T12 espera D6** |

Ordem: 4.1 → 4.2 → (4.3) → 4.4, cada um **publicado** antes do merge do seguinte. Se D2 atrasar, o 4.4 sai sem a seção de transferência e ela vem num PR de painel junto do 4.3.

## Esforço por tarefa

| Tarefa | Opus · esforço | Por quê |
|---|---|---|
| T1 | high | FK circular adiável, políticas novas, release com dois binários |
| T2 | medium | regras puras com testes de tabela |
| T3 | high | concorrência, remoção em voo, canais do removido |
| T4 | high | autorização em todas as rotas |
| T5 | high | RLS de dois escopos, política de INSERT por convite |
| T6 | high | teto sob concorrência, aceite atômico |
| T7 | medium | adaptador e texto, sobre o enviador existente |
| T8 | high | superfície nova, anti-enumeração |
| T9 | high | troca de titular com cotas de quem recebe |
| T10–T14 | medium | painel sobre contrato fixo |

Nunca Haiku.

## Estrutura de arquivos

**Criar:**
- `internal/storage/migrations/00048_membros_e_papeis.sql`, `00049_convites.sql`, `00050_transferencia_de_titular.sql`
- `internal/storage/queries/members.sql`, `queries/invitations.sql`
- `internal/storage/membros_rls_test.go`, `convites_rls_test.go`
- `internal/tenancy/members.go` (regras de membros e teto), `invitation.go` (tipos, token, regras de convite), `invitations_service.go` (`Invitations`), `transfer.go` (4.3), com os `_test.go`
- `internal/tenancy/pgstore/members.go`, `invitations.go`, `transfer.go`, com testes de integração
- `internal/tenancy/convitemail/convitemail.go` (+ teste): adaptador de `tenancy.InvitationMailer` sobre `account.AccountMailer`
- `internal/httpapi/memberhandlers.go`, `invitationhandlers.go` e os testes de integração; `internal/httpapi/testdata/contrato-membros.json`, `contrato-convites.json`
- `web/src/lib/permissoes.ts` e `permissoes.json`; `web/src/features/membros/*`; `web/src/features/convites/*`

**Alterar:**
- `internal/tenancy/role.go` (erros), `ports.go`, `service.go`, `membership.go` (sai); `internal/tenancy/pgstore/store.go` (comentário "sem UPDATE nem DELETE" de `EnsureDefault`)
- `internal/storage/scope.go` (dois escopos de convite), `queries/scope.sql`, `queries/projects.sql`, `queries/channels.sql`, `db/` (gerado: `make generate`)
- `internal/storage/guarda_escopo_test.go`, `rls_projeto_test.go`, `rls_pertenca_test.go`, `testdb/projeto.go` (semente de convite)
- `internal/account/emails.go` (`Links.AceitarConvite`, `emailDeConvite`)
- `internal/apierr/errors.go`; `internal/httpapi/routes.go`, `Deps`, `dto_estrito_internal_test.go`; `cmd/stillon` (fiação)
- `web/src/lib/api/{schemas,cliente,mensagens}.ts`, `web/src/test/api-falsa.ts`, `web/e2e/api-imitada.ts`, `web/src/app/rotas.tsx`, `Layout.tsx`, as telas existentes que escondem ações por papel

**Quem produz cada interface consumida por outra tarefa:**

| Interface | Produz | Consome |
|---|---|---|
| FK do titular, políticas de colegas/UPDATE/DELETE, gatilho de `projects`; queries de `members.sql` e `RenameProject`, `DisableEmailChannelsCreatedBy` | T1 | T3, T6, T9 |
| `tenancy.Authorize*`, `MemberLimit`, `ErrTitularProtected`, `ErrMemberLimit`, `ErrAlreadyMember`, `MemberView`, métodos novos de `ProjectRepository` e `Service` | T2 | T3, T4, T6, T9 |
| `pgstore.Store` (membros e renomear) | T3 | T4 |
| `project_invitations`, `InInvitationTokenTx`, `InInvitationAcceptTx`, queries de `invitations.sql` | T5 | T6, T7 |
| `tenancy.Invitations`, `InvitationRepository`, `InvitationMailer`, `InvitationEmail`, `ErrInvitationInvalid`, `ErrInvitationOtherAccount` | T6 | T7, T8 |
| `convitemail.Mailer`, `Links.AceitarConvite` | T7 | T8 |
| contrato HTTP (abaixo) | T4, T8, T9 | T11 |

**Contrato HTTP (fixo; a T11 pode começar só com ele).** Todas com sessão viva (`noProjeto` ou `protegida`) e `X-Requested-With` nas mutações.

| Rota | Corpo | Sucesso | Erros |
|---|---|---|---|
| `GET /api/v1/projects/{projectID}/members` | — | 200 `{"items":[{"user_id","email","role","is_titular","joined_at"}]}`, o titular primeiro e depois por `joined_at` | 404 |
| `PATCH /api/v1/projects/{projectID}/members/{userID}` | `{role}` | 200 item de membro | 400 `validation_failed` (`role`: `invalid`); 403 `forbidden`; 404; 409 `titular_protected` |
| `DELETE /api/v1/projects/{projectID}/members/{userID}` (o próprio id = sair) | — | 204 | 403; 404; 409 `titular_protected` |
| `PATCH /api/v1/projects/{projectID}` | `{name}` | 200 item de `GET /projects` | 400 (`name`: `required`/`too_long`/`invalid`); 403; 404 |
| `GET /api/v1/projects/{projectID}/invitations` | — | 200 `{"items":[{"id","email","role","created_at","expires_at"}]}` (só pendentes não vencidos) | 403; 404 |
| `POST /api/v1/projects/{projectID}/invitations` | `{email, role}` | 201 item de convite (igual com ou sem conta) | 400 (`email`: `invalid`; `role`: `invalid`); 403 `forbidden`/`email_not_verified`; 404; 409 `already_member`/`member_limit_reached`; 429 |
| `DELETE /api/v1/projects/{projectID}/invitations/{invitationID}` | — | 204 | 403; 404 |
| `POST /api/v1/invitations/accept` | `{token}` | 200 item de `GET /projects` (o projeto em que entrou) | 400 `token_invalid`; 403 `invitation_other_account`; 429 |
| 4.3: `POST /api/v1/projects/{projectID}/titular-transfer` | `{user_id}` | 202 | 403 (só o titular); 404; 409 `titular_transfer_target` (não é owner) |
| 4.3: `POST /api/v1/projects/{projectID}/titular-transfer/accept` | — | 200 item de projeto | 403 (não é o pendente); 404; 409 `project_limit_reached`/`target_limit_reached`/`heartbeat_limit_reached`/`channel_limit_reached` |
| 4.3: `DELETE /api/v1/projects/{projectID}/titular-transfer` | — | 204 (o titular ou o pendente cancelam) | 403; 404 |
| 4.3: `GET /api/v1/projects` | — | cada item ganha `"pending_titular": bool` (você é o pendente) | — |

---

# 4.1: membros e papéis

### Task 1: migration 00048 (titular sempre owner, colegas, UPDATE/DELETE de membros, renomear)

**Files:**
- Create: `internal/storage/migrations/00048_membros_e_papeis.sql`, `internal/storage/queries/members.sql`, `internal/storage/membros_rls_test.go`
- Modify: `internal/storage/queries/projects.sql` (`RenameProject`), `queries/channels.sql` (`DisableEmailChannelsCreatedBy`), `internal/storage/db/` (gerado), `internal/storage/rls_projeto_test.go` (motivo de `project_members` passa a "vínculo pessoa–projeto; a pessoa vê os próprios e, no escopo do projeto, os colegas")

**Interfaces:**
- Consumes: `testdb.New`, `testdb.NovoProjeto`, `testdb.Membro`, `emEscopoDeProjeto`, `codigoPG`.
- Produces (queries, todas sob `InProjectTx` salvo dito):
  - `members.sql`: `LockProjectMembers :exec` (`pg_advisory_xact_lock(hashtextextended('stillon.members:' || project_id::text, 0))`); `ListProjectMembers :many` → `user_id, email, role, is_titular (m.user_id = p.titular_id), created_at`, com `JOIN users` (sem RLS) e `JOIN projects`; `GetProjectMember :one` → `role, is_titular`; `UpdateMemberRole :exec`; `DeleteProjectMember :execrows`; `TitularIsOperator :one` (project_id) → bool;
  - `projects.sql`: `RenameProject :one` → `id, name, is_default, created_at`;
  - `channels.sql`: `DisableEmailChannelsCreatedBy :execrows` (`UPDATE notif_channels SET enabled = false WHERE project_id = $1 AND user_id = $2 AND kind = 'email' AND enabled`).

- [ ] **Step 1: testes que falham** (`membros_rls_test.go`; A titular de PA, B member de PA via `testdb.Membro`, C de fora)
  - `TestMemberSeesColleaguesOnlyInProjectScope`: em `emEscopoDeProjeto(B, PA)`, `SELECT count(*) FROM project_members` = 2; em `InUserTx(B)`, = 1 (só o próprio); em `emEscopoDeProjeto(C, PC)`, nenhum vínculo de PA.
  - `TestTitularAlwaysOwnerInDB`: via `InProjectTx(A, PA)`, `UPDATE project_members SET role = 'member' WHERE user_id = A` e `DELETE … WHERE user_id = A` dão `codigoPG(err) == "23503"` **no commit**; `INSERT INTO projects` sem vínculo de owner, num `InUserTx(A)`, dá 23503 no commit.
  - `TestProjectsUpdateOnlyName`: em `InProjectTx(A, PA)`, `UPDATE projects SET name = 'x'` passa; `SET titular_id = B`, `SET is_default = NOT is_default` e `SET created_at = now()` dão 42501; em `InProjectTx(C, PC)`, `UPDATE projects SET name = 'x' WHERE id = PA` afeta 0 linhas.
  - `TestMemberUpdateDeleteStayInsideProject`: em `emEscopoDeProjeto(C, PC)`, `UPDATE`/`DELETE` em vínculos de PA afetam 0 linhas.
  - `TestOlderBinaryProjectCreationStillCommits`: `storage.CriarProjetoPadraoNaTx` e a sequência `InsertProject` + `InsertProjectOwner` de `Store.Create` comitam com a 00048 aplicada.
  - `TestMembersMigrationDownRestoresCatalog`: `MigrateUpTo(00047)`, snapshot de políticas, constraints e colunas de `projects`/`project_members`; `MigrateUpTo(00048)`, `MigrateDownTo(00047)`, snapshot igual e linhas intactas.
- [ ] **Step 2:** `go test -race -count=1 -p 1 ./internal/storage -run 'TestMemberSeesColleagues|TestTitularAlwaysOwner|TestProjectsUpdateOnlyName|TestMemberUpdateDelete|TestOlderBinaryProjectCreation|TestMembersMigrationDown'`. Esperado: falhas por contagem (1 ≠ 2), `UPDATE` sem efeito e migration 00048 inexistente.
- [ ] **Step 3: implementar a 00048.** `SET LOCAL lock_timeout = '5s'`. Itens, nesta ordem:
  1. `ALTER TABLE projects ADD COLUMN titular_role text NOT NULL DEFAULT 'owner' CHECK (titular_role = 'owner')`;
  2. `ALTER TABLE project_members ADD CONSTRAINT project_members_papel_unico UNIQUE (project_id, user_id, role)`;
  3. `ALTER TABLE projects ADD CONSTRAINT projects_titular_e_owner FOREIGN KEY (id, titular_id, titular_role) REFERENCES project_members (project_id, user_id, role) DEFERRABLE INITIALLY DEFERRED` (valida na hora; um projeto sem owner faria o release falhar sem dano, e isso é inalcançável pelo portão do 2b);
  4. políticas, todas com `NULLIF(current_setting('app.project_id', true), '')::uuid`: `project_members_do_projeto_select` (SELECT), `project_members_do_projeto_update` (UPDATE, USING e WITH CHECK), `project_members_do_projeto_delete` (DELETE), por `project_id`; `projects_do_projeto_update` (UPDATE, USING e WITH CHECK `id = …`);
  5. função e gatilho `guard_projects_imutavel` BEFORE UPDATE ON projects, que recusa com 42501 a mudança de `id`, `titular_id`, `titular_role`, `is_default` ou `created_at`, com comentário dizendo que a 00050 afrouxa `titular_id`.
  Down na ordem inversa. Comentário de cabeçalho com o porquê da FK adiável e da ausência de recursão.
- [ ] **Step 4:** queries acima; `make generate`; os testes do Step 2 passam. Sabotagem: tire `DEFERRABLE INITIALLY DEFERRED` e veja `TestOlderBinaryProjectCreationStillCommits` falhar; tire a política de SELECT e veja a contagem voltar a 1.
- [ ] **Step 5:** `go test -race -count=1 -p 1 ./internal/storage ./internal/tenancy/... ./internal/account` verde (inclui `TestEveryTableForcesRLSWithAPolicy` e as varreduras).
- [ ] **Step 6: commit** `feat(storage): titular sempre owner e membros visíveis no escopo do projeto`

### Task 2: regras de membros no domínio

**Files:**
- Create: `internal/tenancy/members.go`, `internal/tenancy/members_test.go`
- Modify: `internal/tenancy/role.go`, `ports.go`, `service.go`, `service_test.go`; Delete: `membership.go`, `membership_test.go`

**Interfaces:**
- Produces:
  - `var ErrTitularProtected, ErrMemberLimit, ErrAlreadyMember error` (em `role.go`; `ErrLastOwner` sai);
  - `const MaxMembersPerProject = 3` (além do titular), `MaxMembersForOperator = 100`; `func MemberLimit(titularIsOperator bool) int`;
  - `type MemberView struct { UserID uuid.UUID; Email string; Role Role; IsTitular bool; JoinedAt time.Time }`;
  - `func AuthorizeInvite(actor ProjectScope, role Role) error`;
  - `func AuthorizeRoleChange(actor ProjectScope, target MemberView, to Role) error`;
  - `func AuthorizeRemoval(actor ProjectScope, target MemberView) error`;
  - `ProjectRepository` ganha `ListMembers(ctx, actorID, projectID uuid.UUID) ([]MemberView, error)`, `ChangeRole(ctx, actorID, projectID, targetID uuid.UUID, to Role) (MemberView, error)`, `RemoveMember(ctx, actorID, projectID, targetID uuid.UUID) error`, `Rename(ctx, actorID, projectID uuid.UUID, name string) (ProjectSummary, error)`;
  - `Service` ganha `Members`, `ChangeRole(ctx, actorID, projectID, targetID uuid.UUID, role string)`, `RemoveMember`, `Rename(ctx, actorID, projectID uuid.UUID, name string)`: validam a entrada (`ParseRole`, `ParseProjectName`) e delegam; a autorização roda no repositório, com o papel lido na transação.

- [ ] **Step 1: testes que falham** (`members_test.go`, `[]struct` de casos)
  - `TestAuthorizeInviteMatrix`: manager convida member e viewer → nil; manager convida manager ou owner → `ErrForbidden`; owner convida qualquer um → nil; member e viewer → `ErrForbidden`; papel desconhecido → `ErrUnknownRole`.
  - `TestAuthorizeRoleChangeAndRemoval`: owner muda papel de member → nil; manager muda papel → `ErrForbidden`; owner rebaixa o titular → `ErrTitularProtected`; owner rebaixa a si mesmo, não titular → nil; viewer sai (alvo = ele) → nil; titular sai → `ErrTitularProtected`; owner remove manager → nil; manager remove member → `ErrForbidden`.
  - `TestMemberLimit`: `MemberLimit(false) == 3`, `MemberLimit(true) == 100`.
  - `service_test.go`: `TestServiceChangeRoleRejectsUnknownRoleBeforeRepo` (repositório falso não é chamado) e `TestServiceRenameValidatesName`.
- [ ] **Step 2:** `go test -count=1 ./internal/tenancy`. Esperado: símbolos indefinidos.
- [ ] **Step 3: implementar.** Ordem das conferências em `AuthorizeRoleChange`: permissão (`PermManageMembers`), depois titular. Em `AuthorizeRemoval`: alvo = actor → só o titular é recusado; senão, permissão e depois titular. Apagar `membership.go`, `membership_test.go` e `ErrLastOwner`. Atualizar o falso de `ProjectRepository` em `service_test.go` e onde mais ele for implementado (`git grep -n "tenancy.ProjectRepository"`).
- [ ] **Step 4:** testes verdes; `TestTenancyImportsNoInfrastructure` verde.
- [ ] **Step 5: commit** `feat(tenancy): regras de convite, papel e remoção com o titular protegido`

### Task 3: membros e renomear no `pgstore`

**Files:**
- Create: `internal/tenancy/pgstore/members.go`, `members_test.go`
- Modify: `internal/tenancy/pgstore/store.go` (comentário de `EnsureDefault`)

**Interfaces:**
- Consumes: queries da T1; regras da T2.
- Produces: `(*Store).ListMembers`, `ChangeRole`, `RemoveMember`, `Rename`, cumprindo `tenancy.ProjectRepository`.

- [ ] **Step 1: testes que falham** (integração; A titular, B manager, C member, D viewer de PA)
  - `TestListMembersTitularFirst`: os quatro, A primeiro com `IsTitular`, e-mails certos.
  - `TestChangeRoleRequiresOwnerInTx`: B (manager) muda C → `ErrForbidden`; A muda C para viewer → `MemberView.Role == viewer`; A muda A → `ErrTitularProtected`; alvo inexistente → `tenancy.ErrNotMember`.
  - `TestRemovedMemberEmailChannelsDisabled`: C criou em PA um canal de e-mail e um Discord, e em PC (projeto dele) um de e-mail. A remove C: o de e-mail de PA fica `enabled = false`; o Discord de PA e o de PC ficam como estavam.
  - `TestLeaveProject`: D remove D → nil, e `Membership(D, PA)` dá `ErrProjectNotFound`; A remove A → `ErrTitularProtected`.
  - `TestRemovedMemberNextRequestIsNotFound`: depois da remoção, `InProjectTx(C, PA)` dá `ErrProjectNotFound` e `fn` não roda.
  - `TestRemovalLetsInFlightRequestFinish`: C abre `InProjectTx(C, PA)` e para num canal; A remove C e comita; C retoma, cria um alvo e comita sem erro; o pedido seguinte de C dá 404.
  - `TestRenameProjectOwnerOnly`: B → `ErrForbidden`; A → nome novo, `IsDefault` preservado.
  - `TestTitularDemotionBlockedByFKEvenIfDomainSkipped`: chama `UpdateMemberRole` direto sob `InProjectTx(A, PA)` para A → erro 23503 no commit (prova a segunda barreira).
- [ ] **Step 2:** rodar; falham por método inexistente.
- [ ] **Step 3: implementar.** Toda mutação: `InProjectTx(actor, projeto)` → `LockProjectMembers` → `GetProjectMember(alvo)` (`pgx.ErrNoRows` → `tenancy.ErrNotMember`) → `tenancy.Authorize*` com o escopo da transação → comando. `RemoveMember` chama `DisableEmailChannelsCreatedBy` antes do `DeleteProjectMember`. Erro 23503 do commit vira `tenancy.ErrTitularProtected` (defesa; o domínio já recusa antes).
- [ ] **Step 4:** testes verdes. Sabotagem: tire `DisableEmailChannelsCreatedBy` e veja o teste de canais falhar; troque o escopo da transação pelo do chamador (passe `RoleOwner` fixo) e veja `TestChangeRoleRequiresOwnerInTx` falhar.
- [ ] **Step 5: commit** `feat(tenancy): listar, mudar papel, remover membros e renomear projeto`

### Task 4: rotas de membros e renomear, e 403 por papel em toda rota

**Files:**
- Create: `internal/httpapi/memberhandlers.go`, `membros_integration_test.go`, `papeis_rotas_integration_test.go`, `testdata/contrato-membros.json`
- Modify: `internal/httpapi/routes.go`, `projecthandlers.go` (`Rename`), `dto_estrito_internal_test.go`, `internal/apierr/errors.go` (`CodeTitularProtected = "titular_protected"`, 409, "O titular do projeto não pode sair nem perder o papel de owner."), `web/src/test/api-falsa.ts` (`ERROS_DO_SERVIDOR`), `internal/storage/guarda_escopo_test.go` (guarda do portão do 2b)

**Interfaces:**
- Consumes: `tenancy.Service` (T2) com o `pgstore.Store` (T3).
- Produces: as quatro primeiras rotas do contrato, todas em `noProjeto` (o `PATCH` de projeto também, para o 404 de não membro).

- [ ] **Step 1: testes que falham**
  - `membros_integration_test.go`: um caso por linha do contrato (sucesso e cada erro), com o JSON conferido contra `contrato-membros.json`; `TestAnotherProjectsMembersIndistinguishableFromMissing` (não membro recebe o mesmo 404, byte a byte, de um UUID inexistente).
  - `TestEveryMutatingProjectRouteRefusesViewer` (`papeis_rotas_integration_test.go`): enumera as rotas registradas no mux que casam `^(POST|PATCH|PUT|DELETE) /api/v1/projects/\{projectID\}/` e, para cada uma, com um viewer de PA e corpo válido mínimo de uma tabela `corpoMinimo[rota]`, exige 403 `forbidden`. Rota nova sem entrada em `corpoMinimo` reprova com "rota de mutação sem caso de papel". Segundo subteste: para as rotas de canais, status pages, convites e membros (exceto `DELETE` do próprio vínculo), um member recebe 403.
  - `TestQuotaURLsNeverLeaveTargetsQuota` (`guarda_escopo_test.go`, AST): `CotaUrlsComRegraDoTitular` só é chamada em `internal/targets/` e o tipo da linha nunca aparece em arquivo de DTO de `internal/httpapi`.
- [ ] **Step 2:** rodar; 404/405 nas rotas novas, e a enumeração reprova pelas rotas sem caso.
- [ ] **Step 3: implementar** os handlers (tradução: `ErrForbidden` → 403 `forbidden`, `ErrNotMember` → 404, `ErrTitularProtected` → 409, `ErrUnknownRole` → 400 `role: invalid`, erros de nome como em `recusarCriacao`); registrar em `routes.go` com `noProjeto`; preencher `corpoMinimo` para todas as rotas de mutação existentes.
- [ ] **Step 4:** `go test -race -count=1 -p 1 ./internal/httpapi ./internal/storage` verde. Sabotagem: tire o `Require(PermManageChannels)` de `notify.Service.Create` e veja a enumeração falhar.
- [ ] **Step 5:** `go build ./... && go vet ./... && make test`.
- [ ] **Step 6: commit** `feat(httpapi): rotas de membros e renomear projeto, com 403 por papel em toda rota`. Abrir o PR 4.1.

---

# 4.2: convites

### Task 5: migration 00049 (`project_invitations`) e escopos do token

**Files:**
- Create: `internal/storage/migrations/00049_convites.sql`, `internal/storage/queries/invitations.sql`, `internal/storage/convites_rls_test.go`
- Modify: `internal/storage/scope.go`, `internal/storage/db/` (gerado), `rls_projeto_test.go` (`project_invitations` em `tabelasDoProjeto`), `rls_pertenca_test.go` (exceção nomeada), `guarda_escopo_test.go` (o GUC novo só em `scope.go`), `internal/storage/testdb/projeto.go` (`Convite(t, projeto, email, papel string) (token string)` para as sementes)

**Interfaces:**
- Produces:
  - tabela `project_invitations(id uuid PK, project_id uuid NOT NULL REFERENCES projects ON DELETE CASCADE, email text NOT NULL CHECK (email = lower(email) AND char_length(email) BETWEEN 3 AND 254), role text NOT NULL CHECK (role IN ('owner','manager','member','viewer')), token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32), invited_by uuid REFERENCES users ON DELETE SET NULL, created_at timestamptz NOT NULL, expires_at timestamptz NOT NULL CHECK (expires_at > created_at), accepted_at timestamptz, accepted_by uuid REFERENCES users ON DELETE CASCADE, CHECK ((accepted_at IS NULL) = (accepted_by IS NULL)))`; `UNIQUE (project_id, email) WHERE accepted_at IS NULL`; índice `(project_id)`;
  - `func InInvitationTokenTx(ctx context.Context, b Beginner, tokenHash []byte, fn func(*db.Queries) error) error`: só `app.invitation_token_hash` (espiar, para o `Preparar` do e-mail);
  - `func InInvitationAcceptTx(ctx context.Context, b Beginner, userID uuid.UUID, tokenHash []byte, fn func(*db.Queries) error) error`: `app.user_id` e `app.invitation_token_hash` no mesmo `set_config` duplo; `ErrNoUser`/`ErrNoToken` como as irmãs;
  - queries: `DeleteStaleInvitations :exec` (project_id, now: pendentes vencidos e aceitos há mais de 30 dias), `DeletePendingInvitationForEmail :exec`, `CountMembersBesidesTitular :one`, `CountPendingInvitations :one` (project_id, now), `IsMemberEmail :one`, `InsertInvitation :one`, `ListPendingInvitations :many` (project_id, now), `DeleteInvitation :execrows` (project_id, id), `InvitationContext :one` (project_id, inviter) → `project_name, inviter_email`, `PeekInvitation :one` (token_hash, now) → `project_id, email, role`, `AcceptInvitation :one` (token_hash, now) → `project_id, role`, `InsertMemberFromInvitation :exec`, `DeleteAcceptedInvitationsOf :exec` (project_id, user_id).

- [ ] **Step 1: testes que falham** (`convites_rls_test.go`; A titular de PA, B com conta `b@x.com`, C com `c@x.com`; convite de PA para `b@x.com`)
  - `TestInvitationsVisibleOnlyInOwnProjectScope`: em `emEscopoDeProjeto(A, PA)` vê 1; em `InUserTx(A)`, 0; no escopo de outro projeto de A, 0.
  - `TestTokenScopeSeesOnlyItsRow`: `InInvitationTokenTx(hash)` vê exatamente a linha do hash e nenhuma outra de `project_invitations`, nem de `project_members` ou `projects`.
  - `TestAcceptByOtherAccountRefusedInDB`: `InInvitationAcceptTx(C, hash)` → `AcceptInvitation` sem linha; `InsertMemberFromInvitation(PA, C, …)` → 42501.
  - `TestAcceptInsertsOnlyInvitedRoleAndProject`: em `InInvitationAcceptTx(B, hash)`, depois do `AcceptInvitation`, inserir B com outro papel ou em outro projeto → 42501; com o papel do convite → passa.
  - `TestMemberInsertWithoutAcceptedInvitationRefused`: `InUserTx(B)` inserindo `(PA, B, 'viewer')` → 42501 (a política da 00039 continua só para o titular).
  - `TestInvitationsMigrationDownRestoresCatalog`: up/down/up com snapshot igual.
  - A varredura `TestProjectScopeSeesNothingOfOtherProjectOfSameUser` passa a semear `project_invitations` (via `testdb.Convite`) e falha antes da tabela existir.
- [ ] **Step 2:** rodar; tabela inexistente.
- [ ] **Step 3: implementar a 00049.** Políticas (por comando, no estilo da 00034):
  - `project_invitations_do_projeto_select|insert|delete`: `project_id = NULLIF(current_setting('app.project_id', true), '')::uuid`; o INSERT exige também `invited_by = NULLIF(current_setting('app.user_id', true), '')::uuid`;
  - `project_invitations_por_token_select`: `token_hash = decode(NULLIF(current_setting('app.invitation_token_hash', true), ''), 'hex')`;
  - `project_invitations_por_token_update`: USING = o hash `AND accepted_at IS NULL AND email = (SELECT u.email FROM users u WHERE u.id = NULLIF(current_setting('app.user_id', true), '')::uuid)`; WITH CHECK = o hash `AND accepted_by = NULLIF(current_setting('app.user_id', true), '')::uuid`;
  - `project_members_por_convite_insert` ON `project_members` FOR INSERT: `user_id = app.user_id AND EXISTS (SELECT 1 FROM project_invitations i WHERE i.token_hash = decode(…) AND i.project_id = project_members.project_id AND i.role = project_members.role AND i.accepted_by = project_members.user_id)`. Comentário: sem ciclo, porque nenhuma política de `project_invitations` consulta `project_members`.
  `AcceptInvitation` é um `UPDATE … SET accepted_at = sqlc.arg(now), accepted_by = NULLIF(current_setting('app.user_id', true), '')::uuid WHERE token_hash = … AND accepted_at IS NULL AND expires_at > sqlc.arg(now) RETURNING project_id, role`. Exceção em `TestNoProjectTablePolicyNamesAppUserID`: `project_invitations_por_token_update` e `project_invitations_do_projeto_insert`, com o motivo.
- [ ] **Step 4:** `make generate`; verdes. Sabotagem: tire a comparação de e-mail do USING e veja `TestAcceptByOtherAccountRefusedInDB` falhar; tire `i.role = project_members.role` e veja o teste de papel falhar.
- [ ] **Step 5: commit** `feat(storage): convites de projeto com escopo do projeto e do token`

### Task 6: serviço de convites no domínio e no `pgstore`

**Files:**
- Create: `internal/tenancy/invitation.go`, `invitations_service.go` (+ `_test.go`), `internal/tenancy/pgstore/invitations.go` (+ `invitations_test.go`)
- Modify: `internal/tenancy/ports.go`, `internal/tenancy/pgstore/members.go` (`RemoveMember` chama `DeleteAcceptedInvitationsOf`)

**Interfaces:**
- Consumes: T2, T5.
- Produces:
  - `const InvitationTTL = 7 * 24 * time.Hour`; `var ErrInvitationInvalid, ErrInvitationOtherAccount error`;
  - `type Invitation struct { ID, ProjectID uuid.UUID; Email string; Role Role; CreatedAt, ExpiresAt time.Time }`;
  - `type NewInvitation struct { ID uuid.UUID; Email string; Role Role; TokenHash []byte; Now time.Time }`;
  - `type InvitationEmail struct { To, ProjectName, InviterEmail string; Role Role; Token string; TokenHash []byte }`;
  - `func newInvitationToken() (token string, hash []byte, err error)` (32 bytes de `crypto/rand`, `base64.RawURLEncoding`, `sha256.Sum256`);
  - `type InvitationRepository interface { Create(ctx, actorID, projectID uuid.UUID, inv NewInvitation) (Invitation, InvitationEmail, error); ListPending(ctx, actorID, projectID uuid.UUID, now time.Time) ([]Invitation, error); Revoke(ctx, actorID, projectID, invitationID uuid.UUID) error; Accept(ctx, userID uuid.UUID, tokenHash []byte, now time.Time) (ProjectSummary, error) }` (o `InvitationEmail` volta com `ProjectName` e `InviterEmail` preenchidos; o serviço completa `Token`);
  - `type InvitationMailer interface { SendInvitation(ctx context.Context, e InvitationEmail) error }` (enfileira e volta);
  - `type Invitations struct{…}`; `func NewInvitations(repo InvitationRepository, mailer InvitationMailer, clk clock.Clock) *Invitations`; métodos `Invite(ctx, actorID, projectID uuid.UUID, email, role string) (Invitation, error)` (`email` já normalizado por `account.ValidateEmail` no handler), `ListPending`, `Revoke`, `Accept(ctx, userID uuid.UUID, token string) (ProjectSummary, error)`.

- [ ] **Step 1: testes que falham**
  - `invitations_service_test.go` (falsos): `TestInviteSendsAfterCommitWithToken` (o mailer recebe `Token` cujo SHA-256 é o `TokenHash` gravado, e só depois de `Create` voltar sem erro); `TestInviteDoesNotSendWhenRepoFails`; `TestAcceptHashesTokenBeforeRepo` (token vazio ou que não decodifica em 32 bytes → `ErrInvitationInvalid` sem ir ao repositório).
  - `pgstore/invitations_test.go` (integração; A titular, M manager, V viewer de PA):
    - `TestPendingInvitationsCountTowardLimit`: com 1 membro além do titular e 2 pendentes, o quarto convite → `ErrMemberLimit`; vencido não conta (relógio falso avança 7 dias e 1 s);
    - `TestConcurrentInvitesAtLimitOnlyOnePasses`: 2 vagas ocupadas, 5 `Create` simultâneos para e-mails diferentes → exatamente 1 sucesso, 4 `ErrMemberLimit`;
    - `TestReinviteSameEmailKeepsOneSlot`: convidar `x@y` duas vezes → 1 pendente, o hash antigo não espia mais (`PeekInvitation` sem linha);
    - `TestInviteExistingMemberIsAlreadyMember`;
    - `TestOperatorTitularHasLargerMemberLimit` (`testdb` marca `is_operator`; o quarto convite passa);
    - `TestManagerCannotInviteOwner` (`ErrForbidden`, nada gravado) e `TestViewerCannotListInvitations`;
    - `TestConcurrentAcceptOnlyOneWins`: 5 `Accept` simultâneos do mesmo usuário e token → 1 `ProjectSummary`, 4 `ErrInvitationInvalid`; 1 vínculo;
    - `TestAcceptOtherAccount`: conta com outro e-mail → `ErrInvitationOtherAccount`, convite continua pendente;
    - `TestRevokedOrExpiredInvitationIsTokenInvalid`;
    - `TestAcceptedInvitationCannotReinsertRemovedMember`: B aceita, A remove B; um `InInvitationAcceptTx(B, hash)` cru com `InsertMemberFromInvitation` → 42501.
- [ ] **Step 2:** rodar; símbolos indefinidos.
- [ ] **Step 3: implementar.**
  - `Create`: `InProjectTx(actor, projeto)` → `tenancy.AuthorizeInvite(sc, role)` → `LockProjectMembers` → `DeleteStaleInvitations` → `IsMemberEmail` (→ `ErrAlreadyMember`) → `DeletePendingInvitationForEmail` → `TitularIsOperator`, `CountMembersBesidesTitular` + `CountPendingInvitations` ≥ `MemberLimit` → `ErrMemberLimit` → `InsertInvitation` → `InvitationContext`.
  - `Accept`: `InInvitationTokenTx` + `PeekInvitation` (sem linha → `ErrInvitationInvalid`; e-mail ≠ o da conta, lido por `GetAccount` → `ErrInvitationOtherAccount`); depois `InInvitationAcceptTx(user, hash)`: `LockProjectMembers(project)` → `AcceptInvitation` (sem linha → `ErrInvitationInvalid`) → `InsertMemberFromInvitation` (23505 = já membro: devolve o projeto, idempotente) → lê o `ProjectSummary` (o papel do convite).
- [ ] **Step 4:** verdes. Sabotagem: tire `LockProjectMembers` do `Create` e veja o teste concorrente falhar (rodar `-count=20`); some só os membros (sem os pendentes) e veja `TestPendingInvitationsCountTowardLimit` falhar.
- [ ] **Step 5: commit** `feat(tenancy): convites com teto de membros, reenvio e aceite de uso único`

### Task 7: e-mail do convite

**Files:**
- Create: `internal/tenancy/convitemail/convitemail.go`, `convitemail_test.go`
- Modify: `internal/account/emails.go` (+ `emails_test.go`)

**Interfaces:**
- Consumes: `account.AccountMailer`, `account.EntregaDeEmail`, `account.Links`; `storage.InInvitationTokenTx`, `PeekInvitation` (T5); `tenancy.InvitationEmail` (T6).
- Produces:
  - `func (l Links) AceitarConvite(token string) string` → `base + "/invite#token=" + token`;
  - `func EmailDeConvite(projeto, quemConvidou string, papel string, link string) (assunto, corpo string)` (exportada: o adaptador está em outro pacote). Rascunho; a T12 submete ao dono;
  - `type Mailer struct{…}`; `func New(pool *pgxpool.Pool, clk clock.Clock, envio account.AccountMailer, links account.Links) *Mailer`; `(*Mailer).SendInvitation(ctx, e tenancy.InvitationEmail) error`, que monta o texto e chama `SendAccountEmail` com `EntregaDeEmail{Preparar: espia o convite pelo hash, Concluir: nil}`.

- [ ] **Step 1: testes que falham**
  - `TestInvitationEmailSameForAnyRecipient`: o corpo para um endereço com conta e para um sem conta é idêntico (mesmos argumentos), e não contém "crie", "já tem conta" nem nada que dependa da conta.
  - `TestInvitationEmailCarriesLinkAloneOnALine`, `TestInvitationEmailStripsControlCharsFromProjectName` (o nome já passa por `ParseProjectName`, mas `\r\n` num nome antigo não pode quebrar o texto).
  - `TestPrepareSkipsRevokedInvitation` (integração): convite revogado antes do envio → `Preparar` devolve `false`.
  - `TestInvitationHashMatchesCryptoHashToken`: o hash gerado por `tenancy` para um token é igual a `crypto.HashToken(token)` (via função de teste exportada em `export_test.go` do tenancy).
- [ ] **Step 2:** rodar e ver falhar.
- [ ] **Step 3: implementar.** O assunto e o corpo seguem o tom de `emails.go`: "Olá,", o e-mail de quem convidou e o nome do projeto, o link sozinho na linha, "vale por 7 dias e só pode ser usado uma vez", e "se não esperava este convite, ignore". Sem nome de papel em inglês: o rótulo vem de um mapa em `emails.go` (rascunho até a T12).
- [ ] **Step 4:** verdes.
- [ ] **Step 5: commit** `feat(convites): e-mail do convite pelo enviador de conta`

### Task 8: rotas de convite e aceite

**Files:**
- Create: `internal/httpapi/invitationhandlers.go`, `convites_integration_test.go`, `testdata/contrato-convites.json`
- Modify: `internal/httpapi/routes.go` (`Deps.Invitations *tenancy.Invitations`, com pânico se nil como `Projects`), os três testes sem banco que montam `Deps` e `newAPICom`, `dto_estrito_internal_test.go`, `papeis_rotas_integration_test.go` (`corpoMinimo` das rotas novas), as exceções de `TestContaNaoConfirmadaNaoCriaPorNenhumaRota` (aceite: "o token prova a caixa de entrada; não cria recurso"), `internal/apierr/errors.go` (`member_limit_reached` 409, `already_member` 409, `invitation_other_account` 403), `web/src/test/api-falsa.ts`, `cmd/stillon` (fiação: `convitemail.New(pool, clk, enviadorDeConta, links)`; com o envio desligado, `contaemail.SemEnvio`)

**Interfaces:**
- Consumes: T6, T7; `account.ValidateEmail`.
- Produces: as rotas de convite e aceite do contrato. Cadeias: listar e revogar em `noProjeto`; criar em `authn(porUsuario(porConvite(viva(doCaminho(criacao(…))))))`, com `porConvite := middleware.RateLimitUserEvery(ctx, d.Clock, 30*time.Minute, 3)`; aceite em `authn(porUsuario(porAceite(viva(…))))`, com `porAceite := middleware.RateLimitUserEvery(ctx, d.Clock, time.Minute, 5)`.

- [ ] **Step 1: testes que falham**
  - Um caso por linha do contrato, conferido contra `contrato-convites.json`.
  - `TestInviteResponseIdenticalWithOrWithoutAccount`: convidar `tem@x` (com conta) e `nao@x` (sem conta) dá o mesmo status e o mesmo corpo, a menos de `id`, `email` e instantes; o mailer falso recebe o mesmo assunto e o corpo igual a menos do link. Repetir com o `contaemail.Enviador` real sobre um teto esgotado (`TetoDeEmailsDeConta` com 50 reservas feitas): resposta igual, nenhum envio.
  - `TestUnverifiedAccountCannotInvite` (403 `email_not_verified`) e `TestUnverifiedAccountCanAccept` (200).
  - `TestAcceptJoinsProjectAndListsIt`: depois do aceite, `GET /api/v1/projects` de B traz PA com o papel do convite, e `GET /projects/PA/members` de A traz B.
  - `TestInviteRateLimitedPerUser`: o quarto convite seguido → 429.
- [ ] **Step 2:** rodar e ver falhar.
- [ ] **Step 3: implementar** os handlers. Tradução: `ErrInvitationInvalid` → 400 `token_invalid`; `ErrInvitationOtherAccount` → 403; `ErrMemberLimit` → 409 `member_limit_reached`; `ErrAlreadyMember` → 409 `already_member`; `ErrForbidden` → 403; e-mail inválido → 400 `email: invalid`.
- [ ] **Step 4:** `go test -race -count=1 -p 1 ./internal/httpapi ./internal/tenancy/... ./internal/storage ./internal/contaemail ./cmd/stillon` verde.
- [ ] **Step 5:** `go build ./... && go vet ./... && make test`.
- [ ] **Step 6: commit** `feat(httpapi): convidar, listar, revogar e aceitar convites`. Abrir o PR 4.2.

---

# 4.3: transferência de titularidade (espera D2; tarefa escrita para a opção recomendada, em dois passos)

### Task 9: propor, aceitar e cancelar a transferência

**Files:**
- Create: `internal/storage/migrations/00050_transferencia_de_titular.sql`, `internal/tenancy/transfer.go` (+ teste), `internal/tenancy/pgstore/transfer.go` (+ teste), handlers e testes em `internal/httpapi/projecthandlers.go` / `transferencia_integration_test.go`
- Modify: `queries/projects.sql` (`ProposeTitularTransfer`, `CancelTitularTransfer`, `AcceptTitularTransfer`, `ListMyProjects` com `pending_titular`), `internal/apierr/errors.go` (`titular_transfer_target` 409), `tenancy.ProjectSummary` (`PendingTitular bool`), contrato de projetos (`contrato-projetos.json`)

**Interfaces:**
- Produces:
  - coluna `projects.pending_titular_id uuid REFERENCES users ON DELETE SET NULL`, com `CHECK (pending_titular_id IS DISTINCT FROM titular_id)`;
  - `guard_projects_imutavel` passa a aceitar mudança de `titular_id` só quando `NEW.titular_id = OLD.pending_titular_id AND NEW.pending_titular_id IS NULL` e o novo titular é `app.user_id`; `pending_titular_id` pode ser gravado e limpo;
  - função `cota_do_receptor(projeto uuid)` no molde das da 00045: exige que `app.user_id` seja o `pending_titular_id` do projeto (visível pela RLS) e devolve `(projetos, alvos, heartbeats, canais)` já com os recursos do projeto somados aos do receptor;
  - `func AuthorizeTransferProposal(actor ProjectScope, titularID uuid.UUID, target MemberView) error` (só o titular; o alvo tem de ser owner, senão `ErrTransferTarget`);
  - `ProjectRepository` ganha `ProposeTransfer(ctx, actorID, projectID, targetID uuid.UUID) error`, `AcceptTransfer(ctx, userID, projectID uuid.UUID) (ProjectSummary, error)` e `CancelTransfer(ctx, userID, projectID uuid.UUID) error`.
- [ ] **Step 1: testes que falham:** `TestOnlyTitularProposesToAnOwner`; `TestAcceptMovesTitularAndQuota` (depois do aceite, `cota_alvos_do_titular` conta os alvos do projeto para o novo titular e não para o antigo; o antigo continua owner e agora pode sair); `TestAcceptRefusedOverReceiverLimits` (receptor com 3 projetos → `ErrProjectLimit`; com alvos no teto → `targets.ErrLimit`, sob as travas `stillon.projects:<receptor>` e `stillon.targets:<receptor>`); `TestDemotingPendingClearsProposal` (rebaixar ou remover o pendente limpa `pending_titular_id`, na T3 estendida); `TestTitularIDChangeOutsideAcceptRefusedInDB` (42501); `TestConcurrentAcceptAndCreateRespectQuota`.
- [ ] **Step 2–4:** ver falhar, implementar (aceite sob `InProjectTx(receptor, projeto)` → `LockProjectMembers` → travas do receptor na ordem projetos, alvos, heartbeats, canais → `cota_do_receptor` → `AcceptTitularTransfer`), ver passar com sabotagem das travas.
- [ ] **Step 5:** `make test`; commit `feat(tenancy): transferência de titularidade com aceite de quem recebe`. Abrir o PR 4.3.

---

# 4.4: painel

### Task 10: permissões no painel e ações escondidas por papel

**Files:**
- Create: `web/src/lib/permissoes.json`, `web/src/lib/permissoes.ts` (+ teste); `internal/tenancy/matriz_painel_test.go`
- Modify: telas que criam/editam (alvos, heartbeats, incidentes, canais, status page) e o `Layout.tsx` (item "Membros")

**Interfaces:**
- Produces: `permissoes.json` = `{ "viewer": ["read"], "member": [...], ... }` com os nomes `read`, `edit_monitors`, `manage_channels`, `manage_status_page`, `invite_basic`, `manage_members`, `manage_project`; `function pode(papel: Papel, p: Permissao): boolean`; `function usePode(p: Permissao): boolean` (lê `useProjeto().papel`).
- [ ] **Step 1:** `TestPainelPermissionsMatchDomain` (Go lê `web/src/lib/permissoes.json` e compara com `Role.Can` para todo par, por nome); `permissoes.test.ts` (viewer não edita; manager convida member e não manager). Testes de tela: com papel viewer, "Novo alvo", "Novo heartbeat", "Reconhecer", "Novo canal" e "Publicar página" não aparecem; com member, canais e status page aparecem só para leitura.
- [ ] **Step 2–4:** ver falhar (sabotagem: troque um valor do JSON), implementar, ver passar. As ações escondidas continuam protegidas pelo servidor (T4).
- [ ] **Step 5:** commit `feat(painel): ações por papel com a matriz do servidor`

### Task 11: cliente, esquemas e APIs falsas

**Files:** `web/src/lib/api/{schemas,cliente,mensagens}.ts` (+ testes de contrato), `web/src/test/api-falsa.ts`, `web/src/test/fixtures.ts`, `web/e2e/api-imitada.ts`

**Interfaces:**
- Produces: esquemas `Membro`, `ListaDeMembros`, `Convite`, `ListaDeConvites` (zod, não estritos); em `ApiDoProjeto`: `membros()`, `mudarPapel(userId, papel)`, `removerMembro(userId)`, `renomear(nome)`, `convites()`, `convidar({email, papel})`, `revogarConvite(id)`, e (4.3) `proporTransferencia`, `aceitarTransferencia`, `cancelarTransferencia`; na API da conta: `aceitarConvite(token)`; chaves de cache por projeto (`chaves.membros(projeto)`, `chaves.convites(projeto)`), no molde de `chaves-do-projeto.ts`; textos de erro dos códigos novos em `mensagens.ts`.
- [ ] Passos TDD com os testes de contrato existentes (`schemas-contrato.test.ts`, `chaves-do-projeto.test.ts`) estendidos; commit `feat(painel): cliente de membros e convites`.

### Task 12: proposta de design e textos (PARA para o dono)

- [ ] **Step 1:** com as skills de design, propor **duas** opções para: página "Membros" (lista, papel, titular, convidar, pendentes, sair, renomear), página de aceite, rótulos em português dos quatro papéis com uma linha do que cada um pode, e o texto do e-mail de convite (T7). Mostrar em capturas do Storybook-equivalente local ou num HTML estático na pasta de rascunho, sem publicar.
- [ ] **Step 2:** **parar** e pedir a decisão D6. Registrar a escolha no ledger.

### Task 13: página Membros

**Files:** `web/src/features/membros/PaginaMembros.tsx`, `ListaDeMembros.tsx`, `DialogoConvidar.tsx`, `ConvitesPendentes.tsx`, `RenomearProjeto.tsx` (+ testes); `web/src/app/rotas.tsx` (`projects/:projectId/members`)

- [ ] **Step 1: testes que falham** (Vitest + MSW): owner vê o seletor de papel e "Remover" em todos menos no titular; manager vê "Convidar" só com member/viewer no seletor; viewer vê só a lista; "Sair do projeto" pede confirmação e, ao sair, invalida `chaves.projetos` e navega para `/` (que escolhe outro projeto); 409 `member_limit_reached` mostra o texto do teto sem fechar o diálogo; reenviar um pendente chama `convidar` com o mesmo e-mail; renomear atualiza o seletor de projeto.
- [ ] **Step 2–4:** ver falhar, implementar com os componentes de `web/src/components/ui`, ver passar; `e2e/a11y.spec.ts` ganha a página.
- [ ] **Step 5:** commit `feat(painel): página de membros, convites e renomear projeto`

### Task 14: aceite do convite

**Files:** `web/src/features/convites/CapturaDoConvite.tsx`, `PaginaAceitarConvite.tsx` (+ testes); `web/src/app/rotas.tsx`

**Interfaces:**
- `/invite` fica **fora** de `ExigirSessao`: lê `#token=`, guarda em `sessionStorage['stillon.convite']`, limpa a barra com `history.replaceState` e navega para `/invite/accept`, que fica **dentro** de `ExigirSessao`. Assim o `?next=/invite/accept` do login devolve a pessoa sem perder o token, que nunca vai para a query.
- `/invite/accept`: botão "Aceitar convite" (sem POST automático); sucesso apaga o token guardado, invalida `chaves.projetos` e navega para `/projects/{id}/`; `token_invalid` mostra "convite inválido ou vencido; peça outro"; `invitation_other_account` mostra "este convite foi enviado para outro e-mail; saia e entre com a conta dele", com o botão de sair. Sem token guardado: a mesma tela do inválido.
- Sem sessão e com o cadastro ligado (`signup-config`), a tela de login já oferece "Criar conta" (PR 3b); nada novo aqui.
- [ ] **Step 1: testes que falham:** o token nunca aparece em `location.search` nem em pedido GET; recarregar `/invite/accept` mantém o token; o duplo clique em "Aceitar" faz um POST só; os dois erros mostram os textos aprovados na T12.
- [ ] **Step 2–4:** ver falhar, implementar, ver passar; jornada em `web/e2e/projetos.spec.ts` com `api-imitada.ts` (convidar, sair, entrar como convidado, aceitar, ver o projeto) e axe.
- [ ] **Step 5:** `cd web && npm test && npm run typecheck && npm run lint`; Playwright das specs afetadas; commit `feat(painel): aceite de convite pelo link`. Abrir o PR 4.4.

---

## Autorrevisão (feita)

- **Cobertura da spec:** Membership com papéis e invariante de owner (T1–T3); Invitation com e-mail normalizado, papel, hash, 7 dias e uso único (T5–T6); papéis viewer/member/manager/owner (T2, T4, T10); portas `InvitationRepository` e `Mailer` (T6, T7); RLS forçada com política em toda tabela nova (T1, T5, varreduras); "o usuário vê só os projetos de que é membro" mantido (a política nova é por `app.project_id`); membros e convites na API e no painel (T4, T8, T13, T14); cota de membros do titular, 3 além dele (T6); e-mails de convite no teto global e no remetente de conta (T7); limites de taxa de convite (T8, em memória até o PR 5); transferência por titular (T9, D2); renomear (T1, T3, T4). A exclusão de projeto fica no PR 6.
- **Portões do 2b:** FK adiável (T1), canais de e-mail do removido (T3), papel no banco (D3), PATCH de projeto (T1–T4), `cota_urls_com_regra_do_titular` (T4).
- **Fora de propósito:** cotas do plano grátis além de membros, baldes no banco, suspensão (PR 5); termos (PR 6); `NOT NULL` e rotas antigas (PR 7).
