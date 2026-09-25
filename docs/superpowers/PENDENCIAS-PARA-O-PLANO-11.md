# Pendências depois do Plano 10

Este arquivo existe porque o ledger de execução vive em `.superpowers/`, fora do controle de
versão. O que está aqui é o subconjunto que não pode se perder. Ele substitui o
`PENDENCIAS-PARA-O-PLANO-10.md` como estado corrente; os anteriores ficam como histórico.

A ordem dos planos mudou em 2026-09-25 (spec do Plano 10): **11 = hospedagem** na Hostinger (o
desenho tem as seções 1–3 aprovadas pelo usuário), **12 = pendências** (as do Plano 9 e as dívidas
do grupo C), **13 = R2** (`auth` para sqlc). Por isso este arquivo tem duas listas de trabalho: o
que é **obrigação do Plano 11** e o que vai **para o Plano 12**.

## Fechadas no Plano 10

A revisão do projeto inteiro antes de hospedar (relatórios em
`docs/superpowers/revisao-2026-09-25/`). Spec: `docs/superpowers/specs/2026-09-25-plano-10-revisao-design.md`.
Todo achado Alto e Médio foi corrigido, e os Baixos baratos ou que tocam segurança também. Cada
linha diz de onde veio o achado (relatório e número).

| Item | Achado | Como fechou |
|---|---|---|
| A1 | auth S1 (Alto) | Semáforo global no Argon2id, em toda verificação e todo hash, inclusive o `VerifyDummy`: `min(NumCPU, 4)` vagas, configurável por `ARGON2_MAX_CONCURRENT` (1–64); sem vaga em 2 s, `429 rate_limited` com `Retry-After: 2`, só nas rotas que pagam Argon2id (login, troca de senha, `enroll`, `disable`, `rotate`; o `/totp/verify` e os confirms não o rodam). Os parâmetros do Argon2 não mudaram |
| A2 | auth S2 (Alto) | A tentativa é **reservada** num `UPDATE … RETURNING` antes de verificar (antes do Argon2, no login); N requisições paralelas gastam N tentativas. O sucesso que **termina** o login zera; a senha certa com o 2FA pela frente só devolve a própria reserva (zerar ali daria a quem tem a senha um botão de destravar entre dois chutes de seis dígitos) |
| A3 | auth S3 e S5, http S1 (Médios e Baixo) | Cookie de dispositivo `vigilume_dispositivo` depois do login completo (MAC com chave derivada da `MASTER_KEY` por HKDF, 180 dias, nada no banco): quem o apresenta não é barrado pelo bloqueio da conta, no login **e** no `/totp/verify`, e as falhas dele contam num contador por dispositivo em memória. As rotas autenticadas de credencial (troca de senha, `enroll`, `confirm`, `disable`, `rotate`, `rotate/confirm`) deixaram de olhar e de alimentar o bloqueio da conta e dividem um contador por família; a 5ª prova errada revoga a família (`401 unauthorized`, auditoria `session.proof_limit`). O `/totp/confirm`, que antes não contava falha nenhuma, conta (fecha o S5; as quatro primeiras continuam `400 validation_failed`). O comentário de `routes.go` sobre o burst do login foi corrigido |
| A4 | auth S4, code review 1 e 2 (Médio) | O consumo do timestep faz CAS em `totp_secret_enc`; a sessão depois do 2FA confere, sob a trava, que o segredo validado ainda vale; o `disable` confere, sob a trava da linha, que o timestep ainda não foi usado, na mesma transação que apaga o segredo (gravar o timestep ali não teria efeito observável); o código do login não desliga o 2FA |
| A5 | code review 6 (Médio) | `disable` toma a trava de sessões primeiro e segue a ordem global (advisory → `users` → `totp_recovery_codes` → `sessions` → `audit_log`); `StartRotation` verifica a prova dentro da transação, depois de reconferir a família |
| A6 | code review 3 e 15 | `reset-2fa` zera `failed_attempts` e `locked_until` quando havia 2FA ligado (sem 2FA ele diz "nada mudou" e não muda nada, nem o bloqueio); `rotate` e `rotate/confirm` bem-sucedidos zeram o contador (revê a D5 do Plano 9) |
| A7 | code review 7 | `rotate` devolve `rotation`; `rotate/confirm` o exige; ausente ou diferente → `403 forbidden`, antes do código e sem contar falha |
| A8 | auth C1 | Código de recuperação sem espaços nem hífens e em **maiúsculas** antes do hash; os gravados continuam válidos. Conferido: o formato gerado é base32 sem padding, maiúsculo (`^[A-Z2-7]{16}$`, preso em `TestRecoveryCodesAreNormalized`); baixar a caixa invalidaria todos os códigos já gravados, cujo hash é do texto maiúsculo |
| A9 | auth C3 | Logout com falha na revogação responde `500 internal` e não apaga o cookie |
| A10 | limite do Plano 9 ("usuário apagado no meio de um rotate ou confirm vira 500") | As rotas do TOTP respondem `401 unauthorized` |
| B1 | http S2 (Médio) | `PATCH` que muda a origem da URL sem `headers` apaga os headers guardados; o painel avisa antes de salvar |
| B2 | http S4 | Várias linhas de `X-Forwarded-For` são lidas juntas, na ordem |
| B3 | http S5, auth S6 | Com `https`, um `X-Forwarded-For` vindo de fora de `TRUSTED_PROXIES` gera um aviso por minuto com o `remote_addr` |
| B4 | http S3 (Médio) | O comentário da config e o README dizem que `CLIENT_IP_HEADER` só serve atrás de proxy que **substitui** o cabeçalho; atrás do Caddy, vazio. A remoção dos cabeçalhos no Caddy é obrigação do Plano 11 (abaixo) |
| B5 | http C2 | 404 e 405 sob `/api/` no envelope, com `trace_id`: `404 not_found` e o código novo `405 method_not_allowed` (o `Allow` do ServeMux fica) |
| B6 | http C1 | A série do detalhe não depende do relógio do navegador: `GET /targets/{id}/checks?window=1h|6h|24h|7d|30d|90d`, com `from = to − janela` no relógio do servidor; `window` vazio, fora da lista ou junto com `from` → `400 validation_failed` (`window`/`invalid`); o painel manda só o `window` |
| C1 | sondador S1 (Alto) | `MaxResponseHeaderBytes: 64 << 10` no transporte do safedial; acima disso, `down` com `error_kind` `unknown` (o `net/http` não tem erro próprio para isso; casar o texto da biblioteca quebraria calado). Vale para todo cliente do `safedial` (sondador, Discord, Resend) |
| C2 | sondador C1 | `expected_status` só em 200–299 e 400–599 (`400 validation_failed`, `out_of_range`) |
| C3 | sondador C2 | Excesso de redirecionamentos e esquema inválido no `Location` deixam de ser `blocked`: `unknown` (sentinela `safedial.ErrRedirect`); redirecionamento para endereço interno continua `blocked`, e no despacho de avisos continua falha definitiva de canal |
| C4 | sondador C3 | Trocar URL ou método zera a histerese do alvo (`last_checked_at` fica: é a guarda de check fora de ordem). O incidente aberto **não** fecha no PATCH: fecha pelos sucessos da URL nova, com o aviso "voltou" (limite abaixo) |
| D1 | banco S1 (Alto) e C1 (Médio) | O `seed.sh` recusa rodar (sai 1, antes de chamar o binário) se `PUBLIC_BASE_URL` não for `http` em `localhost`, `127.0.0.0/8` ou `[::1]`; `restart: unless-stopped` no `db` do `compose.yaml`; o cabeçalho do compose diz, em destaque, que ele é só para a máquina local |
| D2 | banco S2 | `cooldown: default-days: 7` no Dependabot de Go e de npm. Pela documentação do GitHub, o cooldown só vale para atualização de versão: as de segurança não esperam |
| D3 | banco S3 | `.gitignore` com `*.key`, `*.dump`, `*.age` e `.env.*` (os nomes do runbook: `vigilume-backup.key`, `vigilume.dump`, `vigilume-AAAAMMDD.dump.age`) |
| D4 | banco C2 | A marca d'água dos rollups é o último rollup **antes** da hora corrente (`max(bucket) WHERE bucket < ate`): um rollup no futuro não a congela, e (desde a revisão, M1) também não esconde a última marca real, então uma parada de mais de 2 h com um rollup no futuro ainda recupera as horas perdidas (`TestConsolidationCatchesUpAfterAGapWithAWatermarkInTheFuture`) |
| D5 | banco C3 | Comentário da migration 00001 diz que a produção roda o 18 |
| E1–E6 | code review 4, 5, 8, 9, 10, 11 | Painel: passo 1 da troca trava o diálogo; resposta perdida no confirm diz que **não sabe** se a troca aconteceu; confirms com prazo de 30 s; guarda por canal; aviso de código de recuperação possivelmente gasto no passo 1; foco não cai no `<body>` (vai ao botão Ativar/Desativar ao lado). Textos finais que diferem da spec: o aviso de canal usa o `hint`, porque o canal não tem nome (`Canal discord.com/…/abcd retomado.`); o 429 diz `Muitas tentativas. Aguarde N segundos e tente de novo.` com o `Retry-After`; o aviso de código de recuperação gasto aparece em qualquer 401 depois de um código que não tem 6 dígitos, não só no `unauthorized` |
| E7 | code review 12; limite do Plano 9 ("as checagens de CSP dos diálogos de canal leem as violações uma vez") | O controle positivo da CSP no E2E identifica e espera a própria violação e exige o resto vazio, depois de cada diálogo |
| E8 | code review 13 | A API falsa (MSW) reproduz o servidor nas rotas dos Planos 8–10 |
| E9 | code review 14 | `TestRotateRoutesRefuseALockedAccount` confere o `error.code`, reescrito para a regra da A3: agora `TestRotateRoutesIgnoreTheAccountLock` (`internal/httpapi/totp_troca_integration_test.go`) |
| Plano 9 | limites "O rotate gasta a prova antes da trava" e "Um `disable` entre o `Verify` e o `UPDATE` do `StartRotation`" | Fechados pela A5: o `StartRotation` toma a trava de sessões, reconfere a família e só então lê a linha de `users` com `FOR NO KEY UPDATE` e confere a prova (`fatorTravado.provar`). Família morta não gasta nada (`TestStartRotationSpendsNothingForADeadFamily`); um `disable` concorrente espera a mesma trava, e o rotate que vem depois vê o 2FA desligado e responde `403` sem gastar a prova |

