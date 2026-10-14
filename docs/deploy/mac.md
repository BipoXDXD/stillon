# O runbook da VPS num Mac

O [runbook](vps.md) foi escrito para um computador com Ubuntu. Num Mac, **tudo o que roda "na VPS"
fica igual** (a VPS continua sendo o Ubuntu da Hostinger); só muda o que roda **"no seu
computador"**. Esta página diz o que trocar, na ordem dos passos. O que não aparece aqui se faz no
Mac exatamente como está no runbook.

## Antes de tudo (no lugar do passo 0.2)

1. **Terminal.** Abra o app **Terminal** (`Cmd+Espaço`, digite "Terminal"). Colar é `Cmd+V`, e não
   `Ctrl+Shift+V`. No `nano` (que você usa **na VPS**), salvar e sair continuam sendo `Ctrl+O` e
   `Ctrl+X`: é a tecla `Control`, não a `Command`.
2. **bash, não zsh.** O Terminal do Mac abre o `zsh`; o runbook foi escrito para o `bash`, e ao
   menos um comando dele (o `read -rs -p` do passo 7.2) quebra no `zsh`
   (`read: -p: no coprocess`). **Em todo terminal novo**, antes de qualquer comando do runbook:

   **1/2 · Mac.** Rode só este e espere a saída:

   ```bash
   bash
   ```

   **2/2 · Mac.** Rode só este e espere a saída:

   ```bash
   . ~/.stillon-vps
   ```

   O `bash` que vem no macOS é antigo (3.2), mas serve para tudo o que o runbook usa.
3. **Homebrew**, o instalador de programas do Mac: siga o comando da página inicial de
   <https://brew.sh> e, no fim, os dois comandos de "Next steps" que ele imprime (põem o `brew`
   no `PATH`).
4. **As ferramentas** (`ssh`, `scp`, `curl`, `openssl`, `dig` e `git` já vêm no macOS; o Xcode
   Command Line Tools pode pedir para instalar o `git` na primeira vez):

   **1/2 · Mac.** Rode só este e espere a saída:

   ```bash
   brew install age nmap gh
   ```

   **2/2 · Mac.** Rode só este e espere a saída:

   ```bash
   gh auth login
   ```

   No `gh auth login`: GitHub.com, HTTPS, e entrar pelo navegador.
