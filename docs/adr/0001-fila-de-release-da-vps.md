# ADR 0001: falha de release de um projeto e a fila de releases da VPS

- **Status:** Aceito (opção B, decisão da coordenação em 2026-11-10)
- **Data:** 2026-11-10 (prazo da decisão: 2026-11-21)
- **Origem:** ação 5 do postmortem
  [`docs/postmortems/2026-11-10-release-261-variavel-obrigatoria.md`](../postmortems/2026-11-10-release-261-variavel-obrigatoria.md)
- **Decisores:** coordenação (dono da VPS compartilhada). Análise delegada ao StillOn; este ADR
  não altera código.
- **Afeta:** receptor compartilhado `receiver.py` 2.1.1 (protocolo 2), cópias em StillOn, Duora e
  GoblinShift; `deploy/coolify/release/ISOLATION.md`; ADR-0014 / INV-12 / T-12 do Duora.

## Contexto

### O problema

Em 2026-11-10 a release do StillOn `3003d54` falhou em `validate_runtime` (contêiner sem saúde).
O receptor deixou dois intents duráveis:

- o comum, `/var/lib/coolify-release/pending.json` (`{"project", "commit", "status": "importing"}`);
- o do projeto, `/var/lib/coolify-release/stillon/pending.json` (com `deployment_uuid`).

Enquanto existirem, `require_host_idle()` recusa **qualquer** release de qualquer projeto: ela
confere o intent comum e o `pending.json` de todo subdiretório de `STATE`. A fila de StillOn,
Duora e GoblinShift ficou parada até a reconciliação administrativa (ISOLATION.md
§Reconciliação), feita em cerca de 15 min.

Números da coordenação: 1 release com intents presos em cerca de 220 releases registradas
(62 StillOn, 157 Duora e GoblinShift). O risco está aceito até esta decisão.

### Como o receptor se comporta hoje (lido no código da `main`, idêntico à cópia revisada)

| Momento | O que o receptor grava ou exige |
|---|---|
| Entrada (`main`, ~905) | `flock` não bloqueante em `host.lock`, depois em `<projeto>/release.lock` e, no StillOn, em `/opt/stillon/.entrada.trava` (a mesma trava do backup). Quem perde a corrida é recusado, sem fila. |
| Admissão (`require_host_idle`, ~853) | Recusa se houver intent comum ou `pending.json` em qualquer diretório de projeto, inclusive de receptor antigo ou projeto desconhecido. |
| Antes do primeiro `docker load` (~920) | Grava o intent comum com `status: "importing"`. Esse status **nunca é atualizado** nas fases seguintes. |
| `deploy` (~767–795) | `require_no_pending` e `require_idle` do recurso; grava o intent do projeto (`deployment_uuid: null`); muda envs, manifest Duora ou allowlist GoblinShift; `POST /start`; regrava o intent do projeto com o UUID. |
| Espera e validação (~796–835) | Até 1200 s pelo estado terminal do deployment; depois saúde (até 120 s em `starting`), identidade da imagem e readiness. |
| Sucesso (~836–848, ~935) | `current.json`, histórico, remove o intent do projeto e por último o comum. |
| Qualquer exceção ou morte | Nenhum intent é removido (só o `incoming-*` temporário é apagado). As travas `flock` caem com o processo; os intents ficam. |

O backup do StillOn (`stillon-backup-entrada.sh`, linha 5) também recusa enquanto existir
`/var/lib/coolify-release/stillon/pending.json`.

### O que o ISOLATION.md fixa

- Linha 16: "Importação pode continuar no daemon após a morte do cliente; pendência de um projeto
  não bloqueava outro" → "Intent comum durável antes do primeiro import. **Qualquer falha mantém
  esse intent**; pendências antigas de qualquer diretório de projeto também bloqueiam o host."
- Linhas 41–43: o intent comum cobre resposta perdida de Docker ou Coolify, inclusive sem UUID; é
  conservador de propósito; timeout nunca autoriza apagar intent.
- §Reconciliação: operação exclusiva de administrador, sem endpoint remoto, sem limpeza global,
  sem bypass, sem retry automático.
- §Versionamento: qualquer mudança no receptor exige nova versão e hash, três PRs coordenados,
  homologação descartável e instalação administrativa.

### A hipótese da coordenação e a objeção do revisor

Hipótese: bloquear só o projeto que falhou e manter o intent comum como cerca.

