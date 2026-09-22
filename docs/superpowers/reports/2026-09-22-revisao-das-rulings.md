# Revisão das rulings R1–R40 — Plano 1

Estas são as 40 decisões que tomei no lugar do usuário durante a execução do Plano 1. O ledger
reservava a entrega desta lista para o fim, porque é o único lugar onde essas decisões chegam
até ele. Esta revisão vai além da lista: cada ruling foi reavaliada contra o que a revisão
ampla do branch, o relatório de cobertura, a verificação dos 30 itens e a onda de correção
estabeleceram depois — ou seja, contra evidência que eu não tinha quando decidi.

**Veredito geral: duas rulings estavam erradas, uma estava pela metade, uma nunca foi
escrita, e 35 se sustentaram.** As duas erradas eram de segurança, e as duas já foram
corrigidas em código nesta sessão.

---

## 1. As que estavam ERRADAS

### R35 — a premissa não valia em produção. CORRIGIDA.

**O que eu decidi:** que `LockThreshold=5` contra o burst 5 do limitador não era defeito,
porque "o limitador é por IP e o bloqueio é por conta", e que a ordem tinha uma propriedade
boa: o rate limit dispara antes do handler, então um atacante remoto não consegue trancar a
conta de uma vítima martelando senhas.

**Por que estava errada:** o raciocínio inteiro se apoia em "por IP", e isso **não era verdade
em produção**. A spec §15 escolhe Fly.io, onde todo tráfego passa pelo proxy da plataforma, e
o código lia `r.RemoteAddr` cru. Medido: 20 clientes distintos atrás do mesmo proxy, 5 passam e
15 levam 429 — todos dividiam um balde só. Com balde único, "por IP" deixa de existir e a
proteção anti-DoS-de-lockout que eu declarei se inverte: um atacante sozinho consome o balde de
todo mundo.

O erro não foi de lógica, foi de **não ter verificado o ambiente de deploy contra o código**.
A ruling é internamente coerente e falsa no mundo.

**Estado:** corrigida no commit `215ee2e` (item 21). Com `TRUSTED_PROXIES` preenchido, a
premissa volta a valer e a R35 volta a ser verdadeira.
**Pendência que não é código:** `TRUSTED_PROXIES` precisa entrar na configuração do Fly.io.
Enquanto não entrar, a R35 continua falsa em produção.

### R38 — separou mal "ligar" de "desligar". CORRIGIDA.

**O que eu decidi:** que `Enroll` recusaria quando `totp_confirmed_at IS NOT NULL`, escolhendo
entre três alternativas. Descartei "re-autenticar por senha" com a justificativa de que era
"meia proteção (não cobre token roubado sem senha)".

**Por que estava pela metade:** essa frase é **verdadeira para DESLIGAR e falsa para LIGAR**, e
eu não separei os dois casos. Ao fechar só o desligamento, deixei aberto o espelho: um access
token roubado **ligava** o segundo fator com o autenticador do ladrão. E a própria R38 tornou
isso pior, porque removeu o único caminho de volta — não havia rota de desativação, e a guarda
do enroll barrava até a vítima legítima.

Provado ponta a ponta contra Postgres real: conta sem 2FA, atacante só com access token, enroll
200 + confirm 200 com o segredo dele, os dez códigos de recuperação indo para o atacante, e a
vítima **com a senha correta** passando a receber `totp_required` e nenhum token. Probes de
desativação: 404 nos três.

Ou seja: a R38 trocou "escalar sessão→conta desligando o fator" por "escalar sessão→conta
ligando o fator", e a segunda versão é **permanente**.

**Estado:** corrigida no commit `c6ca4cf` (item 20). O enroll passou a exigir a senha, e a rota
`POST /auth/totp/disable` existe, com senha + posse (código do app ou de recuperação).
**Efeito colateral bom:** a "consequência aceita e registrada" da R38 — não haver caminho para
trocar de autenticador — **deixou de existir**. Quem perde o telefone desativa por código de
recuperação e recadastra. A pendência do Plano 5 encolheu de "desenhar substituir fator" para
"dar um fluxo dedicado ao que já é possível".

