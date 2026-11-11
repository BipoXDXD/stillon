import { describe, expect, it } from 'vitest'
import { Heartbeat, ItemDoResumo } from '@/lib/api/schemas'
import { heartbeat, itemDoResumo } from '@/test/fixtures'
import { SEM_FILTROS } from './filtros'
import { buscaDaLista, linhasDaLista, vizinhosNaLista } from './lista'

const agora = new Date('2026-09-24T12:00:00Z')
const alvo = (p: Parameters<typeof itemDoResumo>[0]) => ItemDoResumo.parse(itemDoResumo(p))
const tarefa = (p: Parameters<typeof heartbeat>[0]) => Heartbeat.parse(heartbeat(p))
const nomes = (linhas: { nome: string }[]) => linhas.map((l) => l.nome)

describe('linhasDaLista', () => {
  const dados = {
    itens: [alvo({ id: 's', name: 'Site' }), alvo({ id: 'b', name: 'Banco', state: 'down' })],
    heartbeats: [tarefa({ id: 'h', name: 'Backup' })],
  }

  it('junta alvos e heartbeats na ordem por gravidade', () => {
    expect(nomes(linhasDaLista(dados, SEM_FILTROS, 'gravidade', agora))).toEqual(['Banco', 'Backup', 'Site'])
  })

  // Sabotagem: ignorar a ordem pedida.
  it('segue a ordem pedida', () => {
    expect(nomes(linhasDaLista(dados, SEM_FILTROS, 'nome', agora))).toEqual(['Backup', 'Banco', 'Site'])
  })

  // Sabotagem: não aplicar a busca.
  it('aplica a busca aos alvos e aos heartbeats', () => {
    expect(nomes(linhasDaLista(dados, { ...SEM_FILTROS, busca: 'ba' }, 'nome', agora))).toEqual(['Backup', 'Banco'])
  })
})

describe('vizinhosNaLista', () => {
  const linhas = linhasDaLista(
    { itens: [alvo({ id: 'a', name: 'A' }), alvo({ id: 'b', name: 'B' }), alvo({ id: 'c', name: 'C' })], heartbeats: [] },
    SEM_FILTROS,
    'nome',
    agora,
  )

  it('no meio, os dois vizinhos', () => {
    const v = vizinhosNaLista(linhas, 'http:b')
    expect([v?.anterior?.nome, v?.proxima?.nome]).toEqual(['A', 'C'])
  })

  // Sabotagem: dar a volta na lista (o último de anterior do primeiro).
  it('no primeiro, sem anterior; no último, sem próximo', () => {
    expect(vizinhosNaLista(linhas, 'http:a')?.anterior).toBeNull()
    expect(vizinhosNaLista(linhas, 'http:c')?.proxima).toBeNull()
  })

  it('fora da lista, nenhum vizinho', () => {
    expect(vizinhosNaLista(linhas, 'http:z')).toBeNull()
  })

  it('sozinho na lista, os dois lados vazios', () => {
    expect(vizinhosNaLista(linhas.slice(0, 1), 'http:a')).toEqual({ anterior: null, proxima: null })
  })
})

describe('buscaDaLista', () => {
  // Sabotagem: devolver a URL inteira.
  it('guarda filtros e ordem, e deixa o resto de fora', () => {
    expect(buscaDaLista(new URLSearchParams('janela=7d&tag=prod&ordem=nome&busca=api'))).toBe('tag=prod&busca=api&ordem=nome')
  })

  it('sem lista na URL, nada', () => {
    expect(buscaDaLista(new URLSearchParams('janela=7d&ordem=gravidade'))).toBe('')
  })
})
