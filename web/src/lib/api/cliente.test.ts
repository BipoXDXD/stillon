import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import {
  EMAIL,
  LIMITE_DE_PROVAS,
  PROJETO_PADRAO,
  RECUPERACAO_OK,
  rotacao,
  SEGREDO_NOVO,
  SENHA,
  SITE_KEY_DO_TURNSTILE,
  TOKEN_DO_TURNSTILE,
  TOTP_NOVO,
  TOTP_OK,
} from '@/test/api-falsa'
import versaoDosTermos from '@/features/legal/versao.json'
import { alvo, canal } from '@/test/fixtures'
import { buscarDoTeste, novaSessao, usarApiFalsa } from '@/test/ambiente'
import { servidor } from '@/test/setup'
import { ApiError, type Buscar } from './http'
import { mensagemDe } from './mensagens'

type ApiFalsa = ReturnType<typeof usarApiFalsa>

const ROTA_DO_REFRESH = '*/api/v1/auth/refresh'

async function logado() {
  const falsa = usarApiFalsa()
  const s = novaSessao()
  await s.sessao.entrar(EMAIL, SENHA)
  return { falsa, ...s }
}

describe('prazo das consultas', () => {
  it('adiantar os timers do polling não vence o prazo de um GET em voo', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    const prazoNativo = new AbortController()
    const criarPrazo = vi.spyOn(AbortSignal, 'timeout').mockReturnValue(prazoNativo.signal)
    let responder!: (resposta: Response) => void
    const resposta = new Promise<Response>((resolve) => { responder = resolve })
    const buscar = vi.fn<Buscar>(() => resposta)
    const { api } = novaSessao({ buscar })
    const terminou = vi.fn<(resultado: unknown) => void>()
    const consulta = api.doProjeto(PROJETO_PADRAO).alvos()
    const observar = consulta.then(terminou, terminou)
    try {
      expect(buscar).toHaveBeenCalledTimes(1)
      await vi.advanceTimersByTimeAsync(10_000)
      expect(terminou).not.toHaveBeenCalled()
      expect(buscar.mock.calls[0]?.[1].signal?.aborted).toBe(false)
      expect(criarPrazo).toHaveBeenCalledExactlyOnceWith(10_000)
    } finally {
      responder(Response.json({ items: [] }))
      await observar
      criarPrazo.mockRestore()
      vi.useRealTimers()
    }
    await expect(consulta).resolves.toEqual({ items: [] })
  })

  it.each(['antes dos cabeçalhos', 'durante o corpo'])('termina em erro de rede %s', async (fase) => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    const criarPrazo = vi.spyOn(AbortSignal, 'timeout').mockImplementation((ms) => {
      const controlador = new AbortController()
      setTimeout(() => controlador.abort(new DOMException('Prazo vencido.', 'TimeoutError')), ms)
      return controlador.signal
    })
    try {
      const corpoAbortado = vi.fn<() => void>()
      const buscar = vi.fn<Buscar>(async (_entrada, { signal }) => {
        if (fase === 'antes dos cabeçalhos') {
          return new Promise<Response>((_, rejeitar) => {
            signal?.addEventListener('abort', () => rejeitar(signal.reason), { once: true })
          })
        }
        return new Response(new ReadableStream<Uint8Array>({
          start(corpo) {
            signal?.addEventListener('abort', () => {
              corpoAbortado()
              corpo.error(signal.reason)
            }, { once: true })
          },
        }), { headers: { 'Content-Type': 'application/json' } })
      })
      const { api } = novaSessao({ buscar })
      const terminou = vi.fn<(resultado: unknown) => void>()
      void api.me().then(terminou, terminou)

      await vi.advanceTimersByTimeAsync(9_999)
      expect(terminou).not.toHaveBeenCalled()
      await vi.advanceTimersByTimeAsync(1)

      expect(terminou).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ code: 'network', status: 0 }))
      expect(buscar).toHaveBeenCalledTimes(1)
      expect(buscar.mock.calls[0]?.[1].signal?.aborted).toBe(true)
      const abortosEsperados = fase === 'durante o corpo' ? 1 : 0
      expect(corpoAbortado).toHaveBeenCalledTimes(abortosEsperados)
    } finally {
      criarPrazo.mockRestore()
      vi.useRealTimers()
    }
  })

  it('uma consulta já cancelada propaga a causa sem enviar o GET', async () => {
    const buscar = vi.fn<Buscar>(async () => Response.json({ items: [] }))
    const { api } = novaSessao({ buscar })
    const motivo = new DOMException('Consulta cancelada.', 'AbortError')

    await expect(api.doProjeto(PROJETO_PADRAO).alvos(AbortSignal.abort(motivo))).rejects.toBe(motivo)
    expect(buscar).not.toHaveBeenCalled()
  })

  it('cancela e libera os recursos do GET em navegador sem AbortSignal.any', async () => {
    const metodo = Object.getOwnPropertyDescriptor(AbortSignal, 'any')
    Object.defineProperty(AbortSignal, 'any', { configurable: true, value: undefined })
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    const prazo = new AbortController()
    const criarPrazo = vi.spyOn(AbortSignal, 'timeout').mockReturnValue(prazo.signal)
    const ouvirPrazo = vi.spyOn(prazo.signal, 'addEventListener')
    const limparPrazo = vi.spyOn(prazo.signal, 'removeEventListener')
    try {
      const buscar = vi.fn<Buscar>((_entrada, { signal }) =>
        new Promise<Response>((_, rejeitar) => {
          signal?.addEventListener('abort', () => rejeitar(signal.reason), { once: true })
        }),
      )
      buscar.mockResolvedValueOnce(Response.json({ items: [] }))
      const { api } = novaSessao({ buscar })
      const concluida = new AbortController()
      const ouvirConcluida = vi.spyOn(concluida.signal, 'addEventListener')
      const limparConcluida = vi.spyOn(concluida.signal, 'removeEventListener')

      await expect(api.doProjeto(PROJETO_PADRAO).alvos(concluida.signal)).resolves.toEqual({ items: [] })
      expect(ouvirConcluida).toHaveBeenCalledTimes(1)
      expect(limparConcluida).toHaveBeenCalledExactlyOnceWith('abort', ouvirConcluida.mock.calls[0]?.[1])
      expect(ouvirPrazo).toHaveBeenCalledTimes(1)
      expect(limparPrazo).toHaveBeenCalledExactlyOnceWith('abort', ouvirPrazo.mock.calls[0]?.[1])
      expect(vi.getTimerCount()).toBe(0)

      const pendente = new AbortController()
      const ouvirPendente = vi.spyOn(pendente.signal, 'addEventListener')
      const limparPendente = vi.spyOn(pendente.signal, 'removeEventListener')
      const motivo = new DOMException('Consulta cancelada.', 'AbortError')
      const resultado = api.doProjeto(PROJETO_PADRAO).alvos(pendente.signal).catch((erro: unknown) => erro)
      expect(ouvirPendente).toHaveBeenCalledTimes(1)
      pendente.abort(motivo)

      await expect(resultado).resolves.toBe(motivo)
      expect(buscar.mock.calls[1]?.[1].signal?.reason).toBe(motivo)
      expect(limparPendente).toHaveBeenCalledExactlyOnceWith('abort', ouvirPendente.mock.calls[0]?.[1])
      expect(ouvirPrazo).toHaveBeenCalledTimes(2)
      expect(limparPrazo).toHaveBeenNthCalledWith(2, 'abort', ouvirPrazo.mock.calls[1]?.[1])
      expect(vi.getTimerCount()).toBe(0)
    } finally {
      criarPrazo.mockRestore()
      vi.useRealTimers()
      if (metodo) Object.defineProperty(AbortSignal, 'any', metodo)
      else Reflect.deleteProperty(AbortSignal, 'any')
    }
  })

  it('cancela só a espera desta consulta durante o refresh compartilhado', async () => {
    let responderRefresh!: (r: Response) => void
    const refresh = new Promise<Response>((resolve) => { responderRefresh = resolve })
    const buscar = vi.fn<Buscar>(async (entrada, init) => {
      if (entrada.endsWith('/auth/login')) return Response.json({ access_token: 'antes', expires_in: 900 })
      if (entrada.endsWith('/auth/refresh')) return refresh
      if (new Headers(init.headers).get('Authorization') === 'Bearer antes') {
        return Response.json({ error: { code: 'unauthorized' } }, { status: 401 })
      }
      return Response.json({ items: [] })
    })
    const { api, sessao } = novaSessao({ buscar })
    await sessao.entrar(EMAIL, SENHA)
    const renovar = vi.spyOn(sessao, 'renovar')
    const controlador = new AbortController()
    const motivo = new DOMException('Consulta cancelada.', 'AbortError')
    const terminou = vi.fn<(resultado: unknown) => void>()
    const cancelada = api.doProjeto(PROJETO_PADRAO).alvos(controlador.signal)
    const ativa = api.doProjeto(PROJETO_PADRAO).heartbeats()
    void cancelada.then(terminou, terminou)
    const ambas = Promise.allSettled([cancelada, ativa])
    try {
      await vi.waitFor(() => expect(renovar).toHaveBeenCalledTimes(2))
      controlador.abort(motivo)
      await vi.waitFor(() => expect(terminou).toHaveBeenCalledExactlyOnceWith(motivo), { timeout: 250 })
      const refreshes = buscar.mock.calls.filter(([entrada]) => entrada.endsWith('/auth/refresh'))
      expect(refreshes).toHaveLength(1)
      expect(refreshes[0]?.[1].signal?.aborted).toBe(false)
    } finally {
      responderRefresh(Response.json({ access_token: 'novo', expires_in: 900 }))
      await ambas
      renovar.mockRestore()
    }
    await expect(ativa).resolves.toEqual({ items: [] })
    expect(buscar.mock.calls.filter(([entrada]) => entrada.includes('/targets?'))).toHaveLength(1)
    expect(buscar.mock.calls.filter(([entrada]) => entrada.includes('/heartbeats?'))).toHaveLength(2)
    expect(sessao.token()).toBe('novo')
    expect(sessao.estado()).toBe('autenticado')
  })
})

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
      tags: [],
      tls_warn: true,
      tls: null,
      content_rule: null,
      created_at: '2026-09-24T12:00:00Z',
    }))
    falsa.estado.atrasoRefresh = 20
    falsa.expirarAcessos()
    const r = await Promise.all([1, 2, 3, 4, 5].map((i) => api.doProjeto(PROJETO_PADRAO).alvo(`a${i}`)))
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

  // Durante um deploy o refresh falha sem dizer nada sobre a sessão: ela pode
  // estar boa, e só o 401 do refresh a encerra (spec §11.2; aqui todo merge é
  // deploy). Sabotagens previstas: em pedir(), "sessao.renovar(usado).catch(()
  // => null)" derruba os quatro casos; em renovarAgora(), "r.status !== 401 &&
  // r.status < 500" no lugar de "r.status !== 401" derruba os três 5xx.
  describe.each([
    {
      caso: '503 da API',
      status: 503,
      code: 'unavailable',
      provocar: (falsa: ApiFalsa) => falsa.estado.forcar.set('POST /auth/refresh', { status: 503, code: 'unavailable' }),
    },
    {
      caso: '500 da API',
      status: 500,
      code: 'internal_error',
      provocar: (falsa: ApiFalsa) => falsa.estado.forcar.set('POST /auth/refresh', { status: 500, code: 'internal_error' }),
    },
    {
      caso: '502 do proxy, sem o envelope de erro',
      status: 502,
      code: 'unexpected',
      provocar: () =>
        servidor.use(
          http.post(
            ROTA_DO_REFRESH,
            () => new HttpResponse('<h1>Bad Gateway</h1>', { status: 502, headers: { 'Content-Type': 'text/html' } }),
          ),
        ),
    },
    {
      caso: 'rede caída',
      status: 0,
      code: 'network',
      provocar: () => servidor.use(http.post(ROTA_DO_REFRESH, () => HttpResponse.error())),
    },
  ])('refresh com $caso', ({ status, code, provocar }) => {
    it('a chamada falha com o erro do refresh, sem repetir, e a sessão fica aberta', async () => {
      const { falsa, sessao, api } = await logado()
      const tokenAntes = sessao.token()
      falsa.expirarAcessos()
      provocar(falsa)

      const erro = await api.me().catch((x: unknown) => x)

      expect(erro).toBeInstanceOf(ApiError)
      expect(erro).toMatchObject({ status, code })
      expect(falsa.chamadas('GET /me')).toBe(1)
      expect(sessao.estado()).toBe('autenticado')
      expect(sessao.motivo()).toBeNull()
      expect(tokenAntes).not.toBeNull()
      expect(sessao.token()).toBe(tokenAntes)
    })
  })

  // A prova de que a sessão não ficou "meio aberta". Sabotagem prevista: tirar
  // o ".finally" que limpa o emVoo em renovar() → a promessa rejeitada fica e
  // nenhum refresh novo sai.
  it('passado o 503, a chamada seguinte renova e a sessão segue', async () => {
    const { falsa, sessao, api } = await logado()
    falsa.expirarAcessos()
    falsa.estado.forcar.set('POST /auth/refresh', { status: 503, code: 'unavailable' })
    await expect(api.me()).rejects.toMatchObject({ status: 503 })

    falsa.estado.forcar.delete('POST /auth/refresh')

    await expect(api.me()).resolves.toMatchObject({ email: EMAIL })
    expect(falsa.chamadas('POST /auth/refresh')).toBe(2)
    expect(sessao.estado()).toBe('autenticado')
  })

  // O contraste dos casos acima, pela mesma rota: aqui o servidor recusou a
  // sessão. Sabotagem prevista: tirar o "sessao.encerrar('expirou')" do !novo
  // em pedir().
  it('refresh respondendo 401 encerra a sessão, larga o token e devolve o 401 da chamada', async () => {
    const { falsa, sessao, api } = await logado()
    falsa.expirarAcessos()
    falsa.estado.forcar.set('POST /auth/refresh', { status: 401, code: 'unauthorized' })

    const erro = await api.me().catch((x: unknown) => x)

    expect(erro).toBeInstanceOf(ApiError)
    expect(erro).toMatchObject({ status: 401, code: 'unauthorized' })
    expect(falsa.chamadas('POST /auth/refresh')).toBe(1)
    expect(falsa.chamadas('GET /me')).toBe(1)
    expect(sessao.estado()).toBe('anonimo')
    expect(sessao.motivo()).toBe('expirou')
    expect(sessao.token()).toBeNull()
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
    await expect(api.doProjeto(PROJETO_PADRAO).reconhecer('nao-existe')).rejects.toMatchObject({ code: 'not_found' })
  })
})

