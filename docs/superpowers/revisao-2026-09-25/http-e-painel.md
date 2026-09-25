# Auditoria: camada HTTP e painel (Vigilume @ 640cd9e)

Escopo: `internal/httpapi/middleware/`, `internal/httpapi/routes.go`, os handlers de alvos,
canais, incidentes, status page e summary, `internal/statuspage/`, o template público,
`internal/webui/` e `web/src`. Só leitura. `npm audit --omit=dev`: **0 vulnerabilidades**.

Resumo: **Segurança**: 0 Crítico, 0 Alto, 3 Médio, 2 Baixo. **Correção**: 0 Crítico, 0 Alto,
0 Médio, 2 Baixo.

---

## Segurança

### S1 (Médio): cinco senhas erradas de UM IP trancam a conta do dono, e dá para repetir para sempre

- **Onde:** `internal/httpapi/routes.go:98` (`RateLimit(ctx, 10, 5, …)`, burst **5**) com
  `internal/auth/user.go:20` (`LockThreshold = 5`) e `user.go:131-151` (`RecordFailure`: um
  bloqueio vencido zera o contador para 1).
- **Ataque:** quem sabe o e-mail do dono manda 5 `POST /api/v1/auth/login` com senha errada, de
  um IP só e de uma vez. O burst deixa os 5 passarem, a 5ª falha grava
  `locked_until = agora + 15 min`, e a partir daí até a senha certa é recusada (`CheckPassword`
  nem olha a senha). Aos 15 min o balde já está cheio de novo (enche em 30 s): mais 5 tentativas,
  mais 15 min. São 5 requisições a cada 15 min (480 por dia) para deixar o dono fora do painel
  indefinidamente, sem precisar de mais de um IP nem de spoof. Quem tem o painel aberto continua
  entrando pelo refresh; o que fica bloqueado é qualquer login novo, como outro aparelho, um
  cookie vencido ou o logout.
- **Por que as defesas não bastam:** o comentário em `routes.go:87-92` diz que o limitador
  barra o atacante "antes de acumular falhas suficientes para trancar a conta de uma vítima".
  Com burst igual ao limiar, isso é falso. O conserto do `RecordFailure` (recontar depois do
  bloqueio vencido) só fez a armadilha precisar de 5 requisições em vez de 1. O bloqueio não
  se anuncia (R34), então o dono vê só "e-mail ou senha incorretos".
- **Correção:** o bloqueio duro por conta tem de deixar de valer contra origens que nunca
  acertaram. Uma saída é contar as falhas por (conta, prefixo de IP) e deixar só o bloqueio
  dessa chave recusar a senha certa. A outra é trocar o bloqueio duro por um atraso progressivo
  que não recusa a senha certa, com aviso ao dono (e-mail ou canal) quando o limiar é
  atingido. No mínimo, o burst do login tem de ficar abaixo de `LockThreshold` e o comentário
  tem de ser corrigido. Isso sozinho não resolve, porque 2 IPs já bastam.

### S2 (Médio): o `PATCH` que troca a URL de um alvo mantém os headers secretos e os manda para o host novo

- **Onde:** `internal/targets/service.go:146` (abre os headers gravados), `:157`
  (`p.apply`: sem `SetHeaders`, `f.Headers` fica como está) e `:162` (sela os mesmos headers
  para a URL nova). `mudouASonda` põe `next_run_at = agora` (`:167`), então a sonda sai em
  segundos. O handler está em `internal/httpapi/targethandlers.go:198-234`.
- **Ataque:** o alvo `https://api.minha.com/health` tem `Authorization: Bearer <chave>`. Quem
  tem um access token do dono manda
  `PATCH /api/v1/targets/{id} {"url":"https://atacante.example/"}` sem `headers`. Pode ser o
  token de uma sessão roubada ou o de uma sessão já encerrada, que vale 15 min nas rotas de
  recurso (limite conhecido, PENDENCIAS-6). O sondador manda a chave ao atacante no próximo
  check. O atacante devolve o PATCH para a URL antiga e não sobra rastro visível.
- **Por que as defesas não bastam:** o projeto trata os valores de header como "só escrita".
  O DTO não tem campo para eles (D5, `targethandlers.go:52-54`), e o sondador derruba
  Authorization e Cookie num redirect para outro host:porta ou num rebaixamento https→http
  (`internal/prober/safedial/transport.go:149`, I3 do Plano 3). O PATCH de URL é esse mesmo
  redirecionamento feito pela API, e não passa por proteção nenhuma. Com isso, a janela de 15
  min, que o documento apresenta como "só nas rotas de recurso", vira roubo de credencial de
  terceiros.
