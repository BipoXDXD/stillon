# Etapa 1: multi-inquilino e cadastro público — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** transformar o StillOn de "um dono, contas pelo CLI" em serviço multi-inquilino com cadastro público, projetos com papéis, cotas e proteção contra abuso, sem regressão para o dono atual.

**Architecture:** projeto é o inquilino; recursos ganham `project_id`; o isolamento continua no banco (FORCE RLS), com a pertença conferida ao abrir a transação (`InProjectTx`) e políticas por `app.project_id`. Migração em expand → migrate → contract, um PR por passo, cada um publicável sozinho (merge = deploy). Contexto novo `internal/tenancy` com domínio puro e portas (Clean Architecture/DDD); pacotes existentes passam de `userID` para `ProjectScope`.

**Tech Stack:** Go 1.26.11, PostgreSQL 17/18, pgx + sqlc, testcontainers, React (painel embutido), Resend, Cloudflare Turnstile.

**Spec:** `docs/superpowers/specs/2026-11-22-multiinquilino-cadastro-design.md` (aprovada pelo dono em 2026-11-22).

## Global Constraints

- Migrations a partir da **00039**; a 00015 fica vaga; nunca número menor que o aplicado.
- Um único papel de banco com FORCE RLS; boot recusa superusuário e BYPASSRLS.
- Cada PR publica sozinho com o binário anterior ainda rodando durante o release (expand → migrate → contract).
- Cotas do plano grátis, por conta (do **titular**): projetos 3; membros por projeto 3 além do titular; alvos HTTP 20 com intervalo mínimo 300 s; heartbeats 20; canais por projeto 5. Conta `users.is_operator` sem essas cotas (tetos atuais de 100).
- E-mails de conta: teto global de **50/dia** no banco; avisos de incidente nunca bloqueados por eles.
- Respostas de cadastro, reenvio e "esqueci a senha" sempre iguais (sem enumeração).
- Operador em espaço reservado nos textos legais; nunca publicar nome nem CNPJ.
- Português do Brasil em código, commits e docs; TDD com o teste visto falhando; revisão independente no SHA final; merge fora de 00:00–01:00 e 03:10–03:45 BRT; conferir `Coolify release` e `readyz` depois de cada merge.

## Roteiro da etapa (um PR por linha)

| PR | Entrega | Depende de | Detalhe |
|---|---|---|---|
| 1 | Expand + backfill: `projects`, `project_members`, colunas novas em `users`, `project_id` nullable nos recursos, Projeto padrão do dono atual | — | tarefas abaixo |
| 2 | Escopo de projeto: `InProjectTx`, `internal/tenancy`, rotas `/api/v1/projects/{id}/…`, seletor de projeto no painel; rotas antigas apontam para o Projeto padrão | 1 | tarefas abaixo |
| 2b | Políticas por pertença ao projeto (permissivas por `app.project_id`, substituindo `user_id = app.user_id` nos recursos), fan-out de avisos e sondador por `project_id`, `POST /api/v1/projects` e seletor de projeto no painel | 2 | plano próprio quando o PR 2 estiver na `main` |
| 3 | Cadastro público: signup, confirmação de e-mail, "esqueci a senha", Turnstile, teto de 50 e-mails/dia, remetente de conta | 2 | idem |
| 4 | Convites e papéis: convites por e-mail (cria `project_invitations`), gestão de membros, regras do último owner, transferência de titular | 2b, 3 | idem |
| 5 | Cotas e abuso: cotas do titular sob trava, limites de taxa no banco, sondador justo por projeto, teto por destino, slugs reservados, CLI `suspend`/`usage`, suspensão derivada | 2 | idem |
| 6 | Exportar e excluir conta, termos e privacidade com aceite versionado | 3 | idem |
| 7 | Contract: `project_id NOT NULL`, remoção do gatilho de preenchimento e do `UNIQUE` em `status_pages.user_id`, remoção das rotas antigas | 2–6 no ar por um release | idem |

Portão (achado no rascunho): convites só depois do 2b, porque um membro convidado não passa em `user_id = app.user_id`.

Atalho da C: a SP2 (canais) pode começar depois do PR 2b na `main`, com `project_id` nas tabelas novas desde o início.

## Review Focus

Modos de falha que a spec implica e que nenhum teste atual cobre. Cada um ganhou um teste na tarefa dona:

1. **Reverter o expand apaga monitores em cascata.** `project_id` tem `ON DELETE CASCADE` para `projects`. Um down que apagasse projetos levaria alvos, heartbeats, canais e status pages junto. A spec proíbe perder dados e quer o binário anterior funcionando. Teste: `TestProjectMigrationsDownKeepResources` (Task 3).
2. **Linha gravada pelo binário anterior durante o release fica com `project_id` nulo e some.** Basta a política por projeto estar ligada para a linha sumir do escopo de projeto: é um monitor "perdido" para o dono. Testes: `TestOlderBinaryInsertsLandInDefaultProject` (Task 2) e `TestRestrictiveMigrationBackfillsOrphanRows` (Task 6).
3. **Dois projetos do mesmo usuário.** As políticas atuais (`user_id = app.user_id`) deixam os dados de um projeto aparecerem no outro. A spec exige o isolamento entre projetos no banco, não só no Go. Teste: `TestProjectScopeSeesNothingOfOtherProjectOfSameUser` (Task 6), que varre todas as tabelas com RLS.
4. **Titular suspenso, mas os outros membros continuam entrando.** A spec diz que eles perdem o acesso enquanto durar a suspensão. Teste: `TestInProjectTxRefusesMemberWhenTitularIsSuspended` (Task 7).
5. **Dá para descobrir se um projeto existe.** Isso acontece se o não membro receber 403, ou um 404 com corpo diferente do de um projeto inexistente. Teste: `TestAnotherUsersProjectIsIndistinguishableFromMissing` (Task 15).

## Decisões tomadas neste rascunho (uma linha de porquê cada)

- **`project_invitations` fica para o PR 4.** O `TestEveryTableForcesRLSWithAPolicy` exigiria agora uma política para um escopo de aceite por token que ainda não existe, e a tabela é nova: o PR 4 a cria sem precisar de expand.
- **Sim ao gatilho `BEFORE INSERT` que preenche `project_id`.** Ele usa o Projeto padrão do `user_id` e só age fora do escopo de projeto. O binário anterior, e o próprio binário do PR 1, gravam sem `project_id` durante o release. Rerodar só o backfill deixaria janelas. Com `app.project_id` ligado o gatilho não preenche, para que código novo que esqueça o `project_id` falhe alto (42501) em vez de cair no Projeto padrão. O gatilho sai no contract, junto com o `NOT NULL`.
- **Projeto padrão = `projects.is_default` mais índice único parcial por titular.** Não foi usado `users.default_project_id`: ele criaria uma FK circular com `users` em cascata. A política de `projects` já mostra ao titular os projetos dele, então o gatilho enxerga o padrão no `InUserTx` do código antigo, que é o único caminho de INSERT que ele tem.
- **`titular_id` e `project_id` com `ON DELETE CASCADE`.** Apagar usuário continua levando tudo, como hoje (testes de vários pacotes fazem `DELETE FROM users`). E nenhuma política dá DELETE em `projects`, então nenhum escopo da aplicação apaga projeto até o PR 6 decidir isso.
- **Índices `(project_id, id DESC)` e `status_pages (project_id) UNIQUE` vêm já no PR 1, não no contract.** As queries do PR 2 filtram por projeto. O `UNIQUE` já vale hoje (um usuário, uma página, um Projeto padrão), e o contract só tira o `UNIQUE` de `user_id`.
- **`users.deleted_at` não é criado.** A spec escolheu exclusão física. `user_id` fica nos recursos como "criado por": o PR 2 grava `user_id = scope.UserID`.
- **O PR 2 acrescenta políticas `AS RESTRICTIVE` por `app.project_id`, e não permissivas em OR.** A restritiva só estreita: dentro de `InProjectTx` some o que é de outro projeto, mesmo do mesmo usuário. Em todo outro escopo (`InUserTx` do binário anterior, sondador, ping, verify, página pública) `app.project_id` está vazio e ela vale `true`. Uma OR alargaria: qualquer transação com `app.project_id` veria as linhas daquele projeto sem `user_id`, e um erro na conferência de pertença viraria vazamento. Isso, num momento em que nenhum projeto tem um segundo membro, ou seja, sem ganho nenhum.
- **Portões que este rascunho deixa explícitos para o roteiro:**
  - O PR 4 (convites) não pode sair antes de políticas **permissivas** por projeto. Membro que não criou a linha falha em `user_id = app.user_id`. Ou o PR 7 vem antes do PR 4, ou o PR 4 as traz.
  - O fan-out (`ListDeliverableChannels(user_id)`) e o sondador passam a `project_id` antes de existir um segundo projeto por usuário (`POST /api/v1/projects`). No PR 2 isso não é possível em produção.
  - O seletor de projeto no painel, que o roteiro põe no PR 2, não está nestas tarefas: o painel continua nas rotas antigas durante o PR 2.

---

# PR 1 — expand + backfill

### Task 1: projetos, membros e colunas novas em `users`, com RLS forçada

**Files:**
- Create: `internal/storage/migrations/00039_projetos.sql`
- Test: `internal/storage/projetos_rls_test.go`
- Modify (gerado): `internal/storage/db/models.go`

**Interfaces:**
- Consumes: `testdb.New`, `novoUsuario`, `emEscopo(t, pool, guc, valor string, fn func(pgx.Tx) error) error` (de `heartbeat_rls_test.go`), `codigoPG`, `violaCheck`.
- Produces: tabelas `projects(id, name, titular_id, is_default, created_at)` e `project_members(project_id, user_id, role, created_at)`; colunas `users.is_operator bool NOT NULL DEFAULT false`, `users.email_verified_at timestamptz`, `users.suspended_at timestamptz`; modelos gerados `db.Project` e `db.ProjectMember`.

