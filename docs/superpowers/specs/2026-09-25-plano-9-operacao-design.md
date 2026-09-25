# Vigilume — Plano 9: operação (design)

**Status:** aprovada pelo usuário em 2026-09-25. Nada aqui é código de produto nem plano de
implementação; o plano vem depois da aprovação desta spec.

**Base de código:** `main` em `88c1a62` (Plano 8 integrado).

**Origem:** o grupo B de `docs/superpowers/PENDENCIAS-PARA-O-PLANO-9.md` ("Operação"), escolhido
pelo usuário em 2026-09-25.

---

## 1. Objetivo

Deixar o dono **operar o Vigilume sem SQL e sem gambiarra**: sair de uma conta travada pelo
terminal, trocar de celular sem desligar o 2FA, pausar um canal barulhento sem perder a
configuração, e rodar a API e o sondador no mesmo host em desenvolvimento.

**Critérios de sucesso:**

1. Cada item sai **utilizável** por quem opera: pelo CLI (§3.1), pelo painel (§3.2, §3.3) ou sem
   configuração (§3.4).
2. **Nenhum contrato atual muda.** Só entram rotas e campos novos; os shapes, códigos HTTP e
   `error.code` existentes ficam como estão.
3. Cada correção ou recurso tem teste que fica vermelho quando é desfeito (sabotagem registrada).
4. A suíte com `-race`, os gates (`lint`, `sec`, `sqlc diff`, `gofmt -l .`), os testes do painel
   e o E2E ficam verdes; o PR só é integrado com **todos** os jobs verdes no GitHub.
5. `PENDENCIAS-PARA-O-PLANO-10.md` substitui o arquivo atual como estado corrente.

## 2. Escopo e fora de escopo

**Dentro (decisões do usuário em 2026-09-25):**

| # | Item | Seção |
|---|---|---|
| 1 | `vigilume user reset-2fa`: recuperação do 2FA pelo CLI | §3.1 |
| 2 | Troca de autenticador com segredo pendente: API **e** painel | §3.2 |
| 3 | Pausar e retomar canal: API **e** painel | §3.3 |
| 4 | Porta de métricas padrão por papel (M7) | §3.4 |
| 5 | Documentação (runbook, README) e pendências | §3.5 |

**Fora, e continuam nas pendências:** as dívidas menores (grupo C), o R2 (grupo D), e tudo que
depende do dono (Fly, secrets, licença, visibilidade, hospedagem sem custo).

## 3. Desenho por item

### 3.1 `vigilume user reset-2fa --email <e-mail>`

**Hoje.** Quem perde o autenticador **e** os códigos de recuperação só sai por SQL à mão.

**Desenho.** Um subcomando novo em `cmd/vigilume/user.go`, no molde do `set-password`:

- **Entrada:** só `--email` e a variável `DATABASE_URL`. **Não pede senha:** quem tem a
  `DATABASE_URL` já pode fazer tudo no banco; o comando existe para que o SQL certo não precise
  ser escrito à mão, e não é uma fronteira de segurança.
- **Ordem do boot** (D7 do Plano 5): `CheckRole` → `Migrate` → operação.
- **Efeito**, numa transação só:
  - desliga o 2FA: `totp_secret_enc`, `totp_confirmed_at`, `totp_last_timestep` e o segredo
    pendente da §3.2 ficam nulos;
  - apaga os códigos de recuperação do usuário;
  - **encerra todas as sessões** (o mesmo `RevokeAllFamilies` do `set-password`);
  - grava a auditoria `totp.reset_cli`.
  - A marca do desafio (`totp_challenge_mark`, Plano 8) fica como está: ela só serve para
    recusar desafios velhos, e zerá-la não ajuda ninguém.
