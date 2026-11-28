# PR 6 — Exportar, excluir conta e textos legais: plano de implementação

> **Para agentes:** SUB-SKILL OBRIGATÓRIA: superpowers:subagent-driven-development (recomendada) ou superpowers:executing-plans, tarefa a tarefa. Os passos usam checkbox (`- [ ]`).

**Objetivo:**
- A pessoa exporta tudo o que é dela.
- Exclui a própria conta e os projetos que são só dela.
- Exclui projetos dos quais é owner.
- Aceita termos versionados no cadastro.
- No fim, o cadastro público liga em produção.

**Arquitetura:**
- **Exclusão de conta:** uma transação só, num escopo novo (`storage.InAccountDeletionTx`).
  - Entra em cada projeto pelo `ActiveMembershipRole`.
  - Trava na ordem que o resto do código já usa: titular → membros.
  - Recusa quando ela é titular de um projeto compartilhado.
  - Apaga os projetos só dela.
  - Remove o `users`. As FKs anulam o "criado por" nos recursos alheios.
- **Exportação:** um `io.Writer` em streaming, lendo cada projeto no seu `InProjectTx`.
- **Aceite:** duas colunas em `users`, mais uma constante de versão espelhada no painel.

**Stack:** Go 1.26, pgx v5 + sqlc, goose, testcontainers; React 19, TanStack Query 5, zod 4, Vitest + MSW, Playwright + axe.

**Spec:** `docs/superpowers/specs/2026-11-22-multiinquilino-cadastro-design.md`. Seções Contas (Exportar, Excluir conta), Textos legais, Testes e Fica com o builder.

## Restrições globais

**Processo**
- Tudo em pt-BR. Commits no formato `tipo(escopo): descrição`. Busca com `git grep`.
- Nada é declarado pronto sem `go build ./... && go vet ./... && make test`.
- Um teste só conta depois de visto falhando com o código sabotado. Cada teste deste plano traz a sua sabotagem.

**Migrations**
- Os números são relativos: M e M+1. Cada uma pega o próximo número livre no dia.
  - Hoje a main está em 00048; 00049 é do 4.2, 00050 do 4.3, e o PR 5 usa os seguintes.
- goose com `SET LOCAL lock_timeout = '5s'` e seção Down.
- Expand → migrate → contract: todo sub-PR sobe com o binário anterior ainda rodando.

**Contratos que toda rota nova cumpre**
- `set_config` só em `internal/storage/scope.go`. Arquivo novo que abre escopo entra nas listas do `guarda_escopo_test.go`.
- Código de erro novo entra em `internal/apierr/errors.go` e no `ERROS_DO_SERVIDOR` de `web/src/test/api-falsa.ts`.
- Handler que decodifica JSON entra no `corposEstritos()`.
- Rota mutante de projeto ganha entrada no `corpoMinimo` do `TestEveryMutatingProjectRouteRefusesViewer`.
- Rota autenticada nova entra no `TestProtectedRoutesRequireAuthentication`.

**Textos legais**
- Nunca publicar nome da empresa nem CNPJ. O operador aparece como "[operador]".

**Segredos**
- O dono nunca cola segredo no chat. Segredos vão para o Bitwarden (pasta "StillOn") e para o Coolify.
- DNS se mexe em Hostinger → **Registros DNS**, nunca em Nameservers.
- Merge é deploy: conferir a run `Coolify release`.

**Painel**
- Direção C ("Editorial claro", Fraunces + Inter, índigo) e WCAG 2.2 AA.
- A tarefa de design para e espera a decisão do dono.

## O que já existe (não refazer)

| Peça | Onde |
|---|---|
| Ordem senha → 2FA → CAS do hash → revogação | `account.ChangePassword` / `gravarTroca` (`internal/account/account.go`) |
| Contagem de prova errada por família | `provasDaFamilia.reservar/errou`, `res.Succeed()` (`internal/httpapi/provas.go`) |
| Cadeia autenticada com limite | `autenticadaComLimite` (`internal/httpapi/routes.go`) |
| Travas | `LockUserSessions`, `LockTitularProjects`, `LockProjectMembers`, `travarFamiliaViva` |
| Contagem de membros além do titular | `CountMembersBesidesTitular` (`queries/invitations.sql`, 4.2) |
| Desligar canais de e-mail de quem sai | `DisableEmailChannelsCreatedBy` (usado em `pgstore/members.go`) |
| Apagar cookie de refresh | `apagarCookieDeRefresh` (`accounthandlers.go`) |
| Retenção | `internal/storage/maintenance.go`: checks 30 dias, rollups 13 meses, auditoria 12 meses, notificações e eventos 30 dias |
| Backups | Artifact diário de 30 dias + backup semanal da Hostinger (`docs/deploy/vps.md`) |
| Cadastro | `Cadastro.Cadastrar`; handler `Signup` (`cadastrohandlers.go`) com a ordem: decode → campos → Turnstile → serviço |
| Transferência de titular | Sub-PR 4.3: `projects.pending_titular_id ... ON DELETE SET NULL` |

## Foco da revisão

1. **Exclusão da conta de um membro apaga recursos no projeto de outra pessoa.** Hoje `targets/heartbeats/notif_channels/status_pages.user_id` são `ON DELETE CASCADE`.
   - Esperado: os recursos ficam, com `user_id` NULL.
   - Teste: `TestExcluirContaDeMembroPreservaRecursosNoProjetoAlheio` (T6).
2. **Corrida entre exclusão de conta e aceite de convite no projeto que era só dela.**
   - Esperado: ou a exclusão vê o membro novo e recusa, ou o aceite falha.
   - Nunca: o projeto apagado com o membro novo dentro.
   - Teste: `TestExcluirContaCorreComAceiteDeConvite` (T6).
3. **Sessão roubada exclui a conta sem o segundo fator, ou adivinha a senha sem custo.**
   - Esperado: com 2FA ligado, exige o código (ou um código de recuperação). Cada prova errada conta na família; a 5ª derruba a família.
   - Teste: `TestExcluirContaProvaErradaContaNaFamilia` (T7).
4. **Exportação vaza segredo ou dado de outro inquilino.** Segredos: hash, segredo TOTP, URL do webhook, valor de header, token de ping, ip_hash.
   - Esperado: nenhum segredo no arquivo. Projetos onde ela não é owner aparecem só como vínculo.
   - Teste: `TestExportacaoNaoVazaSegredoNemProjetoAlheio` (T8).
5. **Cadastro sem aceite, ou com versão velha (aba aberta antes da troca dos termos).**
   - Esperado: 400 no campo, antes do Turnstile, sem gravar nada.
   - Teste: `TestCadastroSemAceiteRecusaAntesDoTurnstile` (T1).

---

## Decisões do dono

