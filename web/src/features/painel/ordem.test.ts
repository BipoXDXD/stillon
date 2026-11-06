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
  http('Site', 'up', 0.9, 3000),
  tarefa('Backup', 'down'),
  http('API', 'up', 0.99, 120),
  http('Banco', 'down', 0.5, null),
  http('Fila', 'degraded', 0.9, 2400),
  http('Novo', 'unknown', null, null),
  tarefa('Relatório', 'up'),
]

const nomes = (l: readonly LinhaDeMonitor[]) => l.map((x) => x.nome)

describe('ordenarLinhas', () => {
  it('gravidade: os problemas primeiro, e o nome desempata', () => {
    expect(nomes(ordenarLinhas(linhas, 'gravidade'))).toEqual(['Backup', 'Banco', 'Fila', 'API', 'Relatório', 'Site', 'Novo'])
  })

  it('nome: alfabética, com número em ordem de número', () => {
    const l = [http('Serviço 10', 'up', 1, 1), http('Serviço 2', 'down', 1, 1), http('API', 'up', 1, 1)]
    expect(nomes(ordenarLinhas(l, 'nome'))).toEqual(['API', 'Serviço 2', 'Serviço 10'])
  })

  // A tarefa atrasada não tem uptime e vem na frente; o empate em 0,9 fica com o
  // mais grave; quem não mede e está bem vai para o fim.
  it('menor tempo no ar: problema sem medida, do pior ao melhor, e o resto no fim', () => {
    expect(nomes(ordenarLinhas(linhas, 'menor-uptime'))).toEqual(['Backup', 'Banco', 'Fila', 'Site', 'API', 'Relatório', 'Novo'])
  })

  // A API devolve latência nula quando o último check foi down: Banco não pode
  // ir para o fim por estar fora do ar. Sabotagem: tirar o sentido -1.
  it('maior latência: problema sem medida, da maior à menor, e o resto no fim', () => {
    expect(nomes(ordenarLinhas(linhas, 'maior-latencia'))).toEqual(['Backup', 'Banco', 'Site', 'Fila', 'API', 'Relatório', 'Novo'])
  })

  it('degradado sem medida também vem na frente', () => {
    const l = [http('Lento', 'up', 1, 900), http('Site', 'degraded', 1, null), http('Novo', 'unknown', null, null)]
    expect(nomes(ordenarLinhas(l, 'maior-latencia'))).toEqual(['Site', 'Lento', 'Novo'])
  })

  it.each(ORDENS)('%s não esconde nem repete linha', (ordem) => {
    expect(nomes(ordenarLinhas(linhas, ordem)).sort()).toEqual(['API', 'Backup', 'Banco', 'Fila', 'Novo', 'Relatório', 'Site'])
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
