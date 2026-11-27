import { QueryClient } from '@tanstack/react-query'
import { expect, it } from 'vitest'
import { chaves } from './consultas'

// As chaves de recurso são do projeto; a conferência vale dentro de um.
const doP = chaves.doProjeto('p')

// Páginas de tamanhos diferentes não podem dividir uma entrada do cache: a
// de 100 do contador da navegação e uma de 20 mostrariam uma a outra.
// Sabotagem prevista: tirar o limite de doP.incidentes.
it('o limite faz parte da chave dos incidentes', () => {
  expect(doP.incidentes('open', undefined, 20)).not.toEqual(doP.incidentes('open', undefined, 100))
  expect(doP.incidentes('open', 'alvo-1', 20)).not.toEqual(doP.incidentes('open', 'alvo-1'))
  // Tudo continua sob os incidentes do projeto, que o reconhecer invalida.
  expect(doP.incidentes('open', undefined, 100).slice(0, 3)).toEqual(doP.todosOsIncidentes)
})

// Uma chave de cada família no cache, para ver o que um prefixo alcança.
function cacheComUmaDeCada() {
  const qc = new QueryClient()
  const entradas = {
    resumo: doP.resumo,
    resumoFiltrado: doP.resumoFiltrado({ tags: [], estados: [] }),
    alvos: doP.alvos,
    alvo: doP.alvo('a'),
    outroAlvo: doP.alvo('b'),
    serie: doP.serie('a', '24h'),
    incidentes: doP.incidentes('open'),
    heartbeats: doP.heartbeats,
    heartbeat: doP.heartbeat('h'),
    canais: doP.canais,
    paginas: doP.paginas,
    sessoes: chaves.sessoes,
  }
  for (const chave of Object.values(entradas)) qc.setQueryData(chave, 1)
  const invalidada = (nome: keyof typeof entradas) => qc.getQueryState(entradas[nome])?.isInvalidated === true
  const existe = (nome: keyof typeof entradas) => qc.getQueryState(entradas[nome]) !== undefined
  return { qc, nomes: Object.keys(entradas) as (keyof typeof entradas)[], invalidada, existe }
}

// Quem invalida o prefixo tem de alcançar tudo da família, e nada de fora:
// uma chave trocada não dá erro, só deixa a tela velha. Sabotagem prevista:
// trocar o valor de todosOsAlvos para [...p, 'target'].
it('todosOsAlvos alcança as cinco chaves de alvos e nenhuma de fora', async () => {
  const c = cacheComUmaDeCada()
  await c.qc.invalidateQueries({ queryKey: doP.todosOsAlvos, refetchType: 'none' })
  const alcancadas = c.nomes.filter(c.invalidada)
  expect(alcancadas).toEqual(['resumo', 'resumoFiltrado', 'alvos', 'alvo', 'outroAlvo', 'serie'])
})

it('todosOsIncidentes alcança só os incidentes', async () => {
  const c = cacheComUmaDeCada()
  await c.qc.invalidateQueries({ queryKey: doP.todosOsIncidentes, refetchType: 'none' })
  expect(c.nomes.filter(c.invalidada)).toEqual(['incidentes'])
})

// Apagar o alvo tira a entrada dele e a série, mas o resumo é de todos.
it('remover doP.alvo(id) leva o alvo e a série, e deixa o resumo e os outros alvos', () => {
  const c = cacheComUmaDeCada()
  c.qc.removeQueries({ queryKey: doP.alvo('a') })
  expect(c.nomes.filter((n) => !c.existe(n))).toEqual(['alvo', 'serie'])
})

// Os valores combinados com o resto do painel, escritos à mão.
it('os prefixos têm o valor que o cache usa', () => {
  expect(doP.todosOsAlvos).toEqual(['projeto', 'p', 'targets'])
  expect(doP.todosOsIncidentes).toEqual(['projeto', 'p', 'incidents'])
})
