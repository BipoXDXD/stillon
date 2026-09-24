# Vigilume — Plano 5: lacunas da API para o painel (design)

**Status:** proposta, aguardando revisão do usuário. Nada aqui é código de produto nem plano de
implementação; o plano vem depois de as três specs (5, 6 e 7) serem aprovadas.

**Contrato:** `docs/superpowers/specs/2026-09-24-planos-5-6-7-contrato.md`. Os shapes JSON e as
decisões de lá valem como estão. Onde esta spec discorda, a discordância está na §11, e o
desenho segue o contrato até a reconciliação.

**Spec do projeto:** `docs/superpowers/specs/2026-09-22-vigilume-design.md` (§2.1, §5.2, §5.3,
§5.5, §9, §9.1, §11.2, §11.3, §17).

**Base de código:** `main` em `a008b01` (Plano 4 integrado).

**Validação prévia.** Duas perguntas desta spec foram respondidas com medição, e não com
opinião, num PostgreSQL 16 descartável (`postgres:16-alpine`, fora do repositório, com as seis
migrations reais aplicadas pelo papel comum, dono das tabelas, com a RLS valendo):

1. **O custo do `/targets/summary`** (§4.8 e Apêndice A).
2. **Uma corrida entre revogar uma família e rotacioná-la** (§4.7 e Apêndice B). A corrida
   existe hoje, no código do Plano 1, e afeta o logout e a detecção de reúso; o Plano 5 a
   tornaria pior, porque passa a revogar famílias por id, sem ter o token delas na mão.

---

## 1. Objetivo

Fechar o que o painel (Plano 6) precisa e a API não tem:

- criar o primeiro usuário e recuperar a senha sem rota pública (`vigilume user create` e
  `vigilume user set-password`);
- dizer ao painel quem está logado e se o 2FA está ligado (`GET /me` enriquecido);
- trocar a senha por dentro (`POST /auth/password`);
- listar e encerrar sessões (`GET /sessions`, `DELETE /sessions/{id}`);
- montar o dashboard numa chamada (`GET /targets/summary`).

E fazê-lo sem abrir nenhuma das portas que os Planos 1–4 fecharam: enumeração por tempo, lockout
anunciado, senha em argumento, vazamento de IP, recurso alheio respondendo 403.

## 2. Escopo e fora de escopo

**Dentro:**

| Item | Onde |
|---|---|
| Subcomandos `user create` e `user set-password` | `cmd/vigilume` + pacote novo `internal/account` |
| `GET /me` com `email`, `totp_enabled`, `created_at` | `internal/httpapi` + `internal/account` |
| `POST /auth/password` | idem |
| `GET /sessions`, `DELETE /sessions/{id}` | idem |
| `GET /targets/summary` | `internal/targets` + `internal/httpapi` |
| Claim `sid` no access token (para o `current` de `/sessions`) | `internal/auth/token.go`, `middleware/authn.go` |
| Serialização por usuário das mutações de sessão (corrige a corrida da §4.7) | `internal/auth/session.go`, `store.go` |
| `auth.CheckPassword`: a verificação de senha do login, extraída e reusada | `internal/auth` |
| Regra única do estado atual de um alvo (página pública e summary) | pacote novo `internal/targetstate` |
| Migration 00007 (um índice) e as queries sqlc novas | `internal/storage` |

**Fora (e para onde vai):**

- **R2**, migrar `auth.PGStore`/`auth.TOTP` para sqlc: plano próprio (contrato, item 5). Os
  dois métodos novos do `pgTx` são sqlc (D27); o resto do `PGStore` fica como está.
- **Rota de cadastro, "esqueci a senha" por e-mail, troca de e-mail:** fora (contrato, item 1).
- **Pendências herdadas que o `PENDENCIAS-PARA-O-PLANO-5.md` lista e o contrato não trouxe
  para cá:** `jti`/`challenge_id` do desafio de 2FA, troca de autenticador, `HashPasswordWith`
  fora de faixa, `LogValue` de `Spec.Headers` (M6), porta de métricas em dev (M7), rota de
  desabilitar canal, `METRICS_ADDR` no Fly. Continuam na lista, para o plano que as pegar. As
  que são de tela (incidentes, canais, status page, gráficos, textos de ajuda) são do Plano 6.
- **Leitura do `audit_log` pela API:** não existe e não passa a existir.
- **Checagem de família viva em toda rota autenticada:** proposta em D4 como decisão do
  usuário; o desenho padrão não a inclui.
- **Blocklist de senhas (NIST):** fora, por causa do vocabulário fechado do contrato (§11, Div. 1).

## 3. Decisões

Cada uma com a alternativa e o custo de estar errada. As marcadas com ★ estão também na §10,
para segundo olhar.

