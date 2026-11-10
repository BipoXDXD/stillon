# SP1, PR 5: regra de conteúdo na sonda e no alvo — plano de implementação

> **Para agentes:** SUB-SKILL OBRIGATÓRIA: use superpowers:subagent-driven-development (recomendado) ou
> superpowers:executing-plans, tarefa por tarefa. Os passos usam `- [ ]`.

**Objetivo:** o dono cadastra, num alvo, uma regra de conteúdo (`contains`, `not_contains` ou `json_equals` com ponteiro
RFC 6901); a sonda lê até 64 KiB + 1 byte só quando há regra e sai down com `content_mismatch` (regra reprovada),
`content_encoding` (corpo comprimido num formato que ela não lê) ou `body_limit` (corpo cortado sem conclusão). O corpo nunca sai de `Probe`.

**Arquitetura:** abordagem A da spec: a regra sai da requisição que a sonda já faz, pelo mesmo cliente `safedial`. O tipo
`targets.ContentRule` nasce no domínio do alvo (validação e ponteiro); a decisão fica numa função pura do sondador,
`avaliarConteudo`. A regra entra na cerca `conferirSonda`. A migration "+2 `targets_content_rule`" acrescenta três colunas
em `targets` e os valores `content_mismatch` e `content_encoding` aos CHECKs de `checks.error_kind` e `incidents.cause`, na forma que o portão de
lock (Task 1) escolher.

**Stack:** Go 1.26.10, pgx v5, sqlc, goose, Postgres 18 (17 também, na matriz do CI) via testcontainers; React + Zod +
Vitest no painel.

**Spec:** `/Users/01bipo/Documents/Claude/stillon-continuidade/repo/.claude/worktrees/sp1-piso/docs/superpowers/specs/2026-11-09-sp1-heartbeats-tls-conteudo.md`,
seções 2, 4 (abordagem A), 5.3, 6 (linha +2, regra do `set_config`, "Portão de PR em +2"), 7.2 (`content_rule`), 8
("Alvo: formulário", "Alvo: detalhe" na parte da causa), 9 (Sonda: conteúdo, Sonda: cerca, Descida, Acesso, Entrada,
Saída, Painel), 10 (T2, I1, I2, D1), 11 (regra `contains` e JSON) e 12 (PR 5). Modelo de formato:
`/Users/01bipo/Documents/Claude/stillon-pos-janela/specs/planos/2026-11-10-sp1-pr2-faixas-e-acoes.md`.

**Decisões do dono:** estágios 14/7/3 e SP1 antes da SP2 (nenhuma muda este PR); numeração de migration "próximo livre no
merge".

## Rulings deste plano

- **A forma da troca do CHECK em `checks` sai do portão (Task 1), pelo critério escrito lá.** O goose roda cada migration
  numa transação só: `NOT VALID` seguido de `VALIDATE` na mesma migration não ganha nada, porque o `ACCESS EXCLUSIVE` do
  `ADD` vale até o commit. Se a forma `NOT VALID` for a escolhida, a constraint **fica `NOT VALID`**, sem `VALIDATE`
  posterior: o CHECK novo é mais largo que o antigo, já validado, então toda linha existente o cumpre.
- **`SET LOCAL lock_timeout = '5s'` no topo da +2.** A migration roda no boot com o sondador do binário anterior gravando
  em `checks`. Sem prazo, o pedido de `ACCESS EXCLUSIVE` enfileira todas as gravações atrás de si. Com prazo, o boot falha
  em 5 s (55P03), o banco fica na versão anterior, e o reinício tenta de novo.
- **Trocar a regra é "mudou a sonda"** (D13): `next_run_at = agora` e a histerese é zerada, como URL e método (C4 do
  Plano 10). O incidente aberto fica e fecha pelos sucessos sob a regra nova.
- **Erros de validação**, no vocabulário de `targets/validate.go`: `content_rule.mode` `invalid`; `content_rule.value`
  `required`, `too_long` ou `invalid` (NUL e controle, inclusive tab e quebra de linha, pela §7.2); `content_rule.json_pointer`
  `required` (modo JSON sem ponteiro), `invalid` (não começa em `/`, escape que não é `~0` nem `~1`, ponteiro em modo que
  não é JSON) ou `too_long`. Conflito com HEAD ou com o header `Accept-Encoding` do dono: campo `content_rule`, motivo
  `forbidden`, venha a mudança da regra, do método ou dos headers.
- **Semântica de `json_equals`:** o corpo inteiro precisa ser **um** valor JSON (lixo depois dele é JSON inválido);
  JSON inválido, ponteiro ausente, objeto ou array: `content_mismatch`. A forma de texto de um escalar: string = o valor
  decodificado; número = o token como escrito no corpo (`json.Number`, então `1.0` não é `1`); `true`, `false`, `null`
  literais. Chave duplicada: vale a última (comportamento do `encoding/json`), registrado em teste.
- **Content-Encoding com regra:** a sonda pede `Accept-Encoding: gzip` e descomprime ela mesma só quando todas as linhas
  do header somam exatamente um `gzip` (`identity` não conta); qualquer outra codificação é `content_encoding`. O
  transporte do `safedial` tem `DisableCompression`. Corpo vazio é julgado pela regra com qualquer codificação.
