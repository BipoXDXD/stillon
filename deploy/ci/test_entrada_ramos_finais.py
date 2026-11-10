"""Ramos finais do rollback e da cifra do stillon-entrada, com binários falsos.

Roda uma cópia do script real, com os caminhos trocados como o checar-vps.sh
faz. Sem rede, sem Docker e sem VPS: docker, age e as ferramentas que só
existem no Linux (flock, stat -c, tee -p, df --output, timeout) são falsos,
num PATH temporário. O docker falso guarda cada chamada para o teste afirmar
o que NÃO aconteceu.
"""
import io
import json
from pathlib import Path
import shlex
import shutil
import subprocess
import sys
import tarfile
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
ORIGINAL = ROOT / "deploy/vps/stillon-entrada"
# O bash do PATH de quem roda o teste: o script exige um recente (o /bin/bash
# do macOS é o 3.2), e o PATH do script é só o dos binários falsos.
BASH = shutil.which("bash")

SHA_NOVO = "b" * 40
SHA_NO_AR = "a" * 40
SHA_ANTERIOR = "c" * 40
ID_NOVO = "sha256:" + "1" * 64
ID_NO_AR = "sha256:" + "2" * 64
ID_DB = "sha256:" + "3" * 64
PEDIDO_DEPLOY = f"deploy {SHA_NOVO} {ID_NOVO}"
LOG_DO_APP = "CANARIO-LOG-DO-APP"
DUMP = "CANARIO-DUMP-EM-CLARO"
METADE_CIFRADA = b"age-encryption.org/v1 PELA-METADE"
FIM_ERRO_DEPLOY = (
    r"== \d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ fim: "
    + PEDIDO_DEPLOY
    + r": código 1 \(erro; veja as linhas acima\)"
)
FIM_ERRO_BACKUP = r"== \d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ fim: backup: código 1 \(erro; veja as linhas acima\)"

DOCKER_FALSO = r'''
import hashlib
import json
import os
from pathlib import Path
import sys

BANCADA = Path(__file__).resolve().parent.parent
CENARIO = json.loads((BANCADA / "cenario.json").read_text())
CAMINHO_ESTADO = BANCADA / "estado.json"
estado = json.loads(CAMINHO_ESTADO.read_text()) if CAMINHO_ESTADO.exists() else {"recursos": {}}


def achar_recurso(alvo):
    for cid, recurso in estado["recursos"].items():
        if alvo in (cid, recurso["nome"]):
            return cid, recurso
    return None, None


def info(args):
    print("/docker" if "DockerRootDir" in args[1] else "[[io.containerd.snapshotter.v1]]")
    return 0


def image(args):
    if args[0] != "inspect":
        return 99
    identificador = CENARIO["imagens"].get(args[-1])
    if identificador is None:
        print(f"Error: No such image: {args[-1]}", file=sys.stderr)
        return 1
    print(identificador)
    return 0


def load(_args):
    print(f"Loaded image ID: {CENARIO['id_carregado']}")
    return 0


def nada(_args):
    return 0


def compose(args):
    subcomando, resto = args[2], args[3:]
    if subcomando == "config":
        print(json.dumps({"name": "stillon"}) if "json" in resto else "app")
    elif subcomando == "up":
        sha = os.environ["STILLON_SHA"]
        estado["ultimo_up"] = sha
        return CENARIO["up"][sha]
    elif subcomando == "logs":
        print(CENARIO["log_do_app"])
    elif subcomando == "ps":
        print("ctr-db")
    else:
        return 99
    return 0


def ps(args):
    if "label=com.docker.compose.service=app" in args:
        print("ctr-app")
    return 0


def inspect(args):
    formato, alvo = args[1], args[-1]
    if alvo == "ctr-app":
        sha = estado["ultimo_up"]
        saude = CENARIO["saude"].get(sha, "healthy")
        print(f"running {saude} stillon:{sha} {CENARIO['ids_por_sha'][sha]}")
    elif alvo == "ctr-db":
        if "IPAddress" in formato:
            print("192.0.2.10")
        elif "Networks" in formato:
            print("rede-teste")
        else:
            print(CENARIO["id_db"])
    else:
        return inspecionar_recurso(formato, alvo)
    return 0


def inspecionar_recurso(formato, alvo):
    cid, recurso = achar_recurso(alvo)
    if cid is None:
        return 1
    if formato == "{{.Id}}":
        print(cid)
        return 0
    rotulos = recurso["rotulos"]
    imagem = rotulos["io.stillon.backup.imagem"]
    print(f"/{recurso['nome']} {rotulos['io.stillon.backup.raiz']} {rotulos['io.stillon.backup.tipo']} "
          f"{rotulos['io.stillon.backup.nonce']} {imagem} {imagem}")
    return 0


def criar_recurso(args):
    nome = args[args.index("--name") + 1]
    cid = hashlib.sha256(nome.encode()).hexdigest()
    rotulos = dict(args[i + 1].split("=", 1) for i, valor in enumerate(args) if valor == "--label")
    saida = next((a.split(":")[0] for a in args if a.endswith(":/saida")), None)
    estado["recursos"][cid] = {"nome": nome, "rotulos": rotulos, "saida": saida}
    Path(args[args.index("--cidfile") + 1]).write_text(cid + "\n")
    return 0


def copiar(args):
    origem, destino = args
    if origem.endswith(":/stillon"):
        Path(destino).write_bytes(b"\x7fELF-falso")
    return 0


def executar(args):
    if "--backup-snapshot-protocol" in args:
        print("stillon-backup-snapshot-v1")
    elif "/deploy/backup/backup.sh" in args:
        recurso = next(r for r in estado["recursos"].values() if r["nome"] in args)
        Path(recurso["saida"], "stillon.dump").write_text(CENARIO["dump"])
    return 0


def remover(args):
    cid, _recurso = achar_recurso(args[-1])
    if cid is None:
        return 1
    del estado["recursos"][cid]
    return 0


VERBOS = {"info": info, "image": image, "load": load, "tag": nada, "rmi": nada, "compose": compose, "ps": ps,
          "inspect": inspect, "create": criar_recurso, "run": criar_recurso, "cp": copiar, "exec": executar,
          "rm": remover}

verbo, *resto = sys.argv[1:]
with (BANCADA / "chamadas.jsonl").open("a") as chamadas:
    chamadas.write(json.dumps({"verbo": verbo, "args": resto, "sha": os.environ.get("STILLON_SHA")}) + "\n")
codigo = VERBOS[verbo](resto)
CAMINHO_ESTADO.write_text(json.dumps(estado))
sys.exit(codigo)
'''


