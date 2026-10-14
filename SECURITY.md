# Política de segurança

## Versões cobertas

Só a `main` e a imagem publicada a partir dela (`ghcr.io/bipoxdxd/stillon`) recebem correção. Não
há versões antigas mantidas em paralelo.

## Como relatar uma vulnerabilidade

**Não abra issue pública.** Relate em particular pelo GitHub:
[Security → Report a vulnerability](https://github.com/BipoXDXD/stillon/security/advisories/new).

Ajuda incluir:

- a rota, o componente ou o arquivo afetado;
- os passos para reproduzir (uma requisição `curl` basta) e o impacto que você observou;
- a versão (`stillon version` ou o commit).

Não teste contra a instância de produção (`stillon.tech`) nada além do necessário para provar o
problema: nada de negação de serviço, varredura em massa nem acesso a dados de outras contas.
Para reproduzir com calma, suba a pilha local com `make up` (veja o README).

## O que esperar

- Confirmação de recebimento em até 7 dias.
- Uma avaliação (aceito, precisa de mais dados ou fora do escopo) em até 14 dias.
- A correção entra com um teste que falha sem ela, e o aviso público (GitHub Security Advisory)
  sai depois que a imagem corrigida estiver em produção, com crédito a quem relatou, se quiser.

## Escopo

Dentro: a API e o painel (`internal/`, `web/`), o sondador e o isolamento entre contas, os
avisos, o deploy (`deploy/`) e os workflows (`.github/`).

Fora: falhas que exigem acesso prévio à VPS ou à conta do GitHub do dono, relatórios só de
ferramenta automática sem impacto demonstrado, e a ausência de cabeçalhos que não se aplicam a uma
API JSON.
