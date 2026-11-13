"""O checar-workflows.sh reprova host key desligada em workflow e em script."""

import pathlib
import re
import shutil
import subprocess
import tempfile
import unittest

RAIZ = pathlib.Path(__file__).resolve().parents[2]
SCRIPT = pathlib.Path("deploy/ci/checar-workflows.sh")
SONDA_YML = pathlib.Path(".github/workflows/zz-sonda.yml")
SONDA_SH = pathlib.Path("deploy/ci/zz-sonda.sh")
COOLIFY = pathlib.Path(".github/workflows/coolify.yml")
CI = pathlib.Path(".github/workflows/ci.yml")
MENSAGEM_HOST_KEY = "FALHOU: StrictHostKeyChecking desligado"
MENSAGEM_KEYSCAN = "FALHOU: ssh-keyscan"
LINHAS_PROIBIDAS = (
    ("ssh -o StrictHostKeyChecking=no host", MENSAGEM_HOST_KEY),
    ("ssh -o StrictHostKeyChecking=off host", MENSAGEM_HOST_KEY),
    ("ssh -o StrictHostKeyChecking=accept-new host", MENSAGEM_HOST_KEY),
    ("ssh -o stricthostkeychecking=no host", MENSAGEM_HOST_KEY),
    ('ssh -o "stricthostkeychecking no" host', MENSAGEM_HOST_KEY),
    ("ssh -o UserKnownHostsFile=/dev/null host", MENSAGEM_HOST_KEY),
    ("ssh-keyscan host >> known_hosts", MENSAGEM_KEYSCAN),
)
CHECKOUT_FIXADO = re.compile(r"^\s*- uses: (actions/checkout@[0-9a-f]{40})(?:\s|$)", re.M)


def checkout_fixado_por_sha():
    """Lê a referência do workflow, para o bump do Dependabot não quebrar o teste."""
    achado = CHECKOUT_FIXADO.search((RAIZ / COOLIFY).read_text())
    if achado is None:
        raise AssertionError(f"{COOLIFY}: actions/checkout não está fixada por SHA completo")
    return achado.group(1)