Objeção: isso contraria a linha 16, porque (a) o intent comum existe justamente porque o
pendente de um projeto não bloqueava os outros e (b) um `docker load` pode seguir no daemon
depois que o cliente morre.

**Resposta de frente.** A objeção está certa contra a hipótese tal como escrita, por dois
motivos:

1. A hipótese é contraditória no estado atual do receptor. Se o intent comum continua existindo,
   `require_host_idle` continua bloqueando o host inteiro; para liberar os outros projetos alguém
   teria que remover ou ignorar o intent comum depois de uma falha, e é exatamente isso que a
   linha 16 proíbe ("qualquer falha mantém esse intent").
2. O intent comum de hoje não diz **em que fase** a release estava. O status gravado é sempre
   `importing`. Sem essa informação, nenhum leitor (receptor ou administrador sem investigação)
   consegue distinguir "o daemon pode ainda estar importando" de "tudo terminou e só o app ficou
   ruim". Na dúvida, o fail-closed exige bloquear o host. Portanto "bloquear só o projeto" não é
   implementável com segurança sem mudar o receptor.

Mas os dois motivos da linha 16 **não valem para toda falha**. Eles descrevem efeitos externos
que podem estar em andamento depois que o cliente perde o controle:

- importação no daemon após morte do cliente;
- start aceito pelo Coolify com resposta perdida;
- deployment ainda em andamento quando o receptor estoura o prazo de 1200 s.

Na falha de 10/11 nenhum deles estava presente. O `docker load` tinha retornado (é síncrono no
receptor e a identidade da imagem já tinha sido conferida), o deployment tinha chegado a estado
terminal (`deployment_finished`) e a exceção saiu da validação de saúde. O que sobrou foi estado
**do projeto**: um recurso com imagem nova e app em restart loop. Os riscos que justificam
bloquear o host (disco e daemon compartilhados ocupados por uma operação desconhecida, fila do
Coolify em estado incerto) não existiam.

Logo, a versão defensável da hipótese é a **cerca graduada por fase** (opção C abaixo): o intent
comum continua cercando o host sempre que algum efeito externo pode estar vivo, e só deixa de
cercar os outros projetos quando o próprio receptor provou, antes de falhar, que todos os efeitos
externos terminaram. Isso não é "bloquear só o projeto" em toda falha; é bloquear o host enquanto
houver incerteza. Mesmo assim, ela **muda o texto** da linha 16 e precisa da mesma revisão
conjunta que a criou. Não há leitura do ISOLATION.md atual que a autorize.

## Opções

Toda opção mantém: reconciliação administrativa para o projeto que falhou, nenhuma remoção ou
retry automático de release, `host.lock` serializando receptores participantes e fail-closed
diante de estado desconhecido.

### A. Manter como está (risco aceito)

Qualquer falha bloqueia o host até a reconciliação manual.

**Prós**
- Zero mudança, zero risco novo. É o desenho revisado em 21/10 e coberto pelos testes de
  isolamento (`test_host_isolation.py`: import morto, start com resposta perdida, deployment
  morto).
- Frequência baixa: cerca de 0,5% das releases. O bloqueio é seguro: protege Duora e
  GoblinShift de importar enquanto o host está em estado desconhecido.
- A reconciliação está documentada e foi executada em cerca de 15 min.

**Contras**
- O raio de impacto é a VPS inteira: uma falha do StillOn trava releases (inclusive correções
  urgentes) de Duora e GoblinShift.
- O tempo de bloqueio depende de quem pode fazer a reconciliação estar disponível. Fora do horário
  (noite, viagem), 15 min viram horas.
- A reconciliação é manual e sob pressão; cada passo feito à mão sob incidente é chance de erro
  (arquivar o intent errado, não conferir a fila do recurso).

**Corrupção e concorrência:** nenhum risco novo. O risco que resta é humano, na reconciliação
manual (ver B).

### B. Manter a semântica e encurtar a reconciliação (sem mudar o receptor)

O bloqueio continua de host inteiro. Muda o tempo até destravar, com três peças fora do receptor:

1. **Detecção:** uma peça nova, root-only e só de leitura (timer do systemd próprio), avisa quando
   existe `pending.json` (comum ou de projeto) há mais de N minutos sem `host.lock` ocupado, com o
   projeto e o commit. Não pode ser o `stillon-vigia.service`: ele roda com `DynamicUser=yes` e
   não lê `/var/lib/coolify-release` (root 0700), e dar esse acesso a ele aumentaria o privilégio
   do vigia. A ocupação do `host.lock` é lida sem tocar a trava (`/proc/locks` ou `lslocks`),
   nunca com `flock -n`: pegar a trava por um instante faria o receptor, que usa `LOCK_NB`,
   recusar uma release que chegasse nesse momento. Hoje quem nota é o vigia de `readyz` ou a
   sessão que acompanha o run.
