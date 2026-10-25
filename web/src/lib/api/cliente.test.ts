import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import {
  EMAIL,
  LIMITE_DE_PROVAS,
  RECUPERACAO_OK,
  rotacao,
  SEGREDO_NOVO,
  SENHA,
  TOTP_NOVO,
  TOTP_OK,
} from '@/test/api-falsa'
import { canal } from '@/test/fixtures'
import { buscarDoTeste, novaSessao, usarApiFalsa } from '@/test/ambiente'
import { servidor } from '@/test/setup'
import { ApiError } from './http'
import { mensagemDe } from './mensagens'

async function logado() {
  const falsa = usarApiFalsa()
  const s = novaSessao()
  await s.sessao.entrar(EMAIL, SENHA)
  return { falsa, ...s }
}

describe('a regra do 401', () => {
  // A spec §11.2, item 3. Sabotagem prevista: tirar o "emVoo ??=" de
  // sessao.ts (renovar a cada chamada) → 5 refreshes e família revogada.
  it('cinco 401 simultâneos disparam um refresh só, e os cinco repetem com o token novo', async () => {
    const { falsa, api } = await logado()
    falsa.estado.alvos = [1, 2, 3, 4, 5].map((i) => ({
      id: `a${i}`,
      name: `Alvo ${i}`,
      url: 'https://exemplo.com',
      method: 'GET' as const,
      interval_sec: 60,
      timeout_ms: 10000,
      expected_status: 200,
      degraded_threshold_ms: 2000,
      enabled: true,
      header_names: [],
      created_at: '2026-09-24T12:00:00Z',
    }))
    falsa.estado.atrasoRefresh = 20
    falsa.expirarAcessos()
    const r = await Promise.all([1, 2, 3, 4, 5].map((i) => api.alvo(`a${i}`)))
    expect(r.map((a) => a.id)).toEqual(['a1', 'a2', 'a3', 'a4', 'a5'])
    expect(falsa.chamadas('POST /auth/refresh')).toBe(1)
    expect(falsa.estado.familiaRevogada).toBe(false)
  })

  // Nota do Plano 5 §12. Sabotagem prevista: podeRenovar() devolvendo true
  // para todo 401 (code !== '' && caminho !== '') → 1 refresh e 2 chamadas
  // de /auth/password.
  it('invalid_credentials na troca de senha não renova nem repete', async () => {
    const { falsa, api } = await logado()
    await expect(
      api.trocarSenha({ current_password: 'errada', new_password: 'x'.repeat(20) }),
    ).rejects.toMatchObject({ code: 'invalid_credentials' })
    expect(falsa.chamadas('POST /auth/refresh')).toBe(0)
    expect(falsa.chamadas('POST /auth/password')).toBe(1)
  })

  // Spec do Plano 9 §4.2: a troca de autenticador tem o tratamento do
  // totpDesativar (D24 do Plano 5): invalid_credentials no rotate e no
  // rotate/confirm não renova nem repete (repetir contaria outra falha no
  // bloqueio). Perna positiva: as provas certas passam, com o mesmo token.
  // Sabotagem: podeRenovar() devolvendo true para todo 401 → refresh e
  // chamada em dobro.
  it('invalid_credentials na troca de autenticador não renova nem repete', async () => {
    const { falsa, api } = await logado()
    falsa.estado.totpLigado = true
    await expect(api.totpTrocar(SENHA, '000000')).rejects.toMatchObject({ code: 'invalid_credentials' })
    expect(falsa.chamadas('POST /auth/totp/rotate')).toBe(1)
    const troca = await api.totpTrocar(SENHA, TOTP_OK)
    expect(troca.otpauth_uri).toContain(`secret=${SEGREDO_NOVO}`)
    expect(troca.rotation).toBe(rotacao(1))
    expect(falsa.corpos('POST /auth/totp/rotate')[1]).toEqual({ password: SENHA, code: TOTP_OK })
    await expect(api.totpConfirmarTroca('000000', troca.rotation)).rejects.toMatchObject({ code: 'invalid_credentials' })
    expect(falsa.chamadas('POST /auth/totp/rotate/confirm')).toBe(1)
    expect((await api.totpConfirmarTroca(TOTP_NOVO, troca.rotation)).recovery_codes).toHaveLength(10)
    expect(falsa.corpos('POST /auth/totp/rotate/confirm')[1]).toEqual({ code: TOTP_NOVO, rotation: rotacao(1) })
    expect(falsa.chamadas('POST /auth/refresh')).toBe(0)
  })

  // A7 do Plano 10: o confirm leva o rotation que o rotate devolveu, e um
  // rotate sem ele está fora do contrato (o painel não teria o que mandar).
  // O rotation de um pendente que outro rotate da mesma família substituiu
  // é 403, como "sem troca". Sabotagem: tirar o rotation do corpo do
  // totpConfirmarTroca → 403 no confirm certo.
  it('a troca de autenticador leva o rotation do rotate ao confirm', async () => {
    const { falsa, api } = await logado()
    falsa.estado.totpLigado = true
    const velha = await api.totpTrocar(SENHA, TOTP_OK)
    const nova = await api.totpTrocar(SENHA, TOTP_OK)
    expect(nova.rotation).not.toBe(velha.rotation)
    await expect(api.totpConfirmarTroca(TOTP_NOVO, velha.rotation)).rejects.toMatchObject({
      status: 403,
      code: 'forbidden',
    })
    expect((await api.totpConfirmarTroca(TOTP_NOVO, nova.rotation)).recovery_codes).toHaveLength(10)
    servidor.use(
      http.post('*/api/v1/auth/totp/rotate', () =>
        HttpResponse.json({ otpauth_uri: 'otpauth://totp/StillOn:x?secret=AAAA' }),
      ),
    )
    await expect(api.totpTrocar(SENHA, TOTP_OK)).rejects.toMatchObject({ code: 'unexpected' })
  })

  // Só "unauthorized" (o Authn recusando o token) renova. Sabotagem
  // prevista: trocar "code === 'unauthorized'" por "code !== ''" em
  // podeRenovar().
  it('outro 401 fora de /auth/* é erro, sem refresh', async () => {
    const { falsa, api } = await logado()
    falsa.estado.forcar.set('GET /me', { status: 401, code: 'invalid_session' })
    await expect(api.me()).rejects.toMatchObject({ code: 'invalid_session' })
    expect(falsa.chamadas('POST /auth/refresh')).toBe(0)
  })

  // Reconciliação, item 3: nada em /auth/* renova, nem com "unauthorized".
  // A sessão continua (o refresh nem foi tentado); a tela guarda o
  // formulário e oferece "Renovar acesso e tentar de novo" (D2, Task 10).
  // Sabotagem prevista: trocar o "!caminho.startsWith('/auth/')"
  // por "caminho !== ''".
  it('token vencido na troca de senha não renova: /auth/* nunca renova', async () => {
    const { falsa, sessao, api } = await logado()
    falsa.expirarAcessos()
    await expect(
      api.trocarSenha({ current_password: SENHA, new_password: 'y'.repeat(20) }),
    ).rejects.toMatchObject({ code: 'unauthorized' })
    expect(falsa.chamadas('POST /auth/refresh')).toBe(0)
    expect(falsa.chamadas('POST /auth/password')).toBe(1)
    expect(sessao.estado()).toBe('autenticado')
  })

  it('refresh recusado encerra a sessão com o motivo "expirou"', async () => {
    const { falsa, sessao, api } = await logado()
    falsa.expirarAcessos()
    falsa.estado.familiaRevogada = true
    await expect(api.me()).rejects.toMatchObject({ code: 'unauthorized' })
    expect(sessao.estado()).toBe('anonimo')
    expect(sessao.motivo()).toBe('expirou')
  })

  it('família revogada encerra o acesso sem esperar o token vencer', async () => {
    const { falsa, sessao, api } = await logado()
    await expect(api.me()).resolves.toMatchObject({ email: EMAIL })
    // Sem expirarAcessos: o mesmo access token ainda está presente e é a
    // revogação da família que faz o servidor recusar a próxima chamada.
    falsa.estado.familiaRevogada = true
    await expect(api.me()).rejects.toMatchObject({ code: 'unauthorized' })
    expect(sessao.estado()).toBe('anonimo')
    expect(sessao.motivo()).toBe('expirou')
  })

  it('401 de novo depois de renovar não entra em laço', async () => {
    const { falsa, sessao, api } = await logado()
    falsa.estado.forcar.set('GET /me', { status: 401, code: 'unauthorized' })
    await expect(api.me()).rejects.toMatchObject({ code: 'unauthorized' })
    expect(falsa.chamadas('POST /auth/refresh')).toBe(1)
    expect(falsa.chamadas('GET /me')).toBe(2)
    expect(sessao.estado()).toBe('anonimo')
  })

  // Uma consulta que ainda voava quando a sessão acabou volta 401 sem
  // token: não renova (o cookie já foi apagado) e não troca o motivo do fim.
  // Sabotagem prevista: tirar o "usado === null ||" do cliente.
  it('401 de requisição sem token não dispara refresh nem troca o motivo', async () => {
    const { falsa, sessao, api } = await logado()
    sessao.encerrar('encerrada-aqui')
    await expect(api.me()).rejects.toMatchObject({ code: 'unauthorized' })
    expect(falsa.chamadas('POST /auth/refresh')).toBe(0)
    expect(sessao.motivo()).toBe('encerrada-aqui')
  })

  // Um refresh em voo quando a sessão acaba aqui (encerrar a própria sessão,
  // sair) não ressuscita o token, e o motivo do fim continua o primeiro.
  // Duas guardas, duas sabotagens: em renovarAgora(), trocar
  // "if (minha !== epoca)" por "if (minha !== epoca && Math.random() > 2)"
  // (o token volta); em pedir(), trocar "sessao.estado() === 'autenticado'"
  // por "sessao.estado() !== 'verificando'" (o motivo vira "expirou").
  it('sessão encerrada com um refresh em voo: o token não volta e o motivo fica', async () => {
    const { falsa, sessao, api } = await logado()
    falsa.expirarAcessos()
    falsa.estado.atrasoRefresh = 300
    const pedido = api.me().catch((x: unknown) => x)
    await vi.waitFor(() => expect(falsa.chamadas('POST /auth/refresh')).toBe(1), { interval: 5 })
    sessao.encerrar('encerrada-aqui')
    expect(await pedido).toMatchObject({ code: 'unauthorized' })
    expect(sessao.token()).toBeNull()
    expect(sessao.estado()).toBe('anonimo')
    expect(sessao.motivo()).toBe('encerrada-aqui')
  })

  // O segundo 401 (depois de renovar) só encerra uma sessão que ainda está
  // aberta: se ela acabou enquanto a repetição voava, o motivo que vale é o
  // primeiro. Sabotagem prevista: tirar o "sessao.estado() === 'autenticado' &&"
  // do segundo 401 em pedir() → o motivo vira "expirou".
  it('401 de novo depois de renovar não troca o motivo de uma sessão que já acabou', async () => {
    const falsa = usarApiFalsa()
    let s: ReturnType<typeof novaSessao> | null = null
    let chamadasMe = 0
    const buscar = (entrada: string, init: RequestInit) => {
      // A repetição sai; a sessão acaba aqui antes de a resposta chegar.
      if (entrada.endsWith('/me') && ++chamadasMe === 2) s?.sessao.encerrar('encerrada-aqui')
      return buscarDoTeste(entrada, init)
    }
    s = novaSessao({ buscar })
    await s.sessao.entrar(EMAIL, SENHA)
    falsa.estado.forcar.set('GET /me', { status: 401, code: 'unauthorized' })
    await expect(s.api.me()).rejects.toMatchObject({ code: 'unauthorized' })
    expect(chamadasMe).toBe(2)
    expect(s.sessao.motivo()).toBe('encerrada-aqui')
  })

  // Sabotagem prevista: tirar o X-Requested-With de enviar() → 403 da API
  // falsa, como o RequireCustomHeader do servidor.
  it('mutações levam X-Requested-With', async () => {
    const { api } = await logado()
    // not_found, e não forbidden: a guarda do cabeçalho deixou passar.
    await expect(api.reconhecer('nao-existe')).rejects.toMatchObject({ code: 'not_found' })
  })
})

