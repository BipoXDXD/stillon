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
   sobe.
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

## Para o Plano 3 (incidentes e notificações)

- O sondador grava o check e **não** avalia incidente. O ponto de extensão é
  `(*Scheduler).gravar`, depois do `INSERT` bem-sucedido. A avaliação precisa saber o
  `user_id` do alvo; `ClaimDueTargets` não o devolve hoje.
- **Sonda abortada pelo shutdown não é gravada (D12)**, então não conta como falha para a
  histerese. Um deploy não abre incidente.
- Toda tabela nova com `user_id` nasce com RLS `FORCE` e política com `NULLIF`, como a
  migration 00004.

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

- Reivindicação que devolva mais linhas que vagas (subselect reexecutado num nested loop)
  daria panic "released more than held" no semáforo. Saída: CTE `MATERIALIZED` ou guarda
  `len(alvos) > vagas` (Task 7).
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
- `checks_via_target` usa `target_id` não qualificado (Task 3).
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
- Histórico de execução: `.superpowers/sdd/<data>-alvos-e-sondador/progress.md` (git-ignored).
