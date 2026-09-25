#!/bin/sh
# Testes de deploy/ci/vps.sh com um ssh FALSO (nenhuma VPS é tocada). O job
# lint do CI os roda; localmente, da raiz do repositório:
#
#   sh deploy/ci/testar-vps.sh
#
# O ssh falso grava os argumentos, a entrada padrão e a chave que recebeu,
# escreve SSH_FALSO_SAIDA na saída padrão e SSH_FALSO_ERRO na de erro, e sai
# com SSH_FALSO_CODIGO.
set -eu

RAIZ=$(cd "$(dirname "$0")/../.." && pwd)
VPS=$RAIZ/deploy/ci/vps.sh
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
falhas=0
casos=0
falhar() { echo "FALHOU: $*" >&2; falhas=$((falhas + 1)); }
# ler <arquivo>: o conteúdo, ou nada se não existe (um caso que falha não
# derruba os seguintes pelo set -e).
ler() { cat "$1" 2>/dev/null || true; }

mkdir "$tmp/bin"
cat >"$tmp/bin/ssh" <<'FALSO'
#!/bin/sh
: >"$SSH_FALSO_DIR/chamado"
printf '%s\n' "$@" >"$SSH_FALSO_DIR/args"
# A chave e o known_hosts, copiados enquanto existem (o script os apaga).
anterior=
for a in "$@"; do
    case $anterior in
        -i)
            cp "$a" "$SSH_FALSO_DIR/chave"
            stat -c %a "$a" >"$SSH_FALSO_DIR/chave.modo"
            echo "$a" >"$SSH_FALSO_DIR/chave.caminho"
            ;;
    esac
    case $a in
        UserKnownHostsFile=*) cp "${a#UserKnownHostsFile=}" "$SSH_FALSO_DIR/known_hosts" ;;
    esac
    anterior=$a
done
cat >"$SSH_FALSO_DIR/stdin"
printf '%s' "${SSH_FALSO_SAIDA:-}"
printf '%s' "${SSH_FALSO_ERRO:-}" >&2
exit "${SSH_FALSO_CODIGO:-0}"
FALSO
chmod +x "$tmp/bin/ssh"

CHAVE='-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW
-----END OPENSSH PRIVATE KEY-----'
HOSTKEY='ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl root@vps'
SHA=0123456789abcdef0123456789abcdef01234567
ID=sha256:1111111111111111111111111111111111111111111111111111111111111111
AGE='age-encryption.org/v1
-> X25519 abc
--- def
binario'

# rodar <nome do caso> <args do vps.sh...>: roda com o ambiente de um secret
# válido (sobrescrevível por variáveis exportadas antes) e deixa em $saida a
# saída e o erro juntos, e em $codigo o código.
rodar() {
    casos=$((casos + 1))
    caso=$1
    shift
    SSH_FALSO_DIR=$tmp/caso-$casos
    mkdir -p "$SSH_FALSO_DIR"
    export SSH_FALSO_DIR
    codigo=0
    saida=$(PATH="$tmp/bin:$PATH" RUNNER_TEMP="$tmp" \
        VPS_HOST="${T_HOST-vps.exemplo.com}" VPS_SSH_KEY="${T_CHAVE-$CHAVE}" VPS_HOST_KEY="${T_HOSTKEY-$HOSTKEY}" \
        sh "$VPS" "$@" <"${T_ENTRADA:-/dev/null}" 2>&1) || codigo=$?
}
esperar_codigo() {
    [ "$codigo" = "$1" ] || falhar "$caso: código $codigo, queria $1 (saída: $saida)"
}
esperar_texto() {
    case $saida in
        *"$1"*) ;;
        *) falhar "$caso: a saída não diz \"$1\" (saída: $saida)" ;;
    esac
}
esperar_ssh_nao_chamado() {
    [ ! -e "$SSH_FALSO_DIR/chamado" ] || falhar "$caso: chamou o ssh, e devia recusar antes"
}

# ---- deploy ----------------------------------------------------------------

