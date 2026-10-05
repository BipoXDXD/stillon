# Snapshot coerente do backup: plano da próxima onda

> **Revisado na retomada de 2026-10-04:** o
> [plano da segunda onda](2026-10-04-backup-supervisionado-segunda-onda.md)
> substitui a proposta POSIX/FIFO abaixo por supervisão Go com clientes libpq.
> Este documento preserva a investigação da primeira onda; não executar seus
> fragmentos como implementação corrente. O checkpoint da primeira onda permanece intacto.

> **Estado:** proposta técnica local para revisão. Não implementada, não implantada e não aprovada pelo dono. A onda atual está congelada. A execução deste desenho permanece bloqueada por prontidão técnica do lifecycle e definição de limites operacionais; não há novo pedido de permissão para rotinas locais já autorizadas. Nenhuma ação na VPS/GitHub faz parte desta proposta.
>
> **Para a execução futura:** quando os bloqueadores técnicos deste plano forem resolvidos e o escopo da onda seguinte estiver definido, aplicar `superpowers:executing-plans`, TDD e verificação antes de conclusão, tarefa por tarefa. Passos usam caixas de seleção. A revisão independente deste plano já foi atribuída pelo coordenador; não iniciar novos agentes nem implementar scripts de produção nesta onda.

**Objetivo:** fazer versão Goose, contagens de usuários/alvos e `pg_dump` usarem o mesmo snapshot, evitando rejeitar um dump correto após exclusão concorrente, sem enfraquecer a verificação da restauração.

**Arquitetura:** um supervisor em shell POSIX inicia um `psql` persistente em `REPEATABLE READ READ ONLY` e o `pg_dump` como filhos diretos. Um FIFO privado transporta somente comandos SQL fixos; um manifesto privado transporta snapshot e contagens. O exportador fica aberto até o dump terminar e é encerrado/aguardado antes do restauro.

**Stack:** shell POSIX executado por `sh`, ferramentas já presentes no PostgreSQL 18 Alpine fixado pelo projeto, clientes `psql`/`pg_dump`/`pg_restore` dessa imagem; Go/testcontainers existentes para os testes. Nenhuma nova dependência de pacote, npm, Go ou imagem.

**Spec e evidência:** [auditoria comprovada de snapshot](../../../.superpowers/auditoria-backup-snapshot-2026-10-04.md); [operação e livros consultados](../../../.superpowers/operacao-2026-10-04.md); [runbook de produção](../../deploy/vps.md). Os relatórios em `.superpowers/` são evidência local e podem não acompanhar um clone novo: o resumo causal e os cenários necessários estão reproduzidos abaixo.

## Requisitos e restrições globais

- Português do Brasil, nome atual StillOn, alterações de main somente por PR.
- Não executar VPS, GitHub, age com chave privada real ou qualquer comando sobre volumes do dono. Toda prova usa dados públicos sintéticos e contêineres próprios.
- Preservar **3 GiB** de memória e **2 GiB** de tmpfs do restauro em `stillon-entrada`. Não aumentar limites, trocar para disco persistente nem contornar OOM para fazer o teste passar.
- Preservar papel `stillon_backup` com BYPASSRLS, owners do dump, FORCE RLS, schema Goose, partições e regra de usuários mínimos. Não usar `--no-owner`, `--no-policies` ou desativar checks.
- Preservar código 0 para cópia conferida; 3 para falha da conferência; 4 para cópia conferida com aviso de disco ≥85%, que ainda deve ser cifrada/guardada. Falha de criação do snapshot/dump/encerramento é ausência de backup, nunca 0/4.
- Preservar parsing e percent-decoding de credenciais já endurecidos. Senhas somente pelo ambiente, nunca URI secreta, `-v` do psql, argv, manifesto, SQL gerado ou log. PGPASSWORD do restauro não pode ser substituída pela da origem.
- Não adicionar paralelismo de dump: formato custom e um cliente de cópia. Não modificar cron, concorrência de workflow, sudoers, entrada SSH, imagem/digest ou deploy para implementar este escopo.
- Manter `>=` nas contagens nesta onda. Snapshot alinhado torna a referência correta; igualdade é fortalecimento separado, sujeito a revisão. Não tolerar exclusões relaxando para `<=`, zero, delta percentual ou skip.
- Novo teste só é evidência após RED observado, sabotagem compilável/executável da propriedade, restauração e GREEN. Mutantes somente em cópia isolada, sem alterar checkout concorrente.

## Problema e alcance comprovados

Hoje `backup.sh` executa três `psql` independentes e depois `pg_dump`. No ensaio, origem contada com dois alvos foi reduzida a um antes do dump; arquivo e restauro tinham corretamente um alvo, mas o verificador recusou com 3. A entrada de produção não cifraria/enviaria essa cópia. É falso negativo real e falha de disponibilidade do backup, prioridade média.

