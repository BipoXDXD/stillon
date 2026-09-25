# Vigilume — Plano 10: correções da revisão do projeto (design)

**Status:** decidida pelo controlador em 2026-09-25, com autorização do usuário ("tome as decisões por
mim, exceto as cruciais"). As decisões de política de segurança estão marcadas **[D]** e listadas no
fim (§6) para o usuário revisar. Nada aqui é código nem plano de implementação.

**Base de código:** `main` em `640cd9e` (Plano 9 integrado).

**Origem:** o usuário pediu, antes de hospedar, uma revisão do projeto inteiro buscando erros e falhas de
segurança. Cinco revisores independentes rodaram em 2026-09-25; os relatórios completos, com cenário de
ataque e arquivo:linha, estão em `docs/superpowers/revisao-2026-09-25/`:

| Arquivo | Área |
|---|---|
| `auth.md` | autenticação, sessão, 2FA, cripto, conta, CLI |
| `http-e-painel.md` | middlewares, IP do cliente, autorização, status page, painel |
| `sondador.md` | sondador, SSRF, notificações, alvos, incidentes |
| `banco-e-ci.md` | RLS, migrations, retenção, CI, imagem, configuração, segredos |
| `code-review-pr15.md` | revisão de correção do último merge (Plano 9) |

**Nova ordem dos planos:** 10 = esta revisão; 11 = hospedagem na Hostinger (desenho com seções 1–3
aprovadas pelo usuário); 12 = pendências do Plano 9 e dívidas do grupo C; 13 = R2 (auth → sqlc).

---

## 1. Objetivo

Fechar, antes de o Vigilume ir para a internet, todo achado **Alto** e **Médio** da revisão e os
**Baixos** baratos ou que tocam segurança; registrar o resto como pendência com motivo.

**Critérios de sucesso:**

1. Cada item de §3 tem teste que fica vermelho quando a correção é desfeita (sabotagem registrada).
2. Mudanças de contrato HTTP só as listadas em §4.
3. Suíte com `-race`, `lint`, `sec`, `sqlc diff`, `gofmt -l .`, testes do painel, build e E2E verdes; o
   PR só é integrado com todos os jobs do GitHub verdes.
4. `PENDENCIAS-PARA-O-PLANO-11.md` substitui o arquivo corrente e diz, item a item, o destino de cada
   achado da revisão que não foi corrigido aqui.

## 2. Resumo dos achados

Nenhum Crítico. A guarda contra SSRF, o isolamento por RLS, o CI e as dependências passaram limpos
(ver "Verificado e descartado" em cada relatório).

| Sev. | Achado | Origem |
|---|---|---|
| Alto | Argon2id (64 MiB) sem teto de concorrência: anônimo derruba o processo por memória | auth S1 |
| Alto | Bloqueio lido antes e gravado depois: requisições paralelas furam o limite de 5 (força bruta do TOTP) | auth S2 |
| Alto | Cabeçalhos de resposta de até 10 MiB por sonda × 64 sondas: OOM | sondador S1 |
| Alto | `compose.yaml` de demonstração usado em servidor: usuário demo com senha pública | banco S1 |
| Médio | Anônimo tranca o dono fora (5 tentativas a cada 15 min) | auth S3, http S1 |
| Médio | `PATCH` que troca a URL leva os headers secretos ao host novo | http S2 |
| Médio | `CLIENT_IP_HEADER` forjável atrás do Caddy | http S3 |
| Médio | `disable` aceita código TOTP já consumido; provas do app antigo sobrevivem à troca | auth S4, CR 1–2 |
| Médio | Sem cota por usuário no sondador e no despacho | sondador S2–S3 |
| Médio | `db` sem política de restart | banco C1 |
| Médio | Deadlock `disable` × `rotate/confirm`/`reset-2fa` | CR 6 |
| Médio | Painel: troca de autenticador perde códigos ou mente em corridas (passo 1 sem trava, resposta perdida, confirm sem prazo) | CR 4, 5, 10 |
| Baixo | ~20 itens (ver §3 e §5) | todos |

## 3. Escopo — o que muda

### 3.1 Autenticação e bloqueio (Grupo A)

- **A1 — Teto de concorrência do Argon2id.** Um semáforo global envolve **toda** chamada de hash e de
  verificação, inclusive o `VerifyDummy` (o tempo continua igual para e-mail existente ou não).
  Capacidade padrão `min(NumCPU, 4)`, configurável por `ARGON2_MAX_CONCURRENT` (1–64). Aquisição com
  prazo de 2 s; esgotado, a rota responde `429 rate_limited` com `Retry-After: 2`. Parâmetros do
  Argon2 **não** mudam (hashes existentes continuam válidos). **[D]**
- **A2 — Reserva atômica da tentativa.** Toda verificação de credencial que conta no bloqueio
  **reserva** a tentativa antes de verificar: um `UPDATE … SET failed_attempts = failed_attempts + 1,
  locked_until = CASE … END WHERE id = $1 AND (locked_until IS NULL OR locked_until <= now) RETURNING`
  (sem linha = bloqueado). No sucesso, zera. Vale para a senha do login (reservar **antes** do Argon2),
  o `/auth/totp/verify` e as provas das rotas autenticadas de §A3. N requisições paralelas passam a
  gastar N tentativas.
- **A3 — Bloqueio sem negação de serviço ao dono.** **[D]**
  - **Cookie de dispositivo** (OWASP "device cookie"): depois de um login **completo** (senha e, se
    ligado, 2FA), o servidor põe `vigilume_dispositivo` (HttpOnly, Secure quando https, SameSite=Strict,
    `Path=/api/v1/auth`, validade 180 dias) com um identificador aleatório e um MAC do par
    (usuário, identificador), com chave derivada da `MASTER_KEY` por HKDF com rótulo próprio. Nada novo
    no banco.
  - Um login que apresenta cookie de dispositivo **válido para aquela conta** não é barrado pelo
    bloqueio da conta; suas falhas contam num contador **por dispositivo** em memória (5 falhas →
    o cookie é ignorado por 15 min). O limitador por IP continua valendo para todos.
  - Sem cookie válido, o comportamento é o de hoje (5 falhas → 15 min), agora atômico (A2).
  - **Rotas autenticadas** (troca de senha, `enroll`/`confirm`/`disable`/`rotate`/`rotate/confirm` do
    TOTP) deixam de consultar e de alimentar o bloqueio da conta. Cada família de sessão tem um contador
    próprio de provas erradas (em memória); na 5ª, a família é revogada e a rota responde
    `401 unauthorized`. Quem tem a sessão e chuta a senha é expulso; o dono não é trancado por isso.
  - O comentário de `internal/httpapi/routes.go` sobre o burst do login é corrigido.
- **A4 — Prova do TOTP amarrada ao segredo que a validou.** O consumo do timestep passa a fazer CAS em
  `totp_secret_enc` (como o `Confirm` já faz). A emissão de sessão depois do 2FA confere, sob a trava
  de sessões do usuário, que o segredo validado ainda é o vigente; se não for, `401 invalid_credentials`.
  O `disable` passa a **consumir** o timestep (hoje só valida) e a verificar dentro da mesma transação.
- **A5 — Ordem de travas.** `disable` toma `LockUserSessions` primeiro e segue a ordem global
  (advisory → `users` → `totp_recovery_codes` → `sessions` → `audit_log`); some o deadlock com
  `rotate/confirm` e `reset-2fa`. `StartRotation` verifica a prova **dentro** da transação, sob a trava,
  depois de reconferir a família (`SELECT … FOR NO KEY UPDATE` em `users`, não `FOR UPDATE`): família
  morta ou 2FA desligado não gastam mais a prova.
- **A6 — Zerar o bloqueio quando a identidade foi provada.** `reset-2fa` (CLI) zera `failed_attempts` e
  `locked_until`, como o `set-password`. `rotate` e `rotate/confirm` bem-sucedidos zeram o contador
  (revê a D5 do Plano 9). **[D]**
- **A7 — Pendente substituído na mesma família.** O `rotate` devolve também `rotation` (16 bytes em hex
  derivados do segredo pendente cifrado); o `rotate/confirm` exige o mesmo `rotation`. Diferente →
  `403 forbidden` (o mesmo de "sem troca"), sem contar falha. O painel envia o valor que recebeu.
- **A8 — Normalização de código de recuperação.** Antes do hash: remover espaços e hífens e passar para
  minúsculas. Códigos já gravados continuam válidos (o formato gerado já é minúsculo sem separador —
  conferir e registrar).
- **A9 — Logout honesto.** Se a revogação no banco falha, o logout responde `500 internal` e **não**
  apaga o cookie, para o cliente poder tentar de novo.
- **A10 — Usuário apagado no meio de uma rota do TOTP** responde `401 unauthorized` em vez de 500
  (`enroll`, `confirm`, `disable`, `rotate`, `rotate/confirm`), como `/me` e a troca de senha.

### 3.2 HTTP e IP do cliente (Grupo B)

- **B1 — Troca de destino apaga os headers.** Um `PATCH` que muda a **origem** da URL (esquema, host
  ou porta) sem mandar `headers` apaga os headers guardados (`header_names: []` na resposta). Mudar só
  o caminho mantém. O painel avisa antes de salvar ("os headers deste alvo serão apagados"). **[D]**
- **B2 — Várias linhas de `X-Forwarded-For`** são lidas juntas, na ordem, antes de escolher o último
  salto.
- **B3 — `TRUSTED_PROXIES` errado não passa em silêncio.** Com `PUBLIC_BASE_URL` https, uma requisição
  com `X-Forwarded-For` vinda de origem fora de `TRUSTED_PROXIES` gera um aviso no log (no máximo um
  por minuto) com o `remote_addr`, para o operador corrigir a lista.
- **B4 — `CLIENT_IP_HEADER` atrás de proxy genérico.** O comentário da config e o README dizem que ele
  só serve atrás de proxy que **substitui** esse cabeçalho (o do Fly substituía); atrás do Caddy, deixar
  vazio. A configuração do Caddy do Plano 11 remove os cabeçalhos `Fly-Client-IP`, `X-Real-IP` e
  `CF-Connecting-IP` vindos do cliente (registrado como requisito do Plano 11).
- **B5 — Envelope nos 404/405 da API.** Rota ou método inexistente sob `/api/` responde no envelope de
  erro com `trace_id` (`404 not_found`, `405 method_not_allowed` — se o código não existir no
  vocabulário, usar o mais próximo existente e registrar).
- **B6 — Série do detalhe sem relógio do navegador.** A janela do gráfico é pedida sem `from` calculado
  no cliente (o servidor calcula a partir de um parâmetro de janela, ou o cliente usa o relógio do
  servidor); decidir na tarefa pelo menor contrato.

### 3.3 Sondador e alvos (Grupo C)

- **C1 — Teto dos cabeçalhos de resposta.** `MaxResponseHeaderBytes: 64 << 10` no transporte do
  safedial; resposta acima vira `down` com o tipo de erro existente mais próximo.
- **C2 — `expected_status` válido** só em 200–299 e 400–599 (1xx e 3xx nunca são vistos, porque o
  cliente segue redirecionamentos): `400 validation_failed` com `out_of_range`, no POST e no PATCH.
- **C3 — Classificação de redirecionamentos.** Excesso de saltos ou esquema inválido no `Location` deixa
  de ser "bloqueado pela proteção de rede" e vai para o tipo de erro existente mais próximo.
- **C4 — Trocar URL ou método zera a histerese** do alvo (o estado volta a "sem dados").

### 3.4 Banco, compose e CI (Grupo D)

- **D1 — O demo não roda em servidor.** O `seed.sh` recusa rodar se `PUBLIC_BASE_URL` não for http em
  host de loopback. `compose.yaml` ganha `restart: unless-stopped` no `db`. O cabeçalho do
  `compose.yaml` diz, em destaque, que ele é só para a máquina local e que produção usa o compose do
  Plano 11.
- **D2 — Dependabot com `cooldown`** de 7 dias para npm e Go (atualizações de segurança continuam
  imediatas, se a opção existir; conferir na documentação).
- **D3 — `.gitignore`** cobre os nomes de arquivo que o runbook usa para a chave privada do `age`.
- **D4 — Marca d'água dos rollups no futuro** (salto de relógio) é limitada a "agora" em vez de congelar
  a consolidação.
- **D5 — Comentário desatualizado** da migration 00001 corrigido.

### 3.5 Painel e testes (Grupo E)

- **E1 — Passo 1 da troca trava o diálogo** enquanto o `rotate` está em voo (como os confirms).
- **E2 — Resposta perdida no confirm** (rede, 5xx de proxy, prazo): o diálogo diz que **não sabe** se a
  troca foi concluída e o que fazer (testar o app novo; se valer, os códigos antigos não valem mais);
  nunca "Nada mudou". O fechamento nesse estado usa o mesmo texto.
- **E3 — Confirms com prazo.** `totpConfirmar` e `totpConfirmarTroca` usam `AbortSignal.timeout(30 s)`;
  estourado, cai em E2.
- **E4 — Canais: guarda por canal.** Retomar/pausar um canal não bloqueia o botão de outro; o toast diz
  o nome do canal.
- **E5 — Aviso de código de recuperação possivelmente consumido** também no passo 1 da troca (como a
  troca de senha já faz) quando o rotate responde `401 unauthorized`.
- **E6 — Foco não cai no `<body>`** quando o botão "Trocar autenticador" desmonta (2FA desligado em
  outra aba): o foco vai para o título da seção ou para o botão de ativar.
- **E7 — CSP do E2E com barreira.** O `controlePositivo` passa a identificar a violação que ele mesmo
  provoca, esperar por ela, removê-la e exigir que o resto seja `[]`; chamado depois de cada diálogo
  (inclusive os três passos da troca e os diálogos de canal).
- **E8 — API falsa alinhada.** O MSW passa a reproduzir o servidor nas rotas dos Planos 8–10 (troca de
  senha mata a troca pendente, código de recuperação gasto não vale de novo, ordem senha→fator no
  rotate, `rotation`, família, 404/400 do PATCH de canal). Cada divergência corrigida ganha um teste
  que falharia contra a falsa antiga.
- **E9 — `TestRotateRoutesRefuseALockedAccount`** confere o `error.code`, não só o status. (Com A3 a
  semântica de conta bloqueada nas rotas autenticadas muda: o teste é reescrito para a regra nova.)

## 4. Contrato HTTP — o que muda

| Rota | Antes | Depois |
|---|---|---|
| `POST /auth/login`, `/auth/totp/verify` e rotas com Argon2 | — | `429 rate_limited` com `Retry-After` quando o semáforo esgota (A1) |
| Rotas autenticadas de credencial | conta bloqueada → `401 invalid_credentials` | não olham o bloqueio da conta; 5ª prova errada na família → `401 unauthorized` e família revogada (A3) |
| `POST /auth/totp/rotate` | `{secret, otpauth_uri}` | + `rotation` (A7) |
| `POST /auth/totp/rotate/confirm` | `{code}` | exige `rotation`; diferente → `403 forbidden` (A7) |
| `POST /auth/logout` | 204 mesmo com falha | `500 internal` se a revogação falhar (A9) |
| Rotas do TOTP, usuário apagado | 500 | `401 unauthorized` (A10) |
| `PATCH /targets/{id}` com URL de outra origem sem `headers` | mantinha os headers | apaga os headers (B1) |
| `POST`/`PATCH /targets`, `expected_status` 1xx/3xx | aceito | `400 validation_failed` (C2) |
| 404/405 sob `/api/` | texto puro | envelope JSON (B5) |
| Cookie novo | — | `vigilume_dispositivo` depois do login completo (A3) |

## 5. Fora de escopo (vão para `PENDENCIAS-PARA-O-PLANO-11.md`, com motivo)

- **Cotas por usuário no sondador e no despacho** (sondador S2–S3): contas só são criadas pelo operador
  (CLI); o abuso exige uma conta que o próprio dono criou. Registrar o limite e reavaliar se o cadastro
  for aberto.
- **IP público da VPS e IPv6 global da rede Docker fora da lista do safedial** (sondador S4): o Plano
  11 não publica porta além de 80/443 do Caddy; registrar como requisito do Plano 11.
- **`blocked` × `dns` enumera nomes internos** (sondador S5): baixo, só o dono vê.
- **Refresh repetido depois de resposta perdida vira "roubo"** (auth C2): pede janela de tolerância,
  que é mudança de desenho da detecção de reúso.
- **Checagem de `Host` contra `PUBLIC_BASE_URL`** (banco S1, parte): o compose de produção do Plano 11
  não tem o seed nem as chaves de demo; o `seed.sh` passa a recusar (D1).
- Itens de limpeza do code review (código repetido, round-trips, CHECKs do par pendente/família numa
  migration futura, comentários de sabotagem desatualizados).
- Tudo o que já estava em `PENDENCIAS-PARA-O-PLANO-10.md` e não foi tocado aqui segue para o Plano 12.

## 6. Decisões do controlador para o usuário revisar

Marcadas **[D]** acima. As que mais importam:

1. **A3 — cookie de dispositivo + contador por família** em vez do bloqueio só por conta. Custo se
   errado: um atacante que roube o cookie de dispositivo (HttpOnly) ganha 5 tentativas próprias a cada
   15 min, além das do IP; o contador por família e por dispositivo vive em memória e zera num restart.
2. **A1 — 429 quando o semáforo do Argon2 esgota**, capacidade `min(NumCPU, 4)`. Custo se errado: sob
   ataque, o dono também pode receber 429 no login por alguns segundos.
3. **B1 — trocar a origem da URL apaga os headers** em vez de recusar o PATCH. Custo se errado: quem
   troca de domínio precisa redigitar os headers (o painel avisa antes).
4. **A6 — rotate/confirm zeram o contador** (reverte a D5 do Plano 9).
5. **Cotas por usuário ficam fora** (§5).
