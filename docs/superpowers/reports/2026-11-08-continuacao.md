# Continuidade do StillOn — 2026-11-08

Atualiza o [registro de 2026-11-06](2026-11-06-continuacao.md), que segue valendo como histórico.

Conferido em 2026-11-08 com `git log`, `gh pr`, `gh run` e `curl` de fora. O que não deu para
conferir está em "Não confirmado".

## Estado real

- **Publicado: `3980e61`** (#209). O `ci` da `main` (run 42465337705) e o `Coolify release`
  (run 42466237034) passaram, com `gates` e `deploy` verdes. `https://stillon.tech/readyz` respondeu
  200. A publicação anterior foi a `e68d5bd` (#203, #205 e #206), também conferida.
- **Publicação alternada.** A VPS é compartilhada com o Duora e o GoblinShift. Cada merge com código
  espera vaga, e sai uma release por vez. A sessão que coordena os três projetos decide a ordem.
- **Ensaio de restauro verde na `main`.** O environment do ensaio passou a aceitar a `main`, com sim
  direto do dono. O `revalidar-backup` passou na execução manual de 2026-11-07 (run 42339253780) e
  na agendada de 2026-11-08 (run 42435949023). A issue #198 está fechada. O environment ainda aceita
  a branch antiga do ensaio, e retirá-la depende de sim direto do dono.
- **PRs abertos:** nenhum. Os #181, #184, #185 e #188, pendentes no registro anterior, entraram em
  2026-11-06.
- **Migrations:** a última é a 00022, e a vinculação Discord usará a 00023.
- **Go:** fica em 1.26.10, por decisão do dono.

## O que entrou desde o registro anterior

| PR | O que entrou | Publica? |
|---|---|---|
| #203 | `fix(prober)`: recusa o host cujo domínio muda entre versões da tabela do IDNA (ẞ maiúsculo). O alvo é gravado como down/blocked e não vai à rede | sim, em `e68d5bd` |
| #205 | `fix(api)`: `null` em campo obrigatório de PATCH responde 400 | sim, em `e68d5bd` |
| #206 | `fix(web)`: o grupo de incidentes "Em andamento" passa a se chamar "Abertos" | sim, em `e68d5bd` |
| #204, #207 | `docs`: decisões D1 a D3 e achados da revisão aplicados à spec e ao plano da vinculação Discord | não (`[skip ci]`) |
| #208 | `docs`: auditoria de refatoração conferida contra a `main` (16 itens resolvidos, 9 novos) | não (`[skip ci]`) |
| #209 | `test`: cinco lacunas achadas na revisão por sabotagem. Cada teste foi visto falhar com o código sabotado, e os de Postgres rodaram contra banco real | sim, em `3980e61` |

## Esperam o dono

- **Retirar a branch antiga da política do environment do ensaio.** Só com sim direto.
- **Vinculação Discord:** o plano está revisado, mas a implementação só começa com ordem do dono.
- **Refatoração:** o dono quer fazê-la junto, a partir do backlog da
  [auditoria](2026-11-05-auditoria-refatoracao.md). Os itens marcados como "decisão pendente do
  dono" têm o detalhe fora do repositório.
- **`stash@{0}` local** (volume externo do compose): continua guardado até o dono decidir.
- **Detalhes de design do redesign** e as demais decisões da
  [continuidade de 2026-11-05](2026-11-05-continuacao.md#decisões-pendentes-do-dono).

## Próximos passos

- **Janela de IP real** ([`docs/deploy/janela-ip-real.md`](../../deploy/janela-ip-real.md)): o
  calendário do registro anterior segue. O StillOn não publica nada durante a janela. A mudança no
  site acontece no repositório do GoblinShift, que é o dono do `stillon.tech.caddy` em produção. O
  comentário de `deploy/vps/edge/stillon.caddy` ainda cita o caminho antigo desse arquivo e será
  corrigido junto com a janela.
- **Comparação do `.caddy` em produção com o do repositório:** proposta para o repositório do
  GoblinShift. O StillOn não guarda cópia desse arquivo.
- **Triagem de segurança de 2026-11-08** sobre `3980e61`:
  - Dependabot, code scanning, secret scanning e `npm audit` (produção e dev) estão sem nada aberto.
  - O `govulncheck` não acusa vulnerabilidade atingível; a stdlib do Go 1.26.10 está limpa.
  - Fica um aviso fora do caminho do código: GO-2026-5932, no `golang.org/x/crypto/openpgp`, que
    nunca é importado (do módulo, só o `argon2` é usado).
  - A correção existe apenas como pseudo-versão do `x/crypto`. O bump espera uma versão estável,
    para não publicar dependência que não é release.

## Não confirmado

- Que o container em execução é o de `3980e61`. Vi o run verde e o `readyz`, não o container.