## Fora de escopo do Plano 10 (spec, §5), item a item

| Item | Origem | Motivo | Destino |
|---|---|---|---|
| Cotas por usuário no sondador (as 64 vagas globais) e no despacho (4 avisos/s em FIFO, cota do Resend) | sondador S2 e S3 (Médios) | Contas só são criadas pelo operador, pelo CLI; o abuso exige uma conta que o próprio dono criou | Limite registrado. Reavaliar se o cadastro for aberto |
| IP público da VPS e IPv6 global da rede Docker fora da lista do safedial (hairpin por fora do firewall externo) | sondador S4 | O compose de produção não publica porta além de 80/443 do Caddy | **Requisito do Plano 11** (abaixo). Uma lista extra (`PROBE_DENY_CIDRS`) fica para o Plano 12, se o Plano 11 não bastar |
| `blocked` contra `dns` enumera os nomes internos da rede Docker | sondador S5 | Baixo: só informação, e só o dono vê | Aceito como está; Plano 12 se o cadastro abrir (recusar host sem ponto) |
| Refresh repetido depois de resposta perdida vira "roubo" (família revogada, auditoria `session.reuse_detected` falsa) | auth C2 | Pede janela de tolerância, que é mudança de desenho da detecção de reúso | Plano 12, com desenho próprio |
| Checagem de `Host` contra `PUBLIC_BASE_URL` | banco S1, parte | O compose de produção do Plano 11 não tem o seed nem as chaves de demo. O `seed.sh` (D1) **não** cobre este caso: ele só olha a URL declarada, e atrás de um proxy com a `PUBLIC_BASE_URL` local ele e o boot aceitam (limite abaixo; revisão, banco-docs I1) | **Requisito do Plano 11** (obrigação 5); a checagem no app, Plano 12, se o Plano 11 não a tornar necessária |
| Limpeza do code review (lista abaixo) | code review do PR #15 | Confirmados, sem efeito para o usuário | Plano 12, exceto o que o Plano 10 tocou de passagem |
| O que estava em `PENDENCIAS-PARA-O-PLANO-10.md` e não foi tocado | — | Fora do tema da revisão | Plano 12 (abaixo) |

