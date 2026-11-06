"""Restaura o backup guardado mais recente num Postgres sem rede.

Prova que o artifact cifrado volta a ser um banco, usando só a API do GitHub e
a chave do age. A produção nunca é contatada: nada aqui fala com a VPS.

As falhas saem como códigos curtos: o log do Actions é público, e a saída dos
passos isolados pode trazer dados do dump, por isso nunca é repetida.
"""

import datetime
import hashlib
import io
import json
import os
import pathlib
import re
import shutil
import subprocess
import tempfile
import time
import urllib.parse
import urllib.request
import uuid
import zipfile

REPO = "BipoXDXD/stillon"
API = "https://api.github.com/"
IMAGEM = "postgres@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873"
TETO_DE_RESPOSTA = 16 * 1024 * 1024
TEMPO_DA_API_S = 40
ITENS_POR_PAGINA = 100
# Dez mil artifacts: bem acima dos 963 de 2026-11-06. Chegar aqui é erro nomeado,
# nunca uma lista cortada em silêncio, que foi o defeito original.
TETO_DE_PAGINAS = 100
# O agendamento do backup atrasa até 8 h; acima de 26 h, um dia ficou sem backup.
IDADE_MAXIMA_S = 26 * 3600
PREFIXO_DO_BACKUP = "stillon-backup-"
WORKFLOW_DO_BACKUP = ".github/workflows/backup.yml"
# Os gatilhos do backup.yml. Um pull_request de fork roda o mesmo caminho, com
# conteúdo alheio, e guarda artifact neste repositório.
EVENTOS_DO_BACKUP = {"schedule", "workflow_dispatch"}
ROTULO = "io.stillon.audit"
VALOR_DO_ROTULO = "github-stored-restore"
ESPERA_DO_BANCO_S = 60


def main():
    token = os.environ.pop("GH_TOKEN")
    identidade = os.environ.pop("AGE_IDENTITY")
    obter = criar_obter(token)
    pasta = pathlib.Path(tempfile.mkdtemp(prefix="stillon-ensaio-", dir=os.environ["RUNNER_TEMP"]))
    pasta.chmod(0o700)
    try:
        escolhido = escolher_backup(listar_artifacts(obter))
        idade = conferir_idade(escolhido, agora_utc())
        run_id = escolhido["workflow_run"]["id"]
        conferir_origem(obter(f"repos/{REPO}/actions/runs/{run_id}"))
        cifrado = ler_cifrado(obter(f"repos/{REPO}/actions/artifacts/{escolhido['id']}/zip", True), escolhido)
        job = job_do_backup(obter(f"repos/{REPO}/actions/runs/{run_id}/jobs")["jobs"])
        log = obter(f"repos/{REPO}/actions/jobs/{job['id']}/logs", True).decode(errors="replace")
        versao, usuarios, alvos = ler_metadados(log)
        restaurar_isolado(decifrar(cifrado, identidade, pasta), versao, usuarios, alvos)
        print(
            json.dumps(
                {
                    "stored_restore_verified": True,
                    "artifact_id": escolhido["id"],
                    "backup_run": run_id,
                    "backup_age_hours": round(idade / 3600, 2),
                    "snapshot_schema": int(versao),
                    "minimum_users": int(usuarios),
                    "minimum_targets": int(alvos),
                    "isolation": "GitHub-hosted runner, PostgreSQL network none, no production mounts",
                    "checked_at_utc": agora_utc().isoformat(),
                }
            )
        )
    finally:
        shutil.rmtree(pasta)


def listar_artifacts(obter):
    """Todos os artifacts do repositório: o backup não está em posição garantida."""
    artifacts = []
    for pagina in range(1, TETO_DE_PAGINAS + 1):
        lote = obter(f"repos/{REPO}/actions/artifacts?per_page={ITENS_POR_PAGINA}&page={pagina}")["artifacts"]
        artifacts.extend(lote)
        if len(lote) < ITENS_POR_PAGINA:
            return artifacts
    raise RuntimeError("too_many_artifact_pages")