Com inserção no mesmo intervalo, três linhas no dump passam contra referência antiga de duas. A remoção deliberada de uma linha **após** o pg_restore real pode ficar mascarada: duas restantes ainda passam. Esse ensaio prova fraqueza da referência antiga, não corrupção causada por PostgreSQL nem arquivo inválido. O snapshot compartilhado já foi comprovado em `/tmp`: contagem=2; exclusão confirmou; origem atual=1; dump/restauro=2; `verificar.sql` original saiu 0.

O plano corrige a relação entre referência e cópia. Não promete identidade completa de todas as linhas, atomicidade entre backup e mudanças posteriores, PITR, propriedades de sequências fora de MVCC ou reparação de uma origem já inválida.

## Foco da revisão

1. Exportador morre antes/durante o handshake: nenhuma abertura/leitura/escrita de FIFO pode ficar esperando sem limite; nenhum dump começa sem manifesto completo.
2. Dump termina com erro ou exportador vence enquanto ele trabalha: nenhum manifesto ou arquivo parcial deve permitir 0/4; ambos os processos e sessões precisam terminar.
3. TERM/INT/PIPE entre fork, atribuição de PID, COMMIT e wait: cleanup precisa ser idempotente, limitado e restrito aos processos/diretório desta execução.
4. Credenciais escapadas e PG* do restauro: fonte e restauro continuam com senhas distintas; aliases ou query params não podem reintroduzir segredo em argv.
5. Escritas/DDL concorrentes e pressão de recursos: CRUD normal continua possível; lock exclusivo falha em prazo limitado; MVCC retém versões somente durante a janela necessária, sem ampliar 3 GiB/2 GiB.

## Mapa de arquivos da futura implementação

| Arquivo | Responsabilidade | Mudança futura |
|---|---|---|
| `deploy/backup/snapshot.sh` | Supervisor POSIX do exportador/dump, handshake, prazos, sinais e cleanup | Criar |
| `deploy/backup/exportar.sql` | Transação read only, exportação, contagens e publicação do manifesto | Criar |
| `deploy/backup/backup.sh` | Parser existente, chamada ao supervisor, restauro, verificações e disco | Alterar somente bloco origem/dump |
| `deploy/backup/verificar.sql` | Verificação existente; comentário sobre referência agora coerente | Apenas ajustar comentário, sem relaxar condições |
| `internal/config/backup_snapshot_test.go` | Testes reais e determinísticos, recursos e ciclo de vida | Criar usando dependências existentes |
| `internal/config/scripts_test.go` | Testes atuais de argv/credenciais | Adaptar falso psql ao protocolo, preservando asserções |
| `deploy/ci/checar-vps.sh` | Distribuição dos quatro arquivos na fixture de produção local | Ajustar somente cópia dos auxiliares; nenhum workflow |
| `docs/deploy/vps.md` | Distribuição scp/install/Confira/tabela e diagnóstico de timeout | Alterar depois dos gates técnicos |

`stillon-entrada`, workflows e compose entram como leitura e asserções de compatibilidade, não arquivos de implementação. A entrada monta a árvore instalada `deploy` inteira, mas isso **não distribui** auxiliares novos: `deploy/ci/checar-vps.sh:830` copia hoje apenas backup.sh/verificar.sql, e o runbook entrega os mesmos dois. A próxima onda deve copiar também snapshot.sh/exportar.sql na fixture e nos comandos scp/install/Confira/tabela de atualização; sem esse ajuste o backup real falha por arquivo ausente. Não alterar a entrada nem workflow por conveniência.

## Contratos de transporte e sucesso

`snapshot.sh` será executado como `sh "$AQUI/snapshot.sh"`. Consome `SAIDA`, `STILLON_ORIGEM_PUBLICA` (URI já sem senha) e `PGPASSWORD` da origem, passados por assignments de ambiente no processo filho. O supervisor recebe os budgets públicos descritos abaixo por parâmetros internos de suas funções; testes podem encurtá-los em cópia, sem criar variável de produção que desligue o prazo.

Produz no stdout **uma única linha** somente após sucesso completo:

```text
snapshot|versao|usuarios|alvos
```

Exemplo público de teste: `0000002C-00000002-1|1|1|2`. O restante da comunicação operacional vai para stderr, mantendo rótulos `== origem`, `schema …`, `== dump`. `backup.sh` lê quatro campos, não executa o manifesto com `.` ou `eval`, e continua as etapas existentes. `snapshot` é metadado público para o teste e será descartado depois de extrair as três referências.

Manifesto interno: diretório exclusivo `mktemp -d` sob `umask 077`, FIFO modo 0600, arquivo regular `meta.parcial` 0600 e publicação para `meta.pronta` por rename no mesmo diretório. Nenhum caminho vem do URI. O parser exige uma linha, quatro campos, três inteiros decimais não negativos, snapshot não vazio e sem espaços/controle, e nenhum quinto campo. Validar o ID contra o formato hexadecimal/hífens dos clientes PG18, documentando a dependência de versão; valor desconhecido causa falha segura.

