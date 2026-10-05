# Terceira onda: tema persistido e contrato de mutação

Data: 2026-10-04. Continuação autônoma autorizada durante a ausência do dono.
Branch `melhoria/plano-12-seguranca-painel`; base `cd81622`. O checkpoint combinado
da segunda onda fica preservado antes destas alterações. Estado: código congelado,
gates/revisões locais concluídos. [Resultados](../reports/2026-10-04-terceira-onda.md).

## Problemas e decisões

O bootstrap de tema tratava preferência inválida como claro, enquanto o provider
normalizava para sistema. Com sistema escuro, havia divergência antes/depois da montagem.
Normalizar o bootstrap para sistema, mantendo chave, carregamento síncrono e CSP.
Testar o arquivo público real executado em DOM, provider real e persistência após
nova carga com preferência de sistema oposta. Não comparar constantes por regex;
teste DOM não prova primeira pintura nem ausência de piscada no navegador.

README e cliente/API falsa exigem `X-Requested-With: stillon`; antes desta onda,
o servidor aceitava qualquer valor não vazio. Alinhar o backend ao literal publicado, com valor
sensível a caixa; nome do header continua seguindo a semântica HTTP. Clientes
oficiais usam o valor correto. Callers que enviam outro valor passam a receber 403.
Não afirmar bypass CSRF explorado: header customizado exige preflight independente
do valor. Não mudar CORS, cookies, GET/HEAD/OPTIONS ou exceções públicas/verificação.
Não ampliar nesta onda a política de headers duplicados/OWS normalizado pelo HTTP.

## Ownership e sequência

- `painel_contexto`: `web/public/tema.js` e novo `web/src/lib/tema.test.tsx`.
- `revisao_backend`: `internal/httpapi/middleware/csrf.go`, o teste existente de
  middleware e `internal/httpapi/auth_integration_test.go`.
- Coordenação: documentação comum, gates gerais, índice e novo checkpoint.
- Revisão: agente diferente do implementador; todos preservam edições concorrentes.

Começar pelos testes: inválido + sistema escuro deve falhar no bootstrap atual;
valor incorreto deve alcançar login hoje e reprovar a expectativa de 403. Controle
positivo HTTP real exige 401 invalid_credentials, distinguindo auth/body/rota.
Depois corrigir minimamente e executar GREEN. Sabotagens em cópia: chave divergente
do tema/bootstrap, remoção da normalização e retorno à guarda de mera presença;
exigir RED comportamental compilável e GREEN depois de restaurar. Não sabotar o
checkout compartilhado nem os checkpoints anteriores.

## Gates e entrega

Vitest focado e completo, typecheck/lint/build; Go unit e integração real focados,
depois `go build ./... && go vet ./... && make test` com Docker e race. Lint/sec e
nome/whitespace/diff de segredos. Imagens atuais precisam rebuild/checagem por
mudança de código/painel; scans e provas adicionais somente conforme risco novo.
Não repetir toda a fixture de operação do backup sem alteração desses arquivos.

Testes devem observar classe/estado/recarga e resposta/efeito do handler; não só
espelhar a expressão implementada. Revisões precisam distinguir logs de reexecução.
Atualizar pendências e handoff depois das provas, preservando checkpoint anterior.
GPG, integração via PR e hospedagem continuam pendentes; sem ação remota ou segredo.
