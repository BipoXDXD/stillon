#!/usr/bin/env python3
"""Diagnóstico limitado das fixtures; o resultado continua sendo o do comando."""
import argparse
import datetime
import json
import os
from pathlib import Path
import re
import shlex
import signal
import subprocess
import threading
import time


CID_RE = re.compile(r"[a-f0-9]{64}\Z")
SESSION_RE = re.compile(r"(?:[a-f0-9]{32,64}|stillon-test-[a-f0-9]{16}-[1-9][0-9]{0,9}-[a-f0-9]{32})\Z")
IMAGE_RE = re.compile(r"(?:postgres:(?:17|18)-alpine|testcontainers/ryuk:[0-9.]+)(?:@sha256:[a-f0-9]{64})?\Z")
STAMP_RE = re.compile(r"(?:\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,9})?Z)\Z")
ACTIONS = ("create", "start", "stop", "kill", "die", "oom", "destroy")
MAX_LINE = 16384
MAX_BYTES = 16 * 1024 * 1024
MAX_CONTAINERS = 32
MAX_EVENTS = 8192
INSPECT_FORMAT = ('{"id":{{json .Id}},"status":{{json .State.Status}},'
                  '"running":{{json .State.Running}},"exitCode":{{json .State.ExitCode}},'
                  '"oomKilled":{{json .State.OOMKilled}},"autoRemove":{{json .HostConfig.AutoRemove}},'
                  '"created":{{json .Created}},"started":{{json .State.StartedAt}},'
                  '"finished":{{json .State.FinishedAt}}}')


def utc():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def valid_id(value):
    return isinstance(value, str) and CID_RE.fullmatch(value) is not None


def safe_event(value):
    """Nenhum atributo bruto, nome arbitrário ou erro do parser é persistido."""
    if not isinstance(value, dict) or value.get("Type") != "container":
        return None
    actor = value.get("Actor")
    if not isinstance(actor, dict) or not valid_id(actor.get("ID")):
        return None
    action, stamp = value.get("Action"), value.get("timeNano")
    if action not in ACTIONS or type(stamp) is not int or stamp < 0:
        return None
    attrs = actor.get("Attributes")
    if not isinstance(attrs, dict):
        return None
    session, image = attrs.get("org.testcontainers.sessionId"), attrs.get("image")
    if (attrs.get("org.testcontainers") != "true" or attrs.get("org.testcontainers.lang") != "go"
            or not isinstance(session, str) or SESSION_RE.fullmatch(session) is None
            or not isinstance(image, str) or IMAGE_RE.fullmatch(image) is None):
        return None
    labels = {"org.testcontainers": "true", "org.testcontainers.lang": "go",
              "org.testcontainers.sessionId": session}
    version = attrs.get("org.testcontainers.version")
    if isinstance(version, str) and re.fullmatch(r"\d+\.\d+\.\d+", version):
        labels["org.testcontainers.version"] = version
    for key in ("reap", "reaper", "ryuk"):
        label = "org.testcontainers." + key
        if attrs.get(label) in ("true", "false"):
            labels[label] = attrs[label]
    result = {"cid": actor["ID"], "action": action, "timeNano": stamp,
              "image": image, "labels": labels}
    for key in ("exitCode", "signal"):
        if isinstance(attrs.get(key), str) and re.fullmatch(r"\d{1,3}", attrs[key]):
            result[key] = int(attrs[key])
    return result


def safe_state(value, cid):
    if not isinstance(value, dict) or value.get("id") != cid:
        return None
    if value.get("status") not in ("created", "running", "paused", "restarting", "removing", "exited", "dead"):
        return None
    if any(type(value.get(key)) is not bool for key in ("running", "oomKilled", "autoRemove")):
        return None
    if type(value.get("exitCode")) is not int or not 0 <= value["exitCode"] <= 255:
        return None
    if any(not isinstance(value.get(key), str) or STAMP_RE.fullmatch(value[key]) is None
           for key in ("created", "started", "finished")):
        return None
    return {key: value[key] for key in ("id", "status", "running", "exitCode", "oomKilled",
                                       "autoRemove", "created", "started", "finished")}


