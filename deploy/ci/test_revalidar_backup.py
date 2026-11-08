"""Testes do ensaio de restauração do backup, sem rede, Docker nem chave real."""

import datetime
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest import mock
import urllib.request
from urllib.parse import parse_qs, urlparse
import zipfile


AGORA = datetime.datetime(2026, 11, 6, 16, 0, tzinfo=datetime.timezone.utc)
REPOSITORIO = 1384500257
FORK = 999


def carregar(nome):
    caminho = Path(__file__).parent.parent / "backup" / f"{nome}.py"
    especificacao = importlib.util.spec_from_file_location(nome, caminho)
    modulo = importlib.util.module_from_spec(especificacao)
    sys.modules[nome] = modulo
    especificacao.loader.exec_module(modulo)
    return modulo


ensaio = carregar("revalidar_restauro")
aviso = carregar("avisar_revalidacao")


def artifact(nome="stillon-backup-1", criado="2026-11-06T14:23:51Z", ramo="main", expirado=False, origem=REPOSITORIO):
    return {
        "id": 7,
        "name": nome,
        "expired": expirado,
        "created_at": criado,
        "workflow_run": {"id": 1, "head_branch": ramo, "repository_id": REPOSITORIO, "head_repository_id": origem},
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


    def test_fork_com_branch_main_nao_entra_mesmo_sendo_o_mais_novo(self):
        # head_branch é só um nome: um fork pode ter a sua própria main.
        nosso = artifact("stillon-backup-1", "2026-11-05T14:46:11Z")
        do_fork = artifact("stillon-backup-2", "2026-11-06T14:23:51Z", origem=FORK)
        self.assertEqual(ensaio.escolher_backup([do_fork, nosso]), nosso)

    def test_so_o_fork_e_o_mesmo_que_nao_ter_backup(self):
        with self.assertRaisesRegex(RuntimeError, "^no_stored_backup$"):
            ensaio.escolher_backup([artifact(origem=FORK)])

    def test_sem_repositorio_de_origem_nao_conta_como_nosso(self):
        for campos in ({}, {"head_branch": "main"}, {"head_branch": "main", "repository_id": REPOSITORIO}):
            with self.subTest(campos=campos):
                with self.assertRaisesRegex(RuntimeError, "^no_stored_backup$"):
                    ensaio.escolher_backup([{**artifact(), "workflow_run": campos}])

    def test_ids_ausentes_nos_dois_lados_nao_conferem(self):
        sem_ids = {"head_branch": "main", "repository_id": None, "head_repository_id": None}
        with self.assertRaisesRegex(RuntimeError, "^no_stored_backup$"):
            ensaio.escolher_backup([{**artifact(), "workflow_run": sem_ids}])

    def test_workflow_run_nulo_nao_derruba_a_escolha(self):
        # O run de um artifact antigo pode ter sumido: a API devolve null.
        nosso = artifact()
        self.assertEqual(ensaio.escolher_backup([{**artifact("stillon-backup-2"), "workflow_run": None}, nosso]), nosso)

    def test_sem_workflow_run_nao_derruba_a_escolha(self):
        sem_run = artifact("stillon-backup-2")
        del sem_run["workflow_run"]
        nosso = artifact()
        self.assertEqual(ensaio.escolher_backup([sem_run, nosso]), nosso)


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
    BOM = {
        "conclusion": "success",
        "event": "schedule",
        "head_branch": "main",
        "path": ".github/workflows/backup.yml",
    }

    def test_aceita_o_run_de_backup_da_main(self):
        ensaio.conferir_origem(self.BOM)

    def test_aceita_o_disparo_manual(self):
        ensaio.conferir_origem({**self.BOM, "event": "workflow_dispatch"})

    def test_recusa_evento_que_um_pr_consegue_provocar(self):
        for evento in ("pull_request", "pull_request_target", "push", "workflow_run", ""):
            with self.subTest(evento=evento):
                with self.assertRaisesRegex(RuntimeError, "^untrusted_backup_run$"):
                    ensaio.conferir_origem({**self.BOM, "event": evento})

    def test_recusa_run_sem_evento(self):
        sem_evento = {k: v for k, v in self.BOM.items() if k != "event"}
        with self.assertRaisesRegex(RuntimeError, "^untrusted_backup_run$"):
            ensaio.conferir_origem(sem_evento)

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

    def test_entrada_de_diretorio_nao_conta_como_arquivo(self):
        saida = io.BytesIO()
        with zipfile.ZipFile(saida, "w") as z:
            z.writestr("pasta/", b"")
            z.writestr("pasta/stillon.dump.age", self.CIFRADO)
        arquivo = saida.getvalue()
        self.assertEqual(ensaio.ler_cifrado(arquivo, com_digest(arquivo)), self.CIFRADO)

    def test_recusa_arquivo_acima_do_teto_sem_descompactar(self):
        # Zeros comprimem para quase nada: o zip é pequeno, a entrada declarada não.
        saida = io.BytesIO()
        with zipfile.ZipFile(saida, "w", zipfile.ZIP_DEFLATED) as z:
            z.writestr("stillon.dump.age", bytes(ensaio.TETO_DE_RESPOSTA + 1))
        arquivo = saida.getvalue()
        with mock.patch.object(zipfile.ZipFile, "read", autospec=True) as ler:
            with self.assertRaisesRegex(RuntimeError, "^unexpected_archive$"):
                ensaio.ler_cifrado(arquivo, com_digest(arquivo))
        ler.assert_not_called()


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


class TesteRedirecionamento(unittest.TestCase):
    ORIGEM = "https://api.github.com/repos/x/actions/artifacts/1/zip"

    def redirecionar(self, destino):
        pedido = urllib.request.Request(self.ORIGEM, headers={"Authorization": "Bearer segredo", "User-Agent": "x"})
        return ensaio.RedirecionamentoSeguro().redirect_request(pedido, None, 302, "Found", {}, destino)

    def test_fora_da_api_o_token_nao_vai_junto(self):
        novo = self.redirecionar("https://blob.exemplo.net/arquivo.zip")
        self.assertFalse(novo.has_header("Authorization"))
        self.assertEqual(novo.full_url, "https://blob.exemplo.net/arquivo.zip")

    def test_o_resto_do_pedido_continua(self):
        self.assertEqual(self.redirecionar("https://blob.exemplo.net/a").get_header("User-agent"), "x")

    def test_subdominio_parecido_nao_conta_como_a_api(self):
        self.assertFalse(self.redirecionar("https://api.github.com.exemplo.net/a").has_header("Authorization"))

    def test_outro_host_do_github_nao_recebe_o_token(self):
        # Só a API recebe o token, e não qualquer host sob github.com.
        self.assertFalse(self.redirecionar("https://uploads.github.com/a").has_header("Authorization"))

    def test_dentro_da_api_o_token_continua(self):
        self.assertTrue(self.redirecionar("https://api.github.com/outro").has_header("Authorization"))

    def test_recusa_redirecionamento_sem_https(self):
        with self.assertRaisesRegex(RuntimeError, "^insecure_redirect$"):
            self.redirecionar("http://api.github.com/outro")


class RespostaFalsa(io.BytesIO):
    def __enter__(self):
        return self

    def __exit__(self, *erro):
        self.close()


class TesteObter(unittest.TestCase):
    def test_recusa_resposta_acima_do_teto(self):
        abridor = mock.Mock()
        abridor.open.return_value = RespostaFalsa(b"x" * (ensaio.TETO_DE_RESPOSTA + 1))
        with mock.patch.object(ensaio.urllib.request, "build_opener", return_value=abridor):
            obter = ensaio.criar_obter("token")
            with self.assertRaisesRegex(RuntimeError, "^response_too_large$"):
                obter("repos/x", True)


class TesteIsolamento(unittest.TestCase):
    """Os passos que tocam age e Docker. Só o teste do próprio rodar() abre um
    subprocesso (o Python atual); nos outros, rodar() é falso e age e Docker não
    são chamados."""

    def test_passo_que_falha_vira_erro_nomeado(self):
        with self.assertRaisesRegex(RuntimeError, "^isolated_step_failed$"):
            ensaio.rodar([sys.executable, "-c", "raise SystemExit(3)"])

    def test_recusa_o_que_nao_e_dump_do_postgres(self):
        with tempfile.TemporaryDirectory() as pasta:
            with mock.patch.object(ensaio, "rodar", return_value=b"texto qualquer"):
                with self.assertRaisesRegex(RuntimeError, "^invalid_dump$"):
                    ensaio.decifrar(b"age-encryption.org/v1\n", "AGE-SECRET-KEY-FALSA", Path(pasta))

    def test_a_identidade_nao_fica_no_disco_depois_de_decifrar(self):
        with tempfile.TemporaryDirectory() as pasta:
            with mock.patch.object(ensaio, "rodar", return_value=b"PGDMP..."):
                ensaio.decifrar(b"age-encryption.org/v1\n", "AGE-SECRET-KEY-FALSA", Path(pasta))
            self.assertFalse((Path(pasta) / "identity").exists())

    def test_a_identidade_sai_do_disco_mesmo_quando_o_age_falha(self):
        with tempfile.TemporaryDirectory() as pasta:
            with mock.patch.object(ensaio, "rodar", side_effect=RuntimeError("isolated_step_failed")):
                with self.assertRaisesRegex(RuntimeError, "^isolated_step_failed$"):
                    ensaio.decifrar(b"age-encryption.org/v1\n", "AGE-SECRET-KEY-FALSA", Path(pasta))
            self.assertFalse((Path(pasta) / "identity").exists())

    def test_nao_remove_container_sem_o_rotulo_do_ensaio(self):
        chamadas = []

        def rodar(argumentos):
            chamadas.append(argumentos)
            return b"outro-valor\n"

        with mock.patch.object(ensaio, "rodar", side_effect=rodar):
            with self.assertRaisesRegex(RuntimeError, "^cleanup_label_mismatch$"):
                ensaio.remover_banco("stillon-ensaio-x")
        self.assertEqual([a[:2] for a in chamadas], [["docker", "inspect"]])

    def test_o_banco_do_ensaio_sobe_sem_rede(self):
        argumentos = ensaio.argumentos_do_banco("stillon-ensaio-x")
        self.assertEqual(argumentos[argumentos.index("--network") + 1], "none")


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

    def test_sucesso_fecha_todas_as_issues_do_aviso(self):
        gh = self.registrar("success", [issue(9), issue(12)])
        self.assertEqual([a[2] for a in gh.chamadas if a[1] == "close"], ["9", "12"])

    def test_a_consulta_pede_as_abertas_ate_o_teto(self):
        # O padrão do gh é 30 issues: com mais abertas, o aviso nosso podia ficar de fora.
        consulta = self.registrar("failure").chamada("list")
        self.assertEqual(consulta[consulta.index("--state") + 1], "open")
        self.assertEqual(consulta[consulta.index("--limit") + 1], "200")

    def test_sucesso_sem_issue_nao_faz_nada(self):
        self.assertEqual(self.registrar("success").verbos(), [])

    def test_sucesso_nao_fecha_issue_alheia(self):
        gh = self.registrar("success", [issue(9, autor={"login": "curioso", "is_bot": False})])
        self.assertEqual(gh.verbos(), [])


if __name__ == "__main__":
    unittest.main()