| # | Decisão | Alternativa | Custo se errada |
|---|---|---|---|
| D1 ★ | **Política de senha nova: 15 a 128 caracteres**, contados em *code points* (`utf8.RuneCountInString`); qualquer caractere, inclusive espaço e Unicode; nenhuma regra de composição; nenhum `trim`. Vale para senha **nova** (`user create`, `set-password`, `POST /auth/password`); o login continua aceitando a senha antiga mais curta | 8 (NIST para MFA); 12 (meio-termo) | Com 15, quem digita à mão acha chato; com menos, a senha é fraca justamente no caso sem 2FA. Mudar o número é uma constante |
| D2 | **Sem normalização Unicode** da senha | NFC (NIST 800-63B-4 §3.1.1.2, *SHOULD*) | Quem usa acento digitado de formas diferentes em dois aparelhos não entra. Acrescentar NFC depois exige verificar as duas formas no login durante a transição |
| D3 | **Access token ganha o claim `sid`** = `family_id` da sessão que o emitiu. **Obrigatório**: token sem `sid` é token inválido | Ler o cookie de refresh em `/sessions` | O cookie tem `Path=/api/v1/auth` e não chega em `/api/v1/sessions`; mudar o `Path` alarga a exposição do refresh. Não há painel em produção, então nenhum token vivo fica sem `sid` |
| D4 ★ | **Rotas de credencial e sessão exigem a família do token viva** (`POST /auth/password`, `GET /sessions`, `DELETE /sessions/{id}`); família morta é `401 unauthorized`. As demais rotas mantêm a janela de até 15 min da spec §5.2 | Checar em toda rota (`Authn` consulta o banco) | Uma sessão encerrada (ou de quem teve a senha trocada) continua lendo e **mexendo em alvos, canais e status page** por até 15 min. É o preço do JWT sem estado que a spec escolheu; a alternativa custa uma consulta indexada por requisição e mexe em todo teste que cunha token à mão |
| D5 | **Mutação de sessões serializada por usuário**: `pg_advisory_xact_lock(hashtextextended('vigilume.sessions:' \|\| user_id, 0))` antes de tocar em linha de `sessions`, em **toda** mutação: `Rotate`, `Revoke` (logout), troca de senha, `DELETE /sessions/{id}` e `set-password`. `Rotate` e `Revoke` descobrem o `user_id` numa leitura sem trava e só então travam | `SELECT … FOR UPDATE` na linha viva; tabela `session_families` | Sem a trava, o sucessor inserido por uma rotação concorrente **escapa** da revogação (provado, Apêndice B). Com trava por linha, a linha viva muda no meio. A tabela de famílias é o modelo certo a longo prazo, mas mexe no `Issue`/`Rotate` além do que o R2 quer mexer |
| D6 | Ordem de `POST /auth/password`: **política → família viva (leitura) → bloqueio → senha atual → TOTP → hash novo → transação** (trava do usuário, família viva de novo, CAS do hash, revoga as outras, auditoria) | Verificar a senha antes da política | A política é pública: validá-la primeiro não vaza nada e não gasta Argon2 nem conta falha por erro de digitação na senha nova |
| D7 | **Falhas na troca contam no bloqueio progressivo** (o mesmo `failed_attempts` do login, como o TOTP no R40); conta bloqueada responde o mesmo `401 invalid_credentials`, pagando `VerifyDummy`; sucesso zera o contador | Não contar | Sem contar, um access token roubado vira oráculo de senha limitado só por IP |
| D8 | **CAS no hash**: `UPDATE users SET password_hash = $novo … WHERE id = $1 AND password_hash = $verificado`. Zero linhas = `401 invalid_credentials` | `SELECT … FOR UPDATE` antes de verificar | Duas trocas simultâneas com a mesma senha atual: a segunda gravaria por cima da primeira. O CAS evita segurar trava durante o Argon2 |
| D9 | **TOTP na troca usa `auth.TOTP.Verify`**: aceita código do app ou de recuperação (consumido e auditado como hoje); `totp_code` ausente com 2FA ligado é código errado; `totp_code` com 2FA desligado é ignorado | Só código do app | Quem perdeu o celular desliga o 2FA com o código de recuperação primeiro. Aceitar aqui só evita um passo; não abre nada que `Verify` já não aceite no login |
| D10 | A troca **mantém a família atual sem rotacionar** o refresh | Emitir refresh novo na resposta | O contrato fixa `204` sem corpo; a família atual já provou senha e 2FA nesta requisição |
| D11 | `POST /auth/password` usa **a mesma instância** do limitador do login (10/min, burst 5, por IP), não uma cópia com os mesmos números | Instância própria | Com instâncias separadas, quem adivinha senha dobra o orçamento alternando as duas rotas |
| D12 ★ | **`auth.CheckPassword`**: bloqueio + `VerifyDummy` + `VerifyPassword` + `RecordFailure`, extraído do `Login` e usado por login, troca de senha, **`/totp/enroll` e `/totp/disable`**; as duas rotas de TOTP passam também pelo limitador do login | Deixar o `senhaConfere` como está | Hoje `senhaConfere` **não conta falha**: com um access token roubado, `/totp/enroll` testa senhas a 120/min por IP sem nunca travar a conta. Com a troca de senha fechada, essa vira a porta mais fraca |
| D13 | **`users` e `sessions` continuam sem RLS** (D7 do Plano 2). A barreira é o `WHERE user_id = $1` em toda query nova, com `userID` como primeiro parâmetro depois de `ctx`, e a prova por sabotagem de cada um | RLS com um escopo "auth" | Login e refresh buscam por e-mail e por hash **antes** de existir usuário na transação: o escopo teria de enxergar tudo, e a política viraria cerimônia |
| D14 | `GET /sessions`: família viva = tem linha com `used_at IS NULL AND revoked_at IS NULL AND expires_at > agora`. `created_at` = **instante do UUIDv7 da família**; `last_used_at` = `created_at` da linha viva (o último refresh); `user_agent` da linha viva, cortado em 256 *code points*; ordem `created_at DESC, id DESC` | `created_at` = menor `created_at` das linhas | A retenção apaga linhas com mais de 37 dias de uma família que continua viva (cada rotação estende o prazo): o mínimo andaria para a frente. O id v7 não muda |
| D15 | `DELETE /sessions/{id}`: **404 para toda família que não aparece em `GET /sessions`** (inexistente, de outro usuário, já revogada, vencida, id malformado); viva → revoga, `204`. Se for a atual, a resposta **também apaga o cookie de refresh** | `204` idempotente para família própria já morta | Um duplo clique recebe 404 no segundo; o Plano 6 trata 404 como "já encerrada" (§12) |
| D16 | CLI: **senha só pelo stdin.** Terminal: sem eco (`golang.org/x/term`) e com confirmação. Pipe: uma linha, até 4 KiB, remove **um** `\n` ou `\r\n` final; mais de uma linha é erro. Nenhuma flag e nenhuma variável de ambiente de senha | `--password`; `VIGILUME_PASSWORD` | Argumento aparece em `ps` e no histórico do shell; variável aparece em `/proc/<pid>/environ`, `docker inspect` e arquivos de compose |
| D17 | CLI lê **só `DATABASE_URL`**, e faz o que o boot faz: `CheckRole` → `Migrate` → operação. Superusuário ou `BYPASSRLS`: recusa (código 1) | Usar `config.Load` inteiro; pular `CheckRole` | Rodar o CLI antes do primeiro boot com o DSN de admin criaria as tabelas com o dono errado, e o papel comum não conseguiria usá-las (D8 do Plano 2) |
| D18 ★ | `user create` **recusa se já existe qualquer usuário** (spec §2.1: usuário único), código 3 | Recusar só o mesmo e-mail | Um segundo usuário exige SQL. Mas o `compose` que mudar o e-mail na env não cria um segundo dono em silêncio |
| D19 | `set-password`: hash novo, **zera o bloqueio, revoga todas as famílias**, audita; **não mexe no TOTP**. E-mail não cadastrado: código 4 | Manter as sessões | `set-password` é o caminho de recuperação, e recuperação quase sempre é "perdi" ou "vazou": manter sessões deixaria o invasor dentro |
| D20 | `normalizarEmail` vira **`auth.NormalizeEmail`**, usada pelo login e pelo CLI (a `PENDENTE` do `authhandlers.go` pede isso). O CLI também exige endereço puro (`net/mail`, sem nome de exibição, ≤ 254 bytes) | Validar só no CLI | Duas normalizações diferentes criam o e-mail que cadastra e não entra |
| D21 | **Regra única do estado atual** num pacote puro, `internal/targetstate`, usada pela página pública (`statuspage.estado` passa a delegar) e pelo summary | Copiar a regra | Duas cópias divergem, e o dashboard diria "no ar" onde a página pública diz "sem dados" |
| D22 | Summary **híbrido**: `spark` lê `check_rollup_1h` até a marca d'água do dono e `checks` depois dela; **`uptime_24h` = as horas do `spark` mais a hora corrente** | Só rollups; só `checks` | Só rollups: a última hora fechada falta por até 15 min e o uptime não vê uma queda em curso ("fora" com "100%"). Só `checks`: ~390 ms com 100 alvos, a cada 15 s por painel aberto (Apêndice A) |
| D23 | `last_latency_ms` é `null` quando não há check **ou quando o último foi `down`** | A latência do down | A latência de um `down` é o tempo até o timeout ou a recusa, não a do serviço; os rollups já a excluem (D5 do Plano 4) |
| D24 ★ | **Teto de 100 alvos por usuário** (= `targets.MaxPageSize`), com `409 target_limit_reached` no `POST /targets` | Sem teto | É acréscimo ao contrato (§11, Div. 2). Sem teto, o summary cresce linear em tempo (~0,3 ms/alvo) e em tamanho (~1,8 KB/alvo a cada 15 s) |
| D25 | Auditoria: `user.created`, `user.password_reset` (CLI, sem `ip_hash`), `user.password_changed`, `session.revoked` (`entity = 'session_family'`, `entity_id` = família) | Auditar também tentativas falhas | O login não audita falha; o bloqueio já é o registro. Coerência |
| D26 | `GET /me` lê numa query sqlc; usuário que sumiu é `401 unauthorized`; a rota ganha o limite por usuário das rotas de recurso | 404 | Não há caminho de apagar usuário; se houver, o painel volta ao login em vez de mostrar erro |
| D27 | **Todo SQL novo é sqlc**, inclusive o que entra no `pgTx` (`LockUserSessions`, `SessionUserByHash`): o método chama `db.New(t.tx)` | SQL à mão no `PGStore` | Contrato, item 5 |
| D28 | Pacote novo **`internal/account`**: o serviço de conta que o CLI e a API chamam (criar, redefinir, trocar senha, `Me`, listar e revogar sessões) | Espalhar por `auth` e `httpapi` | A política de senha e a normalização de e-mail têm de ser as mesmas no CLI e na API |
| D29 | Dependência nova: **`golang.org/x/term`** | `syscall` à mão | Mesmo mantenedor do `x/crypto`, que já está no `go.mod`; ler sem eco à mão é código de plataforma |

## 4. Desenho por item

### 4.1 CLI: `vigilume user create` e `vigilume user set-password`

**Forma.** `main` olha `os.Args[1]` antes do `flag.Parse` global: se for `user`, desvia para
`runUser(ctx, args, stdin, stdout, stderr, getenv) int`, que devolve o código de saída e é o que
os testes chamam. Subcomandos com `flag.NewFlagSet(…, flag.ContinueOnError)`; a única flag é
`--email`. Argumento posicional sobrando ou flag desconhecida: código 2.

