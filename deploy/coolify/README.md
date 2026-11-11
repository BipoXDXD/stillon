# Publicação no Coolify

A publicação usa aplicações Git Compose em modo Raw, com repositório preservado e sem comando de start customizado. Os containers reais são pertencentes ao recurso no Coolify. Não há observador substituindo a aplicação.

Aplicação e infraestrutura têm recursos separados para que uma publicação da aplicação não pare o banco. As definições compartilham o nome Compose original, usam redes e volumes externos e não executam remoção de órfãos. Os binds privados existem previamente no host; suas credenciais não ficam no repositório. Cada imagem é fixada por digest e deve estar previamente importada e validada; `pull_policy: never` impede trocar a imagem durante o corte.

A configuração de preparação não executa escritores em segundo plano. Depois da cópia final consistente, a configuração definitiva deve habilitar os serviços completos antes da mudança de DNS.

Segredos de interpolação são configurados como variáveis privadas no Coolify. O modo Raw conserva o isolamento por serviço das variáveis e dos arquivos de segredo. Não adicionar env_file global a todos os serviços.

`PROBE_BLOCKED_CIDRS` (os IPs públicos da VPS) é variável obrigatória do recurso do app, com as mesmas marcações das outras que o `app.json` exige com `${…:?}`: sem ela, o manifesto não renderiza e o app recusa o boot. Ela entra no recurso **antes** de qualquer release que a exija, e o commit de infraestrutura do recurso só passa a apontar para o `app.json` que a exige depois disso. A ordem geral para qualquer variável obrigatória nova está em [release/README.md](release/README.md#variável-obrigatória-nova-expandcontract).

O `infra-fixada` guarda o SHA completo do commit de infraestrutura fixado no recurso do app. O release só troca a imagem, e o `TestOAppDesteCommitSobeNaInfraestruturaFixada` roda o `config.Load` do código com o ambiente que o `app.json` desse commit entrega: se o código passar a exigir uma variável que esse `app.json` não repassa, o CI reprova antes do merge (postmortem de 2026-11-10). Quem atualiza o commit de infraestrutura no recurso (Passo 7) atualiza este arquivo no mesmo passo, num PR. Um nome novo interpolado no `app.json` entra também no `variaveisDoRecurso` do teste, depois de conferido no recurso.

Manter backups e restauração verificada antes de atualizar uma versão. Não apontar tráfego para a origem com dados antigos após o destino aceitar escritas. Uma reversão precisa primeiro preservar e transferir o estado novo.

Todo serviço tem teto de CPU (`cpus`) e de memória (`mem_limit`): a VPS tem 2 vCPUs divididas com outros projetos. Os valores e o porquê de cada um estão comentados no `deploy/vps/compose.prod.yaml`, que usa os mesmos tetos; o `TestProductionComposeLimitsEveryService` exige os dois em todo serviço. Como o Coolify fixa o commit de infraestrutura, um teto novo só chega aos containers depois que esse commit é atualizado no recurso e o recurso é publicado de novo.

## Atualizar o digest de uma imagem

O Dependabot atualiza os arquivos Compose (`compose.yaml` e `deploy/vps/compose.prod.yaml`), mas não enxerga os manifestos daqui. Em todo PR dele que troque o digest do Caddy ou do Postgres, leve o mesmo digest, à mão, a `app.json` (Caddy) ou `database.json` (Postgres) antes do merge. Publicar a troca é um passo separado: a imagem tem de estar importada no host, e o commit de infraestrutura, atualizado no recurso.

## Imagem do banco: não fazer redeploy do `db` antes de restaurá-la

Em 2026-11-06, a imagem de `stillon-db-1` estava em disco, sem tag nem digest:
`postgres:18-alpine@sha256:77f58511…` respondia `No such image` por nome. O compose do banco usa
`pull_policy: never`, então qualquer ação que recrie esse container falha, como no incidente do
GoblinShift de 2026-11-05. O release não recria o banco: ele só atualiza o recurso do app. Reboot e
restart preservam o container, e o cleanup do Coolify não remove imagem de container existente.

Para restaurar, fora do cleanup das 00:00 às 06:30 UTC e sem reserva da VPS:

1. Confirmar no registro, sem baixar, que o digest existe e é de manifesto, e não um ID local.
2. Fazer `docker pull` pelo digest exato, sem recriar o container.
3. Conferir com `docker image inspect` que o ID não mudou e que `stillon-db-1` segue de pé.

Se o digest não existir ou o ID mudar, pare. Um redeploy do banco precisa do pull na mesma sessão da
publicação.
