#!/usr/bin/env bash
# DEPURAÇÃO (debug-vps-*), nunca vai para a main: o `docker compose up -d
# --wait --wait-timeout N` com um app que nunca fica saudável tem de sair
# diferente de 0. Conta quantas vezes ele sai 0 no compose do runner.
set -uo pipefail
d=$(mktemp -d)
cat > "$d/compose.yaml" <<'EOF'
name: diag-wait
x-hc: &hc
  interval: 10s
  timeout: 5s
  retries: 3
  start_period: 30s
  start_interval: 500ms
services:
  caddy:
    image: alpine:3
    command: ["sh", "-c", "trap 'exit 0' TERM; while :; do sleep 1; done"]
  db:
    image: alpine:3
    command: ["sh", "-c", "trap 'exit 0' TERM; while :; do sleep 1; done"]
    healthcheck:
      test: ["CMD", "true"]
      <<: *hc
  app:
    image: alpine:3
    command: ["sh", "-c", "trap 'exit 0' TERM; while :; do sleep 1; done"]
    healthcheck:
      test: ["CMD", "false"]
      <<: *hc
    depends_on:
      db:
        condition: service_healthy
    restart: unless-stopped
EOF
cd "$d" || exit 1
docker pull -q alpine:3 >/dev/null
docker compose up -d --wait --wait-timeout 5 >/dev/null 2>&1
zeros=0
codigos=
for _ in $(seq 1 20); do
    docker compose up -d --wait --wait-timeout 2 > saida 2>&1
    c=$?
    codigos="$codigos $c"
    if [ "$c" = 0 ]; then
        zeros=$((zeros + 1))
        echo "--- saiu 0; o app está: $(docker inspect -f '{{.State.Status}} {{.State.Health.Status}}' "$(docker compose ps -q app)")"
        tail -n 4 saida
    fi
done
echo "códigos:$codigos"
echo "DIAG: up --wait --wait-timeout 2 saiu 0 com o app em starting: $zeros de 20"
docker compose down -t 1 >/dev/null 2>&1
