"""Roda o stillon-backup-entrada.sh de verdade, sem VPS, sem root e sem rede.

O script é o `command=` forçado da chave SSH do backup: só o comando `backup` passa, e só sem
publicação pendente. Os dois caminhos absolutos dele (`pending.json` e o `stillon-entrada`)
só existem na VPS, então os casos de execução rodam numa cópia com exatamente esses dois
caminhos trocados por caminhos de uma pasta temporária; o resto do script é o original. A
troca falha alto se o script mudar um deles, em vez de testar uma cópia que ninguém usa.
As recusas por comando inválido rodam no arquivo original, sem cópia nenhuma.
"""

import os
import shutil
import stat
import subprocess
import tempfile
import unittest
from dataclasses import dataclass
from pathlib import Path

SCRIPT = Path(__file__).resolve().parent / "stillon-backup-entrada.sh"
CAMINHO_PENDENTE = "/var/lib/coolify-release/stillon/pending.json"
CAMINHO_ENTRADA = "/usr/local/sbin/stillon-entrada"

RECUSA_COMANDO = 2
RECUSA_PENDENTE = 3
SAIDA_ENTRADA = 7
PRAZO_SEGUNDOS = 60

# O que o recibo do Coolify ou um cliente SSH curioso poderia mandar no lugar de `backup`.
COMANDOS_INVALIDOS = [
    "",
    "restore",
    "BACKUP",
    "backup ",
    " backup",
    "backup extra",
    "backup;id",
    "backup\n",
    "bash",
    "-c backup",
    "deploy",
]


@dataclass(frozen=True)
class Execucao:
    codigo: int
    stdout: str
    stderr: str


class BackupEntradaTests(unittest.TestCase):
    def setUp(self) -> None:
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.dir = Path(tmp.name)
        self.pendente = self.dir / "pending.json"
        self.entrada = self.dir / "stillon-entrada"
        self.registro = self.dir / "registro-entrada"
        self.pid = self.dir / "pid-entrada"
        self.copia = self.dir / "stillon-backup-entrada.sh"
        self.copia.write_text(self._script_com_caminhos_de_teste())
        self._instalar_entrada_falsa(SAIDA_ENTRADA)

    def _script_com_caminhos_de_teste(self) -> str:
        texto = SCRIPT.read_text()
        for caminho, novo in [
            (CAMINHO_PENDENTE, self.dir / "pending.json"),
            (CAMINHO_ENTRADA, self.dir / "stillon-entrada"),
        ]:
            self.assertEqual(
                texto.count(caminho),
                1,
                f"o script deve citar {caminho} exatamente uma vez; ajuste este teste se mudou",
            )
            texto = texto.replace(caminho, str(novo))
        return texto

    def _instalar_entrada_falsa(self, codigo: int) -> None:
        """Faz o papel do stillon-entrada: anota como foi chamado e devolve `codigo`."""
        self.entrada.write_text(
            "#!/bin/sh\n"
            f'printf "args=%s\\n" "$#" > "{self.registro}"\n'
            f'printf "pedido=%s\\n" "${{SSH_ORIGINAL_COMMAND:-}}" >> "{self.registro}"\n'
            f'printf "%s" "$$" > "{self.pid}"\n'
            'echo "saida-da-entrada"\n'
            'echo "erro-da-entrada" >&2\n'
            f"exit {codigo}\n"
        )
        self.entrada.chmod(self.entrada.stat().st_mode | stat.S_IXUSR)

    def _rodar(self, script: Path, comando: str | None) -> Execucao:
        ambiente = {"PATH": os.environ["PATH"]}
        if comando is not None:
            ambiente["SSH_ORIGINAL_COMMAND"] = comando
        feito = subprocess.run(
            [shutil.which("sh") or "/bin/sh", str(script)],
            capture_output=True,
            text=True,
            env=ambiente,
            timeout=PRAZO_SEGUNDOS,
            check=False,
        )
        return Execucao(feito.returncode, feito.stdout, feito.stderr)

    def test_comando_backup_sem_pendencia_chama_a_entrada_e_devolve_o_codigo_dela(self) -> None:
        feito = self._rodar(self.copia, "backup")

        self.assertEqual(feito.codigo, SAIDA_ENTRADA)
        self.assertEqual(feito.stdout, "saida-da-entrada\n")
        self.assertEqual(feito.stderr, "erro-da-entrada\n")

    def test_entrada_recebe_o_pedido_pelo_ambiente_e_nenhum_argumento(self) -> None:
        self.assertEqual(self._rodar(self.copia, "backup").codigo, SAIDA_ENTRADA)

        self.assertEqual(self.registro.read_text(), "args=0\npedido=backup\n")

    def test_entrada_substitui_o_processo_do_script_em_vez_de_virar_filha(self) -> None:
        ambiente = {"PATH": os.environ["PATH"], "SSH_ORIGINAL_COMMAND": "backup"}
        with subprocess.Popen(
            [shutil.which("sh") or "/bin/sh", str(self.copia)],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            env=ambiente,
        ) as processo:
            processo.wait(timeout=PRAZO_SEGUNDOS)

        self.assertEqual(self.pid.read_text(), str(processo.pid))

    def test_codigo_zero_da_entrada_vira_sucesso(self) -> None:
        self._instalar_entrada_falsa(0)

        self.assertEqual(self._rodar(self.copia, "backup").codigo, 0)

    def test_pendencia_recusa_o_backup_sem_chamar_a_entrada(self) -> None:
        self.pendente.write_text("{}")

        feito = self._rodar(self.copia, "backup")

        self.assertEqual(feito.codigo, RECUSA_PENDENTE)
        self.assertEqual(feito.stdout, "")
        self.assertEqual(
            feito.stderr, "Publicação pendente; conferir Coolify antes do backup\n"
        )
        self.assertFalse(self.registro.exists(), "a entrada não pode rodar com pendência")

    def test_pendencia_vazia_tambem_recusa(self) -> None:
        self.pendente.touch()

        self.assertEqual(self._rodar(self.copia, "backup").codigo, RECUSA_PENDENTE)
        self.assertFalse(self.registro.exists())

    def test_comando_invalido_e_recusado_no_script_original(self) -> None:
        for comando in COMANDOS_INVALIDOS:
            with self.subTest(comando=comando):
                feito = self._rodar(SCRIPT, comando)

                self.assertEqual(feito.codigo, RECUSA_COMANDO)
                self.assertEqual(feito.stdout, "")
                self.assertEqual(feito.stderr, "Acesso restrito ao backup\n")

    def test_comando_ausente_e_recusado_no_script_original(self) -> None:
        feito = self._rodar(SCRIPT, None)

        self.assertEqual(feito.codigo, RECUSA_COMANDO)
        self.assertEqual(feito.stdout, "")
        self.assertEqual(feito.stderr, "Acesso restrito ao backup\n")

    def test_comando_invalido_nao_chama_a_entrada_nem_com_o_caminho_liberado(self) -> None:
        for comando in [*COMANDOS_INVALIDOS, None]:
            with self.subTest(comando=comando):
                feito = self._rodar(self.copia, comando)

                self.assertEqual(feito.codigo, RECUSA_COMANDO)
                self.assertFalse(self.registro.exists())

    def test_comando_invalido_vale_mais_que_a_pendencia(self) -> None:
        self.pendente.write_text("{}")

        feito = self._rodar(self.copia, "restore")

        self.assertEqual(feito.codigo, RECUSA_COMANDO)
        self.assertEqual(feito.stderr, "Acesso restrito ao backup\n")


if __name__ == "__main__":
    unittest.main()
