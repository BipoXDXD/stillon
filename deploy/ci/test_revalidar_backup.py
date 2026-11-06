"""Testes do ensaio de restauração do backup, sem rede, Docker nem chave real."""

import datetime
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import sys
import unittest
from urllib.parse import parse_qs, urlparse
import zipfile


AGORA = datetime.datetime(2026, 11, 6, 16, 0, tzinfo=datetime.timezone.utc)


def carregar(nome):
    caminho = Path(__file__).parent.parent / "backup" / f"{nome}.py"
    especificacao = importlib.util.spec_from_file_location(nome, caminho)
    modulo = importlib.util.module_from_spec(especificacao)
    sys.modules[nome] = modulo
    especificacao.loader.exec_module(modulo)
    return modulo


ensaio = carregar("revalidar_restauro")
aviso = carregar("avisar_revalidacao")


def artifact(nome="stillon-backup-1", criado="2026-11-06T14:23:51Z", ramo="main", expirado=False):
    return {
        "id": 7,
        "name": nome,
        "expired": expirado,
        "created_at": criado,
        "workflow_run": {"id": 1, "head_branch": ramo},
    }


def artifacts_de_ci(quantidade):
    return [artifact(f"fixture-{i}", "2026-11-06T15:00:00Z") for i in range(quantidade)]


class ApiFalsa:
    """Responde como a listagem do GitHub: por página, e guarda o que foi pedido."""

    def __init__(self, artifacts):
        self.artifacts = artifacts
        self.paginas = []

    def __call__(self, caminho):
        consulta = parse_qs(urlparse(caminho).query)
        por_pagina = int(consulta.get("per_page", ["30"])[0])
        pagina = int(consulta.get("page", ["1"])[0])
        self.paginas.append(pagina)
        inicio = (pagina - 1) * por_pagina
        return {
            "total_count": len(self.artifacts),
            "artifacts": self.artifacts[inicio : inicio + por_pagina],
        }


class TesteListagem(unittest.TestCase):
    def test_acha_o_backup_depois_da_primeira_pagina(self):
        # O caso real de 2026-11-06: 302 artifacts de CI mais novos que o backup.
        api = ApiFalsa(artifacts_de_ci(302) + [artifact()])
        escolhido = ensaio.escolher_backup(ensaio.listar_artifacts(api))
        self.assertEqual(escolhido["name"], "stillon-backup-1")

    def test_pede_paginas_ate_a_ultima_incompleta(self):
        api = ApiFalsa(artifacts_de_ci(250))
        self.assertEqual(len(ensaio.listar_artifacts(api)), 250)
        self.assertEqual(api.paginas, [1, 2, 3])

    def test_pagina_exatamente_cheia_nao_e_tratada_como_a_ultima(self):
        api = ApiFalsa(artifacts_de_ci(200))
        self.assertEqual(len(ensaio.listar_artifacts(api)), 200)
        self.assertEqual(api.paginas, [1, 2, 3])

    def test_sem_artifacts_devolve_lista_vazia(self):
        self.assertEqual(ensaio.listar_artifacts(ApiFalsa([])), [])

    def test_para_com_erro_nomeado_no_teto_de_paginas(self):
        quantidade = ensaio.TETO_DE_PAGINAS * ensaio.ITENS_POR_PAGINA + 1
        with self.assertRaisesRegex(RuntimeError, "^too_many_artifact_pages$"):
            ensaio.listar_artifacts(ApiFalsa(artifacts_de_ci(quantidade)))


class TesteEscolha(unittest.TestCase):
    def test_escolhe_o_mais_novo_em_qualquer_ordem(self):
        antigo = artifact("stillon-backup-1", "2026-11-04T14:40:48Z")
        novo = artifact("stillon-backup-2", "2026-11-06T14:23:51Z")
        meio = artifact("stillon-backup-3", "2026-11-05T14:46:11Z")
        self.assertEqual(ensaio.escolher_backup([antigo, novo, meio]), novo)

    def test_lista_vazia_tem_erro_nomeado(self):
        with self.assertRaisesRegex(RuntimeError, "^no_stored_backup$"):
            ensaio.escolher_backup([])

    def test_recusa_nome_ramo_e_expirado(self):
        recusados = [
            artifact("fixture-1"),
            artifact("xstillon-backup-1"),
            artifact(ramo="outro"),
            artifact(expirado=True),
        ]
        with self.assertRaisesRegex(RuntimeError, "^no_stored_backup$"):
            ensaio.escolher_backup(recusados)