2. **Diagnóstico somente leitura:** um script administrativo root-only, fora do SSH de CI, que
   executa os passos 1 a 3 da §Reconciliação sem alterar nada: lê os intents, a fase deduzível
   (intent do projeto com ou sem UUID), o estado do deployment no Coolify, processos `docker load`
   ativos, a fila de cada recurso, a saúde dos contêineres e a última linha relevante de
   `/var/log/coolify-release-errors.log`. Imprime um veredito: "efeitos externos terminados" ou
   "estado desconhecido, não reconciliar".
3. **Arquivamento confirmado:** o passo 4 (rename dos intents exatos para `audit/`, com fsync)
   vira um comando que exige o veredito "terminados", o commit e o projeto digitados pelo
   administrador e a posse de `host.lock`. Não roda sozinho, não tem endpoint e não apaga nada.

**Prós**
- Não toca o receptor, o protocolo nem a linha 16. Cabe nesta ação.
- Ataca a parte do custo que mais cresce (tempo até destravar fora do horário) e reduz erro
  humano no passo mais delicado.
- O script de diagnóstico também serve a qualquer falha futura e documenta a evidência que o
  passo 1 pede para guardar.

**Contras**
- O raio de impacto continua sendo a VPS inteira; só fica mais curto.
- Ainda depende de um administrador disponível.
- Mais uma peça administrativa para manter alinhada com o receptor nos três repositórios: se a
  fase deduzida pelo script divergir do que o receptor grava, o veredito engana.

**Corrupção e concorrência**
- O diagnóstico só lê: não há risco de escrita. O risco é de **falso "terminados"**, por exemplo
  um `docker load` órfão que o script não reconhece. Mitigação: o veredito padrão é "desconhecido";
  só vira "terminados" com prova positiva de cada efeito (deployment terminal pelo UUID, nenhum
  processo do daemon importando, fila do recurso ociosa); intent do projeto sem UUID nunca conta
  como terminado.
- O arquivamento roda sob `host.lock` (não bloqueante): não corre junto com um receptor.
- Não pode virar limpeza automática: nenhuma chamada por cron, vigia ou CI.

### C. Cerca graduada por fase (receptor 2.2, com revisão do ISOLATION.md)

Exige mudar o receptor numa ação futura; aqui só se descreve.

1. O intent comum passa a registrar a fase, com escrita atômica e fsync a cada transição:
   `importing` → `imported` → `configuring` → `started` (com UUID) → `terminal` (deployment em
   estado terminal observado) → `validating`.
2. Numa exceção **capturada** depois de `terminal`, o receptor não remove nada. Ele:
   - regrava o intent do projeto com `{"status": "quarantined", "phase", "deployment_uuid",
     "error_class"}` e faz fsync;
   - só depois move, por rename, o intent comum para `<projeto>/host-intent.json` (preservado como
     evidência) e faz fsync de `STATE` e do diretório do projeto.
3. `require_host_idle` passa a bloquear o host se existir intent comum (qualquer fase), ou intent
   de projeto em qualquer estado diferente de `quarantined`, ou qualquer arquivo ilegível ou
   desconhecido. Um intent `quarantined` bloqueia só o próprio projeto e o seu backup.
4. Morte do processo (SIGKILL, queda da VPS), timeout de 1200 s, exceção antes de `terminal`,
   resposta perdida do `/start` ou falha na própria quarentena deixam o intent comum onde está:
   o host fica bloqueado exatamente como hoje.
5. A reconciliação do projeto em quarentena continua administrativa e igual à §Reconciliação
   (passos 1 a 5), incluindo arquivar os dois arquivos.

A falha de 10/11 cairia no caso 2: só o StillOn ficaria bloqueado.

**Prós**
- Responde à objeção do revisor: o host continua cercado em todos os cenários que motivaram a
  linha 16 (import após morte do cliente, resposta perdida, deployment em curso). Só muda o caso
  em que o próprio receptor observou, antes de falhar, que nenhum efeito externo continua.
- Reduz o raio de impacto do caso mais provável: app que não sobe por configuração, como em
  10/11. Os outros projetos seguem publicando correções.
