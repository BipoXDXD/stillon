"""Run only the snapshot/guard from checar-vps.sh with synthetic Docker output.

Docker/Compose are not invoked. Bash and jq execute the actual extracted guard.
The double implements only the two inspect projection formats, without sorting.
"""

import copy
import itertools
import json
from pathlib import Path
import re
import subprocess
import sys
import unittest


SOURCE = Path(sys.argv.pop(1)) if len(sys.argv) > 1 else Path(__file__).with_name("checar-vps.sh")
BASE = {
    "Id": "fixture-container-a",
    "StartedAt": "2026-10-25T00:00:00.000000001Z",
    "RestartCount": 0,
    "Mounts": [
        {"Type": "volume", "Name": "fixture-volume", "Source": "/fixture/PRIVATE_SENTINEL", "Destination": "/var/lib/postgresql/data", "Driver": "local", "Mode": "z", "RW": True, "Propagation": ""},
        {"Type": "bind", "Source": "/fixture/initdb.sh", "Destination": "/docker-entrypoint-initdb.d/initdb.sh", "Mode": "ro", "RW": False, "Propagation": "rprivate"},
        {"Type": "tmpfs", "Source": "", "Destination": "/run/postgresql", "Mode": "", "RW": True, "Propagation": ""},
    ],
}


def compare(before, after, provider_status=0):
    source = SOURCE.read_text()
    functions = source[source.index("snapshot_banco_backup() {"):source.index("banco_antes_backup=$(snapshot_banco_backup)")]
    guard_start = source.index('ok "stillon-entrada backup: nenhum volume sobra dos descartáveis"')
    guard = source[source.index("\n", guard_start) + 1:source.index('[ "$(sql "SELECT md5', guard_start)]
    shell = r'''
set -euo pipefail
dc() { [[ "$*" = 'ps -q db' ]] && printf '%s\n' fixture-container; }
docker() {
    [[ "$#" = 4 && "$1" = inspect && "$2" = -f && "$4" = fixture-container ]] || return 81
    [[ "$fixture_status" = 0 ]] || return "$fixture_status"
    case "$3" in
        '{{.Id}}|{{.State.StartedAt}}|{{.RestartCount}}|{{json .Mounts}}')
            printf '%s\n' "$fixture" | jq -r '.Id + "|" + .StartedAt + "|" + (.RestartCount|tostring) + "|" + (.Mounts|tojson)' ;;
        '{"Id":{{json .Id}},"StartedAt":{{json .State.StartedAt}},"RestartCount":{{json .RestartCount}},"Mounts":{{json .Mounts}}}')
            printf '%s\n' "$fixture" ;;
        *) return 82 ;;
    esac
}
falhas=0
falhar() { printf 'FALHOU: %s\n' "$*"; falhas=$((falhas + 1)); }
fixture_status=0
IFS= read -r fixture
'''
    shell += functions + '\nbanco_antes_backup=$(snapshot_banco_backup)\nIFS= read -r fixture\n'
    shell += f'fixture_status={provider_status}\n' + guard + '\nprintf "failures=%s\\n" "$falhas"\n'
    result = subprocess.run(
        ["/bin/bash", "-c", shell],
        input=json.dumps(before) + "\n" + json.dumps(after) + "\n",
        text=True, capture_output=True, timeout=5,
        env={"PATH": "/opt/homebrew/bin:/usr/bin:/bin", "LC_ALL": "C"},
    )
    if result.returncode != 0:
        raise AssertionError(f"guard execution exited {result.returncode}")
    if result.stderr:
        raise AssertionError("guard emitted unexpected stderr")
    failures = re.search(r"^failures=(\d+)$", result.stdout, re.M)
    if failures is None:
        raise AssertionError("guard result missing")
    return int(failures[1]), result.stdout


