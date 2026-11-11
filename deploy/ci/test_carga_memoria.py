"""carga.py da medição de memória, contra um servidor HTTP falso local.

Sem Docker: um ThreadingHTTPServer em 127.0.0.1 faz o papel do app, e as
constantes de tempo do carga.py são encurtadas no módulo importado. A carga só
pode sair 0 se todo cadastro receber 201, se nenhuma resposta for 5xx e se o
app responder até o fim.
"""
from pathlib import Path
import contextlib
import http.server
import importlib.util
import io
import json
import sys
import tempfile
import threading
import unittest
from unittest import mock


ROOT = Path(__file__).resolve().parents[2]
CARGA = ROOT / "deploy/ci/memoria/carga.py"
ALVOS = 3


def carregar_carga():
    spec = importlib.util.spec_from_file_location("carga_em_teste", CARGA)
    modulo = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(modulo)
    return modulo


class AppFalso(http.server.BaseHTTPRequestHandler):
    """Responde como o app; o comportamento vem dos atributos do servidor."""

    def log_message(self, *_):
        pass

    def responder(self, status, corpo=b"{}"):
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(corpo)))
        self.end_headers()
        self.wfile.write(corpo)

    def do_POST(self):
        tamanho = int(self.headers.get("Content-Length", 0))
        corpo = json.loads(self.rfile.read(tamanho) or b"{}")
        if self.path == "/api/v1/auth/login":
            if corpo.get("email") == "demo@stillon.local":
                self.responder(200, json.dumps({"access_token": "t"}).encode())
            else:
                self.responder(401)
        elif self.path == "/api/v1/targets":
            self.responder(self.server.status_cadastro)
        else:
            self.responder(404)

    def do_GET(self):
        if self.server.cair_no_resumo:
            # O app some: derruba a conexão sem resposta e para de aceitar novas.
            self.close_connection = True
            threading.Thread(target=self.server.desligar, daemon=True).start()
            return
        self.responder(self.server.status_resumo)


class ServidorFalso(http.server.ThreadingHTTPServer):
    daemon_threads = True
    # A fila padrão do listen (5) estoura com a rajada de logins e o macOS
    # responde RST: seria falha do servidor falso, não do app.
    request_queue_size = 64

    def __init__(self):
        super().__init__(("127.0.0.1", 0), AppFalso)
        self.status_cadastro = 201
        self.status_resumo = 200
        self.cair_no_resumo = False
        self._desligado = threading.Event()

    def desligar(self):
        if not self._desligado.is_set():
            self._desligado.set()
            self.shutdown()
            self.server_close()


class Carga(unittest.TestCase):
    def setUp(self):
        self.servidor = ServidorFalso()
        threading.Thread(target=self.servidor.serve_forever, daemon=True).start()
        self.addCleanup(self.servidor.desligar)
        self.carga = carregar_carga()
        porta = self.servidor.server_address[1]
        for nome, valor in (("BASE", f"http://127.0.0.1:{porta}"), ("ALVOS", ALVOS),
                            ("ESPERA_INICIAL_S", 0), ("DURACAO_S", 0.3),
                            ("PAUSA_DA_API_S", 0.01), ("PAUSA_ENTRE_RAJADAS_S", 0.05)):
            self.assertTrue(hasattr(self.carga, nome), nome)
            setattr(self.carga, nome, valor)
        self.arquivo = Path(tempfile.mkdtemp(prefix="carga-")) / "carga.txt"

    def rodar(self):
        erro = io.StringIO()
        with mock.patch.object(sys, "argv", ["carga.py", str(self.arquivo)]), \
                contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(erro):
            try:
                codigo = self.carga.main()
            except SystemExit as saida:
                codigo = saida.code
        # Como no interpretador: main() sem retorno ou exit(None) é saída 0.
        return 0 if codigo is None else codigo, erro.getvalue()

    def assertFalhou(self, motivo):
        codigo, erro = self.rodar()
        self.assertNotEqual(codigo, 0, erro)
        self.assertIn(motivo, erro)

    def test_caminho_feliz_sai_zero(self):
        codigo, erro = self.rodar()
        self.assertEqual(codigo, 0, erro)
        self.assertIn(f"cadastro 201: {ALVOS}", self.arquivo.read_text())

    def test_cadastro_sem_201_falha(self):
        self.servidor.status_cadastro = 400
        self.assertFalhou(f"cadastro 201 em 0 de {ALVOS}")

    def test_qualquer_5xx_falha(self):
        self.servidor.status_resumo = 500
        self.assertFalhou("5xx: resumo 500")

    def test_app_que_some_no_meio_falha(self):
        self.servidor.cair_no_resumo = True
        self.assertFalhou("sem resposta: ")

    def test_thread_que_morre_falha(self):
        def ler_resumo_quebrado(*_):
            raise RuntimeError("quebrou")
        self.carga.ler_resumo = ler_resumo_quebrado
        self.assertFalhou("thread ler_resumo_quebrado morreu")


if __name__ == "__main__":
    unittest.main()
