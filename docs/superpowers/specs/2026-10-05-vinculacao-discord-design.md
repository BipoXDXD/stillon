# Vinculação da conta StillOn ao Discord

Estado: especificação aprovada pelo dono em 2026-10-05 e emendada em 2026-11-07 com as
decisões D1, D2 e D3 ([plano](../plans/2026-10-05-vinculacao-discord.md)), conferida contra a
`main` em `42715ac`. Implementação ainda pendente.

## Objetivo e decisões do dono

O usuário autenticado no StillOn vincula sua conta Discord e recebe o cargo
**Verificado** no servidor configurado. O cargo apenas identifica contas vinculadas:
não bloqueia canais, não concede administração e não comprova identidade civil ou
verificação de e-mail. O webhook existente continua responsável pelos avisos de
incidente; sua confirmação não verifica uma pessoa.

Esta fase usa as contas existentes do StillOn. Não inclui cadastro público, login
com Discord, criação automática de usuário, ingresso automático no servidor, DMs,
leitura de mensagens, novos canais ou alteração de permissões de membros.

## Fluxo para a pessoa

1. Em **Configurações → Discord**, clicar em **Vincular Discord**.
2. Autorizar a identificação básica na página oficial do Discord.
3. Voltar a Configurações, com o endereço limpo e a identidade vinculada exibida.
4. Se ainda não estiver no servidor, usar **Entrar no servidor** e depois
   **Verificar participação**. Se houver triagem de membros, concluí-la no Discord.
5. O StillOn confirma a participação e concede somente o cargo configurado.
   A interface mostra **Cargo confirmado** apenas depois da confirmação do efeito.

Estados apresentados: integração indisponível; não vinculada; vinculada e aguardando
participação; aguardando conclusão da triagem; cargo pendente; cargo confirmado;
remoção pendente; ação temporariamente indisponível. Cancelamento ou expiração do
OAuth produz orientação para iniciar novamente, sem perder a sessão do StillOn.

O usuário pode **Desvincular Discord**, com diálogo explicando a remoção do cargo.
Não existe troca silenciosa de identidade: desvincular e concluir a remoção antes
de vincular de novo, mesmo a mesma conta. Sair do StillOn não desfaz o vínculo já
confirmado.

## Autorização e retorno

Direção escolhida: Authorization Code com PKCE S256 (D1), no backend, com um cookie
temporário próprio. O painel atual usa Bearer em memória e refresh
HttpOnly/SameSite=Strict com caminho restrito. Não alterar esse cookie nem transportar
tokens do StillOn na URL.

- POST autenticado inicia a tentativa; exige o header CSRF existente, limite de
  requisições e família de sessão viva. Gera `state` e `code_verifier` independentes,
  com 32 bytes aleatórios cada; o verifier é base64url sem padding (43 caracteres).
  Guarda somente os dois hashes SHA-256, dono, FamilyID e validade de cinco minutos.
  Novo início invalida a tentativa anterior dessa família. Com vínculo existente, em
  qualquer estado, o início é recusado.
- O `code_challenge` enviado ao Discord é o base64url desse mesmo SHA-256, com
  `code_challenge_method=S256`. O hash do verifier não é segredo: quem o tem não obtém
  o verifier, e o callback só aceita a preimagem.
- O verifier vive só no cookie de ponte e faz os dois papéis: amarra a tentativa ao
  navegador e prova a troca do code. Nunca vai ao banco, a log ou à resposta JSON.
  Cookie host-only, HttpOnly, SameSite=Lax, Path exato do callback e Max-Age de cinco
  minutos. Secure obrigatório em produção. HTTP só é permitido no modo local explícito
  `DISCORD_ALLOW_INSECURE_LOOPBACK=true`, com PUBLIC_BASE_URL de loopback; nunca aceitar
  HTTP remoto por fallback. O padrão é false.
- Callback GET exige state e cookie da mesma tentativa, ainda válidos, e família
  viva. Consumo/reserva atômico impede dois callbacks de trocar o mesmo code.
  A ordem das travas segue LockUserSessions antes da tentativa/vínculo.
