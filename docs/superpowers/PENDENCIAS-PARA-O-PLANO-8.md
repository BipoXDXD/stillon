# Pendências depois do Plano 7

Este arquivo existe porque o ledger de execução vive em `.superpowers/`, fora do controle de
versão. O que está aqui é o subconjunto que não pode se perder. Ele substitui o
`PENDENCIAS-PARA-O-PLANO-6.md` como estado corrente; os anteriores ficam como histórico.

## Fechadas no Plano 7

A seção "Já valendo em produção" do `PENDENCIAS-PARA-O-PLANO-5.md` (itens 1 a 6) e as duas do
`PENDENCIAS-PARA-O-PLANO-6.md`. Fechadas no código e no runbook; nada foi executado no Fly ainda.

| Pendência | Como fechou |
|---|---|
| 1. `TRUSTED_PROXIES` no Fly | Não era só configuração: o proxy do Fly põe o IP do **app** no fim do `X-Forwarded-For`. `CLIENT_IP_HEADER=Fly-Client-IP` (D22) nos três limitadores e no `ip_hash`, com aviso limitado no log que mostra o `remote_addr` do proxy; o endereço é medido no passo 9 do runbook (de preferência `/32` ou `/128`), e o workflow `deploy` recusa o `TRUSTED_PROXIES` vazio; o boot e o `deploy` recusam um prefixo `/0` |
| 2. Papel comum do Postgres | `deploy/compose/initdb.sql`: `vigilume_app` sem superusuário nem `BYPASSRLS`, dono do banco. O mesmo SQL no compose, na restauração do backup e em produção |
| 3. `METRICS_ADDR` | `off` em produção (D33); raspar pelo Fly exigiria abrir a rede privada |
| 4. E-mail | Opcional no runbook (passo 6): `RESEND_API_KEY` e `EMAIL_FROM` juntas, domínio verificado |
| 5. `PUBLIC_BASE_URL` | `https://<app>.fly.dev` no `fly.toml`; é o host da API, do painel e da status page. O boot a valida (esquema `http`/`https`, host não vazio, sem usuário e senha) e só aceita as chaves de demonstração com `http` e host de loopback (`localhost` ou IP de loopback); com `https` recusa sempre, até em `https://localhost` |
| 6. Primeira manutenção | Num banco novo não há o que consolidar (runbook, passo 12) |
| Usuário inicial | Seed do compose por pipe (código 3 = sucesso); em produção, `vigilume user create` pelo `fly proxy` (passo 10) |
| Política de senha | A senha de demonstração tem 18 caracteres |
| Compressão das respostas da API (deixada ao Plano 7 pelo Plano 6: o summary tem ~180 KB a cada 15 s com 100 alvos) | Pela documentação do Fly (não ensaiado: não há app no Fly), o proxy comprime toda resposta sem `Content-Encoding` quando o app usa `[http_service]`, que é o caso; o painel já sai com gzip próprio e passa direto. Em desenvolvimento não há compressão, e não faz falta. Conferir no primeiro deploy |

## Dependem do dono do projeto (nenhum agente faz)

- Criar os apps, o volume, os papéis e os segredos no Fly, e o primeiro deploy (runbook
  `docs/deploy/fly.md`). **Tem custo recorrente** (estimativa do plano: ~US$ 9–11/mês); a decisão
  de criar é do dono.
- Gravar os secrets de repositório (`FLY_DEPLOY_TOKEN`, `FLY_BACKUP_TOKEN`,
  `BACKUP_DATABASE_PASSWORD`, `AGE_RECIPIENT`) e as variáveis (`PG_VOLUME_GB`, `BACKUP_ENABLED=1`);
  rodar o `backup` à mão uma vez (runbook, passo 13).
- Medir o endereço do proxy do Fly, preencher `TRUSTED_PROXIES` e levá-lo ao `fly.toml` por PR;
  o teste de dois pontos de rede (que também prova se o proxy sobrescreve um `Fly-Client-IP`
  mandado pelo cliente).
- Opcional: GitHub Pro (~US$ 4/mês) dá secrets por environment num repositório privado; o plano
  não depende dele.
- Escolher a licença (hoje: ainda não definida, repositório privado) e tornar o repositório e o
  pacote do GHCR públicos, quando quiser (runbook, última seção).

## Limites conhecidos do Plano 7

- **O caminho rápido do compose depende da imagem publicada.** Com o pacote privado, o compose
  constrói do código: o build frio da imagem mediu ~34 s num builder novo, numa máquina de 16
  núcleos (provavelmente já com o download das bases); o `up --wait` mediu ~4,5–5 s com as
  imagens prontas e ~8 s quando também construiu.
  O `docker compose pull` pela imagem publicada não foi ensaiado (o `publish` só roda depois do
  merge).
- **O seed não refaz os alvos de exemplo.** Se o `user create` passou e o cadastro dos alvos
  falhou, o `up` seguinte vê o usuário (código 3) e não tenta de novo.
- **Deploy sem revisor obrigatório:** no GitHub Free com repositório privado não há
  environments. A trava é quem pode disparar o workflow (acesso de escrita: hoje, só o dono), e o
  job só roda da `main`. Com o repositório público, um environment `production` com revisor entra
  (runbook, "Tornar o repositório público").