def escolher_backup(artifacts):
    candidatos = [a for a in artifacts if e_backup_da_main(a)]
    if not candidatos:
        raise RuntimeError("no_stored_backup")
    return max(candidatos, key=criado_em)


def e_backup_da_main(artifact):
    # head_branch é só um nome: um fork com a sua própria main passaria por ele.
    # A API devolve workflow_run null quando o run já não existe.
    run = artifact.get("workflow_run") or {}
    repositorio = run.get("repository_id")
    return (
        not artifact["expired"]
        and artifact["name"].startswith(PREFIXO_DO_BACKUP)
        and run.get("head_branch") == "main"
        and repositorio is not None
        and run.get("head_repository_id") == repositorio
    )


def criado_em(artifact):
    return datetime.datetime.fromisoformat(artifact["created_at"].replace("Z", "+00:00"))


def conferir_idade(artifact, agora):
    idade = (agora - criado_em(artifact)).total_seconds()
    if not 0 <= idade <= IDADE_MAXIMA_S:
        raise RuntimeError("backup_age_outside_policy")
    return idade


def conferir_origem(run):
    confiavel = (
        run["conclusion"] == "success"
        and run["head_branch"] == "main"
        and run["path"] == WORKFLOW_DO_BACKUP
        and run.get("event") in EVENTOS_DO_BACKUP
    )
    if not confiavel:
        raise RuntimeError("untrusted_backup_run")


def ler_cifrado(arquivo, artifact):
    if artifact.get("digest") != "sha256:" + hashlib.sha256(arquivo).hexdigest():
        raise RuntimeError("stored_digest_mismatch")
    with zipfile.ZipFile(io.BytesIO(arquivo)) as pacote:
        entradas = [e for e in pacote.infolist() if not e.is_dir()]
        if len(entradas) != 1 or entradas[0].file_size > TETO_DE_RESPOSTA:
            raise RuntimeError("unexpected_archive")
        cifrado = pacote.read(entradas[0])
    if not cifrado.startswith(b"age-encryption.org/v1\n"):
        raise RuntimeError("not_age")
    return cifrado


def job_do_backup(jobs):
    for job in jobs:
        if job["name"] == "backup" and job["conclusion"] == "success":
            return job
    raise RuntimeError("backup_job_missing")


def ler_metadados(log):
    achado = re.search(r"schema (\d+), (\d+) usuário\(s\), (\d+) alvo\(s\)", log)
    if not achado:
        raise RuntimeError("snapshot_metadata_missing")
    return achado.group(1, 2, 3)


def decifrar(cifrado, identidade, pasta):
    caminho_cifrado = pasta / "stored.age"
    caminho_cifrado.write_bytes(cifrado)
    caminho_cifrado.chmod(0o600)
    caminho_identidade = pasta / "identity"
    caminho_identidade.touch(mode=0o600)
    caminho_identidade.write_text(identidade + "\n")
    try:
        claro = rodar(["age", "-d", "-i", str(caminho_identidade), str(caminho_cifrado)])
    finally:
        caminho_identidade.unlink()
    if not claro.startswith(b"PGDMP"):
        raise RuntimeError("invalid_dump")
    return claro