def tar_oci():
    """O tar do deploy: layout OCI, sem nome, com o manifesto ID_NOVO."""
    index = {"schemaVersion": 2, "manifests": [{"mediaType": "application/vnd.oci.image.manifest.v1+json",
                                                "digest": ID_NOVO, "size": 2}]}
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode="w", format=tarfile.GNU_FORMAT) as tar:
        for nome in ("blobs", "blobs/sha256"):
            pasta = tarfile.TarInfo(nome)
            pasta.type = tarfile.DIRTYPE
            pasta.mode = 0o755
            tar.addfile(pasta)
        membros = {"oci-layout": b'{"imageLayoutVersion":"1.0.0"}', "index.json": json.dumps(index).encode(),
                   "blobs/sha256/" + "4" * 64: b"{}"}
        for nome, conteudo in membros.items():
            arquivo = tarfile.TarInfo(nome)
            arquivo.size = len(conteudo)
            arquivo.mode = 0o644
            tar.addfile(arquivo, io.BytesIO(conteudo))
    return buffer.getvalue()


class Bancada(unittest.TestCase):
    def setUp(self):
        self.raiz = Path(tempfile.mkdtemp(prefix="stillon-entrada-")).resolve()
        self.addCleanup(shutil.rmtree, self.raiz)
        self.dir = self.raiz / "opt"
        self.shm = self.raiz / "shm"
        self.bin = self.raiz / "bin"
        for pasta in (self.dir, self.shm, self.bin):
            pasta.mkdir()
        (self.dir / "versao").write_text(SHA_NO_AR + "\n")
        (self.dir / "versao-anterior").write_text(SHA_ANTERIOR + "\n")
        (self.dir / "historico").write_text(SHA_NO_AR + "\n")
        self.instalar_falsos()
        self.entrada = self.copiar_entrada()
        self.definir_cenario()
        self.definir_age(codigo=0, escreve=b"")
        self.definir_disco(10)

    def instalar_falsos(self):
        registro = shlex.quote(str(self.raiz))
        falsos = {
            "docker": f"#!{sys.executable}\n{DOCKER_FALSO}",
            "flock": "#!/bin/sh\nexit 0\n",
            "stat": '#!/bin/sh\n[ "$1" = -c ] && [ "$2" = %s ] || exit 97\nwc -c < "$3" | tr -d " "\n',
            "tee": '#!/bin/sh\n[ "$1" = -p ] && [ "$2" = -a ] || exit 97\nexec /usr/bin/tee -a "$3"\n',
            "timeout": '#!/bin/sh\n[ "$1" = -k ] || exit 97\nshift 3\nexec "$@"\n',
            "df": (f'#!/bin/sh\ncase "$1" in\n'
                   "    -B1) printf '1B-blocks\\n1000000\\n' ;;\n"
                   f'    --output=pcent) printf \'Use%%\\n%s%%\\n\' "$(cat {registro}/disco)" ;;\n'
                   "    *) exit 97 ;;\nesac\n"),
            "age": (f'#!/bin/sh\nprintf \'%s\\n\' "$@" > {registro}/age.args\n'
                    f"cat > {registro}/age.entrada\n"
                    f"cat {registro}/age.escreve\n"
                    f"exit \"$(cat {registro}/age.codigo)\"\n"),
        }
        for nome, conteudo in falsos.items():
            arquivo = self.bin / nome
            arquivo.write_text(conteudo)
            arquivo.chmod(0o755)

    def copiar_entrada(self):
        texto = ORIGINAL.read_text()
        trocas = {
            "export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin": f"export PATH={self.bin}:/usr/bin:/bin",
            "DIR=/opt/stillon": f"DIR={self.dir}",
            "SHM_BASE=/dev/shm": f"SHM_BASE={self.shm}",
            "ESTADO_COOLIFY=/var/lib/coolify-release/stillon": f"ESTADO_COOLIFY={self.raiz}/coolify-ausente",
        }
        linhas = texto.split("\n")
        for de, para in trocas.items():
            # Exatamente uma linha, ou o teste rodaria contra o /opt de verdade.
            if linhas.count(de) != 1:
                raise RuntimeError(f"a linha {de!r} não está (uma vez) no stillon-entrada")
            linhas[linhas.index(de)] = para
        copia = self.raiz / "stillon-entrada"
        copia.write_text("\n".join(linhas))
        return copia

    def definir_cenario(self, up=None, imagens=None, saude=None):
        cenario = {
            "imagens": {f"stillon:{SHA_NO_AR}": ID_NO_AR} if imagens is None else imagens,
            "up": up or {SHA_NOVO: 1, SHA_NO_AR: 0},
            "saude": saude or {},
            "ids_por_sha": {SHA_NOVO: ID_NOVO, SHA_NO_AR: ID_NO_AR},
            "id_carregado": ID_NOVO,
            "id_db": ID_DB,
            "log_do_app": LOG_DO_APP,
            "dump": DUMP,
        }
        (self.raiz / "cenario.json").write_text(json.dumps(cenario))

    def definir_age(self, codigo, escreve):
        (self.raiz / "age.codigo").write_text(str(codigo))
        (self.raiz / "age.escreve").write_bytes(escreve)

    def definir_disco(self, porcento):
        (self.raiz / "disco").write_text(str(porcento))

    def rodar(self, pedido, entrada=b""):
        ambiente = {"PATH": f"{self.bin}:/usr/bin:/bin", "HOME": str(self.raiz), "SSH_ORIGINAL_COMMAND": pedido}
        return subprocess.run([BASH, str(self.entrada)], input=entrada, capture_output=True, env=ambiente,
                              timeout=60, check=False)

    def chamadas(self, verbo):
        caminho = self.raiz / "chamadas.jsonl"
        registros = [json.loads(linha) for linha in caminho.read_text().splitlines()]
        return [registro for registro in registros if registro["verbo"] == verbo]

    def shas_do_up(self):
        return [c["sha"] for c in self.chamadas("compose") if c["args"][2] == "up"]

    def log(self):
        return (self.dir / "entrada.log").read_text()

    def assert_fim_do_log(self, padrao):
        self.assertRegex(self.log().splitlines()[-1], "^" + padrao + "$")

    def assert_linha_na_saida_de_erro(self, resultado, linha):
        self.assertIn(linha, resultado.stderr.decode().splitlines())


