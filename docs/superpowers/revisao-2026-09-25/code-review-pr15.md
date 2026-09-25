# Code review de correção do PR #15 (Plano 9) — 2026-09-25

Revisão automática (`/code-review max`), escopo efetivo: o merge 640cd9e. 9 ângulos, cada candidato
verificado por sonda em cópia. 12 confirmados, 3 plausíveis (7, 13, 15 — o 15 é a D5 do plano).

| # | Onde | Achado | Destino |
|---|---|---|---|
| 1 | `internal/auth/totp.go:251` | `disable` valida o TOTP sem consumir o timestep: o código usado no login desliga o 2FA por ~60 s | Plano 10, A4 |
| 2 | `internal/auth/totp.go:189` | Prova do app antigo em voo sobrevive à troca: `consumirTimestep`/`disable` sem CAS no segredo; `Issue` não reconfere | Plano 10, A4 |
| 3 | `internal/storage/queries/users.sql:83` | `reset-2fa` não zera `failed_attempts`/`locked_until` | Plano 10, A6 |
| 4 | `web/src/features/configuracoes/PaginaConfiguracoes.tsx:522` | Passo 1 da troca não trava o diálogo com o rotate em voo; callbacks órfãos | Plano 10, E1 |
| 5 | `PaginaConfiguracoes.tsx:564` | Confirm comitado com resposta perdida: "Nada mudou" falso, códigos novos perdidos | Plano 10, E2 |
| 6 | `internal/auth/totp_rotation.go:207` | Deadlock `disable` (código de recuperação) × `rotate/confirm`/`reset-2fa` (~15% sem gancho) | Plano 10, A5 |
| 7 | `internal/auth/totp_rotation.go:146` | Pendente substituído na mesma família dá 401 que conta no bloqueio, em vez de 403 | Plano 10, A7 |
| 8 | `web/src/features/canais/PaginaCanais.tsx:166` | Guarda global descarta em silêncio o Retomar de outro canal | Plano 10, E4 |
| 9 | `PaginaConfiguracoes.tsx:531` | Passo 1 sem o aviso de código de recuperação possivelmente consumido no 401 | Plano 10, E5 |
| 10 | `PaginaConfiguracoes.tsx:430` | Trava do confirm sem prazo: modal preso sem saída | Plano 10, E3 |
| 11 | `PaginaConfiguracoes.tsx:777` | Foco cai no `<body>` quando "Trocar autenticador" desmonta | Plano 10, E6 |
| 12 | `web/e2e/a11y.spec.ts:172` | `expect.poll(...).toEqual([])` resolve na 1ª leitura vazia; violação tardia passa | Plano 10, E7 |
| 13 | `web/src/test/api-falsa.ts:354` | MSW diverge do servidor nas rotas do Plano 9 (9 sondas) | Plano 10, E8 |
| 14 | `internal/httpapi/totp_troca_integration_test.go:383` | Perna do rotate confere só o status | Plano 10, E9 |
| 15 | `internal/httpapi/totphandlers.go:421` | rotate/confirm não zeram `failed_attempts` (D5) | Plano 10, A6 |

**Confirmados mas cortados (limpeza):** CHECKs no banco para o par pendente/família (migration futura);
`Disable` repete o SQL de `ResetTOTP`/`DeleteRecoveryCodes`; tratamento de provas e bloco
RecordFailure+401 copiados 5×; trava+FamilyIsLive 4×; `useEnvioUnico` local; `GetChannelView` repete
`ListChannels`; `substituirCodigos` com 11 round-trips sob a trava; PaginaCanais refaz o GET depois do
PATCH; comentários de sabotagem desatualizados; dica de porta ocupada nunca aparece no Windows. Destino:
`PENDENCIAS-PARA-O-PLANO-11.md` (limpeza), exceto o que as tarefas do Plano 10 tocarem de passagem.