class TesteIdade(unittest.TestCase):
    def conferir(self, criado):
        return ensaio.conferir_idade(artifact(criado=criado), AGORA)

    def test_devolve_a_idade_em_segundos(self):
        self.assertEqual(self.conferir("2026-11-06T14:00:00Z"), 7200)

    def test_aceita_a_fronteira_de_26_horas(self):
        self.assertEqual(self.conferir("2026-11-05T14:00:00Z"), 26 * 3600)

    def test_recusa_um_segundo_alem_de_26_horas(self):
        with self.assertRaisesRegex(RuntimeError, "^backup_age_outside_policy$"):
            self.conferir("2026-11-05T13:59:59Z")

    def test_aceita_idade_zero(self):
        self.assertEqual(self.conferir("2026-11-06T16:00:00Z"), 0)

    def test_recusa_data_no_futuro(self):
        with self.assertRaisesRegex(RuntimeError, "^backup_age_outside_policy$"):
            self.conferir("2026-11-06T16:00:01Z")


class TesteOrigem(unittest.TestCase):
    BOM = {"conclusion": "success", "head_branch": "main", "path": ".github/workflows/backup.yml"}

    def test_aceita_o_run_de_backup_da_main(self):
        ensaio.conferir_origem(self.BOM)

    def test_recusa_qualquer_campo_divergente(self):
        for campo, valor in (
            ("conclusion", "failure"),
            ("head_branch", "outro"),
            ("path", ".github/workflows/ci.yml"),
        ):
            with self.subTest(campo=campo):
                with self.assertRaisesRegex(RuntimeError, "^untrusted_backup_run$"):
                    ensaio.conferir_origem({**self.BOM, campo: valor})


def zip_com(arquivos):
    saida = io.BytesIO()
    with zipfile.ZipFile(saida, "w") as z:
        for nome, conteudo in arquivos.items():
            z.writestr(nome, conteudo)
    return saida.getvalue()


def com_digest(arquivo):
    return {"digest": "sha256:" + hashlib.sha256(arquivo).hexdigest()}


class TesteArquivo(unittest.TestCase):
    CIFRADO = b"age-encryption.org/v1\nconteudo"

    def test_devolve_o_conteudo_cifrado(self):
        arquivo = zip_com({"stillon.dump.age": self.CIFRADO})
        self.assertEqual(ensaio.ler_cifrado(arquivo, com_digest(arquivo)), self.CIFRADO)

    def test_recusa_digest_divergente(self):
        arquivo = zip_com({"stillon.dump.age": self.CIFRADO})
        with self.assertRaisesRegex(RuntimeError, "^stored_digest_mismatch$"):
            ensaio.ler_cifrado(arquivo, {"digest": "sha256:00"})

    def test_recusa_sem_digest(self):
        arquivo = zip_com({"stillon.dump.age": self.CIFRADO})
        with self.assertRaisesRegex(RuntimeError, "^stored_digest_mismatch$"):
            ensaio.ler_cifrado(arquivo, {})

    def test_recusa_dois_arquivos(self):
        arquivo = zip_com({"a.age": self.CIFRADO, "b.age": self.CIFRADO})
        with self.assertRaisesRegex(RuntimeError, "^unexpected_archive$"):
            ensaio.ler_cifrado(arquivo, com_digest(arquivo))

    def test_recusa_texto_claro(self):
        arquivo = zip_com({"stillon.dump.age": b"PGDMP"})
        with self.assertRaisesRegex(RuntimeError, "^not_age$"):
            ensaio.ler_cifrado(arquivo, com_digest(arquivo))


