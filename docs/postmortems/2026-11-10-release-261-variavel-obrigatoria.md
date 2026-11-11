# Postmortem: queda na release do #261 (2026-11-10)

Sem culpados: o texto descreve o sistema e o roteiro,
não quem executou cada passo.
Horários em BRT (UTC-3).

## Resumo

O merge do #261 (Task 10, fail-closed de `PROBE_BLOCKED_CIDRS`)
publicou uma imagem que exige essa variável em produção.
O release automático trocou só a imagem.
O recurso do Coolify continuou com o `app.json`
do commit de infraestrutura anterior (`ad26152`),
que não passa a variável ao contêiner.
O app entrou em restart loop e o site ficou fora do ar
por cerca de 13 minutos, até o Passo 7 do roteiro
atualizar o commit de infraestrutura.

## Impacto

- `stillon.tech` fora do ar das 22:03 às 22:16
  (`readyz` 502), cerca de 13 min.
  O sondador também parou: nenhuma checagem
  nem aviso aos usuários nesse intervalo.
- Nenhum dado perdido e nenhuma migration envolvida.
- A fila de releases da VPS inteira
  (StillOn, Duora e GoblinShift) ficou travada:
  o receptor deixou o `pending.json` comum e o do projeto,
  e recusa qualquer release até a reconciliação administrativa.
  A reconciliação foi feita às 22:19 (ação 4).

## Linha do tempo

| Hora  | Evento |
|-------|--------|
| 21:48 | Merge do #261, SHA `3003d5480c3b3a996ac06dcad5e50d936f3a663a`. |
| 22:02 | CI da `main` verde (run 42934260282). |
| 22:02 | `Coolify release` (run 42935987507) começa e troca a imagem para `release-3003d548`. O recurso segue com o `app.json` de `ad26152`. |
| 22:03 | O binário novo recusa subir: "variável obrigatória ausente em produção: PROBE_BLOCKED_CIDRS". Restart loop; `readyz` 502. |
| 22:03 | O receptor falha em `validate_runtime` ("Application container is not healthy"). O guarda local da release vê o run em failure. Ficam os dois `pending.json`. |
| 22:06 | Aviso à coordenação. |
| 22:16 | A coordenação faz o Passo 7 pela API do Coolify: `git_commit_sha` vai para `3003d54…`, deploy `hhrzkkxdgxlbgrx9irz4xtzp` termina `finished`. `readyz` 200, app `healthy`, 0 restarts, sem o aviso de lista vazia no boot. |
| 22:19 | Reconciliação administrativa da fila: `current.json` vai para `3003d54…`, os dois `pending.json` são arquivados por rename em `/var/lib/coolify-release/audit/2026-11-11-stillon-3003d54/` (a data do diretório está em UTC). Fila liberada. |

## Causa raiz

A ordem do roteiro foi o gatilho.
A causa de fundo é que a imagem e o commit de infraestrutura
são publicados por caminhos independentes,
sem nenhuma checagem de compatibilidade entre os dois.
O `task10-4-passo-do-dono.md` e o plano da Task 10
mandavam publicar a imagem (merge e release automático)
**antes** do Passo 7, que atualiza o commit de infraestrutura.

O release automático só troca a imagem.
O `app.json`, que decide quais variáveis chegam ao contêiner,
fica preso ao commit de infraestrutura escolhido no recurso.
A variável já estava cadastrada no Coolify
(o Passo 0.3 conferiu esse cadastro), mas o `app.json` antigo não a listava,
então ela nunca chegou ao processo.

O plano assumiu que a exigência nova estaria
só no `${PROBE_BLOCKED_CIDRS:?…}` do manifesto,
que entra junto com o Passo 7.
Mas o próprio binário também falha fechado
(`config.Load` recusa a variável vazia em produção).
O plano descreveu esse cenário como "Caso A" do rollback,
mas o tratou como "recurso sem a variável",
e o Passo 0.3 conferia o recurso, não o manifesto em uso.

Regra geral que faltou:
qualquer código que passe a **exigir** uma variável
que só o `app.json` novo injeta derruba o app
se a imagem chegar antes do commit de infraestrutura.

## O que funcionou

- O guarda do receptor (`validate_runtime`) reprovou
  o contêiner em cerca de 1 min e não marcou a release como boa:
  sem `current.json` novo, sem etiqueta de SHA.
- O alerta chegou rápido: run vermelho às 22:03,
  `readyz` 502 visto de fora, aviso à coordenação às 22:06.
- O caminho de volta estava pronto:
  a variável já estava no recurso e no Bitwarden,
  e o Passo 7 resolveu numa só ação, sem trocar imagem
  e sem mexer no banco.
- O fail-closed fez o que devia: sem a lista,
  o processo não subiu sondando os vizinhos da VPS.
  Falhou alto, não em silêncio.

## O que não funcionou

- A ordem do roteiro tinha a imagem antes da infraestrutura,
  e nenhuma checagem comparava as duas.
