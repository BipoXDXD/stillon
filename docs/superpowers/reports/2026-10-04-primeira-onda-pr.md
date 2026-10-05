# Rascunho local de PR — primeira onda do Plano 12

Não enviado ao GitHub. Branch: `melhoria/plano-12-seguranca-painel`; destino futuro:
`main`. Mudança contém código, portanto não usar `[skip ci]` ao abrir o PR completo.

## Título

Reforçar segurança das sondas, autenticação e operação; renovar o painel

## Descrição

Redirects podiam transportar User-Agent personalizado para outro destino, a saturação
do segundo hash podia consumir o fator de recuperação e esperas SQL podiam continuar
depois do timeout de escrita HTTP. Esta mudança remove headers privados após salto
inseguro, canonicaliza o host pelo mesmo IDNA do PATCH, calcula o hash novo antes de
consumir o fator e propaga prazo cooperativo de 15 segundos para API/verificação.

As migrations aditivas recusam checks infinitos/mais de um minuto no futuro e pares
TOTP pendentes incompletos. Inicialização e backup retiram senhas PostgreSQL de argv;
seed valida a URL inteira e instalações npm preservam opcionais com scripts desligados.
O ensaio de SSH usa OpenSSH/sudo reais e o cliente de produção, conferindo códigos,
bytes, host key, PTY e comando forçado em laboratório isolado.

O painel ganha navegação lateral no desktop, navegação visível no celular, temas com
contraste conferido, contagens de estado, latência/uptime e avisos de canais baseados
nos dados existentes. Corrige Pausar durante Retomar e a oclusão do aviso de HTTPS.
Mantém APIs, sessão, DTOs e dependências Go/npm; aceitação estética aguarda o dono.

## Validação local

- `go build ./... && go vet ./... && make test`: código 0, PostgreSQL 18 real e `-race`.
- Suíte Go completa também verde com PostgreSQL 17 real.
- Painel: typecheck, lint, 251 testes, build e npm audit verdes.
- Playwright: 14/14 em 4,2 min numa pilha API/PG real. Fluxo, duas abas e troca de
  autenticador exercitam o backend real; acessibilidade/viewport/HTTPS usam fixtures
  de API sob CSP real. Conferidos teclado, temas, 360 px e geometria do aviso HTTPS.
- `make lint sec`, ShellCheck, actionlint, workflows, teste de nome e whitespace verdes.
- Runtime/tools construídos; Trivy sem HIGH/CRITICAL corrigível. Runtime nonroot,
  sem shell, assets/versão conferidos e 7.679.693 bytes comprimidos, abaixo de 15 MB.
- Compose descartável e ensaio local completo de VPS: deploy/rollback, rede/Caddy,
  backup cifrado, restauro, códigos e limpeza de volumes próprios verdes.
- Testes novos observados RED, com sabotagens comportamentais compiláveis e restauração;
  revisão independente dos grupos e da correção adicional de deadline.

[Relatório completo, capturas e limites](2026-10-04-primeira-onda.md).

## Cuidados antes de integrar/deploy

Conferir pares TOTP pendentes inconsistentes no legado antes de aplicar 00011; se houver,
investigar sem limpeza automática de credenciais. A guarda de clock não repara checks
ou histerese antigos. O deadline é cooperativo: não interrompe Argon2 já em execução,
não desfaz COMMIT confirmado e mantém contratos de 429/502 e projeção pública de 5 s.

O backup ainda precisa de uma onda própria para tornar dump e contagens coerentes
sob escrita concorrente. Assinatura/proveniência de imagens, rotação de MASTER_KEY,
licença e continuidade na VPS real permanecem pendentes. Esta onda não acessou VPS,
DNS ou segredos nem executou push, merge, deploy ou Actions remoto.

Os PRs Dependabot continuam separados: #17 apto localmente; #18 tem ajuste de tipos
preparado, sem atualização de manifests nesta branch. [Parecer](2026-10-04-dependabot.md).
