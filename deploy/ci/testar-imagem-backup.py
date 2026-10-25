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


class ImagemBackup(unittest.TestCase):
    def setUp(self):
        # Em CI sudo usa metadados reais sob /run, sem /tmp gravável na ancestralidade.
        self.directory = Path(tempfile.mkdtemp(prefix="stillon-helper-", dir="/run" if os.geteuid() == 0 else None)).resolve()
        self.addCleanup(shutil.rmtree, self.directory)
        self.state = self.directory / "state"
        self.state.mkdir(mode=0o700)
        (self.directory / "versao").write_text(SHA + "\n")
        self.receipt = {"release": {"version": 2, "receiver_sha256": "d" * 64,
                                   "project": "stillon", "commit": SHA, "images": {"app": REFERENCE}},
                        "deployment_uuid": "fixture", "finished_at": 1}
        self.env = {**os.environ, "FIXTURE": str(self.directory), "LEGACY": "0", "REVISION": SHA,
                    "SOURCE": SOURCE, "PRESENT": "1", "BAD_METADATA": "", "LEGACY_ID": IMAGE}
        self.bin = self.directory / "bin"
        self.bin.mkdir()
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
        # Só a fronteira de metadados root é modelada no Mac. CI roda este teste
        # como root e usa stat GNU real, inclusive nas recusas de modo/symlink.
        metadata = ""
        if os.geteuid() != 0:
            metadata = '''stat() {
 if [ "$2" = '%u %a %h %s' ]; then
  case "$BAD_METADATA" in owner) echo '1000 700 1 100';; mode) echo '0 777 1 100';; links) echo '0 600 2 100';; size) echo '0 600 1 262145';; *) echo '0 700 1 100';; esac
 else command stat "$@"; fi
}
'''
        script = "set -euo pipefail\nsair() { exit \"$1\"; }\n" + metadata + "\n".join(functions) + '''
DIR=$FIXTURE
ESTADO_COOLIFY=$FIXTURE/state
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
        self.env["LEGACY"] = "1"
        target = self.directory / "target"
        target.write_text(json.dumps(self.receipt))
        (self.state / "current.json").symlink_to(target)
        self.assertIsNone(self.invoke(receipt=False))

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

    @unittest.skipUnless(os.geteuid() == 0, "ancestralidade exige Linux root real; não modelar no Mac")
    def test_ancestral_symlink_nao_faz_fallback(self):
        target = self.directory / "state-real"
        self.state.rename(target)
        self.state.symlink_to(target, target_is_directory=True)
        self.assert_recusa_sem_fallback()

    @unittest.skipUnless(os.geteuid() == 0, "ancestralidade exige Linux root real; não modelar no Mac")
    def test_ancestral_gravavel_nao_faz_fallback(self):
        self.state.chmod(0o770)
        self.assert_recusa_sem_fallback()

    @unittest.skipUnless(os.geteuid() == 0, "ancestralidade exige Linux root real; não modelar no Mac")
    def test_ancestral_de_outro_dono_nao_faz_fallback(self):
        os.chown(self.state, 1000, 1000)
        self.addCleanup(os.chown, self.state, 0, 0)
        self.assert_recusa_sem_fallback()


if __name__ == "__main__":
    unittest.main()
