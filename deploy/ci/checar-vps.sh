#!/usr/bin/env bash
# Checagens da produção na VPS (Plano 11, spec §8; spec 2026-10-13-vps-
# compartilhada), sem VPS: sobe uma borda de teste igual ao contrato
# (deploy/ci/vps/borda, CA interna, domínio stillon.example) e, atrás dela,
# o deploy/vps/compose.prod.yaml, e confere o que a revisão exigiu da
# hospedagem. O job "vps" do CI roda; localmente, da raiz do repositório:
#
#   bash deploy/ci/checar-vps.sh <imagem do app>     (ex.: stillon:dev)
#
# Precisa de docker (com o image store containerd, como a VPS), jq, curl,
# openssl, python3 (monta os tars maliciosos) e do age/age-keygen no PATH.
# Os projetos do compose são "stillon-teste-vps" e "stillon-teste-borda", e
# as portas da borda vão para o loopback (TESTE_VPS_HTTP, padrão 18580;
# TESTE_VPS_HTTPS, padrão 18543). As redes são as de produção (edge,
# 172.31.0.0/24, e 10.30.0.0/24): não rode numa máquina que já tenha a borda
# ou o StillOn de produção de pé.
# Derruba tudo no fim (TESTE_VPS_MANTER=1 deixa de pé, para investigar).
# O script escreve outros scripts e procura ${...} em arquivos: muita string
# com $ literal entre aspas simples, de propósito.
# shellcheck disable=SC2016
set -euo pipefail

IMAGEM_APP=${1:?uso: checar-vps.sh <imagem do app>}
# As ferramentas, antes de subir qualquer coisa: sem uma delas, o teste
# morreria no meio (127) com a pilha de pé.
for f in docker jq curl openssl python3 age age-keygen awk sed tar sha1sum base64 od flock timeout; do
    command -v "$f" >/dev/null || { echo "checar-vps.sh: falta '$f' no PATH (veja o cabeçalho)" >&2; exit 2; }
done
docker compose version >/dev/null 2>&1 || { echo "checar-vps.sh: falta o plugin docker compose" >&2; exit 2; }
RAIZ=$(cd "$(dirname "$0")/../.." && pwd)
HTTP=${TESTE_VPS_HTTP:-18580}
HTTPS=${TESTE_VPS_HTTPS:-18543}
DOMINIO=stillon.example
PROJETO=stillon-teste-vps
REPO_TESTE=stillon-teste-vps
EMAIL=dono@stillon.example
SENHA=senha-do-teste-da-vps-2026

falhas=0
falhar() { echo "FALHOU: $*" >&2; falhas=$((falhas + 1)); }
# "ok" só quando o bloco que ele fecha não falhou.
falhas_antes=0
ok() {
    [ "$falhas" = "$falhas_antes" ] && echo "ok: $*"
    falhas_antes=$falhas
}

# O diretório que faz o papel de /opt/stillon.
DIR=$(mktemp -d)
TERCEIRO_BACKUP=
limpar() {
    if [ "${TESTE_VPS_MANTER:-}" = 1 ]; then
        echo "de pé em $DIR (projeto $PROJETO)" >&2
        return
    fi
    # Só os recursos com identidade privada desta fixture; prefixo não basta.
    local cid nome nonce tipo raiz imagem esperada
    for cid in $(docker ps -aq --filter "label=io.stillon.backup.raiz=$DIR"); do
        read -r nome nonce tipo raiz imagem esperada < <(docker inspect -f '{{.Name}} {{index .Config.Labels "io.stillon.backup.nonce"}} {{index .Config.Labels "io.stillon.backup.tipo"}} {{index .Config.Labels "io.stillon.backup.raiz"}} {{.Image}} {{index .Config.Labels "io.stillon.backup.imagem"}}' "$cid") || continue
        if ! { [[ "$nonce" =~ ^[0-9a-f]{16}$ ]] && [ "$raiz" = "$DIR" ] && [ "$imagem" = "$esperada" ]; }; then
            continue
        fi
        case "$tipo:$nome" in "restauro:/stillon-restauro-$nonce" | "helper:/stillon-backup-helper-$nonce")
            docker rm -f -v "$cid" >/dev/null 2>&1 || true ;; esac
    done
    [ -z "$TERCEIRO_BACKUP" ] || docker rm -f -v "$TERCEIRO_BACKUP" >/dev/null 2>&1 || true
    dc down -v --remove-orphans >/dev/null 2>&1 || true
    bd down -v --remove-orphans >/dev/null 2>&1 || true
    for r in "$REPO_TESTE" "$REPO_TESTE-fonte"; do
        docker image ls --format '{{.Repository}}:{{.Tag}}' "$r" | xargs -r docker rmi >/dev/null 2>&1 || true
    done
    rm -rf "$DIR" "${TARS:-}"
}
trap limpar EXIT

cp "$RAIZ/deploy/vps/compose.prod.yaml" "$RAIZ/deploy/vps/Caddyfile" "$DIR/"
# 0755 como o runbook instala (3.3). Com o 0644 do checkout, o Docker Desktop
# (VirtioFS) responde -x ao bash do entrypoint do Postgres, o exec falha e o
# banco sobe sem os papéis no reinício.
install -m 0755 "$RAIZ/deploy/vps/initdb.sh" "$DIR/"
cp "$RAIZ/deploy/ci/vps/compose.teste.yaml" "$DIR/"
# A borda de teste com o trecho de produção, trocando só o domínio (uma
# linha, exatamente, ou o teste rodaria outra coisa).
mkdir -p "$DIR/borda/conf.d"
cp "$RAIZ/deploy/ci/vps/borda/compose.yaml" "$RAIZ/deploy/ci/vps/borda/Caddyfile" "$DIR/borda/"
[ "$(grep -cx 'stillon.tech {' "$RAIZ/deploy/vps/edge/stillon.caddy")" = 1 ] ||
    { echo "FALHOU: o stillon.caddy não tem a linha 'stillon.tech {' (uma vez)" >&2; exit 1; }
sed "s/^stillon\.tech {\$/$DOMINIO {/" "$RAIZ/deploy/vps/edge/stillon.caddy" > "$DIR/borda/conf.d/stillon.caddy"
b64() { openssl rand -base64 32; }
IP_HASH_KEY=$(b64)
cat > "$DIR/.env" <<EOF
STILLON_DOMINIO=$DOMINIO
POSTGRES_PASSWORD=$(openssl rand -hex 32)
STILLON_APP_PASSWORD=$(openssl rand -hex 32)
STILLON_BACKUP_PASSWORD=$(openssl rand -hex 32)
MASTER_KEY=$(b64)
JWT_SECRET=$(b64)
IP_HASH_KEY=$IP_HASH_KEY
RESEND_API_KEY=
EMAIL_FROM=
TESTE_VPS_HTTP=$HTTP
TESTE_VPS_HTTPS=$HTTPS
EOF
chmod 600 "$DIR/.env"

# O compose como a entrada o chama: o .env do diretório do projeto para a
# interpolação, mais o arquivo do teste por cima.
dc() { COMPOSE_FILE="$DIR/compose.prod.yaml:$DIR/compose.teste.yaml" docker compose --project-directory "$DIR" "$@"; }
sql() { dc exec -T db psql -U postgres -d stillon -Atq -c "$1"; }
bd() {
    CADDY_IMAGEM=$caddy_img TESTE_VPS_HTTP=$HTTP TESTE_VPS_HTTPS=$HTTPS \
        docker compose --project-directory "$DIR/borda" -f "$DIR/borda/compose.yaml" "$@"
}
caddy_img=

# A entrada do teste: a de produção com as constantes trocadas (o diretório,
# o nome da imagem, o compose do teste por cima, uma espera menor). Cada troca
# tem de casar exatamente uma linha, ou o teste rodaria outra coisa.
TARS=$(mktemp -d)
ENTRADA=$TARS/stillon-entrada
trocar() {
    [ "$(grep -cxF -- "$1" "$ENTRADA")" = 1 ] || { echo "FALHOU: a linha '$1' não está (uma vez) no stillon-entrada" >&2; exit 1; }
    awk -v de="$1" -v para="$2" '$0 == de { print para; next } { print }' "$ENTRADA" > "$ENTRADA.novo" && mv "$ENTRADA.novo" "$ENTRADA"
}
cp "$RAIZ/deploy/vps/stillon-entrada" "$ENTRADA"
trocar 'DIR=/opt/stillon' "DIR=$DIR"
trocar 'IMAGEM=stillon' "IMAGEM=$REPO_TESTE"
trocar 'ARQUIVOS_COMPOSE="$DIR/compose.prod.yaml"' 'ARQUIVOS_COMPOSE="$DIR/compose.prod.yaml:$DIR/compose.teste.yaml"'
trocar 'ESPERA_S=90' 'ESPERA_S=45'
trocar 'SHM_BASE=/dev/shm' "SHM_BASE=$TARS"
# O age do teste pode não estar num diretório do PATH fixo da entrada (na
# VPS ele vem do apt, em /usr/bin).
trocar 'export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin' \
    "export PATH=$(dirname "$(command -v age)"):/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
# Como o sudo a roda: ambiente limpo, só o pedido do SSH. A saída padrão e a
# de erro ficam em $TARS/saida e $TARS/erro; devolve o código.
entrada() {
    local pedido=$1 codigo=0
    shift
    env -i PATH="$PATH" HOME="$HOME" SSH_ORIGINAL_COMMAND="$pedido" bash "$ENTRADA" "$@" \
        > "$TARS/saida" 2> "$TARS/erro" || codigo=$?
    echo "$codigo"
}
# O tar como o deploy.yml o manda (crane pull --format oci): layout OCI com o
# manifesto linux/amd64 da imagem, SEM nome. Imprime o digest do manifesto,
# que é o image-id do protocolo.
oci_tar() {
    local img=$1 saida=$2 x y d f m manifesto config
    x=$(mktemp -d "$TARS/x.XXXX")
    y=$(mktemp -d "$TARS/y.XXXX")
    docker save "$img" | tar -xf - -C "$x"
    d=$(jq -r '.manifests[0].digest' "$x/index.json")
    while :; do
        f="$x/blobs/${d/://}"
        case "$(jq -r '.mediaType // empty' "$f")" in
            *index* | *manifest.list*)
                d=$(jq -r '[.manifests[] | select(.platform.os == "linux" and .platform.architecture == "amd64")][0].digest' "$f") ;;
            *) break ;;
        esac
    done
    m=$d
    mkdir -p "$y/blobs/sha256"
    cp "$x/oci-layout" "$y/"
    for b in "$m" $(jq -r '.config.digest, .layers[].digest' "$x/blobs/${m/://}"); do
        cp "$x/blobs/${b/://}" "$y/blobs/sha256/"
    done
    # O index.json como chega à VPS: o do crane v0.22.1, já sem o artifactType
    # que o deploy.yml tira, e COM o platform (o os e a architecture da
    # config). Sem ele, o teste não passaria pelo caminho real do load.
    manifesto=$x/blobs/${m/://}
    config=$x/blobs/$(jq -r '.config.digest | sub(":"; "/")' "$manifesto")
    jq -c -n --arg mt "$(jq -r .mediaType "$manifesto")" --arg d "$m" \
        --argjson tam "$(stat -c %s "$manifesto")" \
        --argjson plat "$(jq -c '{architecture, os}' "$config")" \
        '{schemaVersion: 2, mediaType: "application/vnd.oci.image.index.v1+json",
          manifests: [{mediaType: $mt, size: $tam, digest: $d, platform: $plat}]}' > "$y/index.json"
    tar -cf "$saida" -C "$y" .
    rm -rf "$x" "$y"
    echo "$m"
}
# Imagens diferentes do mesmo app (um LABEL a mais), e uma que nunca fica
# saudável (o boot recusa ARGON2_MAX_CONCURRENT que não é inteiro de 1 a 64,
# e repete o valor no log; o compose não define essa variável, e o ENV da
# imagem vale). O valor é um canário: o log do app doente não pode chegar à
# saída de erro da entrada, que o job do Actions (público) mostra.
CANARIO=CANARY-$(python3 -c 'import uuid; print(uuid.uuid4())')
# sem_canario <caso>: o log do app ficou fora da saída do SSH (as duas fds) e
# dentro do entrada.log, que diz onde lê-lo.
sem_canario() {
    ! grep -qF "$CANARIO" "$TARS/erro" "$TARS/saida" ||
        falhar "$1: o log do app (o canário) apareceu na saída do SSH, que o job público mostra"
    grep -qF "$CANARIO" "$DIR/entrada.log" || falhar "$1: o log do app (o canário) não ficou no entrada.log"
    grep -qF "tail -n 100 $DIR/entrada.log" "$TARS/erro" ||
        falhar "$1: a saída do SSH não diz onde ler o log do app (tail -n 100 $DIR/entrada.log)"
}
fonte() {
    local nome=$1 extra=$2
    printf 'FROM %s\n%s\n' "$IMAGEM_APP" "$extra" | docker build -q -t "$REPO_TESTE-fonte:$nome" - >/dev/null
    oci_tar "$REPO_TESTE-fonte:$nome" "$TARS/$nome.tar"
}
sha_de() { printf %s "$1" | sha1sum | cut -c1-40; }
versao() { cat "$DIR/versao" 2>/dev/null || echo -; }
imagem_do_app() { docker inspect -f '{{.Config.Image}}' "$(dc ps -q app)"; }
# no_ar <sha>: a versão gravada e a imagem do app são as desse SHA.
no_ar() { [ "$(versao)" = "$1" ] && [ "$(imagem_do_app)" = "$REPO_TESTE:$1" ]; }
# Os contêineres do app em qualquer estado, pelo docker (e não pelo compose
# que a entrada usa), e a evidência para a mensagem de uma falha: o estado de
# cada um e o fim da saída de erro da entrada.
apps() { docker ps -aq --filter "label=com.docker.compose.project=$PROJETO" --filter label=com.docker.compose.service=app; }
estado() {
    local c
    echo
    for c in $(apps); do
        docker inspect -f '  app {{.Name}}: {{.State.Status}}, saúde {{if .State.Health}}{{.State.Health.Status}}{{else}}-{{end}}, {{.RestartCount}} reinícios, imagem {{.Config.Image}}' "$c"
    done
    echo "  fim da saída de erro da entrada:"
    grep -v '^{' "$TARS/erro" | tail -n 12 | sed 's/^/    /'
}

