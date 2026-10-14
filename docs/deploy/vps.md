# StillOn numa VPS da Hostinger: do zero ao ar, e a operação

Runbook **manual**, escrito para quem **nunca usou VPS nem SSH**. Nada aqui roda sozinho: cada
passo é feito por você, na ordem, e só se passa ao seguinte depois da verificação do passo
("**Confira**"). Se uma verificação não der o que o texto diz, pare ali: seguir com um passo
errado quase sempre complica o conserto.

O porquê das escolhas está na spec do Plano 11 (apagada em 2026-09-25; ver README, Histórico);
as decisões que você ainda podia revisar estavam na §9 dela.

## Como ler este runbook

- **Dois computadores.** Todo bloco de comando diz onde roda:
  - **no seu computador** (o ponto de operação: um Linux, num terminal `Ctrl+Alt+T` no Ubuntu, ou
    o Mac, pelo [mac.md](mac.md));
  - **na VPS** (o servidor da Hostinger, num terminal conectado a ela por SSH, que você abre no
    bloco 2).
- **Um comando por vez.** Copie a linha inteira, cole no terminal (`Ctrl+Shift+V`), aperte Enter e
  leia o que apareceu antes de colar a próxima. Linhas que começam com `#` são comentários: o
  terminal as ignora.
- **`sudo` pede a sua senha** (a do seu usuário na VPS, criada no passo 2.5). Ao digitá-la, nada
  aparece na tela: é normal.
- **Segredos só vão para o gerenciador de senhas e para dois arquivos do root na VPS:** o
  `/opt/stillon/.env` (os do app e do banco) e o `/opt/stillon/vigia.env` (só a URL do vigia,
  bloco 8). Nunca num chat, num e-mail, num print, num arquivo do repositório ou num campo do
  GitHub. Os três segredos que o GitHub precisa (bloco 4) vão por um comando que lê de arquivo,
  sem colar nada.
- **O `compose.yaml` da raiz do repositório não vai para o servidor.** Ele cria um usuário de
  demonstração com senha pública e usa chaves públicas: num servidor, qualquer um entraria como
  dono. O servidor usa só os arquivos de `deploy/vps/`.

Desde 2026-10-13 a operação parte de um **Mac**: siga o [mac.md](mac.md) para o que muda no
"seu computador" (o Terminal do Mac abre o `zsh`; rode `bash` antes de colar os comandos).
Preserve as chaves e as entradas do gerenciador de senhas existentes.

**Caminho do clone.** Os blocos 7.2 e 8 escrevem o clone como `~/Projetos/Go/StillOn`. Se o seu
está em outro lugar (no Mac do dono, `~/Documents/Projetos/Go/StillOn`), troque o caminho nesses
comandos. E se o clone estiver em outro branch ou com arquivos seus não rastreados, não rode o
`git switch main && git pull --ff-only` do bloco 8: leia os arquivos da `main` com `git fetch` e
`git archive origin/main`.

Se a VPS tiver sido reinstalada, os registros de passos concluídos antes disso são históricos:
confira novamente cada passo aplicável com o respectivo **Confira** antes de continuar. O estado
anterior, inclusive o registrado no PR #26, não comprova o estado do servidor reinstalado. Esta
conferência deve ocorrer antes de restaurar segredos ou iniciar o serviço.

## Topologia

Uma VPS (Hostinger KVM 2: 2 vCPU, 8 GB, 100 GB NVMe, datacenter no Brasil) com Ubuntu 24.04 LTS e
Docker, **dividida com o GoblinShift**, outro projeto seu
([spec](../superpowers/specs/2026-10-13-vps-compartilhada-design.md)):

```
internet ──80/443──► borda: Caddy (/srv/edge, 172.31.0.2 na rede edge)
                     ├─ stillon.tech ──► caddy do StillOn :80 (172.31.0.3 na edge; rede stillon)
                     │                     └─► app :8080 ─► db  (rede stillon, 10.30.0.0/24)
                     └─ :80 por IP ────► goblinshift-web
```

