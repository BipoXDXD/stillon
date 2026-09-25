# Auditoria: banco/RLS, supply chain/CI, configuração/segredos (+ correção)

Repositório: /home/bipo/Projetos/Go/Vigilume @ 640cd9e (main). Somente leitura; nada editado.

Contexto que muda o peso dos achados: o sistema é **de usuário único** (`account.CreateUser` recusa
com `ErrUserExists` quando já existe qualquer usuário, spec §2.1). Isolamento entre usuários pela
RLS é, na prática, defesa contra bug de lógica; as fronteiras que importam de verdade são os
escopos **não autenticados** (público `/p/{slug}` e verificação `/verify/{token}`) e o sondador.

Resumo: **Crítico 0 · Alto 1 · Médio 1 · Baixo 4**

| # | Sev. | Seção | Título |
|---|---|---|---|
| S1 | Alto | Segurança | `compose.yaml` levado à VPS cria o único usuário com senha pública, e o portão das chaves de demo depende só do `PUBLIC_BASE_URL` declarado |
| C1 | Médio | Correção | `db` do compose sem `restart:`: depois de reboot da VPS o Postgres não volta e o monitor morre calado |
| S2 | Baixo | Segurança | Dependabot agrupado (`'*'`) sem `cooldown`: versão maliciosa recém-publicada entra no bundle do painel |
| S3 | Baixo | Segurança | Chave privada do `age` gerada no diretório de trabalho e não ignorada pelo `.gitignore` |
| C2 | Baixo | Correção | Marca d'água global dos rollups no futuro (salto de relógio) congela a consolidação |
| C3 | Baixo | Correção | Comentário da 00001 diz que "a produção roda o 17" (é o 18) |

---

## Segurança

### S1 — Alto — compose.yaml na VPS: usuário de demo com senha pública + portão das chaves pelo URL declarado

**Onde:** `compose.yaml:119-130` (bloco `x-ambiente`), `compose.yaml:156-176` (serviço `seed`,
`DEMO_PASSWORD: demo-vigilume-2026`), `deploy/compose/seed.sh:34`, `internal/config/config.go:279`
(portão das chaves), `compose.yaml:138` (`POSTGRES_PASSWORD: postgres`),
`deploy/compose/initdb.sql:14` (senha `vigilume_app`).

**Cenário concreto.** O dono disse que vai usar "Docker compose com Caddy + app + Postgres". Não
existe compose de produção nem runbook de VPS (o `compose.yaml` diz "Produção: docs/deploy/fly.md",
e o PENDENCIAS-PARA-O-PLANO-10 registra a VM como "alvo de deploy novo, com runbook próprio", ainda
não feito). O caminho natural é copiar o `compose.yaml` e acrescentar um serviço `caddy` com
`reverse_proxy app:8080`.

1. **O seed roda e cria o dono.** Com banco novo, o `seed` sobe antes do app e executa
   `vigilume user create --email demo@vigilume.local` com a senha `demo-vigilume-2026`, que está no
   repositório (e no ci.yml). Como o sistema é de usuário único, esse passa a ser **o** usuário. O
   `vigilume user create` do dono depois sai com código 3 ("já existe usuário"). Nada no seed nem no
   CLI olha `PUBLIC_BASE_URL` ou as chaves: trocar as chaves e pôr `https://` **não** impede isso.
   Qualquer pessoa que conheça o projeto entra em `https://<domínio>/` com `demo@vigilume.local` /
   `demo-vigilume-2026` (sem 2FA) e passa a controlar alvos, canais (URLs de webhook e e-mails
   decifrados pela API), status page e sessões. Se o repositório for aberto (está planejado), a senha
   fica pública de verdade.
