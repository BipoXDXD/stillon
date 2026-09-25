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
  Anote o SHA do commit (40 hex) do resumo do job: a tag `:<sha>` só existe depois que o Trivy
  aprovou o digest.
- `docker` com `buildx`, logado no GHCR com um token `read:packages`
  (`docker login ghcr.io -u <usuário>`).
- `jq`, para tirar o digest e a revisão da saída do `docker buildx imagetools inspect`.
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
3. **Banco no ar.** Primeiro a senha do superusuário, **à vista**:
   ```sh
   PG_SENHA=$(openssl rand -hex 32); echo "$PG_SENHA"
   ```
   Guarde-a no gerenciador de senhas **agora**, antes de seguir: `fly secrets` não devolve o
   valor depois, e ela é o único acesso de superusuário ao banco. Só então:
   ```sh
   fly secrets set -a vigilume-db --stage POSTGRES_PASSWORD="$PG_SENHA"
   fly deploy -c deploy/fly/db/fly.toml --ha=false --image "$(sed -n 's/^FROM //p' deploy/fly/db/Dockerfile)"
   fly machines list -a vigilume-db   # exatamente UMA máquina, com o volume pgdata
   fly logs -a vigilume-db    # esperado: "database system is ready to accept connections"
   ```
   A imagem vai por `--image`, direto do Docker Hub e pelo digest de `deploy/fly/db/Dockerfile`
   (que o Dependabot atualiza), sem build remoto. `--ha=false`: sem ele, o primeiro deploy cria
   duas máquinas, e uma segunda máquina de Postgres não teria volume. (Não ensaiado: a
   validação não tem conta no Fly. As flags foram conferidas no `flyctl` 0.4.108.)

   Toda senha de papel do Postgres aqui é gerada com `openssl rand -hex 32`, e não com
   `-base64`: elas entram em URLs `postgres://usuário:senha@…` (o `DATABASE_URL`, o `ORIGEM_URL`
   do backup), e o `/`, o `+` e o `=` do base64 quebram a leitura da URL ou exigem escape.
4. **Papéis e banco.** A imagem oficial tem shell:
   ```sh
   fly ssh console --pty -a vigilume-db -C "psql -U postgres"
   ```
   Gere as duas senhas numa máquina sua (hexadecimais, pelo motivo do passo 3) e guarde as duas
   no gerenciador **antes** de usá-las:
   ```sh
   openssl rand -hex 32   # senha de vigilume_app
   openssl rand -hex 32   # senha de vigilume_backup
   ```
   Rode o mesmo SQL de `deploy/compose/initdb.sql`, com essas **senhas novas**, e o papel de
   backup:
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
6. **Segredos do app**, sem reiniciar nada. Primeiro a `MASTER_KEY`, **à vista**:
   ```sh
   MK=$(openssl rand -base64 32); echo "$MK"
   ```
   Guarde-a no gerenciador de senhas **agora**, antes do comando seguinte. **Perder a
   `MASTER_KEY` torna ilegíveis os headers de alvo e as configurações de canal, e nenhum backup
   devolve isso**: o dump guarda o texto cifrado, não a chave. Depois de passada ao Fly, ela não
   volta: `fly secrets` não mostra valores, e a imagem do app não tem shell para lê-la do
   ambiente. Só então:
   ```sh
   fly secrets set -a vigilume --stage \
     DATABASE_URL='postgres://vigilume_app:<senha do passo 4>@vigilume-db.internal:5432/vigilume?sslmode=disable' \
     MASTER_KEY="$MK" \
     JWT_SECRET="$(openssl rand -base64 32)" \
     IP_HASH_KEY="$(openssl rand -base64 32)"
   ```
   `JWT_SECRET` e `IP_HASH_KEY` podem nascer sem ser vistos: perder a primeira só derruba as
   sessões abertas, e perder a segunda só separa os hashes de IP antigos dos novos (troque as
   duas pelo mesmo comando, com valores novos).

   `sslmode=disable` é deliberado (D31): a rede privada do Fly é uma malha WireGuard, e o banco
   não tem porta pública.

   E-mail é opcional: `RESEND_API_KEY` e `EMAIL_FROM` **juntas**, com o domínio do remetente já
   verificado no Resend (senão todo envio volta 403). Sem elas, o Discord funciona e o cadastro
   de canal de e-mail responde que ele está indisponível.
