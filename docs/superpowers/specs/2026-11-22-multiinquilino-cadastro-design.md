# Etapa 1: multi-inquilino e cadastro público — design

Data: 2026-11-22. Status: pronto para aprovação do dono (builder check feito em 2026-11-22).

## Intenção e porquê

O dono quer que o StillOn ofereça **no mínimo tudo o que o healthchecks.io oferece, e mais**, com
design próprio, para **qualquer pessoa** que se cadastre e use de verdade. Hoje o StillOn é de um
dono só: contas nascem pelo CLI (`account.CreateUser` recusa um segundo usuário) e cada recurso
pertence a um `user_id`. Esta etapa é a fundação do roteiro aprovado em 2026-11-22 (caminho A com
o atalho da C):

1. **Etapa 1 (este documento):** multi-inquilino, cadastro, cotas e proteção contra abuso.
2. Paridade de heartbeats (SP1 restante: PRs 4, 7–12).
3. Canais (SP2), que pode começar quando o modelo de projeto da etapa 1 estiver na `main`.
4. Além do healthchecks.io: API com chave, badges, Prometheus por check, WebAuthn, aviso de
   certificado, heartbeats na status page.

Inventário e comparação: `docs/superpowers/reports/2026-11-22-healthchecks-io-funcionalidades.md`
e a spec de paridade `2026-11-05-paridade-heartbeats.md` (vários descartes dela ficam reabertos
pelo pedido de 2026-11-22; cada um volta na etapa em que cabe).

## Decidido pelo dono (2026-11-22)

- Cadastro aberto ao público, para uso real (não só vitrine): proteção contra abuso e operação
  vêm antes da amplitude de funcionalidades.
- **Projeto é o inquilino.** Todo recurso pertence a um projeto; projetos têm membros com papéis;
  um usuário pode ter vários projetos. Organização acima de projeto fica para quando houver
  cobrança ou SSO.
- **Cotas do plano grátis, por conta:** 3 projetos; 3 membros por projeto; 20 alvos HTTP com
  intervalo mínimo de 5 min; 20 heartbeats; 5 canais por projeto. A conta do operador (dono) não
  tem essas cotas.

## Metas

- Qualquer pessoa cria conta com e-mail e senha, confirma o e-mail, recupera a senha pela web,
  exporta os dados e exclui a conta.
- Todo recurso (alvos, heartbeats, canais, status page) pertence a um projeto; membros com papéis
  acessam conforme o papel; convites por e-mail.
- O que existe hoje continua funcionando para o dono atual, no "Projeto padrão" dele, sem perda
  de dados nem de URLs (tokens de ping, slug da status page, links de verificação).
- Abuso contido: robôs no cadastro, envio de e-mail, sondagem HTTP a partir da VPS, ocupação de
  slugs, um projeto monopolizando o sondador.

## Não-metas

- Cobrança, planos pagos, organização acima de projeto, SSO, login com GitHub/Google (fases
  futuras).
- Painel web de administração (o operador usa o CLI).
- Novos tipos de monitor, canais novos, API com chave, badges (etapas 2–4).

## Anti-metas (falha mesmo funcionando)

- Um inquilino ler ou alterar dado de outro, por qualquer caminho (HTTP, worker, status page,
  ping). O isolamento continua garantido **no banco** (RLS com FORCE), não só no Go.
- A VPS compartilhada virar origem de varredura ou DDoS, ou o domínio de e-mail perder reputação.
- Regressão para o dono atual: monitor parado, aviso perdido, token de ping ou slug que deixam de
  valer durante ou depois da migração.
- Qualquer endpoint de conta que permita enumerar e-mails cadastrados.

## Restrições

- Go 1.26.11; Postgres (testes em 17 e 18); pgx + sqlc; migrations numeradas a partir da
  **00039** (a 00015 fica vaga; `storage.Migrate` recusa número menor que o aplicado).
- Um único papel de banco, dono das tabelas, com FORCE RLS; o boot recusa superusuário e
  BYPASSRLS (`internal/storage/role.go`). Continua assim.