`meta.pronta` sozinho não significa backup aceito: o supervisor exige dump exit 0, COMMIT enviado, EOF fechado e **wait do exportador exit 0**. Se a sessão exportadora acabar após importação, o dump pode ainda terminar usando seu snapshot importado; neste desenho conservador, a morte prematura do exportador continua fazendo o supervisor reprovar. Nenhum resultado parcial chega ao restauro ou à cifra.

Chamada conceitual, exclusivamente para o código futuro:

```sh
if meta=$(PGPASSWORD=$origem_senha \
    STILLON_ORIGEM_PUBLICA=$origem_url_publica \
    SAIDA=$SAIDA sh "$AQUI/snapshot.sh"); then
    :
else
    snap_status=$?
    case "$snap_status" in
        130 | 143) exit "$snap_status" ;;
        *) exit 1 ;;
    esac
fi
# Ler campos de $meta como dados e validar; nunca eval/source.
# PGPASSWORD do shell pai continua sendo a do restauro.
```

Falha interna do exportador, incluindo psql exit 2/3, vira ausência de backup (1) nessa etapa; o 3 reservado à cópia inválida continua vindo de `verificar.sql`. Preservar sinais externos 130/143 nos scripts locais; a entrada já trata códigos fora de 0/3/4 como ausência de backup. Não deixar um código 3 de contagem/exportação ser apresentado como prova de dump inválido.

## Ciclo de vida POSIX

| Estado | Ação | Condição para avançar |
|---|---|---|
| PREPARAR | Diretório/FIFO, handlers de sinais e guardião de prazo antes de operações bloqueantes | Recursos exclusivos criados; guardião vivo |
| EXPORTAR | Filho psql abre FIFO para leitura; pai abre descritor 3 para escrita e envia SQL fixo | Manifesto publicado e validado dentro do limite |
| COPIAR | Iniciar pg_dump como outro filho direto, com `--snapshot` | Processo termina 0 dentro do budget |
| ENCERRAR | Escrever COMMIT e `\q`, fechar descritor 3, wait exportador | Exportador termina 0; nenhum filho/sessão retido |
| CONCLUIR | Parar/reap guardião, apagar IPC, imprimir manifesto validado | Somente então retornar 0 ao backup |
| ABORTAR | Bloquear sucesso, fechar IPC, TERM/limite/KILL/reap dos filhos, remover temporários | Nenhum resultado entregue ao chamador |

Fragmento para orientar a implementação, sem constituir código pronto:

```sh
# Há somente um escritor persistente do FIFO; não usar <> (não portátil).
PGPASSWORD=$PGPASSWORD PGCONNECT_TIMEOUT=10 \
    PGAPPNAME=stillon-snapshot-exportador \
    psql --no-psqlrc -X -Atq -v ON_ERROR_STOP=1 \
    "$STILLON_ORIGEM_PUBLICA" <"$snap_dir/comandos" &
snap_exportador_pid=$!
exec 3>"$snap_dir/comandos"
cat "$AQUI/exportar.sql" >&3
# Esperar meta.pronta com budget e detecção de término/erro do exportador.
# Só depois de validado, iniciar/aguardar o filho pg_dump.
PGPASSWORD=$PGPASSWORD PGCONNECT_TIMEOUT=10 \
    PGAPPNAME=stillon-snapshot-dump \
    pg_dump --format=custom --no-password --snapshot="$snap_id" \
    --lock-wait-timeout=10000 --file="$SAIDA" \
    "$STILLON_ORIGEM_PUBLICA" 3>&- &
snap_dump_pid=$!
# wait precisa ser protegido de set -e para guardar a causa da falha.
if wait "$snap_dump_pid"; then
    snap_dump_pid=
else
    snap_status=$?
    # Falha: ir para ABORTAR, nunca continuar ao restauro.
fi
printf 'COMMIT;\n\\q\n' >&3
exec 3>&-
# Capturar wait exportador e exigir zero antes do resultado ao chamador.
```

Os comentários representam pontos do algoritmo detalhado neste plano, não funções implícitas já existentes. O guardião e os PGOPTIONS dos budgets ainda precisam ser implementados e provados; copiar esse fragmento isolado para produção é incorreto.

Regras obrigatórias do supervisor:

