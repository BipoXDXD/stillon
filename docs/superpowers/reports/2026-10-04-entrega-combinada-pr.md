# Rascunho local de PR — segurança, operação e painel

Não enviado ao GitHub. Branch `melhoria/plano-12-seguranca-painel` → `main`.
Mudança com código: não usar `[skip ci]`. O rascunho da primeira onda permanece
como registro anterior; este descreve o escopo final combinado das três ondas.

## Título

Reforçar autenticação e sondas, supervisionar backup e renovar o painel

## Descrição

Redirects podiam transportar headers privados, saturação do segundo hash de senha
podia consumir recovery e SQL podia continuar depois do timeout de escrita HTTP.
Remove headers após salto inseguro, compartilha canonicalização IDNA, calcula o
hash antes de consumir o fator e propaga contexto cooperativo de 15 s. Migrations
aditivas recusam checks infinitos/futuros e pares TOTP pendentes incompletos.

Contagens e dump do backup tinham referências diferentes sob escrita concorrente.
Agora compartilham snapshot exportado, com psql/pg_dump reais supervisionados pelo
binário existente. Distribuição automática usa imagem por ID imutável e protocolo
explícito; helper sem root/capabilities, coleta dos filhos, URI/ambiente protegidos
e recuperação de leases próprios. Predicados de restauro, códigos 0/3/4 e recursos
3 GiB/2 GiB preservados. Teste comportamental cobre a trava comum de partições.

Inicialização/backup retiram senhas de argv; seed recusa URL inválida e instalação
npm desliga hooks. Ensaio SSH com OpenSSH/sudo confere bytes, códigos, host key,
PTY e comando forçado. Painel ganha navegação lateral/celular, contraste conferido,
contagens, latência/uptime e avisos de canais; corrige Pausar durante Retomar e
aviso HTTPS oculto. Bootstrap normaliza preferência inválida como sistema, com
prova de persistência/recarga. Mutações HTTP exigem literal stillon já publicado,
com negativos e controle positivo no login real. Aceitação estética aguarda o dono.
Sem novas dependências.
Runbook passa a usar esta máquina Linux como ponto de operação; o guia Mac fica
histórico/opcional e uma eventual reinstalação exige novas conferências.

## Validação local

- Pacote final: `go build ./... && go vet ./... && make test`, PG18 real e race,
  código 0. Suíte completa PG17 passou na primeira onda; teste novo de lock passou
  focado nos PG17/18. Não repetir o resultado antigo como suíte final PG17.
- `make lint sec`, ShellCheck, actionlint/workflows, cliente VPS, nome/whitespace
  verdes. Govulncheck: zero achados chamados/importados e um em módulo requerido.
- Runtime/tools novos construídos; Trivy sem HIGH/CRITICAL corrigível. Runtime
  nonroot/sem shell, versão/painel conferidos: 7.741.153 bytes comprimidos.
- Ensaio VPS local completo com distribuição, oito fronteiras de falha,
  recuperação após SIGKILL, backup cifrado/restauro e recursos de terceiros.
- Testes reais de snapshot/concorrência, SCRAM, owners/RLS/partições e encerramento
  de clientes/sessões. Sabotagens comportamentais compiláveis, restauração e GREEN.
- Frontend atual: typecheck/lint/build e 269 testes verdes; 14/14 Playwright em
  3,8 min na imagem atual/pilha API/PG real. A11y/viewport/HTTPS usam fixtures sob
  CSP real; abas não consultam a auditoria SQL opcional nesse Compose externo.
- Grupos revisados por agente diferente do implementador, sem bloqueador final.

[Primeira onda, capturas e limites](2026-10-04-primeira-onda.md);
[segunda onda e logs](2026-10-04-segunda-onda.md);
[terceira onda e gates atuais](2026-10-04-terceira-onda.md).

## Antes de integrar e hospedar

Conferir pares TOTP inconsistentes antes da 00011, sem limpeza automática. Deadline
não preempta Argon2 nem desfaz COMMIT confirmado. Outro valor de X-Requested-With
agora recebe 403; não foi demonstrado bypass CSRF anterior. DOM não prova primeira
pintura/ausência de piscada. Atualizar scripts/imagem juntos.
ID imutável não prova assinatura da imagem; UID/capabilities não isolam o banco
local de restauro. Morte do cliente Docker/SSH não garante morte do helper; orçamento
interno não limita toda a entrada/restauro. Não houve teste de exaustão de memória.

GPG do commit, proveniência/assinatura de imagem, rotação de MASTER_KEY e licença
continuam pendentes. Nenhuma VPS/DNS/segredo do dono, push, merge, deploy ou Actions
remoto nesta execução. Dependabot #17/#18 e PRs documentais permanecem separados.