class TesteMetadados(unittest.TestCase):
    def test_le_o_que_o_backup_escreveu_no_log(self):
        log = "2026 cópia conferida: schema 16, 3 usuário(s), 12 alvo(s), 9 tabela(s)"
        self.assertEqual(ensaio.ler_metadados(log), ("16", "3", "12"))

    def test_recusa_log_sem_metadados(self):
        with self.assertRaisesRegex(RuntimeError, "^snapshot_metadata_missing$"):
            ensaio.ler_metadados("nada aqui")

    def test_acha_o_job_de_backup_concluido(self):
        jobs = [{"id": 1, "name": "x", "conclusion": "success"}, {"id": 2, "name": "backup", "conclusion": "success"}]
        self.assertEqual(ensaio.job_do_backup(jobs)["id"], 2)

    def test_recusa_run_sem_job_de_backup_concluido(self):
        jobs = [{"id": 2, "name": "backup", "conclusion": "failure"}]
        with self.assertRaisesRegex(RuntimeError, "^backup_job_missing$"):
            ensaio.job_do_backup(jobs)


URL = "https://github.com/BipoXDXD/stillon/actions/runs/1"
BOT = {"login": "app/github-actions", "is_bot": True}


def issue(numero=9, titulo=None, autor=None):
    return {"number": numero, "title": titulo or aviso.TITULO, "author": autor or BOT}


class GhFalso:
    def __init__(self, abertas=()):
        self.abertas = list(abertas)
        self.chamadas = []

    def __call__(self, *args):
        self.chamadas.append(args)
        return json.dumps(self.abertas) if args[:2] == ("issue", "list") else ""

    def verbos(self):
        return [a[1] for a in self.chamadas if a[:2] != ("issue", "list")]

    def chamada(self, verbo):
        return next(a for a in self.chamadas if a[1] == verbo)


class TesteAviso(unittest.TestCase):
    def registrar(self, resultado, abertas=()):
        gh = GhFalso(abertas)
        aviso.registrar(resultado, URL, "dono", gh)
        return gh

    def test_falha_abre_issue_com_o_link_e_para_o_dono(self):
        gh = self.registrar("failure")
        chamada = gh.chamada("create")
        self.assertEqual(gh.verbos(), ["create"])
        self.assertEqual(chamada[chamada.index("--title") + 1], aviso.TITULO)
        self.assertEqual(chamada[chamada.index("--assignee") + 1], "dono")
        self.assertIn(URL, chamada[chamada.index("--body") + 1])

    def test_estouro_de_tempo_tambem_avisa(self):
        self.assertEqual(self.registrar("cancelled").verbos(), ["create"])

    def test_resultado_desconhecido_avisa(self):
        self.assertEqual(self.registrar("").verbos(), ["create"])

    def test_falha_repetida_comenta_em_vez_de_abrir_outra(self):
        gh = self.registrar("failure", [issue(9)])
        self.assertEqual(gh.verbos(), ["comment"])
        self.assertEqual(gh.chamada("comment")[2], "9")
        self.assertIn(URL, " ".join(gh.chamada("comment")))

    def test_issue_alheia_com_o_mesmo_titulo_nao_cala_o_aviso(self):
        # O repositório é público: qualquer conta abre uma issue com este título.
        gh = self.registrar("failure", [issue(9, autor={"login": "curioso", "is_bot": False})])
        self.assertEqual(gh.verbos(), ["create"])

    def test_issue_de_outro_assunto_nao_conta(self):
        gh = self.registrar("failure", [issue(9, titulo="Outro problema")])
        self.assertEqual(gh.verbos(), ["create"])

    def test_sucesso_fecha_a_issue_aberta(self):
        gh = self.registrar("success", [issue(9)])
        self.assertEqual(gh.verbos(), ["close"])
        self.assertEqual(gh.chamada("close")[2], "9")

    def test_sucesso_sem_issue_nao_faz_nada(self):
        self.assertEqual(self.registrar("success").verbos(), [])

    def test_sucesso_nao_fecha_issue_alheia(self):
        gh = self.registrar("success", [issue(9, autor={"login": "curioso", "is_bot": False})])
        self.assertEqual(gh.verbos(), [])


if __name__ == "__main__":
    unittest.main()
