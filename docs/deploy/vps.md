# Vigilume numa VPS da Hostinger: do zero ao ar, e a operação

Runbook **manual**, escrito para quem **nunca usou VPS nem SSH**. Nada aqui roda sozinho: cada
passo é feito por você, na ordem, e só se passa ao seguinte depois da verificação do passo
("**Confira**"). Se uma verificação não der o que o texto diz, pare ali: seguir com um passo
errado quase sempre complica o conserto.

O porquê das escolhas está na spec do Plano 11
(`docs/superpowers/specs/2026-09-25-plano-11-hospedagem-design.md`); as decisões que você ainda
pode revisar estão na §9 dela.

## Como ler este runbook

- **Dois computadores.** Todo bloco de comando diz onde roda:
  - **no seu computador** (o seu Linux, num terminal: `Ctrl+Alt+T` no Ubuntu);
  - **na VPS** (o servidor da Hostinger, num terminal conectado a ela por SSH, que você abre no
    bloco 2).
- **Um comando por vez.** Copie a linha inteira, cole no terminal (`Ctrl+Shift+V`), aperte Enter e
  leia o que apareceu antes de colar a próxima. Linhas que começam com `#` são comentários: o
  terminal as ignora.
- **`sudo` pede a sua senha** (a do seu usuário na VPS, criada no passo 2.5). Ao digitá-la, nada
  aparece na tela: é normal.
- **Segredos só vão para o gerenciador de senhas e para dois arquivos do root na VPS:** o
  `/opt/vigilume/.env` (os do app e do banco) e o `/opt/vigilume/vigia.env` (só a URL do vigia,
  bloco 8). Nunca num chat, num e-mail, num print, num arquivo do repositório ou num campo do
  GitHub. Os três segredos que o GitHub precisa (bloco 4) vão por um comando que lê de arquivo,
  sem colar nada.
- **O `compose.yaml` da raiz do repositório não vai para o servidor.** Ele cria um usuário de
  demonstração com senha pública e usa chaves públicas: num servidor, qualquer um entraria como
  dono. O servidor usa só os arquivos de `deploy/vps/`.

## Topologia

Uma VPS (Hostinger KVM 2: 2 vCPU, 8 GB, 100 GB NVMe, datacenter no Brasil) com Ubuntu 24.04 LTS e
Docker. Em `/opt/vigilume`, um `compose.prod.yaml` com três serviços numa rede interna
`vigilume` (172.30.0.0/24, sem IPv6):