- **`contains` e `not_contains` comparam bytes UTF-8**, sem conversão de charset, sobre o corpo do último salto.
- **`Spec.LogValue` mostra o modo da regra, nunca o valor nem o ponteiro** (podem ser texto do dono que ele não quer em log).
- **Resíduo de rollback aceito:** o binário anterior ignora a regra (sonda só pelo status), mostra `content_mismatch` e `content_encoding` como
  "erro de rede" no aviso (`notify.causa` cai no residual) e "Falha" no painel (`tiposDeErro[kind] ?? 'Falha'`), e um PATCH
  dele que troque o método para HEAD num alvo com regra dá 500 (23514 do CHECK `targets_content_rule_not_head`).
- **Benchmark da regra JSON é portão** (§11): acima de 400 KiB por operação (64 sondas × 400 KiB ≈ 25 MiB, a estimativa da
  spec), trocar a árvore por uma descida com `json.Decoder.Token` antes do merge. O ensaio de pico com os limitadores
  novos é do PR 8, que cria os limitadores.

## Restrições globais

- Migration: `internal/storage/migrations/000NN_targets_content_rule.sql`, `000NN` = **próximo número livre na hora do
  merge**; a `00015` é vaga para sempre. Testes acham a versão por `versaoDaMigration(t, "targets_content_rule")` (helper do
  PR 2; se ele ainda não estiver na `main`, criá-lo igual em `internal/storage/helpers_test.go`).
- Toda descida com dado começa por `SELECT set_config('app.scope', 'prober', true)`.
- Leitura de corpo: só com regra, até `safedial.MaxBodyBytes + 1` (65537) bytes, sobre o corpo já descomprimido. Sem
  regra, o laço atual (`io.Copy(io.Discard, io.LimitReader(..., MaxBodyBytes))`) não muda.
- Ordem em `Probe`: erro de transporte → status esperado (`http_status`) → regra → limiar de lentidão.
- Valor de 1 a 256 caracteres; ponteiro de 1 a 256 caracteres, começando em `/` (§7.2). Regra recusada com método HEAD e
  com header `Accept-Encoding` do dono.
- Só o resultado sai de `Probe`: `Result` não ganha campo de corpo; nada do corpo vai a banco, log, métrica, aviso ou API.
- Alheio ou inexistente: 404 igual, corpo sem dado do dono, alvo intacto. Entrada por DTO estrito (`DisallowUnknownFields`
  vale também dentro de `content_rule`).
- Merge só depois do fim do congelamento e com o portão da Task 1 decidido; merge é deploy: conferir o run do
  `Coolify release` deste SHA (`gh run list --workflow coolify.yml`) antes de anunciar.
- `go build ./... && go vet ./... && make test` antes de dizer que funciona; teste novo só vale visto falhar com a
  sabotagem indicada, registrada no corpo do PR. Valor esperado é literal.
- Comandos pesados sob `flock -o /Users/01bipo/.claude/docker-suite.lock`, um por vez; com bateria abaixo de 50%, só o CI.

## Foco de revisão

1. Corpo de exatamente 65536 bytes com o valor nos 6 últimos bytes: `contains` passa. Corpo de 65537 bytes com o valor
   no offset 65531 (atravessa a borda): `body_limit`, nunca `content_mismatch`. Teste na Task 5.
2. `204 No Content` esperado, com regra `contains "ok"`: corpo vazio e completo → `content_mismatch` (não `body_limit`).
   Teste na Task 5.
3. PATCH que troca o método para HEAD num alvo com regra, sem mexer na regra: 400 `content_rule` `forbidden`, nada gravado
   (método segue o antigo). Teste na Task 4.
4. Corpo em Latin-1 (`"a\xe7\xe3o"`) e regra `contains "ação"`: `content_mismatch`, como a §5.3 manda (bytes exatos); o
   formulário avisa "Compara bytes em UTF-8". Teste na Task 5 e texto na Task 9.
5. Regra trocada com incidente `content_mismatch` aberto: a histerese zera, o incidente segue aberto e fecha com os
   sucessos da regra nova (não com os da antiga, em voo). Teste na Task 6.

---

### Task 1: portão de lock — medir a troca do CHECK em `checks` e escolher a forma

**Files:**
- Create: `internal/storage/portao_checks_test.go` (com `//go:build portao`; fica fora do `make test` e do CI)

**Interfaces:**
- Produz: a **decisão registrada** (forma A ou B, com os números) no comentário de topo da migration da Task 3 e no corpo
  do PR. Nenhum código de produção.

