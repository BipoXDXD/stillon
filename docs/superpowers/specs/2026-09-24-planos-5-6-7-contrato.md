# Contrato entre os Planos 5, 6 e 7

O roadmap previa um "Plano 5" com painel React, deploy e README. Em 2026-09-24 o usuário decidiu
dividi-lo em três planos, escritos em paralelo por agentes separados. Este documento é o que os
três compartilham. Nenhum agente muda o que está aqui sozinho; uma divergência vai para a seção
"Divergências" da spec do agente, e é reconciliada na revisão.

| Plano | Escopo | Spec |
|---|---|---|
| 5 | Lacunas da API para o painel (Go) | `docs/superpowers/specs/2026-09-24-plano-5-api-do-painel-design.md` |
| 6 | Painel React (TypeScript) e o servir do painel pelo binário | `docs/superpowers/specs/2026-09-24-plano-6-painel-react-design.md` |
| 7 | Empacotamento, compose, CI, deploy e README | `docs/superpowers/specs/2026-09-24-plano-7-empacotamento-e-deploy-design.md` |

Ordem de execução: 5 → 6 → 7. O 6 é escrito contra este contrato; o 7, contra os dois.

## Decisões já tomadas

1. **Usuário inicial por subcomando de CLI** (decisão do usuário): `vigilume user create --email <e>`
   lê a senha do stdin (sem eco num terminal; aceita pipe, para o compose). Nenhuma rota pública
   de cadastro. `vigilume user set-password --email <e>` é o caminho de recuperação (não há
   "esqueci a senha" por e-mail num sistema de usuário único).
2. **O painel é servido pelo binário Go, na mesma origem da API.** O build do Vite sai em
   `internal/webui/dist/`, embutido por `internal/webui` com `//go:embed all:dist` (um
   `.gitkeep` versionado mantém o diretório existindo antes do primeiro build). Motivo: o cookie
   de refresh é `SameSite=Strict; Path=/api/v1/auth`, e o painel precisa estar no mesmo site;
   mesma origem também dispensa CORS em produção e mantém um deploy só. O handler que serve o
   painel (arquivos com hash de conteúdo com cache longo, `index.html` sem cache, fallback de SPA
   para rotas do painel, CSP própria) é do **Plano 6**. `/api/`, `/p/`, `/verify/`, `/healthz`,
   `/readyz` continuam como estão.
3. **Desenvolvimento local do painel:** `vite` com proxy de `/api` para `http://localhost:8080`.
   `CORS_ORIGIN` continua existindo para quem servir o painel em outro host.
4. **Toda mutação envia `X-Requested-With: vigilume`** (spec §11.2 item 4). Envelope de erro
   da spec §9.1 com `fields` de vocabulário fechado.
5. **Código novo de banco em sqlc.** A migração de `auth.PGStore`/`auth.TOTP` para sqlc (R2) é
   plano próprio e não entra em nenhum dos três.
6. **Deploy real no Fly.io só com confirmação explícita do usuário** (ação externa, conta dele).
   O Plano 7 entrega os artefatos (`fly.toml`, runbook) e o passo de deploy fica marcado como
   manual.

## API nova (Plano 5 implementa, Plano 6 consome)

Todas sob `/api/v1`, autenticadas (Bearer) salvo indicação. Erros no envelope da §9.1.

### `GET /me` (muda)

```json
{ "id": "…uuid…", "email": "dono@exemplo.com", "totp_enabled": true, "created_at": "RFC3339" }
```

### `POST /auth/password`

Troca a senha. Corpo `{"current_password": "…", "new_password": "…", "totp_code": "123456"}`;
`totp_code` só quando o 2FA está ligado. Sucesso: `204`. Revoga todas as outras sessões (famílias
de refresh) do usuário e mantém a atual. Senha atual errada ou código TOTP errado: `401
invalid_credentials`. Senha nova fora da política: `400 validation_failed` com
`fields: [{"field": "new_password", "reason": "too_short" | "too_long"}]`. Mesmo limite por IP
da rota de login.

### `GET /sessions`

```json
{ "items": [ { "id": "…uuid da família…", "created_at": "RFC3339", "last_used_at": "RFC3339",
               "user_agent": "…", "current": true } ] }
```

Só famílias vivas (não revogadas, não vencidas), a mais recente primeiro. Não expõe IP nem hash.

### `DELETE /sessions/{id}`

Revoga a família. `204`; família de outro usuário ou inexistente: `404`. Revogar a atual é
permitido (equivale a sair em todos os dispositivos daquela família).

