#!/bin/sh
# Gera as fontes do painel em web/public/fonts a partir do repositório oficial
# do Google Fonts (github.com/google/fonts), num commit fixo, e confere cada
# arquivo baixado pelo hash do blob do Git antes de usá-lo.
# Precisa de curl, git e fonttools com brotli: no Mac, `brew install fonttools`;
# no Linux, `pip install 'fonttools[woff]'`.
# Uso, na raiz do repositório: sh web/scripts/gerar-fontes.sh
set -eu

COMMIT=e9678a5385322c63730122ba464765baad83cfd8
BASE="https://raw.githubusercontent.com/google/fonts/$COMMIT/ofl"
# Subconjunto latino do Google Fonts, mais as setas U+2190-2193 ("← Painel",
# "Ver incidente →"), que o latino deles não traz inteiras.
UNICODES='U+0000-00FF,U+0131,U+0152-0153,U+02BB-02BC,U+02C6,U+02DA,U+02DC,U+0304,U+0308,U+0329,U+2000-206F,U+20AC,U+2122,U+2190-2193,U+2212,U+2215,U+FEFF,U+FFFD'
# DESTINO pode vir do ambiente, para gerar fora do repositório e conferir antes.
DESTINO=${DESTINO:-web/public/fonts}
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# baixar <caminho no repositório> <arquivo local> <blob esperado>
baixar() {
  curl -fsSL -o "$TMP/$2" "$BASE/$1"
  blob=$(git hash-object "$TMP/$2")
  if [ "$blob" != "$3" ]; then
    echo "gerar-fontes: $1 não confere (blob $blob, esperado $3)" >&2
    exit 1
  fi
}

baixar 'fraunces/Fraunces%5BSOFT%2CWONK%2Copsz%2Cwght%5D.ttf' Fraunces.ttf 8210f9488d3c732359a9292dd09aca3f2bae830e
baixar 'fraunces/OFL.txt' OFL-Fraunces.txt 22fa7ce5733434d0c634f6e55116f410858e938c
baixar 'inter/Inter%5Bopsz%2Cwght%5D.ttf' Inter.ttf 047c92f6e2212473dc436020afed689527076d44
baixar 'inter/OFL.txt' OFL-Inter.txt 21f6aff961064c2e429f570995e446bcdd555422

# A Fraunces sai sem os eixos SOFT e WONK (fixos em 0, o desenho padrão): o
# painel não os usa, e o arquivo cai a menos da metade. Ficam opsz e wght.
fonttools varLib.instancer "$TMP/Fraunces.ttf" SOFT=0 WONK=0 -o "$TMP/Fraunces-fixa.ttf"

mkdir -p "$DESTINO"
pyftsubset "$TMP/Fraunces-fixa.ttf" --unicodes="$UNICODES" --layout-features='*' \
  --flavor=woff2 --output-file="$DESTINO/fraunces-latin.woff2"
pyftsubset "$TMP/Inter.ttf" --unicodes="$UNICODES" --layout-features='*' \
  --flavor=woff2 --output-file="$DESTINO/inter-latin.woff2"
cp "$TMP/OFL-Fraunces.txt" "$TMP/OFL-Inter.txt" "$DESTINO/"
ls -l "$DESTINO"