7. **Primeiro deploy do app.** O Fly puxa de `registry.fly.io` (D34): o pacote do GHCR é privado,
   e dar ao Fly uma credencial do GitHub seria guardar um token pessoal fora do GitHub. Copie a
   imagem **testada**, pelo digest, sem rebuild (o digest para o qual a tag `:<sha>` aponta, que
   só existe depois do Trivy):
   ```sh
   fly auth docker
   DIGEST=$(docker buildx imagetools inspect ghcr.io/bipoxdxd/vigilume:<sha do commit> \
     --format '{{json .Manifest}}' | jq -r .digest)
   echo "$DIGEST"   # sha256:… (vazio ou erro: esse commit não foi publicado)
   docker buildx imagetools create \
     -t registry.fly.io/vigilume:<sha do commit> \
     "ghcr.io/bipoxdxd/vigilume@$DIGEST"
   fly deploy --image "registry.fly.io/vigilume@$DIGEST" --ha=false
   fly machines list -a vigilume   # exatamente UMA máquina de pé (started)
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
   O aviso (no máximo um por minuto) traz o `remote_addr` do proxy. Ponha em `TRUSTED_PROXIES`, no
   `fly.toml`, o **menor** prefixo que cobre o que você mediu: de preferência o endereço exato
   (`/32` para IPv4, `/128` para IPv6). Olhe o aviso por alguns minutos e de mais de uma rede: se
   o proxy aparecer com endereços diferentes, liste cada um (separados por vírgula) em vez de
   alargar a faixa. Uma faixa larga da rede privada (`fdaa::/16`, `172.16.0.0/12`) funciona, mas
   **confia em toda a organização do Fly**: qualquer outra máquina sua na 6PN poderia mandar um
   `Fly-Client-IP` forjado e escolher o balde de rate limit e o `ip_hash` de quem quisesse. Use-a
   só se o endereço do proxy mudar sem padrão, e sabendo disso. Um prefixo `/0` (`0.0.0.0/0`,
   `::/0`) é recusado duas vezes: o boot não sobe com ele, e o workflow `deploy` para antes de
   começar.

   Faça o deploy de novo pelo mesmo digest (o comando do passo 7). **Leve o valor, e a região,
   para o `fly.toml` do repositório por PR** antes de usar o workflow `deploy`: ele faz o deploy
   com o `fly.toml` da `main`, e **recusa** um `TRUSTED_PROXIES` vazio ou com `/0`. Então o
   **teste de dois pontos de rede**, com um e-mail qualquer e senha errada:
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

   Se a rede B levar `429`, o `TRUSTED_PROXIES` não cobre o proxy: volte ao aviso. Se o aviso
   voltar a aparecer depois, com outro `remote_addr`, o proxy mudou de endereço: acrescente-o à
   lista pelo mesmo caminho (PR e deploy).

   Depois do `TRUSTED_PROXIES` no ar, há um segundo aviso, o do outro lado, também no máximo um
   por minuto:
   ```sh
   fly logs -a vigilume | grep "cabeçalho de IP do cliente ausente ou ilegível"
   ```
   A mensagem inteira é "cabeçalho de IP do cliente ausente ou ilegível numa conexão de
   TRUSTED_PROXIES: confira o CLIENT_IP_HEADER; estes clientes dividem o balde do proxy", com o
   nome do cabeçalho (`cabecalho`) e o `remote_addr` da conexão; o valor do cabeçalho não vai para
   o log. Quer dizer que uma conexão veio de um endereço da lista, mas sem o cabeçalho, ou com um
   valor que não é um IP. Essa requisição conta com o IP do proxy: todos os clientes nessa
   situação dividem um balde de rate limit e um `ip_hash`. Confira, nesta ordem:
   - `CLIENT_IP_HEADER` no `fly.toml`: tem de ser `Fly-Client-IP`, escrito certo. Com outro nome,
     o app procura um cabeçalho que o proxy não manda, e o aviso aparece a cada minuto;
   - `TRUSTED_PROXIES`: se o `remote_addr` do aviso é de outra máquina sua na 6PN, e não do
     proxy, a faixa está larga demais (volte ao `/32` ou `/128`, acima);
   - se o `remote_addr` é mesmo o do proxy, é tráfego do próprio Fly que chega sem
     `Fly-Client-IP`. Não foi ensaiado (não há app no Fly na validação); se acontecer, o aviso
     fica voltando uma vez por minuto, e isso vai para as pendências, não para o `fly.toml`.
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

    **Depois de ligado, um `backup` agendado "skipped" (cinza) é uma falha**, tanto quanto um
    vermelho: não houve backup naquele dia, e o GitHub **não** manda e-mail por isso. A causa
    usual é a variável `BACKUP_ENABLED` apagada ou mudada; confira-a e rode o workflow à mão.

    Rode o workflow `backup` à mão (Actions → backup → Run workflow) e confira que ficou verde.
    Se o `flyctl proxy` falhar por permissão, o token de app não basta para o túnel: troque o
    `FLY_BACKUP_TOKEN` por um token de organização, sabendo que ele alcança todos os seus apps.
14. **Voltar uma versão.** Só é seguro se as migrations da versão nova forem aditivas: o
    `goose` não roda `Down` sozinho. Os digests das versões anteriores:
    ```sh
    fly releases -a vigilume --image   # cada release com a imagem (registry.fly.io/vigilume@sha256:…)
    ```
    O workflow `deploy` pede o **SHA do commit**, não o digest. Para achar o commit de um digest,
    leia a revisão gravada na imagem pelo `publish` (o label `org.opencontainers.image.revision`):
    ```sh
    fly auth docker
    docker buildx imagetools inspect registry.fly.io/vigilume@sha256:<digest anterior> \
      --format '{{json .Image}}' | jq -r '.["linux/amd64"].config.Labels["org.opencontainers.image.revision"]'
    ```
    (ou `docker run --rm registry.fly.io/vigilume@sha256:<digest anterior> version`, que mostra
    o SHA de um build da `main` e o nome da tag de um build de tag `v*`).

    O caminho preferido é o mesmo de todo deploy: Actions → deploy → Run workflow **na `main`**
    com esse SHA. Atenção: uma tag `v*` publicada depois **move** a tag `:<sha>` daquele commit
    para o digest do build da tag (mesmo código, outro build, reescaneado); o workflow leva então
    esse digest, e não o que estava no ar. Para voltar exatamente ao digest antigo, use o caminho
    à mão.

    O workflow faz o deploy com o `fly.toml` **atual** da `main`: a imagem volta, a configuração
    não. Uma variável de ambiente nova no `fly.toml` continua lá (a versão antiga a ignora), e uma
    que a versão antiga exige e que tenha saído do `fly.toml` precisa voltar por PR antes.

    Se precisar fazer à mão, faça-o **de uma `main` atualizada** (`git switch main && git pull`),
    pelo mesmo motivo: o `fly deploy` usa o `fly.toml` local, e um `fly.toml` velho apagaria o
    `TRUSTED_PROXIES` medido no passo 9.
    ```sh
    fly deploy --image registry.fly.io/vigilume@sha256:<digest anterior> --ha=false
    ```

## Deploys seguintes

Actions → ci → o `publish` da `main` publica a imagem e só dá a tag `:<sha>` depois do Trivy;
Actions → deploy → Run workflow **na `main`** com o **SHA completo do commit** (40 hex; o resumo
do `publish` o mostra). O job recusa, em vermelho: outro branch; um SHA fora do formato; um
commit que não está na `main` (por exemplo, o de uma tag `v*` feita num branch); um SHA sem a tag
`:<sha>` no GHCR (commit não publicado, reprovado no Trivy ou com o `publish` cancelado); e um
`TRUSTED_PROXIES` vazio ou com `/0`. Ele resolve `:<sha>` no digest, copia a imagem do GHCR para
o `registry.fly.io` por esse digest e faz o deploy com `--strategy rolling` e `--ha=false`
(alguns segundos fora com uma máquina só, D26), e confere que ficou exatamente uma máquina de
pé.

**Limite da trava "só da `main`"** (desvio da D21): um `workflow_dispatch` disparado num branch
roda o `deploy.yml` **daquele branch**. Quem tem acesso de escrita pode empurrar um branch com o
passo "só da main" apagado e disparar dele; o `FLY_DEPLOY_TOKEN` é secret de repositório e chega
a qualquer branch. Hoje só o dono tem escrita, então a trava vale contra engano, não contra quem
tem acesso. Quando o repositório abrir, o environment `production` com revisor (última seção)
fecha isso, junto com uma regra de proteção da `main` (PR obrigatório, sem push direto).

O Trivy do `publish` lê a credencial do GHCR do `~/.docker/config.json` que o `docker/login-action`
gravou: é o `GITHUB_TOKEN` daquele job, com `packages:write`, montado só-leitura no contêiner do
Trivy. Risco aceito: o contêiner é fixado por digest (`deploy/ci/trivy/Dockerfile`, atualizado
pelo Dependabot) e o token expira com o job.

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

## Se o nome do app estiver tomado

Os nomes `vigilume` e `vigilume-db` são globais no Fly. Se `fly apps create` disser que o nome
existe, escolha outros e troque **todos** os lugares abaixo num PR só, antes do primeiro deploy
pelo workflow. Nenhum deles é lido de outro: cada um está escrito à mão.

| Nome | Onde |
|---|---|
| App do binário (`vigilume`) | `fly.toml`: `app`, e o host de `PUBLIC_BASE_URL` e `CORS_ORIGIN` (`https://vigilume.fly.dev`); `.github/workflows/deploy.yml`: `DESTINO` (`registry.fly.io/vigilume`) e o `-a` do `flyctl machines list`; o secret `FLY_DEPLOY_TOKEN` (`fly tokens create deploy -a <app>`) |
| App do banco (`vigilume-db`) | `deploy/fly/db/fly.toml`: `app`; `.github/workflows/backup.yml`: o `-a` do `flyctl proxy`; o host do secret `DATABASE_URL` do app (`<app do banco>.internal`); o secret `FLY_BACKUP_TOKEN` |
| Pacote do GHCR (`ghcr.io/bipoxdxd/vigilume`) | `.github/workflows/ci.yml`: `IMAGEM`; `.github/workflows/deploy.yml`: `ORIGEM`; `compose.yaml`: o `image` do `seed` e do `app` |
| Este runbook | Os comandos com `-a vigilume`, `-a vigilume-db`, `registry.fly.io/vigilume`, e as URLs `https://vigilume.fly.dev` (passos 9 e 11) |

