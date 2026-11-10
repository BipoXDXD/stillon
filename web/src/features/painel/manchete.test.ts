import { describe, expect, it } from 'vitest'
import { Heartbeat, Incidente, ItemDoResumo } from '@/lib/api/schemas'
import { heartbeat, incidente, itemDoResumo } from '@/test/fixtures'
import {
  detalheDaManchete,
  fraseDoUltimoResolvido,
  manchete,
  mancheteSemRede,
  metaDoPainel,
  resumoEmTexto,
  textoDaManchete,
  ultimoResolvido,
  type Manchete,
} from './manchete'

const alvo = (p: Parameters<typeof itemDoResumo>[0] = {}) => ItemDoResumo.parse(itemDoResumo(p))
const inc = (p: Parameters<typeof incidente>[0] = {}) => Incidente.parse(incidente(p))
const hb = (p: Parameters<typeof heartbeat>[0] = {}) => Heartbeat.parse(heartbeat(p))
const semTarefas = { itens: [], confirmadas: true }
const tarefas = (...itens: Heartbeat[]) => ({ itens, confirmadas: true })

describe('mancheteSemRede', () => {
  // Sem rede não se sabe o estado atual: nenhuma frase pode afirmá-lo.
  // Sabotagem: devolver a manchete recebida.
  it.each<Manchete>([
    { tipo: 'tudo-no-ar' },
    { tipo: 'sem-alvos' },
    { tipo: 'todos-pausados' },
    { tipo: 'tarefas-sem-confirmacao' },
    { tipo: 'aguardando', quantos: 1, tarefas: 0 },
  ])('troca $tipo pelo aviso de queda de rede', (m) => {
    expect(mancheteSemRede(m)).toEqual({ tipo: 'sem-rede' })
  })

  // O problema já conhecido continua à vista, como em tarefas-sem-confirmacao.
  // Sabotagem: devolver sempre sem-rede.
  it('mantém o problema já conhecido', () => {
    const problemas: Manchete = { tipo: 'problemas', quantos: 1, tarefas: 0 }
    expect(mancheteSemRede(problemas)).toEqual(problemas)
  })

  it('o texto diz que não dá para conferir', () => {
    expect(textoDaManchete({ tipo: 'sem-rede' })).toBe('Sem conexão para conferir os monitores.')
  })
})

