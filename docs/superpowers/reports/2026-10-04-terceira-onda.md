# Terceira onda do Plano 12 — 2026-10-04

**Estado: código congelado, revisões favoráveis e gates locais concluídos com código 0.** Branch `melhoria/plano-12-seguranca-painel`, base
`cd81622`; três agentes GPT-6.1 High com ownership disjunto. O Plano 12 inteiro
continua parcial. Checkpoints anteriores preservados.

## Resultado e compatibilidade

O script público de tema normaliza preferência inválida para sistema, como o
provider já fazia. Antes, inválido + sistema escuro produzia classe clara antes do
React e escura após a montagem. Mudança mínima, sem chave, provider, CSP, configuração
ou dependência nova. Dezoito testes executam o script público real sobre DOM/storage
compartilhados com o provider real: matriz preferência/sistema, storage indisponível,
seleção, recarga com sistema oposto, mediaquery e limpeza de listener.

A guarda HTTP agora exige `X-Requested-With: stillon`, com valor sensível a caixa,
alinhando README, cliente e API falsa. O nome do header segue a semântica HTTP;
GET/HEAD/OPTIONS e exceções públicas/verificação permanecem. Cliente oficial usa
o literal; callers que enviavam outro valor passam a receber 403. Isso corrige
um desvio de contrato, sem afirmar bypass CSRF explorado nem tratar o valor como
segredo. CORS, cookies e política de duplicados/OWS não foram ampliados.

## Provas causais e revisão

Tema recebeu RED inicial compilável no caso inválido + sistema escuro antes do
provider; correção passou 18/18. Três mutantes isolados: chave só no bootstrap,
chave só no provider e normalização removida. Todos receberam RED comportamental,
restauração por bytes e GREEN. Falhas preliminares de cwd/import/tipos/lint são
separadas de RED e foram corrigidas somente no teste; manifests/config intocados.

CSRF tem 35 casos unitários (sete métodos × cinco entradas), com next sentinela
não chamado nos recusados. Cinco casos no login HTTP/PostgreSQL real exigem
403 forbidden nos negativos e 401 invalid_credentials no controle literal, com
trace_id. Guarda original e mutante isolado de mera presença falharam em oito
casos unitários e dois HTTP; restauração passou com race. Tentativas do harness
que não editaram código ou executaram no cwd errado não contam como RED.

Agentes diferentes dos implementadores revisaram código, contratos, isolamento,
logs e hashes congelados, sem bloqueador final. Revisão de logs não foi apresentada
como execução de suítes. Pareceres detalhados em `.superpowers/`.

## Gates atuais

| Gate | Resultado observado |
| --- | --- |
| `go build ./... && go vet ./... && make test` | Código 0; PG18 real, todos os pacotes com race; inclui integração de backup/config novamente |
| Frontend typecheck/lint/test/build | Código 0; 269 testes em 20 arquivos |
| `make lint sec` | Código 0; zero vulnerabilidades chamadas/importadas e um aviso em módulo requerido |
| Runtime/tools | Build 0; versão `cd81622-plano12-terceira` |
| Checagem runtime | Código 0; nonroot, sem shell, painel/versão; 7.741.153 bytes comprimidos |
| Trivy runtime/tools | Código 0; nenhum HIGH/CRITICAL corrigível no filtro usado |
| Compose e Playwright | Código 0; 14/14 em 3,8 min, imagens atuais, projeto descartável exclusivo |

Limpeza seletiva terminou com código 0. Conferência independente do projeto exato
retornou zero contêineres/redes/volumes restantes; pilha do dono preservada.

Logs da coordenação: `/tmp/stillon-terceira-coordenacao-20261004/`.
Tema: `/tmp/stillon-tema-terceira-onda-20261004/`.
CSRF: `/tmp/stillon-terceira-onda-20261004/csrf/`.
[Plano e decisões](../plans/2026-10-04-tema-header-terceira-onda.md).

## Limites e continuidade

DOM/VM não mede primeira pintura ou piscada visual no navegador. E2E de a11y,
viewport e HTTPS usa fixtures de API sob CSP da pilha real; fluxo/troca usam backend
real. Duas abas usam backend real, mas auditoria SQL opcional fica indisponível
contra Compose externo: não alegar prova SQL de reuse=0 nessa modalidade.

A fixture VPS completa e os laboratórios causais de snapshot/SIGKILL não foram
repetidos nesta rodada sem alteração de seus scripts/helper. Suíte Go atual executou
os testes de backup/config. [Segunda onda e suas provas](2026-10-04-segunda-onda.md)
continuam registradas separadamente. Não houve nova suíte completa PG17 nesta rodada.
Predicados de restauro e os quatro manifests permanecem equivalentes à base.

Sem push, PR, merge, Actions, VPS/DNS ou segredo do dono. GPG continua habilitado
sem retry após falta de pinentry na tentativa única da primeira onda. Estética,
integração, proveniência assinada da imagem, rotação de MASTER_KEY, licença e as
outras decisões críticas permanecem pendentes. [Handoff](2026-10-04-continuacao.md).
