# Correção do CI do PR #27

Trabalho na branch `melhoria/plano-12-seguranca-painel`, sobre o commit
`83fca6e74e7bd92210d2f97abc24bfbd0ac1d6f0`. O
[CI original](https://github.com/BipoXDXD/stillon/actions/runs/37253229747)
terminou com falhas em `workflows` e `test (postgres:17-alpine)`; os demais
checks passaram, exceto `publish`, que foi pulado. As três ondas anteriores
foram preservadas. Não houve merge nem deploy.

## ShellCheck: versão local diferente do runner

O comando exato do job, com ShellCheck 0.9.0, reproduziu seis SC2015:
um em `deploy/ci/checar-vps.sh`, quatro em `deploy/vps/stillon-entrada`
e um em `deploy/backup/backup.sh`. A mesma cópia sem correção passou
com 0.11.0. A [release oficial 0.11.0](https://github.com/koalaman/shellcheck/releases/tag/v0.11.0)
registra a mudança da regra quando a segunda condição é um teste.

Os seis guards usam agora `if ! { condições; }; then ...; fi`, padrão já
adotado pelos scripts. Permanecem a ordem das condições, o curto-circuito,
a recusa de symlinks, a identidade dos recursos próprios e os códigos de saída.
Não foram acrescentadas supressões nem reduzida a severidade do CI.

Depois da correção, o job local passou: ShellCheck 0.9.0 com o comando
completo, actionlint 1.7.12 com ShellCheck no PATH, `checar-workflows.sh`,
`checar-nome.sh` e os 56 casos de `testar-vps.sh`. ShellCheck 0.11.0 e as
checagens de sintaxe dos três scripts também passaram.

## Fixture de backup: stderr não é ID de contêiner

O teste `TestBackupSnapshotConcurrentChangesAndRestore` usa PostgreSQL 18
para o snapshot/restauro, inclusive na perna geral PG17. Nessa perna do CI,
o download da imagem escreveu progresso no stderr. A fixture tratava o
`CombinedOutput` de `docker run` inteiro como ID do contêiner: o ID incluía
o progresso, causando falha de prontidão e de limpeza, apesar do banco pronto.

O helper de teste captura stdout e stderr separadamente. O chamador recebe
somente stdout; em caso de erro, ambas as saídas continuam disponíveis para
diagnóstico. O código do produto não mudou nessa correção.

O teste existente agora usa um launcher que acrescenta uma mensagem ao stderr
de `docker run` e dá EXEC ao Docker real. Não fabrica ID, consultas, dump nem
resultado de restauro. Assim a regressão é exercitada também com imagem quente.
O teste falhou antes da correção, passou depois e voltou a falhar quando a
mistura de streams foi reintroduzida temporariamente. Depois da restauração,
passou novamente; os contêineres exclusivos desses ensaios foram removidos.

## Validação integrada e cobertura

`go build ./... && go vet ./... && make test` passou com race nas duas
imagens da matriz: PG17 com o digest do CI e PG18 padrão. Cada suíte aprovou
os 20 pacotes com testes. O ensaio VPS completo também passou e preservou
os IDs e a saúde dos serviços preexistentes; a fixture foi removida.
O parecer independente conferiu diff, hashes, logs, sabotagens e resultados,
sem bloqueador no escopo das correções.

A medição adicional `go test -race -count=1 -coverpkg=./...
-covermode=atomic -coverprofile=... ./...` passou. O perfil agregado por
blocos únicos registra **4.441 / 5.151 instruções, 86,22%**: inclui todos
os pacotes, código gerado, auxiliares e blocos fora de funções nomeadas.
`go tool cover -func` registra 86,3%, pois seu denominador considera apenas
blocos dentro de declarações de função. As 710 instruções não exercitadas
não foram excluídas da medição. Logs, perfil e inventários locais ficam em
`.superpowers/validacao-ci-2026-10-04/`; o gate PG17 e a prova causal têm
logs próprios em `.superpowers/`.

Não se declara cobertura global de 100%. O helper corrigido é código de
`_test.go`, excluído da instrumentação de cobertura do Go; sua regressão é
comprovada pelo ensaio real e pela sabotagem. A cobertura do produto será
registrada conforme a medição, sem confundir testes aprovados com linhas cobertas.
Provas HTTP/2, cobertura adicional do painel/configuração e avisos da troca
de autenticador são fases isoladas; não compõem os resultados deste fix.

## Integração

Esta correção pertence ao [PR #27](https://github.com/BipoXDXD/stillon/pull/27).
Commits continuam assinados. Se a chave não estiver liberada no agente GPG,
a assinatura aguardará o terminal do dono; a configuração de assinatura
não será desativada. Limpeza da VPS, deploy, migrações reais, licença,
publicação e decisões sobre chaves/sessões permanecem pendentes para o dono.