def safe_ryuk(line, owned):
    """Extrai classes e IDs próprios, nunca mensagem livre ou labels de filtros."""
    fields = shlex.split(line)
    if not fields or STAMP_RE.fullmatch(fields[0]) is None:
        return None
    data = dict(part.split("=", 1) for part in fields[1:] if "=" in part)
    msg = data.get("msg")
    if msg not in ("Started", "client connected", "client disconnected", "prune check",
                   "shutdown, aborting client", "remove", "removed"):
        return None
    result = {"timestamp": fields[0], "class": msg}
    if msg == "remove":
        if data.get("resource") != "container" or data.get("id") not in owned:
            return None
        result["targetCid"] = data["id"]
    for key in ("clients", "containers", "attempt"):
        value = data.get(key)
        if isinstance(value, str) and re.fullmatch(r"\d{1,6}", value):
            result[key] = int(value)
    return result


class Collector:
    def __init__(self, output, exclusive, session_file=None):
        self.output = Path(output)
        self.exclusive = exclusive
        self.session_file = session_file
        self.allowed_sessions = None
        self.lock = threading.RLock()
        self.stopping = threading.Event()
        self.children = []
        self.threads = []
        self.registry = {}
        self.following = set()
        self.seen = set()
        self.errors = {}
        self.written = 0
        self.logs_written = {}
        self.started_ns = time.time_ns()
        self.started = utc()
        self.status = "ownership_unverified"
        self.output.mkdir(parents=True, exist_ok=False)

    def error(self, category):
        with self.lock:
            self.errors[category] = self.errors.get(category, 0) + 1

    def write(self, filename, value):
        text = json.dumps(value, sort_keys=True) + "\n"
        size = len(text.encode())
        with self.lock:
            if filename != "manifesto.json" and self.written + size > MAX_BYTES:
                self.error("size_limit")
                return
            with (self.output / filename).open("a") as file:
                file.write(text)
            self.written += size

    def docker(self, args):
        try:
            result = subprocess.run(["docker", *args], stdout=subprocess.PIPE,
                                    stderr=subprocess.DEVNULL, timeout=2, check=False)
            if result.returncode != 0 or len(result.stdout) > MAX_LINE:
                return None
            return result.stdout.decode()
        except (OSError, subprocess.TimeoutExpired, UnicodeError):
            return None

    def spawn(self, args, merge_stderr=False):
        proc = subprocess.Popen(["docker", *args], stdout=subprocess.PIPE,
                                stderr=subprocess.STDOUT if merge_stderr else subprocess.DEVNULL,
                                start_new_session=True)
        with self.lock:
            self.children.append(proc)
        return proc

    def thread(self, target, *args):
        def guarded():
            try:
                target(*args)
            except Exception:
                self.error("worker_unavailable")
        thread = threading.Thread(target=guarded, daemon=True)
        self.threads.append(thread)
        thread.start()

    def lines(self, pipe):
        dropping = False
        while not self.stopping.is_set():
            chunk = pipe.readline(MAX_LINE + 1)
            if not chunk:
                return
            if len(chunk) > MAX_LINE or dropping:
                dropping = not chunk.endswith(b"\n")
                self.error("line_limit")
                continue
            try:
                yield chunk.decode()
            except UnicodeError:
                self.error("invalid_encoding")

    def start(self):
        if self.session_file:
            try:
                with Path(self.session_file).open("rb") as file:
                    raw = file.read(MAX_LINE + 1)
                if len(raw) > MAX_LINE:
                    raise ValueError
                sessions = json.loads(raw)
                if (not isinstance(sessions, list) or not 0 < len(sessions) <= MAX_CONTAINERS
                        or any(not isinstance(item, str) or SESSION_RE.fullmatch(item) is None for item in sessions)):
                    raise ValueError
                self.allowed_sessions = set(sessions)
            except (OSError, ValueError, RecursionError):
                self.status = "registry_unavailable"
                return
        if not self.exclusive and self.allowed_sessions is None:
            return
        # Contrato explícito: só habilitar em daemon local exclusivo do job.
        endpoint = self.docker(["context", "inspect", "--format", "{{.Endpoints.docker.Host}}"])
        if endpoint is None or not endpoint.strip().startswith("unix://") or os.environ.get("DOCKER_HOST"):
            self.status = "daemon_unverified"
            return
        filters = ["label=org.testcontainers=true"] if self.allowed_sessions is None else [
            "label=org.testcontainers.sessionId="+session for session in sorted(self.allowed_sessions)]
        self.baseline = set()
        for filter_value in filters:
            baseline = self.docker(["container", "ls", "-aq", "--no-trunc", "--filter", filter_value])
            if baseline is None or any(not valid_id(cid) for cid in baseline.split()):
                self.status = "baseline_unavailable"
                return
            self.baseline.update(baseline.split())
        args = ["events", "--since", str(self.started_ns / 1e9), "--filter", "type=container",
                "--filter", "label=org.testcontainers=true", "--format", "{{json .}}"]
        for action in ACTIONS:
            args += ["--filter", "event=" + action]
        try:
            proc = self.spawn(args)
        except OSError:
            self.status = "events_unavailable"
            return
        self.status = "started"
        self.thread(self.events, proc)

    def events(self, proc):
        for line in self.lines(proc.stdout):
            try:
                value = safe_event(json.loads(line))
            except (ValueError, TypeError, RecursionError):
                self.error("invalid_event")
                continue
            if value is None:
                self.error("rejected_event")
                continue
            cid, action = value["cid"], value["action"]
            if self.allowed_sessions is not None and value["labels"]["org.testcontainers.sessionId"] not in self.allowed_sessions:
                continue
            if cid in self.baseline or value["timeNano"] < self.started_ns:
                continue
            key = (cid, action, value["timeNano"])
            with self.lock:
                if key in self.seen:
                    continue
                if len(self.seen) >= MAX_EVENTS:
                    self.error("event_limit")
                    continue
                if cid not in self.registry:
                    if action != "create":
                        self.error("birth_not_observed")
                        continue
                    if len(self.registry) >= MAX_CONTAINERS:
                        self.error("container_limit")
                        continue
                    self.registry[cid] = value
                self.seen.add(key)
            value["observedAt"] = utc()
            self.write("eventos.jsonl", value)
            # No create a CLI logs pode encerrar antes de o processo iniciar.
            # Start + --since recupera o início; assinar antes do inspect lento.
            if action == "start" and value["image"].startswith("testcontainers/ryuk:"):
                self.follow(cid)
            if action in ("create", "start", "die", "oom"):
                self.snapshot(cid)
        if not self.stopping.is_set():
            self.error("events_ended")

    def snapshot(self, cid):
        output = self.docker(["inspect", "--type", "container", "--format", INSPECT_FORMAT, cid])
        if output is None:
            self.write("estados.jsonl", {"cid": cid, "class": "inspect_unavailable", "observedAt": utc()})
            return
        try:
            value = safe_state(json.loads(output), cid)
        except (ValueError, TypeError, RecursionError):
            value = None
        if value is None:
            self.error("invalid_state")
            return
        value["observedAt"] = utc()
        self.write("estados.jsonl", value)

    def follow(self, cid):
        if cid in self.following:
            return
        try:
            proc = self.spawn(["logs", "--follow", "--timestamps", "--since", str(self.started_ns / 1e9),
                               "--tail", "200", cid], merge_stderr=True)
        except OSError:
            self.error("logs_unavailable")
            return
        self.following.add(cid)
        self.thread(self.logs, proc, cid)

    def logs(self, proc, cid):
        for line in self.lines(proc.stdout):
            try:
                with self.lock:
                    value = safe_ryuk(line, self.registry)
            except (ValueError, TypeError):
                value = None
            if value is None:
                self.error("omitted_log")
                continue
            value["cid"] = cid
            size = len(json.dumps(value).encode())
            with self.lock:
                self.logs_written[cid] = self.logs_written.get(cid, 0) + size
                if self.logs_written[cid] > 1024 * 1024:
                    self.error("log_limit")
                    continue
            self.write("ryuk.jsonl", value)

    def close(self, test_exit, interrupted):
        deadline = time.monotonic() + 10
        self.stopping.set()
        # Os filhos do coletor têm grupos próprios; nenhum contêiner é parado.
        with self.lock:
            children = list(self.children)
        for proc in children:
            if proc.poll() is None:
                try:
                    os.killpg(proc.pid, signal.SIGTERM)
                except ProcessLookupError:
                    pass
        for proc in children:
            try:
                proc.wait(timeout=max(0, min(1, deadline - time.monotonic())))
            except subprocess.TimeoutExpired:
                try:
                    os.killpg(proc.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                try:
                    proc.wait(timeout=max(0, deadline - time.monotonic()))
                except subprocess.TimeoutExpired:
                    self.error("close_timeout")
        for thread in self.threads:
            thread.join(timeout=max(0, deadline - time.monotonic()))
            if thread.is_alive():
                self.error("close_timeout")
        # BufferedReader.close espera o lock de readline: pipe herdado pode
        # bloquear apesar de a CLI já ter encerrado. Não ultrapassar deadline.
        if not any(thread.is_alive() for thread in self.threads):
            for proc in children:
                proc.stdout.close()
        else:
            self.error("pipe_not_closed")
        for cid in self.registry:
            if deadline - time.monotonic() < 2:
                self.error("close_timeout")
                break
            self.snapshot(cid)
        self.write("manifesto.json", {"startedAt": self.started, "finishedAt": utc(),
                                     "testExit": test_exit, "interrupted": interrupted,
                                     "collectorStatus": self.status, "errors": self.errors,
                                     "ownershipMode": "session_registry" if self.allowed_sessions is not None else "exclusive_daemon" if self.exclusive else "denied",
                                     "containers": list(self.registry), "bytesWritten": self.written,
                                     "collectionComplete": False})


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True)
    ownership = parser.add_mutually_exclusive_group()
    ownership.add_argument("--daemon-exclusivo", action="store_true")
    ownership.add_argument("--registro-sessoes")
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args(argv)
    command = args.command[1:] if args.command[:1] == ["--"] else args.command
    if not command:
        parser.error("comando de teste ausente")
    collector = None
    interrupted = 0
    target = None

    def forward(signum, _frame):
        nonlocal interrupted
        interrupted = signum
        if target is not None and target.poll() is None:
            try:
                os.killpg(target.pid, signum)
            except ProcessLookupError:
                pass

    old_handlers = {sig: signal.signal(sig, forward) for sig in (signal.SIGINT, signal.SIGTERM)}
    try:
        try:
            collector = Collector(args.output, args.daemon_exclusivo, args.registro_sessoes)
            collector.start()
        except Exception:
            print("::warning::diagnóstico de fixture indisponível", flush=True)
        try:
            target = subprocess.Popen(command, start_new_session=True)
            if interrupted:
                try:
                    os.killpg(target.pid, interrupted)
                except ProcessLookupError:
                    pass
            result = target.wait()
            result = 128 - result if result < 0 else result
        except OSError:
            result = 127
            print("::error::comando de teste indisponível", flush=True)
        if collector is not None:
            try:
                collector.close(result, interrupted)
            except Exception:
                print("::warning::finalização do diagnóstico indisponível", flush=True)
        return result
    finally:
        for sig, handler in old_handlers.items():
            signal.signal(sig, handler)


if __name__ == "__main__":
    raise SystemExit(main())