- **Saídas:**
  - `0` e "2FA desativado; N sessões encerradas. Entre com a senha e ative de novo pelo painel."
    quando havia 2FA;
  - `0` e "o 2FA já estava desativado; nada mudou" quando não havia (idempotente, sem auditoria
    e sem encerrar sessões);
  - `4` para e-mail não cadastrado, `2` para uso errado, `1` para ambiente, `130` para
    interrupção — os mesmos códigos e o mesmo sentido do `set-password`.
- O texto de uso (`usoUser`) ganha a linha do subcomando.

A lógica fica no serviço `internal/account` (`ResetTwoFactor`), como o `ResetPassword`, e o CLI
só a chama.

### 3.2 Troca de autenticador com segredo pendente

**Hoje.** Trocar de celular exige desativar (senha + código) e ativar de novo. No meio, a conta
fica sem 2FA, e quem desiste no meio fica desprotegido sem perceber.

**Decisão do usuário:** segredo pendente, e não um assistente que encadeia desativar e ativar.

**Dados.** Migration `00009_totp_pending_secret.sql`: `ALTER TABLE users ADD COLUMN
totp_pending_secret_enc bytea;` (anulável, sem `DEFAULT`). É cifrado com o mesmo `crypto.Box`, com
a AAD amarrada à tabela, à coluna e ao usuário (`crypto.AAD("users", "totp_pending_secret_enc",
userID)`), como o `totp_secret_enc` já é. Um valor não pode ser copiado de uma coluna para a outra
nem de um usuário para outro; por isso a confirmação **decifra** o pendente e **cifra de novo** com
a AAD de `totp_secret_enc`, em vez de copiar os bytes.

**Rotas** (atrás de `authn` e da exigência de família viva, como as outras do TOTP):

1. **`POST /api/v1/auth/totp/rotate`**, corpo `{password, code}`. As provas são **as mesmas do
   `disable`**: a senha pela verificação do login (`auth.CheckPassword`: conta bloqueada é
   recusada, senha errada conta no bloqueio) **e** a posse do fator atual, por código do app ou
   por código de recuperação no mesmo campo `code` (o `disable` aceita também `recovery_code`, e
   esta rota aceita os dois nomes do mesmo jeito).
   - Sucesso: gera um segredo novo, grava em `totp_pending_secret_enc` (substituindo um pendente
     anterior) e devolve `200 {otpauth_uri}`, no mesmo formato do `enroll`.
   - 2FA desligado: `403 forbidden` (o espelho do `enroll` com o 2FA ligado).
   - Prova errada ou ausente: `401 invalid_credentials`, que conta no bloqueio. Nunca
     `unauthorized` (D24 do Plano 5).
   - Gastar um código de recuperação aqui é a prova de posse, e ele fica gasto mesmo que a troca
     não seja confirmada — é o mesmo comportamento do `disable`.
2. **`POST /api/v1/auth/totp/rotate/confirm`**, corpo `{code}`, com o código do app **novo**.
   - Sucesso, numa transação só: o pendente vira o segredo atual; `totp_last_timestep` recebe o
     timestep que acabou de ser usado (o mesmo que o `confirm` faz: o código da confirmação não
     vale de novo); os códigos de recuperação antigos são apagados e saem 10 novos; as **outras**
     famílias de sessão são encerradas e a atual fica; auditoria `totp.rotated`. Resposta
     `200 {recovery_codes}`, no mesmo formato do `confirm`.
   - Sem troca em andamento (nenhum pendente): `403 forbidden`.
   - Código errado: `401 invalid_credentials`, que conta no bloqueio; o pendente continua lá.
   - O segredo **antigo** vale até este passo e para de valer nele.
- **O pendente não tem prazo.** Ele só é confirmável por quem viu o QR, com uma sessão viva do
  dono. É apagado na confirmação, no `disable`, no `reset-2fa` (§3.1) e numa nova chamada de
  `rotate` (que o substitui).
