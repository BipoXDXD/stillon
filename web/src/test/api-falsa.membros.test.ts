import { describe, expect, it } from 'vitest'
import { usarApiFalsa } from './ambiente'
import { EMAIL, PROJETO_PADRAO, USUARIO } from './api-falsa'

// As rotas da equipe na API falsa imitam as guardas do servidor: sem isso, as
// telas de membros passariam num estado que o servidor recusa.

const TOKEN = 'acesso-do-teste'

function apiLogada() {
  const falsa = usarApiFalsa()
  falsa.estado.acessos.set(TOKEN, falsa.estado.familia)
  return falsa
}

function pedir(metodo: string, caminho: string, corpo?: unknown) {
  const headers = new Headers({ 'X-Requested-With': 'stillon', Authorization: `Bearer ${TOKEN}` })
  const init: RequestInit = { method: metodo, headers }
  if (corpo !== undefined) {
    headers.set('Content-Type', 'application/json')
    init.body = JSON.stringify(corpo)
  }
  return fetch(new URL(`/api/v1${caminho}`, location.origin), init)
}

async function codigo(r: Response) {
  return { status: r.status, code: ((await r.json()) as { error: { code: string } }).error.code }
}

// Um projeto do usuário (titular) com a equipe dada.
function projetoComEquipe(falsa: ReturnType<typeof apiLogada>) {
  const pf = falsa.criarProjeto('Loja')
  return { pf, base: `/projects/${pf.projeto.id}` }
}

