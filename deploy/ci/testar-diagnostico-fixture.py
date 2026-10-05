#!/usr/bin/env python3
"""Testes reais de subprocessos; Docker falso apenas na fronteira da CLI."""
import json
import importlib.util
import io
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from unittest import mock


COLLECTOR = Path(__file__).with_name("diagnostico-fixture.py")
CID = "a" * 64
RYUK = "b" * 64
OLD = "c" * 64
SESSION = "d" * 64
SECRET = "SEGREDO_SENTINELA_NAO_PUBLICAR"
TRACE_BOOTSTRAP = r'''
import json,os,pathlib,runpy,sys,threading,trace
tracer=trace.Trace(count=True,trace=False)
workers={}
def worker_trace(frame,event,arg):
    ident=threading.get_ident()
    if ident not in workers: workers[ident]=trace.Trace(count=True,trace=False)
    return workers[ident].globaltrace(frame,event,arg)
sys.argv=sys.argv[1:]
threading.settrace(worker_trace)
try:
    tracer.runfunc(runpy.run_path,sys.argv[0],run_name="__main__")
finally:
    threading.settrace(None)
    def selected(value):
        return {str(line):count for (filename,line),count in value.results().counts.items()
                if pathlib.Path(filename).resolve()==pathlib.Path(sys.argv[0]).resolve()}
    counts={"main":selected(tracer),"threads":[selected(value) for value in workers.values()]}
    folder=pathlib.Path(os.environ["TRACE_COLLECTOR_DIR"])
    folder.mkdir(parents=True,exist_ok=True)
    (folder/(str(os.getpid())+".json")).write_text(json.dumps(counts))
'''
SPEC = importlib.util.spec_from_file_location("fixture_diagnostic", COLLECTOR)
DIAG = importlib.util.module_from_spec(SPEC)
if COLLECTOR.exists():
    # Mutantes/restauração não podem reutilizar pyc com mtime do mesmo segundo.
    exec(compile(COLLECTOR.read_text(), str(COLLECTOR), "exec"), DIAG.__dict__)

FAKE_DOCKER = r'''#!/usr/bin/env python3
import json, os, pathlib, sys, time
root = pathlib.Path(os.environ["FAKE_ROOT"])
args = sys.argv[1:]
with (root / "pids").open("a") as file:
    file.write(str(os.getpid()) + "\n")
with (root / "calls").open("a") as file:
    file.write(json.dumps(args) + "\n")
if args[:2] == ["context", "inspect"]:
    if (root/"slow_context").exists():
        (root/"startup_ready").touch()
        time.sleep(30)
    print("tcp://SEGREDO_SENTINELA_NAO_PUBLICAR" if (root/"remote").exists() else "unix:///var/run/docker.sock")
elif args[:2] == ["container", "ls"]:
    print("SEGREDO_SENTINELA_NAO_PUBLICAR" if (root/"bad_baseline").exists() else "c" * 64)
    if (root/"remove_cli").exists(): pathlib.Path(sys.argv[0]).unlink()
elif args[:1] == ["events"]:
    (root / "ready").touch()
    if (root/"events_end").exists(): sys.exit(1)
    while not (root / "trigger").exists():
        time.sleep(.01)
    for value in json.loads((root / "events").read_text()):
        if isinstance(value, dict) and "timeNano" in value:
            value["timeNano"] = time.time_ns()
        print(json.dumps(value) if isinstance(value, dict) else value, flush=True)
    (root / "emitted").touch()
    time.sleep(30)
elif args[:1] == ["inspect"]:
    if (root / "missing").exists():
        print("SEGREDO_SENTINELA_NAO_PUBLICAR", file=sys.stderr)
        sys.exit(1)
    if (root/"bad_inspect").exists():
        print("SEGREDO_SENTINELA_NAO_PUBLICAR")
        sys.exit(0)
    print(json.dumps({"id": args[-1], "status": "running", "running": True,
                     "exitCode": 0, "oomKilled": False, "autoRemove": True,
                     "created": "2026-10-05T00:00:00Z", "started": "2026-10-05T00:00:01Z",
                     "finished": "0001-01-01T00:00:00Z"}))
elif args[:1] == ["logs"]:
    for line in (root / "logs").read_text().splitlines():
        print(line, flush=True, file=sys.stderr)
    time.sleep(30)
else:
    sys.exit(2)
'''


