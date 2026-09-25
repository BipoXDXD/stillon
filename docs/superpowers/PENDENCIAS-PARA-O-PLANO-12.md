# Pendências depois do Plano 11

Este arquivo existe porque o ledger de execução vive em `.superpowers/`, fora do controle de
versão. O que está aqui é o subconjunto que não pode se perder. Ele substitui o
`PENDENCIAS-PARA-O-PLANO-11.md` como estado corrente; os anteriores ficam como histórico.

O Plano 11 pôs o Vigilume numa VPS da Hostinger (spec
`docs/superpowers/specs/2026-09-25-plano-11-hospedagem-design.md`): compose de produção com Caddy
em `deploy/vps/`, deploy e backup do GitHub Actions por SSH com chave restrita, vigia externo, e o
runbook `docs/deploy/vps.md`, escrito para um dono que nunca usou VPS. O Fly saiu do repositório
inteiro; o que era genérico no runbook dele (reset-2fa, operação do Postgres, restauração, tornar o
repositório público) migrou para o runbook novo. **A execução na VPS de verdade é feita com o dono,
guiada, depois do merge** (spec §1, critério 5): até lá, nada disto está no ar.

A ordem dos planos segue a de 2026-09-25: **12 = pendências** (as do Plano 9, as dívidas do grupo
C e o que os Planos 10 e 11 empurraram para cá), **13 = R2** (`auth` para sqlc).

## Fechadas no Plano 11: as 7 obrigações do Plano 10

Todas eram critério de pronto (`PENDENCIAS-PARA-O-PLANO-11.md`, "Obrigações do Plano 11"). A
coluna "Verificação" é o passo do runbook que a confere na VPS de verdade; a coluna "Prova no
repositório" é o que o CI roda.

| # | Obrigação | Como foi atendida | Prova no repositório | Verificação na VPS |
|---|---|---|---|---|
| 1 | O Caddy remove `Fly-Client-IP`, `X-Real-IP` e `CF-Connecting-IP` do cliente, e **não** configura `trusted_proxies` | `deploy/vps/Caddyfile`: um bloco de site só para o domínio, com a remoção dos três cabeçalhos na entrada, sem `trusted_proxies` (o Caddy substitui o `X-Forwarded-For` pelo IP real) | ⟦VPS: teste do CI que manda os cabeçalhos forjados e confere o IP visto⟧ | runbook 9.2 (o `429` continua com os cabeçalhos forjados) |
| 2 | `TRUSTED_PROXIES` = o IP fixo do Caddy, `CLIENT_IP_HEADER` vazio | Rede `vigilume` 172.30.0.0/24 fixada por `ipam` no `compose.prod.yaml`, Caddy em 172.30.0.2; o app com `TRUSTED_PROXIES=172.30.0.2/32` e `CLIENT_IP_HEADER` vazio | ⟦VPS: teste que lê o compose.prod.yaml e confere os dois valores⟧ | runbook 9.2 (sem o aviso da B3 no log; o teste de duas redes separa os baldes) |
| 3 | Só 80 e 443, do Caddy, publicadas; sem `ports:` no `db` e no `app`; sem IPv6 global na rede do compose | `compose.prod.yaml` com `ports` só no `caddy` e a rede sem IPv6; na VPS, `ufw` e o firewall do painel com 22, 80 e 443 | ⟦VPS: teste do compose de produção (portas publicadas e rede sem IPv6)⟧ | runbook 9.1 (`nmap` de fora, `ss -tlnp` de dentro) |
| 4 | `GOMEMLIMIT` no app e `mem_limit` em cada serviço | `compose.prod.yaml` | ⟦VPS: teste que confere `mem_limit` por serviço e `GOMEMLIMIT` abaixo do do app⟧ | runbook 9.5 |
| 5 | Compose de produção sem `seed`, sem `x-ambiente`, sem chave ou senha do repositório; segredos num `env_file` fora do git, gerados com `openssl rand`; `restart: unless-stopped` em tudo; o runbook diz que o `compose.yaml` não vai para o servidor e a `PUBLIC_BASE_URL` é `https://<domínio>` | `compose.prod.yaml` + `deploy/vps/env.exemplo` (modelo sem valores) + `deploy/vps/initdb.sh` (papéis a partir das senhas do ambiente); runbook "Como ler este runbook" (a frase literal) e 3.4 (segredos gerados na VPS, cópia no gerenciador) | ⟦VPS: teste que confere a ausência do seed e de chave do repositório no compose de produção⟧ | runbook 3.4 e 9.5 (`config --services` sem `seed`) e 9.3 (`Host` estranho não chega ao app) |
| 6 | Backup e deploy da VPS; a chave do `age` gerada fora do repositório | `deploy.yml` e `backup.yml` reescritos para `ssh deploy@VPS deploy …`/`backup`, com a chave presa a `deploy/vps/vigilume-entrada` por `restrict,command=` e `sudoers`; host key fixada em `VPS_HOST_KEY`; a chave do `age` nasce em `~/vigilume-backup.key`, fora do repositório (runbook 3.5) | ⟦VPS: testes do vigilume-entrada⟧; `deploy/ci/checar-workflows.sh` (job `workflows` do CI: nenhum `StrictHostKeyChecking=no`, nada do Fly) e `deploy/ci/vps.sh` (os códigos do deploy e do backup com mensagem) | runbook 4.4 (a chave só faz o que deve), 4.8 (primeiro deploy), 7.1 e 7.2 (backup e ensaio de restauração) |
| 7 | Um vigia externo do `/healthz` público | `deploy/vps/vigia.sh` + `vigilume-vigia.service` + `vigilume-vigia.timer`: a cada minuto, `curl` no `/healthz` público e ping no healthchecks.io (`HC_URL` no `.env`), `/fail` quando falha | ⟦VPS: teste do vigia.sh (ping de sucesso e /fail)⟧ | runbook 8 (derrubar o app de propósito e receber o e-mail) |