- Rede acontece fora da transação. O servidor troca o code com client secret e
  verifier, consulta a identidade e reconfere a família viva antes de persistir o
  vínculo. Revogação durante a rede impede a finalização. Falha após consumir a
  tentativa exige nova autorização; não repetir automaticamente a troca de code. As
  duas chamadas têm cinco segundos cada e cabem no prazo de 15 s da API.
- Cookie é apagado em sucesso ou falha validada. Redirect 303 vai para destino interno
  fixo, `/settings`; não carrega code, state, token ou texto de erro externo.
  Callback e respostas usam no-store e no-referrer. Logging não registra sua query
  nem o cookie.

O PKCE não tem queda para fluxo sem verifier: `code_challenge` vai em toda autorização.
A página principal de OAuth2 do Discord não traz esses parâmetros; o guia do Social SDK
mostra `code_verifier` junto do client secret na troca, sem nomear o método. Por isso a
aceitação real prova que um verifier errado é recusado. Se o provedor recusar o fluxo ou
não exigir o verifier, a ativação para e a decisão volta ao dono; state e cookie seguem
valendo de todo modo.

Escopo OAuth mínimo: `identify`. O bot consulta um único membro pelo DiscordID
confirmado. Não solicitar e-mail, lista de servidores, ingresso ou webhook ao usuário.
Tokens de acesso/refresh do Discord não são persistidos nem expostos ao painel;
existem apenas durante a autorização. A integração não depende de Gateway ou intents
privilegiadas.

Alternativas consideradas: callback público na SPA e POST final autenticado exigiriam
reter code durante bootstrap/login e limpar query antes do Authguard; o fluxo atual
de `next` poderia copiá-la. Cargo manual não atenderia à confirmação automática.
O cookie dedicado mantém esse processamento no backend sem relaxar o refresh. Guardar
o verifier cifrado na tentativa também funcionaria, mas põe no banco um segredo que o
cookie dispensa, e um segundo valor no cookie só adicionaria parsing.

## Persistência e efeitos externos

Duas tabelas próprias: tentativas OAuth e vínculos. A reconciliação é por estado
desejado (D2): cada DiscordID tem uma linha em `discord_links`, e essa linha é ao mesmo
tempo o vínculo e o trabalho pendente. Não há tabela de operações, lease, geração,
compensação nem tombstone.

A linha tem UUID, `user_id` único, DiscordID único global, nome de exibição, guild e
cargo gravados no vínculo, `desired` (`present` ou `absent`), `observed` (`unknown`,
`awaiting_member`, `awaiting_screening` ou `present`), data da última confirmação,
`next_attempt_at`, contador de tentativas, erro categorizado e `paused_at`. A remoção usa
a guild e o cargo gravados, nunca os da configuração atual. Snowflakes são strings
decimais canônicas, nunca números JavaScript. Nome de exibição é texto escapado e não
define identidade. Conflito de identidade retorna erro genérico, sem revelar outra
conta.

A linha só anda num sentido: nasce com `desired=present`, passa a `absent` ao
desvincular e é apagada quando a remoção se confirma. As duas chaves únicas fazem a
reserva: enquanto a linha existe, nem o DiscordID nem o usuário entram em outro vínculo.

Tentativas e vínculos usam FORCE RLS e escopo do dono. Callback recebe escopo mínimo
para localizar apenas a tentativa pela prova dupla. O worker tem escopo próprio, que lê,
atualiza e apaga `discord_links` e apaga tentativas vencidas, sem criar vínculo e sem ver
outras tabelas. Não ampliar os escopos de notificação, prober ou verificação de e-mail.
Auditoria e mudança de `desired` são gravadas na mesma transação, sem segredos; a
confirmação e a remoção do cargo também geram auditoria, com o IP nulo.

Um worker pequeno acompanha o papel API, compartilha seu ciclo de vida e roda a cada dez
segundos. Cada rodada tenta uma advisory lock de sessão numa conexão dedicada, sem
transação aberta; outra instância com a lock pula a rodada, e a queda do processo solta
a lock. Rodadas não se sobrepõem, e por isso não há claim por linha. A rodada lê um lote
limitado de linhas vencidas e não pausadas, em transação curta. Nenhuma transação
permanece aberta durante chamadas HTTP. Timeout do cliente externo é limitado; espera de
retry ocorre no agendamento, nunca dormindo no handler.