Depois do PR, este `grep` não deve achar o nome antigo fora desta seção (a tabela acima e o
próprio comando citam os nomes antigos de propósito):
```sh
grep -rn 'vigilume\.fly\.dev\|vigilume-db\|registry\.fly\.io/vigilume\|app = "vigilume"\|-a vigilume' \
  .github fly.toml deploy docs/deploy
```
O `app = "vigilume"` pega o `app` do `fly.toml`, e o `-a vigilume` pega o do `deploy.yml` e os
comandos deste runbook (o do banco, `-a vigilume-db`, também).

## Depuração

A imagem do app não tem shell (spec §5.9): `fly ssh console -a vigilume` não serve. O que serve:
`fly logs`, os subcomandos do binário (`vigilume version`, `vigilume healthcheck`,
`vigilume user …`) e o console do **banco**, que tem shell. O `vigilume healthcheck` consulta o
`/readyz` com prazo de 3 s (acima dos 2 s que o próprio `/readyz` dá ao banco, para que um 503
apareça como 503, e não como tempo esgotado). O healthcheck do `compose.yaml` dá 5 s ao
subcomando (`timeout: 5s`), acima desses 3 s: quem responde pela falha é o próprio subcomando
(código 1), e não o Docker matando o processo por tempo. Os códigos de saída:

