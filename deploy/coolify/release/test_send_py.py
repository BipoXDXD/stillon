"""Subcomando `requested` do send.py: o que lê, o TSV que imprime e o que recusa.

O send.sh consome essa saída linha a linha (`role<TAB>referência`), então a ordem das
colunas e a ordenação dos papéis são contrato. Os arquivos de entrada (publicação do
Duora, imagens escaneadas) são escritos à mão em pasta temporária; nada de rede.
"""

import contextlib
import io
import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from typing import Any
from unittest.mock import patch

import send

RELEASE_DIR = Path(__file__).resolve().parent
SEND_PY = RELEASE_DIR / "send.py"

SHA = "0123456789abcdef0123456789abcdef01234567"
OUTRO_SHA = "89abcdef0123456789abcdef0123456789abcdef"
DIGEST = "sha256:" + "b" * 64

GOBLIN_APP = "ghcr.io/bipoxdxd/goblinshift"
GOBLIN_WEB = "ghcr.io/bipoxdxd/goblinshift-web"
GOBLIN_EXECUTOR = "ghcr.io/bipoxdxd/goblinshift-executor"
DUORA_PAPEIS = ["web", "worker", "migrator", "edge", "postgres"]


def duora_imagens() -> dict[str, str]:
    return {papel: f"ghcr.io/bipoxdxd/duora-{papel}@{DIGEST}" for papel in DUORA_PAPEIS}


def goblin_imagens() -> dict[str, str]:
    # Ordem de inserção diferente da ordenada: a saída tem de vir ordenada mesmo assim.
    return {
        "web": f"{GOBLIN_WEB}@{DIGEST}",
        "executor": f"{GOBLIN_EXECUTOR}@{DIGEST}",
        "app": f"{GOBLIN_APP}@{DIGEST}",
    }


def publicacao_duora(**mudancas: Any) -> dict[str, Any]:
    return {"commit": SHA, "architecture": "amd64", "images": duora_imagens()} | mudancas


class RequestedTestCase(unittest.TestCase):
    def setUp(self) -> None:
        pasta = tempfile.TemporaryDirectory()
        self.addCleanup(pasta.cleanup)
        self.pasta = Path(pasta.name)

    def arquivo_json(self, nome: str, conteudo: Any) -> Path:
        caminho = self.pasta / nome
        caminho.write_text(json.dumps(conteudo))
        return caminho

    def ambiente(self, projeto: str, commit: str = SHA, **extras: Path) -> dict[str, str]:
        ambiente = {"RELEASE_PROJECT": projeto, "RELEASE_COMMIT": commit}
        if "publicacao" in extras:
            ambiente["PUBLICATION_FILE"] = str(extras["publicacao"])
        if "escaneadas" in extras:
            ambiente["SCANNED_IMAGES_FILE"] = str(extras["escaneadas"])
        return ambiente

    def imprimir(self, ambiente: dict[str, str], argumentos: list[str] | None = None) -> str:
        """Roda send.main() com o ambiente dado e devolve só o que foi para o stdout."""
        if argumentos is None:
            argumentos = ["requested"]
        saida = io.StringIO()
        with (
            patch.dict(os.environ, ambiente, clear=True),
            patch.object(sys, "argv", ["send.py", *argumentos]),
            contextlib.redirect_stdout(saida),
        ):
            send.main()
        return saida.getvalue()


class StillonTests(RequestedTestCase):
    def test_imprime_a_unica_imagem_marcada_com_o_commit(self) -> None:
        self.assertEqual(
            self.imprimir(self.ambiente("stillon")),
            "app\tghcr.io/bipoxdxd/stillon:" + SHA + "\n",
        )

    def test_nao_le_arquivo_de_entrada(self) -> None:
        ausente = self.pasta / "nao-existe.json"
        ambiente = self.ambiente("stillon", escaneadas=ausente, publicacao=ausente)
        self.assertEqual(
            self.imprimir(ambiente),
            "app\tghcr.io/bipoxdxd/stillon:" + SHA + "\n",
        )