- [ ] **Step 1: escrever `TestPortaoTrocaDoCheckEmChecks`.** Volume `V` = `PORTAO_LINHAS` (padrão 17.568.000 = 100 alvos ×
  2880 checks/dia × 61 dias, o pior caso de um dono no teto de alvos com intervalo de 30 s). Banco `testdb.New`, migrado até
  a versão atual; 100 alvos; `V` linhas em `checks` por `INSERT ... SELECT generate_series`, espalhadas por 61 dias até
  `2026-11-09` (três partições mensais, criadas com `ensure_checks_partition`), com 10% `down` (`error_kind` variado).
  `ANALYZE checks`. Confirmar o nome da constraint: `SELECT conname FROM pg_constraint WHERE conrelid = 'checks'::regclass
  AND contype = 'c'` contém `checks_error_kind_check`. Medir, cada forma num banco novo com o mesmo volume:
  - **Forma A:** uma transação com `SET LOCAL lock_timeout = '5s'` e `ALTER TABLE checks DROP CONSTRAINT
    checks_error_kind_check, ADD CONSTRAINT checks_error_kind_check CHECK (error_kind IN (<lista atual>, 'content_mismatch', 'content_encoding'))`.
  - **Forma B:** o mesmo `ALTER` com `NOT VALID` no `ADD`; depois, em outra transação, `VALIDATE CONSTRAINT` (só para
    medir; a migration não o terá, pelo Ruling).
  - Em cada forma, uma goroutine grava 1 check a cada 10 ms por `storage.InProberTx` + `q.InsertCheck` durante o `ALTER`
    e registra a **maior latência de INSERT** (o tempo que o sondador fica parado) e o tempo total do `ALTER`.
  - Na forma B, conferir também: o Postgres aceita `NOT VALID` em tabela particionada; cada partição tem a constraint
    (`count(*)` em `pg_constraint` por partição = número de partições) com `convalidated = false`; INSERT com
    `error_kind = 'xyz'` em partição antiga e numa criada **depois** (`ensure_checks_partition('2027-01-01')`) dá 23514;
    `drop_checks_partitions_before` continua funcionando.
  - `t.Logf` de uma tabela: forma, `V`, tempo do `ALTER`, maior latência de INSERT, tempo do `VALIDATE` (B), aceitação.
- [ ] **Step 2: contar o volume real, só leitura.** Na VPS (`ssh stillon`, sem segredo no chat), no psql do banco do
  StillOn: `SELECT sum(c.reltuples)::bigint FROM pg_class c JOIN pg_inherits i ON i.inhrelid = c.oid WHERE i.inhparent =
  'checks'::regclass;` (estimativa do catálogo; não passa pela RLS). Rodar o portão com `V = max(1,5 × contagem, 17.568.000)`.
- [ ] **Step 3: rodar no Postgres 18 (imagem de produção, o padrão do `testdb`):**
  `flock -o /Users/01bipo/.claude/docker-suite.lock go test -tags portao -run TestPortaoTrocaDoCheckEmChecks -timeout 60m -v ./internal/storage`.
  Depois, só a aceitação no 17 (matriz do CI), com volume pequeno:
  `TEST_POSTGRES_IMAGE=<imagem postgres:17 do .github/workflows/ci.yml> PORTAO_LINHAS=100000 flock -o ... go test -tags portao -run TestPortaoTrocaDoCheckEmChecks -v ./internal/storage`.
- [ ] **Step 4: decidir pelo critério, sem exceção:**
  1. **Forma B** se o 17 e o 18 aceitam `NOT VALID` em `checks`, a constraint chega a todas as partições (inclusive à
     criada depois) e recusa o valor fora da lista, e a maior latência de INSERT durante o `ALTER` fica **abaixo de
     500 ms** com o volume `V`.
  2. Senão, **forma A** se a maior latência de INSERT durante o `ALTER` fica **abaixo de 2 s** com `V` (folga sobre o
     `PersistTimeout` e o `tickTimeout`, ambos de 5 s, para nenhuma gravação do sondador expirar).
  3. Senão, **parar** e levar ao dono, com os números, as opções: migration `-- +goose NO TRANSACTION` com `VALIDATE`
     separado; janela de manutenção com o sondador parado; manter `content_mismatch` e `content_encoding` só em `incidents` e gravar o check como
     `unknown`. Recomendação e custo no bottom line.
- [ ] **Step 5:** registrar a tabela de números e a forma escolhida no corpo do PR. Commit
  `test(storage): portão da troca do CHECK de error_kind em checks`.

### Task 2: `targets.ContentRule`, ponteiro RFC 6901 e validação

**Files:**
- Create: `internal/targets/contentrule.go`, `internal/targets/contentrule_test.go`
- Modify: `internal/targets/target.go` (`Target`, `Input`, `Patch`), `internal/targets/validate.go` (`fields`, `validate`)
- Test: `internal/targets/validate_test.go`

**Interfaces:**
- Produz:
  `type ContentMode string`; `const ContentContains ContentMode = "contains"`, `ContentNotContains = "not_contains"`,
  `ContentJSONEquals = "json_equals"`;
  `type ContentRule struct { Mode ContentMode; Value string; JSONPointer string }` (`JSONPointer` vazio fora de `json_equals`);
  `func ParseJSONPointer(p string) ([]string, error)` (tokens já com `~1`→`/` e `~0`→`~`);
  `Target.ContentRule *ContentRule`, `Input.ContentRule *ContentRule`, `Patch.SetContentRule bool` +
  `Patch.ContentRule *ContentRule` (nil com `SetContentRule` limpa); `fields.ContentRule *ContentRule`.
- Constantes: `maxContentValueRunes = 256`, `maxJSONPointerRunes = 256`.

