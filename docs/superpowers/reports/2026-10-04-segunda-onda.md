# Segunda onda do Plano 12 — 2026-10-04

**Estado: código congelado e gates locais concluídos com código 0.** Retomada
às 17:00 UTC com três agentes GPT-6.1 High. Branch
`melhoria/plano-12-seguranca-painel`, base `cd81622`. O Plano 12 inteiro continua
parcial. O checkpoint da primeira onda permanece intacto, separado da entrega
combinada descrita no [handoff atual](2026-10-04-continuacao.md).

## Resultado

Contagens da origem e dump agora compartilham um snapshot exportado do PostgreSQL.
Antes, DELETE concorrente produzia dump válido recusado com código 3; INSERT
podia mascarar uma remoção artificialmente injetada depois do restauro. Não foi
reproduzida corrupção do dump. Os predicados originais de `verificar.sql` e os
códigos 0/3/4 permanecem; comparar mínimos de cardinalidade não prova identidade
integral de todas as linhas.

O subcomando `stillon backup-snapshot` supervisiona psql/pg_dump reais, preservando
libpq e seu contrato de URI. Exportador RR/RO fica aberto durante o dump. Sucesso
exige dump/Wait, ausência de outras sessões identificadas pela execução, rollback,
EOF e Wait do exportador. O manifesto final é uma única linha validada como dados.
Arquivo novo é criado exclusivamente, com modo 0600; falha remove somente o inode
próprio. Arquivo existente ou symlink é recusado antes de conectar.

Conexão 10 s, preparação 30 s, dump 540 s, total 600 s, graça/cleanup 5 s.
TERM/KILL/Wait, morte do pai direto e `client_connection_check_interval=1000ms`
complementam os limites. A thread que cria cada filho permanece presa até Wait,
conforme a semântica Linux de Pdeathsig. URI recusa onze parâmetros reservados,
inclusive opções percent-encoded; ambiente PG da origem é reconstruído sem herdar
o do restauro. Senhas não entram em argv, manifesto ou mensagens de erro.

A entrada copia automaticamente o binário de um contêiner parado da imagem app
por ID imutável. O PG18 descartável recebe `--init` e no-new-privileges; helper
roda como UID/GID 65532 com CapEff=0. O probe de protocolo antecede acesso à origem:
imagem antiga ou cópia inválida falha fechada. Leases/labels próprios permitem
recuperação após SIGKILL e preservam recursos de terceiros. Arquivos do dump são
transferidos ao chamador sem chown recursivo nem seguir symlinks. Recursos continuam
3 GiB de memória e tmpfs de 2 GiB.

[Plano e contratos](../plans/2026-10-04-backup-supervisionado-segunda-onda.md).
Sem dependência Go nova nem alteração de go.mod/go.sum; compilação linux/arm64 e
darwin/arm64 conferida. Em plataforma sem a proteção Linux, o helper falha fechado;
compilação cruzada não é prova de execução nessas plataformas.

## Testes e revisões

A integração usa PG18 real, SCRAM com senha especial e controle de senha errada,
papel backup sem superuser, com BYPASSRLS e pg_read_all_data, owners/RLS/FORCE,
partição e Goose 11.
DELETE/INSERT exercitam o snapshot comum; erro SQL, URI/ambiente, protocolo inválido,
TERM/prazo e SIGKILL exercitam falhas. Clients observados por wrappers executam os
binários PG reais. Hooks de sincronização existem somente numa cópia de teste do
módulo; a produção não oferece switch de teste.

A prova da CLI real executa gosu → helper → pg_dump, verifica UID/capabilities e
mata o shell pai. Clientes e sessões SQL desaparecem enquanto a trava SQL continua
retida. Cinco sabotagens comportamentais compiláveis receberam RED e restauração:
snapshot, URI reservada, morte do pai, senha em argv e manifesto frouxo. Suíte
restaurada focada com race: 14,116 s. Testes unitários também provaram criação
exclusiva, limpeza, pipes e orçamento independente do encerramento do exportador.

O ensaio completo `checar-vps` usa imagem nova, age e restauro reais. Oito fronteiras
incluem create/cópias/protocolo, probe antigo, probe travado, SIGKILL com/sem CID;
prova de recuperação, app parado, terceiro preservado e códigos 3/4. A primeira
execução falhou por variável da fixture filtrada pela entrada. Depois da correção,
o cenário exige marcador da fronteira e o ensaio completo terminou com código 0.
Falha de infraestrutura não foi contabilizada como RED causal nem como GREEN.

