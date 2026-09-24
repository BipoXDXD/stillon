# Deploy e operação no Fly.io

Runbook **manual**. Nada aqui roda sozinho: cada passo mexe na conta do Fly ou do GitHub do dono
do projeto e é executado por ele. O porquê de cada escolha está na spec do Plano 7
(`docs/superpowers/specs/2026-09-24-plano-7-empacotamento-e-deploy-design.md`); as decisões são
citadas como D*n*.

## Topologia

| App | O que roda | Máquina | Rede |
|---|---|---|---|
| `vigilume` | O binário com `--role=all`: API, painel, sondador, dispatcher e manutenção | `shared-cpu-1x`, 512 MB | HTTP público pelo proxy do Fly |
| `vigilume-db` | Imagem oficial `postgres:18-alpine` (digest em `deploy/fly/db/Dockerfile`), volume `pgdata` | `shared-cpu-1x`, 512 MB, volume de 3 GB | Só a rede privada: `vigilume-db.internal:5432` |

O Postgres **não tem gerência do Fly**. Backup, disco e upgrade são do operador (seção
"Operação do Postgres"). Custo aproximado: US$ 9–11/mês (duas máquinas de 512 MB, 3 GB de volume,
snapshots dentro da faixa grátis de 10 GB). Conferir a página de preços antes de criar.

## O que você precisa antes

- `flyctl` instalado e autenticado (`fly auth login`).
- A imagem publicada no GHCR pelo job `publish` do CI (pacote **privado**, como o repositório).
  Anote o digest (`sha256:…`) do resumo do job.
- `docker` com `buildx`, logado no GHCR com um token `read:packages`
  (`docker login ghcr.io -u <usuário>`).