- Fail-closed preservado: tudo que não for prova positiva de "terminal" bloqueia o host.

**Contras**
- Muda o texto da linha 16 e das linhas 41–43 do ISOLATION.md e a INV-12 do Duora. Exige a
  mesma revisão conjunta de 21/10, três PRs coordenados, nova versão e hash, homologação
  descartável com Docker e Coolify reais e instalação administrativa com ingresso congelado.
  Para um problema de 1 em 220, esse custo é alto.
- Mais estados no receptor e mais testes (cada fase com SIGKILL, falha durante a quarentena,
  leitura de arquivo corrompido, downgrade).
- O benefício real é limitado: na VPS compartilhada a falha que mais importa liberar é rara, e a
  reconciliação continua manual para o projeto que falhou.

**Corrupção e concorrência**
- **Falso terminal:** se `deployment_finished` aceitar um status que o Coolify ainda processa,
  a quarentena libera o host com deployment vivo. Mitigação: a fase `terminal` só é gravada
  depois que `deployment_finished` devolve verdadeiro, e ele só aceita `finished`; os status
  terminais de `require_idle` (que incluem falhas) não servem para liberar. Qualquer outro
  status, ou erro de rede ao consultar, mantém o host bloqueado.
- **Crash no meio da quarentena:** a ordem (projeto `quarantined` primeiro, rename do comum
  depois) faz qualquer interrupção cair num estado em que os dois arquivos existem, e o comum
  bloqueia o host. Nunca há janela com o host liberado e o projeto sem marca.
- **Downgrade do receptor:** como a marca continua no nome `pending.json` do projeto, um receptor
  2.1.1 reinstalado bloqueia o host inteiro ao vê-la (fail-closed). Se a marca usasse um nome
  novo (`quarantine.json`), o 2.1.1 não a veria e liberaria **o próprio projeto que falhou**:
  falha aberta. O nome precisa continuar `pending.json`.
- **Recursos compartilhados durante a quarentena:** o app em restart loop consome CPU e memória;
  as imagens importadas do projeto em quarentena ocupam disco. O preflight de storage mede espaço
  livre real a cada release, então o disco já usado entra na conta; CPU e memória não são
  orçados por nenhuma opção (limite já registrado no ISOLATION.md).
- **Reconciliação concorrente:** o administrador reconcilia o projeto em quarentena enquanto
  outro projeto publica. Ambos exigem `host.lock`; o administrador espera a release terminar.
  Sem mudança.
- **Backup do StillOn:** continua recusando enquanto o `pending.json` do StillOn existir, como
  hoje. Correto: o recurso está em estado não reconciliado.

### D. Escopo por projeto sem fase (hipótese literal)

O intent comum só existe durante a importação (ou é removido em qualquer falha capturada); depois
disso, a pendência de um projeto bloqueia só ele.

**Prós**
- A mais simples de implementar entre as que mudam o receptor.
- Libera os outros projetos em quase toda falha.

**Contras**
- É o estado anterior a 21/10 que a revisão conjunta corrigiu. Contraria as linhas 16 e 41–43
  sem resposta para os seus motivos.
- Remover intent em falha é limpeza automática, proibida pela §Reconciliação.

**Corrupção e concorrência**
- **Start com resposta perdida** (sem UUID): o Coolify pode estar implantando o projeto A
  enquanto B importa e implanta, disputando o disco que o preflight de B mediu antes e a fila de
  builds do servidor.
- **Timeout de 1200 s:** deployment de A ainda vivo com B admitido; o administrador que depois
  reconcilia A vê um host que mudou sob a investigação.
- **Import órfão** fora da janela do intent comum (por exemplo, se a remoção acontecer antes de
  confirmar o fim do `docker load`): duas expansões de camadas simultâneas furam o orçamento de
  16 GiB e os 36 GiB de folga, que valem por release.

Rejeitada: troca integridade por disponibilidade de publicação, que é o contrário do
que o ISOLATION.md e as regras do projeto pedem.

## Comparação

| | A. Como está | B. Reconciliação curta | C. Cerca por fase | D. Por projeto |
|---|---|---|---|---|
| Muda o receptor | Não | Não | Sim (2.2) | Sim |
| Revisa ISOLATION.md linha 16 | Não | Não | Sim | Sim, contra o motivo |
| Raio de impacto da falha de 10/11 | VPS | VPS, por menos tempo | Só StillOn | Só StillOn |
| Raio com efeito externo incerto | VPS | VPS | VPS | Só o projeto (inseguro) |
| Risco novo de corrupção | Nenhum | Falso veredito (lido, não executado sozinho) | Falso terminal, downgrade | Alto |
| Custo | Zero | Baixo (scripts admin e alerta) | Alto (3 PRs, homologação, instalação) | Médio, mais o retrabalho de reverter a revisão |

