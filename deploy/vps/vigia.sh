#!/bin/sh
# Vigia externo do Vigilume (Plano 11, spec §6; obrigação 7): o Vigilume não
# consegue avisar da própria queda. Instalado em /usr/local/sbin/vigilume-vigia
# e rodado a cada minuto pelo vigilume-vigia.timer.
#
# Pede https://<domínio>/healthz como um cliente de fora (DNS, TLS com o
# certificado conferido, Caddy e app) e pinga o healthchecks.io: a HC_URL se
# deu certo, a HC_URL/fail se não. Se a VPS morrer ou perder a rede, os pings
# param, e o healthchecks.io manda o e-mail ao dono do mesmo jeito.
#
# Lê VIGILUME_DOMINIO e HC_URL do /opt/vigilume/.env. O serviço roda com um
# usuário dinâmico, sem acesso ao /opt/vigilume: o systemd entrega uma cópia
# do .env em $CREDENTIALS_DIRECTORY/vigilume-env (LoadCredential).
set -eu

if [ -n "${CREDENTIALS_DIRECTORY:-}" ]; then
    ARQ_ENV=$CREDENTIALS_DIRECTORY/vigilume-env
else
    ARQ_ENV=/opt/vigilume/.env
fi
valor() { sed -n "s/^$1=//p" "$ARQ_ENV" | tail -n 1; }

hc=$(valor HC_URL)
dominio=$(valor VIGILUME_DOMINIO)
if [ -z "$hc" ]; then
    echo "vigia: HC_URL vazio em $ARQ_ENV: não há a quem avisar" >&2
    exit 1
fi
hc=${hc%/}

pingar() { curl -fsS -o /dev/null --max-time 10 --retry 3 "$1"; }

if [ -n "$dominio" ] && curl -fsS -o /dev/null --max-time 15 "https://$dominio/healthz"; then
    pingar "$hc"
    exit 0
fi
echo "vigia: https://${dominio:-<VIGILUME_DOMINIO vazio>}/healthz falhou" >&2
pingar "$hc/fail" || true
exit 1
