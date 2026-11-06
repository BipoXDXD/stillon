"""Transforma o resultado do ensaio de restauração em uma issue para o dono.

Um job vermelho sozinho não bastou: a falha do ensaio durou 3 dias em silêncio.
A issue fica aberta enquanto o ensaio falhar e fecha sozinha quando ele passa.
O texto é público: leva só o link do run, nunca o motivo nem dados do backup.
"""

import json
import os
import subprocess

TITULO = "Ensaio de restauração do backup falhou"
TETO_DE_ISSUES = 200
TEMPO_DO_GH_S = 60
# O repositório é público: o autor impede que uma issue alheia com o mesmo
# título cale o aviso. Sem a issue nossa na lista, abre outra, o lado seguro.
AUTORES_DO_AVISO = {"app/github-actions", "github-actions[bot]"}


def registrar(resultado, url_do_run, dono, gh):
    abertas = avisos_abertos(gh)
    if resultado == "success":
        for numero in abertas:
            gh("issue", "close", numero, "--comment", f"O ensaio voltou a passar: {url_do_run}")
    elif abertas:
        gh("issue", "comment", abertas[0], "--body", f"Falhou de novo: {url_do_run}")
    else:
        corpo = f"O ensaio de restauração não confirmou o backup guardado: {url_do_run}"
        gh("issue", "create", "--title", TITULO, "--body", corpo, "--assignee", dono)


def avisos_abertos(gh):
    saida = gh("issue", "list", "--state", "open", "--limit", str(TETO_DE_ISSUES), "--json", "number,title,author")
    return [
        str(issue["number"])
        for issue in json.loads(saida)
        if issue["title"] == TITULO and issue["author"]["login"] in AUTORES_DO_AVISO
    ]


def rodar_gh(*args):
    processo = subprocess.run(["gh", *args], capture_output=True, text=True, timeout=TEMPO_DO_GH_S, check=True)
    return processo.stdout


def main():
    registrar(
        os.environ["RESULTADO"],
        os.environ["URL_DO_RUN"],
        os.environ["GITHUB_REPOSITORY_OWNER"],
        rodar_gh,
    )


if __name__ == "__main__":
    main()