- [ ] **Step 1: testes que falham** (`projetos_rls_test.go`)
  - `TestProjectsVisibleOnlyToMembersAndTitular`. A, no escopo dele, cria `PA` e o próprio vínculo `owner`. Então:
    ```go
    if n := contarComEscopo(t, pool, a, "projects"); n != 1 { t.Errorf("A vê %d projetos, queria 1", n) }
    if n := contarComEscopo(t, pool, b, "projects"); n != 0 { t.Errorf("B vê %d projetos de A", n) }
    if n := contarComEscopo(t, pool, b, "project_members"); n != 0 { t.Errorf("B vê %d vínculos de A", n) }
    ```
  - `TestMemberCannotJoinAnotherTitularsProject`. Todos dão `codigoPG(err) == "42501"`:
    - B insere `(PA, B, 'owner')`;
    - A insere `(PA, B, 'viewer')` (incluir terceiros é do PR 4);
    - A insere `(PA, A, 'manager')` (o titular só entra como owner);
    - B insere um projeto com `titular_id = A`.
  - `TestProjectMemberRoleCheckRejectsUnknownRole`: via `testdb.Admin`, `role = 'admin'` dá `violaCheck(err)`.
  - `TestOnlyOneDefaultProjectPerTitular`: o segundo `is_default = true` do mesmo titular dá `codigoPG(err) == "23505"`; um segundo projeto não padrão passa.
  - `TestUsersStartAsNonOperatorUnverifiedActive`: depois de `novoUsuario`, `is_operator = false`, `email_verified_at IS NULL`, `suspended_at IS NULL`.
- [ ] **Step 2:** `GOTOOLCHAIN=go1.26.11 go test -race -count=1 -p 1 ./internal/storage -run 'TestProjectsVisibleOnlyToMembersAndTitular|TestMemberCannotJoinAnotherTitularsProject|TestProjectMemberRoleCheckRejectsUnknownRole|TestOnlyOneDefaultProjectPerTitular|TestUsersStartAsNonOperatorUnverifiedActive'`. Esperado: `ERROR: relation "projects" does not exist (SQLSTATE 42P01)` e `column "is_operator" does not exist`.
- [ ] **Step 3: implementar** `00039_projetos.sql`:
  ```sql
  -- +goose Up
  SET LOCAL lock_timeout = '5s';

  ALTER TABLE users
      ADD COLUMN is_operator       boolean NOT NULL DEFAULT false,
      ADD COLUMN email_verified_at timestamptz,
      ADD COLUMN suspended_at      timestamptz;

  CREATE TABLE projects (
      id         uuid PRIMARY KEY,
      name       text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
      titular_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
      is_default boolean NOT NULL DEFAULT false,
      created_at timestamptz NOT NULL
  );
  CREATE INDEX projects_titular_idx ON projects (titular_id);
  CREATE UNIQUE INDEX projects_um_padrao_por_titular ON projects (titular_id) WHERE is_default;

  CREATE TABLE project_members (
      project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
      user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
      role       text NOT NULL CHECK (role IN ('owner', 'manager', 'member', 'viewer')),
      created_at timestamptz NOT NULL,
      PRIMARY KEY (project_id, user_id)
  );
  CREATE INDEX project_members_user_idx ON project_members (user_id);

  ALTER TABLE projects ENABLE ROW LEVEL SECURITY;
  ALTER TABLE projects FORCE ROW LEVEL SECURITY;
  ALTER TABLE project_members ENABLE ROW LEVEL SECURITY;
  ALTER TABLE project_members FORCE ROW LEVEL SECURITY;

  -- A pessoa vê os próprios vínculos. Ver os colegas de projeto é do PR 4.
  CREATE POLICY project_members_own_select ON project_members FOR SELECT
      USING (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid);
  -- Só o titular entra, e só como owner, no projeto de que é titular. Esta
  -- política consulta projects, e a de projects consulta project_members só
  -- pela política de SELECT acima, que não volta a projects: sem ciclo (42P17).
  CREATE POLICY project_members_titular_owner_insert ON project_members FOR INSERT
      WITH CHECK (user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
                  AND role = 'owner'
                  AND EXISTS (SELECT 1 FROM projects p
                               WHERE p.id = project_members.project_id
                                 AND p.titular_id = NULLIF(current_setting('app.user_id', true), '')::uuid));

  -- O titular aparece antes do vínculo existir: o INSERT do vínculo de owner
  -- consulta o projeto recém-criado.
  CREATE POLICY projects_member_select ON projects FOR SELECT
      USING (titular_id = NULLIF(current_setting('app.user_id', true), '')::uuid
             OR EXISTS (SELECT 1 FROM project_members m
                         WHERE m.project_id = projects.id
                           AND m.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid));
  CREATE POLICY projects_titular_insert ON projects FOR INSERT
      WITH CHECK (titular_id = NULLIF(current_setting('app.user_id', true), '')::uuid);
  -- Sem UPDATE nem DELETE: renomear, transferir e excluir são dos PRs 4 e 6.

  -- +goose Down
  SET LOCAL lock_timeout = '5s';
  DROP TABLE project_members;
  DROP TABLE projects;
  ALTER TABLE users DROP COLUMN suspended_at, DROP COLUMN email_verified_at, DROP COLUMN is_operator;
  ```
  Depois: `make generate`.
- [ ] **Step 4:** o mesmo comando do Step 2 passa. Também passam `go test ... ./internal/storage -run 'TestEveryTableForcesRLSWithAPolicy|TestMigrat'`, e `sqlc diff` sai limpo.
- [ ] **Step 5:** `feat(storage): projetos e membros com RLS forçada e colunas de conta em users`

### Task 2: `project_id` nos recursos, gatilho de preenchimento e trava de imutabilidade

**Files:**
- Create: `internal/storage/migrations/00040_project_id_recursos.sql`
- Create: `internal/storage/testdb/projeto.go`
- Modify: `internal/storage/queries/targets.sql`, `heartbeats.sql`, `channels.sql`, `statuspages.sql` (só as projeções completas)
- Modify (gerado): `internal/storage/db/*`
- Test: `internal/storage/project_id_recursos_test.go`
- Modify: `internal/storage/endurecimento_rls_test.go` (`TestGatilhosDeGuardaSaoBeforeUpdatePorLinha` ganha os quatro gatilhos novos)

**Interfaces:**
- Produces:
  - `func testdb.ProjetoPadrao(t *testing.T, userID uuid.UUID) uuid.UUID`: idempotente, via `Admin(t)`, cria `is_default` + vínculo `owner` com `uuid.NewV7()`.
  - Coluna `project_id uuid NULL` em `targets`, `heartbeats`, `notif_channels`, `status_pages`. Nos modelos: `db.Target.ProjectID *uuid.UUID`, e o mesmo em `db.Heartbeat`, `db.NotifChannel`, `db.StatusPage`.
  - Funções SQL `preencher_project_id_padrao()` e `guard_project_id_imutavel()`.

- [ ] **Step 1: testes que falham**
  - `TestOlderBinaryInsertsLandInDefaultProject`, com subtestes por tabela. Constantes `antigaInsertTarget`, `antigaInsertHeartbeat`, `antigaInsertChannel` e `antigaInsertStatusPage` com o texto **atual** das queries `Insert*` (comentário "não atualize junto com as queries", como em `heartbeat_rollback_test.go`), rodadas em `emEscopo(..., "app.user_id", dono)`:
    ```go
    projeto := testdb.ProjetoPadrao(t, dono)
    // ... INSERT com o SQL antigo ...
    var got *uuid.UUID
    testdb.Admin(t).QueryRow(ctx, `SELECT project_id FROM `+tabela+` WHERE id = $1`, id).Scan(&got)
    if got == nil || *got != projeto { t.Errorf("%s: project_id = %v, queria o padrão %s", tabela, got, projeto) }
    ```
  - `TestInsertWithoutDefaultProjectKeepsNullProjectID`: usuário sem projeto, o INSERT antigo passa e `project_id IS NULL`. O binário anterior nunca quebra.
  - `TestFillTriggerSkipsProjectScope`: com `app.user_id` e `app.project_id` ligados e `project_id` omitido, a linha fica com `project_id IS NULL`.
  - `TestProjectIDIsImmutableOnceSet`:
    - `UPDATE ... SET project_id = <outro>` dá 42501 no escopo do dono (as quatro tabelas), no do ping (`heartbeats`) e no do verify (`notif_channels`);
    - `NULL → padrão` passa.
  - `TestDeletingUserTakesDefaultProjectAlong`: com projeto padrão e alvo, `DELETE FROM users` passa, e `projects` e `targets` daquele usuário ficam com 0 linhas (via admin).
  - `TestGetTargetReturnsProjectID`: `q.GetTarget(...)` no `InUserTx` devolve `row.ProjectID` igual a `&projeto`. Hoje não compila: `db.Target` não tem `ProjectID`.