A **borda** é do host, não de um projeto: ela ouve a 80 e a 443, faz o HTTPS (Let's Encrypt) e
encaminha cada domínio para o seu projeto pela rede Docker `edge`. Os arquivos dela vêm do
repositório do GoblinShift; o StillOn só entrega o trecho dele, `deploy/vps/edge/stillon.caddy`.

Em `/opt/stillon`, um `compose.prod.yaml` com três serviços numa rede interna `stillon`
(10.30.0.0/24, sem IPv6):

| Serviço | O que é | Endereço | Porta publicada |
|---|---|---|---|
| `caddy` | Proxy só HTTP, só para o seu domínio; o único do StillOn na rede `edge` | 10.30.0.2 | nenhuma |
| `app` | O binário do StillOn (`--role=all`): API, painel, sondador, avisos, manutenção | 10.30.0.3 | nenhuma |
| `db` | Postgres 18, a imagem oficial, fixada por digest | 10.30.0.4 | nenhuma |

Por fora: o GitHub Actions entrega cada versão nova por SSH, com uma chave que só sabe fazer
`deploy` e `backup` (bloco 4); o backup diário sai cifrado para o GitHub (bloco 7); e o
healthchecks.io avisa por e-mail se o site parar de responder (bloco 8).

## Mapa

| Bloco | O quê | Onde |
|---|---|---|
| 0 | Gerenciador de senhas, o arquivo de variáveis, as ferramentas do seu computador | seu computador |
| 1 | Chaves SSH, domínio, Resend, healthchecks.io | seu computador, painéis |
| 2 | Primeiro acesso e endurecimento da VPS | VPS |
| 3 | Docker, `/opt/stillon`, segredos, chave do backup | VPS e seu computador |
| 4 | Usuário `deploy`, secrets do GitHub, primeiro deploy | VPS, seu computador, GitHub |
| 5 | Seu usuário no StillOn e o 2FA | VPS, navegador |
| 6 | E-mail, canal e alvo de teste | navegador |
| 7 | Backup ligado e ensaio de restauração | GitHub, seu computador |
| 8 | Vigia externo | VPS, healthchecks.io |
| 9 | Verificações finais | seu computador, VPS |
| 10 | Operação: deploys, logs, 2FA perdido, Postgres, "a VPS morreu", repositório público | — |

Tempo estimado da primeira vez: uma tarde, sem contar a espera do DNS e do Resend (de minutos a
algumas horas).

---

## Bloco 0: antes de tudo

### 0.1 Gerenciador de senhas

**Faça:** escolha um gerenciador de senhas (Bitwarden ou KeePassXC servem) e crie nele uma pasta
"StillOn". Tudo o que este runbook mandar "guardar no gerenciador" vai para lá, cada coisa numa
entrada com o nome indicado.

**Por quê:** a VPS pode morrer, e com ela o `.env`; o que está no gerenciador é o que permite
reconstruir (bloco 10). Perder a `MASTER_KEY`, em especial, torna ilegíveis os headers de alvo e as
configurações de canal para sempre: nenhum backup devolve a chave.

**Confira:** a pasta existe e você sabe abri-la no computador e no celular.

### 0.2 Ferramentas do seu computador

**Faça** (no seu computador):

```bash
sudo apt update
sudo apt install -y openssh-client age nmap git curl
gh auth status
```

**Por quê:** `ssh`/`scp` falam com a VPS; `age` decifra o backup; `nmap` confere de fora quais
portas estão abertas; `gh` grava os secrets do GitHub sem você colar nada. O Docker do seu
computador (o mesmo do `docker compose up` do README) sobe a cópia do banco no ensaio de
restauração (passo 7.2) e na reconstrução (bloco 10).

**Deve aparecer:** a instalação termina sem `E:`; o `gh auth status` diz `Logged in to github.com`
com a sua conta. Se o `gh` não estiver instalado ou logado: `sudo apt install gh` e
`gh auth login` (escolha GitHub.com, HTTPS, e entre pelo navegador).

**Confira:**

```bash
age --version
nmap --version | head -1
git -C ~/Projetos/Go/StillOn remote -v
docker run --rm hello-world
```

Quatro respostas: as versões, a terceira com `BipoXDXD/stillon` e a última com
`Hello from Docker!`, **sem** `sudo`. (Se o seu clone estiver em outro lugar, use o caminho dele
em todo este runbook.) Se o `docker` não existir, instale o Docker Engine pelo repositório da
Docker ([fonte](https://docs.docker.com/engine/install/ubuntu/), os mesmos comandos do passo 3.1,
no seu computador); se ele disser `permission denied` no `docker.sock`, é o seu usuário fora do
grupo `docker`: `sudo usermod -aG docker "$USER"`, saia da sessão do computador e entre de novo.

### 0.3 O arquivo de variáveis do seu computador

Os comandos deste runbook usam três nomes, que você define uma vez num arquivo:

- `VPS`: o endereço IPv4 da VPS (você o acha no passo 1.3);
- `DOMINIO`: o domínio grátis da Hostinger (passo 1.4), sem `https://` e sem `www`;
- `EU`: o nome do seu usuário administrador na VPS (passo 2.5), em minúsculas, sem espaço (ex.:
  `lucas`).

**Faça** (no seu computador), trocando os três valores de exemplo quando os tiver (pode criar o
arquivo agora e corrigir depois, com `nano ~/.stillon-vps`):

```bash
cat > ~/.stillon-vps <<'FIM'
VPS=203.0.113.10
DOMINIO=exemplo.com
EU=lucas
FIM
. ~/.stillon-vps
```

**Por quê:** nenhum dos três é segredo, e tê-los num arquivo evita digitar errado o IP no meio de
um comando.

**Toda vez que abrir um terminal novo no seu computador**, rode `. ~/.stillon-vps` antes dos
comandos deste runbook.

**Confira:** `echo "$VPS $DOMINIO $EU"` mostra os três valores.

---

## Bloco 1: chaves, domínio e contas

### 1.1 Chave SSH do administrador

**Faça** (no seu computador):

```bash
ssh-keygen -t ed25519 -C "stillon-admin" -f ~/.ssh/stillon_admin
```

Ele pede uma *passphrase* duas vezes. **Antes**, gere a frase no gerenciador (entrada "StillOn –
passphrase da chave SSH"; no Bitwarden, o gerador no modo *Passphrase*, com palavras sem acento) e
salve a entrada. Depois **cole** a frase nas duas perguntas, sem digitar: uma frase digitada pode
sair diferente da guardada (acentos, outro teclado), e aí nenhum computador abre a chave. Nada
aparece enquanto você cola, e a segunda pergunta (`Enter same passphrase again:`) espera a
colagem de novo.

**Por quê:** a VPS vai aceitar entrada **só** por chave, nunca por senha (passo 2.7). A chave
privada (`~/.ssh/stillon_admin`) nunca sai do seu computador; a pública (`.pub`) vai para a VPS.
A passphrase protege a chave se alguém copiar o arquivo.

**Deve aparecer:** `Your identification has been saved in /home/…/.ssh/stillon_admin` e um
desenho de caracteres (o *randomart*).

**Confira:** `ls -l ~/.ssh/stillon_admin*` lista dois arquivos, o sem `.pub` com permissão
`-rw-------`. E que a frase do gerenciador abre a chave: `ssh-keygen -y -f ~/.ssh/stillon_admin`,
colando a frase, imprime a mesma linha `ssh-ed25519 AAAA…` do `.pub`. Com
`incorrect passphrase supplied to decrypt private key`, a frase guardada não é a da chave: apague
os dois arquivos e gere de novo, agora colando (em 2026-09-28 foi isso que obrigou a refazer a
chave, já no Mac).

**Cópia de segurança:** guarde também o **conteúdo** da chave privada no gerenciador (entrada
"StillOn – chave SSH do administrador", como anexo ou nota segura): `cat ~/.ssh/stillon_admin`
mostra o texto. Sem ela e sem o computador, a volta é pelo terminal do painel (bloco 10, "Perdi a
chave SSH").

### 1.2 Conta da Hostinger e a VPS

**Faça** (no navegador): entre em <https://hpanel.hostinger.com>. No menu **VPS**, clique em
**Manage** ao lado do seu servidor. Na página **OS & Panel → Operating System**, confira o sistema.

**Deve aparecer:** **Ubuntu 24.04** puro (sem painel como CyberPanel, sem "Docker" pré-instalado).
Se for outro, reinstale ali mesmo escolhendo **Ubuntu 24.04** simples. Reinstalar apaga a VPS, o
que numa VPS nova não custa nada.

**Por quê:** o runbook foi escrito e conferido para o Ubuntu 24.04 limpo; um painel ou um Docker
de outra origem mudaria portas, firewall e atualizações.

**Faça também:** em **Settings → Main settings**, defina uma senha de root longa (gere no
gerenciador; entrada "StillOn – senha de root da VPS"). Ela só serve para o terminal do painel
em emergência: o SSH de root vai ser desligado no passo 2.7.

### 1.3 O IP da VPS e a chave pública no painel

**Faça:** na página **Overview** da VPS, copie o endereço **IPv4** e ponha-o no `~/.stillon-vps`
(`VPS=…`, passo 0.3). Depois, no seu computador:

```bash
cat ~/.ssh/stillon_admin.pub
```

Copie a linha inteira (começa com `ssh-ed25519`, termina com `stillon-admin`). No painel, em
**Settings → SSH keys**, clique em adicionar, dê o nome `stillon-admin` e cole a linha. (A chave
**pública** não é segredo: é a única exceção à regra de "não colar", e ela é pública por desenho.)

**Por quê:** a Hostinger instala essa chave para o root, e é com ela que você entra pela primeira
vez (passo 2.1).

**Confira:** a chave aparece listada em **SSH keys**.

### 1.4 O domínio apontado para a VPS

Fonte: Hostinger, [How to get a free domain](https://support.hostinger.com/en/articles/1583407-how-to-register-a-domain-for-free)
e [How to point a domain to your VPS](https://www.hostinger.com/support/1583227-how-to-point-a-domain-to-your-vps-at-hostinger/).

**Faça:**

1. Se ainda não pegou o domínio grátis do plano: na página inicial do painel, **Claim Domain** →
   registre um nome novo (planos VPS não têm transferência grátis, só registro). Ponha o nome no
   `~/.stillon-vps` (`DOMINIO=…`).
2. Vá em **Domain portfolio** → **Manage** ao lado do domínio → **DNS / Nameservers**.
3. **Apague** qualquer registro `A`, `AAAA` ou `CNAME` cujo nome seja `@` ou `www` (os que a
   Hostinger cria apontando para a hospedagem dela).
4. Adicione um registro **A**: **Name** `@`, **Points to** o IPv4 da VPS, **TTL** o padrão.

**Por quê:** o registro `A` diz ao mundo que `seu-domínio` é a sua VPS; o Caddy só consegue o
certificado HTTPS quando isso já vale. Sem `AAAA`: o StillOn é configurado e conferido só em
IPv4. Sem `www`: o Caddy atende um nome só (o `www` não teria certificado).

**Deve aparecer:** na lista do DNS, uma linha `A  @  <o IP da VPS>`, e nenhum `AAAA`/`CNAME` para
`@` ou `www`.

**Confira** (no seu computador; a propagação pode levar de minutos até 24 h):

```bash
getent ahostsv4 "$DOMINIO" | head -1
```

Deve mostrar o IP da VPS. Se mostrar outro IP ou nada, espere e repita; siga para os passos 1.5 e
1.6 enquanto isso, mas **não** faça o primeiro deploy (4.8) antes disto dar certo.

### 1.5 Resend: domínio verificado e chave de API

Fonte: Resend, [Add and verify a domain](https://resend.com/docs/add-a-domain) e
[What if my domain is not verifying?](https://resend.com/docs/knowledge-base/what-if-my-domain-is-not-verifying).

**Faça:**

1. Crie a conta em <https://resend.com>.
2. **Domains → Add Domain.** Use um **subdomínio** do seu domínio, como `avisos.seu-domínio`
   (recomendação do Resend: separa a reputação de envio do domínio principal). Região: a mais
   próxima do Brasil que ele oferecer.
3. O Resend mostra os registros DNS a criar (SPF e DKIM: `TXT` e `MX`, ou `CNAME` em domínios
   novos). Para cada um, no painel da Hostinger (**DNS / Nameservers** do domínio), adicione um
   registro do **mesmo tipo**, com o **Name** e o **valor** exatamente como o Resend mostra.
   Atenção ao **Name**: a Hostinger completa o domínio sozinha. Se o Resend mostra
   `resend._domainkey.avisos.seu-domínio`, digite só `resend._domainkey.avisos`; se mostra
   `send.avisos.seu-domínio`, digite `send.avisos`. O DKIM diferencia maiúsculas de minúsculas:
   copie com o botão de copiar do Resend.
4. De volta ao Resend, clique no botão de verificar o domínio.
5. **API Keys → Create API Key**: nome `stillon-vps`, permissão **Sending access**, restrita ao
   domínio `avisos.seu-domínio`. A chave aparece **uma vez**: guarde-a direto no gerenciador
   (entrada "StillOn – RESEND_API_KEY"). Ela só volta a ser usada no passo 3.3.

**Por quê:** sem domínio verificado, todo envio do StillOn volta com erro 403 do Resend; o SPF e
o DKIM são o que faz o e-mail de alerta não cair no spam. A chave só de envio e só daquele domínio
limita o estrago se vazar.

**Deve aparecer:** o domínio com status **Verified** (muitas vezes em 15 minutos; o Resend diz que
pode levar até 72 h).

**Confira:** status **Verified** na lista de domínios. Se ficar **Pending** por horas, compare
registro a registro com o painel da Hostinger (o erro mais comum é o domínio repetido no **Name**).

Anote o remetente que vai usar, no formato `StillOn <alertas@avisos.seu-domínio>`: é o
`EMAIL_FROM` do passo 3.3 (não é segredo).

### 1.6 healthchecks.io

**Faça:**

1. Crie a conta em <https://healthchecks.io> (o plano grátis basta) com o e-mail em que quer
   receber o alarme.
2. No projeto, **Add Check**: nome `StillOn`; **Period** `1 minute`; **Grace Time**
   `5 minutes`.
3. Copie a **ping URL** do check (o formato é `https://hc-ping.com/<uuid>`) para o gerenciador
   (entrada "StillOn – HC_URL"). Ela só volta a ser usada no bloco 8.

**Por quê:** um timer na VPS vai pingar essa URL a cada minuto quando o site responder, e pingar
`/fail` quando não responder (bloco 8). Se os pings pararem (VPS morta) ou falharem, o
healthchecks manda e-mail: o StillOn não consegue avisar da própria queda. A carência de 5 min
só vale para ping que **não chega** (a VPS desligada por uns minutos no reinício automático das
04:30, passo 2.11). Um `/fail` põe o check em **down** na hora e manda o e-mail, sem carência: se
o vigia rodar enquanto o app ainda sobe (logo depois de um reinício, ou nos segundos de troca de
um deploy), chega um par de e-mails, **down** e, no minuto seguinte, **up**. Reconheça o par pela
hora (perto das 04:30, ou de um deploy seu) e pelo **up** logo depois: não é queda. Um **down**
sem **up** em poucos minutos é queda de verdade. A URL é quase-segredo: quem a tem pode pingar
por você e esconder uma queda.

**Confira:** o check aparece com status **new** (cinza) e o canal de e-mail da conta está ativo
(**Integrations**).

---

## Bloco 2: primeiro acesso e endurecimento

### 2.1 Primeiro SSH, conferindo a impressão digital

**Faça, primeiro no painel:** na página **Overview** da VPS, clique em **Web console** (ou
**Browser terminal**; fonte: Hostinger, [How to use the Browser terminal](https://www.hostinger.com/support/7978544-how-to-use-the-browser-terminal-in-hostinger/)).
Abre uma aba com um terminal já logado como root. Nele:

```bash
ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub
```

**Deve aparecer:** uma linha como `256 SHA256:AbC…xyz root@… (ED25519)`. Deixe essa aba aberta.

**Faça, depois, no seu computador**, com a passphrase **já copiada** do gerenciador (o servidor
dá 2 minutos para o login terminar; passado disso, fecha com `Connection closed`, e basta repetir):

```bash
ssh -i ~/.ssh/stillon_admin root@"$VPS"
```

Ele pergunta `Are you sure you want to continue connecting (yes/no/[fingerprint])?` e mostra um
`ED25519 key fingerprint is SHA256:…`.

**Por quê:** essa pergunta só aparece na primeira conexão. Se a impressão digital for a do terminal
do painel, você está falando com a sua VPS, e não com alguém no meio do caminho.

**Confira:** o `SHA256:…` da pergunta é **idêntico** ao do terminal do painel. Só então digite
`yes`, e a passphrase da chave. O prompt vira `root@…:~#`: você está **na VPS**.

Se forem diferentes, digite `no` e pare: releia o IP no `~/.stillon-vps`.

**Confira também** (na VPS, como root) quais chaves o root aceita, porque o passo 2.5 as copia para
o seu usuário:

```bash
cat /root/.ssh/authorized_keys
```

Uma linha só, terminando em `stillon-admin`; se você trocou a chave no painel, a antiga não pode
estar ali. Se o prompt aparecer **colado** no fim da linha, o arquivo não termina com quebra de
linha (a Hostinger o grava assim), e uma chave acrescentada depois com `>>` grudaria nela:
`printf '\n' >> /root/.ssh/authorized_keys`.

### 2.2 Atualizar o sistema

**Faça** (na VPS, como root):

```bash
apt update && apt full-upgrade -y
```

**Por quê:** a imagem do Ubuntu da Hostinger é de semanas ou meses atrás; as correções de
segurança do intervalo entram agora.

**Deve aparecer:** a lista de pacotes atualizados e o prompt de volta, sem linhas começando com
`E:`. Se aparecer uma tela azul perguntando sobre um arquivo de configuração, aceite o padrão
(Enter).

**Confira:**

```bash
ls /var/run/reboot-required 2>/dev/null && echo "precisa reiniciar" || echo "sem reinício"
```

Se disser `precisa reiniciar`: `reboot`, espere um minuto e entre de novo com o mesmo `ssh` do
passo 2.1 (sem a pergunta, desta vez).

### 2.3 Fuso horário e relógio

**Faça** (na VPS, como root):

```bash
apt install -y systemd-timesyncd
timedatectl set-timezone America/Sao_Paulo
timedatectl set-ntp true
timedatectl status
```

**Por quê:** o reinício automático (passo 2.11) é às 04:30 **no horário da VPS**; o fuso de
Brasília faz esse horário ser o seu. O relógio certo (NTP, pelo `systemd-timesyncd`) é exigência
do 2FA (os códigos mudam a cada 30 s) e dos rollups de uptime. No Ubuntu 24.04 o
`systemd-timesyncd` é um pacote à parte: sem ele, o `set-ntp true` falha com
`NTP not supported`. O `apt install` não faz nada se ele já estiver lá.

**Deve aparecer:** `Time zone: America/Sao_Paulo (-03, -0300)`, `System clock synchronized: yes`,
`NTP service: active`.

**Confira:** `systemctl is-active systemd-timesyncd` diz `active`. (Se `synchronized` ainda for
`no`, espere um minuto e rode `timedatectl status` de novo.)

### 2.4 Uma foto da VPS antes de mexer no SSH (opcional, recomendado)

**Faça:** no painel, **Backups & Monitoring → Snapshots & Backups → Create Snapshot**.

**Por quê:** os próximos passos mexem no acesso; se algo der muito errado, restaurar o snapshot
volta a VPS a este ponto. O snapshot da Hostinger expira em 24 h e só existe um por vez
([fonte](https://www.hostinger.com/support/1583232-how-to-back-up-or-restore-a-vps)). Restaurar
**apaga tudo** o que veio depois.

**Confira:** o snapshot aparece listado com a data de hoje.

### 2.5 Seu usuário administrador

**Faça** (na VPS, como root), trocando `lucas` pelo valor de `EU` do seu `~/.stillon-vps`:

```bash
adduser lucas
```

Ele pede uma senha duas vezes (gere uma longa no gerenciador, entrada "StillOn – senha sudo do
administrador") e depois nome, telefone etc.: deixe em branco com Enter e confirme com `Y`.

```bash
usermod -aG sudo lucas
install -d -m 700 -o lucas -g lucas /home/lucas/.ssh
install -m 600 -o lucas -g lucas /root/.ssh/authorized_keys /home/lucas/.ssh/authorized_keys
```

**Por quê:** ninguém entra mais como root (passo 2.7). Você entra como `lucas`, só com a chave, e
vira root com `sudo` quando precisa, digitando a senha: quem roubar só a chave não vira root.

**Confira**, num **terminal novo** do seu computador (deixe o do root aberto, é a sua rede de
segurança):

```bash
. ~/.stillon-vps
ssh -i ~/.ssh/stillon_admin "$EU@$VPS"
```

e, já na VPS, como você:

```bash
sudo whoami
```

Deve pedir a senha do `sudo` e responder `root`. Só siga se deu certo.

### 2.6 A chave no `~/.ssh/config` do seu computador

**Faça** (no seu computador):

```bash
. ~/.stillon-vps
cat >> ~/.ssh/config <<FIM

Host stillon
    HostName $VPS
    User $EU
    IdentityFile ~/.ssh/stillon_admin
    IdentitiesOnly yes
    ServerAliveInterval 30
FIM
chmod 600 ~/.ssh/config
```

**Se já existe um `Host stillon`** no seu `~/.ssh/config` (você está refazendo este passo numa
reconstrução, "A VPS morreu" no bloco 10), **não rode o bloco acima**: um segundo `Host stillon`
fica depois do primeiro, e o `ssh` usa o primeiro valor que lê, ou seja, o IP velho. Em vez disso,
abra o arquivo com `nano ~/.ssh/config`, troque só o valor da linha `HostName` do `Host stillon`
pelo IP novo, salve (`Ctrl+O`, Enter, `Ctrl+X`) e rode o **Confira** abaixo. Confira quantos há com
`grep -c '^Host stillon$' ~/.ssh/config`: tem de dar `1`.

**Por quê:** a partir daqui, `ssh stillon` e `scp arquivo stillon:` bastam; o `IdentitiesOnly`
evita que o SSH ofereça outras chaves suas e conte tentativas no `fail2ban` (passo 2.8). O
`ServerAliveInterval` manda um sinal a cada 30 s: sem ele, uma sessão parada enquanto você lê o
runbook pode ser derrubada pelo roteador (`Operation timed out`), e o terminal volta para o seu
computador sem aviso. **Olhe o prompt antes de colar cada comando**: um comando "da VPS" rodado no
seu computador pode achar a mesma pasta (o macOS também tem `/etc/ssh/sshd_config.d`).

**Confira:** `ssh stillon true && echo ok` imprime `ok` (depois da passphrase, se o seu
chaveiro não a lembrar).

### 2.7 SSH sem senha e sem root

**Faça** (na VPS, como você):

```bash
sudo tee /etc/ssh/sshd_config.d/00-stillon.conf >/dev/null <<'FIM'
# Endurecimento do StillOn (spec do Plano 11, §5). O nome começa com 00 porque
# o sshd usa o PRIMEIRO valor que lê, e os arquivos deste diretório são lidos em
# ordem alfabética: um 50-cloud-init.conf com PasswordAuthentication yes não
# vence este.
PermitRootLogin no
PasswordAuthentication no
KbdInteractiveAuthentication no
PubkeyAuthentication yes
FIM
sudo sshd -t && sudo systemctl restart ssh
sudo sshd -T | grep -Ei '^(permitrootlogin|passwordauthentication|kbdinteractiveauthentication) '
```

**Por quê:** robôs tentam senhas em toda VPS do mundo o dia inteiro; sem senha aceita, não há o
que adivinhar. A porta continua 22: mudar a porta só diminui o barulho do log, não é segurança.

**Deve aparecer:** o `sshd -t` não imprime nada (configuração válida); as três linhas finais são
`permitrootlogin no`, `passwordauthentication no` e `kbdinteractiveauthentication no`.

**Se alguma das três disser outra coisa** (`yes`, `prohibit-password`…), não siga: o root ou a
senha continuariam abertos. Veja de onde vem:

```bash
sudo grep -nE '^[[:space:]]*(Include|Match|PermitRootLogin|PasswordAuthentication|KbdInteractiveAuthentication)' /etc/ssh/sshd_config
```

- Uma linha `PermitRootLogin`, `PasswordAuthentication` ou `KbdInteractiveAuthentication`
  **antes** da linha `Include /etc/ssh/sshd_config.d/*.conf` vence o `00-stillon.conf` (o
  primeiro valor lido vale). Ponha um `#` no começo dela (`sudo nano /etc/ssh/sshd_config`),
  rode `sudo sshd -t && sudo systemctl restart ssh` e o `sshd -T … | grep` de novo, até as três
  darem `no`.
- Uma linha `Match`: ela muda as regras para certas conexões, e o `sshd -T` sozinho não a
  mostra. Uma imagem limpa do Ubuntu não tem nenhuma: se houver, pare aqui e não siga sem
  entender o que ela libera.

**Confira**, **sem fechar** o terminal em que você está (num terminal novo do seu computador):

```bash
. ~/.stillon-vps
ssh stillon true && echo "entrada por chave: ok"
ssh -o PubkeyAuthentication=no -o PreferredAuthentications=password "$EU@$VPS" true
ssh -i ~/.ssh/stillon_admin root@"$VPS" true
```

A primeira imprime `entrada por chave: ok`; a segunda e a terceira terminam em
`Permission denied (publickey).` Se a primeira falhar, **não feche** o terminal aberto: desfaça
com `sudo rm /etc/ssh/sshd_config.d/00-stillon.conf && sudo systemctl restart ssh` e releia os
passos 2.5 e 2.6.

**Confira também o terminal do painel:** feche a aba do **Web console** e abra de novo. Anote o
que acontece: se ele entrar sozinho, ótimo; se pedir usuário e senha, entre como `root` com a
senha de root do passo 1.2 e anote se funcionou (é a volta se um dia o `sudo` quebrar, passo
4.1); se o root não entrar, confira ao menos que o **seu** usuário e a senha do `sudo` entram. Ele é a sua porta de emergência (bloco 10, "Perdi a chave SSH"): garanta
agora que ela abre. Se não abrir de jeito nenhum, a emergência passa a ser o **Emergency mode** do
painel (**Settings**, [fonte](https://support.hostinger.com/en/articles/5726577-how-to-use-emergency-mode-on-your-vps)).

### 2.8 fail2ban

**Faça** (na VPS):

```bash
sudo apt install -y fail2ban python3-systemd
sudo tee /etc/fail2ban/jail.d/stillon.local >/dev/null <<'FIM'
[sshd]
enabled  = true
backend  = systemd
maxretry = 5
findtime = 10m
bantime  = 1h
FIM
sudo systemctl enable fail2ban
sudo systemctl restart fail2ban
```

**Por quê:** mesmo sem senha, cada tentativa gasta log e CPU; o `fail2ban` bloqueia por uma hora o
IP que errar 5 vezes em 10 minutos. `backend = systemd` lê o diário do sistema, que sempre existe.

**Deve aparecer:** nada de erro.

**Confira:**

```bash
sudo fail2ban-client status sshd
```

Mostra `Status for the jail: sshd` com `Currently failed` e `Currently banned` (provavelmente 0).
Se você mesmo for bloqueado um dia: pelo terminal do painel,
`sudo fail2ban-client set sshd unbanip <seu IP>`.

### 2.9 Firewall da VPS (ufw)

**Faça** (na VPS):

```bash
sudo ufw default deny incoming
sudo ufw default allow outgoing
sudo ufw allow 22/tcp
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw enable
```

O `enable` avisa que pode derrubar conexões SSH: responda `y`. (A 22 foi liberada antes, então a
sua não cai.)

**Por quê:** fecha tudo o que chega, menos SSH (22), HTTP (80, que a borda usa para o certificado e
para redirecionar) e HTTPS (443). **Atenção:** o Docker passa por cima do `ufw` nas portas que um
contêiner publica. Quem garante que só 80 e 443 ficam abertas são os composes (só a borda publica
porta; o StillOn, nenhuma) e o firewall do painel (passo seguinte); o bloco 9 confere de fora.

**Confira:**

```bash
sudo ufw status verbose
```

`Status: active`, `Default: deny (incoming), allow (outgoing)`, e as três portas com `ALLOW IN`
(em IPv4 e `(v6)`). E, num terminal novo do seu computador, **sem fechar** o terminal em que você
está, `ssh stillon true && echo ok` continua dando `ok`. Se não der, desfaça pelo terminal que
ficou aberto (`sudo ufw disable`) e releia o passo.

### 2.10 Firewall do painel da Hostinger

Fonte: Hostinger, [How to use a managed VPS firewall](https://www.hostinger.com/support/8172641-how-to-use-a-managed-vps-firewall-at-hostinger/).

**Faça** (no painel da VPS): **Security → Firewall** → **Add Firewall**, nome `stillon`,
**Create**. No `⋯` ao lado dele, **Edit**, e em **Add firewall rule** crie três regras, cada uma
com **Action** `accept`, **Protocol** `TCP`, **Traffic Source** `anywhere`, e **Port** `22`, `80`
e `443` (uma por regra; **Add rule** a cada uma). Confira que o firewall está **ativo** para esta
VPS.

**Por quê:** é a segunda camada, fora da VPS: ela vale mesmo se o Docker, um erro de configuração
ou um pacote novo abrir uma porta lá dentro. A Hostinger descarta todo tráfego que nenhuma regra
aceita, e aplica a mudança em até 2 minutos. Com isso o `ping` para de responder: é esperado.

**Confira**, depois de 2 minutos, **sem fechar** o terminal da VPS em que você está (ele é a
rede de segurança: se a conexão nova falhar, a aberta costuma seguir de pé):

```bash
# no seu computador, num terminal novo
ssh stillon true && echo "ssh ok"
```

```bash
# na VPS: a saída continua livre (o sondador precisa dela)
curl -sS -o /dev/null -w '%{http_code}\n' https://example.com/
sudo apt update
```

`ssh ok`; o `curl` responde `200`; o `apt update` termina sem `E:`. Se a saída falhar, o firewall
do painel está bloqueando as respostas: revise as regras (só entrada, `accept`) antes de seguir.
Se o `ssh ok` não vier, desative o firewall no painel (o `⋯` ao lado dele), espere 2 minutos,
confira a regra da porta `22` e ative de novo.

### 2.11 Atualizações automáticas, com reinício às 04:30

**Faça** (na VPS):

```bash
sudo apt install -y unattended-upgrades
sudo tee /etc/apt/apt.conf.d/20auto-upgrades >/dev/null <<'FIM'
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
FIM
sudo tee /etc/apt/apt.conf.d/52stillon-reinicio >/dev/null <<'FIM'
// Spec do Plano 11, §5 e §9 (decisão 1): reinicia sozinho, de madrugada, quando
// uma atualização de segurança (o kernel, quase sempre) pedir. A hora é a do
// fuso da VPS (America/Sao_Paulo, passo 2.3).
Unattended-Upgrade::Automatic-Reboot "true";
Unattended-Upgrade::Automatic-Reboot-WithUsers "true";
Unattended-Upgrade::Automatic-Reboot-Time "04:30";
FIM
```

**Por quê:** correções de segurança entram todo dia sem você lembrar; uma que exige reinício é
aplicada às 04:30 (horário de Brasília), com ~1 minuto fora do ar. Os contêineres voltam sozinhos
(`restart: unless-stopped` no compose). `WithUsers` reinicia mesmo com alguém logado: uma sessão
SSH esquecida aberta não pode segurar uma correção de kernel para sempre.

**Confira:**

```bash
apt-config dump | grep -E 'Periodic::(Update-Package-Lists|Unattended-Upgrade)|Automatic-Reboot'
sudo unattended-upgrade --dry-run 2>&1 | tail -3
```

As cinco linhas com os valores acima (`"1"`, `"1"`, `"true"`, `"true"`, `"04:30"`); o *dry-run*
termina sem `Traceback` nem `error`.

---

## Bloco 3: Docker, `/opt/stillon`, segredos e a chave do backup

### 3.1 O Docker que já está na VPS

O Docker foi instalado pelo `provision.sh` do GoblinShift, do repositório oficial da Docker. Aqui
você só confere que ele serve ao StillOn.

**Faça** (na VPS, prompt `<seu usuário>@<host da VPS>:~$`), um comando por vez:

```bash
docker --version
```

```bash
sudo docker info -f '{{.DriverStatus}}'
```

```bash
ip -4 route
```

**Deve aparecer:** `Docker version 29` ou maior; no `DriverStatus`, algo com
`io.containerd.snapshotter.v1`; e nenhuma linha do `ip -4 route` começando com `10.30.`.

**Por quê:** o deploy (bloco 4) confere a imagem pelo ID, o que só funciona com o *image store* do
containerd, o padrão do Docker 29 instalado do zero
([fonte](https://docs.docker.com/engine/storage/containerd/)); o `stillon-entrada` recusa o deploy
sem ele. E a rede interna do StillOn usa `10.30.0.0/24`, que não pode coincidir com nenhuma rota da
máquina.

**Confira:** sem o `snapshotter`, pare e chame o Claude: trocar de store esconde as imagens que o
GoblinShift já baixou. Com uma rota em `10.30.`, pare também: a faixa precisa mudar no
`compose.prod.yaml` antes de seguir.

### 3.2 `live-restore` sem derrubar ninguém

O `/etc/docker/daemon.json` é do host e já tem a configuração do GoblinShift (rotação de log, as
faixas de rede e o runtime do gVisor). Você só acrescenta uma chave, sem apagar nada.

**Faça** (na VPS), um comando por vez:

```bash
sudo apt install -y jq
```

```bash
sudo cp /etc/docker/daemon.json /etc/docker/daemon.json.antes-do-stillon
```

```bash
sudo jq '. + {"live-restore": true}' /etc/docker/daemon.json.antes-do-stillon | sudo tee /etc/docker/daemon.json
```

```bash
sudo systemctl reload docker
```

```bash
sudo docker info -f '{{.LiveRestoreEnabled}}'
```

**Deve aparecer:** o `tee` mostra o JSON com as chaves que já existiam (`default-address-pools`,
`log-opts`, `runtimes`) **e** `"live-restore": true`; o último comando mostra `true`.

**Por quê:** o `live-restore` mantém os contêineres de pé quando o próprio Docker é atualizado ou
reiniciado. O `reload` aplica a opção sem reiniciar o Docker (ela é das que ele relê), então o
GoblinShift não cai.

**Confira:** `sudo docker ps --format '{{.Names}} {{.Status}}'` não mostra nenhum contêiner com
`Up` de poucos segundos. Se o `tee` mostrar um JSON sem as chaves antigas, volte a cópia
(`sudo cp /etc/docker/daemon.json.antes-do-stillon /etc/docker/daemon.json`) e chame o Claude.

### 3.3 Os arquivos do StillOn em `/opt/stillon`

**Faça, primeiro no seu computador**, com a `main` atualizada (os arquivos da VPS têm de ser os da
versão que vai para o ar):

```bash
cd ~/Projetos/Go/StillOn
git switch main && git pull --ff-only
ssh stillon 'mkdir -p ~/stillon-arquivos'
scp deploy/vps/compose.prod.yaml deploy/vps/Caddyfile deploy/vps/initdb.sh deploy/vps/env.exemplo \
    deploy/vps/stillon-entrada deploy/vps/sudoers-stillon-entrada \
    stillon:stillon-arquivos/
```

**Deve aparecer:** uma linha de progresso por arquivo, seis no total. (Os arquivos do vigia vêm
só no bloco 8, quando são instalados; o trecho da borda, `stillon.caddy`, no 4.9.)

**Depois, na VPS:**

```bash
cd ~/stillon-arquivos
sudo install -d -m 0755 -o root -g root /opt/stillon
sudo install -m 0644 -o root -g root compose.prod.yaml Caddyfile /opt/stillon/
sudo install -m 0755 -o root -g root initdb.sh /opt/stillon/
sudo install -m 0600 -o root -g root env.exemplo /opt/stillon/.env
ls -la /opt/stillon
```

**Por quê:** `/opt/stillon` é o lugar fixo que o script de deploy usa. Tudo é do root: o usuário
`deploy` (bloco 4) não escreve ali, só o script que ele pode chamar. O `.env` nasce do modelo, sem
valores, com permissão `600` (só o root lê).

**Deve aparecer:** `compose.prod.yaml`, `Caddyfile`, `initdb.sh` e `.env`, todos com dono `root`;
o `.env` com `-rw-------`.

**Faça também** os scripts do backup e os pacotes de que o `stillon-entrada` precisa. No seu
computador:

```bash
cd ~/Projetos/Go/StillOn
scp deploy/backup/backup.sh deploy/backup/verificar.sql deploy/compose/initdb.sql \
    stillon:stillon-arquivos/
```

Depois, na VPS:

```bash
sudo apt install -y age curl jq util-linux
cd ~/stillon-arquivos
sudo install -d -m 0755 -o root -g root /opt/stillon/deploy/backup /opt/stillon/deploy/compose
sudo install -m 0644 -o root -g root backup.sh verificar.sql /opt/stillon/deploy/backup/
sudo install -m 0644 -o root -g root initdb.sql /opt/stillon/deploy/compose/
ls -la /opt/stillon/deploy/backup /opt/stillon/deploy/compose
command -v age curl flock jq
```

**Por quê:** o verbo `backup` do `stillon-entrada` roda o `deploy/backup/backup.sh` do
repositório, sem mudança, dentro de um Postgres descartável, e ele acha o `initdb.sql` por
`../compose/`: por isso os três arquivos ficam em `/opt/stillon/deploy/` com o mesmo layout do
repositório. O `age` cifra o dump com a chave pública (passo 3.5), o `flock` (do `util-linux`)
impede que um deploy e um backup rodem ao mesmo tempo, o `jq` confere o índice da imagem que o
deploy recebe antes de carregá-la, e o `curl` é o que o vigia (bloco 8) usa.

**Deve aparecer:** `backup.sh` e `verificar.sql` em `/opt/stillon/deploy/backup`, `initdb.sql`
em `/opt/stillon/deploy/compose`, todos do `root`, e os quatro caminhos do `command -v` (por
exemplo `/usr/bin/age`, `/usr/bin/curl`, `/usr/bin/flock`, `/usr/bin/jq`).

### 3.4 Os segredos, gerados na VPS

**Faça** (na VPS):

Cole o bloco **inteiro de uma vez** (do `sudo sh -s` até o `FIM`):

```bash
sudo sh -s <<'FIM'
set -eu
umask 077
cd /opt/stillon
gerar() {
  VALOR=$(openssl rand "-$2" 32) NOME=$1 awk '
    $0 == ENVIRON["NOME"] "=" { print ENVIRON["NOME"] "=" ENVIRON["VALOR"]; next }
    { print }
  ' .env > .env.novo
  cat .env.novo > .env
  rm .env.novo
}
for v in POSTGRES_PASSWORD STILLON_APP_PASSWORD STILLON_BACKUP_PASSWORD; do gerar "$v" hex; done
for v in MASTER_KEY JWT_SECRET IP_HASH_KEY; do gerar "$v" base64; done
FIM
```

Ele não imprime nada. Cada valor nasce e é gravado dentro de um processo do root: não passa pela
linha de comando de nenhum programa (que outro usuário da VPS veria no `ps`), nem pelo seu
histórico. Só preenche variável **vazia**: rodar de novo por engano não troca um segredo que já
existe.

(`POSTGRES_PASSWORD` é a senha do superusuário do Postgres; `STILLON_APP_PASSWORD` e
`STILLON_BACKUP_PASSWORD`, as dos papéis do app e do backup.)

**Por quê:** segredo gerado na própria VPS nunca passou pela rede nem por outro computador. As
senhas do Postgres são hexadecimais porque entram em URLs `postgres://usuário:senha@…`, e o `/`,
o `+` e o `=` do base64 quebrariam a URL. As três chaves do app são 32 bytes aleatórios em
base64, o formato que o boot exige.

**Agora preencha o que não é gerado**, no editor:

```bash
sudo nano /opt/stillon/.env
```

- `STILLON_DOMINIO`: o seu domínio, sem `https://` (o valor de `DOMINIO`).
- `RESEND_API_KEY`: cole a chave do gerenciador (passo 1.5). Este `.env` é um dos lugares
  onde segredo pode ser colado.
- `EMAIL_FROM`: `StillOn <alertas@avisos.seu-domínio>` (passo 1.5).

Salve com `Ctrl+O` e Enter, saia com `Ctrl+X`.

O `.env` tem só estas nove variáveis (a `HC_URL` do healthchecks não está nele: ela vai para
o `vigia.env` do bloco 8, e o vigia nunca lê o `.env`). O resto o `compose.prod.yaml` monta sozinho: a
`PUBLIC_BASE_URL` e a `CORS_ORIGIN` (`https://` + `STILLON_DOMINIO`), a `DATABASE_URL` (com a
`STILLON_APP_PASSWORD` e o IP fixo do banco) e o `TRUSTED_PROXIES` (o IP fixo do Caddy). O
compose lê o `.env` pela interpolação (`${…}`), e cada serviço recebe só as variáveis de que
precisa: o Caddy não vê segredo nenhum, e o app não vê a senha do superusuário nem a do backup.

**Confira:**

```bash
sudo stat -c '%U %a' /opt/stillon/.env
sudo grep -cE '^[A-Z_]+=$' /opt/stillon/.env
sudo docker compose -f /opt/stillon/compose.prod.yaml config -q && echo "compose ok"
```

`root 600`; `0` (nenhuma variável vazia); `compose ok`.

**Guarde no gerenciador**, agora, cada valor (uma entrada por nome, "StillOn – MASTER_KEY" e
assim por diante):

```bash
sudo cat /opt/stillon/.env
clear
```

Copie cada valor do terminal para o gerenciador e rode o `clear` em seguida. É a única vez que
eles aparecem na tela. O mais importante é a `MASTER_KEY`: sem ela, os headers de alvo e as
configurações de canal ficam ilegíveis para sempre, e nenhum backup a devolve. O `JWT_SECRET` e o
`IP_HASH_KEY` podem ser trocados a qualquer hora (bloco 10) com custo pequeno; as senhas do
Postgres e a `MASTER_KEY`, não.

**Confira:** o gerenciador tem uma entrada por variável do `.env`, e o valor de uma delas, tirado
ao acaso, confere com o do terminal (ex.: `sudo grep '^JWT_SECRET=' /opt/stillon/.env` e compare
com os olhos, depois `clear`).

### 3.5 A chave do backup (`age`): a privada fica só com você

**Faça** (no seu computador, **fora** do diretório do repositório):

```bash
age-keygen -o ~/stillon-backup.key
chmod 600 ~/stillon-backup.key
age-keygen -y ~/stillon-backup.key > ~/stillon-age-recipient.txt
cat ~/stillon-age-recipient.txt
```

**Por quê:** o backup diário sai da VPS já cifrado com a chave **pública**; só a **privada**,
que nunca vai para a VPS nem para o GitHub, abre os dumps. Quem invadir a VPS ou ler os artifacts
do GitHub vê só texto cifrado. Fora do repositório para um `git add -A` nunca a pegar (o
`.gitignore` também cobre `*.key`, por garantia).

**Deve aparecer:** o `age-keygen -o` imprime `Public key: age1…`, e o `cat` mostra a mesma linha
`age1…`.

**Guarde** o conteúdo de `~/stillon-backup.key` no gerenciador (entrada "StillOn – chave
privada do backup (age)"):

```bash
cat ~/stillon-backup.key
clear
```

Copie as três linhas do arquivo (o comentário `# created: …`, o `# public key: age1…` e a
`AGE-SECRET-KEY-1…`) para o gerenciador e rode o `clear` em seguida, como no 3.4: a chave privada
não fica na tela. **Perder a chave privada = perder todos os backups.**

**Leve a pública para a VPS:**

```bash
scp ~/stillon-age-recipient.txt stillon:stillon-arquivos/
ssh -t stillon 'sudo install -m 0644 -o root -g root ~/stillon-arquivos/stillon-age-recipient.txt /opt/stillon/age-recipient'
```

**Confira** (no seu computador): a pública da VPS abre com a sua privada.

```bash
ssh stillon cat /opt/stillon/age-recipient | diff - ~/stillon-age-recipient.txt && echo "iguais"
echo "ida e volta" | age -R ~/stillon-age-recipient.txt | age -d -i ~/stillon-backup.key
```

`iguais` e `ida e volta`.

---

## Bloco 4: o usuário `deploy`, os secrets do GitHub e o primeiro deploy

O GitHub Actions entrega cada versão nova por SSH, como o usuário `deploy`. Esse usuário não tem
shell, não tem `sudo` geral e não está no grupo `docker`: a chave dele só consegue rodar
`/usr/local/sbin/stillon-entrada`, e o script só aceita dois pedidos, `deploy` e `backup`.

### 4.1 O script de entrada e a regra do `sudo`

**Faça** (na VPS):

```bash
cd ~/stillon-arquivos
sudo install -m 0755 -o root -g root stillon-entrada /usr/local/sbin/stillon-entrada
sudo visudo -cf sudoers-stillon-entrada
sudo install -m 0440 -o root -g root sudoers-stillon-entrada /etc/sudoers.d/stillon-entrada
sudo visudo -c
```

**Por quê:** o `sudoers` deixa o `deploy` rodar como root **só** esse script. O `visudo -c` confere
a sintaxe antes e depois: um `sudoers` quebrado pode travar o `sudo` de todo mundo. Se
acontecer, o conserto não pode depender do `sudo`: pelo terminal do painel, entre como **root**
com a senha de root do passo 1.2 e apague o arquivo (`rm /etc/sudoers.d/stillon-entrada`). Se o
terminal do painel só entrar com o seu usuário (o `sudo` quebrado não serve), use o **Emergency
mode** (bloco 10, "Perdi a chave SSH", item 3), que dá acesso ao disco.

**Deve aparecer:** `parsed OK` nas duas conferências (a segunda lista todos os arquivos).

**Confira:** `ls -l /usr/local/sbin/stillon-entrada /etc/sudoers.d/stillon-entrada` mostra
`-rwxr-xr-x root root` e `-r--r----- root root`.

### 4.2 O usuário `deploy`

**Faça** (na VPS):

```bash
sudo useradd --create-home --shell /bin/sh deploy
id deploy
sudo passwd -S deploy
```

**Por quê:** o `deploy` existe só para receber a chave do GitHub. A senha nasce travada (ninguém
entra por senha, e o SSH nem aceita senha). O shell `/bin/sh` é o que o SSH usa para executar o
comando forçado; interativo ele nunca fica, por causa do `restrict,command=` do passo 4.4.

**Deve aparecer:** `id` com `groups=…(deploy)` e **nenhum** outro grupo (nem `sudo`, nem
`docker`); `passwd -S` com `deploy L` (*locked*).

### 4.3 A chave do deploy (no seu computador)

**Faça** (no seu computador):

```bash
ssh-keygen -t ed25519 -N "" -C "stillon-deploy-github" -f ~/.ssh/stillon_deploy
scp ~/.ssh/stillon_deploy.pub stillon:stillon-arquivos/
```

**Por quê:** uma chave só para o GitHub, sem passphrase (o Actions não tem quem a digite). Ela
não precisa ser guardada: depois do passo 4.6 ela é apagada, e se um dia precisar, gera-se outra.

### 4.4 O `authorized_keys` do `deploy`, com a chave presa ao script

**Faça** (na VPS):

```bash
sudo install -d -m 0755 -o root -g root /home/deploy/.ssh
printf 'restrict,command="sudo /usr/local/sbin/stillon-entrada" %s\n' \
  "$(cat ~/stillon-arquivos/stillon_deploy.pub)" \
  | sudo tee /home/deploy/.ssh/authorized_keys >/dev/null
sudo chmod 0644 /home/deploy/.ssh/authorized_keys
sudo cat /home/deploy/.ssh/authorized_keys
```

**Por quê:** `command=` faz toda conexão com essa chave rodar o script, seja qual for o comando
pedido (o pedido chega ao script como texto, e ele recusa o que não for `deploy` ou `backup`);
`restrict` desliga terminal, túnel de porta, agente e X11. O diretório e o arquivo são do root:
nem o próprio `deploy` pode trocar a chave.

**Deve aparecer:** uma linha só, começando com
`restrict,command="sudo /usr/local/sbin/stillon-entrada" ssh-ed25519 AAAA` e terminando em
`stillon-deploy-github`.

**Confira** (no seu computador), que a chave **só** faz o que deve:

```bash
. ~/.stillon-vps
ssh -i ~/.ssh/stillon_deploy -o IdentitiesOnly=yes deploy@"$VPS" ; echo "saiu com $?"
ssh -i ~/.ssh/stillon_deploy -o IdentitiesOnly=yes deploy@"$VPS" ls / ; echo "saiu com $?"
ssh -i ~/.ssh/stillon_deploy -o IdentitiesOnly=yes -W 10.30.0.4:5432 deploy@"$VPS" ; echo "saiu com $?"
```

- As duas primeiras: `stillon-entrada: pedido vazio (uso: "deploy <sha de 40 hex> sha256:<64 hex>" ou "backup")`
  (a segunda com `caractere não permitido no pedido` no lugar de `pedido vazio`) e `saiu com 2` (pedido
  recusado; nada de lista de arquivos, nada de terminal). A primeira pode mostrar antes
  `PTY allocation request failed on channel 0`: é o `restrict` negando o terminal, como deve.
- A terceira: `open failed: administratively prohibited` e `saiu com 255` (o túnel para o banco
  é recusado).

Se alguma pedir senha, a chave não foi aceita: confira o passo 4.4 e `sudo journalctl -u ssh -n 20`
na VPS.

### 4.5 A chave do servidor, para o GitHub

**Faça** (no seu computador):

```bash
. ~/.stillon-vps
ssh stillon cat /etc/ssh/ssh_host_ed25519_key.pub > ~/stillon-host-key.txt
cat ~/stillon-host-key.txt
ssh-keygen -lf ~/stillon-host-key.txt
ssh-keyscan -t ed25519 "$VPS" 2>/dev/null | awk '{print $2, $3}'
```

**Por quê:** o Actions vai conferir que fala com a **sua** VPS (e não com alguém no meio) pela
chave do servidor fixada no secret `VPS_HOST_KEY`, sem `StrictHostKeyChecking=no`. O valor é a
linha inteira de `/etc/ssh/ssh_host_ed25519_key.pub`. Ela vem pela sua conexão SSH, cuja chave
você já conferiu no passo 2.1; o `ssh-keyscan` a busca de novo, por fora, para comparar.

**Deve aparecer:** o `cat` mostra uma linha `ssh-ed25519 AAAA… root@…`; o `ssh-keygen -lf`,
`256 SHA256:… (ED25519)`; o `ssh-keyscan`, `ssh-ed25519 AAAA…` com o mesmo `AAAA…` do `cat`.

**Confira:** o `SHA256:…` é idêntico ao do `ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub`
do terminal do painel (passo 2.1), e o `AAAA…` do `ssh-keyscan` é igual ao do `cat`. Se algo
diferir, pare e não grave o secret.

### 4.6 Os secrets do GitHub (sem colar nada)

**Faça** (no seu computador):

```bash
. ~/.stillon-vps
gh secret set VPS_HOST --repo BipoXDXD/stillon --body "$VPS"
gh secret set VPS_SSH_KEY --repo BipoXDXD/stillon < ~/.ssh/stillon_deploy
gh secret set VPS_HOST_KEY --repo BipoXDXD/stillon < ~/stillon-host-key.txt
gh secret list --repo BipoXDXD/stillon
```

**Por quê:** o `gh` lê o valor do arquivo e o cifra antes de mandar; nada passa pela área de
transferência nem pela tela. No GitHub Free com repositório privado não há *environments*: são
secrets de repositório, e quem pode disparar o deploy é quem tem acesso de escrita (hoje, só
você). Secrets de repositório não chegam a PR de fork.

**Deve aparecer:** a lista com `VPS_HOST`, `VPS_HOST_KEY` e `VPS_SSH_KEY` (com a data de hoje). São
os únicos secrets que o deploy e o backup usam; se aparecer algum outro, de uma hospedagem
anterior, apague-o com `gh secret delete <nome> --repo BipoXDXD/stillon`. A única variável é a
`BACKUP_ENABLED` (bloco 7): `gh variable list --repo BipoXDXD/stillon` mostra as que existem, e
outras antigas saem com `gh variable delete <nome> --repo BipoXDXD/stillon`.

**Agora apague a chave do deploy do seu computador:**

```bash
shred -u ~/.ssh/stillon_deploy
rm ~/.ssh/stillon_deploy.pub
```

**Confira:** `ls ~/.ssh/stillon_deploy*` diz que não existe.

### 4.7 Limpar a pasta de trabalho da VPS

**Faça** (na VPS):

```bash
rm -r ~/stillon-arquivos
```

**Por quê:** os arquivos já estão instalados nos lugares certos; cópias soltas envelhecem e
confundem. Na próxima mudança de `deploy/vps/` você recopia (bloco 10, "Arquivos da VPS").

### 4.8 O primeiro deploy, pelo Actions

**Antes:** a rede `edge` já existe e o `172.31.0.3` está livre. O Caddy do StillOn nasce nesse
IP, e o Docker recusa subir se outro contêiner já o usa. (O DNS do domínio não importa aqui: o
certificado agora é da borda, no 4.9.)

**Confira** (na VPS), um comando por vez:

```bash
sudo docker network inspect edge --format '{{range .IPAM.Config}}{{.Subnet}} {{.IPRange}}{{"\n"}}{{end}}'
```

```bash
sudo docker network inspect edge --format '{{range .Containers}}{{.Name}} {{.IPv4Address}}{{"\n"}}{{end}}'
```

**Por quê:** o contrato da borda fixa a sub-rede e reserva o `.128/25` aos IPs dinâmicos; o
`172.31.0.3` fica de fora dele, só para o Caddy do StillOn.

**Deve aparecer:** no primeiro, `172.31.0.0/24 172.31.0.128/25`. No segundo, a borda em
`172.31.0.2/24` e o Nginx do GoblinShift em `172.31.0.4/24`, e **nenhuma linha** com
`172.31.0.3/24`. Se o Docker disser `No such network`, a borda não foi instalada: pare e siga a
[spec](../superpowers/specs/2026-10-13-vps-compartilhada-design.md) ("Ordem na VPS"). Se outro contêiner estiver no `.3`, pare: o IP é do StillOn pelo ADR 014.

**Faça** (no navegador, ou no seu computador):

1. Ache o SHA completo (40 caracteres) do último commit da `main` com o `ci` verde (o job
   `publish` só roda depois de todos os outros, e a tag `:<sha>` da imagem só existe depois que o
   Trivy aprovou). O `gh` pergunta direto ao GitHub; o `git fetch` atualiza a sua cópia, para
   comparar. Um comando por vez, cada um numa linha só (sem `\` no fim):
   ```bash
   SHA=$(gh run list -R BipoXDXD/stillon -w ci.yml -b main -e push -s success -L 1 --json headSha -q '.[0].headSha')
   ```
   ```bash
   echo "$SHA"
   ```
   ```bash
   cd ~/Projetos/Go/StillOn
   ```
   (No Mac do dono o clone é `~/Documents/Projetos/Go/StillOn`: use o caminho do seu.)
   ```bash
   git fetch origin
   ```
   ```bash
   git rev-parse origin/main
   ```
   **Deve aparecer:** duas linhas de 40 caracteres, normalmente **iguais**. Se forem diferentes,
   o topo da `main` ainda não passou no `ci` (está rodando ou ficou vermelho: veja
   `gh run list --repo BipoXDXD/stillon --workflow ci.yml --branch main --limit 3`). Espere ele
   ficar verde e repita, ou faça o deploy do `$SHA` da primeira linha, que é o último que passou.
   Se a primeira linha vier vazia, nenhum `ci` da `main` passou ainda: não há imagem para
   entregar.

   **Cuidado com o `[skip ci]`.** Um squash merge cujo título ou corpo traga `[skip ci]` faz o `ci`
   da `main` pular, e a ponta da `main` fica **sem imagem**. Aí o `--status success --limit 1`
   devolve o SHA **anterior**, que pode não ter o último conserto. Se as duas linhas divergirem e
   o commit que falta mexe em código do deploy, não faça o deploy do `$SHA` às cegas: publique a
   ponta por outro caminho (um PR de código, que roda o `ci` completo) antes.
2. **Actions → deploy → Run workflow**, na `main`, com o SHA no campo `sha`. Ou, pelo
   terminal, no mesmo terminal do item 1 (ele usa o `$SHA`):
   ```bash
   gh workflow run deploy.yml --repo BipoXDXD/stillon --ref main -f sha="$SHA"
   ```
   O `gh run watch` **sem id pergunta** qual execução acompanhar (é interativo). Pegue o id da
   execução que acabou de começar e passe-o, com `--exit-status` para o comando terminar com o
   resultado do job:
   ```bash
   gh run list --repo BipoXDXD/stillon --workflow deploy.yml --limit 1
   ```
   ```bash
   gh run watch ID --repo BipoXDXD/stillon --exit-status
   ```
   (`ID` é o número da execução que aparece na linha do `list`.)

**Por quê:** o deploy é sempre manual e sempre de uma imagem que passou no CI e no Trivy. O job
resolve a tag no digest, baixa do GHCR a imagem linux/amd64 por esse digest (um tar em layout
OCI, sem nome), manda por SSH (`ssh deploy@… "deploy <sha> <id>"`, com o tar na entrada padrão), e
o script na VPS confere o ID, sobe a pilha inteira com a versão nova (no primeiro deploy, o Caddy e
o banco sobem junto com o app; nos seguintes, só o app muda), espera o healthcheck por até 90 s e
volta sozinho à anterior se ela não ficar saudável.

**Deve aparecer:** o job verde. Os códigos do script, se ficar vermelho, estão no bloco 10
("Depuração do deploy").

**Não cancele o job no meio**, mesmo que ele pareça parado (a espera do healthcheck leva até
90 s sem imprimir quase nada). E se cancelar, ou a conexão cair: depois que a imagem chegou à VPS,
o deploy (e o rollback, se for o caso) **termina sozinho lá**, sem o GitHub. O job fica vermelho
(`255`, ou cancelado), mas isso não diz como a VPS terminou. Confira na VPS antes de repetir:

```bash
cat /opt/stillon/versao
tail -n 30 /opt/stillon/entrada.log
```

O `versao` é o SHA que está no ar; o `entrada.log` é o log de cada deploy e backup, o mesmo que o
job mostra, gravado na VPS. Os dois são `0644`: leia-os **sem** `sudo`. O log tem um teto de
~1 MiB: passado dele, a entrada o renomeia para `entrada.log.1` (no lugar do `.1` que houver) e
começa um novo, e o `entrada.log.1` guarda a rodada anterior do log. Cada deploy e backup começa
com a linha `== <UTC> início: <pedido>` e, mesmo que o SSH tenha caído, a entrada grava **por
último** a linha `== <UTC> fim: <pedido>: código N (<sentido>)`. **É essa linha `fim:` que diz
como o deploy acabou** (código 0, `feito`; 4, `não ficou saudável; rollback feito, ou app parado
no primeiro deploy`; os outros, na tabela de "Depuração do deploy"); as linhas logo acima dela
dão o detalhe. Se a última `início:` ainda não tem a sua `fim:` (as linhas continuam chegando), ele
ainda está rodando: espere dois minutos e olhe de novo. Só rode o deploy outra vez depois de ver
a `fim:` deste.

**Se a `fim:` nunca chegar** (as linhas pararam, e passaram mais de 12 minutos da `início:`), a
entrada morreu no meio, sem chance de gravar a última linha: um `SIGKILL`, a VPS sem memória
(OOM) ou o reinício automático das 04:30 (passo 2.11) no meio do deploy. Confira se ela ainda
roda; se o comando não mostrar nada, ela morreu:

```bash
pgrep -af stillon-entrada
cat /opt/stillon/versao
sudo docker compose -f /opt/stillon/compose.prod.yaml ps
```

O `versao` diz qual SHA ficou registrado no ar, e o `ps` diz se o `app` está de pé e `healthy`.
Qualquer que seja o resultado, **repita o deploy do mesmo SHA** pelo Actions (passo 4.8, item 2),
mesmo que o `versao` já seja o SHA novo e o `app` esteja `healthy`. O motivo: a entrada grava o
`versao` antes de mover a etiqueta `stillon:atual`; se ela morreu entre as duas coisas, o `atual`
ficou na versão velha, e o próximo `up -d app` sem SHA rebaixaria o app sem aviso. Repetir o mesmo
SHA é seguro em qualquer ponto em que a entrada possa ter morrido (a imagem é a mesma) e termina o
que ficou pela metade.

A trava pode demorar a sair: um filho da entrada morta (o `docker load` ou o `docker compose up`)
pode seguir rodando, órfão, e segurar a trava até terminar. O próximo deploy espera por ela até
10 minutos. Se ainda assim ele falhar com `outra operação da entrada não terminou em 10 min`,
veja se sobrou algum desses filhos, espere alguns minutos e repita:

```bash
pgrep -af 'docker (load|compose)'
```

O próximo **deploy** apaga, antes de começar, os temporários que o morto deixou em `/opt/stillon`
(os `.stillon-entrada.*`, que podem ocupar alguns GB): não os apague à mão.

**Se o primeiro deploy sair com 4**, o app **ficou parado**: no primeiro deploy não há versão
anterior para onde voltar (a mensagem é
`primeiro deploy e sem versão anterior: o app ficou parado (e removido)`). O site responde `502`, e
o Caddy e o banco ficam de pé. Quase sempre é o `.env` (uma variável mal preenchida no 3.4) ou o
DNS. O contêiner do app é **removido**, e não só parado: um app que morre no boot fica em laço de
reinício (`restart: unless-stopped`), e só a remoção garante que ele não volta. Por isso o
`logs app` do compose fica vazio: as últimas 100 linhas do log do app estão no log do job e no
`entrada.log`, logo depois de `a pilha não ficou saudável em 90 s; o log do app:`. Veja por que o app
não subiu:

```bash
tail -n 150 /opt/stillon/entrada.log
```

(Se a entrada sair com **1** e `o app não saiu do ar depois de 3 tentativas`, o contêiner do app
seguiu de pé: `sudo docker compose -f /opt/stillon/compose.prod.yaml ps -a` mostra o estado, e
`sudo docker rm -f $(sudo docker compose -f /opt/stillon/compose.prod.yaml ps -aq app)` o mata e
o remove. O `compose rm` não serve aqui: ele pula um contêiner em `restarting`.)

O app diz no boot o que falta ou o que está errado (uma variável ausente, uma chave fora do
formato, o banco recusando o papel). Corrija a causa (por exemplo, `sudo nano /opt/stillon/.env`)
e rode o deploy de novo com o mesmo SHA: não é preciso publicar outra versão.

**Confira** (na VPS):

```bash
sudo docker compose -f /opt/stillon/compose.prod.yaml ps
cat /opt/stillon/versao
sudo docker compose -f /opt/stillon/compose.prod.yaml logs app | head -20
```

- `ps`: `caddy`, `app` e `db` com estado `running` (e `healthy` onde há healthcheck);
- `versao`: o SHA que você mandou (o `cat` vai sem `sudo`: o `versao`, o `versao-anterior` e o
  `historico` são `0644`, legíveis pelo seu usuário, porque não são segredo; o `.env` e o
  `vigia.env` continuam só do root). Se aparecer `Permission denied`, o `stillon-entrada`
  instalado é de antes dessa mudança: recopie-o (bloco 10, "Arquivos da VPS") e, até o próximo
  deploy, use `sudo cat`;
- `logs`: `iniciando` com a versão, `migrations aplicadas`, `sondador ativo`, `servindo`, e
  **nenhuma** linha `TRUSTED_PROXIES vazio`.

**De fora** ainda não responde: a borda só passa a encaminhar o domínio para o StillOn no 4.9.

**Um atalho para os próximos comandos** (na VPS; opcional, mas o resto do runbook o usa):

A linha do alias passa de 90 colunas e mistura aspas simples e duplas: digitada ou colada num
`echo`, ela se parte ou perde uma aspa. Grave-a num editor, uma única linha:

```bash
nano ~/.bash_aliases
```

No editor, cole esta linha (e salve com `Ctrl+O` e Enter, saia com `Ctrl+X`):

```text
alias vc='sudo docker compose -f /opt/stillon/compose.prod.yaml'
```

```bash
. ~/.bash_aliases
```

```bash
vc ps
```

O alias só vale em **shell interativo**: não funciona em `ssh stillon '<comando>'`. E uma sessão
nova só o terá se o `~/.bashrc` ler o `~/.bash_aliases` (no Ubuntu lê; se `vc` disser
`command not found` numa sessão nova, rode o `. ~/.bash_aliases` de novo). Se você preferir não
criar o alias, onde o runbook diz `vc …` use o comando longo
`sudo docker compose -f /opt/stillon/compose.prod.yaml …`.

**Confira no banco** quem é dono das tabelas (as migrations rodaram como o papel comum). As duas
consultas têm mais de 100 colunas com aspas aninhadas, e o `exec` sem `-T` não aceita
redirecionamento de arquivo; por isso elas vão para um arquivo, lido com `exec -T`:

```bash
nano ~/estado.sql
```

No editor, cole as duas consultas (uma por linha):

```text
SELECT tableowner, count(*) FROM pg_tables WHERE schemaname = 'public' GROUP BY 1;
SELECT rolname, rolsuper, rolbypassrls FROM pg_roles WHERE rolname LIKE 'stillon%' ORDER BY 1;
```

```bash
vc exec -T db psql -U postgres -d stillon < ~/estado.sql
```

Só `stillon_app` na primeira; `stillon_app | f | f` e `stillon_backup | f | t` na segunda.
Depois, `rm ~/estado.sql`.

### 4.9 O StillOn na borda

**Antes:** a borda em `/srv/edge` já é o Caddy (não o nginx), feito na troca da borda, guiada
pelo Claude ([spec](../superpowers/specs/2026-10-13-vps-compartilhada-design.md), "Ordem na
VPS"). Sem isso, o pedido de certificado falha e conta no limite do Let's Encrypt.

**A 443 ainda não responde, e é assim mesmo.** A borda só tem o site `:80` até o trecho deste
passo entrar: sem um site com nome de domínio, o Caddy não ouve a 443. Por isso um `nc` na 443
**antes** do trecho dá porta fechada (e não prova nada). A prova da 443 é a própria emissão do
certificado, no Confira do fim do passo. O que se confere antes é o DNS e a porta 80.

**Confira o Antes** (no seu computador, prompt `%`), um comando por vez:

```bash
. ~/.stillon-vps
```

```bash
dig +short "$DOMINIO"
```

```bash
dig +short AAAA "$DOMINIO"
```

```bash
dig +short CAA "$DOMINIO"
```

```bash
curl -sI "http://$DOMINIO/"
```

**Deve aparecer:** o primeiro `dig` devolvendo um IP igual ao `$VPS`; o `AAAA` e o `CAA`
**vazios** (um `AAAA` aponta o Let's Encrypt para um endereço que a VPS não atende, e um `CAA`
alheio o impede de emitir); e o `curl` com `404` (do GoblinShift) e `Via: 1.1 Caddy`, que mostra
que a resposta passou pela borda nova. Confira também, no painel da Hostinger, que a regra da
443/tcp existe no firewall (passo 2.10). Se o `dig` vier vazio ou diferente, o DNS (passo 1.4)
ainda não propagou; se o `curl` não mostrar o `Via`, a borda ainda é o nginx. Em qualquer caso,
não siga: o pedido de certificado falharia e contaria no limite do Let's Encrypt.

(No macOS, se for testar uma porta com `nc`, o tempo de conexão é `-G`; o `-w` é de ociosidade.)

**Faça, primeiro no seu computador** (prompt `%`), com o arquivo da `main` atualizada. Use o
caminho completo do clone (o comando não pode depender da pasta em que o terminal está, nem do
branch em que o clone está):

```bash
scp ~/Projetos/Go/StillOn/deploy/vps/edge/stillon.caddy stillon:
```

(Troque o caminho pelo do seu clone; no Mac do dono, `~/Documents/Projetos/Go/StillOn`. Se o clone
não estiver na `main`, o arquivo pode ser de outra versão: compare o SHA-256 do que chegou à VPS
com o do `git show origin/main:deploy/vps/edge/stillon.caddy`.)

**Depois, na VPS**, um comando por vez:

```bash
sudo install -m 0644 -o root -g root ~/stillon.caddy /srv/edge/conf.d/
```

```bash
rm ~/stillon.caddy
```

```bash
cd /srv/edge
```

```bash
sudo docker compose exec proxy caddy validate --config /etc/caddy/Caddyfile
```

```bash
sudo docker compose exec proxy caddy reload --config /etc/caddy/Caddyfile
```

**Deve aparecer:** `Valid configuration` no `validate`; o `reload` sem erro.

**Por quê:** o trecho diz à borda para pedir o certificado de `stillon.tech` e encaminhar o
domínio ao Caddy do StillOn (`172.31.0.3`, o IP reservado a ele na rede `edge`). O `validate` vem antes porque um
trecho inválido impede o `reload` da borda inteira, que também atende o GoblinShift (a
configuração anterior fica no ar, mas nenhuma mudança entra).

**A emissão do certificado é assíncrona:** o `reload` volta na hora, mas o Caddy da borda ainda
leva alguns segundos para pedir e receber o certificado. Um `curl` colado logo em seguida pode
falhar por TLS sem que nada esteja errado. Espere e repita **até uns 90 segundos** antes de
suspeitar; é essa emissão que prova que a 443 e o DNS estão certos.

**Confira** (no seu computador, prompt `%`):

```bash
. ~/.stillon-vps
```

```bash
curl -sS "https://$DOMINIO/healthz"; echo
```

```bash
curl -sS -o /dev/null -w '%{http_code}\n' "https://$DOMINIO/readyz"
```

```bash
curl -s -o /dev/null -w '%{http_code}\n' "http://$VPS/"
```

`/healthz` responde (o corpo do app) sem erro de certificado, e `/readyz` dá `200`. O último
`curl` confere que o GoblinShift, que a mesma borda atende pelo IP, continua respondendo depois
do `reload`: tem de devolver o código da resposta dele (hoje `404`), nunca erro de conexão. Se o
`curl` do domínio reclamar do certificado, espere um minuto e veja, na VPS, em `/srv/edge`:
`sudo docker compose logs --tail 30 proxy`. Um erro de "challenge" quase sempre é DNS ainda não
propagado ou a 80/443 fechada no firewall do painel. Não repita o `reload` em sequência: cada
tentativa falha conta no limite do Let's Encrypt.

---

## Bloco 5: seu usuário no StillOn e o 2FA

### 5.1 Criar o dono

**Antes, no gerenciador de senhas** (passo próprio, antes de qualquer comando): crie a entrada
"StillOn – login do painel" e gere nela a senha (de 15 a 128 caracteres). Deixe-a copiada.

**Faça** (na VPS, **numa sessão interativa**: abra com `ssh stillon` e rode o comando no prompt
dela; ele pede um terminal, então não funciona como `ssh stillon '…'`), com o seu e-mail no lugar
do exemplo:

```bash
vc exec app /stillon user create --email voce@exemplo.com
```

Ele pede `Senha:` e `Confirme a senha:`, sem eco. Cole a senha da entrada nas duas perguntas.

**Por quê:** o sistema é de um dono só, e não há cadastro pela web: o primeiro usuário nasce pelo
CLI, dentro do contêiner que já tem a `DATABASE_URL`. A senha vem do terminal, nunca de argumento
(argumento aparece no `ps` e no histórico). O `docker compose exec` já abre um terminal, por isso
não ecoa.

**Deve aparecer:** `usuário criado: voce@exemplo.com (id …)`. Se disser
`já existe um usuário; nada foi alterado` (código 3), alguém já criou: use
`vc exec app /stillon user set-password --email <e-mail>` para trocar a senha.

### 5.2 Entrar e ligar o 2FA

**Antes, no gerenciador de senhas:** crie a entrada "StillOn – códigos de recuperação do 2FA"
(vazia por ora), para os códigos terem onde cair assim que aparecerem.

**Faça** (no navegador): abra `https://seu-domínio`, entre com o e-mail e a senha. Vá em
**Configurações → Verificação em duas etapas** e siga os botões, nesta ordem: **Ativar**, leia o
QR code com o seu app autenticador (Aegis, 2FAS, Google Authenticator…), **Continuar**, digite o
código de 6 dígitos, **Ativar**. Aparecem os **códigos de recuperação**: use só **Copiar** e cole
na entrada do gerenciador (**não use Baixar .txt**: ele deixa os códigos em texto numa pasta de
Downloads). Confira a entrada, volte à tela e clique **Guardei os códigos** e depois **Concluir**.

**Por quê:** com o painel na internet, a senha sozinha é pouco. Os códigos de recuperação são a
volta se o celular sumir; sem eles e sem o app, só o `reset-2fa` pelo CLI (bloco 10).

**Confira:** saia (**Sair**), entre de novo: depois da senha, ele pede o código do app. A tela
**Configurações** mostra a verificação em duas etapas ativada.

---

## Bloco 6: e-mail, canal e alvo de teste

### 6.1 Canal de e-mail

**Faça** (no painel): no menu, **Canais** (a página se chama "Canais de notificação") → botão
**Novo canal**, tipo **E-mail**, com o seu e-mail.

**Por quê:** confere ponta a ponta o Resend (domínio verificado, chave, remetente) e os links, que
usam a `PUBLIC_BASE_URL` de produção.

**Deve aparecer:** `Enviamos o link de verificação.` e, em até alguns minutos, um e-mail do
remetente `StillOn <alertas@avisos.seu-domínio>` com um link para `https://seu-domínio/…`.
Clique nele.

**Confira:** o canal aparece como verificado. Se o e-mail não chegar: a caixa de spam; depois
`vc logs app | grep -i resend | tail` (um `403` é domínio não verificado ou `EMAIL_FROM` com
outro domínio).

(Opcional: um canal do Discord pelo mesmo caminho; ele manda uma mensagem de teste na hora.)

### 6.2 Alvo de teste e um incidente de verdade

**Faça** (no painel): **Cadastrar alvo** (página "Novo alvo") com a URL `https://example.com/`,
**Intervalo** "30 segundos" (é uma lista, não um número) e **status esperado 500** (de propósito:
o site responde 200, então o StillOn o verá fora do ar). O botão que grava é **Cadastrar**; o
**Salvar** só aparece ao editar um alvo existente.

**Por quê:** força um incidente sem depender de derrubar nada: confere sondador, detector,
fila de avisos e e-mail, tudo de uma vez.

**Deve aparecer:** em um ou dois minutos, o alvo fica fora do ar no painel, um incidente abre e
chega o e-mail de alerta (o detector abre com 3 falhas seguidas).

**Confira:** depois do e-mail, edite o alvo e ponha o status esperado **200**. O incidente fecha
só com **5 sucessos seguidos**, ou seja, uns 2,5 minutos a 30 s (não "um ou dois minutos"), e
então chega o aviso de que voltou. Então apague o alvo, ou deixe-o com 200
como monitor de exemplo.

---

## Bloco 7: backup ligado e ensaio de restauração

O backup diário (06:17 UTC, 03:17 de Brasília) roda no GitHub: `ssh deploy@VPS backup` devolve o
dump **já cifrado** com a sua chave pública, que vira um artifact de 30 dias. Na VPS, o script
sobe um Postgres descartável, faz o dump como `stillon_backup`, restaura a cópia e a confere
(`deploy/backup/backup.sh` e `verificar.sql`) antes de cifrar. Um backup que não restaura não é
backup. A referência de schema/usuários/alvos e o dump usam o mesmo snapshot; mudanças
confirmadas depois da referência não entram nesse dump.

O supervisor vem automaticamente de `/stillon` na imagem local `stillon:<SHA>` indicada
por `/opt/stillon/versao`: a entrada resolve seu ID, cria um contêiner auxiliar parado,
copia o binário para memória e depois para a camada regular do Postgres descartável.
Não precisa de binário instalado à mão, imagem extra ou app rodando. O probe de protocolo
ocorre antes de conectar à origem; imagem antiga e scripts novos recusam com código 1.
O binário nunca executa no host ou por montagem executável de `/dev/shm`.

O probe tem prazo de 30 s, com mais 5 s para matar o cliente Docker; como matar
o cliente sozinho não encerra um `docker exec` remoto, a entrada remove seu contêiner
exato na falha.

O helper roda com UID/GID 65532, sem capabilities efetivas, em contêiner com `--init`
e `no-new-privileges`. O PostgreSQL descartável conserva o limite de 3 GiB e o tmpfs de
2 GiB. Esse controle de UID/capabilities é do sistema operacional: o helper ainda
pode alcançar o PostgreSQL descartável local e a cópia dos dados de produção; não
é uma barreira de isolamento entre o helper e o banco de conferência.
O supervisor limita preparação, dump e encerramento dos seus filhos; restauro,
verificação e consulta final de disco são etapas separadas. A consulta de disco tem
connect timeout de 10 s e statement timeout de 30 s; isso não é prazo global de todo o
backup. A URI pública continua aceitando parâmetros libpq, exceto credenciais na query e
`options`, `service`, `application_name`, `fallback_application_name`, `connect_timeout`,
que poderiam alterar as proteções de conexão/sessão. A senha da autoridade não vai a argv.

### 7.1 Ligar e rodar o primeiro à mão

**Confira primeiro** (na VPS, depois do deploy e da cópia dos scripts da mesma versão):

```bash
vc exec -T app /stillon version --backup-snapshot-protocol
```

Deve aparecer somente `stillon-backup-snapshot-v1`. Se recusar o argumento, a imagem
é antiga: atualize imagem, `stillon-entrada` e `backup.sh` juntos antes de ligar o backup.
Esse comando só verifica o protocolo; não inicia servidor nem conecta ao banco.

**Faça** (no seu computador):

```bash
gh variable set BACKUP_ENABLED --repo BipoXDXD/stillon --body 1
gh workflow run backup.yml --repo BipoXDXD/stillon --ref main
gh run list --repo BipoXDXD/stillon --workflow backup.yml --limit 1
```

```bash
gh run watch ID --repo BipoXDXD/stillon --exit-status
```

(O `gh run watch` sem id pergunta qual execução acompanhar; com o id da execução do `backup` que
acabou de começar, que o `list` mostra, ele acompanha e termina com o resultado do job. **Anote
esse id**: o 7.2 baixa o artifact dessa execução exata.)

**Por quê:** até a variável existir, o agendamento diário termina em *skipped* sem fazer nada.
Rodar à mão agora confere tudo (chave, script, `age`, artifact) antes de confiar no automático.

**Deve aparecer:** o job verde, e no resumo uma linha `Postgres: N% …` com a ocupação do disco.

**Confira** (na VPS), que o dump em texto claro e o banco descartável não sobraram:

```bash
sudo ls -A /dev/shm
sudo docker ps -a --filter label=io.stillon.backup.raiz=/opt/stillon
```

O `ls` não mostra nenhum `stillon-backup.*` nem `stillon-backup-helper.*`, e o `docker ps` mostra só a linha de títulos
(`CONTAINER ID   IMAGE …`), sem nenhum contêiner. O dump em claro só existe durante o backup, em
`/dev/shm` (memória, não disco), e a restauração de conferência roda num Postgres descartável
chamado `stillon-restauro-…`; o script apaga os dois no fim, e só o cifrado sai da VPS. Se sobrar
algum recurso próprio (um backup morto por SIGKILL), a entrada seguinte o recupera sob
a mesma trava, conferindo labels privadas, nome, nonce e imagem antes de remover por ID.
Contêineres legados sem esses labels ou de outros projetos não são apagados automaticamente;
se houver um, confira sua origem antes de qualquer remoção manual.

**Depois de ligado, um `backup` agendado *skipped* (cinza) é falha**, tanto quanto um vermelho: não
houve backup naquele dia, e o GitHub não manda e-mail por isso. A causa usual é a variável
`BACKUP_ENABLED` apagada ou mudada.

### 7.2 Ensaio de restauração (antes de dar o StillOn por pronto)

**Faça** (no seu computador):

1. Baixe o artifact da execução do passo 7.1:
   ```bash
   mkdir -p ~/stillon-restauro && cd ~/stillon-restauro
   RUN_ID=ID_DO_PASSO_7_1
   echo "$RUN_ID"
   gh run download "$RUN_ID" --repo BipoXDXD/stillon
   ARQ=$(find ~/stillon-restauro -name 'stillon-*.dump.age' | sort | tail -1)
   echo "$ARQ"
   ```
   Troque `ID_DO_PASSO_7_1` pelo id anotado no 7.1: o `gh run list --status success --limit 1`
   pode devolver outro backup (um agendado, mais antigo ou mais novo), e os números do item 3
   são os do log **desta** execução. Deve aparecer o caminho de um arquivo
   `stillon-AAAAMMDD.dump.age` (o dump do Postgres, em formato custom, cifrado).
2. Decifre, com a chave que só você tem:
   ```bash
   age -d -i ~/stillon-backup.key -o ~/stillon-restauro/stillon.dump "$ARQ"
   ```
3. Restaure num Postgres descartável, da mesma imagem de produção (o digest de
   `deploy/vps/db/Dockerfile`), só no `127.0.0.1` do seu computador. O `docker compose stop`
   para o compose de desenvolvimento, se estiver de pé: ele ocupa a `127.0.0.1:8080` que a cópia
   usa no item 4 (sem ele de pé, o comando não faz nada):
   ```bash
   cd ~/Projetos/Go/StillOn
   docker compose stop
   docker run -d --name restauro -p 127.0.0.1:25433:5432 -e POSTGRES_PASSWORD=restauro \
     "$(sed -n 's/^FROM //p' deploy/vps/db/Dockerfile)"
   until docker exec restauro pg_isready -h 127.0.0.1 -U postgres; do sleep 1; done
   docker exec -i restauro psql -U postgres < deploy/compose/initdb.sql
   docker exec -i restauro pg_restore -U postgres --exit-on-error -d stillon < ~/stillon-restauro/stillon.dump
   ```
   As mesmas checagens do backup, com os números da linha `schema N, U usuário(s), A alvo(s)` do
   log daquela execução (`gh run view "$RUN_ID" --repo BipoXDXD/stillon --log | grep 'schema '`):
   ```bash
   docker exec -i restauro psql -U postgres -d stillon -v versao=N -v usuarios=U -v alvos=A \
     < deploy/backup/verificar.sql
   ```
   Deve terminar em `NOTICE:  cópia conferida`.
4. Suba o StillOn **da versão que está no ar** contra a cópia, **só com a API** (`--role=api`):
   com `--role=all`, a cópia sondaria os seus alvos e mandaria alertas aos canais **reais**. A
   imagem é privada (o pacote tem visibilidade própria: **continua privado mesmo com o repositório
   público**, e um `pull` anônimo dá `UNAUTHORIZED`): entre no GHCR com o token do `gh`, depois de
   dar a ele a permissão `read:packages` (o `refresh` abre o navegador uma vez), baixe a imagem e
   **saia logo em seguida**. Se o token do `gh` já tem `write:packages` (veja o `gh auth status`),
   que inclui a leitura, o `refresh` é desnecessário: pule-o.
   ```bash
   gh auth refresh -s read:packages
   SHA=$(ssh stillon cat /opt/stillon/versao)
   echo "$SHA"
   gh auth token | docker login ghcr.io -u BipoXDXD --password-stdin
   docker pull "ghcr.io/bipoxdxd/stillon:$SHA"
   docker logout ghcr.io
   ```
   O `echo` tem de mostrar os 40 caracteres do SHA no ar (o `versao` é legível sem `sudo`, passo
   4.8). Se mostrar uma linha em branco, com um `Permission denied` antes, e o `docker pull`
   falhar com `invalid reference format`, o `stillon-entrada` da VPS é de antes dessa mudança:
   recopie-o (bloco 10, "Arquivos da VPS") ou, por ora, rode `sudo cat /opt/stillon/versao` na
   VPS e, aqui, `SHA=` seguido do valor colado, e repita a partir do `docker login`.
   O `docker login` grava o token no `~/.docker/config.json` do seu computador, **em claro** (o
   próprio Docker avisa: `stored unencrypted`). **No Mac não é assim:** o Docker Desktop guarda no
   Chaveiro (`credsStore: desktop`), e o `config.json` não mostra o `ghcr.io`; veja o item 6. E esse token não é só de leitura de pacotes: é o
   do `gh`, que escreve em todos os seus repositórios. O `docker logout` o apaga; a imagem já
   baixada continua no seu Docker. Então suba a cópia:
   ```bash
   read -rs -p 'MASTER_KEY de produção (do gerenciador): ' MASTER_KEY; echo
   export MASTER_KEY
   docker run --rm --network host \
     -e DATABASE_URL='postgres://stillon_app:stillon_app@127.0.0.1:25433/stillon?sslmode=disable' \
     -e MASTER_KEY \
     -e JWT_SECRET="$(openssl rand -base64 32)" -e IP_HASH_KEY="$(openssl rand -base64 32)" \
     -e PUBLIC_BASE_URL=http://localhost:8080 -e CORS_ORIGIN=http://localhost:8080 \
     -e HTTP_ADDR=127.0.0.1:8080 -e METRICS_ADDR=off \
     "ghcr.io/bipoxdxd/stillon:$SHA" --role=api
   ```
   O `read -rs -p` só existe no `bash`: no `zsh` do Mac ele falha com `read: -p: no coprocess`
   (rode `bash` antes, ou execute os comandos num script com `#!/bin/bash`). O
   `--network host` exige, no Docker Desktop, a opção **Enable host networking**; sem ela, use a
   variante do [mac.md](mac.md) (com `host.docker.internal` e `-p 127.0.0.1:8080:8080`), que
   convém subir com `-d --rm` para não prender o terminal (aí o `Ctrl+C` do item 6 não é preciso:
   `docker rm -f` remove o contêiner).
   O `read -rs` lê a chave sem eco e sem gravá-la no histórico; o `-e MASTER_KEY` sem valor passa
   a variável ao contêiner sem ela aparecer na linha de comando. A senha `stillon_app` vale aqui
   porque o `initdb.sql` é o do compose local (a de demonstração, num banco que só existe no seu
   `127.0.0.1`). `JWT_SECRET` e `IP_HASH_KEY` são novos: a cópia não aceita sessões de produção.
5. Num navegador, `http://localhost:8080`: entre com o seu usuário de produção (senha e 2FA) e
   abra um alvo que tenha header, ou um canal. **É isto que o ensaio prova**: os dados voltam, e a
   `MASTER_KEY` do gerenciador é a certa (com a errada, header e canal dão erro).
6. Limpe:
   ```bash
   # Ctrl+C no terminal do docker run, e então:
   unset MASTER_KEY
   docker rm -f restauro
   shred -u ~/stillon-restauro/stillon.dump
   docker logout ghcr.io
   grep -q ghcr.io ~/.docker/config.json 2>/dev/null && echo "AINDA LOGADO" || echo "sem login do ghcr"
   ```
   O `docker logout` de novo é por garantia (se o item 4 parou no meio); a última linha tem de
   dizer `sem login do ghcr`. **No Mac:** não existe `shred` (use `rm -f`, com o FileVault ligado:
   [mac.md](mac.md)), e o `grep` no `config.json` não prova nada, porque o login mora no Chaveiro.
   A prova é `docker-credential-desktop list`, que não pode mostrar o `ghcr.io`.

**Confira:** o passo 5 funcionou. Anote a data do ensaio no gerenciador: ele se repete a cada
trimestre (bloco 10).

### 7.3 A segunda camada: backup semanal da Hostinger

**Confira** no painel, **Backups & Monitoring → Snapshots & Backups**, que os backups automáticos
estão ligados (a Hostinger faz um semanal; fonte acima, passo 2.4). Eles guardam a VPS inteira e
servem para "a VPS morreu" (bloco 10) quando o de ontem for velho demais. Restaurar um deles
sobrescreve a VPS inteira.

---

## Bloco 8: o vigia externo

**Faça** (no seu computador, na `main` atualizada):

```bash
cd ~/Projetos/Go/StillOn
git switch main && git pull --ff-only
ssh stillon 'mkdir -p ~/stillon-arquivos'
scp deploy/vps/vigia.sh deploy/vps/stillon-vigia.service deploy/vps/stillon-vigia.timer \
    deploy/vps/vigia.env.exemplo stillon:stillon-arquivos/
```

**Depois, na VPS**, os arquivos nos lugares certos e o `vigia.env` nascendo do modelo, sem
valores, só do root:

```bash
cd ~/stillon-arquivos
```

```bash
sudo install -m 0755 vigia.sh /usr/local/sbin/stillon-vigia
```

```bash
sudo install -m 0644 stillon-vigia.service /etc/systemd/system/
```

```bash
sudo install -m 0644 stillon-vigia.timer /etc/systemd/system/
```

```bash
sudo install -m 0600 vigia.env.exemplo /opt/stillon/vigia.env
```

(Sob `sudo` o dono dos arquivos instalados já é o root, por isso sem `-o root -g root`: mesmo
resultado, em linhas que cabem na tela. Uma unidade por `install`, pelo mesmo motivo.
`/usr/local/sbin/stillon-vigia` é o caminho que o `ExecStart` do `stillon-vigia.service` espera.)

**Preencha o `vigia.env`.** O domínio é o do `.env` (não é segredo; confira-o primeiro, que o
`grep` o mostra):

```bash
sudo grep '^STILLON_DOMINIO=' /opt/stillon/.env
```

```bash
sudo nano /opt/stillon/vigia.env
```

No editor, digite o domínio depois de `STILLON_DOMINIO=` (o mesmo que o `grep` mostrou) e cole em
`HC_URL=` a ping URL do gerenciador (passo 1.6), sem espaço em volta do `=`. Salve com `Ctrl+O` e
Enter, saia com `Ctrl+X`. (Um comando `sed` com `$(…)` e `\$` numa linha só se perde na colagem;
por isso o domínio é digitado no editor.)

**Por quê:** o vigia recebe **só** este arquivo, com as duas variáveis de que precisa: o domínio e
a URL do healthchecks. O `.env`, com a `MASTER_KEY` e as senhas do banco, nunca chega a ele. O
serviço roda com um usuário criado na hora, sem acesso ao `/opt/stillon`; o systemd, como root,
lê o `vigia.env` e entrega uma cópia ao vigia (`LoadCredential`).

**Confira** (o `HC_URL` não aparece na tela):

```bash
sudo stat -c '%U %a' /opt/stillon/vigia.env
sudo grep -oE '^[A-Z_]+=' /opt/stillon/vigia.env
sudo grep -cE '^[A-Z_]+=$' /opt/stillon/vigia.env
sudo grep -c '^HC_URL=https://hc-ping.com/' /opt/stillon/vigia.env
sudo grep -c '^HC_URL=' /opt/stillon/.env
sudo grep '^STILLON_DOMINIO=' /opt/stillon/vigia.env
```

`root 600`; só `STILLON_DOMINIO=` e `HC_URL=`; `0` (nenhuma vazia); `1` (a URL do healthchecks
no formato certo); `0` na quinta (a `HC_URL` não está no `.env`); e, na última, o mesmo domínio
que o `.env` tem.

**Ligue o timer e limpe a pasta de trabalho:**

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now stillon-vigia.timer
rm -r ~/stillon-arquivos
```

**Por quê:** a cada minuto o timer faz um `curl` em `https://seu-domínio/healthz`, passando por
DNS, certificado, Caddy e app como um visitante; se der certo, pinga a `HC_URL` do `vigia.env`;
se não, pinga `HC_URL/fail`. O healthchecks manda e-mail quando chega um `/fail` ou quando os pings
param (VPS morta, rede fora).

**Confira:**

```bash
systemctl list-timers stillon-vigia.timer
sudo journalctl -u stillon-vigia.service -n 5 --no-pager
```

O timer com a próxima execução em menos de um minuto; o diário sem erro. Só o `journalctl` exige
`sudo`; o resultado da última execução se lê sem ele:

```bash
systemctl show stillon-vigia.service -p Result -p ExecMainStatus
```

Deve dar `Result=success` e `ExecMainStatus=0`. No healthchecks.io, o check `StillOn` fica **up**
(verde): como o timer dispara no segundo `:00` de cada minuto, em geral um minuto basta (até dois
no pior caso).

**Confira que o alarme funciona**, derrubando o app de propósito (na VPS):

```bash
vc stop app
```

Em um ou dois minutos o check fica **down** e chega o e-mail do healthchecks (o e-mail de `down`
sai **na hora** do `/fail` que o vigia manda quando o `curl` falha; os 5 minutos de carência só
valem quando o ping *some*, com a VPS morta). Então:

```bash
vc start app
```

O check volta a **up**, e chega o e-mail de volta.

---

## Bloco 9: verificações finais

Cada item abaixo é uma das obrigações do Plano 10 (a tabela "Fechadas no Plano 11" de
`docs/superpowers/PENDENCIAS-PARA-O-PLANO-12.md`) ou um critério da spec. Só dê o StillOn por
pronto com todos verdes.

### 9.1 Só 22, 80 e 443 abertas, vistas de fora (obrigação 3)

**Faça** (no seu computador, **fora** da VPS; pode levar **até meia hora**, porque o firewall
do painel descarta em silêncio as 65 mil portas fechadas e o `nmap` espera cada uma; não
interrompa):

```bash
. ~/.stillon-vps
sudo nmap -Pn -T4 -p- --open "$VPS"
```

**Deve aparecer:** `22/tcp open ssh`, `80/tcp open http`, `443/tcp open https`, e **nenhuma**
outra porta (sem 5432, sem 8080, sem 9090).

**No Mac**, a varredura SYN (`-sS`, a padrão com `sudo`) pede root. A varredura de conexão
(`-sT`) **não pede senha**, é mais lenta e basta para este objetivo:

```bash
nmap -Pn -T4 -p- --open -sT "$VPS"
```

**E por dentro** (na VPS; sem `sudo` o `ss` funciona, só não mostra o nome do processo):

```bash
sudo ss -tlnp
```

Escutando em todos os endereços (`0.0.0.0` ou `[::]`): só a 22 (`sshd` ou `systemd`: no
Ubuntu 24.04 o SSH é ativado por socket, e quem escuta a 22 pode aparecer como `systemd`, com ou
sem o `sshd` ao lado) e a 80 e 443 (`docker-proxy`, da borda). O resto, se houver, só em
`127.0.0.x` (como o DNS local do Ubuntu, `127.0.0.53`, e o OpenObserve do GoblinShift, `127.0.0.1:5080`).

**Confira nos composes:**

```bash
vc config | grep -c 'ports:'
```

```bash
cd /srv/edge
```

```bash
sudo docker compose config | grep -n -A6 'ports:'
```

O StillOn não tem `ports` nenhum (`0`); só a borda publica, e só a 80 e a 443. (Os dois comandos
da borda são separados, sem `&&`: um `cd` solto numa linha com outro comando se perde se a
sessão cair. A alternativa sem `config` é ler o `compose.prod.yaml` do StillOn e o
`/srv/edge/compose.yaml`, que são legíveis, e procurar `ports:`.) Volte com `cd` ao terminar.

### 9.2 O IP do cliente chega certo (obrigações 1 e 2)

**Faça** (na VPS), depois de ter usado o painel por uns minutos:

```bash
vc logs app | grep -c 'TRUSTED_PROXIES vazio'
vc logs app | grep 'cabeçalho de IP do cliente' | tail -3
```

**Deve aparecer:** `0` e nenhuma linha. O primeiro aviso diria que o app não confia em proxy
nenhum; o segundo (`… ignorado` ou `… ausente ou ilegível`), que o `TRUSTED_PROXIES` não bate com
o IP do Caddy (10.30.0.2).

**O teste de duas redes** (no seu computador; sempre o e-mail `ninguem@exemplo.com`, que não
existe, e senha errada: com o **seu** e-mail, cada erro contaria no bloqueio da conta, 5 em
15 minutos).

O limitador do login deixa **5 tentativas de uma vez** por IP e devolve **uma a cada 6 s**. Por
isso as provas abaixo têm de ser rápidas: depois de 6 s parado, o balde já ganhou uma ficha, e um
`401` não prova mais nada.

**Parte A, de casa: o cabeçalho forjado não escolhe o balde.** Cole o bloco **inteiro de uma
vez** (os sete pedidos saem em poucos segundos, dentro da janela de 6 s):

```bash
. ~/.stillon-vps
tentar() {
  curl -s -o /dev/null -w '%{http_code}\n' -X POST "https://$DOMINIO/api/v1/auth/login" \
    -H 'Content-Type: application/json' -H 'X-Requested-With: stillon' \
    -d '{"email":"ninguem@exemplo.com","password":"errada-errada-1234"}' "$@"
}
for i in 1 2 3 4 5 6; do printf '%s: ' "$i"; tentar; done
printf 'forjado: '
tentar -H 'X-Real-IP: 203.0.113.77' -H 'Fly-Client-IP: 203.0.113.77' \
  -H 'CF-Connecting-IP: 203.0.113.77' -H 'X-Forwarded-For: 203.0.113.77'
```

**Deve aparecer:** `1: 401` a `5: 401`, `6: 429` e `forjado: 429`. O pedido forjado diz ser de
outro IP (`203.0.113.77`), mas a borda troca o `X-Forwarded-For` pelo IP da sua casa, o Caddy do
StillOn só acredita na borda e remove os outros três: ele cai no mesmo balde vazio da sua casa. Se
o `forjado` der `401`, o app acreditou no cabeçalho do cliente e escolheu um balde novo: pare e
confira, na VPS, o `Caddyfile` que o Caddy do StillOn está usando
(`vc exec caddy cat /etc/caddy/Caddyfile`), o trecho da borda
(`cat /srv/edge/conf.d/stillon.caddy`, sem `header_up` nem `trusted_proxies`) e o
`TRUSTED_PROXIES` (`vc config | grep TRUSTED`).
A conclusão só vale com o `6: 429`: se ele vier `401` (a rede demorou mais de 6 s nos cinco
primeiros) ou se já o `1` vier `429` (você testou há pouco), espere um minuto, para o balde
encher de novo, e cole o bloco outra vez.

**Parte B, casa e celular ao mesmo tempo: cada cliente tem o seu balde.** No computador, deixe a
casa gastando o balde por um minuto e meio (a função `tentar` é a da parte A, no mesmo terminal):

```bash
for i in $(seq 1 90); do printf '%s: ' "$i"; tentar; sleep 1; done
```

Quase tudo `429`, com um `401` a cada 6 s mais ou menos (a ficha que volta é gasta logo). **Enquanto
ele roda**, no celular em 4G/5G, com o **Wi-Fi desligado**, faça **três** tentativas, com alguns
segundos entre elas: o mesmo `curl` no Termux, ou o painel no navegador com o e-mail
`ninguem@exemplo.com` e uma senha qualquer.

**Deve aparecer:** no celular, três vezes `401` (no painel, "E-mail ou senha incorretos."), nunca
`429`. O balde do celular está cheio, porque é outro IP.

Se o celular levar algum `429`, todos os clientes estão dividindo um balde. Há duas causas, e
as duas se conferem:

- O app vê o IP do Caddy do StillOn em todo pedido: o `TRUSTED_PROXIES` do app não é o IP do
  Caddy (10.30.0.2). Confira o `compose.prod.yaml` e o `.env`.
- O app vê o IP da borda (172.31.0.2) em todo pedido: o Caddyfile do StillOn está sem
  `header_up X-Forwarded-For {client_ip}`, ou o `trusted_proxies` dele não casa com o IP da
  borda (`172.31.0.2/32`). Confira com `vc exec caddy cat /etc/caddy/Caddyfile`.

### 9.3 `Host` estranho não chega ao app (obrigação 5, a parte do Caddy)

**Faça** (no seu computador):

```bash
. ~/.stillon-vps
curl -s -o /dev/null -w '%{http_code}\n' -H 'Host: outro.exemplo' "http://$VPS/healthz"
curl -sk -o /dev/null -w '%{http_code}\n' -H 'Host: outro.exemplo' "https://$DOMINIO/healthz"
curl -sk -o /dev/null -w '%{http_code}\n' "https://$VPS/healthz"; echo "curl saiu com $?"
```

**Deve aparecer:** na primeira, a resposta do GoblinShift (a borda manda todo `http` pelo IP para
ele: hoje, `404`), sem passar pelo StillOn; `200` na segunda, mas **vazio e da própria borda**:
nenhum bloco casa com esse `Host`, e nenhum app é chamado; e `000` com `curl saiu com 35` na
terceira (sem certificado para o IP, o TLS nem fecha; o `curl` do macOS usa o LibreSSL, e o código
de saída pode não ser exatamente `35`: o que vale é o `000`). Para ver que nenhuma delas veio do
StillOn, repita cada uma com `-D -` no lugar do `-o /dev/null -w …`: nenhum `x-trace-id` (todo
pedido que chega ao app do StillOn volta com ele). Nunca o corpo do StillOn. **Não use a ausência
de `Server: nginx` como prova**: o nginx interno do GoblinShift continua respondendo atrás da
borda, e a borda aparece em `Via: 1.1 Caddy`. A prova é a ausência do `x-trace-id`.

**Por quê:** a borda só tem o bloco do seu domínio para o StillOn, e o Caddy do StillOn também só
aceita esse nome; uma requisição com outro nome não chega ao app.

### 9.4 Certificado e cabeçalhos

**Faça** (no seu computador):

```bash
. ~/.stillon-vps
echo | openssl s_client -connect "$DOMINIO:443" -servername "$DOMINIO" 2>/dev/null \
  | openssl x509 -noout -subject -issuer -dates
curl -sI "https://$DOMINIO/" | grep -iE '^(HTTP|strict-transport-security|content-security-policy|x-content-type-options)'
curl -s -o /dev/null -w '%{http_code} %{redirect_url}\n' "http://$DOMINIO/"
```

**Deve aparecer:** `subject` com o seu domínio, `issuer` do Let's Encrypt (ou do ZeroSSL, a
alternativa automática do Caddy da borda) e `notAfter` uns 90 dias à frente; a resposta `200` com
`Strict-Transport-Security`, `Content-Security-Policy` e `X-Content-Type-Options`; e o `http://`
redirecionando (`308`) para `https://`.

### 9.5 Segredos e compose de produção (obrigações 4 e 5)

**Faça** (na VPS):

```bash
vc config --services
vc config | grep -nE 'mem_limit|GOMEMLIMIT|restart:'
vc exec app /stillon version
```

**Deve aparecer:** os serviços `caddy`, `app` e `db`, e **nenhum** `seed`; um `mem_limit` e um
`restart: unless-stopped` por serviço, e o `GOMEMLIMIT` no app; a versão igual ao
`/opt/stillon/versao`.

Nenhuma senha de produção está no repositório: o `.env` só existe na VPS e no gerenciador, e o
`gitleaks` do CI varre o histórico em todo PR.

### 9.6 Reboot de teste: tudo volta sozinho

**Antes** (na VPS), a linha de base: guarde a saída deste comando, que mostra as três pilhas (a
borda, o GoblinShift e o StillOn), no lugar de um `vc ps` mais um `docker compose ps` na pasta da
borda:

```bash
sudo docker ps --format '{{.Names}} {{.Status}}'
```

**Faça** (na VPS):

```bash
sudo reboot
```

A sessão SSH cai no `reboot` e o prompt volta para o seu computador: é o esperado. Espere dois
minutos e entre de novo, numa sessão nova (`ssh stillon`; se você deu `ssh-add -t 4h` no começo,
a chave pode ter vencido durante o bloco 9: dê o `ssh-add` outra vez). Repita o comando da linha
de base e compare: os mesmos contêineres, de volta (os com healthcheck `healthy`). Depois:

```bash
systemctl list-timers stillon-vigia.timer
sudo ufw status | head -1
sudo fail2ban-client status sshd | head -1
timedatectl | grep synchronized
```

**Deve aparecer:** os serviços do StillOn (`caddy`, `app`, `db`) e a borda de pé, como na linha de
base; o timer do vigia agendado; `Status:
active`; `Status for the jail: sshd`; `System clock synchronized: yes`. E, de fora,
`curl -sS -o /dev/null -w '%{http_code}\n' "https://$DOMINIO/readyz"` dá `200`, e
`curl -s -o /dev/null -w '%{http_code}\n' "http://$VPS/"` devolve a resposta do GoblinShift
(hoje `404`), nunca erro de conexão: o outro projeto da borda também voltou. O healthchecks
pode ter mandado um par de e-mails, **down** e **up** no minuto seguinte: é o custo do reinício
(passo 1.6, "Por quê"), e não uma queda.

### 9.7 Lista final

| Verificação | Passo |
|---|---|
| SSH só por chave, sem root; `fail2ban`; `ufw` e firewall do painel | 2.7–2.10 |
| Atualizações automáticas com reinício às 04:30 | 2.11 |
| Docker com rotação de log, `live-restore` e o store do containerd | 3.1, 3.2 |
| `.env` do root, `600`, sem variável vazia, copiado no gerenciador | 3.4 |
| Chave privada do `age` só no seu computador e no gerenciador | 3.5 |
| `deploy` preso ao script; túnel recusado | 4.4 |
| Primeiro deploy verde, papéis e dono das tabelas certos | 4.8 |
| A borda encaminha o domínio, com certificado | 4.9 |
| 2FA ligado e códigos de recuperação guardados | 5.2 |
| E-mail e incidente de teste chegaram | 6.1, 6.2 |
| Backup verde e restauração ensaiada | 7.1, 7.2 |
| Vigia externo avisou da queda de propósito | 8 |
| Portas, IP do cliente, `Host`, certificado, compose, reboot | 9.1–9.6 |

---

## Bloco 10: operação

### Os três comandos de todo dia (na VPS)

```bash
vc ps                               # quem está de pé e saudável
vc logs --since 1h app              # o log do app da última hora (troque app por caddy ou db)
vc logs -f --tail 50 app            # acompanhar ao vivo; Ctrl+C sai
```

O vigia: `sudo journalctl -u stillon-vigia.service --since today`. O deploy e o backup: o log
do próprio job no Actions (o script manda o log dele pela saída de erro, que o job mostra) e,
na VPS, o mesmo log em `/opt/stillon/entrada.log`, de todos os deploys e backups (`0644`, lido
sem `sudo`; acima de ~1 MiB ele vira `entrada.log.1`, que guarda a rodada anterior do log, e
recomeça): `tail -n 50 /opt/stillon/entrada.log`. É pela última linha
`== <UTC> fim: <pedido>: código N (<sentido>)`, que a entrada grava sempre por último, que você
sabe como terminou um deploy cuja conexão caiu (passo 4.8).

### Deploy seguinte

Actions → `ci` na `main` → espere o `publish` verde → Actions → `deploy` →
**Run workflow** na `main`, com o SHA completo do commit (passo 4.8). O script guarda a versão que
estava no ar em `/opt/stillon/versao-anterior` e mantém as 3 últimas imagens. Durante a troca há
alguns segundos de `502` (o painel mostra "Resposta inesperada do servidor (502)"; recarregue).

Os três arquivos de versão, na VPS, são legíveis sem `sudo` (`0644`: não são segredo):
`/opt/stillon/versao` (o SHA no ar), `/opt/stillon/versao-anterior` (o que estava antes) e
`/opt/stillon/historico` (um SHA por deploy saudável).

### Voltar uma versão

Rode o mesmo workflow com o SHA anterior (`cat /opt/stillon/versao-anterior` na VPS). Só é
seguro se as migrations da versão nova forem **aditivas**: o `goose` não roda `Down` sozinho, e o
rollback automático do script também não desfaz migration.

**Se sair 3 com `stillon:<sha> já existe com outra imagem`:** uma tag `v*` publicada depois
daquele deploy **moveu** a tag `:<sha>` do GHCR para o digest do build da tag (mesmo código, outro
build, reescaneado; `ci.yml`), e o workflow leva esse digest novo. A VPS ainda guarda a imagem
antiga daquele SHA e **recusa** trocá-la, porque é exatamente a imagem para onde um rollback
voltaria: ela só recusa assim o SHA que está no ar (`versao`) e o anterior (`versao-anterior`);
para qualquer outro SHA, a etiqueta velha é trocada e o deploy segue. **Rodar de novo não
resolve** (sai 3 outra vez), e não é adulteração. Dois caminhos:

1. **Sem pressa (o recomendado):** um commit novo na `main` com o código da versão boa (um
   `git revert` dos commits da versão ruim, por PR, como sempre), e o deploy do SHA **desse
   commit** quando o `ci` dele ficar verde. É um SHA que a VPS nunca viu: nada a recusar.
2. **Com pressa, à mão na VPS**, com a imagem que já esteve no ar e ainda está lá. Primeiro,
   confira que ela existe (sem trava nenhuma, só leitura):
   ```bash
   ANT=$(cat /opt/stillon/versao-anterior)
   ATUAL=$(cat /opt/stillon/versao)
   echo "no ar: $ATUAL; voltar para: $ANT"
   sudo docker image inspect -f '{{.Id}}' "stillon:$ANT"
   ```
   **Deve aparecer:** os dois SHAs e um `sha256:…`. Se disser `No such image`, a imagem já foi
   podada: só o caminho 1 serve. Então troque, sob a mesma trava do deploy e do backup (se um
   deles estiver rodando, o comando espera até 10 min):
   ```bash
   sudo ANT="$ANT" ATUAL="$ATUAL" flock -w 600 /opt/stillon/.entrada.trava sh -ec '
     docker tag "stillon:$ANT" stillon:atual
     docker compose -f /opt/stillon/compose.prod.yaml up -d --force-recreate --wait --wait-timeout 90 app
     test "$(docker inspect -f "{{.State.Health.Status}}" "$(docker compose -f /opt/stillon/compose.prod.yaml ps -q app)")" = healthy
     echo "$ATUAL" > /opt/stillon/versao-anterior
     echo "$ANT" > /opt/stillon/versao
     echo "no ar: $ANT"
   '
   ```
   **Deve aparecer:** o `app` `Healthy` e `no ar: <o SHA anterior>`; `cat /opt/stillon/versao`
   mostra o mesmo. Os dois arquivos de versão são atualizados à mão porque o próximo deploy os
   usa para saber para onde voltar se ele mesmo falhar. Se o `up` não ficar saudável, o comando
   para antes de mexer neles (o `test` confere a saúde pelo próprio Docker: até a v5.5.0, o
   `docker compose up --wait` pode sair 0 com o app ainda em `starting` quando o prazo vence); volte para a versão que estava no ar com
   `sudo docker tag "stillon:$ATUAL" stillon:atual` e `vc up -d --force-recreate app`, e siga
   pelo caminho 1.

Refazer o deploy do SHA que **já está no ar** depois de um `v*` também sai 3 com essa mensagem:
não há o que fazer, a versão no ar é o mesmo código.

### Depuração do deploy

Os códigos de saída do `stillon-entrada deploy`, mais o 255 do próprio SSH. O job termina com um
resumo do código (`deploy/ci/vps.sh`), mas a razão exata é a linha que começa com
`stillon-entrada:` no log do job (o código 0 é a exceção: a linha dele é só
`deploy de <sha> saudável`, sem o prefixo). Procure-a na coluna do meio. Na VPS, o
`/opt/stillon/entrada.log` traz a mesma linha logo acima da última, a
`== <UTC> fim: <pedido>: código N (<sentido>)`, que dá o código; os pedidos recusados antes da
trava (a primeira linha de código 2 da tabela e o 1 da trava ocupada) não chegam ao
`entrada.log`, só ao log do job.

| Código | A linha diz (com o prefixo `stillon-entrada:`, menos no 0) | O que fazer |
|---|---|---|
| 0 | `deploy de <sha> saudável` (sem prefixo) | nada |
| 2 | `pedido vazio`, `caractere não permitido no pedido`, `verbo desconhecido`, `deploy leva o SHA e o image-id`, `SHA malformado`, `image-id malformado` ou `argumento na linha de comando não é aceito` | conferir o SHA passado ao workflow (os 40 caracteres do commit, passo 4.8) |
| 2 | `nada chegou na entrada padrão` | o tar não chegou: veja no job se o passo que baixa a imagem falhou; rode de novo |
| 2 | `a imagem passa de 2147483648 bytes` | a imagem normal tem dezenas de MB: o `publish` produziu algo errado; não force, investigue |
| 3 | `stillon:<sha> já existe com outra imagem (…)` | só com o SHA no ar ou o anterior, depois de um `v*`: **rodar de novo não resolve**; veja "Voltar uma versão" acima |
| 3 | `a imagem carregada é …, e o deploy pediu …`, `o index.json do tar não é uma imagem só, sem nome, com o manifesto …`, `o tar não trouxe exatamente uma imagem sem nome`, `o que chegou não é um tar legível`, ou outra que começa com `o tar` (layout OCI, membro repetido ou estranho; as duas de disco cheio são código 1) | rodar de novo **uma vez**; se repetir, a imagem mudou no caminho (ou o formato do `crane` mudou): não force, investigue |
| 4 | `rollback feito: <anterior> de volta no ar; <sha> não subiu` | a anterior já voltou: o log do job traz o log do app; corrija e publique outra versão |
| 4 | `primeiro deploy e sem versão anterior: o app ficou parado (e removido)` | **o site está fora** (não há anterior): passo 4.8, "Se o primeiro deploy sair com 4" |
| 1 | `primeiro deploy e sem versão anterior, e o app não saiu do ar depois de 3 tentativas: veja docs/deploy/vps.md` | **o site está fora**, e o app doente segue de pé: passo 4.8, "Se o primeiro deploy sair com 4" |
| 1 | `o rollback para <anterior> também não ficou saudável: veja docs/deploy/vps.md` | **o site está fora**: "O rollback também não ficou saudável", logo depois desta seção |
| 1 | `a imagem de <anterior> (stillon:<anterior>) sumiu: o rollback não foi tentado; veja docs/deploy/vps.md` | **o site está fora**, e a imagem da versão que estava no ar não está mais na VPS: rode o workflow com o SHA de `cat /opt/stillon/versao` (passo 4.8), que a manda de novo |
| 1 | `o jq não está instalado (…): veja docs/deploy/vps.md` | `sudo apt install -y jq` (passo 3.3) e rode de novo |
| 1 | `o Docker não usa o image store containerd; veja docs/deploy/vps.md (Docker)` | `sudo docker info -f '{{.DriverStatus}}'` e o `daemon.json` do passo 3.2 |
| 1 | `outra operação da entrada não terminou em 10 min` | um backup (ou outro deploy) segurou a trava: veja o `entrada.log`, espere terminar e rode de novo |
| 1 | `o tar não se extraiu …` ou `o tar refeito não se gravou …` (`disco cheio?`) | disco: `df -h /opt/stillon /var/lib/docker` (veja "Operação do Postgres", linha Disco) |
| 1 | outra | a mensagem diz qual (Docker parado: `sudo systemctl status docker`) |
| 255 | (nenhuma: o SSH não chegou ao script, ou a conexão caiu no meio) | se o job caiu **depois** de mandar a imagem, o deploy pode ter terminado na VPS: confira `cat /opt/stillon/versao` e a última linha `fim:` do `entrada.log` antes de repetir (passo 4.8). Se nem conectou: VPS fora do ar, porta 22 fechada, chave do servidor diferente do `VPS_HOST_KEY` (VPS reinstalada?) ou chave do deploy recusada; `ssh stillon true` do seu computador; se a VPS foi reinstalada, passos 4.2 a 4.6 de novo |

O `backup` usa os mesmos códigos do `deploy/backup/backup.sh`: 0, cópia restaurada e conferida;
3, uma checagem da cópia falhou (o dump não serve, e nada é guardado); 4, conferido e guardado,
mas o disco passou de 85% (o job fica vermelho no fim, de propósito); 1, outro erro (não houve
backup), inclusive `outra operação da entrada não terminou em 10 min` (um deploy segurou a
trava: rode de novo); 130/143, interrupção por sinal; 255, o SSH não chegou ao script.

| Mensagem do backup (código 1) | Confira |
|---|---|
| `falta a versão aprovada` ou `imagem local da versão aprovada ausente` | `cat /opt/stillon/versao` e `sudo docker image inspect stillon:<SHA> --format '{{.Id}}'`; a imagem deve ser a do deploy aprovado, não uma tag arbitrária |
| `imagem aprovada não oferece o protocolo` ou `protocolo incompatível` | imagem, entrada e scripts precisam ser da mesma versão; use o probe do passo 7.1 |
| `não foi possível copiar` ou `instalar o helper` | confira espaço e o erro Docker no `entrada.log`; a entrada não conecta à origem sem cópia e probe válidos |
| `snapshot ou dump não concluído` | consulte o `entrada.log`; timeout, perda da origem ou falha de encerramento não publicam manifesto nem tornam o dump válido |

O ID fixa os bytes da imagem local durante a cópia; não é uma assinatura de procedência.
A confiança continua dependendo do deploy aprovado e do controle root do Docker e dos
arquivos em `/opt/stillon`. Não substitua a tag de SHA por uma imagem de terceiros.

A imagem do app **não tem shell** (distroless): `vc exec app sh` não funciona. O que serve: os
logs, os subcomandos do binário (`vc exec app /stillon version`, `… healthcheck`, `… user …`) e o
`psql` do banco (`vc exec db psql -U postgres -d stillon`). O `stillon healthcheck` consulta o
`/readyz` com prazo de 3 s:

| Código | Quando |
|---|---|
| 0 | o `/readyz` respondeu 200 dentro dos 3 s |
| 1 | qualquer outra resposta, prazo esgotado, conexão recusada ou `HTTP_ADDR` inválido |
| 2 | argumento a mais (`stillon healthcheck x`); o `stillon version` faz o mesmo |
| 130 | interrompido (Ctrl-C ou SIGTERM) |

Uma `PUBLIC_BASE_URL` com usuário e senha (`https://usuario:senha@…`) é recusada no boot: o app
não sobe, e o log diz "PUBLIC_BASE_URL não pode trazer credenciais na URL (a parte com @ antes do
host)", sem repetir o valor.

### O rollback também não ficou saudável

A mensagem é `o rollback para <anterior> também não ficou saudável: veja docs/deploy/vps.md`,
com código 1. **O site está fora do ar**: nem a versão nova nem a anterior ficaram saudáveis. Como
a anterior estava bem minutos antes, a causa quase sempre é comum às duas, e não o código: o
banco, o disco ou o `.env`. Na VPS:

```bash
vc ps -a
vc logs --tail 100 app db
df -h /opt/stillon /var/lib/docker
```

- `db` fora de `healthy`, ou erro de conexão/senha no log do app: o banco (veja o log do `db`; um
  `vc up -d db` o sobe de novo se ele tiver parado).
- Disco a 100%: libere espaço (linha Disco de "Operação do Postgres").
- Erro de variável no boot do app: o `.env` mudou (`sudo nano /opt/stillon/.env`, compare com o
  gerenciador).

Corrigida a causa, suba a última versão saudável (a etiqueta `stillon:atual`, que só anda num
deploy saudável):

```bash
vc up -d --wait app
cat /opt/stillon/versao
```

O `app` `Healthy`, e o `versao` é o SHA dela. Se não ficar saudável, o log do `vc logs --tail 100
app` diz o que ainda falta. Só depois disso tente de novo o deploy da versão nova.

### Arquivos da VPS: quando algo em `deploy/vps/` muda na `main`

O deploy troca só a imagem do app. Os arquivos abaixo foram copiados à mão (passos 3.3, 3.5, 4.1 e
8) e **não** se atualizam sozinhos. Quando um PR mudar um deles, recopie o que mudou do mesmo
jeito: no seu computador, na `main` atualizada, o `scp` para `stillon:stillon-arquivos/`; na
VPS, o `sudo install` para o destino da tabela, com o modo dela e dono `root`; e aplique:

| Arquivo do repositório | Destino na VPS | Modo | Para valer |
|---|---|---|---|
| `deploy/vps/compose.prod.yaml` | `/opt/stillon/compose.prod.yaml` | `0644` | `vc up -d` |
| `deploy/vps/Caddyfile` | `/opt/stillon/Caddyfile` | `0644` | `vc up -d --force-recreate caddy` e a conferência abaixo |
| `deploy/vps/initdb.sh` | `/opt/stillon/initdb.sh` | `0755` | nada: só roda num banco **vazio**, e trocá-lo não muda um banco que já existe |
| `deploy/vps/edge/stillon.caddy` | `/srv/edge/conf.d/stillon.caddy` | `0644` | `caddy validate` e `caddy reload` na borda, em `/srv/edge` (passo 4.9) |
| `deploy/vps/env.exemplo` | nenhum: é o modelo do `/opt/stillon/.env` | — | variável nova no modelo: acrescente-a ao `.env` (`sudo nano /opt/stillon/.env`) e ao gerenciador; variável que saiu: apague-a do `.env` |
| `deploy/vps/stillon-entrada` | `/usr/local/sbin/stillon-entrada` | `0755` | nada: vale no próximo deploy ou backup |
| `deploy/vps/sudoers-stillon-entrada` | `/etc/sudoers.d/stillon-entrada` | `0440` | `sudo visudo -cf` **antes** do `install` e `sudo visudo -c` depois (passo 4.1) |
| `deploy/vps/vigia.sh` | `/usr/local/sbin/stillon-vigia` | `0755` | nada: vale na próxima execução do timer |
| `deploy/vps/stillon-vigia.service` e `stillon-vigia.timer` | `/etc/systemd/system/` | `0644` | `sudo systemctl daemon-reload && sudo systemctl restart stillon-vigia.timer` |
| `deploy/vps/vigia.env.exemplo` | nenhum: é o modelo do `/opt/stillon/vigia.env` | — | variável nova no modelo: acrescente-a ao `vigia.env` (`sudo nano /opt/stillon/vigia.env`) |
| `deploy/backup/backup.sh` e `deploy/backup/verificar.sql` | `/opt/stillon/deploy/backup/` | `0644` | nada: vale no próximo backup |
| `deploy/compose/initdb.sql` | `/opt/stillon/deploy/compose/` | `0644` | nada: vale no próximo backup |

Quando mudar o protocolo do backup, publique a imagem e recopie `stillon-entrada` e
`backup.sh` da mesma revisão; confira o probe do passo 7.1. A imagem fornece o helper
automaticamente, sem quarto arquivo de backup para instalar.

Os pacotes do host que o `stillon-entrada` e o vigia usam (`age`, `curl`, `jq`, `util-linux`,
passo 3.3) vêm do Ubuntu e se atualizam sozinhos (passo 2.11). Se um PR passar a exigir um pacote
novo, a descrição dele diz qual: `sudo apt install -y <pacote>` antes de recopiar o script.

**Por que o Caddy precisa de `--force-recreate`:** o `Caddyfile` entra no contêiner como um
arquivo só (`./Caddyfile:/etc/caddy/Caddyfile:ro`). O `sudo install` põe um arquivo **novo** no
lugar do velho, e o contêiner que já está de pé continua vendo o velho; o `vc up -d` sozinho não
recria um serviço cujo compose não mudou. Sem recriar, uma correção de segurança no `Caddyfile`
fica instalada no disco e sem efeito nenhum. **Confira** depois de todo `Caddyfile` novo (na VPS):

```bash
vc up -d --force-recreate caddy
vc exec -T caddy cat /etc/caddy/Caddyfile | sudo diff - /opt/stillon/Caddyfile && echo "Caddy com o arquivo novo"
```

`Caddy com o arquivo novo`, e nenhuma linha de diferença. Recriar o Caddy derruba o site por um
ou dois segundos.

Para ver o que mudou desde a última cópia: no seu computador,
`git log --oneline -- deploy/vps/ deploy/backup/ deploy/compose/initdb.sql` e
`git diff <commit da última cópia> main -- deploy/vps/ deploy/backup/ deploy/compose/initdb.sql`.
Anote no gerenciador o commit de cada cópia (`git rev-parse HEAD` na hora do `scp`): é o
`<commit da última cópia>` da próxima vez.

### Atualizações do sistema e do Docker

As de segurança do Ubuntu entram sozinhas (passo 2.11). **Uma vez por mês**, na VPS:

```bash
sudo apt update && sudo apt full-upgrade
ls /var/run/reboot-required 2>/dev/null && echo "precisa reiniciar"
```

Isso atualiza também o Docker (que o automático não toca). Com o `live-restore`, os contêineres
seguem de pé durante a atualização do Docker. Se pedir reinício, reinicie numa hora calma (ou
deixe para as 04:30).

### Perdeu o autenticador e os códigos de recuperação

Sem o app **e** sem os códigos, o login para no segundo fator. Na VPS:

```bash
vc exec app /stillon user reset-2fa --email voce@exemplo.com
```

Ele desliga o 2FA, apaga os códigos de recuperação, zera o bloqueio da conta e encerra todas as
sessões, numa transação só. Não pede senha: quem tem a `DATABASE_URL` (o contêiner) já pode tudo
no banco. A saída diz o e-mail e quantas sessões caíram ("2FA desativado para …; N sessões
encerradas. Acessos já abertos expiram em até 15 minutos."); com o 2FA já desligado, diz "nada
mudou" e sai com 0, **sem** mexer no bloqueio (para destravar uma conta sem 2FA, use o
`set-password`). Código 4 é e-mail não cadastrado; 2, uso errado; 1, banco ou papel. Depois,
entre só com a senha e ative o 2FA de novo (passo 5.2).

Quem ainda tem o app ou um código de recuperação não precisa disto: **Configurações → Trocar
autenticador** troca de celular sem desligar o 2FA.

Esqueceu a senha: `vc exec app /stillon user set-password --email voce@exemplo.com` (pede a
senha nova duas vezes, sem eco; encerra todas as sessões).

### Perdi a chave SSH, ou fui bloqueado

1. No painel, **Overview → Web console** (passo 2.7). Se o `fail2ban` bloqueou o seu IP:
   `sudo fail2ban-client set sshd unbanip <seu IP>` (o seu IP: <https://ifconfig.me> no
   navegador).
2. Chave perdida: gere outra (passo 1.1) e, pelo terminal do painel, ponha a `.pub` nova em
   `/home/<você>/.ssh/authorized_keys` (apague a linha da velha). Uma linha longa no terminal do
   navegador é chata de colar: alternativa é adicioná-la em **Settings → SSH keys** (vai para o
   root) e, pelo terminal do painel, `sudo cp /root/.ssh/authorized_keys /home/<você>/.ssh/` e
   `sudo chown <você>: /home/<você>/.ssh/authorized_keys`.
3. Se nem o terminal do painel abrir: **Settings → Emergency mode** (fonte no passo 2.7), que dá
   acesso ao disco para consertar o `sshd_config.d` ou o `authorized_keys`.

### Operação do Postgres

| Responsabilidade | Mecanismo | Frequência |
|---|---|---|
| Backup | O `backup` diário do Actions (dump conferido, cifrado, artifact de 30 dias) + o backup semanal da Hostinger (a VPS inteira) | Automático: conferir que o `backup` segue verde. Vermelho **ou** *skipped* (cinza) é falha |
| Ensaio de restauração | Automático em cada `backup`; à mão a cada trimestre (passo 7.2) | Diário e trimestral |
| Cópia local | Baixe um artifact por mês para o seu computador (ou um disco externo): os do GitHub somem em 30 dias | Mensal |
| Disco | O `backup` avisa acima de 70% e **falha** acima de 85% (o dump do dia ainda é guardado; o job fica vermelho no fim). O disco é o da VPS: `df -h /var/lib/docker` na VPS. Para liberar: logs e imagens velhas (`sudo docker image prune` só depois de conferir `sudo docker image ls`; o script já guarda só 3 versões do app); se for o banco crescendo, o plano maior da Hostinger | Quando avisar |
| Upgrade menor (18.x) | PR do Dependabot em `deploy/vps/db/Dockerfile`. O mesmo digest está em mais lugares, que sobem juntos no mesmo PR (`deploy/vps/db/Dockerfile`, `compose.yaml`, `.github/workflows/ci.yml`, `internal/storage/testdb` e `deploy/vps/compose.prod.yaml`; o Dependabot já muda o do compose de produção junto). Depois do merge, recopie o `compose.prod.yaml` (seção acima) e `vc up -d db` (10–30 s fora). O backup passa a usar a imagem nova sozinho | Quando o Dependabot abrir |
| Upgrade maior (18 → 19) | **Não** vem por PR: o `dependabot.yml` ignora versão maior de `postgres`. Dump e restauração num volume novo (abaixo) | ~1 vez por ano |

#### Upgrade maior do Postgres

Numa janela calma, com o backup do dia verde:

1. Rode o `backup` à mão (passo 7.1) e **ensaie a restauração** com a imagem nova (passo 7.2,
   trocando a imagem do `docker run` pela do 19): prova que o dump do 18 sobe no 19.
2. Na VPS, pare o app e faça um dump fresco (fica em `/root`, que só o root lê, e só durante a
   janela: a VPS já guarda o banco inteiro em claro, então o arquivo não aumenta a exposição):
   ```bash
   sudo -v
   vc stop app
   vc exec -T db pg_dump -U postgres --format=custom stillon | sudo tee /root/stillon-antes-do-19.dump >/dev/null
   sudo ls -l /root/stillon-antes-do-19.dump
   ```
3. Por PR, a imagem nova em `deploy/vps/db/Dockerfile` e nos outros lugares do digest, e o
   `compose.prod.yaml` apontando o `db` para um **volume novo** (hoje o volume se chama `pgdata`, `stillon_pgdata` no `docker volume ls`: montado
   como `pgdata:/var/lib/postgresql` no serviço `db` e declarado no bloco `volumes:` do fim do
   arquivo; o novo, por exemplo `pgdata19`, troca o nome nos dois lugares), para o volume velho continuar
   intacto. Recopie o `compose.prod.yaml` e suba **só o banco**: `vc up -d db` (o `initdb.sh`
   cria os papéis no volume novo com as senhas do `.env`).
4. Restaure no banco novo como superusuário, preservando os donos, e apague o dump:
   ```bash
   sudo -v
   sudo cat /root/stillon-antes-do-19.dump | vc exec -T db pg_restore -U postgres --exit-on-error -d stillon
   sudo shred -u /root/stillon-antes-do-19.dump
   ```
5. `vc up -d app`, confira o passo 4.8 (dono das tabelas, papéis) e rode o `backup` à mão.
6. Mantenha o volume velho por 7 dias antes de apagá-lo (`sudo docker volume rm <volume velho>`).

### A VPS morreu (ou apaguei um alvo)

**Apaguei um alvo, um canal, uma status page:** não restaure a produção inteira por isso.
Restaure o backup de ontem numa cópia local (passo 7.2), olhe a configuração perdida no painel da
cópia (`http://localhost:8080`) e recrie à mão na produção.

**A VPS morreu** (não liga, disco corrompido, conta suspensa):

1. Primeiro o mais barato: **Snapshots & Backups** da Hostinger (passo 7.3). Restaurar o backup
   semanal volta a VPS inteira à semana passada; depois disso, siga do item 4 abaixo com o dump de
   ontem para recuperar a semana.
2. Se não houver backup da Hostinger que sirva: reinstale o Ubuntu 24.04 (ou crie outra VPS),
   refaça o bloco 2 e, **antes do bloco 3**, o provisionamento do GoblinShift (o `provision.sh`
   dele instala o Docker e a borda em `/srv/edge`, com a rede `edge`; sem ela, o 4.8 falha com
   "network edge declared as external, but could not be found"). Depois, o bloco 3 e o bloco 4
   **até o 4.7** (o 4.8 e o 4.9 são o item 3 abaixo). Nos passos
   que guardam segredo, **não gere nada novo; restaure do gerenciador**:
   - **3.4:** pule o bloco que gera os segredos. Abra o `.env` (`sudo nano /opt/stillon/.env`) e
     cole **todos** os valores do gerenciador (a `MASTER_KEY`, as três senhas do Postgres, tudo).
     Com outra `MASTER_KEY`, o banco restaurado teria headers e canais ilegíveis; com outras
     senhas, os papéis não bateriam com o dump.
   - **3.5:** **não** rode o `age-keygen -o`: uma chave nova não abre os dumps que já existem, e o
     item 4 falharia. Se o `~/stillon-backup.key` ainda existe no seu computador, use-o; se não
     (o computador também foi trocado), recrie-o com o conteúdo da entrada "StillOn – chave
     privada do backup (age)". Crie o arquivo **vazio e já `600`** antes de abrir o editor, para
     a chave nunca ficar num arquivo que outro usuário leia, e cole nele:
     ```bash
     install -m 600 /dev/null ~/stillon-backup.key
     nano ~/stillon-backup.key
     stat -c '%a' ~/stillon-backup.key
     ```
     (cole, `Ctrl+O`, Enter, `Ctrl+X`; o `stat` tem de dizer `600`). Depois, só a pública, a
     partir dela:
     `age-keygen -y ~/stillon-backup.key > ~/stillon-age-recipient.txt`, e siga o 3.5 do
     "Leve a pública para a VPS" em diante (o `echo "ida e volta"` confere o par).

   O IP pode mudar: atualize o registro `A` (passo
   1.4), o `~/.stillon-vps`, o `HostName` do `~/.ssh/config` (editando a linha, sem acrescentar
   outro `Host stillon`: passo 2.6), e grave de novo `VPS_HOST` e `VPS_HOST_KEY`
   (passos 4.5 e 4.6; a VPS nova tem outra chave de servidor, e o `ssh` do seu computador vai
   reclamar dela: `ssh-keygen -R <IP velho>` e confira a nova pelo terminal do painel).
3. Faça o primeiro deploy (passo 4.8) com o SHA que estava no ar (o último deploy verde do Actions)
   e ponha o StillOn na borda (passo 4.9).
4. Restaure o último dump do Actions no banco novo: pare o app (`vc stop app`), decifre o dump no
   seu computador (passo 7.2, itens 1 e 2), leve-o para a VPS e apague a cópia em claro do seu
   computador:
   ```bash
   scp ~/stillon-restauro/stillon.dump stillon:
   shred -u ~/stillon-restauro/stillon.dump
   ```
   Depois, na VPS:
   ```bash
   vc exec -T db dropdb -U postgres stillon
   vc exec -T db psql -U postgres -c "CREATE DATABASE stillon OWNER stillon_app;"
   vc exec -T db pg_restore -U postgres --exit-on-error -d stillon < ~/stillon.dump
   shred -u ~/stillon.dump
   vc start app
   ```
   (O banco é recriado vazio porque o app já migrou um schema no primeiro deploy.)
5. Refaça o **bloco 8** inteiro (o vigia, com o `vigia.env`: a `HC_URL` vem do gerenciador,
   entrada "StillOn – HC_URL"), até o healthchecks voltar a **up**. Sem ele, a VPS nova fica sem
   vigia justamente depois de um desastre, e o `down` do healthchecks parece ser só a
   reconstrução. Não pause nem silencie o check no healthchecks: ele volta a **up** sozinho quando
   o vigia novo pingar.
6. Confira os passos 4.8 (tabelas e papéis), 4.9 (o domínio pela borda), 5.2 (entrar com 2FA),
   6.1 (e-mail) e rode o
   `backup` à mão (passo 7.1). Perde-se no máximo o que aconteceu desde o dump de ontem (checks e
   incidentes).

### Trocar um segredo

- `JWT_SECRET`: gere outro (`openssl rand -base64 32`, no `.env` pelo `nano`), guarde no
  gerenciador, `vc up -d app`. Todas as sessões caem; entra-se de novo.
- `IP_HASH_KEY`: o mesmo caminho; os hashes de IP antigos da auditoria deixam de casar com os
  novos.
- `RESEND_API_KEY`: no painel do Resend (**API Keys**), crie uma chave nova e apague a velha;
  cole a nova no `.env` (`sudo nano /opt/stillon/.env`) e no gerenciador; `vc up -d app`.
- `HC_URL` (um check novo no healthchecks, ou a URL vazou): troque-a no `vigia.env`
  (`sudo nano /opt/stillon/vigia.env`) e no gerenciador. Vale na próxima execução do timer, sem
  reiniciar nada.
- `STILLON_APP_PASSWORD` (a senha do papel do app): logo abaixo, "A senha do app".
- `MASTER_KEY`, `POSTGRES_PASSWORD` e `STILLON_BACKUP_PASSWORD`: **não** há rotação pronta. A
  `MASTER_KEY` cifra os segredos gravados no banco (trocá-la exige decifrar e cifrar de novo
  tudo); as duas senhas o app nem vê (o compose não as entrega a ele). Se uma delas vazar, é
  trabalho de plano próprio; até lá, troque na origem o que a `MASTER_KEY` protege (item 6 da
  lista abaixo).
- A chave de deploy do GitHub, numa troca **de rotina** (sem suspeita de vazamento): passos 4.3
  a 4.6 com uma chave nova (a linha antiga do `authorized_keys` é substituída).

#### A senha do app (`STILLON_APP_PASSWORD`)

A senha do papel do app vive em dois lugares, no `.env` e no banco, e os dois mudam juntos. Na
VPS, cole o bloco **inteiro de uma vez**; como no 3.4, a senha nasce e é gravada dentro de um
processo do root, sem passar pela tela nem pelo `ps`:

```bash
sudo sh -s <<'FIM'
set -eu
umask 077
cd /opt/stillon
NOVA=$(openssl rand -hex 32)
printf "ALTER ROLE stillon_app PASSWORD '%s';\n" "$NOVA" \
  | docker compose -f compose.prod.yaml exec -T db psql -U postgres -v ON_ERROR_STOP=1 -q
VALOR=$NOVA awk '
  /^STILLON_APP_PASSWORD=/ { print "STILLON_APP_PASSWORD=" ENVIRON["VALOR"]; next }
  { print }
' .env > .env.novo
cat .env.novo > .env
rm .env.novo
echo "senha do app trocada no banco e no .env"
FIM
```

**Deve aparecer:** só `senha do app trocada no banco e no .env` (se o `psql` falhar, o bloco
para antes de mexer no `.env`, e nada mudou). Rode **logo em seguida** `vc up -d --wait app`: o
app que está de pé ainda usa a senha velha, e cada conexão nova dele ao banco falha até a troca.
Depois, copie a senha nova para o gerenciador: `sudo grep '^STILLON_APP_PASSWORD='
/opt/stillon/.env` e `clear`. A senha velha do gerenciador não serve mais: numa reconstrução
("A VPS morreu"), o `initdb.sh` cria o papel com a do `.env`, e a do gerenciador tem de ser a
mesma.

#### A chave de deploy vazou (ou você suspeita)

**Não é uma troca simples.** A chave só roda o `stillon-entrada`, e ele não dá root na VPS nem
acesso ao `.env`. Mas ela faz **deploy de qualquer imagem** como o app: quem a tem sobe um
programa seu no lugar do StillOn, e esse programa vê tudo o que o app vê. Isso é o app
**inteiro** e os segredos que ele recebe: a `MASTER_KEY`, o `JWT_SECRET`, o `IP_HASH_KEY`, a
senha do `stillon_app` (dona das tabelas: lê e muda qualquer dado), a `RESEND_API_KEY`, e, por
meio da `MASTER_KEY`, os headers dos alvos e as configurações dos canais (webhooks do Discord,
tokens) guardados cifrados. O `image-id` do pedido não protege contra isso: quem tem a chave
escolhe o ID. (O backup pela chave só entrega o dump **cifrado**: sem a sua chave privada do
`age`, não serve para nada.) Na ordem, **na VPS**:

1. **Corte o acesso**, antes de tudo: esvazie o `authorized_keys` do `deploy`. Daqui em diante
   nenhum deploy nem backup entra (o backup diário fica vermelho até o item 5; é o esperado).
   ```bash
   sudo truncate -s 0 /home/deploy/.ssh/authorized_keys
   sudo wc -c /home/deploy/.ssh/authorized_keys
   ```
   `0 /home/deploy/.ssh/authorized_keys`.
2. **Tire do ar o que pode ser dele** e veja o que aconteceu:
   ```bash
   vc rm -sf app
   tail -n 200 /opt/stillon/entrada.log
   cat /opt/stillon/entrada.log.1
   cat /opt/stillon/historico
   sudo docker image ls stillon
   ```
   O site fica fora (`502`) até o item 7. (O `entrada.log.1` só existe se o log já passou do teto
   de ~1 MiB; se não existir, o `cat` diz `No such file or directory`, e tudo bem.) Compare cada
   `deploy de <sha>` do `entrada.log` e do `entrada.log.1`, e cada SHA do `historico` com as
   execuções do `deploy` na aba Actions
   (`gh run list --repo BipoXDXD/stillon --workflow deploy.yml --limit 20`): um deploy que não
   saiu de lá foi de outra pessoa. Anote a data do primeiro.
3. **Apague todas as imagens `stillon` locais**, para nenhuma etiqueta apontar para uma imagem
   dele (o próximo deploy baixa a certa do GHCR):
   ```bash
   sudo docker image ls stillon --format '{{.Repository}}:{{.Tag}}' | xargs -r sudo docker rmi
   sudo docker image ls stillon
   ```
   A segunda linha mostra só os títulos, sem imagem nenhuma.
4. **Troque os segredos que o app vê**, com o app parado (itens acima): o `JWT_SECRET`, o
   `IP_HASH_KEY`, a `RESEND_API_KEY` e a `STILLON_APP_PASSWORD` ("A senha do app": pule o
   `vc up -d --wait app`: quem sobe o app é o deploy do item 7). O `db` continua de pé.
5. **Uma chave de deploy nova:** passos 4.3 a 4.6 (o 4.4 grava a linha nova no
   `authorized_keys` vazio; o 4.6 troca o secret `VPS_SSH_KEY`), e apague a chave do seu
   computador, como lá.
6. **Troque na origem o que a `MASTER_KEY` protege**, porque um programa dele pôde ler tudo
   decifrado: crie webhooks novos do Discord (e apague os velhos no Discord), gere tokens novos
   para os headers dos seus alvos, e atualize-os no painel depois do item 7. A `MASTER_KEY` em si
   continua a mesma (não há rotação pronta; ela é só uma chave: o que importa é o que ela abria).
   Troque também a sua senha do painel (`vc exec app /stillon user set-password …`, depois do
   item 7) e o autenticador (**Configurações → Trocar autenticador**): o segredo do 2FA também
   estava no banco.
7. **O deploy de um SHA conhecido**: o último verde da `main` que **você** conferiu (passo 4.8,
   item 1), pelo workflow. Sem imagem local nenhuma, não há o que recusar. Se o deploy sair com 1
   (`o rollback para … também não ficou saudável`), é porque a versão anterior não existe mais
   (o item 3 a apagou): olhe o log do job como no 4.8 e corrija.
8. **Os dados:** se o item 2 mostrou deploy de outra pessoa, o banco pode ter sido mudado por
   ela. Restaure uma cópia do backup **anterior à data anotada** no seu computador (passo 7.2) e
   compare no painel dela com a produção (usuários, alvos, canais). Se algo foi mexido, restaure
   esse backup na produção ("A VPS morreu", item 4), perdendo o que veio depois dele.

### Minutos de Actions

Com o repositório privado, o CI gasta a cota gratuita (2.000 min/mês na conta pessoal): ~15 min de
runner por push somando os jobs, dos quais o `e2e` é o maior, e alguns minutos por dia do
`backup`. Um push novo no mesmo branch cancela a execução anterior. Se a cota apertar, o primeiro
corte é o `e2e` só na `main` (`if: github.event_name == 'push'` no job).

## Tornar o repositório público (passo futuro)

> **Atualização (2026-10-14):** o repositório **já ficou público**, e os itens 1 a 8 abaixo valem
> como lista do que isso mudou. A decisão do dono sobre o item 7: **os artifacts do backup ficam
> públicos**, porque são cifrados com o `age` e a confidencialidade depende só da chave privada,
> que só o dono tem. Custo aceito: tamanho, data e frequência do dump, e as contagens do log do
> `backup` (versão do schema, usuários, alvos, disco), ficam à vista de qualquer conta do GitHub.
> Alternativas, se isso incomodar: voltar o repositório para privado (com custo de minutos de
> Actions) ou reduzir o `retention-days`. O pacote do GHCR tem visibilidade própria e continua
> **privado** (item 4): por isso o `docker login` do 7.2 segue necessário.

O repositório era **privado por decisão do dono**. No dia de abrir:

1. Varredura do histórico inteiro: `gitleaks git . --redact -v` (o job `secrets` do CI já faz
   isso em todo PR). O falso positivo conhecido está no `.gitleaksignore`, com o motivo.
2. **Se aparecer um segredo real:** considere-o vazado e **rotacione primeiro** (Resend, Discord,
   o que for). Depois, tire-o do histórico com `git filter-repo --replace-text` num clone novo,
   `git push --force` de todos os branches e tags, e rode a varredura de novo até zerar.
   Reescrever a história não desfaz um vazamento.
3. O e-mail pessoal do autor está no histórico e **fica** (decisão do dono).
4. Settings → General → Change visibility. O pacote do GHCR tem visibilidade própria: Package
   settings → Change visibility → Public. Confira de uma máquina sem login:
   `docker pull ghcr.io/bipoxdxd/stillon:latest`.
5. No README, troque o parágrafo "enquanto o repositório for privado" pelo dos 30 segundos: o
   `docker compose up` passa a puxar a imagem em vez de construir, sem mudar o compose.
6. **Deploy com revisor:** num repositório público, o GitHub Free tem *environments* com revisor
   obrigatório. Crie o `production`, mova para ele os secrets `VPS_SSH_KEY`, `VPS_HOST` e
   `VPS_HOST_KEY`, e ponha `environment: production` nos jobs do deploy e do backup. Junto, uma
   regra de proteção da `main` (PR obrigatório, sem push direto): hoje, um `workflow_dispatch`
   disparado num branch roda o workflow **daquele** branch, e a trava "só da `main`" vale contra
   engano, não contra quem tem escrita.
7. **Backups e logs do Actions ficam públicos.** Num repositório público, qualquer pessoa baixa os
   artifacts e lê os logs. Os artifacts são 30 dias de dumps **cifrados** com o `age` (ilegíveis
   sem a chave privada, mas passam a depender só dela; um vazamento da chave, ou uma falha futura
   do algoritmo, abriria o histórico inteiro). Os logs do `backup` mostram a versão do schema, o
   número de usuários e de alvos, o tamanho do dump e a ocupação do disco; os do deploy, os
   digests em produção e o IP da VPS se ele aparecer num passo. **Antes de mudar a
   visibilidade**, mova os backups para um lugar privado (um repositório privado separado só para
   o workflow de backup, ou um bucket com o upload no lugar do `upload-artifact`) e apague os
   artifacts antigos, **ou** aceite esse risco explicitamente, por escrito.
8. **Agendamentos:** num repositório público, o GitHub **desliga** os workflows agendados depois
   de 60 dias sem atividade no repositório, e avisa por e-mail. O `backup` para junto: reative-o
   em Actions quando o aviso chegar.
