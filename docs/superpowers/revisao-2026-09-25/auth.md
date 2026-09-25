# Auditoria: autenticação, sessão e criptografia (Vigilume @ 640cd9e)

Escopo lido por inteiro: `internal/auth/*` (password, token, session, store, user, totp, totp_rotation),
`internal/crypto/*`, `internal/account/account.go`, `cmd/vigilume/user.go`,
`internal/httpapi/{authhandlers,accounthandlers,totphandlers,routes}.go`,
`internal/httpapi/middleware/*`, as queries de `sessions.sql`/`users.sql` e os trechos relevantes
de `internal/config/config.go`. Também li as pendências dos Planos 5, 8, 9 e 10, para não
reportar como novo o que já está documentado.

Nada foi editado no repositório. A única execução foi uma medição do Argon2id num módulo
descartável no scratchpad (resultado no achado S1).

## Resumo

| Severidade | Segurança | Correção |
|---|---|---|
| Crítico | 0 | 0 |
| Alto | 2 | 0 |
| Médio | 2 | 0 |
| Baixo | 2 | 3 |

---

# Segurança

## S1 — Alto: o Argon2id não tem limite de concorrência. Qualquer um derruba o processo por falta de memória, sem estar autenticado

**Onde**
- `internal/auth/password.go:20` (m=64 MiB) e `:82`/`:122` (`argon2.IDKey`)
- `internal/httpapi/authhandlers.go:65` (`VerifyDummy`, e-mail inexistente) e `:88` (`CheckPassword`)
- `internal/httpapi/routes.go:98` (`limiteLogin` com burst 5 por IP)
- `fly.toml:57` (`memory = "512mb"`)

**Medição.** Rodei `argon2.IDKey` com os parâmetros de produção, N chamadas simultâneas:

| N | RSS máximo | Tempo |
|---|---|---|
| 1 | 66 MiB | 124 ms |
| 5 | 323 MiB | 296 ms |
| 10 | 644 MiB | 453 ms |
| 20 | 1285 MiB | 911 ms |

Numa máquina de 16 núcleos. Numa VPS de 1 ou 2 vCPU cada chamada demora mais, e as chamadas se
sobrepõem mais.

**Ataque.** Um atacante anônimo, sem saber e-mail nenhum (`VerifyDummy` também custa 64 MiB),
dispara `POST /api/v1/auth/login` em paralelo:
- Cada IP tem burst 5 no limitador do login, e um bloco IPv6 /64 conta como um balde só.
- Com 2 IPs saem 10 Argon2 simultâneos, cerca de 640 MiB. Isso já passa dos 512 MB do Fly.
- Com uma /48 de IPv6 (túnel gratuito, se a VPS publicar AAAA) ou alguns proxies IPv4, o total
  vai a dezenas de GiB.

O processo morre por falta de memória (OOM). No `--role=all`, o sondador morre junto e o monitor
de uptime fica cego. O `restart: unless-stopped` sobe o processo de novo com os limitadores
zerados, porque eles vivem em memória, e o atacante repete.

**Por que as defesas não bastam.** O rate limit por IP limita a vazão por origem, não a
concorrência total. Não há semáforo, nem `GOMEMLIMIT`, nem teto de requisições em voo.

**Correção.**
- Pôr um semáforo global em volta de todo `argon2.IDKey`, inclusive o `VerifyDummy`, para manter
  o tempo constante. Tamanho algo como `min(NumCPU, (memória disponível − folga) / 64 MiB)`.
- Adquirir o semáforo com timeout curto. Esgotado o timeout, responder 429 ou 503.
- Considerar m=19–46 MiB com t maior (as faixas da OWASP) e definir `GOMEMLIMIT`.

## S2 — Alto (pressupõe a senha): a leitura do bloqueio e o registro da falha não são atômicos. Tentativas paralelas furam o bloqueio e permitem força bruta do TOTP

**Onde**
- `internal/httpapi/totphandlers.go:131-144`: `ByID` e `IsLocked` são lidos antes.
- `internal/httpapi/totphandlers.go:163-172`: `Verify` e só depois `RecordFailure`.
- O mesmo padrão existe em `internal/auth/user.go:68-84` (`CheckPassword`: lê `u` antes, registra
  a falha depois do Argon2) e em `account.go:159-188`.

**Ataque.** O atacante já tem a senha do dono (vazamento, reúso, phishing), que é exatamente o
caso para o qual o 2FA existe.
1. Faz login, recebe o desafio (vale 5 min e só é consumido quando o código acerta).
2. Dispara K `POST /auth/totp/verify` simultâneos, cada um com um código diferente, a partir de P
   origens (burst 5 por /64 ou por IPv4, então K ≈ 5P).
