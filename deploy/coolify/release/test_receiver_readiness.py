"""What each outcome of the receiver's final checks leaves published, without Docker or network."""

import http.client
import json
import tempfile
import unittest
from pathlib import Path
from typing import Any
from unittest.mock import patch

import receiver as r

SHA = "a" * 40
IMAGE_ID = "sha256:" + "b" * 64
IMPORTED_IMAGE = "ghcr.io/bipoxdxd/stillon:release@" + IMAGE_ID
API_ADDRESS = "192.0.2.10"
WEB_ADDRESS = "192.0.2.20"
OTHER_NETWORK_ADDRESS = "192.0.2.99"
FINISHED_AT = 1_800_000_000
MAX_SLEEPS = 2000
COOLIFY_DEADLINE = 1200
HEALTH_WAIT = 120
HANGS = object()

API_TARGET = (API_ADDRESS, 8080)
WEB_TARGET = (WEB_ADDRESS, 9090)
API_REQUEST = (API_ADDRESS, 8080, "GET", "/readyz", 30)
WEB_REQUEST = (WEB_ADDRESS, 9090, "GET", "/ready", 30)
API_PROBE = {"container": "api", "network": "interna", "port": 8080, "path": "/readyz"}
WEB_PROBE = {"container": "web", "network": "interna", "port": 9090, "path": "/ready"}


class ManualClock:
    def __init__(self) -> None:
        self.now = 0.0
        self.slept: list[float] = []

    def monotonic(self) -> float:
        return self.now

    def sleep(self, seconds: float) -> None:
        if len(self.slept) >= MAX_SLEEPS:
            raise AssertionError("Wait is not bounded by its deadline")
        self.slept.append(seconds)
        self.now += seconds


class ProbeResponse:
    def __init__(self, status: int) -> None:
        self.status = status

    def read(self) -> bytes:
        return b""


class ProbeConnection:
    def __init__(self, targets: "ProbeTargets", host: str, port: int, timeout: Any) -> None:
        self.targets = targets
        self.target = (host, port)
        self.timeout = timeout

    def request(self, method: str, path: str) -> None:
        self.targets.requests.append((*self.target, method, path, self.timeout))

    def getresponse(self) -> ProbeResponse:
        outcome = self.targets.outcomes[self.target]
        if outcome is HANGS:
            if self.timeout is None:
                raise AssertionError("A probe without timeout would hang the release")
            raise TimeoutError("timed out")
        if isinstance(outcome, Exception):
            raise outcome
        return ProbeResponse(outcome)

    def close(self) -> None:
        self.targets.open_connections -= 1


class ProbeTargets:
    """Stands in for http.client.HTTPConnection with a scripted outcome per host and port."""

    def __init__(self, outcomes: dict[tuple[str, int], Any]) -> None:
        self.outcomes = outcomes
        self.requests: list[tuple[Any, ...]] = []
        self.open_connections = 0

    def __call__(self, host: str, port: int, timeout: Any = None) -> ProbeConnection:
        self.open_connections += 1
        return ProbeConnection(self, host, port, timeout)