class RamosFinaisDoRollback(Bancada):
    def assert_nada_avancou(self):
        """O app não ficou na versão nova, então nada pode dizer que ficou."""
        self.assertEqual((self.dir / "versao").read_text(), SHA_NO_AR + "\n")
        self.assertEqual((self.dir / "versao-anterior").read_text(), SHA_ANTERIOR + "\n")
        self.assertEqual((self.dir / "historico").read_text(), SHA_NO_AR + "\n")
        etiquetas = [c["args"] for c in self.chamadas("tag")]
        self.assertEqual(etiquetas, [[ID_NOVO, f"stillon:{SHA_NOVO}"]])
        self.assertEqual(self.chamadas("rmi"), [], "a imagem antiga não pode sair num deploy que falhou")

    def assert_nao_alega_rollback(self, resultado):
        self.assertNotIn("rollback feito", resultado.stderr.decode())
        self.assertNotIn("rollback feito", self.log())
        self.assertEqual(resultado.stdout, b"")

    def test_imagem_anterior_sumiu_sai_1_sem_tentar_o_rollback(self):
        self.definir_cenario(up={SHA_NOVO: 1, SHA_NO_AR: 0}, imagens={})

        resultado = self.rodar(PEDIDO_DEPLOY, tar_oci())

        self.assertEqual(resultado.returncode, 1, resultado.stderr.decode())
        self.assert_linha_na_saida_de_erro(
            resultado,
            f"stillon-entrada: a imagem de {SHA_NO_AR} (stillon:{SHA_NO_AR}) sumiu: "
            "o rollback não foi tentado; veja docs/deploy/vps.md",
        )
        self.assertEqual(self.shas_do_up(), [SHA_NOVO])
        self.assert_nada_avancou()
        self.assert_nao_alega_rollback(resultado)
        self.assert_fim_do_log(FIM_ERRO_DEPLOY)

    def assert_rollback_tambem_falhou(self, resultado, guardados_do_app):
        self.assertEqual(resultado.returncode, 1, resultado.stderr.decode())
        self.assert_linha_na_saida_de_erro(
            resultado,
            f"stillon-entrada: o rollback para {SHA_NO_AR} também não ficou saudável: veja docs/deploy/vps.md",
        )
        self.assertEqual(self.shas_do_up(), [SHA_NOVO, SHA_NO_AR])
        self.assert_nada_avancou()
        self.assert_nao_alega_rollback(resultado)
        self.assert_fim_do_log(FIM_ERRO_DEPLOY)
        self.assertEqual(self.log().count("== log do app"), guardados_do_app)
        self.assertEqual(self.log().count(LOG_DO_APP), guardados_do_app, "o log do app tem de ficar no entrada.log")
        self.assertNotIn(LOG_DO_APP, resultado.stderr.decode(), "o log do app não pode ir ao log público do job")

    def test_up_falha_nas_duas_vezes_sai_1(self):
        self.definir_cenario(up={SHA_NOVO: 1, SHA_NO_AR: 1})

        resultado = self.rodar(PEDIDO_DEPLOY, tar_oci())

        self.assert_rollback_tambem_falhou(resultado, guardados_do_app=2)

    def test_rollback_que_sobe_doente_sai_1_mesmo_com_o_up_dizendo_0(self):
        self.definir_cenario(up={SHA_NOVO: 1, SHA_NO_AR: 0}, saude={SHA_NO_AR: "unhealthy"})

        resultado = self.rodar(PEDIDO_DEPLOY, tar_oci())

        self.assert_rollback_tambem_falhou(resultado, guardados_do_app=2)
        self.assert_linha_na_saida_de_erro(resultado, "o app está unhealthy")

    def test_controle_rollback_saudavel_sai_4(self):
        # Com a mesma bancada, o ramo vizinho: prova que os casos acima param
        # por causa do cenário, e não porque o falso sempre falha.
        self.definir_cenario(up={SHA_NOVO: 1, SHA_NO_AR: 0})

        resultado = self.rodar(PEDIDO_DEPLOY, tar_oci())

        self.assertEqual(resultado.returncode, 4, resultado.stderr.decode())
        self.assert_linha_na_saida_de_erro(
            resultado, f"stillon-entrada: rollback feito: {SHA_NO_AR} de volta no ar; {SHA_NOVO} não subiu"
        )
        self.assertEqual(self.shas_do_up(), [SHA_NOVO, SHA_NO_AR])
        self.assert_nada_avancou()