describe('manchete', () => {
  it('sem alvos', () => {
    expect(manchete([], semTarefas)).toEqual({ tipo: 'sem-alvos' })
  })

  it('fora do ar e degradado contam como problema; pausado e sem dados, não', () => {
    const itens = [alvo({ state: 'down' }), alvo({ state: 'degraded' }), alvo({ state: 'down', enabled: false }), alvo({ state: 'unknown' }), alvo()]
    expect(manchete(itens, semTarefas)).toEqual({ tipo: 'problemas', quantos: 2, tarefas: 0 })
  })

  it('sem problema, um ativo ainda sem dados segura o "Tudo no ar."', () => {
    expect(manchete([alvo(), alvo({ state: 'unknown' })], semTarefas)).toEqual({ tipo: 'aguardando', quantos: 1, tarefas: 0 })
  })

  // Review Focus 2.
  it('todos pausados', () => {
    expect(manchete([alvo({ enabled: false }), alvo({ state: 'unknown', enabled: false })], semTarefas)).toEqual({ tipo: 'todos-pausados' })
  })

  it('tudo no ar: todo ativo no ar, com os pausados à parte', () => {
    expect(manchete([alvo(), alvo({ enabled: false })], semTarefas)).toEqual({ tipo: 'tudo-no-ar' })
  })

  // Spec da tela, A manchete: ela não pode ficar verde com heartbeat fora.
  // Sabotagem: contar só os alvos em problemas → "tudo-no-ar".
  it.each(['late', 'failing'] as const)('só um heartbeat %s, com os alvos no ar, é problema', (state) => {
    expect(manchete([alvo()], tarefas(hb({ state })))).toEqual({ tipo: 'problemas', quantos: 0, tarefas: 1 })
  })

  it('alvo e heartbeat com problema somam', () => {
    expect(manchete([alvo({ state: 'down' })], tarefas(hb({ state: 'late' }), hb({ state: 'up' })))).toEqual({
      tipo: 'problemas',
      quantos: 1,
      tarefas: 1,
    })
  })

  // Pausar não encerra o incidente (D13): ele segue aberto até a retomada, e o
  // verde esconderia isso. É a mesma regra da seção de /incidents (T13).
  it('heartbeat pausado com incidente aberto pede atenção, com os alvos no ar', () => {
    expect(manchete([alvo()], tarefas(hb({ state: 'paused', open_incident_id: 'i1' })))).toEqual({
      tipo: 'problemas',
      quantos: 0,
      tarefas: 1,
    })
  })

  it('heartbeat pausado sem incidente não pede atenção', () => {
    expect(manchete([alvo()], tarefas(hb({ state: 'paused' })))).toEqual({ tipo: 'tudo-no-ar' })
  })

  it('pausado com incidente aparece na linha de detalhe', () => {
    const lista = tarefas(hb({ state: 'paused', open_incident_id: 'i1' }))
    expect(textoDaManchete(manchete([alvo()], lista))).toBe('Uma tarefa pede atenção.')
    expect(detalheDaManchete([alvo()], lista)).toBe('1 no ar. Tarefas: 1 pausada.')
  })

  it('o alvo pausado com incidente segue fora da conta', () => {
    const pausado = alvo({ state: 'down', enabled: false, open_incident_id: 'i1' })
    expect(manchete([pausado, alvo()], semTarefas)).toEqual({ tipo: 'tudo-no-ar' })
  })

  it('alvo fora do ar e tarefa pausada com incidente somam', () => {
    const fora = alvo({ state: 'down', open_incident_id: 'i1' })
    const lista = tarefas(hb({ state: 'paused', open_incident_id: 'i2' }))
    expect(manchete([fora], lista)).toEqual({ tipo: 'problemas', quantos: 1, tarefas: 1 })
    expect(textoDaManchete(manchete([fora], lista))).toBe('Um serviço e uma tarefa pedem atenção.')
    expect(detalheDaManchete([fora], lista)).toBe('1 fora do ar. Tarefas: 1 pausada.')
  })

  it('heartbeat aguardando o primeiro sinal segura o "Tudo no ar."', () => {
    expect(manchete([alvo()], tarefas(hb({ state: 'waiting' })))).toEqual({ tipo: 'aguardando', quantos: 0, tarefas: 1 })
  })

  it('só heartbeats, todos em dia: o painel não diz que está vazio', () => {
    expect(manchete([], tarefas(hb()))).toEqual({ tipo: 'tudo-no-ar' })
  })

  it('tudo pausado inclui as tarefas', () => {
    expect(manchete([alvo({ enabled: false })], tarefas(hb({ state: 'up' })))).toEqual({ tipo: 'tudo-no-ar' })
    expect(manchete([alvo({ enabled: false })], tarefas(hb({ state: 'paused' })))).toEqual({ tipo: 'todos-pausados' })
  })

  // Não saber não é "tudo no ar". Sabotagem: ignorar `confirmadas`.
  it('consulta de heartbeats sem resposta nunca vira "Tudo no ar."', () => {
    expect(manchete([alvo()], { itens: [], confirmadas: false })).toEqual({ tipo: 'tarefas-sem-confirmacao' })
    expect(manchete([], { itens: [], confirmadas: false })).toEqual({ tipo: 'tarefas-sem-confirmacao' })
  })

  it('um poll que falhou não apaga o problema já conhecido', () => {
    expect(manchete([alvo()], { itens: [hb({ state: 'late' })], confirmadas: false })).toEqual({ tipo: 'problemas', quantos: 0, tarefas: 1 })
    expect(manchete([alvo({ state: 'down' })], { itens: [], confirmadas: false })).toEqual({ tipo: 'problemas', quantos: 1, tarefas: 0 })
  })
})

