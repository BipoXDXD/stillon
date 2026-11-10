# SP1, PR 6: leitura e exibição da validade do certificado — plano de implementação

> **Para agentes:** SUB-SKILL OBRIGATÓRIA: use superpowers:subagent-driven-development (recomendado) ou
> superpowers:executing-plans, tarefa por tarefa. Os passos usam `- [ ]`.

**Objetivo:** a sonda observa o certificado do host cadastrado, guarda em `target_tls` o pior certificado visto (com a
regra de substituição da §5.2) e o detalhe do alvo mostra "Certificado válido até …, em N dias". Nenhum aviso: o aviso e a
entrega são do PR 7.

**Arquitetura:** abordagem A da spec, sem conexão nova: `Probe` anexa um `httptrace.ClientTrace` cujo `TLSHandshakeDone`
guarda, atrás de mutex, os handshakes verificados; uma função pura escolhe o do host cadastrado e calcula a validade
efetiva pelas `VerifiedChains`. `gravar` atualiza `target_tls`, na mesma transação do check, por outra função pura
(`substituirValidade`). A migration "+3 `target_tls`" cria a tabela com RLS que nomeia dono ou sondador e a coluna
`targets.tls_warn`.

**Stack:** Go 1.26.10, pgx v5, sqlc, goose, Postgres 18 (17 na matriz do CI) via testcontainers; React + Zod + Vitest.

**Spec:** `/Users/01bipo/Documents/Claude/stillon-continuidade/repo/.claude/worktrees/sp1-piso/docs/superpowers/specs/2026-11-09-sp1-heartbeats-tls-conteudo.md`,
seções 2, 4 (abordagem A), 5.2 itens 1 a 4 e 8 (item 6 só na parte do painel), 6 (linha +3 e RLS de `target_tls`), 7.2
(`tls_warn`, `tls`), 8 ("Alvo: detalhe"), 9 (TLS, TLS em balanceador, RLS, ON DELETE, Acesso, Entrada, Saída, Painel e o
parágrafo da CA de teste), 10 (T3, I8), 11 (linha TLS) e 12 (PR 6). Plano anterior: `2026-11-10-sp1-pr5-conteudo.md` (mesma
pasta); este PR depende dele (mesmos `Result`, `Spec` e `ClaimDueTargets`).

**Decisões do dono:** estágios 14/7/3 (do PR 7; nada aqui depende deles); SP1 antes da SP2; migration "próximo livre no merge".

## Rulings deste plano

- **`tls_alerts` nasce no PR 7, na migration +4, e não na +3.** Neste PR nada a escreve nem a lê: criá-la aqui seria uma
  tabela morta com RLS e testes próprios entre dois deploys. Na +4 ela fica junto do arco de `notifications` que a
  referencia, da retenção e da descida que a remove (a nota da §6, "Roda depois da descida da +4", deixa de ser necessária).
  Custo: a linha +3 da §6 perde `tls_alerts` e a +4 a ganha; o plano do PR 7 precisa dizer isso.
- **`tls_warn` entra na coluna e na API, não no painel.** Um interruptor de aviso que não avisa nada até o PR 7 enganaria o
  dono; o controle no formulário é do PR 7.
- **CA de teste sem mudar o `safedial`.** O padrão já existe em `probe_test.go` (o teste de HTTP/2 troca o
  `TLSClientConfig` do transporte devolvido por `NewClientAllowing`). Um `export_test.go` no `safedial` não seria visível ao
  pacote `prober`. Os helpers ficam em `internal/prober/export_tls_test.go`; nenhum código de produção ganha caminho para
  raízes próprias ou `InsecureSkipVerify`, e um teste do `safedial` trava isso.
- **Vários handshakes com o host cadastrado** (redirect para outra porta do mesmo host, `http` → `https` no mesmo host):
  vale a **menor** validade efetiva entre eles.
