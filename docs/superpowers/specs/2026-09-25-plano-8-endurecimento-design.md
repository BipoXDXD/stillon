# Vigilume — Plano 8: endurecimento (design)

**Status:** proposta, aguardando revisão do usuário. Nada aqui é código de produto nem plano de
implementação; o plano vem depois da aprovação desta spec.

**Base de código:** `main` em `86ef779` (Plano 7 integrado).

**Origem:** `docs/superpowers/PENDENCIAS-PARA-O-PLANO-8.md` (seção "Da revisão do Plano 7"), a
seção "Herdadas, ainda abertas" do `PENDENCIAS-PARA-O-PLANO-6.md` e os três itens não
bloqueantes do PR #11.

---

## 1. Objetivo

O roteiro original terminou no Plano 7: os cinco subsistemas da spec estão no `main`. O Plano 8
fecha as pendências de **segurança e correção** que sobraram, sem custo externo e sem tocar no
Fly. Ele termina quando cada item abaixo estiver fechado com teste e prova por sabotagem, ou
recusado por escrito, e quando o arquivo de pendências refletir o estado real.

**Critérios de sucesso:**

1. Nenhum contrato da API muda: os mesmos códigos HTTP, os mesmos shapes JSON e os mesmos
   `error.code`. O painel não é tocado.
2. Cada correção tem um teste que fica vermelho quando a correção é desfeita (sabotagem
   registrada no ledger).
3. A suíte completa com `-race` e os gates (`lint`, `gitleaks`, `actionlint`) ficam verdes, e o
   PR só é integrado com **todos** os jobs verdes no GitHub.
4. `PENDENCIAS-PARA-O-PLANO-9.md` substitui o `PENDENCIAS-PARA-O-PLANO-8.md` como estado
   corrente.

## 2. Escopo e fora de escopo

**Dentro (grupo A, decisão do usuário em 2026-09-25):**

| # | Item | Seção |
|---|---|---|
| 1 | Desafio do 2FA de uso único | §3.1 |
| 2 | `ClientIP` normalizado em todas as saídas | §3.2 |
| 3 | Denylist de nomes no `CLIENT_IP_HEADER` | §3.3 |
| 4 | `HashPasswordWith` recusa parâmetros que levam o Argon2 ao panic | §3.4 |
| 5 | `prober.Spec` com `LogValue` que oculta os valores dos headers | §3.5 |
| 6 | Prova de que o `MaxChannels` já é serializado, e correção do R9 | §3.6 |
| 7 | Os três itens não bloqueantes do PR #11 | §3.7 |

**Fora, e continuam nas pendências** (o usuário escolheu só o grupo A):

- **Operação (grupo B):** recuperação do 2FA pelo CLI, troca de autenticador, rota para
  desabilitar canal, porta de métricas em desenvolvimento (M7).
- **Dívidas menores (grupo C):** a seção homônima do `PENDENCIAS-PARA-O-PLANO-5.md`.
- **R2 (grupo D):** migrar `auth.PGStore` e `auth.TOTP` para sqlc.
- Tudo que depende do dono do projeto (Fly, secrets, licença, visibilidade do repositório).

## 3. Desenho por item

### 3.1 Desafio do 2FA de uso único

**Hoje.** O desafio de `POST /auth/login` é um JWT HS256 sem `jti`, com audiência própria,
validade de cinco minutos (`ChallengeTTL`) e o carimbo da senha (`pws`). O `POST
/auth/totp/verify` o aceita quantas vezes vier dentro da validade
(`internal/httpapi/totphandlers.go:97`). O resíduo é estreito, porque cada sessão extra exige um
código TOTP novo (o timestep já é consumido), mas é sessão paralela aberta com um desafio só.

**Decisão do usuário:** marca d'água em `users`, e não tabela de desafios consumidos.

**Desenho.**

1. **Migration `00008_totp_challenge_mark.sql`:**
   `ALTER TABLE users ADD COLUMN totp_challenge_mark timestamptz;` (anulável; `NULL` = nenhum
   desafio consumido). O `Down` remove a coluna.