**Também fechado no Plano 11**, dos itens que o Plano 10 mandou para cá:

- **`age-keygen -o ~/vigilume-backup.key`, fora do repositório, no runbook** (banco S3 da revisão):
  runbook 3.5.
- **O Fly**: `docs/deploy/fly.md` apagado, com o genérico migrado para `docs/deploy/vps.md`
  (reset-2fa, operação do Postgres, restauração, "tornar o repositório público"); ⟦CONTROLADOR: o que o grupo vps removeu (`fly.toml`, `deploy/fly/`)⟧; os workflows não falam mais com o Fly
  (o `deploy/ci/checar-workflows.sh` do CI recusa qualquer menção fora de `docs/superpowers/`), e
  o Dependabot lê o digest do Postgres em `deploy/vps/db/Dockerfile`. Os secrets e variáveis de
  antes (`BACKUP_DATABASE_PASSWORD`, `AGE_RECIPIENT`, `PG_VOLUME_GB` e os tokens) saíram: o backup
  usa só `BACKUP_ENABLED`, e a chave pública do `age` mora em `/opt/vigilume/age-recipient`.
- **A checagem de `Host`**: o Caddy só tem o bloco do domínio, e uma requisição com outro `Host`
  não chega ao app (runbook 9.3). A checagem **no app** fica para o Plano 12 só se o cadastro abrir
  ou se aparecer outra porta de entrada.
- **Os itens do Fly em "Dependem do dono"** (apps, volume, tokens, `PG_VOLUME_GB`, medir o proxy
  do Fly): extintos com o Fly. O runbook manda apagar os secrets e a variável se existirem (4.6).

## Decisões do controlador para o dono revisar (spec do Plano 11, §9)

Tomadas com a autorização do dono ("tome as decisões por mim, exceto as cruciais"), marcadas
**[D]** na spec:

1. **Reinício automático às 04:30** (horário de Brasília) quando uma atualização de segurança
   pedir. Custo: ~1 min fora do ar de madrugada, e o vigia pode mandar um alarme falso curto (a
   carência de 5 min do healthchecks o absorve na maioria das vezes).
2. **`fail2ban` + SSH só por chave, porta 22.** Custo: perder a chave do computador = acesso só
   pelo terminal do painel da Hostinger (o runbook ensina a recuperação, bloco 10).
3. **healthchecks.io com timer na própria VPS**, em vez de um serviço que consulta de fora.
   Custo: se o DNS ou a rede entre a VPS e o mundo falhar de um jeito que o `curl` da própria VPS
   não veja, o alarme atrasa ou não vem.
