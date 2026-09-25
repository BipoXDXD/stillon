#!/bin/sh
# O nome anterior do projeto não volta ao repositório (renomeação para
# StillOn, 2026-09-25). O job "workflows" do CI roda; localmente, da raiz:
#
#   sh deploy/ci/checar-nome.sh
#
# Olha o conteúdo e os caminhos de todo arquivo versionado, e dos não
# versionados que o .gitignore não esconde, sem exceção de pasta. A única
# ocorrência aceita é a linha 1 do README, que conta o nome anterior. O nome
# é montado em partes para este arquivo não ser ele mesmo uma exceção.
set -eu

RAIZ=$(cd "$(dirname "$0")/../.." && pwd)
cd "$RAIZ"
ANTIGO=$(printf '%s%s' vigi lume)
TITULO="# StillOn (até 2026-09-25, $(printf 'V%s' "${ANTIGO#v}"))"
falhas=0
falhar() { echo "FALHOU: $*" >&2; falhas=$((falhas + 1)); }

# 1. Conteúdo, em qualquer caixa; -I pula binários.
achados=$(git grep --untracked -nIi -e "$ANTIGO" -- . || true)
fora=$(printf '%s\n' "$achados" | grep -v '^README\.md:1:' | grep . || true)
if [ -n "$fora" ]; then
    printf '%s\n' "$fora" >&2
    falhar "o nome anterior aparece acima"
fi

# 2. A exceção é exatamente a linha 1 do README.
[ "$(sed -n 1p README.md)" = "$TITULO" ] || falhar "README.md:1 não é '$TITULO'"

# 3. Caminhos (arquivos e pastas).
if git ls-files --cached --others --exclude-standard | grep -i -e "$ANTIGO" >&2; then
    falhar "caminho com o nome anterior (acima)"
fi

if [ "$falhas" -gt 0 ]; then
    echo "$falhas falha(s): o nome anterior voltou" >&2
    exit 1
fi
echo "nome: tudo certo"