**Decidido pelo dono em 2026-11-28:**
- **D2:** o builder escreve os textos inteiros (não só esqueleto), revisa e passa um resumo ao dono. O operador continua como "[operador]" e "[contato do operador]": nada de nome nem CNPJ.
- **D3 = A:** auditoria anônima (`user_id` NULL) até a retenção de 12 meses; `ip_hash` fica; a política informa os backups.
- **D4 = B:** apaga os canais criados por ela cujo endereço é o e-mail da conta; desliga os outros.
- **D5 = A, D7 = A, D8 = A:** recomendação seguida (checks fora da exportação; sem novo aceite por ora; aviso de exclusão por e-mail).
- **D6 (novo valor):** remetente `StillOn <avisos@stillon.tech>`, no domínio raiz. A caixa `avisos@stillon.tech` é criada no Zoho Mail (MX do domínio raiz) pelo builder, no 6.6. O envio continua pelo Resend, que precisa do domínio `stillon.tech` verificado (DKIM `resend._domainkey` e SPF/MX do subdomínio de retorno `send`), sem conflitar com o MX do Zoho. O passo 3 do runbook do T12 segue esta opção, não a A nem a B abaixo.
- **D1:** continua pendente; o T10 para com as maquetes.

Não decido estas. Cada uma traz opções, prós e contras, e uma recomendação.

**D1 — Telas de exportar, excluir conta, excluir projeto, aceite e páginas legais**

| Opção | O que é | Prós | Contras |
|---|---|---|---|
| A (recomendada) | Em Configurações, seção "Seus dados" (botão Exportar) e, embaixo, "Excluir conta" numa zona de perigo. Um diálogo mostra a prévia (projetos bloqueantes, com link para transferir a titularidade; projetos que somem junto) e pede senha + código. "Excluir projeto" fica nas configurações do projeto e exige digitar o nome. | Uma página só; padrão conhecido; a prévia evita surpresa | O diálogo fica denso quando há muitos projetos |
| B | Página própria `/settings/excluir-conta` em etapas: prévia → confirmação → feito | Respira melhor; dá para explicar cada consequência | Mais rota, mais estado, mais teste E2E |

A tarefa T10 traz as maquetes e para para o dono escolher.

**D2 — Conteúdo dos termos e da política de privacidade**

| Opção | Prós | Contras |
|---|---|---|
| A (recomendada) | O builder escreve um esqueleto com "[operador]" e "[contato do operador]", seções padrão e os prazos reais de retenção. O dono (ou um advogado) revisa antes do 6.6. | Destrava o 6.1 já; os prazos saem do código | Não é texto jurídico final |
| B | O dono entrega o texto antes do 6.1 | Texto final desde o início | Trava o 6.1 |

Nas duas opções, nada sai com nome nem CNPJ. O 6.6 só começa com o texto aprovado pelo dono.

**D3 — Retenção depois da exclusão**

| Opção | Prós | Contras |
|---|---|---|
| A (recomendada) | Mantém as linhas de `audit_log` dela com `user_id` NULL, como a spec diz ("já é"), até a retenção de 12 meses. Grava `user.deleted` com `entity_id` = id antigo, sem e-mail nem IP. A política informa que os backups (30 dias + semanal da Hostinger) guardam o dado até a rotação. | Mantém um rastro para investigar abuso sem dado pessoal direto | O `ip_hash` das linhas antigas continua lá (pseudônimo) por até 12 meses |
| B | A + zerar o `ip_hash` das linhas dela | Menos dado pseudônimo | Perde o sinal de abuso por IP |
| C | Apagar as linhas de auditoria dela | O mínimo de dados | Perde a trilha da conta e diverge da spec |

**D4 — Canais de e-mail que ela criou em projetos de outras pessoas**

Os outros recursos ficam com `user_id` NULL; esta decisão é só sobre os canais.

| Opção | Prós | Contras |
|---|---|---|
| A | Desliga, como o `RemoveMember` já faz | Consistente; reversível pelo owner | O endereço dela fica guardado num projeto do qual saiu |
| B (recomendada) | Apaga os canais criados por ela cujo endereço é o e-mail da conta; desliga os outros | Tira o dado pessoal; preserva endereços de equipe | Duas regras em vez de uma |
| C | Apaga todos os canais criados por ela | Simples | Pode apagar o canal de alerta da equipe |

**D5 — Checks brutos na exportação**

| Opção | Prós | Contras |
|---|---|---|
| A (recomendada) | Fora: entram incidentes e rollups | Arquivo pequeno, sem risco de timeout | Não traz cada medição |
| B | Últimos 30 dias de checks | Completo | Plano grátis: até ~170 mil linhas por projeto; operador: muito mais |

**D6 — Remetente dos e-mails de conta (`ACCOUNT_EMAIL_FROM`)**

| Opção | Prós | Contras |
|---|---|---|
| A (recomendada) | `StillOn <conta@avisos.stillon.tech>` | Domínio já verificado; nenhum DNS novo; a chave atual cobre | Reputação compartilhada com os alertas |
| B | Subdomínio novo (ex.: `conta.stillon.tech`) | Reputação separada | DNS novo; o plano grátis do Resend pode limitar a um domínio (conferir no painel); a chave pode precisar ser trocada |

**D7 — Novo aceite quando os termos mudarem**

| Opção | Prós | Contras |
|---|---|---|
| A (recomendada) | Agora não: grava só a versão aceita no cadastro; aviso/novo aceite num PR futuro | Escopo menor | Contas antigas ficam sem registro de aceite |
| B | Bloqueia o login até aceitar a versão nova | Registro completo | Fluxo novo no login; risco de travar o operador |

**D8 — E-mail avisando que a conta foi excluída**

| Opção | Prós | Contras |
|---|---|---|
| A (recomendada) | Envia, depois do commit, ao endereço antigo; texto curto definido no D1 | A pessoa descobre se foi uma sessão roubada | Gasta uma unidade da cota de e-mail |
| B | Não envia | Simples | Exclusão silenciosa |

## Decisões que eu tomei

- **B1. Exclusão numa transação só**, no escopo `InAccountDeletionTx`. Assim não sobra estado parcial: ou tudo, ou nada.
- **B2. Reautenticação igual à troca de senha:** senha + 2FA (TOTP ou código de recuperação); provas contadas pela família; cadeia `autenticadaComLimite`. Reaproveita a defesa que já existe.
- **B3. Quando a exclusão é recusada.** É recusada quando ela é **titular** de um projeto com ao menos um outro membro, mesmo que haja outro owner. O titular é sempre owner e a FK do titular apagaria o projeto, então o caminho é transferir a titularidade (4.3). Owner que não é titular pode sair: o titular continua owner.
- **B4. Projetos só dela somem junto,** com convites, membros e recursos (cascade por `project_id`).
- **B5. `projects.titular_id` passa a `ON DELETE RESTRICT`.** É uma segunda barreira: nenhum caminho apaga um projeto compartilhado sem passar pela regra B3.
- **B6. `user_id` dos recursos passa a `ON DELETE SET NULL`,** e a trigger da 00031 aceita NULL só quando o usuário já não existe.
- **B7. A conta do operador não se exclui pela API** (409 `operator_account`). É a conta que mantém a instância.
- **B8. Excluir projeto exige owner (`PermManageProject`) e o nome digitado no corpo** (`{"name": "..."}`). O projeto padrão é recusado (409 `default_project_protected`) enquanto existirem as rotas legadas (o PR 7 decide).
- **B9. A exportação não pede senha.** É um GET de dados que a mesma sessão já vê no painel. Tem limite por usuário e registro na auditoria (`account.exported`).
- **B10. Uma versão só para termos e privacidade:** `legal.VersaoAtual = "2026-11-28"`. Contas criadas pela CLI ficam com as duas colunas NULL.
- **B11. O 409 da exclusão bloqueada não carrega a lista de projetos.** O envelope só tem `fields`; o painel relê `GET /me/deletion-preview`.
- **B12. A checagem de bloqueio roda também antes das provas.** É só leitura e evita gastar um código de recuperação no caso comum. A checagem que vale é a de dentro da transação.