2. **O portão das chaves de demo é contornável sem querer.** `config.go:279` só recusa as chaves de
   demo se `PUBLIC_BASE_URL` for https ou um host não local. Atrás do Caddy o app não confere `Host`
   nem `Origin` contra `PUBLIC_BASE_URL` (grep em `internal/httpapi`: só o CORS o usa). O painel
   funciona inteiro em `https://dominio` com `PUBLIC_BASE_URL=http://localhost:8080`. O único sintoma
   são links errados em e-mails e na status page. Quem só acrescenta o Caddy fica com
   `JWT_SECRET`/`MASTER_KEY` públicas e o boot aceita. (O JWT forjado ainda precisa do UUID do usuário,
   que não vaza por rota pública. Por isso o impacto imediato é o do item 1, e a `MASTER_KEY` pública
   decifra headers e canais de qualquer dump ou backup.)
3. **Senhas do Postgres públicas.** Superusuário `postgres/postgres` e `vigilume_app/vigilume_app`. O
   `pg_hba` da imagem oficial aceita qualquer host da rede do compose. Um contêiner comprometido (o
   Caddy, por exemplo) vira superusuário do banco (`COPY … TO PROGRAM` = RCE no contêiner do db, com
   leitura total sem RLS). Se o dono publicar a 5432 para fazer backup, o Docker ignora o UFW e o
   superusuário com senha `postgres` fica na internet.

**Por que as defesas não bastam:** a trava D14 foi desenhada para "compose copiado para servidor".
Ela deduz "local" de um valor que o operador declara, e não da exposição real. O seed não tem trava
nenhuma. O `CheckRole` recusa superusuário **para o app**, mas não impede que o superusuário exista com
senha conhecida.

**Refutação tentada:** com `PUBLIC_BASE_URL` https e chaves novas, o item 2 some. O item 1 continua
(testado pela leitura: `seed.sh` não tem condição além do código 3). O item 3 exige outro contêiner
comprometido ou uma porta publicada: fica como agravante, não como achado isolado.

**Correção sugerida:**
- Criar `compose.prod.yaml` (ou `deploy/vps/compose.yaml`) **sem** o serviço `seed` e **sem** o bloco
  `x-ambiente`, com `env_file` fora do git, `POSTGRES_PASSWORD` e senha do `vigilume_app` geradas
  (`openssl rand -hex 32`), `restart: unless-stopped` em tudo, db sem `ports:`, e o Caddy.
- Endurecer o seed: recusar (sair ≠ 0) quando `PUBLIC_BASE_URL` não for local, ou quando
  `DEMO_PASSWORD` for a de demonstração fora de localhost. Melhor ainda: `user create` recusar a senha
  `demo-vigilume-2026` salvo com um flag explícito.
- Opcional, e mais robusto que o URL declarado: no middleware, recusar requisição cujo `Host` não bata
  com o host de `PUBLIC_BASE_URL` (com exceção para o healthcheck em loopback). Assim, um
  `PUBLIC_BASE_URL` local atrás de proxy público quebra na hora, em vez de funcionar com as chaves
  de demo.
- Runbook de VPS com: TRUSTED_PROXIES = sub-rede da rede do compose (fixá-la com `ipam`), backup
  (o `backup.yml` atual só fala com o Fly via `flyctl proxy`; na VPS não há backup nenhum até isso ser
  adaptado; **não** publicar a 5432 para o runner do GitHub).

### S2 — Baixo — Dependabot agrupado sem `cooldown` alimenta o bundle do painel

**Onde:** `.github/dependabot.yml` (grupos `go: ['*']` e `painel: ['*']`, sem `cooldown`),
`Dockerfile:17` e `ci.yml:168` (`npm ci` executa scripts de instalação).

**Cenário:** um pacote do grafo npm (365 pacotes, 131 de produção) tem uma versão maliciosa
publicada, como nos ataques a pacotes populares de 2025. No domingo seguinte o Dependabot abre um PR
agrupado com "tudo atualizado", o CI fica verde e o dono mescla. O código entra no bundle servido
pelo binário e roda no navegador do dono **com o access token em memória**: cria canal de webhook
para o atacante, lê as configurações de canal decifradas, mexe em alvos. O `npm ci` do `web`/`image`
também executa `postinstall` no runner (hoje só `msw`/`fsevents` têm script, mas uma versão nova pode
acrescentar).