- [ ] **Step 2:** `go test ... ./internal/storage -run 'TestOlderBinaryInsertsLandInDefaultProject|TestInsertWithoutDefaultProjectKeepsNullProjectID|TestFillTriggerSkipsProjectScope|TestProjectIDIsImmutableOnceSet|TestDeletingUserTakesDefaultProjectAlong|TestGetTargetReturnsProjectID|TestGatilhosDeGuardaSaoBeforeUpdatePorLinha'`. Esperado: erro de compilação `row.ProjectID undefined (type db.Target has no field or method ProjectID)`.
- [ ] **Step 3: implementar** `00040_project_id_recursos.sql`:
  ```sql
  -- +goose Up
  SET LOCAL lock_timeout = '5s';

  ALTER TABLE targets        ADD COLUMN project_id uuid REFERENCES projects(id) ON DELETE CASCADE;
  ALTER TABLE heartbeats     ADD COLUMN project_id uuid REFERENCES projects(id) ON DELETE CASCADE;
  ALTER TABLE notif_channels ADD COLUMN project_id uuid REFERENCES projects(id) ON DELETE CASCADE;
  ALTER TABLE status_pages   ADD COLUMN project_id uuid REFERENCES projects(id) ON DELETE CASCADE;

  CREATE INDEX targets_project_idx        ON targets (project_id, id DESC);
  CREATE INDEX heartbeats_project_idx     ON heartbeats (project_id, id DESC);
  CREATE INDEX notif_channels_project_idx ON notif_channels (project_id, id DESC);
  CREATE UNIQUE INDEX status_pages_project_key ON status_pages (project_id);

  -- O binário anterior (e o deste PR) grava sem project_id: o gatilho põe o
  -- Projeto padrão do user_id. Roda sob a RLS de quem insere; no InUserTx o
  -- titular enxerga o próprio projeto (projects_member_select). Dentro do
  -- escopo de projeto não preenche: código novo que esquece o project_id tem
  -- de falhar alto. Sai no contract, com o NOT NULL.
  -- +goose StatementBegin
  CREATE FUNCTION preencher_project_id_padrao() RETURNS trigger
  LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
  BEGIN
      IF NEW.project_id IS NULL
         AND NULLIF(current_setting('app.project_id', true), '') IS NULL THEN
          SELECT p.id INTO NEW.project_id
            FROM projects p
           WHERE p.titular_id = NEW.user_id AND p.is_default;
      END IF;
      RETURN NEW;
  END
  $$;
  -- +goose StatementEnd

  -- Como guard_user_id_imutavel (00031): os escopos de serviço (ping, verify,
  -- sondador) têm UPDATE de linha inteira. NULL → valor é o backfill.
  -- +goose StatementBegin
  CREATE FUNCTION guard_project_id_imutavel() RETURNS trigger
  LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp AS $$
  BEGIN
      IF OLD.project_id IS NOT NULL AND NEW.project_id IS DISTINCT FROM OLD.project_id THEN
          RAISE EXCEPTION 'O projeto (project_id) de % não muda.', TG_TABLE_NAME
              USING ERRCODE = '42501';
      END IF;
      RETURN NEW;
  END
  $$;
  -- +goose StatementEnd

  CREATE TRIGGER targets_project_id_padrao BEFORE INSERT ON targets
      FOR EACH ROW EXECUTE FUNCTION preencher_project_id_padrao();
  CREATE TRIGGER heartbeats_project_id_padrao BEFORE INSERT ON heartbeats
      FOR EACH ROW EXECUTE FUNCTION preencher_project_id_padrao();
  CREATE TRIGGER notif_channels_project_id_padrao BEFORE INSERT ON notif_channels
      FOR EACH ROW EXECUTE FUNCTION preencher_project_id_padrao();
  CREATE TRIGGER status_pages_project_id_padrao BEFORE INSERT ON status_pages
      FOR EACH ROW EXECUTE FUNCTION preencher_project_id_padrao();

  CREATE TRIGGER targets_project_id_imutavel BEFORE UPDATE OF project_id ON targets
      FOR EACH ROW EXECUTE FUNCTION guard_project_id_imutavel();
  CREATE TRIGGER heartbeats_project_id_imutavel BEFORE UPDATE OF project_id ON heartbeats
      FOR EACH ROW EXECUTE FUNCTION guard_project_id_imutavel();
  CREATE TRIGGER notif_channels_project_id_imutavel BEFORE UPDATE OF project_id ON notif_channels
      FOR EACH ROW EXECUTE FUNCTION guard_project_id_imutavel();
  CREATE TRIGGER status_pages_project_id_imutavel BEFORE UPDATE OF project_id ON status_pages
      FOR EACH ROW EXECUTE FUNCTION guard_project_id_imutavel();

  -- +goose Down
  SET LOCAL lock_timeout = '5s';
  -- DROP COLUMN leva a FK, os índices e os gatilhos da coluna; nenhuma linha
  -- de recurso é apagada (ver TestProjectMigrationsDownKeepResources).
  ALTER TABLE status_pages   DROP COLUMN project_id;
  ALTER TABLE notif_channels DROP COLUMN project_id;
  ALTER TABLE heartbeats     DROP COLUMN project_id;
  ALTER TABLE targets        DROP COLUMN project_id;
  DROP FUNCTION guard_project_id_imutavel();
  DROP FUNCTION preencher_project_id_padrao();
  ```
  Nas queries que devolvem o modelo inteiro, acrescente `, project_id` **no fim** da projeção para o sqlc continuar reusando o modelo: `GetTarget`, `GetTargetForUpdate`, `ListTargets`, `GetHeartbeat`, `GetHeartbeatForUpdate`, `LockHeartbeatForPing`, `GetChannel` e `GetStatusPage`. Nenhum INSERT muda neste PR. Depois: `make generate`.
- [ ] **Step 4:** o comando do Step 2 passa, mais `go test ... ./internal/storage` inteiro (o espelho `filtro_dono_*` e os de rollback continuam verdes) e `sqlc diff` limpo.
- [ ] **Step 5:** `feat(storage): project_id nos recursos com preenchimento pelo Projeto padrão e trava de imutabilidade`

### Task 3: backfill do Projeto padrão, operador e e-mail verificado

**Files:**
- Create: `internal/storage/migrations/00041_projeto_padrao.sql`
- Test: `internal/storage/projeto_padrao_migration_test.go`

**Interfaces:**
- Consumes: `storage.MigrateUpTo` e `storage.MigrateDownTo` (`export_test.go`), `testdb.EmptyDatabase`, `versaoDaMigration(t, sufixo)`, `novoUsuario`, `novoAlvo` e `novoCheck` (só colunas antigas).
- Produces: as funções SQL `gerar_uuid_v7() RETURNS uuid` e `backfill_projetos_padrao() RETURNS void`, idempotente e reusada pela 00042 e pelo contract.

- [ ] **Step 1: testes que falham.** O dado legado é gravado na **00038**, com SQL só das colunas daquela versão.
  - `TestDefaultProjectBackfillOnLegacyData`, com dois usuários; cada um tem alvo com check, heartbeat, canal de e-mail pendente e status page. Depois de `MigrateUpTo(versão da 00041)`, para cada usuário, no escopo dele:
    ```go
    // um projeto só, padrão, titular = u, nome "Projeto padrão", UUIDv7
    `SELECT count(*), bool_and(is_default), bool_and(titular_id = $1), bool_and(name = 'Projeto padrão'),
            bool_and(uuid_extract_version(id) = 7) FROM projects`  // == 1, true, true, true, true
    `SELECT role FROM project_members WHERE user_id = $1`           // == "owner"
    `SELECT count(*) FROM targets WHERE project_id IS DISTINCT FROM $2` // == 0 (idem heartbeats, notif_channels, status_pages)
    ```
    Mais estas conferências:
    - `ping_token_hash`, `slug` e `verify_token_hash` byte a byte iguais aos de antes;
    - `count(*) FROM checks` igual;
    - `users.is_operator = true` e `email_verified_at IS NOT NULL` nos dois.
  - `TestBackfillIsIdempotent`: `SELECT backfill_projetos_padrao()` duas vezes deixa `count(*) FROM projects` e `FROM project_members` iguais (lidos via admin).
  - `TestBackfillRerunDoesNotPromoteOperator`: no banco migrado, `novoUsuario` X e depois a função. X ganha o projeto padrão, mas `is_operator = false` e `email_verified_at IS NULL`.
  - `TestProjectMigrationsDownKeepResources`: com dado legado, sobe até a 00041 e `MigrateDownTo(versão da 00038)`. Então:
    - as contagens de `targets`, `heartbeats`, `notif_channels`, `status_pages` e `checks` ficam iguais;
    - `projects` não existe;
    - `antigaInsertTarget` (Task 2) continua passando;
    - subir de novo refaz o projeto padrão.
