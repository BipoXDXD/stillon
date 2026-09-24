# Pendências depois do Plano 7

Este arquivo existe porque o ledger de execução vive em `.superpowers/`, fora do controle de
versão. O que está aqui é o subconjunto que não pode se perder. Ele substitui o
`PENDENCIAS-PARA-O-PLANO-6.md` como estado corrente; os anteriores ficam como histórico.

## Fechadas no Plano 7

A seção "Já valendo em produção" do `PENDENCIAS-PARA-O-PLANO-5.md` (itens 1 a 6) e as duas do
`PENDENCIAS-PARA-O-PLANO-6.md`. Fechadas no código e no runbook; nada foi executado no Fly ainda.

| Pendência | Como fechou |
|---|---|
| 1. `TRUSTED_PROXIES` no Fly | Não era só configuração: o proxy do Fly põe o IP do **app** no fim do `X-Forwarded-For`. `CLIENT_IP_HEADER=Fly-Client-IP` (D22) nos três limitadores e no `ip_hash`, com aviso limitado no log que mostra o `remote_addr` do proxy; o CIDR é medido no passo 9 do runbook, e o workflow `deploy` recusa o `TRUSTED_PROXIES` vazio |
| 2. Papel comum do Postgres | `deploy/compose/initdb.sql`: `vigilume_app` sem superusuário nem `BYPASSRLS`, dono do banco. O mesmo SQL no compose, na restauração do backup e em produção |
| 3. `METRICS_ADDR` | `off` em produção (D33); raspar pelo Fly exigiria abrir a rede privada |
| 4. E-mail | Opcional no runbook (passo 6): `RESEND_API_KEY` e `EMAIL_FROM` juntas, domínio verificado |
| 5. `PUBLIC_BASE_URL` | `https://<app>.fly.dev` no `fly.toml`; é o host da API, do painel e da status page. O boot a valida (esquema `http`/`https`, host não vazio) e recusa as chaves de demonstração quando ela é https, sem diferença de maiúsculas |
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
  constrói do código: o build frio da imagem mediu ~34 s numa máquina de 16 núcleos, sem contar
  o download das bases e do Postgres; com as imagens na máquina, o `up --wait` mediu 4,5–8,2 s.
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
  (podia ser um que reprovou, o de um `publish` cancelado ou o da variante `tools`).
- **A imagem publicada não é, byte a byte, a do E2E:** o `publish` reconstrói do mesmo código e
  das mesmas bases, para as duas arquiteturas, e reescaneia com o Trivy antes de dar tag. O Trivy
  contra o GHCR privado não foi ensaiado (só num registry local); a variante `tools` ganha as tags
  sem reescaneamento (o job `image` a varre na amd64).
- **Máquina única do app:** cada deploy deixa o monitor alguns segundos fora (`rolling`, D26).
- **Postgres num nó só**, sem PITR: perde-se até um dia de checks no pior caso.
- **Backup:** código 3 = uma checagem falhou (não há backup que sirva); código 4 = disco acima de
  85%: o dump conferido é cifrado e guardado mesmo assim, e o job fica vermelho no fim. Os
  comandos do Fly de queda do host e de rollback vêm da documentação, não foram ensaiados.
- **O digest do Postgres está em quatro lugares:** `deploy/fly/db/Dockerfile`, `compose.yaml`, a
  matriz do `.github/workflows/ci.yml` e `internal/storage/testdb` (o 17 da matriz também). O
  Dependabot vê só os dois primeiros, em dois PRs separados (ecossistemas `docker` e
  `docker-compose`); a matriz e o `testdb` se atualizam à mão, no mesmo PR ou em PRs mergeados
  juntos, para desenvolvimento, testes e produção seguirem na mesma imagem.
- **Versões das ferramentas Go do CI** (`staticcheck`, `gosec`, `govulncheck`, `sqlc`, `gitleaks`,
  no `env` do `ci.yml`) se atualizam à mão: o Dependabot não as vê.
- **O painel exige contexto seguro** (HTTPS ou `localhost`). Em HTTP por um IP da rede, ele mostra
  um aviso e a coordenação do refresh entre abas (Web Locks) fica desligada.
- **O E2E espera ~21 s** antes do fluxo principal, para os limitadores de requisição encherem de
  novo depois das specs anteriores; roda em série.

## Da revisão do Plano 7, ainda abertas

Achados menores das revisões das tarefas, registrados no ledger para a rodada final do Plano 7.
O que não entrar nela fica para o Plano 8.

- **Runbook × deploy pelo SHA:** a seção "Deploys seguintes" de `docs/deploy/fly.md` ainda
  manda disparar o `deploy` com o digest; o workflow pede o SHA do commit.
  Também: `jq` como pré-requisito, e a nota de que um dispatch em outro branch usa o próprio
  arquivo do workflow.
- `deploy.yml`: conferir que o SHA é ancestral da `main` (`git merge-base --is-ancestor`); uma tag
  `v*` fora da `main` publicaria um `:<sha>` que nunca passou por ela.
- `ci.yml`: comentário velho dizendo que o Trivy do job `image` não varre o `tools`.
- `vigilume healthcheck`: `HTTP_ADDR` sem `TrimSpace`; prazo do cliente igual ao do `/readyz`
  (esconde o 503); Ctrl-C sai 1 em vez de 130; argumentos extras ignorados.
- `PUBLIC_BASE_URL` com usuário e senha (`https://u:s@host`) é aceita e sai no log de boot.
- IP do cliente: teste de "sem aviso com par confiável"; aviso para par confiável sem o cabeçalho
  (um nome de cabeçalho errado junta todos os clientes em silêncio); `Unmap` no caminho
  `X-Forwarded-For`/`RemoteAddr` do `ip_hash`.
- Imagem: `ARG VERSION` logo antes do `go build` (hoje invalida o cache do `go mod download`);
  `checar-imagem.sh` com timeout no `version` e remoção do contêiner no `trap`; `.dockerignore`
  com `**/.env*`, `vigilume` e `**/*.test`.
- Compose: `seed.sh` conferir o logout; `PUBLIC_BASE_URL` seguir `VIGILUME_PORT`; comentário do
  healthcheck do `db`.
- Teste oscilante: `TestAllRoleConsolidatesAndServesThePublicPage` (`cmd/vigilume`) faz poll de
  `/p/` a cada 100 ms e bate no próprio limitador sob carga (429).
- Refazer o ensaio do backup no tree integrado, com o `initdb.sql` do compose.

## Herdadas, ainda abertas

As do `PENDENCIAS-PARA-O-PLANO-6.md`, seção "Herdadas, ainda abertas", seguem como estavam.
