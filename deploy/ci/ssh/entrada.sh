#!/bin/sh
# Só a fronteira SSH/sudo é real neste ensaio; não simula deploy/rollback.
# checar-vps.sh já exercita a entrada de produção contra o Docker real.
set -eu
set -f
umask 077
[ "$#" -eq 0 ] || { echo 'argumentos chegaram ao comando forçado' >&2; exit 2; }
[ "$(id -u)" = 0 ] || { echo 'entrada não elevou para root' >&2; exit 2; }
[ "${SUDO_USER:-}" = deploy ] || { echo 'entrada não veio do sudo de deploy' >&2; exit 2; }
if [ -t 0 ] || [ -t 1 ]; then
    echo 'a entrada binária ganhou um terminal' >&2
    exit 2
fi
dir=/var/lib/stillon-ssh
n=$(cat "$dir/chamadas" 2>/dev/null || printf 0)
printf '%s\n' "$((n + 1))" > "$dir/chamadas"
printf '%s\n' "${SSH_ORIGINAL_COMMAND:-}" > "$dir/pedido"
id -u > "$dir/uid"
printf '%s\n' "$SUDO_USER" > "$dir/sudo-user"
cat > "$dir/stdin"
sha256sum "$dir/stdin" | cut -d' ' -f1 > "$dir/sha256"
if [ -z "${SSH_ORIGINAL_COMMAND:-}" ]; then
    echo 'SSH_ORIGINAL_COMMAND ausente depois do sudo' >&2
    exit 2
fi
pedido=$SSH_ORIGINAL_COMMAND
if [ "$pedido" = backup ]; then
    # Header age e payload são simulados: a cifragem real já tem seu ensaio.
    codigo=$(cat /fixture/codigo-backup)
    case "$codigo" in 0 | 3 | 4) ;; *) exit 2 ;; esac
    cat /fixture/age-simulado.bin
    echo "backup simulado: código $codigo" >&2
    exit "$codigo"
fi
# Não executar o pedido: só seleciona os códigos com SHAs válidos do ensaio.
# shellcheck disable=SC2086 # separação de palavras intencional; glob está desligado
set -- $pedido
[ "$#" -eq 3 ] && [ "$1" = deploy ] && [ "${#2}" -eq 40 ] || exit 2
case "$2" in *[!0-9a-f]*) exit 2 ;; esac
case "$3" in sha256:*) digest=${3#sha256:} ;; *) exit 2 ;; esac
[ "${#digest}" -eq 64 ] || exit 2
case "$digest" in *[!0-9a-f]*) exit 2 ;; esac
ultimo=${2#"${2%?}"}
case "$ultimo" in
    0 | 2 | 3 | 4) echo "entrada de teste: código $ultimo" >&2; exit "$ultimo" ;;
    *) exit 2 ;;
esac