- [ ] **Step 1: testes que falham.**
  - `TestParseJSONPointer` (tabela): `"/a"` → `["a"]`; `"/a/0"` → `["a","0"]`; `"/a~1b"` → `["a/b"]`; `"/m~0n"` → `["m~n"]`;
    `"/"` → `[""]`; `"/a//b"` → `["a","","b"]`; `"a"`, `""`, `"/a~2"`, `"/a~"` → erro.
  - `TestValidateContentRule` (tabela sobre `validate`): sem regra → sem erro; `contains` com valor `"ok"` → sem erro;
    modo `"regex"` → `content_rule.mode invalid`; valor `""` → `content_rule.value required`; 256 × `"é"` → aceito;
    257 × `"é"` → `too_long`; valor com `"\x00"`, `"\t"` ou `"\n"` → `invalid`; `json_equals` sem ponteiro → `content_rule.json_pointer required`;
    ponteiro `"status"` → `invalid`; ponteiro `"/a~2"` → `invalid`; ponteiro em `contains` → `invalid`; ponteiro de 257
    caracteres → `too_long`; método HEAD com regra → `content_rule forbidden`; header `accept-encoding: gzip` com regra
    (antes da canonicalização) → `content_rule forbidden`; HEAD sem regra → sem erro.
- [ ] **Step 2:** `go test -run 'ParseJSONPointer|ValidateContentRule' ./internal/targets` → FAIL (símbolos inexistentes).
- [ ] **Step 3: implementar.** O conflito com `Accept-Encoding` olha `f.Headers` **depois** de `normalizeHeaders`, para
  pegar qualquer caixa. `Patch.apply` aplica a regra e devolve `true` em `mudouASonda` quando ela muda (Ruling).
- [ ] **Step 4:** PASS. Sabotagens: tirar a checagem de HEAD → o caso HEAD quebra; contar bytes em vez de runas → o caso
  256 × `"é"` quebra; aceitar `~2` → o caso do ponteiro quebra.
- [ ] **Step 5:** commit `feat(targets): regra de conteúdo e ponteiro RFC 6901 no domínio do alvo`.

### Task 3: migration +2 `targets_content_rule`

**Files:**
- Create: `internal/storage/migrations/000NN_targets_content_rule.sql`, `internal/storage/content_rule_migration_test.go`

**Interfaces:**
- Consome: a forma escolhida na Task 1; `versaoDaMigration`; `migrationsAte` (de `heartbeat_rollback_test.go`).
- Produz: colunas `targets.content_mode`, `content_value`, `content_json_pointer` (anuláveis, sem default); constraints
  `targets_content_rule_shape` e `targets_content_rule_not_head`; `checks_error_kind_check` e `incidents_cause_check` com
  `'content_mismatch'` e `'content_encoding'` a mais (mesmos nomes).

- [ ] **Step 1: testes que falham** (`content_rule_migration_test.go`, `testdb`):
  - `TestContentRuleChecksAcceptOnlyWholeRules`: INSERT direto no escopo do dono: sem regra → ok; `contains` + valor → ok;
    `json_equals` + valor + `/a` → ok; modo sem valor, valor sem modo, `contains` com ponteiro, `json_equals` sem ponteiro,
    ponteiro `"a"`, valor de 257 caracteres, regra com método HEAD → `23514`.
  - `TestContentMismatchIsAcceptedInChecksAndIncidents`: check `down`/`content_mismatch` e incidente com causa
    `content_mismatch` → ok, e o mesmo com `content_encoding`; `error_kind = 'xyz'` → `23514`.
  - `TestContentRuleDownConvertsAndRestores`: `EmptyDatabase` → `Migrate`; um alvo com regra `contains`, um check
    `content_mismatch`, um incidente `content_encoding`. `MigrateDownTo(versão - 1)`: o check e o incidente ficam com
    `unknown`; as três colunas e as duas constraints somem; INSERT de check `content_mismatch` → `23514`.
    `MigrateUpTo(versão)` → sem erro.
  - `TestFloorBinaryBootsOnContentRule`: `Migrate`, depois `goose.NewProvider` com `migrationsAte(t, "<arquivo anterior à +2>")`
    → `Up` aplica 0 e não falha.
  - `TestContentRuleMigrationGivesUpOnLockTimeout`: banco na versão anterior; uma conexão abre transação com INSERT em
    `checks` sem commit; `MigrateUpTo(versão)` falha com `55P03` em menos de 8 s, e `goose_db_version` segue na anterior;
    depois do rollback da conexão, `MigrateUpTo` passa.
- [ ] **Step 2:** `flock -o ... go test -run 'ContentRule|ContentMismatch|FloorBinaryBootsOnContentRule' ./internal/storage` → FAIL.
- [ ] **Step 3: escrever a migration.** Up: `SET LOCAL lock_timeout = '5s'`; `ALTER TABLE targets ADD COLUMN content_mode
  text CHECK (content_mode IN ('contains','not_contains','json_equals')), ADD COLUMN content_value text CHECK
  (char_length(content_value) BETWEEN 1 AND 256), ADD COLUMN content_json_pointer text CHECK
  (char_length(content_json_pointer) BETWEEN 1 AND 256 AND left(content_json_pointer, 1) = '/')`; a forma do tudo ou nada,
  que precisa ser `CASE` (com `=` e NULL o CHECK passaria):

  ```sql
  ALTER TABLE targets ADD CONSTRAINT targets_content_rule_shape CHECK (CASE
      WHEN content_mode IS NULL THEN content_value IS NULL AND content_json_pointer IS NULL
      WHEN content_mode = 'json_equals' THEN content_value IS NOT NULL AND content_json_pointer IS NOT NULL
      ELSE content_value IS NOT NULL AND content_json_pointer IS NULL END),
    ADD CONSTRAINT targets_content_rule_not_head CHECK (content_mode IS NULL OR method <> 'HEAD');
  ```

  Depois, `checks_error_kind_check` na forma da Task 1 e `incidents_cause_check` direto (tabela pequena), cada lista igual à
  atual (ler com `pg_get_constraintdef` antes de escrever) mais `'content_mismatch'` e `'content_encoding'`. Down: `set_config`;
  `UPDATE checks SET error_kind = 'unknown' WHERE error_kind IN ('content_mismatch', 'content_encoding')`; o mesmo em `incidents.cause`; CHECKs antigos na mesma forma
  do Up; `DROP CONSTRAINT` das duas de `targets`; `DROP COLUMN` das três. Comentário no topo: a decisão da Task 1 com os
  números, o Ruling do `NOT VALID` (se B) e do `lock_timeout`, e o resíduo de rollback.
