#!/usr/bin/env python3
"""Fecha a medição de memória: pico, OOM e reinícios de cada contêiner.

argv: arquivo de amostras, arquivo da carga (pode faltar) e o resultado do
Playwright. Sai 1 se houve OOM, reinício ou teste vermelho.
"""
import json
import os
import re
import subprocess
import sys

UNIDADES = {'B': 1, 'KiB': 1024, 'MiB': 1024**2, 'GiB': 1024**3}
SERVICOS = ['app', 'caddy', 'db']


def executar(*args):
    return subprocess.run(args, capture_output=True, text=True, check=False).stdout.strip()


def bytes_de(texto):
    m = re.fullmatch(r'([\d.]+)(B|KiB|MiB|GiB)', texto)
    return float(m.group(1)) * UNIDADES[m.group(2)] if m else None


def mib(valor):
    return f'{valor / 1024**2:.1f}' if valor is not None else '?'


def pico_das_amostras(caminho):
    picos, quantas = {}, {}
    if os.path.exists(caminho):
        for linha in open(caminho):
            partes = linha.split()
            if len(partes) != 3 or (valor := bytes_de(partes[2])) is None:
                continue
            picos[partes[1]] = max(picos.get(partes[1], 0), valor)
            quantas[partes[1]] = quantas.get(partes[1], 0) + 1
    return picos, quantas


def ler_cgroup(ident, nome):
    try:
        with open(f'/sys/fs/cgroup/system.slice/docker-{ident}.scope/{nome}') as arquivo:
            return arquivo.read()
    except OSError:
        return None


def linha_do_servico(servico, picos, quantas):
    """Devolve a linha da tabela e se o serviço teve problema."""
    ident = executar('docker', 'compose', 'ps', '-aq', '--no-trunc', servico)
    if not ident:
        return f'| {servico} | sem contêiner | | | | | | | | |', True
    info = json.loads(executar('docker', 'inspect', ident))[0]
    nome = info['Name'].lstrip('/')
    estado = info['State']
    env = [e.split('=', 1)[1] for e in info['Config']['Env'] if e.startswith('GOMEMLIMIT=')]
    peak = ler_cgroup(ident, 'memory.peak')
    achado = re.search(r'oom_kill (\d+)', ler_cgroup(ident, 'memory.events') or '')
    oom = int(achado.group(1)) if achado else None
    teto = info['HostConfig']['Memory']
    ruim = bool(estado['OOMKilled'] or info['RestartCount'] or oom)
    return (f"| {servico} | {mib(teto) if teto else 'sem teto'} | {env[0] if env else '-'} | {mib(picos.get(nome))} | "
            f"{quantas.get(nome, 0)} | {mib(float(peak)) if peak else '?'} | {oom} | {estado['OOMKilled']} | "
            f"{info['RestartCount']} | {estado['Status']} |"), ruim


def main():
    amostras, arquivo_carga, playwright = sys.argv[1:4]
    subprocess.run(['pkill', '-f', 'amostrar.sh'], check=False)
    picos, quantas = pico_das_amostras(amostras)
    linhas = ['| serviço | teto (MiB) | GOMEMLIMIT | pico docker stats (MiB) | amostras | memory.peak (MiB) '
              '| oom_kill | OOMKilled | reinícios | estado |',
              '|---|---|---|---|---|---|---|---|---|---|']
    ruim = playwright != 'success'
    for servico in SERVICOS:
        linha, problema = linha_do_servico(servico, picos, quantas)
        linhas.append(linha)
        ruim = ruim or problema
    relatorio = '\n'.join(linhas) + f'\n\nPlaywright: {playwright}\n'
    if os.path.exists(arquivo_carga):
        with open(arquivo_carga) as arquivo:
            relatorio += '\nCarga:\n\n```\n' + arquivo.read() + '```\n'
    relatorio += '\nSondas gravadas: ' + executar(
        'docker', 'compose', 'exec', '-T', 'db', 'psql', '-U', 'postgres', '-d', 'stillon', '-Atq',
        '-c', 'SELECT count(*) FROM checks') + '\n'
    print(relatorio)
    resumo = os.environ.get('GITHUB_STEP_SUMMARY')
    if resumo:
        with open(resumo, 'a') as arquivo:
            arquivo.write(relatorio)
    sys.exit(1 if ruim else 0)


if __name__ == '__main__':
    main()
