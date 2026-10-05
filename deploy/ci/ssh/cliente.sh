#!/bin/sh
# A chave é efêmera; seu conteúdo não entra no argv de docker nem nos logs.
set -eu
umask 077
export VPS_HOST=stillon-ssh
VPS_SSH_KEY=$(cat /fixture/cliente)
VPS_HOST_KEY=$(cat "/fixture/${STILLON_SSH_HOSTKEY:-servidor.pub}")
export VPS_SSH_KEY VPS_HOST_KEY
export RUNNER_TEMP=/tmp
modo=$1
shift
case "$modo" in
    vps) exec sh /workspace/vps.sh "$@" ;;
    ssh)
        # Só os ensaios adicionais do restrict/comando forçado usam ssh direto.
        printf '%s %s\n' "$VPS_HOST" "$VPS_HOST_KEY" > /tmp/known_hosts
        exec ssh -F /dev/null -i /fixture/cliente -o IdentitiesOnly=yes \
            -o UserKnownHostsFile=/tmp/known_hosts -o GlobalKnownHostsFile=/dev/null \
            -o StrictHostKeyChecking=yes -o HostKeyAlgorithms=ssh-ed25519 \
            -o BatchMode=yes -o ConnectTimeout=5 "$@"
        ;;
    *) echo 'modo desconhecido na fixture cliente' >&2; exit 2 ;;
esac