- Escopos de transação em `internal/storage/scope.go` (`InUserTx`, `InProberTx`, `InPublicTx`,
  `InPingTx`, `InVerifyTx`, `InIPRateLimitTx`) via `set_config(..., true)`.
- Deploy automático a cada merge na `main` (merge = deploy), VPS compartilhada com outros
  projetos; migrations precisam ser seguras com o binário anterior ainda rodando durante o
  release (expand → migrate → contract).
- Textos legais (termos, privacidade) com o operador **em espaço reservado**: a identificação
  da empresa é assunto que o dono trata depois; não publicar nome nem CNPJ.
- Tudo em português do Brasil; um PR por mudança; TDD; revisão independente no SHA final.

## Abordagens consideradas

| | Descrição | Troca | Escolha |
|---|---|---|---|
| A | Projeto como inquilino; RLS por `app.project_id`, pertença conferida ao abrir a tx | Troca de posse em todas as tabelas, uma vez | **Escolhida pelo dono** |
| B | Organização → projetos | Nível a mais sem cobrança nem SSO à vista (YAGNI) | Depois, sobre A |
| C | Usuário dono + compartilhamento por recurso | RLS vira exceções; não chega a papéis por projeto | Descartada |

Ordem do roteiro: A (fundação primeiro) com o atalho de C (canais em paralelo quando o modelo de
projeto estiver pronto), escolhida pelo dono. B (paridade primeiro) descartada: migraria duas
vezes cada tabela nova.

## Desenho

### Domínio (DDD)

Contexto novo **Contas e Projetos** (`internal/tenancy`, nome do builder se preferir outro em
português/inglês consistente com o repositório):

- `Project` (id UUIDv7, nome, **`titular_id`**: o usuário cuja cota o projeto consome; nasce com
  quem criou o projeto e só muda por transferência explícita para outro owner).
- `Membership` (project_id, user_id, `role` ∈ {owner, manager, member, viewer}). Invariantes:
  todo projeto tem **pelo menos um owner**; o último owner não sai nem é rebaixado; owner
  transfere a posse promovendo outro antes.
- `Invitation` (project_id, e-mail normalizado, role, hash do token, expira em 7 dias, uso único).
- Papéis (o que cada um pode):
  - viewer: ler tudo do projeto.
  - member: viewer + criar/editar/pausar alvos, heartbeats, reconhecer incidentes.
  - manager: member + canais, status page, convites de member/viewer.
  - owner: tudo + membros e papéis, renomear e excluir o projeto.

Clean Architecture: o contexto novo tem **domínio puro** (tipos, invariantes, regras de papel) e
**portas** (`ProjectRepository`, `InvitationRepository`, `Mailer`) implementadas por adaptadores
pgx/sqlc e Resend. Os pacotes existentes (`targets`, `heartbeat`, `notify`, `statuspage`)
trocam `userID` por um `ProjectScope{ProjectID, UserID, Role}` e conferem a permissão pelo papel;
extrair portas deles **não** faz parte desta etapa (refatoração própria depois).

### Dados e migração (expand → migrate → contract)

1. **Expand:** tabelas `projects`, `project_members`, `project_invitations`; coluna `project_id`
   (nullable) em `targets`, `heartbeats`, `notif_channels`, `status_pages`; colunas novas em
   `users`: `email_verified_at`, `suspended_at`, `deleted_at` (ou exclusão física; ver Exclusão).
2. **Migrate (backfill, na mesma migration ou na seguinte):** um "Projeto padrão" por usuário
   existente, com o usuário como owner e titular; `project_id` preenchido; o dono atual marcado
   `users.is_operator`; `email_verified_at` dos usuários existentes = agora (o dono já é confiável).
3. **Contract (PR posterior, depois de um release com o código novo no ar):** `project_id NOT
   NULL`, índices `(project_id, id DESC)`, políticas trocadas para `app.project_id`, remoção de
   `status_pages.user_id UNIQUE` (passa a `project_id UNIQUE`), gatilho de imutabilidade passa a
   valer para `project_id`. `user_id` nas tabelas de recurso fica como "criado por" (auditoria)
   ou é removido — escolha do builder, registrada no plano.