## Sub-PRs

| Sub-PR | Tarefas | Migration | Depende de | Esforço |
|---|---|---|---|---|
| 6.1 Textos legais e aceite | T1, T2 | M | PR 3 na main | M |
| 6.2 Expand + excluir projeto | T3, T4 | M+1 | 4.2 na main | L |
| 6.3 Excluir conta | T5, T6, T7 | — | 6.2 **no ar** e 4.3 na main | L |
| 6.4 Exportar | T8, T9 | — | 4.2 na main (paralelo ao 6.3) | M |
| 6.5 Painel | T10 (para), T11 | — | 6.3, 6.4 e D1 | M |
| 6.6a / 6.6b Ligar o cadastro em produção | T12 | — | 6.1–6.5 no ar, D2 aprovado, D6 decidido; passos do dono | S de código + runbook |

**Por que o 6.2 sobe antes do 6.3:** a M+1 só torna `user_id` anulável. NULL só aparece quando alguém exclui a conta (6.3). Então o binário anterior nunca encontra um NULL que não consegue ler.

**Por que o `RESTRICT` não quebra o binário anterior:** nenhum caminho de produção apaga `users` antes do 6.3.

## Estrutura de arquivos

**Criar**
- `internal/legal/versao.go`, `internal/legal/versao_test.go`
- `internal/storage/migrations/<M>_aceite_dos_termos.sql`
- `internal/storage/migrations/<M+1>_exclusao_de_conta_expand.sql`
- `internal/storage/queries/exclusao.sql`
- `internal/storage/testdb/usuario.go` (`ApagarUsuario`)
- `internal/account/exclusao.go`, `internal/account/exclusao_test.go`
- `internal/exportacao/exportacao.go`, `internal/exportacao/exportacao_test.go`
- `internal/httpapi/exclusaohandlers.go`, `internal/httpapi/exportacaohandlers.go`, mais os testes de integração
- `web/src/features/legal/` (`versao.json`, `PaginaTermos.tsx`, `PaginaPrivacidade.tsx`, `legal.test.tsx`)
- `web/src/features/configuracoes/` (`SeusDados.tsx`, `ExclusaoDeConta.tsx`)

**Alterar**
- `internal/storage/queries/users.sql` e `projects.sql`
- `internal/account/cadastro.go`, `internal/httpapi/cadastrohandlers.go`
- `internal/tenancy/{role,ports,service}.go`, `internal/tenancy/pgstore/`
- `internal/httpapi/routes.go`, `internal/apierr/errors.go`
- Leitores de `user_id`:
  - `internal/heartbeat/service.go:219`;
  - `internal/notify/dispatcher.go:575`;
  - `internal/statuspage/service.go:83`;
  - `queries/incidents.sql:54`.
- `web/src/app/rotas.tsx`, `web/src/features/conta/PaginaCadastro.tsx`, `web/src/test/api-falsa.ts`
- `deploy/coolify/app.json`, `deploy/coolify/infra-fixada`
- `internal/config/{manifestos_coolify_test,infra_fixada_test}.go`
- `docs/deploy/vps.md`, `CLAUDE.md`

## Contrato HTTP novo

| Método e rota | Cadeia | Corpo | Respostas |
|---|---|---|---|
| `GET /api/v1/auth/signup-config` | pública | — | ganha `terms_version` |
| `POST /api/v1/auth/signup` | pública | ganha `accepted_terms_version` | 400 `fields[accepted_terms_version]` com motivo `required` ou `outdated` |
| `DELETE /api/v1/projects/{projectID}` | `noProjeto` | `{name}` | 204; 403 `forbidden`; 404; 409 `default_project_protected`; 400 `fields[name]` = `mismatch` |
| `GET /api/v1/me/deletion-preview` | `protegida` | — | 200 `{blocking_projects:[{id,name}], projects_to_delete:[{id,name}]}` |
| `DELETE /api/v1/me` | `autenticadaComLimite` | `{password, totp_code}` | 204 + cookie apagado; 401 com os mesmos códigos do `ChangePassword`; 409 `account_deletion_blocked`; 409 `operator_account` |
| `GET /api/v1/me/export` | `protegida` + limite de exportação | — | 200 anexo JSON; 429 |

---

## Tarefas

### Sub-PR 6.1 — Textos legais e aceite

#### T1: Versão dos termos, migration M e gravação no cadastro (esforço M)

**Arquivos**
- Criar: `internal/legal/versao.go`, `internal/legal/versao_test.go`, `internal/storage/migrations/<M>_aceite_dos_termos.sql`.
- Alterar: `queries/users.sql` (`InsertSignupUser` grava as duas colunas), `internal/account/cadastro.go`, `internal/httpapi/cadastrohandlers.go`.
- Testes: `internal/account/cadastro_test.go`, `internal/httpapi/cadastro_integration_test.go` (os arquivos atuais do cadastro).

**Interfaces**
- Produz:
  - `legal.VersaoAtual string = "2026-11-28"`.
  - `Cadastrar` ganha o parâmetro `versaoDosTermos string`. O ramo de e-mail já cadastrado não grava nada.
  - `signupRequest.AcceptedTermsVersion *string` com a tag `json:"accepted_terms_version"`.
  - `signup-config` devolve `terms_version`.
- Consome: o handler `Signup` existente e o `VerificadorTurnstile`.

**Migration M:**
```sql
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE users ADD COLUMN terms_version text, ADD COLUMN terms_accepted_at timestamptz;
ALTER TABLE users ADD CONSTRAINT users_aceite_em_par
  CHECK ((terms_version IS NULL) = (terms_accepted_at IS NULL));
-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE users DROP CONSTRAINT users_aceite_em_par, DROP COLUMN terms_accepted_at, DROP COLUMN terms_version;
```
O binário anterior insere sem as colunas: as duas ficam NULL e o CHECK aceita. Por isso é expand seguro.

- [ ] **Passo 1 — testes que falham:**

| Teste | Afirma | Sabotagem |
|---|---|---|
| `TestAceiteDosTermosExigeParDeColunas` | INSERT com só uma das colunas dá 23514 | remover o CHECK |
| `TestCadastroGravaVersaoAceita` | depois do `Cadastrar`, `terms_version = legal.VersaoAtual` e `terms_accepted_at = clk.Now()` | não passar a versão ao INSERT |
| `TestCadastroDeEmailExistenteNaoTrocaAceite` | as colunas da conta antiga não mudam | gravar também no ramo de conflito |
| `TestCadastroSemAceiteRecusaAntesDoTurnstile` | corpo sem o campo dá 400 com `fields[0] = {accepted_terms_version, required}`; o Turnstile falso registra **0** chamadas; nenhuma linha em `users` | validar depois do Turnstile |
| `TestCadastroComVersaoVelhaRecusa` | `"2020-01-01"` dá 400 com o motivo `outdated` | aceitar qualquer valor não vazio |
| `TestSignupConfigInformaVersaoDosTermos` | `terms_version == legal.VersaoAtual` | omitir o campo |
| `TestVersaoDosTermosIgualAoPainel` | lê `web/src/features/legal/versao.json` e compara com `legal.VersaoAtual` | mudar o JSON |