describe('textoDaManchete', () => {
  it.each([
    [{ tipo: 'sem-alvos' }, 'Nenhum serviço vigiado ainda.'],
    [{ tipo: 'problemas', quantos: 1, tarefas: 0 }, 'Um serviço pede atenção.'],
    [{ tipo: 'problemas', quantos: 2, tarefas: 0 }, 'Dois serviços pedem atenção.'],
    [{ tipo: 'problemas', quantos: 10, tarefas: 0 }, 'Dez serviços pedem atenção.'],
    [{ tipo: 'problemas', quantos: 11, tarefas: 0 }, '11 serviços pedem atenção.'],
    [{ tipo: 'problemas', quantos: 0, tarefas: 1 }, 'Uma tarefa pede atenção.'],
    [{ tipo: 'problemas', quantos: 0, tarefas: 2 }, 'Duas tarefas pedem atenção.'],
    [{ tipo: 'problemas', quantos: 0, tarefas: 3 }, 'Três tarefas pedem atenção.'],
    [{ tipo: 'problemas', quantos: 1, tarefas: 1 }, 'Um serviço e uma tarefa pedem atenção.'],
    [{ tipo: 'problemas', quantos: 2, tarefas: 11 }, 'Dois serviços e 11 tarefas pedem atenção.'],
    [{ tipo: 'aguardando', quantos: 1, tarefas: 0 }, 'Um serviço ainda sem dados.'],
    [{ tipo: 'aguardando', quantos: 3, tarefas: 0 }, 'Três serviços ainda sem dados.'],
    [{ tipo: 'aguardando', quantos: 0, tarefas: 1 }, 'Uma tarefa ainda sem execução concluída.'],
    [{ tipo: 'aguardando', quantos: 1, tarefas: 2 }, 'Um serviço ainda sem dados e duas tarefas sem execução concluída.'],
    [{ tipo: 'tarefas-sem-confirmacao' }, 'Não foi possível conferir as tarefas.'],
    [{ tipo: 'todos-pausados' }, 'Todos os monitores estão pausados.'],
    [{ tipo: 'tudo-no-ar' }, 'Tudo no ar.'],
  ] satisfies [Manchete, string][])('%j → "%s"', (m, texto) => {
    expect(textoDaManchete(m)).toBe(texto)
  })
})

describe('resumoEmTexto', () => {
  it('conta por gravidade, com plural, e omite o estado que não aparece', () => {
    const itens = [alvo(), alvo({ state: 'degraded' }), alvo({ state: 'degraded' }), alvo({ state: 'down' }), alvo({ state: 'unknown' }), alvo({ enabled: false })]
    expect(resumoEmTexto(itens)).toBe('1 fora do ar, 2 degradados, 1 no ar, 1 sem dados, 1 pausado')
    expect(resumoEmTexto([alvo(), alvo()])).toBe('2 no ar')
  })
})

describe('detalheDaManchete', () => {
  it('os alvos, depois as tarefas, por gravidade e com plural', () => {
    const lista = [hb({ state: 'up' }), hb({ state: 'late' }), hb({ state: 'late' }), hb({ state: 'failing' }), hb({ state: 'paused' })]
    expect(detalheDaManchete([alvo(), alvo({ state: 'down' })], tarefas(...lista))).toBe('1 fora do ar, 1 no ar. Tarefas: 1 com falha, 2 atrasadas, 1 em dia, 1 pausada.')
  })

  it('sem tarefas, a frase de sempre; só tarefas, sem a parte dos alvos', () => {
    expect(detalheDaManchete([alvo()], semTarefas)).toBe('1 no ar.')
    expect(detalheDaManchete([], tarefas(hb({ state: 'waiting' })))).toBe('Tarefas: 1 aguardando.')
  })

  // Sabotagem: contar as tarefas sem olhar `confirmadas`.
  it('sem a resposta das tarefas, não conta com dado velho', () => {
    expect(detalheDaManchete([alvo()], { itens: [hb()], confirmadas: false })).toBe('1 no ar.')
  })

  // Sabotagem: devolver a junção sem a guarda → um parágrafo só com ".".
  it('sem nada a dizer, não há linha', () => {
    expect(detalheDaManchete([], { itens: [], confirmadas: false })).toBeNull()
    expect(detalheDaManchete([], { itens: [hb()], confirmadas: false })).toBeNull()
  })
})