describe('o envelope de erro', () => {
  it('traz code, fields e trace_id', async () => {
    const { falsa, api } = await logado()
    falsa.estado.forcar.set('POST /projects/:projectId/targets', {
      status: 400,
      code: 'validation_failed',
      fields: [{ field: 'url', reason: 'forbidden' }],
    })
    const e = await api
      .doProjeto(PROJETO_PADRAO).criarAlvo({
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
    await expect(api.doProjeto(PROJETO_PADRAO).resumo()).rejects.toMatchObject({ name: 'ApiError', status: 200, code: 'unexpected' })
    servidor.use(http.get('*/api/v1/me', () => new HttpResponse('{quebrado', { status: 200, headers: { 'Content-Type': 'application/json' } })))
    await expect(api.me()).rejects.toMatchObject({ name: 'ApiError', status: 200, code: 'unexpected' })
  })

  // Parse, don't validate: com esquema, um 204 (ou qualquer sucesso sem corpo)
  // não pode chegar à tela como `undefined` com tipo T; é o mesmo erro de
  // contrato do JSON quebrado. Os endpoints que respondem 204 de verdade (os
  // DELETE, logout, password, totp/disable, resend, clear) não têm esquema.
  // Sabotagem: devolver undefined quando o status é 204, também com esquema.
  it('204 numa rota com esquema vira ApiError "unexpected", não undefined com tipo T', async () => {
    const { api } = await logado()
    servidor.use(http.get('*/api/v1/me', () => new HttpResponse(null, { status: 204 })))
    await expect(api.me()).rejects.toMatchObject({ name: 'ApiError', status: 204, code: 'unexpected' })
  })

  // D10: uma chave que some do DTO quebra alto, com o nome do campo.
  it('resposta fora do schema falha com o nome do campo', async () => {
    const { falsa, api } = await logado()
    falsa.estado.resumo = [{ id: 'x', name: 'API' } as never]
    await expect(api.doProjeto(PROJETO_PADRAO).resumo()).rejects.toThrow(/enabled|state/)
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
    const desconhecido = await patchCru(sessao.token(), `/projects/${PROJETO_PADRAO}/channels/canal-1`, '{"enabled":true,"cor":"azul"}')
    expect(desconhecido.status).toBe(400)
    const envelope = (await desconhecido.json()) as { error: { code: string; fields?: unknown } }
    expect(envelope.error.code).toBe('validation_failed')
    expect(envelope.error.fields).toBeUndefined()
    const semCanal = await patchCru(sessao.token(), `/projects/${PROJETO_PADRAO}/channels/nao-existe`, '{"enabled":"sim"}')
    expect(semCanal.status).toBe(400)
    expect((await semCanal.json()).error.fields).toEqual([{ field: 'enabled', reason: 'invalid' }])
    expect((await patchCru(sessao.token(), `/projects/${PROJETO_PADRAO}/channels/nao-existe`, '{"enabled":true}')).status).toBe(404)
  })

  // Sem esquema (DELETE, 204) o pedido resolve sem valor: é o `undefined` que as
  // sobrecargas de `pedir` declaram como Promise<void>. Sabotagem: devolver o
  // corpo (ou um objeto) no lugar de undefined.
  it('apagar um canal que existe resolve sem valor', async () => {
    const { falsa, api } = await logado()
    const c = canal({ id: 'c1' })
    falsa.estado.canais = [c]
    await expect(api.doProjeto(PROJETO_PADRAO).apagarCanal('c1')).resolves.toBeUndefined()
    expect(falsa.estado.canais).toEqual([])
  })

  // ChannelHandlers.Delete e Resend: canal que não existe é 404.
  it('apagar ou reenviar a verificação de canal que não existe é 404', async () => {
    const { api } = await logado()
    await expect(api.doProjeto(PROJETO_PADRAO).apagarCanal('nao-existe')).rejects.toMatchObject({ status: 404, code: 'not_found' })
    await expect(api.doProjeto(PROJETO_PADRAO).reenviarVerificacao('nao-existe')).rejects.toMatchObject({ status: 404, code: 'not_found' })
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

// Cada rota com id manda o id como um segmento só. O caminho.test.ts prova o
// helper; aqui se prova que cada método do cliente o usa. Sabotagem: trocar o
// caminho`…` de uma rota por um template cru → o `?c#d` do id vira query e
// fragmento, e a linha dessa rota falha.
describe('o id hostil vai codificado em toda rota com id', () => {
  const ID = 'a/b?c#d'
  const SEG = 'a%2Fb%3Fc%23d'
  const CURSOR = 'cur/sor?'
  const CURSOR_SEG = 'cur%2Fsor%3F'

  const rotas: [string, (api: ReturnType<typeof novaSessao>['api']) => Promise<unknown>, string][] = [
    ['alvo', (a) => a.doProjeto('p').alvo(ID), `GET /api/v1/projects/p/targets/${SEG}`],
    ['alterarAlvo', (a) => a.doProjeto('p').alterarAlvo(ID, {}), `PATCH /api/v1/projects/p/targets/${SEG}`],
    ['apagarAlvo', (a) => a.doProjeto('p').apagarAlvo(ID), `DELETE /api/v1/projects/p/targets/${SEG}`],
    ['serie', (a) => a.doProjeto('p').serie(ID, '24h'), `GET /api/v1/projects/p/targets/${SEG}/checks?window=24h`],
    ['reconhecer', (a) => a.doProjeto('p').reconhecer(ID), `POST /api/v1/projects/p/incidents/${SEG}/ack`],
    ['heartbeat', (a) => a.doProjeto('p').heartbeat(ID), `GET /api/v1/projects/p/heartbeats/${SEG}`],
    ['alterarHeartbeat', (a) => a.doProjeto('p').alterarHeartbeat(ID, {}), `PATCH /api/v1/projects/p/heartbeats/${SEG}`],
    ['apagarHeartbeat', (a) => a.doProjeto('p').apagarHeartbeat(ID), `DELETE /api/v1/projects/p/heartbeats/${SEG}`],
    ['rotacionarHeartbeat', (a) => a.doProjeto('p').rotacionarHeartbeat(ID), `POST /api/v1/projects/p/heartbeats/${SEG}/token/rotate`],
    ['duplicarHeartbeat', (a) => a.doProjeto('p').duplicarHeartbeat(ID), `POST /api/v1/projects/p/heartbeats/${SEG}/clone`],
    ['limparEventosDoHeartbeat', (a) => a.doProjeto('p').limparEventosDoHeartbeat(ID), `POST /api/v1/projects/p/heartbeats/${SEG}/events/clear`],
    ['sinalizarAgora', (a) => a.doProjeto('p').sinalizarAgora(ID), `POST /api/v1/projects/p/heartbeats/${SEG}/ping-now`],
    ['eventosDoHeartbeat', (a) => a.doProjeto('p').eventosDoHeartbeat(ID), `GET /api/v1/projects/p/heartbeats/${SEG}/events?limit=20`],
    [
      'eventosDoHeartbeat com cursor',
      (a) => a.doProjeto('p').eventosDoHeartbeat(ID, CURSOR),
      `GET /api/v1/projects/p/heartbeats/${SEG}/events?cursor=${CURSOR_SEG}&limit=20`,
    ],
    ['incidentesDoHeartbeat', (a) => a.doProjeto('p').incidentesDoHeartbeat(ID), `GET /api/v1/projects/p/heartbeats/${SEG}/incidents?limit=20`],
    [
      'incidentesDoHeartbeat com cursor',
      (a) => a.doProjeto('p').incidentesDoHeartbeat(ID, CURSOR),
      `GET /api/v1/projects/p/heartbeats/${SEG}/incidents?cursor=${CURSOR_SEG}&limit=20`,
    ],
    [
      'reconhecerDoHeartbeat',
      (a) => a.doProjeto('p').reconhecerDoHeartbeat(ID, ID),
      `POST /api/v1/projects/p/heartbeats/${SEG}/incidents/${SEG}/ack`,
    ],
    ['apagarCanal', (a) => a.doProjeto('p').apagarCanal(ID), `DELETE /api/v1/projects/p/channels/${SEG}`],
    ['reenviarVerificacao', (a) => a.doProjeto('p').reenviarVerificacao(ID), `POST /api/v1/projects/p/channels/${SEG}/verify/resend`],
    ['pausarCanal', (a) => a.doProjeto('p').pausarCanal(ID, true), `PATCH /api/v1/projects/p/channels/${SEG}`],
    ['alterarPagina', (a) => a.doProjeto('p').alterarPagina(ID, {}), `PATCH /api/v1/projects/p/status-pages/${SEG}`],
    ['encerrarSessao', (a) => a.encerrarSessao(ID), `DELETE /api/v1/sessions/${SEG}`],
  ]

  it.each(rotas)('%s', async (_nome, chamar, esperada) => {
    const pedidos: string[] = []
    // 200 com `{}`: nem 401 (que dispararia o refresh) nem 5xx (que um retry
    // futuro poderia repetir) mexem na contagem de pedidos. O corpo vazio de
    // propósito não passa no schema de resposta, então a chamada pode rejeitar;
    // o que importa é a URL pedida, uma vez só.
    const buscar = (entrada: string, init: RequestInit) => {
      pedidos.push(`${init.method ?? 'GET'} ${entrada}`)
      return Promise.resolve(Response.json({}))
    }
    const { api } = novaSessao({ buscar })
    await chamar(api).catch(() => undefined)
    expect(pedidos).toEqual([esperada])
  })
})

// As rotas públicas do cadastro dispensam as credenciais da sessão.
// Se a API responder 401, o cliente devolve o erro sem renovar a sessão.
describe('o cadastro público', () => {
  it('cadastrar envia o corpo exato sem credenciais da sessão', async () => {
    const falsa = usarApiFalsa()
    const cabecalhos: Headers[] = []
    const buscar = vi.fn<Buscar>((entrada, init) => {
      cabecalhos.push(new Headers(init.headers))
      return buscarDoTeste(entrada, init)
    })
    const { sessao, api } = novaSessao({ buscar })
    // Com uma sessão aberta: o token existe, e mesmo assim não vai.
    await sessao.entrar(EMAIL, SENHA)
    const entrada = {
      email: 'nova@exemplo.com',
      password: SENHA,
      turnstile_token: TOKEN_DO_TURNSTILE,
      accepted_terms_version: versaoDosTermos.versao,
    }

    await expect(api.cadastrar(entrada)).resolves.toBeUndefined()

    expect(falsa.corpos('POST /auth/signup')).toEqual([entrada])
    const doCadastro = cabecalhos.at(-1)!
    expect(doCadastro.has('Authorization')).toBe(false)
    expect(buscar.mock.lastCall?.[1].credentials).toBe('omit')
    expect(doCadastro.get('X-Requested-With')).toBe('stillon')
  })

  it('confirmarEmail com token_invalid rejeita com ApiError code token_invalid', async () => {
    const falsa = usarApiFalsa()
    const { api } = novaSessao()

    const e = await api.confirmarEmail('token-que-nao-existe', SENHA).catch((x: unknown) => x)

    expect(e).toBeInstanceOf(ApiError)
    expect(e).toMatchObject({ status: 400, code: 'token_invalid' })
    expect(falsa.corpos('POST /auth/email/confirm')).toEqual([{ token: 'token-que-nao-existe', password: SENHA }])
    expect(falsa.chamadas('POST /auth/refresh')).toBe(0)
  })

  it('confirmarEmail com senha errada rejeita com invalid_credentials sem tentar o refresh', async () => {
    const falsa = usarApiFalsa()
    falsa.estado.cadastro.tokens.set('token-1', 'confirm_email')
    const { api } = novaSessao()

    const e = await api.confirmarEmail('token-1', 'outra senha qualquer').catch((x: unknown) => x)

    expect(e).toBeInstanceOf(ApiError)
    expect(e).toMatchObject({ status: 401, code: 'invalid_credentials' })
    expect(falsa.chamadas('POST /auth/refresh')).toBe(0)
  })

  it('me aceita email_verified', async () => {
    const { falsa, api } = await logado()
    falsa.estado.emailConfirmado = false
    await expect(api.me()).resolves.toMatchObject({ email_verified: false })
    falsa.estado.emailConfirmado = true
    await expect(api.me()).resolves.toMatchObject({ email_verified: true })
  })

  it('configDoCadastro lê enabled, a site key e a versão dos termos', async () => {
    const falsa = usarApiFalsa()
    const { api } = novaSessao()
    const terms_version = versaoDosTermos.versao

    await expect(api.configDoCadastro()).resolves.toEqual({
      enabled: true,
      turnstile_site_key: SITE_KEY_DO_TURNSTILE,
      terms_version,
    })
    falsa.estado.cadastro.habilitado = false
    await expect(api.configDoCadastro()).resolves.toEqual({ enabled: false, turnstile_site_key: '', terms_version })
  })
})

// Os recursos moram no projeto (spec do PR 2b): o id do projeto entra no
// caminho codificado como segmento, como o id do recurso.
describe('recursos vão para o projeto', () => {
  function capturar() {
    const pedidos: { rota: string; init: RequestInit }[] = []
    const buscar = (entrada: string, init: RequestInit) => {
      pedidos.push({ rota: `${init.method ?? 'GET'} ${entrada}`, init })
      return Promise.resolve(Response.json({}))
    }
    return { pedidos, ...novaSessao({ buscar }) }
  }

  // Sabotagem: doProjeto sem o prefixo /projects/{id} → a rota sai na raiz.
  it('o recurso do projeto vai para /projects/{id}, com o id codificado', async () => {
    const { pedidos, api } = capturar()
    await api.doProjeto('p/1').alvo('a').catch(() => undefined)
    expect(pedidos.map((p) => p.rota)).toEqual(['GET /api/v1/projects/p%2F1/targets/a'])
  })

  it('a lista de projetos vem de /projects', async () => {
    const { pedidos, api } = capturar()
    await api.projetos().catch(() => undefined)
    expect(pedidos.map((p) => p.rota)).toEqual(['GET /api/v1/projects'])
  })

  it('criarProjeto manda X-Requested-With e o corpo {name}', async () => {
    const { pedidos, api } = capturar()
    await api.criarProjeto({ name: 'Loja' }).catch(() => undefined)
    expect(pedidos.map((p) => p.rota)).toEqual(['POST /api/v1/projects'])
    const init = pedidos[0]!.init
    expect(new Headers(init.headers).get('X-Requested-With')).toBe('stillon')
    expect(JSON.parse(String(init.body))).toEqual({ name: 'Loja' })
  })

  it('o projeto da API falsa responde com os recursos dele, não com os do padrão', async () => {
    const { falsa, api } = await logado()
    const outro = falsa.criarProjeto('Loja')
    outro.recursos.alvos.push(alvo({ id: 'alvo-da-loja' }))
    falsa.estado.alvos.push(alvo({ id: 'alvo-do-padrao' }))
    expect((await api.doProjeto(outro.projeto.id).alvos()).items.map((a) => a.id)).toEqual(['alvo-da-loja'])
    expect((await api.doProjeto(PROJETO_PADRAO).alvos()).items.map((a) => a.id)).toEqual(['alvo-do-padrao'])
  })

  it('a API falsa lista e cria projetos, o padrão primeiro', async () => {
    const { api } = await logado()
    const criado = await api.criarProjeto({ name: 'Loja' })
    expect(criado).toMatchObject({ name: 'Loja', is_default: false, role: 'owner' })
    expect((await api.projetos()).items.map((p) => [p.id, p.is_default])).toEqual([
      [PROJETO_PADRAO, true],
      [criado.id, false],
    ])
  })

  it('projeto que não existe é 404 na API falsa', async () => {
    const { api } = await logado()
    await expect(api.doProjeto('nao-existe').alvos()).rejects.toMatchObject({ code: 'not_found' })
  })
})