- [ ] **Step 2:** `go test ... ./internal/storage -run 'TestDefaultProjectBackfillOnLegacyData|TestBackfillIsIdempotent|TestBackfillRerunDoesNotPromoteOperator|TestProjectMigrationsDownKeepResources'`. Esperado: `versaoDaMigration` falha com "nenhuma migration termina em projeto_padrao", ou `function backfill_projetos_padrao() does not exist`.
- [ ] **Step 3: implementar** `00041_projeto_padrao.sql`:
  ```sql
  -- +goose Up
  SET LOCAL lock_timeout = '5s';

  -- UUIDv7 em SQL (o uuidv7() nativo só existe no 18; a CI testa o 17).
  -- +goose StatementBegin
  CREATE FUNCTION gerar_uuid_v7() RETURNS uuid
  LANGUAGE sql VOLATILE SET search_path = pg_catalog, pg_temp AS $$
      SELECT encode(
          set_bit(set_bit(
              overlay(uuid_send(gen_random_uuid())
                      PLACING substring(int8send((extract(epoch FROM clock_timestamp()) * 1000)::bigint) FROM 3)
                      FROM 1 FOR 6),
              52, 1), 53, 1),
          'hex')::uuid
  $$;
  -- +goose StatementEnd

  -- Roda com o papel da aplicação, sob FORCE RLS: sem escopo, os UPDATEs não
  -- enxergariam linha nenhuma e a migration "passaria" sem preencher nada.
  -- Por isso liga app.user_id de cada usuário, e as políticas de dono (e as
  -- de projects/project_members da 00039) deixam passar só o que é dele.
  -- Idempotente: a 00042 e o contract a chamam de novo para as linhas que o
  -- binário anterior gravar sem projeto durante o release.
  -- +goose StatementBegin
  CREATE FUNCTION backfill_projetos_padrao() RETURNS void
  LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
  DECLARE
      u       uuid;
      projeto uuid;
  BEGIN
      FOR u IN SELECT id FROM users ORDER BY id LOOP
          PERFORM set_config('app.user_id', u::text, true);
          INSERT INTO projects (id, name, titular_id, is_default, created_at)
          VALUES (gerar_uuid_v7(), 'Projeto padrão', u, true, now())
          ON CONFLICT (titular_id) WHERE is_default DO NOTHING;
          SELECT p.id INTO STRICT projeto FROM projects p WHERE p.titular_id = u AND p.is_default;
          INSERT INTO project_members (project_id, user_id, role, created_at)
          VALUES (projeto, u, 'owner', now())
          ON CONFLICT DO NOTHING;
          UPDATE targets        SET project_id = projeto WHERE user_id = u AND project_id IS NULL;
          UPDATE heartbeats     SET project_id = projeto WHERE user_id = u AND project_id IS NULL;
          UPDATE notif_channels SET project_id = projeto WHERE user_id = u AND project_id IS NULL;
          UPDATE status_pages   SET project_id = projeto WHERE user_id = u AND project_id IS NULL;
      END LOOP;
      PERFORM set_config('app.user_id', '', true);
  END
  $$;
  -- +goose StatementEnd

  SELECT backfill_projetos_padrao();

  -- Uma vez só, e fora da função: até aqui só o CLI criava usuário, e ele
  -- recusa o segundo (account.ErrUserExists). Quem existe agora é o operador,
  -- e o e-mail dele é confiável. Rerodar a função depois do cadastro público
  -- não pode promover ninguém.
  UPDATE users SET is_operator = true, email_verified_at = COALESCE(email_verified_at, now());

  -- +goose Down
  -- Os projetos e o project_id preenchidos ficam: apagar projeto levaria os
  -- recursos junto (ON DELETE CASCADE da 00040). O down da 00040 derruba as
  -- colunas e o da 00039 as tabelas, sem apagar recurso.
  DROP FUNCTION backfill_projetos_padrao();
  DROP FUNCTION gerar_uuid_v7();
  ```
- [ ] **Step 4:** o comando do Step 2 passa, mais `go test ... ./internal/storage`.
- [ ] **Step 5:** `feat(storage): backfill do Projeto padrão e marcação do operador`

### Task 4: `account.CreateUser` cria o Projeto padrão e marca o operador

**Files:**
- Create: `internal/storage/queries/projects.sql`
- Modify: `internal/storage/queries/users.sql`, `internal/account/account.go`
- Modify (gerado): `internal/storage/db/projects.sql.go`, `users.sql.go`
- Test: `internal/account/projeto_padrao_test.go`

**Interfaces:**
- Consumes: `q.ScopeToUser(ctx, userID uuid.UUID) error`.
- Produces as queries (nomes de `sqlc.arg` fixados para fixar os campos gerados):
  ```sql
  -- name: EnsureDefaultProject :exec
  INSERT INTO projects (id, name, titular_id, is_default, created_at)
  VALUES (sqlc.arg(id), sqlc.arg(name), sqlc.arg(titular_id), true, sqlc.arg(now))
  ON CONFLICT (titular_id) WHERE is_default DO NOTHING;
  -- name: GetDefaultProjectID :one
  SELECT id FROM projects WHERE titular_id = sqlc.arg(titular_id) AND is_default;
  -- name: EnsureProjectOwner :exec
  INSERT INTO project_members (project_id, user_id, role, created_at)
  VALUES (sqlc.arg(project_id), sqlc.arg(user_id), 'owner', sqlc.arg(now))
  ON CONFLICT DO NOTHING;
  -- name: MarkUserOperator :exec   (users.sql)
  UPDATE users SET is_operator = true, email_verified_at = sqlc.arg(now) WHERE id = sqlc.arg(id);
  ```
  E a constante `const nomeProjetoPadrao = "Projeto padrão"` em `account`. Ela passa a `tenancy.DefaultProjectName` na Task 8.

- [ ] **Step 1: testes que falham**
  - `TestCreateUserCreatesDefaultProjectAndMarksOperator`: depois de `svc.CreateUser(ctx, "dono@exemplo.com", senha)`, lendo via `testdb.Admin`:
    ```go
    // is_operator, email_verified_at = clk.Now()
    // exatamente 1 projeto: titular = id, is_default, name "Projeto padrão"
    // 1 vínculo (projeto, id, "owner")
    ```
  - `TestCreatedUserTargetLandsInDefaultProject`: `InsertTarget` gerado (sem `project_id`), num `storage.InUserTx` do usuário criado, e `GetTarget(...).ProjectID` aponta para o projeto padrão.
  - `TestCreateUserRefusesSecondUserWithoutCreatingProject`: a segunda chamada dá `ErrUserExists` e `count(*) FROM projects` continua 1.
- [ ] **Step 2:** `go test ... ./internal/account -run 'TestCreateUserCreatesDefaultProjectAndMarksOperator|TestCreatedUserTargetLandsInDefaultProject|TestCreateUserRefusesSecondUserWithoutCreatingProject'`. Esperado: `is_operator = false`, queria `true`; `0 projetos, queria 1`.
- [ ] **Step 3: implementar.** No `CreateUser`, dentro da mesma transação e depois de `InsertUser`, nesta ordem:
  1. `MarkUserOperator(id, now)`;
  2. `ScopeToUser(id)`, porque projects/project_members estão sob FORCE RLS;
  3. `EnsureDefaultProject(gerado com uuid.NewV7(), nomeProjetoPadrao, id, now)`;
  4. `GetDefaultProjectID(id)`;
  5. `EnsureProjectOwner(projeto, id, now)`;
  6. a auditoria (`audit`), como hoje.

  O bloqueio "já existe usuário" continua: sai no PR 3. Depois: `make generate`.
- [ ] **Step 4:** o comando do Step 2 passa, mais `go test ... ./internal/account ./cmd/...` e `sqlc diff` limpo.
- [ ] **Step 5:** `feat(account): usuário criado pelo CLI nasce operador e com Projeto padrão`

---

# PR 2 — escopo de projeto

### Task 5: contexto `internal/tenancy`: papéis, permissões e invariante de owner

**Files:**
- Create: `internal/tenancy/role.go`, `internal/tenancy/scope.go`, `internal/tenancy/membership.go`
- Test: `internal/tenancy/role_test.go`, `internal/tenancy/membership_test.go`, `internal/tenancy/fronteiras_test.go`

**Interfaces (Produces):**
```go
package tenancy

type Role string
const (
    RoleOwner   Role = "owner"
    RoleManager Role = "manager"
    RoleMember  Role = "member"
    RoleViewer  Role = "viewer"
)
func ParseRole(s string) (Role, error) // ErrUnknownRole

type Permission uint8
const (
    PermRead Permission = iota + 1 // ler tudo do projeto
    PermEditMonitors               // criar/editar/pausar alvos e heartbeats, reconhecer incidentes
    PermManageChannels
    PermManageStatusPage
    PermInviteBasic                // convidar member e viewer
    PermManageMembers              // membros, papéis, convites de manager/owner
    PermManageProject              // renomear e excluir
)
func (r Role) Can(p Permission) bool // papel desconhecido: false para tudo

const DefaultProjectName = "Projeto padrão"

var (
    ErrUnknownRole      = errors.New("papel desconhecido")
    ErrForbidden        = errors.New("operação não permitida para o papel")
    ErrProjectNotFound  = errors.New("projeto não encontrado")
    ErrLastOwner        = errors.New("o projeto ficaria sem owner")
    ErrNotMember        = errors.New("não é membro do projeto")
)

type ProjectScope struct {
    ProjectID, UserID uuid.UUID
    Role              Role
}
func (s ProjectScope) Require(p Permission) error // nil ou ErrForbidden

type Member struct {
    UserID uuid.UUID
    Role   Role
}
func ChangeRole(members []Member, userID uuid.UUID, to Role) ([]Member, error)
func RemoveMember(members []Member, userID uuid.UUID) ([]Member, error)
```
Matriz fixada (✓ = pode):

| | viewer | member | manager | owner |
|---|---|---|---|---|
| Read | ✓ | ✓ | ✓ | ✓ |
| EditMonitors | | ✓ | ✓ | ✓ |
| ManageChannels, ManageStatusPage, InviteBasic | | | ✓ | ✓ |
| ManageMembers, ManageProject | | | | ✓ |

- [ ] **Step 1: testes que falham**
  - `TestRolePermissionMatrix`: tabela com os 4 papéis e as 7 permissões, exaustiva, contra a matriz acima.
  - `TestUnknownRoleHasNoPermission`: `Role("admin").Can(PermRead) == false`; `ParseRole("admin")` dá `errors.Is(err, ErrUnknownRole)`.
  - `TestProjectScopeRequire`: `ProjectScope{Role: RoleViewer}.Require(PermEditMonitors)` dá `ErrForbidden`; owner dá `nil`.
  - `TestLastOwnerCannotBeDemotedOrRemoved`:
    - `ChangeRole([{A owner},{B member}], A, RoleManager)` dá `ErrLastOwner`;
    - `RemoveMember([{A owner}], A)` dá `ErrLastOwner`;
    - com dois owners, rebaixar um passa e o resultado tem exatamente um owner.
  - `TestChangeRoleOfNonMember`: dá `ErrNotMember`.
  - `TestTenancyImportsNoInfrastructure`: com `go/parser` sobre os `.go` não-teste do pacote, os únicos imports são a biblioteca padrão, `github.com/google/uuid` e `internal/clock`.