- [ ] **Passo 2:** rodar `go test ./internal/legal/ ./internal/account/ ./internal/httpapi/ -run 'Aceite|Cadastro|SignupConfig|VersaoDosTermos'`. Esperado: FAIL.
- [ ] **Passo 3:** implementar. A validação do campo fica junto da de `email` e `password`, antes do Turnstile.
- [ ] **Passo 4:** os mesmos testes passam. Rodar `go build ./... && go vet ./... && make test`.
- [ ] **Passo 5:** commit `feat(cadastro): grava a versão dos termos aceita`.

#### T2: Páginas `/termos` e `/privacidade` e caixa de aceite (esforço M)

O visual é funcional, no padrão das telas públicas atuais. O acabamento fica para o T11.

**Arquivos**
- Criar: `web/src/features/legal/{versao.json,PaginaTermos.tsx,PaginaPrivacidade.tsx,legal.test.tsx}`.
- Alterar: `web/src/app/rotas.tsx` (rotas públicas fora de `ExigirSessao`), `web/src/features/conta/PaginaCadastro.tsx`, `web/src/test/api-falsa.ts`, `web/e2e/cadastro.spec.ts`, `web/e2e/a11y.spec.ts`.

**Interfaces**
- Produz: `versao.json` = `{"versao":"2026-11-28"}`. O cadastro envia `accepted_terms_version` com esse valor.
- Conteúdo do esqueleto (D2-A):
  - **Quem opera:** "[operador]".
  - **Contato:** "[contato do operador]".
  - **Dados tratados:** e-mail, hash da senha, IP só como hash, dados de monitoramento.
  - **Finalidade.**
  - **Prazos**, saídos do `maintenance.go`: checks 30 dias, rollups 13 meses, auditoria 12 meses, notificações 30 dias, backups 30 dias + semanal da Hostinger. O texto sobre o pós-exclusão vem do D3.
  - **Direitos:** exportar e excluir pelo painel.
- Os links da caixa abrem em aba nova (`target="_blank" rel="noopener"`) para não perder o formulário.

- [ ] **Passo 1 — testes que falham (Vitest + MSW):**

| Teste | Afirma | Sabotagem |
|---|---|---|
| `cadastro exige marcar o aceite` | enviar sem marcar mostra o erro no campo e nenhuma requisição sai | remover o `required` |
| `cadastro envia a versão aceita` | o corpo capturado pelo MSW tem `accepted_terms_version: "2026-11-28"` | enviar a versão fixa errada |
| `cadastro mostra o erro de versão desatualizada` | a resposta `outdated` vira um aviso pedindo para recarregar | ignorar o motivo |
| `páginas legais abrem sem sessão` | `/termos` e `/privacidade` renderizam sem `/api/v1/me` | pôr as rotas dentro de `ExigirSessao` |
| `textos legais não citam empresa` | o texto renderizado contém "[operador]" e não casa `/\d{2}\.\d{3}\.\d{3}\/\d{4}-\d{2}/` | colar um CNPJ de exemplo |

- O E2E do cadastro marca a caixa.
- O axe cobre as duas páginas.
- [ ] **Passo 2:** rodar os testes do `web/`. Esperado: FAIL.
- [ ] **Passo 3:** implementar.
- [ ] **Passo 4:** os testes passam, e também o E2E e o axe.
- [ ] **Passo 5:** commit `feat(painel): termos, privacidade e aceite no cadastro`.

### Sub-PR 6.2 — Expand e excluir projeto

#### T3: Migration M+1 e leitores de `user_id` anulável (esforço L)

**Arquivos**
- Criar:
  - `internal/storage/migrations/<M+1>_exclusao_de_conta_expand.sql`;
  - `internal/storage/testdb/usuario.go`;
  - `internal/storage/exclusao_expand_test.go`.
- Alterar:
  - os tipos gerados pelo sqlc (`user_id` vira `*uuid.UUID`);
  - os leitores `internal/heartbeat/service.go:219`, `internal/notify/dispatcher.go:575` e `internal/statuspage/service.go:83`;
  - `queries/incidents.sql:54`. O casamento legado `c.user_id = COALESCE(t.user_id, h.user_id)` passa a falhar com NULL, que é o esperado: recurso sem criador não casa canal legado de usuário. Documentar no comentário.
- Trocar o `DELETE FROM users` direto por `testdb.ApagarUsuario` nestes testes:
  - `internal/auth/provas_test.go:431`;
  - `internal/httpapi/auth_lacunas_integration_test.go:230`;
  - `risco6a10_internal_test.go:42`;
  - `totp_prova_integration_test.go:290`;
  - `internal/storage/heartbeat_rls_test.go:582`;
  - `project_id_recursos_test.go:228`;
  - `target_tls_test.go:245`.

**Interfaces**
- Produz: `testdb.ApagarUsuario(t *testing.T, id uuid.UUID)`. Pela conexão de admin, apaga os projetos dos quais o usuário é titular e depois o usuário.
- Produz: a política `projects_do_projeto_delete` (`FOR DELETE USING (id = current_setting('app.project_id', true)::uuid)`), que T4 e T6 usam.

**Esboço da migration M+1.** Confirme os nomes reais das constraints com `SELECT conname FROM pg_constraint WHERE conrelid = 'targets'::regclass`.
```sql
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- para cada uma de targets, heartbeats, notif_channels, status_pages:
ALTER TABLE targets ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE targets DROP CONSTRAINT targets_user_id_fkey,
  ADD CONSTRAINT targets_user_id_fkey FOREIGN KEY (user_id) REFERENCES users ON DELETE SET NULL NOT VALID;
ALTER TABLE targets VALIDATE CONSTRAINT targets_user_id_fkey;
-- titular: segunda barreira
ALTER TABLE projects DROP CONSTRAINT projects_titular_id_fkey,
  ADD CONSTRAINT projects_titular_id_fkey FOREIGN KEY (titular_id) REFERENCES users ON DELETE RESTRICT NOT VALID;
ALTER TABLE projects VALIDATE CONSTRAINT projects_titular_id_fkey;
-- a função da 00031 (guard_user_id_imutavel) recusa a troca, salvo:
--   NEW.user_id IS NULL AND NOT EXISTS (SELECT 1 FROM users WHERE id = OLD.user_id)
CREATE OR REPLACE FUNCTION guard_user_id_imutavel() ...;
CREATE POLICY projects_do_projeto_delete ON projects FOR DELETE
  USING (id = current_setting('app.project_id', true)::uuid);
-- +goose Down
-- um bloco DO dá RAISE EXCEPTION se houver user_id NULL em qualquer das quatro tabelas;
-- depois restaura NOT NULL + CASCADE, a função original, o CASCADE do titular e remove a política.
```

- [ ] **Passo 1 — testes que falham:**

