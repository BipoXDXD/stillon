"""Seleção real do helper; Docker simulado, sem dump ou conexão com produção."""
import copy
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
SHA = "a" * 40
IMAGE = "sha256:" + "b" * 64
REFERENCE = "ghcr.io/bipoxdxd/stillon@sha256:" + "c" * 64
SOURCE = "https://github.com/BipoXDXD/stillon"
MODOS_INSEGUROS = {"grupo": 0o770, "outros": 0o702, "sticky": 0o1777}


def diretorio_executavel():
    """Cria um diretório temporário em que um script roda de verdade.

    Há runners com /run ou /tmp noexec, e a montagem não avisa ao criar o
    arquivo: só executar prova. Sem isso o docker falso não roda e toda recusa
    passa sem ter testado nada.
    """
    tentativas = []
    for base in [tempfile.gettempdir(), "/var/tmp", str(Path.home()), str(ROOT)]:
        try:
            directory = Path(tempfile.mkdtemp(prefix="stillon-helper-bin-", dir=base))
        except OSError as error:
            tentativas.append(f"{base}: {error}")
            continue
        probe = directory / "sonda"
        try:
            probe.write_text("#!/bin/sh\nexit 0\n")
            probe.chmod(0o755)
            subprocess.run([str(probe)], check=True)
        except (OSError, subprocess.CalledProcessError) as error:
            tentativas.append(f"{base}: {error}")
            shutil.rmtree(directory)
            continue
        probe.unlink()
        return directory.resolve()
    raise RuntimeError("nenhum diretório executável para o docker falso: " + "; ".join(tentativas))


