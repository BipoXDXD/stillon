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
    if saida=$(docker run --rm --entrypoint "$sh" "$IMAGEM" -c true 2>&1); then
        falhar "a imagem tem shell: $sh"
    elif ! printf '%s' "$saida" | grep -q "no such file or directory"; then
        falhar "docker run com $sh falhou por outro motivo: $saida"
    fi
done

# 3. A versão carimbada chegou ao binário.
got=$(docker run --rm "$IMAGEM" version)
[ "$got" = "$VERSAO" ] || falhar "vigilume version = '$got', queria '$VERSAO'"

# 4. O painel está DENTRO do binário. O build passa com o dist vazio (o
#    .gitkeep basta para o go:embed), e o painel viraria um 503: por isso a
#    checagem olha o conteúdo, não o build. O index.html do Vite referencia o
#    bundle com hash; nenhum código Go tem essa string.
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
id=$(docker create "$IMAGEM")
docker cp -q "$id:/vigilume" "$tmp/vigilume"
docker rm "$id" >/dev/null
grep -aq 'src="/assets/index-' "$tmp/vigilume" || falhar "o binário não embute o index.html do painel"

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
