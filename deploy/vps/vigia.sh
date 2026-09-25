#!/bin/sh
# Vigia externo do StillOn (Plano 11, spec §6; obrigação 7): o StillOn não
# consegue avisar da própria queda. Instalado em /usr/local/sbin/stillon-vigia
# e rodado a cada minuto pelo stillon-vigia.timer.
#
# Pede https://<domínio>/healthz como um cliente de fora (DNS, TLS com o
# certificado conferido, Caddy e app) e pinga o healthchecks.io: a HC_URL se
# deu certo, a HC_URL/fail se não. Se a VPS morrer ou perder a rede, os pings
# param, e o healthchecks.io manda o e-mail ao dono do mesmo jeito.
#
# Lê STILLON_DOMINIO e HC_URL do /opt/stillon/vigia.env (root, 600; modelo
# em deploy/vps/vigia.env.exemplo), que tem SÓ essas duas: o vigia não vê os
# segredos do .env. O serviço roda com um usuário dinâmico, sem acesso ao
# /opt/stillon: o systemd entrega uma cópia do vigia.env em
# $CREDENTIALS_DIRECTORY/stillon-vigia (LoadCredential).
set -eu

if [ -n "${CREDENTIALS_DIRECTORY:-}" ]; then
    ARQ_ENV=$CREDENTIALS_DIRECTORY/stillon-vigia
else
    ARQ_ENV=/opt/stillon/vigia.env
fi
valor() { sed -n "s/^$1=//p" "$ARQ_ENV" | tail -n 1; }

hc=$(valor HC_URL)
dominio=$(valor STILLON_DOMINIO)
if [ -z "$hc" ]; then
    echo "vigia: HC_URL vazio em $ARQ_ENV: não há a quem avisar" >&2
    exit 1
fi
hc=${hc%/}

# Prazos: o pior caso cabe no TimeoutStartSec do stillon-vigia.service (a
# conta está lá; deploy/ci/checar-vigia.sh a refaz com as opções de verdade).
# O --retry-max-time limita também o Retry-After que o hc-ping mandar.
pingar() { curl -fsS -o /dev/null --max-time 8 --retry 2 --retry-max-time 15 "$1"; }

if [ -n "$dominio" ] && curl -fsS -o /dev/null --max-time 15 "https://$dominio/healthz"; then
    pingar "$hc"
    exit 0
fi
echo "vigia: https://${dominio:-<STILLON_DOMINIO vazio>}/healthz falhou" >&2
pingar "$hc/fail" || true
exit 1