- Guardião de prazo começa **antes** do fork/open de FIFO. Deve interromper também pai preso em redirecionamento ou escrita; não depender apenas de `kill -0`/polling depois que a operação já bloqueou.
- `psql` e `pg_dump` são filhos diretos, com PIDs privados capturados imediatamente. Não executar pg_dump por `psql \!`, pipeline que perde status, `sh -c` com segredo interpolado, ou processo destacado sem wait. A alternativa `\!` simplifica o SQL, mas complica cancelamento e propriedade da árvore de processos; não foi escolhida.
- Comandos de criação/COMMIT são SQL constante. O EOF do escritor é controlado: não fechar descritor 3 após enviar apenas as contagens, pois psql terminaria a transação e invalidaria a exportação.
- Espera de manifesto termina por pronto, erro/término do filho ou budget. Arquivo regular permite detectar estado sem bloqueio de `read` em FIFO de respostas; se o filho falhar antes do manifesto, o guardião impede espera indefinida.
- Traps POSIX `0`, HUP, INT, TERM e PIPE registram a causa uma vez; desarmar reentrada durante cleanup. Não tentar escrever ROLLBACK no FIFO durante abort: o leitor pode ter morrido. Fechar FD e encerrar o psql provoca rollback da transação read only.
- Na falha, enviar TERM apenas aos PIDs ainda pertencentes a esta execução; escalada KILL após 5 segundos para processos que não terminarem; wait de todos os filhos e do guardião. Não usar `pkill psql`, `kill 0`, listas globais Docker ou cleanup por prefixo compartilhado.
- Fechar o descritor 3 em pg_dump/guardião/timers, para que apenas o supervisor possua o escritor do FIFO. Incluir esse cuidado na prova de EOF e encerramento.
- O guardião usa somente utilitários já existentes (`sleep`, `kill`, `wait` e shell). Se tiver timer filho, também deve encerrá-lo/aguardá-lo no cancelamento. Não presumir que matar um shell mata seus netos ou que `timeout` BusyBox equivale ao GNU.
- Revisar a janela entre fork/PID e sinais e evitar ação sobre PID já recolhido/reutilizado. Normalizar status interrompido de wait sem trocar uma falha real por sucesso. Provar cleanup quando o processo ignora TERM e quando FIFO ainda não está aberto.
- Em sucesso, reap/parar guardião sem deixar `sleep` órfão; somente então remover diretório. Em erro, apagar SAIDA parcial; saída do supervisor nunca contém manifesto de sucesso. A entrada preserva seu cleanup adicional de dump/contêiner.

### Conteúdo requerido de exportar.sql

A sessão recebe os timeouts via PGOPTIONS exclusivo da origem, não por ALTER ROLE/ALTER SYSTEM. A transação e a projeção de referência são:

```sql
\set ON_ERROR_STOP on
BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;
SELECT pg_export_snapshot() AS snapshot,
       (SELECT max(version_id) FROM goose_db_version) AS versao,
       (SELECT count(*) FROM users) AS usuarios,
       (SELECT count(*) FROM targets) AS alvos
\gset
```

Se `versao` for NULL, `\gset` não a define: a publicação deve ser recusada explicitamente, não imprimir uma variável literal e seguir. Importar com `pg_dump --snapshot` corresponde à mesma visão; não emitir `SET TRANSACTION SNAPSHOT` depois de queries no cliente importador.

Depois de validar variáveis definidas, o SQL lê os caminhos públicos `STILLON_META_PARCIAL`/`STILLON_META_PRONTA` com `\getenv`, escreve só a linha de quatro campos em `meta.parcial` por `\o` + `\qecho`, fecha o output com `\o` e publica por comando **fixo**:

```sql
\! mv "$STILLON_META_PARCIAL" "$STILLON_META_PRONTA"
\if :SHELL_ERROR
    SELECT 1 / 0;
\endif
```

O erro deliberado nessa ramificação converte a falha de publicação em exit não zero por ON_ERROR_STOP; nenhum dado externo vira SQL. `\!` não interpola variáveis do psql, por isso caminhos passam pelo ambiente e expansão com aspas do shell. Não usar `\q 1`: psql18 não oferece esse contrato de exitcode. O exportador não contém COMMIT nem `\q` até o supervisor enviá-los após o dump. Os metacomandos exatos devem ser conferidos no cliente real antes de integrar.

## Budgets propostos para revisão

Valores para uma primeira implementação local, **não parâmetros aprovados para produção**:

| Limite | Valor proposto | Onde aplicar |
|---|---:|---|
| Conexão libpq | 10 s | PGCONNECT_TIMEOUT em cada cliente da origem |
| Preparação/manifesto | 30 s | Guardião de estágio + statement_timeout do exportador |
| Espera de lock DDL | 10 s | lock_timeout do exportador e --lock-wait-timeout=10000 do dump |
| Dump inteiro | 540 s | Guardião independente do supervisor; NÃO PGOPTIONS (pg_dump zera timeout) |
| Transação exportadora | 600 s | transaction_timeout e idle_in_transaction_session_timeout exclusivos do exportador |
| Supervisor de origem/dump | 600 s | Guardião global iniciado antes do FIFO |
| Encerramento após TERM | 5 s | Cleanup com escalada KILL e reap |