| Teste | Afirma | Sabotagem |
|---|---|---|
| `TestExcluirUsuarioAnulaUserIDDosRecursos` | B (membro) criou alvo, heartbeat, canal e página no projeto de A. `ApagarUsuario(B)` roda **com o papel da aplicação, no escopo de B**. As quatro linhas continuam, com `user_id` NULL. Isso também prova que a ação da FK ignora a RLS com FORCE. | manter CASCADE (as linhas somem) |
| `TestGuardaUserIDAindaRecusaTroca` | UPDATE para outro usuário existente dá 42501. UPDATE para NULL com o usuário ainda existente dá 42501. | aceitar qualquer NULL na função |
| `TestTitularNaoSaiComProjetoCompartilhado` | `DELETE FROM users` do titular dá 23503 | manter CASCADE |
| `TestDownDaExclusaoRecusaComNulos` | com uma linha NULL, o Down falha com a mensagem do RAISE. Siga o padrão de teste de Down que já existir em `internal/storage` (`git grep -n "goose.Down"`); se não houver, rode o SQL do Down via `testdb.Psql`. | remover o bloco DO |
| `TestDispatcherComRecursoSemCriador` (`internal/notify`) | uma notificação de alvo com `user_id` NULL é entregue pelo canal do projeto, sem pânico | desreferenciar `*n.UserID` |

- [ ] **Passo 2:** rodar `go test ./internal/storage/ ./internal/notify/ -run 'ExcluirUsuario|GuardaUserID|Titular|DownDaExclusao|SemCriador'`. Esperado: FAIL.
- [ ] **Passo 3:** implementar a migration, rodar `make sqlc` (ou o alvo de geração que já existir), corrigir os leitores e criar o helper.
- [ ] **Passo 4:** `go build ./... && go vet ./... && make test`.
- [ ] **Passo 5:** commit `feat(storage): exclusão de conta preserva recursos em projetos alheios`.

**Correção levada do plano do PR 7 (risco R1):** a guarda `guardar_notificacao_no_projeto` (00043) trata `recurso_dono IS NULL` como recurso escondido e descarta o aviso. Com `user_id` anulável, o aviso de um recurso cujo criador excluiu a conta sumiria. A migration M+1 do T3 troca a função para resolver o projeto pelo `project_id` do recurso, e não pelo dono. Teste a mais no T3: `TestNotificacaoComCriadorApagadoPassa` (alvo e canal no projeto de A, criado por B, que excluiu a conta: o INSERT em `notifications` sob `InProberTx` entra). Sabotagem: manter o `IF recurso_dono IS NULL`.

#### T4: `DELETE /api/v1/projects/{projectID}` (esforço M)

**Arquivos**
- Alterar:
  - `queries/projects.sql` (`DeleteProject :execrows`: `DELETE FROM projects WHERE id = sqlc.arg(id)`);
  - `internal/tenancy/role.go`, `internal/tenancy/ports.go`, `internal/tenancy/service.go`, `internal/tenancy/pgstore/` (onde está o `Rename`);
  - o handler de projetos (o arquivo do `PATCH /projects/{projectID}`);
  - `routes.go`, `apierr/errors.go`, `api-falsa.ts`;
  - `corposEstritos()` e `corpoMinimo`.

**Interfaces**
- Produz:
  - `tenancy.ErrDefaultProjectProtected`;
  - `tenancy.ErrConfirmationMismatch`;
  - `ProjectRepository.Delete(ctx context.Context, userID, projectID uuid.UUID, nomeConfirmado string) error`;
  - `Service.Delete(...)` com a mesma assinatura;
  - o código `default_project_protected`.
- Ordem dentro da transação (`InProjectTx`):
  1. `ActiveMembershipRole`;
  2. `PermManageProject`;
  3. `LockTitularProjects(titular)`;
  4. `LockProjectMembers`;
  5. relê `name` e `is_default`; padrão dá `ErrDefaultProjectProtected`;
  6. nome diferente dá `ErrConfirmationMismatch`;
  7. `DeleteProject`; 0 linhas dá `ErrProjectNotFound`;
  8. auditoria `project.deleted`.
- A ordem titular → membros é a mesma da exclusão de conta (T6) e da criação, para evitar deadlock.
- Alvos apagados enquanto há um check em andamento: confira como o `DELETE` de alvo trata isso hoje no agendador e no prober, e reaproveite. O teste abaixo trava esse caso.

- [ ] **Passo 1 — testes que falham:**

| Teste | Afirma | Sabotagem |
|---|---|---|
| `TestExcluirProjetoSoOwner` | editor dá 403 `forbidden`; o viewer fica coberto pelo `corpoMinimo` | exigir `PermEditResources` |
| `TestExcluirProjetoPadraoRecusa409` | 409 `default_project_protected`, e nada muda | tirar a checagem |
| `TestExcluirProjetoExigeNomeConfirmado` | nome errado dá 400 `fields[name]=mismatch` | não comparar |
| `TestExcluirProjetoLevaRecursosEConvites` | alvos, checks, canais, convites e membros somem; o outro projeto do mesmo titular fica intacto | remover a política de DELETE (dá 0 linhas e o teste vê 404) |
| `TestExcluirProjetoDeOutroInquilino404` | quem não é membro recebe 404 | checar o papel depois do DELETE |
| `TestExcluirProjetoLiberaCota` | com a cota cheia, excluir um projeto permite criar outro | (prova de comportamento) |
| `TestExcluirProjetoComCheckEmVoo` | um resultado de check do alvo apagado é descartado sem derrubar o worker | tratar o erro de FK como fatal |

- [ ] **Passo 2:** rodar `go test ./internal/tenancy/... ./internal/httpapi/ -run 'ExcluirProjeto|EveryMutatingProjectRoute'`. Esperado: FAIL.
- [ ] **Passo 3:** implementar.
- [ ] **Passo 4:** `go build ./... && go vet ./... && make test`.
- [ ] **Passo 5:** commit `feat(projetos): owner exclui projeto confirmando o nome`.

### Sub-PR 6.3 — Excluir conta

#### T5: Escopo `InAccountDeletionTx` (esforço S)

**Arquivos**
- Alterar: `internal/storage/scope.go`.
- Teste: `internal/storage/scope_exclusao_test.go`.

**Interfaces**
- Produz:
```go
type EntrarNoProjeto func(ctx context.Context, projectID uuid.UUID) (role string, err error)
func InAccountDeletionTx(ctx context.Context, b Beginner, userID uuid.UUID,
    fn func(q *db.Queries, entrar EntrarNoProjeto) error) error
```
- Abre no escopo do usuário (`ScopeToUser`).
- `entrar` roda `ActiveMembershipRole` e só depois `ScopeToProject`. Se ela não for membro, devolve `tenancy.ErrNotMember` sem tocar no GUC.

- [ ] **Passo 1 — teste:** `TestEscopoDeExclusaoSoEntraEmProjetoDoUsuario`.
  - `entrar` num projeto alheio devolve `ErrNotMember`.
  - Depois disso, um `SELECT` em `targets` ainda não enxerga nada daquele projeto.
  - Sabotagem: chamar `ScopeToProject` antes da checagem.
- [ ] **Passo 2:** o teste falha.
- [ ] **Passo 3:** implementar.
- [ ] **Passo 4:** o teste passa, e o `guarda_escopo_test` continua verde.
- [ ] **Passo 5:** commit `feat(storage): escopo da exclusão de conta`.

#### T6: `account.ExcluirConta` e `PreviaDaExclusao` (esforço L)

**Arquivos**
- Criar: `internal/account/exclusao.go`, `internal/account/exclusao_test.go`, `internal/storage/queries/exclusao.sql`.
  - `ListTitularProjectsForDeletion :many`: `id, name` com `titular_id = $1`, `ORDER BY id`.
  - `ListNonTitularMemberships :many`.
  - `DeleteUserAccount :execrows`.
  - A query de canais de e-mail conforme o D4.

