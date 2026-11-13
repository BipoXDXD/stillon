"""O checar-conteineres.sh reprova limpeza de contêiner por diff, nome, padrão ou prune."""

import pathlib
import shutil
import subprocess
import tempfile
import unittest

RAIZ = pathlib.Path(__file__).resolve().parents[2]
SCRIPT = pathlib.Path("deploy/ci/checar-conteineres.sh")
SONDA_SH = pathlib.Path("deploy/ci/zz-sonda.sh")
SONDA_PY = pathlib.Path("deploy/ci/zz_sonda.py")
SONDA_MAKE = pathlib.Path("Makefile")
SONDA_SHEBANG = pathlib.Path("deploy/vps/zz-sonda")

DIFF = "escolha de contêiner por diff de docker ps"
PRUNE = "prune remove contêineres"
LISTAGEM = "docker rm sobre uma listagem"
NOME = "docker rm por nome"
LISTA_NOME = "lista contêineres por nome, imagem ou grep"

SCRIPTS_RUINS = (
    (
        "foto antes e depois em arquivo, comm e rm",
        "docker ps -q > antes.txt\ngo test ./...\ndocker ps -q > depois.txt\n"
        'for id in $(comm -13 antes.txt depois.txt); do docker rm -f "$id"; done\n',
        DIFF,
    ),
    (
        "foto em variável e comm",
        'antes=$(docker ps -q)\ngo test ./...\nnovos=$(comm -13 <(echo "$antes") <(sort))\n'
        "docker rm -f $novos\n",
        DIFF,
    ),
    ("docker ps com grep -v na mesma linha", "docker ps -q | grep -v \"$meu\" > alheios\n", DIFF),
    ("comm com docker ps na mesma linha", "comm -13 antes <(docker ps -q | sort)\n", DIFF),
    ("container prune", "docker container prune -f\n", PRUNE),
    ("system prune", "docker system prune -af\n", PRUNE),
    ("rm por nome literal", "docker rm -f stillon-pg\n", NOME),
    ("container rm por nome literal", "docker container rm stillon-pg >/dev/null\n", NOME),
    ("rm sobre docker ps por nome", "docker rm -f $(docker ps -aq --filter name=stillon-)\n", LISTAGEM),
    ("rm por xargs", "docker ps -aq --filter label=x | xargs docker rm -f\n", LISTAGEM),
    (
        "laço sobre docker ps filtrado por nome",
        'for c in $(docker ps -aq --filter "name=stillon"); do\n    docker rm -f "$c"\ndone\n',
        LISTA_NOME,
    ),
    (
        "laço sobre docker ps com grep",
        "for c in $(docker ps -aq | grep abc); do\n    docker rm -f \"$c\"\ndone\n",
        LISTA_NOME,
    ),
)

SCRIPTS_BONS = (
    (
        "id vindo do docker run -d",
        'cid=$(docker run -d --label "stillon.run=$EXECUCAO" postgres)\n'
        'docker rm -f "$cid" >/dev/null 2>&1 || true\n',
    ),
    (
        "limpeza pela label da execução",
        'docker rm -f $(docker ps -aq --filter "label=stillon.run=$EXECUCAO")\n',
    ),
    ("comentário que cita o proibido", "# nunca: docker system prune nem docker rm -f stillon-pg\n"),
    ("rm de imagem e de rede por nome", "docker image rm stillon:dev\ndocker network rm edge\n"),
    ("diff sem foto de docker ps", 'docker rm -f "$cid"\ndiff <(echo "$a") <(echo "$b")\n'),
)


class ChecarConteineresTest(unittest.TestCase):
    """Cada caso roda numa cópia mínima: o script, o caso e um git init."""

    def setUp(self):
        self.pasta = tempfile.TemporaryDirectory()
        self.addCleanup(self.pasta.cleanup)
        self.copia = pathlib.Path(self.pasta.name)
        (self.copia / SCRIPT).parent.mkdir(parents=True)
        shutil.copy2(RAIZ / SCRIPT, self.copia / SCRIPT)
        subprocess.run(["git", "init", "-q"], cwd=self.copia, check=True)

    def escrever(self, caminho, texto):
        destino = self.copia / caminho
        destino.parent.mkdir(parents=True, exist_ok=True)
        destino.write_text(texto)

    def checar(self, cwd=None):
        return subprocess.run(
            ["sh", str(SCRIPT)], cwd=cwd or self.copia, capture_output=True, text=True
        )

    def test_repositorio_real_passa(self):
        resultado = self.checar(cwd=RAIZ)
        self.assertEqual(resultado.returncode, 0, resultado.stderr)

    def test_script_ruim_reprova(self):
        for nome, corpo, mensagem in SCRIPTS_RUINS:
            with self.subTest(nome):
                self.escrever(SONDA_SH, "#!/bin/sh\n" + corpo)
                resultado = self.checar()
                self.assertNotEqual(resultado.returncode, 0, resultado.stderr)
                self.assertIn(mensagem, resultado.stderr)
                self.assertIn(str(SONDA_SH), resultado.stderr)

    def test_script_bom_passa(self):
        for nome, corpo in SCRIPTS_BONS:
            with self.subTest(nome):
                self.escrever(SONDA_SH, "#!/bin/sh\n" + corpo)
                resultado = self.checar()
                self.assertEqual(resultado.returncode, 0, resultado.stderr)

    def test_makefile_e_arquivo_com_shebang_entram_na_busca(self):
        for caminho, texto in (
            (SONDA_MAKE, "limpar:\n\tdocker system prune -af\n"),
            (SONDA_SHEBANG, "#!/bin/bash\ndocker rm -f stillon-pg\n"),
        ):
            with self.subTest(str(caminho)):
                self.escrever(caminho, texto)
                resultado = self.checar()
                self.assertNotEqual(resultado.returncode, 0, resultado.stderr)
                self.assertIn(str(caminho), resultado.stderr)
                (self.copia / caminho).unlink()

    def test_python_com_prune_reprova(self):
        self.escrever(SONDA_PY, 'subprocess.run(["docker", "container", "prune", "-f"])\n')
        resultado = self.checar()
        self.assertNotEqual(resultado.returncode, 0, resultado.stderr)
        self.assertIn(PRUNE, resultado.stderr)

    def test_linha_marcada_como_permitida_passa(self):
        self.escrever(
            SONDA_PY,
            '["docker", "system", "prune"],  # checar-conteineres: permitido, a guarda recusa\n',
        )
        resultado = self.checar()
        self.assertEqual(resultado.returncode, 0, resultado.stderr)


if __name__ == "__main__":
    unittest.main()