- [ ] **Step 4:** PASS. Sabotagens: tirar o `set_config` do Down → o `ADD CONSTRAINT` antigo falha com a linha
  `content_mismatch`; trocar o `CASE` por `(content_mode IS NULL) = (content_value IS NULL)` → "`contains` com ponteiro"
  passa e o teste quebra; tirar o `lock_timeout` → o teste do prazo trava até o timeout do teste.
- [ ] **Step 5:** commit `feat(storage): colunas da regra de conteúdo e causa content_mismatch`.

### Task 4: persistência e serviço do alvo

**Files:**
- Modify: `internal/storage/queries/targets.sql` (`InsertTarget`, `UpdateTarget` com as três colunas); `make generate`
- Modify: `internal/targets/service.go` (`Create`, `Update`, `toTarget`; regra ↔ colunas)
- Test: `internal/targets/service_test.go`

**Interfaces:**
- Consome: Task 2 e Task 3.
- Produz: `func regraDaLinha(mode, value, pointer *string) *ContentRule` e `func colunasDaRegra(r *ContentRule) (mode,
  value, pointer *string)` (privadas, em `contentrule.go`). `Get`, `List` e `Update` devolvem `ContentRule`.

- [ ] **Step 1: testes que falham** (integração, `service_test.go`):
  - `TestContentRuleRoundTrip`: `Create` com `json_equals` `/status` `"ok"` → `Get` devolve a mesma regra; `Update` com
    `SetContentRule` e nil → `Get` sem regra; `Update` sem `SetContentRule` mantém a regra.
  - `TestPatchToHeadWithRuleIsRefused`: alvo GET com regra; `Update` só com `Method: "HEAD"` → `ValidationError` com
    `content_rule forbidden`; relido: método `GET`, regra intacta.
  - `TestPatchAddingAcceptEncodingWithRuleIsRefused`: `SetHeaders` com `Accept-Encoding: br` → `content_rule forbidden`,
    headers intactos.
  - `TestRuleChangeResetsHealthAndProbesNow`: com `target_health` em 2 falhas e `next_run_at` daqui a 50 s, trocar a regra
    → falhas 0 e `next_run_at` = agora do relógio do teste (literal).
- [ ] **Step 2:** `flock -o ... go test -run 'ContentRuleRoundTrip|PatchToHead|PatchAddingAcceptEncoding|RuleChangeResets' ./internal/targets` → FAIL.
- [ ] **Step 3: implementar.** A condição de `ResetTargetHealth` em `Update` passa a ser "URL, método ou regra mudou".
- [ ] **Step 4:** PASS. Sabotagem: tirar a regra da condição de reset → o teste de histerese quebra.
- [ ] **Step 5:** commit `feat(targets): grava e lê a regra de conteúdo`.

### Task 5: `avaliarConteudo` e a leitura do corpo em `Probe`

**Files:**
- Create: `internal/prober/content.go`, `internal/prober/content_test.go`
- Modify: `internal/prober/probe.go` (`Spec.Content`, leitura, `LogValue`), `internal/prober/classify.go`
  (`KindContentMismatch`; o comentário de `KindBodyLimit` deixa de dizer que ele nunca é emitido)
- Test: `internal/prober/probe_test.go`, `internal/prober/spec_log_test.go`

**Interfaces:**
- Consome: `targets.ContentMode`, `targets.ParseJSONPointer`.
- Produz: `const KindContentMismatch ErrorKind = "content_mismatch"`; `const KindContentEncoding ErrorKind = "content_encoding"`;
  `type RegraDaSonda struct { Mode targets.ContentMode; Value []byte; Pointer []string }`; `Spec.Content *RegraDaSonda`
  (nil = sem regra); `func avaliarConteudo(r RegraDaSonda, corpo []byte, cortado bool) ErrorKind` ("" = passa);
  `func lerCorpo(r io.Reader) (corpo []byte, cortado bool, err error)` (lê até `MaxBodyBytes + 1`, devolve no máximo
  `MaxBodyBytes`).