# ---------------------------------------------------------------------------
# 1. O compose.prod.yaml sozinho (o que vai para a VPS), pela config resolvida.
# ---------------------------------------------------------------------------
cfg=$(cd "$DIR" && docker compose -f compose.prod.yaml config --format json)
q() { jq -r "$1" <<<"$cfg"; }
caddy_img=$(q '.services.caddy.image')

[ "$(q '[.services | to_entries[] | select(.value.ports != null and (.value.ports | length) > 0) | .key] | join(",")')" = "" ] ||
    falhar "algum serviço publica porta: quem ouve a 80 e a 443 é a borda do host"
[ "$(q '.services | keys | sort | join(",")')" = app,caddy,db ] ||
    falhar "serviços do compose.prod: $(q '.services | keys | join(",")'), queria app, caddy e db"
# Só o Caddy está na rede da borda, no IP fixo que o trecho dela usa. IP, e
# não alias: um segundo contêiner com o mesmo alias dividiria o tráfego em
# silêncio; com o mesmo IP, o Docker recusa.
[ "$(q '[.services | to_entries[] | select(.value.networks.edge != null) | .key] | join(",")')" = caddy ] ||
    falhar "outro serviço além do caddy está na rede edge"
[ "$(q '.services.caddy.networks.edge.ipv4_address')" = 172.31.0.3 ] ||
    falhar "o caddy não está no IP fixo 172.31.0.3 da rede edge"
[ "$(q '.services.caddy.networks.edge.aliases // [] | length')" = 0 ] ||
    falhar "o caddy tem alias na rede edge: o trecho da borda usa o IP fixo"
[ "$(q '.networks.edge.external')/$(q '.networks.edge.name')" = true/edge ] ||
    falhar "a rede edge não é a externa 'edge' da borda"
# Sem NET_RAW, o Caddy não forja ARP nem pacote cru na rede edge, que é
# dividida com os outros projetos.
[ "$(q '.services.caddy.cap_drop // [] | index("NET_RAW") != null')" = true ] ||
    falhar "o caddy não tira NET_RAW (cap_drop): forjaria ARP na rede edge"
# O db sem as capacidades padrão: o entrypoint do Postgres só precisa acertar
# dono e modo do volume e trocar para o usuário postgres. O app, que não
# escreve em disco, com o sistema de arquivos só leitura. O docker inspect,
# mais abaixo, confere que os contêineres de pé têm o mesmo.
DB_CAPS=CHOWN,DAC_OVERRIDE,FOWNER,SETGID,SETUID
for s in caddy app db; do
    [ "$(q ".services.$s.security_opt // [] | index(\"no-new-privileges:true\") != null")" = true ] ||
        falhar "$s sem security_opt no-new-privileges:true"
done
[ "$(q '.services.db.cap_drop // [] | join(",")')" = ALL ] || falhar "o db não tira todas as capacidades (cap_drop: [ALL])"
[ "$(q '.services.db.cap_add // [] | sort | join(",")')" = "$DB_CAPS" ] ||
    falhar "cap_add do db é '$(q '.services.db.cap_add // [] | sort | join(",")')', queria $DB_CAPS"
[ "$(q '.services.app.read_only // false')" = true ] || falhar "o app sem read_only: true"
for s in caddy app db; do
    [ "$(q ".services.$s.restart")" = unless-stopped ] || falhar "$s sem restart: unless-stopped"
    [ "$(q ".services.$s.mem_limit // empty")" != "" ] || falhar "$s sem mem_limit"
done
# GOMEMLIMIT abaixo do mem_limit do app (obrigação 4).
gomem=$(q '.services.app.environment.GOMEMLIMIT // empty')
limite=$(q '.services.app.mem_limit')
case "$gomem" in
    *MiB) gomem_b=$((${gomem%MiB} * 1024 * 1024)) ;;
    *GiB) gomem_b=$((${gomem%GiB} * 1024 * 1024 * 1024)) ;;
    *) gomem_b=0 ;;
esac
if ! { [ "$gomem_b" -gt 0 ] && [ "$gomem_b" -lt "$limite" ]; }; then
    falhar "GOMEMLIMIT '$gomem' não está abaixo do mem_limit do app ($limite bytes)"
fi
# O IP do cliente (obrigação 2): TRUSTED_PROXIES é o IP fixo do Caddy.
caddy_ip=$(q '.services.caddy.networks.stillon.ipv4_address')
[ "$caddy_ip" = 10.30.0.2 ] || falhar "IP do caddy é '$caddy_ip', queria 10.30.0.2"
[ "$(q '.services.app.environment.TRUSTED_PROXIES')" = "$caddy_ip/32" ] ||
    falhar "TRUSTED_PROXIES do app não é $caddy_ip/32"
[ "$(q '.services.app.environment.CLIENT_IP_HEADER')" = "" ] || falhar "CLIENT_IP_HEADER do app não é vazio"
[ "$(q '.services.app.environment.PUBLIC_BASE_URL')" = "https://$DOMINIO" ] || falhar "PUBLIC_BASE_URL não é https://<domínio>"
[ "$(q '.services.app.environment.METRICS_ADDR')" = off ] || falhar "METRICS_ADDR do app não é off"
[ "$(q '.services.app.networks.stillon.ipv4_address')/$(q '.services.db.networks.stillon.ipv4_address')" = 10.30.0.3/10.30.0.4 ] ||
    falhar "IPs fixos do app e do db não são 10.30.0.3 e 10.30.0.4"
[ "$(q '.networks.stillon.enable_ipv6')" = false ] || falhar "a rede stillon não desliga o IPv6"
[ "$(q '.networks.stillon.ipam.config[0].subnet')" = 10.30.0.0/24 ] || falhar "sub-rede da rede stillon não é 10.30.0.0/24"
# O Caddy não recebe segredo nenhum; o app não recebe a senha do superusuário.
[ "$(q '.services.caddy.environment | keys | sort | join(",")')" = STILLON_DOMINIO ] ||
    falhar "o caddy recebe variáveis além do domínio: $(q '.services.caddy.environment | keys | join(",")')"
[ "$(q '.services.app.environment | has("POSTGRES_PASSWORD") or has("STILLON_BACKUP_PASSWORD")')" = false ] ||
    falhar "o app recebe a senha do superusuário ou a do backup"
