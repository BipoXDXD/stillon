import type { createMemoryRouter } from 'react-router'

// A interação com o botão pode terminar antes do import lazy e da navegação.
// O sinal do teste encerra a espera sem um segundo prazo nem polling.
export function esperarNavegacao(
  router: ReturnType<typeof createMemoryRouter>,
  pathname: string,
  signal: AbortSignal,
): Promise<void> {
  const pronta = () => router.state.location.pathname === pathname && router.state.navigation.state === 'idle'
  if (signal.aborted) return Promise.reject(signal.reason)
  if (pronta()) return Promise.resolve()
  return new Promise<void>((resolve, reject) => {
    let encerrada = false
    const limpar = () => {
      parar()
      signal.removeEventListener('abort', abortar)
    }
    const abortar = () => {
      if (encerrada) return
      encerrada = true
      limpar()
      reject(signal.reason)
    }
    const verificar = () => {
      if (encerrada || !pronta()) return
      encerrada = true
      limpar()
      resolve()
    }
    const parar = router.subscribe(verificar)
    signal.addEventListener('abort', abortar)
    // Fecha o intervalo entre a leitura inicial e a instalação dos ouvintes.
    if (signal.aborted) abortar()
    verificar()
  })
}