def event(cid, action="create", **extra):
    attrs = {"org.testcontainers": "true", "org.testcontainers.lang": "go",
             "org.testcontainers.version": "0.44.0", "org.testcontainers.sessionId": SESSION,
             "image": "testcontainers/ryuk:0.14.0" if cid == RYUK else "postgres:18-alpine",
             "org.testcontainers.ryuk": "true" if cid == RYUK else "false",
             "unrelated_secret": SECRET}
    value = {"Type": "container", "Action": action, "timeNano": time.time_ns(),
             "Actor": {"ID": cid, "Attributes": attrs}}
    value.update(extra)
    return value


class WrapperTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.addCleanup(self.cleanup_docker_children)
        docker = self.root / "docker"
        docker.write_text(FAKE_DOCKER)
        docker.chmod(0o755)
        (self.root / "python3").symlink_to(sys.executable)
        self.env = dict(os.environ, FAKE_ROOT=str(self.root), PATH=str(self.root))
        (self.root / "events").write_text(json.dumps([event(OLD), event(CID), event(RYUK), event(RYUK, "start"),
                                                     event(CID, "oom"), event(CID, "destroy"),
                                                     SECRET, {"Actor": SECRET}]))
        (self.root / "logs").write_text(
            '2026-10-05T00:00:01Z time=1 level=INFO msg="client disconnected" clients=0\n'
            f'2026-10-05T00:00:02Z time=2 level=DEBUG msg=remove resource=container id={CID} attempt=1\n'
            f'2026-10-05T00:00:03Z msg="{SECRET}\n'
            f'SQL password={SECRET}\n')

    def command(self, code, owned=True):
        target = self.root / "target.py"
        target.write_text('import os,pathlib;pathlib.Path(os.environ["FAKE_ROOT"]+"/target_pid").write_text(str(os.getpid()))\n' + code)
        args = self.prefix()
        args += [str(COLLECTOR), "--output", str(self.root / "artifact")]
        if owned:
            args += ["--daemon-exclusivo"]
        return args + ["--", sys.executable, str(target)]

    def prefix(self):
        args = [sys.executable]
        if os.environ.get("TRACE_COLLECTOR_DIR"):
            args += ["-c", TRACE_BOOTSTRAP]
        return args

    def run_target(self, exit_code=0, owned=True):
        code = f'''import os,pathlib,time,sys
root=pathlib.Path(os.environ["FAKE_ROOT"])
if {owned!r} and not any((root/name).exists() for name in ("remote","bad_baseline","remove_cli")):
    deadline=time.monotonic()+5
    while not (root/"ready").exists() and time.monotonic()<deadline: time.sleep(.01)
    (root/"trigger").touch()
    while not (root/"emitted").exists() and time.monotonic()<deadline: time.sleep(.01)
    time.sleep(.3)
sys.exit({exit_code})
'''
        return subprocess.run(self.command(code, owned), env=self.env, capture_output=True, timeout=10)

    def artifact(self):
        files = list((self.root / "artifact").glob("*"))
        text = "\n".join(file.read_text() for file in files)
        self.assertNotIn(SECRET, text)
        return text, json.loads((self.root / "artifact" / "manifesto.json").read_text())

    def assert_children_reaped(self):
        for pid in (self.root / "pids").read_text().splitlines():
            with self.assertRaises(ProcessLookupError):
                os.kill(int(pid), 0)

    def cleanup_docker_children(self):
        pids = self.root/"pids"
        if not pids.exists():
            return
        for pid in pids.read_text().splitlines():
            try:
                command = Path("/proc")/pid/"cmdline"
                if str(self.root/"docker").encode() in command.read_bytes().split(b"\0"):
                    os.kill(int(pid), signal.SIGKILL)
            except (FileNotFoundError, ProcessLookupError):
                pass

        target_pid = self.root/"target_pid"
        if target_pid.exists():
            try:
                pid = target_pid.read_text()
                if str(self.root/"target.py").encode() in (Path("/proc")/pid/"cmdline").read_bytes().split(b"\0"):
                    os.kill(int(pid), signal.SIGKILL)
            except (FileNotFoundError, ProcessLookupError):
                pass

    @staticmethod
    def cleanup_wrapper(proc):
        if proc.poll() is None:
            proc.kill()
        proc.wait(timeout=2)
        for pipe in (proc.stdout, proc.stderr):
            if pipe is not None:
                pipe.close()

    def test_preserva_exit_e_nao_publica_segredos(self):
        result = self.run_target(7)
        self.assertEqual(result.returncode, 7, result.stderr.decode())
        text, manifest = self.artifact()
        self.assertEqual(manifest["testExit"], 7)
        self.assertIn(CID, text)
        self.assertIn("destroy", text)
        self.assertIn("client disconnected", text)
        self.assertNotIn(OLD, text)
        calls = (self.root / "calls").read_text()
        self.assertNotIn('"stop"', calls)
        self.assertNotIn('"rm"', calls)
        self.assertNotIn('"--details"', calls)
        self.assertNotIn('"' + OLD + '"', calls)
        self.assert_children_reaped()

    def test_exit_zero_e_coleta_negada_por_padrao(self):
        result = self.run_target(0, owned=False)
        self.assertEqual(result.returncode, 0)
        _, manifest = self.artifact()
        self.assertEqual(manifest["testExit"], 0)
        self.assertEqual(manifest["collectorStatus"], "ownership_unverified")
        self.assertFalse((self.root / "calls").exists())

    def test_registry_explicito_no_daemon_compartilhado(self):
        own_session = "stillon-test-" + "e"*16 + "-1234-" + "f"*32
        own = event(CID)
        own["Actor"]["Attributes"]["org.testcontainers.sessionId"] = own_session
        foreign = event(RYUK)
        (self.root/"events").write_text(json.dumps([foreign, own]))
        registry = self.root/"sessions.json"
        registry.write_text(json.dumps([own_session]))
        args = self.command('''import os,pathlib,time
root=pathlib.Path(os.environ["FAKE_ROOT"])
deadline=time.monotonic()+5
while not (root/"ready").exists() and time.monotonic()<deadline: time.sleep(.01)
(root/"trigger").touch()
while not (root/"emitted").exists() and time.monotonic()<deadline: time.sleep(.01)
time.sleep(.2)
''', owned=False)
        separator = args.index("--")
        args[separator:separator] = ["--registro-sessoes", str(registry)]
        result = subprocess.run(args,env=self.env,capture_output=True,timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)
        text, manifest = self.artifact()
        self.assertIn(CID, text)
        self.assertNotIn(RYUK, text)
        self.assertEqual(manifest["ownershipMode"], "session_registry")
        calls = (self.root/"calls").read_text()
        self.assertNotIn('"'+RYUK+'"', calls)

    def test_registry_invalido_nega_sem_expor_input(self):
        registry = self.root/"sessions.json"
        registry.write_text(json.dumps([SECRET]))
        args = self.command("pass", owned=False)
        separator = args.index("--")
        args[separator:separator] = ["--registro-sessoes", str(registry)]
        result = subprocess.run(args,env=self.env,capture_output=True,timeout=5)
        self.assertEqual(result.returncode, 0)
        _, manifest = self.artifact()
        self.assertEqual(manifest["collectorStatus"], "registry_unavailable")
        self.assertFalse((self.root/"calls").exists())

    def test_registry_ausente_malformado_e_grande_negam_coleta(self):
        import shutil
        registry = self.root/"sessions.json"
        for raw in (None, b"[", b"\xff", json.dumps({"secret": SECRET}).encode(), b"[]", b"x"*(DIAG.MAX_LINE+1)):
            with self.subTest(raw_type=type(raw).__name__):
                if raw is not None:
                    registry.write_bytes(raw)
                args = self.command("pass", owned=False)
                separator = args.index("--")
                args[separator:separator] = ["--registro-sessoes", str(registry)]
                result = subprocess.run(args,env=self.env,capture_output=True,timeout=5)
                self.assertEqual(result.returncode, 0)
                _, manifest = self.artifact()
                self.assertEqual(manifest["collectorStatus"], "registry_unavailable")
                self.assertFalse((self.root/"calls").exists())
                shutil.rmtree(self.root/"artifact")
                if registry.exists():
                    registry.unlink()

    def test_inspect_falhando_nao_mascara_exit_um(self):
        (self.root / "missing").touch()
        result = self.run_target(1)
        self.assertEqual(result.returncode, 1)
        text, _ = self.artifact()
        self.assertIn("inspect_unavailable", text)
        self.assertIn("destroy", text)

    def test_daemon_remoto_e_baseline_invalida_negam_coleta(self):
        for marker, status in (("remote", "daemon_unverified"), ("bad_baseline", "baseline_unavailable")):
            with self.subTest(marker=marker):
                (self.root / marker).touch()
                result = self.run_target(3)
                self.assertEqual(result.returncode, 3)
                _, manifest = self.artifact()
                self.assertEqual(manifest["collectorStatus"], status)
                (self.root / marker).unlink()
                import shutil
                shutil.rmtree(self.root / "artifact")

    def test_json_inspect_invalido_e_stream_interrompido(self):
        (self.root / "bad_inspect").touch()
        result = self.run_target(0)
        self.assertEqual(result.returncode, 0)
        text, manifest = self.artifact()
        self.assertIn("invalid_state", text)
        self.assertIn(CID, manifest["containers"])

    def test_cli_desaparece_sem_mascarar_teste(self):
        (self.root / "remove_cli").touch()
        result = self.run_target(2)
        self.assertEqual(result.returncode, 2)
        _, manifest = self.artifact()
        self.assertEqual(manifest["collectorStatus"], "events_unavailable")

    def test_exit_de_sinal_do_comando_tambem_e_preservado(self):
        result = subprocess.run(self.command("import os,signal;os.kill(os.getpid(),signal.SIGTERM)", owned=False),
                                env=self.env, capture_output=True, timeout=5)
        self.assertEqual(result.returncode, 143)
        self.artifact()

    def test_comando_ausente_e_diretorio_existente(self):
        (self.root / "artifact").mkdir()
        result = subprocess.run(self.prefix() + [str(COLLECTOR), "--output", str(self.root/"artifact"),
                                 "--", str(self.root/"inexistente")], env=self.env, capture_output=True, timeout=5)
        self.assertEqual(result.returncode, 127)
        self.assertNotIn(SECRET, result.stderr.decode())

    def test_erro_real_de_gravacao_nao_muda_resultado(self):
        code = '''import os,pathlib,shutil,sys
root=pathlib.Path(os.environ["FAKE_ROOT"])
shutil.rmtree(root/"artifact")
(root/"artifact").write_text("bloqueado")
sys.exit(7)
'''
        result = subprocess.run(self.command(code, owned=False), env=self.env, capture_output=True, timeout=5)
        self.assertEqual(result.returncode, 7)
        self.assertIn(b"finaliza", result.stdout)
        self.assertNotIn(SECRET.encode(), result.stdout + result.stderr)

    def test_stream_terminando_deixa_lacuna_visivel(self):
        (self.root/"events_end").touch()
        code = "import time;time.sleep(.3)"
        result = subprocess.run(self.command(code), env=self.env, capture_output=True, timeout=5)
        self.assertEqual(result.returncode, 0)
        _, manifest = self.artifact()
        self.assertIn("events_ended", manifest["errors"])

    def test_json_nao_utf8_e_linha_longa_nao_vazam(self):
        (self.root / "events").write_text(json.dumps([SECRET * 2000, event(CID)]))
        result = self.run_target(0)
        self.assertEqual(result.returncode, 0)
        text, _ = self.artifact()
        self.assertIn("line_limit", text)
        self.assertIn(CID, text)

    def test_json_recursivo_nao_expoe_excecao_bruta(self):
        nested = "["*2000 + json.dumps(SECRET) + "]"*2000
        (self.root/"events").write_text(json.dumps([nested, event(CID)]))
        result = self.run_target(0)
        self.assertEqual(result.returncode, 0)
        text, manifest = self.artifact()
        self.assertNotIn(b"Traceback", result.stderr)
        self.assertIn(CID, text)
        self.assertTrue(any(key in manifest["errors"] for key in ("invalid_event", "rejected_event")))

    def test_follower_nao_e_aberto_antes_de_start(self):
        (self.root/"events").write_text(json.dumps([event(RYUK)]))
        result = self.run_target(0)
        self.assertEqual(result.returncode, 0)
        self.assertNotIn('"logs"', (self.root/"calls").read_text())
        self.artifact()

    def test_encaminha_sinal_e_recolhe_filhos(self):
        code = '''import os,pathlib,time,signal,sys
root=pathlib.Path(os.environ["FAKE_ROOT"])
def stop(signum,frame):
    (root/"signaled").write_text(str(signum))
    sys.exit(128+signum)
signal.signal(signal.SIGTERM,stop)
(root/"target_ready").touch()
while True: time.sleep(.01)
'''
        proc = subprocess.Popen(self.command(code), env=self.env, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.addCleanup(self.cleanup_wrapper, proc)
        deadline = time.monotonic() + 5
        while not (self.root / "target_ready").exists() and time.monotonic() < deadline:
            time.sleep(.01)
        self.assertTrue((self.root / "target_ready").exists())
        proc.send_signal(signal.SIGTERM)
        try:
            out, err = proc.communicate(timeout=5)
        except subprocess.TimeoutExpired:
            self.fail("wrapper não encaminhou o sinal ou não encerrou os filhos no prazo")
        self.assertEqual(proc.returncode, 143, (out, err))
        self.assertEqual((self.root / "signaled").read_text(), "15")
        self.artifact()
        self.assert_children_reaped()

    def test_sinal_durante_startup_tambem_preserva_resultado(self):
        (self.root/"slow_context").touch()
        proc = subprocess.Popen(self.command("import time;time.sleep(30)"), env=self.env,
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.addCleanup(self.cleanup_wrapper, proc)
        deadline = time.monotonic() + 5
        while not (self.root/"startup_ready").exists() and time.monotonic() < deadline:
            time.sleep(.01)
        self.assertTrue((self.root/"startup_ready").exists())
        proc.send_signal(signal.SIGTERM)
        proc.communicate(timeout=5)
        self.assertEqual(proc.returncode, 143)
        self.artifact()
        self.assert_children_reaped()


class BoundaryTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)

    def collector(self):
        return DIAG.Collector(Path(self.temp.name)/"artifact", False)

    def test_limites_linhas_e_manifesto_reservado(self):
        collector = self.collector()
        pipe = io.BytesIO(b"x" * (DIAG.MAX_LINE + 10) + b"\n" + b"\xff\n" + b"ok\n")
        self.assertEqual(list(collector.lines(pipe)), ["ok\n"])
        self.assertIn("line_limit", collector.errors)
        self.assertIn("invalid_encoding", collector.errors)
        with mock.patch.object(DIAG, "MAX_BYTES", 1):
            collector.write("eventos.jsonl", {"cid": CID})
            collector.close(0, 0)
        self.assertFalse((collector.output/"eventos.jsonl").exists())
        self.assertTrue((collector.output/"manifesto.json").exists())

    def test_eventos_sem_nascimento_duplicados_e_limites(self):
        collector = self.collector()
        collector.baseline = set()
        value = event(CID)
        value["timeNano"] = time.time_ns()
        destroy = dict(value, Action="destroy")
        unknown = dict(destroy, Actor=dict(value["Actor"], ID=RYUK))
        content = [unknown, value, value, destroy]
        # Sem Docker, consultas selecionadas dão unavailable; não se stubba o método.
        with mock.patch.dict(os.environ, {"PATH": ""}):
            proc = type("Pipe", (), {"stdout": io.BytesIO("\n".join(map(json.dumps, content)).encode())})()
            collector.events(proc)
        self.assertEqual(len(collector.seen), 2)
        self.assertIn("birth_not_observed", collector.errors)
        self.assertIn("events_ended", collector.errors)
        collector.seen.clear()
        with mock.patch.object(DIAG, "MAX_EVENTS", 0):
            proc.stdout = io.BytesIO(json.dumps(value).encode())
            collector.events(proc)
        self.assertIn("event_limit", collector.errors)
        collector.registry.clear()
        collector.seen.clear()
        with mock.patch.object(DIAG, "MAX_CONTAINERS", 0):
            proc.stdout = io.BytesIO(json.dumps(value).encode())
            collector.events(proc)
        self.assertIn("container_limit", collector.errors)

    def test_worker_erro_classe_fixa_e_logs_limite(self):
        collector = self.collector()
        def invalid_worker():
            raise ValueError(SECRET)
        collector.thread(invalid_worker)
        collector.threads[0].join()
        self.assertIn("worker_unavailable", collector.errors)
        collector.registry[CID] = {}
        collector.logs_written[RYUK] = 1024 * 1024
        proc = type("Pipe", (), {"stdout": io.BytesIO(b'2026-10-05T00:00:01Z msg=Started\n')})()
        collector.logs(proc, RYUK)
        self.assertIn("log_limit", collector.errors)
        collector.close(0, 0)
        self.assertNotIn(SECRET, (collector.output/"manifesto.json").read_text())

    def test_docker_indisponivel_e_logs_nao_disponiveis(self):
        collector = self.collector()
        with mock.patch.dict(os.environ, {"PATH": ""}):
            collector.exclusive = True
            collector.start()
            self.assertEqual(collector.status, "daemon_unverified")
            collector.follow(RYUK)
        self.assertIn("logs_unavailable", collector.errors)
        collector.following.add(RYUK)
        collector.follow(RYUK)

    def test_parser_cli_sem_comando(self):
        with self.assertRaises(SystemExit) as raised:
            DIAG.main(["--output", str(Path(self.temp.name)/"artifact")])
        self.assertEqual(raised.exception.code, 2)

    def test_corrida_de_process_lookup_e_wait_timeout_na_finalizacao(self):
        collector = self.collector()
        child = subprocess.Popen([sys.executable, "-c", "pass"], stdout=subprocess.PIPE, start_new_session=True)
        child.wait(timeout=2)
        collector.children.append(child)
        # PID já foi recolhido; o kernel responde ProcessLookupError real.
        # poll/wait simulam apenas as respostas atrasadas dessa fronteira OS.
        with mock.patch.object(child, "poll", return_value=None), mock.patch.object(
                child, "wait", side_effect=[subprocess.TimeoutExpired("docker", 1),
                                           subprocess.TimeoutExpired("docker", 1)]):
            collector.close(7, 0)
        value = json.loads((collector.output/"manifesto.json").read_text())
        self.assertEqual(value["testExit"], 7)
        self.assertIn("close_timeout", value["errors"])

    def test_deadline_limita_thread_e_snapshots_finais(self):
        collector = self.collector()
        event = threading.Event()
        collector.thread(event.wait)
        self.addCleanup(event.set)
        collector.registry[CID] = {}
        ticks = iter([0] + [10]*10)
        with mock.patch.object(DIAG.time, "monotonic", side_effect=lambda: next(ticks)):
            collector.close(1, 0)
        event.set()
        collector.threads[0].join(timeout=1)
        value = json.loads((collector.output/"manifesto.json").read_text())
        self.assertIn("close_timeout", value["errors"])

    def test_pipe_herdado_nao_bloqueia_finalizacao(self):
        collector = self.collector()
        read_fd, write_fd = os.pipe()
        reader = os.fdopen(read_fd, "rb")
        self.addCleanup(reader.close)
        self.addCleanup(lambda: os.close(write_fd))
        child = subprocess.Popen([sys.executable, "-c", "pass"], stdout=subprocess.PIPE, start_new_session=True)
        child.wait(timeout=2)
        child.stdout.close()
        child.stdout = reader
        collector.children.append(child)
        collector.thread(reader.readline)
        ticks = iter([0] + [10]*10)
        # O writer é liberado por timer apenas se close tentar bloquear.
        timer = threading.Timer(.5, lambda: os.write(write_fd, b"done\n"))
        timer.start()
        self.addCleanup(timer.join)
        started = time.perf_counter()
        with mock.patch.object(DIAG.time, "monotonic", side_effect=lambda: next(ticks)):
            collector.close(0, 0)
        elapsed = time.perf_counter() - started
        timer.join()
        collector.threads[0].join(timeout=1)
        self.assertLess(elapsed, .2)

    def test_sinal_antes_do_comando_com_pid_ja_encerrado(self):
        root = Path(self.temp.name)
        docker = root/"docker"
        docker.write_text(FAKE_DOCKER)
        docker.chmod(0o755)
        (root/"python3").symlink_to(sys.executable)
        (root/"slow_context").touch()
        timer = threading.Timer(.1, lambda: os.kill(os.getpid(), signal.SIGTERM))
        timer.start()
        self.addCleanup(timer.join)
        with mock.patch.dict(os.environ, {"PATH": str(root), "FAKE_ROOT": str(root)}), mock.patch.object(
                DIAG.os, "killpg", side_effect=ProcessLookupError):
            result = DIAG.main(["--output", str(root/"artifact"), "--daemon-exclusivo", "--", sys.executable, "-c", "pass"])
        self.assertEqual(result, 0)
        self.assertEqual(json.loads((root/"artifact"/"manifesto.json").read_text())["interrupted"], 15)

    def test_comando_ausente_e_process_lookup_no_forward(self):
        output = Path(self.temp.name)/"artifact"
        timer = threading.Timer(.1, lambda: os.kill(os.getpid(), signal.SIGTERM))
        timer.start()
        self.addCleanup(timer.join)
        with mock.patch.object(DIAG.os, "killpg", side_effect=ProcessLookupError):
            result = DIAG.main(["--output", str(output), "--", sys.executable, "-c", "import time;time.sleep(.2)"])
        self.assertEqual(result, 0)
        self.assertEqual(json.loads((output/"manifesto.json").read_text())["interrupted"], 15)

    def test_docker_timeout_e_encoding_invalido(self):
        collector = self.collector()
        with mock.patch.object(DIAG.subprocess, "run", side_effect=subprocess.TimeoutExpired("docker", 2)):
            self.assertIsNone(collector.docker(["context", "inspect"]))
        with mock.patch.object(DIAG.subprocess, "run", return_value=subprocess.CompletedProcess([], 0, b"\xff")):
            self.assertIsNone(collector.docker(["context", "inspect"]))

    def test_sinal_hardkill_do_filho_do_coletor(self):
        collector = self.collector()
        child = subprocess.Popen([sys.executable, "-c",
                                  "import signal,time;signal.signal(signal.SIGTERM,signal.SIG_IGN);print('ready',flush=True);time.sleep(30)"],
                                 stdout=subprocess.PIPE, start_new_session=True)
        self.addCleanup(lambda: child.kill() if child.poll() is None else None)
        self.assertEqual(child.stdout.readline(), b"ready\n")
        collector.children.append(child)
        collector.close(1, 0)
        self.assertEqual(child.returncode, -signal.SIGKILL)
        self.assertEqual(json.loads((collector.output/"manifesto.json").read_text())["testExit"], 1)

    def test_eventos_sao_projetados_e_campos_invalidos_negados(self):
        value = event(CID)
        value["Actor"]["Attributes"].update({"exitCode": "137", "signal": "9", "org.testcontainers.reap": "true"})
        safe = DIAG.safe_event(value)
        self.assertEqual(safe["exitCode"], 137)
        self.assertNotIn(SECRET, json.dumps(safe))
        value["Actor"]["Attributes"]["org.testcontainers.sessionId"] = "stillon-test-" + "e"*16 + "-1234-" + "f"*32
        self.assertIsNotNone(DIAG.safe_event(value))
        for bad in (None, [], SECRET, {}, {"Type": "network"}, {"Type": "container", "Actor": SECRET}):
            self.assertIsNone(DIAG.safe_event(bad))
        for path, bad in (("Action", "exec_create"), ("timeNano", SECRET), ("timeNano", -1)):
            broken = dict(value, **{path: bad})
            self.assertIsNone(DIAG.safe_event(broken))
        for key in ("org.testcontainers.sessionId", "image", "org.testcontainers.lang", "org.testcontainers"):
            broken = event(CID)
            broken["Actor"]["Attributes"][key] = SECRET
            self.assertIsNone(DIAG.safe_event(broken))
        broken = event(CID)
        broken["Actor"]["Attributes"] = SECRET
        self.assertIsNone(DIAG.safe_event(broken))
        broken = event(CID)
        broken["Actor"]["ID"] = SECRET
        self.assertIsNone(DIAG.safe_event(broken))
        broken = event(CID)
        broken["Actor"]["Attributes"].update({"org.testcontainers.version": SECRET, "signal": SECRET})
        self.assertNotIn(SECRET, json.dumps(DIAG.safe_event(broken)))

    def test_estado_tem_lista_fechada_de_campos(self):
        value = {"id": CID, "status": "running", "running": True, "oomKilled": False,
                 "autoRemove": True, "exitCode": 0, "created": "2026-10-05T00:00:00Z",
                 "started": "2026-10-05T00:00:00Z", "finished": "0001-01-01T00:00:00Z",
                 "Env": SECRET}
        self.assertNotIn(SECRET, json.dumps(DIAG.safe_state(value, CID)))
        self.assertIsNone(DIAG.safe_state(None, CID))
        for key in ("id", "status", "running", "exitCode", "created"):
            self.assertIsNone(DIAG.safe_state(dict(value, **{key: SECRET}), CID))

    def test_logs_ryuk_nao_guardam_sql_filtros_ou_ids_alheios(self):
        prefix = "2026-10-05T00:00:01Z "
        self.assertIsNone(DIAG.safe_ryuk("SQL password=" + SECRET, {CID}))
        self.assertIsNone(DIAG.safe_ryuk(prefix + 'msg="password=' + SECRET + '"', {CID}))
        self.assertIsNone(DIAG.safe_ryuk(prefix + f"msg=remove resource=container id={OLD}", {CID}))
        self.assertIsNone(DIAG.safe_ryuk(prefix + f"msg=remove resource=volume id={CID}", {CID}))
        value = DIAG.safe_ryuk(prefix + f'msg="client connected" clients=2 address={SECRET} filter={SECRET}', {CID})
        self.assertEqual(value["clients"], 2)
        self.assertNotIn(SECRET, json.dumps(value))
        with self.assertRaises(ValueError):
            DIAG.safe_ryuk(prefix + 'msg="' + SECRET, {CID})


if __name__ == "__main__":
    unittest.main(verbosity=2)