grep -q -- '-demo-\|ZGVtby12aWdpbHVtZS' "$RAIZ/deploy/vps/compose.prod.yaml" && falhar "o compose.prod.yaml tem chave de demonstração"
# Nenhuma chave ou senha escrita no compose (obrigação 5): na config resolvida
# com o .env do teste, cada segredo é o valor gerado acima. Um literal
# qualquer (POSTGRES_PASSWORD: postgres, uma MASTER_KEY fixa) fica diferente.
env_do_teste() { sed -n "s/^$1=//p" "$DIR/.env"; }
for par in app:MASTER_KEY app:JWT_SECRET app:IP_HASH_KEY db:POSTGRES_PASSWORD db:STILLON_APP_PASSWORD db:STILLON_BACKUP_PASSWORD; do
    s=${par%%:*} v=${par#*:}
    [ "$(q ".services.$s.environment.$v // empty")" = "$(env_do_teste "$v")" ] ||
        falhar "o $v do $s não é o do .env (valor escrito no compose.prod.yaml?)"
done
[ "$(q '.services.app.environment.DATABASE_URL')" = "postgres://stillon_app:$(env_do_teste STILLON_APP_PASSWORD)@10.30.0.4:5432/stillon?sslmode=disable" ] ||
    falhar "a DATABASE_URL do app não usa a senha do .env: $(q '.services.app.environment.DATABASE_URL' | sed 's/:[^:@]*@/:***@/')"
# O Caddyfile confia só na borda (obrigação 1), fora dos comentários: uma
# lista mais larga deixaria um vizinho da rede edge escolher o IP do cliente.
sem_comentario() { sed 's/#.*//' "$1"; }
if ! { [ "$(sem_comentario "$RAIZ/deploy/vps/Caddyfile" | grep -c 'trusted_proxies' || true)" = 2 ] &&
    sem_comentario "$RAIZ/deploy/vps/Caddyfile" | grep -qxE '[[:space:]]*trusted_proxies static 172\.31\.0\.2/32' &&
    sem_comentario "$RAIZ/deploy/vps/Caddyfile" | grep -qxE '[[:space:]]*trusted_proxies_strict'; }; then
    falhar "o Caddyfile não confia exatamente em 172.31.0.2/32, em modo estrito"
fi
sem_comentario "$RAIZ/deploy/vps/Caddyfile" | grep -qxE '[[:space:]]*header_up X-Forwarded-For \{client_ip\}' ||
    falhar "o Caddyfile não reescreve o X-Forwarded-For com o {client_ip}: o app veria a borda"
if ! { [ "$(q '.services.app.networks.stillon.ipv4_address')" = 10.30.0.3 ] &&
    sem_comentario "$RAIZ/deploy/vps/Caddyfile" | grep -qxE '[[:space:]]*reverse_proxy 10\.30\.0\.3:8080 \{'; }; then
    falhar "o Caddyfile não chama o app pelo IP fixo 10.30.0.3: um vizinho chamado app na rede edge receberia o tráfego"
fi
# O trecho da borda não confia em ninguém e aponta para o IP fixo.
[ "$(sem_comentario "$RAIZ/deploy/vps/edge/stillon.caddy" 2>/dev/null | grep -c trusted_proxies || true)" = 0 ] ||
    falhar "o trecho da borda tem trusted_proxies: um cliente escolheria o próprio IP"
sem_comentario "$RAIZ/deploy/vps/edge/stillon.caddy" 2>/dev/null | grep -qxE '[[:space:]]*reverse_proxy 172\.31\.0\.3:80' ||
    falhar "o trecho da borda não encaminha para 172.31.0.3:80"
# O env.exemplo é o modelo do .env: sem valor nenhum, e com toda variável que
# o compose.prod.yaml lê (menos STILLON_SHA, da entrada, e o modo de teste).
grep -Ev '^(#|$)' "$RAIZ/deploy/vps/env.exemplo" | grep -v '=$' && falhar "o env.exemplo tem valor preenchido"
while read -r v; do
    case "$v" in STILLON_SHA) continue ;; esac
    grep -q "^$v=$" "$RAIZ/deploy/vps/env.exemplo" || falhar "o env.exemplo não tem $v"
done < <(grep -o '\${[A-Z_]*' "$RAIZ/deploy/vps/compose.prod.yaml" | sed 's/^\${//' | sort -u)
# E o contrário: o env.exemplo só tem o que o compose lê (a HC_URL do vigia
# fica no vigia.env, longe dos segredos).
while read -r v; do
    grep -q "\${${v}[:}-]" "$RAIZ/deploy/vps/compose.prod.yaml" || falhar "o env.exemplo tem $v, que o compose.prod.yaml não lê"
done < <(grep -Eo '^[A-Z_]+=' "$RAIZ/deploy/vps/env.exemplo" | tr -d =)
ok "compose.prod.yaml: sem portas, só o caddy na rede edge, restart, mem_limit, GOMEMLIMIT, IPs fixos, IPv6, variáveis, segredos do .env; Caddyfile confia só na borda"

# A entrada e o compose falam da mesma imagem.
if ! { grep -qx 'IMAGEM=stillon' "$RAIZ/deploy/vps/stillon-entrada" &&
    [ "$(q '.services.app.image')" = "stillon:atual" ]; }; then
    falhar "o IMAGEM do stillon-entrada e o image do app no compose.prod.yaml não casam"
fi

# ---------------------------------------------------------------------------
# 2. A pilha de pé pelo stillon-entrada, atrás da borda de teste.
# ---------------------------------------------------------------------------
# A borda antes da pilha: o compose.prod.yaml só sobe com a rede edge.
bd up -d --wait --wait-timeout 60 >&2 || { echo "FALHOU: a borda de teste não subiu" >&2; exit 1; }
# O curl do host chega à borda pelo gateway da rede edge: é esse o IP do
# cliente que o app tem de ver.
cliente_ip=$(docker network inspect -f '{{(index .IPAM.Config 0).Gateway}}' edge)
[ -n "$cliente_ip" ] || { echo "FALHOU: a rede edge não informou o gateway" >&2; exit 1; }

ID_A=$(fonte a 'LABEL stillon.teste=a')
ID_DOENTE=$(fonte doente "ENV ARGON2_MAX_CONCURRENT=$CANARIO")
SHA_A=$(sha_de a)
SHA_DOENTE=$(sha_de doente)

# 2a. Primeiro deploy que não fica saudável: sem versão anterior, o app sai
#     do ar (parado e removido: nenhum contêiner do app, em estado nenhum,
#     nem reiniciando) e o código é 4.
c=$(entrada "deploy $SHA_DOENTE $ID_DOENTE" < "$TARS/doente.tar")
[ "$c" = 4 ] || falhar "primeiro deploy doente saiu $c, queria 4: $(estado)"
grep -q 'sem versão anterior' "$TARS/erro" || falhar "primeiro deploy doente não disse que não havia versão anterior"
[ "$(versao)" = - ] || falhar "primeiro deploy doente gravou a versão $(versao)"
[ -z "$(apps)" ] || falhar "primeiro deploy doente deixou contêiner do app: $(estado)"
sem_canario "primeiro deploy doente"

# 2b. O deploy saudável.
c=$(entrada "deploy $SHA_A $ID_A" < "$TARS/a.tar")
if [ "$c" != 0 ]; then
    cat "$TARS/erro" >&2
    dc logs >&2
    echo "FALHOU: o deploy de $SHA_A saiu $c" >&2
    exit 1
fi
[ "$(versao)" = "$SHA_A" ] || falhar "depois do deploy, versao = $(versao)"
[ "$(imagem_do_app)" = "$REPO_TESTE:$SHA_A" ] || falhar "o app roda $(imagem_do_app), queria $REPO_TESTE:$SHA_A"
[ "$(docker image inspect -f '{{.Id}}' "$REPO_TESTE:atual")" = "$ID_A" ] || falhar "$REPO_TESTE:atual não aponta para a versão no ar"
[ ! -s "$TARS/saida" ] || falhar "o deploy escreveu na saída padrão"
ok "stillon-entrada: primeiro deploy doente para o app (4); deploy saudável (0)"

# Ninguém da pilha publica porta: só a borda, e só 80 e 443.
publicadas=$(dc ps --format json | jq -rs '[.[] | (.Publishers // [])[] | select(.PublishedPort > 0)] | length')
[ "$publicadas" = 0 ] || falhar "a pilha do StillOn publica porta"
borda_portas=$(bd ps --format json | jq -rs '[.[] | .Service as $s | (.Publishers // [])[] | select(.PublishedPort > 0) | "\($s):\(.TargetPort)"] | sort | unique | join(" ")')
[ "$borda_portas" = "proxy:443 proxy:80" ] || falhar "portas publicadas na borda: '$borda_portas'"
for s in caddy app db; do
    [ "$(docker inspect -f '{{json .HostConfig.PortBindings}}' "$(dc ps -q "$s")")" = "{}" ] ||
        falhar "$s tem porta publicada no host"
done
# O endurecimento chegou aos contêineres de pé (e não só ao arquivo).
db_cid=$(dc ps -q db)
docker inspect -f '{{json .HostConfig.SecurityOpt}}' "$db_cid" | grep -qF '"no-new-privileges:true"' ||
    falhar "o contêiner do db sem no-new-privileges"
[ "$(docker inspect -f '{{json .HostConfig.CapDrop}}' "$db_cid" | jq -r 'join(",")')" = ALL ] ||
    falhar "o contêiner do db não tirou todas as capacidades"
# O Docker devolve as capacidades com o prefixo CAP_.
[ "$(docker inspect -f '{{json .HostConfig.CapAdd}}' "$db_cid" | jq -r 'map(sub("^CAP_"; "")) | sort | join(",")')" = "$DB_CAPS" ] ||
    falhar "o contêiner do db tem as capacidades $(docker inspect -f '{{json .HostConfig.CapAdd}}' "$db_cid"), queria $DB_CAPS"
[ "$(docker inspect -f '{{.HostConfig.ReadonlyRootfs}}' "$(dc ps -q app)")" = true ] ||
    falhar "o contêiner do app não tem o sistema de arquivos só leitura"
ok "db sem capacidades além das do entrypoint e com no-new-privileges; app só leitura"
rede=$(docker inspect -f '{{range $k, $v := .NetworkSettings.Networks}}{{$k}}{{end}}' "$(dc ps -q db)")
[ "$(docker network inspect -f '{{.EnableIPv6}}' "$rede")" = false ] || falhar "a rede $rede tem IPv6"

# A CA interna da borda, para o curl conferir o certificado de verdade.
bd exec -T proxy cat /data/caddy/pki/authorities/local/root.crt > "$DIR/ca.crt"
cu() { curl -sS --max-time 10 --cacert "$DIR/ca.crt" --resolve "$DOMINIO:$HTTPS:127.0.0.1" "$@"; }
BASE=https://$DOMINIO:$HTTPS

# 3. /healthz pela borda, com TLS conferido, chegando ao app (X-Trace-Id é do app).
resp=$(cu -D - -o /dev/null "$BASE/healthz" || true)
echo "$resp" | head -1 | grep -q ' 200' || falhar "/healthz pela borda não deu 200: $(echo "$resp" | head -1)"
echo "$resp" | grep -qi '^x-trace-id:' || falhar "/healthz pela borda não veio do app (sem X-Trace-Id)"
ok "/healthz pela borda, com o certificado da CA interna"

# 4. Host estranho não chega ao app.
#    a) outro nome no SNI: sem certificado para ele, nada responde.
if curl -sS --max-time 10 --cacert "$DIR/ca.crt" --resolve "estranho.teste:$HTTPS:127.0.0.1" \
    -o /dev/null "https://estranho.teste:$HTTPS/healthz" 2>/dev/null; then
    falhar "https com SNI estranho.teste respondeu"
fi
#    b) o SNI do domínio com outro Host.
resp=$(cu -D - -o /dev/null -H 'Host: estranho.teste' "$BASE/healthz" || true)
echo "$resp" | grep -qi '^x-trace-id:' && falhar "Host estranho pelo https chegou ao app"
#    c) http puro, com outro Host e com Host nenhum reconhecível (o IP).
for h in estranho.teste 127.0.0.1; do
    resp=$(curl -sS --max-time 10 -D - -o /dev/null -H "Host: $h" "http://127.0.0.1:$HTTP/healthz" || true)
    echo "$resp" | grep -qi '^x-trace-id:' && falhar "Host $h pelo http chegou ao app"
done
#    d) o domínio pelo http vira redirecionamento para o https, sem passar no app.
resp=$(curl -sS --max-time 10 -D - -o /dev/null -H "Host: $DOMINIO" "http://127.0.0.1:$HTTP/healthz" || true)
echo "$resp" | head -1 | grep -q ' 308' || falhar "http no domínio não redirecionou (308): $(echo "$resp" | head -1)"
ok "Host estranho não chega ao app"

# 5. O IP que o app vê é o da conexão com o Caddy, com qualquer cabeçalho que
#    o cliente mande. O observável é o ip_hash da sessão criada pelo login:
#    HMAC-SHA256(IP_HASH_KEY, ip), o mesmo cálculo de crypto.HashIP.
hmac() { printf %s "$1" | openssl dgst -sha256 -mac HMAC -macopt "hexkey:$(printf %s "$IP_HASH_KEY" | base64 -d | od -An -tx1 | tr -d ' \n')" | sed 's/^.*= //'; }
printf '%s\n' "$SENHA" | dc exec -T app /stillon user create --email "$EMAIL" >&2 ||
    { falhar "stillon user create falhou"; exit 1; }
papeis=$(sql "SELECT string_agg(rolname || ':' || rolsuper::int || rolbypassrls::int || pg_has_role(oid, 'pg_read_all_data', 'MEMBER')::int, ' ' ORDER BY rolname) FROM pg_roles WHERE rolname IN ('stillon_app', 'stillon_backup')")
[ "$papeis" = "stillon_app:000 stillon_backup:011" ] ||
    falhar "papéis do initdb.sh: '$papeis', queria stillon_app:000 stillon_backup:011 (super, bypassrls, pg_read_all_data)"
entrar() {
    cu -o /dev/null -w '%{http_code}' -X POST "$BASE/api/v1/auth/login" \
        -H 'Content-Type: application/json' -H 'X-Requested-With: stillon' "$@" \
        -d "{\"email\":\"$EMAIL\",\"password\":\"$SENHA\"}"
}
ultimo_hash() { sql "SELECT encode(ip_hash, 'hex') FROM sessions ORDER BY created_at DESC, id DESC LIMIT 1"; }

[ "$(entrar)" = 200 ] || falhar "login pela borda não deu 200"
real=$(ultimo_hash)
[ -n "$real" ] || falhar "a sessão do login não tem ip_hash"
[ "$real" = "$(hmac "$caddy_ip")" ] && falhar "o app viu o IP do Caddy: TRUSTED_PROXIES não casa, e todo cliente divide um balde"
[ "$real" = "$(hmac 172.31.0.2)" ] && falhar "o app viu o IP da borda: o Caddy do StillOn não reescreve o X-Forwarded-For"
[ "$real" = "$(hmac "$cliente_ip")" ] || falhar "o app não viu o IP do cliente ($cliente_ip, o gateway da rede edge)"
[ "$(entrar -H 'X-Real-IP: 1.2.3.4' -H 'Fly-Client-IP: 1.2.3.5' -H 'CF-Connecting-IP: 1.2.3.6' -H 'X-Forwarded-For: 1.2.3.7')" = 200 ] ||
    falhar "login com cabeçalhos forjados não deu 200"
forjado=$(ultimo_hash)
[ "$forjado" = "$real" ] || falhar "cabeçalhos do cliente mudaram o IP visto pelo app"
for ip in 1.2.3.4 1.2.3.5 1.2.3.6 1.2.3.7; do
    [ "$forjado" = "$(hmac "$ip")" ] && falhar "o app acreditou no IP forjado $ip"
done
# O aviso da B3 (X-Forwarded-For de fora de TRUSTED_PROXIES) não aparece.
# Em arquivo, e não num pipe: sob pipefail, o grep -q sairia no acerto e o
# SIGPIPE do `logs` deixaria a pipeline falsa (a asserção de ausência nunca
# falharia).
dc logs app > "$TARS/app.log" 2>&1 || falhar "docker compose logs app falhou"
grep -q 'não vem de TRUSTED_PROXIES' "$TARS/app.log" && falhar "o app avisou que o X-Forwarded-For não vem de TRUSTED_PROXIES"
ok "X-Real-IP, Fly-Client-IP, CF-Connecting-IP e X-Forwarded-For do cliente não mudam o IP visto"

# 6. O Caddy tira os três cabeçalhos: com um CLIENT_IP_HEADER posto por
#    engano, o app cai no IP do Caddy (e avisa), e não no valor do cliente.
for cab in X-Real-IP Fly-Client-IP CF-Connecting-IP; do
    cat > "$DIR/compose.cabecalho.yaml" <<EOF
services:
  app:
    environment:
      CLIENT_IP_HEADER: $cab
EOF
    if ! COMPOSE_FILE="$DIR/compose.prod.yaml:$DIR/compose.teste.yaml:$DIR/compose.cabecalho.yaml" STILLON_SHA=$SHA_A \
        docker compose --project-directory "$DIR" up -d --wait --wait-timeout 120 app >&2; then
        falhar "o app não subiu com CLIENT_IP_HEADER=$cab"
        continue
    fi
    [ "$(entrar -H "$cab: 9.8.7.6")" = 200 ] || falhar "login com $cab não deu 200"
    h=$(ultimo_hash)
    [ "$h" = "$(hmac 9.8.7.6)" ] && falhar "o Caddy repassou o $cab do cliente"
    [ "$h" = "$(hmac "$caddy_ip")" ] || falhar "com CLIENT_IP_HEADER=$cab o app não caiu no IP do Caddy"
done
rm -f "$DIR/compose.cabecalho.yaml"
STILLON_SHA=$SHA_A dc up -d --wait --wait-timeout 120 app >&2 || falhar "o app não voltou à configuração de produção"
ok "o Caddy remove X-Real-IP, Fly-Client-IP e CF-Connecting-IP"

# 6b. Um contêiner de outro projeto na rede edge, falando direto com o Caddy
#     do StillOn (sem a borda), não escolhe o IP que o app vê: o Caddy só
#     confia no X-Forwarded-For que vem de 172.31.0.2. A conta é a do dono
#     (o StillOn tem um usuário só); o limitador de login é por IP, e o
#     vizinho tem o dele.
caddy_edge=$(docker inspect -f '{{(index .NetworkSettings.Networks "edge").IPAddress}}' "$(dc ps -q caddy)")
vizinho_ip=172.31.0.77
c=$(docker run --rm --network edge --ip "$vizinho_ip" --add-host "$DOMINIO:$caddy_edge" \
    --entrypoint curl "$caddy_img" -sS --max-time 10 -o /dev/null -w '%{http_code}' \
    -X POST "http://$DOMINIO/api/v1/auth/login" \
    -H 'Content-Type: application/json' -H 'X-Requested-With: stillon' -H 'X-Forwarded-For: 1.2.3.8' \
    -d "{\"email\":\"$EMAIL\",\"password\":\"$SENHA\"}" || true)
[ "$c" = 200 ] || falhar "login do vizinho direto no Caddy do StillOn não deu 200 ($c)"
direto=$(ultimo_hash)
[ "$direto" = "$(hmac 1.2.3.8)" ] && falhar "o Caddy do StillOn acreditou no X-Forwarded-For de um vizinho da rede edge"
[ "$direto" = "$(hmac "$vizinho_ip")" ] || falhar "o app não viu o IP do vizinho ($vizinho_ip)"
ok "um vizinho da rede edge não escolhe o IP visto pelo app"

# 6b'. O modo estrito do Caddy do StillOn (trusted_proxies_strict): com uma
#      borda que confia no cliente e por isso ACRESCENTA o salto (chega
#      "1.2.3.9, <cliente>"), vale o salto mais à direita que não é da borda,
#      o IP real. Sem o modo estrito, o Caddy pegaria o mais à esquerda, o
#      que o cliente escreveu. Com a borda do contrato (que troca o
#      X-Forwarded-For) os dois modos dão o mesmo IP: só esta variante
#      distingue. A variante entra por caddy reload, que mantém a CA.
awk '{ print } /^\tservers \{$/ { print "\t\ttrusted_proxies static private_ranges"; n++ } END { exit n != 1 }' \
    "$DIR/borda/Caddyfile" > "$DIR/borda/Caddyfile.saltos" ||
    { echo "FALHOU: o Caddyfile da borda de teste não tem o bloco servers (uma vez)" >&2; exit 1; }
recarregar_borda() { bd exec -T proxy caddy reload --adapter caddyfile --config "$1" >&2; }
if recarregar_borda /dev/stdin < "$DIR/borda/Caddyfile.saltos"; then
    [ "$(entrar -H 'X-Forwarded-For: 1.2.3.9')" = 200 ] || falhar "login pela borda que acrescenta salto não deu 200"
    h=$(ultimo_hash)
    [ "$h" = "$(hmac 1.2.3.9)" ] && falhar "com a borda acrescentando salto, o Caddy do StillOn pegou o X-Forwarded-For do cliente (sem modo estrito?)"
    [ "$h" = "$(hmac "$cliente_ip")" ] || falhar "com a borda acrescentando salto, o app não viu o IP do cliente ($cliente_ip)"
else
    falhar "a borda de teste não recarregou a variante que acrescenta salto"
fi
recarregar_borda /etc/caddy/Caddyfile || { echo "FALHOU: a borda de teste não voltou ao Caddyfile do contrato" >&2; exit 1; }
[ "$(cu -o /dev/null -w '%{http_code}' "$BASE/healthz" || true)" = 200 ] || falhar "depois de restaurar a borda, /healthz não deu 200"
ok "com a borda acrescentando salto, o modo estrito ignora o X-Forwarded-For do cliente"

# 6c. Um vizinho na rede edge que se chame "app" não recebe o tráfego do
#     StillOn: o Caddy fala com o app pelo IP fixo, e não pelo nome, que o DNS
#     do Docker resolveria em qualquer rede do contêiner. O chamariz responde
#     200 sem X-Trace-Id; o app de verdade sempre manda o dele. O Caddy é
#     reiniciado depois que o chamariz sobe: sem isso, ele reusaria a conexão
#     keep-alive com o app e nem consultaria o DNS de novo.
chamariz=$(docker run -d --network edge --network-alias app --entrypoint caddy "$caddy_img" \
    respond --listen :8080 --body chamariz)
dc restart caddy >&2
for _ in $(seq 30); do
    [ "$(cu -o /dev/null -w '%{http_code}' "$BASE/healthz" || true)" = 200 ] && break
    sleep 1
done
chegou_ao_chamariz=
for _ in 1 2 3 4 5; do
    resp=$(cu -D - -o /dev/null "$BASE/healthz" || true)
    echo "$resp" | grep -qi '^x-trace-id:' || chegou_ao_chamariz=1
done
docker rm -f "$chamariz" >/dev/null
[ -z "$chegou_ao_chamariz" ] || falhar "um contêiner chamado app na rede edge recebeu o tráfego do StillOn"
ok "um vizinho chamado app na rede edge não recebe o tráfego do StillOn"

# 6d. A checagem de Host é do Caddy do StillOn, não só da borda: direto nele,
#     pela rede edge, só o domínio chega ao app.
direto_host() {
    docker run --rm --network edge --entrypoint curl "$caddy_img" -sS --max-time 10 -D - -o /dev/null "$@" \
        "http://172.31.0.3/healthz" || true
}
direto_host -H 'Host: estranho.teste' | grep -qi '^x-trace-id:' && falhar "Host estranho direto no Caddy do StillOn chegou ao app"
direto_host | grep -qi '^x-trace-id:' && falhar "Host 172.31.0.3 direto no Caddy do StillOn chegou ao app"
direto_host -H "Host: $DOMINIO" | grep -qi '^x-trace-id:' || falhar "controle: o domínio direto no Caddy do StillOn não chegou ao app"
ok "direto no Caddy do StillOn, só o domínio chega ao app"

# 7. O banco não aceita conexão de fora da rede do compose. O controle
#    positivo (de dentro da rede) prova que a sonda funciona.
pg=$(docker inspect -f '{{.Config.Image}}' "$(dc ps -q db)")
docker run --rm --network "$rede" "$pg" pg_isready -h 10.30.0.4 -t 5 >/dev/null ||
    falhar "controle: de dentro da rede, o pg_isready não alcançou o banco"
if docker run --rm "$pg" pg_isready -h 10.30.0.4 -t 5 >/dev/null 2>&1; then
    falhar "de outra rede do Docker, o banco aceitou conexão"
fi
ok "o banco só aceita conexão de dentro da rede"

# 7b. A borda não depende do StillOn: com o Caddy dele parado, ela continua
#     de pé e responde 502 ao domínio; com ele de volta, encaminha de novo.
dc stop caddy >&2
c=$(cu -o /dev/null -w '%{http_code}' "$BASE/healthz" || true)
[ "$c" = 502 ] || falhar "com o StillOn parado, a borda respondeu $c, queria 502"
[ "$(docker inspect -f '{{.State.Running}}' "$(bd ps -q proxy)")" = true ] || falhar "a borda caiu com o StillOn parado"
dc start caddy >&2
de_volta=
for _ in $(seq 30); do
    if [ "$(cu -o /dev/null -w '%{http_code}' "$BASE/healthz" || true)" = 200 ]; then
        de_volta=1
        break
    fi
    sleep 1
done
[ -n "$de_volta" ] || falhar "com o Caddy do StillOn de volta, a borda não voltou a encaminhar em 30 s"
ok "a borda segue de pé com o StillOn parado (502) e volta a encaminhar"

# ---------------------------------------------------------------------------
# 8. stillon-entrada: recusas, imagem divergente, rollback, as 3 últimas.
# ---------------------------------------------------------------------------
# 8a. Pedidos recusados com 2, antes de ler a entrada padrão ou mexer em
#     qualquer coisa. O tar da entrada padrão é o de a, válido: o que está
#     errado é só o pedido.
recusa() {
    local pedido=$1 motivo=$2 c
    shift 2
    c=$(entrada "$pedido" "$@" < "$TARS/a.tar")
    [ "$c" = 2 ] || falhar "pedido '$pedido' ($motivo) saiu $c, queria 2"
}
recusa "" "vazio"
recusa "   " "só espaços"
recusa "reiniciar" "verbo desconhecido"
recusa "DEPLOY $SHA_A $ID_A" "verbo em maiúsculas"
recusa "deploy" "sem argumentos"
recusa "deploy $SHA_A" "sem image-id"
recusa "deploy $SHA_A $ID_A extra" "palavra a mais"
recusa "deploy ${SHA_A:0:39} $ID_A" "SHA com 39 caracteres"
recusa "deploy ${SHA_A}0 $ID_A" "SHA com 41 caracteres"
recusa "deploy $(tr a-f A-F <<<"$SHA_A") $ID_A" "SHA em maiúsculas"
recusa "deploy g${SHA_A:1} $ID_A" "SHA com letra fora do hex"
recusa "deploy $SHA_A ${ID_A#sha256:}" "image-id sem sha256:"
recusa "deploy $SHA_A ${ID_A:0:70}" "image-id curto"
recusa "deploy $SHA_A;id $ID_A" "caractere de shell"
recusa 'deploy $(id) '"$ID_A" "substituição de comando"
# Com um pedido e um tar válidos: só o argumento é recusado.
c=$(entrada "deploy $SHA_A $ID_A" backup < "$TARS/a.tar")
[ "$c" = 2 ] || falhar "argumento na linha de comando saiu $c, queria 2"
no_ar "$SHA_A" || falhar "um pedido recusado mexeu na versão"
c=$(entrada "deploy $SHA_A $ID_A" < /dev/null)
[ "$c" = 2 ] || falhar "deploy sem nada na entrada padrão saiu $c, queria 2"
ok "stillon-entrada: pedidos malformados recusados com 2"

# 8b. Imagem divergente: o tar de uma imagem com o ID de outra. Nada é
#     etiquetado e a versão no ar não muda.
ID_B=$(fonte b 'LABEL stillon.teste=b')
SHA_B=$(sha_de b)
c=$(entrada "deploy $SHA_B $ID_A" < "$TARS/b.tar")
[ "$c" = 3 ] || falhar "tar de b com o ID de a saiu $c, queria 3"
docker image inspect "$REPO_TESTE:$SHA_B" >/dev/null 2>&1 && falhar "a imagem divergente foi etiquetada"
c=$(entrada "deploy $SHA_B sha256:$(printf '0%.0s' {1..64})" < "$TARS/b.tar")
[ "$c" = 3 ] || falhar "ID inexistente saiu $c, queria 3"
# Um tar COM nome (o docker save comum) é recusado antes do load: o nome
# etiquetaria outra imagem da máquina.
docker tag "$REPO_TESTE-fonte:b" "$REPO_TESTE-fonte:b-com-nome"
docker save "$REPO_TESTE-fonte:b-com-nome" -o "$TARS/b-com-nome.tar"
docker rmi "$REPO_TESTE-fonte:b-com-nome" >/dev/null
c=$(entrada "deploy $SHA_B $ID_B" < "$TARS/b-com-nome.tar")
[ "$c" = 3 ] || falhar "tar com nome saiu $c, queria 3"
docker image inspect "$REPO_TESTE-fonte:b-com-nome" >/dev/null 2>&1 && falhar "o tar com nome foi carregado (a etiqueta voltou)"
# O mesmo vale para um tar OCI sem manifest.json com o nome numa anotação do
# index.json (as duas que o containerd lê).
for anot in io.containerd.image.name org.opencontainers.image.ref.name; do
    rm -rf "$TARS/anotado" && mkdir "$TARS/anotado"
    tar -xf "$TARS/b.tar" -C "$TARS/anotado"
    jq --arg a "$anot" --arg n "docker.io/library/$REPO_TESTE-fonte:anotado" \
        '.manifests[0].annotations = {($a): $n}' "$TARS/anotado/index.json" > "$TARS/index.json"
    mv "$TARS/index.json" "$TARS/anotado/index.json"
    tar -cf "$TARS/b-anotado.tar" -C "$TARS/anotado" .
    c=$(entrada "deploy $SHA_B $ID_B" < "$TARS/b-anotado.tar")
    [ "$c" = 3 ] || falhar "tar com $anot saiu $c, queria 3"
    docker image inspect "$REPO_TESTE-fonte:anotado" >/dev/null 2>&1 && falhar "o tar com $anot foi carregado com o nome"
    docker rmi "$REPO_TESTE-fonte:anotado" >/dev/null 2>&1 || true
done
# Os desvios da revisão (I1): tars que o docker load (containerd) lê com
# nome e que uma checagem por texto deixaria passar. O nome pedido é o do
# app no ar ($REPO_TESTE:atual): se o load acontecesse, a etiqueta atual
# passaria a apontar para b, sem healthcheck e sem historico. Cada um sai 3,
# e nenhuma etiqueta muda. O pedido traz o ID certo de b: só o tar é o
# problema.
etiquetas() {
    docker image ls --no-trunc --format '{{.Repository}}:{{.Tag}} {{.ID}}' |
        grep -E "^($REPO_TESTE|$REPO_TESTE-fonte):" | sort || true
}
rm -rf "$TARS/bx" && mkdir "$TARS/bx"
tar -xf "$TARS/b.tar" -C "$TARS/bx"
NOME_ATUAL=docker.io/library/$REPO_TESTE:atual
jq -c --arg n "$NOME_ATUAL" '.manifests[0].annotations = {"io.containerd.image.name": $n}' "$TARS/bx/index.json" > "$TARS/index-nomeado.json"
# O mesmo nome com escape JSON na chave ("\u002e" é o "."), que o
# encoding/json do containerd decodifica igual.
jq -c --arg n "$NOME_ATUAL" '.manifests[0].annotations = {"CHAVE": $n}' "$TARS/bx/index.json" |
    sed 's/"CHAVE"/"io\\u002econtainerd\\u002eimage\\u002ename"/' > "$TARS/index-escapado.json"
grep -q 'u002e' "$TARS/index-escapado.json" || falhar "controle: o index.json escapado não tem o escape"
# O nome no começo e depois 1 MB de espaço em branco (JSON válido).
{
    jq -c --arg n "$NOME_ATUAL" '{manifests: [{annotations: {"io.containerd.image.name": $n}} + .manifests[0]], schemaVersion, mediaType}' "$TARS/bx/index.json"
    head -c 1048576 /dev/zero | tr '\0' '\n'
} > "$TARS/index-grande.json"
# O manifest.json do formato v1.2 (sem oci-layout), com o nome no RepoTags
# e espaço depois dos dois-pontos.
man=$(jq -r '.manifests[0].digest | sub(":"; "/")' "$TARS/bx/index.json")
jq -c --arg n "$REPO_TESTE:atual" '[{Config: ("blobs/" + (.config.digest | sub(":"; "/"))), RepoTags: [$n],
    Layers: [.layers[].digest | "blobs/" + sub(":"; "/")]}]' "$TARS/bx/blobs/$man" |
    sed 's/"RepoTags":\[/"RepoTags": [/' > "$TARS/manifest-v12.json"
