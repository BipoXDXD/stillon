# Pendências que atravessam o Plano 2

Este arquivo existe porque o ledger de execução vive em `.superpowers/`, que é **git-ignored**:
um `git clean -fdx` o apaga. O que está aqui é o subconjunto que não pode se perder.

## Já valendo em produção — não é código

1. **`TRUSTED_PROXIES` na configuração do Fly.io.** Herdada do Plano 1, ainda aberta. Sem ela,
   o rate limit por IP e o `ip_hash` da auditoria colapsam num valor único, e linha já gravada
   com o hash do proxy não é recuperável.
2. **Papel comum do Postgres no Fly.io.** Desde o Plano 2, o boot **recusa** papel superusuário
   ou `BYPASSRLS` (D8). A `DATABASE_URL` de produção precisa apontar para um papel que seja dono
   das tabelas e não seja superusuário. Criar o papel e transferir a posse das tabelas já
   existentes (`ALTER TABLE ... OWNER TO`) **antes** do deploy do Plano 2; senão o processo não
   sobe. Três detalhes que fazem o primeiro deploy falhar no `Migrate` se esquecidos:
   - `GRANT CREATE ON SCHEMA public TO <papel>`. Desde o PostgreSQL 15, o `public` não dá
     `CREATE` a todo mundo, e a 00004 precisa criar `targets` e `checks`.
   - `goose_db_version` entra na lista do `ALTER TABLE ... OWNER TO`, junto com as tabelas do
     Plano 1. A sequência da tabela acompanha a mudança de dono.
   - A ordem: o boot recusa o papel errado **antes** de migrar (D8), então o papel precisa estar
     pronto antes do deploy, e não depois do primeiro boot.
3. **`METRICS_ADDR` no Fly.io.** O padrão (`127.0.0.1:9090`) funciona, mas não é raspável pela
   rede privada do Fly. Decidir no Plano 5 entre `off` e um bind na interface privada, que
   hoje a config recusa (D10).

## Resolvidas no Plano 2

| Origem | Como fechou |
|---|---|
| R15 | `TestEachRequestOpensANewConnection` conta conexões novas no servidor |
| R22 | 6to4, Teredo e NAT64 (e as formas de IPv4 que `Unmap` não desfaz) na lista de bloqueio |
| R23 | Zona checada antes do `Unmap`, que a descartava em endereço mapeado |
| Item 9b | goose `Provider` com session locker |
| §5.5 RLS | `targets` e `checks`, com `FORCE`, papel único e teste com papel não-super |
| §3.3 sqlc | Código novo gerado; CI roda `sqlc diff` |
| §13 métricas | Sondador, pool e runtime; `/metrics` só em loopback |
| Revisão final, I1 | Guarda `len(alvos) > vagas` antes de devolver a sobra ao semáforo |
| Revisão final, I3 | Headers do alvo não seguem redirect para outro host:porta nem rebaixamento https→http |
| Revisão final, M3 | `fec0::/10` (site-local, obsoleto) na lista de bloqueio |
| Revisão final, M5 | `recover` na goroutine de sonda, que devolve a vaga e o `inflight` |
| Revisão final | `checks_via_target` com `checks.target_id` qualificado |

## Para o Plano 3 (incidentes e notificações)

- O sondador grava o check e **não** avalia incidente. O ponto de extensão é
  `(*Scheduler).gravar`, depois do `INSERT` bem-sucedido. A avaliação precisa saber o
  `user_id` do alvo; `ClaimDueTargets` não o devolve hoje.
- **Sonda abortada pelo shutdown não é gravada (D12)**, então não conta como falha para a
  histerese. Um deploy não abre incidente. Em compensação, o alvo só volta a ser sondado
  depois de um intervalo inteiro: o `next_run_at` já foi empurrado na reivindicação, e o
  shutdown não o devolve.
- **Duas sondas do mesmo alvo em voo (M4).** Um `PATCH` que reagenda (D13) com uma sonda em voo
  deixa o alvo vencido de novo, e o próximo tick dispara uma segunda sonda antes de a primeira
  terminar, e os dois checks podem ser gravados fora de ordem. A histerese precisa tolerar isso.
- **Headers em redirect (revisão final, I3).** Desde a correção, um redirect para outro
  host:porta, ou de https para http, leva só o `User-Agent`. Um alvo cujo monitor dependa de
  header autenticado DEPOIS de um redirect para outro host passa a ver 401 e vira down. É o
  comportamento pretendido (spec §5.4), mas quem tinha esse arranjo vai ver um incidente que
  não é do alvo. Vale uma linha na documentação do cadastro de alvos.
- Toda tabela nova com `user_id` nasce com RLS `FORCE` e política com `NULLIF`, como a
  migration 00004.

## Decisões e limites registrados na revisão final do Plano 2

- **`Scheduler.Run` sempre devolve `nil`.** Erro de `Tick` (banco fora, por exemplo) só vai ao
  log; o errgroup não cai, e o sondador segue tentando a cada segundo. É intencional: é assim
  que ele se reconecta sozinho quando o banco volta. Mas não há alerta: com o banco fora, o
  processo parece saudável. O Plano 3 ou o 5 decide se o health check ou o `/readyz` reflete
  isso.