2. **Claim `cat`** no `challengeJWT`: o instante de emissão em **microssegundos** Unix
   (`int64`). O `iat` não serve: `jwt.NewNumericDate` arredonda para segundos, e dois desafios
   do mesmo usuário no mesmo segundo teriam a mesma marca, com o segundo recusado sem motivo.
   Microssegundo é a resolução do `timestamptz`, então o valor volta do banco sem perda.
3. **`NewChallengeToken`** grava `cat = now.UnixMicro()`. **`ParseChallengeToken`** passa a
   devolver também o `cat` como `time.Time` e recusa com `ErrInvalidToken` um desafio sem `cat`
   ou com `cat <= 0`. Um desafio emitido antes do deploy morre: custa, no pior caso, refazer um
   login que estava no meio (a janela é de cinco minutos).
4. **Consumo atômico**, num método novo do store de usuários (hoje o `auth.PGStore`, escrito à
   mão até o R2):

   ```sql
   UPDATE users SET totp_challenge_mark = $2
    WHERE id = $1 AND (totp_challenge_mark IS NULL OR totp_challenge_mark < $2)
   ```

   Devolve `true` se mudou uma linha. Não precisa de trava explícita: dois `UPDATE` concorrentes
   disputam a mesma linha; o segundo espera o primeiro, relê a linha depois do commit (READ
   COMMITTED) e não satisfaz mais o `WHERE`.
5. **Ordem no `TOTPVerify`:** desafio válido → usuário existe e não está bloqueado → código TOTP
   confere → `ResetFailures` → **consumo** → `issue`. O consumo vem **depois** do código de
   propósito: consumido antes, um código digitado errado queimaria o desafio e obrigaria a
   digitar a senha de novo. O custo dessa ordem é gastar um timestep TOTP numa requisição que
   depois é recusada, o que não abre nada.
6. **Desafio já consumido:** `401 unauthorized`, a mesma resposta de um desafio vencido ou
   adulterado. O código não conta no bloqueio progressivo: quem chega aqui acertou o código.

**Efeito colateral aceito.** Consumir um desafio invalida os desafios **mais antigos** do mesmo
usuário ainda abertos. Dois logins quase simultâneos em dois aparelhos: se o mais novo terminar o
2FA primeiro, o mais antigo recebe 401 e refaz a senha. O inverso funciona.

**Interação com o carimbo de senha.** Nenhuma. O `pws` continua levado a `Issue`, que recusa a
sessão se a senha mudou depois do desafio. A marca é independente e não é zerada pela troca de
senha (não precisa: um desafio com a senha antiga já morre pelo `pws`).

**Comentário a corrigir:** o bloco de `TOTPVerify` (linhas 96–106) que diz "o desafio NÃO é
consumido" passa a descrever a marca.

### 3.2 `ClientIP` normalizado em todas as saídas

**Hoje.** `middleware.ClientIP` tem três saídas. Só a do cabeçalho de um valor só
(`peloCabecalho`) faz `Unmap()` e recusa endereço com zona. As outras duas devolvem a string
crua: o `RemoteAddr` e o último salto do `X-Forwarded-For`. O mesmo cliente IPv4 pode aparecer
como `1.2.3.4` e como `::ffff:1.2.3.4`. O efeito é maior que o registrado nas pendências: além de
dois `ip_hash` na auditoria, são **dois baldes** em cada limitador de requisições, porque a chave
do balde é essa string.

**Desenho.** Uma função `normalizar(s string) (string, bool)`: `netip.ParseAddr`, recusa zona,
devolve `Unmap().String()`. Todas as saídas passam por ela:

- **Último salto do `X-Forwarded-For`** ilegível ou com zona: cai no endereço direto, como já
  acontece hoje com o ilegível.
- **Endereço direto** (`RemoteAddr`) que não é IP (listener de unix socket, `httptest` com
  `pipe`): continua como veio. Não há outro valor para usar, e isso é o comportamento atual.
- **Cabeçalho de um valor só:** usa a mesma função, sem mudança de comportamento.

A função `confiavel` já faz `Unmap()` antes de comparar e não muda.

### 3.3 Denylist de nomes no `CLIENT_IP_HEADER`