- [ ] **Step 2:** `go test ... ./internal/tenancy -run 'TestRolePermissionMatrix|TestUnknownRoleHasNoPermission|TestProjectScopeRequire|TestLastOwnerCannotBeDemotedOrRemoved|TestChangeRoleOfNonMember|TestTenancyImportsNoInfrastructure'`. Esperado: `no non-test Go files in .../internal/tenancy` ou `undefined: Role`.
- [ ] **Step 3: implementar** as assinaturas acima. `ChangeRole` e `RemoveMember` devolvem cópia, sem alterar a entrada.
- [ ] **Step 4:** o comando do Step 2 passa.
- [ ] **Step 5:** `feat(tenancy): papéis, permissões e invariante de pelo menos um owner`

### Task 6: política restritiva por `app.project_id` e segundo passe do backfill

**Files:**
- Create: `internal/storage/migrations/00042_rls_restrita_por_projeto.sql`
- Modify: `internal/storage/testdb/projeto.go`
- Test: `internal/storage/rls_projeto_test.go`
- Modify: `internal/storage/endurecimento_rls_test.go` (`TestPoliticasDeUpdateDasTabelasComDono`)

**Interfaces:**
- Produces:
  ```go
  func testdb.NovoProjeto(t *testing.T, titular uuid.UUID, nome string) uuid.UUID // não padrão, com owner
  func testdb.Membro(t *testing.T, projeto, usuario uuid.UUID, papel string)
  func testdb.Suspender(t *testing.T, usuario uuid.UUID)
  ```
  E o helper de teste `emEscopoDeProjeto(t, pool, usuario, projeto uuid.UUID, fn func(pgx.Tx) error) error`, que liga os dois GUCs com SQL cru.
- [ ] **Step 1: testes que falham**
  - `TestProjectScopeSeesNothingOfOtherProjectOfSameUser`. U é titular de A (padrão) e de B (`NovoProjeto`).
    - Via admin, uma linha por tabela em A e em B: alvo com check, `target_health`, rollup, incidente, tag e `target_tls`; heartbeat com evento, tag e incidente; canal com notificação e `channel_health`. A status page e os itens ficam só em A, porque `status_pages.user_id` é UNIQUE.
    - Para cada tabela de `tabelasDoProjeto`: `count(*)` sem WHERE em `emEscopoDeProjeto(U, A)` dá as de A, e em `(U, B)` dá as de B (0 páginas).
    - A lista é conferida contra o catálogo (`lerTabelasDaAplicacao`): toda tabela com RLS fora de `tabelasForaDoProjeto` está em `tabelasDoProjeto`. A lista de fora, cada uma com o motivo: `sessions`, `session_family_proofs`, `totp_recovery_codes`, `ip_rate_limits`, `projects`, `project_members` e as partições de `checks`, cobertas pela mãe.
  - `TestProjectScopeRefusesWritingIntoAnotherProject`, em `(U, A)`:
    - INSERT de alvo com `project_id = B` dá 42501;
    - INSERT sem `project_id` dá 42501 (o gatilho não preenche no escopo de projeto);
    - `UPDATE targets SET name='x' WHERE id = <alvo de B>` afeta 0 linhas.
  - `TestRestrictivePolicyLeavesServiceScopesUntouched`:
    - `InUserTx(U)` vê os alvos de A e B (binário anterior);
    - `InProberTx` reivindica os dois;
    - `InPingTx(hash do heartbeat de B)` acha a linha;
    - `InPublicTx(slug)` acha a página.
  - `TestRestrictiveMigrationBackfillsOrphanRows`: banco vazio até a 00041; usuário **sem** projeto e alvo gravado com o SQL antigo (`project_id NULL`); `MigrateUpTo(00042)`. O alvo fica com o `project_id` do projeto padrão criado.
  - Em `TestPoliticasDeUpdateDasTabelasComDono`, acrescentar às esperadas:
    ```go
    "heartbeats.heartbeats_so_projeto_do_escopo:*",
    "notif_channels.notif_channels_so_projeto_do_escopo:*",
    "targets.targets_so_projeto_do_escopo:*",
    ```
- [ ] **Step 2:** `go test ... ./internal/storage -run 'TestProjectScopeSeesNothingOfOtherProjectOfSameUser|TestProjectScopeRefusesWritingIntoAnotherProject|TestRestrictivePolicyLeavesServiceScopesUntouched|TestRestrictiveMigrationBackfillsOrphanRows|TestPoliticasDeUpdateDasTabelasComDono'`. Esperado: no escopo de A, `targets` conta 2 (A e B), queria 1; o INSERT em B passa, queria 42501.
- [ ] **Step 3: implementar** `00042_rls_restrita_por_projeto.sql`:
  ```sql
  -- +goose Up
  -- Políticas RESTRITIVAS: entram em AND com as permissivas (as de dono por
  -- user_id continuam até o contract). Só estreitam: com app.project_id
  -- ligado (só storage.InProjectTx o liga, depois de conferir a pertença), a
  -- linha tem de ser daquele projeto; sem ele (InUserTx do binário anterior,
  -- sondador, ping, verify, página pública), valem true. As tabelas de posse
  -- indireta herdam pelo EXISTS que já fazem no pai. NULLIF pelo motivo da
  -- 00004 (GUC vira '' na conexão reusada).
  SET LOCAL lock_timeout = '5s';

  CREATE POLICY targets_so_projeto_do_escopo ON targets AS RESTRICTIVE FOR ALL
      USING (NULLIF(current_setting('app.project_id', true), '') IS NULL
             OR project_id = NULLIF(current_setting('app.project_id', true), '')::uuid)
      WITH CHECK (NULLIF(current_setting('app.project_id', true), '') IS NULL
                  OR project_id = NULLIF(current_setting('app.project_id', true), '')::uuid);
  CREATE POLICY heartbeats_so_projeto_do_escopo ON heartbeats AS RESTRICTIVE FOR ALL
      USING (NULLIF(current_setting('app.project_id', true), '') IS NULL
             OR project_id = NULLIF(current_setting('app.project_id', true), '')::uuid)
      WITH CHECK (NULLIF(current_setting('app.project_id', true), '') IS NULL
                  OR project_id = NULLIF(current_setting('app.project_id', true), '')::uuid);
  CREATE POLICY notif_channels_so_projeto_do_escopo ON notif_channels AS RESTRICTIVE FOR ALL
      USING (NULLIF(current_setting('app.project_id', true), '') IS NULL
             OR project_id = NULLIF(current_setting('app.project_id', true), '')::uuid)
      WITH CHECK (NULLIF(current_setting('app.project_id', true), '') IS NULL
                  OR project_id = NULLIF(current_setting('app.project_id', true), '')::uuid);
  CREATE POLICY status_pages_so_projeto_do_escopo ON status_pages AS RESTRICTIVE FOR ALL
      USING (NULLIF(current_setting('app.project_id', true), '') IS NULL
             OR project_id = NULLIF(current_setting('app.project_id', true), '')::uuid)
      WITH CHECK (NULLIF(current_setting('app.project_id', true), '') IS NULL
                  OR project_id = NULLIF(current_setting('app.project_id', true), '')::uuid);

  -- Segundo passe: linhas que o binário anterior gravou sem projeto entre a
  -- 00041 e aqui (usuário criado por binário anterior ao PR 1).
  SELECT backfill_projetos_padrao();

  -- +goose Down
  DROP POLICY status_pages_so_projeto_do_escopo ON status_pages;
  DROP POLICY notif_channels_so_projeto_do_escopo ON notif_channels;
  DROP POLICY heartbeats_so_projeto_do_escopo ON heartbeats;
  DROP POLICY targets_so_projeto_do_escopo ON targets;
  ```
  Mais os três helpers de `testdb`, via `Admin(t)`.
- [ ] **Step 4:** o comando do Step 2 passa, mais `go test ... ./internal/storage`.
- [ ] **Step 5:** `feat(storage): política restritiva por projeto no escopo app.project_id`

### Task 7: `storage.InProjectTx`

**Files:**
- Modify: `internal/storage/scope.go`, `internal/storage/queries/scope.sql`
- Modify (gerado): `internal/storage/db/scope.sql.go`
- Test: `internal/storage/escopo_projeto_test.go`

**Interfaces:**
- Consumes: `tenancy.ProjectScope`, `tenancy.ParseRole`, `tenancy.ErrProjectNotFound`.
- Produces:
  ```go
  var ErrNoProject = errors.New("transação de projeto sem projeto")
  // InProjectTx: na MESMA transação, liga app.user_id, confere a pertença
  // (membro não suspenso, titular não suspenso) e só então liga
  // app.project_id. Sem pertença: tenancy.ErrProjectNotFound, igual ao de
  // projeto inexistente, e fn não roda. fn recebe o escopo lido aqui: a
  // permissão vale pelo papel desta transação, nunca pelo de quem chamou.
  func InProjectTx(ctx context.Context, b Beginner, userID, projectID uuid.UUID,
      fn func(*db.Queries, tenancy.ProjectScope) error) error
  ```
  As queries:
  ```sql
  -- name: ScopeToProject :exec
  SELECT set_config('app.project_id', sqlc.arg(project_id)::uuid::text, true);
  -- name: ActiveMembershipRole :one
  SELECT m.role
    FROM project_members m
    JOIN users u ON u.id = m.user_id
    JOIN projects p ON p.id = m.project_id
    JOIN users t ON t.id = p.titular_id
   WHERE m.project_id = sqlc.arg(project_id) AND m.user_id = sqlc.arg(user_id)
     AND u.suspended_at IS NULL AND t.suspended_at IS NULL;
  ```
  Sem `FOR SHARE`: a trava de linha exigiria política de UPDATE em `project_members`. A remoção concorrente de membro é do PR 4, com a mesma semântica da revogação de sessão: o pedido em voo conclui.
