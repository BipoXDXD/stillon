# Pendências depois do Plano 8

Este arquivo existe porque o ledger de execução vive em `.superpowers/`, fora do controle de
versão. O que está aqui é o subconjunto que não pode se perder. Ele substitui o
`PENDENCIAS-PARA-O-PLANO-8.md` como estado corrente; os anteriores ficam como histórico.

## Fechadas no Plano 8

O grupo A das pendências (segurança e correção), escolhido pelo usuário em 2026-09-25. Spec:
`docs/superpowers/specs/2026-09-25-plano-8-endurecimento-design.md`.

| Pendência | Como fechou |
|---|---|
| Desafio do 2FA reutilizável por cinco minutos (`jti` em denylist ou `challenge_id` consumido) | Marca d'água em `users.totp_challenge_mark` (migration `00008`) e claim `cat` em microssegundos no desafio; o `/totp/verify` consome o desafio depois do código certo, num `UPDATE` condicional. Desafio reusado: `401 unauthorized` |
| `Unmap` no caminho `X-Forwarded-For`/`RemoteAddr` do `ip_hash` | Toda saída de `middleware.ClientIP` passa por `normalizar` (`Unmap`, zona recusada). O limitador já normalizava a chave do balde (`chaveDe`); o defeito atingia o `ip_hash` e o `ip` da auditoria e de `GET /sessions` |
| Nomes de cabeçalho perigosos em `CLIENT_IP_HEADER` | Denylist no boot: `Forwarded`, a família `X-Forwarded-*`, `Host`, `Cookie`, `Authorization`, `Proxy-Authorization`, os hop-by-hop, `Content-Length` e `Content-Type` |
| `HashPasswordWith` com parâmetros fora de faixa | Devolve `ErrInvalidHash` com `t=0`, `p=0`, memória abaixo de `8*p` ou salt vazio; a regra é a mesma função do `decodeHash` |
| `LogValue` de `Spec.Headers` (M6 do Plano 2) | `prober.Spec.LogValue` mostra os nomes dos headers, nunca os valores |
| `MaxChannels` "conta e insere sem trava" | **O registro estava errado** (R9 da spec do Plano 5): a trava (`LockUser`, `FOR UPDATE` na linha de `users`) existe desde o Plano 3. Faltava a prova: `TestConcurrentCreatesDoNotPassTheChannelLimit`, que fica vermelho sem a trava |
| Teste de `https` com host de loopback (PR #11) | `TestLoadRejectsDemoKeysOverHTTPS` cobre `https://localhost` e `https://127.0.0.1` |
| Grep de renomeação casava comentários (PR #11) | A tabela de nomes do runbook ganhou a linha dos comentários que citam os nomes |

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

## Limites conhecidos do Plano 8

- **Consumir um desafio invalida os mais antigos ainda abertos** do mesmo usuário. Dois logins
  quase simultâneos em dois aparelhos: se o mais novo terminar o 2FA primeiro, o mais antigo
  recebe 401 e refaz a senha.
- **O painel diz "O tempo para digitar o código acabou"** também para o desafio recusado por
  já ter sido usado ou por um mais novo ter sido consumido (`PaginaLogin.tsx` trata todo
  `401 unauthorized` do verify como desafio vencido). O remédio é o mesmo, entrar de novo; só
  o texto não é exato. O duplo clique não chega a isso: o botão fica desabilitado enquanto
  envia.
- **Desafios emitidos antes do deploy do Plano 8 morrem** (não têm `cat`): no pior caso, um
  login refeito dentro de cinco minutos.
- **Relógio andando para trás** entre dois desafios do mesmo usuário deixa o mais novo com
  `cat` menor que a marca: 401 e um login refeito.
- **Um desafio reusado com o código certo zera o contador de falhas** antes de ser recusado
  (o `ResetFailures` vem antes do consumo). Chegar lá exige a senha e o segundo fator.
- **Endereço de conexão com zona** (par IPv6 link-local ligado direto no app) continua como
  veio, com a zona. Não acontece atrás de proxy.

Os limites do Plano 7 seguem como estão no `PENDENCIAS-PARA-O-PLANO-8.md`, seção "Limites
conhecidos do Plano 7".

## Herdadas, ainda abertas

**Operação** (grupo B, fora do Plano 8 por decisão do usuário):

- **Recuperação do 2FA pelo CLI:** quem perde o autenticador e os códigos de recuperação só sai
  por SQL.
- **Troca de autenticador** sem desativar e reativar o 2FA.
- **Rota para desabilitar canal** (a coluna `enabled` já é respeitada pelo despachante).
- **Porta de métricas em desenvolvimento** com `--role=api` e `--role=prober` no mesmo host
  (M7 do Plano 2).

**Dívidas menores** (grupo C): a seção "Dívidas menores herdadas, ainda abertas" do
`PENDENCIAS-PARA-O-PLANO-5.md`, inteira.

**R2** (grupo D): migrar `auth.PGStore` e `auth.TOTP` para sqlc. O método novo do Plano 8
(`ConsumeChallenge`) também é SQL à mão, no mesmo arquivo.
