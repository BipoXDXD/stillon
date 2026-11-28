import { act, render, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { SITE_KEY_DO_TURNSTILE, TOKEN_DO_TURNSTILE } from '@/test/fixtures'
import { esquecerOScriptDoTurnstile, Turnstile, URL_DO_TURNSTILE, type OpcoesDoTurnstile, type ResultadoDoTurnstile } from './Turnstile'

// Um window.turnstile de mentira: guarda as opções de cada render, e o teste
// faz o papel da Cloudflare chamando os callbacks.
function turnstileFalso() {
  const widgets: OpcoesDoTurnstile[] = []
  const falso = {
    render: vi.fn<(alvo: HTMLElement, o: OpcoesDoTurnstile) => string>((_alvo, o) => {
      widgets.push(o)
      return `w${widgets.length}`
    }),
    reset: vi.fn<(id: string) => void>(),
    remove: vi.fn<(id: string) => void>(),
  }
  return { falso, widgets }
}

function ultimo(widgets: OpcoesDoTurnstile[]): OpcoesDoTurnstile {
  const w = widgets.at(-1)
  if (!w) throw new Error('nenhum widget desenhado')
  return w
}

const props = { siteKey: SITE_KEY_DO_TURNSTILE, acao: 'signup', tema: 'auto', reiniciar: 0, falhou: false } as const

beforeEach(() => esquecerOScriptDoTurnstile())
afterEach(() => {
  delete window.turnstile
  for (const s of document.querySelectorAll('script')) s.remove()
})

describe('Turnstile', () => {
  // Sabotagem prevista: trocar o "carga ??=" do carregador por "carga =",
  // e cada widget põe um <script> seu na página.
  it('carrega o script uma vez para dois widgets', async () => {
    const { falso, widgets } = turnstileFalso()
    render(
      <>
        <Turnstile {...props} aoMudar={() => {}} />
        <Turnstile {...props} acao="forgot" aoMudar={() => {}} />
      </>,
    )
    const scripts = document.querySelectorAll<HTMLScriptElement>('script')
    expect(scripts).toHaveLength(1)
    expect(scripts[0]?.src).toBe(URL_DO_TURNSTILE)
    expect(URL_DO_TURNSTILE).toBe('https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit')
    expect(falso.render).not.toHaveBeenCalled()
    // O api.js "chega": define window.turnstile e dispara o load.
    window.turnstile = falso
    act(() => {
      scripts[0]?.dispatchEvent(new Event('load'))
    })
    await waitFor(() => expect(falso.render).toHaveBeenCalledTimes(2))
    expect(widgets.map((w) => w.action).sort()).toEqual(['forgot', 'signup'])
    expect(widgets[0]).toMatchObject({ sitekey: SITE_KEY_DO_TURNSTILE, theme: 'auto', language: 'pt-br' })
  })

  // Sabotagem prevista: não sinalizar a expiração do token.
  it('expirado devolve o desafio à espera', async () => {
    const { falso, widgets } = turnstileFalso()
    window.turnstile = falso
    const aoMudar = vi.fn<(resultado: ResultadoDoTurnstile) => void>()
    render(<Turnstile {...props} aoMudar={aoMudar} />)
    await waitFor(() => expect(falso.render).toHaveBeenCalledTimes(1))
    act(() => ultimo(widgets).callback(TOKEN_DO_TURNSTILE))
    expect(aoMudar).toHaveBeenLastCalledWith({ fase: 'resolvido', token: TOKEN_DO_TURNSTILE })
    act(() => ultimo(widgets)['expired-callback']())
    expect(aoMudar).toHaveBeenLastCalledWith({ fase: 'aguardando' })
  })

  // Sabotagem prevista: não sinalizar a falha depois de um token resolvido.
  it('erro do widget sinaliza que o desafio falhou', async () => {
    const { falso, widgets } = turnstileFalso()
    window.turnstile = falso
    const aoMudar = vi.fn<(resultado: ResultadoDoTurnstile) => void>()
    render(<Turnstile {...props} aoMudar={aoMudar} />)
    await waitFor(() => expect(falso.render).toHaveBeenCalledTimes(1))
    act(() => ultimo(widgets).callback(TOKEN_DO_TURNSTILE))
    act(() => {
      ultimo(widgets)['error-callback']()
    })
    expect(aoMudar).toHaveBeenLastCalledWith({ fase: 'falhou' })
  })

  // Escolha 3 do dono: o widget segue o tema do painel e é recriado quando
  // ele muda. Sabotagem prevista: tirar o tema das dependências do efeito.
  it('trocar o tema recria o widget no tema novo', async () => {
    const { falso, widgets } = turnstileFalso()
    window.turnstile = falso
    const aoMudar = vi.fn<(resultado: ResultadoDoTurnstile) => void>()
    const r = render(<Turnstile {...props} tema="light" aoMudar={aoMudar} />)
    await waitFor(() => expect(falso.render).toHaveBeenCalledTimes(1))
    act(() => ultimo(widgets).callback(TOKEN_DO_TURNSTILE))
    r.rerender(<Turnstile {...props} tema="dark" aoMudar={aoMudar} />)
    await waitFor(() => expect(falso.render).toHaveBeenCalledTimes(2))
    expect(falso.remove).toHaveBeenCalledWith('w1')
    expect(ultimo(widgets).theme).toBe('dark')
    // O token do widget antigo morreu com ele.
    expect(aoMudar).toHaveBeenLastCalledWith({ fase: 'aguardando' })
  })

  // Sabotagem prevista: tirar o reset do efeito do reiniciar.
  it('reiniciar reseta o widget, e desmontar o remove', async () => {
    const { falso } = turnstileFalso()
    window.turnstile = falso
    const r = render(<Turnstile {...props} aoMudar={() => {}} />)
    await waitFor(() => expect(falso.render).toHaveBeenCalledTimes(1))
    expect(falso.reset).not.toHaveBeenCalled()
    r.rerender(<Turnstile {...props} reiniciar={1} aoMudar={() => {}} />)
    expect(falso.reset).toHaveBeenCalledWith('w1')
    r.unmount()
    expect(falso.remove).toHaveBeenCalledWith('w1')
  })
})