- **A leitura independe do status HTTP:** um alvo down por `http_status` com certificado verificado grava a validade. Sonda
  descartada (sonda velha, alvo pausado, alvo sumido, shutdown) não grava nada, porque a transação de `gravar` não chega lá.
- **Bordas estritas:** `observed_at` renova com leitura igual só se passou **mais** de 1 h; leitura maior substitui só se a
  guardada ficou sem ser vista por **mais** de `max(6 h, 3 × intervalo)`.
- **Serialização:** `guardarValidade` roda depois de `avaliar`, que segura `target_health` com `FOR UPDATE` até o commit.
  `LockTargetProbeSpec` é `FOR KEY SHARE` e não serializa duas sondas do mesmo alvo; a trava da histerese, sim.
- **Troca de destino esquece o certificado:** o PATCH em que `mudouDeDestino(antes, depois)` é verdadeiro apaga a linha de
  `target_tls` na mesma transação. Sem isso, o pior certificado do host antigo ficaria na linha até a janela (6 h ou mais) e
  o PR 7 avisaria sobre o host errado.
- **Dias restantes no painel:** `ceil((not_after - agora) / 24 h)` em TypeScript, com `agora` = `dataUpdatedAt` da consulta
  do alvo (como `TopoDoDetalhe` faz com o resumo). A API não ganha `days_left` (fora da §7.2). A função Go do PR 7 usa a
  mesma tabela de valores literais da Task 7.
- **No detalhe, uma linha abaixo dos quatro indicadores**, não um quinto cartão (a grade é de 4 colunas). Item de design
  para o dono revisar depois do PR, sem bloquear.

## Restrições globais

- Migration: `internal/storage/migrations/000NN_target_tls.sql`, `000NN` = **próximo número livre na hora do merge**; a
  `00015` é vaga; testes acham a versão por `versaoDaMigration(t, "target_tls")`.
- `target_tls(target_id uuid PRIMARY KEY REFERENCES targets(id) ON DELETE CASCADE, not_after timestamptz NOT NULL,
  observed_at timestamptz NOT NULL)`; `targets.tls_warn bool NOT NULL DEFAULT true`. ENABLE + FORCE + política que nomeia
  dono ou sondador, com `NULLIF` no GUC; nunca "o alvo é visível".
- Handshake válido: `ServerName` sem ponto final igual ao host cadastrado canonicalizado por `safedial.DialedHost` e sem
  ponto final. IP literal ou URL `http` sem salto `https` para o mesmo host: sem leitura. `DialedHost` não muda.
- Validade efetiva: o maior, entre as `VerifiedChains`, do menor `NotAfter` de cada cadeia. Handshake com erro ou sem
  `VerifiedChains`: ignorado.
- Substituição: menor substitui na hora; igual renova `observed_at` só se ele tem mais de 1 h; maior substitui só depois de
  `max(6 h, 3 × interval_sec)` sem ser vista.
- Saída da API: `tls_warn` (bool) e `tls: {"not_after", "observed_at"} | null`; entrada: `tls_warn` (POST opcional, padrão
  `true`; PATCH `optional[bool]`, null é 400). `tls` é só de leitura (campo desconhecido na entrada → 400).
- Alheio ou inexistente: 404 igual, corpo sem dado do dono, alvo intacto.
- Merge depois do PR 5 publicado; merge é deploy: conferir o run do `Coolify release` deste SHA antes de anunciar.
- `go build ./... && go vet ./... && make test` antes de dizer que funciona; teste novo só vale visto falhar com a
  sabotagem indicada, registrada no corpo do PR. Valor esperado é literal.
- Comandos pesados sob `flock -o /Users/01bipo/.claude/docker-suite.lock`, um por vez; com bateria abaixo de 50%, só o CI.

## Foco de revisão

1. Alvo pausado por dias com o certificado renovado nesse tempo: na volta, a leitura maior substitui na primeira sonda (a
   guardada não é vista há mais que a janela). Teste na Task 2 (puro) e na Task 5.
