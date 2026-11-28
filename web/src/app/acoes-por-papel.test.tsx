import { screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { PROJETO_PADRAO, type ApiFalsa } from '@/test/api-falsa'
import { alvo, canal, eventoDeHeartbeat, heartbeat, incidente, incidenteDeHeartbeat, itemDoResumo, pagina } from '@/test/fixtures'
import { renderizarPainel } from '@/test/renderizar'
// Pré-carrega as rotas lazy e a folha de estilos na coleta, que não tem prazo.
import '@/features/alvos/PaginaDetalhe'
import '@/test/aquecer-estilos'

// O painel esconde as ações que o papel não pode (permissoes.ts, a matriz de
// tenancy.Role.Can). Quem decide é o servidor; aqui, só o que aparece.
// Sabotagem prevista em cada tela: trocar o usePode(...) dela por true.

const P = `/projects/${PROJETO_PADRAO}`

function comPapel(papel: string, preparar?: (f: ApiFalsa) => void) {
  return (f: ApiFalsa) => {
    f.definirPapel(PROJETO_PADRAO, papel)
    preparar?.(f)
  }
}

const botao = (nome: string | RegExp) => screen.queryByRole('button', { name: nome })
const link = (nome: string | RegExp) => screen.queryByRole('link', { name: nome })

describe('painel', () => {
  it('viewer não vê os atalhos de cadastrar alvo e heartbeat', async () => {
    await renderizarPainel(`${P}/`, { antes: comPapel('viewer') })
    await screen.findByRole('heading', { level: 1, name: 'Painel' })
    expect(await screen.findByRole('heading', { level: 2 })).toBeInTheDocument()
    expect(link('Cadastrar heartbeat')).not.toBeInTheDocument()
    expect(link('Cadastrar alvo')).not.toBeInTheDocument()
    expect(link('Cadastrar o primeiro alvo')).not.toBeInTheDocument()
  })

  it('member vê os atalhos', async () => {
    await renderizarPainel(`${P}/`, { antes: comPapel('member') })
    expect(await screen.findByRole('link', { name: 'Cadastrar heartbeat' })).toBeInTheDocument()
    expect(link('Cadastrar alvo')).toBeInTheDocument()
    expect(await screen.findByRole('link', { name: 'Cadastrar o primeiro alvo' })).toBeInTheDocument()
  })
})

describe('detalhe do alvo', () => {
  const comAlvo = (f: ApiFalsa) => {
    f.estado.alvos = [alvo()]
  }

  it('viewer não vê Editar, Pausar e Apagar', async () => {
    await renderizarPainel(`${P}/targets/alvo-1`, { antes: comPapel('viewer', comAlvo) })
    await screen.findByRole('heading', { level: 1, name: 'API' })
    expect(link('Editar')).not.toBeInTheDocument()
    expect(botao('Pausar')).not.toBeInTheDocument()
    expect(botao('Apagar')).not.toBeInTheDocument()
  })

  it('member vê Editar, Pausar e Apagar', async () => {
    await renderizarPainel(`${P}/targets/alvo-1`, { antes: comPapel('member', comAlvo) })
    await screen.findByRole('heading', { level: 1, name: 'API' })
    expect(link('Editar')).toBeInTheDocument()
    expect(botao('Pausar')).toBeInTheDocument()
    expect(botao('Apagar')).toBeInTheDocument()
  })
})

describe('Reconhecer', () => {
  const foraDoAr = (f: ApiFalsa) => {
    f.estado.alvos = [alvo()]
    f.estado.resumo = [itemDoResumo({ state: 'down', open_incident_id: 'inc-1' })]
    f.estado.incidentes = [incidente({ cause: 'conn_refused' })]
  }

  it.each([
    ['viewer', false],
    ['member', true],
  ])('na faixa do alvo, %s: aparece = %s', async (papel, aparece) => {
    await renderizarPainel(`${P}/targets/alvo-1`, { antes: comPapel(papel, foraDoAr) })
    const faixa = await screen.findByRole('region', { name: /^Fora do ar há / })
    expect(within(faixa).queryByRole('button', { name: 'Reconhecer' }) !== null).toBe(aparece)
    expect(within(faixa).queryByText(/Reconhecer interrompe os lembretes/) !== null).toBe(aparece)
  })

  it.each([
    ['viewer', false],
    ['member', true],
  ])('nos incidentes, %s: aparece = %s', async (papel, aparece) => {
    await renderizarPainel(`${P}/incidents`, {
      antes: comPapel(papel, (f) => {
        f.estado.incidentes = [incidente()]
      }),
    })
    await screen.findByText(/Tempo esgotado/)
    expect(botao('Reconhecer o incidente de API') !== null).toBe(aparece)
    expect(screen.queryByText(/Reconhecer interrompe os lembretes/) !== null).toBe(aparece)
  })

  it.each([
    ['viewer', false],
    ['member', true],
  ])('nos incidentes do heartbeat, %s: aparece = %s', async (papel, aparece) => {
    await renderizarPainel(`${P}/heartbeats/h1`, {
      antes: comPapel(papel, (f) => {
        f.estado.heartbeats = [heartbeat({ id: 'h1', name: 'Backup' })]
        f.estado.incidentesDeHeartbeat = [incidenteDeHeartbeat({ heartbeat_id: 'h1' })]
      }),
    })
    const secao = await screen.findByRole('region', { name: 'Incidentes' })
    await within(secao).findByRole('list')
    expect(within(secao).queryByRole('button', { name: /^Reconhecer o incidente de / }) !== null).toBe(aparece)
    expect(within(secao).queryByText(/Reconhecer interrompe os lembretes/) !== null).toBe(aparece)
  })
})

describe('detalhe do heartbeat', () => {
  const comHeartbeat = (f: ApiFalsa) => {
    f.estado.heartbeats = [heartbeat({ id: 'h1', name: 'Backup' })]
    f.estado.eventos = [eventoDeHeartbeat()]
  }

  it('viewer não vê Editar, Duplicar, Pausar, Sinalizar agora, Apagar, limpar nem gerar URL', async () => {
    await renderizarPainel(`${P}/heartbeats/h1`, { antes: comPapel('viewer', comHeartbeat) })
    await screen.findByRole('heading', { level: 1, name: 'Backup' })
    // O log de eventos chegou: o Limpar dependeria dele.
    await within(await screen.findByRole('region', { name: 'Log de eventos' })).findByRole('list')
    expect(link('Editar')).not.toBeInTheDocument()
    expect(botao('Duplicar')).not.toBeInTheDocument()
    expect(botao('Pausar')).not.toBeInTheDocument()
    expect(botao('Sinalizar agora')).not.toBeInTheDocument()
    expect(botao('Apagar')).not.toBeInTheDocument()
    expect(botao('Limpar eventos')).not.toBeInTheDocument()
    expect(botao('Gerar nova URL')).not.toBeInTheDocument()
  })

  it('member vê todas as ações', async () => {
    await renderizarPainel(`${P}/heartbeats/h1`, { antes: comPapel('member', comHeartbeat) })
    await screen.findByRole('heading', { level: 1, name: 'Backup' })
    expect(link('Editar')).toBeInTheDocument()
    expect(botao('Duplicar')).toBeInTheDocument()
    expect(botao('Pausar')).toBeInTheDocument()
    expect(botao('Sinalizar agora')).toBeInTheDocument()
    expect(botao('Apagar')).toBeInTheDocument()
    expect(await screen.findByRole('button', { name: 'Limpar eventos' })).toBeInTheDocument()
    expect(botao('Gerar nova URL')).toBeInTheDocument()
  })
})

describe('canais', () => {
  const comCanal = (f: ApiFalsa) => {
    f.estado.canais = [canal({ status: 'pending' })]
  }

  it('member vê a lista sem Novo canal, Reenviar, Pausar e Apagar', async () => {
    await renderizarPainel(`${P}/channels`, { antes: comPapel('member', comCanal) })
    expect(await screen.findByText('discord.com/…/abcd')).toBeInTheDocument()
    expect(botao('Novo canal')).not.toBeInTheDocument()
    expect(botao(/^Reenviar verificação/)).not.toBeInTheDocument()
    expect(botao(/^Pausar o canal/)).not.toBeInTheDocument()
    expect(botao(/^Apagar o canal/)).not.toBeInTheDocument()
  })

  it('member com a lista vazia não vê Novo canal', async () => {
    await renderizarPainel(`${P}/channels`, { antes: comPapel('member') })
    expect(await screen.findByText(/^Nenhum canal\./)).toBeInTheDocument()
    expect(botao('Novo canal')).not.toBeInTheDocument()
  })

  it('manager vê as ações', async () => {
    await renderizarPainel(`${P}/channels`, { antes: comPapel('manager', comCanal) })
    expect(await screen.findByText('discord.com/…/abcd')).toBeInTheDocument()
    expect(botao('Novo canal')).toBeInTheDocument()
    expect(botao(/^Reenviar verificação/)).toBeInTheDocument()
    expect(botao(/^Pausar o canal/)).toBeInTheDocument()
    expect(botao(/^Apagar o canal/)).toBeInTheDocument()
  })
})

describe('status page', () => {
  const comPagina = (f: ApiFalsa) => {
    f.estado.alvos = [alvo()]
    f.estado.paginas = [pagina()]
  }

  it('member vê os campos desabilitados, sem Salvar, e a prévia', async () => {
    await renderizarPainel(`${P}/status-pages`, { antes: comPapel('member', comPagina) })
    expect(await screen.findByRole('textbox', { name: 'Título' })).toBeDisabled()
    expect(screen.getByRole('textbox', { name: 'Endereço (slug)' })).toBeDisabled()
    expect(screen.getByRole('textbox', { name: 'Nome de exibição de API' })).toBeDisabled()
    expect(botao('Salvar')).not.toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'Prévia aproximada' })).toBeInTheDocument()
  })

  it('member sem página não vê Criar status page', async () => {
    await renderizarPainel(`${P}/status-pages`, { antes: comPapel('member') })
    expect(await screen.findByRole('textbox', { name: 'Título' })).toBeDisabled()
    expect(botao('Criar status page')).not.toBeInTheDocument()
  })

  it('manager vê os campos habilitados e Salvar', async () => {
    await renderizarPainel(`${P}/status-pages`, { antes: comPapel('manager', comPagina) })
    expect(await screen.findByRole('textbox', { name: 'Título' })).toBeEnabled()
    expect(screen.getByRole('textbox', { name: 'Nome de exibição de API' })).toBeEnabled()
    expect(botao('Salvar')).toBeInTheDocument()
  })
})