**Sequência.**

1. Valida o e-mail (`auth.NormalizeEmail` + endereço puro, D20). Inválido: código 2.
2. Lê `DATABASE_URL` (e só ela, D17). Ausente: código 2.
3. `storage.NewPool` → `storage.CheckRole` → `storage.Migrate`. Qualquer falha: código 1, com a
   mensagem do erro (a de `CheckRole` já diz o que fazer).
4. Pré-condição, **antes** de pedir a senha, para ninguém digitar duas vezes à toa:
   `create` com usuário existente → código 3; `set-password` com e-mail inexistente → código 4.
5. Lê a senha (D16) e aplica a política (D1). Fora: código 2, com `muito curta (mínimo 15)` ou
   `muito longa (máximo 128)`; confirmação diferente: código 2.
6. `auth.HashPassword` fora de transação (o Argon2 leva ~100 ms e não segura trava).
7. Transação:
   - `create`: `pg_advisory_xact_lock('vigilume.user_create')`, reconta os usuários (fecha a
     corrida de dois CLIs), `InsertUser`, auditoria `user.created`. Violação de unicidade
     que ainda escape: código 3.
   - `set-password`: trava do usuário (D5), `ResetPasswordHash` (hash, `failed_attempts = 0`,
     `locked_until = NULL`), `RevokeAllFamilies`, auditoria `user.password_reset`.
8. Sucesso: código 0 e uma linha no stdout, sem a senha:
   `usuário criado: dono@exemplo.com (id …)` ou
   `senha redefinida; 3 sessões encerradas. Acessos já abertos expiram em até 15 minutos.`

**Leitura da senha.**

- `term.IsTerminal(fd do stdin)`: *prompt* no **stderr** (`Senha: `, `Confirme: `), leitura com
  `term.ReadPassword`, `\n` no stderr depois de cada uma (o terminal não ecoa o Enter).
- Senão (pipe, arquivo, `docker compose exec -T`): `io.LimitReader(stdin, 4096+1)`; mais de 4096
  bytes é erro (a política já corta em 128 *code points* = 512 bytes no pior caso; o teto de
  leitura só impede ler um arquivo gigante por engano). Remove **um** `\n` ou `\r\n` final; se
  sobrar `\n` ou `\r`, é "a senha tem mais de uma linha" (código 2): pegar só a primeira linha
  de um arquivo errado seria criar um usuário com uma senha que ninguém sabe.
- UTF-8 inválido: código 2 (a API nunca vê isso, porque o `encoding/json` troca por U+FFFD).
- Stdin vazio: `muito curta`.
- A senha vive como `[]byte` até virar `string` para o hash; não há como zerar `string` em Go, e
  a spec não finge que há.

**Códigos de saída.**

| Código | Quando |
|---|---|
| 0 | Criado / redefinida |
| 1 | Ambiente: banco inalcançável, papel com `rolsuper`/`rolbypassrls`, migration falhou |
| 2 | Uso: flag, e-mail inválido, `DATABASE_URL` ausente, senha fora da política, confirmação diferente, mais de uma linha |
| 3 | `create`: já existe usuário (nada foi alterado) |
| 4 | `set-password`: e-mail não cadastrado (nada foi alterado) |

O 3 existe para o Plano 7: um `compose` que roda `user create` a cada `up` trata 3 como sucesso
sem precisar de flag `--if-not-exists`.

**Papel do banco.** `users`, `sessions` e `audit_log` não têm RLS (D13), então o papel comum,
sem `BYPASSRLS`, faz tudo o que o CLI precisa sem escopo nenhum. O `CheckRole` não é para ler
essas tabelas: é para que o `Migrate` do passo 3, num banco virgem, crie as tabelas com o dono
certo.

### 4.2 `GET /me`

Query `GetAccount` (sqlc): `id, email, (totp_confirmed_at IS NOT NULL) AS totp_enabled,
created_at`. `totp_enabled` é o fator **confirmado**; um cadastro iniciado e não confirmado não
conta (é o mesmo critério de `TOTP.IsEnabled`). O `email` sai como gravado (o `citext` preserva a
caixa). Sem linha: `401 unauthorized` (D26).

### 4.3 Identificar a família atual: claim `sid`

`auth.NewAccessToken(secret, userID, familyID, now)` passa a gravar `sid` = `familyID` (o claim
`sid` é o "Session ID" registrado na IANA pelo OpenID Connect). `auth.ParseAccessToken` devolve
`auth.AccessClaims{UserID, FamilyID}` e recusa token sem `sid` ou com `sid` que não é UUID
(D3). `middleware.Authn` põe os dois no contexto; `middleware.FamilyID(ctx)` é o acessor novo.

`grantSession` já recebe a `auth.Session` de `Issue`/`Rotate`, que tem `FamilyID`; é só passá-la.

O desafio de TOTP (`NewChallengeToken`) não muda: não é access token e não tem sessão.

**Família viva** é o predicado que D4 aplica (`FamilyIsLive`):

```sql
SELECT EXISTS (SELECT 1 FROM sessions
                WHERE user_id = $1 AND family_id = $2
                  AND used_at IS NULL AND revoked_at IS NULL AND expires_at > $3);
```

`$3` é o relógio injetado (o mesmo que `Rotate` usa), nunca `now()` do banco.

### 4.4 `POST /auth/password`

**Cadeia:** `limiteLogin → authn → handler` (D11). `X-Requested-With` já é exigido pela cadeia
global de mutações.

**Handler → `account.Service.ChangePassword(ctx, userID, familyID, atual, nova, totp, meta)`**,
na ordem de D6:

1. `auth.ValidateNewPassword(nova)` → `400 validation_failed` com
   `fields: [{"field": "new_password", "reason": "too_short" | "too_long"}]`.
2. `FamilyIsLive(userID, familyID, agora)` → falso: `401 unauthorized`. Leitura sem trava: é o
   atalho barato; a verdade é conferida de novo no passo 7.
3. `users.ByID`. Não achou: `401 unauthorized`.
4. `auth.CheckPassword(user, atual, agora)` (D12): bloqueada → `VerifyDummy` e falso; errada →
   `RecordFailure` e falso. Falso: `401 invalid_credentials`.
5. 2FA ligado → `totp.Verify(userID, totp_code, agora)`; `ErrTOTPInvalid`/`ErrTOTPNotEnrolled`
   → `RecordFailure` e `401 invalid_credentials` (D7, D9).
6. `auth.HashPassword(nova)` fora da transação.
7. Transação: `LockUserSessions(userID)` (D5) → `FamilyIsLive` de novo (falso: rollback e
   `401 unauthorized`) → `ChangePasswordHash(userID, novoHash, hashVerificado)` (CAS, D8; zero
   linhas: rollback e `401 invalid_credentials`; também zera `failed_attempts` e
   `locked_until`) → `RevokeOtherFamilies(userID, familyID, agora)` → auditoria
   `user.password_changed` com `ip_hash`.
8. `204`, sem corpo e sem `Set-Cookie`.

**Tempo constante.** Os três caminhos de falha que importam (bloqueada, senha errada, senha certa
com TOTP errado) pagam exatamente **um** Argon2id. A diferença que sobra é o TOTP (µs) e uma
consulta a mais no terceiro caminho (~1 ms, contra a variância de ~100 ms do Argon2). O teste
segue o de `TestLoginSpendsTheSameCPUWhetherOrNotTheAccountExists`: relativo à verificação real,
menor de duas medidas. O sucesso paga dois Argon2 (verificar e gerar); não é oráculo, porque o
próprio `204` diz que deu certo.

**As outras famílias** caem na mesma transação que troca o hash. Os access tokens delas ainda
valem até 15 min nas rotas de recurso (D4); nas de credencial e sessão, não.

**A atual revogada ao mesmo tempo.** Três casos, todos serializados pela trava do usuário:

- Outra aba ou aparelho faz `DELETE /sessions/{atual}` **antes** do passo 7: o passo 7 vê a
  família morta, desfaz tudo e responde `401 unauthorized`. A senha **não** muda: o `204` sempre
  significa "trocada e a atual continua viva", que é o que o contrato promete.