- [ ] **Step 1: testes que falham**
  - `TestInProjectTxScopesUserAndProject`: U com alvos em A e B; dentro de `InProjectTx(U, A)`, `q.CountTargets(ctx, U)` (filtra só por `user_id`) dá 1, o que prova `app.project_id`; `sc == tenancy.ProjectScope{ProjectID: A, UserID: U, Role: tenancy.RoleOwner}`.
  - `TestInProjectTxRefusesNonMemberLikeMissingProject`: V em A e V num `uuid.New()` dão os dois `errors.Is(err, tenancy.ErrProjectNotFound)`, e `chamou == false`.
  - `TestInProjectTxRefusesSuspendedMember`: `testdb.Suspender(t, U)` faz `InProjectTx(U, A)` dar `ErrProjectNotFound`.
  - `TestInProjectTxRefusesMemberWhenTitularIsSuspended`: `testdb.Membro(t, A, M, "viewer")`; com `testdb.Suspender(t, U)` (titular), M dá `ErrProjectNotFound`; sem a suspensão, `sc.Role == tenancy.RoleViewer`.
  - `TestInProjectTxRejectsNilIDs`: `uuid.Nil` como usuário dá `ErrNoUser`; como projeto, dá `ErrNoProject`.
  - `TestProjectScopeSurvivesConnectionReuse`, no molde de `TestScopeSurvivesConnectionReuse`, numa `*pgxpool.Conn`:
    - depois de `InProjectTx`, `SELECT count(*) FROM targets` sem escopo não falha e dá 0;
    - `InUserTx(U)` na mesma conexão vê os 2 alvos;
    - premissa: `current_setting('app.project_id', true) = ''`.
- [ ] **Step 2:** `go test ... ./internal/storage -run 'TestInProjectTx|TestProjectScopeSurvivesConnectionReuse'`. Esperado: `undefined: storage.InProjectTx`.
- [ ] **Step 3: implementar** com as assinaturas e queries acima, na ordem:
  1. `Begin`;
  2. `userID`/`projectID` não nulos, senão `ErrNoUser` ou `ErrNoProject`;
  3. `ScopeToUser`;
  4. `ActiveMembershipRole`: `pgx.ErrNoRows` vira `tenancy.ErrProjectNotFound`;
  5. `ParseRole`: falha vira erro interno embrulhado;
  6. `ScopeToProject`;
  7. `fn`;
  8. `Commit`.

  Comentário no código: "único lugar que liga `app.project_id`". Depois: `make generate`.
- [ ] **Step 4:** o comando do Step 2 passa, mais `go test ... ./internal/storage`.
- [ ] **Step 5:** `feat(storage): escopo de projeto com pertença conferida na transação`

### Task 8: serviço do contexto, porta e adaptador pgx

**Files:**
- Create: `internal/tenancy/ports.go`, `internal/tenancy/service.go`, `internal/tenancy/pgstore/store.go`
- Modify: `internal/storage/queries/projects.sql`, `internal/account/account.go` (passa a usar `tenancy.DefaultProjectName`)
- Test: `internal/tenancy/pgstore/store_test.go`, `internal/tenancy/service_test.go`

**Interfaces (Produces):**
```go
// tenancy
type ProjectSummary struct {
    ID        uuid.UUID
    Name      string
    IsDefault bool
    Role      Role
    CreatedAt time.Time
}
type ProjectRepository interface {
    Membership(ctx context.Context, userID, projectID uuid.UUID) (ProjectScope, error)
    EnsureDefault(ctx context.Context, userID uuid.UUID, now time.Time) (ProjectScope, error)
    ListForUser(ctx context.Context, userID uuid.UUID) ([]ProjectSummary, error)
}
type Service struct { /* repo, clk */ }
func NewService(repo ProjectRepository, clk clock.Clock) *Service
func (s *Service) Scope(ctx context.Context, userID, projectID uuid.UUID) (ProjectScope, error)
func (s *Service) DefaultScope(ctx context.Context, userID uuid.UUID) (ProjectScope, error) // garante o padrão
func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]ProjectSummary, error)

// pgstore
type Store struct{ /* pool */ }
func New(pool *pgxpool.Pool) *Store // satisfaz tenancy.ProjectRepository
```
A query nova:
```sql
-- name: ListMyProjects :many
SELECT p.id, p.name, p.is_default, m.role, p.created_at
  FROM project_members m
  JOIN projects p ON p.id = m.project_id
  JOIN users t ON t.id = p.titular_id
 WHERE m.user_id = sqlc.arg(user_id) AND t.suspended_at IS NULL
 ORDER BY p.is_default DESC, p.id;
```
`EnsureDefault`:
1. `InUserTx`: `EnsureDefaultProject` (`uuid.NewV7`, `DefaultProjectName`), `GetDefaultProjectID`, `EnsureProjectOwner`;
2. depois, `Membership`, que aplica as regras de suspensão.

`InvitationRepository` e `Mailer` ficam para o PR 4.
- [ ] **Step 1: testes que falham**
  - `TestEnsureDefaultCreatesOneProjectUnderRace`: duas goroutines `EnsureDefault(U)` para usuário sem projeto devolvem o mesmo `ProjectID`, com `count(*) FROM projects` = 1 e vínculos = 1 (admin).
  - `TestEnsureDefaultKeepsExistingDefault`: com `testdb.ProjetoPadrao`, devolve esse id.
  - `TestListForUserShowsOnlyOwnMemberships`: U vê `[A padrão owner, B viewer]` nessa ordem; o projeto de V, de que U não é membro, não aparece.
  - `TestListForUserHidesProjectsOfSuspendedTitular`.
  - `TestServiceDefaultScopeUsesClock` (`service_test.go`, com repositório falso): `now` repassado == `clk.Now()`.
- [ ] **Step 2:** `go test ... ./internal/tenancy/... -run 'TestEnsureDefault|TestListForUser|TestServiceDefaultScopeUsesClock'`. Esperado: `undefined: pgstore.New` / `undefined: tenancy.NewService`.
- [ ] **Step 3: implementar** as assinaturas acima; `make generate`.
- [ ] **Step 4:** o comando do Step 2 passa, mais `go test ... ./internal/account`.
- [ ] **Step 5:** `feat(tenancy): serviço de projetos com adaptador pgx e Projeto padrão garantido`

### Task 9: middleware de projeto e rotas antigas apontando para o Projeto padrão

**Files:**
- Create: `internal/httpapi/middleware/projeto.go`, `internal/httpapi/escopo.go`
- Modify: `internal/httpapi/routes.go`, `cmd/stillon/main.go`
- Modify (testes que montam `Deps`): `internal/httpapi/auth_integration_test.go` (`newAPICom`), `limites_por_ip_test.go`, `erro_forcado_sem_banco_test.go`, `prazo_requisicao_test.go`
- Test: `internal/httpapi/middleware/projeto_test.go`, `internal/httpapi/projeto_padrao_integration_test.go`, `internal/httpapi/escopo_internal_test.go`

**Interfaces (Produces):**
```go
// middleware
type ResolvedorDeProjeto interface {
    Scope(ctx context.Context, userID, projectID uuid.UUID) (tenancy.ProjectScope, error)
    DefaultScope(ctx context.Context, userID uuid.UUID) (tenancy.ProjectScope, error)
}
func ProjetoDoCaminho(res ResolvedorDeProjeto) func(http.Handler) http.Handler // r.PathValue("projectID")
func ProjetoPadrao(res ResolvedorDeProjeto) func(http.Handler) http.Handler
func Projeto(ctx context.Context) (tenancy.ProjectScope, bool)

// httpapi
// Deps.Projects *tenancy.Service; Routes entra em pânico se nil, como LimitesIP.
func escopo(w http.ResponseWriter, r *http.Request) (tenancy.ProjectScope, bool)
// sem escopo no contexto: 500 internal_error e log "rota de recurso sem escopo de projeto" (falha fechada)
```
Erros do middleware:
- sem usuário no contexto: 401 `unauthorized`;
- `projectID` que não é UUID e `tenancy.ErrProjectNotFound`: o **mesmo** 404 `not_found`;
- outro erro: 500.

Em `routes.go` surgem as cadeias `recursoLegado := authn(porUsuario(viva(padrao(fn))))` e `noProjeto := authn(porUsuario(viva(doCaminho(fn))))`, mais as variantes `comEnvio`. `/me` e `/sessions` continuam em `protegida`, sem projeto (não garantem projeto). Neste passo, todas as rotas de recurso atuais passam de `protegida`/`comEnvio` para `recursoLegado`/`comEnvioLegado`, e os handlers ainda não mudam.
- [ ] **Step 1: testes que falham**
  - `TestProjetoDoCaminhoMapsBadUUIDAndNonMemberToSame404` (resolvedor falso): o corpo sem `trace_id` é igual nos dois.
  - `TestProjetoDoCaminhoPutsScopeInContext`.
  - `TestProjetoPadraoResolvesDefault`.
  - `TestProjectMiddlewareWithoutUserIs401`.
  - `TestResourceHandlerWithoutProjectScopeFailsClosed`: `escopo()` sem contexto dá 500 e `ok == false`.
  - `TestLegacyRouteEnsuresDefaultProject` (integração): o usuário de `newAPI`, inserido cru e sem projeto, faz login; `GET /api/v1/targets` dá 200; via admin, um projeto padrão com vínculo owner. A segunda chamada continua com 1.
