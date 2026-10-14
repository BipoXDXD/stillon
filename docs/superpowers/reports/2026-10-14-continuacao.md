# Continuidade do StillOn — 2026-10-14

Atualiza o [registro de 2026-10-05](2026-10-05-continuacao.md), preservando o histórico. Fecha a
Task 6 do [plano da VPS compartilhada](../plans/2026-10-13-vps-compartilhada.md): a execução
guiada na VPS terminou.

## Estado real

O StillOn está **no ar** em <https://stillon.tech>, com o deploy do commit `b685891` (o último, de
2026-10-14; ver "Deploy de 2026-10-14 (feito)"). Antes disso o deploy era o
`1a5e2ca` (a imagem da `main` depois do #67, que tirou o `artifactType` do `index.json`). O
certificado é do Let's Encrypt, válido até 2027-01-12. O StillOn fica atrás da **borda Caddy compartilhada com o GoblinShift**, em
`/srv/edge`, como a [spec](../specs/2026-10-13-vps-compartilhada-design.md) decidiu: a borda
termina o TLS por domínio, e o Caddy do StillOn (IP fixo na rede `edge`) recebe só o domínio
`stillon.tech`.

## Feito e conferido

- **Task 6.1 a 6.7** do plano, cada passo com o seu Confira: Docker e `live-restore` (6.1, 6.2),
  troca da borda do nginx para o Caddy (6.3), regra da 443 no painel (6.4), runbook do 3.3 ao 4.8
  (6.5), o StillOn na borda com o certificado emitido (6.6) e os blocos 5 a 9 (6.7).
- **Runbook, blocos 3 a 9.** Inclui o **7.1 e o 7.2**: o backup rodou verde pelo Actions e o
  **ensaio de restauração deu certo** (o dump decifrado e restaurado num Postgres descartável, com a cópia
  conferida pelo `verificar.sql`, como manda o 7.2). E o **9.2 a 9.6**: IP do cliente certo
  atrás das duas camadas de proxy, `Host` estranho sem chegar ao app, certificado e cabeçalhos,
  segredos e compose de produção.
- **Reboot de teste (9.6), em 2026-10-14 às 03:29 (-03).** Voltaram sozinhos: StillOn `healthy`, a
  borda de pé, o vigia agendado, `ufw` ativo, `fail2ban` com a jaula do SSH, relógio sincronizado,
  `/readyz` com `200` de fora, o GoblinShift respondendo `404` pelo IP (como antes) e todos os
  contêineres de volta.
- **Limpeza.** O `sudo` sem senha temporário, usado só na execução guiada, foi **removido e
  conferido** (`sudo -n` pede senha). A pasta `~/borda` da VPS foi apagada.
- **Hostinger (7.3).** Os backups semanais automáticos existem (2026-10-03 e 2026-10-10, cerca de
  1,5 GB cada).
- **healthchecks.io.** Sem defeito: a conta é de outro e-mail do dono, e os alarmes, o do reboot
  inclusive, foram entregues lá.

### 9.1: portas vistas de fora

Por dentro (`ss`), só 22, 80 e 443 escutam em todas as interfaces; o resto (a porta interna do
GoblinShift, o agente da Hostinger e o DNS local) só em `127.x`, e o StillOn não publica porta.
De fora, `nmap -Pn -sT -T4 -p- --open` do Mac em 2026-10-14 (03:37–04:22 -03, 45 min): das 65.535
portas TCP, **só 22, 80 e 443 abertas**; as outras 65.532 sem resposta (o firewall do painel
descarta).

## Decisões

- **Artifacts do backup ficam públicos** (o repositório é público). Eles são cifrados com o `age`,
  então a confidencialidade depende só da chave privada, que só o dono tem. Custo aceito: tamanho,
  data e frequência do dump, e as contagens do log do `backup`, ficam visíveis. Registrado também
  no runbook (seção do repositório público). O pacote do GHCR continua privado.
- **2FA do usuário do StillOn adiado por decisão do dono.** O runbook (5.2) segue válido para
  quando ele ligar.

## Armadilha registrada

`[skip ci]` no **título do PR** vai para a mensagem do squash merge e **pula o CI da `main`
também**. Sem o CI da `main`, o job `publish` não roda e a ponta da `main` fica sem imagem. Para
documentação isso é o desejado; mas se o PR mexer em código, o `publish` precisa de outro caminho
(um PR de código sem `[skip ci]`). Foi o que aconteceu com o #65, e o runbook (4.8) agora avisa:
o `gh run list --status success --limit 1` devolveria o SHA anterior, sem o conserto do #67.

## Runbook corrigido

O 6.8 aplicou ao [runbook](../../deploy/vps.md) (e ao [mac.md](../../deploy/mac.md)) o que a
execução real mostrou que estava errado ou impraticável: o `nc` na 443 do 4.9 (a 443 só abre com o
trecho), o `gh run watch` sem id, o SHA do 4.8, o alias `vc` e as consultas do banco em arquivo, a
emissão assíncrona do certificado, o 5.1 e o 5.2 (entradas do gerenciador antes, TTY, só **Copiar**),
os nomes reais dos botões e o tempo de fechamento do incidente (6.1, 6.2), o `RUN_ID` exato do
7.1 ao 7.2, o `zsh`/`bash`, o login do GHCR no Chaveiro do Mac, o vigia (comandos curtos, domínio
digitado no editor), o `nmap -sT` e o 9.6 com linha de base. O Confira do 6.3 do plano também foi
corrigido: o `Server: nginx` que aparece é o nginx interno do GoblinShift; a prova da borda é
`Via: 1.1 Caddy`.

## Pendências do dono

1. **URGENTE: renovar a VPS** no painel da Hostinger. Ela **expira em 2026-10-25**; sem a
   renovação, o StillOn e o GoblinShift saem do ar.
2. **Ligar o 2FA** do usuário do StillOn (runbook 5.2).
3. **Mover o login do StillOn para o Bitwarden** (entrada "StillOn – login do painel").
4. *Opcional:* adicionar também o e-mail principal na integração do healthchecks.io.

## Desenvolvimento na ausência do dono (tarde de 2026-10-14)

O dono pediu trabalho autônomo com vários agentes, sem perguntas. Cada PR de código passou por
revisão independente antes do merge, e todos tiveram CI verde (os 9 checks e, a partir do #77, o
CodeQL). Tudo isso foi para produção no deploy de 2026-10-14 (ver "Deploy de 2026-10-14 (feito)").

| PR | O que entrou |
|---|---|
| #73 | Lembretes de incidente aberto ([design](../specs/2026-10-14-lembretes-design.md)). O Discord avisa a cada 30 min e o e-mail a cada 2 h; depois de 6 h de incidente, a cada 6 h e a cada 24 h. O reconhecimento corta os lembretes. A migration é a 00012. |
| #74, #80, #86 | Acessibilidade do painel: bordas com contraste ≥ 3:1, estado sinalizado também sem cor (minigráfico e página pública), alvos de toque de 44 px. |
| #75 | A página pública mostra a queda em curso sem atraso, com teto de 24 h nos checks ainda não consolidados. Rollups gravados no futuro são ignorados. |
| #76 | `stop_grace_period: 30s` no `app` do `compose.prod.yaml`. |
| #77 | Reforço de segurança: sessões com idade máxima de 90 dias, JSON estrito em todas as rotas, `Retry-After` nas respostas 429, JWT sem `exp` recusado, IP disfarçado e caracteres invisíveis recusados nos nomes, testes de nega-por-padrão e de acesso a recurso alheio em todas as rotas, `SECURITY.md`. O gitleaks não achou nada nos 409 commits. |
| #79 | A troca de senha relê o 2FA antes de enviar. |
| #87, #88 | Painel: a mesma regra de caracteres invisíveis do servidor, reenvio de verificação sem POST duplo, tempos relativos corretos, refetch dos incidentes. |
| #89 | Prazos em cada rodada do sondador, do dispatcher e da manutenção. O pool usa `statement_timeout` de 30 s e `idle_in_transaction_session_timeout` de 1 min. O shutdown do dispatcher tem orçamento de 12 s. Erros de banco deixam de passar por "alvo apagado". |
| #90 | O limitador de requisições despeja em O(1). Os erros do backup guardam a causa. A migration 00013 cria os CHECKs de latência do rollup como `NOT VALID`. Os testes do Argon2 não usam mais `sleep`. |
| #70–#72 | Plano da vinculação Discord revisado contra o código; runbook com um comando por bloco, todos numerados e com o lugar onde rodam. |
| #78, #81–#85 | Atualizações do Dependabot, todas minor ou patch. |

**Configuração do GitHub (autorizada pelo dono):**
- ruleset da `main` com PR obrigatório, 9 checks, sem force push nem exclusão; o admin só contorna no merge de PR, então os PRs `[skip ci]` entram com `gh pr merge --admin`;
- tags `v*` imutáveis;
- alertas e correções de segurança do Dependabot;
- relato privado de vulnerabilidade;
- CodeQL;
- actions só fixadas por SHA;
- aprovação para PR de fork.

**Decisões tomadas sem o dono:**
- o desenho dos lembretes: uma linha por lembrete com `seq` e UNIQUE no banco, e o nome do alvo gravado na fila para o corpo não mudar entre tentativas;
- o teto de 24 h nos checks brutos da página pública;
- os CHECKs da 00013 como `NOT VALID`;
- E7, a diferença de cerca de 0,4 ms no login, foi medida e não mexida, porque detectá-la exige uns 88 mil logins por e-mail.

## Deploy de 2026-10-14 (feito)

1. **`compose.prod.yaml` copiado para `/opt/stillon`.** O deploy só envia a imagem; sem essa
   cópia, o grace period de 30 s do #76 não valeria.
2. **Deploy do `e7db119` às 12:11 UTC**, com as migrations 00012 e 00013.
3. **Consulta da 00013 em produção: voltou vazia** (a tabela estava vazia, então nenhuma linha
   violava os CHECKs). A 00014 (#93) valida os dois CHECKs com `VALIDATE CONSTRAINT`.
4. **Deploy do `b685891` às 12:43 UTC**, com a 00014. É o que está no ar.

5. **Lembretes conferidos em produção**, com o alvo de teste "Teste de lembretes (apagar)" (sempre
   503, criado direto no banco). Já apagado.
   - O incidente abriu às 12:16 UTC e o e-mail de abertura saiu.
   - Às 12:58 houve um ack pelo painel, provavelmente do dono, que cortou o primeiro lembrete
     (comportamento certo). O ack não gera linha no `audit_log`: achado menor, sem correção ainda.
   - O ack foi tirado e a base recuou 2 h no banco. Com isso, o lembrete por e-mail (`seq 1`) foi
     enfileirado e enviado no tique seguinte (14:20:10 → 14:20:13).
   - Com o ack de novo e a base do segundo lembrete também vencida, nenhum `seq 2` saiu em 90 s
     (três tiques). **O reconhecimento corta os lembretes.**

## Revisão completa (tarde de 2026-10-14)

Três frentes: código, documentação e segurança.

- **Código:** 2 defeitos, cada um com PR: os lembretes de um alvo pausado e a série longa da
  página pública, que perdia a queda em curso.
- **Documentação:** este PR (licença MIT, estado pós-deploy, trechos do README e comentários dos
  workflows que ainda tratavam o repositório como privado).
- **Segurança:** nenhum segredo (gitleaks em todo o histórico e nas refs de PR) e nenhuma falha
  alta. Corrigidos em PR: o log do app no Actions público, o endurecimento do compose e o host de
  rótulo único. **Ficam com o dono:** o environment `production`, o e-mail noreply nos commits, a
  sessão viva nas rotas de recurso e os dumps públicos por 30 dias.

## Decisões pendentes do dono

- **Vinculação Discord:** D1 (PKCE), D2 (mecanismo de cargo) e D3 (provedor falso no E2E), com
  recomendação no [plano](../plans/2026-10-05-vinculacao-discord.md). A migration dela é a 00015.
- **Environment `production` no GitHub:** passar os segredos da VPS do nível do repositório
  para o environment, colando do Bitwarden.
- **Exigir sessão viva nas rotas de recurso:** hoje um access token de sessão encerrada vale por
  até 15 min.
- **Tipos da API:** gerar de uma spec OpenAPI, ou manter o `schemas.ts` escrito à mão.
- **Lembretes:** um canal pausado por mais de 30 dias com o incidente aberto perde a base dos
  lembretes, porque a retenção apaga as linhas. A recomendação é registrar a limitação e não
  mudar nada.

## Redesign decidido

Direção C, "Editorial claro" (Fraunces + Inter, primária índigo), na
[spec](../specs/2026-10-14-redesign-painel-design.md). O painel antigo fica na tag
`painel-antes-do-redesign` e em `docs/design/painel-antes/`.

## Depois

- **Vinculação Discord**, assim que D1, D2 e D3 estiverem decididos.
- **Redesign do painel**, conforme a spec acima.

Não confundir planejamento aprovado com código pronto, nem anunciar deploy sem conferência.