- O mesmo `DELETE` chega **depois** do commit: a senha mudou e a família cai depois. Dois eventos
  em ordem, cada um correto.
- Detecção de reúso na família atual (alguém apresentou um refresh já usado): mesmo tratamento,
  porque `Rotate` também pega a trava do usuário.

Efeito colateral aceito: no primeiro caso, se o 2FA está ligado, o timestep do código já foi
consumido no passo 5 (o `Verify` grava fora da transação). Quem tentar de novo espera o próximo
código de 30 s.

### 4.5 `GET /sessions`

Cadeia `protegida` (authn + limite por usuário). Handler exige família viva (D4) e chama
`account.Service.ListSessions(ctx, userID, familyID)`:

```sql
-- name: ListLiveFamilies :many
SELECT family_id, created_at AS last_used_at, coalesce(user_agent, '') AS user_agent
  FROM sessions
 WHERE user_id = $1 AND used_at IS NULL AND revoked_at IS NULL AND expires_at > $2;
```

O serviço monta cada item: `id` = `family_id`; `created_at` = `family_id.Time()` (UUIDv7, D14);
`last_used_at`; `user_agent` cortado em 256 *code points*; `current` = `family_id == familyID`
do token. Ordena por `created_at DESC, id DESC` e devolve `{"items": [...]}` (lista vazia, e
não `null`, se por algum motivo não houver nada).

A invariante "no máximo uma linha viva por família" vem de `Rotate` marcar a antiga e inserir a
nova na mesma transação. O serviço **não** depende dela: se aparecerem duas linhas vivas da
mesma família, fica a de `created_at` maior.

Nada de `ip_hash`, `token_hash`, `expires_at` nem id de linha: o DTO é construído campo a campo,
e o teste procura esses nomes e os bytes do hash no corpo.

`last_used_at` tem a granularidade do refresh: um painel ativo renova a cada ~15 min. O painel
deve dizer "último acesso" com essa folga.

### 4.6 `DELETE /sessions/{id}`

Cadeia `protegida`; família do token viva (D4). `{id}` malformado: `404` (D16 do Plano 2).

`account.Service.RevokeSession(ctx, userID, familyID, alvo, meta)`, numa transação:
`LockUserSessions(userID)` → `FamilyIsLive(userID, alvo, agora)` → falso: `404` → verdadeiro:
`RevokeFamilyOfUser(userID, alvo, agora)` (todas as linhas não revogadas da família, como o
`RevokeFamily` de hoje) e auditoria `session.revoked`. `204`.

**Revogar a própria família** (alvo = `sid` do token): tudo acima, e a resposta leva
`Set-Cookie: vigilume_refresh=; Path=/api/v1/auth; Max-Age=0; HttpOnly; Secure; SameSite=Strict`
(o `clearCookie` de hoje). O navegador aceita apagar um cookie de outro `Path` a partir de
`/api/v1/sessions/…`: o atributo `Path` não é validado contra o caminho da requisição (RFC 6265
§5.2.4). O access token em mãos continua valendo até 15 min nas rotas de recurso (D4), e **é o
painel que tem de descartá-lo** (§12).

### 4.7 Serialização por usuário (corrige uma corrida existente)

**O defeito.** Em READ COMMITTED, `UPDATE sessions SET revoked_at = … WHERE family_id = F AND
revoked_at IS NULL` fica esperando a linha viva `R1` se uma rotação a travou. A rotação insere o
sucessor `R2`, marca `R1` usada e comita. O `UPDATE` então revoga `R1`, mas **não vê `R2`**, que
entrou depois do seu *snapshot*. A família "revogada" continua viva por `R2`. Reproduzido no
spike (Apêndice B): `linhas vivas da família revogada = 1`.

Hoje isso já afeta:

- a **detecção de reúso** (`Rotate` com token usado revoga a família, e o dono pode estar
  rotacionando a linha viva no mesmo instante);
- o **logout** apresentado com um refresh antigo quando o ladrão está rotacionando.

E afetaria tudo o que o Plano 5 acrescenta: `DELETE /sessions/{id}` contra um invasor que
renova em laço, e a troca de senha que "revoga as outras".

**A correção (D5).** Toda transação que muda linhas de `sessions` de um usuário toma primeiro a
advisory lock de transação daquele usuário. `Rotate` e `Revoke` não sabem o usuário antes de ler
a linha, então:

1. `SessionUserByHash(hash)` — leitura sem trava; não achou: `ErrInvalidSession` (ou `nil`, no
   logout, R28).
2. `LockUserSessions(userID)`.
3. `LockSessionByHash(hash)` como hoje, e **toda** a lógica de hoje sobre a linha relida.

Como todo mundo pega a advisory lock **antes** de qualquer trava de linha, não há ciclo e não há
*deadlock*. Com a trava, a rotação concorrente termina antes de o `UPDATE` começar, e o
*snapshot* do `UPDATE` inclui `R2` (Apêndice B: `= 0`).

**Custo:** uma consulta a mais por refresh (a leitura do passo 1, pelo índice único de
`token_hash`). A trava é por usuário: dois usuários nunca esperam um pelo outro; o mesmo usuário
com duas abas espera milissegundos.

**Interface.** `auth.Tx` ganha `SessionUserByHash` e `LockUserSessions` (sqlc por baixo, D27).
O `txFalsa` de `session_test.go` precisa dos dois métodos.

**Resíduo aceito.** O `Issue` (login) não é serializado contra a troca de senha: um login com a
senha **antiga**, verificada seus ~100 ms antes de o commit da troca acontecer, pode inserir a
família depois dele e sobreviver. Fechar exige reverificar o hash sob a trava dentro do `Issue`.
Quem explora isso já sabe a senha antiga e acerta uma janela de ~100 ms; a sessão nova aparece em
`GET /sessions` e se encerra dali.

### 4.8 `GET /targets/summary`

Cadeia `protegida`. `GET /targets/summary` e `GET /targets/{id}` convivem no `ServeMux`: o
padrão literal é mais específico e ganha, sem conflito de registro.

`targets.Service.Summary(ctx, userID)` roda **uma** `InUserTx` (RLS do dono) com quatro
consultas. Com `agora` do relógio injetado, `H = agora.UTC().Truncate(time.Hour)` e
`início = H − 24h`:

1. **`ListTargetSummaries`** — alvos do dono na ordem de `GET /targets` (`id DESC`), com o
   último check por `LATERAL … ORDER BY checked_at DESC LIMIT 1` e o incidente aberto por
   `LEFT JOIN incidents … AND resolved_at IS NULL` (o índice único parcial
   `incidents_one_open_per_target` garante no máximo um).
2. **`LatestUserRollupBucket`** — a marca d'água **do dono**: o maior `bucket` dos rollups dos
   alvos dele, por `LATERAL … ORDER BY bucket DESC LIMIT 1` na PK de cada alvo.
   `corte = clamp(marca + 1h, início, H)`; sem rollup nenhum, `corte = início`.
   Não serve `max(bucket)` da tabela: sob a RLS, um dono sem rollups recentes faria o Postgres
   varrer o índice de `bucket` da instância inteira testando a política linha a linha.
3. **`ListSummaryRollups`** — `total, ok, p50` de `check_rollup_1h` para `início ≤ bucket < corte`.
4. **`ListSummaryRawHours`** — agregação horária de `checks` para `corte ≤ checked_at < H`, com
   `percentile_disc(0.5)` dos não-`down`, **a mesma fórmula de `ConsolidateRollups`**; e
   **`ListSummaryCurrentHour`** — só `count(*)` e `count(*) FILTER (WHERE status <> 'down')` para
   `H ≤ checked_at < agora` (o `spark` não mostra a hora corrente, então ela não paga percentil).

O `corte` é um valor fixo lido antes das consultas 3 e 4: uma consolidação que comite entre
elas não conta hora nenhuma duas vezes (a 3 para antes do corte, a 4 começa nele).

**Montagem, por alvo:**

- `state` = `targetstate.Current(enabled, intervalo, últimoStatus, últimoInstante, agora)` (D21):
  o do último check; `unknown` se desativado, se nunca houve check ou se o último tem mais de
  **3 intervalos** (limite inclusivo, como `public.go` hoje).