describe('o envelope de erro', () => {
  it('traz code, fields e trace_id', async () => {
    const { falsa, api } = await logado()
    falsa.estado.forcar.set('POST /targets', {
      status: 400,
      code: 'validation_failed',
      fields: [{ field: 'url', reason: 'forbidden' }],
    })
    const e = await api
      .criarAlvo({
        name: 'x',
        url: 'http://10.0.0.1',
        method: 'GET',
        interval_sec: 60,
        timeout_ms: 1000,
        expected_status: 200,
        enabled: true,
      })
      .catch((x: unknown) => x)
    expect(e).toBeInstanceOf(ApiError)
    expect(e).toMatchObject({ status: 400, code: 'validation_failed', traceId: 'trace-falso' })
    expect((e as ApiError).fields).toEqual([{ field: 'url', reason: 'forbidden' }])
  })

  // A1 do Plano 10: o 429 do teto de hashes manda Retry-After em segundos.
  // Ele vai para o erro e para o texto; a forma de data HTTP não é lida (o
  // texto de sempre). Sabotagem: em duracao(), trocar o Math.ceil por
  // Math.floor → "1 minuto" para 90 s.
  it('o Retry-After de um 429 vai para o erro e para o texto', async () => {
    const { api } = await logado()
    const esperas: [string, number | null, string][] = [
      ['1', 1, 'Muitas tentativas. Aguarde 1 segundo e tente de novo.'],
      ['2', 2, 'Muitas tentativas. Aguarde 2 segundos e tente de novo.'],
      ['90', 90, 'Muitas tentativas. Aguarde 2 minutos e tente de novo.'],
      ['Wed, 21 Oct 2026 07:28:00 GMT', null, 'Muitas requisições. Aguarde um minuto e tente de novo.'],
    ]
    for (const [cabecalho, segundos, texto] of esperas) {
      servidor.use(
        http.get(
          '*/api/v1/me',
          () =>
            HttpResponse.json(
              { error: { code: 'rate_limited', message: '' }, trace_id: 't' },
              { status: 429, headers: { 'Retry-After': cabecalho } },
            ),
          { once: true },
        ),
      )
      const e = await api.me().catch((x: unknown) => x)
      expect({ cabecalho, retryAfter: (e as ApiError).retryAfter, texto: mensagemDe(e) }).toEqual({
        cabecalho,
        retryAfter: segundos,
        texto,
      })
    }
  })

  // B5 do Plano 10: método inexistente sob /api/ responde 405 no envelope,
  // com o código novo method_not_allowed (o painel velho, depois de uma rota
  // mudar). Sabotagem: tirar method_not_allowed de porCodigo → "Não foi
  // possível concluir.".
  it('405 method_not_allowed diz que o painel pode estar desatualizado', async () => {
    const { api } = await logado()
    servidor.use(
      http.get(
        '*/api/v1/me',
        () => HttpResponse.json({ error: { code: 'method_not_allowed', message: '' }, trace_id: 't' }, { status: 405 }),
        { once: true },
      ),
    )
    const e = await api.me().catch((x: unknown) => x)
    expect(e).toMatchObject({ status: 405, code: 'method_not_allowed' })
    expect(mensagemDe(e)).toBe('O servidor não aceita esta operação. O painel pode estar desatualizado: recarregue a página.')
  })

  // Um 502 do proxy com HTML não é o envelope: vira 'unexpected', com o
  // status. Sabotagem prevista: em lerErro, trocar 'unexpected' por
  // 'internal_error'.
  it('resposta de erro que não é o envelope vira "unexpected"', async () => {
    const { api } = await logado()
    servidor.use(
      http.get('*/api/v1/me', () => new HttpResponse('<html>502 Bad Gateway</html>', { status: 502, headers: { 'Content-Type': 'text/html' } })),
    )
    const e = await api.me().catch((x: unknown) => x)
    expect(e).toBeInstanceOf(ApiError)
    expect(e).toMatchObject({ status: 502, code: 'unexpected' })
  })

  // Spec do Plano 6 §3.4: nunca uma exceção crua. Sucesso com JSON quebrado
  // ou fora do schema também é ApiError 'unexpected' (status 200: o
  // QueryClient não repete). Sabotagem prevista: em pedir(), trocar
  // "return lerCorpo(r, esquema)" por
  // "return esquema.parse(await r.json()) ?? lerCorpo(r, esquema)".
  it('sucesso sem JSON válido ou fora do schema vira ApiError "unexpected", não exceção crua', async () => {
    const { falsa, api } = await logado()
    falsa.estado.resumo = [{ bug: true } as never]
    await expect(api.resumo()).rejects.toMatchObject({ name: 'ApiError', status: 200, code: 'unexpected' })
    servidor.use(http.get('*/api/v1/me', () => new HttpResponse('{quebrado', { status: 200, headers: { 'Content-Type': 'application/json' } })))
    await expect(api.me()).rejects.toMatchObject({ name: 'ApiError', status: 200, code: 'unexpected' })
  })

  // D10: uma chave que some do DTO quebra alto, com o nome do campo.
  it('resposta fora do schema falha com o nome do campo', async () => {
    const { falsa, api } = await logado()
    falsa.estado.resumo = [{ id: 'x', name: 'API' } as never]
    await expect(api.resumo()).rejects.toThrow(/enabled|state/)
  })
})