- [ ] **Step 1: testes que falham.**
  - `TestAvaliarConteudo` (puro, tabela, corpo e valor literais): `contains` achado → ""; não achado e completo →
    `content_mismatch`; não achado e cortado → `body_limit`; achado e cortado → ""; `not_contains` achado (cortado ou não) →
    `content_mismatch`; não achado e completo → ""; não achado e cortado → `body_limit`; `json_equals`: `{"status":"ok"}`
    `/status` `ok` → ""; `"OK"` → `content_mismatch`; `{"n":1.0}` `/n` `1` → `content_mismatch`; `1.0` → ""; `{"a":true}`
    `/a` `true` → ""; `{"a":null}` `/a` `null` → ""; `{"a":{}}` e `{"a":[1]}` → `content_mismatch`; `{"a/b":1}` `/a~1b` `1` → "";
    `[10,20]` `/1` `20` → ""; `/2` → `content_mismatch`; ponteiro ausente → `content_mismatch`; `{"a":1}{"b":2}` →
    `content_mismatch`; `{"a":1,"a":2}` `/a` `2` → ""; corpo vazio → `content_mismatch`; JSON cortado → `body_limit`;
    `"a\xe7\xe3o"` com `contains "ação"` → `content_mismatch`.
  - Em `probe_test.go`, com `servidor(t, ...)`:
    - `TestProbeContentAtTheExactLimit`: corpo de 65536 bytes terminando em `"FIM-OK"` e `contains "FIM-OK"` → up; corpo de
      65537 bytes com `"FIM-OK"` no offset 65531 (0-based; o último byte dele é o 65537º) → down `body_limit`.
    - `TestProbeContentIsCheckedAfterTheStatus`: 500 com corpo que reprova a regra → down `http_status`.
    - `TestProbeEmptyBodyFailsContains`: 204 esperado, `contains "ok"` → down `content_mismatch`.
    - `TestProbeWithoutRuleIgnoresTheBody`: corpo de 1 MiB sem regra → up (comportamento atual).
    - `TestGzipBombStopsAtLimit` (D1): o handler responde `Content-Encoding: gzip` com 100 MiB de zeros comprimidos
      (o transporte pediu gzip sozinho); `contains "x"` → down `body_limit` em menos de 2 s.
    - `TestContentRuleRedirectToInternalIsBlocked` (I2): 302 do servidor liberado para outra porta de 127.0.0.1, não
      liberada, com regra → down `blocked`.
    - `TestResultHasNoRoomForTheBody` (I1, forma): por `reflect`, os campos de `Result` são exatamente `Status`, `Latency`,
      `StatusCode`, `ErrorKind` (o PR 6 acrescenta o seu e atualiza a lista).
  - `spec_log_test.go`: `LogValue` com regra traz `content_mode` e não contém o valor nem o ponteiro (canário
    `CANARY-<uuid>` nos dois).
  - `BenchmarkAvaliarConteudoJSON64KiB`: JSON de 64 KiB (objeto com 1000 chaves e arrays aninhados), ponteiro na última chave.
- [ ] **Step 2:** `go test -run 'AvaliarConteudo|ProbeContent|ProbeEmptyBody|ProbeWithoutRule|GzipBomb|ContentRuleRedirect|ResultHasNoRoom|LogValue' ./internal/prober` → FAIL.
- [ ] **Step 3: implementar.** `Probe` só chama `lerCorpo` com `s.Content != nil`; a latência continua indo até o fim da
  leitura. `json_equals` com `json.Decoder` + `UseNumber`, conferindo `io.EOF` no segundo `Decode` para recusar lixo depois
  do valor.
- [ ] **Step 4:** PASS. Rodar `go test -run XXX -bench AvaliarConteudoJSON64KiB -benchmem ./internal/prober` e colar o
  resultado no PR; acima de 400 KiB/op, aplicar o Ruling do benchmark e rodar de novo. Sabotagens: devolver "" no ramo
  cortado → `TestGzipBombStopsAtLimit` e o caso de borda quebram; trocar `lerCorpo` por `io.ReadAll(resp.Body)` → o
  `TestGzipBombStopsAtLimit` estoura o prazo; avaliar a regra antes do status → `...AfterTheStatus` quebra.
- [ ] **Step 5:** commit `feat(prober): regra de conteúdo avaliada na sonda, com teto de 64 KiB`.

### Task 6: a regra no sondador — reivindicação, cerca e canário

**Files:**
- Modify: `internal/storage/queries/prober.sql` (`ClaimDueTargets` e `LockTargetProbeSpec` devolvem as três colunas);
  `make generate`
- Modify: `internal/prober/scheduler.go` (`especificar`, `conferirSonda`, mensagem de `errSondaVelha`)
- Modify: `internal/notify/message.go` (`causas`)
- Test: `internal/prober/sonda_velha_test.go`, `internal/prober/scheduler_test.go`, `internal/notify/message_test.go`

**Interfaces:**
- Consome: `RegraDaSonda` (Task 5), `targets.ParseJSONPointer`.
- Produz: `func regraDaSonda(mode, value, pointer *string) (*RegraDaSonda, error)` (erro só com ponteiro ilegível, que
  `especificar` trata como "preparar sonda" falhou, sem gravar down); `type regraGravada struct { modo, valor, ponteiro
  string }` comparável com `==`, montada das duas linhas em `conferirSonda`.
- Textos: `causas["content_mismatch"] = "conteúdo da resposta diferente do esperado"`;
  `causas["content_encoding"] = "a resposta veio comprimida num formato que o StillOn não lê"`; `causas["body_limit"] = "resposta
  grande demais para conferir o conteúdo"`.