Os achados da revisão que nenhuma tabela acima cobre ficaram de fora por serem sugestões parciais
de um item corrigido:

- **`npm ci --ignore-scripts` no Dockerfile e no CI** (banco S2, segunda metade da correção): o
  Plano 10 fez só o cooldown, que é o que a spec pediu. Destino: Plano 12.
- **`age-keygen -o ~/vigilume-backup.key`, fora do repositório, no runbook** (banco S3): o
  `.gitignore` já cobre o nome no diretório de trabalho. Destino: o runbook do Plano 11, que é
  onde o backup da VPS vai ser descrito.
- **Recusar o INSERT de check com `checked_at` muito à frente do `now()` do banco** (banco C2,
  segunda metade): o limite da marca d'água já desfaz o congelamento. Destino: Plano 12.
- **A janela de horas brutas do resumo com a marca no futuro** (banco C2): `Summary` já limita o
  corte à hora corrente, e a última hora fechada fica de fora só até a próxima rodada da
  manutenção consolidá-la: ela não congela, e a marca d'água ignora o rollup do futuro, então
  nem uma parada de mais de 2 h a perde (M1 da revisão). Aceito como está.
- **Aviso no boot quando `CLIENT_IP_HEADER` está preenchido fora do Fly** (http S3, parte da
  correção): a B4 ficou na documentação, e o Plano 11 fixa `CLIENT_IP_HEADER` vazio no compose
  de produção. Destino: Plano 12, se o Plano 11 não o fizer.

