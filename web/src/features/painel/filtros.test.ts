import { describe, expect, it } from 'vitest'
import { Heartbeat, ItemDoResumo } from '@/lib/api/schemas'
import { heartbeat, itemDoResumo } from '@/test/fixtures'
import {
  casaComBusca,
  contarDoConjunto,
  contarTags,
  descreverFiltros,
  escreverFiltros,
  heartbeatNosFiltros,
  lerFiltros,
  SEM_FILTROS,
  temFiltro,
  temFiltroDeApi,
  tagsVisiveis,
} from './filtros'

const alvo = (p: Parameters<typeof itemDoResumo>[0] = {}) => ItemDoResumo.parse(itemDoResumo(p))
const hb = (p: Parameters<typeof heartbeat>[0] = {}) => Heartbeat.parse(heartbeat(p))

describe('heartbeatNosFiltros', () => {
  it('sem filtro, aparece', () => {
    expect(heartbeatNosFiltros(hb(), SEM_FILTROS)).toBe(true)
  })

  // Spec da tela, T4. Sabotagem: não olhar as tags.
  it('com filtro de tag, sai: heartbeat não tem tag', () => {
    expect(heartbeatNosFiltros(hb(), { ...SEM_FILTROS, tags: ['prod'] })).toBe(false)
  })

  // O botão "Fora do ar" agrupa por gravidade (T3).
  it.each([
    ['late', 'down', true],
    ['failing', 'down', true],
    ['up', 'down', false],
    ['waiting', 'unknown', true],
    ['paused', 'paused', true],
    ['paused', 'up', false],
  ] as const)('estado %s com o filtro %s → %s', (state, estado, casa) => {
    expect(heartbeatNosFiltros(hb({ state }), { ...SEM_FILTROS, estados: [estado] })).toBe(casa)
  })

  it('a busca olha o nome', () => {
    expect(heartbeatNosFiltros(hb({ name: 'Relatório' }), { ...SEM_FILTROS, busca: 'relatorio' })).toBe(true)
    expect(heartbeatNosFiltros(hb({ name: 'Relatório' }), { ...SEM_FILTROS, busca: 'backup' })).toBe(false)
  })
})

describe('contarDoConjunto', () => {
  // Sabotagem: contar só os alvos → o botão "Fora do ar" some com tarefa atrasada.
  it('soma alvos e heartbeats por estado exibido', () => {
    expect(contarDoConjunto([alvo(), alvo({ state: 'down' })], [hb({ state: 'late' }), hb({ state: 'failing' }), hb({ state: 'paused' })])).toEqual({
      up: 1,
      degraded: 0,
      down: 3,
      unknown: 0,
      pausado: 1,
    })
  })
})

describe('lerFiltros', () => {
  it('lê tag, estado e busca da URL', () => {
    expect(lerFiltros(new URLSearchParams('tag=prod&tag=api&estado=down&estado=paused&busca=site'))).toEqual({
      tags: ['prod', 'api'],
      estados: ['down', 'paused'],
      busca: 'site',
    })
  })

  // A URL é entrada externa: valor fora da regra some em vez de virar 400 na
  // cara de quem colou um link velho. Sabotagem: devolver o estado sem conferir.
  it('descarta o que a allowlist não conhece', () => {
    expect(lerFiltros(new URLSearchParams('tag=a%20b&tag=ok&estado=caido&estado=up&outro=1'))).toEqual({
      tags: ['ok'],
      estados: ['up'],
      busca: '',
    })
  })

  it('normaliza a tag como o servidor, sem repetir', () => {
    expect(lerFiltros(new URLSearchParams('tag=PROD&tag=prod')).tags).toEqual(['prod'])
  })

  // Os limites da API: nove tags dariam 400, e a tela inteira viraria erro.
  it('respeita os limites da API', () => {
    const muitas = Array.from({ length: 9 }, (_, i) => `tag=t${i}`).join('&')
    expect(lerFiltros(new URLSearchParams(muitas)).tags).toHaveLength(8)
    expect(lerFiltros(new URLSearchParams(`tag=${'a'.repeat(25)}`)).tags).toEqual([])
    expect(lerFiltros(new URLSearchParams(`busca=${'a'.repeat(101)}`)).busca).toHaveLength(100)
  })

  it('sem nada na URL, sem filtro', () => {
    expect(lerFiltros(new URLSearchParams())).toEqual(SEM_FILTROS)
  })
})