2. PATCH da URL para outro host: a linha some, o detalhe volta a "Aguardando a primeira sonda", e nenhuma sonda velha em
   voo a recria (a cerca descarta). Teste na Task 6.
3. Certificado que vence em 5 h: "em 1 dia", nunca "em 0 dias"; leitura guardada já vencida (alvo pausado): "Certificado
   venceu em …". Teste na Task 7.
4. Alvo com intervalo de 86400 s: janela de 72 h; leitura maior em 72 h não substitui, em 72 h + 1 s substitui. Teste na
   Task 2.
5. Alvo down por `http_status` (500) atrás de certificado válido: a validade é gravada. Teste na Task 5.

---

### Task 1: escolher o handshake e calcular a validade efetiva

**Files:**
- Create: `internal/prober/certificado.go`, `internal/prober/certificado_test.go`

**Interfaces:**
- Produz:
  `func hostDoCertificado(rawURL string) (string, bool)` — host por `DialedHost`, sem ponto final; `false` para IP literal
  (com ou sem colchetes) e para host que o IDNA recusa;
  `func validadeEfetiva(cadeias [][]*x509.Certificate) time.Time`;
  `func validadeDoHost(host string, estados []tls.ConnectionState) (time.Time, bool)` — menor validade efetiva entre os
  estados com `ServerName` (sem ponto final) igual a `host` e `VerifiedChains` não vazio.

- [ ] **Step 1: testes que falham** (puros, certificados `x509.Certificate{NotAfter: ...}` montados no teste, sem rede;
  instantes literais, `t0 = 2026-11-10T00:00:00Z`):
  - `TestValidadeEfetiva`: uma cadeia folha `t0+90d`, intermediário `t0+30d`, raiz `t0+3650d` → `t0+30d` (intermediário
    vence antes); duas cadeias, uma com intermediário `t0+30d` e outra com `t0+60d`, folha `t0+90d` → `t0+60d` (a cadeia
    alternativa válida vale); cadeia só com a folha → a folha.
  - `TestHostDoCertificado`: `https://Exemplo.COM/x` → `exemplo.com`; `https://exemplo.com./` → `exemplo.com`;
    `https://ação.com.br/` → `xn--ao-hia9b.com.br` (conferir o literal com `idna.Lookup` uma vez e fixar);
    `https://203.0.113.5/`, `https://[2001:db8::1]/` → `false`.
  - `TestValidadeDoHost`: estado com `ServerName "exemplo.com"` e cadeia `t0+30d` + estado de `"outro.com"` com `t0+5d` →
    `t0+30d` (redirect para terceiro não conta); `ServerName "exemplo.com."` → casa; `ServerName ""` (IP) → `false`; dois
    estados de `exemplo.com` com `t0+30d` e `t0+20d` → `t0+20d`; estado sem `VerifiedChains` → `false` (T3).
- [ ] **Step 2:** `go test -run 'ValidadeEfetiva|HostDoCertificado|ValidadeDoHost' ./internal/prober` → FAIL.
- [ ] **Step 3: implementar.**
- [ ] **Step 4:** PASS. Sabotagens: usar só `cadeia[0].NotAfter` → o caso do intermediário quebra; usar o menor entre as
  cadeias → o caso das duas cadeias quebra; comparar sem tirar o ponto final → o caso `exemplo.com.` quebra.
- [ ] **Step 5:** commit `feat(prober): validade efetiva do certificado do host cadastrado`.

### Task 2: `substituirValidade`, a regra do pior certificado

**Files:**
- Modify: `internal/prober/certificado.go`; Test: `internal/prober/certificado_test.go`

**Interfaces:**
- Produz: `type validadeGuardada struct { NotAfter, ObservedAt time.Time }`;
  `func substituirValidade(guardada *validadeGuardada, lida, em time.Time, intervalo time.Duration) (validadeGuardada, bool)`
  (`bool` = gravar); `const renovarVistoApos = time.Hour`, `janelaMinimaDeTroca = 6 * time.Hour`, `fatorDaJanelaDeTroca = 3`.

