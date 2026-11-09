"""Contrato do recibo: send.py e receiver.py gravam, stillon-entrada lê.

Reais: a montagem do release, a validação, a importação, o deploy, o JSON gravado,
as funções shell do backup e o filtro jq. Simulados só Docker, a API do Coolify e o
dono root dos arquivos. Nenhum lado copia o formato do outro: o recibo sai do código
que grava e cada caso negativo altera esse arquivo, nunca um recibo escrito à mão.
"""

import json
import os
import re
import shutil
import stat
import subprocess
import sys
import tempfile
import unittest
from collections.abc import Callable
from pathlib import Path
from typing import Any
from unittest.mock import patch

import receiver as r
import send
from test_image_storage import layout

ROOT = Path(__file__).resolve().parents[3]
SHA = "a" * 40
OUTRO_SHA = "e" * 40
# Digests fixos do layout() padrão, calculados à parte da lógica testada.
MANIFESTO = "sha256:8734e1bf931200f2c99f552bcfd87c539da53444d06025146a7fae551537cbe0"
IMAGEM_ID = "sha256:9cd31762e7050165195c9469ccea41a23c079f2d0f51b5e9bf0c9d7842618b72"
IMAGEM_LEGADA = "sha256:" + "f" * 64
REFERENCIA = "ghcr.io/bipoxdxd/stillon@" + MANIFESTO
REFERENCIA_IMPORTADA = "ghcr.io/bipoxdxd/stillon:release-" + SHA + "@" + MANIFESTO
ALIAS_LEGADO = "stillon:" + SHA
ORIGEM = "https://github.com/BipoXDXD/stillon"
APLICACAO = "aplicacao-stillon"
CONTAINER = "conteiner-stillon"
IMPLANTACAO = "implantacao-1"
CONCLUIDO_EM = 1762560000.0
CONFIG = {
    "uuid": APLICACAO,
    "containers": [CONTAINER],
    "image_roles": {CONTAINER: "app"},
    "readiness": [],
}
Alteracao = Callable[[dict[str, Any]], None]

# Docker e stat são funções para não depender de /tmp executável nem de stat GNU.
FALSOS_DO_LEITOR = r"""
sair() { printf '%s\n' "$2" >&2; exit "$1"; }
stat() {
    if [ "$3" = "$ESTADO_COOLIFY/current.json" ]; then
        printf '%s\n' "$META_RECIBO"
    else
        echo '0 700 2 4096'
    fi
}
docker() {
    printf '%s\n' "$*" >> "$RAIZ/comandos"
    if [ "$1" = create ]; then
        # Limite do teste: registra a imagem escolhida e para antes de criar recurso.
        printf '%s' "${@: -2:1}" > "$RAIZ/selecionada"
        return 1
    fi
    local alvo="${@: -1}"
    case "$alvo" in
        "$REFERENCIA_PUBLICADA"|ghcr.io/bipoxdxd/stillon:latest)
            printf '%s\n' "$IMAGEM_ID|$COMMIT|$ORIGEM" ;;
        "$ALIAS_LEGADO") printf '%s\n' "$IMAGEM_LEGADA" ;;
        *) return 1 ;;
    esac
}
"""


def funcao_do_leitor(fonte: str, nome: str) -> str:
    achado = re.search(r"^" + nome + r"\(\) \{.*?^\}", fonte, re.M | re.S)
    if achado is None:
        raise AssertionError("stillon-entrada não define " + nome)
    return achado.group()


def variavel_do_leitor(fonte: str, nome: str) -> str:
    achado = re.search(r"^" + nome + r"=(.+)$", fonte, re.M)
    if achado is None:
        raise AssertionError("stillon-entrada não define " + nome)
    return achado.group(1)


def remover(*caminho: str) -> Alteracao:
    def alterar(recibo: dict[str, Any]) -> None:
        alvo = recibo
        for chave in caminho[:-1]:
            alvo = alvo[chave]
        del alvo[caminho[-1]]

    return alterar


def formato_antigo(recibo: dict[str, Any]) -> None:
    del recibo["release"]["receiver_sha256"]
    recibo["release"]["version"] = 1