## Obrigações do Plano 11 (hospedagem)

O Plano 10 fechou o que era código; estes itens são configuração da hospedagem e fazem parte do
critério de pronto do Plano 11. Cada um vem de um achado da revisão.

1. **O Caddy remove, na entrada, os cabeçalhos `Fly-Client-IP`, `X-Real-IP` e
   `CF-Connecting-IP` mandados pelo cliente** (http S3, B4). Por padrão o Caddy repassa todo
   cabeçalho do cliente e só reescreve os `X-Forwarded-*`. E o Caddy **não** configura
   `trusted_proxies` (sem CDN na frente): assim ele substitui o `X-Forwarded-For` pelo IP real.
2. **`TRUSTED_PROXIES` = o IP fixo do Caddy** na rede do compose (fixado com `ipam`), com
   **`CLIENT_IP_HEADER` vazio**: o app lê o último salto do `X-Forwarded-For`. Vazio, todo cliente
   divide o balde de rate limit do login, e um atacante tranca o dono para fora por IP (banco,
   "Observações para a VPS"). O aviso da B3 no log diz quando o valor está errado.
3. **Só as portas 80 e 443, do Caddy, são publicadas.** Nenhum `ports:` no `db` nem no `app`
   (sondador S4: o sondador alcança o IP público da própria VPS por hairpin, por fora do
   firewall externo da Hostinger). A 5432 **não** é publicada nem para backup: o Docker ignora o
   UFW. Sem IPv6 global na rede do compose (ou, com ele, o prefixo entra numa lista de bloqueio
   do safedial, que hoje não existe).
4. **`GOMEMLIMIT` no app e `mem_limit` em cada serviço** (auth S1 e sondador S1: o semáforo do
   Argon2 e o teto dos cabeçalhos limitam os picos conhecidos; o limite do contêiner impede que
   um pico desconhecido leve a VPS inteira, e o `GOMEMLIMIT` abaixo dele faz o GC trabalhar
   antes do OOM).