- [ ] **Step 1: `TestSubstituirValidade`** (tabela, `A = 2026-12-01T00:00:00Z`, `B = 2027-02-01T00:00:00Z`,
  `t = 2026-11-10T12:00:00Z`, intervalo 60 s salvo onde dito):
  - sem guardada, lida A → `{A, t}`, gravar;
  - guardada `{B, t-1min}`, lida A → `{A, t}`, gravar (menor substitui na hora);
  - guardada `{A, t-1h}`, lida A → não grava; `{A, t-1h-1s}`, lida A → `{A, t}`, gravar;
  - guardada `{A, t-6h}`, lida B → não grava; `{A, t-6h-1s}`, lida B → `{B, t}`, gravar;
  - intervalo 10800 s (janela 9 h): `{A, t-6h-1s}`, lida B → não grava; `{A, t-9h-1s}` → grava;
  - intervalo 86400 s (janela 72 h): `{A, t-72h}` → não grava; `{A, t-72h-1s}` → grava;
  - alternância de balanceador: guardada `{A, t-10min}`, lidas B, A, B, A em `t, t+1min, t+2min, t+3min` → nenhuma grava.
- [ ] **Step 2:** `go test -run SubstituirValidade ./internal/prober` → FAIL.
- [ ] **Step 3: implementar.**
- [ ] **Step 4:** PASS. Sabotagens: devolver sempre a lida → o caso B na janela e a alternância quebram; trocar `>` por `>=`
  na janela → o caso `t-6h` quebra.
- [ ] **Step 5:** commit `feat(prober): regra de substituição da validade guardada`.

### Task 3: migration +3 `target_tls`

**Files:**
- Create: `internal/storage/migrations/000NN_target_tls.sql`, `internal/storage/target_tls_test.go`

**Interfaces:**
- Produz: tabela `target_tls`; política `target_tls_owner_or_prober`; coluna `targets.tls_warn`.

- [ ] **Step 1: testes que falham.**
  - `TestTargetTLSFollowsOwnerOrProber`: dono A lê a linha do seu alvo; dono B lê 0 linhas e o UPDATE de B afeta 0; o
    sondador (`InProberTx`) lê e grava a linha de qualquer alvo; sem escopo nenhum, 0 linhas.
  - `TestPublicScopeCannotReadTargetTLS` (I8): alvo de A numa página publicada; `InPublicTx` com o slug lê o alvo (controle)
    e 0 linhas de `target_tls`.
  - `TestTargetTLSGoesWithTheTargetAndTheUser` (ON DELETE): apagar o alvo leva a linha; apagar o usuário leva a de outro alvo.
  - `TestTargetTLSDownAndUp`: `Migrate`, uma linha, `MigrateDownTo(versão - 1)` → tabela e `targets.tls_warn` somem;
    `MigrateUpTo(versão)` → sem erro, `tls_warn` `true` nos alvos existentes.
  - `TestFloorBinaryBootsOnTargetTLS`: `migrationsAte(t, "<arquivo anterior à +3>")` sobre o schema novo → `Up` aplica 0.
  - `TestEveryTableForcesRLSWithAPolicy` já cobre a tabela nova sem mudança.
- [ ] **Step 2:** `flock -o ... go test -run 'TargetTLS|PublicScopeCannotReadTargetTLS|FloorBinaryBootsOnTargetTLS|EveryTableForcesRLS' ./internal/storage` → FAIL.
- [ ] **Step 3: escrever a migration.** Up: `ALTER TABLE targets ADD COLUMN tls_warn bool NOT NULL DEFAULT true`; a tabela;
  ENABLE e FORCE; a política, no molde de `incidents_via_heartbeat`:

  ```sql
  CREATE POLICY target_tls_owner_or_prober ON target_tls
      USING (EXISTS (SELECT 1 FROM targets t WHERE t.id = target_tls.target_id
                        AND (t.user_id = NULLIF(current_setting('app.user_id', true), '')::uuid
                             OR current_setting('app.scope', true) = 'prober')))
      WITH CHECK (<a mesma expressão>);
  ```

  Comentário: por que não delega só ao alvo (`targets_public_select` abriria a tabela à status page). Down: `DROP TABLE
  target_tls`; `ALTER TABLE targets DROP COLUMN tls_warn` (sem dado a converter).