- **Correção:** em `Update`, se o destino muda (esquema, host ou porta) e o PATCH não traz
  `headers`, recusar com `400 headers required` ou apagar os headers. O painel já tem os modos
  "substituir" e "remover" para isso. Se der, exigir também a sessão viva (`viva`) no PATCH de
  alvo.

### S3 (Médio, depende da configuração): atrás do Caddy, qualquer `CLIENT_IP_HEADER` é forjável pelo cliente

- **Onde:** `internal/httpapi/middleware/clientip.go:434-459` (`peloCabecalho`) e
  `internal/config/config.go:213-233` (aceita qualquer nome fora da lista de proibidos).
- **Ataque:** numa VPS com Caddy, o dono copia o ambiente do `fly.toml`
  (`CLIENT_IP_HEADER = "Fly-Client-IP"`) ou, por hábito do nginx, põe `X-Real-IP`, e preenche
  `TRUSTED_PROXIES` com o endereço do Caddy. Por padrão, o Caddy repassa ao backend todo
  cabeçalho que o cliente mandou e só reescreve os `X-Forwarded-*` (documentação do
  `reverse_proxy`, "Defaults"). O atacante manda `Fly-Client-IP: 203.0.113.<n>` com um valor
  novo a cada requisição. A conexão vem do Caddy (confiável), então o valor é aceito. Cada
  requisição cai num balde novo em todos os limitadores por IP: login, `/totp/verify`, o
  limite global da API, `/p/` e `/verify/`. O `ip_hash` da auditoria também passa a registrar
  a origem que o atacante quiser. Combinado com S1, o atacante nem precisa de IPs.
- **Por que as defesas não bastam:** a checagem de origem confiável só prova que o salto veio
  do proxy. Não prova que o proxy escreveu o cabeçalho. No Fly isso vale porque o proxy
  sobrescreve o cabeçalho (o runbook manda testar). O único runbook é o do Fly, e o repositório
  não tem nada para Caddy, então nada avisa quem vai para a VPS.
- **Refutado para o caso padrão:** com `CLIENT_IP_HEADER` vazio (modo `X-Forwarded-For`) e o
  Caddy sem `trusted_proxies`, o Caddy **substitui** o `X-Forwarded-For` pelo IP real, e o
  último salto é o cliente. Esse caso **não** é forjável.
- **Correção:** documentar o deploy com Caddy: `CLIENT_IP_HEADER` vazio e `TRUSTED_PROXIES` com
  o endereço que o app realmente vê (atenção ao item S5). Se quiser `X-Real-IP`, pôr
  `header_up X-Real-IP {remote_host}` no Caddyfile. No boot, avisar quando `CLIENT_IP_HEADER`
  estiver preenchido e o `PUBLIC_BASE_URL` não for `*.fly.dev`.

### S4 (Baixo): no modo `X-Forwarded-For`, só a primeira linha do cabeçalho é lida

- **Onde:** `internal/httpapi/middleware/clientip.go:419` (`r.Header.Get("X-Forwarded-For")`).
- **Ataque:** um proxy que acrescenta uma **linha nova** de `X-Forwarded-For` em vez de
  concatenar, como o HAProxy com `option forwardfor`, deixa a linha do cliente em primeiro
  lugar. O `Get` devolve essa linha, e o "último salto" dela é escolhido pelo atacante. O
  resultado é spoof completo, como em S3.
- **Por que é Baixo:** o Caddy (`Set` numa linha só) e o nginx ≥ 1.23 (junta as linhas) não
  fazem isso. O código diz que usa "o último elemento", mas não usa, se houver mais de uma
  linha.
- **Correção:** `v := r.Header.Values("X-Forwarded-For")` e pegar o último item da última
  linha.

### S5 (Baixo): um `TRUSTED_PROXIES` errado no modo `X-Forwarded-For` passa em silêncio

- **Onde:** `internal/httpapi/middleware/clientip.go:406-413`. O aviso limitado só existe no
  modo com cabeçalho (`peloCabecalho`). O de `config.go:270` só dispara com a lista vazia.
- **Ataque:** na VPS, com o app num container e a porta publicada em `127.0.0.1:8080`, o
  Caddy do host conecta pelo docker-proxy, e o app vê o gateway da bridge (`172.x.0.1`), não
  `127.0.0.1`. O dono põe `TRUSTED_PROXIES=127.0.0.1/32`. Nenhum aviso aparece, e todo
  visitante cai no mesmo balde. Qualquer pessoa na internet esgota o login (10/min) ou a API
  inteira (120/min, 2 req/s) de todos, e o dono leva 429 no painel.
- **Por que as defesas não bastam:** a "falha fechada" é deliberada e já está documentada
  para a lista **vazia**. Com a lista preenchida e errada, nada avisa.