5. **Compose de produção sem o `seed`, sem o bloco `x-ambiente` do `compose.yaml` e sem nenhuma
   chave ou senha do repositório** (banco S1): segredos num `env_file` fora do git,
   `POSTGRES_PASSWORD` e a senha do `vigilume_app` geradas (`openssl rand -hex 32`),
   `restart: unless-stopped` em todos os serviços (banco C1). É **a única** proteção contra o
   `compose.yaml` local copiado para o servidor atrás do Caddy com a `PUBLIC_BASE_URL` ainda
   local (o seed e o boot aceitam; revisão, banco-docs I1): o runbook do Plano 11 diz, com essas
   palavras, que o `compose.yaml` não vai para o servidor, e a `PUBLIC_BASE_URL` de produção é
   `https://<domínio>` (com ela o boot recusa as chaves de demo).
6. **Backup e deploy da VPS.** O `backup.yml` e o `deploy.yml` só falam com o Fly: na VPS não há
   backup nenhum até isso ser adaptado. O runbook novo gera a chave do `age` fora do repositório.
7. **Um vigia externo do `/healthz` público** (banco C1): o Vigilume não consegue avisar da
   própria queda.

## Limpeza do code review (para o Plano 12)

Confirmados no code review do PR #15 e cortados do Plano 10 por não mudarem o comportamento. Conferido
contra os diffs dos grupos A e E na integração: nenhum item saiu inteiro; um mudou de forma (abaixo).

- CHECKs no banco para o par segredo pendente / família (`totp_pending_secret_enc` e
  `totp_pending_family_id` juntos), numa migration futura.
- `Disable` repete o SQL de `ResetTOTP` e `DeleteRecoveryCodes`.
- A reserva da prova (`provas.reservar`, `defer Release`, `Succeed`) repetida nos 6 handlers de
  credencial. O bloco "registra a falha e responde 401", que era copiado 5 vezes, virou uma chamada
  a `provasDaFamilia.errou` na A3.
- Trava de sessões + `FamilyIsLive` repetidos 4 vezes.
- `useEnvioUnico` local no painel, em vez de compartilhado.
- `GetChannelView` repete a query de `ListChannels`.
- `substituirCodigos` faz 11 idas ao banco sob a trava.
- `PaginaCanais` refaz o `GET` depois do `PATCH`, que já devolve o canal.
- Comentários de sabotagem desatualizados nos testes.
- A dica de porta de métricas ocupada nunca aparece no Windows (o erro não é `EADDRINUSE`).

## Minors da revisão do Plano 10 (para o Plano 12)