SHA_DO_CHECKOUT = checkout_fixado_por_sha()
MENSAGEM_IF_DO_GATE = "se a mudança for de propósito, atualize IF_DO_GATE"
CONDICAO_MAIN = "      github.ref == 'refs/heads/main' &&\n"
CONDICAO_SUCESSO = "        github.event.workflow_run.conclusion == 'success' &&\n"
CITACAO_KNOWN_HOSTS = "KNOWN_HOSTS: ${{ secrets.COOLIFY_RELEASE_KNOWN_HOSTS }}"
SABOTAGENS_COOLIFY = (
    (
        "gatilho push",
        "  workflow_dispatch:\n    inputs:",
        "  push:\n  workflow_dispatch:\n    inputs:",
        "coolify.yml: gatilhos",
    ),
    (
        "workflow_run de qualquer branch",
        "    branches: [main]\n",
        "    branches: ['**']\n",
        "o workflow_run não está preso à main",
    ),
    (
        "workflow_run de outro workflow",
        "    workflows: [ci]\n",
        "    workflows: [ci, outro]\n",
        "o workflow_run não vem só do ci",
    ),
    (
        "permissão de escrita no topo",
        "permissions:\n  contents: read\n\nconcurrency",
        "permissions:\n  contents: write\n\nconcurrency",
        "permissão de escrita",
    ),
    (
        "permissão a mais no topo",
        "permissions:\n  contents: read\n\nconcurrency",
        "permissions:\n  contents: read\n  issues: read\n\nconcurrency",
        "coolify.yml: permissions",
    ),
    (
        "actions com escrita no gates",
        "      actions: read\n    outputs:",
        "      actions: write\n    outputs:",
        "permissão de escrita",
    ),
    (
        "packages a mais no gates",
        "      actions: read\n    outputs:",
        "      actions: read\n      packages: read\n    outputs:",
        "coolify.yml: gates com permissions",
    ),
    (
        "packages a menos no deploy",
        "      actions: read\n      packages: read\n",
        "      actions: read\n",
        "coolify.yml: deploy com permissions",
    ),
    ("if do gate sem a main", CONDICAO_MAIN, "", MENSAGEM_IF_DO_GATE),
    ("if do gate sem o sucesso do ci", CONDICAO_SUCESSO, "", MENSAGEM_IF_DO_GATE),
    ("gates sem ref main", "          ref: main\n", "", "o job gates não lê a main"),
    (
        "deploy sem o gate",
        "    if: needs.gates.outputs.eligible == 'true'\n",
        "    if: always()\n",
        "o deploy não depende de eligible",
    ),
    (
        "deploy sem needs",
        "    needs: gates\n",
        "",
        "o deploy não espera o job gates",
    ),
    (
        "deploy sem environment",
        "    environment: production\n",
        "",
        "o deploy não está no environment production",
    ),
    (
        "environment em outro job",
        "    timeout-minutes: 40\n    permissions:\n      contents: read\n      actions: read\n    outputs:",
        "    timeout-minutes: 40\n    environment: production\n    permissions:\n"
        "      contents: read\n      actions: read\n    outputs:",
        "environment citado",
    ),
    (
        "deploy de outro commit",
        "          ref: ${{ needs.gates.outputs.commit }}\n",
        "          ref: main\n",
        "o deploy não faz checkout do commit aprovado",
    ),
    (
        "publicação sem a segunda conferência",
        "        if: steps.latest.outputs.eligible == 'true'\n",
        "",
        "a publicação não depende da última conferência",
    ),
    (
        "known_hosts fora do secret",
        CITACAO_KNOWN_HOSTS,
        "KNOWN_HOSTS: ${{ vars.COOLIFY_RELEASE_KNOWN_HOSTS }}",
        "o known_hosts não vem do secret",
    ),
    (
        "known_hosts sem exigir valor",
        '          test -n "$SSH_KEY" && test -n "$KNOWN_HOSTS" && test -n "$TARGET"\n',
        "",
        "o deploy não recusa known_hosts vazio",
    ),
    (
        "known_hosts escrito em outro arquivo",
        '"$RUNNER_TEMP/release-ssh/known_hosts"\n',
        '"$RUNNER_TEMP/release-ssh/outro"\n',
        "o known_hosts do secret não é gravado",
    ),
    (
        "send com outro known_hosts",
        "          DEPLOY_KNOWN_HOSTS: ${{ runner.temp }}/release-ssh/known_hosts\n",
        "          DEPLOY_KNOWN_HOSTS: /dev/null\n",
        "o send.sh não recebe o known_hosts gravado",
    ),
    (
        "ssh-keyscan injetado",
        '          test -n "$SSH_KEY"',
        '          ssh-keyscan "$TARGET" >> known_hosts\n          test -n "$SSH_KEY"',
        MENSAGEM_KEYSCAN,
    ),
    (
        "host key desligada",
        "      - name: Skip superseded main commit\n",
        "      - run: ssh -o StrictHostKeyChecking=accept-new alvo true\n"
        "      - name: Skip superseded main commit\n",
        MENSAGEM_HOST_KEY,
    ),
    (
        "action por tag",
        SHA_DO_CHECKOUT,
        "actions/checkout@v7",
        "action sem SHA completo",
    ),
    (
        "secret fora da lista",
        "          GH_TOKEN: ${{ github.token }}\n          SKIP_SUPERSEDED: 'true'\n",
        "          GH_TOKEN: ${{ secrets.OUTRO }}\n          SKIP_SUPERSEDED: 'true'\n",
        "acesso a secrets fora das citações da publicação",
    ),
    (
        "todos os secrets",
        "          TARGET: ${{ vars.COOLIFY_RELEASE_TARGET }}\n        run: |",
        "          TARGET: ${{ toJSON(secrets) }}\n        run: |",
        "acesso a secrets fora das citações da publicação",
    ),
    (
        "secret da chave no gates",
        "          CI_RUN_ID: ${{ github.event.workflow_run.id }}\n",
        "          CI_RUN_ID: ${{ github.event.workflow_run.id }}\n"
        "          SSH_KEY: ${{ secrets.COOLIFY_RELEASE_SSH_KEY }}\n",
        "o job gates não pode ver segredo",
    ),
)