**Hoje.** `config.Load` valida que o valor é um nome de cabeçalho HTTP e recusa
`X-Forwarded-For`. Nada impede `Host`, `Cookie`, `Authorization` ou `Forwarded`. Com qualquer
um deles, o app lê um valor que não é IP, cai no endereço do proxy para todo cliente e só avisa
uma vez por minuto no log: o erro de configuração aparece como rate limit coletivo.

**Desenho.** O boot recusa, sem diferenciar maiúsculas, esta lista fechada, com uma mensagem que
nomeia a variável e diz que ela é para o cabeçalho de IP de um valor só que o proxy escreve (como
`Fly-Client-IP`):

- `Forwarded`, e qualquer nome que comece com `X-Forwarded-` (o `X-Forwarded-For` mantém a
  mensagem própria de hoje, que diz para deixar a variável vazia);
- `Host`, `Cookie`, `Authorization`, `Proxy-Authorization`;
- `Connection`, `Upgrade`, `TE`, `Trailer`, `Transfer-Encoding`, `Keep-Alive`;
- `Content-Length`, `Content-Type`.

**Por que denylist e não allowlist.** Uma lista de permitidos (`Fly-Client-IP`,
`CF-Connecting-IP`, `True-Client-IP`, `X-Real-IP`...) seria mais estrita, mas recusaria o
cabeçalho de um proxy legítimo que não esteja na lista, e o usuário teria que mudar código para
trocar de provedor. A denylist pega os erros plausíveis (copiar um nome de cabeçalho comum) sem
fechar a porta.

### 3.4 `HashPasswordWith` com limites

**Hoje.** `HashPasswordWith` existe para os testes construírem hashes com parâmetros antigos e
aceita qualquer valor. `argon2.IDKey` entra em panic com `time = 0`, `threads = 0` ou memória
abaixo de `8 * threads`; o `decodeHash` recusa exatamente esses valores ao ler um hash, mas a
função de escrita não.

**Desenho.** A assinatura passa a `HashPasswordWith(plain string, salt []byte, memory, time
uint32, threads uint8) (string, error)` e recusa, com erro, os mesmos valores que o `decodeHash`
recusa, mais salt vazio. As regras ficam numa função só, chamada pelos dois, para não divergirem.
`HashPassword` repassa o erro. Os dois chamadores de teste (`internal/auth/password_test.go` e
`internal/httpapi/login_race_integration_test.go`) se ajustam.

Não há teto de memória novo: quem chama é código do próprio repositório, e o custo é escolhido
por ele.

### 3.5 `prober.Spec` com `LogValue`

**Hoje.** `prober.Spec` leva os headers do alvo **decifrados** num `map[string]string`. Nenhum
log imprime o `Spec` hoje (conferido: nenhum `slog` recebe um `Spec` inteiro), mas um
`slog.Any("spec", spec)` futuro gravaria no log um `Authorization: Bearer ...` de terceiro.

**Desenho.** `func (s Spec) LogValue() slog.Value` devolvendo um grupo com `url`, `method`,
`timeout`, `expected_status`, `degraded_threshold` e `headers` como a lista **ordenada dos
nomes**, sem os valores. É o mesmo padrão do `crypto.Secret.LogValue` e do `Config.LogValue`. A
URL sai como está: ela já é exibida ao dono na API e não carrega segredo por contrato (userinfo é
recusada no cadastro).

### 3.6 `MaxChannels`: provar a trava e corrigir o registro

**Achado desta spec.** O R9 da spec do Plano 5 afirma que `ChannelService.Create` "conta e insere
sem trava". **Não é verdade**, e o código veio assim desde o Plano 3 (`18e9792`). O limite é
conferido duas vezes: antes do envio de teste, sem trava (para não mandar mensagem a um canal que
não será gravado), e na gravação, dentro da mesma transação do `INSERT`, depois de
`LockUser` (`SELECT id FROM users WHERE id = $1 FOR UPDATE`). A tabela `users` não tem RLS, então
a linha é travada de fato e duas criações do mesmo usuário se serializam. O R9 foi escrito por
analogia com o teto de alvos, sem reprodução.

**O que falta é a prova.** Nenhum teste exercita duas criações simultâneas; o
`TestChannelLimitIsCheckedBeforeSending` trava só a primeira conferência. Sem teste, a trava pode
ser removida por engano.

