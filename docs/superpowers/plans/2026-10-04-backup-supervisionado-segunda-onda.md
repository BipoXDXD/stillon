# Segunda onda: backup com referência coerente e processos supervisionados

Data: 2026-10-04. Continuação local autorizada pelo dono durante sua ausência.
Branch: `melhoria/plano-12-seguranca-painel`; base `cd81622`.

**Estado: implementação local congelada, revisões independentes e gates de código,
segurança, imagens e VPS local concluídos com código 0.**
[Resultados e limites](../reports/2026-10-04-segunda-onda.md).
Este plano revê a alternativa POSIX/FIFO do
[plano anterior](2026-10-04-backup-snapshot-proxima-onda.md). A prova anterior
não fechou o encerramento de uma árvore de processos sob SIGKILL; os timeouts
SQL zerados por `pg_dump` também não eram uma proteção para dump órfão.

## Problema e alcance

As contagens da origem e o dump usam snapshots distintos. Uma exclusão entre
essas etapas produziu dump/restauro corretos, mas recusa com código 3; uma
inclusão mascarou perda artificialmente injetada depois do restauro. O trabalho
alinha referência e cópia, mantendo o verificador existente. Não foi reproduzida
corrupção do dump e não se promete prova integral de todas as linhas ou PITR.

## Decisões locais

- Preferir um subcomando `stillon backup-snapshot` no binário existente, limitado
  à referência, exportação e dump. Sem novo pacote do sistema ou dependência Go.
- Preservar libpq: `psql` exportador e `pg_dump` como filhos diretos do helper,
  com pipes anônimos. Não traduzir silenciosamente o contrato de URI para pgx.
- Exportador abre RR/RO, obtém snapshot e três referências numa única consulta,
  permanece aberto durante o dump e termina com rollback/Wait antes do sucesso.
- Processos: contexto total limitado, TERM seguido de KILL/Wait; Pdeathsig nos
  filhos e thread criadora presa de Start até Wait. O helper arma sua própria
  morte do pai direto depois de registrar sinais, conferindo a corrida de PPID.
- `backup.sh` chama o helper em foreground direto, com stdout num arquivo
  privado. Não usar command substitution, wrapper shell ou executar protocolo.
- `client_connection_check_interval` complementa o encerramento dos clientes.
  O ensaio demonstrou SQL residual sem ele; não é promessa universal de preempção.
- Recusar parâmetros de URI que possam desligar proteção, orçamento ou identidade;
  conservar os demais parâmetros libpq. Credenciais permanecem fora de argv,
  protocolo e erros. Ambiente PG da origem não herda o do restauro.
  Reservados: `options`, `service`, `application_name`,
  `fallback_application_name`, `connect_timeout`, `password`, `sslpassword`,
  `passfile`, `oauth_client_secret`, `scram_client_key`, `scram_server_key`.
  Decodificar percent-encoding e recusar também variações de caixa/duplicatas.
- Distribuição automática: criar contêiner **parado** de ID imutável da imagem
  app aprovada localmente, copiar `/stillon` para armazenamento privado e depois
  para a camada regular do PG18 descartável. Não executar de SHM/noexec nem do
  contêiner app mutável. Remoção por ID/lease próprios, recuperável após SIGKILL.
- Probe `stillon version --backup-snapshot-protocol` antes de conectar; versão
  antiga recusa a opção no comando conhecido. Nenhum fallback para servidor.
- Manter 3 GiB de memória e tmpfs de 2 GiB; acrescentar `--init` para recolher
  órfãos. O restauro, owners, FORCE RLS, partições e códigos 0/3/4 ficam preservados.
- Executar o binário copiado como UID/GID 65532, sem capabilities e com
  no-new-privileges, usando launcher já existente que faça exec direto. O shell
  de preparação/restauro não transfere root ao helper. Provar a cadeia de morte
  depois da troca de UID e conservar acesso do chamador ao dump e à limpeza.
- Budgets implementados: conexão 10 s, preparação 30 s, dump 540 s, total 600 s,
  grace/cleanup 5 s. Intervalo de conexão 1000 ms no produto e 200 ms em ensaios
  internos. A CLI real também recebeu teste com o intervalo de produção.

## Etapas e ownership

1. **Provas descartáveis independentes:** supervisor e cadeia pai/filhos;
   distribuição/falhas/leases; PostgreSQL real com papel backup, SCRAM,
   owners/RLS/partições, concorrência e URI. Agentes GPT-6.1 High, arquivos
   disjuntos em `/tmp` e relatórios ignorados. Não misturar protótipo com produto.
