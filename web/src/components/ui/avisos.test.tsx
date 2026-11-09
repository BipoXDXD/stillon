import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useEffect } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ProvedorDoPainel } from '@/app/contexto'
import { novaSessao, usarApiFalsa } from '@/test/ambiente'
import { ProvedorDeAvisos, useAvisar } from './avisos'

type Avisar = ReturnType<typeof useAvisar>

function montar() {
  const falsa = usarApiFalsa()
  falsa.logado()
  const { sessao, api } = novaSessao()
  let avisar: Avisar = () => {
    throw new Error('o provedor não montou')
  }
  function Tela() {
    const doProvedor = useAvisar()
    useEffect(() => {
      avisar = doProvedor
    }, [doProvedor])
    return (
      <main>
        <h1 tabIndex={-1}>A tela</h1>
      </main>
    )
  }
  render(
    <ProvedorDoPainel sessao={sessao} api={api}>
      <ProvedorDeAvisos>
        <Tela />
      </ProvedorDeAvisos>
    </ProvedorDoPainel>,
  )
  return { sessao, avisar: (texto: string, tipo?: 'ok' | 'erro') => act(() => avisar(texto, tipo)) }
}

const regiao = () => within(screen.getByRole('region', { name: 'Avisos (F8)' }))
const dispensar = () => regiao().getAllByRole('button', { name: 'Dispensar aviso' })

afterEach(() => {
  vi.useRealTimers()
})

describe('o tempo de cada aviso', () => {
  // Fronteira de 6 s. Sabotagem: mudar a duração do sucesso.
  it('sucesso some em 6 s', () => {
    vi.useFakeTimers()
    const { avisar } = montar()
    avisar('Alvo salvo.')
    act(() => {
      vi.advanceTimersByTime(5999)
    })
    expect(regiao().getByText('Alvo salvo.')).toBeInTheDocument()
    act(() => {
      vi.advanceTimersByTime(1)
    })
    expect(regiao().queryByText('Alvo salvo.')).not.toBeInTheDocument()
  })

  // Sabotagem: dar ao erro a duração do sucesso.
  it('erro fica até ser dispensado, com ícone e "X" de 44 px', () => {
    vi.useFakeTimers()
    const { avisar } = montar()
    avisar('Não foi possível sair. Tente de novo.', 'erro')
    act(() => {
      vi.advanceTimersByTime(600_000)
    })
    const texto = regiao().getByText('Não foi possível sair. Tente de novo.')
    expect(texto).toBeInTheDocument()
    expect(dispensar()[0]).toHaveClass('size-11')
    const aviso = texto.closest('li')
    expect(aviso?.querySelector('svg.lucide-circle-alert')).toHaveAttribute('aria-hidden', 'true')
    expect(aviso).toHaveClass('border-perigo')
  })

  it('sucesso não tem "X": some sozinho', () => {
    const { avisar } = montar()
    avisar('Alvo salvo.')
    expect(regiao().queryByRole('button', { name: 'Dispensar aviso' })).not.toBeInTheDocument()
  })
})

