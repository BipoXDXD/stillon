import { QueryClient } from '@tanstack/react-query'
import { expect, it } from 'vitest'
import { chaves } from './consultas'

// Páginas de tamanhos diferentes não podem dividir uma entrada do cache: a
// de 100 do contador da navegação e uma de 20 mostrariam uma a outra.
// Sabotagem prevista: tirar o limite de chaves.incidentes.
it('o limite faz parte da chave dos incidentes', () => {
  expect(chaves.incidentes('open', undefined, 20)).not.toEqual(chaves.incidentes('open', undefined, 100))
  expect(chaves.incidentes('open', 'alvo-1', 20)).not.toEqual(chaves.incidentes('open', 'alvo-1'))
  // Tudo continua sob ['incidents'], que o reconhecer invalida.
  expect(chaves.incidentes('open', undefined, 100)[0]).toBe('incidents')
})

// Uma chave de cada família no cache, para ver o que um prefixo alcança.
function cacheComUmaDeCada() {
  const qc = new QueryClient()
  const entradas = {
    resumo: chaves.resumo,
    resumoFiltrado: chaves.resumoFiltrado({ tags: [], estados: [] }),
    alvos: chaves.alvos,
    alvo: chaves.alvo('a'),
    outroAlvo: chaves.alvo('b'),
    serie: chaves.serie('a', '24h'),
    incidentes: chaves.incidentes('open'),
    heartbeats: chaves.heartbeats,
    heartbeat: chaves.heartbeat('h'),
    canais: chaves.canais,
    paginas: chaves.paginas,
    sessoes: chaves.sessoes,
  }
  for (const chave of Object.values(entradas)) qc.setQueryData(chave, 1)
  const invalidada = (nome: keyof typeof entradas) => qc.getQueryState(entradas[nome])?.isInvalidated === true
  const existe = (nome: keyof typeof entradas) => qc.getQueryState(entradas[nome]) !== undefined
  return { qc, nomes: Object.keys(entradas) as (keyof typeof entradas)[], invalidada, existe }
}

// Quem invalida o prefixo tem de alcançar tudo da família, e nada de fora:
// uma chave trocada não dá erro, só deixa a tela velha. Sabotagem prevista:
// trocar o valor de chaves.todosOsAlvos para ['target'].
it('todosOsAlvos alcança as cinco chaves de alvos e nenhuma de fora', async () => {
  const c = cacheComUmaDeCada()
  await c.qc.invalidateQueries({ queryKey: chaves.todosOsAlvos, refetchType: 'none' })
  const alcancadas = c.nomes.filter(c.invalidada)
  expect(alcancadas).toEqual(['resumo', 'resumoFiltrado', 'alvos', 'alvo', 'outroAlvo', 'serie'])
})

it('todosOsIncidentes alcança só os incidentes', async () => {
  const c = cacheComUmaDeCada()
  await c.qc.invalidateQueries({ queryKey: chaves.todosOsIncidentes, refetchType: 'none' })
  expect(c.nomes.filter(c.invalidada)).toEqual(['incidentes'])
})

// Apagar o alvo tira a entrada dele e a série, mas o resumo é de todos.
it('remover chaves.alvo(id) leva o alvo e a série, e deixa o resumo e os outros alvos', () => {
  const c = cacheComUmaDeCada()
  c.qc.removeQueries({ queryKey: chaves.alvo('a') })
  expect(c.nomes.filter((n) => !c.existe(n))).toEqual(['alvo', 'serie'])
})

// Os valores combinados com o resto do painel, escritos à mão.
it('os prefixos têm o valor que o cache usa', () => {
  expect(chaves.todosOsAlvos).toEqual(['targets'])
  expect(chaves.todosOsIncidentes).toEqual(['incidents'])
})
