#!/bin/sh
set -eu
[ "${SSH_ORIGINAL_COMMAND:-}" = backup ] || { echo "Acesso restrito ao backup" >&2; exit 2; }
# A disconnected/failed publisher can leave a native task active after its lock exits.
[ ! -e /var/lib/coolify-release/stillon/pending.json ] || { echo "Publicação pendente; conferir Coolify antes do backup" >&2; exit 3; }
exec /usr/local/sbin/stillon-entrada