- **Correção:** no modo `X-Forwarded-For`, se a requisição traz o cabeçalho e o
  `remote_addr` não é confiável, e `TRUSTED_PROXIES` não está vazio, emitir o mesmo
  `avisoDeProxy` limitado a um por minuto.

### Verificado e descartado (sem achado)

- **IDOR:** alvos, checks, incidentes (ack e lista com `target_id`), canais (delete, PATCH,
  resend), status page (`Update` confere `atual.ID == id`) e sessões filtram por `user_id` sob
  `InUserTx`. Os itens da página conferem o dono (`gravarItens` e o WITH CHECK da RLS). Id
  malformado responde 404.
- **Wiring:** toda rota de recurso passa por `authn`; as de 2FA e sessão, por `viva`; toda
  mutação da API, por `RequireCustomHeader`. `/verify/` e `/p/` ficam fora de propósito:
  `/verify/` não tem cookie e o token está no caminho; `/p/` é só GET.
- **CORS:** uma origem exata, `Vary: Origin` incondicional, sem curinga.
- **JWT:** HS256 fixo, e o token com audience (o desafio de TOTP) é recusado.
- **CSP:** a da API e a de `/p/` são `default-src 'none'`. O painel tem `script-src 'self'` e
  nonce só em estilo, sem `unsafe-*`. `frame-ancestors 'none'` e `X-Frame-Options: DENY` em
  tudo.
- **Cache:** `no-store` em toda a API. O index do painel é `no-store` por causa do nonce. `/p/`
  sai com `public, max-age=30` e só dados públicos.
- **XSS:** `/p/` usa `html/template`, e os campos são texto sem controle. O painel não tem
  `dangerouslySetInnerHTML`. A URL do alvo aparece como texto, não como link. O único `href`
  dinâmico é `public_url`, montado pelo servidor.
- **Open redirect:** `destinoSeguro` do `?next=` resiste a `//host`, `/\host` e caracteres de
  controle.
- **Tokens:** o access token fica em variável de módulo. O refresh é HttpOnly, SameSite=Strict,
  com `Path=/api/v1/auth` e Secure com https. O `localStorage` guarda só o tema.
- **Vazamento de erro:** as mensagens vêm de uma tabela fechada. `Recover` e os logs usam
  `r.Pattern`, nunca o caminho com token.
- **Enumeração em `/p/`:** 404 idêntico para página inexistente e rascunho. O slug de rascunho
  aparece via `409 slug_taken` só para usuário autenticado, o que é irrelevante.
- **Janela de 15 min do access token revogado** nas rotas de recurso: limite conhecido
  (PENDENCIAS-6). Só entra aqui porque agrava S2.

---

## Correção

### C1 (Baixo): a série do detalhe depende do relógio do navegador, ao contrário do que o comentário diz

- **Onde:** `web/src/lib/api/cliente.ts:115-117` ("Só from: o 'agora' é do servidor, sem
  depender do relógio do navegador"), `web/src/features/alvos/serie.ts` (`desde(j, Date.now())`)
  e `internal/targets/service.go:245-251`.
- **Cenário:** o `from` é calculado com o relógio do navegador, e o `to` é o `now` do servidor.
  Com o navegador adiantado 1 h ou mais, a janela "1h" manda `from >= to`, o servidor responde
  `400 from invalid` e o detalhe mostra erro. Adiantado 40 min, a janela "1h" mostra só 20 min
  sem aviso. Atrasado, a janela fica maior que o rótulo.
- **Correção:** mandar só a duração (por exemplo `?window=1h`) e deixar o servidor calcular
  `from = now - window`, ou mandar também `to`, e corrigir o comentário.

### C2 (Baixo): a API responde 404 e 405 fora do envelope

- **Onde:** `internal/httpapi/routes.go:76` (o `ServeMux` da API). Uma rota inexistente sob
  `/api/` ou um método não registrado (por exemplo `PUT /api/v1/targets/{id}`) cai no
  `404 page not found` ou `405` em texto puro do `net/http`, sem `error.code` e sem
  `trace_id` no corpo.
- **Cenário:** um cliente da API, ou o painel depois de uma rota ser renomeada, recebe
  `unexpected` ("Resposta inesperada do servidor (404)") em vez de `not_found`, e o 404 de
  recurso apagado (`naoEncontrado`) continua funcionando só porque olha o status. Isso diverge
  do contrato da spec §9.1: todo erro com envelope.
- **Correção:** registrar `mux.Handle("/api/", …)` como fallback que responde
  `apierr.WriteError(w, r, apierr.CodeNotFound)`, e tratar o 405 com um wrapper que confira
  `r.Pattern == ""`.