class RamoFinalDaCifra(Bancada):
    def setUp(self):
        super().setUp()
        self.criar_arquivos_do_backup()
        self.definir_cenario(imagens={f"stillon:{SHA_NO_AR}": ID_NO_AR})

    def criar_arquivos_do_backup(self):
        (self.dir / "age-recipient").write_text("age1chavepublicafalsa\n")
        (self.dir / ".env").write_text("STILLON_BACKUP_PASSWORD=senhafalsa\n")
        scripts = self.dir / "deploy"
        (scripts / "backup").mkdir(parents=True)
        (scripts / "compose").mkdir()
        for relativo in ("backup/backup.sh", "backup/verificar.sql", "compose/initdb.sql"):
            (scripts / relativo).write_text("")

    def assert_age_recebeu_o_dump(self):
        argumentos = (self.raiz / "age.args").read_text().splitlines()
        self.assertEqual(argumentos, ["-R", f"{self.dir}/age-recipient"])
        self.assertEqual((self.raiz / "age.entrada").read_text(), DUMP)

    def assert_nada_em_claro_sobrou(self, resultado):
        self.assertEqual(list(self.shm.iterdir()), [], "o dump em claro não pode sobrar no /dev/shm")
        for texto in (resultado.stdout.decode(), resultado.stderr.decode(), self.log()):
            self.assertNotIn(DUMP, texto)
        estado = json.loads((self.raiz / "estado.json").read_text())
        self.assertEqual(estado["recursos"], {}, "o descartável do backup não pode ficar de pé")

    def assert_age_falhou(self, resultado):
        self.assertEqual(resultado.returncode, 1, resultado.stderr.decode())
        self.assert_linha_na_saida_de_erro(resultado, "stillon-entrada: o age falhou: o backup cifrado está incompleto")
        self.assertNotIn("backup cifrado enviado", resultado.stderr.decode())
        self.assertNotIn("backup cifrado enviado", self.log())
        self.assert_fim_do_log(FIM_ERRO_BACKUP)
        self.assert_age_recebeu_o_dump()
        self.assert_nada_em_claro_sobrou(resultado)

    def test_age_falha_sem_escrever_sai_1_e_nada_vai_na_saida(self):
        self.definir_age(codigo=1, escreve=b"")

        resultado = self.rodar("backup")

        self.assert_age_falhou(resultado)
        self.assertEqual(resultado.stdout, b"")

    def test_age_falha_depois_de_meio_cifrado_sai_1_sem_acrescentar_nada(self):
        self.definir_age(codigo=1, escreve=METADE_CIFRADA)

        resultado = self.rodar("backup")

        self.assert_age_falhou(resultado)
        self.assertEqual(resultado.stdout, METADE_CIFRADA)

    def test_age_falha_com_disco_cheio_sai_1_e_nao_o_4_de_enviado(self):
        # O backup.yml guarda o arquivo no código 4: aqui ele estaria incompleto.
        self.definir_age(codigo=1, escreve=METADE_CIFRADA)
        self.definir_disco(85)

        resultado = self.rodar("backup")

        self.assert_age_falhou(resultado)
        self.assert_linha_na_saida_de_erro(
            resultado, "ERRO: o disco da VPS passou de 85%: libere espaço (docs/deploy/vps.md, disco)"
        )
        self.assertEqual(resultado.stdout, METADE_CIFRADA)

    def test_controle_age_que_funciona_sai_0(self):
        self.definir_age(codigo=0, escreve=b"CIFRADO-INTEIRO")

        resultado = self.rodar("backup")

        self.assertEqual(resultado.returncode, 0, resultado.stderr.decode())
        self.assertEqual(resultado.stdout, b"CIFRADO-INTEIRO")
        self.assert_linha_na_saida_de_erro(resultado, "backup cifrado enviado (código 0)")
        self.assertNotIn("o age falhou", resultado.stderr.decode())
        self.assert_age_recebeu_o_dump()
        self.assert_nada_em_claro_sobrou(resultado)


if __name__ == "__main__":
    unittest.main()