- **Limitador:** a mesma cadeia das outras rotas do TOTP (`enroll`, `confirm`, `disable`):
  `authn` e família viva, sem limitador por IP próprio. Quem limita as provas erradas é o
  bloqueio progressivo por conta, que as duas rotas alimentam (D4 e D5 do Plano 5).

**Por que encerrar as outras sessões.** Um celular trocado pode ter sido perdido ou roubado; é o
mesmo motivo de a troca de senha encerrar as outras famílias.

### 3.3 Pausar e retomar canal

**Hoje.** `notif_channels.enabled` existe, e o despacho o respeita (`semEnvio`, em
`internal/notify/dispatcher.go`: canal desabilitado **cancela** a notificação na hora do envio).
Nenhuma rota altera a coluna, e o painel só sabe apagar o canal.

**Rota.** `PATCH /api/v1/channels/{id}`, corpo `{"enabled": bool}`.
- `200` com o canal no mesmo formato de um item de `GET /channels` (que já traz `enabled`).
- Canal de outro usuário ou inexistente: `404`.
- Corpo sem `enabled`, com `enabled` que não é booleano, ou com qualquer outro campo: `400
  validation_failed`, com `fields` no vocabulário fechado.
- Atrás de `protegida` (o limitador comum): a rota não manda mensagem para fora, ao contrário de
  criar canal e reenviar verificação.
- Mudar para o valor que já está é `200` sem efeito (idempotente).

**Semântica, dita ao usuário no painel:** enquanto o canal está pausado, **os avisos são
descartados** — o despacho os cancela. Retomar não reenvia o que passou. Guardar avisos para
depois exigiria outro estado na fila e renderia uma enxurrada de alertas velhos ao retomar.

A lógica fica no `notify.ChannelService` (`SetEnabled`), sob a RLS (`storage.InUserTx`), como o
resto do serviço.

### 3.4 Porta de métricas padrão por papel (M7)

**Hoje.** `METRICS_ADDR` tem padrão `127.0.0.1:9090` para qualquer papel. `--role=api` e
`--role=prober` no mesmo host disputam a porta, e o segundo processo cai no boot. Compose e Fly
usam `METRICS_ADDR=off`, então o problema só aparece rodando dois processos à mão.

**Desenho.** Com `METRICS_ADDR` **não definida**, o padrão passa a depender do papel efetivo
(`ROLE`, que o `--role` já sobrepõe antes do `config.Load`):

| Papel | Padrão |
|---|---|
| `all` | `127.0.0.1:9090` (como hoje) |
| `api` | `127.0.0.1:9090` |
| `prober` | `127.0.0.1:9091` |

Com `METRICS_ADDR` definida (inclusive `off`), nada muda, e as validações atuais (só loopback,
host vazio é erro) valem igual. A falha de porta ocupada no boot passa a sugerir o
`METRICS_ADDR` na mensagem de erro.

### 3.5 Documentação

- **Runbook** (`docs/deploy/fly.md`): a recuperação do 2FA pelo `fly proxy`, no molde do passo
  10 (`user create`), com o mesmo aviso de usar o binário da versão que está no ar.
- **README**: o `reset-2fa` na seção do CLI; a nota do M7 na parte de desenvolvimento.
- **`PENDENCIAS-PARA-O-PLANO-10.md`**: o grupo B fechado, o C e o D transcritos, e os limites
  deste plano.

## 4. Painel

### 4.1 Tela de canais (`web/src/features/canais/PaginaCanais.tsx`)

- Cada canal ganha um botão **Pausar** ou **Retomar** (secundário, pequeno, com `aria-label`
  que cita o canal, no padrão do "Reenviar verificação").
- **Pausar pede confirmação** com o `Confirmar` existente: "Enquanto pausado, os avisos de
  incidentes deste canal são descartados, e não são reenviados ao retomar." **Retomar** não pede.
- Um canal pausado mostra, abaixo do estado, "⏸ Pausado: os avisos são descartados". O estado de
  verificação continua aparecendo (um canal pode estar pausado e verificado).
