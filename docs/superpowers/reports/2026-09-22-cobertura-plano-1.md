# Relatório de cobertura — Plano 1 (Fundação e Autenticação)

Branch `feat/fundacao-e-autenticacao`, faixa `7950399..8917111`, 25 commits.
Go 1.26.8, `-covermode=atomic`, Docker disponível (testes de integração rodaram de verdade).

**Critério vem da spec §14:** cobertura "alta e verificada em safedial, incident, auth e
crypto; cobertura de handler CRUD não é perseguida". `incident` só nasce no Plano 3, então
esta análise julga **crypto, safedial e auth**. Não há número global neste relatório: número
global é fácil de inflar testando handler CRUD e não diz nada sobre se a propriedade de
segurança está travada.

## (a) Números reais

Suíte inteira verde: 7 pacotes com teste, `go test ./...` exit 0.

### Cobertura pelos testes do próprio pacote

| Pacote | Cobertura |
|---|---|
| `internal/prober/safedial` | **94.4%** |
| `internal/crypto` | **87.5%** |
| `internal/config` | 81.6% |
| `internal/auth` | **79.3%** |
| `internal/httpapi/middleware` | 74.7% |
| `internal/storage` | 72.0% |
| `internal/httpapi` | 70.5% |

### Cobertura de união (suíte inteira, `-coverpkg` nos três pacotes nomeados)

| Pacote | Coberto / Total | Union |
|---|---|---|
| `internal/prober/safedial` | 34/36 | **94.4%** |
| `internal/crypto` | 35/40 | **87.5%** |
| `internal/auth` | 227/280 | **81.1%** |
| **Os três juntos** | 296/356 | **83.2%** |

`auth` sobe de 79.3% para 81.1% quando os testes de `httpapi` entram: os testes de integração
de login/refresh/logout exercitam caminhos que os testes de unidade do pacote não alcançam.

### Os 0.0% que não são buracos

`internal/apierr`, `internal/clock` e `internal/observability` aparecem com 0.0% na listagem
por pacote porque **não têm binário de teste próprio** — não porque não sejam exercitados.
Medidos sob a suíte inteira:

| Função | Cobertura real |
|---|---|
| `apierr.WriteError` | 77.8% |
| `apierr.WriteJSON` | 75.0% |
| `observability.WithTraceID` / `TraceID` | 100% |
| `clock.Fake.{Now,Advance}` / `NewFake` | 100% |
| `clock.Real.Now` | 0.0% (só produção usa; o `Fake` é o caminho testado) |

`internal/storage/testdb` e `cmd/vigilume` em 0.0% são esperados: infraestrutura de teste e
wiring de processo.

## (b) Análise dos três pacotes nomeados

**`safedial` — 94.4%, o mais alto do repositório, e merecido.** A tabela de casos percorre
`::ffff:`, as bordas da /12, loopback, link-local, ULA e multicast, e há teste de ponta a
ponta contra `httptest.Server`. `AllowAddr`, `NewTransport` e `NewClient` em 100%. Os dois
statements descobertos estão listados em (c) — um deles importa.

**`crypto` — 87.5%.** `Open`, `AAD`, `HashToken`, `HashIP`, `MarshalJSON`, `LogValue`,
`Format` e `Reveal` em 100%. Os cinco statements descobertos são três `fmt.Errorf` de erro de
infra (falha do `rand.Read`, do construtor AES/GCM) e **dois métodos de redação inteiros**,
que importam.

**`auth` — 81.1%, o mais baixo dos três, e o número não é o problema.** 53 statements
descobertos, e **42 deles são `return fmt.Errorf(...)` ou `return err` em caminho de falha de
infraestrutura** (transação que não abre, `INSERT` que falha, `rand.Read` que quebra). Isso é
ruído: forçá-los exigiria injetar falha de banco, e o retorno em confiança é quase nulo. O
número baixo de `auth` é quase inteiramente custo de tratar erro com rigor — o pacote tem mais
ramos de erro que os outros dois somados. **O que importa em `auth` é um único ramo descoberto,
e ele é de segurança.**

## (c) Linhas descobertas, com julgamento

Separei o que é ruído do que morde. Para cada buraco, a pergunta não foi "está coberto?" e sim
**"se eu quebrasse a propriedade, algum teste ficaria vermelho?"** — e a resposta foi obtida
por sabotagem num worktree separado, não por leitura.

### BURACO 1 — `internal/auth/totp.go:115` — `Confirm` nunca foi testado com código errado

```go
if !validateTOTP(code, secret, now) {
    return nil, ErrTOTPInvalid   // <- NUNCA executado em teste nenhum
}
```

`Verify` com código errado é testado em três lugares (`totp_test.go:157`, `:273`, `:440`).
`Confirm` com código errado, em nenhum. É a assinatura exata do padrão que apareceu nove vezes
nesta execução: a propriedade parece testada porque a função vizinha é.