### R5 — a justificativa estava parcialmente errada.

**O que eu decidi:** aceitar que `safedial` não tem consumidor no Plano 1, com a justificativa
de que "ele é exercido pelos próprios testes, incluindo o de ponta a ponta".

**O que estava errado:** o relatório de cobertura mostrou que **uma guarda de segurança não era
exercida por teste nenhum** — `AllowIP` com `netip.Addr` zerado. Sabotagem: desligada a guarda,
a suíte inteira ficava verde. Sem ela, o endereço zerado não casa com prefixo nenhum e `AllowIP`
devolve `nil`, isto é, **permite**.

A conclusão da R5 (aceitar a antecipação) continua certa; a justificativa era forte demais.
**Estado:** guarda travada no commit `0c3a467` (item 17). O pacote segue sem consumidor —
verificado agora: nenhum arquivo de produção importa `prober/safedial`.

### R37 — nunca foi escrita.

É citada no item 5 da fila de correção como "(decisão minha, ruling R37)", mas **não existe
linha `Ruling R37:` no ledger**. A decisão que ela representava — não zerar `failed_attempts`
quando o bloqueio vence — acabou sendo revertida como defeito: a verificação provou DoS de
conta permanente (96 requisições por dia trancam a vítima para sempre), e foi corrigida no
commit `cc47f8b`.

É uma falha de registro minha, e vale como lição: uma ruling não escrita é uma decisão que
ninguém pode auditar. Esta foi a única das 40 que se perdeu.

---

## 2. As que a onda VALIDOU de forma forte

Estas não só se sustentaram: a onda produziu evidência nova a favor delas.

**R33 (`Vary: Origin` incondicional)** — a validação mais bonita. A correção "óbvia" do item 24
seria trocar duas linhas em `routes.go` para o limitador pegar o preflight. Medido: isso faria
**toda** resposta 429 perder `Access-Control-Allow-Origin` e `Vary: Origin`. A R33 previu
exatamente esse modo de falha um ano-máquina antes de ele aparecer, e foi o que obrigou a
correção em duas peças.

**R34 (o bloqueio não se anuncia)** — o revisor do branch a chamou de "achado mais valioso da
execução inteira", e a verificação mostrou que o **plano** ainda carregava a versão pré-R34 em
três linhas. Quem reconstruísse o handler a partir dele reintroduziria a enumeração de contas.
Corrigido no `cc47f8b`.

**A família R6 / R9 / R13 / R25 / R30 / R31 (teste vacuamente verde)** — a onda encontrou
**mais quatro** casos da mesma classe (itens 16, 17, 18 e 19), incluindo o wiring inteiro de
`routes.go`. O padrão que a R6 nomeou era real e estava **subcontado**: não foram nove
ocorrências, foram treze.