3. Todas as K requisições executam o `ByID` e veem `locked_until` nulo antes de as cinco
   primeiras gravarem `RecordFailure`. O pool tem 10 conexões e cada requisição faz umas 4 idas
   ao banco em sequência, então a fila do pool alarga a janela em vez de fechá-la.
4. Cada palpite acerta com probabilidade ≈ 3/10⁶ (a tolerância é de ±1 período).
5. Quando o bloqueio de 15 min vence, o próximo `RecordFailure` zera a contagem
   (`user.go:137-154`). O atacante refaz o login, sem falha nenhuma porque a senha está certa,
   pega um desafio novo e dispara outra rajada.

**Números.** Com P = 1.000 origens são cerca de 5.000 palpites a cada 15 min: ~1,5% por ciclo,
~75% em um dia. Com uma /48 IPv6 (65 mil /64), uma rajada só já passa de 50%.

**Por que as defesas não bastam.**
- O próprio comentário em `totphandlers.go:127-130` diz que o bloqueio por conta existe
  justamente contra "um atacante com várias origens". A corrida devolve o limite ao número de
  origens.
- O `ConsumeChallenge` e o `consumirTimestep` só protegem o código que acertou, não os errados.
- A mesma corrida vale para a senha (a janela ali é de ≥120 ms de Argon2). É menos grave com a
  política de 15 caracteres, mas fura o teto de 5 tentativas a cada 15 min numa lista de
  credential stuffing. Na prática o S1 derruba o processo antes.

**Correção.** Tornar "reservar a tentativa" atômico e anterior à verificação. Duas opções:
- Um `UPDATE users SET failed_attempts = failed_attempts + 1, locked_until = CASE … END WHERE id=$1
  AND (locked_until IS NULL OR locked_until <= $now) RETURNING failed_attempts`. Se não voltar
  linha, está bloqueado. Só depois verificar, e no sucesso rodar `ResetFailures`.
- Ou serializar as verificações do mesmo usuário com `SELECT … FOR UPDATE` ou
  `pg_advisory_xact_lock` por usuário durante check, verificação e registro. Para o TOTP é barato.
  Para a senha, reservar antes do Argon2.

## S3 — Médio: um anônimo tranca o dono fora com 5 requisições a cada 15 min, para sempre. O bloqueio também desliga as operações de credencial no painel

**Onde**
- `internal/auth/user.go:20`: `LockThreshold = 5`.
- `internal/httpapi/routes.go:98`: login com burst 5 por IP.
- `internal/httpapi/routes.go:85-92`: o comentário afirma o contrário do que o código faz.

**Ataque.** Quem conhece o e-mail do dono manda 5 senhas erradas de um único IP:
- As 5 cabem no burst e a conta trava 15 min. A cada 15 min, mais 5 (o limitador dá 10/min).
- Isso dá cerca de 20 requisições por hora, com um único IP.

Enquanto isso:
- Nenhum login novo entra, nem com a senha certa. A resposta é o 401 genérico e o bloqueio não
  se anuncia, então o dono nem sabe o motivo.
- `POST /auth/password`, `/totp/enroll`, `/totp/disable`, `/totp/rotate` e `/totp/rotate/confirm`
  recusam todos, porque passam por `CheckPassword` ou por `IsLocked`. O dono sob ataque não
  consegue trocar a senha nem o 2FA pelo painel, mesmo com a sessão aberta.
- O `vigilume user set-password` zera o bloqueio, e o atacante trava de novo em segundos.

**Por que as defesas não bastam.** O comentário em `routes.go:88-92` diz que "o rate limit
responde ANTES … então um atacante remoto é barrado por IP antes de acumular falhas suficientes
para trancar a conta". Isso é falso: o burst (5) é igual ao limiar (5). O documento de
pendências não registra este limite. Sessões já abertas continuam renovando, e é isso que deixa
o achado em Médio, e não em Alto.

**Correção.**
- Contar as falhas por (conta, origem /24 ou /64) em vez de só por conta, mantendo um teto
  global mais alto e lento para a conta.
- Ou usar o "device cookie" da OWASP: o navegador que já autenticou não é afetado pelo bloqueio
  disparado por outras origens.
- Não aplicar o bloqueio global às rotas autenticadas com família viva, e registrar as falhas
  delas num contador próprio.
- Corrigir o comentário de `routes.go`.

## S4 — Médio: `/totp/disable` aceita um código TOTP já consumido. O código usado no login serve de novo para desligar o 2FA

**Onde**
- `internal/auth/totp.go:251`: `validateTOTP` sem `consumirTimestep`.
- Compare com `Verify` (`totp.go:188-189`) e com `StartRotation` (`totp_rotation.go:35-37`, cujo
  comentário diz que "o código observado não pode entrar depois").