Tabelas de posse indireta (checks, target_health, rollups, tags, tls, incidents, notifications,
heartbeat_events…) continuam indiretas; suas políticas passam a olhar o `project_id` do pai.

### Isolamento (RLS)

- Escopo novo `InProjectTx(ctx, userID, projectID, fn)`: na mesma transação, confere a pertença
  (`project_members` com o usuário **não suspenso**) e só então faz
  `set_config('app.project_id', ...)` e `set_config('app.user_id', ...)`; sem pertença →
  `ErrNotFound` (404, sem revelar que o projeto existe).
- Políticas dos recursos comparam `project_id = NULLIF(current_setting('app.project_id', true),
  '')::uuid`; nada de JOIN com `project_members` por linha (custo nas partições de `checks`).
- `project_members` e `projects` também com RLS: o usuário vê só os projetos de que é membro.
- `InUserTx` continua para o que é da pessoa (sessões, 2FA, perfil, lista de projetos).
- Workers (`InProberTx` etc.) seguem globais.

### API e painel

- Rotas de recurso ganham o projeto: `/api/v1/projects/{projectID}/targets`, etc. As rotas
  atuais sem projeto continuam por **um release** apontando para o Projeto padrão do usuário
  (compatibilidade do painel durante o deploy) e saem no PR de contract.
- `GET/POST /api/v1/projects`, `PATCH/DELETE /api/v1/projects/{id}`, membros e convites.
- Painel: seletor de projeto no topo (o projeto atual fica na URL), tela de membros e convites,
  telas de cadastro, confirmação de e-mail, "esqueci a senha", exportar e excluir conta. Design
  segue a direção C ("Editorial claro", Fraunces + Inter, índigo) já em produção.

### Contas (cadastro público)

- `POST /api/v1/auth/signup` {email, senha}: senha com as mesmas regras e Argon2id atuais;
  resposta **sempre** a mesma (202 "enviamos um e-mail") exista ou não a conta — se já existe,
  o e-mail enviado é "você já tem conta; esqueceu a senha?" (sem enumeração).
- Confirmação por link de uso único (token 256 bits, hash no banco, 24 h). Sem e-mail
  confirmado: login permitido, criação de monitores e canais bloqueada.
- "Esqueci a senha": link de uso único (1 h), resposta sempre igual; a troca revoga todas as
  sessões (reusa a revogação da troca de senha atual) e invalida links anteriores.
- Turnstile (Cloudflare) no cadastro e no "esqueci a senha"; a chave secreta vai para a VPS e o
  Bitwarden pelo runbook, nunca para o repositório. Teste com as chaves de teste públicas do
  Turnstile.
- Exportar: JSON com tudo que é da pessoa e dos projetos em que é owner.
- Excluir conta: exige senha (+ 2FA se ligado); bloqueia se a pessoa é a última owner de um
  projeto com outros membros (precisa transferir antes); projetos só dela são excluídos junto;
  sessões revogadas; e-mail liberado. Exclusão física com `audit_log.user_id` em SET NULL (já é).
- `account.CreateUser` do CLI continua para o operador; o bloqueio "já existe usuário" sai.

### Cotas e abuso

- Cotas por conta (decididas): projetos 3; membros por projeto 3; alvos HTTP 20 com intervalo
  mínimo 300 s; heartbeats 20; canais por projeto 5.
- *Chamada do Claude:* a cota é sempre do **titular** do projeto (`projects.titular_id`), não de
  quem cria o recurso nem dos demais owners; contagem = soma dos recursos nos projetos de que o
  usuário é titular; conferida na mesma tx da criação, sob a trava do **titular** (evita corrida
  de duas criações). Promover alguém a owner não mexe em cota.
- *Chamada do Claude:* "3 membros por projeto" = 3 pessoas **além do titular**.
- *Chamada do Claude:* a isenção do operador fica na conta (`users.is_operator`, marcado no
  backfill para o dono atual e pelo CLI); projeto cujo titular é operador não tem cotas (mantém
  os tetos atuais de 100).
