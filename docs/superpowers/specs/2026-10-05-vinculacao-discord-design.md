# Vinculação da conta StillOn ao Discord

Estado: proposta para revisão do dono; nenhuma implementação deste fluxo está concluída.

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
de vincular outra conta. Sair do StillOn não desfaz o vínculo já confirmado.

## Autorização e retorno

Direção escolhida: Authorization Code no backend com um cookie temporário próprio.
O painel atual usa Bearer em memória e refresh HttpOnly/SameSite=Strict com caminho
restrito. Não alterar esse cookie nem transportar tokens do StillOn na URL.

- POST autenticado inicia a tentativa; exige o header CSRF existente, limite de
  requisições e família de sessão viva. Gera state e nonce de navegador independentes,
  com 32 bytes aleatórios cada. Guarda somente hashes, dono, FamilyID e validade de
  cinco minutos. Novo início invalida a tentativa anterior dessa família.
- Cookie temporário host-only, HttpOnly, SameSite=Lax, Path exato do callback e
  Max-Age de cinco minutos. Secure obrigatório em produção. HTTP só é permitido no
  modo local explícito `DISCORD_ALLOW_INSECURE_LOOPBACK=true`, com PUBLIC_BASE_URL
  de loopback; nunca aceitar HTTP remoto por fallback. O padrão é false.
- Callback GET exige state e cookie da mesma tentativa, ainda válidos, e família
  viva. Consumo/reserva atômico impede dois callbacks de trocar o mesmo code.
  A ordem das travas segue LockUserSessions antes da tentativa/vínculo.
- Rede acontece fora da transação. O servidor troca o code, consulta a identidade e
  reconfere a família viva antes de persistir o vínculo e o trabalho de cargo.
  Revogação durante a rede impede a finalização. Falha após consumir a tentativa
  exige nova autorização; não repetir automaticamente a troca de code.
- Cookie é apagado em sucesso ou falha validada. Redirect 303 vai para destino interno
  fixo em Configurações; não carrega code, state, token ou texto de erro externo.
  Callback e respostas usam no-store e no-referrer. Logging não registra sua query.

Escopo OAuth mínimo: `identify`. O bot consulta um único membro pelo DiscordID
confirmado. Não solicitar e-mail, lista de servidores, ingresso ou webhook ao usuário.
Tokens de acesso/refresh do Discord não são persistidos nem expostos ao painel;
existem apenas durante a autorização. A integração não depende de Gateway ou intents
privilegiadas.

Alternativas consideradas: callback público na SPA e POST final autenticado exigiriam
reter code durante bootstrap/login e limpar query antes do Authguard; o fluxo atual
de `next` poderia copiá-la. Cargo manual não atenderia à confirmação automática.
O cookie dedicado mantém esse processamento no backend sem relaxar o refresh.

## Persistência e efeitos externos

Adicionar tabelas próprias para tentativas OAuth, vínculos e operações de cargo.
Vínculo tem UUID, user_id único, DiscordID único global, guild/cargo configurados,
estado desejado, geração e datas de confirmação. Snowflakes são strings decimais
canônicas, nunca números JavaScript. Nome de exibição é texto escapado e não define
identidade. Conflito de identidade retorna erro genérico, sem revelar outra conta.

Tentativas e vínculos usam FORCE RLS e escopo do dono. Callback recebe escopo mínimo
para localizar apenas a tentativa pela prova dupla; worker tem acesso somente ao
trabalho necessário. Não ampliar os escopos de notificação, prober ou verificação de
e-mail. Auditoria e intenção de cargo são gravadas na mesma transação, sem segredos.

Operações duráveis add/remove usam lease, geração e chave única. Um worker pequeno
acompanha o papel API, compartilha seu ciclo de vida e consulta trabalhos a cada dez
segundos. Leases permitem várias instâncias sem duplicar trabalho deliberadamente.
Nenhuma transação permanece aberta durante chamadas HTTP. Timeout do cliente externo
é limitado; espera de retry ocorre no agendamento, nunca dormindo no handler.

Antes de add, consultar membro e triagem; ausência ou triagem pendente impede cargo.
PUT/DELETE de um único cargo evita sobrescrever outros cargos. Estado confirmado só
é publicado após resposta válida ou reconferência do efeito. 429 respeita o prazo
informado pelo provedor; rede/5xx usa backoff exponencial limitado a quinze minutos.
Após dez tentativas falhas, pausar e permitir nova tentativa explícita somente depois
de quinze minutos e de qualquer Retry-After maior. Cliques não zeram backoff de
trabalho ativo, não antecipam o prazo e não duplicam operações. Uma retomada elegível
abre novo orçamento de dez tentativas e preserva a auditoria anterior. 401/403 e
cargo inválido pausam imediatamente para correção operacional.

Desvincular cancela tentativas, avança geração e agenda remoção. Serializar trabalho
por DiscordID entre todas as gerações, inclusive retirada e nova associação. DiscordID
fica reservado até remoção confirmada (ou membro comprovadamente ausente), sem lease
ativo ou operação antiga/compensação pendente. Operação em voo reconfere a geração
depois do efeito; se ficou obsoleta, agenda reconciliação com o estado desejado ATUAL
da identidade, nunca uma remoção cega baseada na geração anterior. Assim, uma
compensação atrasada não retira o cargo de uma nova associação válida.