Achados menores das quatro revisões por grupo (`.superpowers/sdd/2026-09-25-revisao/revisao-*.md`)
que a onda de correção não fechou. Os que ela fechou: auth M1 (a espera do Argon2id respeita o
`ctx`), banco-docs M1 (marca d'água), m2 (`.gitignore`) e m3 (README do `reset-2fa`), painel I1
(prazo do rotate) e M2 (a troca no CI), http Q2 (a lista de divergências, com o Q1).

- auth M2: `FamilyProofs.Fail` decide pelo `n` da hora da reserva; se reservas anteriores em voo
  forem devolvidas (um `429` do semáforo), a 5ª reserva que errar derruba a família com menos de 5
  erros reais. Só com requisições simultâneas da mesma família; não é explorável por terceiros.
- auth M3: a família parada é esquecida em 24 h (`familiaParada`): um ladrão com a sessão (30 dias de
  refresh) tem 4 chutes de senha por dia, além dos de cada restart. O D3 só cita o restart.
- painel M1: na ativação, repetir o confirm depois de "não se sabe" termina no toast "já está
  ativada" (o `403`), sem repetir que os códigos desta ativação se perderam; a troca trata o `403`
  depois da resposta perdida, a ativação não.
- painel M4a: o teste "a barreira da CSP pega a violação que ainda não chegou" (`a11y.spec.ts`) fica
  verde sem a espera (sabotagem S2 declarada); quem prova a espera é o do coletor quebrado. O nome
  promete mais do que verifica.
- painel M4b: o PATCH de alvo da API falsa compara a origem pela regra WHATWG do painel, e não pelo
  `SameOrigin` do servidor: um teste de IPv4 não canônico ou de host que o `ToASCII` recusa passa
  contra a falsa e diverge do servidor. A falsa não serve de prova nesse ponto.
- painel M5: em Canais, o `aria-disabled` não segura o clique: entre o `recarregar()` do Retomar e o
  `finally`, o botão já diz "Pausar", o clique abre o Confirmar e o `finally` o fecha em seguida (uma
  piscada; nada é enviado).
- banco-docs m1: o `seed.sh` aceita URLs inválidas que o boot recusa (`http://localhost:evil.com`,
  `http://[::1]:8080.evil.com`, quebra de linha na `PUBLIC_BASE_URL`): o host real nunca é outro,
  mas o usuário demo já foi criado quando o boot falha. Endurecer: o resto depois do host vazio ou
  `:[0-9]+`, e recusar espaço, tab e quebra de linha.
- revisão final, conta: a troca de senha consome o código do 2FA ou o de recuperação
  (`s.totp.Verify`) **antes** do `auth.HashPassword` da senha nova (`internal/account/account.go`,
  `ChangePassword`); sem vaga no semáforo do Argon2id, ela responde `429` com o código já gasto. O
  painel (`erroDeCredencial` em `PaginaConfiguracoes.tsx`) só avisa do código de recuperação
  possivelmente gasto em `401` ou resposta perdida, não no `429`.
- revisão final, cookie de dispositivo: o carimbo não muda com o encerramento de sessões (nem com
  "encerrar todas") nem com a troca de autenticador; só com a troca de senha, o `set-password`, o
  `reset-2fa` e a ativação ou o disable do 2FA. Quem teve acesso completo (senha e 2FA) pode cunhar
  vários cookies antes de perdê-lo, e cada um tem o seu contador (5 chutes a cada 15 min) no
  `/totp/verify`, fora do bloqueio da conta. A remediação é trocar a senha, e o painel e o README não
  dizem isso. O mesmo carimbo derruba o cookie do próprio dono nessas operações (ver "Limites
  conhecidos"): re-emitir no Plano 11.
- revisão final, `SameOrigin`: `internal/targets/validate.go` usa o `golang.org/x/net/idna` direto
  (versão do `go.mod`), e o `net/http` disca com a cópia embutida na biblioteca padrão (versão do
  Go). As tabelas Unicode das duas podem divergir num host novo, e a comparação deixa de ser "como
  o `net/http` disca" exatamente nesse host.
- revisão final, e2e: o `docker compose exec app /vigilume user reset-2fa` do `troca.spec`
  (`E2E_COMPOSE_SERVICE: app` no job `e2e` do `ci.yml`) só foi validado localmente; conferir no
  primeiro CI que a troca roda e não é pulada.

## Para o Plano 12 (de `PENDENCIAS-PARA-O-PLANO-10.md`, ainda abertas)

Os limites conhecidos do Plano 9 que o Plano 10 não fechou:

- **Pausar descarta os avisos.** Retomar não reenvia o que passou; o diálogo de "Pausar" diz
  isso.
- **Um canal pausado só aparece na lista de canais**: o dashboard não avisa.
- **O código de recuperação usado para iniciar uma troca fica gasto**, mesmo que a troca não seja
  confirmada. O painel avisa ao fechar no passo 2 (e, desde o Plano 10, no 401 do passo 1).
- **O segredo pendente não tem prazo**, mas é da família que o criou e morre com ela (confirmação,
  nova troca, `disable`, `reset-2fa`, troca de senha, `set-password`).
- **`PATCH /channels/{id}` com campo desconhecido** responde `400 validation_failed` sem `fields`.
- **O `reset-2fa` não zera a marca do desafio** (`totp_challenge_mark`).
- **Porta 9091 ocupada por outro programa** em desenvolvimento: o `--role=prober` não sobe.
- **Uma notificação já reivindicada pode sair logo depois de uma pausa.**
- **No painel, o `/me` relido no passo 2 da troca** fecha o diálogo pelo portão, sem o aviso de
  "nada mudou".
- **Um 403 na confirmação da troca perde o aviso de código de recuperação gasto.**
- **O E2E de acessibilidade falhou uma vez sem causa encontrada.** Se repetir, guardar o trace.
- **`ROLE` inválido fica em `cfg.Role`** (`internal/config/config.go`), inofensivo porque o `Load`
  devolve `nil, err`.
- **Os testes de trava procuram a espera pelo `pg_stat_activity` sem amarrar o backend** (a lista
  completa dos testes está no `PENDENCIAS-PARA-O-PLANO-10.md`).

Os limites dos Planos 7 e 8 seguem como estão no `PENDENCIAS-PARA-O-PLANO-8.md` e no
`PENDENCIAS-PARA-O-PLANO-9.md`.

**Dívidas menores** (grupo C): a seção "Dívidas menores herdadas, ainda abertas" do
`PENDENCIAS-PARA-O-PLANO-5.md`, inteira.

## Limites conhecidos do Plano 10

- **O contador por dispositivo e o contador por família vivem em memória** (A3): zeram num
  restart, e com `--role=api` em mais de um processo cada um conta o seu. Quem rouba o cookie de
  dispositivo (HttpOnly) ganha 5 tentativas próprias a cada 15 min, além das do IP.
- **Sob ataque ao Argon2id, o dono também pode receber `429` no login** por alguns segundos (A1).
- **Quem troca o domínio de um alvo redigita os headers** (B1); o painel avisa antes de salvar.
- **O `compose.yaml` copiado para um servidor atrás de um proxy, com a `PUBLIC_BASE_URL` ainda
  local, sobe com o usuário de demonstração** (revisão, banco-docs I1): o `seed.sh` (D1) e o boot
  só olham a URL declarada, e nenhum dos dois vê o `Host` que chega. O demo, com a senha pública,
  vira o dono. Só o compose de produção do Plano 11 resolve (obrigação 5); a checagem de `Host`
  no app ficou fora (tabela do "fora de escopo").
- **O `seed.sh` só aceita `localhost`, `127.0.0.0/8` em IPv4 decimal e `[::1]`**: formas que o
  boot aceita e o seed não (como `[::ffff:127.0.0.1]`) são recusadas pelo seed, que é o lado
  seguro. O teste roda o script no `sh` do host (o `dash` no CI); o `busybox sh` da imagem
  `tools`, que é o que roda de verdade, só é exercitado pelo caminho local, no job `e2e`
  (`deploy/ci/checar-compose.sh`).
- **O rollup gravado no futuro fica no banco** (D4) até a retenção o apagar pela idade; a barra da
  status page o mostra na hora dele quando o tempo real chegar lá.
- **O cookie de dispositivo morre com as credenciais** (I1 da revisão): o MAC cobre o hash da senha
  e a data de ativação do 2FA, então a troca de senha, o `set-password`, o `reset-2fa`, a ativação
  e o disable do 2FA invalidam todos os cookies da conta, **os do dono inclusive**: ele volta ao
  bloqueio da conta até o próximo login completo (o mesmo custo do D7 com a `MASTER_KEY`); o
  rehash do Argon2id no login (parâmetros novos) também. A troca de autenticador **não** muda o
  carimbo: quem tinha a senha e a semente e guardou cookies continua com eles até o dono trocar
  a senha. Um cookie cunhado com o 2FA desligado volta a valer se o 2FA for ligado e resetado com
  a mesma senha (quem o tem entrou com essa senha, que sozinha já abre a conta). Para o Plano 11:
  re-emitir o cookie na resposta das rotas em que o dono logado muda a própria credencial.
- **O dono num navegador novo (ou com os cookies limpos) enquanto um anônimo mantém a conta
  travada** não tem cookie de dispositivo: espera os 15 min ou usa `vigilume user set-password`
  (A3, D6 do plano).
- **Painel velho em cache depois do deploy**: o `rotate/confirm` sem `rotation` recebe `403`, e o
  painel antigo diz "concluída ou cancelada em outra aba" até o dono recarregar (A7).
- **A 5ª prova errada numa rota autenticada desloga** sem explicação própria: o painel mostra "O
  acesso desta tela venceu." e, ao renovar, "Sua sessão terminou" (A3).
- **Sonda da URL antiga em voo durante o PATCH** entra na histerese zerada como o primeiro check da
  URL nova (C4). E o incidente aberto da URL antiga fecha pelos sucessos da nova, com a duração
  somada (H10 do plano).
- **O aviso do painel e o servidor divergem em hosts exóticos** (B1): IPv4 em hexadecimal, ponto
  final no host, host que o navegador aceita e o `idna.Lookup.ToASCII` recusa. O servidor apaga os
  headers (o lado seguro) e o painel pode não avisar. Desde a revisão (Q1), o servidor compara o
  host como o `net/http` o disca (`idna.Lookup.ToASCII`, o mesmo UTS 46 do `new URL().origin`):
  `straße.de` e `STRAẞE.de` são origens diferentes (discam `xn--strae-oqa.de` e `strasse.de`), e
  Unicode × punycode do mesmo host é a mesma origem (`TestSameOriginAgreesWithTheDialedHost`).
- **Alvo com `expected_status` 1xx ou 3xx gravado antes do Plano 10** (só pela API) continua no
  banco, e todo PATCH nele responde `400 expected_status/out_of_range` até o valor ser corrigido (C2),
  inclusive o `{"enabled": false}` do Pausar do painel, que não destaca campo nenhum: justamente o
  alvo que está `down` para sempre não pode ser pausado (revisão, http-sondador Q3).
- **HTTP/2 com cabeçalho de resposta enorme** (C1): o teto vale, mas os testes só exercitam HTTP/1.
- **O aviso de `TRUSTED_PROXIES` (B3) não distingue lista errada de porta do app exposta**: um
  cliente falando direto com a porta publicada gera o mesmo aviso.
- **Resposta perdida de um confirm que o servidor comitou**: o painel diz que não sabe e ensina a
  conferir; nenhum teste passa por um servidor que comita e perde a resposta (E2).
- **Dois canais mudando juntos e os dois falhando**: a tela mostra um erro só, o último (E4).
- **Logout com o banco fora por muito tempo**: cada "Sair" mostra o erro e a aba continua dentro
  (A9); não há "sair só desta aba". O mesmo vale para o `502`/`504` sem envelope do proxy com o
  binário fora (o caso mais provável atrás do Caddy do Plano 11): o painel diz "Resposta inesperada
  do servidor (502)" e a sessão local fica, enquanto sem rede ela é largada (P8; revisão, painel M3).

## Dependem do dono do projeto (nenhum agente faz)

- **A hospedagem é a Hostinger (Plano 11).** Os itens do Fly que estavam aqui (criar os apps,
  o volume, os papéis e os segredos no Fly; gravar `FLY_DEPLOY_TOKEN`, `FLY_BACKUP_TOKEN`,
  `BACKUP_DATABASE_PASSWORD`, `AGE_RECIPIENT`, `PG_VOLUME_GB` e `BACKUP_ENABLED`; medir o proxy do
  Fly para o `TRUSTED_PROXIES`; o tráfego do Fly sem `Fly-Client-IP`) ficam suspensos. O Plano 11
  decide se o runbook `docs/deploy/fly.md`, o `deploy.yml` e o `backup.yml` saem ou ficam.
- Contratar a VPS e o domínio (tem custo recorrente; a decisão é do dono).
- Opcional: GitHub Pro (~US$ 4/mês) dá secrets por environment num repositório privado.
- Escolher a licença e tornar o repositório e o pacote do GHCR públicos, quando quiser (runbook
  do Fly, última seção; o Plano 11 pode mover a seção).
- Revisar as decisões **[D]** da spec do Plano 10 (§6): cookie de dispositivo e contador por
  família (A3), `429` do semáforo do Argon2 (A1), trocar a origem da URL apaga os headers (B1),
  `rotate`/`rotate/confirm` zeram o contador (A6), cotas por usuário fora.

## Herdadas, ainda abertas

**R2** (Plano 13): migrar `auth.PGStore` e `auth.TOTP` para sqlc. O `ConsumeChallenge` do Plano 8,
os métodos da troca de autenticador do Plano 9 (`internal/auth/totp_rotation.go`) e o SQL novo do
Plano 10 no `auth` (`ReserveAttempt` e `RefundAttempt` em `internal/auth/user.go`, o `fatorTravado`
e o consumo do timestep com CAS em `internal/auth/totp.go` e `internal/auth/totp_rotation.go`, e
`Tx.UserTOTPSecret` em `internal/auth/store.go`) também são SQL à mão.