- **D9 quase nunca dispara (M1).** `Metrics.forget` só roda quando o alvo some com uma sonda em
  voo (o `INSERT` do check falha). No fluxo normal, apagar um alvo parado não passa pelo
  sondador, e com `--role=api` e `--role=prober` em processos separados nem poderia: a série de
  `probe_duration_seconds` do alvo apagado vive até o restart. Precisa de reconciliação
  periódica (por exemplo, a cada N ticks, apagar as séries de alvos que não existem mais).
- **`Spec.Headers` decifrado é um `map[string]string` comum (M6).** Nada o loga hoje, mas um
  `slog.Any("spec", spec)` futuro vazaria os segredos. Dar a `Spec` (ou a um tipo próprio para
  os headers) um `LogValue`/`String` que redija os valores.
- **Porta de métricas em desenvolvimento (M7).** `--role=api` e `--role=prober` no mesmo host
  disputam `127.0.0.1:9090`, e o segundo a subir cai. Localmente, dê a cada processo um
  `METRICS_ADDR` diferente (ou `off` num deles).

## Para o Plano 4

- **R2**, adiada por decisão do usuário: migrar `auth.PGStore` e `auth.TOTP` para sqlc.
- **D6:** `GET /targets/{id}/checks` com janela acima de 48h passa a ler `check_rollup_1h`.
- **Retenção:** `MaintainChecksPartitions` ganha o `DROP` das partições vencidas (spec §12).

## Para o Plano 5

- `jti` em denylist, ou `challenge_id` consumido, para o desafio de 2FA (item 12 residual).
- Fluxo dedicado de troca de autenticador.
- `HashPasswordWith` aceita parâmetros fora de faixa (porta exportada só para teste).
- O contrato JSON de `/targets` está na Task 5 do Plano 2, incluindo o `error.fields` (D14) e
  o tri-estado de `degraded_threshold_ms`.

## Dívidas menores registradas na execução do Plano 2

Adiadas nas revisões das tarefas; nenhuma bloqueia o Plano 3. Ficam aqui porque o ledger é
git-ignored.

**Para a revisão final do Plano 2**

- Nenhum teste trava que `POST /metrics` recebe 405: trocar `"GET /metrics"` por `"/metrics"`
  passaria (Task 8).
- `METRICS_ADDR` só reconhece `off` minúsculo; `OFF` dá erro de boot (Task 8).
- `CREATE TABLE IF NOT EXISTS ... PARTITION OF` não é seguro sob boots concorrentes. Com
  `EnsureChecksPartitions` no boot de cada sondador, duas máquinas subindo juntas podem colidir
  (advisory lock ou retry) (Task 3).

**Banco e partições**

- Partições não herdam a RLS: acesso direto a `checks_AAAA_MM` veria tudo. A aplicação nunca
  consulta partição, e está comentado no código (Task 3).
- `MaintainChecksPartitions` sem teste; usa ticker real, dá panic com `every <= 0` e sempre
  devolve `nil`. O Plano 4 a estende (Task 3).
- O comentário de `targets.sql` diz que toda query filtra por `user_id`; o `INSERT` não filtra
  (Task 3).
- `initialisms` sem `ip` gera `IpHash` nos modelos de auth gerados, ainda não usados (Task 3).

**Alvos e API**

- Cada `Get`/`List` decifra os headers só para devolver os nomes; uma linha corrompida derruba o
  `List`, e nem `PATCH` com `SetHeaders` a conserta (Task 4).
- `apply` compara antes de normalizar: `method` `get` vs `GET` reagenda à toa (Task 4).
- `Update` revalida a URL guardada; endurecer as regras de URL impede renomear alvo antigo sem
  corrigir a URL (Task 4).
- `Service.List` com `limit` fora de faixa cai para 50, não 100 (o handler já recusa >100) (Task 4).
- `0` explícito em `POST` vira o padrão (`cmp.Or`) e em `PATCH` dá 400; `limit` não numérico
  reporta `out_of_range` em vez de `invalid` (Task 5).
- Testes faltando: mapeamento e bordas `[from,to)` de `Checks`, `List` excluindo outro usuário,
  `Create` inválido não gravar nada (Task 4); tabela das 6 rotas em
  `TestTargetRoutesRequireAuthentication`, e cursor/`from`/`to` malformados e `limit=0` via HTTP
  (Task 5).
- `Probe` com `Timeout == 0` gera contexto já vencido; a validação garante ≥ 1000 ms (Task 6).

**Testes e ferramentas**

- `TestConcurrentMigrationsSerialize` não confere a versão final do schema (Task 2).
- `GRANT CREATE ON DATABASE` do testdb não sobrevive ao `Restore` (inofensivo; falta comentário
  em `criarPapelDaAplicacao`) (Task 2).
- Logs do testcontainers poluem a saída de teste (Task 2).
- Nomes de teste em inglês ao lado de helpers em português (Task 1).

## Onde está o resto

- `docs/superpowers/plans/2026-09-24-alvos-e-sondador.md`: decisões D1–D16, com o custo de
  cada uma estar errada.
- `docs/superpowers/reports/2026-09-22-cobertura-plano-1.md`: o que o Plano 1 cobriu e o que
  ficou de fora.
- `docs/superpowers/reports/2026-09-22-revisao-das-rulings.md`: as rulings R1–R40 do Plano 1,
  reavaliadas contra a revisão final.
- Histórico de execução: `.superpowers/sdd/<data>-alvos-e-sondador/progress.md` (git-ignored).