- `last_checked_at`, `last_latency_ms` (D23), `open_incident_id`: `null` quando não há.
- `spark`: as horas de 3 e 4 em ordem crescente de `bucket` (RFC 3339, UTC), `p50_ms = null`
  quando `ok = 0`. Hora sem check não aparece (contrato).
- `uptime_24h` = `Σok / Σtotal` sobre o `spark` **mais a hora corrente** (D22); `null` se
  `Σtotal = 0`. `ok` conta os não-`down` (`degraded` é sucesso, D5 do Plano 4). Número cru, sem
  arredondar (§12).

**Custo medido** (Apêndice A; 5.100 alvos na instância, RLS ativa):

| Cenário | Consultas 1–4 |
|---|---|
| 100 alvos, manutenção em dia ou uma hora atrás | ~33 ms |
| 500 alvos, manutenção uma hora atrás (pior caso normal) | ~190–250 ms |
| 500 alvos, manutenção em dia (`corte = H`) | ~80 ms |
| Alternativa rejeitada: 24h só de `checks`, 100 alvos | ~390 ms (com p50), ~150 ms (só contagens) |

A RLS delegada (`EXISTS` em `targets`) virou `hashed SubPlan` em todas as consultas: um *seq
scan* de `targets` por consulta (~2 ms com 5.100 alvos na instância), e não um por linha. Cresce
com o total de alvos da instância, linear.

**Tamanho.** Um item com 24 horas de `spark` tem ~1,8 KB de JSON. Com o teto de 100 alvos
(D24), ~180 KB a cada 15 s por painel aberto, sem compressão (a API não tem gzip; ver R8).

Se a manutenção parar, `corte` recua sozinho e a consulta 4 cobre até 24h de `checks`: o
dashboard continua certo, só mais caro, e o custo é limitado pelas 24h.

## 5. Dados e migrations

### 5.1 Migration `00007_sessions_live.sql`

Um índice só:

```sql
-- GET /sessions e FamilyIsLive procuram as linhas vivas do usuário. Sem ele,
-- sessions_user_idx lê todas as linhas do usuário, inclusive as já rotacionadas,
-- que passam de mil por família ativa em 37 dias (uma a cada ~15 min de uso).
CREATE INDEX sessions_user_live_idx ON sessions (user_id, family_id)
    WHERE used_at IS NULL AND revoked_at IS NULL;
```

O summary usa só índices que já existem (`targets_user_idx`, `checks_target_at_idx` das
partições, a PK de `check_rollup_1h`, `incidents_one_open_per_target`).

### 5.2 Queries sqlc novas

| Arquivo | Query | Tipo | Observação |
|---|---|---|---|
| `queries/users.sql` | `GetAccount` | `:one` | §4.2 |
| | `CountUsers` | `:one` | `user create` |
| | `GetUserIDByEmail` | `:one` | `set-password` |
| | `InsertUser` | `:exec` | `id, email, password_hash, created_at` do relógio |
| | `ChangePasswordHash` | `:execrows` | CAS (D8); zera bloqueio |
| | `ResetPasswordHash` | `:execrows` | CLI; zera bloqueio |
| | `LockUserCreation` | `:exec` | `pg_advisory_xact_lock(hashtextextended('vigilume.user_create', 0))` |
| | `InsertAuditEntry` | `:exec` | com `entity` e `entity_id`; o `insertAudit` à mão fica para o R2 |
| `queries/sessions.sql` | `SessionUserByHash` | `:one` | §4.7 passo 1 |
| | `LockUserSessions` | `:exec` | `hashtextextended('vigilume.sessions:' \|\| $1::text, 0)` |
| | `FamilyIsLive` | `:one` | §4.3 |
| | `ListLiveFamilies` | `:many` | §4.5 |
| | `RevokeFamilyOfUser` | `:execrows` | `WHERE user_id = $1 AND family_id = $2 AND revoked_at IS NULL` |
| | `RevokeOtherFamilies` | `:execrows` | `… AND family_id <> $2 …` |
| | `RevokeAllFamilies` | `:execrows` | CLI |
| `queries/targets.sql` | `ListTargetSummaries`, `LatestUserRollupBucket`, `ListSummaryRollups`, `ListSummaryRawHours`, `ListSummaryCurrentHour` | `:many`/`:one` | §4.8, todas com `t.user_id = sqlc.arg(user_id)` **e** sob `InUserTx` |
| | `CountTargets` | `:one` | teto (D24), sob a trava do dono |

O prefixo `vigilume.` das advisory locks segue o de `ensure_checks_partition`; `hashtextextended`
devolve 64 bits, e a colisão entre um usuário e as chaves fixas é desprezível.

O teto de alvos (D24) conta e insere na mesma `InUserTx`, depois de
`pg_advisory_xact_lock(hashtextextended('vigilume.targets:' || user_id, 0))`: sem a trava, dois
`POST` simultâneos passam os dois pela contagem (o `MaxChannels` do Plano 3 tem esse furo,
registrado em R9).

### 5.3 Mapa de arquivos

| Arquivo | O que muda |
|---|---|
| `cmd/vigilume/main.go`, `cmd/vigilume/user.go` (novo) | Desvio para `runUser`; leitura da senha; códigos de saída |
| `internal/config/config.go` | `LoadDatabaseURL(getenv)` para o CLI |
| `internal/account/` (novo) | `Service`: `CreateUser`, `ResetPassword`, `ChangePassword`, `Me`, `ListSessions`, `RevokeSession` |
| `internal/auth/password.go` | `ValidateNewPassword`, `MinPasswordLen = 15`, `MaxPasswordLen = 128` |
| `internal/auth/user.go` | `CheckPassword`, `NormalizeEmail` |
| `internal/auth/token.go` | `sid`; `AccessClaims` |
| `internal/auth/session.go`, `store.go` | Trava do usuário em `Rotate`/`Revoke`; `Tx` com dois métodos novos |
| `internal/httpapi/middleware/authn.go` | `FamilyID(ctx)` |
| `internal/httpapi/authhandlers.go` | `Login` usa `CheckPassword`; `Me` via `account`; `normalizarEmail` sai |
| `internal/httpapi/totphandlers.go` | `senhaConfere` vira `CheckPassword` (D12) |
| `internal/httpapi/accounthandlers.go` (novo) | `ChangePassword`, `ListSessions`, `RevokeSession` |
| `internal/httpapi/targethandlers.go` | `Summary`; `409 target_limit_reached` no `Create` |
| `internal/httpapi/routes.go` | Rotas novas; limitador do login compartilhado; `Deps.Accounts` |
| `internal/apierr/errors.go` | `CodeTargetLimit` (se D24 for aceita) |
| `internal/targetstate/` (novo) | `State`, `Current` |
| `internal/statuspage/public.go` | `estado` delega a `targetstate.Current` |
| `internal/targets/service.go`, `target.go` | `Summary`, `MaxTargetsPerUser`, trava e contagem no `Create` |
| `internal/storage/migrations/00007_sessions_live.sql`, `queries/{users,sessions,targets}.sql` | §5.1, §5.2 |
| `go.mod` | `golang.org/x/term` |

## 6. Segurança

**RLS (D13).** Nada muda em `users` e `sessions`. A segunda barreira que a spec §5.5 pede vem, ali,
da forma das funções (`userID` obrigatório) e de um teste de isolamento por rota com sabotagem
(§8). O summary roda sob a RLS de sempre, com o `WHERE user_id` além dela.

**Rate limits.**

| Rota | Cadeia |
|---|---|
| `POST /auth/password` | global por IP (120/min) → **limitador do login** (10/min, burst 5, mesma instância) → authn |
| `POST /auth/totp/enroll`, `/totp/disable` | idem (D12) |
| `GET /me`, `GET /sessions`, `DELETE /sessions/{id}`, `GET /targets/summary` | global por IP → authn → por usuário (60/min, burst 20) |

O painel chama o summary a cada 15 s (4/min) e `/me` uma vez por boot: cabe folgado nos 60/min.

**Auditoria (D25).** Tudo na mesma transação da mudança: não existe troca de senha sem rastro, nem
rastro de troca que não aconteceu (mesmo argumento do R39).