- O Passo 0.3 conferiu o lugar errado para este risco:
  o cadastro da variável no recurso do Coolify,
  e não se o `app.json` em uso a repassava.
- O CI não sabe qual commit de infraestrutura
  está fixado no recurso, então não tinha como barrar.
- A falha travou a fila da VPS inteira,
  e a reconciliação depende de um passo administrativo manual.

## Achado posterior: cancelamento que não impediu a publicação (release do #262)

Às 22:58 o guarda local da sessão do StillOn cancelou a release do #262
(run 42942692734, tentativa 1), registrando "deploy não iniciado",
porque havia outra release ativa na VPS.
O job `deploy` tinha começado às 22:58:27 e já estava no passo de publicação.
O cancelamento parou o runner, mas não o receptor:
o host publicou `fc1e772` às 22:59 (`current.json` gravado, deployment terminado, `readyz` 200).
O efeito foi o contrário do que o guarda registrou,
e por sorte a publicação era a esperada.
A reexecução das 23:13 foi recusada na entrada (`BlockingIOError`, `host.lock` ocupado)
sem gravar estado.

Causa, confirmada no log do guarda: ele lia "status do run" e "status do deploy"
separados por espaço e zerava o segundo quando os dois eram iguais.
Com os dois `in_progress`, o deploy ficou vazio, e vazio era tratado como "não iniciado".
Corrigido às 23:20: campos separados por `|`, e o guarda só cancela
com o job `deploy` explicitamente `queued`, `waiting` ou `pending`;
ausente na resposta conta como desconhecido e não cancela.

Lição geral: cancelar o run depois que o passo de publicação começou
não desfaz nada no host. Um guarda só pode cancelar antes desse passo,
e "não sei" nunca vale como "não começou".

## Ações

| # | Ação | Dono | Prazo |
|---|------|------|-------|
| 1 | Regra de publicação: mudança que exige variável nova segue expand/contract. Primeiro o código tolerante (só aviso), depois a infraestrutura (Passo 7), só então o código que exige. Alternativa aceita: o Passo 7 antes da release. Registrar no `deploy/coolify/release/README.md`. | Claude (sessão de código) | 2026-11-14 |
| 2 | Check no CI: um teste compara as variáveis obrigatórias em produção declaradas em `internal/config` com as que o `app.json` do commit de infraestrutura fixado entrega, e falha se o código exige algo que a infraestrutura ainda não passa. Precisa de uma fonte versionada do SHA fixado. Alternativa que dispensa essa fonte: a própria release compara as variáveis exigidas pela imagem com as que o recurso entrega e recusa antes de trocar a imagem. Só a ideia aqui; não implementado neste PR. | Claude (sessão de código) | 2026-11-21 |
| 3 | Revisar os roteiros de passo do dono para a ordem da ação 1 (começando pelo `task10-4-passo-do-dono.md` e pelo plano da Task 10), com o Passo 7 antes do merge quando o código exigir variável nova, e corrigir o Caso A do rollback, que ainda diz que a queda só acontece se a variável sumir do recurso. | Coordenação | 2026-11-14 |
| 4 | Reconciliar os dois `pending.json` pelo `ISOLATION.md` e destravar a fila da VPS. | Coordenação (administrativo) | Concluída em 2026-11-10 às 22:19; intents arquivados em `/var/lib/coolify-release/audit/2026-11-11-stillon-3003d54/` (data em UTC). |
| 5 | Análise em formato ADR, com opções e trade-offs, de como uma release falha de um projeto deixa de travar a fila da VPS inteira, sem mudar o receptor nesta ação. O `ISOLATION.md` proíbe limpeza automática e bypass: toda opção mantém a reconciliação administrativa e o fail-closed. Hipótese inicial: bloquear só o projeto que falhou, com o intent comum como cerca. Até a decisão, o risco fica aceito: falha rara (1 release com intents presos em ~220 releases registradas no histórico do host: 62 StillOn, 157 Duora+GoblinShift); o bloqueio é fail-closed e protege as outras apps; a reconciliação está documentada (`ISOLATION.md` §Reconciliação) e foi executada em ~15 min em 10/11; a vigia de `readyz` e os guardas das sessões detectam a fila parada. Trocar isso por limpeza automática violaria o `ISOLATION.md`. | Coordenação (análise delegada ao StillOn) | 2026-11-21 |
| 6 | Guardas de release das sessões: cancelar só com prova de que o passo de publicação não começou (job `deploy` explicitamente na fila, ou o passo anterior à publicação ainda sem conclusão); estado ausente ou ilegível nunca cancela. Corrigido no guarda do StillOn em 10/11; conferir os guardas das outras sessões que cancelam runs (o do Duora cancela quando o status do `deploy` vem vazio) e registrar a regra junto do `deploy/coolify/release/README.md`. | Claude (sessão do StillOn); guardas das outras sessões com a Coordenação | 2026-11-14 |