**Por que as defesas não bastam:** o lockfile com integridade e o `npm audit` (0 vulnerabilidades
hoje) protegem contra adulteração de uma versão já fixada, não contra uma versão nova maliciosa. O
agrupamento `'*'` torna a revisão do diff de dependências impraticável.

**Refutação:** exige o dono mesclar dentro da janela (normalmente horas a poucos dias) antes de a
versão ser removida. Por isso é Baixo, não Médio.

**Correção:** `cooldown: { default-days: 7 }` (e `semver-major-days` maior) nos ecossistemas npm e
gomod. `npm ci --ignore-scripts` no Dockerfile e no CI (o build do Vite não precisa de scripts de
instalação; o `msw` só precisa do script em dev).

### S3 — Baixo — chave privada do age sem proteção no `.gitignore`

**Onde:** `docs/deploy/fly.md:247` (`age-keygen -o vigilume-backup.key`), `.gitignore` (não cobre
`*.key`, `*.age`, `*.dump`).

**Cenário:** o dono segue o runbook na raiz do repositório (onde executa os outros comandos), a
chave privada fica como arquivo não rastreado e um `git add -A` a leva junto no próximo commit. Com o
push, a chave que decifra **todos** os backups (que guardam `password_hash`, `totp_secret_enc`
cifrado, `token_hash` de sessões e, com a MASTER_KEY, tudo) está no histórico do GitHub. O
repositório vai ser aberto (planejado).

**Defesas existentes:** o gitleaks do CI (regra padrão `age-secret-key`) pega a chave, mas **depois**
do push, com ela já no histórico remoto. O `.dockerignore` e o `COPY` seletivo do Dockerfile não a
levam à imagem.

**Correção:** acrescentar `*.key`, `*.age`, `*.dump`, `.env.*` ao `.gitignore`, e no runbook
`age-keygen -o ~/vigilume-backup.key` (fora do repositório). Hoje o `.gitignore` só tem `.env`, e um
`.env.production` na raiz seria versionável.

---

## Correção

### C1 — Médio — `db` sem política de restart no compose

**Onde:** `compose.yaml:133-154` (serviço `db` sem `restart:`); só o `app` tem
`restart: unless-stopped` (`compose.yaml:198`).

**Cenário:** na VPS, reboot do host, atualização do Docker ou `systemctl restart docker`. O daemon
religa só os contêineres com política de restart: o `app` volta, o `db` não. O `depends_on` não é
reavaliado num restart do daemon. O app falha no `NewPool`/`Ping`, sai e é religado em loop. O
monitor de uptime fica **parado e calado**: é o sistema de alerta que caiu, e ninguém é avisado.

**Refutação:** no uso previsto do compose (avaliação local com `up --wait`) não importa. Importa
exatamente no uso que o dono vai fazer (VPS).

**Correção:** `restart: unless-stopped` no `db` (e no Caddy). Vale também um healthcheck externo
(outro serviço de uptime vigiando o `/healthz` público), porque o Vigilume não consegue avisar da
própria queda.

### C2 — Baixo — marca d'água dos rollups no futuro congela a consolidação

**Onde:** `internal/storage/maintenance.go:84-96` e `queries/maintenance.sql:27`
(`max(bucket)` global).

**Cenário:** a VPS sobe com o relógio adiantado (RTC errado antes do NTP sincronizar, ou relógio
ajustado à mão) e o sondador grava checks e a manutenção grava rollups com `bucket` no futuro. Quando o
relógio volta ao certo, `de = ultima − 2h` fica depois de `ate` e `ConsolidateRollups` retorna sem
fazer nada (`!de.Before(ate)`) até o tempo real alcançar a marca falsa. Durante esse intervalo a barra
de uptime da status page e o spark do dashboard não recebem horas novas. O `LatestUserRollupBucket`
no futuro também esvazia a janela de horas brutas do resumo.