Implementadores revisaram grupos diferentes: supervisor/CLI, operação e contratos
de integração. Nenhum bloqueador aberto nas revisões finais. Pareceres detalhados
estão em `.superpowers/`, fora do git; este relatório preserva seus resultados e
limites. Revisão de logs não foi apresentada como reexecução de testes.

## Criação e retenção de partições

`TestPartitionCreationAndRetentionShareTheirTransactionLock` usa as funções SQL
reais nos dois sentidos, transações/conexões próprias e observação por PID.
A query dona já retornou, mas a transação permanece aberta; a outra operação precisa
esperar a mesma advisory lock até o commit. Corte sem DROP evita mascarar a prova
por DDL; no sentido retenção → criação, a partição aparece apenas após o commit.

GREEN PG18; RED compilável em cópia alterando somente a chave da retenção, nos dois
sentidos; migration restaurada por hash e GREEN. Teste focado também GREEN no PG17
fixado pelo CI. Suíte storage inteira PG18 com race: 12,005 s; nenhuma função SQL
ou migration foi alterada nesta tarefa. Revisão independente sem bloqueador.

## Gates do pacote final

| Verificação | Resultado observado |
| --- | --- |
| `go build ./... && go vet ./... && make test` | Código 0; PG18 real, todos os pacotes com `-race -count=1` |
| `make lint sec` | Código 0; vet/staticcheck/gosec; zero vulnerabilidades chamadas ou em pacotes importados |
| Imagens runtime/tools novas | Build 0; versão `cd81622-plano12-segunda`; runtime nonroot/sem shell/painel conferidos |
| Tamanho runtime comprimido | 7.741.204 bytes, abaixo de 15 MB |
| Trivy nas duas imagens | Código 0; nenhum HIGH/CRITICAL corrigível no filtro usado |
| VPS local completa | Código 0; distribuição, backup cifrado/restauro, falhas e recuperação |
| Sintaxe/ShellCheck, 56 casos cliente VPS, workflows | Verdes; revisão independente da operação |
| Teste novo de advisory lock | GREEN/RED/restauração/GREEN no PG18; GREEN focado no PG17; observação comportamental |

Govulncheck ainda informa uma vulnerabilidade em módulo requerido sem caminho
chamado/importado, já registrada na primeira onda; não é “zero achados em todas
as dependências”. Trivy foi executado com HIGH/CRITICAL e ignore-unfixed.
O painel não mudou nesta segunda onda: 251 testes e 14 E2E são resultados da
primeira onda, não uma nova execução. A segunda onda não repetiu a suíte Go inteira
no PG17; o novo teste de lock foi executado nessa versão.

Logs finais: `/tmp/stillon-segunda-go-final.log`,
`/tmp/stillon-segunda-lint-sec.log`, `/tmp/stillon-segunda-checar-imagem.log`,
`/tmp/stillon-segunda-trivy-{runtime,tools}.log` e
`/tmp/stillon-backup-segunda-onda-20261004/arquitetura/checar-vps-segunda-onda-2.log`.
Provas RED/restauradas e pareceres: `/tmp/stillon-backup-segunda-onda-20261004/`
e `.superpowers/`. [Leitura de Markdown/livros](2026-10-04-leitura.md).

## Limites e próximos passos

Não houve VPS real, DNS, acesso a segredo do dono, push, PR, merge ou Actions remoto.
A assinatura GPG continua habilitada: a tentativa única da primeira onda falhou
por falta de pinentry; não houve retry nem commit. Patch combinado, inventário de
hashes/modos e instruções de recuperação estão no [handoff](2026-10-04-continuacao.md).

ID imutável de imagem não é assinatura/proveniência. UID/capabilities descrevem
privilégios de sistema; o helper ainda alcança o banco descartável com seu trust
local. A morte do cliente Docker/SSH não equivale à morte do pai direto no contêiner.
Os limites internos não garantem duração máxima de toda a entrada/restauro nem
preempção universal de SQL/kernel/rede. Não houve ensaio de exaustão dos 3 GiB/2 GiB.
Recuperação automática exige labels próprios; restos legados sem labels precisam
diagnóstico, sem limpeza ampla por nome.

Assinatura/proveniência da imagem, rotação de MASTER_KEY, resposta de refresh
perdida, cookies de dispositivo, licença/publicação e auth para sqlc continuam
pendentes. O dono revisará estética do painel e integração; hospedagem real segue
runbook guiado, um passo e Confira por vez. Esta entrega local não autoriza deploy.