- [ ] **Step 4:** PASS. Sabotagens: trocar a política por `EXISTS (SELECT 1 FROM targets t WHERE t.id = target_tls.target_id)`
  → I8 quebra; tirar o `FORCE` → o teste de cobertura quebra; tirar o `CASCADE` → o teste de ON DELETE quebra (23503).
- [ ] **Step 5:** commit `feat(storage): target_tls com RLS de dono ou sondador`.

### Task 4: `Probe` observa os handshakes

**Files:**
- Modify: `internal/prober/probe.go` (`Result.CertNotAfter`, trace no contexto), `internal/prober/certificado.go`
  (`coletorDeHandshakes`)
- Create: `internal/prober/export_tls_test.go` (helpers), `internal/prober/certificado_probe_test.go`
- Modify: `internal/prober/safedial/transport_test.go`; `internal/prober/probe_test.go` (`TestResultHasNoRoomForTheBody`)

**Interfaces:**
- Consome: `hostDoCertificado`, `validadeDoHost` (Task 1).
- Produz: `Result.CertNotAfter time.Time` (zero = sem leitura); `func coletorDeHandshakes() (*httptrace.ClientTrace, func()
  []tls.ConnectionState)` (mutex; só guarda handshake com `err == nil`).
- Helpers de teste: `cadeiaDeTeste(t, host string, folhaAte, intermediarioAte time.Time) (raiz *x509.Certificate, cert
  tls.Certificate)` (folha com SAN `host`, servida com o intermediário); `clienteComRaiz(t, raiz *x509.Certificate, liberados
  ...netip.AddrPort) *http.Client` (`NewClientAllowing` + `TLSClientConfig{RootCAs}` no transporte, como em
  `TestProbeHTTP2ResponseHeadersAboveTheCapAreDown`); `resolverDeTeste(t, ip string)` (troca o `net.DefaultResolver`, cópia
  do `instalarResolver` de `safedial/dns_interno_test.go`, sem `t.Parallel`).

- [ ] **Step 1: testes que falham** (servidores `httptest.NewUnstartedServer` com `TLS.Certificates` da cadeia de teste,
  nomes `sonda.exemplo.test` e `outro.exemplo.test` resolvidos para 127.0.0.1; `folhaAte = 2027-01-01T00:00:00Z`,
  `intermediarioAte = 2026-12-15T00:00:00Z`):
  - `TestProbeReadsTheRegisteredHostCertificate`: `https://sonda.exemplo.test:<p>/` → up e `CertNotAfter` = `2026-12-15`
    (o intermediário).
  - `TestProbeIgnoresTheCertificateOfARedirectTarget`: 302 de `sonda` para `outro` (folha `2026-11-20`) → `2026-12-15`.
  - `TestProbeOverHTTPHasNoCertificate`: `http://sonda...` sem salto → zero; `http://sonda...` → 302 →
    `https://sonda...` → `2026-12-15`.
  - `TestProbeOnIPLiteralHasNoCertificate`: `https://127.0.0.1:<p>/` com SAN de IP → up e zero.
  - `TestProbeAcceptsTheTrailingDot`: `https://sonda.exemplo.test.:<p>/` → `2026-12-15`.
  - `TestTLSHandshakeObservedOverHTTP2`: servidor com `EnableHTTP2` → o handler vê `ProtoMajor == 2` e a validade é lida.
  - `TestUnverifiedCertificateRecordsNothing` (T3): raiz do cliente diferente da do servidor → down `tls` e zero.
  - `TestDownByStatusStillReadsTheCertificate` (Foco 5): 500 → down `http_status` e `2026-12-15`.
  - `TestProductionClientKeepsDefaultVerification` (em `safedial`): o transporte de `safedial.NewClient(0)` tem
    `TLSClientConfig == nil` (sem raízes próprias e sem `InsecureSkipVerify`).
  - `TestResultHasNoRoomForTheBody`: a lista esperada ganha `CertNotAfter`.