describe('escreverFiltros', () => {
  it('é o inverso de lerFiltros', () => {
    const f = { tags: ['prod', 'api'], estados: ['down' as const], busca: 'site' }
    expect(lerFiltros(escreverFiltros(f))).toEqual(f)
  })

  it('sem filtro, a URL fica limpa', () => {
    expect(escreverFiltros(SEM_FILTROS).toString()).toBe('')
  })
})

describe('temFiltro e temFiltroDeApi', () => {
  // A busca é local: sem isto, cada tecla viraria uma requisição.
  it('a busca é filtro, mas não vai à API', () => {
    const busca = { ...SEM_FILTROS, busca: 'a' }
    expect(temFiltro(busca)).toBe(true)
    expect(temFiltroDeApi(busca)).toBe(false)
    expect(temFiltroDeApi({ ...SEM_FILTROS, tags: ['a'] })).toBe(true)
    expect(temFiltroDeApi({ ...SEM_FILTROS, estados: ['up'] })).toBe(true)
    expect(temFiltro(SEM_FILTROS)).toBe(false)
  })
})

describe('casaComBusca', () => {
  it.each([
    ['', true],
    ['ban', true],
    ['BAN', true],
    ['  ban  ', true],
    ['produção', true],
    ['producao', true],
    ['xyz', false],
  ])('"%s" → %s', (busca, casa) => {
    expect(casaComBusca(alvo({ name: 'Banco', tags: ['produção'] }), busca)).toBe(casa)
  })
})

describe('contarTags', () => {
  // Sabotagem: ordenar só pelo nome.
  it('conta cada tag, da mais usada para a menos usada, e pelo nome no empate', () => {
    const itens = [alvo({ tags: ['b', 'a'] }), alvo({ tags: ['c', 'b'] })]
    expect(contarTags(itens)).toEqual([
      { tag: 'b', quantos: 2 },
      { tag: 'a', quantos: 1 },
      { tag: 'c', quantos: 1 },
    ])
  })

  it('sem tags, lista vazia', () => {
    expect(contarTags([alvo()])).toEqual([])
  })
})

describe('tagsVisiveis', () => {
  it.each([
    [[], [], []],
    [['a', 'b', 'c'], ['a', 'b', 'c'], []],
    [['a', 'b', 'c', 'd', 'e'], ['a', 'b', 'c'], ['d', 'e']],
    // D5 da revisão: o orçamento de caracteres mantém a célula em uma linha.
    [['oito-car', 'oito-car2', 'x'], ['oito-car', 'oito-car2', 'x'], []],
    [['oito-car', 'nove-cars', 'ab'], ['oito-car', 'nove-cars'], ['ab']],
    [['tag-longa-de-vinte-e-qua', 'outra'], ['tag-longa-de-vinte-e-qua'], ['outra']],
  ])('%j', (tags, visiveis, resto) => {
    expect(tagsVisiveis(tags)).toEqual({ visiveis, resto })
  })

  it('a primeira tag sempre aparece, por maior que seja', () => {
    expect(tagsVisiveis(['a'.repeat(24)]).visiveis).toHaveLength(1)
  })
})

describe('descreverFiltros', () => {
  it('diz o que está ativo, em português', () => {
    expect(descreverFiltros({ tags: ['prod', 'api'], estados: ['down', 'paused'], busca: 'site' })).toBe(
      'Tags prod e api · Fora do ar ou Pausado · Busca “site”',
    )
    expect(descreverFiltros({ ...SEM_FILTROS, tags: ['prod'] })).toBe('Tag prod')
  })
})