MENSAGEM_CONCORRENCIA_CI = "ci.yml: o ci da main não pode ser cancelado"
CANCELA_SO_FORA_DA_MAIN = "  cancel-in-progress: ${{ github.ref != 'refs/heads/main' }}\n"
SABOTAGENS_CI = (
    ("cancela tudo", CANCELA_SO_FORA_DA_MAIN, "  cancel-in-progress: true\n"),
    ("condição invertida", CANCELA_SO_FORA_DA_MAIN,
     "  cancel-in-progress: ${{ github.ref == 'refs/heads/main' }}\n"),
    ("grupo único para todos os refs", "  group: ci-${{ github.ref }}\n", "  group: ci\n"),
)


class ChecarWorkflowsTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.pasta = tempfile.TemporaryDirectory()
        cls.copia = pathlib.Path(cls.pasta.name)
        rastreados = subprocess.run(
            ["git", "ls-files", "-z"], cwd=RAIZ, check=True, capture_output=True
        ).stdout.decode().split("\0")
        for nome in filter(None, rastreados):
            destino = cls.copia / nome
            destino.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(RAIZ / nome, destino)
        subprocess.run(["git", "init", "-q"], cwd=cls.copia, check=True)
        subprocess.run(["git", "add", "-A"], cwd=cls.copia, check=True)

    @classmethod
    def tearDownClass(cls):
        cls.pasta.cleanup()

    def tearDown(self):
        for sonda in (SONDA_YML, SONDA_SH):
            (self.copia / sonda).unlink(missing_ok=True)

    def checar(self):
        return subprocess.run(
            ["sh", str(SCRIPT)], cwd=self.copia, capture_output=True, text=True
        )

    def test_repositorio_sem_ocorrencia_passa(self):
        resultado = self.checar()
        self.assertEqual(resultado.returncode, 0, resultado.stderr)

    def test_workflow_com_linha_proibida_reprova(self):
        for linha, mensagem in LINHAS_PROIBIDAS:
            with self.subTest(linha=linha):
                (self.copia / SONDA_YML).write_text(f"run: {linha}\n")
                resultado = self.checar()
                self.assertNotEqual(resultado.returncode, 0)
                self.assertIn(mensagem, resultado.stderr)

    def test_script_com_linha_proibida_reprova(self):
        for linha, mensagem in LINHAS_PROIBIDAS:
            with self.subTest(linha=linha):
                (self.copia / SONDA_SH).write_text(f"#!/bin/sh\n{linha}\n")
                resultado = self.checar()
                self.assertNotEqual(resultado.returncode, 0)
                self.assertIn(mensagem, resultado.stderr)

    def test_comentario_em_script_nao_reprova(self):
        (self.copia / SONDA_SH).write_text("#!/bin/sh\n# ssh-keyscan fica proibido\n")
        resultado = self.checar()
        self.assertEqual(resultado.returncode, 0, resultado.stderr)
        self.assertNotIn(MENSAGEM_KEYSCAN, resultado.stderr)

    def test_coolify_sabotado_reprova(self):
        caminho = self.copia / COOLIFY
        original = caminho.read_text()
        self.addCleanup(caminho.write_text, original)
        for nome, trecho, troca, mensagem in SABOTAGENS_COOLIFY:
            with self.subTest(sabotagem=nome):
                self.assertIn(trecho, original)
                caminho.write_text(original.replace(trecho, troca, 1))
                resultado = self.checar()
                self.assertNotEqual(resultado.returncode, 0, "o script não reprovou")
                self.assertIn(mensagem, resultado.stderr)

    def test_ci_sabotado_reprova(self):
        caminho = self.copia / CI
        original = caminho.read_text()
        self.addCleanup(caminho.write_text, original)
        for nome, trecho, troca in SABOTAGENS_CI:
            with self.subTest(sabotagem=nome):
                self.assertIn(trecho, original)
                caminho.write_text(original.replace(trecho, troca, 1))
                resultado = self.checar()
                self.assertNotEqual(resultado.returncode, 0, "o script não reprovou")
                self.assertIn(MENSAGEM_CONCORRENCIA_CI, resultado.stderr)


if __name__ == "__main__":
    unittest.main()