PGOPTIONS do exportador: `-c transaction_timeout=600000 -c idle_in_transaction_session_timeout=600000 -c statement_timeout=30000 -c lock_timeout=10000`. Não estabelecer PGOPTIONS de timeout como proteção do dump: pg_dump18 executa SET statement_timeout=0, lock_timeout=0, idle_in_transaction_session_timeout=0 e transaction_timeout=0 em setup_connection, antes de BEGIN. A opção --lock-wait-timeout limita apenas locks iniciais. PGOPTIONS do exportador é escopado a esse processo, nunca exportado para psql/pg_restore do banco descartável. Fazer a prova contra PG18; cliente mais velho não é substituto.

Os 30 segundos de metadata mais 540 de dump deixam margem para encerramento dentro de 600. O cron atual tem job de 30 minutos; o timeout remoto não substitui o guardião local ou o limite servidor. SIGKILL do supervisor não é capturável: o timeout do exportador limita apenas sua sessão, não encerra uma transação já importada pelo dump. Não existe fallback servidor comprovado para o dump neste desenho. Remoção do contêiner pela entrada é defesa adicional, não uma garantia de prazo. **Execução bloqueada:** antes de implementar, desenhar e provar guardião independente que sobreviva à morte do supervisor e termine somente seu pg_dump/backend no prazo, com identificação segura do processo e encerramento/reap do próprio guardião. A prova curta com falsos abaixo não cobre SIGKILL nem banco real.

Uma conexão extra mantém o horizonte MVCC desde as contagens até o dump; pode retardar VACUUM de versões ainda visíveis. Fechar exportador antes do restauro reduz a janela. O dump já usa snapshot próprio hoje: alinhar referência antecipa sua visão e acrescenta a conexão, sem duplicar os dados. Access Share permite CRUD normal, mas DDL exclusivo pode conflitar. Não congelar a aplicação com lock exclusivo geral nem exigir SERIALIZABLE DEFERRABLE.

## Tarefa 1 — provas da referência e fixture real

**Arquivos:** criar `internal/config/backup_snapshot_test.go`; adaptar `internal/config/scripts_test.go` apenas para o novo protocolo quando ele existir.

**Interfaces:** helper de teste `runSnapshotCase(t *testing.T, caso string) SnapshotResult`, com campos `ExitCode int`, `SourceIDs []int`, `DumpIDs []int`, `RestoreIDs []int`, `ExporterActiveBeforeRestore bool`, `RemainingChildren int` e `RemainingSessions int`. Os nomes são contratos a criar nesta tarefa; erro de harness chama t.Fatal e não fabrica ExitCode. Método auxiliar `requireCase(t, got, exit, ids)` compara código e slices completos, não apenas len.

- [ ] Criar dois contêineres exclusivos com imagem do `deploy/vps/db/Dockerfile`, rede sem egress, senhas sentinelas públicas e schema com owners/RLS/FORCE/Goose/partição reais. Aplicar 3 GiB/2 GiB ao restauro; cleanup por IDs capturados, nunca limpar contêineres do dono.
- [ ] Usar wrapper de pg_dump em PATH: sinalizar barreira FIFO ao entrar, aguardar release e chamar o binário real. O supervisor já terá exportado/contado. O controlador confirma DELETE/INSERT em outra conexão antes de liberar; sem sleeps para escolher interleaving.
- [ ] Escrever cenários e resultados completos antes do código:

```go
func TestBackupSnapshotExclusaoConcorrente(t *testing.T) {
    got := runSnapshotCase(t, "exclusao-apos-referencia")
    requireCase(t, got, 0, []int{1, 2})
}
func TestBackupSnapshotInclusaoConcorrente(t *testing.T) {
    got := runSnapshotCase(t, "inclusao-apos-referencia")
    requireCase(t, got, 0, []int{1, 2})
}
func TestBackupSnapshotInclusaoNaoMascaraRestauroIncompleto(t *testing.T) {
    got := runSnapshotCase(t, "inclusao-e-delete-no-restauro")
    requireCase(t, got, 3, []int{1, 2})
}
```

No primeiro caso origem final `[1]`, mas dump e restauro `[1,2]`; no segundo origem final `[1,2,3]`, mas dump/restauro `[1,2]`. No terceiro o arquivo `[1,2]` é válido e o restauro sabotado `[2]` precisa reprovar contra referência=2. `requireCase` inspeciona o dump por restauração independente sem sabotagem; assertions específicas comparam também origem e restauro.

- [ ] Rodar `go test -v -count=1 ./internal/config -run '^TestBackupSnapshot(Exclusao|Inclusao)' -timeout 120s` contra código atual. Exigir RED causal: exclusão atual retorna3, inclusão atual inclui ID3 e sabotagem atual pode passar com duas linhas. Guardar logs em /tmp.
- [ ] Acrescentar controle sem mutação (dump/restauro `[1,2]`, exit0) e controle da sabotagem sem inclusão (restauro `[2]`, exit3). Ambos precisam se comportar corretamente antes e depois da mudança.