describe('a API falsa imita o servidor', () => {
  // totphandlers.go (TOTPConfirm): com o 2FA já ligado, confirmar de novo é
  // 403 forbidden, como o enroll; reconfirmar rotacionaria os códigos de
  // recuperação.
  it('confirmar o 2FA já ligado é 403 forbidden', async () => {
    const { falsa, api } = await logado()
    falsa.estado.totpLigado = true
    await expect(api.totpConfirmar(TOTP_OK)).rejects.toMatchObject({ status: 403, code: 'forbidden' })
    expect(falsa.estado.totpLigado).toBe(true)
  })
})

// E8 do Plano 10: a API falsa reproduz o servidor nas rotas dos Planos 8 a
// 10. Cada teste é uma divergência que a falsa antiga tinha (o teste
// falharia contra ela), com o handler Go que ela imita. Sem isto, um teste
// de tela passava contra um servidor que não existe.
describe('a API falsa segue o servidor', () => {
  // A mutação crua, para os corpos que o cliente nunca manda.
  async function patchCru(token: string | null, caminho: string, corpo: string) {
    return fetch(new URL(`/api/v1${caminho}`, location.origin), {
      method: 'PATCH',
      headers: {
        Authorization: `Bearer ${token ?? ''}`,
        'X-Requested-With': 'stillon',
        'Content-Type': 'application/json',
      },
      body: corpo,
    })
  }

  // account.ChangePassword: SwapUserHash apaga o pendente (D6 do Plano 9).
  it('trocar a senha mata a troca de autenticador pendente', async () => {
    const { falsa, api } = await logado()
    falsa.estado.totpLigado = true
    const troca = await api.totpTrocar(SENHA, TOTP_OK)
    await api.trocarSenha({ current_password: SENHA, new_password: 'outra senha bem comprida', totp_code: TOTP_OK })
    await expect(api.totpConfirmarTroca(TOTP_NOVO, troca.rotation)).rejects.toMatchObject({ status: 403 })
  })

  // TOTP.Verify apaga o código de recuperação usado: ele não vale de novo
  // em rota nenhuma (disable, verify do login).
  it('código de recuperação gasto não vale de novo', async () => {
    const { falsa, api, sessao } = await logado()
    falsa.estado.totpLigado = true
    await api.totpTrocar(SENHA, RECUPERACAO_OK)
    await expect(api.totpDesativar(SENHA, RECUPERACAO_OK)).rejects.toMatchObject({ code: 'invalid_credentials' })
    expect(falsa.estado.totpLigado).toBe(true)
    await sessao.sair()
    const r = await sessao.entrar(EMAIL, SENHA)
    expect(r.tipo).toBe('totp')
    await expect(sessao.verificarTotp('desafio-1', RECUPERACAO_OK)).rejects.toMatchObject({ code: 'invalid_credentials' })
  })

  // TOTPRotate: senhaConfere ANTES do StartRotation. Com o 2FA desligado,
  // senha errada é 401, e só a senha certa chega ao 403.
  it('rotate confere a senha antes de olhar o 2FA', async () => {
    const { api } = await logado()
    await expect(api.totpTrocar('errada', TOTP_OK)).rejects.toMatchObject({ status: 401, code: 'invalid_credentials' })
    await expect(api.totpTrocar(SENHA, TOTP_OK)).rejects.toMatchObject({ status: 403, code: 'forbidden' })
  })

  // TOTPDisable com o 2FA desligado: o Disable devolve ErrTOTPNotEnrolled,
  // que é o mesmo 401 invalid_credentials da prova errada.
  it('disable com o 2FA desligado é 401 invalid_credentials', async () => {
    const { api } = await logado()
    await expect(api.totpDesativar(SENHA, TOTP_OK)).rejects.toMatchObject({ status: 401, code: 'invalid_credentials' })
  })

  // ConfirmRotation: o pendente é da família que o criou. Outra sessão (um
  // login novo) recebe 403, como "sem troca".
  it('o pendente é da sessão que o criou', async () => {
    const { falsa, api, sessao } = await logado()
    falsa.estado.totpLigado = true
    const troca = await api.totpTrocar(SENHA, TOTP_OK)
    await sessao.sair()
    await sessao.entrar(EMAIL, SENHA)
    await sessao.verificarTotp('desafio-1', TOTP_OK)
    await expect(api.totpConfirmarTroca(TOTP_NOVO, troca.rotation)).rejects.toMatchObject({ status: 403 })
  })

  // account.ChangePassword valida a senha nova ANTES da atual: senha nova
  // curta é 400 mesmo com a atual errada.
  it('troca de senha olha a política da senha nova antes da senha atual', async () => {
    const { api } = await logado()
    const e = await api.trocarSenha({ current_password: 'errada', new_password: 'curta' }).catch((x: unknown) => x)
    expect(e).toMatchObject({ status: 400, code: 'validation_failed' })
    expect((e as ApiError).fields).toEqual([{ field: 'new_password', reason: 'too_short' }])
  })

  // ChannelHandlers.SetEnabled: o corpo é lido (decodeJSONStrict) ANTES de
  // procurar o canal, e campo desconhecido é 400 SEM fields (o nome do campo
  // vem do cliente e não ecoa). Canal que não existe, com corpo bom, é 404.
  it('PATCH de canal: corpo antes do canal, e campo desconhecido sem fields', async () => {
    const { falsa, sessao } = await logado()
    falsa.estado.canais = [canal()]
    const desconhecido = await patchCru(sessao.token(), '/channels/canal-1', '{"enabled":true,"cor":"azul"}')
    expect(desconhecido.status).toBe(400)
    const envelope = (await desconhecido.json()) as { error: { code: string; fields?: unknown } }
    expect(envelope.error.code).toBe('validation_failed')
    expect(envelope.error.fields).toBeUndefined()
    const semCanal = await patchCru(sessao.token(), '/channels/nao-existe', '{"enabled":"sim"}')
    expect(semCanal.status).toBe(400)
    expect((await semCanal.json()).error.fields).toEqual([{ field: 'enabled', reason: 'invalid' }])
    expect((await patchCru(sessao.token(), '/channels/nao-existe', '{"enabled":true}')).status).toBe(404)
  })

  // ChannelHandlers.Delete e Resend: canal que não existe é 404.
  it('apagar ou reenviar a verificação de canal que não existe é 404', async () => {
    const { api } = await logado()
    await expect(api.apagarCanal('nao-existe')).rejects.toMatchObject({ status: 404, code: 'not_found' })
    await expect(api.reenviarVerificacao('nao-existe')).rejects.toMatchObject({ status: 404, code: 'not_found' })
  })
})