- `age` (<https://age-encryption.org>) para a chave do backup.
- Um gerenciador de senhas. Quatro coisas vão para ele e **não** podem ficar só no Fly: a senha
  do superusuário do Postgres, as senhas de `vigilume_app` e `vigilume_backup`, a `MASTER_KEY` e
  a chave privada do `age`.

## Primeiro deploy

Troque `gru` pela região escolhida em `fly platform regions`, nos dois `fly.toml` também.

1. **Apps.** Sem `fly launch` (ele gera `auto_stop_machines`, e um monitor desligado por falta
   de tráfego para de sondar em silêncio, D23):
   ```sh
   fly apps create vigilume
   fly apps create vigilume-db
   ```
2. **Volume do banco**, com snapshots diários guardados por 14 dias:
   ```sh
   fly volumes create pgdata -a vigilume-db --region gru --size 3 --snapshot-retention 14
   ```
3. **Banco no ar:**
   ```sh
   fly secrets set -a vigilume-db --stage POSTGRES_PASSWORD="$(openssl rand -base64 32)"
   fly deploy -c deploy/fly/db/fly.toml --ha=false --image "$(sed -n 's/^FROM //p' deploy/fly/db/Dockerfile)"
   fly machines list -a vigilume-db   # exatamente UMA máquina, com o volume pgdata
   fly logs -a vigilume-db    # esperado: "database system is ready to accept connections"
   ```
   A imagem vai por `--image`, direto do Docker Hub e pelo digest de `deploy/fly/db/Dockerfile`
   (que o Dependabot atualiza), sem build remoto. `--ha=false`: sem ele, o primeiro deploy cria
   duas máquinas, e uma segunda máquina de Postgres não teria volume. (Não ensaiado: a
   validação não tem conta no Fly. As flags foram conferidas no `flyctl` 0.4.108.)
   Guarde a `POSTGRES_PASSWORD` no gerenciador (`fly secrets` não mostra o valor depois).
4. **Papéis e banco.** A imagem oficial tem shell:
   ```sh
   fly ssh console --pty -a vigilume-db -C "psql -U postgres"
   ```
   Rode o mesmo SQL de `deploy/compose/initdb.sql`, com **senhas novas**, e o papel de backup:
   ```sql
   CREATE ROLE vigilume_app LOGIN PASSWORD '<senha nova>'
       NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;
   CREATE DATABASE vigilume OWNER vigilume_app;
   CREATE ROLE vigilume_backup LOGIN PASSWORD '<outra senha nova>'
       NOSUPERUSER BYPASSRLS NOCREATEDB NOCREATEROLE;
   GRANT pg_read_all_data TO vigilume_backup;
   ```
   O `vigilume_backup` tem `BYPASSRLS` porque o `pg_dump` do dono falha com
   `FORCE ROW LEVEL SECURITY`. É exatamente o tipo de papel que o boot **recusa**: o
   `DATABASE_URL` do app nunca aponta para ele.
5. **Pré-checagem** (o que o boot vai conferir), no mesmo `psql`:
   ```sql
   SELECT rolname, rolsuper, rolbypassrls FROM pg_roles
    WHERE rolname IN ('vigilume_app', 'vigilume_backup');
   -- vigilume_app    | f | f
   -- vigilume_backup | f | t
   \c vigilume
   SELECT has_schema_privilege('vigilume_app', 'public', 'CREATE');   -- t
   ```
6. **Segredos do app**, sem reiniciar nada:
   ```sh
   fly secrets set -a vigilume --stage \
     DATABASE_URL='postgres://vigilume_app:<senha>@vigilume-db.internal:5432/vigilume?sslmode=disable' \
     MASTER_KEY="$(openssl rand -base64 32)" \
     JWT_SECRET="$(openssl rand -base64 32)" \
     IP_HASH_KEY="$(openssl rand -base64 32)"
   ```
   Gere a `MASTER_KEY` antes, guarde-a no gerenciador e só então a passe ao Fly. **Perder a
   `MASTER_KEY` torna ilegíveis os headers de alvo e as configurações de canal, e nenhum backup
   devolve isso**: o dump guarda o texto cifrado, não a chave.

   `sslmode=disable` é deliberado (D31): a rede privada do Fly é uma malha WireGuard, e o banco
   não tem porta pública.

   E-mail é opcional: `RESEND_API_KEY` e `EMAIL_FROM` **juntas**, com o domínio do remetente já
   verificado no Resend (senão todo envio volta 403). Sem elas, o Discord funciona e o cadastro
   de canal de e-mail responde que ele está indisponível.
7. **Primeiro deploy do app.** O Fly puxa de `registry.fly.io` (D34): o pacote do GHCR é privado,
   e dar ao Fly uma credencial do GitHub seria guardar um token pessoal fora do GitHub. Copie a
   imagem **testada**, pelo digest, sem rebuild:
   ```sh
   fly auth docker
   docker buildx imagetools create \
     -t registry.fly.io/vigilume:<sha do commit> \
     ghcr.io/bipoxdxd/vigilume@sha256:<digest do publish>
   fly deploy --image registry.fly.io/vigilume@sha256:<o mesmo digest> --ha=false
   fly machines list -a vigilume   # exatamente UMA máquina
   fly logs -a vigilume
   ```
   `--ha=false` importa: sem ele, o primeiro deploy cria **duas** máquinas, e dois processos
   `--role=all` mandariam cada notificação em dobro. O workflow `deploy` confere a mesma coisa.
   Esperado: `iniciando` com a versão, `migrations aplicadas`, `sondador ativo`, `servindo`. O
   aviso "TRUSTED_PROXIES vazio com PUBLIC_BASE_URL https" é esperado nesta etapa.
8. **Conferência do dono das tabelas**, no console do banco:
   ```sql
   \c vigilume
   SELECT tableowner, count(*) FROM pg_tables WHERE schemaname = 'public' GROUP BY 1;
   -- só vigilume_app
   ```
9. **IP do cliente.** O proxy do Fly põe o IP do **app** no fim do `X-Forwarded-For`; o do
   cliente vem em `Fly-Client-IP`, que o app lê (`CLIENT_IP_HEADER`) **só** de conexões vindas de
   `TRUSTED_PROXIES` (D22). A documentação do Fly não diz de qual endereço o proxy conecta, e
   por isso `TRUSTED_PROXIES` começa vazio. Abra o app no navegador e procure no log:
   ```sh
   fly logs -a vigilume | grep "cabeçalho de IP do cliente ignorado"
   ```
   O aviso (no máximo um por minuto) traz o `remote_addr` do proxy. Ponha o menor CIDR que o
   cobre em `TRUSTED_PROXIES` no `fly.toml` (candidatos esperados: `172.16.0.0/12` ou
   `fdaa::/16`) e faça o deploy de novo pelo mesmo digest (o comando do passo 7). **Leve o valor,
   e a região, para o `fly.toml` do repositório por PR** antes de usar o workflow `deploy`: ele faz
   o deploy com o `fly.toml` da `main`, e **recusa** um `TRUSTED_PROXIES` vazio. Então o **teste
   de dois pontos de rede**, com um e-mail qualquer e senha errada:
   ```sh
   for i in 1 2 3 4 5 6; do
     curl -s -o /dev/null -w '%{http_code}\n' -X POST https://vigilume.fly.dev/api/v1/auth/login \
       -H 'Content-Type: application/json' -H 'X-Requested-With: vigilume' \
       -d '{"email":"ninguem@exemplo.com","password":"errada-errada-1234"}'
   done
   ```
   - Da rede A (casa): cinco `401` e um `429`.
   - Logo depois, da rede B (4G do celular): `401`, não `429`. Os baldes estão separados.
   - De novo da rede A, com `-H 'Fly-Client-IP: 203.0.113.77' -H 'X-Forwarded-For: 203.0.113.77'`:
     continua `429`. O cabeçalho forjado não vale.
   - O aviso some do log.

   Se a rede B levar `429`, o `TRUSTED_PROXIES` não cobre o proxy: volte ao aviso.
10. **Seu usuário**, com o binário **da versão que está no ar** (o `user create` aplica as
    migrations antes: uma versão mais nova migraria o banco de produção por fora do deploy).
    `fly releases -a vigilume --image` mostra o digest em produção; `vigilume version` numa imagem
    diz o commit. Num terminal, o túnel; no outro, o subcomando (a senha é lida do terminal, sem
    eco):
    ```sh
    fly proxy 15432:5432 -a vigilume-db
    ```
    - Pela imagem em produção, **com** `-t` (sem ele o terminal mostraria a senha):
      ```sh
      fly auth docker
      docker run --rm -it --network host \
        -e DATABASE_URL='postgres://vigilume_app:<senha>@127.0.0.1:15432/vigilume?sslmode=disable' \
        registry.fly.io/vigilume@sha256:<digest em produção> user create --email <seu e-mail>
      ```
      `--network host` é do Linux. No Docker Desktop (macOS, Windows) o contêiner não vê o
      `127.0.0.1` da máquina: troque o host do `DATABASE_URL` por `host.docker.internal` e tire o
      `--network host`.
    - Ou compilando o mesmo commit: `git checkout <sha em produção> && make build`, e
      `DATABASE_URL='…@127.0.0.1:15432/…' ./bin/vigilume user create --email <seu e-mail>`
      (o painel não precisa estar construído: o `user create` não o usa).
11. **Conferência pública.** `https://vigilume.fly.dev/healthz` e `/readyz` dão 200; login no
    painel; `curl -sI https://vigilume.fly.dev/` mostra `Strict-Transport-Security`, a CSP com
    nonce e `X-Content-Type-Options`; crie a status page e abra `/p/<slug>` numa aba anônima.

    O painel exige **contexto seguro**: HTTPS, ou `localhost`. Em HTTP por outro endereço ele
    mostra um aviso e desliga a coordenação entre abas (Web Locks), e as abas passam a disputar
    o refresh. No Fly isso já está resolvido (`force_https = true`); não sirva o painel por um
    domínio próprio sem TLS nem pelo IP da máquina.
12. **Primeira manutenção.** Num banco novo não há o que consolidar. A rodada pesada (62 dias de
    checks de uma vez) só acontece num banco importado.
13. **CI de deploy e de backup** (GitHub → Settings → Secrets and variables → Actions). No
    GitHub Free com repositório privado **não há environments** (nem secrets por environment,
    nem revisor obrigatório): tudo é secret **de repositório**. A trava real é quem pode
    disparar um workflow à mão: só quem tem acesso de escrita, hoje só você. Secrets de
    repositório não chegam a PR de fork.
    - Secrets: `FLY_DEPLOY_TOKEN` = `fly tokens create deploy -a vigilume`;
      `FLY_BACKUP_TOKEN` = `fly tokens create deploy -a vigilume-db`; `BACKUP_DATABASE_PASSWORD`
      = senha do `vigilume_backup`; `AGE_RECIPIENT` = a chave **pública** de
      `age-keygen -o vigilume-backup.key` (a privada vai para o gerenciador e para mais nenhum
      lugar). Um token por app: um token vazado só alcança aquele app.
    - Variáveis: `PG_VOLUME_GB` = `3`; `BACKUP_ENABLED` = `1` (sem ela, o `backup` agendado
      termina em "skipped" todo dia, sem gastar minutos).

    Rode o workflow `backup` à mão (Actions → backup → Run workflow) e confira que ficou verde.
    Se o `flyctl proxy` falhar por permissão, o token de app não basta para o túnel: troque o
    `FLY_BACKUP_TOKEN` por um token de organização, sabendo que ele alcança todos os seus apps.
14. **Voltar uma versão.** `fly releases -a vigilume` e
    `fly deploy --image registry.fly.io/vigilume@sha256:<digest anterior> --ha=false`. Só é seguro se as
    migrations da versão nova forem aditivas: o `goose` não roda `Down` sozinho.

## Deploys seguintes

Actions → ci → o `publish` da `main` gera o digest; Actions → deploy → Run workflow **na `main`**
com esse digest (o job recusa outro branch e um `TRUSTED_PROXIES` vazio). O job copia a imagem do
GHCR para o `registry.fly.io` pelo digest e faz o deploy com `--strategy rolling` e `--ha=false`
(alguns segundos fora com uma máquina só, D26), e confere que ficou uma máquina.

A imagem publicada é construída de novo no `publish`, do **mesmo código e com as mesmas bases
fixadas por digest** da imagem que o E2E testou, e é **reescaneada** pelo Trivy antes de ganhar
tag: não é, byte a byte, a do E2E (o binário é reprodutível; a imagem não, D9 da spec).

### Minutos de Actions

Com o repositório privado, o CI gasta a cota gratuita (2.000 min/mês na conta pessoal): ~15 min de
runner por push somando os jobs, dos quais o `e2e` é o maior, e ~2 min por dia do `backup`
(~60 min/mês). Um push novo no mesmo branch cancela a execução anterior (`concurrency` do `ci.yml`),
então só a última conta inteira. Se a cota apertar, o primeiro corte é o `e2e` só na `main`
(`if: github.event_name == 'push'` no job), trocando a garantia do PR por um aviso depois do merge
(D19 da spec).

## Depuração

A imagem do app não tem shell (spec §5.9): `fly ssh console -a vigilume` não serve. O que serve:
`fly logs`, os subcomandos do binário (`vigilume version`, `vigilume healthcheck`,
`vigilume user …`) e o console do **banco**, que tem shell.

## Operação do Postgres

| Responsabilidade | Mecanismo | Frequência |
|---|---|---|
| Backup | Snapshots diários do volume (14 dias) + workflow `backup` diário: dump, restauração conferida, cifra `age`, artifact de 30 dias | Automático: conferir que o `backup` segue verde |
| Teste de restauração | Automático em cada `backup`; manual a cada trimestre (abaixo) | Diário e trimestral |
| Disco | O `backup` compara o tamanho dos bancos com `PG_VOLUME_GB`: aviso acima de 70%, **falha** acima de 85% (o GitHub manda e-mail). Depois de `fly volumes extend <id> -s <GB>`, **atualize a variável `PG_VOLUME_GB`**, senão o aviso continua contando o tamanho velho | Quando avisar |
| Upgrade menor (18.x) | PR do Dependabot em `deploy/fly/db/Dockerfile` (e o do `compose.yaml`, juntos) → merge → `fly deploy -c deploy/fly/db/fly.toml --ha=false --image "$(sed -n 's/^FROM //p' deploy/fly/db/Dockerfile)"` (10–30 s fora). O `backup` passa a usar a imagem nova sozinho | Quando o Dependabot abrir |
| Upgrade maior (18 → 19) | **Não** vem por PR: o `dependabot.yml` ignora versão maior de `postgres` (e de `node`). Dump e restauração num app novo (abaixo, D36) | ~1 vez por ano |

### Restauração manual (trimestral)

1. Baixe o artifact do último `backup` (Actions → backup → execução → Artifacts).
2. `age -d -i vigilume-backup.key -o vigilume.dump vigilume-AAAAMMDD.dump.age`
3. Num Postgres descartável, o mesmo caminho do workflow:
   ```sh
   docker run -d --name restauro -p 127.0.0.1:25433:5432 -e POSTGRES_PASSWORD=restauro \
     "$(sed -n 's/^FROM //p' deploy/fly/db/Dockerfile)"
   until docker exec restauro pg_isready -h 127.0.0.1 -U postgres; do sleep 1; done
   docker exec -i restauro psql -U postgres < deploy/compose/initdb.sql
   docker exec -i restauro pg_restore -U postgres --exit-on-error -d vigilume < vigilume.dump
   ```
4. Suba o `vigilume` contra ele **com `--role=api`** (`DATABASE_URL=postgres://vigilume_app:vigilume_app@127.0.0.1:25433/vigilume?sslmode=disable`,
   **a mesma `MASTER_KEY` de produção**, `PUBLIC_BASE_URL=http://localhost:8080`) e faça login.
   Só a API: com `--role=all`, a cópia sondaria os alvos de produção e mandaria alertas aos
   canais **reais** (Discord, e-mail), como se fosse a produção. Abra um alvo com header: se a
   `MASTER_KEY` estiver errada, é aqui que aparece.
5. `docker rm -f restauro` e apague o `vigilume.dump`.

### Queda do host do banco

Máquina única, sem réplica. A máquina velha está presa ao volume velho: destrua-a primeiro, ou o
deploy tentaria reusá-la.
```sh
fly machines list -a vigilume-db
fly machine destroy <id da máquina> -a vigilume-db --force
fly volumes list -a vigilume-db
fly volumes snapshots list <id do volume>
fly volumes create pgdata -a vigilume-db --snapshot-id <snapshot> -s 3 --region gru
fly deploy -c deploy/fly/db/fly.toml --ha=false --image "$(sed -n 's/^FROM //p' deploy/fly/db/Dockerfile)"
```
O Postgres faz a recuperação de crash sozinho. Se o snapshot não servir, restaure o último dump
(seção anterior) num banco novo, com os papéis do passo 4.

### Upgrade maior

1. `fly apps create vigilume-db19`, volume novo, `deploy/fly/db/Dockerfile` com a imagem nova.
2. Papéis do passo 4 no banco novo.
3. Janela curta: pare o app (`fly scale count 0 -a vigilume`), faça o dump do antigo como
   `vigilume_backup` (`fly proxy` + `pg_dump -Fc`) e restaure no novo como superusuário,
   preservando os donos.
4. `fly secrets set -a vigilume DATABASE_URL=…vigilume-db19.internal…`, `fly scale count 1`.
5. Mantenha o `vigilume-db` antigo parado por 7 dias antes de destruí-lo.

## Tornar o repositório público (passo futuro)

O repositório é **privado por decisão do dono**. No dia de abrir:

1. Varredura do histórico inteiro: `gitleaks git . --redact -v` (o job `secrets` do CI já faz
   isso em todo PR). O falso positivo conhecido está no `.gitleaksignore`, com o motivo.
2. **Se aparecer um segredo real:** considere-o vazado e **rotacione primeiro** (Resend, Discord,
   Fly, o que for). Depois, tire-o do histórico com `git filter-repo --replace-text` num clone
   novo, `git push --force` de todos os branches e tags, e rode a varredura de novo até zerar.
   Reescrever a história não desfaz um vazamento.
3. O e-mail pessoal do autor está no histórico e **fica** (decisão do dono).
4. Settings → General → Change visibility. O pacote do GHCR tem visibilidade própria: Package
   settings → Change visibility → Public. Confira de uma máquina sem login:
   `docker pull ghcr.io/bipoxdxd/vigilume:latest`.
5. No README, troque o parágrafo "enquanto o repositório for privado" pelo dos 30 segundos: o
   `docker compose up` passa a puxar a imagem em vez de construir, sem mudar o compose.
6. **Deploy com revisor:** num repositório público, o GitHub Free tem environments com revisor
   obrigatório. Crie o `production`, mova o `FLY_DEPLOY_TOKEN` para ele e ponha
   `environment: production` no job do `deploy.yml`. (O GitHub Pro, ~US$ 4/mês, dá secrets por
   environment num repositório privado, mas **não** o revisor obrigatório; nada aqui depende dele.)
7. **Agendamentos:** num repositório público, o GitHub **desliga** os workflows agendados depois
   de 60 dias sem atividade no repositório, e avisa por e-mail. O `backup` para junto: reative-o
   em Actions quando o aviso chegar.