- [ ] **Step 2:** `go test ... ./internal/httpapi/... -run 'TestProjetoDoCaminho|TestProjetoPadrao|TestProjectMiddlewareWithoutUserIs401|TestResourceHandlerWithoutProjectScopeFailsClosed|TestLegacyRouteEnsuresDefaultProject'`. Esperado: `undefined: middleware.ProjetoDoCaminho`.
- [ ] **Step 3: implementar** as assinaturas acima. `main.go` monta `tenancy.NewService(pgstore.New(pool), clk)`. Os testes sem banco passam `tenancy.NewService(repoFalso{}, clk)`.
- [ ] **Step 4:** o comando do Step 2 passa, mais `go test ... ./internal/httpapi/...` (`TestEveryRegisteredRouteDeniesByDefault` intacto).
- [ ] **Step 5:** `feat(httpapi): middleware de escopo de projeto e rotas antigas no Projeto padrão`

### Task 10: alvos por projeto (serviço, queries, handlers e rotas gêmeas)

**Files:**
- Modify: `internal/storage/queries/targets.sql`, `internal/targets/service.go`, `summary.go`, `tags.go`, `internal/httpapi/targethandlers.go`, `internal/httpapi/routes.go`
- Modify: os espelhos de filtro por dono das queries de alvo em `internal/storage` (`filtro_dono_test.go`, `filtro_dono_series_test.go`, `filtro_dono_resumo_test.go`: o filtro passa a separar projeto A de projeto B); os testes do pacote `targets` (helper `escopoDe(t, pool, userID) tenancy.ProjectScope` em `export_test.go`); `internal/httpapi/dono_e_id_internal_test.go`
- Test: `internal/targets/projeto_test.go`, `internal/httpapi/targets_projeto_integration_test.go`

**Interfaces (Produces):**
```go
func (s *Service) Create(ctx context.Context, scope tenancy.ProjectScope, in Input) (Target, error)        // PermEditMonitors
func (s *Service) Get(ctx context.Context, scope tenancy.ProjectScope, id uuid.UUID) (Target, error)       // PermRead
func (s *Service) List(ctx context.Context, scope tenancy.ProjectScope, before *uuid.UUID, limit int32) ([]Target, error)
func (s *Service) Update(ctx context.Context, scope tenancy.ProjectScope, id uuid.UUID, p Patch) (Target, error)
func (s *Service) Delete(ctx context.Context, scope tenancy.ProjectScope, id uuid.UUID) error
func (s *Service) Checks(ctx context.Context, scope tenancy.ProjectScope, id uuid.UUID, from, to time.Time) (Series, error)
func (s *Service) Summary(ctx context.Context, scope tenancy.ProjectScope, filtro SummaryFilter) ([]Summary, error)
```
Todas rodam em `storage.InProjectTx(ctx, s.pool, scope.UserID, scope.ProjectID, func(q, atual) error { if err := atual.Require(perm); ... })`. `tenancy.ErrForbidden` vira 403 `forbidden` em `TargetHandlers.falhou`.

Regra das queries:
- Todo filtro `user_id = ...` de posse passa a `project_id = sqlc.arg(project_id)`: `GetTarget`, `GetTargetForUpdate`, `ListTargets`, `UpdateTarget`, `DeleteTarget`, `ResetTargetHealth`, `Get`/`List`/`DeleteTargetTLSOfOwner`, `ListTargetTags`, `DeleteTargetTags`, `ListChecks`, `ListRawHours`, `ListSummary*`, `ListTargetSummaries`, `LatestUserRollupBucket` (renomeada `LatestProjectRollupBucket`) e `ListUserTargetTags` (renomeada `ListProjectTargetTags`).
- `InsertTarget` ganha `project_id` e grava `user_id = scope.UserID` ("criado por").
- Ficam por `user_id`, até as cotas do titular no PR 5: `CountTargets`, `LockUserTargets`, `ListContentRuleTargetURLs`.

Rotas gêmeas, como literais (a enumeração exige literal): `GET|POST /api/v1/projects/{projectID}/targets`, `GET .../targets/summary`, `GET|PATCH|DELETE .../targets/{id}`, `GET .../targets/{id}/checks`, com `noProjeto`. As antigas usam `escopo()` via `recursoLegado`.
- [ ] **Step 1: testes que falham**
  - `TestTargetsOfOtherProjectOfSameUserAreNotFound`: alvo em B; com o escopo de A, `Get`, `Update` e `Delete` dão `ErrNotFound` e `List` não o traz.
  - `TestViewerReadsButCannotChangeTargets`: `testdb.Membro(t, A, V, "viewer")`; `List` passa; `Create`, `Update` e `Delete` dão `errors.Is(err, tenancy.ErrForbidden)`, e o estado lido via admin fica igual.
  - `TestTargetRoleIsReadInsideTheTransaction`: com `scope.Role = RoleOwner` e o banco dizendo `viewer`, `Create` dá `ErrForbidden`.
  - `TestCreateTargetRecordsProjectAndCreator`: via admin, `project_id = A` e `user_id = U`.
  - `TestProjectTargetRoutesMirrorLegacy` (HTTP): POST em `/projects/{padrão}/targets`; o GET da rota antiga e o da gêmea devolvem o mesmo JSON.
  - `TestTargetOfOtherProjectIsNotFoundOnProjectRoutes` (HTTP, mesmo usuário com dois projetos): `GET /projects/{A}/targets/{idDeB}` dá 404 com corpo igual ao de um id aleatório.
- [ ] **Step 2:** `go test ... ./internal/targets ./internal/httpapi -run 'TestTargetsOfOtherProjectOfSameUserAreNotFound|TestViewerReadsButCannotChangeTargets|TestTargetRoleIsReadInsideTheTransaction|TestCreateTargetRecordsProjectAndCreator|TestProjectTargetRoutesMirrorLegacy|TestTargetOfOtherProjectIsNotFoundOnProjectRoutes'`. Esperado: compilação (`cannot use scope (variable of struct type tenancy.ProjectScope) as uuid.UUID`).
- [ ] **Step 3: implementar** as assinaturas, as queries e as rotas acima; `make generate`.
- [ ] **Step 4:** o comando do Step 2 passa, mais `go test ... ./internal/targets ./internal/storage ./internal/httpapi ./internal/prober` (o sondador não muda) e `sqlc diff`.
- [ ] **Step 5:** `refactor(targets): alvos no escopo de projeto com permissão por papel`

### Task 11: heartbeats por projeto

**Files:**
- Modify: `internal/storage/queries/heartbeats.sql` (só as da API do dono), `internal/heartbeat/service.go`, `internal/httpapi/heartbeathandlers.go`, `routes.go`, os espelhos `filtro_dono_heartbeats_test.go` e os testes do pacote `heartbeat`
- Test: `internal/heartbeat/projeto_test.go`, `internal/httpapi/heartbeats_projeto_integration_test.go`

**Interfaces (Produces):** `Create`, `Clone`, `Get`, `List`, `Update`, `Delete`, `ClearEvents`, `RotateToken`, `PingNow`, `Events`, `Incidents` e `Ack` trocam `userID uuid.UUID` por `scope tenancy.ProjectScope`, com o resto da assinatura igual.
- Exigem `PermEditMonitors`: `Create`, `Clone`, `Update`, `Delete`, `ClearEvents`, `RotateToken`, `PingNow`, `Ack`.
- Exigem `PermRead`: `Get`, `List`, `Events`, `Incidents`.
- `CronPreview` continua sem escopo.
- `InsertHeartbeat` ganha `project_id`. O clone nasce no projeto do escopo.
- Ficam por `user_id`: `CountHeartbeats`, `LockUserHeartbeats`.
- Não mudam: as queries do ping (`LockHeartbeatForPing`, `SaveHeartbeatPing`) e do avaliador (`ClaimHeartbeatsTo*`).
- Rotas gêmeas: as 13 de `/api/v1/heartbeats...` sob `/api/v1/projects/{projectID}/heartbeats...`.
- [ ] **Step 1: testes que falham**
  - `TestHeartbeatsOfOtherProjectOfSameUserAreNotFound`: `Get`, `RotateToken`, `PingNow` e `Ack` com o id de B dão `ErrNotFound`.
  - `TestViewerCannotRotateOrPingHeartbeat`: `ErrForbidden`, e o `ping_token_hash` lido via admin não muda.
  - `TestCloneStaysInScopeProject`: o clone tem `project_id = A`.
  - `TestPingTokenStillWorksAfterProjectMove` (HTTP): heartbeat criado pela rota gêmea; `POST /ping/{token}` dá 200.
- [ ] **Step 2:** `go test ... ./internal/heartbeat ./internal/httpapi -run 'TestHeartbeatsOfOtherProjectOfSameUserAreNotFound|TestViewerCannotRotateOrPingHeartbeat|TestCloneStaysInScopeProject|TestPingTokenStillWorksAfterProjectMove'`. Esperado: compilação.
- [ ] **Step 3: implementar**; `make generate`.
- [ ] **Step 4:** o comando do Step 2 passa, mais `go test ... ./internal/heartbeat ./internal/storage ./internal/httpapi ./internal/prober`.
- [ ] **Step 5:** `refactor(heartbeat): heartbeats no escopo de projeto com permissão por papel`

### Task 12: canais por projeto

