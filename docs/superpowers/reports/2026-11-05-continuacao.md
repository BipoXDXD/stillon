# Continuidade do StillOn — 2026-11-05

Atualiza o [registro de 2026-10-14](2026-10-14-continuacao.md), preservando o histórico.

Tudo aqui foi conferido em 2026-11-05 com `git log`, `gh pr view` e `gh api`, ou vem do pedido do
dono (dito na própria linha). O que não deu para conferir está em "Não confirmado".

## Estado real

- **`main` em `a552515`** (PR #144), publicado e conferido: `ci` e `Coolify release` verdes nesse SHA
  e `https://stillon.tech/readyz` respondendo 200. Os runs do `ci` de `289ee7a` (#142) e `bae4d8e`
  (#143) foram cancelados pelo push seguinte; o de `a552515` cobre os três PRs.
- **Todo merge na `main` publica sozinho.** O `Coolify release` dispara quando o `ci` de um push na
  `main` termina com sucesso, confere o SHA exato e todos os gates e envia as imagens à VPS por SSH
  (detalhes em [`deploy/coolify/release/README.md`](../../../deploy/coolify/release/README.md)).
  O `deploy.yml` antigo (manual, por SHA) ainda existe no repositório.
- **Consequência para `[skip ci]`.** Commit com `[skip ci]` suprime o `ci` e, portanto, **não
  publica**; o README do release diz que um commit assim não pode ser publicado por esse fluxo. Para
  documentação isso é o desejado. Um PR com código não leva `[skip ci]`.
- **Environment `production`** existe desde 2026-10-14 (15:00 UTC), restrito por política de branch
  e sem revisor obrigatório. Guarda `COOLIFY_RELEASE_SSH_KEY` e `COOLIFY_RELEASE_KNOWN_HOSTS`. Os
  segredos `VPS_HOST`, `VPS_HOST_KEY` e `VPS_SSH_KEY` (do `deploy.yml`) seguem no nível do
  repositório.
- **Ruleset da `main`:** ativo (PR obrigatório e checks). O ruleset das tags `v*` também.
- **Migrations:** a última é a 00014; nenhuma nova nesta rodada, então a 00015 da vinculação
  Discord continua livre.
- **Go 1.26.10** no `go.mod` e no `Dockerfile` (fixado no #127).

## O que mudou desde 2026-10-14

Só os títulos, tirados do `git log --first-parent`; não revi o diff de cada um.

| PR | Título |
|---|---|
| #101–#105 | Onda 0 da refatoração; redesign, PRs 1 a 3 (já descritos no registro anterior) |
| #106 | `feat(painel): integrar detalhe do alvo do redesign` (2026-10-23) |
| #110–#118 | Publicação nativa pelo Coolify: stacks, release automático após a CI, receptor compartilhado 2.0.2, fila da publicação, proteção do build Docker |
| #119–#124 | Atualizações de dependências (builder Go, distroless, trivy, grupos Go e painel) |
| #125, #126 | `fix(backup)`: imagem aprovada do Coolify; `fix(ops)`: backup, revogação de acessos e isolamento de testes |
| #127 | Go 1.26.10 fixado para Docker e ferramentas de deploy |
| #128 | Fonte revisada do receptor compartilhado 2.1.1 |
| #130 | Tetos de CPU e memória menores em produção |

## Feito nesta rodada (2026-11-04 e 05)

| PR | O que entrou |
|---|---|
| #136 | Consolida os PRs #131, #133, #134 e #135 do Dependabot: `distroless/static-debian13` (`06bfc1e` → `c7b8faa`), `aquasec/trivy` 0.76.0 → 0.76.1, `docker/login-action` 4.6.0 → 4.7.0 e o grupo do painel (`@hookform/resolvers` 6.0.0, `@tanstack/react-query` 5.104.5, `lucide-react` 1.61.0, `react-router` 8.5.0, `oxlint` 1.89.0, `vite` 8.3.5). |
| #137 | Redesign, PR 5: tela de incidentes (resumo dos abertos, filtro segmentado, grupos "Em andamento" e "Resolvidos recentes", vazio positivo). |
| #138 | `fix(ci)`: o `docker` falso do teste do backup sai de `/run`, porque o runner `ubuntu-24.04` da imagem 20261018 monta `/run` com `noexec` e o job `workflows` do #136 falhava. Só o `bin/` falso mudou de lugar; os metadados continuam em `/run`. |
| #139 | Redesign, PR 6: canais, status page, formulário de alvo, configurações e login. Traz `internal/httpapi/testdata/faixa.json` com **9 casos** (a frase da faixa de estado), lido pelo teste TS. |
| #140 | Redesign, PR 7: página pública (tokens OKLCH no CSS inline, Fraunces e Inter em `/fonts/`, faixa em frase lida do mesmo `faixa.json`, barras de 90 dias com forma e cor, CSP com `font-src 'self'`, gzip). |
| #141 | Redesign, PR 8, que fecha o redesign: `sistema.test.ts` em todo o `src`, 44 capturas em `docs/design/painel-depois/` e README. Mesclado com o CI verde e a revisão aprovada; a verificação local completa não ficou verde por carga da máquina. |
| #142 | `fix(web)`: custo de carregar módulos fora do prazo dos testes. Só imports e comentários. |
| #143 | `fix(web)`: contraste do item ativo do controle segmentado, foco do "Carregar mais" e limpeza do e2e público. |
| #144 | `test(backup)`: diretório executável de verdade e ancestralidade até `/`. Só muda o teste. |

**Fechado sem merge:** o #132 (`golang` 1.26.10 → 1.27.2-alpine) foi recusado porque o 1.27.2 tem a
CVE-2026-111943 (texto do #136); a `main` fica no 1.26.10, que traz a correção. Os #131, #133, #134 e
#135 foram fechados quando o #136 entrou.

## Em andamento

- **Tempo do `httpapi`:** medido; falta escolher como reduzir. Ver "Backlog técnico".

## Decisões pendentes do dono

Cada item diz o que decidir. Nenhum foi decidido.

1. **Termo "Em andamento" na tela de incidentes (#137).** Tem dois sentidos: no resumo ("1 em
   andamento, 1 reconhecido") são só os sem reconhecimento; no grupo "Em andamento" entram também os
   reconhecidos. A spec pede os dois. Decidir se mantém os dois sentidos ou se um deles ganha outro
   nome.
2. **Dois botões primários em Configurações.** O #139 diz que o número de botões primários "não
   mudou (decisão do dono)". Decidir se fica assim ou se a tela passa a ter um só primário.
3. **Forma do dia "sem dados" a 400 px na página pública (#140).** A 400 px cada dia tem 2,72 px; o
   contorno de 1 px fechava o miolo e "no ar" e "sem dados" só se distinguiam pela cor (WCAG 1.4.1).
   O coordenador decidiu na revisão: abaixo de 40rem, "sem dados" é um traço baixo cheio
   (`height:12%`); a partir de 40rem, o contorno da spec, agora com `box-shadow` inset. Decidir se
   aceita esse desvio da spec.
4. **Leitura dos 10 KB da §10.1 com gzip (#140).** A spec pede "resposta abaixo de 10 KB". O
   `TestPublicPageFitsInTenKilobytes` mede o corpo **comprimido** contra os 10 KB e o texto puro do
   pior caso (5 serviços) contra 16 KB. Medido: **12.525 B em texto e 2.311 B em gzip** no pior caso
   de 5 serviços; com 20 serviços (o máximo), 31.553 B e 2.710 B. O caso de 20 serviços só tem teto
   comprimido (o teto de 16 KB em texto cobre os 5). O gzip foi aceito em 2026-10-14, mas a leitura
   "10 KB = comprimido" fica para o dono confirmar.
5. **Rótulos diferentes na prévia da faixa (#139).** Na prévia da status page, o cartão usa os rótulos
   do painel ("Degradado", "Pausado") e a faixa usa os da página pública ("Lento", "Sem dados"), como
   o plano pedia. Decidir se unifica.
6. **Controle de janela do gráfico a 400 px (#141).** Quebra em duas linhas e deixa "90 dias"
   sozinho; "Latência mediana (24 h)" também quebra no cartão do indicador
   (`detalhe-alvo-400-*.png`). Decidir o ajuste de design.
7. **"Painel" ativo em `/targets/new` e no detalhe do alvo (#141).** A navegação marca "Painel"
   nessas duas telas. Decidir se está certo ou se deve mudar.
8. **2FA do usuário do StillOn.** Ligar (runbook 5.2); adiado por decisão do dono em 2026-10-14.
9. **Login do StillOn no Bitwarden.** Mover para a entrada "StillOn – login do painel".
10. **D1–D3 da vinculação Discord** ([plano](../plans/2026-10-05-vinculacao-discord.md)): D1 PKCE, D2
    mecanismo de cargo, D3 provedor falso no E2E. O plano diz para não executar antes disso.
11. **Segredos da VPS no nível do repositório.** O environment `production` já existe e guarda as
    chaves do Coolify; `VPS_HOST`, `VPS_HOST_KEY` e `VPS_SSH_KEY` seguem no repositório para o
    `deploy.yml` manual. Decidir se migram e se o environment ganha revisor obrigatório.
12. **Migração para Go 1.27.3 ou mais novo.** O #119 (builder 1.27.3) entrou em 2026-10-24 e o #127
    fixou o 1.26.10 em 2026-10-28; o #132 foi fechado por causa da CVE no 1.27.2. Decidir quando
    migrar e para qual versão.
13. **`stash@{0}` local**, criado em `chore/volumes-locais-2026` em 2026-11-04: "compose com volume
    externo `stillon-043-2026` + build docker antigo". O stash é compartilhado entre as worktrees;
    nada foi aplicado nem apagado. Decidir se descarta, aplica ou vira PR.
14. **Trilho do controle segmentado (#143).** A borda do item ativo ganhou `--borda-controle`, com
    contraste de 3:1; o trilho segue decorativo, com 1,09 e 1,07. Decidir se ele também deve contrastar.
15. **Foco do `status=all` no "Carregar mais" (#143).** Decidir o comportamento quando a lista muda.
16. **Capturas anteriores ao contorno do #143.** As de `docs/design/painel-depois/` são do #141 e não
    mostram o contorno novo. Decidir se recaptura agora ou no próximo ajuste visual.

### VPS: fatos conferidos em 2026-11-05

Conferido na API da Hostinger pela sessão coordenadora; não há decisão do dono aqui.

- **Produção:** a VPS atual (Coolify), `running`, paga até **2027-10-17**, sem
  renovação automática.
- **Antiga:** a VPS anterior, cancelada de propósito, venceu em 2026-10-25 e está
  `suspended`. Nunca houve plano de renová-la.
- **Acesso:** `ssh stillon` entra como `root` na VPS de produção, com a mesma chave do `Host c2vps`
  deste Mac. O usuário e a chave `stillon-admin` do runbook eram da VPS antiga.

### Itens herdados de 2026-10-14

Não estavam na lista do pedido e não verifiquei se continuam abertos. Vêm do registro anterior e
dos PRs desta rodada.

- Posição da ajuda do Reconhecer (#137): a spec a põe entre o resumo e o filtro; no código fica
  dentro da lista, depois do filtro, para o `aria-describedby` nunca apontar para um id inexistente.
  O #137 marcou como decisão de design para o dono.
- Frase do login (`FRASE_DO_LOGIN`, "Seus serviços, vigiados de perto.", #139): o dono confere.
- Oráculo de host interno (#97, parcial): um nome interno com ponto ainda pode distinguir "existe" de
  "não existe"; a correção completa exige resolver os alvos por um DNS público.
- E-mail noreply nos commits; dumps públicos do backup por 30 dias.
- Tipos da API: gerar de uma spec OpenAPI ou manter o `schemas.ts` escrito à mão.
- Lembretes: canal pausado por mais de 30 dias com incidente aberto perde a base dos lembretes
  (recomendação: registrar a limitação).
- O ack de incidente não gera linha no `audit_log` (achado menor).
- "Exigir sessão viva nas rotas de recurso": o título do #126 (revogação de acessos) sugere que foi
  tratado, e o `CLAUDE.md` já descrevia a regra; não reconferi o código.

## Backlog técnico

- **Detectores que o `web/src/sistema.test.ts` ainda não pega** (lista do dono; conferi que o
  arquivo não tem nenhum destes padrões): `p-(--x)`, `rounded-(--x)`, `text-(--cor)`, `bg-(--cor)`,
  `shadow-(--s)`, `font-[500]`, `drop-shadow-lg` e `leading-[1.1]`.
- **Testes web sob carga: melhoria parcial (#142).** A suíte aguenta carga de 15 a 20; de 35 a 50
  ainda estoura por falta de CPU. Quatro testes de `configuracoes.test.tsx` e um de
  `formulario.cobertura.test.tsx` falham sob essa carga, também na `main`.
- **Tempo do `httpapi`: 1063 s no MacBook Air.** Medido em 2026-11-05 em `21fdec7`, com
  `go test -race -count=1`, carga máxima de 7,33 e nenhum container antes de começar: 194 testes de
  topo e 107 subtestes, mediana de 3 s por teste. O CI levava cerca de 210 s. Uma medição anterior,
  de 1267,5 s, foi descartada porque a carga subiu para 15.
  - `TestParallelWrongProofsVerifyAtMostTheBudget` falhava sempre neste Mac com `-race` (6 em 6
    rodadas isoladas), com qualquer carga. O `x/crypto/argon2` só tem assembly em amd64. Em arm64
    com `-race`, um Argon2id leva 1,5 s sozinho e de 3,3 a 4,0 s com quatro simultâneos, acima dos
    2 s de `Argon2Wait`: a quinta prova desiste. Sem `-race` leva até 0,54 s. Não afeta produção
    (amd64, sem `-race`). A correção, em `fix/orcamento-provas-sem-prazo`, dá ao teste cinco vagas.
  - Numa rodada do pacote inteiro o aviso `teto de Argon2id` some do log: a hipótese, não provada, é
    que o `slog.SetDefault` de `TestCanaryInSensitiveFieldsNeverLeaks` deixa `log` apontando para o
    buffer dele mesmo depois de restaurado.
  - Falta medir quanto dos 1063 s é Argon2id. Se for o custo dominante, o custo reduzido entra por
    um campo em `httpapi.Deps`, nulo em produção (recomendado, com teste HTTP afirmando
    `m=65536,t=3,p=2`), ou por um setter global em `auth` (diff menor, mas expõe um controle de
    custo de senha). `TestLoginRehashDoesNotUndoAPasswordReset` mantém o custo real.
- **Lacunas do teste do backup: fechadas (#144).** Sobrou um defeito no código, abaixo.
- **Alias legado do backup: defeito aberto, severidade média-baixa.** Em
  `deploy/vps/stillon-entrada`, `imagem_helper_aprovada` cai em `stillon:<sha>` quando não há
  `current.json`, sem conferir os ancestrais de `ESTADO_COOLIFY`. Sem PR por enquanto. Só muda algo
  se um ancestral já for gravável por não root, a correção mexe nos fixtures do `checar-vps.sh` e do
  `test_legado_sem_recibo_continua_funcionando` (que rodam fora de árvore root) e, para valer, exige
  uma passagem operacional na VPS, que este Mac não alcança hoje. Ao corrigir: tudo dentro da
  função, casos novos e `docs/deploy/vps.md` atualizado.
- **Painel, itens pequenos:** nenhum teste quebra se a ajuda do Reconhecer passar a aparecer sempre;
  a fixture do axe no e2e não tem cartão reconhecido nem resolvido; no filtro "Resolvidos", depois de
  "Carregar mais", as páginas antigas continuam sob "Resolvidos recentes". Da revisão do #143: foco
  com `activeElement` em `body`, POST sem desfazer antes da resposta, `foco` literal em
  `tokens.test.ts` e `setTimeout(50)`.
- **Da revisão do #141:** `.cts/.mjs/.cjs` e `@apply` no `sistema.test.ts`, `%v` com `*float64` em
  `publicpage_test.go`, comentários de `capturas.spec.ts` e `exemploDaPaginaPublica` e `preparar:
  prepararPainel` nas capturas de incidentes.
- **`ECONNRESET` na limpeza do e2e público:** apareceu sob carga; causa não provada.
- **Refatoração geral:** o [backlog de 2026-10-14](2026-10-14-backlog-refatoracao.md) (Ondas 1 a 6)
  segue valendo; a Onda 0 foi feita (#101, #102).

## Não confirmado

- Quando o StillOn passou a rodar pelo Coolify na VPS, e a topologia atual da VPS. O README e o
  `compose.prod.yaml` ainda descrevem a borda Caddy em `/srv/edge`; o runbook já fala em
  "instalação Coolify". Agora dá para conferir por `ssh stillon`.
- Se o `deploy.yml` manual segue em uso.

## Armadilhas

- **Sem redeploy do banco** até restaurar a imagem por nome: ver `deploy/coolify/README.md`.
- `[skip ci]` no título do PR ou do commit pula o `ci` da `main` e **impede a publicação** daquele
  commit. Serve para documentação; para código, nunca.
- O `stash` do git é compartilhado entre as worktrees: nada de `git stash` ou `pop` sem tag.
- O `make test` roda melhor com a máquina livre; com vários agentes ao mesmo tempo, o
  `internal/httpapi` estoura o teto.

Não confundir planejamento aprovado com código pronto, nem anunciar deploy sem conferência.