class GoblinshiftTests(RequestedTestCase):
    def requisitar(self, conteudo: Any) -> str:
        escaneadas = self.arquivo_json("escaneadas.json", conteudo)
        return self.imprimir(self.ambiente("goblinshift", escaneadas=escaneadas))

    def test_imprime_papeis_ordenados_com_papel_antes_da_referencia(self) -> None:
        self.assertEqual(
            self.requisitar(goblin_imagens()),
            f"app\t{GOBLIN_APP}@{DIGEST}\n"
            f"executor\t{GOBLIN_EXECUTOR}@{DIGEST}\n"
            f"web\t{GOBLIN_WEB}@{DIGEST}\n",
        )

    def test_entrada_vazia_falta_todo_papel(self) -> None:
        with self.assertRaisesRegex(ValueError, "Missing scanned image roles"):
            self.requisitar({})

    def test_lista_no_lugar_do_objeto_falta_todo_papel(self) -> None:
        with self.assertRaisesRegex(ValueError, "Missing scanned image roles"):
            self.requisitar([])

    def test_papel_faltando_e_recusado(self) -> None:
        imagens = goblin_imagens()
        del imagens["executor"]
        with self.assertRaisesRegex(ValueError, "Missing scanned image roles"):
            self.requisitar(imagens)

    def test_papel_a_mais_e_recusado(self) -> None:
        imagens = goblin_imagens() | {"extra": f"{GOBLIN_APP}@{DIGEST}"}
        with self.assertRaisesRegex(ValueError, "Missing scanned image roles"):
            self.requisitar(imagens)

    def test_referencia_por_tag_nao_e_imutavel(self) -> None:
        imagens = goblin_imagens() | {"web": f"{GOBLIN_WEB}:latest"}
        with self.assertRaisesRegex(ValueError, "immutable exact references"):
            self.requisitar(imagens)

    def test_repositorio_de_outro_papel_e_recusado(self) -> None:
        imagens = goblin_imagens() | {"web": f"{GOBLIN_APP}@{DIGEST}"}
        with self.assertRaisesRegex(ValueError, "immutable exact references"):
            self.requisitar(imagens)

    def test_digest_curto_e_recusado(self) -> None:
        imagens = goblin_imagens() | {"app": f"{GOBLIN_APP}@sha256:" + "b" * 63}
        with self.assertRaisesRegex(ValueError, "immutable exact references"):
            self.requisitar(imagens)

    def test_digest_com_hexadecimal_maiusculo_e_recusado(self) -> None:
        imagens = goblin_imagens() | {"app": f"{GOBLIN_APP}@sha256:" + "B" * 64}
        with self.assertRaisesRegex(ValueError, "immutable exact references"):
            self.requisitar(imagens)

    def test_lixo_depois_do_digest_e_recusado(self) -> None:
        imagens = goblin_imagens() | {"app": f"{GOBLIN_APP}@{DIGEST}\n"}
        with self.assertRaisesRegex(ValueError, "immutable exact references"):
            self.requisitar(imagens)

    def test_arquivo_que_nao_e_json_falha(self) -> None:
        escaneadas = self.pasta / "escaneadas.json"
        escaneadas.write_text("isto nao e json")
        with self.assertRaises(json.JSONDecodeError):
            self.imprimir(self.ambiente("goblinshift", escaneadas=escaneadas))

    def test_arquivo_ausente_falha(self) -> None:
        ausente = self.pasta / "nao-existe.json"
        with self.assertRaises(FileNotFoundError):
            self.imprimir(self.ambiente("goblinshift", escaneadas=ausente))

    def test_variavel_do_arquivo_ausente_falha(self) -> None:
        with self.assertRaises(KeyError):
            self.imprimir(self.ambiente("goblinshift"))


class DuoraTests(RequestedTestCase):
    def requisitar(self, publicacao: Any, commit: str = SHA) -> str:
        arquivo = self.arquivo_json("publicacao.json", publicacao)
        return self.imprimir(self.ambiente("duora", commit, publicacao=arquivo))

    def test_imprime_os_cinco_papeis_ordenados(self) -> None:
        self.assertEqual(
            self.requisitar(publicacao_duora()),
            f"edge\tghcr.io/bipoxdxd/duora-edge@{DIGEST}\n"
            f"migrator\tghcr.io/bipoxdxd/duora-migrator@{DIGEST}\n"
            f"postgres\tghcr.io/bipoxdxd/duora-postgres@{DIGEST}\n"
            f"web\tghcr.io/bipoxdxd/duora-web@{DIGEST}\n"
            f"worker\tghcr.io/bipoxdxd/duora-worker@{DIGEST}\n",
        )

    def test_nao_le_o_arquivo_das_imagens_escaneadas(self) -> None:
        publicacao = self.arquivo_json("publicacao.json", publicacao_duora())
        ausente = self.pasta / "nao-existe.json"
        ambiente = self.ambiente("duora", publicacao=publicacao, escaneadas=ausente)
        self.assertIn("edge\t", self.imprimir(ambiente))

    def test_publicacao_de_outro_commit_e_recusada(self) -> None:
        with self.assertRaisesRegex(ValueError, "Wrong publication"):
            self.requisitar(publicacao_duora(commit=OUTRO_SHA))

    def test_arquitetura_que_nao_e_amd64_e_recusada(self) -> None:
        with self.assertRaisesRegex(ValueError, "Wrong publication"):
            self.requisitar(publicacao_duora(architecture="arm64"))

    def test_papeis_vazios_sao_recusados(self) -> None:
        with self.assertRaisesRegex(ValueError, "Unexpected image roles"):
            self.requisitar(publicacao_duora(images={}))

    def test_papel_faltando_e_recusado(self) -> None:
        imagens = duora_imagens()
        del imagens["postgres"]
        with self.assertRaisesRegex(ValueError, "Unexpected image roles"):
            self.requisitar(publicacao_duora(images=imagens))

    def test_papel_a_mais_e_recusado(self) -> None:
        imagens = duora_imagens() | {"extra": f"ghcr.io/bipoxdxd/duora-web@{DIGEST}"}
        with self.assertRaisesRegex(ValueError, "Unexpected image roles"):
            self.requisitar(publicacao_duora(images=imagens))

    def test_referencia_por_tag_e_recusada(self) -> None:
        imagens = duora_imagens() | {"web": "ghcr.io/bipoxdxd/duora-web:latest"}
        with self.assertRaisesRegex(ValueError, "Unexpected published image"):
            self.requisitar(publicacao_duora(images=imagens))

    def test_repositorio_de_outro_papel_e_recusado(self) -> None:
        imagens = duora_imagens() | {"web": f"ghcr.io/bipoxdxd/duora-worker@{DIGEST}"}
        with self.assertRaisesRegex(ValueError, "Unexpected published image"):
            self.requisitar(publicacao_duora(images=imagens))

    def test_campo_commit_ausente_falha(self) -> None:
        publicacao = publicacao_duora()
        del publicacao["commit"]
        with self.assertRaises(KeyError):
            self.requisitar(publicacao)

    def test_campo_images_ausente_falha(self) -> None:
        publicacao = publicacao_duora()
        del publicacao["images"]
        with self.assertRaises(KeyError):
            self.requisitar(publicacao)

    def test_variavel_do_arquivo_ausente_falha(self) -> None:
        with self.assertRaises(KeyError):
            self.imprimir(self.ambiente("duora"))


