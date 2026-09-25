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

# 1. Nenhum workflow aceita a host key do primeiro servidor que responder.
#    O ssh aceita "no", "off" e "accept-new" (este último também confia no
#    primeiro contato); ssh-keyscan no job é o mesmo erro por outro caminho.
if grep -EniH 'StrictHostKeyChecking[[:space:]=]+"?(no|off|accept-new)' "$WF"/*.yml; then
    falhar "workflow com StrictHostKeyChecking desligado (acima): a host key vem do secret VPS_HOST_KEY"
fi
if grep -EniH 'ssh-keyscan' "$WF"/*.yml; then
    falhar "workflow com ssh-keyscan (acima): a host key vem do secret VPS_HOST_KEY, nunca da rede"
fi

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
#    fora a história (docs/superpowers/: specs, planos e pendências de
#    planos já feitos) e este script. O nome do cabeçalho Fly-Client-IP
#    continua valendo: o CLIENT_IP_HEADER o aceita, e o Caddy o apaga da
#    requisição (Obrigação 1 do Plano 11); por isso a busca é pelo que só
#    existe na plataforma, e não pela palavra "Fly".
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