class ReleaseRun:
    """One StillOn deploy() against a fake Coolify, Docker and clock, on a temporary state."""

    def __init__(self, test: unittest.TestCase, probes: list[dict[str, Any]]) -> None:
        temp = tempfile.TemporaryDirectory()
        test.addCleanup(temp.cleanup)
        self.state = Path(temp.name)
        self.clock = ManualClock()
        self.release = {
            "project": "stillon",
            "commit": SHA,
            "images": {"app": "ghcr.io/bipoxdxd/stillon@" + IMAGE_ID},
        }
        names = sorted({probe["container"] for probe in probes}) or ["api"]
        self.config = {
            "uuid": "app",
            "containers": names,
            "image_roles": {name: "app" for name in names},
            "readiness": probes,
        }
        self.health: str | None = "healthy"
        self.addresses = {"api": API_ADDRESS, "web": WEB_ADDRESS}
        self.networks_for_probe: dict[str, Any] | None = None
        self.coolify_busy_for = 0.0
        self.inspected: list[str] = []
        self.tags: list[list[str]] = []
        self.version_files: list[tuple[Path, str]] = []
        self.targets = ProbeTargets({})

    def coolify(self, path: str, method: str = "GET", payload: Any = None) -> Any:
        if path.startswith("deployments/applications/"):
            return {"count": 0, "deployments": []}
        if path == "applications/app/envs/bulk":
            return {}
        if path == "applications/app/start":
            return {"deployment_uuid": "deployment"}
        if path == "deployments/deployment":
            return {"status": "finished" if self.clock.now >= self.coolify_busy_for else "in_progress"}
        raise AssertionError("Unexpected Coolify request: " + path)

    def docker(self, argv: list[str]) -> bytes:
        if argv[:2] == ["/usr/bin/docker", "tag"]:
            self.tags.append(argv)
            return b""
        if argv[:2] != ["/usr/bin/docker", "inspect"] or len(argv) != 3:
            raise AssertionError(argv)
        self.inspected.append(argv[2])
        return json.dumps([self.container(argv[2])]).encode()

    def container(self, name: str) -> dict[str, Any]:
        state: dict[str, Any] = {"Status": "running"}
        if self.health is not None:
            state["Health"] = {"Status": self.health}
        networks = self.networks_for_probe
        if networks is None:
            networks = {
                "outra": {"IPAddress": OTHER_NETWORK_ADDRESS},
                "interna": {"IPAddress": self.addresses[name]},
            }
        return {
            "Image": IMAGE_ID,
            "State": state,
            "Config": {"Labels": {"coolify.applicationUuid": "app"}},
            "NetworkSettings": {"Networks": networks},
        }

    def deploy(self, outcomes: dict[tuple[str, int], Any]) -> Any:
        self.targets = ProbeTargets(outcomes)
        with (
            patch.object(r, "api", side_effect=self.coolify),
            patch.object(r, "command", side_effect=self.docker),
            patch.object(
                r, "atomic_text", side_effect=lambda path, text: self.version_files.append((path, text))
            ),
            patch.object(r.http.client, "HTTPConnection", self.targets),
            patch("time.monotonic", self.clock.monotonic),
            patch("time.sleep", self.clock.sleep),
            patch("time.time", return_value=FINISHED_AT),
        ):
            return r.deploy(
                self.release, {"app": IMPORTED_IMAGE}, self.config, self.state, {"app": IMAGE_ID}
            )