class FronteirasComunsTests(RequestedTestCase):
    def test_commit_fora_de_quarenta_hexadecimais_minusculos_e_recusado(self) -> None:
        invalidos = {
            "vazio": "",
            "curto": SHA[:39],
            "longo": SHA + "0",
            "maiusculo": SHA.upper(),
            "nao hexadecimal": "g" * 40,
            "quebra de linha no fim": SHA + "\n",
        }
        for nome, commit in invalidos.items():
            with self.subTest(nome), self.assertRaisesRegex(ValueError, "Invalid release commit"):
                self.imprimir(self.ambiente("stillon", commit))

    def test_commit_e_conferido_antes_de_ler_qualquer_arquivo(self) -> None:
        ausente = self.pasta / "nao-existe.json"
        ambiente = self.ambiente("duora", "curto", publicacao=ausente)
        with self.assertRaisesRegex(ValueError, "Invalid release commit"):
            self.imprimir(ambiente)

    def test_projeto_desconhecido_falha(self) -> None:
        escaneadas = self.arquivo_json("escaneadas.json", {})
        with self.assertRaises(KeyError):
            self.imprimir(self.ambiente("outro-projeto", escaneadas=escaneadas))

    def test_projeto_ausente_do_ambiente_falha(self) -> None:
        with self.assertRaises(KeyError):
            self.imprimir({"RELEASE_COMMIT": SHA})

    def test_commit_ausente_do_ambiente_falha(self) -> None:
        with self.assertRaises(KeyError):
            self.imprimir({"RELEASE_PROJECT": "stillon"})

    def test_operacao_ausente_ou_desconhecida_e_recusada(self) -> None:
        for argumentos in ([], ["requested", "sobra"], ["pedido"]):
            with (
                self.subTest(argumentos),
                self.assertRaisesRegex(ValueError, "Unexpected metadata operation"),
            ):
                self.imprimir(self.ambiente("stillon"), argumentos)


class ProcessoRealTests(RequestedTestCase):
    """O send.sh só enxerga o stdout e o status de saída do processo."""

    def rodar(self, ambiente: dict[str, str]) -> subprocess.CompletedProcess[str]:
        # Ambiente só com o dado aqui (sem PYTHONPATH): o send.py acha o receiver.py
        # pelo diretório do próprio script, como no send.sh.
        return subprocess.run(
            [sys.executable, str(SEND_PY), "requested"],
            env={**ambiente, "PATH": os.environ["PATH"]},
            capture_output=True,
            text=True,
            timeout=60,
            check=False,
        )

    def test_caso_feliz_sai_com_zero_e_so_o_tsv_no_stdout(self) -> None:
        feito = self.rodar(self.ambiente("stillon"))
        self.assertEqual(feito.returncode, 0)
        self.assertEqual(feito.stdout, "app\tghcr.io/bipoxdxd/stillon:" + SHA + "\n")
        self.assertEqual(feito.stderr, "")

    def test_entrada_invalida_sai_com_erro_e_stdout_vazio(self) -> None:
        feito = self.rodar(self.ambiente("stillon", "curto"))
        self.assertNotEqual(feito.returncode, 0)
        self.assertEqual(feito.stdout, "")
        self.assertIn("Invalid release commit", feito.stderr)

    def test_imagem_invalida_no_arquivo_nao_imprime_linha_parcial(self) -> None:
        imagens = goblin_imagens() | {"web": f"{GOBLIN_WEB}:latest"}
        escaneadas = self.arquivo_json("escaneadas.json", imagens)
        feito = self.rodar(self.ambiente("goblinshift", escaneadas=escaneadas))
        self.assertNotEqual(feito.returncode, 0)
        self.assertEqual(feito.stdout, "")


if __name__ == "__main__":
    unittest.main()