**Desenho.** Um gancho `depoisDeContar` no `ChannelService`, exposto por `export_test.go`, igual
ao de `internal/targets`. Um teste de integração no molde de `internal/targets/limit_test.go`:
nove canais existentes, duas criações simultâneas que se encontram no gancho (com um prazo de
500 ms para não travar quando a serialização funciona), e no fim exatamente `MaxChannels` canais
no banco. **Sabotagem obrigatória:** com o `LockUser` removido, o teste fica vermelho.

A trava continua sendo o `FOR UPDATE` na linha de `users`, e não passa a ser um
`pg_advisory_xact_lock` como o dos alvos. As duas serializam do mesmo jeito; trocar seria mudança
sem ganho.

### 3.7 Itens não bloqueantes do PR #11

1. **Chaves de demonstração com `https` em loopback.** O código recusa, mas
   `TestLoadRejectsDemoKeysOverHTTPS` só usa `https://vigilume.example`. O teste ganha
   `https://localhost` e `https://127.0.0.1`. Sabotagem: trocar a condição por `!hostLocal`
   derruba só os casos novos.
2. **Tráfego do Fly sem `Fly-Client-IP`.** O passo 9 do runbook (`docs/deploy/fly.md`) diz que
   isso "vai para as pendências", mas o item não está lá. Entra no `PENDENCIAS-PARA-O-PLANO-9.md`,
   em "Dependem do dono do projeto", junto da medição do proxy.
3. **Grep de renomeação.** O `grep` da seção de nomes do runbook também casa nomes antigos em
   comentários de workflows e do `fly.toml`, que a tabela não lista. O plano enumera esses
   comentários e escolhe, um a um, entre tirar o nome do comentário ou acrescentá-lo à tabela. O
   critério é que, depois de trocar os nomes da tabela, o `grep` só ache a própria seção.

## 4. Dados e migrations

- `00008_totp_challenge_mark.sql` (§3.1). Coluna anulável, sem valor padrão e sem reescrita da
  tabela: `ADD COLUMN` anulável sem `DEFAULT` é só catálogo no Postgres 11+.
- O papel comum (`vigilume_app`) já é dono de `users`; nenhum `GRANT` novo.
- Nenhuma query sqlc nova: o consumo fica no `auth.PGStore`, que é escrito à mão até o R2.

## 5. Mapa de arquivos

| Arquivo | O que muda |
|---|---|
| `internal/storage/migrations/00008_totp_challenge_mark.sql` (novo) | Coluna da marca |
| `internal/auth/token.go` | Claim `cat`; `ParseChallengeToken` devolve o instante |
| `internal/auth/store.go` | Método de consumo da marca |
| `internal/httpapi/totphandlers.go` | Consumo no `TOTPVerify`; comentário corrigido |
| `internal/httpapi/authhandlers.go` | Interface do store, se o método novo precisar entrar nela |
| `internal/httpapi/middleware/clientip.go` | `normalizar` em todas as saídas |
| `internal/config/config.go` | Denylist do `CLIENT_IP_HEADER` |
| `internal/auth/password.go` | `HashPasswordWith` com erro e limites comuns ao `decodeHash` |
| `internal/prober/probe.go` | `Spec.LogValue` |
| `internal/notify/service.go`, `internal/notify/export_test.go` | Gancho `depoisDeContar` |
| `internal/config/config_test.go` | Casos de `https` em loopback e da denylist |
| `docs/deploy/fly.md`, workflows, `fly.toml` | Grep de renomeação (§3.7) |
| `docs/superpowers/PENDENCIAS-PARA-O-PLANO-9.md` (novo) | Estado corrente |
| Testes | Um por item, listados na §6 |

## 6. Testes

Cada linha tem uma sabotagem que o ledger registra com o resultado.

