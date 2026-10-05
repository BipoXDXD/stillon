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

As fases novas usam o PR #27 como base para manter diffs pequenos. #18 segue
na branch original do Dependabot. Todos os commits da tabela foram assinados
e verificados com `git verify-commit`. A credencial já estava liberada no agente
GPG: modo batch sem prompt, segredo no chat ou desativação da assinatura.

| PR | Resultado | HEAD publicado | Base |
| --- | --- | --- | --- |
| [#27](https://github.com/BipoXDXD/stillon/pull/27) | Guards shell e stdout da fixture Docker | `28159eaa2381f21b2326fe0d607b28ce7f7a0b1e` | main |
| [#18](https://github.com/BipoXDXD/stillon/pull/18) | Tipos Node 24, mantendo TypeScript do Dependabot | `54d255a5354036ac7517c188d1a84b98c0136189` | main |
| [#28](https://github.com/BipoXDXD/stillon/pull/28) | Teto real de cabeçalhos HTTP/2 | `a89e210d40ea9a33004ea02b33343eee46071fba` | #27 |
| [#29](https://github.com/BipoXDXD/stillon/pull/29) | Config: 146/146 instruções | `987ab2e72997724927166ae6c6128c6fc8f28a5f` | #27 |
| [#30](https://github.com/BipoXDXD/stillon/pull/30) | Bootstrap, nonce e API: 100% linhas dos três módulos | `6fff38eae22b3cbb121507576e7a70d6ae7ef878` | #27 |
| [#31](https://github.com/BipoXDXD/stillon/pull/31) | APIerr: 19/19 instruções | `a5919ea98b8cf6f04cd49ad240ced13dd85877aa` | #27 |
| [#32](https://github.com/BipoXDXD/stillon/pull/32) | Avisos da troca: 10/10 linhas e 18/18 ramos alterados | `842fec8ba091d0baf2e24636920686f73c9b4682` | #27 |

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

O fix local em preparação define `TESTCONTAINERS_SESSION_ID` exclusivo por
processo no init da fixture, antes do primeiro Read cached da configuração.
É configuração pública do testcontainers 0.44, validada em laboratório;
mantém paralelismo/Ryuk/snapshots e não modifica o aplicativo. A mesma reprodução
determinística que falhava passou com três sessões/Ryuk distintos; duas
sabotagens compiláveis da identidade dos processos falharam. Init cobre 5/6
instruções (83,33%): o panic defensivo de Setenv não é alcançável com chave fixa
e valor válido no Unix. A exceção à meta de 100% fica pendente; não remover a
proteção nem falsificar o SO para melhorar a métrica. Gates finais ainda
precisam concluir; essa correção poderá ser publicada como rascunho, sem
afirmar o critério de cobertura satisfeito. Novas publicações de código
ficam suspensas até então; trabalho local continua. Não repetir CI para
esconder instabilidade. Captura futura de eventos/inspect selecionado e classes
Ryuk está em implementação, sem Env, SQL ou sessão fixa compartilhada do job.

## Fases locais

- Safedial: três testes defensivos públicos, 80/83 → 83/83 instruções,
  mesmo denominador, três sabotagens detectadas, gate Go completo sob race
  verde e revisão favorável. Unix/callback IDNA são fronteiras defensivas,
  não redirects HTTP ponta a ponta; nenhuma política SSRF alterada.
- Ativação com resposta perdida, pendência painel M1: baseada no #32,
  preserva `ATIVACAO_INCERTA` após retry 403. RED e GREEN focados, três
  sabotagens, 276 testes web, typecheck/lint/build e gate Go completo verdes.
  LCOV focused de 60 testes de configuração cobre 1/1 linha e 2/2 ramos
  alterados; inclui 42 fontes, sem apresentar global focused como full.
  Duas medições full instrumentadas falharam no detalhe de alvo e foram
  preservadas. Contagem de avisos passou a usar a região real: anúncio acessível
  extra não é toast extra; mutante de duas notificações reais falhou.
  Sem publicação ainda.
- [#17](https://github.com/BipoXDXD/stillon/pull/17): revisão do HEAD
  `c488c7bf766c3ec139289bf9af5afb366006c93a`. Os nove arquivos IDNA em x/net
  0.58/0.59 são idênticos byte a byte. Requisito Go 1.26 cabe no Go 1.26.8.
  Web 245 testes/typecheck/lint/build, módulos verificados e gate Go completo
  sob race verdes. Revisão independente favorável; título/corpo do PR
  atualizados em português, sem novo commit ou CI artificial.
- Teste inicial do detalhe de alvo falhou com router sem página em dois
  baselines; focado passou e segunda execução completa do #17 passou.
  Trace confirmou helper retornando com router.initialized=false e zero
  queries; refetch não aguarda a importação lazy. Correção da fixture está
  em preparação, preservando boot atrasado e StrictMode, sem aumentar timeout.
- Esquema de alvos: desenho de testes complementares exportados em andamento,
  sem alterar validação de produto nem afirmar paridade com a guarda do Go.

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
