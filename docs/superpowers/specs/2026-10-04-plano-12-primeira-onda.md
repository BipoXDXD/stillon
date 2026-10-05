# Plano 12: primeira onda de segurança, operação e painel

Data: 2026-10-04. Execução local autorizada pelo dono, que delegou decisões durante sua ausência.
Esta onda fecha um subconjunto verificável das pendências; não declara o Plano 12 inteiro concluído.

## Objetivo e contexto

Preservar o StillOn como um binário Go com painel React embutido e Postgres, corrigir falhas
observáveis e melhorar a leitura operacional do painel. O README continua sendo a fonte de verdade.
Os nove Markdown vivos iniciais foram distribuídos entre os agentes para leitura integral, incluindo o
runbook de 2.291 linhas. Um inventário sem respeitar `.gitignore` também encontrou os históricos
em `.superpowers/arquivo/` e `.superpowers/sdd/`, para leitura sem reexecutar suas instruções.
As specs antigas descrevem decisões históricas de hospedagem; o runbook
atual e o código de `deploy/vps/` prevalecem.

A base é o commit `cd81622`, em uma branch local própria. O `AGENTS.md` encontrado não estava
versionado e será preservado. O checkout registra o bloco 2 como próximo, mas o PR aberto #26
registra os blocos 0 a 2 concluídos e o bloco 3 como próximo. Essa evidência documental não
substitui a conferência guiada na VPS; não repetir o bloco 2 com base no registro antigo.

## Segurança e correção

1. Headers de alvos são segredos, inclusive um `User-Agent` fornecido pelo dono. Um redirect
   que muda o destino ou rebaixa HTTPS para HTTP deve remover todos os valores personalizados.
   A identificação pública do monitor pode ser recolocada, sem copiar o valor secreto.
2. A comparação de hosts deve usar IDNA como o transporte, em vez de `EqualFold` do Unicode
   bruto. O par `straße.de`/`STRAẞE.de` precisa ser tratado como destinos distintos. A regra
   de origem usada no PATCH e a usada no redirect devem compartilhar a canonicalização,
   preservando a política atual de portas e upgrade HTTP para HTTPS.
3. Troca de senha: concluir o hash novo antes de consumir TOTP/código de recuperação. Uma
   saturação do Argon2id deve responder `429` sem gastar o segundo fator. Preservar a prova da
   senha atual, a conferência da família viva, o CAS e a revogação das outras sessões.
4. Inicialização do banco: senhas não podem entrar no argv do `psql`. Usar `\getenv`, quoting
   SQL do próprio psql e testes com valores públicos contendo aspas e caracteres especiais.
   O backup também deve tirar a senha dos argumentos de psql/pg_dump, preservando a conexão
   e a senha independente do banco de restauração.
5. Corrigir a validação do seed local para recusar whitespace e sufixos de porta inválidos.
6. Avaliar `npm ci --ignore-scripts` no Dockerfile/CI com build real antes de adotar.
7. Recusar checks mais de um minuto à frente de `statement_timestamp()` do banco, por trigger
   antes de INSERT/UPDATE. A recusa deve reverter também a gravação da histerese, nas partições
   existentes e futuras no PostgreSQL 17/18. Não reparar registros antigos nesta onda.
8. Provas de família: contar erros reais no Fail e impedir que reservas anteriores a um sucesso
   devolvam vagas da nova geração. A política HTTP de excesso de reservas em voo fica como está.
9. Exercitar `vps.sh` com OpenSSH e sudo reais num ambiente descartável: comando forçado,
   preservação exclusiva de `SSH_ORIGINAL_COMMAND`, recusa de host key errada, bloqueio de
   comandos arbitrários/PTY e fidelidade de bytes do backup. Usar chaves efêmeras, sem portas
   publicadas nem acesso à VPS. Acrescentar o ensaio ao job VPS existente, com prazo limitado,
   e acompanhar a base fixada pelo Dependabot.