## Tarefa 2 — supervisor e integração mínima

**Arquivos:** criar `deploy/backup/snapshot.sh` e `exportar.sql`; alterar somente origem/dump de `backup.sh`; atualizar comentário de `verificar.sql`; adaptar mocks em `scripts_test.go` sem apagar checks de senha/percent-encoding.

**Interfaces:** supervisor/manifesto definidos acima. Chamador continua com ORIGEM_URL/PG*/SAIDA/VOLUME_BYTES atuais; nenhuma opção SSH ou API nova. Todos os recursos internos têm prefixo `snap_` e diretório exclusivo.

- [ ] Implementar PREPARAR/EXPORTAR com guardião ativo antes de FIFO, declaração das traps e PGOPTIONS locais. Publicar manifesto fechado/atômico, validar dados, recusar goose NULL e publicação com erro. Verificar `sh -n` e ShellCheck sem introduzir Bash.
- [ ] Implementar COPIAR como filho direto com `--snapshot`, formato custom, URI pública e PGPASSWORD herdada só da origem. Capture wait de forma explícita; nunca depender de `set -e` para coordenar subprocessos.
- [ ] Implementar ENCERRAR/CONCLUIR e todos os ramos ABORTAR. No caminho feliz esperar exportador **antes** de iniciar `pg_restore`, não apenas enviar COMMIT. Traps permanecem exclusivas do supervisor filho, evitando sobrescrever traps do backup/entrada.
- [ ] Integrar chamada conceitual no backup. Fazer parse com read/IFS, sem eval, preservar log/mensagens e restore password. Continuar initdb.sql/pg_restore/verificar.sql/disco exatamente como antes.
- [ ] Atualizar falso psql do teste de argv para ler o SQL de stdin, publicar manifesto e esperar o COMMIT/EOF enviado; conservar registros de argumentos e asserções de credencial fonte/restauro. Mock não substitui os três cenários reais.
- [ ] Executar `go test -race -v -count=1 ./internal/config -run 'TestBackup(Snapshot|Keeps|Refuses)' -timeout 180s`. Exigir GREEN real dos cenários, incluindo prova `pg_stat_activity`: exportador ativo durante barreira, ausente antes do wrapper de pg_restore.
- [ ] Sabotar só cópia: remover `--snapshot`, fechar exportador antes do dump e retirar wait/checagem de status. Cada mutante deve produzir RED pela propriedade própria; restaurar cópia e repetir GREEN, comparando bytes com checkout antes de usar o log como gate.

## Tarefa 3 — término, locks, recursos e revisão

**Arquivos:** testes em `backup_snapshot_test.go`; ajustes necessários em `snapshot.sh`/`exportar.sql`; cópia dos auxiliares em `deploy/ci/checar-vps.sh`; texto pertinente do `docs/deploy/vps.md`. Não tocar workflows/entrada/compose.