## Decisão

**Decidido pela coordenação em 2026-11-10:** adotar **B** agora e registrar **C** como evolução
condicionada; rejeitar **D**.

- B cabe na restrição desta ação (receptor intacto), mantém a linha 16 ao pé da letra e ataca o
  custo real observado: o tempo até destravar e o erro humano sob incidente.
- C só se paga se a falha deixar de ser rara. Gatilho proposto para abrir C: uma segunda falha com
  intents presos em 90 dias, ou um bloqueio que passe de 1 h, ou uma correção urgente de Duora ou
  GoblinShift atrasada por pendência de outro projeto. Abrir C significa propor a revisão conjunta
  do ISOLATION.md, não implementar direto.
- D fica registrada como rejeitada, para a hipótese não voltar sem fases.

**Bottom line:** decidido B: encurtar a reconciliação com alerta, diagnóstico somente leitura e
arquivamento confirmado, sem mudar o receptor. Motivo: 1 falha em cerca de 220 releases não paga
a revisão conjunta e a reinstalação que a cerca por fase (C) exige, e só C libera os outros
projetos sem violar o motivo da linha 16. Custo principal: a VPS inteira continua bloqueada a
cada falha até um administrador agir; B encurta esse tempo, mas não o elimina.

## Consequências

- Com B: o risco aceito do postmortem continua valendo, agora com tempo de detecção
  e de reconciliação menores. O ISOLATION.md ganha só uma referência ao diagnóstico e ao
  arquivamento confirmado na §Reconciliação, sem mudar regras.
- As três cópias do diretório de release (StillOn, Duora, GoblinShift) precisam receber os
  scripts administrativos juntas, ou eles ficam só no repositório administrativo da VPS; isso é
  decisão da coordenação.
- Se o gatilho de C disparar: abrir proposta de revisão do ISOLATION.md e da INV-12 do Duora,
  com os testes de C (SIGKILL por fase, crash entre a marca do projeto e o rename do comum,
  arquivo ilegível, downgrade para 2.1.1) escritos antes do código.
- Sem relação com esta decisão, mas notado na leitura: o README do release no StillOn ainda cita o
  receptor 2.0.2 e um hash antigo, enquanto `receiver-release.json` diz 2.1.1. Vale um PR de
  documentação separado.

## Compliance

- **B, diagnóstico:** teste em matriz de condições com intents sintéticos (todo intent comum do
  2.1.1 vem sem fase, então a fase vem só do intent do projeto). Eixos: intent do projeto ausente,
  sem UUID ou com UUID; deployment `finished`, em andamento, falho ou ilegível (erro de rede);
  `docker load` ativo ou não; `host.lock` ocupado ou livre. Só devolve "terminados" quando há
  UUID, deployment `finished`, nenhum `docker load` ativo e `host.lock` livre; qualquer outra
  combinação devolve "estado desconhecido, não reconciliar".
- **B, só leitura:** teste que compara a árvore de arquivos (nomes, tamanhos, mtimes) antes e
  depois do diagnóstico e da detecção, e prova que nenhum dos dois pega o `host.lock` (um
  `flock -n` paralelo continua conseguindo a trava durante a execução).
- **B, detecção:** teste que avisa com intent há mais de N minutos e `host.lock` livre, e não
  avisa com o `host.lock` ocupado nem com intent mais novo que N minutos.
- **B, arquivamento:** teste que recusa sem `host.lock`, sem veredito "terminados" ou com projeto
  e commit que não batem, e que confere o rename para `audit/` sem apagar arquivo. Nenhum cron,
  timer do systemd, vigia ou workflow chama o arquivamento (conferível por busca no repositório,
  na crontab e em `systemctl list-timers` da VPS).
- **Todas as opções:** os testes atuais de `test_host_isolation.py` continuam passando sem
  alteração; `verify_artifact.py` confirma os mesmos bytes do receptor nos três repositórios.
- **Se C for adotada no futuro:** um teste por fase prova que SIGKILL deixa o host bloqueado e um
  teste prova que só a exceção capturada depois de `terminal` libera os outros projetos.
