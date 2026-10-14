import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { setupServer } from 'msw/node'
import { afterAll, afterEach, beforeAll } from 'vitest'

// Um servidor MSW para a suíte; cada teste instala a API falsa dele com
// usarApiFalsa(). Requisição sem handler é FALHA, não aviso: uma rota que o
// teste esqueceu de imitar não pode passar em silêncio.
export const servidor = setupServer()

beforeAll(() => servidor.listen({ onUnhandledFrame: 'error' }))
afterEach(() => {
  cleanup()
  servidor.resetHandlers()
  localStorage.clear()
})
afterAll(() => servidor.close())

// O jsdom não tem o que o Radix e o Recharts pedem ao navegador.
class ResizeObserverFalso {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= ResizeObserverFalso as unknown as typeof ResizeObserver
Element.prototype.scrollIntoView ??= function () {}
Element.prototype.hasPointerCapture ??= () => false
Element.prototype.releasePointerCapture ??= function () {}
// O jsdom não implementa isSecureContext; o navegador diz true em
// http://localhost, que é onde os testes "rodam". Os testes do aviso de
// contexto inseguro (login.test.tsx) trocam o valor e o devolvem.
Object.defineProperty(window, 'isSecureContext', { value: true, configurable: true })
window.matchMedia ??= (query: string) =>
  ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    dispatchEvent: () => false,
  }) as MediaQueryList