- [ ] **Step 2:** `go test -run 'ProbeReads|ProbeIgnores|ProbeOverHTTP|ProbeOnIPLiteral|ProbeAccepts|TLSHandshakeObserved|UnverifiedCertificate|DownByStatus|ResultHasNoRoom' ./internal/prober`
  → FAIL. `go test -run ProductionClientKeeps ./internal/prober/safedial` já nasce verde (trava o que existe): vê-lo falhar
  com a sabotagem temporária `TLSClientConfig: &tls.Config{InsecureSkipVerify: true}` em `newTransport` e desfazê-la.
- [ ] **Step 3: implementar.** `Probe` monta o contexto com `httptrace.WithClientTrace` antes de `NewRequestWithContext`;
  ao fim, `validadeDoHost(host, coletados())` só se `hostDoCertificado` deu `true`; também no caminho de erro do `client.Do`
  (um handshake verificado antes de um erro posterior conta).
- [ ] **Step 4:** PASS, também com `-race`. Sabotagens: ler `resp.TLS` → o teste do redirect quebra; tirar o mutex → `-race`
  acusa no teste de HTTP/2 com redirect; guardar handshake com `err != nil` → T3 quebra.
- [ ] **Step 5:** `git grep -n InsecureSkipVerify -- ':!*_test.go'` → vazio. Commit
  `feat(prober): a sonda observa o certificado do host cadastrado`.

### Task 5: `gravar` atualiza `target_tls`

**Files:**
- Modify: `internal/storage/queries/prober.sql` (`GetTargetTLS`, `SaveTargetTLS`); `make generate`
- Modify: `internal/prober/scheduler.go` (`guardarValidade`, gancho `depoisDeLerValidade`)
- Test: `internal/prober/certificado_scheduler_test.go`

**Interfaces:**
- Consome: `substituirValidade` (Task 2), `Result.CertNotAfter` (Task 4).
- Produz: `-- name: GetTargetTLS :one` (`SELECT not_after, observed_at FROM target_tls WHERE target_id = $1`);
  `-- name: SaveTargetTLS :exec` (`INSERT ... ON CONFLICT (target_id) DO UPDATE SET not_after = EXCLUDED.not_after,
  observed_at = EXCLUDED.observed_at`); `func (s *Scheduler) guardarValidade(ctx context.Context, q *db.Queries, a
  db.ClaimDueTargetsRow, em, lida time.Time) error`, chamada em `gravar` depois de `avaliar` quando `lida` não é zero.

- [ ] **Step 1: testes que falham** (`novaBancada`, chamando `b.s.gravar` com `Result` montado, como `semearFalhas`):
  - `TestGravarKeepsTheWorstCertificate`: leituras `B` e depois `A` → linha `A`; depois `B` 1 min adiante → linha `A`.
  - `TestBalancerAlternationDoesNotRewriteTheRow`: 10 gravações alternando `A`/`B` a cada minuto → `xmin` da linha igual ao
    da primeira gravação de `A`, e `not_after = A`.
  - `TestRenewalReplacesAfterTheWindow` (Foco 1): guardada `A` vista em `t`; gravação com `B` em `t + 6h + 1s` (intervalo
    60 s) → linha `B`.
  - `TestConcurrentProbesKeepTheWorstCertificate`: duas `gravar` simultâneas, `B` e `A`, com `depoisDeLerValidade`
    segurando a primeira; `testdb.EsperarBloqueados` confirma a segunda esperando a trava da histerese; final `A`.
  - `TestDiscardedProbeDoesNotTouchTargetTLS`: alvo pausado durante a sonda → nenhuma linha.