**Interfaces**
- Consome: `InAccountDeletionTx` (T5), `DeleteProject` (T4), `CountMembersBesidesTitular`, `LockUserSessions`, `LockTitularProjects`, `LockProjectMembers`, `travarFamiliaViva`, `provarSegundoFator`.
- Produz:
```go
type ProjetoResumo struct { ID uuid.UUID; Nome string }
type PreviaDaExclusao struct { Bloqueantes, Apagados []ProjetoResumo }
type ErroDeExclusaoBloqueada struct { Projetos []ProjetoResumo } // implementa error
var ErrContaDoOperador = errors.New("account: a conta do operador não se exclui pela API")
func (s *Service) PreviaDaExclusao(ctx context.Context, userID uuid.UUID) (PreviaDaExclusao, error)
func (s *Service) ExcluirConta(ctx context.Context, userID, familyID uuid.UUID,
    senha, codigo string, ipHash []byte) (emailAntigo string, err error)
```

**Ordem do `ExcluirConta`** (espelha o `ChangePassword`):

*Antes da transação*
1. `FamilyIsLive`.
2. `PreviaDaExclusao`: se houver bloqueantes, `ErroDeExclusaoBloqueada` (B12).
3. Lê o hash.
4. `verifyPassword`.
5. `provarSegundoFator`.

*Dentro da `InAccountDeletionTx`*
1. `LockUserSessions`, `travarFamiliaViva`.
2. Relê o hash e compara com o verificado; se mudou, `ErrInvalidCredentials`.
3. `UserIsOperator` dá `ErrContaDoOperador`.
4. `LockTitularProjects(user)`.
5. Para cada projeto de que é titular: `entrar` + `LockProjectMembers` + contagem. Se houver bloqueantes, `ErroDeExclusaoBloqueada` (rollback).
6. Para cada um: `entrar` + `DeleteProject`.
7. Para cada vínculo em que não é titular: `entrar` + `LockProjectMembers` + canais (D4).
8. `DeleteUserAccount`, que precisa afetar exatamente 1 linha.
9. Auditoria `user.deleted` com `user_id` NULL e `entity_id` = id antigo (D3).

O e-mail de aviso (D8) sai no handler, depois do commit.

- [ ] **Passo 1 — testes que falham (integração):**

| Teste | Afirma | Sabotagem |
|---|---|---|
| `TestExcluirContaApagaProjetosSoDela` | projetos, alvos e checks dela somem; a linha em `users` some; `InsertSignupUser` com o mesmo e-mail funciona (e-mail liberado) | pular o `DeleteProject` (o RESTRICT dá 23503) |
| `TestExcluirContaBloqueiaTitularDeProjetoCompartilhado` | `errors.As(err, &ErroDeExclusaoBloqueada{})` com o projeto listado, mesmo havendo outro owner; nada é apagado | pular a contagem (o erro vira 23503, não o tipo) |
| `TestExcluirContaDeMembroPreservaRecursosNoProjetoAlheio` | os recursos dela no projeto de A ficam com `user_id` NULL e o projeto de A continua | apagar os recursos por `user_id` |
| `TestExcluirContaCorreComAceiteDeConvite` | o admin segura `stillon.members:<p>`; a exclusão bloqueia (`testdb.EsperarBloqueados`); o aceite de convite grava um membro; ao soltar, a exclusão devolve `ErroDeExclusaoBloqueada` e o projeto continua. Antes, confira que o aceite do 4.2 pega `LockProjectMembers`. | contar antes de travar |
| `TestExcluirContaExigeSenhaE2FA` | senha errada dá `ErrInvalidCredentials`; com 2FA ligado e sem código, o erro do `ChangePassword`; um código de recuperação é aceito e fica consumido | não chamar `provarSegundoFator` |
| `TestExcluirContaCASDaSenha` | o hash trocado entre a verificação e a transação dá `ErrInvalidCredentials` | não reler o hash |
| `TestExcluirContaRecusaOperador` | `ErrContaDoOperador` | tirar a checagem |
| `TestExcluirContaAnulaTransferenciaPendente` | `pending_titular_id` (4.3) que apontava para ela vira NULL | (prova de FK) |
| `TestExcluirContaTrataCanaisDeEmailConformeD4` | segue a opção escolhida no D4 | inverter a regra |
| `TestExcluirContaGravaAuditoriaSemDadoPessoal` | `user.deleted` com `entity_id` certo, sem e-mail e sem `ip_hash` (D3) | gravar o `ipHash` |

- [ ] **Passo 2:** rodar `go test ./internal/account/ -run 'ExcluirConta|Previa'`. Esperado: FAIL.
- [ ] **Passo 3:** implementar.
- [ ] **Passo 4:** `go build ./... && go vet ./... && make test`.
- [ ] **Passo 5:** commit `feat(conta): exclusão de conta com prévia e bloqueio do titular`.

#### T7: Rotas `GET /me/deletion-preview` e `DELETE /me` (esforço M)

**Arquivos**
- Criar: `internal/httpapi/exclusaohandlers.go`, `internal/httpapi/exclusao_integration_test.go`.
- Alterar: `routes.go`, `apierr/errors.go` (`account_deletion_blocked`, `operator_account`), `api-falsa.ts`, `corposEstritos()`, `TestProtectedRoutesRequireAuthentication`.

**Interfaces**
- Consome: `ExcluirConta`, `PreviaDaExclusao` (T6); `provasDaFamilia` (`reservar`, `errou`, `Succeed`); `apagarCookieDeRefresh`; o `AccountMailer` para o aviso (D8, sem bloquear a resposta).
- Corpo estrito: `{password string, totp_code string}`.

- [ ] **Passo 1 — testes que falham:**

| Teste | Afirma | Sabotagem |
|---|---|---|
| `TestExcluirContaPelaAPI` | 204; `Set-Cookie` de refresh com `Max-Age=0`; o **mesmo access token** em `GET /api/v1/me` dá 401 (o `viva` não acha a sessão) | não apagar o cookie |
| `TestExcluirContaProvaErradaContaNaFamilia` | 4 senhas erradas dão 401 cada; a 5ª revoga a família e o refresh falha | não chamar `errou` |
| `TestExcluirContaBloqueada409` | 409 `account_deletion_blocked`; a prévia lista o projeto em `blocking_projects` | mapear para 500 |
| `TestPreviaDaExclusaoSeparaBloqueantesEApagados` | os dois arrays com `id` e `name` | inverter as listas |
| `TestExcluirContaEnviaAvisoDepoisDoCommit` | o mailer falso recebe 1 aviso; com a exclusão bloqueada, recebe 0 (D8) | enviar antes do commit |

- [ ] **Passo 2:** rodar `go test ./internal/httpapi/ -run 'ExcluirConta|PreviaDaExclusao|ProtectedRoutes|TodoHandlerComCorpo'`. Esperado: FAIL.
- [ ] **Passo 3:** implementar.
- [ ] **Passo 4:** `go build ./... && go vet ./... && make test`.
- [ ] **Passo 5:** commit `feat(api): rotas da exclusão de conta`.

### Sub-PR 6.4 — Exportar

#### T8: Pacote `internal/exportacao` (esforço M)

**Arquivos**
- Criar: `internal/exportacao/exportacao.go`, `internal/exportacao/exportacao_test.go`.