4. **Scripts da VPS em shell** (o `vigilume-entrada`, o `vigia.sh`), testados no CI.
5. **Fly removido por inteiro** (decisão do dono), com o conteúdo genérico migrado.

E as do runbook (desta parte do Plano 11):

6. **O dono entra como um usuário administrador próprio com `sudo` e senha**, não como root; o
   `docker` só com `sudo` (o grupo `docker` equivale a root sem senha).
7. **Os secrets do GitHub vão pelo `gh secret set` lendo de arquivo**, sem colar valor em campo
   nenhum; a chave privada do deploy é apagada do computador depois (gera-se outra se precisar).
8. **O ensaio de restauração roda no computador do dono**, com a `MASTER_KEY` lida por
   `read -rs` (sem eco e sem histórico) e passada ao contêiner por `-e MASTER_KEY` sem valor.
9. **Sem registro `AAAA` nem `www`**: o Caddy atende um nome só, em IPv4.

## Limites conhecidos do Plano 11

- **A execução real não foi feita**: nenhum passo do runbook rodou numa VPS da Hostinger (spec §1,
  critério 5). Os comandos foram conferidos com `bash -n`, com o `--help` das ferramentas
  disponíveis e com a documentação pública (Hostinger, Resend, Docker, healthchecks.io); os rótulos
  do painel da Hostinger vêm da documentação dela e podem ter mudado.
- **O terminal do painel (Web console) depois do SSH endurecido**: a documentação da Hostinger não
  diz se ele depende do `sshd` nem com que usuário entra quando o root não pode entrar por SSH. O
  runbook manda testá-lo no passo 2.7, logo depois de endurecer, e aponta o **Emergency mode** se
  ele não abrir. Confirmar na execução guiada.
- **Segundos de `502` durante a troca de versão**, e o rollback automático não desfaz migration
  (as migrations são aditivas; spec §3).
- **O vigia mora na própria VPS** ([D] 3): uma falha de rede que o `curl` de dentro não vê atrasa
  o alarme.
- **O Docker não entra nas atualizações automáticas** (o `unattended-upgrades` só aplica as de
  segurança do Ubuntu): o runbook manda um `apt full-upgrade` mensal (bloco 10).
- **Os arquivos de `deploy/vps/` na VPS são copiados à mão**: o deploy troca só a imagem. Uma
  mudança no `compose.prod.yaml`, no `Caddyfile` ou nos scripts exige recopiar (runbook, bloco 10,
  "Arquivos da VPS").