**Janela de 15 minutos (D4).** Depois de `DELETE /sessions/{id}`, de `POST /auth/password` e de
`set-password`, os access tokens já emitidos das famílias revogadas continuam valendo nas rotas
de recurso até expirar. Não conseguem renovar, trocar a senha, mexer no 2FA (que pede a senha),
listar nem encerrar sessões. Isso precisa aparecer na interface (§12).

**CLI.** Senha nunca em argumento nem em ambiente (D16); nada de `log.Info("iniciando", "cfg", …)`
no caminho do CLI; o stdout só leva e-mail, id e contagem.

**Dados expostos.** `/sessions` expõe o `user_agent`, que é texto controlado por quem fez o
login, inclusive um invasor: o painel o renderiza como texto (o React já escapa; nada de
`dangerouslySetInnerHTML`). `/me` expõe o e-mail do próprio dono.

## 7. Erros e códigos

Envelope da spec §9.1. `401 unauthorized` é o de token ausente ou inválido **e** o de família morta
nas rotas de D4; o painel já o trata renovando (§12).

| Rota | Sucesso | Erros |
|---|---|---|
| `GET /me` | `200` | `401 unauthorized`; `429` |
| `POST /auth/password` | `204` | `400 validation_failed` (`new_password`: `too_short`/`too_long`); `401 invalid_credentials` (senha atual errada, TOTP errado ou ausente, conta bloqueada, corrida do CAS); `401 unauthorized` (token, família morta); `413`; `429 rate_limited` |
| `GET /sessions` | `200` | `401 unauthorized`; `429` |
| `DELETE /sessions/{id}` | `204` (+ `Set-Cookie` que apaga o refresh, se for a atual) | `404 not_found`; `401 unauthorized`; `429` |
| `GET /targets/summary` | `200` | `401 unauthorized`; `429`; `500` |
| `POST /targets` (D24) | `201` como hoje | + `409 target_limit_reached` |

`400` com corpo JSON malformado continua `validation_failed` sem `fields`, como nas outras rotas de
autenticação (`decodeJSON`, não estrito).

## 8. Testes

Tudo contra Postgres real (`testdb`), com o papel comum. Regra de sempre: **toda asserção de
ausência tem sabotagem prevista**; o implementador quebra, vê vermelho, restaura e registra.

### 8.1 CLI (`cmd/vigilume/user_test.go`, chamando `runUser`)

| Teste | Afirma | Sabotagem que tem de deixá-lo vermelho |
|---|---|---|
| `TestUserCreateFromPipe` | código 0; linha em `users` com hash que `VerifyPassword` aceita; `user.created` auditado | gravar o hash de `""` |
| `TestUserCreateStripsOneNewline` | `"senha…\n"` e `"senha…\r\n"` viram a mesma senha; `"a\nb"` é código 2 | pegar só a primeira linha |
| `TestUserCreateRefusesWhenAUserExists` | código 3 **e** a senha do existente continua a mesma **e** nenhum usuário novo | trocar a recusa por *upsert* |
| `TestUserPasswordNeverLeaves` | uma senha marcada (`"marca-d-agua-7f3a…"`) não aparece no stdout nem no stderr, em sucesso **e** em cada erro | imprimir a senha no erro de política |
| `TestUserHasNoPasswordFlagOrEnv` | `--password x` é código 2; `VIGILUME_PASSWORD`/`PASSWORD` no `getenv` com stdin vazio é `too_short` (código 2) | acrescentar o *fallback* de env |
| `TestUserRefusesSuperuserBeforeMigrating` | DSN de admin num banco vazio (`testdb.EmptyDatabase`): código 1 **e** a tabela `users` **não** existe | trocar a ordem para `Migrate` → `CheckRole` (lição do Plano 2: afirmar o efeito ausente, não só o erro) |
| `TestSetPasswordRevokesEverySession` | duas famílias vivas → código 0 → as duas mortas; `failed_attempts = 0` e `locked_until` nulo; TOTP intacto | tirar o `RevokeAllFamilies` |
| `TestSetPasswordLeavesOtherUsersAlone` | família de outro usuário continua viva | tirar o `user_id` do `RevokeAllFamilies` |
| `TestSetPasswordUnknownEmail` | código 4 e nada muda | — |
| `TestUserPolicyBoundaries` | 14 *code points* → 2; 15 → 0; 128 → 0; 129 → 2; 15 caracteres de 4 bytes → 0 | contar bytes em vez de *code points* (o de 4 bytes fica vermelho) |

A leitura em terminal fica atrás de uma interface pequena (`lerSenha(io.Reader, ehTerminal bool)`),
testada com a confirmação diferente (código 2); a chamada real a `term.ReadPassword` não tem teste
automatizado e é conferida à mão uma vez, registrada no relatório.

### 8.2 `/me` e `sid`

- `TestMeReturnsAccount`: `email` como gravado, `totp_enabled` falso → cadastrar e **não**
  confirmar → ainda falso → confirmar → verdadeiro. Sabotagem: `totp_secret_enc IS NOT NULL`.
- `TestAccessTokenCarriesFamily`: o token do login tem `sid` = família de `sessions`; o do
  refresh, a mesma família. `TestTokenWithoutSidIsRejected`: token assinado certo sem `sid` →
  `401`. Sabotagem: aceitar `sid` ausente como `uuid.Nil`.

### 8.3 `POST /auth/password`

| Teste | Afirma | Sabotagem |
|---|---|---|
| `TestChangePasswordKeepsCurrentRevokesOthers` | três famílias (atual, B, C) → `204` → refresh da atual `200`; de B e C `401 invalid_session`; login com a senha nova entra e com a antiga não | revogar todas (tirar o `family_id <> $2`): a atual fica vermelha; não revogar nenhuma: B fica vermelha |
| `TestChangePasswordLeavesOtherUsersAlone` | família de outro usuário viva depois | tirar o `user_id` do `RevokeOtherFamilies` |
| `TestChangePasswordWrongCurrent` | `401 invalid_credentials`, hash igual, `failed_attempts` +1, nenhuma família revogada | não chamar `RecordFailure`; revogar antes de verificar |
| `TestChangePasswordLockedAccount` | conta bloqueada, senha **certa** → `401 invalid_credentials` e nada muda | tirar a checagem de bloqueio |
| `TestChangePasswordSpendsTheSameCPU` | bloqueada e senha certa + TOTP errado custam ≥ metade de "senha errada" (menor de duas medidas) | tirar o `VerifyDummy` do caminho bloqueado |
| `TestChangePasswordRequiresTOTP` | 2FA ligado: sem `totp_code` → `401`; errado → `401` e conta falha; certo → `204`; código de recuperação → `204`, consumido e auditado | não exigir o código quando `totp_code` vem vazio |
| `TestChangePasswordPolicy` | `too_short`/`too_long` com `field: new_password`; **nenhuma** falha contada, mesmo com a senha atual errada no mesmo pedido (a política vem antes) | validar depois de verificar a senha (a contagem de falhas fica vermelha) |
| `TestChangePasswordDeadFamily` | família do token revogada → `401 unauthorized` e hash igual | tirar o passo 2 **e** o 7 (os dois juntos: ver nota) |
| `TestChangePasswordSharesTheLoginLimiter` | esgotar o burst do login de um IP → `POST /auth/password` do mesmo IP → `429` | dar ao `/auth/password` uma instância própria |
| `TestChangePasswordAudited` | `user.password_changed` com `ip_hash` | — |

Nota sobre o `TestChangePasswordDeadFamily`: os passos 2 e 7 são duas guardas da mesma
propriedade; sabotar uma só deixa o teste verde (lição do Plano 1). O teste prova a propriedade;
a guarda do passo 7 tem prova própria no teste de corrida (§8.5).

### 8.4 Sessões