grep -q '"RepoTags": \[' "$TARS/manifest-v12.json" || falhar "controle: o manifest.json não tem o espaço"
# montar <saida> <nome no tar>=<arquivo> ...: um tar com esses membros, na
# ordem, com os nomes exatamente como dados (o GNU tar os normalizaria). Em
# vez de um arquivo: @symlink:<alvo>, @hard:<alvo>, @fifo ou @dir.
montar() {
    python3 - "$@" <<'PY'
import sys, tarfile
tipos = {"@fifo": tarfile.FIFOTYPE, "@dir": tarfile.DIRTYPE}
with tarfile.open(sys.argv[1], "w", format=tarfile.PAX_FORMAT) as t:
    for par in sys.argv[2:]:
        nome, arq = par.split("=", 1)
        info = tarfile.TarInfo(nome)
        info.mode = 0o644
        if arq.startswith("@symlink:") or arq.startswith("@hard:"):
            tipo, alvo = arq[1:].split(":", 1)
            info.type = tarfile.SYMTYPE if tipo == "symlink" else tarfile.LNKTYPE
            info.linkname = alvo
            t.addfile(info)
        elif arq in tipos:
            info.type = tipos[arq]
            info.mode = 0o755 if arq == "@dir" else 0o644
            t.addfile(info)
        else:
            with open(arq, "rb") as f:
                dados = f.read()
            info.size = len(dados)
            with open(arq, "rb") as f:
                t.addfile(info, f)
PY
}
blobs=()
for f in "$TARS"/bx/blobs/sha256/*; do blobs+=("blobs/sha256/${f##*/}=$f"); done
montar "$TARS/m-ponto-barra.tar" "oci-layout=$TARS/bx/oci-layout" ".//index.json=$TARS/index-nomeado.json" "${blobs[@]}"
montar "$TARS/m-ponto-ponto.tar" "oci-layout=$TARS/bx/oci-layout" "x/../index.json=$TARS/index-nomeado.json" "${blobs[@]}"
montar "$TARS/m-duplicado.tar" "oci-layout=$TARS/bx/oci-layout" "index.json=$TARS/bx/index.json" "${blobs[@]}" "./index.json=$TARS/index-nomeado.json"
montar "$TARS/m-escape.tar" "oci-layout=$TARS/bx/oci-layout" "index.json=$TARS/index-escapado.json" "${blobs[@]}"
montar "$TARS/m-repotags.tar" "manifest.json=$TARS/manifest-v12.json" "${blobs[@]}"
montar "$TARS/m-grande.tar" "oci-layout=$TARS/bx/oci-layout" "index.json=$TARS/index-grande.json" "${blobs[@]}"
# Dois valores JSON no index.json, o nomeado e depois o limpo: o jq sem -s
# julgaria só o último, e o containerd lê o primeiro.
cat "$TARS/index-nomeado.json" "$TARS/bx/index.json" > "$TARS/index-dois.json"
montar "$TARS/m-dois-valores.tar" "oci-layout=$TARS/bx/oci-layout" "index.json=$TARS/index-dois.json" "${blobs[@]}"
# Os tipos e caminhos que o GNU tar trataria de outro jeito: o index.json
# nomeado escondido num "blob" e alcançado por link; nomes absoluto e com
# ..; um FIFO; o index.json como diretório; e um layout sem blobs.
FALSO=blobs/sha256/$(printf 'f%.0s' {1..64})
montar "$TARS/m-symlink.tar" "oci-layout=$TARS/bx/oci-layout" "$FALSO=$TARS/index-nomeado.json" "index.json=@symlink:$FALSO" "${blobs[@]}"
montar "$TARS/m-hardlink.tar" "oci-layout=$TARS/bx/oci-layout" "$FALSO=$TARS/index-nomeado.json" "index.json=@hard:$FALSO" "${blobs[@]}"
montar "$TARS/m-absoluto.tar" "oci-layout=$TARS/bx/oci-layout" "/index.json=$TARS/index-nomeado.json" "${blobs[@]}"
montar "$TARS/m-sobe.tar" "oci-layout=$TARS/bx/oci-layout" "../index.json=$TARS/index-nomeado.json" "${blobs[@]}"
montar "$TARS/m-fifo.tar" "oci-layout=$TARS/bx/oci-layout" "index.json=$TARS/bx/index.json" "$FALSO=@fifo" "${blobs[@]}"
montar "$TARS/m-index-dir.tar" "oci-layout=$TARS/bx/oci-layout" "index.json=@dir" "${blobs[@]}"
montar "$TARS/m-sem-blobs.tar" "oci-layout=$TARS/bx/oci-layout" "index.json=$TARS/bx/index.json"
# blobs ou blobs/sha256 como ARQUIVO, com os blobs depois: o tar -x falharia
# (e não seria culpa da máquina).
montar "$TARS/m-blobs-arquivo.tar" "oci-layout=$TARS/bx/oci-layout" "index.json=$TARS/bx/index.json" "blobs=$TARS/bx/oci-layout" "${blobs[@]}"
montar "$TARS/m-sha256-arquivo.tar" "oci-layout=$TARS/bx/oci-layout" "index.json=$TARS/bx/index.json" "blobs/sha256=$TARS/bx/oci-layout" "${blobs[@]}"
antes=$(etiquetas)
for m in ponto-barra ponto-ponto duplicado escape repotags grande dois-valores symlink hardlink absoluto sobe fifo index-dir sem-blobs blobs-arquivo sha256-arquivo; do
    c=$(entrada "deploy $SHA_B $ID_B" < "$TARS/m-$m.tar")
    [ "$c" = 3 ] || falhar "tar malicioso '$m' saiu $c, queria 3: $(tail -1 "$TARS/erro")"
    [ "$(etiquetas)" = "$antes" ] || falhar "tar malicioso '$m' mexeu nas etiquetas: $(diff <(echo "$antes") <(etiquetas) | tr '\n' ' ')"
    if [ "$(docker image inspect -f '{{.Id}}' "$REPO_TESTE:atual")" != "$ID_A" ]; then
        falhar "tar malicioso '$m' reetiquetou $REPO_TESTE:atual"
        # De volta, para o caso seguinte ser julgado sozinho.
        docker tag "$ID_A" "$REPO_TESTE:atual"
    fi
    antes=$(etiquetas)