- [ ] Testar wrapper psql que termina antes de abrir FIFO e depois de abri-lo sem publicar, SQL com erro e manifesto truncado/malformado. O guardião deve encerrar o supervisor dentro do budget curto de teste; nenhum pg_dump ou pg_restore pode ser chamado.
- [ ] Testar pg_dump exit não zero após criar arquivo parcial e exportador encerrado por timeout antes de importar. Exigir retorno1, SAIDA parcial removida, nenhum restauro/cifra e zero sessões source ao concluir.
- [ ] **Gate antes da execução da Tarefa2:** provar SIGKILL do supervisor com pg_dump real após importar snapshot. O guardião independente precisa sobreviver, encerrar o cliente/backend dentro do budget e não deixar timer/processo próprio. Remover esse guardião em cópia deve dar RED; PGOPTIONS não pode ser usado para fingir a propriedade.
- [ ] Testar exportador morto após importação com dump real ainda em voo. O importador pode concluir sua cópia, mas o supervisor deve reprovar ao wait exportador; sem manifesto no stdout e sem restauro. Essa política conservadora é intencional.
- [ ] Injetar TERM/INT/PIPE nas barreiras: antes da abertura do FIFO, após manifesto, durante dump e antes de COMMIT. Usar wrapper que ignora TERM para provar KILL em 5 segundos, sem aumentar budget da aplicação. Verificar filhos/timer por PIDs próprios e sessões por PGAPPNAME próprio mais PID/backend capturado, não contagem global.
- [ ] Sabotar guardião e cleanup em cópia; watchdog **externo do teste** encerra apenas o supervisor e filhos próprios para produzir RED sem pendurar suíte. Remover cleanup não pode virar skip nem PASS.
- [ ] Manter uma transação administrativa com `LOCK TABLE targets IN ACCESS EXCLUSIVE MODE` antes da contagem. Exigir erro limitado pelo lock_timeout e limpeza. Segundo caso: adquirir DDL exclusivo de uma tabela não contada entre manifesto e dump; `--lock-wait-timeout` deve reprovar de forma limitada. Em ambos, soltar lock somente depois da recusa para provar causa.
- [ ] Observar inserção/exclusão normal confirmadas enquanto exportador estiver aberto (os casos da Tarefa1 já exercitam isso). Verificar `backend_xmin` do exportador durante a barreira e ausência depois do dump; não exigir que VACUUM físico remova exatamente N linhas para testar uma regra MVCC.
- [ ] Rodar restauro válido com `VOLUME_BYTES` sintético abaixo da ocupação para exigir4 e cópia válida; preservar verificação de códigos/headers dos testes `deploy/ci/testar-vps.sh`. Reprovar owner/RLS/Goose/partição sabotados com3 usando verificações existentes.
- [ ] Conferir via docker inspect os limites 3 GiB/2 GiB do restauro; coletar pico RSS/tempo em massa sintética registrada, inclusive limites apertados e erro de memória do dump/restauro. Exigir falha segura e cleanup, nunca desativar limites/checks. RSS medido localmente é evidência de uma massa, não garantia para volume de produção.
- [ ] Rodar gates focados, `sh deploy/ci/testar-vps.sh`, `sh deploy/ci/checar-nome.sh`, `git diff --check`; depois coordenador roda `go build ./... && go vet ./... && make test` uma vez, sem suítes PG paralelas concorrendo por recursos. Registrar RED/GREEN/sabotagem, versão, massa, duração, recursos e limpeza.
- [ ] Ajustar a cópia da fixture para os quatro arquivos backup.sh/verificar.sql/snapshot.sh/exportar.sql. Atualizar scp e install do runbook (hoje ~771/781), Confira/saída esperada e tabela de atualização (~1958), com destino /opt/stillon/deploy/backup e modo0644. Confirmar auxiliares no ambiente real do ensaio, sem executar o roteiro na VPS.
- [ ] Obter revisão independente de lifecycle/argv/códigos/RSS. Só depois atualizar a explicação de coerência e diagnóstico; implantação continua passo futuro guiado, sem afirmar mudança no estado instalado da VPS.

## Critérios de aceitação

- [ ] Exclusão concorrente deixa origem atual menor, mas dump/restauro contêm exatamente a visão contada e código0. Inclusão posterior não muda a visão do dump; não mascara perda injetada do restauro.
- [ ] Versão e duas contagens pertencem à mesma transação que exporta o snapshot; pg_dump importa esse ID e cliente/servidor são os suportados.
- [ ] Sucesso depende de ambos os waits; exportador já terminou antes do restauro. Erros/timeout/sinais não geram 0/4 nem resultado legível como sucesso.
- [ ] Todos os caminhos bloqueantes estão cobertos por prazo local; fallback servidor somente para exportador, e dump exige guardião independente comprovado; cleanup de TERM tem prazo e KILL/reap. SIGKILL é documentado com fallback, não vendido como evento capturável.
- [ ] Nenhuma senha em argv/stdout/manifesto; passwords da fonte/restauro continuam distintas, incluindo aspas, barras e percent-encoding.
- [ ] Checks, owners/RLS/partições, cifra e código4 preservados. `>=` não relaxado, memória/tmpfs não ampliados e não surgem dependências/novas ações externas.
- [ ] Testes reais e mutantes passam pelo ciclo RED/restauração/GREEN; nenhum contêiner/volume/FD/timer/sessão do ensaio permanece. Gate completo do projeto e revisão independente aprovados antes de integrar.

## Livros e documentação aplicados

- Dimitri Fontaine, *The Art of PostgreSQL*, cap.4, Business Logic / Correctness, pp.46–48, consultado anteriormente em `Downloads/Livros/Dev`: a coerência é propriedade da transação e da visão compartilhada. Aplicação própria: referência e dump precisam do mesmo snapshot; não ajustar a referência para ocultar exclusão legítima.
- Maurício Aniche, *Effective Software Testing*, §3.11, pp.90–92 (PDF118–120), e §9.2.3–4, pp.236–239 (PDF264–267), já lidos em `Downloads/Livros/Dev`: testar o banco real e a força das assertivas. Aplicação própria: comparar IDs completos, confirmar COMMIT nas barreiras e exigir mutantes da exportação/cleanup falharem.
- Teiva Harsanyi, *100 Go Mistakes and How to Avoid Them*, erros73/81, pp.228–230 e256–260 (PDF248–250 e276–280), já lidos em `Downloads/Livros/Go`: cancelamento só funciona onde há consumidor e recursos precisam ter dono. Aplicação por analogia, não sintaxe shell: limite remoto não termina filhos automaticamente; supervisor deve cancelar e aguardar trabalho próprio.