10. Exigir no banco que `totp_pending_secret_enc` e `totp_pending_family_id` sejam ambos
    nulos ou ambos presentes. Migration aditiva com CHECK validado, sem alterar cifra,
    formato AAD, validade do segundo fator nem recuperação de resposta perdida. Dados
    antigos inconsistentes devem recusar a migration, com investigação própria antes do
    deploy, sem limpeza automática.
11. Limitar o contexto de trabalho das requisições de API, verificação e página pública a
    15 segundos, respeitando prazo anterior do chamador, abaixo do WriteTimeout de 30 s.
    Cancelar SQL e espera no pool, sem goroutine ou buffer de TimeoutHandler. Erro interno
    sob DeadlineExceeded usa o envelope já existente `503 unavailable`; páginas HTML mantêm
    seu formato e erros de domínio. Provar com trava PostgreSQL real que requisições vencidas
    não criam alvos depois da liberação da trava e que pool/SQL ficam livres. Não prometer
    interromper Argon2 em execução ou desfazer commits já confirmados.
    A projeção pública compartilhada preserva seu worker independente de até 5 s com
    `WithoutCancel`/singleflight; o prazo menor de um cliente não interrompe esse worker.

## Design e UX do painel

Manter React, Radix, Tailwind, Recharts, rotas e DTOs atuais. Usar shell com lateral em desktop,
navegação visível que se adapta no celular, superfícies discretas e acento azul. Preservar a
classe `.dark`, tokens de tema, nonce CSP, foco e estrutura dos componentes com estado.

O dashboard exibirá contagens derivadas dos estados reais, atualização dos dados e cartões
com hierarquia clara de estado, latência e uptime. Avisos de canais pausados/ausentes serão
informativos, baseados na consulta existente de canais e sem impedir a visualização dos alvos.
Campos e ações devem caber em 360 px de largura sem rolagem horizontal da página.
O aviso de contexto inseguro deve permanecer visível em HTTP fora de localhost, inclusive
ao lado da navegação fixa de desktop; o aviso da tela de login mantém sua largura original.

O clique de Pausar durante Retomar não deve abrir confirmação. Não mudar regras de envio,
confirmações destrutivas, coordenação entre abas ou política de senha para facilitar testes.

## Arquitetura e alternativas

Escolha: correções locais e um helper de canonicalização com uma responsabilidade, mais
primitivas visuais compartilhadas. Reescrever autenticação ou introduzir nova infraestrutura
aumentaria o risco e não é necessário para os achados desta onda. A migração ampla do auth
para sqlc continua no Plano 13.

## Verificação

Cada teste comportamental novo deve ser observado vermelho antes da correção e novamente
sob sabotagem isolada da propriedade. Registrar comandos, resultado e restauração. Integração
usa Postgres descartável real; não substituir SQL/RLS por mocks. A prova final exige:

```sh
go build ./... && go vet ./... && make test
(cd web && npm run typecheck && npm run lint && npm test && npm run build)
sh deploy/ci/checar-nome.sh
```

E2E, acessibilidade, CSP, temas e largura pequena serão conferidos numa pilha de teste isolada,
sem apagar volumes existentes do dono. Revisão independente depois de cada grupo de mudanças.

## Decisões e pendências críticas

- Não acessar VPS, criar segredos, alterar DNS, publicar imagem, fazer deploy ou merge nesta onda.
  O pedido de autonomia cobre trabalho local; o runbook exige a participação do dono nesses passos.
- Manter refresh com detecção de reúso até um desenho próprio de recuperação de resposta perdida;
  enfraquecer a revogação sem avaliar roubo de sessão seria uma decisão crítica.
- Assinatura de imagens, rotação de MASTER_KEY, licença, publicação do repositório e mudança de
  topologia continuam pendentes. Não executar atualizações amplas de dependências sem evidência.
- Redesign é uma proposta implementada e testada localmente; aceitação estética continua com o dono.
- Usar até três agentes GPT-6.1 High simultâneos com propriedade de arquivos explícita. As frentes
  independentes podem implementar em paralelo conforme pedido do dono; arquivos comuns e commits
  ficam sob coordenação para evitar conflitos.