class ContratoReciboBackupTests(unittest.TestCase):
    def setUp(self) -> None:
        self.raiz = Path(tempfile.mkdtemp(prefix="stillon-recibo-")).resolve()
        self.addCleanup(shutil.rmtree, self.raiz)
        self.estado = self.raiz / "estado"
        self.estado.mkdir(mode=0o700)
        self.opt = self.raiz / "opt"
        self.opt.mkdir()
        self.recibo = self.estado / "current.json"
        self.chamadas_docker: list[list[str]] = []
        self.caminhos_versao: list[Path] = []
        self.fonte_leitor = (ROOT / "deploy/vps/stillon-entrada").read_text()
        self.publicar()

    def publicar(self) -> None:
        palco = self.raiz / "palco"
        layout(palco / "app")
        pares = self.raiz / "pares.tsv"
        pares.write_text("app\t" + REFERENCIA + "\n")
        argumentos = ["send.py", "manifest", str(pares), str(palco)]
        with (
            patch.dict(os.environ, {"RELEASE_PROJECT": "stillon", "RELEASE_COMMIT": SHA}),
            patch.object(sys, "argv", argumentos),
        ):
            send.main()
        release = r.validate_release(
            r.json_unique((palco / "release.json").read_bytes()), "stillon", SHA
        )
        atomic_text = r.atomic_text

        def gravar_versao(caminho: Path, texto: str) -> None:
            self.caminhos_versao.append(caminho)
            atomic_text(self.opt / caminho.name, texto)

        with (
            patch.object(r, "command", side_effect=self.docker_do_receptor),
            patch.object(r, "api", side_effect=self.api_do_coolify),
            patch.object(r, "atomic_text", side_effect=gravar_versao),
            patch("time.time", return_value=CONCLUIDO_EM),
        ):
            importada, ids = r.import_images(release, palco)
            r.deploy(release, importada, CONFIG, self.estado, ids)

    def docker_do_receptor(self, argv: list[str]) -> bytes:
        self.chamadas_docker.append(argv)
        match argv[1:]:
            case ["load", "-i", _] | ["tag", _, _]:
                return b""
            case ["image", "inspect", alvo] if alvo in {IMAGEM_ID, REFERENCIA_IMPORTADA}:
                registro = {"Id": IMAGEM_ID, "Architecture": "amd64", "Os": "linux"}
                return json.dumps([registro]).encode()
            case ["inspect", nome] if nome == CONTAINER:
                registro = {
                    "Image": IMAGEM_ID,
                    "State": {"Status": "running", "Health": {"Status": "healthy"}},
                    "Config": {"Labels": {"coolify.applicationUuid": APLICACAO}},
                }
                return json.dumps([registro]).encode()
        raise AssertionError("chamada Docker fora do roteiro: " + " ".join(argv))

    def api_do_coolify(self, caminho: str, metodo: str = "GET", corpo: Any = None) -> Any:
        if caminho.startswith("deployments/applications/"):
            return {"deployments": [], "count": 0}
        if caminho == "applications/" + APLICACAO + "/envs/bulk" and metodo == "PATCH":
            return {}
        if caminho == "applications/" + APLICACAO + "/start":
            return {"deployment_uuid": IMPLANTACAO}
        if caminho == "deployments/" + IMPLANTACAO:
            return {"status": "finished"}
        raise AssertionError("chamada Coolify fora do roteiro: " + caminho)

    def executar_leitor(self) -> str | None:
        for nome in ["comandos", "selecionada"]:
            (self.raiz / nome).unlink(missing_ok=True)
        funcoes = [
            funcao_do_leitor(self.fonte_leitor, nome)
            for nome in ["imagem_helper_aprovada", "preparar_helper_backup"]
        ]
        script = "\n".join(
            [
                "set -euo pipefail",
                FALSOS_DO_LEITOR,
                *funcoes,
                "DIR=$RAIZ/opt",
                "ESTADO_COOLIFY=$ESTADO",
                "IMAGEM=stillon",
                "SHM_BASE=$RAIZ",
                "preparar_helper_backup",
            ]
        )
        meta = os.lstat(self.recibo)
        ambiente = {
            **os.environ,
            "RAIZ": str(self.raiz),
            "ESTADO": str(self.estado),
            "META_RECIBO": f"0 {stat.S_IMODE(meta.st_mode):o} {meta.st_nlink} {meta.st_size}",
            "REFERENCIA_PUBLICADA": REFERENCIA,
            "IMAGEM_ID": IMAGEM_ID,
            "IMAGEM_LEGADA": IMAGEM_LEGADA,
            "ALIAS_LEGADO": ALIAS_LEGADO,
            "COMMIT": SHA,
            "ORIGEM": ORIGEM,
        }
        resultado = subprocess.run(
            ["bash", "-c", script], env=ambiente, capture_output=True, text=True, timeout=30
        )
        self.assertEqual(resultado.returncode, 1, resultado.stderr)  # create é interrompido.
        selecionada = self.raiz / "selecionada"
        return selecionada.read_text() if selecionada.exists() else None

    def comandos_do_leitor(self) -> list[str]:
        comandos = self.raiz / "comandos"
        return comandos.read_text().splitlines() if comandos.exists() else []

    def alterar_recibo(self, original: bytes, alteracao: Alteracao) -> None:
        recibo = json.loads(original)
        alteracao(recibo)
        self.recibo.write_text(json.dumps(recibo))

    def assert_recusado_sem_recorrer_ao_alias(self) -> None:
        self.assertIsNone(self.executar_leitor())
        for comando in self.comandos_do_leitor():
            self.assertTrue(comando.startswith("image inspect"), comando)
            self.assertNotIn(ALIAS_LEGADO, comando)

    def assert_leitor_aceita_o_recibo_real(self) -> None:
        # Sem esta prova, recusa causada pela fixture passaria por recusa do recibo.
        self.assertEqual(self.executar_leitor(), IMAGEM_ID)

    def test_sucesso_etiqueta_a_imagem_com_o_sha(self) -> None:
        self.assertIn(
            ["/usr/bin/docker", "tag", REFERENCIA_IMPORTADA, ALIAS_LEGADO],
            self.chamadas_docker,
        )

    def test_sucesso_grava_a_versao_aprovada(self) -> None:
        self.assertEqual(self.caminhos_versao, [Path("/opt/stillon/versao")])
        self.assertEqual((self.opt / "versao").read_text(), SHA + "\n")

    def test_sucesso_grava_o_recibo_e_encerra_a_pendencia(self) -> None:
        self.assertEqual(
            json.loads(self.recibo.read_text()),
            {
                "release": {
                    "version": 2,
                    # O hash muda a cada revisão do receptor; não há literal estável.
                    "receiver_sha256": r.receiver_digest(),
                    "project": "stillon",
                    "commit": SHA,
                    "images": {"app": REFERENCIA},
                },
                "deployment_uuid": IMPLANTACAO,
                "finished_at": CONCLUIDO_EM,
            },
        )
        self.assertFalse((self.estado / "pending.json").exists())

    def test_leitor_aprova_a_imagem_do_recibo_real(self) -> None:
        self.assertEqual(self.executar_leitor(), IMAGEM_ID)
        comandos = self.comandos_do_leitor()
        self.assertTrue(any(comando.endswith(REFERENCIA) for comando in comandos), comandos)
        self.assertFalse(any(ALIAS_LEGADO in comando for comando in comandos), comandos)

    def test_lados_usam_os_mesmos_caminhos(self) -> None:
        estado_lido = variavel_do_leitor(self.fonte_leitor, "ESTADO_COOLIFY")
        self.assertEqual(estado_lido, "/var/lib/coolify-release/stillon")
        self.assertEqual(str(r.STATE / "stillon"), estado_lido)
        diretorio_lido = variavel_do_leitor(self.fonte_leitor, "DIR")
        self.assertEqual(diretorio_lido, "/opt/stillon")
        self.assertEqual(self.caminhos_versao, [Path(diretorio_lido) / "versao"])

    def test_campo_ausente_e_recusado(self) -> None:
        original = self.recibo.read_bytes()
        self.assert_leitor_aceita_o_recibo_real()
        for caminho in [
            ("release",),
            ("release", "version"),
            ("release", "receiver_sha256"),
            ("release", "project"),
            ("release", "commit"),
            ("release", "images"),
            ("release", "images", "app"),
        ]:
            with self.subTest(caminho=".".join(caminho)):
                self.alterar_recibo(original, remover(*caminho))
                self.assert_recusado_sem_recorrer_ao_alias()

    def test_versao_ausente_e_recusada(self) -> None:
        self.assert_leitor_aceita_o_recibo_real()
        (self.opt / "versao").unlink()
        self.assert_recusado_sem_recorrer_ao_alias()

    def test_formato_antigo_ou_invalido_e_recusado(self) -> None:
        original = self.recibo.read_bytes()
        self.assert_leitor_aceita_o_recibo_real()
        casos: dict[str, Alteracao] = {
            "versao_1_sem_carimbo": formato_antigo,
            "versao_3": lambda recibo: recibo["release"].update(version=3),
            "versao_texto": lambda recibo: recibo["release"].update(version="2"),
            "campo_novo": lambda recibo: recibo["release"].update(canal="estavel"),
            "papel_novo": lambda recibo: recibo["release"]["images"].update(db=REFERENCIA),
            "commit_de_outra_versao": lambda recibo: recibo["release"].update(commit=OUTRO_SHA),
            "projeto_alheio": lambda recibo: recibo["release"].update(project="outro-projeto"),
            "carimbo_invalido": lambda recibo: recibo["release"].update(receiver_sha256="x"),
            "tag_mutavel": lambda recibo: recibo["release"]["images"].update(
                app="ghcr.io/bipoxdxd/stillon:latest"
            ),
            "release_lista": lambda recibo: recibo.update(release=[]),
        }
        for nome, alteracao in casos.items():
            with self.subTest(nome=nome):
                self.alterar_recibo(original, alteracao)
                self.assert_recusado_sem_recorrer_ao_alias()

    def test_json_corrompido_e_recusado(self) -> None:
        self.assert_leitor_aceita_o_recibo_real()
        for conteudo in ['{"release":', "[]", "null"]:
            with self.subTest(conteudo=conteudo):
                self.recibo.write_text(conteudo)
                self.assert_recusado_sem_recorrer_ao_alias()


if __name__ == "__main__":
    unittest.main()
