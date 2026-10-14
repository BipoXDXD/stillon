import { describe, expect, it } from 'vitest'
import { Canal } from '@/lib/api/schemas'
import { canal } from '@/test/fixtures'
import { avisosDosCanais } from './avisos-dos-canais'

const c = (p: Parameters<typeof canal>[0] = {}) => Canal.parse(canal(p))
const com = (items: Canal[]) => ({ data: { items }, error: null })

const NENHUM_CANAL = 'Nenhum canal de notificação cadastrado. Os incidentes não enviam avisos.'
const NENHUM_VERIFICADO = 'Nenhum canal verificado está ativo para receber avisos.'

describe('avisosDosCanais', () => {
  it.each([
    ['carregando, nada', { data: undefined, error: null }, []],
    ['tudo certo, nada', com([c()]), []],
    ['nenhum canal', com([]), [NENHUM_CANAL]],
    [
      '1 pausado e nenhum verificado ativo',
      com([c({ enabled: false })]),
      ['1 canal pausado. Os avisos desse canal são descartados.', NENHUM_VERIFICADO],
    ],
    [
      'n pausados, com um verificado ativo',
      com([c({ id: 'a', enabled: false }), c({ id: 'b', enabled: false }), c({ id: 'c' })]),
      ['2 canais pausados. Os avisos desses canais são descartados.'],
    ],
    ['nenhum verificado (pendente)', com([c({ status: 'pending' })]), [NENHUM_VERIFICADO]],
    // Um canal falhando continua verificado e recebendo tentativas.
    ['1 falhando conta como verificado ativo', com([c({ status: 'failing' })]), ['1 canal está falhando na entrega.']],
    [
      'n falhando',
      com([c({ id: 'a', status: 'failing' }), c({ id: 'b', status: 'failing' })]),
      ['2 canais estão falhando na entrega.'],
    ],
    ['falhando e pausado não conta como falhando', com([c({ status: 'failing', enabled: false }), c({ id: 'b' })]), [
      '1 canal pausado. Os avisos desse canal são descartados.',
    ]],
    // Não saber não é "nenhum canal".
    [
      'erro na consulta',
      { data: { items: [] }, error: new Error('falhou') },
      ['Não foi possível conferir os canais de notificação agora.'],
    ],
  ] as const)('%s', (_, consulta, avisos) => {
    expect(avisosDosCanais(consulta)).toEqual(avisos)
  })
})
