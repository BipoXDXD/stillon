"""sem-pulados.py: o guard que reprova o CI quando algum teste é pulado.

Teste pulado não vale: o que não deve rodar no CI sai por configuração (um
config próprio do Playwright, um gerador fora do go test), e o guard pega o
resto. Os relatórios aqui são escritos à mão, no formato do
`go test -json` e do reporter json do Playwright.
"""
from pathlib import Path
import json
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
GUARD = ROOT / "deploy/ci/sem-pulados.py"


def evento(acao, pacote="github.com/BipoXDXD/stillon/internal/x", teste=None, saida=None):
    e = {"Action": acao, "Package": pacote}
    if teste is not None:
        e["Test"] = teste
    if saida is not None:
        e["Output"] = saida
    return json.dumps(e)


def teste_do_playwright(titulo, status, anotacoes=(), *, expectativa="passed", resultados=None):
    if resultados is None:
        resultados = {"expected": ["passed"], "skipped": [], "unexpected": ["failed"],
                      "flaky": ["failed", "passed"]}[status]
    return {"title": titulo, "tests": [{"status": status, "annotations": list(anotacoes),
                                       "expectedStatus": expectativa,
                                       "results": [{"status": r} for r in resultados]}]}


def relatorio_do_playwright(specs, esperados, pulados, inesperados=0, describe=None, instaveis=0, erros=()):
    arquivo = {"title": "e2e/a.spec.ts", "file": "e2e/a.spec.ts", "specs": specs, "suites": []}
    if describe is not None:
        arquivo["suites"].append({"title": describe[0], "specs": describe[1], "suites": []})
    return json.dumps({
        "suites": [arquivo],
        "errors": list(erros),
        "stats": {"expected": esperados, "skipped": pulados, "unexpected": inesperados, "flaky": instaveis},
    })


def teste_do_vitest(titulo, status, describe=(), falhas=()):
    return {"ancestorTitles": list(describe), "fullName": " ".join((*describe, titulo)),
            "title": titulo, "status": status, "failureMessages": list(falhas)}


def relatorio_do_vitest(*testes, status_do_arquivo="passed", mensagem=""):
    return json.dumps({"testResults": [{"name": "/repo/web/src/a.test.ts", "status": status_do_arquivo,
                                        "message": mensagem, "assertionResults": list(testes)}]})


class Guard(unittest.TestCase):
    def rodar(self, modo, conteudo):
        with tempfile.TemporaryDirectory() as pasta:
            relatorio = Path(pasta) / "relatorio"
            relatorio.write_text(conteudo)
            return subprocess.run([sys.executable, str(GUARD), modo, str(relatorio)],
                                  capture_output=True, text=True, check=False)

    def rodar_go(self, *linhas):
        return self.rodar("go", "\n".join(linhas) + "\n")