| Código | Quando |
|---|---|
| 0 | o `/readyz` respondeu 200 dentro dos 3 s |
| 1 | qualquer outra resposta, prazo esgotado, conexão recusada ou `HTTP_ADDR` inválido |
| 2 | argumento a mais (`vigilume healthcheck x`); o `vigilume version` faz o mesmo |
| 130 | interrompido (Ctrl-C ou SIGTERM), como o `vigilume user` |

A `PUBLIC_BASE_URL` com usuário e senha (`https://usuario:senha@…`) é recusada no boot, porque
a credencial iria parar nos links dos e-mails: a máquina não sobe, e o erro no log diz
"PUBLIC_BASE_URL não pode trazer credenciais na URL (a parte com @ antes do host)". A mensagem
não repete o valor, de propósito: ele traria a senha para o `fly logs`. Tire a credencial do
`fly.toml` e faça o deploy de novo.

## Operação do Postgres

| Responsabilidade | Mecanismo | Frequência |
|---|---|---|
| Backup | Snapshots diários do volume (14 dias) + workflow `backup` diário: dump, restauração conferida, cifra `age`, artifact de 30 dias | Automático: conferir que o `backup` segue verde. Vermelho **ou** "skipped" (cinza) é falha |
| Teste de restauração | Automático em cada `backup`; manual a cada trimestre (abaixo) | Diário e trimestral |
| Disco | O `backup` compara o tamanho dos bancos com `PG_VOLUME_GB`: aviso acima de 70%, **falha** acima de 85% (o GitHub manda e-mail). Acima de 85% o backup do dia ainda é conferido, cifrado e guardado: o job fica vermelho no último passo. Depois de `fly volumes extend <id> -s <GB>`, **atualize a variável `PG_VOLUME_GB`**, senão o aviso continua contando o tamanho velho | Quando avisar |
| Upgrade menor (18.x) | PR do Dependabot em `deploy/fly/db/Dockerfile`. O mesmo digest está em **quatro** lugares, que sobem juntos (no mesmo PR, ou em PRs mergeados juntos): `deploy/fly/db/Dockerfile`, `compose.yaml`, `.github/workflows/ci.yml` e `internal/storage/testdb` → merge → `fly deploy -c deploy/fly/db/fly.toml --ha=false --image "$(sed -n 's/^FROM //p' deploy/fly/db/Dockerfile)"` (10–30 s fora). O `backup` passa a usar a imagem nova sozinho | Quando o Dependabot abrir |
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
   Opcional, as mesmas checagens do workflow: os números estão na linha `schema N, U usuário(s),
   A alvo(s)` do log daquela execução do `backup`:
   ```sh
   docker exec -i restauro psql -U postgres -d vigilume -v versao=N -v usuarios=U -v alvos=A \
     < deploy/backup/verificar.sql   # esperado: NOTICE: cópia conferida
   ```
