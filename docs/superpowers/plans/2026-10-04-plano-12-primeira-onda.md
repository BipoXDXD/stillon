# Plano 12: execução da primeira onda

> Agentes: aplicar planejamento, depuração, testes com prova por sabotagem e revisão independente.
> O dono solicitou execução autônoma com múltiplos GPT-6.1 High; decisões locais reversíveis não
> precisam de nova confirmação. Não fazer merge, deploy nem mudanças em contas externas.

**Objetivo:** corrigir vazamentos em redirects e inicialização do banco, evitar consumo indevido
de segundo fator e entregar uma proposta de painel acessível e responsivo.

**Arquitetura:** manter binário único, PostgreSQL e React embutido. Compartilhar apenas a
canonicalização de host necessária à segurança e as primitivas de apresentação existentes.

**Stack:** Go 1.26.8, pgx/sqlc/goose, PostgreSQL 17/18, React 19, Tailwind 4, Vitest e Playwright.

**Spec:** [primeira onda](../specs/2026-10-04-plano-12-primeira-onda.md).

## Restrições globais

- Português do Brasil em comunicação e documentação.
- Trabalhar na branch `melhoria/plano-12-seguranca-painel`; main só muda por PR.
- Não adicionar infraestrutura, pacotes de UI ou armazenamento de tokens no navegador.
- Teste novo só vale após vermelho observado e sabotagem da propriedade com restauração.
- Não imprimir segredos; usar sentinelas públicas nos testes de argv e headers.
- Nunca usar `make e2e` sobre volumes existentes: a pilha de verificação deve ser descartável.

## Foco da revisão

1. Redirect com User-Agent secreto, Unicode IDNA, porta diferente ou cadeia que volta à origem.
2. Argon2 saturado entre a senha atual e o hash novo; código de recuperação deve continuar válido.
3. Senhas PostgreSQL com aspas/barra invertida; argv e saída padrão devem permanecer sem segredo.
4. Estado de mutação em voo, falha de polling, foco e dados antigos do painel.
5. Celular com 360 px, tema escuro e CSP estrita, sem perder navegação ou ações.

## Tarefa 1: sondador e troca de senha

Propriedade: `internal/prober/safedial/`, `internal/prober/probe*`,
`internal/targets/validate.go` e testes relacionados, `internal/account/account.go` e testes.

- [x] Testar redirect real com User-Agent sentinela para outro servidor de teste; o destino
  deve receber somente identificação pública. Testar o par IDNA e cadeia insegura que volta.
- [x] Rodar testes novos contra código original e guardar a falha esperada.
- [x] Implementar canonicalização compartilhada, limpeza total e UA público.
- [x] Reproduzir saturação do segundo hash de senha com relógio/banco reais e controle do
  semáforo já disponível nos testes; garantir que o recovery continua utilizável após `429`.
- [x] Ordenar o hash novo antes da verificação que consome o fator, preservando CAS/família.
- [x] Testes focados com `-race`, sabotagem isolada/restaurada e revisão independente.

## Tarefa 2: painel e UX

Propriedade: `web/src/`, `web/public/tema.js`, `web/e2e/` e seus testes.
Não alterar package/lock, configuração de sessão/API ou DTOs nesta tarefa.

- [x] Preparar testes de Pausar durante Retomar, avisos de canais e contagens reais.
- [x] Observar falhas e corrigir a guarda do clique sem alterar semântica de envio.
- [x] Implementar tokens, shell, cartões e hierarquia visual com estados e rótulos existentes.
- [x] Ajustar formulários de headers, canais e status page para telas pequenas.
- [x] Conferir a advertência de HTTPS com contexto inseguro em desktop e celular, sem
  oclusão pela lateral; provar a regressão por geometria e teste de ponto visível.
- [x] Adicionar E2E que mede largura da página e exercita navegação em 360 px, nos dois temas.
- [x] Conferir typecheck, lint, testes, build; provar testes novos por sabotagem; revisão.
- [x] Capturar screenshots depois do build e conferir visual, foco, axe e CSP reais.

## Tarefa 3: operação local e cadeia de suprimentos

Propriedade: `deploy/vps/initdb.sh`, `deploy/compose/seed.sh`, testes de config ligados,
`deploy/backup/backup.sh`, `Dockerfile` e `.github/workflows/ci.yml` se a desativação de scripts npm for comprovada.

- [x] Demonstrar argv de psql com executável falso e sentinelas, sem banco do dono.
- [x] Testar que credenciais com aspas e barras são aplicadas num PostgreSQL descartável.
- [x] Transportar senhas via ambiente e `\getenv`/quoting do psql no initdb; no parser de
  backup, decodificar via stdin e passar PGPASSWORD somente à origem, sem segredo em argv.
