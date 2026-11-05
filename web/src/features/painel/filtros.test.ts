import { describe, expect, it } from 'vitest'
import { ItemDoResumo } from '@/lib/api/schemas'
import { itemDoResumo } from '@/test/fixtures'
import {
  casaComBusca,
  contarTags,
  descreverFiltros,
  escreverFiltros,
  lerFiltros,
  SEM_FILTROS,
  temFiltro,
  temFiltroDeApi,
  tagsVisiveis,
} from './filtros'

const alvo = (p: Parameters<typeof itemDoResumo>[0] = {}) => ItemDoResumo.parse(itemDoResumo(p))

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
  ])('%j', (tags, visiveis, resto) => {
    expect(tagsVisiveis(tags)).toEqual({ visiveis, resto })
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
