"""O ensaio do piso não leva à pilha a chave do Resend nem os alvos extras.

O ensaiar-piso.sh roda de verdade, numa cópia do repositório com um .env de
canário (onde o dono guarda a chave real). Um `docker` falso no PATH aceita a
inspeção das imagens e o `down`; no `docker compose up`, ele roda o
`docker compose config` real, com o ambiente que o script montou, e falha para
o script parar ali. Nada sobe: o config só resolve o compose.yaml. Sem o
docker compose, o teste falha (não pula).
"""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import tempfile
import unittest
import uuid
from pathlib import Path
from typing import Any

ROOT = next(p for p in Path(__file__).resolve().parents if (p / ".git").exists())
SCRIPT = ROOT / "deploy" / "ci" / "ensaiar-piso.sh"
COMPOSE = ROOT / "compose.yaml"
SHELL = shutil.which("sh") or "/bin/sh"
# Onde o compose.yaml usa cada variável: o app envia e-mail; o seed cadastra
# os alvos extras, que o sondador visitaria.
SERVICO = {"RESEND_API_KEY": "app", "EMAIL_FROM": "app", "DEMO_TARGETS_EXTRA": "seed"}
VARIAVEIS = tuple(SERVICO)

DOCKER_FALSO = """#!/bin/sh
case "$1 $2" in
    "image inspect"|"compose down"|"compose logs") exit 0 ;;
    "compose up")
        "$DOCKER_REAL" compose config --format json >"$CONFIG_SAIDA" || exit 3
        exit 1 ;;
esac
echo "docker falso: chamada inesperada: $*" >&2
exit 4
"""

# O script exige curl e jq no PATH, mas para antes de usá-los.
FERRAMENTA_INERTE = "#!/bin/sh\necho \"chamada inesperada: $0 $*\" >&2\nexit 4\n"


class EnsaioDoPisoSemSegredoTests(unittest.TestCase):
    def setUp(self) -> None:
        docker = shutil.which("docker")
        if docker is None:
            self.fail("falta o docker: este teste precisa do `docker compose config`")
        self.docker_real = docker
        versao = subprocess.run(  # noqa: S603 - argumentos fixos
            [docker, "compose", "version"], capture_output=True, text=True, check=False, timeout=60
        )
        self.assertEqual(versao.returncode, 0, f"falta o docker compose: {versao.stderr}")

        diretorio = tempfile.TemporaryDirectory(prefix="ensaio-piso-env-")
        self.addCleanup(diretorio.cleanup)
        base = Path(diretorio.name)
        self.raiz = base / "repo"
        (self.raiz / "deploy" / "ci").mkdir(parents=True)
        shutil.copy2(SCRIPT, self.raiz / "deploy" / "ci" / SCRIPT.name)
        shutil.copy2(COMPOSE, self.raiz / "compose.yaml")

        self.canario = f"CANARY-{uuid.uuid4()}"
        (self.raiz / ".env").write_text(
            f"RESEND_API_KEY=re_{self.canario}\n"
            f"EMAIL_FROM=StillOn <{self.canario}@exemplo.invalid>\n"
            f"DEMO_TARGETS_EXTRA=https://{self.canario.lower()}.exemplo.invalid\n"
        )

        self.binarios = base / "bin"
        self.binarios.mkdir()
        self.instalar("docker", DOCKER_FALSO)
        self.instalar("curl", FERRAMENTA_INERTE)
        self.instalar("jq", FERRAMENTA_INERTE)
        self.config_saida = base / "config.json"

    def instalar(self, nome: str, conteudo: str) -> None:
        caminho = self.binarios / nome
        caminho.write_text(conteudo)
        caminho.chmod(0o755)

    def ambiente_limpo(self) -> dict[str, str]:
        ambiente = {
            chave: valor
            for chave, valor in os.environ.items()
            if chave not in VARIAVEIS and not chave.startswith(("COMPOSE_", "STILLON_"))
        }
        ambiente["PATH"] = f"{self.binarios}{os.pathsep}{ambiente.get('PATH', '')}"
        ambiente["DOCKER_REAL"] = self.docker_real
        ambiente["CONFIG_SAIDA"] = str(self.config_saida)
        return ambiente

    def config_do_ensaio(self, ambiente: dict[str, str]) -> dict[str, Any]:
        """Roda o ensaio até o `up` e devolve o compose resolvido com o ambiente dele."""
        resultado = subprocess.run(  # noqa: S603 - argumentos controlados pelo teste
            [SHELL, str(self.raiz / "deploy" / "ci" / SCRIPT.name), "piso-falso", "branch-falso"],
            capture_output=True,
            text=True,
            check=False,
            timeout=120,
            env=ambiente,
        )
        self.assertTrue(
            self.config_saida.is_file(),
            f"o ensaio não chegou ao docker compose up (código {resultado.returncode}):\n"
            f"{resultado.stdout}\n{resultado.stderr}",
        )
        self.assertNotEqual(resultado.returncode, 0, "o docker falso devia parar o ensaio no up")
        self.assertIn("docker compose up --wait com a tag branch-falso", resultado.stderr)
        config: dict[str, Any] = json.loads(self.config_saida.read_text())
        return config

    def valores(self, config: dict[str, Any]) -> dict[str, str]:
        servicos = config["services"]
        for variavel, servico in SERVICO.items():
            self.assertIn(
                variavel, servicos[servico]["environment"],
                f"o compose.yaml não passa mais {variavel} ao {servico}",
            )
        return {v: servicos[s]["environment"][v] for v, s in SERVICO.items()}

    def assert_sem_canario(self, config: dict[str, Any]) -> None:
        for variavel, valor in self.valores(config).items():
            self.assertEqual(valor, "", f"{variavel} chegou a {SERVICO[variavel]}: {valor!r}")
        self.assertNotIn(self.canario.lower(), json.dumps(config).lower())

    def test_controle_o_env_de_canario_chega_a_pilha_sem_o_script(self) -> None:
        # Sem este controle, um .env que o compose não lesse faria o teste
        # principal passar sem testar nada.
        resultado = subprocess.run(  # noqa: S603 - argumentos controlados pelo teste
            [self.docker_real, "compose", "config", "--format", "json"],
            capture_output=True,
            text=True,
            check=False,
            timeout=120,
            cwd=self.raiz,
            env=self.ambiente_limpo(),
        )
        self.assertEqual(resultado.returncode, 0, resultado.stderr)
        for variavel, valor in self.valores(json.loads(resultado.stdout)).items():
            self.assertIn(self.canario.lower(), valor.lower(), variavel)

    def test_o_ensaio_nao_leva_a_pilha_o_canario_do_env(self) -> None:
        config = self.config_do_ensaio(self.ambiente_limpo())

        self.assert_sem_canario(config)

    def test_o_ensaio_nao_leva_a_pilha_o_canario_do_shell(self) -> None:
        ambiente = self.ambiente_limpo()
        for variavel in VARIAVEIS:
            ambiente[variavel] = f"{variavel}-shell-{self.canario}"

        config = self.config_do_ensaio(ambiente)

        self.assert_sem_canario(config)


if __name__ == "__main__":
    unittest.main()