| Teste | Afirma | Sabotagem |
|---|---|---|
| `TestListSessionsShowsOnlyLiveFamilies` | vivas aparecem; revogada, vencida (relógio adiantado) e de outro usuário não | tirar cada predicado do `WHERE`, um de cada vez |
| `TestListSessionsMarksCurrent` | só a do token tem `current: true` | comparar com o `user_id` |
| `TestListSessionsNeverExposesIPOrHash` | o corpo não tem as chaves `ip`, `ip_hash`, `token_hash`, `expires_at`, nem o hex/base64 do `ip_hash` e do `token_hash` gravados | serializar a linha inteira |
| `TestListSessionsOrderAndTimes` | ordem por `created_at` da família (UUIDv7), não do último uso; `last_used_at` = instante da rotação | ordenar por `last_used_at` |
| `TestListSessionsTruncatesUserAgent` | UA de 300 caracteres multibyte sai com 256 *code points*, UTF-8 válido | cortar por bytes |
| `TestRevokeSessionOfAnotherUserIs404` | `404` **e** a família do outro continua viva (refresh dele `200`) | tirar o `user_id` do `FamilyIsLive` e do `RevokeFamilyOfUser` |
| `TestRevokeSessionTwiceIs404` | segundo `DELETE` → `404`; auditoria só uma | — |
| `TestRevokeCurrentSessionClearsCookie` | `204` com `Set-Cookie` de `vigilume_refresh` vazio, `Max-Age` ≤ 0, `Path=/api/v1/auth`; refresh depois → `401` | não limpar o cookie |
| `TestRevokeOtherSessionKeepsCookie` | revogar outra família **não** manda `Set-Cookie` | limpar sempre |
| `TestSessionRoutesRejectDeadFamily` | com a família do token revogada, `GET` e `DELETE /sessions` → `401 unauthorized` | tirar a checagem de D4 |

`sessions.created_at` usa `DEFAULT now()` do banco, e não o relógio falso: os testes de tempo
comparam `last_used_at` com o instante real (antes/depois da chamada), nunca com `clk.Now()`. O
`created_at` da família vem do UUIDv7, também tempo real.

### 8.5 Corrida (§4.7)

- `TestRotateTakesTheUserLock` e `TestRevokeTakesTheUserLock`: uma conexão do teste segura
  `pg_advisory_lock` da chave do usuário; `Rotate` (e `Revoke`) numa goroutine tem de aparecer em
  `pg_stat_activity` com `wait_event = 'advisory'` e **não** terminar; o teste solta e ela
  termina. Mesmo desenho do teste de `ensure_checks_partition` (Plano 4). Sabotagem: tirar o
  `LockUserSessions` → a goroutine termina com a trava segurada.
- `TestChangePasswordAndRevokeTakeTheUserLock`: o mesmo para `ChangePassword`, `RevokeSession` e
  `ResetPassword`.
- `TestRevokeFamilyDuringRotationLeavesNothingAlive`: a propriedade do Apêndice B com o código
  real — o teste segura a trava, dispara `Rotate` e `RevokeSession` da mesma família, solta, e
  exige zero linhas vivas. Rodar com `-count=20`: é teste de ordenação, e um verde só não prova
  nada (lição do Plano 4 sobre sorteio).

### 8.6 Summary

| Teste | Afirma | Sabotagem |
|---|---|---|
| `TestSummaryStateFollowsThePublicRule` | tabela: up, degraded, down, desativado, sem check, exatamente 3 intervalos (recente), 3 intervalos + 1 s (unknown) | — |
| `TestPublicPageAndSummaryShareTheRule` | mesmo alvo e mesmo relógio → mesmo estado nas duas projeções | voltar `statuspage.estado` à cópia antiga com o limite mudado para 2 |
| `TestSummaryIsolation` | alvos, checks, incidentes e rollups de outro usuário não entram (nem no `spark`, nem no uptime) | tirar o `user_id` das consultas **e** rodar sem `InUserTx` (duas barreiras: sabotar cada uma sozinha deixa verde, e o comentário diz isso) |
| `TestSummarySparkMixesRollupsAndRaw` | rollups até a marca, brutos depois, sem hora duplicada nem faltando; hora corrente fora do `spark` e dentro do uptime | ler as duas fontes sem o corte (hora duplicada) |
| `TestSummaryHourBoundaries` | `agora` exatamente em `H` (hora corrente vazia) e em `H + 59m59s`; bucket sempre em UTC com `TimeZone` da sessão `Asia/Kolkata` | `date_trunc` |
| `TestSummaryUptimeSeesAnOngoingOutage` | 20 min de `down` na hora corrente baixam o `uptime_24h` | excluir a hora corrente |
| `TestSummaryNullables` | sem check: `last_checked_at`, `last_latency_ms`, `uptime_24h` nulos, `spark: []`; último `down`: `last_latency_ms` nulo | — |
| `TestSummaryRouteIsWired` | `GET /api/v1/targets/summary` responde o summary, e não o `404` do `{id}` | registrar só `{id}` |
| `TestTargetLimit` (se D24) | o 101º → `409 target_limit_reached`; dois `POST` simultâneos no 100º → um só passa | tirar a trava (o simultâneo fica vermelho; forçar a sobreposição como no teste de SKIP LOCKED do Plano 2) |

### 8.7 Wiring

Um teste de tabela sobre `Routes`, como o que faltou no Plano 1: para cada rota nova, sem
`Authorization` → `401`; mutação sem `X-Requested-With` → recusada; corpo acima de 1 MiB → `413`
(no `POST /auth/password`). Sabotagem: registrar a rota sem `authn` (o handler também recusa sem
usuário no contexto; o teste afirma o **código** `unauthorized` vindo do middleware e diz no
comentário que as duas guardas existem, como no D25 do Plano 4).

### 8.8 Regressão

`TestLoginSpendsTheSameCPUWhetherOrNotTheAccountExists`, a suíte do R34/R40 e os testes de
rotação e reúso continuam verdes depois de o `Login` passar a usar `CheckPassword` e de `Rotate`
ganhar a trava. Nenhum deles é reescrito para acomodar a mudança.

## 9. Riscos

| # | Risco | Mitigação |
|---|---|---|
| R1 | A janela de 15 min (D4) é lida pelo usuário como "encerrei e continua entrando" | Texto no painel (§12); D4 como decisão explícita |
| R2 | A trava do usuário em `Rotate` introduz espera no refresh | Por usuário, milissegundos; medida no teste de corrida |
| R3 | Refatorar `Login` para `CheckPassword` muda comportamento sem querer (R34, R40) | §8.8: a suíte existente é o árbitro, sem edição |
| R4 | `senhaConfere` → `CheckPassword` muda o TOTP: conta bloqueada passa a receber `401` no enroll/disable | É o objetivo (D12); o dono bloqueado espera 15 min como no login |
| R5 | Mínimo de 15 obriga a trocar a senha de teste e a do dono ao próximo `set-password` | O login aceita a senha antiga; só senhas novas passam pela política |
| R6 | O summary com a manutenção parada custa 24h de `checks` | Limitado; mede-se ~390 ms com 100 alvos; `unknown` e o `corte` recuando são o sintoma visível |
| R7 | Abas múltiplas renovando ao mesmo tempo disparam a detecção de reúso e derrubam a família | É do Plano 6 (§12, Web Locks); registrado aqui porque a causa está na rotação do Plano 1 |
| R8 | ~180 KB de JSON a cada 15 s por painel com 100 alvos | Aceito; gzip na API é pequeno se virar problema (fica para o Plano 7 decidir no proxy) |
| R9 | `MaxChannels` (Plano 3) conta e insere sem trava: dois `POST /channels` simultâneos passam do limite | Fora de escopo; entra no `PENDENCIAS` que o Plano 5 escrever |
| R10 | Login concorrente com a troca de senha sobrevive (§4.7, resíduo) | Aceito; visível em `GET /sessions` |

## 10. Decisões para o usuário revisar

1. **D1 — 15 caracteres de mínimo.** É o número da NIST 800-63B-4 para senha como fator único, e
   o Vigilume tem 2FA opcional. Se o 2FA fosse obrigatório, 8 bastaria.
2. **D4 — checar família viva só nas rotas de credencial e sessão.** A alternativa (toda rota) fecha
   a janela de 15 min ao custo de uma consulta por requisição. Eu fiquei com a spec §5.2; é a
   escolha mais fácil de inverter depois (o `sid` já está no token).
3. **D12 — mexer no `/totp/enroll` e no `/totp/disable`.** Não está na lista do contrato; entrou
   porque, sem isso, a troca de senha fechada deixa o enroll como oráculo de senha sem bloqueio.
