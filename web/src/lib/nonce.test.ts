import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

describe('nonce da resposta', () => {
  let nonceAnterior: string | undefined

  beforeEach(async () => {
    vi.resetModules()
    const { getNonce } = await import('get-nonce')
    nonceAnterior = getNonce()
  })

  afterEach(async () => {
    document.head.querySelectorAll('meta[property="csp-nonce"], meta[name="csp-nonce"]').forEach((m) => m.remove())
    const { setNonce } = await import('get-nonce')
    setNonce(nonceAnterior ?? '')
    vi.restoreAllMocks()
  })

  // Trocar .nonce por getAttribute('nonce') perde o valor que o navegador
  // esconde, e o estilo injetado pelo diálogo seria bloqueado pela CSP.
  it('propaga a propriedade nonce mesmo quando o atributo está escondido', async () => {
    const meta = document.createElement('meta')
    meta.setAttribute('property', 'csp-nonce')
    meta.nonce = 'nonce-da-resposta-123'
    document.head.append(meta)
    const lerAtributo = meta.getAttribute.bind(meta)
    vi.spyOn(meta, 'getAttribute').mockImplementation((nome) => nome === 'nonce' ? '' : lerAtributo(nome))

    const { nonce } = await import('./nonce')
    const { getNonce } = await import('get-nonce')

    expect(nonce).toBe('nonce-da-resposta-123')
    expect(getNonce()).toBe('nonce-da-resposta-123')
  })

  // Um fallback inventado ou selecionar meta[name] em vez de meta[property]
  // substituiria a configuração legítima usada por quem injeta estilos.
  it('sem a metatag do servidor, não inventa nem substitui o nonce de runtime', async () => {
    const { getNonce, setNonce } = await import('get-nonce')
    setNonce('nonce-ja-configurado')
    const outra = document.createElement('meta')
    outra.name = 'csp-nonce'
    outra.nonce = 'nonce-de-outra-metatag'
    document.head.append(outra)

    const { nonce } = await import('./nonce')

    expect(nonce).toBeUndefined()
    expect(getNonce()).toBe('nonce-ja-configurado')
  })

  it('uma metatag vazia não apaga o nonce já configurado', async () => {
    const { getNonce, setNonce } = await import('get-nonce')
    setNonce('nonce-ja-configurado')
    const meta = document.createElement('meta')
    meta.setAttribute('property', 'csp-nonce')
    document.head.append(meta)

    const { nonce } = await import('./nonce')

    expect(nonce).toBeUndefined()
    expect(getNonce()).toBe('nonce-ja-configurado')
  })
})