**R24 (implementador pode fortalecer teste demonstravelmente fraco sem pedir licença)** — a
mais rentável das 40. Rendeu o bypass de 2FA achado pelo implementador na Task 9, a derrota da
minha própria R40, e nesta sessão o agente que descobriu que **o snippet que eu prescrevi para
o item 27 estava errado**. O custo que eu temia ("implementadores alargando tabela de teste sem
supervisão") não se materializou nenhuma vez.

**R26 (recusar hook test-only em código de produção)** — vindicada pelo item 4: o teste de
tempo constante foi endurecido medindo a referência duas vezes e usando a menor, **sem** costura
no código de produção. A alternativa que recusei continuava sendo a pior.

**R8 (AAD amarrada à coluna)** e **R10 (`Secret` implementa `fmt.Formatter`)** — as duas eram
argumentos de assimetria de custo, e as duas se pagaram. O AAD foi reusado sem alteração pelo
`Disable` novo; e o item 18 mostrou que `String`/`GoString` eram inalcançáveis via `fmt`
**precisamente porque** o `Format` intercepta a classe inteira de verbos.

**R40 (falha de segundo fator conta para o bloqueio)** — validada, e com uma nota humilhante
que merece ficar: o implementador **derrotou a minha própria ruling** ao notar que o `Login`
chamava `ResetFailures` antes do desvio do segundo fator, o que zeraria o contador entre um
chute e o próximo. A ruling como escrita não seguraria nada.

**R23 (sabotagem prova ausência dos bypasses que alguém listou, não ausência de bypass)** —
validada pelo relatório de cobertura, que achou guardas que nenhuma lista de sabotagem havia
incluído.

**R28 (logout idempotente)** — sobreviveu intacta à reescrita do `Revoke` no item 22.

**R32 (contexto no `RateLimit` por assimetria de custo)** — a assinatura mudou de novo nesta
onda, ganhando a lista de proxies confiáveis. O raciocínio ("antes custa um parâmetro, depois
custa editar quem chama") se confirmou, com a ressalva honesta de que ela acabou tendo de ser
editada de novo mesmo assim.

**R1 (pacote `apierr`)** — a onda dependeu dela sem atrito: `CodePayloadTooLarge` do item 27
mora lá.

---

## 3. Pendências que rulings deixaram agendadas e continuam abertas

- **R15** — `TestTransportDoesNotReuseConnections` ainda verifica configuração, não
  comportamento. Verificado agora: continua assim. O teste comportamental exige lista de
  bloqueio injetável, e o sondador do Plano 2 precisa dela de qualquer forma.
- **R22** — faixas de transição IPv6 (6to4 `2002::/16`, Teredo `2001::/32`, NAT64
  `64:ff9b::/96`) seguem fora da lista de bloqueio. As quatro razões para não corrigir na
  Task 4 continuam válidas, e a primeira delas — "o pacote não tem consumidor" — foi
  reverificada agora e segue verdadeira.
- **R2** — `TOTP` continua acessando o `*pgxpool.Pool` direto. O custo previsto ("um lugar a
  mais para tocar na migração para sqlc") **cresceu**: o `Disable` novo acrescentou mais um
  uso. Ainda é a decisão certa, mas a dívida é maior do que quando foi contraída.
- **R23** — a revisita de semânticas incomuns de `netip` antes do sondador do Plano 2.

---

## 4. Tabela completa: as 40, com o custo de estar errada

| # | Decisão | Custo se errada | Veredito |
|---|---|---|---|
| R1 | Extrair envelope de erro para `internal/apierr` (ciclo de importação) | Um pacote a mais e rename em ~30 chamadas | Validada |
| R2 | TOTP acessa o pool direto, sem passar por `Store` | Um lugar a mais na migração para sqlc | De pé, dívida cresceu |
| R3 | T9 atualiza o helper `newAPI` da T8 | Nenhum — sem isso não compila | Consumida |
| R4 | Manter o tratamento de erro original em `ParseAccessToken` | Um caminho de erro perdido, que o teste pegaria | Consumida |
| R5 | Aceitar `safedial` sem consumidor no Plano 1 | Nenhum; o consumidor chega no Plano 2 | **Justificativa parcialmente errada** |
| R6 | Asserção positiva de `[REDACTED]` nos testes de `Secret` | Nenhum; só torna o teste mais estrito | Validada e subcontada |
| R7 | `HashIP` entra em panic com chave vazia | Um panic em caminho que deveria degradar | De pé |
| R8 | `AAD` amarra também à coluna | Um parâmetro a mais em 3 chamadas | Validada |
| R9 | Promover 4 achados de "teste sem força" a Importante | Quatro testes a mais que ninguém pediu | Validada |
| R10 | `Secret` implementa `fmt.Formatter` | Um método a mais | Validada |
| R11 | `id[:]` em vez de cópia redundante | Nenhum | De pé |
| R12 | Aceitar `const nonceSize = 12` | Uma constante a acompanhar se o AEAD mudasse | De pé |
| R13 | Apagar teste de redirect que não seguia redirect | Um teste a menos, cujo valor já era zero | Validada |
| R14 | Manter teste aditivo que o implementador criou | Nenhum | Validada |
| R15 | Diferir teste comportamental de não-reuso de conexão | Garantia fica só na configuração até o Plano 2 | **Aberta, Plano 2** |
| R16 | Recusar zona IPv6 (fail-closed) em vez de normalizar | Alvo legítimo com zona recusado — não existe | De pé |
| R17 | Promover a Importante as 6 faixas sem caso que mordesse | Seis linhas de tabela a mais | Validada |
| R18 | Exigir `errors.Is` nas asserções de `safedial` | Baixo | De pé |
| R19 | `>` em vez de `>=` em `maxRedirects` | Um salto a mais, todos revalidados | De pé |
| R20 | Acrescentar `errors.Is(ErrBlocked)` ao teste renomeado | Baixo | De pé |
| R21 | Manter `255.255.255.255/32` com comentário de inalcançável | Nenhum | De pé |
| R22 | NÃO corrigir faixas de transição IPv6 na Task 4 | Alvo hostil alcança interno se houver 6to4/NAT64 — exige consumidor, que não existe | **Aberta, Plano 2** |
| R23 | Registrar o limite epistêmico da sabotagem | Nenhum | Validada |
| R24 | Implementador pode fortalecer teste fraco sem pedir licença | Tabelas de teste alargadas sem supervisão | **A mais rentável das 40** |
| R25 | Dois casos novos para os disjuntos `len(salt)`/`len(sum)` | Baixo | Validada |
| R26 | RECUSAR hook test-only para `VerifyDummy` | `VerifyDummy` regride sem teste acusar | Vindicada |
| R27 | Validar `ipKey` com panic em `NewManager` | Um panic na partida em vez de erro retornado | De pé |
| R28 | `Revoke` de token inexistente devolve nil | Baixo | Validada, sobreviveu ao item 22 |
| R29 | Diferir o `// indirect` do jwt para o tidy final | Nenhum | **Resolvida como previsto** |
| R30 | Manter `WithValidMethods` e testar confusão de algoritmo | Um teste a mais | Validada |
| R31 | Promover a Importante a falta de trava do `token_hash` | Um teste a mais | Validada |
| R32 | Corrigir o vazamento de goroutine do `time.Tick` agora | Um parâmetro a mais em 2 chamadas | Validada |
| R33 | `Vary: Origin` incondicional | Um header a mais em respostas sem CORS | **Validada de forma exemplar** |
| R34 | Conta bloqueada devolve o mesmo 401 genérico | Usuário legítimo sem explicação por 15 min | Validada; o plano estava fora de sincronia |
| R35 | `LockThreshold` vs burst não é defeito; o limite é por IP | — | **ERRADA em produção. Corrigida.** |
| R36 | Promover a Importante o fluxo de reúso não percorrido | Um teste de integração a mais | Validada |
| R37 | (não zerar `failed_attempts` no vencimento) | — | **NUNCA ESCRITA. Revertida como defeito.** |
| R38 | `Enroll` recusa quando já confirmado | Dispositivo perdido exige tratamento fora de banda | **PELA METADE. Corrigida.** |
| R39 | Auditar `totp.enabled` e `totp.recovery_used` | Baixo | Validada e estendida (`totp.disabled`) |
| R40 | Falha de segundo fator conta para o bloqueio | Cinco linhas e um caminho de bloqueio novo | Validada; o implementador a corrigiu |

---

## 5. O que eu tiraria disso

Das 40 decisões, as duas que deram errado têm a **mesma assinatura**: raciocínio internamente
coerente sobre uma premissa que eu não verifiquei. A R35 supôs "por IP" sem conferir o deploy;
a R38 supôs que "prova de posse da senha" tinha o mesmo valor nos dois sentidos sem separar os
casos. Nenhuma das duas foi erro de lógica.

As que mais renderam foram as de **processo**, não as técnicas: a R24 (deixar o implementador
fortalecer teste fraco) produziu mais segurança que qualquer decisão de código isolada, porque
foi ela que permitiu o bypass de 2FA ser achado, a R40 ser corrigida contra mim, e o meu
próprio snippet errado do item 27 ser descoberto por quem executou.

E a R37 é o lembrete mais barato: **decisão não escrita é decisão que ninguém pode revisar.**
Foi a única que se perdeu, e foi também a única que acabou sendo revertida como defeito.
