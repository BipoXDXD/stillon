# Onda 1 de pendências (22/11) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** fechar quatro pendências pequenas e verificadas no código, uma por PR, sem decisão do dono.

**Architecture:** tarefas independentes em arquivos disjuntos; subagentes só editam arquivos e rodam testes; branch, commit, PR, revisão e merge ficam com a sessão principal.

**Tech Stack:** Go 1.26.11 (`GOTOOLCHAIN=go1.26.11`), pgx/pgxpool, testcontainers (Postgres), GitHub Actions.

**Spec:** inventário de 22/11 (auditoria de 20/11 + #411 + revisão do #445). Itens fora desta onda: ver "Fora da onda".

## Global Constraints

- Uma tarefa = um PR, commit `tipo(escopo): descrição em português`; nunca `[skip ci]` com código.
- TDD: o teste novo precisa falhar com o código atual antes da correção (mostrar a saída).
- Pasta única `/Users/01bipo/Documents/Projetos/Go/StillOn`, sem worktree; subagentes **não** fazem git (add/commit/switch/stash).
- Docker: um teste com Postgres por vez na máquina; `-race -count=1 -p 1`; nada de `prune`.
- Sem enfraquecer assertion nem pular teste; `gofmt`, `go vet` limpos.
- Merge só com revisão independente aprovada no SHA final e todos os checks verdes; fora de 00:00–01:00 e 03:10–03:45 BRT; conferir `Coolify release` + `readyz`.

## Review Focus

- Contador de tentativas em caminho de erro: nenhuma reserva perdida nem zerada indevidamente (Task 1).
- Pool pequeno (`MaxConns` baixo) sob a trava de sessões: nenhuma operação de auth pega segunda conexão dentro de uma tx (Task 2).
- Cliente que desiste (`context.Canceled`) não pode virar ERROR nem esconder um erro real quando o contexto segue vivo (Task 3).
- Ensaio de restauro: a política de idade não pode reprovar backup legítimo por fuso/horário do agendamento (Task 4).

---

### Task 1: lacunas de teste do #445 (depende do merge do #445)

**Files:**
- Test: `internal/httpapi/totp_falha_interna_internal_test.go` (dublê `contasDoDuble`)
- Test: `internal/httpapi/auth_lacunas_integration_test.go:~130-160`

- [ ] **Step 1:** teste interno `TestIssueThatFailsRefundsTheAttempt`: `h.issue` com `sessions` cujo Issue devolve erro genérico; esperado 500, `devolucoes == 1`, `zeradas == 0` no `contasDoDuble`.
- [ ] **Step 2:** no teste do `!consumido` (`auth_lacunas…:~158`), semear 2 falhas antes (`errarSenha(t, a, 2)`) e exigir `failed_attempts == 2` no fim (Refund), em vez de 0.
- [ ] **Step 3:** sabotar (`Reset` no lugar de `Refund` nos dois ramos) → ambos falham; desfazer.
- [ ] **Step 4:** `go test -race -count=1 -p 1 ./internal/httpapi -run 'TestIssueThatFailsRefundsTheAttempt|<nome do teste do !consumido>'` → PASS.
- [ ] **Step 5:** commit `test(auth): cobre a devolução da tentativa quando a emissão falha`.

### Task 2: `TOTPService.Confirm` classifica a recusa dentro da tx

**Files:**
- Modify: `internal/auth/totp.go:~188-198` (ramo `tag.RowsAffected() == 0`)
- Test: `internal/auth/` (arquivo de teste de TOTP existente; padrão de pool próprio em `corridas_test.go:169-175`)

- [ ] **Step 1:** teste `TestConfirmAlreadyEnabledWithASingleConnection`: `pgxpool` com `MaxConns = 1` (via `pgxpool.ParseConfig(testdb.DSN(t))`), usuário com 2FA já confirmado, `Confirm` com `context.WithTimeout(5s)`; esperado `ErrTOTPAlreadyEnabled` sem estourar o prazo. Repetir para segredo pendente apagado → `ErrTOTPNotEnrolled`.
- [ ] **Step 2:** rodar → FAIL (prazo vencido: `IsEnabled` espera conexão do pool).
- [ ] **Step 3:** trocar `s.IsEnabled(ctx, userID)` por leitura na mesma tx (`SELECT totp_confirmed_at IS NOT NULL FROM users WHERE id = $1` via `tx`), erro envelopado `fmt.Errorf("classificar confirmação TOTP: %w", err)`.
- [ ] **Step 4:** `go test -race -count=1 -p 1 ./internal/auth -run 'TestConfirm'` → PASS.
- [ ] **Step 5:** commit `fix(auth): classifica o Confirm recusado na mesma transação`.

### Task 3: cliente que desiste não vira ERROR

**Files:**
- Modify: `internal/httpapi/erros.go:15-18` (`erroInterno`)
- Test: `internal/httpapi/erros_internal_test.go` (novo, sem banco)

- [ ] **Step 1:** teste table-driven `TestErroInternoLogLevel` com handler `slog` de captura: (a) `r.Context()` cancelado e `err` envolvendo `context.Canceled` → nível INFO, mensagem com `op`, sem ERROR; (b) contexto vivo e `err` qualquer → ERROR (inalterado); (c) contexto vivo mas `err` com `context.Canceled` vindo de outra fonte → ERROR. Resposta segue o envelope de `apierr.WriteError(CodeInternal)` nos três.
- [ ] **Step 2:** rodar → FAIL em (a).
- [ ] **Step 3:** em `erroInterno`, se `errors.Is(err, context.Canceled) && errors.Is(r.Context().Err(), context.Canceled)` → `slog.Info(op+": cliente desistiu", "trace_id", …)`; senão o `slog.Error` atual.
- [ ] **Step 4:** `go test -race -count=1 ./internal/httpapi -run TestErroInternoLogLevel` → PASS (sem Docker).
- [ ] **Step 5:** commit `fix(httpapi): registra a desistência do cliente como info`.

### Task 4: diagnóstico da #443 (ensaio de restauro de 21/11)

**Files (leitura primeiro):** `.github/workflows/backup.yml`, `deploy/backup/`, run `44958672274` (`gh run view 44958672274 --log-failed`).

- [ ] **Step 1:** achar no log a causa exata de `backup_age_outside_policy` (idade medida, limite, horário do backup guardado vs. horário do ensaio) e comparar com o ensaio de 22/11 que passou.
- [ ] **Step 2:** classificar: (a) defeito de política/código → seguir TDD com teste em `deploy/ci` ou `deploy/backup` que reproduz a idade do caso real, corrigir, commit `fix(backup): …`; (b) falha transitória do agendamento (backup atrasado) → relatório curto para a sessão principal fechar a #443 com a evidência.
- [ ] **Step 3:** nunca operar na VPS nem disparar workflow de produção.

## Execução e paralelismo

- Agora (sem depender do #445): Tasks 2, 3 e 4 em paralelo (arquivos disjuntos). Docker só na Task 2.
- Depois do merge do #445: Task 1 (Docker, após a Task 2 liberar).
- Sessão principal, em série: branch a partir da `main` → commit só dos arquivos da tarefa → PR → revisão independente (Opus · High) no SHA → CI verde → merge → release. Um merge por vez.

## Fora da onda (motivo)

- Itens com decisão do dono (D6, D7, D8, D10, D13, F3, Discord, ruleset, Picomatch, segredos `VPS_*`, `stash@{0}`).
- SP2 PRs 2–6b e SP1 PRs 4, 7–10: G e sequenciais, sem plano escrito para SP1; planejar à parte.
- Backlog de refatoração (grupo 2 em série, N6 antes): próxima onda, plano próprio.
- `catch` de `sessao.ts:186` (mapeia para `offline`, intencional), `semSerVista` negativo (sem efeito real), stderr do `pg_dump` (pode ser proposital: precisa decidir redação antes), `CORS_ORIGIN` (validar sem saber o valor de produção pode derrubar o boot), `npm audit` no CI (pode avermelhar o CI inteiro: decidir política antes).