- [ ] **Step 1: testes que falham.**
  - `TestProbeDiscardedWhenRuleChanged` (T2), no molde de `TestProbeOfTheOldURLFinishingAfterThePatchIsDiscarded`: alvo com
    `contains "a"`, sonda em voo com `clienteSegurado`; `targets.Service.Update` troca a regra para `contains "b"`; liberar →
    nenhum check novo além dos semeados, falhas seguidas 0, nenhum incidente.
  - `TestRuleChangeClosesTheOpenIncidentOnlyWithNewRuleSuccesses` (Foco 5): incidente `content_mismatch` aberto; trocar a
    regra; os sucessos que `incident.Default` exige para fechar (ler o limiar no código e fixá-lo literal no teste) sob a
    regra nova → resolvido; uma sonda da regra antiga em voo durante a troca não conta.
  - `TestSpecComesFromTheClaimedRow` ganha a regra: o `Spec` montado tem `Mode`, `Value` e os tokens do ponteiro literais.
  - `TestProbeBodyCanaryNeverLeaves` (I1): o servidor responde `CANARY-<uuid>` e a regra é `contains "outra coisa"`;
    depois do `Tick` e do `Stop`: o check é `content_mismatch`; nenhuma coluna de `checks`, `incidents` e `notifications` e
    nenhuma linha do log capturado (`slog` com handler de buffer) contém o canário; o texto de `/metrics` do registro do
    teste também não.
  - `message_test.go`: `mensagem` com causa `content_mismatch` traz "conteúdo da resposta diferente do esperado".
- [ ] **Step 2:** `flock -o ... go test -run 'ProbeDiscardedWhenRuleChanged|RuleChangeCloses|SpecComesFromTheClaimedRow|ProbeBodyCanary' ./internal/prober`
  e `go test -run Mensagem ./internal/notify` → FAIL.
- [ ] **Step 3: implementar.** `conferirSonda`: `regraGravada` de `atual` diferente da de `a` → `errSondaVelha`.
- [ ] **Step 4:** PASS; `TestNewUsesTheSSRFGuardedClient` segue verde (I2: a regra não troca o cliente). Sabotagens: tirar a
  regra de `conferirSonda` → T2 quebra; logar `spec` com o valor → o canário aparece no log.
- [ ] **Step 5:** commit `feat(prober): regra de conteúdo na reivindicação e na cerca da gravação`.

### Task 7: API do alvo

**Files:**
- Modify: `internal/httpapi/targethandlers.go` (`contentRuleDTO`, `targetRequest`, `targetPatchRequest`, `targetResponse`)
- Test: `internal/httpapi/targets_integration_test.go`, `internal/httpapi/tags_integration_test.go`
  (`TestAlvoTemExatamenteAsChavesDoContrato`)

**Interfaces:**
- Produz: entrada `content_rule: {"mode": string, "value": string, "json_pointer"?: string} | null` (POST: ausente ou null =
  sem regra; PATCH: `optional[contentRuleDTO]`, null limpa); saída `content_rule: {"mode", "value", "json_pointer": string|null} | null`.

- [ ] **Step 1: testes que falham.**
  - `TestContentRuleCreatePatchAndClear`: POST com `json_equals` → 201 e a regra na resposta; PATCH com outra regra → 200;
    PATCH com `"content_rule": null` → 200 e `null`; PATCH sem o campo mantém.
  - `TestContentRuleValidationNamesTheField`: valor `""` → 400 `content_rule.value required`; HEAD + regra → 400
    `content_rule forbidden`; valor com `\u0000` → 400 `content_rule.value invalid`; 256 caracteres → 201; 257 → 400
    `too_long`; resposta sem eco do valor enviado (canário).
  - `TestContentRuleRejectsUnknownNestedField`: `{"content_rule":{"mode":"contains","value":"a","regex":true}}` → 400,
    nenhum alvo criado.
  - `TestAnotherUsersTargetIs404OnEveryRoute` ganha o PATCH com `content_rule`: 404, corpo sem dado de A, regra de A intacta.
  - Chaves exatas: `queria` ganha `"content_rule"`; dentro dela, exatamente `json_pointer`, `mode`, `value`.
- [ ] **Step 2:** `flock -o ... go test -run 'ContentRule|AnotherUsersTarget|AlvoTemExatamente' ./internal/httpapi` → FAIL.
- [ ] **Step 3: implementar** no molde de `degraded_threshold_ms` (ausente/null/valor) e de `recusouNulo` para os campos
  que não aceitam null.
- [ ] **Step 4:** PASS. Sabotagens: acrescentar campo ao DTO de saída → chaves exatas quebram. O filtro de dono não se
  prova pelo HTTP: sem o `user_id` de `GetTargetForUpdate` o 404 continua 404, porque a RLS de `InUserTx` é a segunda
  barreira (spec §5.5). Quem o prova é `TestTargetQueriesFilterByOwnerWithoutRLS` (`internal/storage`): conexão de
  superusuário e `db.New`, B chama `GetTargetForUpdate`, `UpdateTarget` e `ResetTargetHealth` sobre o alvo de A →
  nenhuma linha, alvo e histerese de A intactos. Tirar o filtro de cada uma das três (uma por vez, com `make generate`,
  mantendo o parâmetro nomeado, ex.: `(user_id = $1 OR true)`) → vermelho.
- [ ] **Step 5:** commit `feat(httpapi): regra de conteúdo no cadastro do alvo`.