class GoTestJson(Guard):
    def test_only_passed_tests_pass_the_guard(self):
        r = self.rodar_go(evento("run", teste="TestA"), evento("pass", teste="TestA"),
                          evento("pass"))
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn("1 teste passou", r.stdout)

    def test_unicode_line_separators_inside_json_are_not_record_delimiters(self):
        for separador in ("\u0085", "\u2028", "\u2029"):
            with self.subTest(separador=separador):
                saida = json.dumps({"Action": "output", "Test": "TestA",
                                    "Output": f"antes{separador}depois\n"}, ensure_ascii=False)
                r = self.rodar_go(evento("run", teste="TestA"), saida, evento("pass", teste="TestA"))
                self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
                self.assertIn("1 teste passou", r.stdout)

    def test_a_skipped_test_fails_and_names_the_test_and_reason(self):
        r = self.rodar_go(
            evento("run", teste="TestB"),
            evento("output", teste="TestB", saida="    b_test.go:9: esta máquina não escuta em ::1\n"),
            evento("output", teste="TestB", saida="--- SKIP: TestB (0.00s)\n"),
            evento("skip", teste="TestB"),
            evento("pass", teste="TestA"),
        )
        self.assertEqual(r.returncode, 1)
        self.assertIn("internal/x TestB", r.stdout)
        self.assertIn("esta máquina não escuta em ::1", r.stdout)

    def test_a_skipped_subtest_also_fails(self):
        r = self.rodar_go(evento("pass", teste="TestA"), evento("skip", teste="TestA/caso"))
        self.assertEqual(r.returncode, 1)
        self.assertIn("TestA/caso", r.stdout)

    def test_a_package_without_test_files_is_not_a_skipped_test(self):
        r = self.rodar_go(
            evento("output", pacote="github.com/BipoXDXD/stillon/cmd/y", saida="?   \tcmd/y\t[no test files]\n"),
            evento("skip", pacote="github.com/BipoXDXD/stillon/cmd/y"),
            evento("pass", teste="TestA"),
        )
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)

    def test_an_empty_report_fails(self):
        r = self.rodar("go", "")
        self.assertEqual(r.returncode, 1)
        self.assertIn("nenhum teste", r.stdout)

    def test_a_report_with_only_package_events_fails(self):
        r = self.rodar_go(evento("start"), evento("pass"))
        self.assertEqual(r.returncode, 1)
        self.assertIn("nenhum teste", r.stdout)

    def test_a_failed_test_fails_and_shows_its_output(self):
        r = self.rodar_go(
            evento("run", teste="TestC"),
            evento("output", teste="TestC", saida="    c_test.go:3: esperava 2, veio 3\n"),
            evento("fail", teste="TestC"),
            evento("pass", teste="TestA"),
        )
        self.assertEqual(r.returncode, 1)
        self.assertIn("esperava 2, veio 3", r.stdout)

    def test_a_failed_build_fails_and_shows_its_output(self):
        r = self.rodar_go(
            json.dumps({"ImportPath": "github.com/BipoXDXD/stillon/internal/x", "Action": "build-output",
                        "Output": "x.go:1: undefined: y\n"}),
            json.dumps({"ImportPath": "github.com/BipoXDXD/stillon/internal/x", "Action": "build-fail"}),
            evento("pass", teste="TestA"),
        )
        self.assertEqual(r.returncode, 1)
        self.assertIn("undefined: y", r.stdout)

    def test_a_test_without_a_final_event_shows_its_output_when_the_package_fails(self):
        # Timeout do go test ou panic numa goroutine: o test2json só emite o
        # fail do pacote, e a saída do teste (com o stack) ficaria escondida.
        r = self.rodar_go(
            evento("run", teste="TestA"),
            evento("pass", teste="TestA"),
            evento("run", teste="TestD"),
            evento("output", teste="TestD", saida="panic: test timed out after 1s\n"),
            evento("output", teste="TestD", saida="goroutine 7 [running]:\n"),
            evento("output", saida="FAIL\tgithub.com/BipoXDXD/stillon/internal/x\t1.2s\n"),
            evento("fail"),
        )
        self.assertEqual(r.returncode, 1)
        self.assertIn("sem resultado: internal/x TestD", r.stdout)
        self.assertIn("panic: test timed out after 1s", r.stdout)
        self.assertIn("goroutine 7 [running]", r.stdout)
        self.assertNotIn("sem resultado: internal/x TestA", r.stdout)

    def test_a_failed_package_without_failed_test_still_fails(self):
        r = self.rodar_go(evento("pass", teste="TestA"),
                          evento("output", saida="FAIL\tinternal/x [setup failed]\n"), evento("fail"))
        self.assertEqual(r.returncode, 1)
        self.assertIn("setup failed", r.stdout)

    def test_repeated_passes_with_count_are_counted(self):
        r = self.rodar_go(evento("run", teste="TestA"), evento("pass", teste="TestA"),
                          evento("run", teste="TestA"), evento("pass", teste="TestA"))
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn("2 testes passaram", r.stdout)

    def test_a_json_line_that_is_not_an_object_fails_without_traceback(self):
        r = self.rodar_go(evento("pass", teste="TestA"), "[1, 2]")
        self.assertEqual(r.returncode, 1)
        self.assertIn("linha 2", r.stdout)
        self.assertNotIn("Traceback", r.stderr)

    def test_a_line_that_is_not_json_fails(self):
        r = self.rodar_go(evento("pass", teste="TestA"), "ok  \tgithub.com/x\t0.1s")
        self.assertEqual(r.returncode, 1)
        self.assertIn("linha 2", r.stdout)

    def test_a_missing_report_fails(self):
        r = subprocess.run([sys.executable, str(GUARD), "go", "/nao/existe.jsonl"],
                           capture_output=True, text=True, check=False)
        self.assertEqual(r.returncode, 1)


