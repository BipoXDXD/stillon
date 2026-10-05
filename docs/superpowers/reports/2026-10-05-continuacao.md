# Continuidade do StillOn — 2026-10-05

Registro desta retomada, em andamento. Atualiza o estado operacional do
[registro anterior](2026-10-04-continuacao.md), preservando o histórico.
Não houve integração à `main`, deploy ou operação na VPS.

## Publicação após liberação da assinatura

Depois do checkpoint abaixo, o dono autorizou novamente: "pode commitar,
dar push, PR e merge". A assinatura GPG voltou a funcionar; todos os nove
commits desta publicação foram assinados e verificados antes do push.
A autorização permite integração com as lacunas apresentadas preservadas
na documentação; não transforma cobertura parcial em 100% nem autoriza
deploy. Os resultados e bloqueios anteriores abaixo são históricos.

| PR | Fase | HEAD publicado |
| --- | --- | --- |
| [#45](https://github.com/BipoXDXD/stillon/pull/45) | Fixture web independente de #34 | `d776863d01e37e82ef8b990187d86ae53d84d8da` |
| [#46](https://github.com/BipoXDXD/stillon/pull/46) | Formulário de alvo | `5d1d121e77315d2669badc85382209dd1ec68558` |
| [#47](https://github.com/BipoXDXD/stillon/pull/47) | Formatação | `c5262540ed5c7ee37585018e3d8f0258b3b35596` |
| [#48](https://github.com/BipoXDXD/stillon/pull/48) | Porta de sessão | `320bfbaf692e66ee76123808d4de62bfd4c82516` |
| [#49](https://github.com/BipoXDXD/stillon/pull/49) | Espera de navegação | `0a871ffe6126320403079e9024a95c84e0297de0` |
| [#50](https://github.com/BipoXDXD/stillon/pull/50) | Diagnóstico textual de incidentes | `13d3c908643b77bcb8254566dc6c597d91b55128` |
| [#51](https://github.com/BipoXDXD/stillon/pull/51) | Contratos de alvos | `2f8d6fa0f429c08ae113896d5629d78099a48b88` |
| [#52](https://github.com/BipoXDXD/stillon/pull/52) | Limitadores do middleware | `b998327f6192aca5c587f0414ba0ae4f244e0e0b` |
| [#53](https://github.com/BipoXDXD/stillon/pull/53) | Contratos do sondador | `590bfbff201ea9ef998f72babc2d1d9328f9ae3b` |

#46–#53 foram abertos em rascunho enquanto os checks remotos e a nova
combinação completa terminam. A validação anterior da candidata conservadora
não substitui a validação conjunta de todas essas fases. Está em preparação
um clone novo, incluindo os HEADs atuais dos PRs documentais, Dependabot,
fases já publicadas e estes nove commits. Gates web e Go nos Postgres 17 e 18
devem concluir antes da integração final na `main`. Não houve merge ainda.
Evidências de assinatura/publicação: `.superpowers/publicacao-final-2026-10-05/`.

## Orientação vigente na preparação anterior

Na retomada posterior de 2026-10-05, o dono pediu coordenação GPT 6 Astra
Medium com quatro auxiliares GPT 6.1 Sol High, e autorizou commit, push e PR
em todas as fases pendentes. Os quatro auxiliares foram iniciados com o
modelo e esforço pedidos; o modelo/esforço da sessão principal depende da
seleção na interface, sem alteração por ferramenta. Merge na `main` foi
autorizado somente com testes reforçados. A autorização não aceita por si
só as exceções de cobertura descritas neste registro, nem inclui deploy.

Uma verificação de assinatura sem prompt nesta retomada encontrou primeiro
a restrição do sandbox ao agente GPG; fora do sandbox, retornou `No pinentry`.
O dono foi avisado para desbloquear a chave no próprio terminal, sem enviar
segredos no chat. Não houve desativação de assinatura ou novo commit.
Validação e preparação das publicações continuam enquanto isso.

Inventário remoto desta retomada: 22 PRs abertos, sem conflito informado pelo
GitHub; os PRs de código tinham nove checks aprovados e `publish` pulado.
O HEAD documental `74fca2d` já está no PR #33: a referência remota local
estava desatualizada, por isso o `ahead 1` não significava push pendente.
As alterações documentais posteriores continuam locais. Evidências e
parecer em `.superpowers/retomada-publicacao-2026-10-05/`.

As quatro fases web tiveram hashes, estados e checkpoints reconferidos sem
deriva; a porta de sessão recebeu a revisão final que faltava. Formulário,
formatação, porta e espera de navegação têm corpos de PR preparados em
`.superpowers/retomada-fases-web-2026-10-05/`. Não foi necessário repetir
gates dessas árvores inalteradas. A limitação histórica de prova do bundle
do formulário permanece explícita.

Incident Stringer, targets e middleware também têm patches recuperados e
corpos de PR preparados em `.superpowers/retomada-go-2026-10-05/`.
Middleware recebeu revisão final favorável ao escopo test-only, com
cobertura focused de 163/194 para 177/194 instruções (91,24%), mesmos 121
blocos. Targets e middleware permanecem candidatos a rascunho, sem aceite
implícito de cobertura parcial. O sondador tinha falhas de bancada: data
futura recusada pelo Postgres, avanço insuficiente para o jitter e competição
entre Run e Tick manual. A terceira rodada passou a ser despachada somente
pelo Run.
A correção dos testes passou nos cenários focados com race. Oito sabotagens
compiláveis passaram na suíte antiga e falharam nos testes novos; produto
restaurado byte a byte. O gate final executou `go build ./...` (1,541 s),
`go vet ./...` (0,912 s) e `make test` (164,762 s), todos com saída zero;
21 pacotes aprovados e dois sem testes. As 399 entradas do snapshot,
incluindo oito arquivos reais do bundle, ficaram invariantes. Revisão final
favorável à publicação em rascunho: 207/250 para 229/250 instruções (91,60%),
mesmos 166 blocos, com 21 instruções ainda descobertas. As oito fases locais
têm material de publicação preparado; nenhuma foi publicada nesta retomada.

A primeira candidata conservadora reuniu #27, #17, #18, #28, #30 e #31 em
`/tmp/stillon-candidato-main-conservador-20261005`, sem commit de merge e sem
herdar #34. Os HEADs atuais de #25/#26 ficaram fora; os ancestrais documentais
históricos não equivalem aos HEADs atuais. Typecheck/lint/build passaram,
mas o full web falhou (274/275): detalhe do alvo não encontrou o heading
API, com fallback de inicialização na tela. Log e árvore foram preservados;
não houve rerun ou gate Go nessa candidata reprovada. O helper é idêntico
ao da reprodução histórica da corrida de inicialização; sem trace desta
rodada, isso não prova retrospectivamente sua causa exata.

Foi preparada uma candidata nova com apenas o diff de dois arquivos
da fixture #36, sem incorporar seu ancestral #34. Isso não altera o PR
remoto ou aceita exceções de cobertura. Typecheck/lint/build e três testes
focados da prontidão passaram; seu único full web passou 278/278 em 24
arquivos. Snapshots antes/depois ficaram iguais, incluindo cinco MERGE_HEADs
e oito arquivos do bundle. A origem dos dois arquivos e as quatro sabotagens
finais do #36 foram vinculadas por hashes idênticos, distinguindo o RED
histórico da forma inicial do teste. O gate Go dessa combinação concluiu:
`go build ./...`, `go vet ./...` e `make test` com saída zero; `make test`
levou 149,419 s, com 20 pacotes aprovados e três sem testes. Nesta árvore
pré-#34, testdb ainda não tem testes próprios. O snapshot final permaneceu
igual, com 402 hashes, cinco MERGE_HEADs e oito arquivos reais do bundle.
Contêineres persistentes e volumes preexistentes foram preservados; onze
testcontainers novos ainda constavam na leitura imediatamente após o gate.
A leitura posterior às 06:19:41 UTC confirmou que os onze já não estavam
presentes, sem executar stop/rm/prune; os dois inventários foram preservados.
A árvore falha anterior e suas evidências permanecem intactas
em `.superpowers/retomada-candidato-web-2026-10-05/`; a rodada corrigida está
em `.superpowers/retomada-candidato-fixture-web-2026-10-05/`, e o gate Go em
`.superpowers/retomada-publicacao-2026-10-05/candidato-fixture-gates/`.

Alternativa pronta para publicar a fixture sem herdar #34:
`/tmp/stillon-fixture-independente-20261005`, branch
`fix/painel-fixture-independente`, base #27. Somente dois arquivos próprios
do #36 no índice, patch byte idêntico ao commit de origem, recuperação
conferida e MERGE_HEAD ausente. Manifesto/corpo/patch em
`.superpowers/fixture-independente-publicacao-2026-10-05/`. Os gates acima
validam a combinação; não são uma nova execução no clone isolado.
Essa alternativa também aguarda assinatura. O PR #36 original não foi
alterado, fechado ou retargetado. Nenhum merge pode omitir essa correção
e ainda alegar equivalência com a candidata aprovada nos testes.

Próximo passo operacional: após o dono desbloquear o GPG no próprio
terminal, publicar commits assinados das oito fases, da documentação e da
alternativa da fixture, verificando assinatura e destino de cada push.
Novos PRs targets/middleware/prober continuam em rascunho por cobertura
parcial. As exceções de #34/#35/#39 continuam sem resposta explícita;
nenhuma foi aceita por silêncio. Para integração, reconferir HEADs remotos,
revisão final e checks da fixture independente, preservando a combinação
testada e mantendo PRs com exceções fora da `main`.

### Orientação da rodada anterior

O dono se ausentou e autorizou decisões não críticas, testes, commit assinado,
push e PR por fase. Cinco agentes GPT 6.1 Sol High trabalham em responsabilidades
separadas. Decisões críticas ficam pendentes; ações já autorizadas continuam
sem novas perguntas rotineiras.

A meta de 100% exige declarar escopo e denominador. Não excluir fontes, reduzir
limiares ou apresentar cobertura parcial como global. Testes novos receberam
sabotagens compiláveis com falha funcional, restauração, GREEN e revisão.

## Checkpoint local adicional: assinatura pendente

Depois das publicações, uma nova tentativa de assinatura do coordenador
retornou `gpg: signing failed: No pinentry` no wrapper batch sem prompt.
Não houve desbloqueio, leitura/pedido de senha, repetição de assinatura ou
desativação da assinatura. Novos commits do coordenador ficam pendentes;
testes, revisão e checkpoints continuam. O minigráfico já tinha commit
assinado/verificado e push antes da comunicação do bloqueio, então seu PR
#44 foi concluído sem nova assinatura. Não inferir que ele foi publicado
sem assinatura nem que o erro foi resolvido.

Este documento atualizado está salvo também no checkout principal, sem
commit após o HEAD documental `74fca2db977f66a6b98c669baa1bdfb1e9f79bca`.
A branch continua `docs/continuacao-fases-20261005`; `AGENTS.md` permanece
não versionado e fora do índice. O checkpoint recuperável é local e não
substitui um push. O dono poderá desbloquear o GPG no próprio terminal ao
retornar; nenhum segredo deve ir para o chat.

Bancada conjunta em `/tmp/stillon-validacao-conjunta-20261005`: doze HEADs
assinados/publicados reunidos em merges locais, sem conflitos; HEAD
`354a2f0abadb6b4ae310c6729b06a83654d81520`. Tentativa de merge assinado
de #43 não criou commit e foi abortada de forma reversível. Testes já
assinados/publicados de #43/#44 foram aplicados como patches apenas na
árvore de trabalho; MERGE_HEAD está ausente. O snapshot de trabalho
congelado inclui índice, status e hashes completos dos testes novos,
com proveniência verificada. Também inclui o teste local de FormularioAlvo,
revisado e ainda sem publicação, com proveniência distinta dos PRs publicados.
São 407 arquivos de fonte, índice sem conflitos e oito arquivos reais do bundle.
Desenho e revisão permitem verificação local sem commit. Os gates Go e web e
os perfis conjuntos passaram nessa árvore; detalhes e limites abaixo.
Não publicar essa branch nem afirmar integração na main.

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
| [#39](https://github.com/BipoXDXD/stillon/pull/39) | Rascunho: statuspage, 82/83 ramos | `08220d326b136a615a905e00e91e7b8d5ac2db47` | #34 |
| [#40](https://github.com/BipoXDXD/stillon/pull/40) | Diagnóstico limitado das fixtures do CI | `079fad2bb3c3f4bb0b93d27504b3edd419ed217f` | #34 |
| [#41](https://github.com/BipoXDXD/stillon/pull/41) | Histórico de incidentes: 23/23 instruções | `57f8c344b997ac6f7c14ba18041b2ede1a283551` | #34 |
| [#42](https://github.com/BipoXDXD/stillon/pull/42) | Aviso de ativação incerta: 1/1 linha e 2/2 ramos alterados | `6480aec4a7bea5e7076a3e4185d10ec446d2c4b5` | #32 |
| [#43](https://github.com/BipoXDXD/stillon/pull/43) | useCopiar: 100% nas quatro métricas do módulo | `bedb602df13a0e12a007368a0cc36a9d57e1f43c` | #36 |
| [#44](https://github.com/BipoXDXD/stillon/pull/44) | Minigrafico: 100% nas quatro métricas do módulo | `e6120bb377d0042b6ed1081c9f340ec5544e5117` | #36 |

#28 usa TLS confiável e HTTP/2 observado no servidor; resposta abaixo do teto
é aceita e acima é recusada. A sabotagem do teto falhou; prober tem 82,8% de
instruções cobertas, sem afirmar 100% global. #30 monta os providers reais;
jsdom verifica propagação do nonce, sem provar enforcement da CSP. #32 preserva
aviso de recuperação consumida e não inventa desativação com `/me` pendente,
sem alterar o protocolo de autenticação.

## CI e perda da fixture Postgres

Snapshot do monitor em 2026-10-05, 03:08 UTC: #27, #18, #30, #31 e #32 têm
todos os nove checks aprovados nos HEADs originais daquela rodada (#32 ainda
842fec8); publish foi pulado nos PRs. Nas consultas de 04:17 e 04:19 UTC,
#29 e #32 atualizados e #34–#38 passaram os nove jobs. #39–#42 ainda estavam
em curso às 04:21 UTC, sem falha observada. Nas consultas posteriores, até
04:29 UTC, todos #29/#32/#34–#42 concluíram com nove jobs aprovados e publish
pulado. #43 também concluiu com nove jobs aprovados às 04:38 UTC; #44 concluiu
com nove jobs aprovados às 04:51:45 UTC, run 37264629429. Runs cancelados automaticamente
pela concorrência de push/retarget em #29/#32 ficam separados de rerun humano.

#28 falhou inicialmente no PG17: Restore da fixture falhou, seguido por EOF,
reset e connection refused no HTTPAPI. Prober/HTTP2 passou. Uma única
reexecução do job falho, com HEAD inalterado, passou na tentativa2; HTTPAPI
em 190,749 s. Isso não prova causa resolvida. Tentativa1 permanece registrada.

#29 no HEAD antigo 987ab2e falhou nas duas pernas PG17/18, apesar do gate local verde. Logs mostram
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

Os itens abaixo preservam a rodada anterior. As conclusões posteriores
de porta de sessão, middleware e sondador estão em **Orientação vigente**,
no início deste documento; os estados históricos "em andamento" não
representam seu estado final nesta retomada.

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
  Publicado no #42 após FF para a base atualizada do #32, novo gate Go
  completo e revisão reconfirmada; três arquivos próprios preservados.
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
- Statuspage #39 em rascunho: 25 casos novos, 16 mutantes compiláveis,
  web completa 294, typecheck/lint/build e Go completo base43 passaram.
  Módulo com 77/77 linhas, 91/91 instruções e 27/27 funções; 82/83 ramos
  (98,79%). O fallback refnull não foi atingido por navegação/remoção pública;
  isso não prova impossibilidade absoluta. Sem manipular ref privado ou
  excluir o ramo. Exceção à meta de 100% pendente. Primeiro push teve rejeição
  automática de aprovação; prova do destino, permissão ADMIN e payload
  restrito resolveu a publicação sem force, segredo ou bloqueio restante.
- Coletor #40: 33 testes, oito sabotagens funcionais compiláveis e 376/376
  linhas executáveis. Essa métrica não representa ramos ou workflow remoto.
  Registry prévio em laboratório real coletou apenas CID próprio e cleanup
  foi confirmado. Gate Go completo base43 e checks estáticos passaram.
  Captura só eventos, inspect selecionado e classes Ryuk; não grava PG/SQL/Env
  ou input bruto, não opera contêineres e preserva o exit dos testes. Upload
  de quatro caminhos com retenção de três dias. A validação remota posterior
  confirmou os dois ZIPs PG17/18: digests coincidem com a API, quatro arquivos
  por ZIP, 24 CIDs/12 sessões e campos restritos; revisão independente favorável.
  Prova em `.superpowers/ci-publicacao-2026-10-05/<HEAD40>/upload-remoto.md`.
  Coleta sempre declara collectionComplete=false, por seus limites.
- Histórico de incidentes #41: dois testes públicos de erros List/Ack e
  recuperação em Postgres próprio. Quatro mutantes compiláveis passaram
  nos antigos e falharam nos novos. 21/23 → 23/23 instruções, mesmos 17 blocos,
  sem alegar 100% de ramos/global. Go completo base43 sob race e revisão
  favorável; quatro recursos persistentes preexistentes preservados. PR
  foi publicado em rascunho e marcado pronto para revisão após CI verde, sem
  exceção própria de cobertura. Isso não integra o PR ou aceita exceção do #34.
- useCopiar #43: dois casos novos e quatro mutantes compiláveis; focused
  de 14 casos inclui controles existentes, módulo com 14/14 linhas, 18/18
  instruções, 7/7 funções e 4/4 ramos. Web completa 274, Go completo base36,
  typecheck/lint/build e revisão passaram; provider temporário removido.
  Ausência da API clipboard é exercício público, sem alegar contexto seguro
  ou funcionamento nativo do clipboard no jsdom.
- Minigrafico #44 publicado após revisão final. Baseline de
  Minigrafico detectou navegação posterior ao cadastro ainda não concluída;
  trace controlado demonstrou esse intervalo, sem atribuir causa exata da
  falha natural ou aumentar timeout. O único full normal final com seis casos
  novos passou 278/278; o full observado também passou. Go completo e perfil
  do módulo 100% receberam parecer favorável antes da publicação.
  Trabalho de produto permanece separado.
- FormularioAlvo local: dois casos públicos de edição de headers e envelope
  de erro com campo desconhecido, cinco sabotagens compiláveis detectadas.
  Módulo com 43/43 linhas, 46/46 instruções, 20/20 funções e 36/36 ramos.
  Web completa 274, typecheck/lint/build, Go completo e revisão favorável.
  Patch recuperável em `.superpowers/fase-formulario-alvo-cobertura-2026-10-05`;
  commit, push e PR pendentes da assinatura GPG.
- Targets local: contratos públicos de headers, JSON autenticado inválido,
  erros reais do Postgres e cancelamento entre consultas. Gate Go completo
  passou. Perfil agregado comparável: 296/324 → 319/324 instruções (98,46%),
  mesmos 245 blocos; focused isolado 291/324 → 318/324. O erro da trava já é
  coberto por outro consumidor no agregado. Cinco instruções defensivas
  permanecem descobertas; não excluir denominador ou declarar 100%.
  Revisão final favorável à entrega local e checkpoint com recuperação
  conferida. Meta de 100% e publicação permanecem pendentes.
- Espera de navegação local: usa caminho esperado e estado idle pelas APIs
  públicas do router, com cancelamento e remoção das inscrições. Quatro casos
  novos e sete sabotagens compiláveis; full web 276 e perfil próprio 100%
  (22 linhas, 28 instruções, seis funções, 14 ramos). Gate Go completo passou,
  com oito arquivos reais do bundle preservados; revisão final favorável.
  Sem aumentar timeout ou alterar o produto; assinatura, commit, push e PR
  pendentes.
- Incident Stringer local: ações reais de Evaluate observadas pela interface
  pública de representação textual, com resultados literais none/open/close.
  Pacote com 20/24 → 24/24 instruções, mesmos 17 blocos, três sabotagens
  compiláveis detectadas. Gate Go completo passou; fontes, manifests e oito
  arquivos do bundle preservados. É contrato diagnóstico, sem alteração de
  eventos/outbox. Recuperação do patch conferida e revisão final favorável;
  publicação pendente de GPG.
- Formatação local: 27 casos novos, 12 sabotagens compiláveis e full normal
  e V8 com 299 testes aprovados. Módulo inteiro em perfil full com 24/24 linhas,
  31/31 instruções, 8/8 funções e 43/43 ramos. Gate Go completo passou, com
  bundle real preservado; recuperação conferida e parecer final favorável.
  Medição focused inicial parcial foi preservada. Publicação pendente de GPG.
- Porta de sessão local: três contratos públicos de recuperação após falha
  de rede, cinco sabotagens compiláveis, full web 275 aprovado e módulo inteiro
  com 12/12 linhas, 12/12 instruções, 2/2 funções e 8/8 ramos. Gate Go e parecer
  final em andamento. Middleware e contratos públicos do sondador têm desenhos
  revisados e testes em execução. Nenhuma dessas fases está publicada.
- Segunda bancada conjunta somente em preparação, em outro clone: a primeira
  árvore congelada e seus resultados permanecem intactos. Só entram patches
  finais, revisados e recuperados; novo congelamento/revisão antes de gates.
  Não somar métricas ou declarar a segunda rodada executada.

## Cobertura e continuidade local

Perfil Go agregado anterior: 4.441/5.151 instruções (86,22%). Na fase #30,
42/42 fontes TS/TSX de produção: 1.324/1.355 linhas (97,71%) e 954/1.073 ramos
(88,90%). No #32, painel com 96,84% linhas e 966/1.091 ramos (88,54%).
São árvores diferentes: não somar métricas nem afirmar medição conjunta.

Na bancada congelada HEAD `354a2f0`, com os três testes locais explicitados,
`go build ./...`, `go vet ./...` e `make test` passaram: 21 pacotes testados
e dois sem testes. O perfil global sob race, com deduplicação por arquivo,
intervalo e peso e união dos hits, mede 4.467/5.157 instruções (86,62%):
690 descobertas em 3.489 blocos únicos. Os 70.080 registros duplicados não
entram duas vezes no denominador. Perfis crypto normal e subprocesso FIPS
permanecem separados: 40/44 e 6/44; isso não significa 100% global.

Web conjunta: full normal e full V8 passaram separadamente, ambos 419/419
testes em 29 arquivos. Typecheck/lint/build passaram antes do congelamento.
Todas as 42 fontes de produção entram no perfil full: 1.344/1.364 linhas
(98,53%), 1.520/1.559 instruções (97,49%), 432/448 funções (96,42%) e
1.028/1.093 ramos (94,05%). Fontes, índice/status, manifests e oito arquivos
reais do bundle ficaram invariantes; provider temporário removido e árvore
original de pacotes restaurada. Recontagem independente e parecer final
favoráveis à coexistência local; não representam aceite das exceções à meta.

Auditoria histórica do bundle: runners Go de statuspage #39, useCopiar #43 e
FormularioAlvo enumeravam `web/dist`, caminho incorreto. Seus gates verdes
continuam registrados, mas aqueles snapshots não provam diretamente a
invariância do bundle durante Go. Os checkpoints/relatórios autorais e corpos
publicados de #39/#43 foram qualificados, com releitura após edição. FormularioAlvo
tem corpo preparado qualificado, ainda sem PR. Snapshots V8 separados têm oito arquivos
reais em outro intervalo. M1 #42 e esquema #38 têm pares separados corretos;
avisos da troca #32 não tem par direto localizado. Nada foi reescrito nas
evidências antigas. A bancada atual confere `internal/webui/dist` diretamente.

Inventário Docker conjunto: os dez contêineres persistentes não-testcontainers
do início continuaram com IDs/estado preservados. Fixtures transitórias de
outros trabalhos e de contratos de snapshot têm delta qualificado. Um volume
anônimo preexistente ficou ausente sem autoria confirmada; não afirmar 100%
de preservação dos volumes nem atribuir limpeza a um agente sem prova.
Nenhum comando de limpeza global foi executado pelos runners desta bancada.

Evidências em `.superpowers/fase-*`, `revisao-*`, `ci-publicacao-2026-10-04`
e `retomada-ci-pr27-2026-10-04.md`. Checkpoints de publicação tiveram aplicação
em clones de recuperação e bytes/modos conferidos. Não reaplicar checkpoints
antigos. Após publicar o [checkpoint documental #33](https://github.com/BipoXDXD/stillon/pull/33),
o principal passou para `docs/continuacao-fases-20261005`, preservando a branch
do #27 e `AGENTS.md` não versionado fora do índice. Instrumentos frontend
temporários foram removidos, sem deriva
dos manifests ou dos pacotes instalados originais.

## Decisões críticas pendentes

Integração dos PRs agora está autorizada, condicionada a testes reforçados,
revisão e publicação assinada dos ajustes necessários. Não houve merge
nesta retomada. Validação visual do redesign, deploy/migrações reais,
limpeza/reinstalação da VPS, assinatura/proveniência de imagem, MASTER_KEY,
refresh com resposta perdida, cookies de dispositivo e licença/publicação
pública continuam com o dono. Nenhuma dessas operações foi realizada.
Também permanece pendente aceitar cobertura abaixo de 100% quando um ramo
defensivo não é exercitável pelas APIs reais; não tratar essa exceção como aceita.
VPS Hostinger sem conferência atual; retomar runbook guiado no Linux, com
um passo e seu Confira por vez.
