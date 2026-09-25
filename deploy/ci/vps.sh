#!/bin/sh
# O lado do GitHub Actions na conversa com a VPS (Plano 11, spec §3 e §4). Os
# workflows deploy.yml e backup.yml só chamam este script; a lógica (SSH com a
# host key fixada, códigos de saída, cabeçalho do age) fica aqui, onde
# deploy/ci/testar-vps.sh a testa com um ssh falso.
#
#   sh deploy/ci/vps.sh deploy <sha 40 hex> <image-id sha256:…> < imagem.tar
#   sh deploy/ci/vps.sh backup <arquivo de saída>
#
# Ambiente (secrets de repositório):
#   VPS_HOST      nome ou IPv4 da VPS
#   VPS_SSH_KEY   a chave PRIVADA ed25519 do usuário deploy (só para isto)
#   VPS_HOST_KEY  a chave pública do servidor, como está no
#                 /etc/ssh/ssh_host_ed25519_key.pub da VPS ("ssh-ed25519 AAAA…")
#
# O outro lado é o /usr/local/sbin/vigilume-entrada (deploy/vps/), que o
# authorized_keys do deploy força: "deploy …" sai 0 (saudável), 2 (uso ou
# argumento recusado), 3 (imagem divergente), 4 (não ficou saudável; rollback
# feito) ou 1 (outro erro); "backup" manda o dump cifrado pela saída padrão e
# o log pela de erro, e sai 0, 3, 4 (os do backup.sh) ou 1. O ssh sai 255
# quando a conexão falha. Este script sai com o MESMO código, depois de dizer
# o que ele significa.
set -eu

erro() { echo "::error::$*"; }

# casa <valor> <ERE>: o valor INTEIRO casa, e numa linha só (o grep -x casaria
# uma das linhas de um valor com quebra de linha).
casa() {
    case $1 in *'
'*) return 1 ;; esac
    printf '%s\n' "$1" | grep -Eqx "$2"
}

uso() {
    erro "uso: vps.sh deploy <sha> <image-id> < imagem.tar | vps.sh backup <arquivo>"
    exit 2
}

# Recusas locais saem 2, como as da VPS: nada foi enviado.
recusar() {
    erro "$*"
    exit 2
}

conferir_ambiente() {
    host=${VPS_HOST:-}
    # Só letras, dígitos, ponto e hífen, sem hífen na ponta: um valor que
    # começasse com "-" viraria opção do ssh.
    if ! casa "$host" '[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?'; then
        recusar "VPS_HOST ausente ou inválido: use o nome ou o IPv4 da VPS"
    fi
    # A host key vem do secret, nunca do primeiro contato (nada de
    # StrictHostKeyChecking=no nem de ssh-keyscan): quem se puser no meio do
    # caminho não recebe a imagem nem entrega um backup falso.
    if ! casa "${VPS_HOST_KEY:-}" 'ssh-ed25519 AAAA[A-Za-z0-9+/]+={0,3}( .*)?'; then
        recusar "VPS_HOST_KEY ausente ou inválido: cole a linha de /etc/ssh/ssh_host_ed25519_key.pub da VPS (ssh-ed25519 AAAA…)"
    fi
    case ${VPS_SSH_KEY:-} in
        *'BEGIN OPENSSH PRIVATE KEY'*) ;;
        *) recusar "VPS_SSH_KEY ausente ou inválido: cole a chave privada inteira (-----BEGIN OPENSSH PRIVATE KEY-----)" ;;
    esac

    dir=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/vps.XXXXXX")
    # A chave privada sai do disco do runner em qualquer saída do script.
    trap 'rm -rf "$dir"' EXIT
    trap 'exit 130' INT
    trap 'exit 143' TERM
    chave=$dir/chave
    conhecidos=$dir/known_hosts
    (
        umask 077
        # O secret colado sem a quebra de linha final vira "invalid format".
        printf '%s\n' "$VPS_SSH_KEY" >"$chave"
        tipo_e_chave=$(printf '%s' "$VPS_HOST_KEY" | cut -d' ' -f1-2)
        printf '%s %s\n' "$host" "$tipo_e_chave" >"$conhecidos"
    )
}

# -F /dev/null: nenhuma configuração do runner muda o que está escrito aqui.
conectar() {
    ssh -F /dev/null \
        -i "$chave" -o IdentitiesOnly=yes \
        -o UserKnownHostsFile="$conhecidos" -o GlobalKnownHostsFile=/dev/null \
        -o StrictHostKeyChecking=yes -o HostKeyAlgorithms=ssh-ed25519 \
        -o BatchMode=yes -o ConnectTimeout=20 \
        -o ServerAliveInterval=15 -o ServerAliveCountMax=4 \
        -o LogLevel=ERROR \
        "deploy@$host" "$1"
}

