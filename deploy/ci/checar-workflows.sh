#!/bin/sh
# Checagens dos workflows do GitHub Actions que o actionlint não faz (Plano 11,
# spec §8). Workflows não rodam fora do GitHub: o que dá para provar aqui é o
# texto deles. O job "workflows" do CI roda este script; localmente, da raiz:
#
#   sh deploy/ci/checar-workflows.sh
#
# A lógica de shell do deploy e do backup não está nos workflows: está em
# deploy/ci/vps.sh, testada por deploy/ci/testar-vps.sh.
set -eu

RAIZ=$(cd "$(dirname "$0")/../.." && pwd)
cd "$RAIZ"
WF=.github/workflows
DEPLOY=$WF/deploy.yml
BACKUP=$WF/backup.yml
falhas=0
falhar() { echo "FALHOU: $*" >&2; falhas=$((falhas + 1)); }
# Os laços com pipe rodam num subshell: as falhas de dentro deles vão como
# linhas para este arquivo, contadas depois.
marcas=$(mktemp)
trap 'rm -f "$marcas"' EXIT
contar_marcas() {
    if [ -s "$marcas" ]; then falhas=$((falhas + $(wc -l <"$marcas"))); fi
    : >"$marcas"
}

# 1. Nem workflow nem script de deploy/ci aceita a host key do primeiro
#    servidor que responder. O ssh aceita "no", "off" e "accept-new" (este
#    último também confia no primeiro contato); UserKnownHostsFile=/dev/null
#    esquece a chave fixada; ssh-keyscan é o mesmo erro por outro caminho.
#    Desde a W2 o ssh de verdade está em deploy/ci/vps.sh, e por isso os
#    scripts entram na busca (menos este, que cita os padrões). Nos scripts
#    só conta o código: um comentário pode citar o que é proibido.
# Os scripts ficam nos parâmetros posicionais ("$@"), que este script não usa.
set --
for f in deploy/ci/*.sh; do
    [ "$f" = deploy/ci/checar-workflows.sh ] || set -- "$@" "$f"
done
# codigo <arquivo...>: as linhas que não são só comentário, com arquivo e número.
codigo() { grep -nH '' "$@" | grep -Ev '^[^:]+:[0-9]+:[[:space:]]*#'; }
DESLIGADO_ERE='StrictHostKeyChecking[[:space:]=]+"?(no|off|accept-new)|UserKnownHostsFile[[:space:]=]+"?/dev/null'
if { grep -EniH "$DESLIGADO_ERE" "$WF"/*.yml; codigo "$@" | grep -Ei "$DESLIGADO_ERE"; } >&2; then
    falhar "StrictHostKeyChecking desligado ou known_hosts vazio (acima): a host key vem do secret VPS_HOST_KEY"
fi
if { grep -EniH 'ssh-keyscan' "$WF"/*.yml; codigo "$@" | grep -Ei 'ssh-keyscan'; } >&2; then
    falhar "ssh-keyscan (acima): a host key vem do secret VPS_HOST_KEY, nunca da rede"
fi
# O ssh usa o PRIMEIRO valor de cada opção: um "-o StrictHostKeyChecking=
# accept-new" a mais, antes do certo, ganharia dele. No vps.sh, cada opção
# que fixa a host key e a identidade aparece EXATAMENTE uma vez, com o valor
# esperado (o nome sem distinguir maiúsculas, como o ssh; "-F <arquivo>"
# também muda tudo, e só vale /dev/null). O
# deploy/ci/testar-vps.sh confere o mesmo nos argumentos que o ssh recebe.
VPS_SH=deploy/ci/vps.sh
# shellcheck disable=SC2016 # o "$conhecidos" é texto do vps.sh
for par in \
    '-F|-F /dev/null' \
    'IdentitiesOnly|-o IdentitiesOnly=yes' \
    'UserKnownHostsFile|-o UserKnownHostsFile="$conhecidos"' \
    'GlobalKnownHostsFile|-o GlobalKnownHostsFile=/dev/null' \
    'StrictHostKeyChecking|-o StrictHostKeyChecking=yes' \
    'HostKeyAlgorithms|-o HostKeyAlgorithms=ssh-ed25519' \
    'BatchMode|-o BatchMode=yes'; do
    nome=${par%%|*}
    certo=${par#*|}
    # -F é flag (sensível a maiúsculas: -f é outra coisa); os nomes do -o, não.
    case $nome in -F) i= ;; *) i=i ;; esac
    n=$(codigo "$VPS_SH" | grep -Eo$i -- "(^|[[:space:]'\"]|-o)$nome([[:space:]=]|\$)" | wc -l)
    [ "$n" -eq 1 ] || falhar "$VPS_SH: $nome aparece $n vez(es) no código, queria exatamente uma ($certo)"
    # O valor inteiro: "ssh-ed25519,ssh-rsa" não é "ssh-ed25519".
    codigo "$VPS_SH" | sed 's/$/ /' | grep -qF -- "$certo " || falhar "$VPS_SH: sem $certo"
done

# 2. Toda action fixada por SHA completo (o comentário diz a tag), em TODOS
#    os workflows: uma tag móvel roda código alheio com o GITHUB_TOKEN e com
#    os secrets da VPS (D20 do Plano 7).
for f in "$WF"/*.yml; do
    grep -En '^[[:space:]-]*uses:' "$f" | while IFS= read -r linha; do
        ref=$(printf '%s\n' "$linha" | sed -E 's/^[0-9]+:[[:space:]-]*uses:[[:space:]]*//; s/[[:space:]]+#.*$//; s/^["'\'']//; s/["'\'']$//')
        case $ref in
            ./*) continue ;; # action local, do próprio commit
        esac
        if ! printf '%s\n' "$ref" | grep -Eqx '[A-Za-z0-9_.-]+/[A-Za-z0-9_./-]+@[0-9a-f]{40}'; then
            echo "FALHOU: $f:${linha%%:*}: action sem SHA completo: $ref" >&2
            echo x >>"$marcas"
        fi
    done
done
contar_marcas

# 3. Deploy: só à mão, por SHA, sem cancelar um deploy no meio, com o mínimo
#    de permissão, e com a conversa com a VPS inteira no vps.sh.
tem() { grep -Eq "$2" "$1" || falhar "$1: $3"; }
# nao_tem olha só o código: um comentário pode citar o que é proibido.
nao_tem() {
    if grep -En "$2" "$1" | grep -Ev '^[0-9]+:[[:space:]]*#' | sed "s|^|$1:|" | grep .; then
        falhar "$1: $3"
    fi
}

gatilhos=$(sed -n '/^on:/,/^[a-z]/p' "$DEPLOY" | grep -E '^  [a-z_]+:' | tr -d ' :' | tr '\n' ' ')
[ "$gatilhos" = "workflow_dispatch " ] || falhar "$DEPLOY: gatilhos \"$gatilhos\", queria só workflow_dispatch"
tem "$DEPLOY" '^      sha:$' "sem o input sha"
for f in "$DEPLOY" "$BACKUP"; do
    tem "$f" '^concurrency:$' "sem concurrency"
    tem "$f" '^  cancel-in-progress: false$' "concurrency tem de ser cancel-in-progress: false"
    nao_tem "$f" 'cancel-in-progress: true' "um deploy ou backup não é cancelado no meio"
    nao_tem "$f" ':[[:space:]]*write' "permissão de escrita (acima): o job só lê"
    nao_tem "$f" '(^|[^.])\bssh[[:space:]]+(-|deploy@|"\$)' "ssh direto no workflow (acima): use deploy/ci/vps.sh"
    nao_tem "$f" 'VPS_SSH_KEY\}\}.*>' "a chave privada gravada pelo workflow (acima): o vps.sh a grava e apaga"
done
perms=$(sed -n '/^permissions:/,/^[a-z]/p' "$DEPLOY" | grep -E '^  ' | tr -d ' ' | sort | tr '\n' ' ')
[ "$perms" = "contents:read packages:read " ] || falhar "$DEPLOY: permissions \"$perms\", queria contents: read e packages: read"
perms=$(sed -n '/^permissions:/,/^[a-z]/p' "$BACKUP" | grep -E '^  ' | tr -d ' ' | sort | tr '\n' ' ')
[ "$perms" = "contents:read " ] || falhar "$BACKUP: permissions \"$perms\", queria só contents: read"
# shellcheck disable=SC2016 # o $SHA e o $IMAGE_ID são texto do workflow
tem "$DEPLOY" 'sh deploy/ci/vps\.sh deploy "\$SHA" "\$IMAGE_ID"$' "a entrega não passa por deploy/ci/vps.sh deploy"
tem "$BACKUP" 'sh deploy/ci/vps\.sh backup ' "o backup não passa por deploy/ci/vps.sh backup"

# Os passos de guarda rodam aqui de verdade, com o bash do runner (o
# `shell: bash` dos dois workflows): o texto deles pode enganar, o
# comportamento não. passo <arquivo> <nome>: o `run: |` do passo, sem o recuo.
passo() {
    awk -v nome="$2" '
        $0 == "      - name: " nome { dentro = 1; next }
        dentro && /^      - / { exit }
        dentro && $0 == "        run: |" { bloco = 1; next }
        dentro && bloco {
            if ($0 == "") print ""
            else if ($0 ~ /^          /) print substr($0, 11)
            else exit
        }' "$1"
}
# guarda <arquivo> <nome> <passa|recusa> <VAR> <valor>: roda o passo com a
# variável e confere se ele deixa o job seguir (sai 0) ou o para.
guarda() {
    bloco=$(passo "$1" "$2")
    if [ -z "$bloco" ]; then
        falhar "$1: sem o passo \"$2\""
        return
    fi
    if env "$4=$5" bash -eo pipefail -c "$bloco" >/dev/null 2>&1; then r=passa; else r=recusa; fi
    [ "$r" = "$3" ] || falhar "$1: o passo \"$2\" com $4=$(printf '%s' "$5" | tr '\n' '|') $r, e devia: $3"
}
# Só da main (revisão dos workflows, M4): o vps.sh que fala com a VPS, com os
# secrets dela, é o que passou por PR. Vale para o deploy e para o backup.
for f in "$DEPLOY" "$BACKUP"; do
    guarda "$f" "só da main" passa GITHUB_REF refs/heads/main
    for ruim in refs/heads/outro refs/tags/v1.0.0 refs/heads/main2 refs/heads/x/refs/heads/main ""; do
        guarda "$f" "só da main" recusa GITHUB_REF "$ruim"
    done
done
# O SHA inteiro, numa linha só (M1): o grep -x casaria UMA das linhas de um
# input com quebra de linha, que o workflow_dispatch pela API aceita.
SHA_BOM=0123456789abcdef0123456789abcdef01234567
guarda "$DEPLOY" "conferir o SHA" passa SHA "$SHA_BOM"
for ruim in "x
$SHA_BOM" "$SHA_BOM
x" "$SHA_BOM
" "${SHA_BOM}0" "0123456789ABCDEF0123456789abcdef01234567" "$SHA_BOM-tools" ""; do
    guarda "$DEPLOY" "conferir o SHA" recusa SHA "$ruim"
done

# O index.json do layout: o deploy.yml o confere com o MESMO programa do jq
# que a entrada da VPS (revisão final, M3), para parar no runner com a causa
# em vez de um 3 genérico na VPS. programa_jq <arquivo>: o texto entre as
# aspas simples do `jq -e -s`, com os espaços juntados.
programa_jq() {
    sed -n "/jq -e -s --arg id /,/\$id)'/p" "$1" |
        sed "1s/^[^']*'//; \$s/'[^']*\$//" | tr -s ' \n' '  ' | sed 's/^ //; s/ $//'
}
ENTRADA=deploy/vps/stillon-entrada
prog_vps=$(programa_jq "$ENTRADA")
prog_wf=$(programa_jq "$DEPLOY")
[ -n "$prog_vps" ] || falhar "$ENTRADA: sem o jq -e -s que confere o index.json"
[ "$prog_wf" = "$prog_vps" ] ||
    falhar "$DEPLOY: o jq do index.json não é o da entrada da VPS ($ENTRADA): '$prog_wf' contra '$prog_vps'"
# E o programa faz o que diz: aceita o index.json do crane e recusa o com
# annotations no descritor.
ID_BOM=sha256:1111111111111111111111111111111111111111111111111111111111111111
indice='{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"'$ID_BOM'","size":1234,"platform":{"architecture":"amd64","os":"linux"}}]}'
printf '%s' "$indice" | jq -e -s --arg id "$ID_BOM" "$prog_wf" >/dev/null ||
    falhar "$DEPLOY: o jq do index.json recusa o layout do crane"
printf '%s' "$indice" | jq -c '.manifests[0].annotations = {"org.opencontainers.image.ref.name": "x"}' |
    jq -e -s --arg id "$ID_BOM" "$prog_wf" >/dev/null &&
    falhar "$DEPLOY: o jq do index.json aceita annotations no descritor"
# O crane v0.22.1 de verdade (log do primeiro deploy, 2026-10-14) grava também
# o artifactType no descritor. O deploy.yml o tira antes da conferência, com o
# programa do `jq -c 'del(...)'`; a VPS continua sem aceitá-lo.
prog_limpa=$(sed -n "s/^ *jq -c '\(del([^']*)\)' .*index\.json.*/\1/p" "$DEPLOY")
[ -n "$prog_limpa" ] || falhar "$DEPLOY: sem o jq -c 'del(...)' que tira o artifactType do index.json"
indice_crane=$(printf '%s' "$indice" | jq -c '.manifests[0].artifactType = "application/vnd.oci.image.config.v1+json"')
printf '%s' "$indice_crane" | jq -e -s --arg id "$ID_BOM" "$prog_vps" >/dev/null &&
    falhar "controle: a conferência da VPS aceita o artifactType"