done
no_ar "$SHA_A" || falhar "uma imagem divergente mexeu na versão"
# O SHA de uma versão que já existe com OUTRA imagem (aqui, o SHA no ar com a
# imagem doente): etiquetar moveria $REPO_TESTE:<sha> para a doente, e o
# rollback, que usa o mesmo SHA, subiria a doente. Recusado com 3 antes.
c=$(entrada "deploy $SHA_A $ID_DOENTE" < "$TARS/doente.tar")
[ "$c" = 3 ] || falhar "SHA existente com outra imagem saiu $c, queria 3: $(tail -1 "$TARS/erro")"
grep -q 'já existe com outra imagem' "$TARS/erro" || falhar "SHA existente com outra imagem: não disse por quê"
[ "$(docker image inspect -f '{{.Id}}' "$REPO_TESTE:$SHA_A")" = "$ID_A" ] || falhar "a etiqueta $REPO_TESTE:$SHA_A foi movida"
no_ar "$SHA_A" || falhar "o SHA existente com outra imagem mexeu na versão"
[ "$(docker inspect -f '{{.State.Health.Status}}' "$(dc ps -q app)")" = healthy ] || falhar "depois do SHA existente com outra imagem o app não está saudável"
ok "stillon-entrada: imagem divergente, tar com nome, os desvios de layout e o SHA já etiquetado recusados com 3"

# 8c. Rollback: um deploy que não fica saudável volta à versão anterior. O
#     entrada.log recomeça vazio: o canário do 2a não vale aqui.
: > "$DIR/entrada.log"
c=$(entrada "deploy $SHA_DOENTE $ID_DOENTE" < "$TARS/doente.tar")
[ "$c" = 4 ] || falhar "deploy doente saiu $c, queria 4: $(estado)"
grep -q 'rollback feito' "$TARS/erro" || falhar "o deploy doente não disse que fez o rollback"
sem_canario "deploy doente com rollback"
[ "$(versao)" = "$SHA_A" ] || falhar "depois do rollback, versao = $(versao)"
[ "$(imagem_do_app)" = "$REPO_TESTE:$SHA_A" ] || falhar "depois do rollback o app roda $(imagem_do_app)"
[ "$(docker image inspect -f '{{.Id}}' "$REPO_TESTE:atual")" = "$ID_A" ] || falhar "depois do rollback, atual não é a versão no ar"
[ "$(docker inspect -f '{{.State.Health.Status}}' "$(dc ps -q app)")" = healthy ] || falhar "depois do rollback o app não está saudável: $(estado)"
[ "$(cu -o /dev/null -w '%{http_code}' "$BASE/healthz")" = 200 ] || falhar "depois do rollback, /healthz pela borda não deu 200"
ok "stillon-entrada: deploy doente volta à versão anterior (4)"

# 8c'. Rollback por tempo esgotado: um app que fica de pé e nunca saudável
#      (o processo não cai; o healthcheck sempre falha). A imagem doente de
#      cima morre no boot e o --wait volta em segundos; esta prova a espera
#      de ESPERA_S inteira. A base é o ubuntu:24.04 (o mesmo do 8f, com sh):
#      a do db declara VOLUME, e cada contêiner dela deixaria um volume
#      anônimo para trás no rollback.
printf '%s\n' "FROM ubuntu:24.04" \
    "RUN printf '#!/bin/sh\\nexit 1\\n' > /stillon && chmod 755 /stillon" \
    'ENTRYPOINT ["/bin/sh", "-c", "trap \"exit 0\" TERM; while :; do sleep 1; done", "--"]' |
    docker build -q -t "$REPO_TESTE-fonte:lento" - >/dev/null