- [ ] **Step 2:** `flock -o ... go test -run 'GravarKeeps|BalancerAlternation|RenewalReplaces|ConcurrentProbesKeep|DiscardedProbeDoesNot' ./internal/prober` → FAIL.
- [ ] **Step 3: implementar.**
- [ ] **Step 4:** PASS. Sabotagens: gravar sempre a leitura → alternância e pior certificado quebram; chamar
  `guardarValidade` antes de `avaliar` → o teste concorrente termina com `B`.
- [ ] **Step 5:** commit `feat(prober): guarda o pior certificado visto por alvo`.

### Task 6: serviço e API do alvo — `tls_warn` e `tls`

**Files:**
- Modify: `internal/storage/queries/targets.sql` (`InsertTarget` e `UpdateTarget` com `tls_warn`;
  `GetTargetTLSOfOwner`, `ListTargetTLSOfOwner`, `DeleteTargetTLSOfOwner`); `make generate`
- Modify: `internal/targets/target.go`, `internal/targets/service.go`; `internal/httpapi/targethandlers.go`
- Test: `internal/targets/service_test.go`, `internal/httpapi/targets_integration_test.go`,
  `internal/httpapi/tags_integration_test.go`

**Interfaces:**
- Produz: `type CertificateReading struct { NotAfter, ObservedAt time.Time }`; `Target.TLSWarn bool`, `Target.TLS
  *CertificateReading`; `Input.TLSWarn bool` (o handler põe `true` quando ausente); `Patch.TLSWarn *bool`. `List` lê as
  validades do dono numa consulta só (`ListTargetTLSOfOwner(user_id)`), sem uma por alvo. Queries de dono filtram por
  `user_id` com `EXISTS` em `targets`, como `ResetTargetHealth`.

- [ ] **Step 1: testes que falham.**
  - `TestPatchToAnotherHostForgetsTheCertificate` (Foco 2, `service_test.go`): linha em `target_tls`; PATCH só do caminho
    da URL → linha fica; PATCH para outro host → linha some, na mesma transação.
  - `TestTargetShowsTheCertificateReading` (httpapi): linha gravada pelo sondador → `GET` e a lista trazem `tls` com
    `not_after` e `observed_at` literais; alvo sem linha → `"tls": null`.
  - `TestTLSWarnDefaultsAndPatch`: POST sem `tls_warn` → `true`; POST com `false` → `false`; PATCH `true` → `true`;
    PATCH `"tls_warn": null` → 400 `tls_warn`; `"tls_warn": "sim"` → 400; POST com `"tls": {...}` → 400 (campo desconhecido),
    nenhum alvo criado.
  - Chaves exatas: `queria` ganha `"tls"` e `"tls_warn"`; dentro de `tls`, exatamente `not_after` e `observed_at`.
  - `TestAnotherUsersTargetIs404OnEveryRoute` ganha o PATCH de `tls_warn`: 404, corpo sem dado de A (nem `not_after`),
    `tls_warn` de A intacto.
- [ ] **Step 2:** `flock -o ... go test -run 'PatchToAnotherHostForgets' ./internal/targets` e
  `flock -o ... go test -run 'TargetShowsTheCertificate|TLSWarn|AlvoTemExatamente|AnotherUsersTarget' ./internal/httpapi` → FAIL.
- [ ] **Step 3: implementar.** Em `Update`, `DeleteTargetTLSOfOwner` quando `mudouDeDestino(row.URL, f.URL)`; o `Target`
  devolvido pelo `Update` relê a validade depois disso.
- [ ] **Step 4:** PASS. Sabotagens: tirar o `DeleteTargetTLSOfOwner` → Foco 2 quebra; tirar `user_id` da consulta de
  validade → o teste de acesso mostra `not_after` de A.
- [ ] **Step 5:** commit `feat(targets): validade do certificado e tls_warn na API do alvo`.

### Task 7: painel — "Certificado válido até …, em N dias"

