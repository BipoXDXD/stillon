#!/usr/bin/env python3
"""Fecha a medição de memória: pico, OOM e reinícios de cada contêiner.

argv: arquivo de amostras, arquivo da carga, resultado do Playwright e
resultado do passo da carga (os `outcome` do GitHub Actions). Sai 1 se houve
OOM, reinício ou teste vermelho, e também quando falta evidência: carga que não
concluiu, contêiner sem nenhuma amostra ou cgroup que não pôde ser lido. Sem
isso, uma medição que não mediu nada sairia verde.

CGROUP_RAIZ troca a raiz do cgroup v2 (padrão /sys/fs/cgroup), para o teste.
"""
import json
import os
import re
import subprocess
import sys

UNIDADES = {'B': 1, 'KiB': 1024, 'MiB': 1024**2, 'GiB': 1024**3}
SERVICOS = ['app', 'caddy', 'db']
CGROUP_RAIZ = os.environ.get('CGROUP_RAIZ', '/sys/fs/cgroup')


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
        with open(f'{CGROUP_RAIZ}/system.slice/docker-{ident}.scope/{nome}') as arquivo:
            return arquivo.read()
    except OSError:
        return None


def ler_oom_kill(ident):
    achado = re.search(r'^oom_kill (\d+)$', ler_cgroup(ident, 'memory.events') or '', re.MULTILINE)
    return int(achado.group(1)) if achado else None


def linha_do_servico(servico, picos, quantas):
    """Devolve a linha da tabela e os problemas do serviço, por categoria."""
    ident = executar('docker', 'compose', 'ps', '-aq', '--no-trunc', servico)
    if not ident:
        return f'| {servico} | sem contêiner | | | | | | | | |', {'sem contêiner'}
    info = json.loads(executar('docker', 'inspect', ident))[0]
    nome = info['Name'].lstrip('/')
    estado = info['State']
    env = [e.split('=', 1)[1] for e in info['Config']['Env'] if e.startswith('GOMEMLIMIT=')]
    peak = ler_cgroup(ident, 'memory.peak')
    oom = ler_oom_kill(ident)
    teto = info['HostConfig']['Memory']
    problemas = set()
    if estado['OOMKilled'] or info['RestartCount'] or oom:
        problemas.add('OOM ou reinício')
    if not quantas.get(nome):
        problemas.add('sem amostras')
    if not peak or oom is None:
        problemas.add('cgroup ilegível')
    linha = (f"| {servico} | {mib(teto) if teto else 'sem teto'} | {env[0] if env else '-'} | "
             f"{mib(picos.get(nome))} | {quantas.get(nome, 0)} | {mib(float(peak)) if peak else '?'} | "
             f"{oom} | {estado['OOMKilled']} | {info['RestartCount']} | {estado['Status']} |")
    return linha, problemas


def problema_da_carga(resultado, arquivo_carga):
    if resultado != 'success':
        return f'carga não concluiu: {resultado}'
    if not os.path.exists(arquivo_carga):
        return 'carga não concluiu: sem resultado'
    return None


def main():
    amostras, arquivo_carga, playwright, resultado_carga = sys.argv[1:5]
    subprocess.run(['pkill', '-f', 'amostrar.sh'], check=False)
    picos, quantas = pico_das_amostras(amostras)
    linhas = ['| serviço | teto (MiB) | GOMEMLIMIT | pico docker stats (MiB) | amostras | memory.peak (MiB) '
              '| oom_kill | OOMKilled | reinícios | estado |',
              '|---|---|---|---|---|---|---|---|---|---|']
    por_problema = {}
    for servico in SERVICOS:
        linha, problemas = linha_do_servico(servico, picos, quantas)
        linhas.append(linha)
        for problema in problemas:
            por_problema.setdefault(problema, []).append(servico)
    falhas = [f'{problema}: {", ".join(servicos)}' for problema, servicos in sorted(por_problema.items())]
    if playwright != 'success':
        falhas.append(f'Playwright não passou: {playwright}')
    if (carga := problema_da_carga(resultado_carga, arquivo_carga)) is not None:
        falhas.append(carga)

    relatorio = '\n'.join(linhas) + f'\n\nPlaywright: {playwright}\n'
    if os.path.exists(arquivo_carga):
        with open(arquivo_carga) as arquivo:
            relatorio += '\nCarga:\n\n```\n' + arquivo.read() + '```\n'
    relatorio += '\nSondas gravadas: ' + executar(
        'docker', 'compose', 'exec', '-T', 'db', 'psql', '-U', 'postgres', '-d', 'stillon', '-Atq',
        '-c', 'SELECT count(*) FROM checks') + '\n'
    if falhas:
        relatorio += '\nMedição vermelha:\n\n' + ''.join(f'- {falha}\n' for falha in falhas)
    print(relatorio)
    resumo = os.environ.get('GITHUB_STEP_SUMMARY')
    if resumo:
        with open(resumo, 'a') as arquivo:
            arquivo.write(relatorio)
    sys.exit(1 if falhas else 0)


if __name__ == '__main__':
    main()
