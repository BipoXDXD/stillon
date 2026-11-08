#!/usr/bin/env python3
"""Exercício de carga da medição de memória: muitos alvos, sondas e logins.

Cadastra alvos em domínios .invalid (RFC 6761: a sonda roda e falha na
resolução, sem tocar em site de terceiros) e, por alguns minutos, lê o resumo
no ritmo que o limitador por usuário aceita e dispara rajadas de login, cada
uma pagando um Argon2id. O resultado vai para o stdout e para o arquivo do
primeiro argumento.
"""
import collections
import concurrent.futures
import json
import sys
import threading
import time
import urllib.error
import urllib.request

BASE = 'http://127.0.0.1:8080'
ALVOS = 95
DURACAO_S = 240
RAJADA_LOGIN = 5
PAUSA_ENTRE_RAJADAS_S = 31  # o balde do login recarrega 5 fichas em 30 s
PAUSA_DA_API_S = 1.05  # 60 por minuto é o limite por usuário
EMAIL = 'demo@stillon.local'
SENHA = 'demo-stillon-2026'


def chamar(metodo, caminho, corpo=None, token=None):
    cabecalhos = {'X-Requested-With': 'stillon', 'Content-Type': 'application/json'}
    if token:
        cabecalhos['Authorization'] = 'Bearer ' + token
    dados = None if corpo is None else json.dumps(corpo).encode()
    pedido = urllib.request.Request(BASE + caminho, data=dados, method=metodo, headers=cabecalhos)
    try:
        with urllib.request.urlopen(pedido, timeout=60) as resposta:
            return resposta.status, resposta.read()
    except urllib.error.HTTPError as erro:
        return erro.code, erro.read()


def entrar():
    status, corpo = chamar('POST', '/api/v1/auth/login', {'email': EMAIL, 'password': SENHA})
    if status != 200:
        raise SystemExit(f'login da carga devolveu {status}: {corpo[:200]!r}')
    return json.loads(corpo)['access_token']


def cadastrar_alvos(token, contagem):
    for i in range(ALVOS):
        status, _ = chamar('POST', '/api/v1/targets', {
            'name': f'Carga {i}', 'url': f'https://alvo-{i}.carga.invalid/',
            'interval_sec': 30, 'timeout_ms': 5000,
        }, token)
        contagem[f'cadastro {status}'] += 1
        time.sleep(PAUSA_DA_API_S)


def ler_resumo(token, fim, contagem):
    while time.monotonic() < fim:
        status, _ = chamar('GET', '/api/v1/targets/summary', token=token)
        contagem[f'resumo {status}'] += 1
        time.sleep(PAUSA_DA_API_S)


def rajadas_de_login(fim, contagem):
    def um(i):
        # E-mail inexistente: paga o Argon2id do VerifyDummy sem travar a conta.
        status, _ = chamar('POST', '/api/v1/auth/login', {'email': f'ninguem{i}@carga.invalid', 'password': SENHA})
        return status

    with concurrent.futures.ThreadPoolExecutor(RAJADA_LOGIN) as grupo:
        while time.monotonic() < fim:
            for status in grupo.map(um, range(RAJADA_LOGIN)):
                contagem[f'login {status}'] += 1
            time.sleep(PAUSA_ENTRE_RAJADAS_S)


def main():
    contagem = collections.Counter()
    time.sleep(31)  # os baldes que o Playwright gastou
    token = entrar()
    cadastrar_alvos(token, contagem)
    fim = time.monotonic() + DURACAO_S
    linhas = [
        threading.Thread(target=ler_resumo, args=(token, fim, contagem)),
        threading.Thread(target=rajadas_de_login, args=(fim, contagem)),
    ]
    for linha in linhas:
        linha.start()
    for linha in linhas:
        linha.join()
    resultado = '\n'.join(f'{chave}: {valor}' for chave, valor in sorted(contagem.items()))
    print(resultado)
    with open(sys.argv[1], 'w') as arquivo:
        arquivo.write(resultado + '\n')


if __name__ == '__main__':
    main()
