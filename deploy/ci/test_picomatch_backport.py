import copy
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest

import picomatch_backport as gate


ROOT = Path(__file__).resolve().parents[2]
VERSION = "0.76.2"


def advisory(identifier="CVE-2026-112366", package=gate.PACKAGE, version=gate.VERSION):
    return {
        "VulnerabilityID": identifier, "PkgName": package, "InstalledVersion": version,
        "PkgIdentifier": {"PURL": f"pkg:npm/{package}@{version}"}, "Severity": "HIGH",
    }


def report(*findings):
    return {
        "SchemaVersion": 2, "Trivy": {"Version": VERSION},
        "ArtifactName": ".", "ArtifactType": "filesystem",
        "Results": [{
            "Target": gate.LOCK, "Class": "lang-pkgs", "Type": "npm",
            "Packages": [{
                "Name": gate.PACKAGE, "Version": gate.VERSION, "Identifier": {"PURL": gate.PURL},
            }],
            "Vulnerabilities": list(findings),
        }],
    }


def evaluated(*fixed, active=()):
    policy = report(*active)
    policy["Results"][0]["ExperimentalModifiedFindings"] = [
        {"Source": gate.VEX_SOURCE, "Status": "fixed", "Type": "vulnerability", "Finding": item}
        for item in fixed
    ]
    return policy


class BackportNpm(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.lock = json.loads((ROOT / gate.LOCK).read_bytes())
        cls.manifest = json.loads((ROOT / gate.MANIFEST).read_bytes())
        cls.vendor = (ROOT / gate.VENDOR).read_bytes()
        cls.hashes = cls.manifest["backport"]["files"]

    def test_lock_and_vendor_are_exact(self):
        gate.verify_lock(self.lock, self.manifest["tarball"]["integrity"])
        gate.verify_archive(self.vendor, self.manifest)

    def test_original_registry_pin_is_rejected(self):
        lock = copy.deepcopy(self.lock)
        original = self.manifest["upstream"]
        lock["packages"]["node_modules/picomatch"].update(
            resolved=original["tarball"], integrity=original["integrity"],
        )
        with self.assertRaisesRegex(ValueError, "lockfile não fixa"):
            gate.verify_lock(lock, self.manifest["tarball"]["integrity"])

    def test_nested_copy_is_rejected(self):
        lock = copy.deepcopy(self.lock)
        lock["packages"]["node_modules/vite/node_modules/picomatch"] = {
            "version": "4.0.7",
        }
        with self.assertRaisesRegex(ValueError, "uma única entrada"):
            gate.verify_lock(lock, self.manifest["tarball"]["integrity"])

    def test_tarball_with_changed_bytes_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "integridade do tarball"):
            gate.verify_archive(self.vendor + b"\x00", self.manifest)

    def test_changed_installed_parser_is_rejected(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            installed = root / gate.INSTALLED
            with tarfile.open(fileobj=io.BytesIO(self.vendor)) as archive:
                for name in self.hashes:
                    path = installed / name
                    path.parent.mkdir(parents=True, exist_ok=True)
                    path.write_bytes(archive.extractfile("package/" + name).read())
            gate.verify_installed(root, self.hashes)
            with (installed / "lib/parse.js").open("ab") as parser:
                parser.write(b"\n")
            with self.assertRaisesRegex(ValueError, "arquivos instalados divergem"):
                gate.verify_installed(root, self.hashes)

    def test_actual_node_resolver_rejects_shadowed_consumer(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            modules = root / "web/node_modules"
            for name in (gate.PACKAGE, *gate.CONSUMERS):
                package = modules / name
                package.mkdir(parents=True)
                (package / "package.json").write_text("{}")
            (modules / gate.PACKAGE / "index.js").touch()
            got = gate.verify_resolution(root)
            self.assertEqual(got["resolved"], dict.fromkeys(gate.CONSUMERS, f"{gate.INSTALLED}/index.js"))
            shadow = modules / "vite/node_modules/picomatch"
            shadow.mkdir(parents=True)
            (shadow / "index.js").touch()
            with self.assertRaisesRegex(ValueError, "consumidor resolveu outra cópia"):
                gate.verify_resolution(root)


class RelatoriosTrivy(unittest.TestCase):
    def test_accepts_exact_fix_and_no_advisories(self):
        known = advisory()
        gate.reconcile(report(known), evaluated(known), VERSION)
        gate.reconcile(report(), report(), VERSION)

    def test_new_cve_is_blocking_even_on_picomatch(self):
        known = advisory()
        future = advisory("CVE-2099-00001")
        with self.assertRaisesRegex(ValueError, "continuam ativas"):
            gate.reconcile(report(known, future), evaluated(known, active=(future,)), VERSION)
        with self.assertRaisesRegex(ValueError, "supressão fora"):
            gate.reconcile(report(known, future), evaluated(known, future), VERSION)
        with self.assertRaisesRegex(ValueError, "achado bruto sumiu"):
            gate.reconcile(report(known, future), evaluated(known), VERSION)

    def test_same_advisory_elsewhere_is_blocking(self):
        known = advisory()
        for foreign in (advisory(package="outro"), advisory(version="4.0.6")):
            with self.subTest(foreign=foreign):
                with self.assertRaisesRegex(ValueError, "supressão fora"):
                    gate.reconcile(report(known, foreign), evaluated(known, foreign), VERSION)
                with self.assertRaisesRegex(ValueError, "continuam ativas"):
                    gate.reconcile(report(known, foreign), evaluated(known, active=(foreign,)), VERSION)

    def test_source_and_inventory_drift_fail(self):
        known = advisory()
        policy = evaluated(known)
        policy["Results"][0]["ExperimentalModifiedFindings"][0]["Source"] = ".trivyignore"
        with self.assertRaisesRegex(ValueError, "supressão fora"):
            gate.reconcile(report(known), policy, VERSION)
        policy = evaluated(known)
        policy["Results"][0]["Packages"].append({"Name": "outro"})
        with self.assertRaisesRegex(ValueError, "inventário de pacotes mudou"):
            gate.reconcile(report(known), policy, VERSION)

    def test_missing_purl_and_wrong_target_are_rejected(self):
        for field, value, message in (
            ("Target", "outro/package-lock.json", "alvo inesperado"),
            ("Packages", [], "inventariar exatamente o PURL"),
        ):
            with self.subTest(field=field):
                raw = report()
                raw["Results"][0][field] = value
                with self.assertRaisesRegex(ValueError, message):
                    gate.reconcile(raw, report(), VERSION)


if __name__ == "__main__":
    unittest.main()