ID_LENTO=$(oci_tar "$REPO_TESTE-fonte:lento" "$TARS/lento.tar")
SHA_LENTO=$(sha_de lento)
inicio=$(date +%s)
c=$(entrada "deploy $SHA_LENTO $ID_LENTO" < "$TARS/lento.tar")
duracao=$(($(date +%s) - inicio))
[ "$c" = 4 ] || falhar "deploy que nunca fica saudável saiu $c (em $duracao s), queria 4: $(estado)"
grep -q 'rollback feito' "$TARS/erro" || falhar "o deploy lento não disse que fez o rollback"
[ "$duracao" -ge 45 ] || falhar "o deploy lento voltou em $duracao s, antes da espera de 45 s: não foi o tempo esgotado"
[ "$(versao)" = "$SHA_A" ] || falhar "depois do rollback por tempo, versao = $(versao)"
[ "$(imagem_do_app)" = "$REPO_TESTE:$SHA_A" ] || falhar "depois do rollback por tempo o app roda $(imagem_do_app)"
[ "$(docker inspect -f '{{.State.Health.Status}}' "$(dc ps -q app)")" = healthy ] || falhar "depois do rollback por tempo o app não está saudável: $(estado)"
ok "stillon-entrada: deploy que nunca fica saudável volta à anterior depois da espera (4)"

# 8c''. A conexão cai no meio do deploy (I1): o runner cancelado ou perdido
#       fecha o canal do SSH, e a stderr da entrada vira um pipe sem leitor. A
#       entrada termina o que começou (aqui, o rollback de um deploy doente) e
#       o log inteiro fica no entrada.log. A stderr vai para um head que lê a
#       primeira linha e sai. O entrada.log começa acima do teto: é rodado.
head -c $((1024 * 1024 + 1)) /dev/zero > "$DIR/entrada.log"
c=0
env -i PATH="$PATH" HOME="$HOME" SSH_ORIGINAL_COMMAND="deploy $SHA_DOENTE $ID_DOENTE" \
    bash "$ENTRADA" < "$TARS/doente.tar" > "$TARS/saida" 2> >(head -n 1 > "$TARS/primeira") || c=$?
[ "$c" = 4 ] || falhar "com a conexão caída no meio, o deploy doente saiu $c, queria 4 (rollback feito): $(estado; tail -n 12 "$DIR/entrada.log")"
no_ar "$SHA_A" || falhar "com a conexão caída no meio, o rollback não aconteceu: versao $(versao), app $(imagem_do_app)"
[ "$(docker inspect -f '{{.State.Health.Status}}' "$(dc ps -q app)")" = healthy ] ||
    falhar "com a conexão caída no meio, o app não ficou saudável"
grep -q 'rollback feito' "$DIR/entrada.log" || falhar "o entrada.log não registrou o rollback"
# A linha final (a que o dono lê depois de uma queda do SSH): o pedido e o código.
tail -n 1 "$DIR/entrada.log" | grep -Eq "^== [0-9TZ:-]+ fim: deploy $SHA_DOENTE $ID_DOENTE: código 4 \(" ||
    falhar "a última linha do entrada.log não é o fim do deploy com o código 4: $(tail -n 1 "$DIR/entrada.log")"
grep -q 'ARGON2_MAX_CONCURRENT' "$DIR/entrada.log" || falhar "o entrada.log não tem o log do app"
[ "$(stat -c %s "$DIR/entrada.log")" -lt 1048576 ] || falhar "o entrada.log acima do teto não foi rodado"
[ "$(stat -c %s "$DIR/entrada.log.1" 2>/dev/null || echo 0)" -gt 1048576 ] || falhar "o entrada.log rodado não ficou em entrada.log.1"
[ "$(stat -c %a "$DIR/entrada.log")" = 644 ] || falhar "o entrada.log com modo $(stat -c %a "$DIR/entrada.log"), queria 644"
rm -f "$DIR/entrada.log.1"
ok "stillon-entrada: a conexão cai no meio e o rollback acontece; entrada.log com teto"

# 8c'''. O compose que diz "saudável" sem estar: até a v5.5.0, o `up --wait
#        --wait-timeout` sai 0 quando o prazo vence com o app em `starting`
#        (uma corrida; o 8c' a pega só às vezes). Aqui, sempre: um docker
#        falso na frente do PATH tira o --wait do `compose up` e sai 0. A
#        entrada não pode acreditar nele: confere ela mesma o contêiner novo,
#        faz o rollback (que o compose falso também diz saudável) e sai 4.
mkdir -p "$TARS/bin-compose"
cat > "$TARS/bin-compose/docker" <<FIM
#!/usr/bin/env bash
if [[ " \$* " == *" compose "*" up "* ]]; then
    a=()
    pula=0
    for x in "\$@"; do
        if [ "\$pula" = 1 ]; then pula=0; continue; fi
        case "\$x" in --wait) continue ;; --wait-timeout) pula=1; continue ;; esac
        a+=("\$x")
    done
    "$(command -v docker)" "\${a[@]}" || true
    exit 0
fi
exec "$(command -v docker)" "\$@"
FIM
chmod +x "$TARS/bin-compose/docker"
sed 's|^export PATH=.*|export PATH="$PATH"|' "$ENTRADA" > "$TARS/entrada-compose"
c=0
env -i PATH="$TARS/bin-compose:$PATH" HOME="$HOME" SSH_ORIGINAL_COMMAND="deploy $SHA_DOENTE $ID_DOENTE" \
    bash "$TARS/entrada-compose" < "$TARS/doente.tar" > "$TARS/saida" 2> "$TARS/erro" || c=$?
[ "$c" = 4 ] || falhar "com o compose que diz saudável sem esperar, o deploy doente saiu $c, queria 4: $(estado)"
grep -q 'rollback feito' "$TARS/erro" || falhar "com o compose que diz saudável sem esperar, não houve rollback: $(estado)"
no_ar "$SHA_A" || falhar "com o compose que diz saudável sem esperar, versao $(versao), app $(imagem_do_app)"
[ "$(docker inspect -f '{{.State.Health.Status}}' "$(dc ps -q app)")" = healthy ] ||
    falhar "com o compose que diz saudável sem esperar, o app não ficou saudável: $(estado)"
# O mesmo compose falso com a imagem lenta (de pé e sempre em `starting` na
# espera): o caso da causa raiz, sempre. O `up` volta na hora; a entrada
# espera ela mesma até ESPERA_S, sai pelo prazo (e não por um `unhealthy`,
# que só viria depois dele) e faz o rollback.
inicio=$(date +%s)
c=0
env -i PATH="$TARS/bin-compose:$PATH" HOME="$HOME" SSH_ORIGINAL_COMMAND="deploy $SHA_LENTO $ID_LENTO" \
    bash "$TARS/entrada-compose" < "$TARS/lento.tar" > "$TARS/saida" 2> "$TARS/erro" || c=$?
duracao=$(($(date +%s) - inicio))
[ "$c" = 4 ] || falhar "com o compose falso, o deploy lento saiu $c (em $duracao s), queria 4: $(estado)"
[ "$duracao" -ge 45 ] || falhar "com o compose falso, o deploy lento voltou em $duracao s, antes da espera de 45 s"
grep -q 'segue em starting depois de 45 s' "$TARS/erro" ||
    falhar "com o compose falso, o deploy lento não saiu pelo prazo da espera: $(estado)"
grep -q 'rollback feito' "$TARS/erro" || falhar "com o compose falso, o deploy lento não fez o rollback: $(estado)"
no_ar "$SHA_A" || falhar "com o compose falso e o deploy lento, versao $(versao), app $(imagem_do_app)"
[ "$(docker inspect -f '{{.State.Health.Status}}' "$(dc ps -q app)")" = healthy ] ||
    falhar "com o compose falso e o deploy lento, o app não ficou saudável: $(estado)"
ok "stillon-entrada: não acredita no código do up --wait; confere ela mesma o contêiner novo e espera o prazo (4)"

# 8d. Deploys seguidos: versao-anterior anda, e ficam as 3 últimas imagens
#     (as 3 últimas que ficaram no ar; a doente sai), fora a etiqueta atual.
ID_C=$(fonte c 'LABEL stillon.teste=c')
SHA_C=$(sha_de c)
ID_D=$(fonte d 'LABEL stillon.teste=d')
SHA_D=$(sha_de d)
# O tar de c sem o "./" na frente dos nomes (tar -C dir oci-layout …): as
# duas formas do layout OCI passam.
rm -rf "$TARS/cx" && mkdir "$TARS/cx" && tar -xf "$TARS/c.tar" -C "$TARS/cx"
tar -cf "$TARS/c.tar" -C "$TARS/cx" oci-layout index.json blobs
tar -tf "$TARS/c.tar" | grep -q '^\./' && falhar "controle: o tar de c ainda tem ./"
# Um historico longo (I2): 2000 versões antigas, como depois de anos de
# deploy. A poda não pode morrer de SIGPIPE, e o historico não cresce sem
# limite.
for i in $(seq 1 2000); do sha_de "antiga-$i"; done >> "$DIR/historico"
# O temporário de um deploy morto por SIGKILL (o trap não roda): o próximo
# deploy o apaga, já sob a trava.
mkdir -p "$DIR/.stillon-entrada.morto"
head -c 4096 /dev/urandom > "$DIR/.stillon-entrada.morto/imagem.tar"
for par in "b $SHA_B $ID_B" "c $SHA_C $ID_C" "d $SHA_D $ID_D"; do
    read -r nome sha id <<<"$par"
    c=$(entrada "deploy $sha $id" < "$TARS/$nome.tar")
    [ "$c" = 0 ] || falhar "deploy de $nome saiu $c: $(tail -3 "$TARS/erro")"
done
[ "$(versao)" = "$SHA_D" ] || falhar "depois de b, c e d, versao = $(versao)"
[ "$(cat "$DIR/versao-anterior" 2>/dev/null)" = "$SHA_C" ] || falhar "versao-anterior não é a de c"
restantes=$(docker image ls "$REPO_TESTE" --format '{{.Tag}}' | grep -vx atual | sort | tr '\n' ' ')
esperadas=$(printf '%s\n' "$SHA_B" "$SHA_C" "$SHA_D" | sort | tr '\n' ' ')
[ "$restantes" = "$esperadas" ] || falhar "imagens que sobraram: '$restantes', queria as de b, c e d"
[ -e "$DIR/.stillon-entrada.morto" ] && falhar "o temporário de um deploy morto ficou para trás"
[ "$(wc -l < "$DIR/historico")" -le 50 ] || falhar "o historico ficou com $(wc -l < "$DIR/historico") linhas: cresce sem limite"
[ "$(tail -n 3 "$DIR/historico" | tr '\n' ' ')" = "$SHA_B $SHA_C $SHA_D " ] || falhar "o historico perdeu as últimas versões"
# O mesmo SHA de novo (redeploy) é aceito e não mexe na anterior. Com
# variáveis do docker e do compose no ambiente (que o sudo tiraria): a
# entrada não as usa.
c=0
env -i PATH="$PATH" HOME="$HOME" SSH_ORIGINAL_COMMAND="deploy $SHA_D $ID_D" LANG=C.UTF-8 \
    DOCKER_HOST=unix:///nada.sock DOCKER_CONTEXT=nada COMPOSE_PROJECT_NAME=outro COMPOSE_FILE=/nada.yaml \
    bash "$ENTRADA" < "$TARS/d.tar" > "$TARS/saida" 2> "$TARS/erro" || c=$?
[ "$c" = 0 ] || falhar "redeploy de d (com DOCKER_* e COMPOSE_* no ambiente) saiu $c: $(tail -3 "$TARS/erro")"
[ "$(cat "$DIR/versao-anterior")" = "$SHA_C" ] || falhar "o redeploy de d mudou a versao-anterior"
[ "$(imagem_do_app)" = "$REPO_TESTE:$SHA_D" ] || falhar "depois do redeploy o app roda $(imagem_do_app)"
# versao, versao-anterior e historico legíveis sem sudo (não são segredo),
# apesar do umask 077 da entrada.
for f in versao versao-anterior historico; do
    [ "$(stat -c %a "$DIR/$f")" = 644 ] || falhar "$f com modo $(stat -c %a "$DIR/$f"), queria 644"
done
ok "stillon-entrada: versao-anterior, as 3 últimas imagens, historico longo, temporário velho, ambiente ignorado, modos 644"