- **Deploy pelo SHA:** a entrada do `deploy` é o SHA completo do commit, resolvido pela tag
  `:<sha>`, que o `publish` só dá depois do reescaneamento do Trivy. Um digest solto não é aceito
  (podia ser um que reprovou, o de um `publish` cancelado ou o da variante `tools`), e o commit
  precisa estar na `main` (`git merge-base --is-ancestor`): uma tag `v*` num branch publica, mas
  não vai para produção. Uma tag `v*` publicada depois move `:<sha>` daquele commit para o build
  da tag; um rollback pelo workflow leva esse digest (runbook, passo 14).
- **A trava "só da `main`" vale contra engano, não contra quem tem escrita:** um dispatch num
  branch roda o `deploy.yml` daquele branch, e o secret de repositório chega a ele (runbook,
  "Deploys seguintes"). Fecha com environment com revisor e proteção da `main`, quando público.
- **A imagem publicada não é, byte a byte, a do E2E:** o `publish` reconstrói do mesmo código e
  das mesmas bases, para as duas arquiteturas, e reescaneia com o Trivy antes de dar tag. O Trivy
  contra o GHCR privado não foi ensaiado (só num registry local); a variante `tools` ganha as tags
  sem reescaneamento (o job `image` a varre na amd64). As tags das duas variantes saem num passo
  único, depois do Trivy. Esse Trivy lê o `GITHUB_TOKEN` do job (`packages:write`) do
  `config.json`: risco aceito, com o contêiner fixado por digest.
- **Máquina única do app:** cada deploy deixa o monitor alguns segundos fora (`rolling`, D26).
- **Postgres num nó só**, sem PITR: perde-se até um dia de checks no pior caso.
- **Backup:** código 3 = uma checagem falhou (não há backup que sirva); código 4 = disco acima de
  85%: o dump conferido é cifrado e guardado mesmo assim, e o job fica vermelho no fim. Os
  comandos do Fly de queda do host e de rollback vêm da documentação, não foram ensaiados.
- **O digest do Postgres está em quatro lugares:** `deploy/fly/db/Dockerfile`, `compose.yaml`, a
  matriz do `.github/workflows/ci.yml` e `internal/storage/testdb` (o 17 da matriz também). O
  Dependabot vê só os dois primeiros, em dois PRs separados (ecossistemas `docker` e
  `docker-compose`); a matriz e o `testdb` se atualizam à mão, no mesmo PR ou em PRs mergeados
  juntos. `TestPostgres18DigestIsTheSameEverywhere` fica vermelho enquanto os quatro diferirem,
  então um PR do Dependabot sozinho não passa no CI.
- **Versões das ferramentas Go do CI** (`staticcheck`, `gosec`, `govulncheck`, `sqlc`, `gitleaks`,
  no `env` do `ci.yml`) se atualizam à mão: o Dependabot não as vê.
- **O painel exige contexto seguro** (HTTPS ou `localhost`). Em HTTP por um IP da rede, ele mostra
  um aviso e a coordenação do refresh entre abas (Web Locks) fica desligada.
- **O E2E espera ~21 s** antes do fluxo principal, para os limitadores de requisição encherem de
  novo depois das specs anteriores, e a limpeza do fim repete com recuo (até ~30 s) depois de um
  429; roda em série.
- **O pipeline nunca rodou no GitHub.** Tudo foi ensaiado localmente (actionlint com shellcheck,
  os passos com o texto exato, o e2e num Docker novo, o `publish` contra um registry local). O
  critério de merge é o PR com todos os jobs verdes; o `publish` só roda depois do merge, e a
  primeira execução dele na `main` precisa ser acompanhada.
- **O que continua não ensaiado no Fly** (não há conta na validação): os dois `fly deploy`
  (inclusive `--ha=false` e o `[[restart]]`), o `flyctl proxy` do backup com token de app, a
  medida do proxy e se ele sobrescreve um `Fly-Client-IP` mandado pelo cliente, a compressão do
  proxy, `fly releases --image`, os comandos de queda do host (`machine clone
  --attach-volume`, `volumes destroy`) e o upgrade maior. O Trivy contra o GHCR privado e o
  `docker compose pull` da imagem publicada também não.

## Da revisão do Plano 7, ainda abertas

A onda final do Plano 7 fechou o resto dos achados das revisões (runbook no deploy pelo SHA,
commit da `main` no `deploy`, `/0`, chaves de demo só com `http` em loopback, `userinfo` na
`PUBLIC_BASE_URL`, `healthcheck`, avisos do IP do cliente, imagem, compose, o teste oscilante do
`cmd/vigilume`, o `pull` do Postgres no e2e e o ensaio do backup no tree integrado). Ficam:

- **Nomes de cabeçalho perigosos em `CLIENT_IP_HEADER`:** nada impede `Forwarded`, `Host` ou
  `Cookie` ali. Uma denylist curta no boot evitaria um erro de configuração que só aparece no
  rate limit.
- **`Unmap` no caminho `X-Forwarded-For`/`RemoteAddr` do `ip_hash`:** o caminho do cabeçalho
  normaliza IPv4 mapeado em IPv6; esse outro não. O mesmo cliente pode gerar dois hashes.

## Herdadas, ainda abertas

As do `PENDENCIAS-PARA-O-PLANO-6.md`, seção "Herdadas, ainda abertas", seguem como estavam.
