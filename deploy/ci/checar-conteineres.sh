#!/bin/sh
# A limpeza de contêineres dos scripts versionados só remove o que o próprio
# script criou (issue #390). Numa máquina compartilhada, escolher "os meus"
# por diferença de `docker ps` antes e depois, por nome, por padrão ou por
# prune derruba o contêiner de outra execução. O job "workflows" do CI roda
# este script; localmente, da raiz:
#
#   sh deploy/ci/checar-conteineres.sh
#
# O padrão aceito: o id vem da criação (a saída de `docker run -d` ou de
# `docker create`) ou a execução marca o que cria com
# `--label stillon.run=<uuid da execução>` e a limpeza filtra só por essa
# label. Contêiner do Testcontainers sai pelo Ryuk, sem remoção manual.
#
# Reprova, em *.sh, *.bash, *.py, *.mk, Makefile e arquivos com shebang de
# shell ou Python (versionados ou não ignorados):
#   1. `docker container prune` e `docker system prune`;
#   2. `docker ps` combinado com comm, diff ou grep -v na mesma linha, ou um
#      comm/diff sobre a foto de um `docker ps` (variável ou arquivo) num
#      script de shell que também chama `docker rm`;
#   3. `docker rm` sobre uma listagem (`$(docker ps …)`, `xargs`) que não
#      filtra por label=stillon.run=, ou com um nome literal como argumento;
#   4. `docker rm` num script de shell que lista contêineres por nome ou
#      imagem (`--filter name=`, `--filter ancestor=`) ou por grep.
# É uma checagem de texto: só conta o código, e um comentário pode citar o
# que é proibido. Uma linha que cita o padrão de propósito (o teste de uma
# guarda que o recusa) leva o comentário "checar-conteineres: permitido" com
# o motivo. Nos .py valem só as regras 1 e 2 (o nome de uma variável
# Python não se distingue de um nome literal).
set -eu

RAIZ=$(cd "$(dirname "$0")/../.." && pwd)
cd "$RAIZ"
EU=deploy/ci/checar-conteineres.sh
TESTE=deploy/ci/test_checar_conteineres.py

lista=$(mktemp)
trap 'rm -f "$lista"' EXIT
git ls-files --cached --others --exclude-standard >"$lista"

set --
while IFS= read -r f; do
    case "$f" in
        "$EU" | "$TESTE" | web/* | */node_modules/*) continue ;;
    esac
    [ -f "$f" ] || continue
    case "$f" in
        *.sh | *.bash | *.py | *.mk | Makefile | */Makefile) set -- "$@" "$f" ;;
        *) if head -n 1 "$f" | grep -Eq '^#!.*(sh|bash|python)'; then set -- "$@" "$f"; fi ;;
    esac
done <"$lista"

[ "$#" -gt 0 ] || { echo "contêineres: nenhum script para olhar" >&2; exit 1; }

# shellcheck disable=SC2016 # o programa awk usa $ dele, sem expansão do shell
awk '
function falhar_em(nome, linha, msg) { printf "FALHOU: %s:%d: %s\n", nome, linha, msg > "/dev/stderr"; falhas++ }
function falhar(msg) { falhar_em(FILENAME, FNR, msg) }
function fechar_arquivo() {
    if (arq == "" || py) return
    if (tem_rm && linha_diff)
        falhar_em(arq, linha_diff, "escolha de contêiner por diff de docker ps (comm ou diff sobre uma listagem de docker ps, e docker rm no mesmo script)")
    if (tem_rm && linha_lista_nome)
        falhar_em(arq, linha_lista_nome, "docker rm num script que lista contêineres por nome, imagem ou grep; filtre só por label=stillon.run=<uuid>")
}
FNR == 1 {
    fechar_arquivo()
    arq = FILENAME; py = (FILENAME ~ /\.py$/)
    tem_rm = 0; linha_diff = 0; linha_lista_nome = 0
    delete foto
}
{
    l = $0
    if (l ~ /^[ \t]*#/ || l ~ /checar-conteineres: permitido/) next
    sub(/[ \t]#.*$/, "", l)
    if (py) gsub(/["\047,\[\]]/, " ", l)

    ps = (l ~ /docker[ \t]+(ps|container[ \t]+(ls|ps|list))([ \t]|$)/)
    rm = (l ~ /docker[ \t]+(container[ \t]+)?rm([ \t]|$)/)
    diff = (l ~ /(^|[^[:alnum:]_.-])(comm|diff)([ \t]|$)/)
    inverte = (l ~ /grep[ \t]+(-[[:alpha:]]*v|.*--invert-match)/)

    # 1. prune
    if (l ~ /docker[ \t]+(container|system)[ \t]+prune/)
        falhar("docker container/system prune remove contêineres de outras execuções")

    # 2. diff de docker ps na mesma linha
    if (ps && (diff || inverte))
        falhar("escolha de contêiner por diff de docker ps (comm, diff ou grep -v)")

    if (py) next

    # A foto de docker ps (variável ou arquivo) que um comm/diff compara depois.
    if (ps && match(l, /[A-Za-z_][A-Za-z0-9_]*="?\$\([ \t]*docker[ \t]/))
        foto["$" substr(l, RSTART, index(substr(l, RSTART), "=") - 1)] = 1
    if (ps && match(l, />>?[ \t]*"?[^ \t"|;&)]+/)) {
        f = substr(l, RSTART, RLENGTH); sub(/^>>?[ \t]*"?/, "", f)
        if (f !~ /^&|^\/dev\//) foto[f] = 1
    }
    if (diff && !linha_diff)
        for (f in foto)
            if (index(l, f) || (f ~ /^\$/ && index(l, "${" substr(f, 2) "}"))) { linha_diff = FNR; break }
    if (ps && !linha_lista_nome && (l ~ /--filter[ =]+["\047]?(name|ancestor)=/ || l ~ /\|[ \t]*grep/))
        linha_lista_nome = FNR
    if (!rm) next
    tem_rm = 1

    # 3. docker rm: sobre listagem só com a label da execução; nunca nome literal
    if (l ~ /stillon\.run=/) next
    if (ps || l ~ /xargs/) {
        falhar("docker rm sobre uma listagem de contêineres; filtre só por label=stillon.run=<uuid>")
        next
    }
    resto = l
    sub(/^.*docker[ \t]+(container[ \t]+)?rm/, "", resto)
    sub(/[0-9]*[;|&<>)`].*$/, "", resto)
    n = split(resto, args, /[ \t]+/)
    for (i = 1; i <= n; i++) {
        a = args[i]
        if (a == "" || a ~ /^-/ || a ~ /^"?\$/) continue
        falhar("docker rm por nome (" a "); use o id da criação ou a label stillon.run=<uuid>")
    }
}
END {
    fechar_arquivo()
    if (falhas) { printf "%d falha(s): limpeza de contêiner fora do padrão (issue #390)\n", falhas > "/dev/stderr"; exit 1 }
    print "contêineres: tudo certo"
}
' "$@"