### Task 8: painel — formulário "Avançado" e texto da causa

**Files:**
- Modify: `web/src/lib/api/schemas.ts` (`Alvo.content_rule`), `web/src/lib/api/cliente.ts` (`EntradaAlvo.content_rule`),
  `web/src/lib/api/mensagens.ts` (`tiposDeErro`), `web/src/features/alvos/esquema.ts` (campos, `valoresDe`,
  `paraCriacao`, `paraAlteracao`, `campoDoServidor`), `web/src/features/alvos/FormularioAlvo.tsx`
- Test: `web/src/features/alvos/esquema.test.ts`, `web/src/features/alvos/formulario.cobertura.test.tsx`,
  `web/src/lib/api/mensagens.test.ts`, `web/src/features/alvos/detalhe.test.tsx`

**Interfaces:**
- Produz: `RegraDeConteudo = z.object({ mode: z.enum(['contains','not_contains','json_equals']), value: z.string(),
  json_pointer: z.string().nullable() })`; `Alvo.content_rule: RegraDeConteudo.nullable()`; no formulário, `modoDaRegra:
  'desligada' | 'contains' | 'not_contains' | 'json_equals'`, `valorDaRegra`, `ponteiroDaRegra`.
- Textos fixos: seção `<details>` "Avançado"; legenda "Verificar o conteúdo da resposta"; opções "Desligada", "Contém",
  "Não contém", "JSON igual a"; ajuda "Compara bytes em UTF-8 nos primeiros 64 KiB da resposta."; ajuda do JSON "Números são
  comparados como escritos no JSON."; com HEAD: "Com HEAD a resposta não tem corpo. Escolha GET ou POST para verificar o
  conteúdo."; com `Accept-Encoding`: "Remova o header Accept-Encoding para verificar o conteúdo: a resposta viria
  comprimida."; erros "Obrigatório.", "Até 256 caracteres.", "Comece com /, como /status.";
  `tiposDeErro.content_mismatch = 'Conteúdo diferente do esperado'`,
  `tiposDeErro.content_encoding = 'Resposta comprimida num formato que o StillOn não lê'`, `tiposDeErro.body_limit = 'Resposta grande demais para conferir o conteúdo'`.

- [ ] **Step 1: testes que falham.**
  - `esquema.test.ts`: regra desligada não manda `content_rule` na criação; `contains` sem valor → "Obrigatório."; 257
    caracteres → "Até 256 caracteres."; ponteiro `status` → "Comece com /, como /status."; `paraAlteracao` de regra para
    desligada manda `content_rule: null`; sem mudança não manda o campo; `campoDoServidor['content_rule.value']` aponta o
    campo do valor.
  - `formulario.cobertura.test.tsx`: "Avançado" fechado por padrão; com HEAD, o seletor fica desabilitado e o texto do HEAD
    aparece; com header `accept-encoding` e regra ligada, o erro do Accept-Encoding aparece no envio, sem apagar o digitado;
    400 `content_rule forbidden` do servidor cai na seção da regra; seletor com rótulo e alvo de toque de 44 px.
  - `mensagens.test.ts`: `textoDoErroDeCheck('content_mismatch', null)` = "Conteúdo diferente do esperado".
  - `detalhe.test.tsx`: incidente aberto com causa `content_mismatch` mostra "Conteúdo diferente do esperado · desde …".
- [ ] **Step 2:** `flock -o ... npx vitest run src/features/alvos src/lib/api` → FAIL.
- [ ] **Step 3: implementar.** O espelho do esquema nunca é mais restritivo que o servidor (comentário do topo de `esquema.ts`).
- [ ] **Step 4:** PASS; `npx tsc --noEmit` e lint limpos. Sabotagens: tirar o `null` de `paraAlteracao` → a regra nunca
  é limpa e o teste quebra; tirar o texto de `content_mismatch` → "Falha" aparece e o teste quebra.
- [ ] **Step 5:** commit `feat(painel): regra de conteúdo no formulário do alvo`.

### Task 9: documentação, ensaio e verificação final

**Files:** `README.md` (bullet de alvos: regra de conteúdo, causas novas); `deploy/ci/ensaiar-piso.sh` (se já existir
pelo PR 2, um cenário a mais).

- [ ] README atualizado, sem citar TLS (PR 6).
- [ ] Cenário no `ensaiar-piso.sh`: com o branch, criar alvo com regra e gravar pelo `sql` um check e um incidente
  `content_mismatch` e outros `content_encoding`; com a tag publicada anterior, `GET /api/v1/targets`, `/targets/summary`, `/targets/{id}/checks` e
  `/incidents` → 200. Rodar uma vez, sob flock, e colar a saída no PR (não entra no CI).
- [ ] `go build ./... && go vet ./... && make test` (sob flock, bateria acima de 50%; senão, só o CI).
- [ ] `sh deploy/ci/checar-nome.sh`; `npx tsc --noEmit`, lint e `npx vitest run` no `web/`.
- [ ] Revisão do branch com `code-review` e `security-review`. PR draft com: número final da migration, a tabela do
  portão (Task 1) e a forma escolhida, o benchmark, as sabotagens de cada task, o resíduo de rollback (Ruling). Merge depois
  do congelamento; conferir o run do `Coolify release` deste SHA antes de anunciar.