def restaurar_isolado(claro, versao, usuarios, alvos):
    """Sobe um Postgres sem rede e sem privilégios e restaura o dump nele."""
    nome = "stillon-ensaio-" + uuid.uuid4().hex[:12]
    rodar(["docker", "pull", IMAGEM], tempo=180)
    rodar(argumentos_do_banco(nome))
    try:
        esperar_banco(nome)
        rodar(
            ["docker", "exec", "-i", nome, "psql", "-X", "-U", "postgres", "-d", "postgres", "-v", "ON_ERROR_STOP=1"],
            b"CREATE ROLE stillon_app LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE;"
            b" CREATE DATABASE stillon OWNER stillon_app;",
        )
        rodar(["docker", "exec", "-i", nome, "pg_restore", "--exit-on-error", "-U", "postgres", "-d", "stillon"], claro)
        rodar(
            ["docker", "exec", "-i", nome, "psql", "-X", "-U", "postgres", "-d", "stillon", "-v", "ON_ERROR_STOP=1"]
            + ["-v", "versao=" + versao, "-v", "usuarios=" + usuarios, "-v", "alvos=" + alvos],
            pathlib.Path("deploy/backup/verificar.sql").read_bytes(),
        )
    finally:
        remover_banco(nome)


def argumentos_do_banco(nome):
    return [
        "docker", "run", "-d", "--name", nome, "--label", f"{ROTULO}={VALOR_DO_ROTULO}",
        "--network", "none", "--read-only", "--cap-drop", "ALL",
        "--security-opt", "no-new-privileges:true", "--user", "70:70",
        "--memory", "768m", "--cpus", "1", "--pids-limit", "128",
        "--tmpfs", "/var/lib/postgresql:rw,size=256m,uid=70,gid=70",
        "--tmpfs", "/var/run/postgresql:rw,size=16m,uid=70,gid=70",
        "--tmpfs", "/tmp:rw,size=16m,uid=70,gid=70",
        "-e", "POSTGRES_HOST_AUTH_METHOD=trust", IMAGEM,
        "-c", "log_error_verbosity=terse", "-c", "log_min_error_statement=panic",
    ]  # fmt: skip


def esperar_banco(nome):
    for _ in range(ESPERA_DO_BANCO_S):
        pronto = subprocess.run(
            ["docker", "exec", nome, "pg_isready", "-h", "127.0.0.1", "-U", "postgres"],
            capture_output=True,
            check=False,
        )
        if pronto.returncode == 0:
            return
        time.sleep(1)
    raise RuntimeError("isolated_database_not_ready")


def remover_banco(nome):
    rotulo = rodar(["docker", "inspect", nome, "--format", "{{index .Config.Labels \"" + ROTULO + "\"}}"])
    if rotulo.decode().strip() != VALOR_DO_ROTULO:
        raise RuntimeError("cleanup_label_mismatch")
    rodar(["docker", "rm", "-f", "-v", nome])


def rodar(argumentos, entrada=None, tempo=90):
    processo = subprocess.run(argumentos, input=entrada, capture_output=True, timeout=tempo, check=False)
    if processo.returncode:
        raise RuntimeError("isolated_step_failed")
    return processo.stdout


def criar_obter(token):
    abridor = urllib.request.build_opener(RedirecionamentoSeguro())

    def obter(caminho, bruto=False):
        pedido = urllib.request.Request(
            API + caminho,
            headers={
                "User-Agent": "StillOn-stored-restore-audit",
                "Accept": "application/vnd.github+json",
                "Authorization": "Bearer " + token,
            },
        )
        with abridor.open(pedido, timeout=TEMPO_DA_API_S) as resposta:
            dados = resposta.read(TETO_DE_RESPOSTA + 1)
        if len(dados) > TETO_DE_RESPOSTA:
            raise RuntimeError("response_too_large")
        return dados if bruto else json.loads(dados)

    return obter


class RedirecionamentoSeguro(urllib.request.HTTPRedirectHandler):
    """Segue o download do artifact sem levar o token para fora da API."""

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        destino = urllib.parse.urlparse(newurl)
        if destino.scheme != "https":
            raise RuntimeError("insecure_redirect")
        pedido = super().redirect_request(req, fp, code, msg, headers, newurl)
        if destino.hostname != "api.github.com":
            pedido.remove_header("Authorization")
        return pedido


def agora_utc():
    return datetime.datetime.now(datetime.timezone.utc)


if __name__ == "__main__":
    main()