describe('o acúmulo de erros', () => {
  // Sabotagem: tirar o some() do avisar.
  it('o mesmo texto não empilha', () => {
    const { avisar } = montar()
    avisar('Não foi possível copiar.', 'erro')
    avisar('Não foi possível copiar.', 'erro')
    expect(regiao().getAllByText('Não foi possível copiar.')).toHaveLength(1)
  })

  // Sabotagem: mostrar todos os erros.
  it('mostra no máximo 3 erros, os mais novos, e conta o resto', () => {
    const { avisar } = montar()
    for (const n of [1, 2, 3, 4, 5]) avisar(`Erro ${n}`, 'erro')
    expect(dispensar()).toHaveLength(3)
    expect(regiao().queryByText('Erro 2')).not.toBeInTheDocument()
    expect(regiao().getByText('Erro 3')).toBeInTheDocument()
    expect(regiao().getByText('Erro 5')).toBeInTheDocument()
    expect(screen.getByText('e mais 2')).toBeInTheDocument()
  })

  // A linha nascia dentro da lista, antes do toast seguinte, e ficava no meio
  // da pilha. Sabotagem: devolver a linha para dentro da região.
  it('"e mais N" vem sempre depois dos 3 visíveis', () => {
    const { avisar } = montar()
    for (const n of [1, 2, 3, 4, 5]) avisar(`Erro ${n}`, 'erro')
    const mais = screen.getByText('e mais 2')
    for (const n of [3, 4, 5]) {
      const visivel = regiao().getByText(`Erro ${n}`)
      expect(visivel.compareDocumentPosition(mais) & Node.DOCUMENT_POSITION_FOLLOWING).toBe(Node.DOCUMENT_POSITION_FOLLOWING)
    }
  })

  it('com 3 erros, não há "e mais"', () => {
    const { avisar } = montar()
    for (const n of [1, 2, 3]) avisar(`Erro ${n}`, 'erro')
    expect(dispensar()).toHaveLength(3)
    expect(screen.queryByText(/^e mais/)).not.toBeInTheDocument()
  })

  it('dispensar um traz o próximo da fila', async () => {
    const usuario = userEvent.setup()
    const { avisar } = montar()
    for (const n of [1, 2, 3, 4]) avisar(`Erro ${n}`, 'erro')
    const [primeiro] = dispensar()
    if (primeiro === undefined) throw new Error('sem botão de dispensar')
    await usuario.click(primeiro)
    await waitFor(() => expect(regiao().getByText('Erro 1')).toBeInTheDocument())
    expect(screen.queryByText(/^e mais/)).not.toBeInTheDocument()
  })

  // Rolar: a pilha nunca empurra um "X" para fora da tela.
  it('a região tem nome em português e rola', () => {
    montar()
    expect(screen.getByRole('region', { name: 'Avisos (F8)' }).querySelector('ol')).toHaveClass('max-h-[calc(100dvh-9rem)]', 'overflow-y-auto')
  })
})

describe('o foco ao dispensar', () => {
  // O Radix deixaria o foco no <ol> da região, mesmo vazio. Sabotagem: não
  // devolver o foco.
  it('o último dispensado devolve o foco ao título da tela', async () => {
    const usuario = userEvent.setup()
    const { avisar } = montar()
    avisar('Erro 1', 'erro')
    const [x] = dispensar()
    if (x === undefined) throw new Error('sem botão de dispensar')
    x.focus()
    await usuario.keyboard('{Enter}')
    await waitFor(() => expect(screen.getByRole('heading', { name: 'A tela' })).toHaveFocus())
  })

  it('sobrando outro erro, o foco vai ao "X" dele', async () => {
    const usuario = userEvent.setup()
    const { avisar } = montar()
    avisar('Erro 1', 'erro')
    avisar('Erro 2', 'erro')
    const [x] = dispensar()
    if (x === undefined) throw new Error('sem botão de dispensar')
    x.focus()
    await usuario.keyboard('{Enter}')
    await waitFor(() => expect(regiao().queryAllByRole('button', { name: 'Dispensar aviso' })).toHaveLength(1))
    await waitFor(() => expect(dispensar()[0]).toHaveFocus())
  })
})

// O provedor fica acima do roteador: sem isto, o erro de uma sessão apareceria
// no login da seguinte. Sabotagem: tirar o efeito do estado anônimo.
it('ao sair da sessão, os erros vão embora', async () => {
  const { sessao, avisar } = montar()
  await act(() => sessao.iniciar())
  avisar('Erro antigo', 'erro')
  expect(regiao().getByText('Erro antigo')).toBeInTheDocument()
  await act(() => sessao.sair())
  await waitFor(() => expect(regiao().queryByText('Erro antigo')).not.toBeInTheDocument())
})
