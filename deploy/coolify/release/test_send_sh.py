"""Runs the real send.sh with fake crane and ssh on a private PATH: no network and no VPS."""

import io
import json
import re
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile
import unittest
from dataclasses import dataclass
from pathlib import Path
from typing import Any

RELEASE_DIR = Path(__file__).resolve().parent
SEND_SH = RELEASE_DIR / "send.sh"

SHA = "0123456789abcdef0123456789abcdef01234567"
TARGET = "cfrelease-stillon@vps.example.test"
NO_FAILURE = "__no_failure_requested__"
STILLON_TAG = "ghcr.io/bipoxdxd/stillon:" + SHA

CONFIG_HEX = "9d99a75171aea000c711b34c0e5e3f28d3d537dd99d110eafbfbc2bd8e52c2bf"
CONFIG = b'{"architecture":"amd64","os":"linux"}'
LAYER_HEX = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
MANIFEST_HEX = "72c6ad54950f6ec6331add34dcab71cebdbe90272bf9fa4cbce0e1212ae33a61"
MANIFEST = (
    b'{"schemaVersion":2,"config":{"digest":"sha256:' + CONFIG_HEX.encode() + b'","size":37},'
    b'"layers":[{"digest":"sha256:' + LAYER_HEX.encode() + b'","size":0,'
    b'"mediaType":"application/vnd.oci.image.layer.v1.tar"}]}'
)
IMAGE_DIGEST = "sha256:" + MANIFEST_HEX
STILLON_PINNED = "ghcr.io/bipoxdxd/stillon@" + IMAGE_DIGEST
OTHER_DIGEST = "sha256:" + "c" * 64

AMD64 = {"digest": IMAGE_DIGEST, "platform": {"os": "linux", "architecture": "amd64"}}
ARM64 = {"digest": OTHER_DIGEST, "platform": {"os": "linux", "architecture": "arm64"}}
ATTESTATION = {
    "digest": "sha256:" + "d" * 64,
    "platform": {"os": "unknown", "architecture": "unknown"},
}
SINGLE_MANIFEST = '{"schemaVersion":2,"config":{},"layers":[]}'

TOKEN_CANARY = "CANARY-env-value-5e1f"
KEY_FILE_CANARY = "CANARY-key-file-9b2c"
KEY_PATH_CANARY = "CANARY-key-path-3d7a"

JQ_HAS = 'has("manifests")'
FAILURE = 73


def index(*entries: dict[str, Any]) -> str:
    return json.dumps({"schemaVersion": 2, "manifests": list(entries)})


def real_tool(name: str) -> str:
    path = shutil.which(name)
    if path is None:
        raise RuntimeError(name + " is required to run send.sh for real")
    return path


@dataclass(frozen=True)
class Run:
    code: int
    out: str
    err: str
    calls: list[str]
    umasks: set[str]
    ssh_argv: list[str] | None
    payload: bytes | None
    leftovers: list[Path]

    @property
    def log(self) -> str:
        return self.out + self.err

    @property
    def ssh_called(self) -> bool:
        return any(call.startswith("ssh\t") for call in self.calls)

    def arguments_of(self, tool_and_verb: str) -> list[str]:
        prefix = tool_and_verb.replace(" ", "\t", 1) + " "
        return [call[len(prefix) :] for call in self.calls if call.startswith(prefix)]

    def effects(self) -> list[str]:
        return [label for label in map(effect_label, self.calls) if label is not None]

    def members(self) -> dict[str, bytes]:
        assert self.payload is not None
        result = {}
        with tarfile.open(fileobj=io.BytesIO(self.payload)) as archive:
            for member in archive.getmembers():
                extracted = archive.extractfile(member)
                if extracted is not None:
                    result[member.name.removeprefix("./")] = extracted.read()
        return result


def effect_label(call: str) -> str | None:
    tool, arguments = call.split("\t", 1)
    if tool == "python3":
        return "send.py " + arguments.split("/send.py ", 1)[1].split()[0]
    if tool == "crane":
        return "crane " + arguments.split()[0]
    return tool if tool in {"tar", "ssh", "rm"} else None