class ImagemBackup(unittest.TestCase):
    def setUp(self):
        # Como root o stat é real. Sem root, ou para / (que o teste não pode
        # alterar), só a fronteira de metadados é modelada: o laço até / é o real.
        self.stat_modelado = os.geteuid() != 0
        # Em CI sudo usa metadados reais sob /run, sem /tmp gravável na ancestralidade.
        self.criar_fixture("/run" if os.geteuid() == 0 else None)
        self.receipt = {"release": {"version": 2, "receiver_sha256": "d" * 64,
                                   "project": "stillon", "commit": SHA, "images": {"app": REFERENCE}},
                        "deployment_uuid": "fixture", "finished_at": 1}
        self.bin = diretorio_executavel()
        self.addCleanup(shutil.rmtree, self.bin)
        docker = self.bin / "docker"
        docker.write_text("""#!/usr/bin/env python3
import json,os,sys
from pathlib import Path
a=sys.argv[1:];p=Path(os.environ['FIXTURE'])
with (p/'commands').open('a') as f:f.write(json.dumps(a)+'\\n')
if a[:2]==['image','inspect']:
 ref=a[-1]
 if ref=='stillon:'+'a'*40:
  if os.environ['LEGACY']!='1':sys.exit(1)
  print(os.environ['LEGACY_ID']);sys.exit(0)
 if ref!='ghcr.io/bipoxdxd/stillon@sha256:'+'c'*64 or os.environ['PRESENT']!='1':sys.exit(1)
 print('|'.join(['sha256:'+'b'*64,os.environ['REVISION'],os.environ['SOURCE']]));sys.exit(0)
if a and a[0]=='create':
 # Limite do teste: registra a imagem escolhida e para antes de criar recurso.
 (p/'selected').write_text(a[-2]);sys.exit(1)
sys.exit(97)
""")
        docker.chmod(0o755)

    def criar_fixture(self, parent):
        self.directory = Path(tempfile.mkdtemp(prefix="stillon-helper-", dir=parent)).resolve()
        self.addCleanup(shutil.rmtree, self.directory)
        self.state = self.directory / "state"
        self.state.mkdir(mode=0o700)
        (self.directory / "versao").write_text(SHA + "\n")
        self.env = {**os.environ, "FIXTURE": str(self.directory), "ESTADO": str(self.state),
                    "LEGACY": "0", "REVISION": SHA, "SOURCE": SOURCE, "PRESENT": "1",
                    "BAD_METADATA": "", "BAD_PATH": "", "LEGACY_ID": IMAGE}

    def tornar_inseguro(self, path, problema):
        if self.stat_modelado:
            self.env["BAD_PATH"] = str(path)
            self.env["BAD_METADATA"] = problema
        elif problema == "owner":
            os.chown(path, 1000, 1000)
        else:
            path.chmod(MODOS_INSEGUROS[problema])

    def invoke(self, receipt=True):
        for name in ["selected", "commands"]:
            (self.directory / name).unlink(missing_ok=True)
        if receipt:
            path = self.state / "current.json"
            path.write_text(json.dumps(self.receipt))
            path.chmod(0o600)
        source = (ROOT / "deploy/vps/stillon-entrada").read_text()
        functions = []
        for name in ["imagem_helper_aprovada", "preparar_helper_backup"]:
            match = re.search(r"^" + name + r"\(\) \{.*?^\}", source, re.M | re.S)
            if match:
                functions.append(match.group())
        # CI roda este teste como root e usa stat GNU real, inclusive nas recusas
        # de modo/symlink. BAD_PATH vazio aplica o problema a todo o caminho.
        metadata = ""
        if self.stat_modelado:
            metadata = '''stat() {
 if [ "$2" != '%u %a %h %s' ]; then command stat "$@"; return; fi
 if [ -n "$BAD_PATH" ] && [ "$3" != "$BAD_PATH" ]; then echo '0 700 1 100'; return; fi
 case "$BAD_METADATA" in owner) echo '1000 700 1 100';; mode) echo '0 777 1 100';; grupo) echo '0 770 2 4096';; outros) echo '0 702 2 4096';; sticky) echo '0 1777 2 4096';; links) echo '0 600 2 100';; size) echo '0 600 1 262145';; *) echo '0 700 1 100';; esac
}
'''
        script = "set -euo pipefail\nsair() { exit \"$1\"; }\n" + metadata + "\n".join(functions) + '''
DIR=$FIXTURE
ESTADO_COOLIFY=$ESTADO
IMAGEM=stillon
SHM_BASE=$FIXTURE
preparar_helper_backup
'''
        result = subprocess.run(["bash", "-c", script], env={**self.env, "PATH": str(self.bin) + os.pathsep + os.environ["PATH"]}, capture_output=True, text=True)
        self.assertEqual(result.returncode, 1, result.stderr)  # create é interrompido pelo teste.
        return (self.directory / "selected").read_text() if (self.directory / "selected").exists() else None

    def test_digest_aprovado_local_sem_alias_legado(self):
        self.assertEqual(self.invoke(), IMAGE)

    def test_legado_sem_recibo_continua_funcionando(self):
        self.env["LEGACY"] = "1"
        self.assertEqual(self.invoke(receipt=False), IMAGE)

    def test_recibo_prevalece_sobre_alias_legado_divergente(self):
        self.env["LEGACY"] = "1"
        self.env["LEGACY_ID"] = "sha256:" + "e" * 64
        self.assertEqual(self.invoke(), IMAGE)

    def test_recibo_invalido_nao_recorre_ao_alias(self):
        self.env["LEGACY"] = "1"
        self.receipt["release"]["commit"] = "e" * 40
        self.assertIsNone(self.invoke())

    def test_repositorio_imagem_estranho_recusado(self):
        self.receipt["release"]["images"]["app"] = "ghcr.io/terceiro/stillon@sha256:" + "c" * 64
        self.assert_recusa_sem_fallback()

    def test_projeto_estranho_recusado(self):
        self.receipt["release"]["project"] = "duora"
        self.assert_recusa_sem_fallback()

    def test_revision_divergente_recusada(self):
        self.env["REVISION"] = "e" * 40
        self.assert_recusa_sem_fallback()

    def test_source_divergente_recusado(self):
        self.env["SOURCE"] = "https://github.com/terceiro/stillon"
        self.assert_recusa_sem_fallback()

    def test_imagem_ausente_nao_faz_pull(self):
        self.env["PRESENT"] = "0"
        self.assert_recusa_sem_fallback()
        commands = [json.loads(line) for line in (self.directory / "commands").read_text().splitlines()]
        self.assertTrue(all(command[:2] == ["image", "inspect"] for command in commands))

    def test_recibo_symlink_recusado(self):
        def caso():
            self.env["LEGACY"] = "1"
            target = self.directory / "target"
            target.write_text(json.dumps(self.receipt))
            (self.state / "current.json").symlink_to(target)
            self.assertIsNone(self.invoke(receipt=False))
        self.em_cada_stat(caso)

    def test_recibo_gravavel_por_grupo_recusado(self):
        self.env["LEGACY"] = "1"
        target = self.state / "current.json"
        target.write_text(json.dumps(self.receipt))
        target.chmod(0o660)
        self.env["BAD_METADATA"] = "mode"
        self.assertIsNone(self.invoke(receipt=False))

    def test_recibo_hardlink_recusado(self):
        self.env["LEGACY"] = "1"
        target = self.state / "current.json"
        target.write_text(json.dumps(self.receipt))
        target.chmod(0o600)
        os.link(target, self.directory / "other")
        self.env["BAD_METADATA"] = "links"
        self.assertIsNone(self.invoke(receipt=False))

    def test_recibo_de_outro_dono_recusado(self):
        self.env["LEGACY"] = "1"
        target = self.state / "current.json"
        target.write_text(json.dumps(self.receipt))
        target.chmod(0o600)
        if os.geteuid() == 0:
            os.chown(target, 1000, 1000)
        self.env["BAD_METADATA"] = "owner"
        self.assertIsNone(self.invoke(receipt=False))

    def test_recibo_muito_grande_recusado(self):
        self.env["LEGACY"] = "1"
        target = self.state / "current.json"
        target.write_text(json.dumps(self.receipt) + " " * 262145)
        target.chmod(0o600)
        self.env["BAD_METADATA"] = "size"
        self.assertIsNone(self.invoke(receipt=False))

    def assert_recusa_sem_fallback(self, receipt=True):
        # Um alias válido continuaria funcionando: recusa não pode ser causada
        # pela ausência dele, nem chegar a create antes de perceber o recibo ruim.
        self.env["LEGACY"] = "1"
        self.assertIsNone(self.invoke(receipt=receipt))
        commands = self.directory / "commands"
        if commands.exists():
            for command in map(json.loads, commands.read_text().splitlines()):
                self.assertEqual(command[:2], ["image", "inspect"])
                self.assertNotEqual(command[-1], "stillon:" + SHA)

    def test_contrato_recibo_corrompido_nao_faz_fallback(self):
        original = copy.deepcopy(self.receipt)
        changes = [
            ("versao", lambda r: r["release"].update(version=1)),
            ("tipo_versao", lambda r: r["release"].update(version="2")),
            ("campo_extra", lambda r: r["release"].update(extra=True)),
            ("sem_receiver", lambda r: r["release"].pop("receiver_sha256")),
            ("hash_receiver", lambda r: r["release"].update(receiver_sha256="invalido")),
            ("tipo_receiver", lambda r: r["release"].update(receiver_sha256=12)),
            ("papel_extra", lambda r: r["release"]["images"].update(db=REFERENCE)),
            ("tipo_imagens", lambda r: r["release"].update(images=[])),
            ("tipo_app", lambda r: r["release"]["images"].update(app=None)),
            ("tag_mutavel", lambda r: r["release"]["images"].update(app="ghcr.io/bipoxdxd/stillon:latest")),
            ("digest_curto", lambda r: r["release"]["images"].update(app="ghcr.io/bipoxdxd/stillon@sha256:abc")),
            ("digest_upper", lambda r: r["release"]["images"].update(app="ghcr.io/bipoxdxd/stillon@sha256:" + "A" * 64)),
            ("tipo_release", lambda r: r.update(release=[])),
            ("sem_release", lambda r: r.pop("release")),
        ]
        for name, change in changes:
            with self.subTest(name=name):
                self.receipt = copy.deepcopy(original)
                change(self.receipt)
                self.assert_recusa_sem_fallback()

    def test_json_truncado_ou_vazio_nao_faz_fallback(self):
        for content in ["", '{"release":', "null", "[]"]:
            with self.subTest(content=content):
                target = self.state / "current.json"
                target.write_text(content)
                target.chmod(0o600)
                self.assert_recusa_sem_fallback(receipt=False)

    def test_labels_ausentes_nao_fazem_fallback(self):
        for name in ["REVISION", "SOURCE"]:
            with self.subTest(name=name):
                original = self.env[name]
                self.env[name] = ""
                self.assert_recusa_sem_fallback()
                self.env[name] = original

    def assert_inseguro_recusado(self, path, problema):
        # A mesma fixture é aceita antes: a recusa só pode vir do problema
        # aplicado, nunca do caminho do teste ou de um docker que não rodou.
        self.assertEqual(self.invoke(), IMAGE)
        self.tornar_inseguro(path, problema)
        self.assert_recusa_sem_fallback()

    def test_ancestral_inseguro_nao_faz_fallback(self):
        for alvo in ["state", "pai"]:
            for problema in [*MODOS_INSEGUROS, "owner"]:
                with self.subTest(alvo=alvo, problema=problema):
                    self.criar_fixture(self.directory.parent)
                    path = self.state if alvo == "state" else self.directory
                    self.assert_inseguro_recusado(path, problema)

    def test_raiz_insegura_nao_faz_fallback(self):
        self.stat_modelado = True
        for problema in [*MODOS_INSEGUROS, "owner"]:
            with self.subTest(problema=problema):
                self.criar_fixture(self.directory.parent)
                self.assert_inseguro_recusado(Path("/"), problema)

    def test_metadados_sob_tmp_recusados(self):
        self.assertEqual(self.invoke(), IMAGE)
        self.criar_fixture(Path("/tmp").resolve())
        if self.stat_modelado:
            self.tornar_inseguro(self.directory.parent, "sticky")
        self.assert_recusa_sem_fallback()

    def test_links_e_tamanho_so_valem_para_o_recibo(self):
        # Diretório real tem vários links e pode passar do teto do recibo.
        self.stat_modelado = True
        for problema in ["links", "size"]:
            with self.subTest(problema=problema):
                self.tornar_inseguro(self.state, problema)
                self.assertEqual(self.invoke(), IMAGE)

    def em_cada_stat(self, caso):
        # No Linux o lstat de um symlink dá modo 777, e a conferência de modo o
        # recusaria mesmo sem o teste de -L. Só o stat modelado isola esse teste.
        for modelado in [False, True] if os.geteuid() == 0 else [True]:
            with self.subTest(stat_modelado=modelado):
                self.stat_modelado = modelado
                self.criar_fixture(self.directory.parent)
                caso()

    def test_ancestral_symlink_nao_faz_fallback(self):
        def caso():
            self.assertEqual(self.invoke(), IMAGE)
            target = self.directory / "state-real"
            self.state.rename(target)
            self.state.symlink_to(target, target_is_directory=True)
            self.assert_recusa_sem_fallback()
        self.em_cada_stat(caso)

    def test_symlink_acima_do_estado_nao_faz_fallback(self):
        def caso():
            self.assertEqual(self.invoke(), IMAGE)
            link = self.directory / "ligacao"
            link.symlink_to(self.directory, target_is_directory=True)
            self.env["ESTADO"] = str(link / "state")
            self.assert_recusa_sem_fallback()
        self.em_cada_stat(caso)


if __name__ == "__main__":
    unittest.main()