// A3 e A6 do Plano 10, depois da integração com o auth: a falsa da parte
// painel imitava o servidor de fab20c2 (sem contador por família, e com a
// conta bloqueada valendo nas rotas autenticadas). Os handlers que ela imita:
// provasDaFamilia (httpapi/provas.go), auth.FamilyProofs (auth/provas.go),
// TOTPConfirm/Disable/Rotate/RotateConfirm e AccountHandlers.ChangePassword.
describe('a API falsa conta as provas erradas por família, como o servidor', () => {
  it('a 5ª prova errada derruba a família, e a troca de senha e o 2FA dividem o contador', async () => {
    const { falsa, api } = await logado()
    falsa.estado.totpLigado = true
    const erradas = [
      () => api.trocarSenha({ current_password: 'errada', new_password: 'outra senha bem comprida', totp_code: TOTP_OK }),
      () => api.totpDesativar('errada', TOTP_OK),
      () => api.totpTrocar(SENHA, '000000'),
      () => api.totpCadastrar('errada'),
    ]
    for (const errar of erradas) {
      await expect(errar()).rejects.toMatchObject({ status: 401, code: 'invalid_credentials' })
    }
    expect(falsa.estado.familiaRevogada).toBe(false)
    await expect(api.totpDesativar(SENHA, '000000')).rejects.toMatchObject({ status: 401, code: 'unauthorized' })
    expect(falsa.estado.familiaRevogada).toBe(true)
    // A família caiu: o 2FA continua ligado, e nem as provas certas passam
    // mais pela guarda da família (o painel renova, e a renovação falha).
    expect(falsa.estado.totpLigado).toBe(true)
    await expect(api.totpDesativar(SENHA, TOTP_OK)).rejects.toMatchObject({ status: 401, code: 'unauthorized' })
    expect(falsa.estado.totpLigado).toBe(true)
  })

  it('a prova certa zera o contador da família', async () => {
    const { falsa, api } = await logado()
    falsa.estado.totpLigado = true
    for (let i = 1; i < LIMITE_DE_PROVAS; i++) {
      await expect(api.totpDesativar('errada', TOTP_OK)).rejects.toMatchObject({ code: 'invalid_credentials' })
    }
    await api.totpTrocar(SENHA, TOTP_OK)
    for (let i = 1; i < LIMITE_DE_PROVAS; i++) {
      await expect(api.totpDesativar('errada', TOTP_OK)).rejects.toMatchObject({ code: 'invalid_credentials' })
    }
    expect(falsa.estado.familiaRevogada).toBe(false)
  })

  it('o /totp/confirm conta o código errado: 400 até a 4ª, e a 5ª é 401 unauthorized', async () => {
    const { falsa, api } = await logado()
    await api.totpCadastrar(SENHA)
    for (let i = 1; i < LIMITE_DE_PROVAS; i++) {
      await expect(api.totpConfirmar('000000')).rejects.toMatchObject({ status: 400, code: 'validation_failed' })
    }
    await expect(api.totpConfirmar('000000')).rejects.toMatchObject({ status: 401, code: 'unauthorized' })
    expect(falsa.estado.familiaRevogada).toBe(true)
    expect(falsa.estado.totpLigado).toBe(false)
  })

  it('403 e política da senha não contam como prova errada', async () => {
    const { falsa, api } = await logado()
    for (let i = 0; i < LIMITE_DE_PROVAS + 1; i++) {
      await expect(api.totpTrocar(SENHA, TOTP_OK)).rejects.toMatchObject({ status: 403 })
      await expect(api.totpConfirmarTroca(TOTP_NOVO, rotacao(1))).rejects.toMatchObject({ status: 403 })
      await expect(api.trocarSenha({ current_password: 'errada', new_password: 'curta' })).rejects.toMatchObject({
        status: 400,
      })
    }
    expect(falsa.estado.familiaRevogada).toBe(false)
  })

  // C1 da revisão do Plano 10: TOTP.Confirm com o 2FA já ligado é 403 ANTES
  // de olhar o código (o certo e o errado recebem o mesmo 403, que não conta),
  // e nem ele nem o enroll com a senha certa zeram a família: com o 2FA
  // ligado, só a prova do fator zera.
  it('com o 2FA ligado, confirm e enroll são 403 sem contar nem zerar a família', async () => {
    const { falsa, api } = await logado()
    falsa.estado.totpLigado = true
    for (let i = 1; i < LIMITE_DE_PROVAS; i++) {
      await expect(api.totpDesativar(SENHA, '000000')).rejects.toMatchObject({ code: 'invalid_credentials' })
    }
    for (let i = 0; i < LIMITE_DE_PROVAS + 1; i++) {
      await expect(api.totpConfirmar('000000')).rejects.toMatchObject({ status: 403, code: 'forbidden' })
      await expect(api.totpConfirmar(TOTP_OK)).rejects.toMatchObject({ status: 403, code: 'forbidden' })
      await expect(api.totpCadastrar(SENHA)).rejects.toMatchObject({ status: 403, code: 'forbidden' })
    }
    expect(falsa.estado.familiaRevogada).toBe(false)
    await expect(api.totpDesativar(SENHA, '000000')).rejects.toMatchObject({ status: 401, code: 'unauthorized' })
    expect(falsa.estado.familiaRevogada).toBe(true)
  })

  it('a conta bloqueada barra o login, não as rotas autenticadas; a troca de autenticador a destrava', async () => {
    const { falsa, api, sessao } = await logado()
    falsa.estado.totpLigado = true
    falsa.estado.contaBloqueada = true
    const troca = await api.totpTrocar(SENHA, TOTP_OK)
    expect(falsa.estado.contaBloqueada).toBe(false)
    falsa.estado.contaBloqueada = true
    await api.totpConfirmarTroca(TOTP_NOVO, troca.rotation)
    expect(falsa.estado.contaBloqueada).toBe(false)
    falsa.estado.contaBloqueada = true
    await sessao.sair()
    await expect(sessao.entrar(EMAIL, SENHA)).rejects.toMatchObject({ code: 'invalid_credentials' })
  })
})
