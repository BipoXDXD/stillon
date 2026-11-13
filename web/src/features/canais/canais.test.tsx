import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { http } from 'msw'
import { describe, expect, it } from 'vitest'
import { Canal } from '@/lib/api/schemas'
import { dataHora } from '@/lib/format'
import type { ErroForcado } from '@/test/api-falsa'
import { canal } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'
import { servidor } from '@/test/setup'

const RESEND = 'POST /channels/:id/verify/resend'
const PATCH = 'PATCH /channels/:id'
const escapar = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')

// O onClick que o React registrou no elemento, para simular um evento que
// chega ao handler antes de o disabled ser aplicado (o fireEvent respeita o
// disabled, e o clique duplo real no mesmo tique não passa por ele).
function onClickDoReact(el: HTMLElement): () => void {
  const chave = Object.keys(el).find((k) => k.startsWith('__reactProps$'))
  const props: unknown = chave ? Reflect.get(el, chave) : undefined
  const onClick: unknown = props && typeof props === 'object' ? Reflect.get(props, 'onClick') : undefined
  if (typeof onClick !== 'function') throw new Error('o elemento não tem onClick do React')
  return () => onClick()
}

// Com a lista ainda carregando, o "Novo canal" é o do topo; quando ela chega
// vazia, ele dá lugar ao do bloco. Clica depois de a lista chegar.
async function clicarNovoCanal(r: Awaited<ReturnType<typeof renderizarPainel>>) {
  await waitFor(() => expect(screen.queryByText('Carregando…')).not.toBeInTheDocument())
  await r.usuario.click(screen.getByRole('button', { name: 'Novo canal' }))
}

// Abre o diálogo, escolhe e-mail e manda o endereço.
async function cadastrarEmail(r: Awaited<ReturnType<typeof renderizarPainel>>, email: string) {
  await clicarNovoCanal(r)
  const dialogo = await screen.findByRole('dialog')
  await r.usuario.click(within(dialogo).getByLabelText('E-mail', { selector: 'input[type=radio]' }))
  await r.usuario.type(within(dialogo).getByLabelText('E-mail', { selector: 'input[type=email]' }), email)
  await r.usuario.click(within(dialogo).getByRole('button', { name: 'Cadastrar canal' }))
  return dialogo
}

describe('canais: lista vazia', () => {
  const titulo = () => screen.getByRole('heading', { name: 'Canais de notificação', level: 1 })

  // Uma ação primária só. Sabotagem: manter o botão do topo com a lista
  // vazia, ou tirar o ícone do bloco.
  it('ícone, frase e botão primário no bloco, sem o "Novo canal" do topo', async () => {
    const r = await renderizarPainel('/channels')
    await r.cliente.refetchQueries()
    const frase = await screen.findByText(/Nenhum canal/)
    const bloco = frase.parentElement
    if (bloco === null) throw new Error('a frase está solta')
    expect(screen.getAllByRole('button', { name: 'Novo canal' })).toHaveLength(1)
    expect(within(bloco).getByRole('button', { name: 'Novo canal' })).toHaveClass('bg-primaria')
    expect(bloco.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
  })

  // Vazio é o que o servidor disse: carregando ou com erro, ainda não se
  // sabe. Sabotagem: esconder o botão do topo sem olhar os dados.
  it('com erro na consulta, o botão do topo continua', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.forcar.set('GET /channels', { status: 500, code: 'internal_error' })
    await r.cliente.resetQueries({ queryKey: ['channels'] })
    await screen.findByRole('alert')
    expect(screen.getByRole('button', { name: 'Novo canal' })).toBeInTheDocument()
  })

  it('com canal na lista, o botão fica no topo e o bloco some', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [canal()]
    await r.cliente.refetchQueries()
    await screen.findByText('Verificado')
    expect(screen.getAllByRole('button', { name: 'Novo canal' })).toHaveLength(1)
    expect(screen.queryByText(/Nenhum canal/)).not.toBeInTheDocument()
  })

  // O botão do topo some quando a lista esvazia. Sabotagem: Botao no lugar do
  // BotaoQueSome → o foco cai no <body>.
  it('o botão do topo que some com o foco leva o foco ao título', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [canal()]
    await r.cliente.refetchQueries()
    await screen.findByText('Verificado')
    screen.getByRole('button', { name: 'Novo canal' }).focus()
    r.falsa.estado.canais = []
    await r.cliente.refetchQueries({ queryKey: ['channels'] })
    await screen.findByText(/Nenhum canal/)
    await waitFor(() => expect(titulo()).toHaveFocus())
  })

  // Sabotagem: Botao no lugar do BotaoQueSome no bloco vazio.
  it('o botão do bloco que some com o foco leva o foco ao título', async () => {
    const r = await renderizarPainel('/channels')
    await r.cliente.refetchQueries()
    await screen.findByText(/Nenhum canal/)
    screen.getByRole('button', { name: 'Novo canal' }).focus()
    r.falsa.estado.canais = [canal()]
    await r.cliente.refetchQueries({ queryKey: ['channels'] })
    await screen.findByText('Verificado')
    await waitFor(() => expect(titulo()).toHaveFocus())
  })
})