**Files:**
- Modify: `internal/storage/queries/channels.sql`, `internal/notify/service.go` (`ChannelService`), `internal/httpapi/channelhandlers.go`, `routes.go`, os espelhos `filtro_dono_canais_test.go` e os testes de `notify`
- Test: `internal/notify/projeto_test.go`, `internal/httpapi/channels_projeto_integration_test.go`

**Interfaces (Produces):** os métodos do `ChannelService` que hoje recebem `userID` (`Create`, `List`, `Delete`, `SetEnabled`, `Resend`) passam a `scope tenancy.ProjectScope`.
- Exigem `PermManageChannels`: `Create`, `Delete`, `SetEnabled`, `Resend`. `List` exige `PermRead`.
- `InsertChannel` ganha `project_id`.
- Ficam por `user_id`: `CountChannels`, `LockUser`. O verify (`FindChannelByVerification`, `ConfirmChannel`, `InVerifyTx`) não muda.
- O balde de envio por usuário (`porEnvio`) continua. Gêmeas com `comEnvioNoProjeto` para `POST` e `.../verify/resend`.
- [ ] **Step 1: testes que falham**
  - `TestChannelsOfOtherProjectOfSameUserAreNotFound`.
  - `TestMemberCannotManageChannels`: o papel `member` dá `ErrForbidden` no `Create`, e nenhum e-mail sai (`carteiroFalso` vazio).
  - `TestVerifyLinkStillConfirmsProjectChannel` (HTTP): canal criado pela gêmea; `POST /verify/{token}` confirma.
- [ ] **Step 2:** `go test ... ./internal/notify ./internal/httpapi -run 'TestChannelsOfOtherProjectOfSameUserAreNotFound|TestMemberCannotManageChannels|TestVerifyLinkStillConfirmsProjectChannel'`. Esperado: compilação.
- [ ] **Step 3: implementar**; `make generate`.
- [ ] **Step 4:** o comando do Step 2 passa, mais `go test ... ./internal/notify ./internal/storage ./internal/httpapi`.
- [ ] **Step 5:** `refactor(notify): canais no escopo de projeto com permissão por papel`

### Task 13: status page por projeto

**Files:**
- Modify: `internal/storage/queries/statuspages.sql` (só as do dono), `internal/statuspage/service.go`, `internal/httpapi/statuspagehandlers.go`, `routes.go`, `filtro_dono_paginas_test.go` e os testes de `statuspage`
- Test: `internal/statuspage/projeto_test.go`

**Interfaces (Produces):**
```go
func (s *Service) Get(ctx context.Context, scope tenancy.ProjectScope) (Page, error)                       // PermRead
func (s *Service) Create(ctx context.Context, scope tenancy.ProjectScope, in Input) (Page, error)          // PermManageStatusPage
func (s *Service) Update(ctx context.Context, scope tenancy.ProjectScope, id uuid.UUID, patch Patch) (Page, error)
```
- Passam a filtrar por `project_id`: `GetStatusPage`, `LockStatusPage`, `OwnTargetIDs`, `UpdateStatusPage`, `ListStatusPageItems`, `DeleteStatusPageItems`.
- `InsertStatusPage` ganha `project_id`.
- As queries públicas (`GetPublicPage`, `ListPublic*`) não mudam.
- Gêmeas: `GET|POST /api/v1/projects/{projectID}/status-pages` e `PATCH .../status-pages/{id}`.
- [ ] **Step 1: testes que falham**
  - `TestStatusPageRefusesTargetOfOtherProject`: com o alvo de B nos itens, o `Update` no escopo de A dá o erro de validação do item; via admin, `status_page_items` não ganha a linha.
  - `TestMemberCannotEditStatusPage`: `ErrForbidden`.
  - `TestPublicPageUnchangedAfterProjectScope`: o `GET /p/{slug}` dá o mesmo corpo antes e depois do PATCH pela gêmea, que não muda nada.
- [ ] **Step 2:** `go test ... ./internal/statuspage ./internal/httpapi -run 'TestStatusPageRefusesTargetOfOtherProject|TestMemberCannotEditStatusPage|TestPublicPageUnchangedAfterProjectScope'`. Esperado: compilação.
- [ ] **Step 3: implementar**; `make generate`.
- [ ] **Step 4:** o comando do Step 2 passa, mais `go test ... ./internal/statuspage ./internal/storage ./internal/httpapi`.
- [ ] **Step 5:** `refactor(statuspage): status page no escopo de projeto com permissão por papel`

### Task 14: histórico de incidentes por projeto

**Files:**
- Modify: `internal/storage/queries/incidents.sql` (`ListIncidents`, `AckIncident`: `t.project_id = sqlc.arg(project_id)`), `internal/incident/history/history.go`, `internal/httpapi/incidenthandlers.go`, `routes.go`, `filtro_dono_incidentes_test.go`
- Test: `internal/incident/history/projeto_test.go`

**Interfaces (Produces):**
```go
func (s *Service) List(ctx context.Context, scope tenancy.ProjectScope, f Filter) ([]Incident, error) // PermRead
func (s *Service) Ack(ctx context.Context, scope tenancy.ProjectScope, id uuid.UUID) (Incident, error)   // PermEditMonitors
```
Gêmeas: `GET /api/v1/projects/{projectID}/incidents` e `POST .../incidents/{id}/ack`. O fan-out (`ListDeliverableChannels`) **não** muda (ver os portões).
- [ ] **Step 1: testes que falham**
  - `TestIncidentsOfOtherProjectOfSameUserAreHidden`: `List(A)` não traz o incidente de B; `Ack(A, idDeB)` dá `ErrNotFound`.
  - `TestViewerCannotAckIncident`: `ErrForbidden`, e `ack_at` continua nulo.
- [ ] **Step 2:** `go test ... ./internal/incident/history -run 'TestIncidentsOfOtherProjectOfSameUserAreHidden|TestViewerCannotAckIncident'`. Esperado: compilação.
- [ ] **Step 3: implementar**; `make generate`.
- [ ] **Step 4:** o comando do Step 2 passa, mais `go test ... ./internal/incident/... ./internal/storage ./internal/httpapi`.
- [ ] **Step 5:** `refactor(incident): histórico de incidentes no escopo de projeto`

### Task 15: `GET /api/v1/projects`, paridade de rotas e varredura de isolamento HTTP

**Files:**
- Create: `internal/httpapi/projecthandlers.go`
- Modify: `internal/httpapi/routes.go`
- Test: `internal/httpapi/projetos_integration_test.go`, `internal/httpapi/rotas_projeto_paridade_test.go`

**Interfaces (Produces):**
```go
type ProjectHandlers struct{ svc *tenancy.Service }
func NewProjectHandlers(svc *tenancy.Service) *ProjectHandlers
func (h *ProjectHandlers) List(w http.ResponseWriter, r *http.Request)
// GET /api/v1/projects -> 200 {"items":[{"id","name","is_default","role","created_at"}]}, no envelope de lista de GET /api/v1/channels
```
Registrada com `protegida`: lista e não garante o padrão.
- [ ] **Step 1: testes que falham**
  - `TestEveryLegacyResourceRouteHasAProjectTwin`: com `rotasRegistradas(t)`, para cada rota `METODO /api/v1/{targets|incidents|heartbeats|channels|status-pages}...` existe `METODO /api/v1/projects/{projectID}/...` com o mesmo resto, e vice-versa.
  - `TestListProjectsReturnsOwnProjectsWithRole`: U, owner do padrão e viewer de B, recebe 2 itens nessa ordem; o projeto de V não aparece.
  - `TestAnotherUsersProjectIsIndistinguishableFromMissing`: para cada rota gêmea com o `projectID` do dono e o token do intruso, o status é 404 e `semTrace(corpo) == semTrace(corpo com uuid.NewString())`. `conferirSemDadosDoDono` passa, e os GETs do dono ficam byte a byte iguais (molde de `conferirIDOR`; `esperarVagaNoLimite` entre as chamadas).
  - `TestViewerGetsForbiddenOnEveryMutatingProjectRoute`: com `testdb.Membro(t, projetoDoDono, intruso, "viewer")`, toda gêmea POST/PATCH/DELETE dá 403 `forbidden` e as leituras do dono não mudam. As gêmeas de leitura dão 200.
- [ ] **Step 2:** `go test ... ./internal/httpapi -run 'TestEveryLegacyResourceRouteHasAProjectTwin|TestListProjectsReturnsOwnProjectsWithRole|TestAnotherUsersProjectIsIndistinguishableFromMissing|TestViewerGetsForbiddenOnEveryMutatingProjectRoute|TestEveryRegisteredRouteDeniesByDefault'`. Esperado: `GET /api/v1/projects` dá 404 (rota inexistente).
- [ ] **Step 3: implementar** o handler e o registro `mux.Handle("GET /api/v1/projects", protegida(ph.List))`, com `Deps.Projects` repassado.
- [ ] **Step 4:** o comando do Step 2 passa, mais `go test -race -count=1 -p 1 ./...` (o `make test` inteiro) e `sqlc diff`.
- [ ] **Step 5:** `feat(httpapi): lista de projetos e prova de paridade e isolamento das rotas por projeto`

### Critical Files for Implementation
- /Users/01bipo/Documents/Projetos/Go/StillOn/internal/storage/scope.go
- /Users/01bipo/Documents/Projetos/Go/StillOn/internal/storage/migrations/00004_targets_checks.sql (molde de RLS/NULLIF) e as novas 00039–00042 no mesmo diretório
- /Users/01bipo/Documents/Projetos/Go/StillOn/internal/storage/rls_cobertura_test.go (varredura de tabelas reaproveitada na Task 6)
- /Users/01bipo/Documents/Projetos/Go/StillOn/internal/httpapi/routes.go
- /Users/01bipo/Documents/Projetos/Go/StillOn/internal/account/account.go