if [ -n "$prog_limpa" ]; then
    printf '%s' "$indice_crane" | jq -c "$prog_limpa" | jq -e -s --arg id "$ID_BOM" "$prog_wf" >/dev/null ||
        falhar "$DEPLOY: o index.json do crane v0.22.1, depois da limpeza, ainda é recusado"
    printf '%s' "$indice_crane" | jq -c '.manifests[0].annotations = {"io.containerd.image.name": "x"}' |
        jq -c "$prog_limpa" | jq -e -s --arg id "$ID_BOM" "$prog_wf" >/dev/null &&
        falhar "$DEPLOY: a limpeza do index.json deixa passar annotations"
fi

# 4. Backup: diário às 06:17 UTC e à mão, desligado até BACKUP_ENABLED=1,
#    artifact por 30 dias.
tem "$BACKUP" "^    - cron: '17 6 \\* \\* \\*'" "sem o agendamento diário das 06:17 UTC"
n=$(grep -Ec '^[[:space:]]*- cron:' "$BACKUP" || true)
[ "$n" = 1 ] || falhar "$BACKUP: $n agendamentos, queria só o das 06:17 UTC"
tem "$BACKUP" '^  workflow_dispatch:' "sem o disparo manual"
tem "$BACKUP" "^    if: vars\\.BACKUP_ENABLED == '1'$" "o job não está desligado até BACKUP_ENABLED=1"
tem "$BACKUP" '^          retention-days: 30$' "o artifact não fica 30 dias"
tem "$BACKUP" '^          if-no-files-found: error$' "o upload sem arquivo tem de falhar"