| Item | Teste | Sabotagem |
|---|---|---|
| 3.1 | Reusar o desafio com um código TOTP novo dá 401 | Tirar a chamada de consumo |
| 3.1 | Dois `verify` concorrentes com o mesmo desafio e códigos diferentes: uma sessão, um 401 | Trocar `<` por `<=` no `WHERE`, e depois tirar a condição |
| 3.1 | O desafio mais novo consumido derruba o mais antigo; o inverso funciona | Consumir antes do código |
| 3.1 | Código errado não consome o desafio: o mesmo desafio com o código certo depois abre sessão | Consumir antes do código |
| 3.1 | Desafio sem `cat` é recusado por `ParseChallengeToken` | Aceitar `cat` ausente |
| 3.1 | Dois desafios emitidos no mesmo segundo têm `cat` diferentes, e os dois funcionam em ordem | Usar `iat` no lugar de `cat` |
| 3.2 | `::ffff:1.2.3.4` pelo `RemoteAddr` e pelo último salto do `X-Forwarded-For` dá `1.2.3.4` | Tirar o `Unmap` de cada caminho |
| 3.2 | Mesmo cliente por `1.2.3.4` e `::ffff:1.2.3.4` cai no mesmo balde do limitador | Idem |
| 3.2 | Último salto com zona (`fe80::1%eth0`) cai no endereço direto | Tirar a recusa de zona |
| 3.3 | Cada nome da denylist, em caixas variadas, é recusado no boot; `Fly-Client-IP` passa | Esvaziar a lista |
| 3.4 | `time = 0`, `threads = 0`, memória `< 8*threads` e salt vazio dão erro, sem panic | Tirar a checagem |
| 3.5 | `slog` num buffer com um `Spec` cujo header tem um valor-canário: o nome aparece, o canário não | Devolver o map inteiro |
| 3.6 | Duas criações simultâneas com nove canais: dez no fim | Tirar o `LockUser` |
| 3.7 | `https://localhost` e `https://127.0.0.1` recusam as chaves de demo | Trocar a condição por `!hostLocal` |

**Regressão.** A suíte inteira com `-race` nos Postgres 17 e 18 (matriz do CI), o E2E do compose
e o `checar-imagem`. O login com 2FA do E2E do painel continua passando sem mudança, o que
confirma o critério 1 da §1.

## 7. Segurança

- §3.1 fecha a sessão paralela com um desafio só. O desafio continua sem estado próprio; o único
  estado novo é um instante por usuário.
- §3.2 fecha dois baldes e dois `ip_hash` por cliente IPv4 em pilha dupla.
- §3.3 transforma um erro de configuração silencioso em falha no boot.
- §3.4 e §3.5 são robustez: nenhum dos dois está explorável hoje.
- §3.6 não muda código de produção; troca uma afirmação falsa por uma prova.

## 8. Riscos

| # | Risco | Tratamento |
|---|---|---|
| R1 | Desafios em trânsito no deploy morrem (não têm `cat`) | Aceito: no pior caso, um login refeito dentro de cinco minutos |
| R2 | Relógio do processo andando para trás entre dois desafios do mesmo usuário deixa o mais novo com `cat` menor | Aceito: a consequência é um 401 e um login refeito; o `clock` do projeto é o do sistema |
| R3 | A denylist recusa um cabeçalho que algum proxy real use para o IP do cliente | Nenhum dos nomes da lista carrega IP de cliente em proxy conhecido; se aparecer, é uma linha a menos |
| R4 | Normalizar o `RemoteAddr` muda a chave de balde de quem hoje chega como `::ffff:` | Desejado; o efeito é um balde a menos por cliente, e os baldes são em memória (zeram no deploy) |
| R5 | O teste concorrente do §3.6 oscila sob carga | O mesmo desenho do `limit_test.go` dos alvos, que não oscilou no CI; o prazo do gancho só encurta o teste quando a trava funciona |

## 9. Execução

Um branch `plano-8-endurecimento` a partir do `main`. Os itens têm arquivos quase disjuntos, e o
plano pode rodá-los em worktrees paralelos: §3.1 (auth + httpapi + migration), §3.2 com §3.3
(middleware + config), §3.4 (auth/password), §3.5 (prober), §3.6 com §3.7 (notify + docs). O
§3.1 e o §3.4 tocam o pacote `auth` em arquivos diferentes, e o `HashPasswordWith` é chamado por
um teste do `httpapi`: a integração é em sequência, com a suíte rodando a cada passo. Revisão
final do branch, PR, e merge só com todos os jobs verdes no GitHub.
