# Continuidade do StillOn — 2026-11-06

Atualiza o [registro de 2026-11-05](2026-11-05-continuacao.md), que segue valendo para o histórico.

Conferido em 2026-11-06 com `git log`, `gh pr`, `gh run`, `gh api` e `curl` de fora. Sem SSH: nada
aqui foi lido na VPS. O que não deu para conferir está em "Não confirmado".

## Estado real

- **`main` em `0005045`** (#182, só documentação, com `[skip ci]`, então não publicado).
- **Publicado: `49bbcd3`** (#183). `ci` e `Coolify release` verdes nesse SHA, com o release às
  17:03 UTC. `https://stillon.tech/readyz` respondeu 200, e `curl -I` mostrou `server: cloudflare`.
  O `ci` de `158b32c` (#179) foi cancelado pelo push seguinte, e o de `49bbcd3` cobre os dois.
- **Migrations:** a última é a 00022.
- **Issues abertas:** nenhuma.
- **Environments:** `production` aceita só a `main`. `forwarding-audit-restore-20261023` aceita só
  `audit/forwarding-review-20261023`.

## PRs #146 a #182

Títulos e estados do `gh pr list`; não revi o diff de cada um.

| PR | O que entrou |
|---|---|
| #146–#148, #152, #158, #162, #164, #174, #176, #178, #182 | Documentação: fatos das VPS, specs, auditoria e complemento, risco da imagem do banco, modelo de ameaça com a Cloudflare |
| #149, #157, #169, #173 | Heartbeats: esquema, ping, avaliador, API, código de saída, última duração, tags, prévia do cron, descrição |
| #161, #168, #151, #154, #156, #170 | Painel e API: tela de heartbeats, tabela densa, tags, filtros, ordenação |
| #150, #175, #179, #180 | Testes: falsos alarmes do `httpapi`, prazos do fluxo, jornada de incidente, IP do cliente na cadeia real |
| #163, #171, #172 | Dependências |
| #177 | `fix(heartbeats)`: domingo do cron e linha do caractere invisível |

**Fechados sem merge:** #153, #155, #159, #160, #165, #166 e #167. Não li o motivo de cada um.

**Aberto no intervalo:** só o #181.

## Janelas

Detalhes em [`docs/deploy/janela-ip-real.md`](../../deploy/janela-ip-real.md). O plano completo é
interno.

| Quando (UTC) | O quê |
|---|---|
| Seg 09/11, 11:10 | Mini-janela, sem mudança no StillOn |
| Ter 10/11, 11:10 | Congelamento dos três repositórios |
| Ter 10/11, 11:20 | Fase 2: site do StillOn no gateway |
| Qui 12/11 | Reserva |

O StillOn não publica nada na janela. A correção do IP do cliente é só o `stillon.tech.caddy`, no
repositório do GoblinShift. O GoblinShift#183 e o #187 seguiam abertos.

## Decisões do dono em aberto

- **As 16 da [continuidade de 2026-11-05](2026-11-05-continuacao.md#decisões-pendentes-do-dono).** Não
  reconferi uma a uma.
- **D6, D7 e D8 da [auditoria](2026-11-05-auditoria-refatoracao.md).** O bloco F3, F4, F5 e F16 anda com
  a D7; este registro só corrigiu o F4 e o F5 em texto, com o aviso no runbook. A D9 e a D11 constam
  como tomadas no corpo do #181. A D10 está sendo executada pela janela.
- **Hash do site na janela.** A sessão do gateway precisa dizer se publica a versão do #183 ou a do
  #187, com as linhas `tls`. Com a do #187, a fixture daqui fica atrás.
- **Renovação do certificado de origem.** Precisa de definição antes de 2026-12-12.
- **Proposta do #181:** check de revisão independente, que só vale com uma segunda conta.
- **Decisões internas da janela:** ficam no plano interno.

## Pendências

- **#181 sem a ampliação do environment.** Nenhum dos passos pós-merge foi feito. O PR estava
  `BLOCKED`, com os checks verdes; não apurei a causa. Até incluir a `main` na política de
  `forwarding-audit-restore-20261023`, o ensaio agendado falha e abre a issue, como o PR prevê.
- **#184** (ajustes do #180): `CLEAN`, checks verdes, sem merge. `sites/stillon.tech.caddy` não muda.
- **#185** (F6 a F10): `BLOCKED`, checks verdes, sem merge. Não apurei a causa. Conflito esperado com
  o #181 só no job `workflows`.
- **Cadeia antiga do restauro.** O run `42170071845` da branch `audit/forwarding-review-20261023` segue
  `waiting` desde 2026-11-06 05:43 UTC. As duas execuções anteriores da cadeia falharam
  (`41879675778` e `42021172944`), e a última verde é a `41739202525`. Cancelá-la e tirar a branch da
  política é o passo 5 do #181.
- **Merge é deploy:** #181, #184 e #185 têm de entrar antes do portão de 10/11 ou depois do fim do
  congelamento.

## Não confirmado

- Que a imagem em execução é a de `49bbcd3`: vi o run verde, não o container.
- `stillon-vigia` ativo na VPS atual, a data do certificado na origem e o commit fixo do recurso.
- Se a imagem do banco já foi restaurada por nome: o `deploy/coolify/README.md` segue dizendo que não.
- Motivo do fechamento dos PRs sem merge e causa do `BLOCKED` de #181 e #185.
