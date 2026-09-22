# Pendências que atravessam o Plano 1

Este arquivo existe porque o ledger de execução vive em `.superpowers/`, que é **git-ignored**:
um `git clean -fdx` o apaga. O que está aqui é o subconjunto que não pode se perder.

Plano 1 encerrado em 2026-09-22, `main` em `c2659c7`.

## Já valendo em produção — não é código

**`TRUSTED_PROXIES` precisa entrar na configuração do Fly.io.** O código está correto desde o
commit `215ee2e`, mas a lista é vazia por padrão (falha fechada, deliberado). Enquanto a
variável não existir no ambiente, `r.RemoteAddr` continua sendo o IP do proxy da plataforma
para todo mundo, e portanto:

- o limitador de `/login` é um balde único compartilhado por todos os clientes;
- a premissa da ruling R35 ("o limitador é por IP e o bloqueio é por conta") continua falsa;
- `sessions.ip_hash` e `audit_log.ip_hash` gravam o mesmo hash para todos, o que anula a razão
  de a coluna existir (spec §4.2).

O terceiro item é o que tem pressa: **linha já gravada com o hash do proxy não é recuperável.**
`config.Load` emite aviso no boot quando `PUBLIC_BASE_URL` é https e a lista está vazia, mas
aviso não configura nada.

## Para o Plano 2

| Origem | O que falta | Por que agora |
|---|---|---|
| R15 | `TestTransportDoesNotReuseConnections` verifica configuração, não comportamento | O teste comportamental exige lista de bloqueio injetável, que o sondador precisa de qualquer forma — escrever os dois juntos |
| R22 | Faixas de transição IPv6 fora da lista: 6to4 `2002::/16`, Teredo `2001::/32`, NAT64 `64:ff9b::/96` | Inerte enquanto `safedial` não tem consumidor; o consumidor chega no Plano 2 |
| R23 | Revisitar semânticas incomuns de `netip` | Antes de o sondador passar a receber endereços de resolução de DNS |
| Item 9b | `goose.UpContext` sem advisory lock (`goose.NewProvider` + `WithSessionLocker`) | Inerte com instância única; vira Importante quando o prober virar processo separado. Modo de falha medido: crash no boot, não corrupção de schema |
| R2 | `auth.TOTP` acessa o `*pgxpool.Pool` direto | Continua sendo a decisão certa, mas a dívida cresceu com o `Disable`: mais lugares para tocar na migração para sqlc |

## Para o Plano 5

- **`jti` em denylist**, ou `challenge_id` consumido no verify, para o desafio de 2FA. Hoje ele
  não é de uso único. O consumo de timestep (RFC 6238 §5.2) já estreitou muito o resíduo: cada
  sessão adicional a partir do mesmo desafio exige um código novo do autenticador.
- **Fluxo dedicado de troca de autenticador.** Já é *possível* via `/auth/totp/disable` seguido
  de novo enroll — a "consequência aceita" da ruling R38 deixou de existir —, mas não há fluxo
  próprio.
- **`HashPasswordWith`** aceita parâmetros fora de faixa e entraria em panic com `t=0`. É a
  porta exportada para os testes construírem hashes com parâmetros antigos, e produção nunca a
  chama com valor vindo de fora.

## Onde está o resto

- `docs/superpowers/reports/2026-09-22-cobertura-plano-1.md`
- `docs/superpowers/reports/2026-09-22-revisao-das-rulings.md` — as 40 decisões tomadas no lugar
  do usuário, com o custo de cada uma estar errada. Duas estavam (R35, R38), ambas corrigidas.
- Histórico completo de execução: `.superpowers/sdd/2026-09-22-fundacao-e-autenticacao/progress.md`
  (git-ignored; se sumir, reconstrói por `git log`).
