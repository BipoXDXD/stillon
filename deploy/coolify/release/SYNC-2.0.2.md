# Sincronização do receptor 2.0.2 — 21/10/2026

O StillOn recebeu o código e metadados no [PR #113](https://github.com/BipoXDXD/stillon/pull/113),
integrado em `f2fe1950e93382eb3c4813252588abd7b7b45096`. A conferência posterior confirma
igualdade byte a byte de `receiver.py` e de `receiver-release.json` com os artefatos aprovados em
[Duora #168](https://github.com/BipoXDXD/duora/pull/168) e
[GoblinShift #125](https://github.com/BipoXDXD/GoblinShift/pull/125).

- Versão: **2.0.2**; protocolo: **2**.
- SHA-256 de receiver.py: `1ccd1ada4777c80081c4f9bb2a64a500b1e221f794406319504d9e66cbb975cb`.
- Duora conferido no head `33719c777e50faf68a1a8db750848d83808320df`.
- GoblinShift conferido no head `be46f82bf0494ba48f9ee3980b4153b2be5ec19b`.

O receptor consulta o config validado e admite fallback para o manifesto apenas em HTTP 404
tipado. O runtime continua vinculado ao grafo OCI e à referência imutável. Saúde `starting` após
a fila Coolify terminar tem espera limitada; identidade incorreta ou falha conserva a pendência.
A trava de backup do StillOn e os procedimentos de reconciliação e rollback foram preservados.

## Verificar sem acessar a VPS

Com os três checkouts dos commits revisados:

```sh
python3 deploy/coolify/release/verify_artifact.py --peer /caminho/duora/scripts/release/coolify
python3 deploy/coolify/release/verify_artifact.py --peer /caminho/GoblinShift/deploy/coolify/release
python3 -m unittest discover -s deploy/coolify/release -v
python3 deploy/coolify/release/assets.py
```

Rodar a partir da raiz do StillOn, usando Python 3.12+. A comparação é somente leitura e
recusa bytes, versão ou protocolo divergentes. CI de um repositório isolado não verifica os
outros checkouts nem o executável da VPS. Os testes integrados existentes conferem o hash local,
recusam drift e exercitam a coordenação de host/projeto/backup com filesystem e processos reais,
sem Docker/Coolify de produção. Os 46 testes de release e os 46 assets originais passaram na
conferência local desta sincronização.

O [CI218](https://github.com/BipoXDXD/stillon/actions/runs/39688654575) e o
[release-tools do #113](https://github.com/BipoXDXD/stillon/actions/runs/39688654645) terminaram
com sucesso. O PR complementar da documentação deve ter seus próprios checks concluídos antes
de ser declarado pronto para revisão; não confundir a rodada do #113 com um head posterior.
Nenhum workflow, gate, código, metadado, Compose, asset, banco ou volume é alterado por este complemento.

## Homologação e instalação são etapas distintas

A homologação descartável anterior do mesmo hash usou Docker 29.9.2/containerd 2.4.2, Coolify 4.4.7
e PostgreSQL 18 reais com workloads sintéticos dos projetos Duora/GoblinShift. Resultados e limites:
[relatório fixado do Duora](https://github.com/BipoXDXD/duora/blob/33719c777e50faf68a1a8db750848d83808320df/scripts/release/coolify/HOMOLOGATION.md).
Ela não é um ensaio completo da aplicação StillOn, de sua cadeia de backup ou da VPS de produção.
Não se afirma compatibilidade do tar OCI bruto com store clássico; essa variante recusou o transporte
no laboratório. Reboot/perda da VPS, pgBackRest/WAL, carga real e schema incompatível não foram homologados.

Esta sincronização lê fontes e executa testes locais/CI: não instala, ativa ou testa na VPS e
não executa deploy em produção. A instalação exige revisão humana própria, verificação dos três
artefatos aprovados e do executável instalado, filas/intents reconciliados e troca atômica sob
as travas existentes. Não ativar parcialmente e não remover intents para facilitar uma atualização.
O runbook em [ISOLATION.md](ISOLATION.md) preserva a versão anterior e o rollback compatível.
