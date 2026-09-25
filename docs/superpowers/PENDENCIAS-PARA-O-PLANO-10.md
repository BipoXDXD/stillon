# Pendências depois do Plano 9

Este arquivo existe porque o ledger de execução vive em `.superpowers/`, fora do controle de
versão. O que está aqui é o subconjunto que não pode se perder. Ele substitui o
`PENDENCIAS-PARA-O-PLANO-9.md` como estado corrente; os anteriores ficam como histórico.

## Fechadas no Plano 9

O grupo B das pendências (operação), escolhido pelo usuário em 2026-09-25, com API e painel.
Spec: `docs/superpowers/specs/2026-09-25-plano-9-operacao-design.md`.

| Pendência | Como fechou |
|---|---|
| Recuperação do 2FA pelo CLI | `vigilume user reset-2fa --email <e>`: desliga o 2FA (inclusive uma troca pendente), apaga os códigos de recuperação e encerra todas as sessões numa transação, com auditoria `totp.reset_cli`. Idempotente: com o 2FA desligado, sai 0 e "nada mudou". Runbook: "Perdeu o autenticador e os códigos de recuperação" |
| Troca de autenticador | Segredo pendente (`users.totp_pending_secret_enc`, migration `00009`): `POST /auth/totp/rotate` (senha + posse do fator atual) e `POST /auth/totp/rotate/confirm` (código do app novo). O app antigo vale até a confirmação; ela troca o segredo, gera 10 códigos novos e encerra as outras sessões. Painel: Configurações → Trocar autenticador |
| Rota para desabilitar canal | `PATCH /api/v1/channels/{id}` com `{"enabled": bool}`. Painel: Pausar (com confirmação) e Retomar na lista de canais |
| Porta de métricas em desenvolvimento (M7) | Sem `METRICS_ADDR`, o padrão depende do papel: `api` e `all` em `127.0.0.1:9090`, `prober` em `127.0.0.1:9091`. A falha de porta ocupada sugere o `METRICS_ADDR` |

## Dependem do dono do projeto (nenhum agente faz)

- Criar os apps, o volume, os papéis e os segredos no Fly, e o primeiro deploy (runbook
  `docs/deploy/fly.md`). **Tem custo recorrente** (estimativa do Plano 7: ~US$ 9–11/mês); a
  decisão de criar é do dono.
- Gravar os secrets de repositório (`FLY_DEPLOY_TOKEN`, `FLY_BACKUP_TOKEN`,
  `BACKUP_DATABASE_PASSWORD`, `AGE_RECIPIENT`) e as variáveis (`PG_VOLUME_GB`, `BACKUP_ENABLED=1`);
  rodar o `backup` à mão uma vez (runbook, passo 13).
- Medir o endereço do proxy do Fly, preencher `TRUSTED_PROXIES` e levá-lo ao `fly.toml` por PR;
  o teste de dois pontos de rede (que também prova se o proxy sobrescreve um `Fly-Client-IP`
  mandado pelo cliente).
- **Tráfego do próprio Fly sem `Fly-Client-IP`** (runbook, passo 9): se, no primeiro deploy, o
  aviso "cabeçalho de IP do cliente ausente ou ilegível" voltar a cada minuto com o
  `remote_addr` do proxy, é tráfego do Fly (health check, por exemplo) que chega sem o
  cabeçalho. Não foi ensaiado; a resposta é decidir se o aviso deve ignorar esse caso, e não
  mudar o `fly.toml`.
- Opcional: GitHub Pro (~US$ 4/mês) dá secrets por environment num repositório privado.
- Escolher a licença e tornar o repositório e o pacote do GHCR públicos, quando quiser
  (runbook, última seção).
- Hospedagem sem custo (pergunta do dono em 2026-09-25): uma VM grátis rodando o
  `compose.yaml` seria um alvo de deploy novo, com runbook, HTTPS e backup próprios. Não é
  trabalho do Plano 8; vira plano próprio se o dono quiser.

## Limites conhecidos do Plano 9

- **Pausar descarta os avisos.** O despacho cancela a notificação de canal desabilitado no envio
  (desde o Plano 3); retomar não reenvia o que passou. O diálogo de "Pausar" diz isso.
- **Um canal pausado só aparece na lista de canais**: o dashboard não avisa. Quem pausa e esquece
  fica sem aviso daquele canal até olhar a lista.
- **O código de recuperação usado para iniciar uma troca fica gasto**, mesmo que a troca não seja
  confirmada (é a prova de posse, como no `disable`). O painel avisa ao fechar no passo 2.
- **O segredo pendente não tem prazo.** Ele morre na confirmação, numa nova troca, no `disable`, no
  `reset-2fa`, na troca de senha e no `set-password`. Confirmá-lo exige uma sessão viva do dono e o
  código do app novo.
- **`PATCH /channels/{id}` com campo desconhecido** responde `400 validation_failed` sem `fields`,
  como as outras rotas de recurso: o vocabulário fechado não tem nome para um campo inventado pelo
  cliente.
- **O `reset-2fa` não zera a marca do desafio** (`totp_challenge_mark`, Plano 8): ela só recusa
  desafios velhos.
- **Porta 9091 ocupada por outro programa** em desenvolvimento: o `--role=prober` não sobe, e a
  mensagem sugere o `METRICS_ADDR`.

Os limites dos Planos 7 e 8 seguem como estão no `PENDENCIAS-PARA-O-PLANO-8.md` e no
`PENDENCIAS-PARA-O-PLANO-9.md`.

## Herdadas, ainda abertas

**Dívidas menores** (grupo C): a seção "Dívidas menores herdadas, ainda abertas" do
`PENDENCIAS-PARA-O-PLANO-5.md`, inteira.

**R2** (grupo D): migrar `auth.PGStore` e `auth.TOTP` para sqlc. O método novo do Plano 8
(`ConsumeChallenge`) e os da troca de autenticador do Plano 9 (`internal/auth/totp_rotation.go`)
também são SQL à mão.