**Refutação:** exige salto de relógio para frente de mais de 2 h. É raro com `systemd-timesyncd`, por
isso Baixo.

**Correção:** limitar a marca a `ate` (`if ultima.After(ate) { ultima = ate }`) e, opcionalmente,
recusar o INSERT de check com `checked_at` muito à frente de `now()` do banco.

### C3 — Baixo — comentário desatualizado

`internal/storage/migrations/00001_init.sql:4-6` diz "a produção roda o 17". A produção roda o 18
(compose, `deploy/fly/db/Dockerfile`, ci.yml). O comportamento não muda (a perna do 17 no CI mantém
a compatibilidade), mas o comentário engana quem for decidir usar `uuidv7()` nativo.

---

## Verificado e sem achado (hipóteses tentadas e refutadas)

**RLS / escopos**
- Toda tabela com dado de alvo, canal, incidente, rollup ou status page tem `ENABLE` + `FORCE` e
  `USING`/`WITH CHECK` coerentes. `users`, `sessions`, `audit_log` e `totp_recovery_codes` estão sem
  RLS **por decisão registrada** (D7 do Plano 2, D13 do Plano 5), e todas as queries delas que partem
  de um usuário filtram por `user_id` (conferido em `queries/users.sql`, `queries/sessions.sql`,
  `internal/auth/store.go`, `internal/auth/totp.go`). Com usuário único, não há cenário de
  vazamento.
- Escopo público: `status_pages_public_select` exige `published` e o slug do GUC. Os itens
  delegam à página, e os alvos a `public_page_target_ids()` (INVOKER, com a RLS dos itens valendo
  dentro dela). Checks, rollups, incidentes e `target_health` delegam a targets. A transação é
  `READ ONLY`, o que fecha as políticas FOR ALL. As queries públicas projetam só nome, estado,
  contagens, p50 e início/fim (sem URL, causa ou status code). O `slugRE` barra o slug antes do banco.
- Escopo de verificação: vê só a linha do hash. `notifications` e `channel_health` exigem dono ou
  sondador, então não abrem nada pelo verify (o limite "UPDATE arbitrário mudaria user_id" está
  documentado na 00005).
- Vazamento de escopo entre conexões: todos os `set_config` são `is_local = true` dentro de
  transação (`storage/scope.go`), as políticas usam `NULLIF(…,'')` e não há `set_config(…, false)`
  no código Go. Não existe SQL dinâmico fora do `testdb`. Uma query no pool sem escopo falha
  fechada (vê zero linhas).
- `CheckRole` recusa `rolsuper`/`rolbypassrls`. Esses atributos não se herdam por membership, então
  pertencer a outro papel não os contorna. O papel do app é dono das tabelas: um SQLi desligaria a
  RLS (`set_config('app.scope','prober')` ou `ALTER TABLE … NO FORCE`). Isso é inerente ao desenho
  "RLS como segunda barreira contra bug de lógica" e não há SQLi (tudo via sqlc/parametrizado).
- Funções: nenhuma `SECURITY DEFINER`. `ensure_checks_partition`/`drop_checks_partitions_before`
  são INVOKER, chamadas só pelo app. O schema `public` é do dono do banco (PG15+), e nenhum outro papel
  cria objetos nele, então não há sombreamento por `search_path`.
- Partições de checks sem RLS: já documentado (PENDENCIAS-5). Não é pior do que o documento diz:
  nenhum outro papel além do app e do backup (que é BYPASSRLS de qualquer forma) as lê.
- `vigilume_backup` (runbook passo 4): `NOSUPERUSER BYPASSRLS` + `pg_read_all_data`, só leitura,
  senha só em secret do GitHub. OK.
