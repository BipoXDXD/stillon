# Continuidade do StillOn — 2026-10-14

Atualiza o [registro de 2026-10-05](2026-10-05-continuacao.md), preservando o histórico. Fecha a
Task 6 do [plano da VPS compartilhada](../plans/2026-10-13-vps-compartilhada.md): a execução
guiada na VPS terminou.

## Estado real

O StillOn está **no ar** em <https://stillon.tech>, com o deploy do commit `1a5e2ca` (a imagem da
`main` depois do #67, que tirou o `artifactType` do `index.json`). O certificado é do Let's Encrypt,
válido até 2027-01-12. O StillOn fica atrás da **borda Caddy compartilhada com o GoblinShift**, em
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

### 9.1: portas vistas de fora

<!-- NMAP -->

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

1. **Ligar o 2FA** do usuário do StillOn (runbook 5.2).
2. **Mover o login do StillOn para o Bitwarden** (entrada "StillOn – login do painel").
3. **Conferir a conta do healthchecks.io.** O e-mail de alarme nunca chegou ao Gmail do dono, o que
   indica que a conta usa outro e-mail. Sem isso, o vigia externo avisa no vazio.
4. **7.3:** conferir no painel da Hostinger que o backup semanal está ligado.

## Depois

- **Vinculação Discord:** o [plano](../plans/2026-10-05-vinculacao-discord.md) aguarda revisão.
- **Política de lembretes** aprovada em 2026-10-05, ainda sem implementação.
- **Redesign do painel.**
- Os PRs do Dependabot que restarem.

Não confundir planejamento aprovado com código pronto, nem anunciar deploy sem conferência.