class ReadinessTests(unittest.TestCase):
    def assert_published(self, run: ReleaseRun) -> None:
        receipt = {"release": run.release, "deployment_uuid": "deployment", "finished_at": FINISHED_AT}
        self.assertEqual(json.loads((run.state / "current.json").read_text()), receipt)
        history = run.state / "history" / f"{SHA}-{FINISHED_AT}.json"
        self.assertEqual(json.loads(history.read_text()), receipt)
        self.assertFalse((run.state / "pending.json").exists())
        self.assertEqual(run.tags, [["/usr/bin/docker", "tag", IMPORTED_IMAGE, "stillon:" + SHA]])
        self.assertEqual(run.version_files, [(Path("/opt/stillon/versao"), SHA + "\n")])
        self.assertEqual(run.targets.open_connections, 0)

    def assert_not_published(self, run: ReleaseRun) -> None:
        pending = json.loads((run.state / "pending.json").read_text())
        self.assertEqual(pending, {"release": run.release, "deployment_uuid": "deployment"})
        self.assertFalse((run.state / "current.json").exists())
        self.assertFalse((run.state / "history").exists())
        self.assertEqual(run.tags, [])
        self.assertEqual(run.version_files, [])
        self.assertEqual(run.targets.open_connections, 0)

    def test_ready_application_publishes_the_release(self) -> None:
        run = ReleaseRun(self, [API_PROBE])

        deployment = run.deploy({API_TARGET: 200})

        self.assertEqual(deployment, "deployment")
        self.assert_published(run)
        self.assertEqual(run.targets.requests, [API_REQUEST])
        self.assertEqual(run.clock.slept, [])

    def test_any_status_other_than_200_keeps_the_release_pending(self) -> None:
        for status in [201, 204, 301, 404, 500, 503]:
            with self.subTest(status=status):
                run = ReleaseRun(self, [API_PROBE])

                with self.assertRaisesRegex(ValueError, "Application readiness failed"):
                    run.deploy({API_TARGET: status})

                self.assert_not_published(run)
                self.assertEqual(run.targets.requests, [API_REQUEST])

    def test_probe_that_never_answers_times_out_and_keeps_the_release_pending(self) -> None:
        run = ReleaseRun(self, [API_PROBE])

        with self.assertRaises(TimeoutError):
            run.deploy({API_TARGET: HANGS})

        self.assert_not_published(run)
        self.assertEqual(run.targets.requests, [API_REQUEST])
        self.assertEqual(run.clock.slept, [])

    def test_unreachable_application_keeps_the_release_pending(self) -> None:
        for error in [ConnectionRefusedError("refused"), http.client.RemoteDisconnected("closed")]:
            with self.subTest(error=type(error).__name__):
                run = ReleaseRun(self, [API_PROBE])

                with self.assertRaises(type(error)):
                    run.deploy({API_TARGET: error})

                self.assert_not_published(run)

    def test_every_probe_must_answer_200(self) -> None:
        for outcomes, asked in [
            ({API_TARGET: 200, WEB_TARGET: 503}, [API_REQUEST, WEB_REQUEST]),
            ({API_TARGET: 503, WEB_TARGET: 200}, [API_REQUEST]),
        ]:
            with self.subTest(outcomes=outcomes):
                run = ReleaseRun(self, [API_PROBE, WEB_PROBE])

                with self.assertRaisesRegex(ValueError, "Application readiness failed"):
                    run.deploy(outcomes)

                self.assert_not_published(run)
                self.assertEqual(run.targets.requests, asked)

    def test_all_probes_answering_200_publish_the_release(self) -> None:
        run = ReleaseRun(self, [API_PROBE, WEB_PROBE])

        run.deploy({API_TARGET: 200, WEB_TARGET: 200})

        self.assert_published(run)
        self.assertEqual(run.targets.requests, [API_REQUEST, WEB_REQUEST])

    def test_probe_address_must_come_from_the_configured_network_as_an_ip(self) -> None:
        for name, networks in [
            ("empty", {"interna": {"IPAddress": ""}}),
            ("hostname", {"interna": {"IPAddress": "app.example"}}),
            ("other network only", {"outra": {"IPAddress": OTHER_NETWORK_ADDRESS}}),
        ]:
            with self.subTest(name=name):
                run = ReleaseRun(self, [API_PROBE])
                run.networks_for_probe = networks

                with self.assertRaises((KeyError, ValueError)):
                    run.deploy({API_TARGET: 200})

                self.assert_not_published(run)
                self.assertEqual(run.targets.requests, [])

    def test_container_without_healthcheck_is_decided_by_the_probe(self) -> None:
        ready = ReleaseRun(self, [API_PROBE])
        ready.health = None
        unready = ReleaseRun(self, [API_PROBE])
        unready.health = None

        ready.deploy({API_TARGET: 200})
        with self.assertRaisesRegex(ValueError, "Application readiness failed"):
            unready.deploy({API_TARGET: 503})

        self.assert_published(ready)
        self.assert_not_published(unready)

    def test_unhealthy_container_is_refused_before_any_probe(self) -> None:
        run = ReleaseRun(self, [API_PROBE])
        run.health = "unhealthy"

        with self.assertRaisesRegex(ValueError, "not healthy"):
            run.deploy({API_TARGET: 200})

        self.assert_not_published(run)
        self.assertEqual(run.targets.requests, [])

    def test_container_still_starting_after_two_minutes_is_refused_before_any_probe(self) -> None:
        run = ReleaseRun(self, [API_PROBE])
        run.health = "starting"

        with self.assertRaisesRegex(ValueError, "not healthy"):
            run.deploy({API_TARGET: 200})

        self.assertEqual(run.clock.now, HEALTH_WAIT)
        self.assert_not_published(run)
        self.assertEqual(run.targets.requests, [])

    def test_health_wait_cannot_outlive_the_coolify_deadline(self) -> None:
        run = ReleaseRun(self, [API_PROBE])
        run.health = "starting"
        run.coolify_busy_for = COOLIFY_DEADLINE - HEALTH_WAIT / 4

        with self.assertRaisesRegex(ValueError, "not healthy"):
            run.deploy({API_TARGET: 200})

        self.assertEqual(run.clock.now, COOLIFY_DEADLINE)
        self.assert_not_published(run)

    def test_coolify_that_never_finishes_times_out_after_twenty_minutes(self) -> None:
        run = ReleaseRun(self, [API_PROBE])
        run.coolify_busy_for = float("inf")

        with self.assertRaisesRegex(ValueError, "Coolify timed out"):
            run.deploy({API_TARGET: 200})

        self.assertEqual(run.clock.now, COOLIFY_DEADLINE)
        self.assertEqual(run.inspected, [])
        self.assertEqual(run.targets.requests, [])
        self.assert_not_published(run)

    def test_coolify_finishing_on_the_last_poll_still_reaches_the_probe(self) -> None:
        run = ReleaseRun(self, [API_PROBE])
        run.coolify_busy_for = COOLIFY_DEADLINE - 5

        run.deploy({API_TARGET: 200})

        self.assertLess(run.clock.now, COOLIFY_DEADLINE)
        self.assertEqual(run.targets.requests, [API_REQUEST])
        self.assert_published(run)


if __name__ == "__main__":
    unittest.main()