describe('metaDoPainel', () => {
  const as = new Date(2026, 9, 14, 14, 32)

  it('conta os ativos no ar sobre o total de ativos', () => {
    expect(metaDoPainel([alvo(), alvo({ state: 'down' }), alvo({ enabled: false })], semTarefas, as)).toBe('Atualizado às 14:32 · 1 de 2 no ar')
  })

  it('sem ativo, não inventa um total', () => {
    expect(metaDoPainel([alvo({ enabled: false })], semTarefas, as)).toBe('Atualizado às 14:32 · nenhum alvo ativo')
  })

  it('as tarefas ativas entram depois dos alvos', () => {
    expect(metaDoPainel([alvo()], tarefas(hb(), hb({ state: 'late' }), hb({ state: 'paused', enabled: false })), as)).toBe(
      'Atualizado às 14:32 · 1 de 1 no ar · 1 de 2 tarefas em dia',
    )
  })

  it('só tarefas: nada de "nenhum alvo ativo"', () => {
    expect(metaDoPainel([], tarefas(hb()), as)).toBe('Atualizado às 14:32 · 1 de 1 tarefa em dia')
  })

  // Sabotagem: contar as tarefas sem olhar `confirmadas` → "2 de 2 tarefas em dia".
  it('sem a resposta das tarefas, nada de "em dia"', () => {
    expect(metaDoPainel([alvo()], { itens: [hb(), hb()], confirmadas: false }, as)).toBe('Atualizado às 14:32 · 1 de 1 no ar')
  })
})

describe('último incidente resolvido', () => {
  // A rota ordena pela abertura: o último a terminar pode vir depois na lista.
  it('é o de resolved_at maior, não o primeiro da lista', () => {
    const cedo = inc({ id: 'i2', started_at: '2026-09-24T09:00:00Z', resolved_at: '2026-09-24T09:05:00Z' })
    const tarde = inc({ id: 'i1', started_at: '2026-09-24T08:00:00Z', resolved_at: '2026-09-24T11:00:00Z' })
    expect(ultimoResolvido([cedo, tarde])?.id).toBe('i1')
  })

  it('ignora o aberto; sem resolvido, null', () => {
    expect(ultimoResolvido([inc({ resolved_at: null })])).toBeNull()
    expect(ultimoResolvido([])).toBeNull()
  })

  it('a frase: sem resposta, nada; lista vazia, "nenhum"; senão, a data do fim', () => {
    expect(fraseDoUltimoResolvido(undefined, false)).toBeNull()
    expect(fraseDoUltimoResolvido([], false)).toBe('Nenhum incidente registrado.')
    expect(fraseDoUltimoResolvido([inc({ resolved_at: new Date(2026, 8, 24, 9, 12).toISOString() })], false)).toMatch(
      /^Último incidente resolvido em 24\/09\/2026,? 09:12\.$/,
    )
  })

  // A consulta só vê os alvos. Sabotagem: ignorar `comTarefas`.
  it('com tarefas cadastradas, a frase diz que fala dos alvos', () => {
    expect(fraseDoUltimoResolvido([], true)).toBe('Nenhum incidente de alvo registrado.')
    expect(fraseDoUltimoResolvido([inc({ resolved_at: new Date(2026, 8, 24, 9, 12).toISOString() })], true)).toMatch(/^Último incidente de alvo resolvido em /)
  })
})
