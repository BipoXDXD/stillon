#!/usr/bin/env bash
set -euo pipefail

root=$PWD
reports=$1
verificador=$root/deploy/ci/picomatch_backport.py
trivy=$(sed -n 's/^FROM //p' deploy/ci/trivy/Dockerfile)
[[ $trivy =~ ^[^[:space:]]+:([0-9]+\.[0-9]+\.[0-9]+)@sha256:[a-f0-9]{64}$ ]]
versao=${BASH_REMATCH[1]}

temporario=$(mktemp -d "${RUNNER_TEMP:?}/supply-chain-npm.XXXXXX")
trap 'rm -rf -- "$temporario"' EXIT
cache=$temporario/cache
alvo=$temporario/alvo
mkdir -p "$cache" "$alvo/web"
cp -- web/package-lock.json "$alvo/web/package-lock.json"
python3 "$verificador" verify "$root"

varrer() {
    local rede=$1
    shift
    docker run --rm --network "$rede" --user "$(id -u):$(id -g)" --env HOME=/tmp \
        --cpus=1 --memory=2g --pids-limit=128 \
        -v "$alvo:/scan:ro" -v "$reports:/reports" -v "$cache:/trivy-cache" \
        -w /scan "$trivy" --config '' --cache-dir /trivy-cache --timeout 8m fs . \
        --parallel 1 \
        --scanners vuln --severity HIGH,CRITICAL --include-dev-deps \
        --db-repository public.ecr.aws/aquasecurity/trivy-db,ghcr.io/aquasecurity/trivy-db \
        --ignorefile /dev/null --format json --list-all-pkgs --show-suppressed "$@"
}

hash_da_base() (
    cd "$cache"
    sha256sum db/trivy.db db/metadata.json
)

varrer bridge --exit-code 0 --output /reports/trivy-raw.json
hash_da_base > "$reports/dataset-digests.sha256"
cmp -- web/package-lock.json "$alvo/web/package-lock.json"
python3 "$verificador" emit "$root" "$reports" "$versao"

vex=()
if [[ -f $reports/picomatch.openvex.json ]]; then
    vex=(--vex /reports/picomatch.openvex.json)
fi
python3 "$verificador" guard "$root" "$reports" "$versao"
hash_da_base | cmp - "$reports/dataset-digests.sha256"
status=0
varrer none --offline-scan --skip-db-update --skip-java-db-update --skip-check-update \
    "${vex[@]}" --exit-code 1 --output /reports/trivy-policy.json || status=$?
hash_da_base | cmp - "$reports/dataset-digests.sha256"
cmp -- web/package-lock.json "$alvo/web/package-lock.json"
python3 "$verificador" audit "$root" "$reports" "$versao"
exit "$status"
