#!/bin/sh
# Amostra o uso de memória da pilha até ser morto. Uma linha por contêiner e
# rodada: época, nome e uso, como o docker stats o escreve (123.4MiB).
set -u
saida=$1
while :; do
    agora=$(date +%s)
    docker stats --no-stream --format '{{.Name}} {{.MemUsage}}' |
        awk -v t="$agora" '{print t, $1, $2}' >>"$saida"
    sleep 1
done