Para cada linha o worker lê o membro, decide, aplica no máximo um efeito e relê:

- `desired=present`: membro ausente vira `awaiting_member`; triagem pendente vira
  `awaiting_screening`; nos dois casos nada fica agendado até a nova verificação
  explícita. Com o cargo já presente, confirma sem PUT. Sem ele, faz PUT do cargo e relê.
- `desired=absent`: membro ausente ou cargo já fora bastam. Se não, faz DELETE do cargo e
  relê. Confirmada a ausência, a linha é apagada.

PUT/DELETE de um único cargo evita sobrescrever outros cargos e é idempotente. Estado
confirmado só é publicado depois da releitura. A gravação do resultado é condicional ao
`desired` lido: se ele mudou durante o efeito, a escrita não afeta nenhuma linha, e a
rodada seguinte reconcilia o estado atual. Assim, um PUT em voo não sobrevive a um
desvínculo, e um timeout ambíguo se resolve pela leitura da rodada seguinte, sem
compensação.

429 respeita o prazo informado pelo provedor, com teto de 24 horas contra valor absurdo,
e encerra a rodada. Rede/5xx usa backoff exponencial limitado a quinze minutos. Após dez
tentativas falhas, pausar e permitir nova tentativa explícita somente depois de quinze
minutos e de qualquer Retry-After maior; a pausa guarda esse prazo em `next_attempt_at`.
Cliques não zeram backoff de trabalho ativo, não antecipam o prazo e não duplicam
operações, porque só alteram uma linha que já existe. Uma retomada elegível abre novo
orçamento de dez tentativas e preserva a auditoria anterior. 401/403 e cargo inválido
pausam imediatamente para correção operacional.

Desvincular grava `desired=absent`, cancela tentativas abertas do dono, limpa pausa e
contador e agenda a remoção sem antecipar um prazo já informado pelo provedor. Repetir o
pedido não muda nada. Sem vínculo, é sucesso sem efeito.

Não existe atomicidade entre Discord e Postgres: estados incertos continuam pendentes
até reconciliação, inclusive após queda do worker. A FK do dono em `discord_links` é
RESTRICT: nenhum CASCADE apaga a linha antes de retirar um cargo concedido, e uma
exclusão de conta futura terá de esperar a remoção; não acrescentar exclusão pública de
conta nesta fase. Tentativas consumidas ou expiradas são limpas 24 horas após a
expiração, pelo próprio worker. Com a função desligada, linhas pendentes ficam paradas e
retomam quando ela voltar.

Risco residual aceito: um PUT que o Discord aplique depois de a linha ter sido apagada
deixa o cargo sem linha para reconciliar. Exige perder a lock durante uma chamada de
cinco segundos e outra instância concluir a remoção nesse intervalo. O cargo não
autoriza nada, e um administrador do servidor o retira.

Não há sincronização permanente de todos os membros. Saída/banimento não provoca
reingresso; mudança manual de cargo após a confirmação só é observada em nova
verificação explícita. A tela informa a data da última confirmação. Administradores
continuam controlando o servidor. O cargo não é uma barreira de autorização no produto.

## Contratos e configuração

API autenticada: GET `/api/v1/account/discord` para estado; POST
`/api/v1/account/discord/authorize` para iniciar; POST
`/api/v1/account/discord/check` para reconferir/reagendar; DELETE
`/api/v1/account/discord` para desvincular. Callback público dedicado:
GET `/api/v1/oauth/discord/callback`, na allowlist explícita de rotas públicas. Rotas
autenticadas herdam proteções existentes. Início e check dividem um balde próprio por
usuário, porque cada check pode virar chamadas do bot. Início com vínculo existente
responde 409. Configuração desligada devolve estado indisponível sem iniciar OAuth. Início retorna
JSON com URL OAuth validada; GET informa estado e resultado público da última tentativa,
sem segredos. Check e DELETE exigem família viva na transação e retornam 202 com estado
pendente quando há trabalho, sem anunciar efeito externo concluído. Check de operação
ativa apenas preserva/reinforma seu estado e prazo; não reinicia o orçamento. Linha sem
trabalho pendente volta a ser agendada, o que cobre a participação nova e a reconferência
do cargo.