- Limites de taxa que hoje ficam em memória por processo passam a usar a tabela compartilhada
  (`ip_rate_limits`, 00036) — cadastro, reenvio de confirmação, esqueci a senha, convites,
  verificação de canal.
- Envio de e-mail por conta: teto diário (convites + verificações + reenvios), além do teto por IP.
- **E-mail (decidido pelo dono, 2026-11-22):** a conta Resend é do plano grátis (100/dia,
  3.000/mês). E-mails de conta (confirmação, reset, convites, verificação de canal) têm **teto
  global de 50 por dia**, contado no banco e compartilhado entre réplicas; os outros 50 ficam
  reservados aos avisos de incidente, que nunca são bloqueados por e-mails de conta. Acima do
  teto, o pedido responde igual (sem enumeração) e o e-mail não sai; o operador vê no
  `user usage`/log.
- *Chamada do Claude:* e-mails de conta saem de um remetente em subdomínio próprio (ex.:
  `conta.stillon.tech`), para a reputação de um não contaminar o outro. Verificar o subdomínio
  no Resend é passo de runbook do operador; até lá, o remetente atual é usado, com o mesmo teto.
- Sondador justo (DDIA): `ClaimDueTargets` intercala por projeto (ex.: `row_number() OVER
  (PARTITION BY project_id ORDER BY next_due)` com teto por projeto por lote) para que um projeto
  não ocupe o lote inteiro.
- SSRF: `safedial` continua; acrescentar teto de requisições por destino (host) por minuto,
  somando todos os inquilinos, para a VPS não martelar um mesmo site.
- Status page: lista de slugs reservados (ex.: `admin`, `api`, `status`, `stillon`, `www`, nomes
  de marcas comuns — lista inicial no plano), além da regex atual.
- Operador (CLI): `user suspend|unsuspend <email>`, `user usage <email>` (projetos, contagens,
  e-mails do dia). Usuário suspenso: login recusado com o 401 genérico.
- *Chamada do Claude:* a suspensão é **derivada**, nada é apagado: o sondador, o avaliador de
  heartbeats, o despachante de avisos e a status page ignoram projetos cujo titular tem
  `suspended_at`; os outros membros desses projetos perdem o acesso enquanto durar. Ao reativar,
  tudo volta ao estado anterior (pausas escolhidas pelo usuário são preservadas).

### Textos legais

Páginas `/termos` e `/privacidade` estáticas no painel, com o operador como espaço reservado
("[operador]"); o cadastro exige marcar o aceite e grava a versão aceita. Conteúdo final depende
do dono (assunto do CNPJ, adiado por ele).

## Testes e evidência

- Isolamento: para cada rota de recurso, um teste que tenta ler/alterar o recurso de outro
  projeto (como não membro e como viewer quando muda estado) e recebe 404/403; e um teste de
  banco que, com `app.project_id` de A, não vê nada de B em todas as tabelas com RLS.
- Migração: teste que aplica as migrations sobre um banco com dados do modelo antigo (usuário,
  alvos, heartbeats, canais, status page, checks) e confere projeto padrão, posse e tokens
  intactos.
- Cadastro: sem enumeração (respostas e tempo equivalentes), tokens de uso único, expiração,
  revogação de sessões no reset, Turnstile obrigatório.
- Cotas sob concorrência (duas criações simultâneas no limite → só uma passa).
- Justiça do sondador: projeto com 1000 alvos vencidos não impede o de 1 alvo de ser sondado no
  mesmo ciclo.

## Fica com o builder

- Nome do pacote do contexto novo e dos tipos, seguindo o estilo do repositório.
- Divisão em PRs (sugestão: 1 expand+backfill; 2 escopo de projeto e rotas novas; 3 cadastro,
  confirmação e reset; 4 convites e papéis; 5 cotas e abuso; 6 exportar/excluir e textos legais;
  7 contract), desde que cada PR publique sem quebrar o anterior.
- Manter ou remover `user_id` das tabelas de recurso no contract.
- Detalhes de UI dentro da direção C.