class SnapshotGuardTests(unittest.TestCase):
    def test_mount_order_alone_is_irrelevant(self):
        after = copy.deepcopy(BASE)
        after["Mounts"].reverse()
        failures, _ = compare(BASE, after)
        self.assertEqual(failures, 0)

    def test_every_mount_permutation_and_object_key_order_is_equivalent(self):
        for order in itertools.permutations(BASE["Mounts"]):
            with self.subTest(order=[m["Destination"] for m in order]):
                after = copy.deepcopy(BASE)
                after["Mounts"] = [dict(reversed(list(m.items()))) for m in order]
                failures, output = compare(BASE, after)
                self.assertEqual(failures, 0)
                self.assertNotIn("PRIVATE_SENTINEL", output)

    def test_id_started_at_and_restart_changes_fail_and_name_only_changed_fields(self):
        for field, value in (("Id", "fixture-container-b"), ("StartedAt", "2026-10-25T00:00:00.000000002Z"), ("RestartCount", 17)):
            with self.subTest(field=field):
                after = copy.deepcopy(BASE)
                after[field] = value
                failures, output = compare(BASE, after)
                self.assertEqual(failures, 1)
                self.assertIn(json.dumps([field], separators=(",", ":")), output)
                self.assertNotIn("PRIVATE_SENTINEL", output)
                self.assertNotIn(str(value), output)

    def test_every_mount_field_addition_removal_and_multiplicity_change_fails(self):
        mutations = []
        for field, value in (("Type", "bind"), ("Name", "another-volume"), ("Source", "/changed"), ("Destination", "/changed"), ("Driver", "other"), ("Mode", "ro"), ("RW", False), ("Propagation", "private")):
            after = copy.deepcopy(BASE)
            after["Mounts"][0][field] = value
            mutations.append((field, after))
        for label, change in (("removed-field", lambda ms: ms[0].pop("Name")), ("added-field", lambda ms: ms[0].update(FutureField={"a": [1, 2]})), ("removed-mount", lambda ms: ms.pop()), ("added-mount", lambda ms: ms.append(copy.deepcopy(ms[0])))):
            after = copy.deepcopy(BASE)
            change(after["Mounts"])
            mutations.append((label, after))
        before = copy.deepcopy(BASE)
        before["Mounts"][0]["FutureField"] = {"a": [1, 2]}
        after = copy.deepcopy(before)
        after["Mounts"][0]["FutureField"]["a"].reverse()
        mutations.append(("nested-array-order", after))
        for label, after in mutations:
            with self.subTest(change=label):
                origin = before if label == "nested-array-order" else BASE
                failures, output = compare(origin, after)
                self.assertEqual(failures, 1)
                self.assertIn('["Mounts"]', output)
                self.assertNotIn("PRIVATE_SENTINEL", output)
                self.assertNotIn("/changed", output)

    def test_multiple_changes_report_exact_closed_field_names_without_values(self):
        after = copy.deepcopy(BASE)
        after.update(Id="different", StartedAt="different", RestartCount=42, Mounts=[])
        failures, output = compare(BASE, after)
        self.assertEqual(failures, 1)
        self.assertIn('["Id","StartedAt","RestartCount","Mounts"]', output)
        self.assertNotIn("different", output)
        self.assertNotIn("PRIVATE_SENTINEL", output)

    def test_invalid_snapshot_and_inspect_failure_fail_closed(self):
        for field, value in (("Id", None), ("StartedAt", None), ("RestartCount", "0"), ("Mounts", None), ("Mounts", [None])):
            with self.subTest(field=field, value=value):
                after = copy.deepcopy(BASE)
                after[field] = value
                failures, output = compare(BASE, after)
                self.assertEqual(failures, 1)
                self.assertNotIn("PRIVATE_SENTINEL", output)
        failures, output = compare(BASE, BASE, provider_status=1)
        self.assertEqual(failures, 1)
        self.assertNotIn("PRIVATE_SENTINEL", output)


if __name__ == "__main__":
    suite = unittest.defaultTestLoader.loadTestsFromTestCase(SnapshotGuardTests)
    result = unittest.TestResult()
    suite.run(result)
    print(json.dumps({"tests": result.testsRun, "failures": len(result.failures), "errors": len(result.errors), "skipped": len(result.skipped), "failed_methods": [test.id() for test, _ in result.failures], "errored_methods": [test.id() for test, _ in result.errors], "successful": result.wasSuccessful()}, sort_keys=True))
    sys.exit(0 if result.wasSuccessful() else 1)
