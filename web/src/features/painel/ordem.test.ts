import { describe, expect, it } from 'vitest'
import { comOrdem, lerOrdem, ORDENS, ordenarLinhas } from './ordem'
import type { LinhaDeMonitor } from './linhas'

const comum = (nome: string, estado: LinhaDeMonitor['estado'], uptime24h: number | null) => ({
  nome,
  href: '/',
  estado,
  textoDoEstado: '',
  situacao: '',
  tags: [],
  uptime24h,
  historico: [],
  incidenteHref: null,
})

function http(nome: string, estado: LinhaDeMonitor['estado'], uptime24h: number | null, latenciaMs: number | null): LinhaDeMonitor {
  return { ...comum(nome, estado, uptime24h), tipo: 'http', chave: `http:${nome}`, latenciaMs }
}

function tarefa(nome: string, estado: LinhaDeMonitor['estado']): LinhaDeMonitor {
  return { ...comum(nome, estado, null), tipo: 'heartbeat', chave: `heartbeat:${nome}` }
}

const linhas = [
  http('Site', 'up', 0.9, 300),
  tarefa('Backup', 'down'),
  http('API', 'up', 0.99, 120),
  http('Banco', 'down', 0.5, null),
  http('Fila', 'degraded', 0.9, 2400),
]

const nomes = (l: readonly LinhaDeMonitor[]) => l.map((x) => x.nome)

describe('ordenarLinhas', () => {
  it('gravidade: os problemas primeiro, e o nome desempata', () => {
    expect(nomes(ordenarLinhas(linhas, 'gravidade'))).toEqual(['Backup', 'Banco', 'Fila', 'API', 'Site'])
  })

  it('nome: alfabética, com número em ordem de número', () => {
    const l = [http('Serviço 10', 'up', 1, 1), http('Serviço 2', 'down', 1, 1), http('API', 'up', 1, 1)]
    expect(nomes(ordenarLinhas(l, 'nome'))).toEqual(['API', 'Serviço 2', 'Serviço 10'])
  })

  // Sem uptime vai para o fim, e o empate em 0,9 fica com o mais grave.
  it('menor tempo no ar: do pior ao melhor, com quem não mede no fim', () => {
    expect(nomes(ordenarLinhas(linhas, 'menor-uptime'))).toEqual(['Banco', 'Fila', 'Site', 'API', 'Backup'])
  })

  // Sabotagem: tirar o sentido -1 → do menor ao maior.
  it('maior latência: da maior à menor, com quem não mede no fim', () => {
    expect(nomes(ordenarLinhas(linhas, 'maior-latencia'))).toEqual(['Fila', 'Site', 'API', 'Backup', 'Banco'])
  })

  it.each(ORDENS)('%s não esconde nem repete linha', (ordem) => {
    expect(nomes(ordenarLinhas(linhas, ordem)).sort()).toEqual(['API', 'Backup', 'Banco', 'Fila', 'Site'])
  })

  it('não mexe na lista recebida', () => {
    const antes = nomes(linhas)
    ordenarLinhas(linhas, 'nome')
    expect(nomes(linhas)).toEqual(antes)
  })
})

describe('ordem na URL', () => {
  it('lê a ordem conhecida', () => {
    expect(lerOrdem(new URLSearchParams('ordem=nome'))).toBe('nome')
  })

  it.each(['', 'ordem=', 'ordem=NOME', 'ordem=gravidade%27', 'ordem=__proto__'])('"%s" abre na ordem padrão', (q) => {
    expect(lerOrdem(new URLSearchParams(q))).toBe('gravidade')
  })

  it('a ordem padrão não vai à URL, e os filtros ficam', () => {
    const p = new URLSearchParams('tag=prod&ordem=nome')
    expect(comOrdem(p, 'gravidade').toString()).toBe('tag=prod')
    expect(comOrdem(p, 'maior-latencia').toString()).toBe('tag=prod&ordem=maior-latencia')
    expect(p.toString()).toBe('tag=prod&ordem=nome')
  })
})
