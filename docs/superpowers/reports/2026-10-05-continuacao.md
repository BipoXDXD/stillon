# Continuidade do StillOn — 2026-10-05

Registro desta retomada, em andamento. Atualiza o estado operacional do
[registro anterior](2026-10-04-continuacao.md), preservando o histórico.
Não houve integração à `main`, deploy ou operação na VPS.

## Orientação vigente

O dono se ausentou e autorizou decisões não críticas, testes, commit assinado,
push e PR por fase. Cinco agentes GPT 6.1 Sol High trabalham em responsabilidades
separadas. Decisões críticas ficam pendentes; ações já autorizadas continuam
sem novas perguntas rotineiras.

A meta de 100% exige declarar escopo e denominador. Não excluir fontes, reduzir
limiares ou apresentar cobertura parcial como global. Testes novos receberam
sabotagens compiláveis com falha funcional, restauração, GREEN e revisão.

## Publicações

As primeiras fases usam o PR #27; as novas usam a fixture corrigida do PR #34
como base para manter diffs pequenos. #18 segue
na branch original do Dependabot. Todos os commits da tabela foram assinados
e verificados com `git verify-commit`. A credencial já estava liberada no agente
GPG: modo batch sem prompt, segredo no chat ou desativação da assinatura.

| PR | Resultado | HEAD publicado | Base |
| --- | --- | --- | --- |
| [#27](https://github.com/BipoXDXD/stillon/pull/27) | Guards shell e stdout da fixture Docker | `28159eaa2381f21b2326fe0d607b28ce7f7a0b1e` | main |
| [#18](https://github.com/BipoXDXD/stillon/pull/18) | Tipos Node 24, mantendo TypeScript do Dependabot | `54d255a5354036ac7517c188d1a84b98c0136189` | main |
| [#28](https://github.com/BipoXDXD/stillon/pull/28) | Teto real de cabeçalhos HTTP/2 | `a89e210d40ea9a33004ea02b33343eee46071fba` | #27 |
| [#29](https://github.com/BipoXDXD/stillon/pull/29) | Config: 146/146 instruções, com fixture corrigida herdada | `7dc365eec011ca60218caa2009d0e2c3a89b60aa` | #34 |
| [#30](https://github.com/BipoXDXD/stillon/pull/30) | Bootstrap, nonce e API: 100% linhas dos três módulos | `6fff38eae22b3cbb121507576e7a70d6ae7ef878` | #27 |
| [#31](https://github.com/BipoXDXD/stillon/pull/31) | APIerr: 19/19 instruções | `a5919ea98b8cf6f04cd49ad240ced13dd85877aa` | #27 |
| [#32](https://github.com/BipoXDXD/stillon/pull/32) | Avisos da troca, com fixture corrigida herdada | `8bf91abd754563b8fd9ab2a55390f140915354a3` | #34 |
| [#34](https://github.com/BipoXDXD/stillon/pull/34) | Rascunho: sessão testcontainers exclusiva por processo | `43f829f56cb2caf8441dd0d7a47e864469da14a6` | #27 |
| [#35](https://github.com/BipoXDXD/stillon/pull/35) | Rascunho: AES-256/FIPS, cobertura defensiva pendente | `1e6aff9b856f8283fa786070617ba779f9e6d909` | #34 |
| [#36](https://github.com/BipoXDXD/stillon/pull/36) | Fixture web aguarda inicialização da rota | `d44387cc90046223a7fd962e6581fa8eb5c0d0aa` | #34 |
| [#37](https://github.com/BipoXDXD/stillon/pull/37) | Safedial: 83/83 instruções | `0294983345867bdd9a9eac08ff194bee09850271` | #34 |
| [#38](https://github.com/BipoXDXD/stillon/pull/38) | Esquema de alvos: 100% nas quatro métricas do módulo | `2b32c463eabf5f50a07e1a7d26551ff9f334f4f6` | #34 |

#28 usa TLS confiável e HTTP/2 observado no servidor; resposta abaixo do teto
é aceita e acima é recusada. A sabotagem do teto falhou; prober tem 82,8% de
instruções cobertas, sem afirmar 100% global. #30 monta os providers reais;
jsdom verifica propagação do nonce, sem provar enforcement da CSP. #32 preserva
aviso de recuperação consumida e não inventa desativação com `/me` pendente,
sem alterar o protocolo de autenticação.

## CI e perda da fixture Postgres

Snapshot do monitor em 2026-10-05, 03:08 UTC: #27, #18, #30, #31 e #32 têm
todos os nove checks aprovados nos HEADs acima; publish foi pulado nos PRs.

#28 falhou inicialmente no PG17: Restore da fixture falhou, seguido por EOF,
reset e connection refused no HTTPAPI. Prober/HTTP2 passou. Uma única
reexecução do job falho, com HEAD inalterado, passou na tentativa2; HTTPAPI
em 190,749 s. Isso não prova causa resolvida. Tentativa1 permanece registrada.

#29 falhou nas duas pernas PG17/18, apesar do gate local verde. Logs mostram
handshake Ryuk `read ack: EOF`, depois Postgres com SQLSTATE 57P01; em PG18,
Docker confirma que o contêiner já não existe. Os outros sete jobs passaram.
Sem novo rerun. Seus seis testes novos são puros e não controlam Docker/Ryuk.

A fonte confirma sessão compartilhada entre processos do mesmo `go test`,
derivada do PID/data do processo pai. No testcontainers 0.44, o ACK é
assíncrono: Connect pode retornar sucesso antes do EOF registrado. HTTPAPI
não termina contêineres no cleanup; Restore executa SQL. Há hipótese de
lifecycle, sem prova de autor da remoção ou OOM. Jobs não atingiram timeout
nem foram cancelados; limpeza de órfãos veio depois.

Reprodução local com três pacotes confirmou a sequência: B recebe EOF do Ryuk
antigo em shutdown; C cria Ryuk novo para a mesma sessão e termina; o novo
Ryuk remove o Postgres de B ainda ativo. Logs dos dois Ryuk e eventos Docker
mostram SQLSTATE 57P01 e remoção antes do cleanup próprio, sem OOM. Uma barreira
no Docker API do laboratório eliminou a dependência de acertar um atraso.
Isso demonstra o mecanismo local compatível com a falha remota, sem reconstituir
os eventos ausentes dos runners anteriores.

O fix publicado no PR #34 define `TESTCONTAINERS_SESSION_ID` exclusivo por
processo no init da fixture, antes do primeiro Read cached da configuração.
É configuração pública do testcontainers 0.44; mantém paralelismo, Ryuk e
snapshots e não modifica o aplicativo. A reprodução determinística passou
com três sessões/Ryuk distintos; duas sabotagens compiláveis falharam.
Gates locais completos e focados nos PG17/18 passaram. O CI remoto
[37260297774](https://github.com/BipoXDXD/stillon/actions/runs/37260297774)
terminou success no HEAD 43f829f: nove jobs aprovados, publish pulado.

Init cobre 5/6 instruções (83,33%): o panic defensivo de Setenv não é alcançável
com chave fixa e valor válido no Unix. O PR permanece rascunho; aceitar a
exceção à meta de 100% é decisão pendente do dono. Não retirar a proteção
nem falsificar o SO para melhorar a métrica. A base corrigida permite seguir
as demais fases, sem declarar 100% global nem integrar à main.

O #32 recebeu merge assinado do fix e base #34; o diff da fase permaneceu
igual. O #29 também recebeu merge assinado publicado, revisão dos hashes
favorável e focused config na nova base com 146/146 instruções. O novo gate
completo passou antes do push; as falhas antigas de CI ficam preservadas.
Esses novos commits validam uma correção real, sem rerun do mesmo código.
Captura futura de eventos, inspect selecionado e classes Ryuk está em
implementação, sem Env, SQL ou sessão fixa compartilhada do job.

## Fases locais

- Safedial #37: três testes defensivos públicos, 80/83 → 83/83 instruções,
  mesmo denominador, três sabotagens detectadas, gate Go completo sob race
  verde e revisão favorável; novo gate completo na base43 também passou
  antes da publicação. Unix/callback IDNA são fronteiras defensivas,
  não redirects HTTP ponta a ponta; nenhuma política SSRF alterada.
- Ativação com resposta perdida, pendência painel M1: baseada no #32,
  preserva `ATIVACAO_INCERTA` após retry 403. RED e GREEN focados, três
  sabotagens, 276 testes web, typecheck/lint/build e gate Go completo verdes.
  LCOV focused de 60 testes de configuração cobre 1/1 linha e 2/2 ramos
  alterados; inclui 42 fontes, sem apresentar global focused como full.
  Duas medições full instrumentadas falharam no detalhe de alvo e foram
  preservadas. Contagem de avisos passou a usar a região real: anúncio acessível
  extra não é toast extra; mutante de duas notificações reais falhou.
  Sem publicação ainda; avançou para a base atualizada do #32 por FF puro,
  preservando os três arquivos próprios.
- [#17](https://github.com/BipoXDXD/stillon/pull/17): revisão do HEAD
  `c488c7bf766c3ec139289bf9af5afb366006c93a`. Os nove arquivos IDNA em x/net
  0.58/0.59 são idênticos byte a byte. Requisito Go 1.26 cabe no Go 1.26.8.
  Web 245 testes/typecheck/lint/build, módulos verificados e gate Go completo
  sob race verdes. Revisão independente favorável; título/corpo do PR
  atualizados em português, sem novo commit ou CI artificial.
- Teste inicial do detalhe de alvo falhou com router sem página em dois
  baselines; focado passou e segunda execução completa do #17 passou.
  Trace confirmou helper retornando com router.initialized=false e zero
  queries; refetch não aguarda a importação lazy. Correção da fixture foi publicada no #36, preservando boot atrasado e
  StrictMode, sem aumentar timeout. Quatro mutantes compiláveis detectados;
  full web 272 e gate Go completo na base43 passaram. Perfil próprio do helper:
  20/20 linhas, 22/22 instruções, 13/13 ramos, 4/4 funções; diff 9/9 linhas e
  6/6 ramos. O perfil global de produção permanece separado.
- Esquema de alvos #38: 99 testes públicos e 13 sabotagens compiláveis; módulo
  inteiro com 90/90 linhas, 116/116 instruções, 121/121 ramos e 19/19 funções.
  Web completa 368, typecheck/lint/build e gate Go anterior passaram. Após
  FF puro para a fixture corrigida, revisão permaneceu favorável e o novo
  gate completo passou antes da publicação. Sem alteração do produto ou alegação de
  paridade com a guarda de conexão do Go.

- Crypto #35: rejeição de chave AES-192, vetor publicado AES-256-GCM e erro
  público de NewGCM em subprocesso com FIPS-only. Três mutantes compiláveis
  passaram nos testes antigos e falharam nos novos. Gate Go completo base43
  passou, com 21 pacotes e revisão favorável. União dos perfis independentes:
  41/44 instruções (93,18%); perfil normal do CI 40/44. Três instruções
  defensivas mantêm a meta de 100% pendente, explicitamente no rascunho.
- Statuspage segue em revisão final; coletor diagnóstico CI recebeu parecer
  favorável e prepara publicação. Os novos CIs ainda precisam concluir.
  Novas fases não alteram dados nem recursos da VPS.

## Cobertura e continuidade local

Perfil Go agregado anterior: 4.441/5.151 instruções (86,22%). Na fase #30,
42/42 fontes TS/TSX de produção: 1.324/1.355 linhas (97,71%) e 954/1.073 ramos
(88,90%). No #32, painel com 96,84% linhas e 966/1.091 ramos (88,54%).
São árvores diferentes: não somar métricas nem afirmar medição conjunta.

Evidências em `.superpowers/fase-*`, `revisao-*`, `ci-publicacao-2026-10-04`
e `retomada-ci-pr27-2026-10-04.md`. Checkpoints de publicação tiveram aplicação
em clones de recuperação e bytes/modos conferidos. Não reaplicar checkpoints
antigos. Após publicar o [checkpoint documental #33](https://github.com/BipoXDXD/stillon/pull/33),
o principal passou para `docs/continuacao-fases-20261005`, preservando a branch
do #27 e `AGENTS.md` não versionado fora do índice. Instrumentos frontend
temporários foram removidos, sem deriva
dos manifests ou dos pacotes instalados originais.

## Decisões críticas pendentes

Integração dos PRs, validação visual do redesign, deploy/migrações reais,
limpeza/reinstalação da VPS, assinatura/proveniência de imagem, MASTER_KEY,
refresh com resposta perdida, cookies de dispositivo e licença/publicação
pública continuam com o dono. Nenhuma dessas operações foi realizada.
Também permanece pendente aceitar cobertura abaixo de 100% quando um ramo
defensivo não é exercitável pelas APIs reais; não tratar essa exceção como aceita.
VPS Hostinger sem conferência atual; retomar runbook guiado no Linux, com
um passo e seu Confira por vez.
