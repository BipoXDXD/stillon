#!/usr/bin/env python3
"""Reprova o CI quando algum teste é pulado, falha ou nenhum teste roda.

Teste pulado não vale: o que não deve rodar no CI sai por configuração (um
config próprio do Playwright, um gerador fora do go test), e este guard
pega o resto, como um runner sem ::1 que faria um teste se
declarar pulado.

Uso:
  sem-pulados.py go <saída do go test -json>
  sem-pulados.py playwright <relatório do reporter json>
  sem-pulados.py vitest <relatório do reporter json>

O relatório do go test vai para um arquivo, e não para o log; por isso o
guard mostra a saída de cada teste que falhou ou pulou, e a dos que
começaram e não terminaram (timeout do go test, panic numa goroutine: aí o
test2json só emite o fail do pacote, e o stack fica sob o nome do teste).
"""
import argparse
import json
import sys
from collections import defaultdict
from pathlib import Path


def plural(n, singular, plural_):
    return f"{n} {singular if n == 1 else plural_}"


def checar_go(linhas):
    """Devolve as mensagens de reprovação e o número de testes que passaram."""
    saidas = defaultdict(list)
    pulados, falhos, passaram = [], [], 0
    abertos = {}  # testes com run e ainda sem pass, fail ou skip, na ordem
    for numero, linha in enumerate(linhas, start=1):
        if not linha.strip():
            continue
        try:
            e = json.loads(linha)
        except json.JSONDecodeError:
            e = None
        if not isinstance(e, dict):
            return [f"linha {numero} não é JSON do go test -json: {linha.strip()[:120]}"], 0
        pacote = (e.get("Package") or e.get("ImportPath") or "").removeprefix("github.com/BipoXDXD/stillon/")
        chave = (pacote, e.get("Test"))
        acao = e.get("Action")
        if e.get("Test"):
            if acao == "run":
                abertos[chave] = True
            elif acao in ("pass", "fail", "skip"):
                abertos.pop(chave, None)
        if acao in ("output", "build-output"):
            saidas[chave].append(e.get("Output", ""))
        elif acao == "pass" and e.get("Test"):
            passaram += 1
        elif acao == "skip" and e.get("Test"):
            # Sem Test, o skip é de um pacote sem arquivo de teste.
            pulados.append(chave)
        elif acao in ("fail", "build-fail"):
            falhos.append(chave)
    erros = []
    for chave in pulados:
        erros.append(f"pulado: {chave[0]} {chave[1]}\n" + "".join(saidas[chave]))
    for chave in abertos:
        erros.append(f"sem resultado: {chave[0]} {chave[1]}\n" + "".join(saidas[chave]))
    for chave in falhos:
        nome = " ".join(p for p in chave if p)
        erros.append(f"falhou: {nome}\n" + "".join(saidas[chave]))
    if passaram == 0:
        erros.append("nenhum teste passou: o relatório está vazio ou não é do go test -json")
    return erros, passaram


def testes_do_playwright(suite, caminho=()):
    caminho = (*caminho, suite["title"]) if suite.get("title") else caminho
    for spec in suite.get("specs", []):
        for teste in spec.get("tests", []):
            yield " › ".join((*caminho, spec["title"])), teste
    for filha in suite.get("suites", []):
        yield from testes_do_playwright(filha, caminho)


def checar_playwright(relatorio):
    stats = relatorio.get("stats") if isinstance(relatorio, dict) else None
    if not isinstance(stats, dict):
        return ["relatório sem stats: não é do reporter json do Playwright"], 0
    erros = [f"erro fora dos testes: {e.get('message', e)}" for e in relatorio.get("errors", [])
             if isinstance(e, dict)]
    pulados = stats.get("skipped", 0)
    if pulados:
        lista = []
        for suite in relatorio.get("suites", []):
            for titulo, teste in testes_do_playwright(suite):
                if teste.get("status") == "skipped":
                    motivos = [a.get("description", "") for a in teste.get("annotations", [])
                               if a.get("type") in ("skip", "fixme")]
                    lista.append(f"  {titulo}" + (f": {'; '.join(motivos)}" if motivos else ""))
        erros.append(plural(pulados, "teste pulado", "testes pulados") + "\n" + "\n".join(lista))
    inesperados = stats.get("unexpected", 0) + stats.get("flaky", 0)
    if inesperados:
        erros.append(plural(inesperados, "teste falhou", "testes falharam"))
    passaram = stats.get("expected", 0)
    if passaram == 0:
        erros.append("nenhum teste passou")
    return erros, passaram


def checar_vitest(relatorio):
    arquivos = relatorio.get("testResults") if isinstance(relatorio, dict) else None
    if not isinstance(arquivos, list):
        return ["relatório sem testResults: não é do reporter json do vitest"], 0
    erros, passaram = [], 0
    for arquivo in arquivos:
        nome = arquivo.get("name", "")
        nome = nome[nome.find("src/"):] if "src/" in nome else nome
        testes = arquivo.get("assertionResults", [])
        if arquivo.get("status") == "failed" and not any(t.get("status") == "failed" for t in testes):
            erros.append(f"falhou: {nome}\n{arquivo.get('message', '')}")
        for teste in testes:
            titulo = f"{nome} › {teste.get('fullName', '')}"
            status = teste.get("status")
            if status == "passed":
                passaram += 1
            elif status == "failed":
                erros.append(f"falhou: {titulo}\n" + "\n".join(teste.get("failureMessages", [])))
            else:
                # skipped, pending e todo: nenhum vale no CI.
                erros.append(f"pulado ({status}): {titulo}")
    if passaram == 0:
        erros.append("nenhum teste passou")
    return erros, passaram


CHECAGENS = {
    "go": lambda texto: checar_go(texto.splitlines()),
    "playwright": lambda texto: checar_playwright(json.loads(texto)),
    "vitest": lambda texto: checar_vitest(json.loads(texto)),
}


def main(argv=None):
    parser = argparse.ArgumentParser(description="Reprova o CI quando algum teste é pulado.")
    parser.add_argument("modo", choices=tuple(CHECAGENS))
    parser.add_argument("relatorio", type=Path)
    args = parser.parse_args(argv)
    try:
        erros, passaram = CHECAGENS[args.modo](args.relatorio.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError, UnicodeDecodeError) as e:
        print(f"::error::relatório ilegível ({args.relatorio}): {e}")
        return 1
    for erro in erros:
        print(f"::error::{erro.splitlines()[0]}")
        print(erro)
    if erros:
        return 1
    print(plural(passaram, "teste passou", "testes passaram") + ", nenhum pulado")
    return 0


if __name__ == "__main__":
    sys.exit(main())