class PlaywrightJson(Guard):
    def test_only_expected_tests_pass_the_guard(self):
        r = self.rodar("playwright", relatorio_do_playwright(
            [teste_do_playwright("entra", "expected")], esperados=1, pulados=0))
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn("1 teste passou", r.stdout)

    def test_an_expected_failure_fails_even_beside_a_passing_test(self):
        r = self.rodar("playwright", relatorio_do_playwright(
            [teste_do_playwright("entra", "expected"),
             teste_do_playwright("falha conhecida", "expected", expectativa="failed",
                                 resultados=["failed"])], esperados=2, pulados=0))
        self.assertEqual(r.returncode, 1)
        self.assertIn("e2e/a.spec.ts › falha conhecida", r.stdout)

    def test_an_expected_pass_without_a_real_pass_fails(self):
        for resultados in ([], ["failed"], ["skipped"], ["timedOut"], ["failed", "passed"]):
            with self.subTest(resultados=resultados):
                r = self.rodar("playwright", relatorio_do_playwright(
                    [teste_do_playwright("sem aprovação", "expected", resultados=resultados)],
                    esperados=1, pulados=0))
                self.assertEqual(r.returncode, 1)
                self.assertIn("e2e/a.spec.ts › sem aprovação", r.stdout)

    def test_a_listed_skip_fails_even_when_the_summary_underreports_it(self):
        r = self.rodar("playwright", relatorio_do_playwright(
            [teste_do_playwright("entra", "expected"), teste_do_playwright("pulou", "skipped")],
            esperados=1, pulados=0))
        self.assertEqual(r.returncode, 1)
        self.assertIn("e2e/a.spec.ts › pulou", r.stdout)

    def test_a_skipped_test_fails_and_names_file_title_and_reason(self):
        r = self.rodar("playwright", relatorio_do_playwright(
            [teste_do_playwright("entra", "expected")], esperados=1, pulados=1,
            describe=("troca", [teste_do_playwright("liga o 2FA", "skipped",
                                                    [{"type": "skip", "description": "sem CLI"}])])))
        self.assertEqual(r.returncode, 1)
        self.assertIn("e2e/a.spec.ts › troca › liga o 2FA", r.stdout)
        self.assertIn("sem CLI", r.stdout)

    def test_skipped_count_without_listed_test_still_fails(self):
        r = self.rodar("playwright", relatorio_do_playwright(
            [teste_do_playwright("entra", "expected")], esperados=1, pulados=2))
        self.assertEqual(r.returncode, 1)
        self.assertIn("2 testes pulados", r.stdout)

    def test_no_test_run_fails(self):
        r = self.rodar("playwright", relatorio_do_playwright([], esperados=0, pulados=0))
        self.assertEqual(r.returncode, 1)
        self.assertIn("nenhum teste", r.stdout)

    def test_an_unexpected_result_fails(self):
        r = self.rodar("playwright", relatorio_do_playwright(
            [teste_do_playwright("entra", "unexpected")], esperados=3, pulados=0, inesperados=1))
        self.assertEqual(r.returncode, 1)

    def test_a_flaky_test_fails(self):
        r = self.rodar("playwright", relatorio_do_playwright(
            [teste_do_playwright("entra", "flaky")], esperados=3, pulados=0, instaveis=1))
        self.assertEqual(r.returncode, 1)
        self.assertIn("1 teste falhou", r.stdout)

    def test_a_report_that_is_not_an_object_fails_without_traceback(self):
        r = self.rodar("playwright", "[]")
        self.assertEqual(r.returncode, 1)
        self.assertNotIn("Traceback", r.stderr)

    def test_a_report_without_stats_fails(self):
        r = self.rodar("playwright", json.dumps({"suites": []}))
        self.assertEqual(r.returncode, 1)

    def test_a_top_level_error_fails_and_shows_its_message(self):
        # Erro ao carregar um spec ou no globalSetup: os outros testes rodam,
        # e o erro só aparece no array errors do relatório.
        r = self.rodar("playwright", relatorio_do_playwright(
            [teste_do_playwright("entra", "expected")], esperados=1, pulados=0,
            erros=[{"message": "SyntaxError: e2e/b.spec.ts: Unexpected token"}]))
        self.assertEqual(r.returncode, 1)
        self.assertIn("Unexpected token", r.stdout)