- **Não há rotação pronta da `MASTER_KEY` nem das senhas do Postgres** (runbook, "Trocar um
  segredo").
- ⟦CONTROLADOR: os limites registrados pelos grupos vps e workflows⟧

## Fora de escopo do Plano 11, com destino

| Item | Origem | Destino |
|---|---|---|
| Aviso no boot quando `CLIENT_IP_HEADER` está preenchido | http S3 (parte), Plano 10 | Plano 12. O compose de produção o fixa vazio, e o aviso da B3 cobre o `TRUSTED_PROXIES` errado |
| Re-emitir o cookie de dispositivo nas rotas em que o dono logado muda a própria credencial | revisão final do Plano 10 | Plano 12 (o Plano 11 não mexeu em código do app) |
| Lista extra de bloqueio no safedial (`PROBE_DENY_CIDRS`) para o IP público da VPS | sondador S4 | Plano 12, só se o hairpin voltar a importar: a rede do compose não tem IPv6 e só o Caddy publica porta |
| `npm ci --ignore-scripts` no Dockerfile e no CI | banco S2 (segunda metade) | Plano 12 |
| Recusar o INSERT de check com `checked_at` muito à frente do `now()` do banco | banco C2 (segunda metade) | Plano 12 |
| Refresh repetido depois de resposta perdida vira "roubo" | auth C2 | Plano 12, com desenho próprio |
| `blocked` contra `dns` enumera os nomes internos da rede Docker | sondador S5 | Aceito; Plano 12 se o cadastro abrir |
| Cotas por usuário no sondador e no despacho | sondador S2 e S3 | Limite registrado; reavaliar se o cadastro abrir |

## Limpeza do code review do PR #15 (para o Plano 12)

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
  conhecidos"): re-emitir no Plano 12 (o Plano 11, de hospedagem, não mexeu em código do app).
- revisão final, `SameOrigin`: `internal/targets/validate.go` usa o `golang.org/x/net/idna` direto
  (versão do `go.mod`), e o `net/http` disca com a cópia embutida na biblioteca padrão (versão do
  Go). As tabelas Unicode das duas podem divergir num host novo, e a comparação deixa de ser "como
  o `net/http` disca" exatamente nesse host.
- revisão final, e2e: o `docker compose exec app /vigilume user reset-2fa` do `troca.spec`
  (`E2E_COMPOSE_SERVICE: app` no job `e2e` do `ci.yml`) só foi validado localmente; conferir no
  primeiro CI que a troca roda e não é pulada.


## Para o Plano 12 (limites do Plano 9, de `PENDENCIAS-PARA-O-PLANO-10.md`, ainda abertos)

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


## Limites conhecidos do Plano 10 (seguem valendo)

- **O contador por dispositivo e o contador por família vivem em memória** (A3): zeram num
  restart, e com `--role=api` em mais de um processo cada um conta o seu. Quem rouba o cookie de
  dispositivo (HttpOnly) ganha 5 tentativas próprias a cada 15 min, além das do IP.
- **Sob ataque ao Argon2id, o dono também pode receber `429` no login** por alguns segundos (A1).
- **Quem troca o domínio de um alvo redigita os headers** (B1); o painel avisa antes de salvar.
- **O `compose.yaml` copiado para um servidor atrás de um proxy, com a `PUBLIC_BASE_URL` ainda
  local, sobe com o usuário de demonstração** (revisão, banco-docs I1): o `seed.sh` (D1) e o boot
  só olham a URL declarada, e nenhum dos dois vê o `Host` que chega. O demo, com a senha pública,
  vira o dono. Em produção, resolvido pelo Plano 11 (obrigação 5, abaixo): o compose de produção não
  tem o seed nem chave do repositório, e o runbook diz que o `compose.yaml` não vai para o servidor.
  O limite continua valendo para quem copiar o `compose.yaml` à mão; a checagem de `Host` no app
  segue fora (tabela "Fora de escopo" do Plano 10).
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
  a mesma senha (quem o tem entrou com essa senha, que sozinha já abre a conta). Para o Plano 12
  (era do Plano 11, que ficou só na hospedagem): re-emitir o cookie na resposta das rotas em que o
  dono logado muda a própria credencial.
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

- **Executar o runbook `docs/deploy/vps.md` na VPS, guiado, um passo por vez** (spec §1, critério
  5): criar as contas (Resend, healthchecks.io), apontar o domínio, gerar os segredos, gravar os
  secrets `VPS_HOST`, `VPS_SSH_KEY` e `VPS_HOST_KEY` e a variável `BACKUP_ENABLED`, o primeiro
  deploy, o 2FA e o ensaio de restauração.
- Revisar as decisões **[D]** acima (spec do Plano 11, §9) e as da spec do Plano 10 (§6): cookie
  de dispositivo e contador por família (A3), `429` do semáforo do Argon2 (A1), trocar a origem da
  URL apaga os headers (B1), `rotate`/`rotate/confirm` zeram o contador (A6), cotas por usuário
  fora.
- Opcional: GitHub Pro (~US$ 4/mês) dá secrets por environment num repositório privado (não o
  revisor obrigatório).
- Escolher a licença e tornar o repositório e o pacote do GHCR públicos, quando quiser (runbook,
  "Tornar o repositório público").

## Herdadas, ainda abertas

**R2** (Plano 13): migrar `auth.PGStore` e `auth.TOTP` para sqlc. O `ConsumeChallenge` do Plano 8,
os métodos da troca de autenticador do Plano 9 (`internal/auth/totp_rotation.go`) e o SQL novo do
Plano 10 no `auth` (`ReserveAttempt` e `RefundAttempt` em `internal/auth/user.go`, o `fatorTravado`
e o consumo do timestep com CAS em `internal/auth/totp.go` e `internal/auth/totp_rotation.go`, e
`Tx.UserTOTPSecret` em `internal/auth/store.go`) também são SQL à mão.