Não existe atomicidade entre Discord e Postgres: estados incertos continuam pendentes
até reconciliação, inclusive após queda do worker. Não apagar trabalho por CASCADE
antes de retirar um cargo concedido. Tombstone mínimo é removido 24 horas depois de
conclusão e ausência de operações antigas; tentativas consumidas ou expiradas são
limpas 24 horas após a expiração. Operações pausadas mantêm só identidade, geração,
estado, datas e erro categorizado enquanto o efeito não for resolvido. Eventual
exclusão de conta deve transferir esse trabalho mínimo à limpeza antes de excluir
dados do dono; não acrescentar exclusão pública de conta nesta fase.

Não há sincronização permanente de todos os membros. Saída/banimento não provoca
reingresso; mudança manual de cargo após a confirmação só é observada em nova
verificação explícita. A tela informa a data da última confirmação. Administradores
continuam controlando o servidor. O cargo não é uma barreira de autorização no produto.

## Contratos e configuração

API autenticada: GET `/api/v1/account/discord` para estado; POST
`/api/v1/account/discord/authorize` para iniciar; POST
`/api/v1/account/discord/check` para reconferir/reagendar; DELETE
`/api/v1/account/discord` para desvincular. Callback público dedicado:
GET `/api/v1/oauth/discord/callback`. Rotas autenticadas herdam proteções existentes.
Configuração desligada devolve estado indisponível sem iniciar OAuth. Início retorna
JSON com URL OAuth validada; GET informa estado e resultado público da última tentativa,
sem segredos. Check e DELETE exigem família viva na transação e retornam 202 com estado
pendente quando há trabalho, sem anunciar efeito externo concluído. Check de operação
ativa apenas preserva/reinforma seu estado e prazo; não reinicia o orçamento.

Configuração proposta: `DISCORD_OAUTH_CLIENT_ID`, `DISCORD_OAUTH_CLIENT_SECRET`,
`DISCORD_BOT_TOKEN`, `DISCORD_GUILD_ID`, `DISCORD_VERIFIED_ROLE_ID` e
`DISCORD_INVITE_URL`. Callback deriva de `PUBLIC_BASE_URL` validada e deve coincidir
com a URL cadastrada no aplicativo. Nunca derivar origem de Host enviado pelo cliente.
Convite aceita somente HTTPS, host exato discord.gg ou discord.com com caminho de
convite, sem credenciais, query ou fragmento. A opção local não relaxa essa validação.
Tudo ausente desliga a função; combinação parcial rejeita a inicialização com erro
sem valores secretos. URLs externas de API são fixas no cliente de produção.

Bot instalado somente no servidor StillOn, com Gerenciar cargos e cargo acima de
Verificado, sem Administrador. O aplicativo, convite do bot e configuração de segredos
serão preparados de forma revisável antes da confirmação de acesso exigida pelo
navegador. Token e client secret vão ao Bitwarden/ambiente local; nunca ao chat,
repositório, capturas ou logs. A ativação em VPS continua sendo uma etapa guiada separada.

## Verificação e conclusão

Testes com PostgreSQL real cobrem isolamento RLS, unicidade concorrente, replay,
expiração, cookie trocado/ausente, refresh da mesma família, revogação durante rede,
rollback de auditoria/trabalho, leases e compensação grant versus desvinculação.
Cliente Discord controlado cobre recusa, membro ausente/pendente, 401/403/429/5xx,
timeout ambíguo, payload inválido e preservação dos outros cargos.

Testes de interface e navegador cobrem iniciar, cancelar, retornar com URL limpa,
sessão expirada, integração desligada, estados pendentes, repetir verificação e
desvincular. Novos testes precisam detectar sabotagens compiláveis, com restauração
e GREEN. Gate final: build/vet/make test nos PG17/18, suíte web, typecheck/lint/build,
checagem de nome, revisão independente e CI do HEAD antes do merge por PR.

Aceitação real exige configuração privada, consentimento de uma conta de teste do
operador, vínculo exibido e cargo observado no Discord; depois, desvinculação com
remoção comprovada. Enquanto isso não ocorrer, relatar implementação testada em
bancada, sem afirmar ativação real. Nenhuma etapa desta proposta está implementada
apenas por este documento existir.

## Referências

Documentação oficial consultada via Context7 e conferência direta:
[OAuth2](https://docs.discord.com/developers/topics/oauth2),
[membros e cargos](https://docs.discord.com/developers/resources/guild#add-guild-member-role),
[hierarquia](https://docs.discord.com/developers/topics/permissions#permission-hierarchy).
Integração local: `internal/httpapi/middleware/authn.go`, `internal/account/account.go`,
`internal/storage/queries/sessions.sql`, `internal/notify/dispatcher.go` e o fluxo
existente de Configurações. Esta proposta usa esses padrões sem modificar o webhook.