**SABOTAGEM (prova, não suposição):** troquei a linha por `if false && !validateTOTP(...)` e
rodei `go test ./...`.

```
ok  internal/auth   3.852s
ok  internal/crypto 0.004s
ok  internal/httpapi 6.522s
ok  internal/prober/safedial 0.004s
ok  internal/storage 2.272s
ok  internal/config, internal/httpapi/middleware
```

**Suíte inteira verde com a guarda desligada.** Com esse defeito em produção,
`POST /auth/totp/confirm` ativaria o segundo fator com *qualquer* código de seis dígitos — o
passo de confirmação deixaria de provar posse do autenticador, que é a única coisa que ele
existe para fazer. Seria um segundo fator ativado sem ninguém nunca ter aberto o app.

Gravidade: **alta**. É o mesmo buraco que o R38 fechou por outro ângulo (enroll sem prova de
posse), sobrevivendo no passo seguinte do mesmo fluxo.

**Correção:** um teste que chama `Confirm` com código inválido e exige `ErrTOTPInvalid`, e que
verifique também que `totp_confirmed_at` continuou `NULL` — sem essa segunda asserção o teste
não distingue "recusou" de "recusou mas ativou mesmo assim".

### BURACO 2 — `internal/prober/safedial/safedial.go:57` — guarda de endereço inválido

```go
if !ip.IsValid() {
    return fmt.Errorf("%w: endereço inválido", ErrBlocked)   // <- NUNCA executado
}
```

**SABOTAGEM:** desliguei a guarda; suíte inteira verde. Sem ela, um `netip.Addr` zerado passa
direto pelo `Unmap` e não casa com prefixo nenhum — `AllowIP` devolve `nil`, ou seja,
**permite**. A guarda é a única coisa entre "endereço que não parseou" e "liberado".

Gravidade: **média**. Hoje nenhum consumidor chega com `Addr` inválido (o R5 registra que
`safedial` não tem consumidor no Plano 1), então é risco latente e não ativo; ele acorda quando
o prober do Plano 2 começar a passar endereços vindos de resolução de DNS. Custo da correção:
duas linhas na tabela de casos existente.

### BURACO 3 — `internal/crypto/secret.go:28-29` — `String()` e `GoString()` sem teste

```go
func (Secret) String() string   { return redacted }   // <- NUNCA executado
func (Secret) GoString() string { return redacted }   // <- NUNCA executado
```

A §5.4 exige que o segredo nunca apareça em claro. `MarshalJSON`, `LogValue` e `Format` estão
em 100% e travados por teste; estes dois, não.

**SABOTAGEM:** troquei `String()` por `return string(s)` — vazamento direto do plaintext — e a
suíte ficou **verde**.

Gravidade: **baixa, mas corrigir é barato**. O `Format` intercepta a classe inteira de verbos
do `fmt`, então na prática `%s`/`%v` não chegam a `String()`; o vazamento real precisaria de
chamada explícita a `.String()` ou de um consumidor fora do `fmt` (um `text/template`, por
exemplo). Ainda assim, é contrato documentado do tipo sem uma linha que o trave, e o teste são
duas asserções.

### Ruído — 42 statements que NÃO valem perseguir

Todos os demais descobertos em `auth`, `crypto` e `safedial` são `return fmt.Errorf(...)` /
`return err` atrás de falha de infraestrutura: `pool.Begin` que não abre, `Exec` que falha,
`rand.Read` que quebra, `uuid.NewV7` que erra, `panic` do hash dummy (inalcançável por
construção). Cobri-los exigiria injeção de falha de banco — uma camada de mock para ganhar
pontos num número. **Não recomendo.** Foi decisão consciente deste relatório deixá-los
descobertos e dizer por quê, em vez de reportar "auth em 81%" como se fosse dívida.

Há três `ErrNotFound` descobertos (`totp.go:102`, `:220`, `:236`, `user.go:106`) — o caminho
"a conta não existe" quando o chamador já está autenticado. Só alcançável por conta apagada
entre a emissão do token e o uso. Anotar como conhecido; não perseguir agora.

## Veredito

| Pacote da §14 | Cobertura | Verificada? |
|---|---|---|
| `crypto` | 87.5% | **Sim**, com uma lacuna barata (Buraco 3) |
| `safedial` | 94.4% | **Sim**, com uma guarda latente aberta (Buraco 2) |
| `auth` | 81.1% | **Não inteiramente** — Buraco 1 é a propriedade central do 2FA sem trava |
| `incident` | — | Só existe no Plano 3 |

O número de `auth` está alto o bastante; o problema não é quanto foi coberto, é **qual ramo
ficou de fora**. Três sabotagens, três suítes verdes: a cobertura apontou para os três, e a
sabotagem confirmou que os três mordem.
