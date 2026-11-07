# Continuidade do StillOn — 2026-11-06

Atualiza o [registro de 2026-11-05](2026-11-05-continuacao.md), que segue valendo para o histórico.

Conferido em 2026-11-06 com `git log`, `gh pr`, `gh run`, `gh api` e `curl` de fora, em duas
passadas (a segunda, no fim da tarde, depois do #187). Sem SSH: nada aqui foi lido na VPS. O que
não deu para conferir está em "Não confirmado".

## Estado real

Atualizado em 2026-11-07.

- **Publicado: `15f3d79`** (#199). `ci` e `Coolify release` verdes nesse SHA, com `gates` e `deploy`
  executados, e `https://stillon.tech/readyz` em 200.
- **Go:** fica em 1.26.10, por decisão do dono. O #190 (1.27.3) foi fechado sem merge, e a branch
  `chore/go-1.27.3` é mantida. Se um `govulncheck` futuro exigir versão mais nova, a decisão volta ao
  dono.
- **Uso:** a produção tem uma conta e nenhum alvo, heartbeat ou sessão recente. O StillOn está no ar sem
  monitorar nada até o dono cadastrar o que quer acompanhar.
- **Migrations:** a última é a 00022.
- **Ensaio de restauro:** roda na `main` desde o #181. O aviso foi provado: os runs de 2026-11-06 e 07
  falharam como previsto, e a issue #198 está aberta. O restauro real segue sem prova.
- **Environments:** cinco. `production` aceita só a `main`, sem revisor nem espera. Os quatro
  `forwarding-audit-*-20261023` são do ensaio; o de restauro ainda aceita só a branch antiga.
- **Workflows:** o `deploy` (manual, antigo) está `disabled_manually`.

## PRs #146 a #188

Títulos e estados do `gh pr list`; não revi o diff de cada um.

| PR | O que entrou |
|---|---|
| #146–#148, #152, #158, #162, #164, #174, #176, #178, #182, #186, #187 | Documentação: fatos das VPS, specs, auditoria e complemento, risco da imagem do banco, modelo de ameaça com a Cloudflare, janela do IP real, aviso no runbook antigo, `CLAUDE.md` sem identificadores da VPS |
| #149, #157, #169, #173 | Heartbeats: esquema, ping, avaliador, API, código de saída, última duração, tags, prévia do cron, descrição |
| #161, #168, #151, #154, #156, #170 | Painel e API: tela de heartbeats, tabela densa, tags, filtros, ordenação |
| #150, #175, #179, #180 | Testes: falsos alarmes do `httpapi`, prazos do fluxo, jornada de incidente, IP do cliente na cadeia real |
| #163, #171, #172 | Dependências |
| #177 | `fix(heartbeats)`: domingo do cron e linha do caractere invisível |

**Fechados sem merge:** #153, #155, #159, #160, #165, #166 e #167. Não li o motivo de cada um.

**Abertos no intervalo:** #181, #184, #185 e #188 (ver "Pendências").

## Janelas

Detalhes em [`docs/deploy/janela-ip-real.md`](../../deploy/janela-ip-real.md). O plano completo é
interno.

| Quando (UTC) | O quê |
|---|---|
| Seg 09/11, 11:10 | Mini-janela, sem mudança no StillOn |
| Ter 10/11, 11:10 | Congelamento dos três repositórios |
| Ter 10/11, 11:20 | Fase 2: site do StillOn no gateway |
| Qui 12/11 | Reserva |

O StillOn não publica nada na janela. A mudança é só o `stillon.tech.caddy`, no repositório do
GoblinShift. O GoblinShift#183 (`BEHIND`) e o #187 (`BLOCKED`) seguiam abertos na
segunda passada.

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

- **Ampliação do environment do ensaio.** Depende do sim direto do dono. Sem ela, o ensaio falha todo dia e
  comenta na issue #198.
- **#184** (ajustes do #180): `CLEAN`, checks verdes, sem merge. `sites/stillon.tech.caddy` não muda.
  Está 5 commits atrás da `main` e mesmo assim `CLEAN`.
- **#185** (F6 a F10): `BLOCKED`, checks verdes, sem merge, 6 commits atrás da `main`, sem pedido de
  revisão nem revisão feita. Como o #184 também está atrás e `CLEAN`, a causa não é só a branch
  desatualizada; não apurei qual. Conflito esperado com o #181 só no job `workflows`.
- **#188** (fecha a revisão da jornada de editar e alinha `statusOf` na API falsa): aberto às 17:55 UTC,
  com todos os checks do `ci` verdes e `CLEAN` (o `publish` pula, como em todo PR). Só mexe em testes
  do painel. Sem merge.
- **Cadeia antiga do restauro.** O run `42170071845` da branch `audit/forwarding-review-20261023` segue
  `waiting` desde 2026-11-06 05:43 UTC. As duas execuções anteriores da cadeia falharam
  (`41879675778` e `42021172944`), e a última verde é a `41739202525`. Cancelá-la e tirar a branch da
  política é o passo 5 do #181.
- **Merge é deploy:** #181, #184, #185 e #188 têm de entrar antes do portão de 10/11 ou depois do fim
  do congelamento.
- **README e `mac.md`.** O PR desta passada reescreve no README a descrição da produção (Coolify,
  Cloudflare e proxy do Coolify na frente) e do painel (tabela única, tags, filtros, ordem), e deixa de
  chamar o runbook antigo de guia da produção. O `docs/deploy/mac.md` ainda pressupõe esse runbook e
  não foi tocado.

## Não confirmado

- Que a imagem em execução é a de `49bbcd3`: vi o run verde, não o container.
- `stillon-vigia` ativo na VPS atual, a data do certificado na origem e o commit fixo do recurso.
- Se a imagem do banco já foi restaurada por nome: o `deploy/coolify/README.md` segue dizendo que não.
- Motivo do fechamento dos PRs sem merge e causa do `BLOCKED` de #181 e #185.
- O que o README diz da VPS atual e só se lê lá: o backup semanal da Hostinger e o peso de ~7,6 MB
  da imagem. O endurecimento do SSH e do firewall saiu do README por esse motivo.