**Interfaces**
- Produz:
  - `exportacao.Novo(pool *pgxpool.Pool, clk clock.Clock) *Servico` (usar o tipo de relógio que o `account` já usa);
  - `func (s *Servico) Escrever(ctx context.Context, userID uuid.UUID, w io.Writer) error`.
- Reaproveita as queries sqlc de listagem. Os DTOs são próprios do pacote; não importa `httpapi`.
- Formato versão 1:
```json
{"format":"stillon-export","version":1,"generated_at":"…",
 "account":{"id","email","email_verified","totp_enabled","created_at","terms_version","terms_accepted_at"},
 "sessions":[{"id","created_at","last_used_at","expires_at"}],
 "audit":[{"action","entity","entity_id","at"}],
 "memberships":[{"project_id","project_name","role","is_titular"}],
 "projects":[{"id","name","is_default","created_at","members":[{"email","role"}],
   "invitations":[{"email","role","expires_at"}],"targets":[…],"heartbeats":[…],
   "channels":[{"id","kind","hint","email","enabled"}],"status_pages":[…],"incidents":[…],"rollups":[…]}]}
```
- `projects` traz só os projetos onde o papel dela é `owner`. Os outros aparecem só em `memberships`.
- Nunca entram:
  - hash de senha, segredo TOTP, códigos de recuperação;
  - hashes de token, `ip_hash`;
  - URL do webhook (vai só o `hint`);
  - valores de header (vão só os nomes, com o valor `"[omitido]"`);
  - token de ping;
  - checks brutos (D5-A).
- Cada projeto é lido no seu `InProjectTx`, sem snapshot único; o comentário documenta isso.
- Erro depois do primeiro byte: devolve o erro e o handler faz `panic(http.ErrAbortHandler)`, para o download falhar em vez de chegar um JSON truncado.

- [ ] **Passo 1 — testes que falham:**

| Teste | Afirma | Sabotagem |
|---|---|---|
| `TestExportacaoTemFormatoVersionado` | `format`, `version == 1`, `generated_at` igual ao relógio | omitir `version` |
| `TestExportacaoChavesDaVersao1` | o conjunto de chaves de topo e de `projects[0]` é exatamente o da lista acima | acrescentar uma chave |
| `TestExportacaoIncluiProjetosOndeEOwner` | owner não titular aparece em `projects`; projeto onde é editor só em `memberships` | filtrar por titular |
| `TestExportacaoNaoVazaSegredoNemProjetoAlheio` | semeia `https://discord.com/api/webhooks/123/segredo`, o header `Bearer segredo-xyz`, o token de ping, o segredo TOTP, um código de recuperação, o hash da senha, o `ip_hash` (hex e base64) e um projeto alheio "Projeto-Alheio"; **nenhuma** dessas strings aparece nos bytes | exportar a URL do webhook |

- [ ] **Passo 2:** rodar `go test ./internal/exportacao/`. Esperado: FAIL.
- [ ] **Passo 3:** implementar. Um `json.Encoder` por seção, com o objeto externo escrito à mão.
- [ ] **Passo 4:** `go build ./... && go vet ./... && make test`.
- [ ] **Passo 5:** commit `feat(conta): exportação dos dados da conta`.

#### T9: Rota `GET /api/v1/me/export` (esforço S)

**Arquivos**
- Criar: `internal/httpapi/exportacaohandlers.go`, mais o teste de integração.
- Alterar: `routes.go` (`Deps` ganha `Exportacao`; `nil` dá pânico como as outras dependências), `TestProtectedRoutesRequireAuthentication`.

**Interfaces**
- Limite: 1 por 10 minutos, rajada de 2, por usuário.
  - Com o PR 5 mesclado: uma chave nova em `middleware.Orcamentos`.
  - Sem ele: `RateLimitUser`.
- Headers:
  - `Content-Type: application/json`;
  - `Content-Disposition: attachment; filename="stillon-export-AAAA-MM-DD.json"`;
  - `Cache-Control: no-store`.
- Auditoria `account.exported`.

- [ ] **Passo 1 — testes:**
  - `TestExportarPelaAPIAnexoSemCache`: headers exatos, e o corpo passa no `json.Valid`. Sabotagem: tirar o `no-store`.
  - `TestExportarTemLimitePorUsuario`: a 3ª chamada na janela dá 429 e a de outro usuário dá 200. Sabotagem: tirar o limite.
- [ ] **Passo 2:** os testes falham.
- [ ] **Passo 3:** implementar.
- [ ] **Passo 4:** `go build ./... && go vet ./... && make test`.
- [ ] **Passo 5:** commit `feat(api): rota de exportação`.

### Sub-PR 6.5 — Painel

#### T10: Design das telas de conta (para e espera o dono) (esforço M)

- Skills: `frontend-design:frontend-design`, `design-skills:ux-writing`, `design-skills:interaction-design`, `design-skills:design-systems`, `design-skills:accessibility-audit` (WCAG 2.2 AA).
- Entrega:
  - as duas opções do D1 em maquete no painel local;
  - os textos de exportar, prévia, bloqueio (com link para a transferência do 4.3), confirmação e sucesso;
  - o texto do aviso por e-mail (D8);
  - a tipografia das páginas legais.
- **PARE** e apresente ao dono. Só siga para o T11 com o D1 decidido.

#### T11: Telas de exportar, excluir conta e excluir projeto (esforço M)

**Arquivos**
- Criar: `web/src/features/configuracoes/SeusDados.tsx`, `ExclusaoDeConta.tsx`.
- Alterar:
  - `PaginaConfiguracoes`;
  - a tela de configurações do projeto (onde está o renomear);
  - o cliente da API (schemas zod da prévia);
  - `api-falsa.ts`;
  - `web/e2e/jornadas.spec.ts`, `web/e2e/a11y.spec.ts`.

**Interfaces**
- Exportar: `fetch`, depois `Blob`, depois `<a download>`. O 429 vira uma mensagem com o prazo.
- Excluir conta:
  - `useQuery` da prévia;
  - mutação `DELETE /me`;
  - no 204: limpa o cache do TanStack Query e vai para `/login` com um aviso.
- Excluir projeto: o botão só aparece para owner e nunca no projeto padrão.

- [ ] **Passo 1 — testes (Vitest + MSW):**
  - `exportar baixa o arquivo`;
  - `exclusão bloqueada mostra os projetos e o caminho da transferência`;
  - `exclusão pede o código quando a conta tem 2FA`;
  - `exclusão concluída sai da sessão`;
  - `excluir projeto exige digitar o nome`;
  - `projeto padrão não oferece excluir`.
  - Cada um com a sabotagem correspondente: remover a checagem no componente.
  - E2E: a jornada "cria conta → exclui conta → e-mail livre para novo cadastro".
  - axe nas telas novas.
- [ ] **Passos 2 a 4:** falha, implementação, verde (`make test` e o E2E).
- [ ] **Passo 5:** commit `feat(painel): exportar, excluir conta e excluir projeto`.

### Sub-PR 6.6 — Ligar o cadastro público em produção (por último)

#### T12: Manifesto, testes de infraestrutura e runbook (esforço S de código)

**Pré-requisitos**
- 6.1–6.5 no ar.
- D2 aprovado (o texto continua com "[operador]").
- D6 decidido.