- `404` (canal apagado em outra aba) segue o tratamento de "lista velha" que o reenviar já usa.
- Cliente: `api.pausarCanal(id, enabled)` (`PATCH`), com o schema do canal que já existe.

### 4.2 Troca de autenticador (`web/src/features/configuracoes/PaginaConfiguracoes.tsx`)

- Com o 2FA ligado, a seção ganha **Trocar autenticador** ao lado de **Desativar**. O diálogo só
  abre depois de reler o `/me`, a mesma trava que Ativar e Desativar têm contra outra aba.
- Três passos, reaproveitando os componentes do fluxo de ativação:
  1. senha e "Código do autenticador" com a ajuda "Ou um código de recuperação" (os mesmos campos
     do Desativar);
  2. o QR e o segredo em texto do app **novo**, e o código gerado por ele;
  3. os 10 códigos de recuperação novos, com o "guardei os códigos" antes de fechar.
- Ao concluir: "Autenticador trocado. As outras sessões foram encerradas."
- Fechar no passo 2: "Nada mudou: o app atual continua valendo."
- Erros:
  - `401 invalid_credentials`: "Senha ou código incorreto." (o `erroDeCredencial` existente);
  - `403` ao iniciar: o 2FA foi desligado em outra aba — o diálogo fecha e avisa;
  - `403` ao confirmar: "A troca foi concluída ou cancelada em outra aba. Comece de novo."
- Cliente: `api.totpTrocar(senha, codigo)` e `api.totpConfirmarTroca(codigo)`. Nenhum dos dois
  dispara o refresh da sessão num `invalid_credentials` (D24 do Plano 5): é o mesmo tratamento
  que o `totpDesativar` já tem.

### 4.3 CSP e acessibilidade

Nada novo de estilo inline ou script: os componentes são os existentes. As telas novas entram no
E2E de acessibilidade (§6) com o controle de CSP que cada spec já faz.

## 5. Dados e migrations

- `00009_totp_pending_secret.sql` (§3.2). Coluna anulável sem `DEFAULT`: só catálogo.
- `sqlc generate` para o modelo (`db.User` ganha o campo); o SQL novo do `auth` segue escrito à
  mão no mesmo arquivo do resto (o R2 continua fora).
- Nenhuma tabela nova; nenhum `GRANT` novo.

## 6. Testes

Cada linha tem uma sabotagem que o ledger registra com o resultado.