4. **D18 — `user create` recusa se já existe qualquer usuário.** Leitura literal de "usuário
   único" (spec §2.1).
5. **D24 — teto de 100 alvos.** Acréscimo ao contrato (§11, Div. 2).
6. **D22 — `uptime_24h` inclui a hora corrente** (entre 24 e 25 horas de janela) para ver a queda
   em curso. O contrato fixa a janela do `spark`, não a do uptime.
7. **D2 — sem normalização Unicode** (NIST recomenda NFC).
8. **D15 — `404` também para a própria família já morta**, em vez de `204` idempotente.
9. **Resíduo da §4.7** (login concorrente com a troca de senha) aceito.
10. **Recuperação de TOTP pelo CLI** (`user reset-totp`) **não** entra: quem perde o celular e os
    códigos de recuperação fica sem saída que não seja SQL. Pequeno para acrescentar, mas fora do
    escopo do contrato.

## 11. Divergências com o contrato

Nenhum shape JSON do contrato muda. O que segue são acréscimos ou lacunas para a reconciliação.

**Div. 1 — Blocklist de senhas. (Resolvida: o usuário decidiu não implementar; só o mínimo de 15.)** A NIST 800-63B-4 §3.1.1.2 diz que o verificador **SHALL**
comparar a senha nova com uma lista de senhas comuns ou vazadas. O contrato fecha os motivos em
`too_short | too_long`. Esta spec não implementa blocklist. Motivo para aceitar: com mínimo de 15,
as listas comuns quase não têm entradas, e o sistema tem um usuário, que é o operador. Alternativa,
se o usuário quiser a NIST inteira: acrescentar `"reason": "too_common"` ao contrato, com uma lista
embutida (as ~10 mil mais comuns de 15+ caracteres), e o Plano 6 mapeia a mensagem.

**Div. 2 — `409 target_limit_reached` em `POST /targets`** (D24). O contrato diz "sem paginação:
limite prático de alvos por usuário" e não define limite. Esta spec o torna explícito (100). O
Plano 6 precisa mapear o código no formulário de alvo; um painel que não o conheça mostra o erro
genérico.

**Div. 3 (esclarecimento, não mudança) — `401 unauthorized` em `POST /auth/password` e em
`/sessions`** quando a família do token está morta (D4). O contrato lista só `invalid_credentials`
para a troca; o `unauthorized` é o código genérico de token recusado, que o painel já sabe tratar.

## 12. Notas para os Planos 6 e 7

**Plano 6 (painel):**

- **Renove só em `401` com `code: unauthorized`.** `invalid_credentials` em `/auth/password` é erro
  de formulário: **nunca** renove e repita (repetir reenviaria a senha errada e contaria outra
  falha no bloqueio). `invalid_session` vindo de `/auth/refresh` é "vá para o login".
- **`DELETE /sessions/{id}` da própria sessão** (`current: true`): o `204` já apagou o cookie.
  Trate como logout: descarte o access token da memória, limpe o cache do TanStack Query e vá para
  `/login`, **sem** tentar `/auth/refresh`. O token em memória ainda funcionaria por até 15 min nas
  rotas de recurso; é o painel que tem de largá-lo.
- **`DELETE` que volta `404`**: a sessão já tinha acabado (duplo clique, expirou, foi encerrada
  por outro aparelho). Recarregue a lista; não é erro para o usuário.
- **Texto de "encerrar sessão"**: encerrar impede a renovação; um acesso já aberto no outro
  aparelho pode continuar por até 15 minutos. O mesmo vale para as outras sessões depois de
  trocar a senha.
- **Troca de senha**: mostre o campo de código só com `totp_enabled` de `/me`; aceite código de
  recuperação no mesmo campo (16 caracteres base32, não só 6 dígitos). Política no cliente:
  15 a 128 **code points** (`[...senha].length`, não `senha.length`, que conta unidades UTF-16).
  `204` mantém o usuário logado.
- **`user_agent`** é texto de terceiros: renderize como texto.
- **Várias abas.** Duas abas renovando o mesmo cookie ao mesmo tempo (restauração de sessão do
  navegador, F5 em duas) fazem a segunda apresentar um refresh já usado, e a detecção de reúso
  derruba a família nas duas. O *single-flight* por aba não cobre isso; `navigator.locks.request`
  (Web Locks) em volta do refresh cobre, porque a segunda aba, ao pegar a trava, já envia o cookie
  novo.
- **Summary**: `uptime_24h` é número cru; trunque na exibição (D16 do Plano 4: "100,00%" com falha
  é pior que "99,99%"). Hora ausente no `spark` é hora sem check, não zero.

**Plano 7 (empacotamento):**

- `vigilume user create --email <e>` precisa só de `DATABASE_URL`, com o **papel comum** (o de
  admin é recusado, código 1). Em compose: `docker compose exec -T app vigilume user create
  --email … < arquivo-da-senha` (o `-T` é o que faz o stdin ser pipe) ou, interativo, sem `-T`
  (terminal, sem eco). Nunca senha em variável ou argumento no `compose.yaml`.
- Código **3** = já existe usuário; um passo idempotente trata 3 como sucesso.
- `vigilume user set-password --email <e>` encerra todas as sessões.

---

## Apêndice A — medição do summary

Ambiente: `postgres:16-alpine` descartável, migrations 00001–00006 aplicadas pelo papel comum
(dono, sem `BYPASSRLS`), consultas sob `set_config('app.user_id', …, true)`, isto é, exatamente a
`InUserTx`. Volume: 51 usuários; 50 com 100 alvos cada (5.000 alvos alheios, checks a cada 60 s nas
últimas 3h, rollups de 14 dias); o dono com 100 alvos (checks a cada 30 s por 48h, 1% `down`, 5%
`degraded`; rollups até `H − 2h`, simulando a manutenção uma hora atrás) e, na segunda rodada, 400
alvos a mais (checks das últimas 2h, rollups de 24h). Totais: 1,48 M checks, 1,68 M rollups,
15,6 mil incidentes (5% dos alvos com um aberto). `agora = 2026-09-24 12:20 UTC`. `EXPLAIN
(ANALYZE)`, três rodadas, cache quente.

| Consulta | 100 alvos | 500 alvos |
|---|---|---|
| 1. alvos + último check + incidente aberto | 4 ms | 10 ms |
| 2. marca d'água do dono | 3 ms | 4 ms |
| 3. rollups até o corte (23 horas) | 3 ms | 34–37 ms |
| 4a. brutos, hora fechada ainda não consolidada, com p50 | — | 113–166 ms |
| 4b. brutos, hora corrente, só contagens | — | 27–43 ms |
| 4 (a+b juntas, 80 min com p50) | 23 ms | 113–127 ms |
| Alternativa: 24h de brutos com p50 | 390 ms | — |
| Alternativa: 24h de brutos, só contagens | 150 ms | — |

Plano da consulta 4: `Index Only Scan Backward` em `targets_user_idx`, *nested loop* em
`checks_2026_09_target_id_checked_at_idx` por alvo, *incremental sort* e `GroupAggregate`. A
política de `checks` virou `hashed SubPlan` com um *seq scan* de `targets` (5.100 linhas, 100
passam) **uma vez** por consulta.

## Apêndice B — a corrida de revogação

Programa Go descartável (`pgx`), duas conexões, mesmo banco do Apêndice A. Família `F2` viva na
linha `R1`.

1. Tx A (rotação): `SELECT … WHERE token_hash = R1 FOR UPDATE`.
2. Tx B (revogação): `UPDATE sessions SET revoked_at = now() WHERE user_id = U AND family_id <> F1
   AND revoked_at IS NULL` — fica esperando `R1`.
3. Tx A: insere `R2` na `F2`, marca `R1` usada, comita. Tx B termina e comita.
4. Linhas vivas (`used_at` e `revoked_at` nulos) da `F2`.

| Variante | Resultado |
|---|---|
| Sem trava por usuário (o código de hoje) | **1** — `R2` sobreviveu à revogação |
| Com `pg_advisory_xact_lock(hashtextextended('vigilume.sessions:' \|\| U, 0))` em A (antes da linha) e em B | **0** |