**Ataque.** Um phishing com proxy reverso (AiTM), ou alguém olhando por cima do ombro, captura
senha + código TOTP:
1. Retransmite o login e ganha uma sessão. O `Verify` marca o timestep como usado.
2. Dentro da janela de ~90 s chama `POST /auth/totp/disable` com a senha e o MESMO código. O
   `Disable` só valida o código, não confere nem marca `totp_last_timestep`, e aceita.
3. O 2FA fica desligado de vez. Mesmo que o dono encerre a sessão (`DELETE /sessions`), o
   atacante volta a entrar só com a senha.

**Por que as defesas não bastam.** Todo o resto do sistema segue a regra "um código, um uso":
`Verify`, `ChangePassword`, `StartRotation`, e o `Confirm` também grava o timestep. O `Disable` é
a exceção. Exigir a senha não ajuda, porque o atacante do cenário a tem.

**Correção.** No `Disable`, quando `validateTOTP` casar, aplicar a guarda
`totp_last_timestep < $step` na mesma transação (o `UPDATE … WHERE … AND (totp_last_timestep IS
NULL OR totp_last_timestep < $step)`) e recusar com `ErrTOTPInvalid` se não casar.

## S5 — Baixo: `/totp/confirm` (ativação) não conta falhas nem olha o bloqueio

**Onde:** `internal/httpapi/totphandlers.go:64-94`, `internal/auth/totp.go:107-115`.

**Ataque.** Condição: um ladrão de sessão (sem a senha) e um dono que iniciou o cadastro do 2FA e
o abandonou. O segredo pendente fica em `totp_secret_enc`.
- O ladrão chuta os 6 dígitos no `/totp/confirm`. Ali só existe o limitador global de 120/min por
  IP: não há bloqueio, não há `RecordFailure` e não há limite por usuário.
- São cerca de 330 mil tentativas para 50%, viável com várias origens enquanto a sessão durar.
- Se acertar, liga o 2FA com um segredo que o dono talvez nunca tenha escaneado e recebe os 10
  códigos de recuperação.
- O dono fica preso fora até rodar `reset-2fa` no CLI. É o sequestro que o comentário do
  `TOTPEnroll` quis evitar, só que por outra porta.

A probabilidade é baixa porque exige o cadastro abandonado.

**Correção.** Tratar como `TOTPRotateConfirm`: checar `IsLocked` antes e chamar `RecordFailure`
no `ErrTOTPInvalid`.

## S6 — Baixo (armadilha de configuração para a VPS com Caddy): um `TRUSTED_PROXIES` errado falha em silêncio no modo X-Forwarded-For

**Onde:** `internal/httpapi/middleware/clientip.go:56-58` e `internal/config/config.go:270-274`.

**Cenário.** No `compose.yaml` a porta é publicada em `127.0.0.1:8080`, e o Caddy do host fala
com ela. O endereço que o app enxerga é o gateway da bridge do Docker (`172.x.0.1`, via
docker-proxy), e não `127.0.0.1`.
- Se o dono configurar `TRUSTED_PROXIES=127.0.0.1/32`, que é o palpite natural, o
  `X-Forwarded-For` do Caddy é ignorado sem aviso. No modo XFF não há o `avisoDeProxy` que o modo
  `CLIENT_IP_HEADER` tem, e o boot só avisa quando a lista está vazia.
- Todos os clientes caem no mesmo balde. Um único atacante esgota o burst do login (5) para o
  mundo inteiro, o dono incluso, e o S3 fica ainda mais barato.

**Correção.**
- Emitir o mesmo aviso limitado (`avisoDeProxy`) quando chega `X-Forwarded-For` de uma origem
  fora da lista.
- Documentar no runbook da VPS que o proxy confiável é o gateway da rede do Docker, ou pôr o
  Caddy na mesma rede do compose e confiar no IP dele.

---

# Correção

## C1 — Baixo: códigos de recuperação diferenciam maiúsculas e não são normalizados, e o erro de digitação conta no bloqueio

**Onde:** `internal/auth/totp.go:209` e `:257` (`crypto.HashToken(code)` do texto cru). O painel
só remove espaços (`PaginaLogin.tsx:110`, `PaginaConfiguracoes.tsx:125/252/348`).

**Cenário.** O código é base32 maiúsculo, como `ABCD…`. O dono digita em minúsculas no celular,
ou o teclado capitaliza só a primeira letra. O hash não casa, a resposta é 401 e a falha conta no
bloqueio. Com 5 tentativas o dono se tranca fora justamente no caminho de recuperação.

**Correção.** No servidor, antes do hash: `strings.ToUpper`, remover espaços e hífens.

## C2 — Baixo: uma renovação repetida depois de uma resposta perdida é tratada como roubo

**Onde:** `internal/auth/session.go:157-170`.