| Item | Teste | Sabotagem |
|---|---|---|
| 3.1 | `reset-2fa` com 2FA ligado: sai 0, desliga o 2FA, apaga os códigos, encerra todas as sessões, grava a auditoria | Não apagar os códigos; não encerrar as sessões |
| 3.1 | `reset-2fa` com 2FA desligado: sai 0, "nada mudou", nenhuma sessão encerrada, nenhuma auditoria | Tratar como o caso ligado |
| 3.1 | E-mail desconhecido sai 4; sem `--email`, argumento extra ou sem `DATABASE_URL` sai 2 | Trocar os códigos |
| 3.1 | Depois do `reset-2fa`, o login só com a senha devolve access token (sem desafio) | Deixar `totp_confirmed_at` |
| 3.2 | `rotate` com senha errada, código errado e código ausente: `401 invalid_credentials`, e cada um conta no bloqueio | Não chamar `RecordFailure` |
| 3.2 | `rotate` com o 2FA desligado: `403` | Aceitar |
| 3.2 | Entre `rotate` e `confirm`, o login usa o app **antigo**; depois do `confirm`, só o **novo** | Trocar o segredo já no `rotate` |
| 3.2 | `confirm` com código errado: `401`, o pendente fica, e um `confirm` certo depois funciona | Apagar o pendente na falha |
| 3.2 | `confirm` sem pendente: `403` | Aceitar |
| 3.2 | Depois do `confirm`: os códigos de recuperação antigos não valem, os 10 novos valem, as outras famílias caem e a atual fica | Não apagar os antigos; revogar todas |
| 3.2 | `disable` e `reset-2fa` apagam o pendente: um `confirm` depois dá `403` | Não limpar o pendente |
| 3.2 | O pendente copiado para outro usuário, ou para a coluna ativa, não abre (AAD por tabela, coluna e usuário) | AAD sem o usuário; confirmação copiando os bytes em vez de cifrar de novo |
| 3.3 | `PATCH` pausa e retoma; `GET` reflete; `404` para canal alheio; `400` para corpo inválido | Ignorar o `user_id` |
| 3.3 | Uma notificação pendente de canal pausado é cancelada no envio; retomado, a seguinte é enviada | (o teste do despacho existente cobre o cancelamento; este prova o caminho pela rota) |
| 3.4 | Sem `METRICS_ADDR`: `api` e `all` em 9090, `prober` em 9091; com `METRICS_ADDR` definida, ela vale | Ignorar o papel |
| 4.1 | Vitest: Pausar pede confirmação e manda `enabled:false`; Retomar manda `true` sem confirmação; a linha "Pausado" aparece; `404` recarrega a lista | Pular a confirmação |
| 4.2 | Vitest: os três passos; os erros `401`, `403` ao iniciar e ao confirmar; fechar no passo 2; a trava do `/me` | Abrir sem reler o `/me` |
| 4.3 | E2E de acessibilidade: um canal pausado nas respostas simuladas; os três passos do diálogo de troca; axe e CSP limpos | — |

**Regressão.** A suíte inteira com `-race` nos Postgres 17 e 18, os testes do painel, o E2E do
compose e o `checar-imagem`.

## 7. Segurança

- **§3.1** não abre caminho novo: exige a `DATABASE_URL`, que já dá acesso total ao banco. Ela
  encerra todas as sessões, então quem estivesse dentro com uma sessão roubada sai.
- **§3.2** exige senha **e** posse do fator atual para iniciar, e posse do fator novo para
  confirmar. Uma sessão roubada sozinha não troca o autenticador, porque isso entregaria o
  segundo fator ao atacante (o bypass que o R38 fechou). O segredo antigo vale até o novo ser
  provado. Provas erradas contam no bloqueio progressivo.
- **§3.3** é mutação comum de recurso do dono, sob RLS.
- **§3.4** mantém a porta de métricas só em loopback.

## 8. Riscos

| # | Risco | Tratamento |
|---|---|---|
| R1 | Um código de recuperação gasto no `rotate` fica gasto mesmo que a troca não seja confirmada | Aceito: é a prova de posse, como no `disable`. O passo 3 entrega 10 novos quando a troca termina |
| R2 | O dono pausa um canal e esquece | A linha "Pausado" fica visível na lista; o dashboard não muda. Um aviso no dashboard fica para depois |
| R3 | Avisos descartados durante a pausa | Dito no diálogo de confirmação; é o comportamento do despacho desde o Plano 3 |
| R4 | Porta 9091 ocupada por outro programa em desenvolvimento | A mensagem de erro sugere o `METRICS_ADDR` |
| R5 | `reset-2fa` rodado contra o banco errado | O mesmo risco do `set-password`: o comando imprime o e-mail e o número de sessões encerradas |

## 9. Execução

Um branch `plano-9-operacao` a partir do `main`. As tarefas se dividem por camada e por arquivo:
a migration e o `auth`/`account` da troca e do `reset-2fa`; o CLI; o `notify` e a rota do canal; a
config do M7; o painel (canais e configurações); a documentação. As de backend que não
compartilham arquivo podem rodar em paralelo em worktrees; as do painel dependem das rotas
existirem só no E2E real, e usam a API falsa nos testes de unidade. Revisão por tarefa, revisão
final do branch, PR, e merge só com todos os jobs verdes.