Configuração proposta: `DISCORD_OAUTH_CLIENT_ID`, `DISCORD_OAUTH_CLIENT_SECRET`,
`DISCORD_BOT_TOKEN`, `DISCORD_GUILD_ID`, `DISCORD_VERIFIED_ROLE_ID` e
`DISCORD_INVITE_URL`. Callback deriva de `PUBLIC_BASE_URL` validada e deve coincidir
com a URL cadastrada no aplicativo. Nunca derivar origem de Host enviado pelo cliente.
Convite aceita somente HTTPS, host exato discord.gg ou discord.com com caminho de
convite, sem credenciais, query ou fragmento. A opção local não relaxa essa validação.
Tudo ausente desliga a função; combinação parcial rejeita a inicialização com erro
sem valores secretos. URLs externas de API são fixas no cliente de produção: só um
binário compilado com a build tag `e2e` aceita outra origem, e apenas de loopback (D3).

Produção é o recurso do Coolify: as seis variáveis entram em `deploy/coolify/app.json` com
padrão vazio (`${DISCORD_BOT_TOKEN:-}`), e os valores são variáveis privadas do recurso.
`DISCORD_ALLOW_INSECURE_LOOPBACK` não entra nesse arquivo. O release só atualiza imagens,
e a configuração dos containers fica fixada no commit de infraestrutura do recurso; o
merge do código, portanto, não liga a função. Ligar exige atualizar esse commit e
publicar o recurso de novo, ainda desligado, e só depois definir as seis variáveis de uma
vez, porque a combinação parcial impede o boot.

Bot instalado somente no servidor StillOn, com Gerenciar cargos e cargo acima de
Verificado, sem Administrador. O aplicativo, convite do bot e configuração de segredos
serão preparados de forma revisável antes da confirmação de acesso exigida pelo
navegador. Token e client secret vão ao Bitwarden e às variáveis privadas do recurso;
nunca ao chat, repositório, capturas ou logs.

## Verificação e conclusão

Testes com PostgreSQL real cobrem isolamento RLS, unicidade concorrente, replay,
expiração, cookie trocado/ausente, refresh da mesma família, revogação durante rede,
rollback de auditoria/trabalho, rodadas concorrentes pela advisory lock e efeito em voo
contra desvinculação. Cliente Discord controlado cobre recusa, verifier errado, membro
ausente/pendente, 401/403/429/5xx, timeout ambíguo, payload inválido e preservação dos
outros cargos.

Testes de interface e navegador cobrem iniciar, cancelar, retornar com URL limpa,
sessão expirada, integração desligada, estados pendentes, repetir verificação e
desvincular. A ida e volta do OAuth no navegador usa um binário compilado com a build tag
`e2e` (D3), apontado para um provedor falso em loopback que confere o PKCE; o binário de
produção não contém esse caminho. Novos testes precisam detectar sabotagens compiláveis,
com restauração e GREEN. Gate final: build/vet/make test nos PG17/18, suíte web, typecheck/lint/build,
checagem de nome, revisão independente e CI do HEAD antes do merge por PR.

Aceitação real exige configuração privada, consentimento de uma conta de teste do
operador, recusa de um verifier errado, vínculo exibido e cargo observado no Discord;
depois, desvinculação com remoção comprovada. Enquanto isso não ocorrer, relatar implementação testada em
bancada, sem afirmar ativação real. Nenhuma etapa desta proposta está implementada
apenas por este documento existir.

## Referências

Documentação oficial consultada via Context7 e conferência direta:
[OAuth2](https://docs.discord.com/developers/topics/oauth2),
[membros e cargos](https://docs.discord.com/developers/resources/guild#add-guild-member-role),
[hierarquia](https://docs.discord.com/developers/topics/permissions#permission-hierarchy),
[PKCE com client secret](https://docs.discord.com/developers/discord-social-sdk/development-guides/provisional-accounts/merging-accounts).
Integração local: `internal/httpapi/middleware/authn.go`, `internal/account/account.go`,
`internal/storage/queries/sessions.sql`, `internal/notify/discord.go`, `deploy/coolify/app.json`
e o fluxo existente de Configurações. Esta proposta usa esses padrões sem modificar o webhook.