**Cenário.** O painel manda `POST /auth/refresh` e o servidor comita a rotação, mas a resposta com
o `Set-Cookie` novo se perde (rede móvel, timeout do proxy, aba fechada). Na próxima tentativa o
navegador reenvia o cookie antigo, já marcado como usado. O resultado:
- a família inteira é revogada;
- é gravada a auditoria `session.reuse_detected`, um falso positivo de incidente de segurança;
- o dono é deslogado.

Os Web Locks do painel coordenam as abas, mas não cobrem esta perda.

**Correção.** Dar um período de graça curto (5–10 s): o token usado reapresentado dentro dele,
com o sucessor ainda não usado, devolve o sucessor ou emite outro na mesma família, sem revogar
nada. Se a escolha for manter o rigor, registrar o comportamento nas pendências.

## C3 — Baixo: o logout responde 204 e apaga o cookie mesmo quando a revogação falhou

**Onde:** `internal/httpapi/authhandlers.go:181-189`.

**Cenário.** O banco fica indisponível ou dá timeout durante `POST /auth/logout`. O `Revoke` falha,
o erro só vai para o log, o navegador recebe 204 e o cookie é apagado. A família continua viva
no servidor por até 30 dias. Se o refresh tiver sido copiado, que é o motivo que o próprio
comentário do `Revoke` dá para o logout existir, quem o copiou continua dentro, e o dono acha que
encerrou.

**Correção.** Com erro no `Revoke`, responder 500 (ou 503) e manter o cookie, para o painel avisar
"não foi possível sair" e permitir tentar de novo.

---

# Verificado e descartado (sem cenário explorável)

- **JWT.** `WithValidMethods(HS256)` fecha a confusão de algoritmo. `alg=none` não passa, porque
  o keyfunc devolve `[]byte`. O access token recusa `aud`, e o desafio exige `aud=totp-challenge`,
  então um não serve no lugar do outro. `sub` e `sid` são validados como UUID, e o `exp` é sempre
  emitido.
- **Desafio do 2FA.** É de uso único pela marca d'água (`ConsumeChallenge`, CAS com `<` estrito).
  O carimbo da senha é conferido sob a trava no `Issue`, em tempo constante.
- **Refresh.** 256 bits de `crypto/rand`, guardado como SHA-256. A rotação tem `FOR UPDATE` e a
  trava do usuário antes da linha. A detecção de reúso comita a revogação, e revogar durante a
  rotação não deixa o sucessor vivo.
- **Cookies.** `HttpOnly`, `SameSite=Strict`, `Path=/api/v1/auth`, e `Secure` derivado de
  `PUBLIC_BASE_URL` https (o que é correto atrás do Caddy).
- **CSRF.** `X-Requested-With` obrigatório em toda mutação de `/api/`. O CORS libera uma origem
  exata, e o preflight de uma origem hostil não recebe ACAO.
- **Autorização.** `DELETE /sessions/{id}` e `ListLiveFamilies` filtram por `user_id`. As rotas
  de 2FA exigem a família viva. O access token continuar valendo 15 min nas rotas de recurso
  depois de revogado já está documentado (D2).
- **Criptografia em repouso.** AES-256-GCM com nonce aleatório de 96 bits por selo, e o volume é
  minúsculo, então não há risco de colisão. A AAD amarra tabela, coluna e id de 16 bytes fixos,
  sem ambiguidade. O pendente é decifrado e cifrado de novo com a AAD do ativo. O `ErrAuthFailed`
  é único.
- **Comparações.** Senha e carimbo usam `subtle.ConstantTimeCompare`. O TOTP é comparado em
  tempo constante no pquerna/otp v1.5.0. Os códigos de recuperação são buscados por hash no
  banco, sem oráculo.
- **Corridas de consumo.** O código de recuperação é consumido por `DELETE … RETURNING` e o
  timestep por CAS. O `Enroll`, o `Confirm` e o `Disable` têm a guarda no WHERE. O
  `ConfirmRotation` faz CAS no pendente e na família. A troca de senha e o `set-password` fazem
  CAS ou trava e revogam sob a trava do usuário.
- **Enumeração.** E-mail inexistente e conta bloqueada pagam o Argon2 (`VerifyDummy`). A
  diferença que sobra, um `UPDATE` de ~1 ms contra o jitter do Argon2, não é explorável num
  sistema de usuário único e com bloqueio.
- **Segredos em log e auditoria.** O `Secret` é redigido. Os handlers só logam o erro, nunca o
  corpo. A auditoria só grava ação e `ip_hash`.
- **CLI.** A senha vem só do stdin, sem eco, e nunca por argumento. O `reset-2fa` exige a
  `DATABASE_URL`, e isso é assumido.
- **Painel.** Nenhum token em `localStorage` ou `sessionStorage`.
- **Já registrados nas pendências, não repetidos aqui:** o `verify` concorrente com o mesmo
  desafio gasta o código; o rotate gasta a prova antes da trava; o usuário apagado vira 500; o
  relógio andando para trás trava a marca.