- [x] Retirar a senha da URI de backup passada como argumento, sem modificar a credencial
  da restauração; validar percent-encoding e backup/restauro reais com caracteres especiais.
- [x] Testar URLs com porta inválida e whitespace no seed, observar vermelho e endurecer parser.
- [x] Consultar documentação atual de npm e ferramentas de build para `--ignore-scripts`.
- [x] Confirmar instalação/build reais antes de mudar Dockerfile e CI; preservar jobs existentes.
- [x] Provar testes por sabotagem, rodar checagens shell/config e obter revisão independente.

## Tarefa 4: integração, revisão e continuidade

Propriedade do coordenador: documentação, registro de evidências, execução integrada e Git.

- [x] Guardar checks futuros no banco com migration aditiva; RED/GREEN/sabotagem/restauração,
  prova de rollback da histerese e compatibilidade PostgreSQL 17/18.
- [x] Adicionar CHECK do par segredo/família pendentes de TOTP, provar INSERT/UPDATE e
  preservação do estado após recusa em PostgreSQL 17/18; sem reparar linhas antigas.
- [x] Ler também Markdown ignorados em `.superpowers/` e registrar cobertura dos históricos,
  distinguindo arquivos antigos de requisitos correntes.
- [x] Registrar livros e capítulos consultados, com aplicação concreta e sem copiar conteúdo.
- [x] Conferir diffs por grupo, tratar achados de revisão com implementador e nova conferência.
- [x] Rodar `go build ./... && go vet ./... && make test`, painel e teste de nome anterior.
- [x] Executar E2E isolado e scanners locais disponíveis; documentar limitações verdadeiras.
- [x] Atualizar pendências correntes sem apagar o histórico das decisões anteriores.
- [x] Preparar relatório com branch, mudanças, provas, riscos e próximo passo guiado da VPS.
- [ ] Criar checkpoint local assinado após verificações; tentativa bloqueada por GPG
  `No pinentry`, sem commit criado. Índice e patch preservados; retomada com o dono,
  sem nova tentativa ou assinatura desabilitada por iniciativa do agente.

O progresso e os resultados completos ficam no relatório desta execução, junto dos comandos
de verificação. Esta primeira onda não muda o estado das pendências que não forem comprovadas.

## Tarefa 5: ensaio de SSH real

Propriedade: `deploy/ci/testar-ssh-real.sh`, `deploy/ci/ssh/` e passo no job VPS de
`.github/workflows/ci.yml`. Acompanhamento do digest em `.github/dependabot.yml` pelo coordenador.

- [x] Construir servidor e cliente OpenSSH no Ubuntu 24.04 fixado por digest, sem publicação
  de portas, socket Docker ou modo privilegiado dentro desses contêineres.
- [x] Criar chaves efêmeras no diretório temporário do teste e usar o sudoers de produção.
- [x] Executar o cliente `vps.sh` real e conferir pedidos, códigos 0/2/3/4 e transporte de
  bytes de backup por hash e comparação integral.
- [x] Recusar host key errada, comandos arbitrários e PTY, sem executar ações do pedido.
- [x] Sabotar apenas a cópia do sudoers retirando o ambiente necessário; observar RED,
  restaurar a cópia e observar GREEN. Não modificar configuração SSH da máquina ou da VPS.
- [x] Obter revisão independente e validar sintaxe shell/workflow; registrar duração e limpeza.

## Tarefa 6: deadline de trabalho HTTP

Propriedade: middleware de deadline e testes, cadeia de `routes.go`/página pública,
`apierr/errors.go`, tratamento HTML da verificação e integração de deadline.

- [x] Reproduzir espera de advisory lock além do WriteTimeout com servidor, rotas e PG reais;
  observar conexões, requisições na fila, ausência de deadline e commits sem resposta.
- [x] Aplicar prazo de 15 s ao contexto de API/verify/pública, herdando cancelamento/prazo menor,
  preservando trace, CORS, envelopes e erros de domínio, sem buffer ou goroutine por requisição.
- [x] Conferir `503 unavailable`, cancelamento de SQL e aquisição do pool, nenhuma criação
  depois de vencer e liberação dos recursos, mantendo a trava durante a recusa.
- [x] Sabotar wiring e classificação de erro em cópia isolada, restaurar e conferir GREEN;
  obter revisão independente antes dos gates finais.
- [x] Preservar semáforo Argon2 e transações; não ampliar refactor de rollback sem necessidade
  demonstrada. Registrar limites de execução CPU e commits já concluídos.