printf 'tar-da-imagem\001\002fim' >"$tmp/imagem.tar"
T_ENTRADA=$tmp/imagem.tar SSH_FALSO_CODIGO=0 SSH_FALSO_ERRO='log do deploy
' rodar "deploy saudável" deploy "$SHA" "$ID"
esperar_codigo 0
esperar_texto "deploy de $SHA saudável na VPS"
esperar_texto "log do deploy"
cmp -s "$tmp/imagem.tar" "$SSH_FALSO_DIR/stdin" || falhar "$caso: a imagem não chegou byte a byte à entrada do ssh"
args=$(ler "$SSH_FALSO_DIR/args")
[ "$(ler "$SSH_FALSO_DIR/args" | tail -n 1)" = "deploy $SHA $ID" ] || falhar "$caso: comando remoto $(ler "$SSH_FALSO_DIR/args" | tail -n 1)"
[ "$(ler "$SSH_FALSO_DIR/args" | tail -n 2 | head -n 1)" = "deploy@vps.exemplo.com" ] || falhar "$caso: destino errado: $args"
for opcao in StrictHostKeyChecking=yes GlobalKnownHostsFile=/dev/null IdentitiesOnly=yes BatchMode=yes HostKeyAlgorithms=ssh-ed25519; do
    printf '%s\n' "$args" | grep -qx "$opcao" || falhar "$caso: sem -o $opcao"
done
printf '%s\n' "$args" | grep -qi 'StrictHostKeyChecking=no' && falhar "$caso: StrictHostKeyChecking=no"
printf '%s\n' "$args" | grep -qx -- '-F' || falhar "$caso: sem -F /dev/null"
[ "$(ler "$SSH_FALSO_DIR/known_hosts")" = "vps.exemplo.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl" ] ||
    falhar "$caso: known_hosts = $(ler "$SSH_FALSO_DIR/known_hosts")"
# O secret vem sem a quebra de linha final; o ssh recusa a chave sem ela.
printf '%s\n' "$CHAVE" | cmp -s - "$SSH_FALSO_DIR/chave" ||
    falhar "$caso: a chave entregue ao ssh não é a do secret terminada em quebra de linha"
[ "$(ler "$SSH_FALSO_DIR/chave.modo")" = 600 ] || falhar "$caso: a chave com modo $(ler "$SSH_FALSO_DIR/chave.modo"), queria 600"
[ -s "$SSH_FALSO_DIR/chave.caminho" ] && [ ! -e "$(ler "$SSH_FALSO_DIR/chave.caminho")" ] || falhar "$caso: a chave privada ficou no disco do runner"

for par in \
    "2|código 2: uso ou argumento inválido" \
    "3|código 3: image-id divergente" \
    "4|rollback feito" \
    "255|falha de SSH (código 255)" \
    "1|erro na VPS (código 1)" \
    "7|erro na VPS (código 7)"; do
    c=${par%%|*}
    T_ENTRADA=$tmp/imagem.tar SSH_FALSO_CODIGO=$c rodar "deploy, a VPS sai $c" deploy "$SHA" "$ID"
    esperar_codigo "$c"
    esperar_texto "${par#*|}"
    esperar_texto "::error::"
done

NL='
'
for ruim in "${SHA}0" "$SHA${NL}x" "x${NL}$SHA" "0123456789ABCDEF0123456789abcdef01234567" "$SHA-tools" "" "$SHA; reboot"; do
    rodar "deploy, SHA recusado ($ruim)" deploy "$ruim" "$ID"
    esperar_codigo 2
    esperar_texto "SHA inválido"
    esperar_ssh_nao_chamado
done
for ruim in "sha256:abc" "1111111111111111111111111111111111111111111111111111111111111111" "sha256:1111111111111111111111111111111111111111111111111111111111111111 x" "$ID${NL}x"; do
    rodar "deploy, image-id recusado ($ruim)" deploy "$SHA" "$ruim"
    esperar_codigo 2
    esperar_texto "image-id inválido"
    esperar_ssh_nao_chamado
done
rodar "deploy sem o image-id" deploy "$SHA"
esperar_codigo 2
esperar_ssh_nao_chamado
rodar "verbo desconhecido" reboot
esperar_codigo 2
esperar_ssh_nao_chamado

for ruim in "" "-oProxyCommand=x" "vps.exemplo.com${NL}x" "vps.exemplo.com -p 2222" "deploy@vps"; do
    T_HOST=$ruim rodar "VPS_HOST recusado ($ruim)" deploy "$SHA" "$ID"
    esperar_codigo 2
    esperar_texto "VPS_HOST"
    esperar_ssh_nao_chamado