4. Suba o `vigilume` contra ele **com `--role=api`**, na versão que está em produção (o digest de
   `fly releases -a vigilume --image`; uma versão mais nova migraria a cópia e o teste deixaria de
   ser o da produção). Só a API: com `--role=all`, a cópia sondaria os alvos de produção e
   mandaria alertas aos canais **reais** (Discord, e-mail), como se fosse a produção.
   ```sh
   fly auth docker
   docker run --rm --network host \
     -e DATABASE_URL='postgres://vigilume_app:vigilume_app@127.0.0.1:25433/vigilume?sslmode=disable' \
     -e MASTER_KEY='<a MASTER_KEY de produção, do gerenciador>' \
     -e JWT_SECRET="$(openssl rand -base64 32)" -e IP_HASH_KEY="$(openssl rand -base64 32)" \
     -e PUBLIC_BASE_URL=http://localhost:8080 -e CORS_ORIGIN=http://localhost:8080 \
     -e HTTP_ADDR=127.0.0.1:8080 -e METRICS_ADDR=off \
     registry.fly.io/vigilume@sha256:<digest em produção> --role=api
   ```
   A senha `vigilume_app` vale aqui porque o `initdb.sql` é o do compose (a de demonstração).
   `JWT_SECRET` e `IP_HASH_KEY` são **novos**, nunca os de produção: a cópia não deve aceitar
   tokens de produção. A `MASTER_KEY` é a **de produção**, a única coisa que o treino confere
   além dos dados: faça login em `http://localhost:8080` e abra um alvo com header. Se a
   `MASTER_KEY` estiver errada, é aqui que aparece. (Sem Docker, compile o mesmo commit e rode
   `./bin/vigilume --role=api` com as mesmas variáveis. No Docker Desktop, troque `--network host`
   por `-p 127.0.0.1:8080:8080`, `HTTP_ADDR` por `:8080` e o host do `DATABASE_URL` por
   `host.docker.internal`.)
5. `docker rm -f restauro` e apague o `vigilume.dump`.

### Queda do host do banco