### `GET /targets/summary`

O dashboard (spec §11.3) numa chamada só, em vez de uma chamada de `/checks` por alvo:

```json
{ "items": [ {
    "id": "…uuid do alvo…", "name": "API", "enabled": true,
    "state": "up" | "degraded" | "down" | "unknown",
    "last_checked_at": "RFC3339" | null,
    "last_latency_ms": 123 | null,
    "uptime_24h": 0.9993 | null,
    "open_incident_id": "…uuid…" | null,
    "spark": [ { "bucket": "RFC3339 (hora cheia UTC)", "total": 60, "ok": 60, "p50_ms": 110 | null } ]
} ] }
```

`state` segue a regra da página pública (Plano 4, D15): último check, `unknown` se mais velho que
3 intervalos, se o alvo está desativado ou se nunca houve check. `spark` tem as 24 horas cheias
anteriores (pode ter menos itens: hora sem check não aparece). Sem paginação: limite prático de
alvos por usuário; a ordem é a de `GET /targets`.

## O que cada agente entrega

Uma spec (design doc) no caminho da tabela acima, seguindo o formato das specs do projeto, com:
objetivo, escopo e fora de escopo, decisões com alternativa e custo se errada, riscos, estratégia
de testes (incluindo que asserções de ausência pedem prova por sabotagem), e uma seção
"Decisões para o usuário revisar" com o que o agente decidiu sozinho e que mereça um segundo olhar.
Nada de código de produto nem de plano de implementação ainda: os planos vêm depois de o usuário
aprovar as três specs.

## Reconciliação das três specs (2026-09-24)

Feita pelo coordenador depois de as três specs chegarem. Estas decisões valem para os três planos
e prevalecem sobre o texto das specs onde houver diferença.

1. **Filtro `GET /incidents?target_id=`** (pedido pelo Plano 6): **entra no Plano 5.** Aditivo, mesma
   paginação; `target_id` de outro usuário devolve lista vazia, não 404.
2. **`409 target_limit_reached` em `POST /targets`, teto de 100 alvos** (proposto pelo Plano 5):
   **entra.** O Plano 6 mapeia o código no formulário.
3. **`401 unauthorized`** em `/auth/password` e `/sessions` com a família do token morta: vale como
   escrito no Plano 5. O painel renova só em `401` com `code: unauthorized`, fora de `/auth/*`.
4. **E2E** (Plano 6 escreve, Plano 7 roda no CI): por padrão o Playwright sobe a própria pilha no
   `globalSetup` (Postgres em container, binário real, `vigilume user create`). Com `E2E_BASE_URL`,
   `E2E_EMAIL` e `E2E_PASSWORD`, roda contra uma pilha já de pé (o compose). O alvo cadastrado
   no teste é `https://vigilume-e2e.invalid/`: não é endereço privado (o `safedial` aceita o
   cadastro), não depende de rede, e o teste só confere que ele aparece, sem depender do estado.
5. **Seed do compose:** a senha de demonstração respeita a política do Plano 5 (15 a 128 code
   points). O código de saída **3** de `vigilume user create` quer dizer "já existe usuário, nada
   mudou", e não muda de sentido; o seed o trata como sucesso.
6. **Despacho de subcomandos** nasce no Plano 5 (`user create`, `user set-password`); o Plano 7
   acrescenta `healthcheck` e `version` no mesmo despacho.
7. **Postgres:** 18 em desenvolvimento e testes (já integrado, PR #6) e, com o Postgres sem gerência
   decidido pelo usuário, também em produção (Plano 7, D28–D29: imagem oficial num app próprio no
   Fly). A matriz 17/18 do CI é do Plano 7.
8. **`.gitignore`:** o Plano 6 troca `/web/dist/` por `internal/webui/dist/*` com exceção do
   `.gitkeep`; o Plano 7 espelha a regra no `.dockerignore`.
9. **Decididas pelo usuário (2026-09-24):**
   - **Sem blocklist de senhas comuns:** só o mínimo de 15 code points. O motivo `too_common` não
     existe (Plano 5, Div. 1, resolvida).
   - **Postgres de produção sem gerência no Fly** (não o Managed Postgres). A spec do Plano 7 é
     revisada para isso: topologia, backups, upgrades e runbook ficam com o operador.
   - **Repositório e imagem GHCR públicos.** Antes de tornar o repositório público, o histórico é
     varrido por segredos. A troca de visibilidade é ação manual do usuário.
   - **README em português.**