# 8d'. A etiqueta de um SHA que já existe com outra imagem: recusada (3) só se
#      o SHA é um alvo do rollback (versao ou versao-anterior; o da versao é o
#      8b). Qualquer outro SHA (um publish refeito, uma tag v* refeita) troca a
#      etiqueta velha e segue.
c=$(entrada "deploy $SHA_C $ID_A" < "$TARS/a.tar")
[ "$c" = 3 ] || falhar "o SHA da versao-anterior com outra imagem saiu $c, queria 3: $(tail -1 "$TARS/erro")"
grep -q 'já existe com outra imagem' "$TARS/erro" || falhar "o SHA da versao-anterior com outra imagem: não disse por quê"
[ "$(docker image inspect -f '{{.Id}}' "$REPO_TESTE:$SHA_C")" = "$ID_C" ] || falhar "a etiqueta $REPO_TESTE:$SHA_C (versao-anterior) foi movida"
no_ar "$SHA_D" || falhar "o SHA da versao-anterior com outra imagem mexeu na versão"
c=$(entrada "deploy $SHA_B $ID_A" < "$TARS/a.tar")
[ "$c" = 0 ] || falhar "um SHA fora do rollback (b) com outra imagem saiu $c, queria 0: $(tail -3 "$TARS/erro")"
[ "$(docker image inspect -f '{{.Id}}' "$REPO_TESTE:$SHA_B")" = "$ID_A" ] || falhar "a etiqueta velha de b não foi trocada pela imagem nova"
no_ar "$SHA_B" || falhar "depois de trocar a etiqueta de b, versao $(versao), app $(imagem_do_app)"
# De volta a d (o 8e' parte dele).
c=$(entrada "deploy $SHA_D $ID_D" < "$TARS/d.tar")
if ! { [ "$c" = 0 ] && no_ar "$SHA_D"; }; then falhar "a volta para d saiu $c: $(tail -3 "$TARS/erro")"; fi
ok "stillon-entrada: SHA etiquetado com outra imagem, 3 se é a versao-anterior, trocado se não é alvo do rollback"

# 8e. Docker sem o image store containerd: recusa com 1. Um docker falso na
#     frente do PATH responde ao `docker info` como o store clássico.
mkdir -p "$TARS/bin"
printf '#!/bin/sh\nif [ "$1" = info ]; then echo "[[Backing Filesystem extfs] [Supports d_type true]]"; exit 0; fi\nexec %s "$@"\n' \
    "$(command -v docker)" > "$TARS/bin/docker"
chmod +x "$TARS/bin/docker"
sed 's|^export PATH=.*|export PATH="$PATH"|' "$ENTRADA" > "$TARS/entrada-path"
c=0
env -i PATH="$TARS/bin:$PATH" HOME="$HOME" SSH_ORIGINAL_COMMAND="deploy $SHA_A $ID_A" \
    bash "$TARS/entrada-path" < "$TARS/a.tar" > /dev/null 2> "$TARS/erro" || c=$?
[ "$c" = 1 ] || falhar "sem o store containerd a entrada saiu $c, queria 1"
grep -q 'containerd' "$TARS/erro" || falhar "sem o store containerd a entrada não disse por quê"
ok "stillon-entrada: recusa o Docker sem o image store containerd"

# 8e'. Falhas da máquina, não do tar: 1, e nada etiquetado. (a) O tar não
#      extrai ou não refaz (disco cheio): um tar falso na frente do PATH que
#      falha no -x ou no -c. (b) Sem jq: um PATH com tudo de /usr/bin e /bin
#      menos ele.
mkdir -p "$TARS/bin-tar" "$TARS/semjq"
printf '#!/bin/sh\nfor a; do case "$a" in -%s*) echo "tar: sem espaço" >&2; exit 2 ;; esac; done\nexec %s "$@"\n' \
    "\${FALHAR_TAR:-nada}" "$(command -v tar)" > "$TARS/bin-tar/tar"
chmod +x "$TARS/bin-tar/tar"
for d in /usr/local/bin /usr/bin /bin /usr/sbin /sbin; do
    for f in "$d"/*; do
        [ "${f##*/}" = jq ] || [ -e "$TARS/semjq/${f##*/}" ] || ln -s "$f" "$TARS/semjq/${f##*/}"
    done
done
ln -sf "$(command -v docker)" "$TARS/semjq/docker"
antes=$(etiquetas)
for caso in "x FALHAR_TAR=x $TARS/bin-tar:$PATH" "c FALHAR_TAR=c $TARS/bin-tar:$PATH" "jq NADA=1 $TARS/semjq"; do
    read -r nome var caminho <<<"$caso"
    c=0
    env -i PATH="$caminho" HOME="$HOME" SSH_ORIGINAL_COMMAND="deploy $SHA_D $ID_D" "$var" \
        bash "$TARS/entrada-path" < "$TARS/d.tar" > /dev/null 2> "$TARS/erro" || c=$?
    [ "$c" = 1 ] || falhar "deploy com falha de máquina ($nome) saiu $c, queria 1: $(tail -1 "$TARS/erro")"
    [ "$(etiquetas)" = "$antes" ] || falhar "deploy com falha de máquina ($nome) mexeu nas etiquetas"
done
grep -q 'docs/deploy/vps.md' "$TARS/erro" || falhar "sem jq, a entrada não apontou o runbook: $(tail -1 "$TARS/erro")"
no_ar "$SHA_D" || falhar "uma falha de máquina mexeu na versão"
ok "stillon-entrada: tar que não extrai ou não refaz, e jq ausente, saem 1 sem etiquetar"

# 8f. O sudoers: sintaxe (visudo) e efeito, num Ubuntu 24.04 descartável com
#     o usuário deploy e uma entrada falsa que mostra o que recebeu.
printf '#!/bin/sh\necho "args=$# pedido=${SSH_ORIGINAL_COMMAND:-} outra=${OUTRA:-} usuario=$(id -un)"\n' > "$TARS/entrada-falsa"
cat > "$TARS/sudo.sh" <<'FIM'
set -eu
apt-get update -qq >/dev/null && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq sudo >/dev/null
useradd -m -s /bin/bash deploy
install -m 0755 /t/entrada-falsa /usr/local/sbin/stillon-entrada
install -m 0440 /t/sudoers /etc/sudoers.d/stillon-entrada
visudo -cf /etc/sudoers.d/stillon-entrada
visudo -c >/dev/null
como_deploy() { su deploy -c "$1" 2>&1 || echo "negado"; }
echo "A $(como_deploy 'SSH_ORIGINAL_COMMAND="deploy abc" OUTRA=x sudo -n /usr/local/sbin/stillon-entrada')"
echo "B $(como_deploy 'sudo -n /usr/local/sbin/stillon-entrada backup' | tail -1)"
echo "C $(como_deploy 'sudo -n /bin/sh -c id' | tail -1)"
echo "D $(como_deploy 'sudo -n /usr/bin/id' | tail -1)"
FIM
cp "$RAIZ/deploy/vps/sudoers-stillon-entrada" "$TARS/sudoers"
chmod 644 "$TARS/entrada-falsa" "$TARS/sudo.sh" "$TARS/sudoers"
chmod 755 "$TARS"
docker run --rm -v "$TARS:/t:ro" ubuntu:24.04 bash /t/sudo.sh > "$TARS/sudo.log" 2>&1 ||
    falhar "o teste do sudoers não rodou: $(tail -5 "$TARS/sudo.log")"
grep -qx 'A args=0 pedido=deploy abc outra= usuario=root' "$TARS/sudo.log" ||
    falhar "sudoers: o deploy não roda a entrada como root com o SSH_ORIGINAL_COMMAND (e só ele): $(grep '^A ' "$TARS/sudo.log")"
grep -qx 'B negado' "$TARS/sudo.log" || falhar "sudoers: a entrada aceitou argumento: $(grep '^B ' "$TARS/sudo.log")"
grep -qx 'C negado' "$TARS/sudo.log" || falhar "sudoers: o deploy rodou um shell como root"
grep -qx 'D negado' "$TARS/sudo.log" || falhar "sudoers: o deploy rodou outro comando como root"
ok "sudoers: só a entrada, sem argumento, com o SSH_ORIGINAL_COMMAND"

# ---------------------------------------------------------------------------
# 9. stillon-entrada backup: só texto cifrado na saída padrão.
# ---------------------------------------------------------------------------
# Os volumes antes dos backups: no fim do bloco, nenhum volume novo fora os da
# pilha (o descartável, inclusive o de um backup morto, sai com os dele).
volumes() {
    docker volume ls -q | sort | comm -23 - <(docker volume ls -q --filter "label=com.docker.compose.project=$PROJETO" | sort)
}
volumes_antes=$(volumes)
# Os scripts do backup no layout que a entrada espera ($DIR/deploy), tais e
# quais os do repositório; a chave do age gerada aqui (a privada fica em
# $TARS, só para conferir).
mkdir -p "$DIR/deploy/backup" "$DIR/deploy/compose"
cp "$RAIZ/deploy/backup/backup.sh" "$RAIZ/deploy/backup/verificar.sql" "$DIR/deploy/backup/"
cp "$RAIZ/deploy/compose/initdb.sql" "$DIR/deploy/compose/"
age-keygen -o "$TARS/chave-age" 2>/dev/null
age-keygen -y "$TARS/chave-age" > "$DIR/age-recipient"

c=$(entrada "backup extra" < /dev/null)
[ "$c" = 2 ] || falhar "backup com argumento saiu $c, queria 2"

# Fronteiras Docker da distribuição: somente a cópia da entrada recebe um
# wrapper de teste. O script de produção não possui chave de desativação.
mkdir -p "$TARS/bin-distribuicao"
docker_real=$(command -v docker)
cat > "$TARS/bin-distribuicao/docker" <<'FIM'
#!/usr/bin/env bash
set -eu
atingiu() { printf 'fixture-distribuicao:%s\n' "$CASO_DISTRIBUICAO" >> "$FIXTURE_STAGE_FILE"; }
case "$CASO_DISTRIBUICAO:$1" in
    create:create) atingiu; exit 1 ;;
    copia-fonte:cp) case "$2" in *:/stillon) atingiu; exit 1 ;; esac ;;
    copia-destino:cp) case "$3" in *:/stillon) atingiu; exit 1 ;; esac ;;
    protocolo:exec | antigo:exec | probe-travado:exec)
        if [ "${*: -2}" = 'version --backup-snapshot-protocol' ]; then
            atingiu
            if [ "$CASO_DISTRIBUICAO" = probe-travado ]; then
                # Processo REAL no descartável: perder o cliente Docker não
                # basta; o trap da entrada deve remover o contêiner exato.
                exec "$FIXTURE_DOCKER_BIN" exec --user 65532:65532 "$4" sleep 600
            fi
            [ "$CASO_DISTRIBUICAO" != antigo ] || exit 2
            printf 'protocolo-incompativel\n'; exit 0
        fi ;;
    kill-sem-cid:create)
        "$FIXTURE_DOCKER_BIN" "$@"
        atingiu
        anterior=
        for arg in "$@"; do
            [ "$anterior" != --cidfile ] || rm -f "$arg"
            anterior=$arg
        done
        kill -KILL "$PPID"
        exit 0 ;;
    kill-com-cid:cp)
        case "$2" in *:/stillon) atingiu; kill -KILL "$PPID"; exit 0 ;; esac ;;