5. **Docker Desktop.** Instale pelo site da Docker
   ([Mac](https://docs.docker.com/desktop/setup/install/mac-install/); escolha **Apple Silicon** ou
   **Intel** conforme o Mac, em  → Sobre Este Mac), abra-o e **entre na sua conta Docker** nele.
   Depois, em **Settings → Resources → Network**, marque **Enable host networking** e clique em
   **Apply and restart** (precisa do Docker Desktop 4.34 ou mais novo, e de ficar logado; fonte:
   Docker, [Host network driver](https://docs.docker.com/engine/network/drivers/host/#docker-desktop)).
   Sem isso, o `docker run --network host` do passo 7.2 não enxerga o `127.0.0.1` do Mac (há uma
   variante mais abaixo, se não quiser ligar a opção). No Mac não existe grupo `docker`: se o
   `docker` disser que não acha o `docker.sock`, é o Docker Desktop fechado.
6. **O clone, no mesmo caminho do runbook:**

   **1/2 · Mac.** Rode só este e espere a saída:

   ```bash
   mkdir -p ~/Projetos/Go
   ```

   **2/2 · Mac.** Rode só este e espere a saída:

   ```bash
   git clone https://github.com/BipoXDXD/stillon.git ~/Projetos/Go/StillOn
   ```

   Se o clone já existe em outro lugar (no Mac do dono é `~/Documents/Projetos/Go/StillOn`), não
   clone de novo: use o caminho dele onde o runbook diz `~/Projetos/Go/StillOn` (blocos 0.2, 3.3,
   4.8, 4.9, 7.2 e 8). E se ele estiver em outro branch, o aviso do `git pull --ff-only` vale
   também para o 3.3: use a variante com `git archive origin/main` do bloco 8 do runbook.

**Confira:** os mesmos quatro comandos do **Confira** do passo 0.2 (`age --version`, `nmap`,
`git -C … remote -v` e `docker run --rm hello-world`, este sem `sudo`).

## Trocando de computador no meio do runbook

O que está nos painéis (Hostinger, DNS, Resend, healthchecks.io), no GitHub e no gerenciador de
senhas vem junto: nada disso se refaz. O que mora só no computador antigo, sim:

| Arquivo | Quando existe | No Mac |
|---|---|---|
| `~/.stillon-vps` | desde o 0.3 | Refaça o passo 0.3 com os **mesmos** valores: o IPv4 está no **Overview** da VPS no painel da Hostinger; o domínio é o do 1.4; o `EU` é o nome que você escolheu para o usuário da VPS (anote-o no gerenciador, se ainda não anotou). |
| `~/.ssh/stillon_admin` e `.pub` | desde o 1.1 | **Não gere outra chave:** a pública já está no painel. Restaure a privada do gerenciador, como abaixo. |
| `~/.ssh/config` com `Host stillon` | desde o 2.6 | Refaça o passo 2.6 (se o Mac nunca teve um `Host stillon`, rode o bloco dele como está). |
| `~/stillon-backup.key` | desde o 3.5 | Restaure do gerenciador, como abaixo, trocando o nome do arquivo; a pública sai com `age-keygen -y ~/stillon-backup.key > ~/stillon-age-recipient.txt`. |

**Restaurar a chave SSH do administrador** (entrada "StillOn – chave SSH do administrador"):

**1/3 · Mac.** Rode só este e espere a saída:

```bash
mkdir -p ~/.ssh && chmod 700 ~/.ssh
```

**2/3 · Mac.** Rode só este e espere a saída:

```bash
install -m 600 /dev/null ~/.ssh/stillon_admin
```

**3/3 · Mac.** Rode só este e espere a saída:

```bash
nano ~/.ssh/stillon_admin
```

Cole o conteúdo inteiro da entrada, da linha `-----BEGIN OPENSSH PRIVATE KEY-----` até a
`-----END OPENSSH PRIVATE KEY-----`, salve (`Ctrl+O`, Enter) e saia (`Ctrl+X`). Depois, a pública:

**1/3 · Mac.** Rode só este e espere a saída:

```bash
ssh-keygen -y -f ~/.ssh/stillon_admin > ~/.ssh/stillon_admin.pub
```

**2/3 · Mac.** Rode só este e espere a saída:

```bash
cat ~/.ssh/stillon_admin.pub
```

**3/3 · Mac.** Rode só este e espere a saída:

```bash
ls -l ~/.ssh/stillon_admin
```

**Deve aparecer:** o `ssh-keygen` pede a passphrase (a da entrada "StillOn – passphrase da chave
SSH"); o `cat` mostra `ssh-ed25519 AAAA…`, com o mesmo `AAAA…` da chave cadastrada no painel da
Hostinger (**Settings → SSH keys**); o `ls` mostra `-rw-------`. Se o `ssh-keygen` disser
`invalid format` ou `error in libcrypto`, a colagem perdeu as quebras de linha ou a última linha:
abra de novo com o `nano` e confira que cada linha ficou como está no gerenciador.

**Se o `ssh-keygen` disser `incorrect passphrase supplied to decrypt private key`** mesmo com a
frase **colada** do gerenciador (e não digitada), a frase guardada não é a desta chave, e não há
como recuperá-la.
Antes do bloco 2 isso custa pouco (a chave ainda não entrou em lugar nenhum além do painel): gere
a frase nova no gerenciador, apague `~/.ssh/stillon_admin*`, refaça o passo 1.1 **colando** a
frase, guarde a privada nova no gerenciador e confira que a entrada ficou igual ao arquivo, sem
depender da área de transferência (ela guarda só o último texto copiado, e o último copiado é um
comando deste runbook). Um comando por vez: o 1/7 copia a chave para você colar na entrada do
gerenciador; o 2/7 limpa a área de transferência; do 3/7 ao 5/7, abra a entrada no gerenciador,
copie o conteúdo e cole-o no editor do `nano` (salve com `Ctrl+O` e Enter, saia com `Ctrl+X`),
e o `diff` do 5/7 diz `iguais`; o 7/7 limpa de novo a área de transferência, que ficou com a
chave copiada do gerenciador:

**1/7 · Mac.** Rode só este e espere a saída:

```bash
pbcopy < ~/.ssh/stillon_admin
```

**2/7 · Mac.** Rode só este e espere a saída:

```bash
pbcopy < /dev/null
```

**3/7 · Mac.** Rode só este e espere a saída:

```bash
install -m 600 /dev/null ~/conferir.key
```

**4/7 · Mac.** Rode só este e espere a saída:

```bash
nano ~/conferir.key
```

**5/7 · Mac.** Rode só este e espere a saída:

```bash
diff ~/conferir.key ~/.ssh/stillon_admin && echo iguais
```

**6/7 · Mac.** Rode só este e espere a saída:

```bash
rm ~/conferir.key
```

**7/7 · Mac.** Rode só este e espere a saída:

```bash
pbcopy < /dev/null
```

Por fim, no painel (**Settings → SSH keys**), apague a `stillon-admin` velha e cadastre a nova.
Foi o que aconteceu em 2026-09-28. Depois do bloco 2, o caminho é o de "Perdi a chave SSH"
(bloco 10).

**A primeira conexão do Mac com a VPS** pergunta a impressão digital de novo (o `known_hosts` é
de cada computador). Confira-a como no passo 2.1, pelo terminal do painel, antes do `yes`. Se o
passo 2.7 já foi feito, a conexão é pelo seu usuário (`ssh stillon`), não pelo root.

**Opcional:** para o Mac lembrar a passphrase no Chaveiro, acrescente ao bloco `Host stillon` do
`~/.ssh/config` as linhas `UseKeychain yes` e `AddKeysToAgent yes`, e rode uma vez
`ssh-add --apple-use-keychain ~/.ssh/stillon_admin`.

## Troca por troca

| Passo | No Linux (como está no runbook) | No Mac |
|---|---|---|
| 0.2 | `sudo apt install -y openssh-client age nmap git curl` | `brew install age nmap gh` (seção acima) |
| 0.2 | `sudo usermod -aG docker "$USER"` | Não existe: abra o Docker Desktop |
| 1.1 | `saved in /home/…/.ssh/stillon_admin` | `saved in /Users/…/.ssh/stillon_admin` |
| 1.4 e 4.8 (**Antes**) | `getent ahostsv4 "$DOMINIO" \| head -1` | `dig +short A "$DOMINIO"` (deve mostrar só o IP da VPS) |
| 4.6, 7.2 (item 6) e bloco 10 | `shred -u <arquivo>` | `rm <arquivo>`, com o **FileVault** ligado (Ajustes do Sistema → Privacidade e Segurança → FileVault) |
| Bloco 10, "A VPS morreu" | `stat -c '%a' ~/stillon-backup.key` | `stat -f '%Lp' ~/stillon-backup.key` (também tem de dizer `600`) |
| 7.2 (item 4) | `docker run --network host …` | Igual, com o **Enable host networking** ligado (seção acima); sem ele, a variante abaixo |
| 7.2 (itens 4 e 6) | `grep -q ghcr.io ~/.docker/config.json` prova que saiu do GHCR | Não prova nada: o Docker Desktop guarda o login no Chaveiro (`credsStore: desktop`). Use `docker-credential-desktop list`, que não pode mostrar o `ghcr.io` |
| 9.1 | `sudo nmap …` | `nmap -Pn -T4 -p- --open -sT` (varredura de conexão, sem `sudo`; mais lenta e basta). Com `sudo`, o SYN scan também funciona |

**Por que o `shred` sai:** o macOS não tem `shred`, e num SSD sobrescrever o arquivo não garante
que os blocos antigos sumam (o disco grava em outro lugar). Quem protege o que foi apagado, no Mac,
é o disco cifrado pelo FileVault.

### 7.2, item 4, sem o host networking

Se preferir não ligar o **Enable host networking**, suba a cópia assim, no lugar dos comandos do
item 4 que vão do `read` ao `docker run` (o resto do item 4 e o item 5 ficam iguais). Ela sobe em
segundo plano e com nome (`restauro-app`); o item 6 a remove com
`docker rm -f restauro-app restauro`, para a cópia com a `MASTER_KEY` não ficar de pé:

**1/17 · Mac.** Rode só este e espere a saída:

```bash
read -rs -p 'MASTER_KEY de produção (do gerenciador): ' MASTER_KEY && echo
```

**2/17 · Mac.** Rode só este e espere a saída:

```bash
export MASTER_KEY
```

**3/17 · Mac.** Rode só este e espere a saída:

```bash
export JWT_SECRET="$(openssl rand -base64 32)"
```

**4/17 · Mac.** Rode só este e espere a saída:

```bash
export IP_HASH_KEY="$(openssl rand -base64 32)"
```

**5/17 · Mac.** Rode só este e espere a saída:

```bash
cd ~/stillon-restauro
```

**6/17 · Mac.** Rode só este e espere a saída:

```bash
echo 'MASTER_KEY' > app.env
```

**7/17 · Mac.** Rode só este e espere a saída:

```bash
echo 'JWT_SECRET' >> app.env
```

**8/17 · Mac.** Rode só este e espere a saída:

```bash
echo 'IP_HASH_KEY' >> app.env
```

**9/17 · Mac.** Rode só este e espere a saída:

```bash
echo 'PUBLIC_BASE_URL=http://localhost:8080' >> app.env
```

**10/17 · Mac.** Rode só este e espere a saída:

```bash
echo 'CORS_ORIGIN=http://localhost:8080' >> app.env
```

**11/17 · Mac.** Rode só este e espere a saída:

```bash
echo 'HTTP_ADDR=:8080' >> app.env
```

**12/17 · Mac.** Rode só este e espere a saída:

```bash
echo 'METRICS_ADDR=off' >> app.env
```

**13/17 · Mac.** Rode só este e espere a saída:

```bash
B=postgres://stillon_app:stillon_app@host.docker.internal:25433
```

**14/17 · Mac.** Rode só este e espere a saída:

```bash
echo "DATABASE_URL=$B/stillon?sslmode=disable" >> app.env
```

**15/17 · Mac.** Rode só este e espere a saída:

```bash
A="ghcr.io/bipoxdxd/stillon:$SHA"
```

**16/17 · Mac.** Rode só este e espere a saída:

```bash
O="-d --rm --name restauro-app -p 127.0.0.1:8080:8080"
```

**17/17 · Mac.** Rode só este e espere a saída:

```bash
docker run $O --env-file app.env $A --role=api
```

**Por quê:** no Docker Desktop, os contêineres rodam numa máquina virtual; o `127.0.0.1` de dentro
deles é o da máquina virtual, não o do Mac. O `host.docker.internal` é o Mac visto de dentro do
contêiner (onde o `restauro` publicou a `25433`), o `HTTP_ADDR=:8080` deixa o app aceitar a porta
publicada, e o `-p 127.0.0.1:8080:8080` a publica só no `127.0.0.1` do Mac: a cópia continua
invisível para a rede. A imagem publicada tem as variantes `linux/amd64` e `linux/arm64`: roda
nativa num Mac Apple Silicon.