Context7 foi consultado novamente nesta preparação (`/websites/postgresql_18`), começando por resolve-library-id. Metacomandos precisos foram conferidos também no manual oficial quando a recuperação do Context7 foi incompleta. Fontes primárias:

- [Exportação de snapshots](https://www.postgresql.org/docs/18/functions-admin.html) e [importação/transações](https://www.postgresql.org/docs/18/sql-set-transaction.html).
- [pg_dump, snapshot e lock-wait-timeout](https://www.postgresql.org/docs/18/app-pgdump.html).
- [psql, gset/getenv/output e status de shell](https://www.postgresql.org/docs/18/app-psql.html).
- [Timeouts de sessão e retenção de versões](https://www.postgresql.org/docs/18/runtime-config-client.html).

Os livros sustentam os critérios; a prova específica de StillOn é a auditoria reproduzida. Não foi copiado texto extenso dos livros para o repositório.

## Prova curta e bloqueadores remanescentes

Em 2026-10-04, laboratório exclusivo `/tmp/stillon-backup-lifecycle-20261004`, sh real da imagem PG18 fixada, leitor falso e FIFO, sem banco/segredos/rede/socket Docker: protótipo ingênuo (guardião sinaliza apenas supervisor) ficou preso em write com leitor ignorando TERM e cancelamento do pai; watchdog externo do teste precisou KILL após6s. O protótipo ajustado, com guardião encerrando também o leitor, retornou erro em4s e registrou wait/cleanup, sem PIDs/timers remanescentes. Open sem leitor foi interrompido e leitor que morreu após read gerou erro seguro. Evidências em [prova local](../../../.superpowers/prova-backup-lifecycle-2026-10-04.md).

Essa prova impede assumir que trap do pai, sozinho, cancela I/O bloqueante. O ajuste é **protótipo de laboratório**, não produção. O status1 resulta de PIPE e não preserva a classificação do TERM; esse detalhe ainda requer tratamento/prova. PIDs registrados numa execução curta não provam imunidade a reutilização de PID. Não foram exercitados pg_dump real, morte SIGKILL do supervisor com importador em voo, árvore completa sob sucesso, cleanup de todos os FDs ou RSS.

Bloqueador remanescente para implementação: especificar e comprovar um guardião independente com lease do cliente/backend, que sobreviva a SIGKILL e também destrave FIFO caso o pai não execute sua trap. A prova real deve incluir término da consulta importadora, desaparecimento de backend e ausência de guardião/timers. Se não for demonstrável com ferramentas existentes e tamanho aceitável, revisar a arquitetura antes de tocar scripts; não substituir por promessa de PGOPTIONS.

Fonte primária do reset de timeouts: [pg_dump.c REL_18_STABLE, setup_connection](https://raw.githubusercontent.com/postgres/postgres/REL_18_STABLE/src/bin/pg_dump/pg_dump.c), linhas1372–1378. Context7 foi consultado primeiro; a recuperação não trouxe esse detalhe, por isso o revisor conferiu o código oficial e o autor confirmou.

## Decisões que exigem revisão antes da execução

1. **Budgets operacionais:** aprovar ou ajustar 30/540/600 segundos e lock10s com base em tamanho e duração observados, sem elevar recursos nem desabilitar prazo. Os números acima permitem implementar/testar localmente, mas não afirmam adequação ao banco de produção desconhecido.
2. **Supervisor POSIX e transporte:** revisar especialmente FIFO sob sinais, timers próprios, wait interrompido, propagação de status e PID reutilizado. Aprovar somente com as provas negativas descritas; alternativa com `psql \!` exigiria outra análise de árvore e não é substituição silenciosa.
3. **Encerramento prematuro após importação:** manter reprovação conservadora mesmo se o pg_dump concluir; isso simplifica comprovação do ciclo de vida, mas pode recusar arquivo tecnicamente válido numa falha rara do exportador.
4. **Comparação exata:** manter `>=` nesta implementação para escopo mínimo. Igualdade em usuários/alvos e outras verificações de conteúdo são evolução separada; não converter em requisito oculto da correção.
5. **Prontidão e rollout:** resolver os bloqueadores técnicos e definir budgets antes de iniciar a próxima implementação, dentro da autonomia local já autorizada. Depois dos gates, implantação de novos scripts deve continuar pelo runbook guiado; nada neste documento confirma atualização, cron habilitado ou backup realizado na VPS.

Nenhum desses pontos requer perguntar novamente ao dono sobre rotinas locais autorizadas. O próximo passo é revisão técnica e checkpoint. A onda atual não muda scripts; a próxima implementação permanece bloqueada enquanto o guardião independente/SIGKILL e os budgets não forem definidos e comprovados.