class VitestJson(Guard):
    def test_only_passed_tests_pass_the_guard(self):
        r = self.rodar("vitest", relatorio_do_vitest(teste_do_vitest("soma", "passed")))
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn("1 teste passou", r.stdout)

    def test_a_skipped_test_fails_and_names_file_and_test(self):
        r = self.rodar("vitest", relatorio_do_vitest(
            teste_do_vitest("soma", "passed"), teste_do_vitest("subtrai", "skipped", describe=("conta",))))
        self.assertEqual(r.returncode, 1)
        self.assertIn("src/a.test.ts › conta subtrai", r.stdout)

    def test_todo_and_pending_tests_fail(self):
        for status in ("todo", "pending"):
            with self.subTest(status=status):
                r = self.rodar("vitest", relatorio_do_vitest(
                    teste_do_vitest("soma", "passed"), teste_do_vitest("divide", status)))
                self.assertEqual(r.returncode, 1)
                self.assertIn("divide", r.stdout)

    def test_a_failed_test_fails_and_shows_its_message(self):
        r = self.rodar("vitest", relatorio_do_vitest(
            teste_do_vitest("soma", "passed"),
            teste_do_vitest("divide", "failed", falhas=["AssertionError: esperava 2, veio 3"]),
            status_do_arquivo="failed"))
        self.assertEqual(r.returncode, 1)
        self.assertIn("esperava 2, veio 3", r.stdout)

    def test_a_file_that_fails_to_load_fails(self):
        r = self.rodar("vitest", json.dumps({"testResults": [
            {"name": "/repo/web/src/a.test.ts", "status": "passed", "message": "",
             "assertionResults": [teste_do_vitest("soma", "passed")]},
            {"name": "/repo/web/src/b.test.ts", "status": "failed",
             "message": "Failed to resolve import ./nao-existe", "assertionResults": []}]}))
        self.assertEqual(r.returncode, 1)
        self.assertIn("Failed to resolve import", r.stdout)

    def test_no_test_run_fails(self):
        r = self.rodar("vitest", relatorio_do_vitest())
        self.assertEqual(r.returncode, 1)
        self.assertIn("nenhum teste", r.stdout)

    def test_a_report_without_test_results_fails(self):
        r = self.rodar("vitest", "[]")
        self.assertEqual(r.returncode, 1)
        self.assertNotIn("Traceback", r.stderr)


class Uso(Guard):
    def test_an_unknown_mode_fails(self):
        r = self.rodar("jest", "{}")
        self.assertNotEqual(r.returncode, 0)


if __name__ == "__main__":
    unittest.main()