done
for ruim in "" "SHA256:abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG" "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQ" "vps.exemplo.com ssh-ed25519 AAAAC3Nza" "x${NL}$HOSTKEY"; do
    T_HOSTKEY=$ruim rodar "VPS_HOST_KEY recusado ($ruim)" deploy "$SHA" "$ID"
    esperar_codigo 2
    esperar_texto "VPS_HOST_KEY"
    esperar_ssh_nao_chamado
done
T_CHAVE="" rodar "VPS_SSH_KEY vazio" deploy "$SHA" "$ID"
esperar_codigo 2
esperar_texto "VPS_SSH_KEY"
esperar_ssh_nao_chamado

# ---- backup ----------------------------------------------------------------

arq=$tmp/vigilume-20260925.sql.age
SSH_FALSO_SAIDA=$AGE SSH_FALSO_ERRO='== dump
Postgres: 40% do disco
' rodar "backup conferido" backup "$arq"
esperar_codigo 0
esperar_texto "backup cifrado e conferido"
esperar_texto "== dump"
[ "$(ler "$arq")" = "$AGE" ] || falhar "$caso: o arquivo não é a saída padrão do ssh"
[ "$(ler "$SSH_FALSO_DIR/args" | tail -n 1)" = backup ] || falhar "$caso: comando remoto $(ler "$SSH_FALSO_DIR/args" | tail -n 1)"
case $(ler "$arq") in *"== dump"*) falhar "$caso: o log foi parar no arquivo do backup" ;; esac
: >"$tmp/resumo"
GITHUB_STEP_SUMMARY=$tmp/resumo SSH_FALSO_SAIDA=$AGE SSH_FALSO_ERRO='Postgres: 40% do disco
' rodar "backup, linha do disco no resumo" backup "$arq"
esperar_codigo 0
grep -qx 'Postgres: 40% do disco' "$tmp/resumo" || falhar "$caso: a linha do disco não foi para o resumo do job"

for claro in 'PGDMP binario do pg_dump' '-- PostgreSQL database dump' 'age-encryption.org/v2
x' ' age-encryption.org/v1
x' 'xage-encryption.org/v1'; do
    SSH_FALSO_SAIDA=$claro rodar "backup em claro recusado ($claro)" backup "$arq"
    esperar_codigo 1
    esperar_texto "não começa com o cabeçalho do age"
    [ ! -e "$arq" ] || falhar "$caso: o arquivo em claro ficou no disco"
done

SSH_FALSO_SAIDA='' rodar "backup vazio" backup "$arq"
esperar_codigo 1
esperar_texto "saída veio vazia"
[ ! -e "$arq" ] || falhar "$caso: o arquivo vazio ficou"

SSH_FALSO_SAIDA=$AGE SSH_FALSO_CODIGO=4 rodar "backup com disco acima de 85%" backup "$arq"
esperar_codigo 4
esperar_texto "acima de 85%"
[ "$(ler "$arq")" = "$AGE" ] || falhar "$caso: o backup conferido do código 4 não ficou para ser guardado"

SSH_FALSO_SAIDA='PGDMP' SSH_FALSO_CODIGO=4 rodar "backup código 4 em claro" backup "$arq"
esperar_codigo 1
[ ! -e "$arq" ] || falhar "$caso: o arquivo em claro ficou"

for par in \
    "3|restauração conferida (código 3)" \
    "255|falha de SSH (código 255)" \
    "1|falhou na VPS (código 1)"; do
    c=${par%%|*}
    SSH_FALSO_SAIDA=$AGE SSH_FALSO_CODIGO=$c rodar "backup, a VPS sai $c" backup "$arq"
    esperar_codigo "$c"
    esperar_texto "${par#*|}"
    [ ! -e "$arq" ] || falhar "$caso: sobrou o arquivo de um backup que não serve"
done

T_HOSTKEY="" rodar "backup sem VPS_HOST_KEY" backup "$arq"
esperar_codigo 2
esperar_ssh_nao_chamado
rodar "backup sem arquivo" backup
esperar_codigo 2
esperar_ssh_nao_chamado

if [ "$falhas" -gt 0 ]; then
    echo "$falhas falha(s) em $casos casos" >&2
    exit 1
fi
echo "vps.sh: $casos casos, tudo certo"
