"""coletar.py da medição de memória, contra um docker falso e um cgroup falso.

Sem Docker e sem runner: o docker e o pkill do PATH são falsos, e o cgroup é
uma pasta temporária apontada por CGROUP_RAIZ. A medição só pode sair verde
com evidência: carga concluída, amostras de cada contêiner e o cgroup lido.
"""
from pathlib import Path
import json
import os
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
COLETAR = ROOT / "deploy/ci/memoria/coletar.py"
SERVICOS = ("app", "caddy", "db")

DOCKER_FALSO = r'''#!/usr/bin/env python3
import json
import sys

args = sys.argv[1:]
if args[:2] == ["compose", "ps"]:
    print("id-" + args[-1])
elif args[:1] == ["inspect"]:
    servico = args[1].removeprefix("id-")
    print(json.dumps([{
        "Name": "/stillon-" + servico + "-1",
        "State": {"OOMKilled": False, "Status": "running"},
        "Config": {"Env": ["GOMEMLIMIT=384MiB"]},
        "HostConfig": {"Memory": 536870912},
        "RestartCount": 0,
    }]))
elif args[:2] == ["compose", "exec"]:
    print("42")
'''


class Coleta(unittest.TestCase):
    def setUp(self):
        self.pasta = Path(tempfile.mkdtemp(prefix="coletar-"))
        bin_falso = self.pasta / "bin"
        bin_falso.mkdir()
        for nome, corpo in (("docker", DOCKER_FALSO), ("pkill", "#!/bin/sh\nexit 1\n")):
            caminho = bin_falso / nome
            caminho.write_text(corpo)
            caminho.chmod(0o755)
        self.path = f"{bin_falso}{os.pathsep}{os.environ['PATH']}"
        self.amostras = self.pasta / "amostras.txt"
        self.amostras.write_text("".join(
            f"1700000000 stillon-{s}-1 100.5MiB\n" for s in SERVICOS))
        self.carga = self.pasta / "carga.txt"
        self.carga.write_text("cadastro 201: 95\n")
        self.cgroup = self.pasta / "cgroup"
        for s in SERVICOS:
            escopo = self.cgroup / "system.slice" / f"docker-id-{s}.scope"
            escopo.mkdir(parents=True)
            (escopo / "memory.peak").write_text("209715200\n")
            (escopo / "memory.events").write_text("low 0\nhigh 0\nmax 0\noom 0\noom_kill 0\n")

    def coletar(self, playwright="success", carga="success"):
        env = {k: v for k, v in os.environ.items() if k != "GITHUB_STEP_SUMMARY"}
        env.update(PATH=self.path, CGROUP_RAIZ=str(self.cgroup))
        return subprocess.run(
            [sys.executable, "-I", str(COLETAR), str(self.amostras), str(self.carga), playwright, carga],
            capture_output=True, text=True, env=env, cwd=self.pasta, check=False)

    def assertVermelho(self, resultado, motivo):
        self.assertEqual(resultado.returncode, 1, resultado.stdout + resultado.stderr)
        self.assertIn(motivo, resultado.stdout)

    def test_com_toda_a_evidencia_sai_verde(self):
        resultado = self.coletar()
        self.assertEqual(resultado.returncode, 0, resultado.stdout + resultado.stderr)
        self.assertIn("| app | 512.0 | 384MiB | 100.5 | 1 | 200.0 | 0 | False | 0 | running |", resultado.stdout)

    def test_playwright_vermelho_sai_vermelho(self):
        self.assertVermelho(self.coletar(playwright="failure"), "Playwright: failure")

    def test_carga_que_falhou_sai_vermelho(self):
        self.assertVermelho(self.coletar(carga="failure"), "carga não concluiu: failure")

    def test_carga_sem_arquivo_de_resultado_sai_vermelho(self):
        self.carga.unlink()
        self.assertVermelho(self.coletar(), "carga não concluiu: sem resultado")

    def test_sem_amostras_sai_vermelho(self):
        self.amostras.write_text("")
        self.assertVermelho(self.coletar(), "sem amostras: app, caddy, db")

    def test_servico_sem_amostra_sai_vermelho(self):
        self.amostras.write_text("1700000000 stillon-app-1 100.5MiB\n")
        self.assertVermelho(self.coletar(), "sem amostras: caddy, db")

    def test_cgroup_ilegivel_sai_vermelho(self):
        for s in SERVICOS:
            (self.cgroup / "system.slice" / f"docker-id-{s}.scope" / "memory.peak").unlink()
        self.assertVermelho(self.coletar(), "cgroup ilegível: app, caddy, db")

    def test_memory_events_sem_oom_kill_sai_vermelho(self):
        (self.cgroup / "system.slice" / "docker-id-app.scope" / "memory.events").write_text("oom 0\n")
        self.assertVermelho(self.coletar(), "cgroup ilegível: app")

    def test_oom_kill_no_cgroup_sai_vermelho(self):
        (self.cgroup / "system.slice" / "docker-id-db.scope" / "memory.events").write_text("oom 1\noom_kill 1\n")
        resultado = self.coletar()
        self.assertEqual(resultado.returncode, 1, resultado.stdout)
        self.assertIn("| db | 512.0 | 384MiB | 100.5 | 1 | 200.0 | 1 | False | 0 | running |", resultado.stdout)


if __name__ == "__main__":
    unittest.main()