describe('canais', () => {
  it('mostra cada estado em texto, e a falha com motivo e data', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [
      canal(),
      canal({ id: 'c2', kind: 'email', hint: 'd***@exemplo.com', status: 'pending' }),
      canal({
        id: 'c3',
        hint: 'discord.com/…/zzzz',
        status: 'failing',
        last_error: 'invalid_channel',
        last_failure_at: '2026-09-24T11:00:00Z',
      }),
    ]
    await r.cliente.refetchQueries()
    expect(await screen.findByText('Verificado')).toBeInTheDocument()
    expect(screen.getByText('Aguardando verificação')).toBeInTheDocument()
    expect(screen.getByText('Falhando')).toBeInTheDocument()
    const falha = screen.getByText(/Falha: Canal inválido \(o webhook foi apagado\?\)/)
    expect(falha).toHaveTextContent(new RegExp(`, em ${escapar(dataHora('2026-09-24T11:00:00Z'))}$`))
    expect(falha).toHaveTextContent('24/09/2026')
    // Reenviar só aparece no pendente, com o canal no nome acessível.
    expect(screen.getAllByRole('button', { name: /^Reenviar verificação/ })).toHaveLength(1)
    expect(screen.getByRole('button', { name: 'Reenviar verificação do canal d***@exemplo.com' })).toBeInTheDocument()
  })

  it('cadastrar Discord verifica com a mensagem de teste', async () => {
    const r = await renderizarPainel('/channels')
    await clicarNovoCanal(r)
    const dialogo = await screen.findByRole('dialog')
    expect(within(dialogo).getByText(/Webhook de canal de fórum não é aceito/)).toBeInTheDocument()
    await r.usuario.type(within(dialogo).getByLabelText('URL do webhook'), 'https://discord.com/api/webhooks/1/abc')
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Cadastrar canal' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(r.falsa.corpos('POST /channels')[0]).toEqual({ kind: 'discord', url: 'https://discord.com/api/webhooks/1/abc' })
    expect(await screen.findByText('Canal verificado com a mensagem de teste.')).toBeInTheDocument()
  })

  it('entrega que falha e e-mail não configurado viram texto claro', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.forcar.set('POST /channels', { status: 502, code: 'delivery_failed' })
    await clicarNovoCanal(r)
    const dialogo = await screen.findByRole('dialog')
    await r.usuario.type(within(dialogo).getByLabelText('URL do webhook'), 'https://discord.com/api/webhooks/1/abc')
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Cadastrar canal' }))
    expect(await within(dialogo).findByRole('alert')).toHaveTextContent('Não conseguimos entregar a mensagem de teste')

    r.falsa.estado.forcar.set('POST /channels', {
      status: 400,
      code: 'validation_failed',
      fields: [{ field: 'kind', reason: 'unavailable' }],
    })
    await r.usuario.click(within(dialogo).getByLabelText('E-mail', { selector: 'input[type=radio]' }))
    await r.usuario.type(within(dialogo).getByLabelText('E-mail', { selector: 'input[type=email]' }), 'dono@exemplo.com')
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Cadastrar canal' }))
    await waitFor(() =>
      expect(within(dialogo).getByRole('alert')).toHaveTextContent('O envio de e-mail não está configurado'),
    )
    expect(r.falsa.corpos('POST /channels')[1]).toEqual({ kind: 'email', email: 'dono@exemplo.com' })
  })

  it('verificação expirada tem símbolo e texto, e pode ser reenviada', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [canal({ kind: 'email', hint: 'd***@exemplo.com', status: 'expired' })]
    await r.cliente.refetchQueries()
    expect(await screen.findByText('Verificação expirada')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Reenviar verificação do canal d***@exemplo.com' })).toBeInTheDocument()
  })

  it('e-mail que o servidor aceita chega ao POST, e o 400 email/invalid vira texto', async () => {
    const r = await renderizarPainel('/channels')
    await cadastrarEmail(r, 'josé@exemplo.com')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    await cadastrarEmail(r, 'dono@münchen.de')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(r.falsa.corpos('POST /channels')).toEqual([
      { kind: 'email', email: 'josé@exemplo.com' },
      { kind: 'email', email: 'dono@münchen.de' },
    ])

    r.falsa.estado.forcar.set('POST /channels', {
      status: 400,
      code: 'validation_failed',
      fields: [{ field: 'email', reason: 'invalid' }],
    })
    const dialogo = await cadastrarEmail(r, 'dono@x')
    expect(await within(dialogo).findByRole('alert')).toHaveTextContent('E-mail inválido.')
    expect(r.falsa.corpos('POST /channels')[2]).toEqual({ kind: 'email', email: 'dono@x' })
  })

  // A mensagem do campo sai de formState.errors de uma união discriminada (a
  // leitura tipada por nome, sem `as`); cada tipo mostra só o seu erro.
  // Sabotagem: ler o erro do campo errado (url no lugar de email).
  it('criar: a validação do formulário aparece junto do campo do tipo escolhido', async () => {
    const r = await renderizarPainel('/channels')
    await clicarNovoCanal(r)
    const dialogo = await screen.findByRole('dialog')
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Cadastrar canal' }))
    expect(await within(dialogo).findByText('Cole a URL do webhook.')).toBeInTheDocument()
    await r.usuario.click(within(dialogo).getByLabelText('E-mail', { selector: 'input[type=radio]' }))
    expect(within(dialogo).queryByText('Cole a URL do webhook.')).not.toBeInTheDocument()
    await r.usuario.type(within(dialogo).getByLabelText('E-mail', { selector: 'input[type=email]' }), 'sem-arroba')
    await r.usuario.click(within(dialogo).getByRole('button', { name: 'Cadastrar canal' }))
    expect(await within(dialogo).findByText('Informe um e-mail válido.')).toBeInTheDocument()
    expect(r.falsa.corpos('POST /channels')).toEqual([])
  })

  it('criar: limite de canais, limite de envios e URL de webhook inválida viram texto', async () => {
    const r = await renderizarPainel('/channels')
    await clicarNovoCanal(r)
    const dialogo = await screen.findByRole('dialog')
    await r.usuario.type(within(dialogo).getByLabelText('URL do webhook'), 'https://discord.com/api/webhooks/1/abc')
    const casos: [ErroForcado, string][] = [
      [{ status: 409, code: 'channel_limit_reached' }, 'Limite de 10 canais atingido.'],
      [{ status: 429, code: 'rate_limited' }, 'Limite de envios atingido (1 a cada 6 minutos): tente de novo em alguns minutos.'],
      [
        { status: 400, code: 'validation_failed', fields: [{ field: 'url', reason: 'invalid' }] },
        'Use a URL do webhook do Discord (https://discord.com/api/webhooks/…).',
      ],
    ]
    for (const [forcado, texto] of casos) {
      r.falsa.estado.forcar.set('POST /channels', forcado)
      await r.usuario.click(within(dialogo).getByRole('button', { name: 'Cadastrar canal' }))
      await waitFor(() => expect(within(dialogo).getByRole('alert')).toHaveTextContent(texto))
    }
  })

  it('reenviar: um envio só, mesmo com dois cliques', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [canal({ kind: 'email', hint: 'd***@exemplo.com', status: 'pending' })]
    await r.cliente.refetchQueries()
    const botao = await screen.findByRole('button', { name: 'Reenviar verificação do canal d***@exemplo.com' })
    fireEvent.click(botao)
    fireEvent.click(botao)
    // Os dois cliques já saíram; um aviso por envio.
    expect((await screen.findAllByText('Link de verificação reenviado.'))[0]).toBeInTheDocument()
    expect(r.falsa.chamadas(RESEND)).toBe(1)
    await waitFor(() => expect(botao).toBeEnabled())
  })

  // E8 da revisão de 2026-10-14: o estado guardava um id só. Com o reenvio de
  // um canal em voo, o clique no de outro canal reabilitava o primeiro, e um
  // novo clique nele mandava o 2º POST (o servidor responde 429 ao 2º).
  // Sabotagens: voltar a um id só (useState<string | null>) → o primeiro
  // reabilita com o POST dele em voo; tirar a guarda da ref em reenviar() → o
  // handler chamado antes do disabled manda o 3º POST.
  it('reenviar: o envio em voo de um canal continua bloqueado ao reenviar outro', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [
      canal({ id: 'c1', kind: 'email', hint: 'a***@exemplo.com', status: 'pending' }),
      canal({ id: 'c2', kind: 'email', hint: 'b***@exemplo.com', status: 'pending' }),
    ]
    await r.cliente.refetchQueries()
    let soltar!: () => void
    const aberto = new Promise<void>((res) => (soltar = res))
    let chegou = 0
    servidor.use(
      http.post('*/api/v1/channels/:id/verify/resend', async () => {
        chegou++
        await aberto
        return new Response(null, { status: 204 })
      }),
    )
    const primeiro = await screen.findByRole('button', { name: 'Reenviar verificação do canal a***@exemplo.com' })
    const segundo = screen.getByRole('button', { name: 'Reenviar verificação do canal b***@exemplo.com' })
    await r.usuario.click(primeiro)
    await waitFor(() => expect(chegou).toBe(1))
    await r.usuario.click(segundo)
    await waitFor(() => expect(chegou).toBe(2))
    // Os dois seguem em voo: nenhum dos botões solta.
    expect(primeiro).toBeDisabled()
    expect(segundo).toBeDisabled()
    // Um clique que chegue ao handler antes do React desabilitar o botão
    // (dois eventos no mesmo tique) também não manda outro POST.
    act(() => onClickDoReact(primeiro)())
    await new Promise((res) => setTimeout(res, 50))
    expect(chegou).toBe(2)
    soltar()
    await waitFor(() => expect(primeiro).toBeEnabled())
    await waitFor(() => expect(segundo).toBeEnabled())
  })

  it('reenviar: limite de envios, e-mail não configurado e entrega que falha viram texto', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [canal({ kind: 'email', hint: 'd***@exemplo.com', status: 'pending' })]
    await r.cliente.refetchQueries()
    const botao = await screen.findByRole('button', { name: 'Reenviar verificação do canal d***@exemplo.com' })
    const casos: [ErroForcado, string][] = [
      [{ status: 429, code: 'rate_limited' }, 'Limite de envios atingido (1 a cada 6 minutos): tente de novo em alguns minutos.'],
      [
        { status: 400, code: 'validation_failed', fields: [{ field: 'kind', reason: 'unavailable' }] },
        'O envio de e-mail não está configurado neste servidor.',
      ],
      [{ status: 502, code: 'delivery_failed' }, 'Não conseguimos entregar o e-mail de verificação. Tente de novo mais tarde.'],
    ]
    for (const [forcado, texto] of casos) {
      r.falsa.estado.forcar.set(RESEND, forcado)
      await r.usuario.click(botao)
      await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent(texto))
    }
    expect(screen.queryByText(/mensagem de teste/)).not.toBeInTheDocument()
  })

  it('reenviar de canal que já não está pendente, ou que foi apagado, recarrega a lista e explica', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [
      canal({ id: 'c1', kind: 'email', hint: 'a***@exemplo.com', status: 'pending' }),
      canal({ id: 'c2', kind: 'email', hint: 'b***@exemplo.com', status: 'pending' }),
    ]
    await r.cliente.refetchQueries()
    const primeiro = await screen.findByRole('button', { name: 'Reenviar verificação do canal a***@exemplo.com' })

    // Verificado em outra aba.
    r.falsa.estado.canais = [
      canal({ id: 'c1', kind: 'email', hint: 'a***@exemplo.com', status: 'verified' }),
      canal({ id: 'c2', kind: 'email', hint: 'b***@exemplo.com', status: 'pending' }),
    ]
    r.falsa.estado.forcar.set(RESEND, { status: 409, code: 'channel_not_pending' })
    await r.usuario.click(primeiro)
    expect(await screen.findByText('O canal não está mais aguardando verificação.')).toBeInTheDocument()
    expect(await screen.findByText('Verificado')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Reenviar verificação do canal a***@exemplo.com' })).not.toBeInTheDocument()

    // Apagado em outra aba.
    r.falsa.estado.canais = [canal({ id: 'c1', kind: 'email', hint: 'a***@exemplo.com', status: 'verified' })]
    r.falsa.estado.forcar.set(RESEND, { status: 404, code: 'not_found' })
    await r.usuario.click(screen.getByRole('button', { name: 'Reenviar verificação do canal b***@exemplo.com' }))
    expect(await screen.findByText('O canal já tinha sido apagado, talvez em outra aba.')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByText('b***@exemplo.com')).not.toBeInTheDocument())
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('apagar pelo Confirmar: um DELETE só, e o erro anterior some', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [canal({ kind: 'email', hint: 'd***@exemplo.com', status: 'pending' })]
    await r.cliente.refetchQueries()
    // Um erro de antes na tela.
    r.falsa.estado.forcar.set(RESEND, { status: 429, code: 'rate_limited' })
    await r.usuario.click(await screen.findByRole('button', { name: 'Reenviar verificação do canal d***@exemplo.com' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Limite de envios atingido')

    await r.usuario.click(screen.getByRole('button', { name: 'Apagar o canal d***@exemplo.com' }))
    const confirmar = await screen.findByRole('alertdialog')
    const acao = within(confirmar).getByRole('button', { name: 'Apagar canal' })
    fireEvent.click(acao)
    fireEvent.click(acao)
    expect((await screen.findAllByText('Canal apagado.'))[0]).toBeInTheDocument()
    expect(r.falsa.chamadas('DELETE /channels/:id')).toBe(1)
    expect(await screen.findByText(/Nenhum canal/)).toBeInTheDocument()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('apagar canal que outra aba já apagou recarrega a lista e explica', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [canal()]
    await r.cliente.refetchQueries()
    await r.usuario.click(await screen.findByRole('button', { name: 'Apagar o canal discord.com/…/abcd' }))
    r.falsa.estado.canais = []
    r.falsa.estado.forcar.set('DELETE /channels/:id', { status: 404, code: 'not_found' })
    await r.usuario.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Apagar canal' }))
    expect(await screen.findByText('O canal já tinha sido apagado, talvez em outra aba.')).toBeInTheDocument()
    expect(await screen.findByText(/Nenhum canal/)).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  // I1 da revisão final: o "Apagar" que abriu o diálogo sumiu com a linha;
  // o foco vai ao título da tela, e não ao <body>. Sabotagem: a de
  // dialogo.test.tsx (a lista só com o gatilho) → o foco cai no body.
  it('depois de apagar, o foco não cai no <body>', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [canal(), canal({ id: 'canal-2', hint: 'discord.com/…/efgh' })]
    await r.cliente.refetchQueries()
    await r.usuario.click(await screen.findByRole('button', { name: 'Apagar o canal discord.com/…/abcd' }))
    await r.usuario.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Apagar canal' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    await waitFor(() => expect(document.activeElement).not.toBe(document.body))
    expect(screen.getByRole('heading', { name: 'Canais de notificação', level: 1 })).toHaveFocus()
  })

  // Spec do Plano 9 §4.1: pausar descarta os avisos, então pede confirmação;
  // retomar não. Sabotagem: no onClick do botão, chamar alternar(c) direto
  // também para o canal ativo (pular o Confirmar) → o PATCH sai antes da
  // confirmação e o Cancelar não impede nada.
  it('pausar pede confirmação, e só o Confirmar manda enabled:false', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [canal()]
    await r.cliente.refetchQueries()
    await r.usuario.click(await screen.findByRole('button', { name: 'Pausar o canal discord.com/…/abcd' }))
    let confirmar = await screen.findByRole('alertdialog')
    expect(confirmar).toHaveTextContent(
      'Enquanto pausado, os avisos de incidentes deste canal são descartados, e não são reenviados ao retomar.',
    )
    // Cancelar não pausa.
    await r.usuario.click(within(confirmar).getByRole('button', { name: 'Cancelar' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(r.falsa.chamadas(PATCH)).toBe(0)

    await r.usuario.click(screen.getByRole('button', { name: 'Pausar o canal discord.com/…/abcd' }))
    confirmar = await screen.findByRole('alertdialog')
    await r.usuario.click(within(confirmar).getByRole('button', { name: 'Pausar canal' }))
    expect((await screen.findAllByText('Canal discord.com/…/abcd pausado.'))[0]).toBeInTheDocument()
    expect(r.falsa.corpos(PATCH)).toEqual([{ enabled: false }])
    // A linha nova aparece, e o estado de verificação continua.
    expect(await screen.findByText('Pausado')).toBeInTheDocument()
    expect(screen.getByText('Verificado')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retomar o canal discord.com/…/abcd' })).toBeInTheDocument()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  })

  // Sabotagem: fazer o Retomar também abrir o Confirmar → nenhum PATCH sai
  // no clique, e o teste espera o aviso em vão.
  it('retomar manda enabled:true sem confirmação, e a linha "Pausado" some', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [canal({ kind: 'email', hint: 'd***@exemplo.com', status: 'pending', enabled: false })]
    await r.cliente.refetchQueries()
    // Pausado e aguardando verificação: as duas linhas.
    expect(await screen.findByText('Pausado')).toBeInTheDocument()
    expect(screen.getByText('Aguardando verificação')).toBeInTheDocument()
    await r.usuario.click(screen.getByRole('button', { name: 'Retomar o canal d***@exemplo.com' }))
    expect((await screen.findAllByText('Canal d***@exemplo.com retomado.'))[0]).toBeInTheDocument()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(r.falsa.corpos(PATCH)).toEqual([{ enabled: true }])
    await waitFor(() => expect(screen.queryByText('Pausado')).not.toBeInTheDocument())
    expect(screen.getByRole('button', { name: 'Pausar o canal d***@exemplo.com' })).toBeInTheDocument()
  })

  // A linha "Pausado" é de enabled:false, e só dele. Sabotagem: mostrar a
  // linha para todo canal (tirar o "!c.enabled &&") → aparece duas vezes.
  it('só o canal pausado mostra a linha "Pausado"', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [canal(), canal({ id: 'c2', hint: 'discord.com/…/zzzz', enabled: false })]
    await r.cliente.refetchQueries()
    expect(await screen.findAllByText('Pausado')).toHaveLength(1)
    expect(screen.getByRole('button', { name: 'Pausar o canal discord.com/…/abcd' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retomar o canal discord.com/…/zzzz' })).toBeInTheDocument()
  })

  // Sabotagem: tirar a guarda de envio de alternar() ("if
  // (emVoo.current.has(c.id)) return") e o ocupado do Confirmar → dois PATCH.
  it('pausar: um PATCH só, mesmo com dois cliques no Confirmar', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [canal()]
    await r.cliente.refetchQueries()
    await r.usuario.click(await screen.findByRole('button', { name: 'Pausar o canal discord.com/…/abcd' }))
    const acao = within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Pausar canal' })
    fireEvent.click(acao)
    fireEvent.click(acao)
    expect((await screen.findAllByText('Canal discord.com/…/abcd pausado.'))[0]).toBeInTheDocument()
    expect(r.falsa.chamadas(PATCH)).toBe(1)
  })

  // Revisão final do Plano 9 (F4): o finally de alternar() fechava QUALQUER
  // Confirmar de pausa. Com o Retomar de um canal em voo, o Pausar aberto de
  // outro fechava sozinho quando o Retomar terminava. Sabotagem: voltar o
  // "setPausar((p) => (p?.id === c.id ? null : p))" para "setPausar(null)" →
  // o Confirmar do outro some.
  it('o Retomar em voo de um canal não fecha o Confirmar de pausa de outro', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [canal({ enabled: false }), canal({ id: 'c2', hint: 'discord.com/…/zzzz' })]
    await r.cliente.refetchQueries()
    let soltar!: () => void
    const aberto = new Promise<void>((res) => (soltar = res))
    let chegou = 0
    servidor.use(
      http.patch('*/api/v1/channels/:id', async () => {
        chegou++
        await aberto
      }),
    )
    await r.usuario.click(await screen.findByRole('button', { name: 'Retomar o canal discord.com/…/abcd' }))
    await waitFor(() => expect(chegou).toBe(1))
    await r.usuario.click(screen.getByRole('button', { name: 'Pausar o canal discord.com/…/zzzz' }))
    const confirmar = await screen.findByRole('alertdialog')
    // E4 do Plano 10: o Retomar do outro canal não ocupa este Confirmar.
    expect(within(confirmar).getByRole('button', { name: 'Pausar canal' })).toBeEnabled()
    soltar()
    expect((await screen.findAllByText('Canal discord.com/…/abcd retomado.'))[0]).toBeInTheDocument()
    // Um tique para o finally do Retomar rodar, se fosse fechar o diálogo.
    await new Promise((res) => setTimeout(res, 50))
    expect(screen.getByRole('alertdialog')).toBe(confirmar)
    // A perna positiva: o Confirmar que ficou pausa o canal certo.
    await waitFor(() => expect(within(confirmar).getByRole('button', { name: 'Pausar canal' })).toBeEnabled())
    await r.usuario.click(within(confirmar).getByRole('button', { name: 'Pausar canal' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(r.falsa.corpos(PATCH)).toEqual([{ enabled: true }, { enabled: false }])
    expect(r.falsa.estado.canais.find((c) => c.id === 'c2')?.enabled).toBe(false)
  })

  // E4 do Plano 10: a guarda do PATCH é por canal. Antes, com o Retomar de
  // um canal em voo, o Retomar de outro era descartado em silêncio. O aviso
  // diz qual canal mudou. Sabotagem: voltar a guarda para "algum canal em
  // voo" (emVoo.current.size > 0) → o 2º PATCH não sai.
  it('retomar um canal com o de outro em voo: os dois PATCH saem, e cada aviso diz o canal', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [canal({ enabled: false }), canal({ id: 'c2', hint: 'discord.com/…/zzzz', enabled: false })]
    await r.cliente.refetchQueries()
    let soltar!: () => void
    const aberto = new Promise<void>((res) => (soltar = res))
    let chegou = 0
    servidor.use(
      http.patch('*/api/v1/channels/:id', async () => {
        chegou++
        await aberto
      }),
    )
    await r.usuario.click(await screen.findByRole('button', { name: 'Retomar o canal discord.com/…/abcd' }))
    await waitFor(() => expect(chegou).toBe(1))
    const outro = screen.getByRole('button', { name: 'Retomar o canal discord.com/…/zzzz' })
    expect(outro).not.toHaveAttribute('aria-disabled')
    await r.usuario.click(outro)
    await waitFor(() => expect(chegou).toBe(2))
    soltar()
    expect((await screen.findAllByText('Canal discord.com/…/abcd retomado.'))[0]).toBeInTheDocument()
    expect((await screen.findAllByText('Canal discord.com/…/zzzz retomado.'))[0]).toBeInTheDocument()
    expect(r.falsa.estado.canais.map((c) => c.enabled)).toEqual([true, true])
    // Terminado, o botão do canal solta: a guarda é do PATCH em voo, não fica.
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Pausar o canal discord.com/…/abcd' })).not.toHaveAttribute('aria-disabled'),
    )
  })

  // O 404 é a lista velha (o canal foi apagado em outra aba), como no
  // reenviar e no apagar. Sabotagem: no catch de alternar(), trocar
  // "if (!(await listaVelha(e)))" por "if (true)" → o erro genérico, sem
  // recarregar.
  it('pausar ou retomar canal que outra aba apagou recarrega a lista e explica', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [canal({ enabled: false })]
    await r.cliente.refetchQueries()
    const retomar = await screen.findByRole('button', { name: 'Retomar o canal discord.com/…/abcd' })
    r.falsa.estado.canais = []
    await r.usuario.click(retomar)
    expect(await screen.findByText('O canal já tinha sido apagado, talvez em outra aba.')).toBeInTheDocument()
    expect(await screen.findByText(/Nenhum canal/)).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(r.falsa.chamadas(PATCH)).toBe(1)
  })

  // Outro erro fica na tela, e o canal não muda.
  it('pausar que falha mostra o erro e não muda o canal', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [canal()]
    await r.cliente.refetchQueries()
    r.falsa.estado.forcar.set(PATCH, { status: 429, code: 'rate_limited' })
    await r.usuario.click(await screen.findByRole('button', { name: 'Pausar o canal discord.com/…/abcd' }))
    await r.usuario.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Pausar canal' }))
    // O erro diz o canal (E4 do Plano 10): pode haver outro mudando junto.
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Não foi possível pausar o canal discord.com/…/abcd. Muitas requisições. Aguarde um minuto e tente de novo.',
    )
    expect(screen.queryByText('Pausado')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Pausar o canal discord.com/…/abcd' })).toBeInTheDocument()
  })

  // Auditoria C2 e S9: os estados usam ícones Lucide, com forma, cor e texto,
  // e não glifos Unicode de métrica desigual.
  it('cada estado é um selo com ícone Lucide e texto, sem glifo Unicode', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [
      canal(),
      canal({ id: 'c2', kind: 'email', hint: 'd***@exemplo.com', status: 'pending' }),
      canal({ id: 'c3', kind: 'email', hint: 'e***@exemplo.com', status: 'expired' }),
      canal({
        id: 'c4',
        hint: 'discord.com/…/zzzz',
        status: 'failing',
        last_error: 'invalid_channel',
        last_failure_at: '2026-09-24T11:00:00Z',
      }),
    ]
    await r.cliente.refetchQueries()
    const falhando = await screen.findByText('Falhando')
    for (const [texto, fundo] of [
      ['Verificado', 'bg-no-ar-100'],
      ['Aguardando verificação', 'bg-degradado-100'],
      ['Verificação expirada', 'bg-sem-dados-100'],
      ['Falhando', 'bg-fora-100'],
    ] as const) {
      const selo = screen.getByText(texto)
      expect(selo).toHaveClass('rounded-full', fundo)
      expect(selo.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
    }
    expect(falhando.closest('ul')?.textContent).not.toMatch(/[✓⧖✕⏸]/)
  })

  // Auditoria C3: num canal pausado, o que importa é que os avisos são
  // descartados. "Pausado" é o selo; a verificação vira texto apagado.
  it('canal pausado mostra "Pausado" em destaque e a verificação rebaixada', async () => {
    const r = await renderizarPainel('/channels')
    r.falsa.estado.canais = [canal({ enabled: false })]
    await r.cliente.refetchQueries()
    expect(await screen.findByText('Pausado')).toHaveClass('rounded-full', 'bg-degradado-100')
    expect(screen.getByText('Os avisos são descartados.')).toBeInTheDocument()
    const verificacao = screen.getByText('Verificado')
    expect(verificacao).not.toHaveClass('rounded-full')
    expect(verificacao).toHaveClass('text-suave')
  })
})


// Sabotagem: remover a guarda do onClick antes de setPausar. A lista
// atualiza durante o PATCH, mas o envio ainda está em voo.
it('Pausar não abre confirmação enquanto o Retomar do mesmo canal está em voo', async () => {
  const r = await renderizarPainel('/channels')
  r.falsa.estado.canais = [canal({ enabled: false })]
  await r.cliente.refetchQueries()
  let soltar!: () => void
  const pendente = new Promise<void>((resolve) => (soltar = resolve))
  let chegou = false
  servidor.use(http.patch('*/api/v1/channels/:id', async () => {
    chegou = true
    await pendente
  }))
  try {
    await r.usuario.click(await screen.findByRole('button', { name: 'Retomar o canal discord.com/…/abcd' }))
    await waitFor(() => expect(chegou).toBe(true))
    r.falsa.estado.canais = [canal({ enabled: true })]
    await r.cliente.refetchQueries({ queryKey: ['channels'] })
    const pausar = await screen.findByRole('button', { name: 'Pausar o canal discord.com/…/abcd' })
    expect(pausar).toHaveAttribute('aria-disabled', 'true')
    await r.usuario.click(pausar)
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  } finally {
    soltar()
  }
  await waitFor(() => expect(screen.getByRole('button', { name: 'Pausar o canal discord.com/…/abcd' })).not.toHaveAttribute('aria-disabled'))
  await r.usuario.click(screen.getByRole('button', { name: 'Pausar o canal discord.com/…/abcd' }))
  expect(await screen.findByRole('alertdialog')).toBeInTheDocument()
})

// Piso de rollback (SP2): uma versão posterior do servidor cria canais de
// tipos que esta não conhece. A lista não pode cair no parse por causa deles.
describe('canais: tipo desconhecido', () => {
  // Sabotagem: voltar o kind ao z.enum puro → a lista inteira vira erro; tirar
  // o kind da condição do Reenviar → o botão aparece na linha genérica.
  it('lista com tipo desconhecido mostra linha genérica', async () => {
    const r = await renderizarPainel('/channels')
    // As chaves a mais vêm da versão posterior; o parse as descarta.
    const webhook = {
      ...canal({ id: 'c2', kind: 'webhook', hint: 'Ops', status: 'pending' }),
      name: 'Ops',
      rev: 1,
      send_down: true,
      send_up: true,
    }
    r.falsa.estado.canais = [canal(), webhook]
    await r.cliente.refetchQueries()
    const outra = (await screen.findByText('Ops')).closest('li')
    const discord = screen.getByText('discord.com/…/abcd').closest('li')
    if (outra === null || discord === null) throw new Error('a linha do canal não está numa <li>')
    expect(discord).toHaveTextContent(/^Discord discord\.com\/…\/abcd/)
    expect(outra).toHaveTextContent(/^Outro tipo Ops/)
    // Reenviar a verificação só existe para e-mail; pausar e apagar não
    // dependem do tipo no servidor e continuam na linha.
    expect(within(outra).queryByRole('button', { name: /^Reenviar verificação/ })).not.toBeInTheDocument()
    expect(within(outra).getByRole('button', { name: 'Pausar o canal Ops' })).toBeInTheDocument()
    expect(within(outra).getByRole('button', { name: 'Apagar o canal Ops' })).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  // O rollback real: o servidor desta versão não sabe ler o destino de um tipo
  // que não conhece e devolve hint vazio. Sem um nome de apoio, dois canais
  // assim ficariam com o mesmo "Apagar o canal ", e daria para apagar o errado.
  // Sabotagem: voltar ao hint cru → nomes iguais e espaço duplo no diálogo.
  it('sem hint, cada canal de tipo desconhecido tem nome próprio, sem espaço duplo', async () => {
    const r = await renderizarPainel('/channels')
    const antes = '2026-09-20T10:00:00Z'
    const depois = '2026-09-21T15:30:00Z'
    const nomeAntes = `criado em ${dataHora(antes)}`
    const nomeDepois = `criado em ${dataHora(depois)}`
    r.falsa.estado.canais = [
      canal({ id: 'c1', kind: 'webhook', hint: '', created_at: antes }),
      canal({ id: 'c2', kind: 'webhook', hint: '', created_at: depois }),
    ]
    await r.cliente.refetchQueries()
    expect(await screen.findByText(nomeAntes)).toBeInTheDocument()
    expect(screen.getByText(nomeDepois)).toBeInTheDocument()
    const nomes = screen.getAllByRole('button', { name: /^(Pausar|Apagar) o canal/ }).map((b) => b.getAttribute('aria-label'))
    expect(nomes).toEqual([
      `Pausar o canal ${nomeAntes}`,
      `Apagar o canal ${nomeAntes}`,
      `Pausar o canal ${nomeDepois}`,
      `Apagar o canal ${nomeDepois}`,
    ])

    await r.usuario.click(screen.getByRole('button', { name: `Apagar o canal ${nomeDepois}` }))
    const confirmar = await screen.findByRole('alertdialog')
    expect(confirmar.textContent).toContain(`O canal ${nomeDepois} deixa de receber os alertas.`)
    expect(confirmar.textContent).not.toMatch(/ {2}/)
    await r.usuario.click(within(confirmar).getByRole('button', { name: 'Cancelar' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())

    await r.usuario.click(screen.getByRole('button', { name: `Pausar o canal ${nomeAntes}` }))
    await r.usuario.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Pausar canal' }))
    expect((await screen.findAllByText(`Canal ${nomeAntes} pausado.`))[0]).toBeInTheDocument()
  })

  // Tolerar tipo novo não afrouxa a forma: sem kind, ou kind que não é
  // texto, continua erro de contrato.
  it('parse de kind ausente continua falhando', () => {
    const { kind: _semKind, ...semKind } = canal()
    expect(Canal.safeParse(semKind).success).toBe(false)
    expect(Canal.safeParse({ ...canal(), kind: 3 }).success).toBe(false)
  })
})
