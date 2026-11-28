/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { z } from 'zod'
import { me, pagina } from '@/test/fixtures'
import { textoDoErroDeCanal } from './mensagens'
import { Canal, Convite, Me, Membro, PaginaDeStatus, Projeto, TiposConhecidosDeCanal } from './schemas'

// O JSON real de channelResponse, escrito e conferido pelo teste Go
// TestChannelResponseKeepsThePanelContract
// (internal/httpapi/channels_contrato_internal_test.go). Nenhuma resposta é
// escrita à mão aqui: se o DTO mudar, o Go exige regravar o arquivo, e este
// teste diz se o painel aceita a forma nova.
const ARQUIVO = resolve(import.meta.dirname, '../../../../internal/httpapi/testdata/contrato-canais.json')
const CASOS = z
  .array(z.strictObject({ caso: z.string().min(1), resposta: z.record(z.string(), z.unknown()) }))
  .parse(JSON.parse(readFileSync(ARQUIVO, 'utf8')))
const RESPOSTAS = CASOS.map((c) => [c.caso, c.resposta] as const)
// Confere o formato das datas gravadas pelo teste de contrato em Go.
const Rfc3339 = z.iso.datetime({ offset: true })
const DatasDaResposta = z.object({ created_at: Rfc3339, last_failure_at: Rfc3339.nullable() })
// O texto que textoDoErroDeCanal usa para o motivo que não conhece.
const SEM_TRADUCAO = 'Erro desconhecido'
const MOTIVOS = [...new Set(CASOS.map((c) => c.resposta.last_error))].filter(
  (motivo): motivo is string => typeof motivo === 'string' && motivo !== '',
)

describe('instantes das respostas da API', () => {
  it.each(['2026-11-17T06:10:07Z', '2026-11-17T03:10:07.123456789-03:00'])(
    'preserva o instante %s',
    (created_at) => {
      expect(Me.parse(me({ created_at })).created_at).toBe(created_at)
    },
  )

  it.each(['data inválida', '2026-02-31T06:10:07Z', '2026-11-17T06:10:07', '2026-11-17T06:10Z'])(
    'recusa o instante %s em created_at',
    (created_at) => {
      const resultado = Me.safeParse(me({ created_at }))
      expect(resultado.error?.issues.map(({ path }) => path)).toEqual([['created_at']])
    },
  )
})

describe('URL pública de página de status', () => {
  it.each([
    'https://status.exemplo.com/p/minha',
    'http://localhost:8080/p/minha',
    'http://127.0.0.1:8080/p/minha',
    'http://[::1]:8080/p/minha',
  ])('preserva a URL %s', (public_url) => {
    expect(PaginaDeStatus.parse(pagina({ public_url })).public_url).toBe(public_url)
  })

  it.each([
    '/p/minha',
    'mailto:status@exemplo.com',
    'https:/status.exemplo.com/p/minha',
    'https://',
    'https://usuario@status.exemplo.com/p/minha',
  ])('recusa a URL %s em public_url', (public_url) => {
    const resultado = PaginaDeStatus.safeParse(pagina({ public_url }))
    expect(resultado.error?.issues.map(({ path }) => path)).toEqual([['public_url']])
  })

  it.each([
    ['tabulação no caminho', 'https://status.exemplo.com/p/mi\tnha'],
    ['DEL na consulta', 'https://status.exemplo.com/p/minha?leitura=\u007f'],
  ])('recusa %s em public_url', (_caso, public_url) => {
    const resultado = PaginaDeStatus.safeParse(pagina({ public_url }))
    expect(resultado.error?.issues.map(({ path }) => path)).toEqual([['public_url']])
  })
})

describe('o schema Canal aceita o que a API devolve', () => {
  it.each(RESPOSTAS)('%s', (_caso, resposta) => {
    const lido = Canal.safeParse(resposta)
    expect(lido.success ? [] : lido.error.issues).toEqual([])
  })

  // z.object descarta chave desconhecida: sem isto, um campo novo no DTO
  // passaria com o artefato regravado, sem ninguém decidir o que fazer com ele.
  it.each(RESPOSTAS)('%s: traz exatamente os campos do schema', (_caso, resposta) => {
    expect(Object.keys(resposta).sort()).toEqual(Object.keys(Canal.shape).sort())
  })

  it.each(RESPOSTAS)('%s: as datas vêm em RFC 3339', (_caso, resposta) => {
    const lidas = DatasDaResposta.safeParse(resposta)
    expect(lidas.success ? [] : lidas.error.issues).toEqual([])
  })

  it.each(MOTIVOS.filter((motivo) => motivo !== 'unknown'))('o motivo %s tem tradução própria', (motivo) => {
    expect(textoDoErroDeCanal(motivo)).not.toBe(SEM_TRADUCAO)
  })

  it('o motivo unknown chega da API e vira o texto genérico', () => {
    expect(MOTIVOS).toContain('unknown')
    expect(textoDoErroDeCanal('unknown')).toBe(SEM_TRADUCAO)
  })

  it('os casos cobrem todo kind, todo status e os dois lados de cada campo que varia', () => {
    const canais = CASOS.map((c) => Canal.parse(c.resposta))
    const valores = <T>(de: (c: Canal) => T) => new Set(canais.map(de))
    const ambos = new Set([true, false])

    // Só os tipos conhecidos: 'desconhecido' é do painel, e o Go nunca o envia.
    expect(valores((c) => c.kind)).toEqual(new Set(TiposConhecidosDeCanal.options))
    expect(valores((c) => c.status)).toEqual(new Set(Canal.shape.status.options))
    expect(valores((c) => c.enabled)).toEqual(ambos)
    expect(valores((c) => c.last_error === null)).toEqual(ambos)
    expect(valores((c) => c.last_failure_at === null)).toEqual(ambos)
  })
})