describe('a equipe na API falsa', () => {
  it('corpo com campo a mais é 400', async () => {
    const falsa = apiLogada()
    const { base } = projetoComEquipe(falsa)
    const r = await pedir('POST', `${base}/invitations`, { email: 'a@exemplo.com', role: 'viewer', papel: 'x' })
    expect(await codigo(r)).toEqual({ status: 400, code: 'validation_failed' })
    expect(falsa.estado.projetos.find((p) => p.projeto.name === 'Loja')!.convites).toEqual([])
  })

  it('viewer convidando é 403', async () => {
    const falsa = apiLogada()
    falsa.definirPapel(PROJETO_PADRAO, 'viewer')
    const r = await pedir('POST', `/projects/${PROJETO_PADRAO}/invitations`, { email: 'a@exemplo.com', role: 'viewer' })
    expect(await codigo(r)).toEqual({ status: 403, code: 'forbidden' })
  })

  it('manager convidando manager é 403, e convidando member passa', async () => {
    const falsa = apiLogada()
    falsa.definirPapel(PROJETO_PADRAO, 'manager')
    const base = `/projects/${PROJETO_PADRAO}`
    expect(await codigo(await pedir('POST', `${base}/invitations`, { email: 'a@exemplo.com', role: 'manager' }))).toEqual({
      status: 403,
      code: 'forbidden',
    })
    expect((await pedir('POST', `${base}/invitations`, { email: 'a@exemplo.com', role: 'member' })).status).toBe(201)
  })

  // O teto conta os membros além do titular e os convites pendentes.
  // Sabotagem: tirar os convites da conta → o 4º passa.
  it('o 4º além do titular é 409 member_limit_reached, contando convites pendentes', async () => {
    const falsa = apiLogada()
    const { pf, base } = projetoComEquipe(falsa)
    falsa.adicionarMembro(pf.projeto.id, { user_id: 'u2', email: 'b@exemplo.com', role: 'member' })
    falsa.adicionarConvite(pf.projeto.id, { email: 'c@exemplo.com', role: 'viewer' })
    expect((await pedir('POST', `${base}/invitations`, { email: 'd@exemplo.com', role: 'viewer' })).status).toBe(201)
    const r = await pedir('POST', `${base}/invitations`, { email: 'e@exemplo.com', role: 'viewer' })
    expect(await codigo(r)).toEqual({ status: 409, code: 'member_limit_reached' })
    // Reconvidar um e-mail pendente troca o convite: mesma vaga.
    expect((await pedir('POST', `${base}/invitations`, { email: 'c@exemplo.com', role: 'member' })).status).toBe(201)
    expect(pf.convites.map((c) => [c.email, c.role])).toEqual([
      ['d@exemplo.com', 'viewer'],
      ['c@exemplo.com', 'member'],
    ])
  })

  it('aceitar o convite de outro e-mail é 403 invitation_other_account', async () => {
    const falsa = apiLogada()
    const alheio = falsa.criarProjetoAlheio('Outra')
    const token = falsa.adicionarConvite(alheio.projeto.id, { email: 'outra@exemplo.com', role: 'viewer' })
    const r = await pedir('POST', '/invitations/accept', { token })
    expect(await codigo(r)).toEqual({ status: 403, code: 'invitation_other_account' })
  })

  it('o token já usado é 400 token_invalid', async () => {
    const falsa = apiLogada()
    const alheio = falsa.criarProjetoAlheio('Outra')
    const token = falsa.adicionarConvite(alheio.projeto.id, { email: EMAIL, role: 'member' })
    const aceito = await pedir('POST', '/invitations/accept', { token })
    expect(aceito.status).toBe(200)
    expect(await aceito.json()).toMatchObject({ id: alheio.projeto.id, role: 'member', pending_titular: false })
    expect(falsa.estado.projetos.some((p) => p.projeto.id === alheio.projeto.id)).toBe(true)
    expect(await codigo(await pedir('POST', '/invitations/accept', { token }))).toEqual({
      status: 400,
      code: 'token_invalid',
    })
  })

  // ClearTitularTransferTo do 4.3: rebaixar o pendente apaga a proposta.
  it('rebaixar o pendente limpa a proposta', async () => {
    const falsa = apiLogada()
    const { pf, base } = projetoComEquipe(falsa)
    falsa.adicionarMembro(pf.projeto.id, { user_id: 'u2', email: 'b@exemplo.com', role: 'owner' })
    expect((await pedir('POST', `${base}/titular-transfer`, { user_id: 'u2' })).status).toBe(202)
    expect(pf.pendente).toBe('u2')
    const lista = (await (await pedir('GET', `${base}/members`)).json()) as { items: { user_id: string; pending_titular: boolean }[] }
    expect(lista.items.map((m) => [m.user_id, m.pending_titular])).toEqual([
      [USUARIO, false],
      ['u2', true],
    ])
    const r = await pedir('PATCH', `${base}/members/u2`, { role: 'manager' })
    expect(r.status).toBe(200)
    expect(await r.json()).toMatchObject({ user_id: 'u2', role: 'manager', pending_titular: false })
    expect(pf.pendente).toBeNull()
  })

  it('mexer no titular é 409 titular_protected, e ele não sai', async () => {
    const falsa = apiLogada()
    const { base } = projetoComEquipe(falsa)
    expect(await codigo(await pedir('PATCH', `${base}/members/${USUARIO}`, { role: 'viewer' }))).toEqual({
      status: 409,
      code: 'titular_protected',
    })
    expect(await codigo(await pedir('DELETE', `${base}/members/${USUARIO}`))).toEqual({
      status: 409,
      code: 'titular_protected',
    })
  })

  it('propor ao projeto padrão ou a quem não é owner é 409 titular_transfer_target', async () => {
    const falsa = apiLogada()
    const { pf, base } = projetoComEquipe(falsa)
    falsa.adicionarMembro(pf.projeto.id, { user_id: 'u2', email: 'b@exemplo.com', role: 'manager' })
    falsa.adicionarMembro(PROJETO_PADRAO, { user_id: 'u3', email: 'c@exemplo.com', role: 'owner' })
    for (const [caminho, alvo] of [
      [`${base}/titular-transfer`, 'u2'],
      [`/projects/${PROJETO_PADRAO}/titular-transfer`, 'u3'],
    ] as const) {
      expect(await codigo(await pedir('POST', caminho, { user_id: alvo }))).toEqual({
        status: 409,
        code: 'titular_transfer_target',
      })
    }
  })

  it('quem recebe a proposta vê pending_titular, aceita e vira titular', async () => {
    const falsa = apiLogada()
    const { pf, base } = projetoComEquipe(falsa)
    falsa.adicionarMembro(pf.projeto.id, { user_id: 'u2', email: 'b@exemplo.com', role: 'owner' })
    // O usuário deixa de ser titular: outro titular propõe a ele.
    for (const m of pf.membros) m.is_titular = m.user_id === 'u2'
    falsa.proporTransferenciaA(pf.projeto.id, USUARIO)
    const projetos = (await (await pedir('GET', '/projects')).json()) as { items: { id: string; pending_titular: boolean }[] }
    expect(projetos.items.find((p) => p.id === pf.projeto.id)?.pending_titular).toBe(true)
    const r = await pedir('POST', `${base}/titular-transfer/accept`)
    expect(r.status).toBe(200)
    expect(pf.membros.find((m) => m.is_titular)?.user_id).toBe(USUARIO)
    expect(pf.pendente).toBeNull()
  })

  // apagarConvitesQueNaoPoderiaMandar: rebaixar quem convidou apaga os
  // pendentes que o papel novo não poderia mandar; os dos outros ficam.
  // Sabotagem: não apagar → o convite do rebaixado continua na lista.
  it('rebaixar quem convidou apaga os convites que ele não pode mais mandar', async () => {
    const falsa = apiLogada()
    const { pf, base } = projetoComEquipe(falsa)
    falsa.adicionarMembro(pf.projeto.id, { user_id: 'u2', email: 'b@exemplo.com', role: 'manager' })
    falsa.adicionarConvite(pf.projeto.id, { email: 'c@exemplo.com', role: 'member', por: 'u2' })
    falsa.adicionarConvite(pf.projeto.id, { email: 'd@exemplo.com', role: 'viewer' })
    expect((await pedir('PATCH', `${base}/members/u2`, { role: 'member' })).status).toBe(200)
    const lista = (await (await pedir('GET', `${base}/invitations`)).json()) as { items: { email: string }[] }
    expect(lista.items.map((c) => c.email)).toEqual(['d@exemplo.com'])
  })
})
