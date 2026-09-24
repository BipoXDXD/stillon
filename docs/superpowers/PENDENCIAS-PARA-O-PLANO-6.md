# Pendências que atravessam o Plano 5

Este arquivo existe porque o ledger de execução vive em `.superpowers/`, fora do controle de
versão. O que está aqui é o subconjunto que não pode se perder. Ele substitui o
`PENDENCIAS-PARA-O-PLANO-5.md` como estado corrente; aquele fica como histórico.

## Já valendo em produção — não é código

Continuam como o Plano 4 deixou (ver `PENDENCIAS-PARA-O-PLANO-5.md`, primeira seção):
`TRUSTED_PROXIES` no Fly.io, papel comum do Postgres, `METRICS_ADDR`, e-mail (`RESEND_API_KEY`
e `EMAIL_FROM`), `PUBLIC_BASE_URL` e a primeira manutenção depois do deploy. Duas acrescentadas:

1. **Usuário inicial:** `vigilume user create --email <e>` com a senha no stdin (terminal sem eco,
   ou pipe com uma linha só). Precisa só de `DATABASE_URL`, com o **papel comum**: o
   superusuário é recusado antes de qualquer migration (código 1). Código **3** = já existe
   usuário, nada mudou; um passo idempotente o trata como sucesso. `vigilume user set-password`
   é a recuperação: senha nova, bloqueio desfeito, **todas** as sessões encerradas.
2. **Política de senha nova:** 15 a 128 caracteres (code points). Senhas antigas mais curtas
   continuam entrando; a política vale na criação e na troca.

## Resolvidas no Plano 5

| Origem | Como fechou |
|---|---|
| Contrato dos Planos 5–7 | `GET /me` enriquecido, `POST /auth/password`, `GET /sessions`, `DELETE /sessions/{id}`, `GET /targets/summary`, `GET /incidents?target_id=`, `409 target_limit_reached` (teto de 100 alvos) |
| Corrida de revogação (achada na spec do Plano 5) | Toda mutação de sessão toma `pg_advisory_xact_lock` do usuário antes de travar linha; o sucessor de uma rotação concorrente não escapa mais da revogação (logout, detecção de reúso, troca de senha, `DELETE /sessions`, `set-password`) |
| `normalizarEmail` pendente no `authhandlers.go` | Virou `auth.NormalizeEmail`, a mesma no login e no CLI |
| `/totp/enroll` e `/totp/disable` sem bloqueio | Passaram a usar `auth.CheckPassword`: conta bloqueada recusa, senha errada conta |
| `/totp/disable` respondia `401 unauthorized` para código errado (apontado pelo Plano 6) | Senha ou código errado ou ausente é `401 invalid_credentials` em `/auth/password`, `/totp/enroll` e `/totp/disable`; `unauthorized` fica só para token recusado (D24) |

## Limites conhecidos do Plano 5

- **Janela de 15 minutos.** Depois de `DELETE /sessions/{id}`, da troca de senha ou do
  `set-password`, o access token já emitido de uma família encerrada continua valendo nas rotas
  de recurso (alvos, canais, status page, incidentes) até expirar. Não renova, não troca senha,
  não mexe no 2FA, não lista nem encerra sessões. É a escolha da spec §5.2 (JWT sem estado); a
  alternativa, checar a família em toda rota, custa uma consulta por requisição.
- **Login concorrente com a troca de senha.** Um login que verificou a senha **antiga** alguns
  milissegundos antes de a troca comitar pode abrir a família depois dela e sobreviver. Aparece
  em `GET /sessions` e se encerra dali.
- **`last_used_at` tem a granularidade do refresh** (~15 min num painel ativo).
- **O summary lê até 24h de `checks` se a manutenção parar**: o corte recua sozinho, e o custo
  sobe (~390 ms com 100 alvos) até ela voltar.
- **~180 KB de JSON por chamada do summary com 100 alvos**, sem compressão.
- **O limitador do login não cobre `/totp/enroll` nem `/totp/disable`** (só `/auth/password`,
  que o contrato manda): o bloqueio por conta limita as tentativas ali.

## Para o Plano 6 (painel)

- Renovar só em `401` com `code: unauthorized`, fora de `/auth/*`. `invalid_credentials` em
  `/auth/password` é erro de formulário: nunca renovar e repetir.
- `DELETE /sessions/{id}` da sessão `current`: o `204` já apagou o cookie; tratar como logout
  (descartar o token da memória, limpar o cache, ir para `/login`) sem tentar `/auth/refresh`.
  `404` no `DELETE`: a sessão já tinha acabado; recarregar a lista.
- Várias abas renovando o mesmo cookie ao mesmo tempo disparam a detecção de reúso e derrubam a
  família: coordenar o refresh entre abas (Web Locks).
- Campo de código da troca de senha: aparece com `totp_enabled` de `/me`; aceita o código de
  recuperação (16 caracteres base32). Política no cliente: 15 a 128 **code points**.
- Não oferecer "desativar 2FA" com `totp_enabled=false` em `/me`: `POST /auth/totp/disable` sem
  2FA ativo conta tentativa falha na conta, por desenho (R34).
- Código de recuperação na troca de senha é consumido **antes** da transação da troca. Se ela
  falhar depois (família da sessão revogada em concorrência, ou outra troca de senha concorrente
  venceu), o código já foi gasto: em `401` depois de enviar um código de recuperação, avisar o
  usuário de que aquele código não vale mais.
- `user_agent` de `/sessions` é texto de terceiros; `uptime_24h` é número cru (truncar na
  exibição, D16 do Plano 4).

## Herdadas, ainda abertas

- Do `PENDENCIAS-PARA-O-PLANO-5.md`: `jti` em denylist ou `challenge_id` consumido para o desafio
  de 2FA; troca de autenticador; `HashPasswordWith` com parâmetros fora de faixa; `LogValue` de
  `Spec.Headers` (M6); porta de métricas em desenvolvimento (M7); rota para desabilitar canal.
- **`MaxChannels` conta e insere sem trava:** dois `POST /channels` simultâneos passam do limite
  de 10. O teto de alvos do Plano 5 usa a trava que falta ali.
- **Recuperação do 2FA pelo CLI:** quem perde o autenticador e os códigos de recuperação só sai
  por SQL.
- **R2**: migrar `auth.PGStore` e `auth.TOTP` para sqlc. Os métodos novos do `pgTx` já são sqlc.
- As dívidas menores da seção homônima do `PENDENCIAS-PARA-O-PLANO-5.md` seguem como estavam.