- Restauração do backup como superusuário (`backup.sh:48`): um app comprometido poderia plantar uma
  função num CHECK, e ela rodaria como superusuário durante o COPY no Postgres **descartável** do
  runner. O impacto fica num contêiner efêmero sem segredos nem acesso ao túnel (a 15432 escuta no
  loopback do host, fora da bridge), e o dump já é do atacante. Descartado como achado.
- Testes focados rodados: `go test ./internal/storage/ -run 'RLS|Scope|Public|Verify'` → ok.

**CI / supply chain**
- Todas as actions estão fixadas por SHA completo. `permissions: contents: read` no topo, e
  `packages: write` só no `publish`, que só roda em `push` de `BipoXDXD/vigilume`. Não há
  `pull_request_target` nem `${{ }}` de dado controlado por terceiro em `run:` (o input `sha` do
  deploy vai por env e é validado com `^[0-9a-f]{40}$`, e o ref é conferido contra `origin/main`).
  `persist-credentials: false` em todo checkout.
- Nenhum job de PR usa secrets. PRs de Dependabot recebem token só de leitura.
- Envenenamento de cache: o `image` grava `type=gha,scope=imagem` também em PR, mas o cache do
  Actions é isolado por ref, e o `publish` (main/tag) não lê cache gravado por ref de PR. Setup-go e
  setup-node seguem a mesma regra.
- Ferramentas por `go install …@vX` são verificadas pelo sum.golang.org. O Trivy e o Postgres vão
  por digest.
- Dockerfile: bases por digest, distroless `nonroot`, sem shell (checado por `checar-imagem.sh`),
  `COPY` seletivo (cmd/, internal/, web/), e o `.dockerignore` tira `.env*`, `.git`, `deploy`,
  `docs`. A tag `v*` vai para `-X main.version=${VERSION}` num `RUN` de shell, mas só quem já tem
  escrita no repositório empurra tag. Descartado.
- `govulncheck ./...`: 0 vulnerabilidades alcançáveis. Só em nível de módulo:
  GO-2026-5932 (`x/crypto/openpgp`, sem correção), que o código não importa.
- `npm audit`: 0 vulnerabilidades (365 pacotes).

**Segredos**
- Histórico inteiro (245 commits, todos os refs): `git log --all -p -S BEGIN` só acha `BEGIN` de SQL.
  A busca por `AGE-SECRET-KEY`, `re_…` (Resend), `ghp_`, `FlyV1`, webhooks do Discord e atribuições de
  `MASTER_KEY/JWT_SECRET/IP_HASH_KEY/PASSWORD` não achou nada além das chaves e da senha de demo
  documentadas. Nenhum `.env`, `.pem`, `.key` ou dump foi adicionado em commit algum. O gitleaks não
  está instalado localmente. O CI roda o gitleaks sobre o histórico completo, com allowlist exata só
  para as três chaves de demo.
- `config.go`: as chaves exigem exatamente 32 bytes em base64, as três de demo são recusadas fora de
  localhost (com a ressalva de S1), `/0` em `TRUSTED_PROXIES` é recusado, `METRICS_ADDR` só em
  loopback e o `LogValue` redige os segredos.

## Observações para a VPS (fora do código, sem severidade)
- `deploy.yml` e `backup.yml` são só para o Fly. Na VPS não há deploy nem **backup** automatizados
  até serem adaptados.
- Atrás do Caddy, `TRUSTED_PROXIES` precisa ser a sub-rede da rede do compose (fixe-a com `ipam`).
  Vazio, todo cliente divide o balde de rate limit do login, e um atacante esgota o balde e tranca o
  dono para fora do login por IP.
- O Caddy sobrescreve o `X-Forwarded-For` de clientes não confiáveis por padrão. Mantenha assim (não
  configure `trusted_proxies` no Caddy sem CDN na frente).
