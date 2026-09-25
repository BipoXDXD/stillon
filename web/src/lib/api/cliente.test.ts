import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import { EMAIL, rotacao, SEGREDO_NOVO, SENHA, TOTP_NOVO, TOTP_OK } from '@/test/api-falsa'
import { buscarDoTeste, novaSessao, usarApiFalsa } from '@/test/ambiente'
import { servidor } from '@/test/setup'
import { ApiError } from './http'

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
        HttpResponse.json({ otpauth_uri: 'otpauth://totp/Vigilume:x?secret=AAAA' }),
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