# 5. Secrets: só os três da VPS (e o GITHUB_TOKEN do GHCR, no deploy). A
#    chave pública do age mora na VPS; a senha do banco e tokens de outra
#    plataforma não passam pelo GitHub.
for f in "$DEPLOY" "$BACKUP"; do
    grep -Eo 'secrets\.[A-Za-z_]+' "$f" | sort -u | while IFS= read -r s; do
        case $s in
            secrets.VPS_HOST | secrets.VPS_SSH_KEY | secrets.VPS_HOST_KEY) ;;
            secrets.GITHUB_TOKEN) [ "$f" = "$DEPLOY" ] || echo "FALHOU: $f: $s" >&2 ;;
            *) echo "FALHOU: $f: secret fora da lista: $s" >&2 ;;
        esac
    done
done 2>&1 | tee "$marcas" >&2
contar_marcas

# 6. Nada do Fly.io sobra no repositório (Plano 11, spec §1, critério 3): nem
#    arquivo, nem comando, nem secret, nem link para o runbook antigo. Fica de
#    fora docs/superpowers/ (a spec do sistema, o contrato, o roadmap e as
#    pendências correntes, que citam o Fly como história) e este script. O
#    nome do cabeçalho Fly-Client-IP continua valendo: o CLIENT_IP_HEADER o
#    aceita, e o Caddy o apaga da requisição (Obrigação 1 do Plano 11); por
#    isso a busca é pelo que só existe na plataforma, e não pela palavra "Fly".
FLY_ERE='flyctl|superfly|fly\.(io|dev|toml)|deploy/fly|registry\.fly|(^|[^a-z0-9_-])fly[[:space:]]+(deploy|ssh|volumes?|tokens?|proxy|launch|secrets?|machines?|apps|auth|scale|logs|pg|postgres|status|config|ips|certs|console|wireguard|regions)([^a-z0-9_-]|$)'
for arq in fly.toml deploy/fly docs/deploy/fly.md; do
    [ ! -e "$arq" ] || falhar "$arq ainda existe: o Fly saiu do repositório (o runbook é docs/deploy/vps.md)"
done
if git grep -nIiE "$FLY_ERE" -- . ':(exclude)docs/superpowers/**' ':(exclude)deploy/ci/checar-workflows.sh' >&2; then
    falhar "referência ao Fly.io (acima): o deploy é na VPS (docs/deploy/vps.md)"
fi
if git grep -nIE 'FLY_[A-Z]' -- . ':(exclude)docs/superpowers/**' ':(exclude)deploy/ci/checar-workflows.sh' >&2; then
    falhar "variável ou secret do Fly.io (acima)"
fi

if [ "$falhas" -gt 0 ]; then
    echo "$falhas falha(s) nos workflows" >&2
    exit 1
fi
echo "workflows: tudo certo"
