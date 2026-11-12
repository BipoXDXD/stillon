# ADR 0002: limite por conta nas rotas autenticadas do 2FA e na troca de senha

- **Status:** Aceito (opção B da análise da issue #335)
- **Data:** 2026-11-12
- **Origem:** issue #335; revisão do PR #327
- **Afeta:** `internal/httpapi/routes.go`, `internal/httpapi/middleware/ratelimit.go` (sem mudança de
  código no middleware), testes de `internal/httpapi`.

## Contexto

O PR #327 pôs as cinco rotas autenticadas do 2FA (enroll, confirm, disable, rotate e rotate/confirm)
e a troca de senha no mesmo balde por IP do login (10 por minuto, rajada de 5), por fora de `authn`.
Isso deixou duas lacunas de disponibilidade, ambas no desenho e não no código:

- Quem divide um IP (NAT, CGNAT) divide também as fichas do login e do 2FA.
- Como o limite fica antes de `authn`, um pedido sem credencial válida gasta ficha do IP e pode deixar
  o 2FA e o login de outra pessoa em 429.

A spec original (§5.7) manda limite por IP nas rotas públicas e por usuário nas autenticadas. A
força bruta do código já é contida por outras camadas: o contador de provas por família (a quinta
prova errada derruba a sessão) e a janela curta do TOTP. O que falta nessas rotas é um teto de taxa e
de custo (Argon2id) que não dependa do IP.

### Alternativas

| Opção | Prós | Contras |
|---|---|---|
| A. Manter o balde por IP compartilhado com o login | Nenhuma mudança; já revisado | As duas lacunas acima; a spec §5.7 segue descumprida |
| **B. Balde por conta nas 6 rotas; o login segue por IP** | Chave é o usuário: NAT e terceiros sem token não interferem; reaproveita `RateLimitUser`; o 429 só aparece para quem tem token válido | Reverte a decisão de IP do #327; as rotas perdem o balde apertado por IP e ficam só com o piso global (120/min, rajada 30) |
| C. Balde por IP e balde por conta juntos | Duas camadas | Mantém as duas lacunas; mais código e teste |
| D. Chave por família de sessão | Simples | Uma sessão a mais multiplica o orçamento de custo |

## Decisão

Opção B. Uma instância só de `RateLimitUser` (10 por minuto, rajada 5, a mesma taxa do login),
dividida pelas cinco rotas do 2FA e por `POST /auth/password`. A cadeia é `authn` → sessão viva →
limite da conta → handler. O limite por IP (`limiteLogin`) fica só no login. O `/totp/verify` segue
com o seu limite por IP, e o piso global por IP e o contador de provas por família continuam
valendo.

Detalhes que a decisão fixa:

- A chave é o id do usuário, nunca a sessão nem a família: outra sessão da mesma conta, de qualquer
  IP, divide o balde, e logar de novo não rende ficha.
- O limite vem depois de `authn`: pedido sem token válido é 401 e não gasta ficha de ninguém.
- O limite vem depois de `viva`: um token de sessão revogada, ainda no prazo, é recusado pelo `viva` e
  não gasta o balde de que o dono precisa para trocar a senha ou o 2FA. Uma sessão viva da mesma
  conta segue com a rajada inteira. `POST /auth/password` também passa pelo `viva`.
- Uma senha atual errada em `/auth/password` gasta ficha da conta, e trocar de IP não zera o balde.
- O 429 sai antes do handler: não reserva nem conta prova na família e não roda Argon2id.

## Consequências

- Cada conta tem teto de taxa e de custo nessas rotas, sem depender do IP; disputas por NAT e fichas
  gastas por terceiros sem credencial deixam de existir para o 2FA e para a troca de senha.
- Login e 2FA param de disputar fichas.
- Perde-se o balde apertado por IP nessas rotas: o que sobra é o piso global de 120 por minuto.
- Custo da ordem: uma consulta de sessão por pedido de quem já tem token assinado, sob o piso global
  por IP. Quem não tem token assinado continua barrado em `authn`, sem tocar o banco.
- Risco aceito: com chave por conta, quem tem um token de sessão ainda viva gasta as fichas da conta e
  pode atrasar o dono nessas seis rotas até as fichas voltarem (uma a cada 6 s). Não é erro de
  ordem: é inerente à chave por conta, e a revogação da sessão fica em outro balde (o das rotas de
  recurso).
- Melhoria futura, sem prazo: um balde por (conta, família), com teto por conta, para que uma sessão
  não esvazie a das outras. Custa mais código e mais memória, e só vale se o risco acima se mostrar
  real.
- O estado continua só em memória de cada instância: com mais de uma réplica, cada uma conta por si.
  A chave por conta migra junto com o resto (issue #328).
- Continua fora de escopo: o total de provas erradas por conta somado entre famílias.
- O contador de provas por família deixou de ser memória do processo (issue #324, ver a atualização
  abaixo); o balde por conta continua só em memória de cada instância.

## Atualização (issue #324): contador de provas por família persistido

O contador de provas erradas por família passou da memória do processo para o Postgres (tabela
`session_family_proofs`, migration 00028), de modo que um restart ou deploy não devolve o orçamento. Ele
e o balde por conta são camadas independentes e se complementam: o balde limita a taxa e o custo por
conta; o contador limita o total de provas erradas por família de sessão, e a quinta derruba a família.
A ordem da cadeia não muda (`authn` → `viva` → limite da conta → handler, e a reserva da prova dentro do
handler, antes de verificar). Um pedido barrado pelo balde (429) continua não reservando nem contando prova.

Decisões:

- O incremento é uma instrução SQL única, sem ler e depois gravar, então o orçamento vale entre processos
  e a reserva continua vindo antes da verificação.
- Erro de banco na reserva ou no registro da prova errada responde 500 e nada é verificado; a escrita do
  registro não segue o cancelamento da requisição.
- Família viva nunca perde o contador por inatividade: a vida dela já é limitada pelo prazo do refresh, e
  só o sucesso o zera. A linha de uma família que deixou de existir (revogada ou vencida) sai na retenção
  periódica, em lote, depois de uma folga curta; a do usuário apagado sai pela chave estrangeira. A
  limpeza fica fora do caminho dos pedidos.
- A métrica `session_family_proofs` mostra o tamanho da tabela.
- Durante a sobreposição de um deploy, a instância antiga (contador em memória) e a nova (banco) contam
  separado por alguns minutos.

## Compliance

Testes de `internal/httpapi/totp_limite_conta_integration_test.go` (cada um vê-se falhar ao sabotar
a regra que o garante):

- 429 com `Retry-After` depois da rajada, em cada uma das seis rotas; as seis dividem um balde.
- Duas sessões da mesma conta, de IPs diferentes, dividem o balde; dois usuários no mesmo IP não.
- Pedido sem token, com token forjado ou vencido: 401 e balde do dono intacto.
- Login e fluxo completo do 2FA do mesmo IP, sem 429; login e `/totp/verify` seguem por IP e não
  gastam o balde da conta (e o contrário).
- Recarga no relógio injetado; nenhum Argon2id num pedido barrado; o 429 não traz id, IP, e-mail,
  família nem nome do balde; pedido barrado não conta como prova errada.
- Piso global por IP continua atingindo as rotas; senha atual errada gasta a ficha da conta mesmo
  trocando de IP; contas diferentes, em IPs diferentes, têm baldes independentes.
- Token de sessão revogada é recusado antes do limite e não gasta ficha (uma sessão viva da mesma
  conta mantém a rajada inteira), nas seis rotas; e não troca a senha.
- Contador persistido (issue #324): testes de `internal/auth/provas_test.go` e de
  `internal/httpapi/provas_reinicio_integration_test.go` (restart no meio, orçamento exato sob
  concorrência, falha de banco, família viva parada por mais de um dia).
- `rotas_negar_por_padrao_integration_test.go` e `rotas_protegidas_test.go` seguem verdes: sem
  credencial, 401, e nunca 429 antes do 401.