def install_tool(directory: Path, name: str, body: str) -> None:
    path = directory / name
    path.write_text(
        f"""#!/bin/sh
printf '%s\\t%s\\n' '{name}' "$*" >> "$BENCH_STATE/calls"
umask >> "$BENCH_STATE/umasks"
case "{name} $*" in *"$BENCH_FAIL_MATCH"*) exit "$BENCH_FAIL_CODE" ;; esac
{body}
"""
    )
    path.chmod(path.stat().st_mode | stat.S_IXUSR)


def install_tools(directory: Path) -> None:
    """Every tool send.sh may reach. Anything else, curl included, is command not found."""
    for name in ("dirname", "mkdir", "mv", "rm", "tar", "jq"):
        install_tool(directory, name, f'exec "{real_tool(name)}" "$@"')
    # BSD mktemp -d ignores TMPDIR, so the wrapper names the template explicitly.
    install_tool(directory, "mktemp", f'exec "{real_tool("mktemp")}" -d "$BENCH_TMP/tmp.XXXXXXXX"')
    install_tool(directory, "python3", f'exec "{sys.executable}" "$@"')
    cat = real_tool("cat")
    cp = real_tool("cp")
    install_tool(
        directory,
        "crane",
        f"""case "$1" in
  manifest) exec "{cat}" "$BENCH_STATE/manifest" ;;
  digest) exec "{cat}" "$BENCH_STATE/digest" ;;
  pull) for destination; do :; done; exec "{cp}" -R "$BENCH_STATE/layout" "$destination" ;;
esac
exit 64""",
    )
    install_tool(
        directory,
        "ssh",
        f"""for argument; do printf '%s\\n' "$argument"; done > "$BENCH_STATE/ssh-argv"
exec "{cat}" > "$BENCH_STATE/ssh-stdin\"""",
    )


class Bench:
    """One TMPDIR, registry reply and call log per run; the fake tools are shared and stateless."""

    def __init__(self, root: Path, bin_directory: Path) -> None:
        self.root = root
        self.bin = bin_directory
        self.tmp = root / "tmp"
        self.state = root / "state"
        for directory in (self.tmp, self.state):
            directory.mkdir()
        self.key = root / KEY_PATH_CANARY / "key"
        self.key.parent.mkdir()
        self.key.write_text(KEY_FILE_CANARY)
        self.known_hosts = root / "known_hosts"
        self.known_hosts.write_text("vps.example.test ssh-ed25519 AAAA\n")
        self.write_layout()
        self.reply_manifest(index(AMD64, ARM64, ATTESTATION))
        self.reply_digest(IMAGE_DIGEST)

    def write_layout(self, digest: str = IMAGE_DIGEST) -> None:
        layout = self.state / "layout"
        blobs = layout / "blobs/sha256"
        blobs.mkdir(parents=True, exist_ok=True)
        (blobs / CONFIG_HEX).write_bytes(CONFIG)
        (blobs / LAYER_HEX).write_bytes(b"")
        (blobs / MANIFEST_HEX).write_bytes(MANIFEST)
        (layout / "oci-layout").write_text('{"imageLayoutVersion":"1.0.0"}')
        descriptor = {
            "digest": digest,
            "size": len(MANIFEST),
            "artifactType": "application/vnd.example.unsupported",
        }
        document = {"schemaVersion": 2, "manifests": [descriptor]}
        (layout / "index.json").write_text(json.dumps(document))

    def reply_manifest(self, text: str) -> None:
        (self.state / "manifest").write_text(text)

    def reply_digest(self, text: str) -> None:
        (self.state / "digest").write_text(text)

    def run(
        self,
        *,
        overrides: dict[str, str] | None = None,
        unset: tuple[str, ...] = (),
        fail: tuple[str, int] | None = None,
    ) -> Run:
        match, code = fail if fail is not None else (NO_FAILURE, 0)
        env = {
            "PATH": str(self.bin),
            "TMPDIR": str(self.tmp),
            "HOME": str(self.root),
            "LC_ALL": "C",
            "PYTHONDONTWRITEBYTECODE": "1",
            "BENCH_STATE": str(self.state),
            "BENCH_TMP": str(self.tmp),
            "COPYFILE_DISABLE": "1",
            "BENCH_FAIL_MATCH": match,
            "BENCH_FAIL_CODE": str(code),
            "RELEASE_COMMIT": SHA,
            "RELEASE_PROJECT": "stillon",
            "DEPLOY_TARGET": TARGET,
            "DEPLOY_KEY": str(self.key),
            "DEPLOY_KNOWN_HOSTS": str(self.known_hosts),
            "GH_TOKEN": TOKEN_CANARY,
            "SSH_KEY": TOKEN_CANARY,
            "KNOWN_HOSTS": TOKEN_CANARY,
        }
        env.update(overrides or {})
        for name in unset:
            del env[name]
        done = subprocess.run(
            [real_tool("bash"), str(SEND_SH)],
            env=env,
            cwd=self.root,
            stdin=subprocess.DEVNULL,
            capture_output=True,
            text=True,
            timeout=60,
            umask=0o022,
            check=False,
        )
        return Run(
            code=done.returncode,
            out=done.stdout,
            err=done.stderr,
            calls=self.normalized_calls(),
            umasks=set(self.read_text("umasks").split()),
            ssh_argv=self.read_text("ssh-argv").splitlines() or None,
            payload=self.read_bytes("ssh-stdin"),
            leftovers=list(self.tmp.iterdir()),
        )

    def normalized_calls(self) -> list[str]:
        text = re.sub(re.escape(str(self.tmp)) + r"/[^/\s]+", "$STAGE", self.read_text("calls"))
        return text.replace(str(RELEASE_DIR), "$SCRIPT").splitlines()

    def read_text(self, name: str) -> str:
        path = self.state / name
        return path.read_text() if path.exists() else ""

    def read_bytes(self, name: str) -> bytes | None:
        path = self.state / name
        return path.read_bytes() if path.exists() else None

    def raw_calls(self) -> str:
        return self.read_text("calls")


class SendShTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        directory = tempfile.TemporaryDirectory()
        cls.addClassCleanup(directory.cleanup)
        cls.bin_directory = Path(directory.name).resolve()
        install_tools(cls.bin_directory)

    def bench(self) -> Bench:
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        return Bench(Path(directory.name).resolve(), self.bin_directory)

    def goblin_overrides(self, bench: Bench) -> dict[str, str]:
        scanned = bench.root / "scanned.json"
        scanned.write_text(
            json.dumps(
                {
                    "app": "ghcr.io/bipoxdxd/goblinshift@" + IMAGE_DIGEST,
                    "web": "ghcr.io/bipoxdxd/goblinshift-web@" + IMAGE_DIGEST,
                    "executor": "ghcr.io/bipoxdxd/goblinshift-executor@" + IMAGE_DIGEST,
                }
            )
        )
        return {
            "RELEASE_PROJECT": "goblinshift",
            "DEPLOY_TARGET": "cfrelease-goblinshift@vps.example.test",
            "SCANNED_IMAGES_FILE": str(scanned),
        }

    def assert_refused_before_any_effect(self, run: Run, code: int) -> None:
        self.assertEqual(run.code, code, run.err)
        self.assertEqual(run.effects(), [])
        self.assertEqual(run.leftovers, [])

    def assert_nothing_sent(self, run: Run, code: int) -> None:
        self.assertEqual(run.code, code, run.err)
        self.assertFalse(run.ssh_called, run.calls)
        self.assertIsNone(run.payload)
        self.assertEqual(run.leftovers, [])

    # The exit status of ${name:?} is bash's own choice and varies by context, so only "not 0".
    def test_release_inputs_that_are_missing_or_empty_stop_before_any_effect(self) -> None:
        for name in ("RELEASE_COMMIT", "RELEASE_PROJECT", "DEPLOY_TARGET"):
            for overrides, unset in (({name: ""}, ()), ({}, (name,))):
                with self.subTest(name=name, unset=unset):
                    run = self.bench().run(overrides=overrides, unset=unset)
                    self.assertNotEqual(run.code, 0, run.calls)
                    self.assertEqual(run.effects(), [])
                    self.assertEqual(run.leftovers, [])
                    self.assertIn(name, run.err)

    def test_ssh_settings_that_are_missing_or_empty_never_reach_ssh(self) -> None:
        for name in ("DEPLOY_KEY", "DEPLOY_KNOWN_HOSTS"):
            for overrides, unset in (({name: ""}, ()), ({}, (name,))):
                with self.subTest(name=name, unset=unset):
                    run = self.bench().run(overrides=overrides, unset=unset)
                    self.assertNotEqual(run.code, 0, run.calls)
                    self.assertFalse(run.ssh_called, run.calls)
                    self.assertIsNone(run.payload)
                    self.assertEqual(run.leftovers, [])
                    self.assertIn(name, run.err)

    def test_commit_that_is_not_a_full_lowercase_sha_is_refused(self) -> None:
        for commit in ("a" * 39, "a" * 41, "A" * 40, "g" * 40, "a" * 40 + "\n"):
            with self.subTest(commit=commit):
                run = self.bench().run(overrides={"RELEASE_COMMIT": commit})
                self.assert_refused_before_any_effect(run, 1)

    def test_unknown_project_is_refused_with_its_own_exit_code(self) -> None:
        for project in ("unknown", "STILLON", "stillon "):
            with self.subTest(project=project):
                run = self.bench().run(overrides={"RELEASE_PROJECT": project})
                self.assert_refused_before_any_effect(run, 2)

    def test_each_known_project_passes_the_project_gate(self) -> None:
        for project in ("stillon", "goblinshift", "duora"):
            with self.subTest(project=project):
                run = self.bench().run(
                    overrides={
                        "RELEASE_PROJECT": project,
                        "DEPLOY_TARGET": f"cfrelease-{project}@vps.example.test",
                    },
                    fail=("send.py requested", FAILURE),
                )
                self.assertEqual(run.code, FAILURE, run.err)
                self.assertEqual(run.effects(), ["send.py requested", "rm"])

    def test_target_outside_the_project_restricted_account_is_refused(self) -> None:
        targets = (
            "cfrelease-duora@vps.example.test",
            "root@vps.example.test",
            "xcfrelease-stillon@vps.example.test",
            "cfrelease-stillon@",
            "cfrelease-stillon@vps;id",
            "cfrelease-stillon@vps example",
        )
        for target in targets:
            with self.subTest(target=target):
                run = self.bench().run(overrides={"DEPLOY_TARGET": target})
                self.assert_refused_before_any_effect(run, 1)

    def test_failure_of_each_external_command_is_the_exit_code_and_nothing_is_sent(self) -> None:
        cases = (
            ("mktemp", "mktemp -d", index(AMD64)),
            ("mkdir", "mkdir ", index(AMD64)),
            ("metadata request", "send.py requested", index(AMD64)),
            ("registry manifest", "crane manifest", index(AMD64)),
            ("platform selection", "jq -er", index(AMD64)),
            ("single image digest", "crane digest", SINGLE_MANIFEST),
            ("image pull", "crane pull", index(AMD64)),
            ("index normalization", "jq -c", index(AMD64)),
            ("normalized index move", "mv ", index(AMD64)),
            ("release manifest", "send.py manifest", index(AMD64)),
            ("archive", "tar ", index(AMD64)),
        )
        for name, match, manifest in cases:
            with self.subTest(name=name):
                bench = self.bench()
                bench.reply_manifest(manifest)
                run = bench.run(fail=(match, FAILURE))
                self.assert_nothing_sent(run, FAILURE)

    def test_ssh_failure_is_the_exit_code_and_the_stage_is_removed(self) -> None:
        run = self.bench().run(fail=("ssh ", 255))
        self.assertEqual(run.code, 255, run.err)
        self.assertEqual(run.effects()[-2:], ["ssh", "rm"])
        self.assertEqual(run.leftovers, [])

    def test_script_directory_failure_never_reaches_ssh(self) -> None:
        run = self.bench().run(fail=("dirname ", FAILURE))
        self.assertNotEqual(run.code, 0)
        self.assertFalse(run.ssh_called, run.calls)

    def test_cleanup_failure_after_delivery_is_still_reported(self) -> None:
        run = self.bench().run(fail=("rm -rf", FAILURE))
        self.assertEqual(run.code, FAILURE, run.err)
        self.assertEqual(run.effects()[-2:], ["ssh", "rm"])

    # Defect: a jq failure inside the `if` reads as "not an index" instead of stopping the release.
    def test_manifest_that_jq_cannot_read_stops_before_publishing(self) -> None:
        bench = self.bench()
        bench.reply_manifest("this is not json")
        run = bench.run()
        self.assertNotEqual(run.code, 0, run.calls)
        self.assertFalse(run.ssh_called, run.calls)

    # Defect: same `if`, with jq itself crashing.
    def test_jq_crash_while_classifying_the_manifest_stops_before_publishing(self) -> None:
        run = self.bench().run(fail=(JQ_HAS, FAILURE))
        self.assertNotEqual(run.code, 0, run.calls)
        self.assertFalse(run.ssh_called, run.calls)

    def test_effects_happen_in_contract_order_and_ssh_is_the_last_delivery_step(self) -> None:
        run = self.bench().run()
        self.assertEqual(run.code, 0, run.err)
        self.assertEqual(
            run.effects(),
            [
                "send.py requested",
                "crane manifest",
                "crane pull",
                "send.py manifest",
                "tar",
                "ssh",
                "rm",
            ],
        )

    def test_success_exits_zero_and_leaves_no_stage_behind(self) -> None:
        run = self.bench().run()
        self.assertEqual(run.code, 0, run.err)
        self.assertEqual(run.leftovers, [])

    def test_every_external_command_runs_under_the_private_umask(self) -> None:
        run = self.bench().run()
        self.assertEqual(run.umasks, {"0077"})

    def test_ssh_is_called_with_exactly_the_pinned_options(self) -> None:
        bench = self.bench()
        run = bench.run()
        self.assertEqual(
            run.ssh_argv,
            [
                "-o",
                "BatchMode=yes",
                "-o",
                "IdentitiesOnly=yes",
                "-o",
                "StrictHostKeyChecking=yes",
                "-i",
                str(bench.key),
                "-o",
                f"UserKnownHostsFile={bench.known_hosts}",
                TARGET,
                "release " + SHA,
            ],
        )

    def test_image_is_resolved_from_the_commit_tag_and_pulled_by_digest(self) -> None:
        run = self.bench().run()
        self.assertEqual(run.arguments_of("crane manifest"), [STILLON_TAG])
        self.assertEqual(
            run.arguments_of("crane pull"),
            [f"--platform linux/amd64 --format oci {STILLON_PINNED} $STAGE/images/app"],
        )

    def test_amd64_entry_is_chosen_wherever_it_sits_in_the_index(self) -> None:
        for entries in ((AMD64, ARM64, ATTESTATION), (ATTESTATION, ARM64, AMD64)):
            with self.subTest(first=entries[0]["platform"]):
                bench = self.bench()
                bench.reply_manifest(index(*entries))
                run = bench.run()
                self.assertEqual(run.code, 0, run.err)
                self.assertEqual(
                    run.arguments_of("crane pull"),
                    [f"--platform linux/amd64 --format oci {STILLON_PINNED} $STAGE/images/app"],
                )

    def test_index_without_exactly_one_linux_amd64_entry_is_refused(self) -> None:
        windows = {"digest": OTHER_DIGEST, "platform": {"os": "windows", "architecture": "amd64"}}
        cases = {
            "no amd64": index(ARM64),
            "amd64 on another system": index(windows),
            "two amd64": index(AMD64, {**AMD64, "digest": OTHER_DIGEST}),
            "empty": index(),
        }
        for name, manifest in cases.items():
            with self.subTest(name=name):
                bench = self.bench()
                bench.reply_manifest(manifest)
                run = bench.run()
                self.assert_nothing_sent(run, 5)
                self.assertEqual(run.arguments_of("crane pull"), [])

    def test_single_image_is_pinned_by_the_digest_the_registry_reports(self) -> None:
        bench = self.bench()
        bench.reply_manifest(SINGLE_MANIFEST)
        run = bench.run()
        self.assertEqual(run.code, 0, run.err)
        self.assertEqual(run.arguments_of("crane digest"), [STILLON_TAG])
        self.assertEqual(
            run.arguments_of("crane pull"),
            [f"--platform linux/amd64 --format oci {STILLON_PINNED} $STAGE/images/app"],
        )

    def test_malformed_digest_is_never_pulled(self) -> None:
        malformed = (
            "sha256:" + "A" * 64,
            "sha256:" + "a" * 63,
            "sha256:" + "a" * 65,
            "md5:" + "a" * 64,
        )
        for digest in malformed:
            with self.subTest(source="index", digest=digest):
                bench = self.bench()
                bench.reply_manifest(index({**AMD64, "digest": digest}))
                run = bench.run()
                self.assert_nothing_sent(run, 1)
                self.assertEqual(run.arguments_of("crane pull"), [])
            with self.subTest(source="registry", digest=digest):
                bench = self.bench()
                bench.reply_manifest(SINGLE_MANIFEST)
                bench.reply_digest(digest)
                run = bench.run()
                self.assert_nothing_sent(run, 1)
                self.assertEqual(run.arguments_of("crane pull"), [])

    def test_delivered_archive_is_the_validated_release_and_nothing_else(self) -> None:
        run = self.bench().run()
        self.assertEqual(run.code, 0, run.err)
        members = run.members()
        self.assertEqual({name.split("/")[0] for name in members}, {"release.json", "app"})
        reviewed = json.loads((RELEASE_DIR / "receiver-release.json").read_text())["sha256"]
        self.assertEqual(
            json.loads(members["release.json"]),
            {
                "version": 2,
                "receiver_sha256": reviewed,
                "project": "stillon",
                "commit": SHA,
                "images": {"app": STILLON_PINNED},
            },
        )

    def test_unsupported_index_field_is_removed_before_validation(self) -> None:
        run = self.bench().run()
        self.assertEqual(run.code, 0, run.err)
        self.assertEqual(
            json.loads(run.members()["app/index.json"]),
            {"schemaVersion": 2, "manifests": [{"digest": IMAGE_DIGEST, "size": 281}]},
        )

    def test_pulled_image_with_another_digest_is_refused_before_ssh(self) -> None:
        bench = self.bench()
        bench.write_layout(digest=OTHER_DIGEST)
        run = bench.run()
        self.assert_nothing_sent(run, 1)
        self.assertEqual(run.effects()[-2:], ["send.py manifest", "rm"])

    def test_every_role_of_a_project_is_pulled_and_delivered(self) -> None:
        bench = self.bench()
        run = bench.run(overrides=self.goblin_overrides(bench))
        self.assertEqual(run.code, 0, run.err)
        self.assertEqual(
            run.arguments_of("crane pull"),
            [
                f"--platform linux/amd64 --format oci ghcr.io/bipoxdxd/goblinshift{suffix}"
                f"@{IMAGE_DIGEST} $STAGE/images/{role}"
                for role, suffix in (("app", ""), ("executor", "-executor"), ("web", "-web"))
            ],
        )
        self.assertEqual(
            json.loads(run.members()["release.json"])["images"],
            {
                "app": "ghcr.io/bipoxdxd/goblinshift@" + IMAGE_DIGEST,
                "executor": "ghcr.io/bipoxdxd/goblinshift-executor@" + IMAGE_DIGEST,
                "web": "ghcr.io/bipoxdxd/goblinshift-web@" + IMAGE_DIGEST,
            },
        )

    def test_failure_on_a_later_role_stops_the_whole_release(self) -> None:
        bench = self.bench()
        run = bench.run(
            overrides=self.goblin_overrides(bench),
            fail=("oci ghcr.io/bipoxdxd/goblinshift-executor", FAILURE),
        )
        self.assert_nothing_sent(run, FAILURE)
        self.assertEqual(len(run.arguments_of("crane pull")), 2)
        self.assertEqual(
            run.arguments_of("crane manifest")[-1:],
            ["ghcr.io/bipoxdxd/goblinshift-executor@" + IMAGE_DIGEST],
        )

    def test_secrets_from_the_environment_never_reach_the_log_or_the_vps(self) -> None:
        for fail in (None, ("crane manifest", FAILURE), ("tar ", FAILURE), ("ssh ", 255)):
            with self.subTest(fail=fail):
                bench = self.bench()
                run = bench.run(fail=fail)
                self.assertNotIn(TOKEN_CANARY, run.log)
                self.assertNotIn(KEY_FILE_CANARY, run.log)
                self.assertNotIn(TOKEN_CANARY, bench.raw_calls())
                self.assertNotIn(KEY_FILE_CANARY, bench.raw_calls())
                self.assertNotIn(TOKEN_CANARY.encode(), run.payload or b"")
                self.assertNotIn(KEY_FILE_CANARY.encode(), run.payload or b"")

    def test_private_key_path_goes_to_ssh_and_never_to_the_log(self) -> None:
        for fail in (None, ("ssh ", 255)):
            with self.subTest(fail=fail):
                bench = self.bench()
                run = bench.run(fail=fail)
                self.assertIn(KEY_PATH_CANARY, bench.raw_calls())
                self.assertNotIn(KEY_PATH_CANARY, run.log)


if __name__ == "__main__":
    unittest.main()