Máquina única, sem réplica. A máquina velha está presa ao volume velho, e o volume velho ao host
que caiu. O volume restaurado é ligado **pelo ID**, para não depender de qual volume `pgdata` o
`flyctl` escolheria (o velho continua na lista, com o mesmo nome). (Não ensaiado: a validação
não tem conta no Fly.)
```sh
fly machines list -a vigilume-db                   # anote <máquina velha>
fly volumes list -a vigilume-db                    # anote <volume velho>
fly volumes snapshots list <volume velho>
fly volumes create pgdata -a vigilume-db --snapshot-id <snapshot> -s 3 --region gru   # anote <volume novo>
# Uma máquina nova com a configuração da velha, ligada ao volume novo:
fly machine clone <máquina velha> -a vigilume-db --region gru \
  --attach-volume <volume novo>:/var/lib/postgresql
fly machine destroy <máquina velha> -a vigilume-db --force
fly machines list -a vigilume-db                   # exatamente UMA máquina, com <volume novo>
```
O Postgres faz a recuperação de crash sozinho. Confira o banco (passo 8 e o `backup` à mão, que
deve ficar verde). Os deploys seguintes do banco atualizam essa máquina e mantêm o volume dela.

O **volume velho**: não o destrua enquanto a volta não estiver conferida (se o host voltar, ele
ainda tem os dados até o instante da queda, mais novos que o snapshot). Depois de conferida,
`fly volumes destroy <volume velho> -a vigilume-db`: um volume `pgdata` sobrando seria escolhido
pelo próximo `fly deploy` ou `fly scale` que criasse uma máquina, com dados velhos.

Se o snapshot não servir, restaure o último dump (seção anterior) num banco novo, com os papéis do
passo 4.

### Upgrade maior

1. `fly apps create vigilume-db19`; volume e segredo com `-a vigilume-db19`
   (`fly volumes create pgdata -a vigilume-db19 …`, `fly secrets set -a vigilume-db19 …`, passo 3);
   `deploy/fly/db/Dockerfile` com a imagem nova, e o deploy **com `-a`**, senão ele cai no app
   do `fly.toml` (o antigo):
   `fly deploy -c deploy/fly/db/fly.toml -a vigilume-db19 --ha=false --image "$(sed -n 's/^FROM //p' deploy/fly/db/Dockerfile)"`.
2. Papéis do passo 4 no banco novo (`fly ssh console --pty -a vigilume-db19 -C "psql -U postgres"`).
3. Janela curta: pare o app (`fly scale count 0 -a vigilume`), faça o dump do antigo como
   `vigilume_backup` (`fly proxy … -a vigilume-db` + `pg_dump -Fc`) e restaure no novo como
   superusuário, preservando os donos.
4. `fly secrets set -a vigilume DATABASE_URL=…vigilume-db19.internal…`, `fly scale count 1`.
5. Na mesma virada, o backup: o `backup.yml` aponta para `-a vigilume-db` (no `flyctl proxy`) e
   o `FLY_BACKUP_TOKEN` só alcança o app antigo. Troque os dois (PR com `-a vigilume-db19` no
   `backup.yml` e no `app` de `deploy/fly/db/fly.toml`; secret novo de
   `fly tokens create deploy -a vigilume-db19`) e rode o `backup` à mão. Até isso, o backup
   diário continua copiando o banco **antigo**: fica verde enquanto o antigo estiver de pé, e
   vermelho depois que ele parar (passo 6), sem nunca ter copiado o novo.
6. Mantenha o `vigilume-db` antigo parado por 7 dias antes de destruí-lo.

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
7. **Backups e logs do Actions ficam públicos.** Num repositório público, qualquer pessoa baixa
   os artifacts e lê os logs das execuções. Os artifacts são 30 dias de dumps **cifrados** com o
   `age` (ilegíveis sem a chave privada, mas passam a depender só dela; um vazamento da chave, ou
   uma falha futura do algoritmo, abriria o histórico inteiro). Os logs do `backup` mostram a
   versão do schema, o número de usuários e de alvos, o tamanho do dump e a ocupação do disco de
   produção; os do `deploy`, os digests em produção. **Antes de mudar a visibilidade**, mova os
   backups para um lugar privado (um repositório privado separado só para o workflow de backup,
   ou um bucket R2/S3 com o upload no lugar do `upload-artifact`) e apague os artifacts antigos,
   **ou** aceite esse risco explicitamente, por escrito.
8. **Agendamentos:** num repositório público, o GitHub **desliga** os workflows agendados depois
   de 60 dias sem atividade no repositório, e avisa por e-mail. O `backup` para junto: reative-o
   em Actions quando o aviso chegar.
