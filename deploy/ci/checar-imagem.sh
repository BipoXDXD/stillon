#!/bin/sh
# Checagens da imagem de produção (spec do Plano 7, 9.2). O job "image" do CI
# as roda depois do Trivy; localmente: sh deploy/ci/checar-imagem.sh <imagem> <versão>.
#
# Cada checagem afirma uma ausência (sem root, sem shell, sem inchaço) ou uma
# presença que o build sozinho não prova (o painel embutido, a versão).
set -eu

IMAGEM=${1:?uso: checar-imagem.sh <imagem> <versão esperada>}
VERSAO=${2:?uso: checar-imagem.sh <imagem> <versão esperada>}
TETO_BYTES=$((15 * 1000 * 1000))
falhas=0
falhar() { echo "FALHOU: $*" >&2; falhas=$((falhas + 1)); }

# Tudo o que o script cria sai no fim, inclusive se um passo abortar no meio
# (set -e): o diretório temporário e os contêineres desta execução. Cada
# contêiner leva a label stillon.run com um id aleatório da execução, e a
# limpeza filtra só por ela: nem nome nem padrão, que pegariam o contêiner de
# outra execução na mesma máquina (issue #390).
tmp=$(mktemp -d)
EXECUCAO=$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')
ROTULO="stillon.run=$EXECUCAO"
limpar() {
    for cid in $(docker ps -aq --filter "label=$ROTULO"); do
        docker rm -f "$cid" >/dev/null 2>&1 || true
    done
    rm -rf "$tmp"
}
trap limpar EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# 1. Não é root. Duas guardas dão isso: a base :nonroot já traz o usuário
#    65532, e o Dockerfile repete USER nonroot:nonroot. Tirar só uma delas
#    fica verde aqui; a checagem prova a propriedade, não cada guarda.
usuario=$(docker image inspect -f '{{.Config.User}}' "$IMAGEM")
case "$usuario" in
    "" | root | 0 | root:* | 0:*) falhar "a imagem roda como root (User='$usuario')" ;;
esac

# 2. Sem shell (spec §5.9). /busybox/sh é o da variante debug, a base do
#    alvo tools: se ela vazar para o runtime, é aqui que aparece.
#    "Sem shell" só vale se o docker run falhou POR FALTA do arquivo: um
#    daemon fora do ar ou uma imagem errada também dão código diferente de
#    zero, e passariam por "sem shell".
for sh in /bin/sh /busybox/sh; do
    if saida=$(docker run --rm --label "$ROTULO" --entrypoint "$sh" "$IMAGEM" -c true 2>&1); then
        falhar "a imagem tem shell: $sh"
    elif ! printf '%s' "$saida" | grep -q "no such file or directory"; then
        falhar "docker run com $sh falhou por outro motivo: $saida"
    fi
done

# 3. A versão carimbada chegou ao binário. Com prazo, e como falha contada,
#    não aborto: um binário que trava ou sai com erro no `version` tem de
#    aparecer como FALHOU, e as outras checagens rodam mesmo assim. O
#    cliente do docker repassa o TERM ao contêiner e continua esperando (um
#    PID 1 que ignora o TERM nunca sai): por isso o -k, que manda KILL ao
#    cliente 5 s depois. O timeout mata só o cliente; o contêiner, marcado
#    com a label da execução, sai no trap.
#    Só o stdout entra na comparação: um aviso do docker no stderr (o
#    WARNING de plataforma, "Unable to find image…") viraria "versão
#    errada". O stderr segue direto para o log, e a mensagem de erro aponta
#    para ele.
if got=$(timeout -k 5 30 docker run --rm --label "$ROTULO" "$IMAGEM" version); then
    [ "$got" = "$VERSAO" ] || falhar "stillon version = '$got', queria '$VERSAO'"
else
    falhar "stillon version não respondeu em 30 s ou saiu com erro (código $?; o stderr está logo acima): '$got'"
fi

# 4. O painel está DENTRO do binário. O build passa com o dist vazio (o
#    .gitkeep basta para o go:embed), e o painel viraria um 503: por isso a
#    checagem olha o conteúdo, não o build. O index.html do Vite referencia o
#    bundle com hash; nenhum código Go tem essa string.
ctr_copia=$(docker create --label "$ROTULO" "$IMAGEM")
docker cp -q "$ctr_copia:/stillon" "$tmp/stillon"
docker rm "$ctr_copia" >/dev/null
grep -aq 'src="/assets/index-' "$tmp/stillon" || falhar "o binário não embute o index.html do painel"

# 5. Teto de tamanho: pega uma base errada ou um node_modules que vazou.
#    Em dois passos: num pipe `docker save | gzip | wc`, um save que falha
#    daria "0 bytes", abaixo do teto. O set -e pega o save que falha.
docker save -o "$tmp/imagem.tar" "$IMAGEM"
comprimido=$(gzip -c "$tmp/imagem.tar" | wc -c)
echo "imagem comprimida: $comprimido bytes"
[ "$comprimido" -gt 1000000 ] || falhar "imagem comprimida com $comprimido bytes: pequena demais, a medida falhou"
[ "$comprimido" -lt "$TETO_BYTES" ] || falhar "imagem comprimida com $comprimido bytes, teto $TETO_BYTES"

if [ "$falhas" -gt 0 ]; then
    exit 1
fi
echo "imagem conferida: $IMAGEM ($VERSAO)"