**Files:**
- Modify: `web/src/lib/api/schemas.ts` (`Alvo.tls_warn`, `Alvo.tls`)
- Create: `web/src/features/alvos/certificado.ts`, `web/src/features/alvos/certificado.test.ts`,
  `web/src/features/alvos/CertificadoDoAlvo.tsx`
- Modify: `web/src/features/alvos/PaginaDetalhe.tsx` (a linha abaixo de `TopoDoDetalhe`)
- Test: `web/src/features/alvos/detalhe.test.tsx`

**Interfaces:**
- Produz: `Alvo.tls_warn: z.boolean()`, `Alvo.tls: z.object({ not_after: instante, observed_at: instante }).nullable()`;
  `diasRestantes(notAfter: string, agora: Date): number` (`Math.ceil((Date.parse(notAfter) - agora.getTime()) / 86_400_000)`);
  `certificadoSeAplica(url: string): boolean` (`https:` e host que não é IP literal);
  `textoDoCertificado(alvo: Pick<Alvo, 'url' | 'tls'>, agora: Date): string`.
- Textos fixos: `Certificado válido até ${dataHora(not_after)}, em ${n} ${plural(n, 'dia', 'dias')}`; com `n <= 0`:
  `Certificado venceu em ${dataHora(not_after)}`; sem leitura e sem HTTPS com nome: "Certificado: não se aplica a este
  endereço."; sem leitura com HTTPS: "Certificado: aguardando a primeira sonda."; consulta com erro: "Certificado:
  indisponível."; carregando: esqueleto de uma linha. Ícone ao lado do texto (cor nunca é o único sinal).

- [ ] **Step 1: testes que falham.**
  - `certificado.test.ts`, `diasRestantes` com `notAfter = 2026-12-01T00:00:00Z`: agora `2026-11-17T00:00:00Z` → 14;
    `2026-11-16T23:59:59Z` → 15; `2026-11-30T19:00:00Z` → 1; `2026-12-01T00:00:00Z` → 0; `2026-12-02T00:00:00Z` → -1 (a
    mesma tabela vai para a função Go do PR 7).
  - `certificadoSeAplica`: `https://exemplo.com` → true; `http://exemplo.com`, `https://203.0.113.5`, `https://[2001:db8::1]` → false.
  - `textoDoCertificado`: os cinco textos acima, com valores literais (`em 1 dia` no singular).
  - `detalhe.test.tsx`: o detalhe mostra a linha com o texto certo para cada estado; erro da consulta mostra
    "Certificado: indisponível." sem acusar o alvo; o parse aceita `tls: null` e recusa `tls` sem `observed_at`.
- [ ] **Step 2:** `flock -o ... npx vitest run src/features/alvos src/lib/api` → FAIL.
- [ ] **Step 3: implementar.** `agora = new Date(consulta.dataUpdatedAt)`; contraste de texto e ícone nos dois temas.
- [ ] **Step 4:** PASS; `npx tsc --noEmit` e lint limpos. Sabotagens: `Math.floor` no lugar de `Math.ceil` → os casos 15 e 1
  quebram; tirar o ramo `n <= 0` → "em 0 dias" aparece e o teste quebra.
- [ ] **Step 5:** commit `feat(painel): validade do certificado no detalhe do alvo`.

### Task 8: documentação e verificação final

**Files:** `README.md` (bullet de alvos: validade do certificado no detalhe, sem aviso ainda).

- [ ] README atualizado; nada sobre aviso de vencimento (PR 7).
- [ ] `go build ./... && go vet ./... && make test` (sob flock, bateria acima de 50%; senão, só o CI).
- [ ] `sh deploy/ci/checar-nome.sh`; `npx tsc --noEmit`, lint e `npx vitest run` no `web/`.
- [ ] Revisão do branch com `code-review` e `security-review`. PR draft com: número final da migration, as sabotagens de
  cada task, o Ruling de `tls_alerts` (o plano do PR 7 cria a tabela na +4) e o de `tls_warn` sem painel. Merge depois do
  PR 5 publicado; conferir o run do `Coolify release` deste SHA antes de anunciar.