2. **Gate de desenho:** controlador confronta logs, limites e interfaces. Go/psql
   precisa repetir a prova real que Go/pgx já recebeu. Não herda seu resultado.
3. **Implementação:** responsabilidades explícitas e disjuntas para pacote/CLI,
   shell/entrada/distribuição, testes de contratos. Controlador cuida dos arquivos
   comuns, integração e documentação. Definir o protocolo antes das edições.
4. **Revisão e sabotagem:** revisor diferente do implementador; RED compilável
   nas propriedades, restauração e GREEN. Falha de build/infra não vale como RED.
5. **Gates finais:** build/vet/make test com Docker; lint/security e shellcheck;
   ensaios de backup e VPS local com imagem nova; versão antiga/falhas de cópia;
   tamanho/segurança da imagem; nome/whitespace/segredos/checkpoint recuperável.

O teste adicional de advisory lock de criação/retenção é independente desta
mudança. Usa funções SQL reais, PID específico e transação aberta depois da
query. Já recebeu RED em cópia com chave divergente e GREEN após restauração;
revisão independente sem bloqueador e GREEN nos PG17/18. O gate Go completo do
pacote final passou com PG18 e race; o PG17 recebeu o teste novo de lock focado.

### Interface acordada

CLI sem argumentos: `/stillon backup-snapshot`. Entradas pelo ambiente:
`STILLON_ORIGEM_PUBLICA` sem senha, `PGPASSWORD` exclusivo da origem e `SAIDA`.
Depois de dump/Wait, ausência de outras sessões identificadas pela execução,
rollback respondido, EOF e Wait do exportador, uma única linha no stdout:

```text
stillon-backup-snapshot-v1|VERSAO|USUARIOS|ALVOS
```

Inteiros decimais não negativos, versão existente; nenhuma mensagem operacional
nesse canal. O shell valida o protocolo como dados, depois executa o restauro.
O runtime não consulta o próprio backend depois que ele fecha: observa outras
sessões antes do rollback e exige finalizar sua transação/cliente. O observador
externo dos testes confirma ausência de todos os backends enquanto mantém o lock.
O probe `version --backup-snapshot-protocol` retorna somente a magic acima, sem
os três campos. Uso inválido sai 2; falha da fase sai 1; interrupção da CLI segue
o código 130 dos subcomandos existentes. O shell preserva suas interrupções e
não confunde erro da origem com o código 3 da cópia reprovada.

Ownership de produção: `operacao_livros` cuida de `internal/backup/` e CLI;
`painel_contexto`, de backup.sh, entrada, fixture VPS e runbook;
`revisao_backend`, dos testes de scripts e integração em `internal/config/`.
Controlador cuida de documentação comum, teste de advisory lock, revisões cruzadas,
gates, índice e checkpoint. Todos preservam edições concorrentes.

## Aceitação do backup

| Propriedade | Evidência exigida |
| --- | --- |
| Referência comum | DELETE confirmado após exportação não muda a cópia; INSERT não mascara perda injetada no restauro |
| Credenciais e conexão | SCRAM com senha especial percent-encoded; nenhuma senha em argv/saída; parâmetros reservados recusados antes da origem; PG do restauro separado |
| Ciclo de vida | TERM/prazo, SIGKILL do helper e do shell direto; ambos clientes e sessões ausentes enquanto lock continua retido |
| Falhas fechadas | Erro/EOF/protocolo parcial/exportador expirado/dump recusado não geram manifesto nem código de backup aceito |
| Distribuição | Imagem imutável, binário antigo/cópia parcial recusados; leases próprios recuperados depois de SIGKILL; terceiros preservados |
| Restauro | Condições originais de `verificar.sql`, owners/RLS/partições e códigos 0/3/4; recursos atuais sem ampliação |
| Integração | Fluxo real local entrada → binário copiado → backup → cifra → restauro, sem VPS, segredo ou serviço do dono |

## Limites e pendências críticas

Sem merge, push, deploy, Actions remoto, DNS ou alteração de segredos nesta onda.
O dono revisará estética do painel e integração quando retornar. Assinatura de
imagem/proveniência e rotação de MASTER_KEY exigem desenho próprio. GPG continua
pendente: a tentativa única da primeira onda falhou por ausência de pinentry;
não repetir nem desabilitar assinatura por conta própria.

A morte de um ancestral de `docker exec` não equivale à morte do pai direto do
helper. Ensaiar o fluxo final e documentar a semântica: não prometer que queda de
SSH ou morte do cliente Docker encerra processos no contêiner. O contexto interno
limita a fase supervisionada; restauro/disco e limpeza da entrada precisam seus
próprios contratos. `--init` recolhe órfãos, não cancela a árvore nem SQL sozinho.