// Lê um contrato gravado pelo Go em internal/httpapi/testdata.
function casosDoContrato(arquivo: string) {
  return z
    .array(z.strictObject({ caso: z.string().min(1), resposta: z.record(z.string(), z.unknown()) }))
    .parse(JSON.parse(readFileSync(resolve(import.meta.dirname, '../../../../internal/httpapi/testdata', arquivo), 'utf8')))
}

// O JSON real de projectResponse, escrito e conferido pelo teste Go
// TestProjectResponseKeepsThePanelContract
// (internal/httpapi/projetos_contrato_internal_test.go).
const CASOS_DE_PROJETOS = casosDoContrato('contrato-projetos.json')
const RESPOSTAS_DE_PROJETOS = CASOS_DE_PROJETOS.map((c) => [c.caso, c.resposta] as const)

describe('o schema Projeto aceita o que a API devolve', () => {
  it.each(RESPOSTAS_DE_PROJETOS)('%s', (_caso, resposta) => {
    const lido = Projeto.safeParse(resposta)
    expect(lido.success ? [] : lido.error.issues).toEqual([])
  })

  it.each(RESPOSTAS_DE_PROJETOS)('%s: traz exatamente os campos do schema', (_caso, resposta) => {
    expect(Object.keys(resposta).sort()).toEqual(Object.keys(Projeto.shape).sort())
  })

  it.each(RESPOSTAS_DE_PROJETOS)('%s: a data vem em RFC 3339', (_caso, resposta) => {
    const lida = z.object({ created_at: Rfc3339 }).safeParse(resposta)
    expect(lida.success ? [] : lida.error.issues).toEqual([])
  })

  it('os casos cobrem o projeto padrão, um criado pela API e os dois lados de pending_titular', () => {
    const projetos = CASOS_DE_PROJETOS.map((c) => Projeto.parse(c.resposta))
    expect(new Set(projetos.map((p) => p.is_default))).toEqual(new Set([true, false]))
    expect(new Set(projetos.map((p) => p.pending_titular))).toEqual(new Set([true, false]))
  })
})

// Os JSON reais de memberResponse e invitationResponse, escritos e conferidos
// pelos testes Go TestMemberResponseKeepsThePanelContract e
// TestInvitationResponseKeepsThePanelContract.
const CASOS_DE_MEMBROS = casosDoContrato('contrato-membros.json')
const RESPOSTAS_DE_MEMBROS = CASOS_DE_MEMBROS.map((c) => [c.caso, c.resposta] as const)
const CASOS_DE_CONVITES = casosDoContrato('contrato-convites.json')
const RESPOSTAS_DE_CONVITES = CASOS_DE_CONVITES.map((c) => [c.caso, c.resposta] as const)

describe('o schema Membro aceita o que a API devolve', () => {
  it.each(RESPOSTAS_DE_MEMBROS)('%s', (_caso, resposta) => {
    const lido = Membro.safeParse(resposta)
    expect(lido.success ? [] : lido.error.issues).toEqual([])
  })

  it.each(RESPOSTAS_DE_MEMBROS)('%s: traz exatamente os campos do schema', (_caso, resposta) => {
    expect(Object.keys(resposta).sort()).toEqual(Object.keys(Membro.shape).sort())
  })

  it.each(RESPOSTAS_DE_MEMBROS)('%s: a data vem em RFC 3339', (_caso, resposta) => {
    const lida = z.object({ joined_at: Rfc3339 }).safeParse(resposta)
    expect(lida.success ? [] : lida.error.issues).toEqual([])
  })

  it('os casos cobrem os dois lados de is_titular e de pending_titular', () => {
    const membros = CASOS_DE_MEMBROS.map((c) => Membro.parse(c.resposta))
    expect(new Set(membros.map((m) => m.is_titular))).toEqual(new Set([true, false]))
    expect(new Set(membros.map((m) => m.pending_titular))).toEqual(new Set([true, false]))
  })
})

describe('o schema Convite aceita o que a API devolve', () => {
  it('há casos gravados', () => {
    expect(RESPOSTAS_DE_CONVITES.length).toBeGreaterThan(0)
  })

  it.each(RESPOSTAS_DE_CONVITES)('%s', (_caso, resposta) => {
    const lido = Convite.safeParse(resposta)
    expect(lido.success ? [] : lido.error.issues).toEqual([])
  })

  it.each(RESPOSTAS_DE_CONVITES)('%s: traz exatamente os campos do schema', (_caso, resposta) => {
    expect(Object.keys(resposta).sort()).toEqual(Object.keys(Convite.shape).sort())
  })

  it.each(RESPOSTAS_DE_CONVITES)('%s: as datas vêm em RFC 3339', (_caso, resposta) => {
    const lidas = z.object({ created_at: Rfc3339, expires_at: Rfc3339 }).safeParse(resposta)
    expect(lidas.success ? [] : lidas.error.issues).toEqual([])
  })
})
