import type { Page, Route } from '@playwright/test'

// Uma ficha por segundo é o contrato público de RateLimitUser(60, 20).
// Reservar os turnos antes de aguardar evita que GETs paralelos formem um lote.
// Auth/refresh seguem nativos; nenhuma resposta, mutação ou falha é repetida.
export async function cadenciarRecursos(page: Page, prazo: number) {
  const padrao = /\/api\/v1\/(?!auth(?:\/|$))/
  let proximo = 0
  let fila = Promise.resolve()
  let encerrada = false
  const esperas = new Map<ReturnType<typeof setTimeout>, () => void>()
  const pendentes = new Set<Promise<void>>()
  const erros: unknown[] = []

  async function executar(route: Route) {
    const agora = performance.now()
    const turno = Math.max(agora, proximo)
    proximo = turno + 1_000
    if (!encerrada && turno >= prazo) {
      await route.abort('timedout')
      throw new Error('cadência do E2E: recurso não cabe no prazo original do teste')
    }
    while (performance.now() < turno) {
      if (encerrada) break
      await new Promise<void>((ok) => {
        const timer = setTimeout(() => {
          esperas.delete(timer)
          ok()
        }, Math.ceil(turno - performance.now()))
        esperas.set(timer, ok)
      })
    }
    if (!encerrada && performance.now() >= prazo) {
      await route.abort('timedout')
      throw new Error('cadência do E2E: prazo original do teste esgotado')
    }
    try {
      if (encerrada) await route.abort('aborted')
      else await route.continue()
    } catch (erro) {
      // F5/navegação pode cancelar uma requisição durante seu turno. Somente
      // o cancelamento confirmado pelo navegador é esperado; o resto falha.
      if (!route.request().failure()) throw erro
    } finally {
      // Um timer atrasado não libera os turnos seguintes em lote.
      proximo = Math.max(proximo, performance.now() + 1_000)
    }
  }

  async function encaminhar(route: Route) {
    // A cadeia reserva a ordem antes de qualquer await e despacha um recurso
    // por vez, inclusive se o event loop atrasar um timer. Erros são guardados
    // abaixo e tornam o teste vermelho ao encerrar; não viram novas tentativas.
    const trabalho = fila.then(() => executar(route))
    fila = trabalho.then(() => {}, () => {})
    pendentes.add(trabalho)
    try { await trabalho } catch (erro) { erros.push(erro) }
    finally { pendentes.delete(trabalho) }
  }
  await page.route(padrao, encaminhar)
  return async () => {
    encerrada = true
    for (const [timer, ok] of esperas) {
      clearTimeout(timer)
      ok()
    }
    esperas.clear()
    // Remover a rota antes de concluir os handlers faria o navegador tratar
    // pedidos ainda enfileirados; primeiro conclui os abortos, sem timers.
    await Promise.allSettled(pendentes)
    await page.unroute(padrao, encaminhar)
    if (erros.length) throw erros[0]
  }
}