resumo() {
    if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then echo "$*" >>"$GITHUB_STEP_SUMMARY"; fi
}

fazer_deploy() {
    [ $# -eq 2 ] || uso
    sha=$1
    id=$2
    casa "$sha" '[0-9a-f]{40}' ||
        recusar "SHA inválido: use o SHA completo do commit (40 hex minúsculos)"
    casa "$id" 'sha256:[0-9a-f]{64}' ||
        recusar "image-id inválido: queria sha256: e 64 hex"
    conferir_ambiente

    # A imagem (tar) vai pela entrada padrão deste script até a VPS.
    if conectar "deploy $sha $id"; then codigo=0; else codigo=$?; fi
    case $codigo in
        0)
            echo "deploy de $sha saudável na VPS"
            resumo "Deploy de \`$sha\` (\`$id\`): saudável."
            ;;
        2) erro "a VPS recusou o pedido (código 2: uso ou argumento inválido); nada mudou em produção" ;;
        3) erro "a imagem que chegou à VPS não é a esperada (código 3: image-id divergente de $id); nada mudou em produção" ;;
        4) erro "a versão nova não ficou saudável (código 4): a VPS voltou para a versão anterior (rollback feito); o log do app está acima" ;;
        255) erro "falha de SSH (código 255): conexão, VPS_SSH_KEY ou VPS_HOST_KEY; confira a mensagem do ssh acima antes de tentar de novo" ;;
        *) erro "erro na VPS (código $codigo); o log está acima" ;;
    esac
    exit "$codigo"
}

# O age escreve esta linha no começo de todo arquivo cifrado (formato
# binário). O dump em claro do pg_dump começa com "PGDMP"; um SQL, com
# comentário ou comando. Só o cabeçalho prova que o que chegou está cifrado.
CABECALHO_AGE=age-encryption.org/v1

cifrado() {
    [ -s "$1" ] || return 1
    primeira=$(head -n 1 "$1" | head -c 64)
    [ "$primeira" = "$CABECALHO_AGE" ]
}

fazer_backup() {
    [ $# -eq 1 ] || uso
    saida=$1
    conferir_ambiente

    # A saída padrão do ssh (o cifrado) vai para o arquivo; a de erro (o log
    # do backup) aparece no job e fica copiada para achar a linha do disco.
    {
        {
            if conectar backup 2>&1 >&3 3>&-; then c=0; else c=$?; fi
            echo "$c" >"$dir/codigo"
        } | tee "$dir/log" >&2
    } 3>"$saida"
    codigo=$(cat "$dir/codigo")
    grep '^Postgres: ' "$dir/log" | while IFS= read -r linha; do resumo "$linha"; done || true

    case $codigo in
        0 | 4)
            if [ ! -s "$saida" ]; then
                rm -f "$saida"
                erro "o backup saiu com código $codigo mas a saída veio vazia: nada foi guardado"
                exit 1
            fi
            if ! cifrado "$saida"; then
                rm -f "$saida"
                erro "a saída do backup não começa com o cabeçalho do age ($CABECALHO_AGE): não é um arquivo cifrado, e foi apagada sem ser guardada"
                exit 1
            fi
            if [ "$codigo" -eq 4 ]; then
                erro "disco da VPS acima de 85% (código 4): o backup de hoje foi conferido e será guardado, mas aumente o disco ou libere espaço"
                exit 4
            fi
            echo "backup cifrado e conferido: $saida ($(wc -c <"$saida") bytes)"
            resumo "Backup conferido: \`$(basename "$saida")\`."
            exit 0
            ;;
        3) erro "o backup não passou na restauração conferida (código 3): o dump não serve e nada foi guardado; o log está acima" ;;
        255) erro "falha de SSH (código 255): conexão, VPS_SSH_KEY ou VPS_HOST_KEY; nenhum backup foi feito" ;;
        *) erro "o backup falhou na VPS (código $codigo): nada foi guardado; o log está acima" ;;
    esac
    rm -f "$saida"
    exit "$codigo"
}

[ $# -ge 1 ] || uso
verbo=$1
shift
case $verbo in
    deploy) fazer_deploy "$@" ;;
    backup) fazer_backup "$@" ;;
    *) uso ;;
esac