| Serviço | O que é | Endereço | Porta publicada |
|---|---|---|---|
| `caddy` | Proxy com HTTPS automático (Let's Encrypt), só para o seu domínio | 172.30.0.2 | **80 e 443, e só ele** |
| `app` | O binário do Vigilume (`--role=all`): API, painel, sondador, avisos, manutenção | 172.30.0.3 | nenhuma |
| `db` | Postgres 18, a imagem oficial, fixada por digest | 172.30.0.4 | nenhuma |

Por fora: o GitHub Actions entrega cada versão nova por SSH, com uma chave que só sabe fazer
`deploy` e `backup` (bloco 4); o backup diário sai cifrado para o GitHub (bloco 7); e o
healthchecks.io avisa por e-mail se o site parar de responder (bloco 8).

## Mapa

| Bloco | O quê | Onde |
|---|---|---|
| 0 | Gerenciador de senhas, o arquivo de variáveis, as ferramentas do seu computador | seu computador |
| 1 | Chaves SSH, domínio, Resend, healthchecks.io | seu computador, painéis |
| 2 | Primeiro acesso e endurecimento da VPS | VPS |
| 3 | Docker, `/opt/vigilume`, segredos, chave do backup | VPS e seu computador |
| 4 | Usuário `deploy`, secrets do GitHub, primeiro deploy | VPS, seu computador, GitHub |
| 5 | Seu usuário no Vigilume e o 2FA | VPS, navegador |
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
"Vigilume". Tudo o que este runbook mandar "guardar no gerenciador" vai para lá, cada coisa numa
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
git -C ~/Projetos/Go/Vigilume remote -v
docker run --rm hello-world
```

Quatro respostas: as versões, a terceira com `BipoXDXD/vigilume` e a última com
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
arquivo agora e corrigir depois, com `nano ~/.vigilume-vps`):

```bash
cat > ~/.vigilume-vps <<'FIM'
VPS=203.0.113.10
DOMINIO=exemplo.com
EU=lucas
FIM
. ~/.vigilume-vps
```

**Por quê:** nenhum dos três é segredo, e tê-los num arquivo evita digitar errado o IP no meio de
um comando.

**Toda vez que abrir um terminal novo no seu computador**, rode `. ~/.vigilume-vps` antes dos
comandos deste runbook.

**Confira:** `echo "$VPS $DOMINIO $EU"` mostra os três valores.

---

## Bloco 1: chaves, domínio e contas

### 1.1 Chave SSH do administrador

**Faça** (no seu computador):

```bash
ssh-keygen -t ed25519 -C "vigilume-admin" -f ~/.ssh/vigilume_admin
```

Ele pede uma *passphrase* duas vezes: invente uma frase longa, guarde-a no gerenciador (entrada
"Vigilume – passphrase da chave SSH") e digite-a. Nada aparece enquanto digita.

**Por quê:** a VPS vai aceitar entrada **só** por chave, nunca por senha (passo 2.7). A chave
privada (`~/.ssh/vigilume_admin`) nunca sai do seu computador; a pública (`.pub`) vai para a VPS.
A passphrase protege a chave se alguém copiar o arquivo.

**Deve aparecer:** `Your identification has been saved in /home/…/.ssh/vigilume_admin` e um
desenho de caracteres (o *randomart*).

**Confira:** `ls -l ~/.ssh/vigilume_admin*` lista dois arquivos, o sem `.pub` com permissão
`-rw-------`.

**Cópia de segurança:** guarde também o **conteúdo** da chave privada no gerenciador (entrada
"Vigilume – chave SSH do administrador", como anexo ou nota segura): `cat ~/.ssh/vigilume_admin`
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
gerenciador; entrada "Vigilume – senha de root da VPS"). Ela só serve para o terminal do painel
em emergência: o SSH de root vai ser desligado no passo 2.7.

### 1.3 O IP da VPS e a chave pública no painel

**Faça:** na página **Overview** da VPS, copie o endereço **IPv4** e ponha-o no `~/.vigilume-vps`
(`VPS=…`, passo 0.3). Depois, no seu computador:

```bash
cat ~/.ssh/vigilume_admin.pub
```

Copie a linha inteira (começa com `ssh-ed25519`, termina com `vigilume-admin`). No painel, em
**Settings → SSH keys**, clique em adicionar, dê o nome `vigilume-admin` e cole a linha. (A chave
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
   `~/.vigilume-vps` (`DOMINIO=…`).
2. Vá em **Domain portfolio** → **Manage** ao lado do domínio → **DNS / Nameservers**.
3. **Apague** qualquer registro `A`, `AAAA` ou `CNAME` cujo nome seja `@` ou `www` (os que a
   Hostinger cria apontando para a hospedagem dela).
4. Adicione um registro **A**: **Name** `@`, **Points to** o IPv4 da VPS, **TTL** o padrão.

**Por quê:** o registro `A` diz ao mundo que `seu-domínio` é a sua VPS; o Caddy só consegue o
certificado HTTPS quando isso já vale. Sem `AAAA`: o Vigilume é configurado e conferido só em
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
5. **API Keys → Create API Key**: nome `vigilume-vps`, permissão **Sending access**, restrita ao
   domínio `avisos.seu-domínio`. A chave aparece **uma vez**: guarde-a direto no gerenciador
   (entrada "Vigilume – RESEND_API_KEY"). Ela só volta a ser usada no passo 3.3.

**Por quê:** sem domínio verificado, todo envio do Vigilume volta com erro 403 do Resend; o SPF e
o DKIM são o que faz o e-mail de alerta não cair no spam. A chave só de envio e só daquele domínio
limita o estrago se vazar.

**Deve aparecer:** o domínio com status **Verified** (muitas vezes em 15 minutos; o Resend diz que
pode levar até 72 h).

**Confira:** status **Verified** na lista de domínios. Se ficar **Pending** por horas, compare
registro a registro com o painel da Hostinger (o erro mais comum é o domínio repetido no **Name**).

Anote o remetente que vai usar, no formato `Vigilume <alertas@avisos.seu-domínio>`: é o
`EMAIL_FROM` do passo 3.3 (não é segredo).

### 1.6 healthchecks.io

**Faça:**

1. Crie a conta em <https://healthchecks.io> (o plano grátis basta) com o e-mail em que quer
   receber o alarme.
2. No projeto, **Add Check**: nome `vigilume-healthz`; **Period** `1 minute`; **Grace Time**
   `5 minutes`.
3. Copie a **ping URL** do check (o formato é `https://hc-ping.com/<uuid>`) para o gerenciador
   (entrada "Vigilume – HC_URL"). Ela só volta a ser usada no bloco 8.

**Por quê:** um timer na VPS vai pingar essa URL a cada minuto quando o site responder, e pingar
`/fail` quando não responder (bloco 8). Se os pings pararem (VPS morta) ou falharem, o
healthchecks manda e-mail: o Vigilume não consegue avisar da própria queda. A carência de 5 min
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

**Faça, depois, no seu computador:**

```bash
ssh -i ~/.ssh/vigilume_admin root@"$VPS"
```

Ele pergunta `Are you sure you want to continue connecting (yes/no/[fingerprint])?` e mostra um
`ED25519 key fingerprint is SHA256:…`.

**Por quê:** essa pergunta só aparece na primeira conexão. Se a impressão digital for a do terminal
do painel, você está falando com a sua VPS, e não com alguém no meio do caminho.

**Confira:** o `SHA256:…` da pergunta é **idêntico** ao do terminal do painel. Só então digite
`yes`, e a passphrase da chave. O prompt vira `root@…:~#`: você está **na VPS**.

Se forem diferentes, digite `no` e pare: releia o IP no `~/.vigilume-vps`.

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

**Faça** (na VPS, como root), trocando `lucas` pelo valor de `EU` do seu `~/.vigilume-vps`:

```bash
adduser lucas
```

Ele pede uma senha duas vezes (gere uma longa no gerenciador, entrada "Vigilume – senha sudo do
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
. ~/.vigilume-vps
ssh -i ~/.ssh/vigilume_admin "$EU@$VPS"
```

e, já na VPS, como você:

```bash
sudo whoami
```

Deve pedir a senha do `sudo` e responder `root`. Só siga se deu certo.

### 2.6 A chave no `~/.ssh/config` do seu computador

**Faça** (no seu computador):

```bash
. ~/.vigilume-vps
cat >> ~/.ssh/config <<FIM

Host vigilume
    HostName $VPS
    User $EU
    IdentityFile ~/.ssh/vigilume_admin
    IdentitiesOnly yes
FIM
chmod 600 ~/.ssh/config
```

**Se já existe um `Host vigilume`** no seu `~/.ssh/config` (você está refazendo este passo numa
reconstrução, "A VPS morreu" no bloco 10), **não rode o bloco acima**: um segundo `Host vigilume`
fica depois do primeiro, e o `ssh` usa o primeiro valor que lê, ou seja, o IP velho. Em vez disso,
abra o arquivo com `nano ~/.ssh/config`, troque só o valor da linha `HostName` do `Host vigilume`
pelo IP novo, salve (`Ctrl+O`, Enter, `Ctrl+X`) e rode o **Confira** abaixo. Confira quantos há com
`grep -c '^Host vigilume$' ~/.ssh/config`: tem de dar `1`.

**Por quê:** a partir daqui, `ssh vigilume` e `scp arquivo vigilume:` bastam; o `IdentitiesOnly`
evita que o SSH ofereça outras chaves suas e conte tentativas no `fail2ban` (passo 2.8).

**Confira:** `ssh vigilume true && echo ok` imprime `ok` (depois da passphrase, se o seu
chaveiro não a lembrar).

### 2.7 SSH sem senha e sem root

**Faça** (na VPS, como você):

```bash
sudo tee /etc/ssh/sshd_config.d/00-vigilume.conf >/dev/null <<'FIM'
# Endurecimento do Vigilume (spec do Plano 11, §5). O nome começa com 00 porque
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
  **antes** da linha `Include /etc/ssh/sshd_config.d/*.conf` vence o `00-vigilume.conf` (o
  primeiro valor lido vale). Ponha um `#` no começo dela (`sudo nano /etc/ssh/sshd_config`),
  rode `sudo sshd -t && sudo systemctl restart ssh` e o `sshd -T … | grep` de novo, até as três
  darem `no`.
- Uma linha `Match`: ela muda as regras para certas conexões, e o `sshd -T` sozinho não a
  mostra. Uma imagem limpa do Ubuntu não tem nenhuma: se houver, pare aqui e não siga sem
  entender o que ela libera.

**Confira**, **sem fechar** o terminal em que você está (num terminal novo do seu computador):

```bash
. ~/.vigilume-vps
ssh vigilume true && echo "entrada por chave: ok"
ssh -o PubkeyAuthentication=no -o PreferredAuthentications=password "$EU@$VPS" true
ssh -i ~/.ssh/vigilume_admin root@"$VPS" true
```

A primeira imprime `entrada por chave: ok`; a segunda e a terceira terminam em
`Permission denied (publickey).` Se a primeira falhar, **não feche** o terminal aberto: desfaça
com `sudo rm /etc/ssh/sshd_config.d/00-vigilume.conf && sudo systemctl restart ssh` e releia os
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
sudo tee /etc/fail2ban/jail.d/vigilume.local >/dev/null <<'FIM'
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

**Por quê:** fecha tudo o que chega, menos SSH (22), HTTP (80, que o Caddy usa para o certificado e
para redirecionar) e HTTPS (443). **Atenção:** o Docker passa por cima do `ufw` nas portas que um
contêiner publica. Quem garante que só 80 e 443 ficam abertas é o `compose.prod.yaml` (só o Caddy
publica porta) e o firewall do painel (passo seguinte); o bloco 9 confere de fora.

**Confira:**

```bash
sudo ufw status verbose
```

`Status: active`, `Default: deny (incoming), allow (outgoing)`, e as três portas com `ALLOW IN`
(em IPv4 e `(v6)`). E, num terminal novo do seu computador, **sem fechar** o terminal em que você
está, `ssh vigilume true && echo ok` continua dando `ok`. Se não der, desfaça pelo terminal que
ficou aberto (`sudo ufw disable`) e releia o passo.

### 2.10 Firewall do painel da Hostinger

Fonte: Hostinger, [How to use a managed VPS firewall](https://www.hostinger.com/support/8172641-how-to-use-a-managed-vps-firewall-at-hostinger/).

**Faça** (no painel da VPS): **Security → Firewall** → **Add Firewall**, nome `vigilume`,
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
ssh vigilume true && echo "ssh ok"
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
sudo tee /etc/apt/apt.conf.d/52vigilume-reinicio >/dev/null <<'FIM'
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

## Bloco 3: Docker, `/opt/vigilume`, segredos e a chave do backup

### 3.1 Docker pelo repositório oficial

Fonte: Docker, [Install Docker Engine on Ubuntu](https://docs.docker.com/engine/install/ubuntu/).

**Faça** (na VPS):

```bash
sudo apt install -y ca-certificates curl
sudo install -m 0755 -d /etc/apt/keyrings
sudo curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
sudo chmod a+r /etc/apt/keyrings/docker.asc
gpg --show-keys /etc/apt/keyrings/docker.asc
```

**Confira** que a impressão digital mostrada é `9DC8 5822 9FC7 DD38 854A  E2D8 8D81 803C 0EBF CD88`
(a chave de assinatura da Docker). Se for outra, pare: não é a chave da Docker.

```bash
sudo tee /etc/apt/sources.list.d/docker.sources >/dev/null <<FIM
Types: deb
URIs: https://download.docker.com/linux/ubuntu
Suites: $(. /etc/os-release && echo "${UBUNTU_CODENAME:-$VERSION_CODENAME}")
Components: stable
Architectures: $(dpkg --print-architecture)
Signed-By: /etc/apt/keyrings/docker.asc
FIM
sudo apt update
sudo apt install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
```

**Por quê:** o Docker do repositório da própria Docker é o suportado e o que recebe correção
primeiro; o `docker.io` do Ubuntu fica para trás. (O `unattended-upgrades` só aplica as
atualizações de segurança do Ubuntu: o Docker você atualiza no passo mensal do bloco 10.)

**Confira:** `sudo docker run --rm hello-world` imprime `Hello from Docker!`.

### 3.2 Configuração do Docker: logs, `live-restore` e o image store do containerd

**Faça** (na VPS):

```bash
sudo tee /etc/docker/daemon.json >/dev/null <<'FIM'
{
  "log-driver": "json-file",
  "log-opts": { "max-size": "10m", "max-file": "3" },
  "live-restore": true,
  "features": { "containerd-snapshotter": true }
}
FIM
sudo systemctl restart docker
sudo docker info -f '{{.LoggingDriver}} {{.LiveRestoreEnabled}}'
sudo docker info -f '{{.DriverStatus}}'
```

**Por quê:** sem rotação, o log de um contêiner cresce até encher o disco (aqui, no máximo 3
arquivos de 10 MB por contêiner). O `live-restore` mantém os contêineres de pé quando o próprio
Docker é atualizado ou reiniciado. E o deploy (bloco 4) confere que a imagem carregada é a
aprovada comparando o ID dela com o digest do GitHub, o que só funciona com o *image store* do
containerd: ele é o padrão das instalações novas do Docker 29
([fonte](https://docs.docker.com/engine/storage/containerd/)), e a linha `features` o garante
mesmo numa instalação que veio de uma versão anterior. O `vigilume-entrada` recusa o deploy (sai
com 1 e diz o motivo) se o store não for o do containerd.

**Deve aparecer:** `json-file true` e, na última linha, algo com
`[[driver-type io.containerd.snapshotter.v1]]`.

**Confira:** se a última linha **não** tiver `io.containerd.snapshotter.v1`, o `daemon.json` não
foi lido: `sudo cat /etc/docker/daemon.json` (tem de ser exatamente o JSON acima) e
`sudo journalctl -u docker -n 20` (um erro de JSON aparece ali). Não siga sem ela. (Trocar de store
esconde as imagens do store antigo; numa VPS nova, só o `hello-world` some, e não faz falta.)

### 3.3 Os arquivos do Vigilume em `/opt/vigilume`

**Faça, primeiro no seu computador**, com a `main` atualizada (os arquivos da VPS têm de ser os da
versão que vai para o ar):

```bash
cd ~/Projetos/Go/Vigilume
git switch main && git pull --ff-only
ssh vigilume 'mkdir -p ~/vigilume-arquivos'
scp deploy/vps/compose.prod.yaml deploy/vps/Caddyfile deploy/vps/initdb.sh deploy/vps/env.exemplo \
    deploy/vps/vigilume-entrada deploy/vps/sudoers-vigilume-entrada \
    vigilume:vigilume-arquivos/
```

**Deve aparecer:** uma linha de progresso por arquivo, seis no total. (Os arquivos do vigia vêm
só no bloco 8, quando são instalados.)

**Depois, na VPS:**

```bash
cd ~/vigilume-arquivos
sudo install -d -m 0755 -o root -g root /opt/vigilume
sudo install -m 0644 -o root -g root compose.prod.yaml Caddyfile /opt/vigilume/
sudo install -m 0755 -o root -g root initdb.sh /opt/vigilume/
sudo install -m 0600 -o root -g root env.exemplo /opt/vigilume/.env
ls -la /opt/vigilume
```

**Por quê:** `/opt/vigilume` é o lugar fixo que o script de deploy usa. Tudo é do root: o usuário
`deploy` (bloco 4) não escreve ali, só o script que ele pode chamar. O `.env` nasce do modelo, sem
valores, com permissão `600` (só o root lê).

**Deve aparecer:** `compose.prod.yaml`, `Caddyfile`, `initdb.sh` e `.env`, todos com dono `root`;
o `.env` com `-rw-------`.

**Faça também** os scripts do backup e os pacotes de que o `vigilume-entrada` precisa. No seu
computador:

```bash
cd ~/Projetos/Go/Vigilume
scp deploy/backup/backup.sh deploy/backup/verificar.sql deploy/compose/initdb.sql \
    vigilume:vigilume-arquivos/
```

Depois, na VPS:

```bash
sudo apt install -y age curl jq util-linux
cd ~/vigilume-arquivos
sudo install -d -m 0755 -o root -g root /opt/vigilume/deploy/backup /opt/vigilume/deploy/compose
sudo install -m 0644 -o root -g root backup.sh verificar.sql /opt/vigilume/deploy/backup/
sudo install -m 0644 -o root -g root initdb.sql /opt/vigilume/deploy/compose/
ls -la /opt/vigilume/deploy/backup /opt/vigilume/deploy/compose
command -v age curl flock jq
```

**Por quê:** o verbo `backup` do `vigilume-entrada` roda o `deploy/backup/backup.sh` do
repositório, sem mudança, dentro de um Postgres descartável, e ele acha o `initdb.sql` por
`../compose/`: por isso os três arquivos ficam em `/opt/vigilume/deploy/` com o mesmo layout do
repositório. O `age` cifra o dump com a chave pública (passo 3.5), o `flock` (do `util-linux`)
impede que um deploy e um backup rodem ao mesmo tempo, o `jq` confere o índice da imagem que o
deploy recebe antes de carregá-la, e o `curl` é o que o vigia (bloco 8) usa.

**Deve aparecer:** `backup.sh` e `verificar.sql` em `/opt/vigilume/deploy/backup`, `initdb.sql`
em `/opt/vigilume/deploy/compose`, todos do `root`, e os quatro caminhos do `command -v` (por
exemplo `/usr/bin/age`, `/usr/bin/curl`, `/usr/bin/flock`, `/usr/bin/jq`).

### 3.4 Os segredos, gerados na VPS

**Faça** (na VPS):

Cole o bloco **inteiro de uma vez** (do `sudo sh -s` até o `FIM`):

```bash
sudo sh -s <<'FIM'
set -eu
umask 077
cd /opt/vigilume
gerar() {
  VALOR=$(openssl rand "-$2" 32) NOME=$1 awk '
    $0 == ENVIRON["NOME"] "=" { print ENVIRON["NOME"] "=" ENVIRON["VALOR"]; next }
    { print }
  ' .env > .env.novo
  cat .env.novo > .env
  rm .env.novo
}
for v in POSTGRES_PASSWORD VIGILUME_APP_PASSWORD VIGILUME_BACKUP_PASSWORD; do gerar "$v" hex; done
for v in MASTER_KEY JWT_SECRET IP_HASH_KEY; do gerar "$v" base64; done
FIM
```

Ele não imprime nada. Cada valor nasce e é gravado dentro de um processo do root: não passa pela
linha de comando de nenhum programa (que outro usuário da VPS veria no `ps`), nem pelo seu
histórico. Só preenche variável **vazia**: rodar de novo por engano não troca um segredo que já
existe.

(`POSTGRES_PASSWORD` é a senha do superusuário do Postgres; `VIGILUME_APP_PASSWORD` e
`VIGILUME_BACKUP_PASSWORD`, as dos papéis do app e do backup.)

**Por quê:** segredo gerado na própria VPS nunca passou pela rede nem por outro computador. As
senhas do Postgres são hexadecimais porque entram em URLs `postgres://usuário:senha@…`, e o `/`,
o `+` e o `=` do base64 quebrariam a URL. As três chaves do app são 32 bytes aleatórios em
base64, o formato que o boot exige.

**Agora preencha o que não é gerado**, no editor:

```bash
sudo nano /opt/vigilume/.env
```

- `VIGILUME_DOMINIO`: o seu domínio, sem `https://` (o valor de `DOMINIO`).
- `RESEND_API_KEY`: cole a chave do gerenciador (passo 1.5). Este `.env` é um dos lugares
  onde segredo pode ser colado.
- `EMAIL_FROM`: `Vigilume <alertas@avisos.seu-domínio>` (passo 1.5).

Salve com `Ctrl+O` e Enter, saia com `Ctrl+X`.

O `.env` tem só estas nove variáveis (a `HC_URL` do healthchecks não está nele: ela vai para
o `vigia.env` do bloco 8, e o vigia nunca lê o `.env`). O resto o `compose.prod.yaml` monta sozinho: a
`PUBLIC_BASE_URL` e a `CORS_ORIGIN` (`https://` + `VIGILUME_DOMINIO`), a `DATABASE_URL` (com a
`VIGILUME_APP_PASSWORD` e o IP fixo do banco) e o `TRUSTED_PROXIES` (o IP fixo do Caddy). O
compose lê o `.env` pela interpolação (`${…}`), e cada serviço recebe só as variáveis de que
precisa: o Caddy não vê segredo nenhum, e o app não vê a senha do superusuário nem a do backup.

**Confira:**

```bash
sudo stat -c '%U %a' /opt/vigilume/.env
sudo grep -cE '^[A-Z_]+=$' /opt/vigilume/.env
sudo docker compose -f /opt/vigilume/compose.prod.yaml config -q && echo "compose ok"
```

`root 600`; `0` (nenhuma variável vazia); `compose ok`.

**Guarde no gerenciador**, agora, cada valor (uma entrada por nome, "Vigilume – MASTER_KEY" e
assim por diante):

```bash
sudo cat /opt/vigilume/.env
clear
```

Copie cada valor do terminal para o gerenciador e rode o `clear` em seguida. É a única vez que
eles aparecem na tela. O mais importante é a `MASTER_KEY`: sem ela, os headers de alvo e as
configurações de canal ficam ilegíveis para sempre, e nenhum backup a devolve. O `JWT_SECRET` e o
`IP_HASH_KEY` podem ser trocados a qualquer hora (bloco 10) com custo pequeno; as senhas do
Postgres e a `MASTER_KEY`, não.

**Confira:** o gerenciador tem uma entrada por variável do `.env`, e o valor de uma delas, tirado
ao acaso, confere com o do terminal (ex.: `sudo grep '^JWT_SECRET=' /opt/vigilume/.env` e compare
com os olhos, depois `clear`).

### 3.5 A chave do backup (`age`): a privada fica só com você

**Faça** (no seu computador, **fora** do diretório do repositório):

```bash
age-keygen -o ~/vigilume-backup.key
chmod 600 ~/vigilume-backup.key
age-keygen -y ~/vigilume-backup.key > ~/vigilume-age-recipient.txt
cat ~/vigilume-age-recipient.txt
```

**Por quê:** o backup diário sai da VPS já cifrado com a chave **pública**; só a **privada**,
que nunca vai para a VPS nem para o GitHub, abre os dumps. Quem invadir a VPS ou ler os artifacts
do GitHub vê só texto cifrado. Fora do repositório para um `git add -A` nunca a pegar (o
`.gitignore` também cobre `*.key`, por garantia).

**Deve aparecer:** o `age-keygen -o` imprime `Public key: age1…`, e o `cat` mostra a mesma linha
`age1…`.

**Guarde** o conteúdo de `~/vigilume-backup.key` no gerenciador (entrada "Vigilume – chave
privada do backup (age)"):

```bash
cat ~/vigilume-backup.key
clear
```

Copie as três linhas do arquivo (o comentário `# created: …`, o `# public key: age1…` e a
`AGE-SECRET-KEY-1…`) para o gerenciador e rode o `clear` em seguida, como no 3.4: a chave privada
não fica na tela. **Perder a chave privada = perder todos os backups.**

**Leve a pública para a VPS:**

```bash
scp ~/vigilume-age-recipient.txt vigilume:vigilume-arquivos/
ssh -t vigilume 'sudo install -m 0644 -o root -g root ~/vigilume-arquivos/vigilume-age-recipient.txt /opt/vigilume/age-recipient'
```

**Confira** (no seu computador): a pública da VPS abre com a sua privada.

```bash
ssh vigilume cat /opt/vigilume/age-recipient | diff - ~/vigilume-age-recipient.txt && echo "iguais"
echo "ida e volta" | age -R ~/vigilume-age-recipient.txt | age -d -i ~/vigilume-backup.key
```

`iguais` e `ida e volta`.

---

## Bloco 4: o usuário `deploy`, os secrets do GitHub e o primeiro deploy

O GitHub Actions entrega cada versão nova por SSH, como o usuário `deploy`. Esse usuário não tem
shell, não tem `sudo` geral e não está no grupo `docker`: a chave dele só consegue rodar
`/usr/local/sbin/vigilume-entrada`, e o script só aceita dois pedidos, `deploy` e `backup`.

### 4.1 O script de entrada e a regra do `sudo`

**Faça** (na VPS):

```bash
cd ~/vigilume-arquivos
sudo install -m 0755 -o root -g root vigilume-entrada /usr/local/sbin/vigilume-entrada
sudo visudo -cf sudoers-vigilume-entrada
sudo install -m 0440 -o root -g root sudoers-vigilume-entrada /etc/sudoers.d/vigilume-entrada
sudo visudo -c
```

**Por quê:** o `sudoers` deixa o `deploy` rodar como root **só** esse script. O `visudo -c` confere
a sintaxe antes e depois: um `sudoers` quebrado pode travar o `sudo` de todo mundo. Se
acontecer, o conserto não pode depender do `sudo`: pelo terminal do painel, entre como **root**
com a senha de root do passo 1.2 e apague o arquivo (`rm /etc/sudoers.d/vigilume-entrada`). Se o
terminal do painel só entrar com o seu usuário (o `sudo` quebrado não serve), use o **Emergency
mode** (bloco 10, "Perdi a chave SSH", item 3), que dá acesso ao disco.

**Deve aparecer:** `parsed OK` nas duas conferências (a segunda lista todos os arquivos).

**Confira:** `ls -l /usr/local/sbin/vigilume-entrada /etc/sudoers.d/vigilume-entrada` mostra
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
ssh-keygen -t ed25519 -N "" -C "vigilume-deploy-github" -f ~/.ssh/vigilume_deploy
scp ~/.ssh/vigilume_deploy.pub vigilume:vigilume-arquivos/
```

**Por quê:** uma chave só para o GitHub, sem passphrase (o Actions não tem quem a digite). Ela
não precisa ser guardada: depois do passo 4.6 ela é apagada, e se um dia precisar, gera-se outra.

### 4.4 O `authorized_keys` do `deploy`, com a chave presa ao script

**Faça** (na VPS):

```bash
sudo install -d -m 0755 -o root -g root /home/deploy/.ssh
printf 'restrict,command="sudo /usr/local/sbin/vigilume-entrada" %s\n' \
  "$(cat ~/vigilume-arquivos/vigilume_deploy.pub)" \
  | sudo tee /home/deploy/.ssh/authorized_keys >/dev/null
sudo chmod 0644 /home/deploy/.ssh/authorized_keys
sudo cat /home/deploy/.ssh/authorized_keys
```

**Por quê:** `command=` faz toda conexão com essa chave rodar o script, seja qual for o comando
pedido (o pedido chega ao script como texto, e ele recusa o que não for `deploy` ou `backup`);
`restrict` desliga terminal, túnel de porta, agente e X11. O diretório e o arquivo são do root:
nem o próprio `deploy` pode trocar a chave.

**Deve aparecer:** uma linha só, começando com
`restrict,command="sudo /usr/local/sbin/vigilume-entrada" ssh-ed25519 AAAA` e terminando em
`vigilume-deploy-github`.

**Confira** (no seu computador), que a chave **só** faz o que deve:

```bash
. ~/.vigilume-vps
ssh -i ~/.ssh/vigilume_deploy -o IdentitiesOnly=yes deploy@"$VPS" ; echo "saiu com $?"
ssh -i ~/.ssh/vigilume_deploy -o IdentitiesOnly=yes deploy@"$VPS" ls / ; echo "saiu com $?"
ssh -i ~/.ssh/vigilume_deploy -o IdentitiesOnly=yes -W 172.30.0.4:5432 deploy@"$VPS" ; echo "saiu com $?"
```

- As duas primeiras: `vigilume-entrada: pedido vazio (uso: "deploy <sha de 40 hex> sha256:<64 hex>" ou "backup")`
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
. ~/.vigilume-vps
ssh vigilume cat /etc/ssh/ssh_host_ed25519_key.pub > ~/vigilume-host-key.txt
cat ~/vigilume-host-key.txt
ssh-keygen -lf ~/vigilume-host-key.txt
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
. ~/.vigilume-vps
gh secret set VPS_HOST --repo BipoXDXD/vigilume --body "$VPS"
gh secret set VPS_SSH_KEY --repo BipoXDXD/vigilume < ~/.ssh/vigilume_deploy
gh secret set VPS_HOST_KEY --repo BipoXDXD/vigilume < ~/vigilume-host-key.txt
gh secret list --repo BipoXDXD/vigilume
```

**Por quê:** o `gh` lê o valor do arquivo e o cifra antes de mandar; nada passa pela área de
transferência nem pela tela. No GitHub Free com repositório privado não há *environments*: são
secrets de repositório, e quem pode disparar o deploy é quem tem acesso de escrita (hoje, só
você). Secrets de repositório não chegam a PR de fork.

**Deve aparecer:** a lista com `VPS_HOST`, `VPS_HOST_KEY` e `VPS_SSH_KEY` (com a data de hoje). São
os únicos secrets que o deploy e o backup usam; se aparecer algum outro, de uma hospedagem
anterior, apague-o com `gh secret delete <nome> --repo BipoXDXD/vigilume`. A única variável é a
`BACKUP_ENABLED` (bloco 7): `gh variable list --repo BipoXDXD/vigilume` mostra as que existem, e
outras antigas saem com `gh variable delete <nome> --repo BipoXDXD/vigilume`.

**Agora apague a chave do deploy do seu computador:**

```bash
shred -u ~/.ssh/vigilume_deploy
rm ~/.ssh/vigilume_deploy.pub
```

**Confira:** `ls ~/.ssh/vigilume_deploy*` diz que não existe.

### 4.7 Limpar a pasta de trabalho da VPS

**Faça** (na VPS):

```bash
rm -r ~/vigilume-arquivos
```

**Por quê:** os arquivos já estão instalados nos lugares certos; cópias soltas envelhecem e
confundem. Na próxima mudança de `deploy/vps/` você recopia (bloco 10, "Arquivos da VPS").

### 4.8 O primeiro deploy, pelo Actions

**Antes:** o `getent` do passo 1.4 já devolve o IP da VPS (o Caddy precisa disso para o
certificado).

**Faça** (no navegador, ou no seu computador):

1. Ache o SHA completo (40 caracteres) do último commit da `main` com o `ci` verde (o job
   `publish` só roda depois de todos os outros, e a tag `:<sha>` da imagem só existe depois que o
   Trivy aprovou). O `gh` pergunta direto ao GitHub; o `git fetch` atualiza a sua cópia, para
   comparar:
   ```bash
   SHA=$(gh run list --repo BipoXDXD/vigilume --workflow ci.yml --branch main --event push \
     --status success --limit 1 --json headSha -q '.[0].headSha')
   echo "$SHA"
   git -C ~/Projetos/Go/Vigilume fetch origin
   git -C ~/Projetos/Go/Vigilume rev-parse origin/main
   ```
   **Deve aparecer:** duas linhas de 40 caracteres, normalmente **iguais**. Se forem diferentes,
   o topo da `main` ainda não passou no `ci` (está rodando ou ficou vermelho: veja
   `gh run list --repo BipoXDXD/vigilume --workflow ci.yml --branch main --limit 3`). Espere ele
   ficar verde e repita, ou faça o deploy do `$SHA` da primeira linha, que é o último que passou.
   Se a primeira linha vier vazia, nenhum `ci` da `main` passou ainda: não há imagem para
   entregar.
2. **Actions → deploy → Run workflow**, na `main`, com o SHA no campo `sha`. Ou, pelo
   terminal, no mesmo terminal do item 1 (ele usa o `$SHA`):
   ```bash
   gh workflow run deploy.yml --repo BipoXDXD/vigilume --ref main -f sha="$SHA"
   gh run watch --repo BipoXDXD/vigilume
   ```

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
cat /opt/vigilume/versao
sudo tail -n 30 /opt/vigilume/entrada.log
```

O `versao` é o SHA que está no ar; o `entrada.log` é o log de cada deploy e backup, o mesmo que o
job mostra, gravado na VPS (fica em até ~1 MiB; o mais velho sai sozinho). As últimas linhas
mostram como o deploy acabou: saudável, com rollback para a anterior, ou o erro. Se ele ainda
estiver rodando (as linhas continuam chegando), espere dois minutos e olhe de novo. Só rode o
deploy outra vez depois de ver como este terminou.

**Se o primeiro deploy sair com 4**, o app **ficou parado**: no primeiro deploy não há versão
anterior para onde voltar (a mensagem é
`primeiro deploy e sem versão anterior: o app ficou parado`). O site responde `502`, e o Caddy e
o banco ficam de pé. Quase sempre é o `.env` (uma variável mal preenchida no 3.4) ou o DNS. Veja
por que o app não subiu:

```bash
sudo docker compose -f /opt/vigilume/compose.prod.yaml logs app | tail -30
sudo docker compose -f /opt/vigilume/compose.prod.yaml ps -a
```

O app diz no boot o que falta ou o que está errado (uma variável ausente, uma chave fora do
formato, o banco recusando o papel). Corrija a causa (por exemplo, `sudo nano /opt/vigilume/.env`)
e rode o deploy de novo com o mesmo SHA: não é preciso publicar outra versão.

**Confira** (na VPS):

```bash
sudo docker compose -f /opt/vigilume/compose.prod.yaml ps
cat /opt/vigilume/versao
sudo docker compose -f /opt/vigilume/compose.prod.yaml logs app | head -20
```

- `ps`: `caddy`, `app` e `db` com estado `running` (e `healthy` onde há healthcheck);
- `versao`: o SHA que você mandou (o `cat` vai sem `sudo`: o `versao`, o `versao-anterior` e o
  `historico` são `0644`, legíveis pelo seu usuário, porque não são segredo; o `.env` e o
  `vigia.env` continuam só do root). Se aparecer `Permission denied`, o `vigilume-entrada`
  instalado é de antes dessa mudança: recopie-o (bloco 10, "Arquivos da VPS") e, até o próximo
  deploy, use `sudo cat`;
- `logs`: `iniciando` com a versão, `migrations aplicadas`, `sondador ativo`, `servindo`, e
  **nenhuma** linha `TRUSTED_PROXIES vazio`.

**E de fora** (no seu computador):

```bash
. ~/.vigilume-vps
curl -sS "https://$DOMINIO/healthz"; echo
curl -sS -o /dev/null -w '%{http_code}\n' "https://$DOMINIO/readyz"
```

`/healthz` responde (o corpo do app) sem erro de certificado, e `/readyz` dá `200`. Se o `curl`
reclamar do certificado, espere um minuto (o Caddy pode estar terminando de pegar o certificado)
e veja `sudo docker compose -f /opt/vigilume/compose.prod.yaml logs caddy | tail -20`: um erro de
"challenge" quase sempre é DNS ainda não propagado ou porta 80 fechada no firewall do painel.

**Um atalho para os próximos comandos** (na VPS; opcional, mas o resto do runbook o usa):

```bash
echo "alias vc='sudo docker compose -f /opt/vigilume/compose.prod.yaml'" >> ~/.bash_aliases
. ~/.bash_aliases
vc ps
```

**Confira no banco** quem é dono das tabelas (as migrations rodaram como o papel comum):

```bash
vc exec db psql -U postgres -d vigilume -c "SELECT tableowner, count(*) FROM pg_tables WHERE schemaname = 'public' GROUP BY 1;"
vc exec db psql -U postgres -c "SELECT rolname, rolsuper, rolbypassrls FROM pg_roles WHERE rolname LIKE 'vigilume%' ORDER BY 1;"
```

Só `vigilume_app` na primeira; `vigilume_app | f | f` e `vigilume_backup | f | t` na segunda.

---

## Bloco 5: seu usuário no Vigilume e o 2FA

### 5.1 Criar o dono

**Faça** (na VPS), com o seu e-mail no lugar do exemplo:

```bash
vc exec app /vigilume user create --email voce@exemplo.com
```

Ele pede `Senha:` e `Confirme a senha:`, sem eco. Gere a senha no gerenciador (entrada "Vigilume
– login do painel"; de 15 a 128 caracteres) e cole-a nas duas perguntas.

**Por quê:** o sistema é de um dono só, e não há cadastro pela web: o primeiro usuário nasce pelo
CLI, dentro do contêiner que já tem a `DATABASE_URL`. A senha vem do terminal, nunca de argumento
(argumento aparece no `ps` e no histórico). O `docker compose exec` já abre um terminal, por isso
não ecoa.

**Deve aparecer:** `usuário criado: voce@exemplo.com (id …)`. Se disser
`já existe um usuário; nada foi alterado` (código 3), alguém já criou: use
`vc exec app /vigilume user set-password --email <e-mail>` para trocar a senha.

### 5.2 Entrar e ligar o 2FA

**Faça** (no navegador): abra `https://seu-domínio`, entre com o e-mail e a senha. Vá em
**Configurações → Verificação em duas etapas → Ativar**, leia o QR code com o seu app
autenticador (Aegis, 2FAS, Google Authenticator…), digite o código de 6 dígitos e guarde os
**códigos de recuperação** no gerenciador (entrada "Vigilume – códigos de recuperação do 2FA").

**Por quê:** com o painel na internet, a senha sozinha é pouco. Os códigos de recuperação são a
volta se o celular sumir; sem eles e sem o app, só o `reset-2fa` pelo CLI (bloco 10).

**Confira:** saia (**Sair**), entre de novo: depois da senha, ele pede o código do app. A tela
**Configurações** mostra a verificação em duas etapas ativada.

---

## Bloco 6: e-mail, canal e alvo de teste

### 6.1 Canal de e-mail

**Faça** (no painel): **Canais de notificação → Novo canal**, tipo **E-mail**, com o seu e-mail.

**Por quê:** confere ponta a ponta o Resend (domínio verificado, chave, remetente) e os links, que
usam a `PUBLIC_BASE_URL` de produção.

**Deve aparecer:** `Enviamos o link de verificação.` e, em até alguns minutos, um e-mail do
remetente `Vigilume <alertas@avisos.seu-domínio>` com um link para `https://seu-domínio/…`.
Clique nele.

**Confira:** o canal aparece como verificado. Se o e-mail não chegar: a caixa de spam; depois
`vc logs app | grep -i resend | tail` (um `403` é domínio não verificado ou `EMAIL_FROM` com
outro domínio).

(Opcional: um canal do Discord pelo mesmo caminho; ele manda uma mensagem de teste na hora.)

### 6.2 Alvo de teste e um incidente de verdade

**Faça** (no painel): **Novo alvo** com a URL `https://example.com/`, intervalo de 30 s e
**status esperado 500** (de propósito: o site responde 200, então o Vigilume o verá fora do
ar).

**Por quê:** força um incidente sem depender de derrubar nada: confere sondador, detector,
fila de avisos e e-mail, tudo de uma vez.

**Deve aparecer:** em um ou dois minutos, o alvo fica fora do ar no painel, um incidente abre e
chega o e-mail de alerta.

**Confira:** depois do e-mail, edite o alvo e ponha o status esperado **200**. Em mais um ou dois
minutos o incidente fecha e chega o aviso de que voltou. Então apague o alvo, ou deixe-o com 200
como monitor de exemplo.

---

## Bloco 7: backup ligado e ensaio de restauração

O backup diário (06:17 UTC, 03:17 de Brasília) roda no GitHub: `ssh deploy@VPS backup` devolve o
dump **já cifrado** com a sua chave pública, que vira um artifact de 30 dias. Na VPS, o script
sobe um Postgres descartável, faz o dump como `vigilume_backup`, restaura a cópia e a confere
(`deploy/backup/backup.sh` e `verificar.sql`) antes de cifrar. Um backup que não restaura não é
backup.

### 7.1 Ligar e rodar o primeiro à mão

**Faça** (no seu computador):

```bash
gh variable set BACKUP_ENABLED --repo BipoXDXD/vigilume --body 1
gh workflow run backup.yml --repo BipoXDXD/vigilume --ref main
gh run watch --repo BipoXDXD/vigilume
```

(O `gh run watch` pergunta qual execução acompanhar: escolha a do `backup` que acabou de começar.)

**Por quê:** até a variável existir, o agendamento diário termina em *skipped* sem fazer nada.
Rodar à mão agora confere tudo (chave, script, `age`, artifact) antes de confiar no automático.

**Deve aparecer:** o job verde, e no resumo uma linha `Postgres: N% …` com a ocupação do disco.

**Confira** (na VPS), que o dump em texto claro e o banco descartável não sobraram:

```bash
sudo ls -A /dev/shm
sudo docker ps -a --filter name=vigilume-restauro-
```

O `ls` não mostra nenhum `vigilume-backup.*`, e o `docker ps` mostra só a linha de títulos
(`CONTAINER ID   IMAGE …`), sem nenhum contêiner. O dump em claro só existe durante o backup, em
`/dev/shm` (memória, não disco), e a restauração de conferência roda num Postgres descartável
chamado `vigilume-restauro-…`; o script apaga os dois no fim, e só o cifrado sai da VPS. Se sobrar
algum (um backup morto no meio), o próximo backup os apaga antes de começar.

**Depois de ligado, um `backup` agendado *skipped* (cinza) é falha**, tanto quanto um vermelho: não
houve backup naquele dia, e o GitHub não manda e-mail por isso. A causa usual é a variável
`BACKUP_ENABLED` apagada ou mudada.

### 7.2 Ensaio de restauração (antes de dar o Vigilume por pronto)

**Faça** (no seu computador):

1. Baixe o artifact da execução do passo 7.1:
   ```bash
   mkdir -p ~/vigilume-restauro && cd ~/vigilume-restauro
   RUN_ID=$(gh run list --repo BipoXDXD/vigilume --workflow backup.yml --status success --limit 1 \
     --json databaseId -q '.[0].databaseId')
   echo "$RUN_ID"
   gh run download "$RUN_ID" --repo BipoXDXD/vigilume
   ARQ=$(find ~/vigilume-restauro -name 'vigilume-*.dump.age' | sort | tail -1)
   echo "$ARQ"
   ```
   Deve aparecer o caminho de um arquivo `vigilume-AAAAMMDD.dump.age` (o dump do Postgres, em
   formato custom, cifrado).
2. Decifre, com a chave que só você tem:
   ```bash
   age -d -i ~/vigilume-backup.key -o ~/vigilume-restauro/vigilume.dump "$ARQ"
   ```
3. Restaure num Postgres descartável, da mesma imagem de produção (o digest de
   `deploy/vps/db/Dockerfile`), só no `127.0.0.1` do seu computador. O `docker compose stop`
   para o compose de desenvolvimento, se estiver de pé: ele ocupa a `127.0.0.1:8080` que a cópia
   usa no item 4 (sem ele de pé, o comando não faz nada):
   ```bash
   cd ~/Projetos/Go/Vigilume
   docker compose stop
   docker run -d --name restauro -p 127.0.0.1:25433:5432 -e POSTGRES_PASSWORD=restauro \
     "$(sed -n 's/^FROM //p' deploy/vps/db/Dockerfile)"
   until docker exec restauro pg_isready -h 127.0.0.1 -U postgres; do sleep 1; done
   docker exec -i restauro psql -U postgres < deploy/compose/initdb.sql
   docker exec -i restauro pg_restore -U postgres --exit-on-error -d vigilume < ~/vigilume-restauro/vigilume.dump
   ```
   As mesmas checagens do backup, com os números da linha `schema N, U usuário(s), A alvo(s)` do
   log daquela execução (`gh run view "$RUN_ID" --repo BipoXDXD/vigilume --log | grep 'schema '`):
   ```bash
   docker exec -i restauro psql -U postgres -d vigilume -v versao=N -v usuarios=U -v alvos=A \
     < deploy/backup/verificar.sql
   ```
   Deve terminar em `NOTICE:  cópia conferida`.
4. Suba o Vigilume **da versão que está no ar** contra a cópia, **só com a API** (`--role=api`):
   com `--role=all`, a cópia sondaria os seus alvos e mandaria alertas aos canais **reais**. A
   imagem é privada: entre no GHCR com o token do `gh`, depois de dar a ele a permissão
   `read:packages` (o `refresh` abre o navegador uma vez), baixe a imagem e **saia logo em
   seguida**:
   ```bash
   gh auth refresh -s read:packages
   SHA=$(ssh vigilume cat /opt/vigilume/versao)
   echo "$SHA"
   gh auth token | docker login ghcr.io -u BipoXDXD --password-stdin
   docker pull "ghcr.io/bipoxdxd/vigilume:$SHA"
   docker logout ghcr.io
   ```
   O `docker login` grava o token no `~/.docker/config.json` do seu computador, **em claro** (o
   próprio Docker avisa: `stored unencrypted`). E esse token não é só de leitura de pacotes: é o
   do `gh`, que escreve em todos os seus repositórios. O `docker logout` o apaga; a imagem já
   baixada continua no seu Docker. Então suba a cópia:
   ```bash
   read -rs -p 'MASTER_KEY de produção (do gerenciador): ' MASTER_KEY; echo
   export MASTER_KEY
   docker run --rm --network host \
     -e DATABASE_URL='postgres://vigilume_app:vigilume_app@127.0.0.1:25433/vigilume?sslmode=disable' \
     -e MASTER_KEY \
     -e JWT_SECRET="$(openssl rand -base64 32)" -e IP_HASH_KEY="$(openssl rand -base64 32)" \
     -e PUBLIC_BASE_URL=http://localhost:8080 -e CORS_ORIGIN=http://localhost:8080 \
     -e HTTP_ADDR=127.0.0.1:8080 -e METRICS_ADDR=off \
     "ghcr.io/bipoxdxd/vigilume:$SHA" --role=api
   ```
   O `read -rs` lê a chave sem eco e sem gravá-la no histórico; o `-e MASTER_KEY` sem valor passa
   a variável ao contêiner sem ela aparecer na linha de comando. A senha `vigilume_app` vale aqui
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
   shred -u ~/vigilume-restauro/vigilume.dump
   docker logout ghcr.io
   grep -q ghcr.io ~/.docker/config.json 2>/dev/null && echo "AINDA LOGADO" || echo "sem login do ghcr"
   ```
   O `docker logout` de novo é por garantia (se o item 4 parou no meio); a última linha tem de
   dizer `sem login do ghcr`.

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
cd ~/Projetos/Go/Vigilume
git switch main && git pull --ff-only
ssh vigilume 'mkdir -p ~/vigilume-arquivos'
scp deploy/vps/vigia.sh deploy/vps/vigilume-vigia.service deploy/vps/vigilume-vigia.timer \
    deploy/vps/vigia.env.exemplo vigilume:vigilume-arquivos/
```

**Depois, na VPS**, os arquivos nos lugares certos e o `vigia.env` nascendo do modelo, sem
valores, só do root:

```bash
cd ~/vigilume-arquivos
sudo install -m 0755 -o root -g root vigia.sh /usr/local/sbin/vigilume-vigia
sudo install -m 0644 -o root -g root vigilume-vigia.service vigilume-vigia.timer /etc/systemd/system/
sudo install -m 0600 -o root -g root vigia.env.exemplo /opt/vigilume/vigia.env
```

(`/usr/local/sbin/vigilume-vigia` é o caminho que o `ExecStart` do `vigilume-vigia.service` espera.)

**Preencha o `vigia.env`.** O domínio vem do `.env` (não é segredo; o `echo` o mostra para você
conferir):

```bash
dom=$(sudo sed -n 's/^VIGILUME_DOMINIO=//p' /opt/vigilume/.env)
echo "$dom"
sudo sed -i "s|^VIGILUME_DOMINIO=\$|VIGILUME_DOMINIO=$dom|" /opt/vigilume/vigia.env
sudo nano /opt/vigilume/vigia.env
```

No editor, cole em `HC_URL=` a ping URL do gerenciador (passo 1.6), sem espaço em volta do `=`.
Salve com `Ctrl+O` e Enter, saia com `Ctrl+X`.

**Por quê:** o vigia recebe **só** este arquivo, com as duas variáveis de que precisa: o domínio e
a URL do healthchecks. O `.env`, com a `MASTER_KEY` e as senhas do banco, nunca chega a ele. O
serviço roda com um usuário criado na hora, sem acesso ao `/opt/vigilume`; o systemd, como root,
lê o `vigia.env` e entrega uma cópia ao vigia (`LoadCredential`).

**Confira** (o `HC_URL` não aparece na tela):

```bash
sudo stat -c '%U %a' /opt/vigilume/vigia.env
sudo grep -oE '^[A-Z_]+=' /opt/vigilume/vigia.env
sudo grep -cE '^[A-Z_]+=$' /opt/vigilume/vigia.env
sudo grep -c '^HC_URL=https://hc-ping.com/' /opt/vigilume/vigia.env
sudo grep -c '^HC_URL=' /opt/vigilume/.env
```

`root 600`; só `VIGILUME_DOMINIO=` e `HC_URL=`; `0` (nenhuma vazia); `1` (a URL do healthchecks
no formato certo); e `0` na última (a `HC_URL` não está no `.env`).

**Ligue o timer e limpe a pasta de trabalho:**

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now vigilume-vigia.timer
rm -r ~/vigilume-arquivos
```

**Por quê:** a cada minuto o timer faz um `curl` em `https://seu-domínio/healthz`, passando por
DNS, certificado, Caddy e app como um visitante; se der certo, pinga a `HC_URL` do `vigia.env`;
se não, pinga `HC_URL/fail`. O healthchecks manda e-mail quando chega um `/fail` ou quando os pings
param (VPS morta, rede fora).

**Confira:**

```bash
systemctl list-timers vigilume-vigia.timer
sudo journalctl -u vigilume-vigia.service -n 5 --no-pager
```

O timer com a próxima execução em menos de um minuto; o diário sem erro. No healthchecks.io, o
check `vigilume-healthz` fica **up** (verde) em até dois minutos.

**Confira que o alarme funciona**, derrubando o app de propósito (na VPS):

```bash
vc stop app
```

Em um ou dois minutos o check fica **down** e chega o e-mail do healthchecks. Então:

```bash
vc start app
```

O check volta a **up**, e chega o e-mail de volta.

---

## Bloco 9: verificações finais

Cada item abaixo é uma das obrigações do Plano 10 (`docs/superpowers/PENDENCIAS-PARA-O-PLANO-11.md`,
"Obrigações do Plano 11") ou um critério da spec. Só dê o Vigilume por pronto com todos verdes.

### 9.1 Só 22, 80 e 443 abertas, vistas de fora (obrigação 3)

**Faça** (no seu computador, **fora** da VPS; pode levar **até meia hora**, porque o firewall
do painel descarta em silêncio as 65 mil portas fechadas e o `nmap` espera cada uma; não
interrompa):

```bash
. ~/.vigilume-vps
sudo nmap -Pn -T4 -p- --open "$VPS"
```

**Deve aparecer:** `22/tcp open ssh`, `80/tcp open http`, `443/tcp open https`, e **nenhuma**
outra porta (sem 5432, sem 8080, sem 9090).

**E por dentro** (na VPS):

```bash
sudo ss -tlnp
```

Escutando em todos os endereços (`0.0.0.0` ou `[::]`): só a 22 (`sshd` ou `systemd`: no
Ubuntu 24.04 o SSH é ativado por socket, e quem escuta a 22 pode aparecer como `systemd`, com ou
sem o `sshd` ao lado) e a 80 e 443 (`docker-proxy`). O resto, se houver, só em `127.0.0.x` (como o DNS local do Ubuntu, `127.0.0.53`).

**Confira no compose:**

```bash
vc config | grep -n -A3 'ports:'
```

Só o serviço `caddy` tem `ports`.

### 9.2 O IP do cliente chega certo (obrigações 1 e 2)

**Faça** (na VPS), depois de ter usado o painel por uns minutos:

```bash
vc logs app | grep -c 'TRUSTED_PROXIES vazio'
vc logs app | grep 'cabeçalho de IP do cliente' | tail -3
```

**Deve aparecer:** `0` e nenhuma linha. O primeiro aviso diria que o app não confia em proxy
nenhum; o segundo (`… ignorado` ou `… ausente ou ilegível`), que o `TRUSTED_PROXIES` não bate com
o IP do Caddy (172.30.0.2).

**O teste de duas redes** (no seu computador; sempre o e-mail `ninguem@exemplo.com`, que não
existe, e senha errada: com o **seu** e-mail, cada erro contaria no bloqueio da conta, 5 em
15 minutos).

O limitador do login deixa **5 tentativas de uma vez** por IP e devolve **uma a cada 6 s**. Por
isso as provas abaixo têm de ser rápidas: depois de 6 s parado, o balde já ganhou uma ficha, e um
`401` não prova mais nada.

**Parte A, de casa: o cabeçalho forjado não escolhe o balde.** Cole o bloco **inteiro de uma
vez** (os sete pedidos saem em poucos segundos, dentro da janela de 6 s):

```bash
. ~/.vigilume-vps
tentar() {
  curl -s -o /dev/null -w '%{http_code}\n' -X POST "https://$DOMINIO/api/v1/auth/login" \
    -H 'Content-Type: application/json' -H 'X-Requested-With: vigilume' \
    -d '{"email":"ninguem@exemplo.com","password":"errada-errada-1234"}' "$@"
}
for i in 1 2 3 4 5 6; do printf '%s: ' "$i"; tentar; done
printf 'forjado: '
tentar -H 'X-Real-IP: 203.0.113.77' -H 'Fly-Client-IP: 203.0.113.77' \
  -H 'CF-Connecting-IP: 203.0.113.77' -H 'X-Forwarded-For: 203.0.113.77'
```

**Deve aparecer:** `1: 401` a `5: 401`, `6: 429` e `forjado: 429`. O pedido forjado diz ser de
outro IP (`203.0.113.77`), mas o Caddy troca o `X-Forwarded-For` e remove os outros três: ele cai
no mesmo balde vazio da sua casa. Se o `forjado` der `401`, o app acreditou no cabeçalho do
cliente e escolheu um balde novo: pare e confira, na VPS, o `Caddyfile` que o Caddy está usando
(`vc exec caddy cat /etc/caddy/Caddyfile`) e o `TRUSTED_PROXIES` (`vc config | grep TRUSTED`).
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

Se o celular levar algum `429`, todos os clientes estão dividindo um balde: o app vê o IP do
Caddy em todo pedido, e o `TRUSTED_PROXIES` do app não é o IP do Caddy. Pare e confira o
`compose.prod.yaml` e o `.env`.

### 9.3 `Host` estranho não chega ao app (obrigação 5, a parte do Caddy)

**Faça** (no seu computador):

```bash
. ~/.vigilume-vps
curl -s -o /dev/null -w '%{http_code}\n' -H 'Host: outro.exemplo' "http://$VPS/healthz"
curl -sk -o /dev/null -w '%{http_code}\n' -H 'Host: outro.exemplo' "https://$DOMINIO/healthz"
curl -sk -o /dev/null -w '%{http_code}\n' "https://$VPS/healthz"; echo "curl saiu com $?"
```

**Deve aparecer:** `308` na primeira (o Caddy manda todo `http` para o `https`, sem passar pelo
app); `200` na segunda, mas **vazio e do próprio Caddy**: nenhum bloco casa com esse `Host`, e o
app não é chamado; e `000` com `curl saiu com 35` na terceira (sem certificado para o IP, o TLS
nem fecha). Para ver que a segunda não veio do app, repita-a com `-D -` no lugar do
`-o /dev/null -w …`: `content-length: 0` e nenhum `x-trace-id` (todo pedido que chega ao app
volta com ele). Nunca `200` com o corpo do Vigilume.

**Por quê:** o Caddy só tem o bloco do seu domínio; uma requisição com outro nome não chega ao app.

### 9.4 Certificado e cabeçalhos

**Faça** (no seu computador):

```bash
. ~/.vigilume-vps
echo | openssl s_client -connect "$DOMINIO:443" -servername "$DOMINIO" 2>/dev/null \
  | openssl x509 -noout -subject -issuer -dates
curl -sI "https://$DOMINIO/" | grep -iE '^(HTTP|strict-transport-security|content-security-policy|x-content-type-options)'
curl -s -o /dev/null -w '%{http_code} %{redirect_url}\n' "http://$DOMINIO/"
```

**Deve aparecer:** `subject` com o seu domínio, `issuer` do Let's Encrypt (ou do ZeroSSL, a
alternativa automática do Caddy) e `notAfter` uns 90 dias à frente; a resposta `200` com
`Strict-Transport-Security`, `Content-Security-Policy` e `X-Content-Type-Options`; e o `http://`
redirecionando (`308`) para `https://`.

### 9.5 Segredos e compose de produção (obrigações 4 e 5)

**Faça** (na VPS):

```bash
vc config --services
vc config | grep -nE 'mem_limit|GOMEMLIMIT|restart:'
vc exec app /vigilume version
```

**Deve aparecer:** os serviços `caddy`, `app` e `db`, e **nenhum** `seed`; um `mem_limit` e um
`restart: unless-stopped` por serviço, e o `GOMEMLIMIT` no app; a versão igual ao
`/opt/vigilume/versao`.

Nenhuma senha de produção está no repositório: o `.env` só existe na VPS e no gerenciador, e o
`gitleaks` do CI varre o histórico em todo PR.

### 9.6 Reboot de teste: tudo volta sozinho

**Faça** (na VPS):

```bash
sudo reboot
```

Espere dois minutos e entre de novo (`ssh vigilume`):

```bash
vc ps
systemctl list-timers vigilume-vigia.timer
sudo ufw status | head -1
sudo fail2ban-client status sshd | head -1
timedatectl | grep synchronized
```

**Deve aparecer:** os três serviços `running`/`healthy`; o timer do vigia agendado; `Status:
active`; `Status for the jail: sshd`; `System clock synchronized: yes`. E, de fora,
`curl -sS -o /dev/null -w '%{http_code}\n' "https://$DOMINIO/readyz"` dá `200`. O healthchecks
pode ter mandado um par de e-mails, **down** e **up** no minuto seguinte: é o custo do reinício
(passo 1.6, "Por quê"), e não uma queda.

### 9.7 Lista final

| Verificação | Passo |
|---|---|
| SSH só por chave, sem root; `fail2ban`; `ufw` e firewall do painel | 2.7–2.10 |
| Atualizações automáticas com reinício às 04:30 | 2.11 |
| Docker com rotação de log, `live-restore` e o store do containerd | 3.2 |
| `.env` do root, `600`, sem variável vazia, copiado no gerenciador | 3.4 |
| Chave privada do `age` só no seu computador e no gerenciador | 3.5 |
| `deploy` preso ao script; túnel recusado | 4.4 |
| Primeiro deploy verde, papéis e dono das tabelas certos | 4.8 |
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

O vigia: `sudo journalctl -u vigilume-vigia.service --since today`. O deploy e o backup: o log
do próprio job no Actions (o script manda o log dele pela saída de erro, que o job mostra) e,
na VPS, o mesmo log em `/opt/vigilume/entrada.log`, de todos os deploys e backups (até ~1 MiB; o
mais velho sai sozinho): `sudo tail -n 50 /opt/vigilume/entrada.log`. É por ele que você sabe
como terminou um deploy cuja conexão caiu (passo 4.8).

### Deploy seguinte

Actions → `ci` na `main` → espere o `publish` verde → Actions → `deploy` →
**Run workflow** na `main`, com o SHA completo do commit (passo 4.8). O script guarda a versão que
estava no ar em `/opt/vigilume/versao-anterior` e mantém as 3 últimas imagens. Durante a troca há
alguns segundos de `502` (o painel mostra "Resposta inesperada do servidor (502)"; recarregue).

Os três arquivos de versão, na VPS, são legíveis sem `sudo` (`0644`: não são segredo):
`/opt/vigilume/versao` (o SHA no ar), `/opt/vigilume/versao-anterior` (o que estava antes) e
`/opt/vigilume/historico` (um SHA por deploy saudável).

### Voltar uma versão

Rode o mesmo workflow com o SHA anterior (`cat /opt/vigilume/versao-anterior` na VPS). Só é
seguro se as migrations da versão nova forem **aditivas**: o `goose` não roda `Down` sozinho, e o
rollback automático do script também não desfaz migration.

**Se sair 3 com `vigilume:<sha> já existe com outra imagem`:** uma tag `v*` publicada depois
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
   ANT=$(cat /opt/vigilume/versao-anterior)
   ATUAL=$(cat /opt/vigilume/versao)
   echo "no ar: $ATUAL; voltar para: $ANT"
   sudo docker image inspect -f '{{.Id}}' "vigilume:$ANT"
   ```
   **Deve aparecer:** os dois SHAs e um `sha256:…`. Se disser `No such image`, a imagem já foi
   podada: só o caminho 1 serve. Então troque, sob a mesma trava do deploy e do backup (se um
   deles estiver rodando, o comando espera até 10 min):
   ```bash
   sudo ANT="$ANT" ATUAL="$ATUAL" flock -w 600 /opt/vigilume/.entrada.trava sh -ec '
     docker tag "vigilume:$ANT" vigilume:atual
     docker compose -f /opt/vigilume/compose.prod.yaml up -d --force-recreate --wait --wait-timeout 90 app
     echo "$ATUAL" > /opt/vigilume/versao-anterior
     echo "$ANT" > /opt/vigilume/versao
     echo "no ar: $ANT"
   '
   ```
   **Deve aparecer:** o `app` `Healthy` e `no ar: <o SHA anterior>`; `cat /opt/vigilume/versao`
   mostra o mesmo. Os dois arquivos de versão são atualizados à mão porque o próximo deploy os
   usa para saber para onde voltar se ele mesmo falhar. Se o `up` não ficar saudável, o comando
   para antes de mexer neles; volte para a versão que estava no ar com
   `sudo docker tag "vigilume:$ATUAL" vigilume:atual` e `vc up -d app`, e siga pelo caminho 1.

Refazer o deploy do SHA que **já está no ar** depois de um `v*` também sai 3 com essa mensagem:
não há o que fazer, a versão no ar é o mesmo código.

### Depuração do deploy

Os códigos de saída do `vigilume-entrada deploy`, mais o 255 do próprio SSH. O job termina com um
resumo do código (`deploy/ci/vps.sh`), mas a razão exata é a linha que começa com
`vigilume-entrada:` no log do job (ou no fim do `/opt/vigilume/entrada.log`, na VPS). Procure-a
na coluna do meio:

| Código | A linha `vigilume-entrada:` diz | O que fazer |
|---|---|---|
| 0 | `deploy de <sha> saudável` | nada |
| 2 | `pedido vazio`, `caractere não permitido no pedido`, `verbo desconhecido`, `deploy leva o SHA e o image-id`, `SHA malformado`, `image-id malformado` ou `argumento na linha de comando não é aceito` | conferir o SHA passado ao workflow (os 40 caracteres do commit, passo 4.8) |
| 2 | `nada chegou na entrada padrão` | o tar não chegou: veja no job se o passo que baixa a imagem falhou; rode de novo |
| 2 | `a imagem passa de 2147483648 bytes` | a imagem normal tem dezenas de MB: o `publish` produziu algo errado; não force, investigue |
| 3 | `vigilume:<sha> já existe com outra imagem (…)` | só com o SHA no ar ou o anterior, depois de um `v*`: **rodar de novo não resolve**; veja "Voltar uma versão" acima |
| 3 | `a imagem carregada é …, e o deploy pediu …`, `o index.json do tar não é uma imagem só, sem nome, com o manifesto …`, `o tar não trouxe exatamente uma imagem sem nome`, `o que chegou não é um tar legível`, ou outra que começa com `o tar` (layout OCI, membro repetido ou estranho; as duas de disco cheio são código 1) | rodar de novo **uma vez**; se repetir, a imagem mudou no caminho (ou o formato do `crane` mudou): não force, investigue |
| 4 | `rollback feito: <anterior> de volta no ar; <sha> não subiu` | a anterior já voltou: o log do job traz o log do app; corrija e publique outra versão |
| 4 | `primeiro deploy e sem versão anterior: o app ficou parado` | **o site está fora** (não há anterior): passo 4.8, "Se o primeiro deploy sair com 4" |
| 1 | `o rollback para <anterior> também não ficou saudável: veja docs/deploy/vps.md` | **o site está fora**: "O rollback também não ficou saudável", logo depois desta seção |
| 1 | `o jq não está instalado (…): veja docs/deploy/vps.md` | `sudo apt install -y jq` (passo 3.3) e rode de novo |
| 1 | `o Docker não usa o image store containerd; veja docs/deploy/vps.md (Docker)` | `sudo docker info -f '{{.DriverStatus}}'` e o `daemon.json` do passo 3.2 |
| 1 | `outra operação da entrada não terminou em 10 min` | um backup (ou outro deploy) segurou a trava: veja o `entrada.log`, espere terminar e rode de novo |
| 1 | `o tar não se extraiu …` ou `o tar refeito não se gravou …` (`disco cheio?`) | disco: `df -h /opt/vigilume /var/lib/docker` (veja "Operação do Postgres", linha Disco) |
| 1 | outra | a mensagem diz qual (Docker parado: `sudo systemctl status docker`) |
| 255 | (nenhuma: o SSH não chegou ao script, ou a conexão caiu no meio) | se o job caiu **depois** de mandar a imagem, o deploy pode ter terminado na VPS: confira `cat /opt/vigilume/versao` e o `entrada.log` antes de repetir (passo 4.8). Se nem conectou: VPS fora do ar, porta 22 fechada, chave do servidor diferente do `VPS_HOST_KEY` (VPS reinstalada?) ou chave do deploy recusada; `ssh vigilume true` do seu computador; se a VPS foi reinstalada, passos 4.2 a 4.6 de novo |

O `backup` usa os mesmos códigos do `deploy/backup/backup.sh`: 0, cópia restaurada e conferida;
3, uma checagem da cópia falhou (o dump não serve, e nada é guardado); 4, conferido e guardado,
mas o disco passou de 85% (o job fica vermelho no fim, de propósito); 1, outro erro (não houve
backup), inclusive `outra operação da entrada não terminou em 10 min` (um deploy segurou a
trava: rode de novo); 255, o SSH não chegou ao script.

A imagem do app **não tem shell** (distroless): `vc exec app sh` não funciona. O que serve: os
logs, os subcomandos do binário (`vc exec app /vigilume version`, `… healthcheck`, `… user …`) e o
`psql` do banco (`vc exec db psql -U postgres -d vigilume`). O `vigilume healthcheck` consulta o
`/readyz` com prazo de 3 s:

| Código | Quando |
|---|---|
| 0 | o `/readyz` respondeu 200 dentro dos 3 s |
| 1 | qualquer outra resposta, prazo esgotado, conexão recusada ou `HTTP_ADDR` inválido |
| 2 | argumento a mais (`vigilume healthcheck x`); o `vigilume version` faz o mesmo |
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
df -h /opt/vigilume /var/lib/docker
```

- `db` fora de `healthy`, ou erro de conexão/senha no log do app: o banco (veja o log do `db`; um
  `vc up -d db` o sobe de novo se ele tiver parado).
- Disco a 100%: libere espaço (linha Disco de "Operação do Postgres").
- Erro de variável no boot do app: o `.env` mudou (`sudo nano /opt/vigilume/.env`, compare com o
  gerenciador).

Corrigida a causa, suba a última versão saudável (a etiqueta `vigilume:atual`, que só anda num
deploy saudável):

```bash
vc up -d --wait app
cat /opt/vigilume/versao
```

O `app` `Healthy`, e o `versao` é o SHA dela. Se não ficar saudável, o log do `vc logs --tail 100
app` diz o que ainda falta. Só depois disso tente de novo o deploy da versão nova.

### Arquivos da VPS: quando algo em `deploy/vps/` muda na `main`

O deploy troca só a imagem do app. Os arquivos abaixo foram copiados à mão (passos 3.3, 3.5, 4.1 e
8) e **não** se atualizam sozinhos. Quando um PR mudar um deles, recopie o que mudou do mesmo
jeito: no seu computador, na `main` atualizada, o `scp` para `vigilume:vigilume-arquivos/`; na
VPS, o `sudo install` para o destino da tabela, com o modo dela e dono `root`; e aplique:

| Arquivo do repositório | Destino na VPS | Modo | Para valer |
|---|---|---|---|
| `deploy/vps/compose.prod.yaml` | `/opt/vigilume/compose.prod.yaml` | `0644` | `vc up -d` |
| `deploy/vps/Caddyfile` | `/opt/vigilume/Caddyfile` | `0644` | `vc up -d --force-recreate caddy` e a conferência abaixo |
| `deploy/vps/initdb.sh` | `/opt/vigilume/initdb.sh` | `0755` | nada: só roda num banco **vazio**, e trocá-lo não muda um banco que já existe |
| `deploy/vps/env.exemplo` | nenhum: é o modelo do `/opt/vigilume/.env` | — | variável nova no modelo: acrescente-a ao `.env` (`sudo nano /opt/vigilume/.env`) e ao gerenciador; variável que saiu: apague-a do `.env` |
| `deploy/vps/vigilume-entrada` | `/usr/local/sbin/vigilume-entrada` | `0755` | nada: vale no próximo deploy ou backup |
| `deploy/vps/sudoers-vigilume-entrada` | `/etc/sudoers.d/vigilume-entrada` | `0440` | `sudo visudo -cf` **antes** do `install` e `sudo visudo -c` depois (passo 4.1) |
| `deploy/vps/vigia.sh` | `/usr/local/sbin/vigilume-vigia` | `0755` | nada: vale na próxima execução do timer |
| `deploy/vps/vigilume-vigia.service` e `vigilume-vigia.timer` | `/etc/systemd/system/` | `0644` | `sudo systemctl daemon-reload && sudo systemctl restart vigilume-vigia.timer` |
| `deploy/vps/vigia.env.exemplo` | nenhum: é o modelo do `/opt/vigilume/vigia.env` | — | variável nova no modelo: acrescente-a ao `vigia.env` (`sudo nano /opt/vigilume/vigia.env`) |
| `deploy/backup/backup.sh` e `deploy/backup/verificar.sql` | `/opt/vigilume/deploy/backup/` | `0644` | nada: vale no próximo backup |
| `deploy/compose/initdb.sql` | `/opt/vigilume/deploy/compose/` | `0644` | nada: vale no próximo backup |

Os pacotes do host que o `vigilume-entrada` e o vigia usam (`age`, `curl`, `jq`, `util-linux`,
passo 3.3) vêm do Ubuntu e se atualizam sozinhos (passo 2.11). Se um PR passar a exigir um pacote
novo, a descrição dele diz qual: `sudo apt install -y <pacote>` antes de recopiar o script.

**Por que o Caddy precisa de `--force-recreate`:** o `Caddyfile` entra no contêiner como um
arquivo só (`./Caddyfile:/etc/caddy/Caddyfile:ro`). O `sudo install` põe um arquivo **novo** no
lugar do velho, e o contêiner que já está de pé continua vendo o velho; o `vc up -d` sozinho não
recria um serviço cujo compose não mudou. Sem recriar, uma correção de segurança no `Caddyfile`
fica instalada no disco e sem efeito nenhum. **Confira** depois de todo `Caddyfile` novo (na VPS):

```bash
vc up -d --force-recreate caddy
vc exec -T caddy cat /etc/caddy/Caddyfile | sudo diff - /opt/vigilume/Caddyfile && echo "Caddy com o arquivo novo"
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
vc exec app /vigilume user reset-2fa --email voce@exemplo.com
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

Esqueceu a senha: `vc exec app /vigilume user set-password --email voce@exemplo.com` (pede a
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
   vc exec -T db pg_dump -U postgres --format=custom vigilume | sudo tee /root/vigilume-antes-do-19.dump >/dev/null
   sudo ls -l /root/vigilume-antes-do-19.dump
   ```
3. Por PR, a imagem nova em `deploy/vps/db/Dockerfile` e nos outros lugares do digest, e o
   `compose.prod.yaml` apontando o `db` para um **volume novo** (hoje o volume se chama `pgdata`, `vigilume_pgdata` no `docker volume ls`: montado
   como `pgdata:/var/lib/postgresql` no serviço `db` e declarado no bloco `volumes:` do fim do
   arquivo; o novo, por exemplo `pgdata19`, troca o nome nos dois lugares), para o volume velho continuar
   intacto. Recopie o `compose.prod.yaml` e suba **só o banco**: `vc up -d db` (o `initdb.sh`
   cria os papéis no volume novo com as senhas do `.env`).
4. Restaure no banco novo como superusuário, preservando os donos, e apague o dump:
   ```bash
   sudo -v
   sudo cat /root/vigilume-antes-do-19.dump | vc exec -T db pg_restore -U postgres --exit-on-error -d vigilume
   sudo shred -u /root/vigilume-antes-do-19.dump
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
2. Se não houver backup da Hostinger que sirva: reinstale o Ubuntu 24.04 (ou crie outra VPS) e
   refaça o bloco 2, o bloco 3 e o bloco 4 **até o 4.7** (o 4.8 é o item 3 abaixo). Nos passos
   que guardam segredo, **não gere nada novo; restaure do gerenciador**:
   - **3.4:** pule o bloco que gera os segredos. Abra o `.env` (`sudo nano /opt/vigilume/.env`) e
     cole **todos** os valores do gerenciador (a `MASTER_KEY`, as três senhas do Postgres, tudo).
     Com outra `MASTER_KEY`, o banco restaurado teria headers e canais ilegíveis; com outras
     senhas, os papéis não bateriam com o dump.
   - **3.5:** **não** rode o `age-keygen -o`: uma chave nova não abre os dumps que já existem, e o
     item 4 falharia. Se o `~/vigilume-backup.key` ainda existe no seu computador, use-o; se não
     (o computador também foi trocado), recrie-o com o conteúdo da entrada "Vigilume – chave
     privada do backup (age)". Crie o arquivo **vazio e já `600`** antes de abrir o editor, para
     a chave nunca ficar num arquivo que outro usuário leia, e cole nele:
     ```bash
     install -m 600 /dev/null ~/vigilume-backup.key
     nano ~/vigilume-backup.key
     stat -c '%a' ~/vigilume-backup.key
     ```
     (cole, `Ctrl+O`, Enter, `Ctrl+X`; o `stat` tem de dizer `600`). Depois, só a pública, a
     partir dela:
     `age-keygen -y ~/vigilume-backup.key > ~/vigilume-age-recipient.txt`, e siga o 3.5 do
     "Leve a pública para a VPS" em diante (o `echo "ida e volta"` confere o par).

   O IP pode mudar: atualize o registro `A` (passo
   1.4), o `~/.vigilume-vps`, o `~/.ssh/config`, e grave de novo `VPS_HOST` e `VPS_HOST_KEY`
   (passos 4.5 e 4.6; a VPS nova tem outra chave de servidor, e o `ssh` do seu computador vai
   reclamar dela: `ssh-keygen -R <IP velho>` e confira a nova pelo terminal do painel).
3. Faça o primeiro deploy (passo 4.8) com o SHA que estava no ar (o último deploy verde do Actions).
4. Restaure o último dump do Actions no banco novo: pare o app (`vc stop app`), decifre o dump no
   seu computador (passo 7.2, itens 1 e 2), leve-o para a VPS e apague a cópia em claro do seu
   computador:
   ```bash
   scp ~/vigilume-restauro/vigilume.dump vigilume:
   shred -u ~/vigilume-restauro/vigilume.dump
   ```
   Depois, na VPS:
   ```bash
   vc exec -T db dropdb -U postgres vigilume
   vc exec -T db psql -U postgres -c "CREATE DATABASE vigilume OWNER vigilume_app;"
   vc exec -T db pg_restore -U postgres --exit-on-error -d vigilume < ~/vigilume.dump
   shred -u ~/vigilume.dump
   vc start app
   ```
   (O banco é recriado vazio porque o app já migrou um schema no primeiro deploy.)
5. Refaça o **bloco 8** inteiro (o vigia, com o `vigia.env`: a `HC_URL` vem do gerenciador,
   entrada "Vigilume – HC_URL"), até o healthchecks voltar a **up**. Sem ele, a VPS nova fica sem
   vigia justamente depois de um desastre, e o `down` do healthchecks parece ser só a
   reconstrução. Não pause nem silencie o check no healthchecks: ele volta a **up** sozinho quando
   o vigia novo pingar.
6. Confira os passos 4.8 (tabelas e papéis), 5.2 (entrar com 2FA), 6.1 (e-mail) e rode o
   `backup` à mão (passo 7.1). Perde-se no máximo o que aconteceu desde o dump de ontem (checks e
   incidentes).

### Trocar um segredo

- `JWT_SECRET`: gere outro (`openssl rand -base64 32`, no `.env` pelo `nano`), guarde no
  gerenciador, `vc up -d app`. Todas as sessões caem; entra-se de novo.
- `IP_HASH_KEY`: o mesmo caminho; os hashes de IP antigos da auditoria deixam de casar com os
  novos.
- `HC_URL` (um check novo no healthchecks, ou a URL vazou): troque-a no `vigia.env`
  (`sudo nano /opt/vigilume/vigia.env`) e no gerenciador. Vale na próxima execução do timer, sem
  reiniciar nada.
- `MASTER_KEY` e as senhas do Postgres: **não** há rotação pronta. A `MASTER_KEY` cifra os
  segredos gravados; as senhas dos papéis vivem também no banco (trocar exige `ALTER ROLE` no
  mesmo momento). Se uma delas vazar, é trabalho de plano próprio.
- A chave de deploy do GitHub: passos 4.3 a 4.6 com uma chave nova (a linha antiga do
  `authorized_keys` é substituída).

### Minutos de Actions

Com o repositório privado, o CI gasta a cota gratuita (2.000 min/mês na conta pessoal): ~15 min de
runner por push somando os jobs, dos quais o `e2e` é o maior, e alguns minutos por dia do
`backup`. Um push novo no mesmo branch cancela a execução anterior. Se a cota apertar, o primeiro
corte é o `e2e` só na `main` (`if: github.event_name == 'push'` no job).

## Tornar o repositório público (passo futuro)

O repositório é **privado por decisão do dono**. No dia de abrir:

1. Varredura do histórico inteiro: `gitleaks git . --redact -v` (o job `secrets` do CI já faz
   isso em todo PR). O falso positivo conhecido está no `.gitleaksignore`, com o motivo.
2. **Se aparecer um segredo real:** considere-o vazado e **rotacione primeiro** (Resend, Discord,
   o que for). Depois, tire-o do histórico com `git filter-repo --replace-text` num clone novo,
   `git push --force` de todos os branches e tags, e rode a varredura de novo até zerar.
   Reescrever a história não desfaz um vazamento.
3. O e-mail pessoal do autor está no histórico e **fica** (decisão do dono).
4. Settings → General → Change visibility. O pacote do GHCR tem visibilidade própria: Package
   settings → Change visibility → Public. Confira de uma máquina sem login:
   `docker pull ghcr.io/bipoxdxd/vigilume:latest`.
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
