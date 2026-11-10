import hashlib
import json
import unittest
from pathlib import Path

MANIFEST = Path(__file__).with_name("preserved-assets.json")

# As mídias originais estão congeladas no source_commit do manifesto. Só tirar
# uma de propósito muda este número.
ORIGINAL_ASSETS = 46

# SHA-256 das linhas "nome digest" do manifesto, em ordem de nome. A contagem não
# vê a troca de uma mídia por outra. Acrescentar mídia ao manifesto também muda
# este valor: atualize-o no mesmo commit, de propósito.
ORIGINAL_ASSETS_SHA256 = "33aed7f47c9ed92f459ee8c914e3cc6407ada7f6899d9811920f8e514b46a05c"


class PreservedAssetsManifestTests(unittest.TestCase):
    def test_manifest_still_lists_every_original_asset(self) -> None:
        # assets.py percorre o manifesto e passa com "assets": {}: esvaziar ou
        # encolher o arquivo desligaria a guarda sem nenhum erro.
        assets = json.loads(MANIFEST.read_text())["assets"]

        self.assertGreaterEqual(len(assets), ORIGINAL_ASSETS)

    def test_manifest_keeps_the_original_name_digest_pairs(self) -> None:
        assets = json.loads(MANIFEST.read_text())["assets"]
        linhas = "".join(f"{nome} {digest}\n" for nome, digest in sorted(assets.items()))

        self.assertEqual(hashlib.sha256(linhas.encode()).hexdigest(), ORIGINAL_ASSETS_SHA256)


if __name__ == "__main__":
    unittest.main()