**6.6a (código)**
- `deploy/coolify/app.json`: acrescentar `ACCOUNT_EMAIL_FROM: ${ACCOUNT_EMAIL_FROM:-}`, `TURNSTILE_SITE_KEY: ${TURNSTILE_SITE_KEY:-}` e `TURNSTILE_SECRET_KEY: ${TURNSTILE_SECRET_KEY:-}`.
- `internal/config/manifestos_coolify_test.go`: remover as três entradas de `diferencasAceitas` e a constante `motivoCadastro`.
- `internal/config/infra_fixada_test.go` (`variaveisDoRecurso`): os três nomes com valores sintéticos.
  - Os valores do Turnstile **não** podem começar com `1x00000000`, `2x00000000` nem `3x00000000`, que a produção recusa. Exemplo: `0x4AAAAAAAsintetico`.
  - O `ACCOUNT_EMAIL_FROM` precisa ser um endereço válido no formato do D6.
- `docs/deploy/vps.md`: reescrever "Cadastro público: Turnstile e remetente de conta" com o runbook abaixo.
- Não leva `[skip ci]`: tem código e configuração.

**6.6b (depois do Passo 7)**
- Atualizar `deploy/coolify/infra-fixada` para o SHA do merge do 6.6a.
- Registrar no `CLAUDE.md` que o cadastro está ligado e qual é o remetente.

**Testes do 6.6a**
- `TestOAppDesteCommitSobeComOManifestoDaArvore` e `TestOAppDesteCommitSobeNaInfraestruturaFixada` verdes.
- Sabotagem: tirar um dos três nomes do `variaveisDoRecurso` deve reprovar o primeiro.

**Runbook (um passo por vez, cada um com um Confira)**

0. **Antes de tudo (o dono).** Nunca cole uma chave no chat. Copie do painel direto para o Bitwarden e do Bitwarden direto para o Coolify.
1. **Bitwarden.** Crie o item "StillOn / Turnstile" na pasta "StillOn", com os campos "site key" e "secret key" (vazios por enquanto).
   - **Confira:** o item aparece na pasta.
2. **Cloudflare.** Turnstile → Add widget: nome "StillOn", hostname `stillon.tech`, modo Managed. Copie as duas chaves para o item do passo 1.
   - **Confira:** o widget lista o hostname `stillon.tech` e o item do Bitwarden tem os dois campos preenchidos.
3. **Remetente (D6).**
   - **Opção A:** no Resend → Domains, confira que `avisos.stillon.tech` está Verified. Em API Keys, confira que a chave em uso tem "Sending access" e que o domínio dela cobre `avisos.stillon.tech`.
   - **Opção B:** Add domain `conta.stillon.tech`. Crie os registros em Hostinger → **Registros DNS** e clique em Verify. Se a chave atual não cobrir o domínio novo, crie outra com "Sending access" para os dois domínios, guarde-a em "StillOn / Resend" e troque o `RESEND_API_KEY` no passo 4. Só apague a chave antiga depois do passo 8.
   - **Confira:** domínio Verified. Veja também, no painel, o limite de domínios e de envios do plano.
4. **Coolify.** Recurso do app → Environment Variables. Acrescente `ACCOUNT_EMAIL_FROM` (o valor do D6), `TURNSTILE_SITE_KEY` e `TURNSTILE_SECRET_KEY` (marcada como segredo), colando do Bitwarden. Não faça Redeploy: o `app.json` atual ainda não repassa essas variáveis.
   - **Confira:** os três nomes aparecem na lista.
5. **Builder:** abre o 6.6a, espera o CI verde e faz o merge.
   - **Confira:** a run `Coolify release` verde, e `curl -s https://stillon.tech/api/v1/auth/signup-config` ainda mostra `"enabled":false` (o recurso continua no commit de infraestrutura antigo).
6. **Passo 7 (atualizar o commit de infraestrutura).**
   - Confira que a fila da VPS não tem `pending.json` e que nenhuma `Coolify release` está em andamento (`gh run list --workflow coolify.yml --limit 5`).
   - Revise `git diff <infra-fixada atual>..<SHA do merge> -- deploy/coolify/`.
   - No recurso, troque o commit de infraestrutura para o SHA do merge e publique.
   - **Confira:**
     - o deploy está `finished`;
     - `curl -s -o /dev/null -w '%{http_code}' https://stillon.tech/readyz` devolve `200`;
     - `curl -s https://stillon.tech/api/v1/auth/signup-config` mostra `"enabled":true` e `terms_version` (a site key é pública e pode aparecer).
7. **Builder:** abre o 6.6b (`infra-fixada` + `CLAUDE.md`).
   - **Confira:** CI verde e merge feito.
8. **Cadastro real.** O dono, numa janela anônima e com um e-mail secundário dele:
   - faz o cadastro em `/signup` marcando o aceite;
   - recebe a confirmação vinda do remetente do D6 (olhe também o spam) e confirma;
   - abre `/termos` e `/privacidade`;
   - exclui essa conta pelo painel.
   - **Confira:** os dois e-mails chegaram (confirmação e aviso do D8), a exclusão deu certo e o mesmo e-mail pode se cadastrar de novo.
9. **Se der errado.** Volte o commit de infraestrutura do recurso para o SHA anterior (Passo 7 ao contrário). O `app.json` antigo não repassa as variáveis e o cadastro desliga. **Não** apague só uma das chaves do Turnstile no Coolify: a produção recusa o par incompleto e o app não sobe.

---

## Verificação

- Em todo sub-PR: `go build ./... && go vet ./... && make test`.
- Nos sub-PRs com painel: também os alvos de teste e E2E do `web/` que o `Makefile` expõe.
- Depois de cada merge: run `Coolify release` verde e `readyz` 200.

## Autorrevisão

**Cobertura da spec**

| Requisito da spec | Onde |
|---|---|
| Exportar | T8, T9 |
| Excluir conta: senha + 2FA | T6, T7 |
| Bloqueio do último owner | B3 / T6 |
| Projetos só dela | T6 |
| Sessões revogadas | cascade + teste do `viva` em T7 |
| E-mail liberado | T6 |
| `audit_log.user_id` SET NULL | D3 / T6 |
| `/termos` e `/privacidade` com "[operador]" | T2 |
| Aceite gravado | T1 |
| Excluir projeto (adiado do PR 4) | T4 |
| Ligar o cadastro | T12 |

O destino de `user_id` nas tabelas de recurso fica para o contract do PR 7. Este plano só o torna anulável.

**Tipos**
- `ProjetoResumo` e `ErroDeExclusaoBloqueada` (T6) são os mesmos que o T7 usa.
- `EntrarNoProjeto` (T5) é o mesmo tipo consumido no T6.
- `DeleteProject` (T4) é reaproveitado no T6.

**Proporção:** os blocos de código ficam só no esboço da migration, nas assinaturas e no formato da exportação.

### Critical Files for Implementation
- internal/account/account.go (padrão do `ChangePassword` que o `ExcluirConta` segue; o novo `exclusao.go` fica ao lado)
- internal/storage/scope.go (escopo novo `InAccountDeletionTx`)
- internal/storage/migrations/00031_user_id_imutavel.sql (a trigger que precisa afrouxar na migration M+1)
- internal/httpapi/routes.go (rotas novas e cadeias)
- internal/config/infra_fixada_test.go (com `manifestos_coolify_test.go`, `deploy/coolify/app.json` e `docs/deploy/vps.md`, para ligar o cadastro no 6.6)