describe('rota direta de cadastro sem edit_monitors', () => {
  it.each([
    ['/targets/new', 'POST /projects/:projectId/targets'],
    ['/heartbeats/new', 'POST /projects/:projectId/heartbeats'],
  ])('viewer em %s vê "Sem permissão" e nada é enviado', async (rota, post) => {
    const r = await renderizarPainel(`${P}${rota}`, { antes: comPapel('viewer') })
    expect(await screen.findByRole('heading', { level: 1, name: 'Sem permissão' })).toBeInTheDocument()
    expect(screen.getByText('Seu papel neste projeto não permite esta ação.')).toBeInTheDocument()
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
    expect(botao('Cadastrar')).not.toBeInTheDocument()
    expect(r.falsa.chamadas(post)).toBe(0)
  })

  it.each([
    ['/targets/alvo-1/edit', 'GET /projects/:projectId/targets/:id'],
    ['/heartbeats/h1/edit', 'GET /projects/:projectId/heartbeats/:id'],
  ])('viewer em %s vê "Sem permissão" sem buscar o recurso', async (rota, get) => {
    const r = await renderizarPainel(`${P}${rota}`, { antes: comPapel('viewer') })
    expect(await screen.findByRole('heading', { level: 1, name: 'Sem permissão' })).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByRole('textbox')).not.toBeInTheDocument())
    expect(r.falsa.chamadas(get)).toBe(0)
  })

  it('member em /targets/new vê o formulário', async () => {
    await renderizarPainel(`${P}/targets/new`, { antes: comPapel('member') })
    expect(await screen.findByRole('heading', { level: 1, name: 'Novo alvo' })).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Sem permissão' })).not.toBeInTheDocument()
  })
})
