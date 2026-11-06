#!/usr/bin/env python3
"""Fala com a borda como o Traefik do Coolify: PROXY v2 e depois TLS.

SÓ PARA O TESTE (deploy/ci/checar-borda-cloudflare.sh). --origem é o IP que o
cabeçalho PROXY declara, ou seja, quem o Traefik diz que conectou. Cada comando
imprime o resultado numa linha, e quem decide se ele está certo é o script.
"""

import argparse
import http.client
import ipaddress
import json
import os
import socket
import ssl
import struct
import sys
import uuid

BORDA_V4 = "172.16.1.2"
# Só o endereço de destino do cabeçalho: a conexão continua sendo pelo IPv4.
BORDA_V6 = "2001:db8:ffff::2"
DOMINIO = "stillon.tech"
PORTA = 443
PORTA_DE_ORIGEM = 40000
PRAZO_SEGUNDOS = 15

ASSINATURA_PROXY_V2 = b"\r\n\r\n\x00\r\nQUIT\n"
VERSAO_2_PROXY = 0x21
TCP_SOBRE_IPV4 = 0x11
TCP_SOBRE_IPV6 = 0x21


def cabecalho_proxy_v2(origem):
    ip = ipaddress.ip_address(origem)
    familia, destino = (
        (TCP_SOBRE_IPV4, BORDA_V4) if ip.version == 4 else (TCP_SOBRE_IPV6, BORDA_V6)
    )
    enderecos = (
        ip.packed
        + ipaddress.ip_address(destino).packed
        + struct.pack("!HH", PORTA_DE_ORIGEM, PORTA)
    )
    return (
        ASSINATURA_PROXY_V2
        + bytes([VERSAO_2_PROXY, familia])
        + struct.pack("!H", len(enderecos))
        + enderecos
    )


class ConexaoPelaBorda(http.client.HTTPConnection):
    def __init__(self, origem):
        super().__init__(BORDA_V4, PORTA, timeout=PRAZO_SEGUNDOS)
        self._origem = origem

    def connect(self):
        bruto = socket.create_connection((self.host, self.port), self.timeout)
        bruto.sendall(cabecalho_proxy_v2(self._origem))
        contexto = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
        contexto.minimum_version = ssl.TLSVersion.TLSv1_2
        # O certificado é o da CA interna de um contêiner descartável, e o
        # teste é sobre o IP do cliente: o TLS só precisa existir.
        contexto.check_hostname = False
        contexto.verify_mode = ssl.CERT_NONE
        contexto.set_alpn_protocols(["http/1.1"])
        self.sock = contexto.wrap_socket(bruto, server_hostname=DOMINIO)


def pedir(conexao, metodo, caminho, cabecalhos, corpo=b""):
    # putheader, e não request(): dois CF-Connecting-IP têm de sair em duas
    # linhas, e o dicionário do request() juntaria as duas.
    conexao.putrequest(metodo, caminho, skip_host=True, skip_accept_encoding=True)
    conexao.putheader("Host", DOMINIO)
    for nome, valor in cabecalhos:
        conexao.putheader(nome, valor)
    conexao.putheader("Content-Length", str(len(corpo)))
    conexao.endheaders(corpo)
    resposta = conexao.getresponse()
    resposta.read()
    return resposta.status


def cabecalhos_do_cliente(args):
    cabecalhos = [("CF-Connecting-IP", valor) for valor in args.cf]
    if args.xff is not None:
        cabecalhos.append(("X-Forwarded-For", args.xff))
    return cabecalhos


def saude(args):
    print(pedir(ConexaoPelaBorda(args.origem), "GET", "/healthz", []))


def login(args):
    corpo = json.dumps(
        {"email": os.environ["TESTE_EMAIL"], "password": os.environ["TESTE_SENHA"]}
    ).encode()
    cabecalhos = cabecalhos_do_cliente(args) + [
        ("Content-Type", "application/json"),
        ("X-Requested-With", "stillon"),
    ]
    print(pedir(ConexaoPelaBorda(args.origem), "POST", "/api/v1/auth/login", cabecalhos, corpo))


def ping(args):
    """Imprime "<enviados> <recusados com 429>", numa conexão só."""
    conexao = ConexaoPelaBorda(args.origem)
    cabecalhos = cabecalhos_do_cliente(args)
    enviados = recusados = 0
    for _ in range(args.quantos):
        # Token novo a cada envio: o balde por token, com rajada de 10, não
        # pode ser o que recusa. O token não existe, então o normal é 404.
        codigo = pedir(conexao, "POST", f"/ping/{uuid.uuid4().hex}", cabecalhos)
        enviados += 1
        if codigo == http.client.TOO_MANY_REQUESTS:
            recusados += 1
            if args.parar_no_429:
                break
        elif codigo != http.client.NOT_FOUND:
            sys.exit(f"ping respondeu {codigo}, queria 404 ou 429")
    print(enviados, recusados)


def argumentos():
    leitor = argparse.ArgumentParser(description=__doc__)
    comandos = leitor.add_subparsers(required=True)
    for nome, funcao in (("saude", saude), ("login", login), ("ping", ping)):
        comando = comandos.add_parser(nome)
        comando.set_defaults(funcao=funcao)
        comando.add_argument("--origem", required=True)
        comando.add_argument("--cf", action="append", default=[])
        comando.add_argument("--xff")
        if nome == "ping":
            comando.add_argument("--quantos", type=int, required=True)
            comando.add_argument("--parar-no-429", action="store_true")
    return leitor.parse_args()


if __name__ == "__main__":
    args = argumentos()
    args.funcao(args)
