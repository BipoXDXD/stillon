#!/usr/bin/env bash
# Prova vps.sh -> ssh real -> authorized_keys -> /bin/sh -> sudo -> stdin.
# Sabotagem: retirar env_keep da cópia do sudoers deve recusar o deploy 0.
# Nada de VPS/chave do dono: dois contêineres e rede interna próprios.
set -euo pipefail
umask 077
RAIZ=$(cd "$(dirname "$0")/../.." && pwd)
tmp=$(mktemp -d /tmp/stillon-ssh.XXXXXXXX)
sufixo=${tmp##*.}
imagem="stillon-ssh-teste:$sufixo"
rede_id=
servidor_id=
clientes=()
limpar() {
    resultado=$?
    trap - EXIT
    if [ "$resultado" -ne 0 ] && [ -n "$servidor_id" ]; then
        docker logs "$servidor_id" >&2 || true
    fi
    for cliente_id in "${clientes[@]}"; do docker rm -f "$cliente_id" >/dev/null 2>&1 || true; done
    if [ -n "$servidor_id" ]; then docker rm -f "$servidor_id" >/dev/null 2>&1 || true; fi
    if [ -n "$rede_id" ]; then docker network rm "$rede_id" >/dev/null 2>&1 || true; fi
    docker image rm "$imagem" >/dev/null 2>&1 || true
    rm -rf "$tmp"
    exit "$resultado"
}
trap limpar EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
falhar() { echo "FALHOU: $*" >&2; exit 1; }
ok() { echo "OK: $*"; }

for ferramenta in docker ssh-keygen timeout awk cmp sha256sum; do
    command -v "$ferramenta" >/dev/null || falhar "ferramenta ausente: $ferramenta"
done
docker build -q -t "$imagem" "$RAIZ/deploy/ci/ssh" >/dev/null
ssh-keygen -q -t ed25519 -N '' -f "$tmp/cliente"
ssh-keygen -q -t ed25519 -N '' -f "$tmp/servidor"
ssh-keygen -q -t ed25519 -N '' -f "$tmp/servidor-incorreto"
cp "$RAIZ/deploy/vps/sudoers-stillon-entrada" "$tmp/sudoers"
# 256 KiB com todos os bytes, inclusive NUL, CR/LF e bytes altos, sem variável binária.
LC_ALL=C awk 'BEGIN { for (i = 0; i < 1024; i++) for (b = 0; b < 256; b++) printf "%c", b }' > "$tmp/binario"
[ "$(wc -c < "$tmp/binario")" -eq 262144 ] || falhar 'fixture binária tem tamanho errado'
hash_esperado=$(sha256sum "$tmp/binario" | cut -d' ' -f1)
# Só o framing do cliente é exercitado; não é um arquivo age realmente cifrado.
{
    printf 'age-encryption.org/v1\n-> X25519 fixture-do-ensaio\n--- mac-simulado\n'
    cat "$tmp/binario"
} > "$tmp/age-simulado.bin"
hash_backup_esperado=$(sha256sum "$tmp/age-simulado.bin" | cut -d' ' -f1)
printf '0\n' > "$tmp/codigo-backup"
rede_id=$(docker network create --internal "stillon-ssh-$sufixo")
servidor_id=$(docker create --name "stillon-ssh-servidor-$sufixo" \
    --network "$rede_id" --network-alias stillon-ssh \
    --memory 256m --cpus 1 --pids-limit 128 \
    --mount "type=bind,src=$tmp,dst=/fixture,readonly" "$imagem" \
    /usr/local/lib/stillon-ssh/iniciar-servidor.sh)
docker start "$servidor_id" >/dev/null
pronto=0
for ((tentativa = 0; tentativa < 40; tentativa++)); do
    if docker logs "$servidor_id" 2>&1 | grep -q 'Server listening on'; then pronto=1; break; fi
    [ "$(docker inspect -f '{{.State.Running}}' "$servidor_id")" = true ] || falhar 'sshd parou antes de escutar'
    sleep 0.25
done
[ "$pronto" -eq 1 ] || falhar 'sshd não escutou dentro do prazo'
[ "$(docker exec "$servidor_id" getent passwd deploy | cut -d: -f7)" = /bin/sh ] || falhar 'deploy não usa /bin/sh'
[ "$(docker inspect -f '{{json .HostConfig.PortBindings}}' "$servidor_id")" = '{}' ] || falhar 'sshd publicou porta no host'
docker logs "$servidor_id" 2>&1 | grep -E 'OpenSSH_|[Ss]udo version' || true
ok 'sshd real pronto; usuário /bin/sh; rede interna sem porta publicada'

rodadas=0
codigo=0
saida=
erro=
ultimo_cliente_id=
cliente() {
    local hostkey=$1
    shift
    rodadas=$((rodadas + 1))
    saida=$tmp/saida-$rodadas
    erro=$tmp/erro-$rodadas
    local cliente_id
    cliente_id=$(docker create -i --network "$rede_id" \
        --memory 128m --cpus 1 --pids-limit 64 \
        --mount "type=bind,src=$tmp,dst=/fixture,readonly" \
        --mount "type=bind,src=$RAIZ/deploy/ci/vps.sh,dst=/workspace/vps.sh,readonly" \
        -e "STILLON_SSH_HOSTKEY=$hostkey" "$imagem" \
        /usr/local/lib/stillon-ssh/cliente.sh "$@") || return 1
    clientes+=("$cliente_id")
    ultimo_cliente_id=$cliente_id
    codigo=0
    timeout 30s docker start -a -i "$cliente_id" < "$tmp/binario" > "$saida" 2> "$erro" || codigo=$?
    # docker start -a entrega o código da entrada; 124 é prazo do ensaio, não código SSH.
}

comprovar_deploy() {
    local esperado=$1 sha pedido
    sha=$(printf '%040d' "$esperado")
    pedido="deploy $sha sha256:1111111111111111111111111111111111111111111111111111111111111111"
    cliente servidor.pub vps deploy "$sha" sha256:1111111111111111111111111111111111111111111111111111111111111111 || return 1
    if [ "$codigo" -ne "$esperado" ]; then
        echo "deploy $esperado retornou $codigo, queria $esperado" >&2
        cat "$erro" >&2
        return 1
    fi
    [ "$(docker exec "$servidor_id" cat /var/lib/stillon-ssh/pedido)" = "$pedido" ] || { echo 'pedido mudou no SSH/sudo' >&2; return 1; }
    [ "$(docker exec "$servidor_id" cat /var/lib/stillon-ssh/uid)" = 0 ] || return 1
    [ "$(docker exec "$servidor_id" cat /var/lib/stillon-ssh/sudo-user)" = deploy ] || return 1
    [ "$(docker exec "$servidor_id" cat /var/lib/stillon-ssh/sha256)" = "$hash_esperado" ] || { echo 'hash do stdin mudou' >&2; return 1; }
    docker cp "$servidor_id:/var/lib/stillon-ssh/stdin" "$tmp/recebido" || return 1
    cmp "$tmp/binario" "$tmp/recebido" || return 1
}

comprovar_backup() {
    local esperado=$1
    printf '%s\n' "$esperado" > "$tmp/codigo-backup"
    cliente servidor.pub vps backup /tmp/backup-simulado.dump.age || return 1
    if [ "$codigo" -ne "$esperado" ]; then
        echo "backup $esperado retornou $codigo, queria $esperado" >&2
        cat "$erro" >&2
        return 1
    fi
    [ "$(docker exec "$servidor_id" cat /var/lib/stillon-ssh/pedido)" = backup ] || return 1
    [ "$(docker exec "$servidor_id" cat /var/lib/stillon-ssh/uid)" = 0 ] || return 1
    [ "$(docker exec "$servidor_id" cat /var/lib/stillon-ssh/sudo-user)" = deploy ] || return 1
    docker exec "$servidor_id" test ! -s /var/lib/stillon-ssh/stdin || { echo 'backup enviou stdin para a VPS' >&2; return 1; }
    if [ "$esperado" -eq 3 ]; then
        # O cliente já terminou; docker cp funciona em contêiner parado, exec não.
        docker cp "$ultimo_cliente_id:/tmp/." "$tmp/cliente-tmp-$rodadas" || return 1
        [ ! -e "$tmp/cliente-tmp-$rodadas/backup-simulado.dump.age" ] || { echo 'backup recusado deixou o arquivo no cliente' >&2; return 1; }
        return 0
    fi
    docker cp "$ultimo_cliente_id:/tmp/backup-simulado.dump.age" "$tmp/backup-recebido" || return 1
    cmp "$tmp/age-simulado.bin" "$tmp/backup-recebido" || { echo 'bytes de saída do backup mudaram' >&2; return 1; }
    [ "$(sha256sum "$tmp/backup-recebido" | cut -d' ' -f1)" = "$hash_backup_esperado" ] || { echo 'hash da saída do backup mudou' >&2; return 1; }
}

# RED antes do GREEN, sem editar sudoers de produção nem a imagem compartilhada.
docker exec "$servidor_id" sh -c "sed '/^Defaults!.* env_keep /d' /fixture/sudoers > /etc/sudoers.d/stillon-entrada; chmod 0440 /etc/sudoers.d/stillon-entrada; visudo -cf /etc/sudoers.d/stillon-entrada" >/dev/null
if comprovar_deploy 0 > "$tmp/red.log" 2>&1; then falhar 'retirar env_keep não derrubou a prova'; fi
grep -q 'SSH_ORIGINAL_COMMAND ausente depois do sudo' "$tmp/red.log" || { cat "$tmp/red.log" >&2; falhar 'RED não veio da perda de SSH_ORIGINAL_COMMAND'; }
ok 'RED observado: retirar env_keep perde SSH_ORIGINAL_COMMAND e recusa deploy 0'
docker exec "$servidor_id" sh -c 'install -o root -g root -m 0440 /fixture/sudoers /etc/sudoers.d/stillon-entrada; visudo -cf /etc/sudoers.d/stillon-entrada' >/dev/null
docker cp "$servidor_id:/etc/sudoers.d/stillon-entrada" "$tmp/sudoers-restaurado"
cmp "$tmp/sudoers" "$tmp/sudoers-restaurado" || falhar 'sudoers não voltou aos bytes originais'
for esperado in 0 2 3 4; do
    comprovar_deploy "$esperado" || falhar "código $esperado, pedido ou stdin binário não atravessou intacto"
    ok "deploy $esperado: código, pedido, root e 262144 bytes intactos"
done

# Sabotagem só na entrada simulada: acrescenta um byte a um header age válido.
# vps.sh aceita o framing, mas a mesma comparação dos positivos deve falhar.
docker exec "$servidor_id" sh -c "sed 's|cat /fixture/age-simulado.bin|cat /fixture/age-simulado.bin; printf X|' /usr/local/lib/stillon-ssh/entrada.sh > /usr/local/sbin/stillon-entrada; chmod 0755 /usr/local/sbin/stillon-entrada"
if comprovar_backup 0 > "$tmp/red-backup.log" 2>&1; then falhar 'alterar stdout não derrubou a prova binária do backup'; fi
grep -q 'bytes de saída do backup mudaram' "$tmp/red-backup.log" || { cat "$tmp/red-backup.log" >&2; falhar 'RED do backup não veio dos bytes alterados'; }
ok 'RED observado: um byte extra no stdout derruba a comparação do backup'
docker exec "$servidor_id" install -o root -g root -m 0755 /usr/local/lib/stillon-ssh/entrada.sh /usr/local/sbin/stillon-entrada
docker cp "$servidor_id:/usr/local/sbin/stillon-entrada" "$tmp/entrada-restaurada"
cmp "$RAIZ/deploy/ci/ssh/entrada.sh" "$tmp/entrada-restaurada" || falhar 'entrada simulada não voltou aos bytes originais'
for esperado in 0 4 3; do
    comprovar_backup "$esperado" || falhar "backup $esperado não preservou o código e o destino da saída"
    if [ "$esperado" -eq 3 ]; then
        ok 'backup simulado 3: código remoto preservado e arquivo apagado'
    else
        ok "backup simulado $esperado: código e stdout binário intactos, cmp e SHA-256 iguais"
    fi
done

# Public key válida, mas de outro servidor: recusa real do known_hosts, antes do comando.
antes=$(docker exec "$servidor_id" cat /var/lib/stillon-ssh/chamadas)
cliente servidor-incorreto.pub vps deploy 0000000000000000000000000000000000000000 sha256:1111111111111111111111111111111111111111111111111111111111111111
[ "$codigo" -eq 255 ] || falhar "host key divergente retornou $codigo, queria 255"
grep -q 'Host key verification failed' "$erro" || falhar '255 não veio da verificação de host key'
[ "$(docker exec "$servidor_id" cat /var/lib/stillon-ssh/chamadas)" = "$antes" ] || falhar 'host key divergente chegou à entrada'
ok 'known_hosts com chave divergente: 255 sem alcançar a entrada'

cliente servidor.pub ssh -tt deploy@stillon-ssh 'deploy 0000000000000000000000000000000000000000 sha256:1111111111111111111111111111111111111111111111111111111111111111'
# -tt exige o PTY: o cliente aborta com 255 quando restrict nega a alocação.
[ "$codigo" -eq 255 ] || falhar "PTY forçada retornou $codigo, queria a recusa do SSH"
grep -q 'PTY allocation request failed' "$erro" || falhar 'restrict não recusou a solicitação de PTY'
ok 'restrict recusou PTY forçada: mensagem de alocação negada e código 255'

cliente servidor.pub ssh deploy@stillon-ssh 'touch /tmp/escape'
[ "$codigo" -eq 2 ] || falhar "comando arbitrário retornou $codigo, queria a recusa da entrada"
[ "$(docker exec "$servidor_id" cat /var/lib/stillon-ssh/pedido)" = 'touch /tmp/escape' ] || falhar 'pedido arbitrário não passou pela entrada forçada'
docker exec "$servidor_id" test ! -e /tmp/escape || falhar 'o shell executou o comando arbitrário'
ok 'command= encaminhou o pedido arbitrário à entrada e impediu sua execução'
comprovar_deploy 0 || falhar 'deploy saudável final não passou após os casos negativos'
ok 'GREEN restaurado: deploy saudável e binário intacto'
echo 'SSH real conferido; cleanup remove somente os recursos deste ensaio'