esac
exec "$FIXTURE_DOCKER_BIN" "$@"
FIM
chmod 0755 "$TARS/bin-distribuicao/docker"
sed 's|^export PATH=.*|export PATH="$PATH"|' "$ENTRADA" > "$TARS/entrada-distribuicao"
for caso_distribuicao in create copia-fonte copia-destino protocolo antigo probe-travado kill-sem-cid kill-com-cid; do
    c=0
    inicio_distribuicao=$SECONDS
    : > "$TARS/fronteira-distribuicao"
    env -i PATH="$TARS/bin-distribuicao:$PATH" HOME="$HOME" SSH_ORIGINAL_COMMAND=backup \
        CASO_DISTRIBUICAO="$caso_distribuicao" FIXTURE_DOCKER_BIN="$docker_real" FIXTURE_STAGE_FILE="$TARS/fronteira-distribuicao" \
        bash "$TARS/entrada-distribuicao" < /dev/null > "$TARS/saida" 2> "$TARS/erro" || c=$?
    grep -qx "fixture-distribuicao:$caso_distribuicao" "$TARS/fronteira-distribuicao" ||
        falhar "$caso_distribuicao: não atingiu fronteira injetada: $(tail -3 "$TARS/erro")"
    if [ "$caso_distribuicao" = probe-travado ]; then
        [ "$((SECONDS - inicio_distribuicao))" -ge 25 ] || falhar "controle do probe travado não bloqueou realmente"
        [ "$((SECONDS - inicio_distribuicao))" -le 45 ] || falhar "probe travado escapou do prazo e limpeza"
        [ -z "$(docker ps -aq --filter "label=io.stillon.backup.raiz=$DIR")" ] || falhar "probe travado deixou processo/contêiner próprio"
    fi
    case "$caso_distribuicao" in
        kill-*) [ "$c" = 137 ] || falhar "$caso_distribuicao saiu $c, queria SIGKILL/137"
            [ -n "$(docker ps -aq --filter "label=io.stillon.backup.raiz=$DIR" --filter label=io.stillon.backup.tipo=helper)" ] ||
                falhar "$caso_distribuicao: controle não deixou auxiliar recuperável" ;;
        *) [ "$c" = 1 ] || falhar "$caso_distribuicao saiu $c, queria 1" ;;
    esac
    [ ! -s "$TARS/saida" ] || falhar "$caso_distribuicao publicou saída"
    grep -q '^== origem' "$TARS/erro" && falhar "$caso_distribuicao conectou à origem antes do probe válido"
    # A entrada seguinte, sem wrapper, precisa recuperar e produzir backup.
    c=$(entrada backup < /dev/null)
    [ "$c" = 0 ] || falhar "$caso_distribuicao: recuperação saiu $c: $(tail -3 "$TARS/erro")"
    [ "$(head -c 21 "$TARS/saida")" = age-encryption.org/v1 ] || falhar "$caso_distribuicao: recuperação sem cifrado"
    [ -z "$(docker ps -aq --filter "label=io.stillon.backup.raiz=$DIR")" ] || falhar "$caso_distribuicao: recurso próprio ficou"
done
ok "distribuição: create/cópias/protocolo recusam antes da origem; SIGKILL com/sem CID recuperado"

# O que um backup morto por SIGKILL deixaria: o dump em claro na memória e o
# postgres descartável. O próximo backup apaga os dois, já sob a trava.
nonce_morto=1111222233334444
pasta_morta=$TARS/stillon-backup.$nonce_morto
mkdir -m 0700 "$pasta_morta"
printf '%s\n' "$DIR" > "$pasta_morta/.stillon-backup-owner"
echo 'PGDMP em claro' > "$pasta_morta/stillon.dump"
pg_id=$(docker image inspect -f '{{.Id}}' "$pg")
# Sem tmpfs: o volume anônimo deve ser removido junto do ID próprio.
morto=$(docker run -d --name "stillon-restauro-$nonce_morto" \
    --label "io.stillon.backup.raiz=$DIR" --label io.stillon.backup.tipo=restauro \
    --label "io.stillon.backup.nonce=$nonce_morto" --label "io.stillon.backup.imagem=$pg_id" \
    --entrypoint sleep "$pg_id" 600)
[ -n "$(docker inspect -f '{{range .Mounts}}{{if eq .Type "volume"}}{{.Name}}{{end}}{{end}}' "$morto")" ] ||
    falhar "controle: o descartável morto do teste não tem volume anônimo"
# Prefixo semelhante SEM labels não é propriedade da entrada. A fixture
# guarda o ID exato para sua própria limpeza, incluindo o volume anônimo.
TERCEIRO_BACKUP=$(docker run -d --name "stillon-restauro-controle-${DIR##*/}" --entrypoint sleep "$pg_id" 600)
volume_terceiro=$(docker inspect -f '{{range .Mounts}}{{if eq .Type "volume"}}{{.Name}}{{end}}{{end}}' "$TERCEIRO_BACKUP")
[ -n "$volume_terceiro" ] || falhar "controle terceiro não tem volume"
dc stop app >/dev/null
c=$(entrada backup < /dev/null)
dc start app >/dev/null
[ ! -e "$pasta_morta" ] || falhar "o dump em claro de um backup morto ficou para trás"
docker inspect "$morto" >/dev/null 2>&1 && falhar "o postgres descartável próprio morto ficou para trás"
docker inspect "$TERCEIRO_BACKUP" >/dev/null 2>&1 || falhar "a entrada apagou terceiro de prefixo semelhante"
docker volume inspect "$volume_terceiro" >/dev/null 2>&1 || falhar "a entrada apagou volume do terceiro"
if [ "$c" != 0 ]; then
    cat "$TARS/erro" >&2
    falhar "backup saiu $c, queria 0"
fi
grep -q 'backup conferido' "$TARS/erro" || falhar "o log do backup não diz 'backup conferido'"
[ "$(head -c 21 "$TARS/saida")" = "age-encryption.org/v1" ] || falhar "a saída padrão do backup não começa com o cabeçalho do age"
grep -aq 'PGDMP' "$TARS/saida" && falhar "a saída padrão do backup tem o dump em claro (PGDMP)"
grep -aqE 'stillon_app|goose_db_version|CREATE TABLE|backup conferido' "$TARS/saida" && falhar "a saída padrão do backup tem texto em claro"
# Decifrado com a privada, é um dump do pg_dump que o pg_restore lê.
if age -d -i "$TARS/chave-age" -o "$TARS/decifrado.dump" "$TARS/saida"; then
    [ "$(head -c 5 "$TARS/decifrado.dump")" = PGDMP ] || falhar "o backup decifrado não é um dump do pg_dump"
    chmod 644 "$TARS/decifrado.dump"
    docker run --rm -v "$TARS/decifrado.dump:/d.dump:ro" "$pg" pg_restore -l /d.dump > "$TARS/lista" 2>/dev/null || true
    grep -q 'TABLE public users' "$TARS/lista" || falhar "o dump decifrado não tem a tabela users"
    rm -f "$TARS/decifrado.dump"
else
    falhar "o backup não decifra com a chave privada"
fi
# Nada sobra: nem o postgres descartável, nem o dump em claro.
[ -z "$(docker ps -aq --filter "label=io.stillon.backup.raiz=$DIR" --filter label=io.stillon.backup.tipo=restauro)" ] || falhar "o postgres descartável do backup ficou para trás"
ls "$TARS"/stillon-backup.* >/dev/null 2>&1 && falhar "o diretório do dump em claro ficou para trás"
ok "stillon-entrada backup: conferido, só o cifrado na saída padrão"

# A senha do backup no .env com aspas em volta e CRLF (o compose tira as
# aspas): o backup a lê igual. Com um caractere que o compose e a URL do
# backup leriam diferente: recusa com 1, antes do dump.
cp "$DIR/.env" "$TARS/env.original"
senha_bk=$(env_do_teste STILLON_BACKUP_PASSWORD)
for forma in "\"$senha_bk\"\r" "'$senha_bk'"; do
    grep -v '^STILLON_BACKUP_PASSWORD=' "$TARS/env.original" > "$DIR/.env"
    printf 'STILLON_BACKUP_PASSWORD=%b\n' "$forma" >> "$DIR/.env"
    c=$(entrada backup < /dev/null)
    [ "$c" = 0 ] || falhar "backup com a senha escrita como $(printf %q "$forma") no .env saiu $c, queria 0: $(tail -2 "$TARS/erro")"
    [ "$(head -c 21 "$TARS/saida")" = "age-encryption.org/v1" ] || falhar "backup com a senha entre aspas não mandou o cifrado"
done
grep -v '^STILLON_BACKUP_PASSWORD=' "$TARS/env.original" > "$DIR/.env"
printf 'STILLON_BACKUP_PASSWORD=%s$x\n' "$senha_bk" >> "$DIR/.env"
c=$(entrada backup < /dev/null)
[ "$c" = 1 ] || falhar "backup com \$ na senha saiu $c, queria 1"
grep -q 'STILLON_BACKUP_PASSWORD' "$TARS/erro" || falhar "backup com \$ na senha não disse por quê: $(tail -1 "$TARS/erro")"
grep -q 'postgres descartável' "$TARS/erro" && falhar "backup com \$ na senha fez o dump antes de recusar"
[ ! -s "$TARS/saida" ] || falhar "backup com \$ na senha escreveu na saída padrão"
cp "$TARS/env.original" "$DIR/.env"
ok "stillon-entrada backup: senha com aspas e CRLF lida igual ao compose; caractere ambíguo recusado (1)"

# Sem a chave pública: 1, e nada na saída padrão.
mv "$DIR/age-recipient" "$DIR/age-recipient.fora"
c=$(entrada backup < /dev/null)
[ "$c" = 1 ] || falhar "backup sem age-recipient saiu $c, queria 1"
[ ! -s "$TARS/saida" ] || falhar "backup sem age-recipient escreveu na saída padrão"
grep -q 'postgres descartável' "$TARS/erro" && falhar "backup sem age-recipient fez o dump antes de recusar"
mv "$DIR/age-recipient.fora" "$DIR/age-recipient"
# A cópia que não passa nas checagens: 3, e nada na saída padrão. O
# verificar.sql do diretório da entrada é trocado por um que sempre falha
# (o do repositório não muda).
cp "$DIR/deploy/backup/verificar.sql" "$TARS/verificar.sql"
printf '\\set ON_ERROR_STOP on\nDO $$ BEGIN RAISE EXCEPTION %s; END $$;\n' "'checagem do teste'" > "$DIR/deploy/backup/verificar.sql"
c=$(entrada backup < /dev/null)
[ "$c" = 3 ] || falhar "backup com a checagem falhando saiu $c, queria 3"
[ ! -s "$TARS/saida" ] || falhar "backup com a checagem falhando escreveu na saída padrão"
cp "$TARS/verificar.sql" "$DIR/deploy/backup/verificar.sql"
[ -z "$(docker ps -aq --filter "label=io.stillon.backup.raiz=$DIR" --filter label=io.stillon.backup.tipo=restauro)" ] || falhar "o postgres descartável ficou para trás depois da falha"
ok "stillon-entrada backup: sem chave (1) e cópia reprovada (3) não mandam nada"

# O código 4: o backup vai, cifrado, e o código avisa do disco. Um df falso na
# frente do PATH: (a) o disco da VPS acima de 85% (o df da entrada); (b) o
# banco acima de 85% do volume (o backup.sh sai 4, pelo VOLUME_BYTES que a
# entrada mede).
mkdir -p "$TARS/bin-df"
printf '#!/bin/sh\ncase "$*" in\n*--output=pcent*) [ -n "${DF_PCT:-}" ] && { echo Use%%; echo " $DF_PCT%%"; exit 0; } ;;\n*--output=size*) [ -n "${DF_TAMANHO:-}" ] && { echo 1B-blocks; echo "$DF_TAMANHO"; exit 0; } ;;\nesac\nexec %s "$@"\n' \
    "$(command -v df)" > "$TARS/bin-df/df"
chmod +x "$TARS/bin-df/df"
sed 's|^export PATH=.*|export PATH="$PATH"|' "$ENTRADA" > "$TARS/entrada-df"
for caso in "DF_PCT=91 disco da VPS" "DF_TAMANHO=1000000 volume do banco"; do
    read -r var motivo <<<"$caso"
    c=0
    env -i PATH="$TARS/bin-df:$PATH" HOME="$HOME" SSH_ORIGINAL_COMMAND=backup "$var" \
        bash "$TARS/entrada-df" < /dev/null > "$TARS/saida" 2> "$TARS/erro" || c=$?
    [ "$c" = 4 ] || falhar "backup com o $motivo acima de 85% saiu $c, queria 4: $(tail -3 "$TARS/erro")"
    grep -q '85%' "$TARS/erro" || falhar "backup com o $motivo acima de 85% não avisou"
    if age -d -i "$TARS/chave-age" -o "$TARS/decifrado.dump" "$TARS/saida" 2>/dev/null; then
        [ "$(head -c 5 "$TARS/decifrado.dump")" = PGDMP ] || falhar "backup com o $motivo cheio: o cifrado não é um dump"
        rm -f "$TARS/decifrado.dump"
    else
        falhar "backup com o $motivo cheio: a saída não decifra (o cifrado não saiu?)"
    fi
done
ok "stillon-entrada backup: disco ou volume acima de 85% manda o cifrado e sai 4"
docker inspect "$TERCEIRO_BACKUP" >/dev/null 2>&1 || falhar "falhas de backup apagaram o terceiro"
docker volume inspect "$volume_terceiro" >/dev/null 2>&1 || falhar "falhas de backup apagaram volume do terceiro"
docker rm -f -v "$TERCEIRO_BACKUP" >/dev/null
TERCEIRO_BACKUP=
sobras=$(comm -13 <(echo "$volumes_antes") <(volumes))
[ -z "$sobras" ] || falhar "os backups deixaram volumes: $(tr "\n" " " <<<"$sobras")"
ok "stillon-entrada backup: nenhum volume sobra dos descartáveis"

if [ "$falhas" -gt 0 ]; then
    dc logs >&2
    exit 1
fi
echo "vps conferida"
