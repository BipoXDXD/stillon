import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useRef, useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { Confirmar, Dialogo } from './dialogo'

// O Radix só ouve o pointerdown de fora depois de um tique (setTimeout 0), e
// com o botão principal só decide no click que vem depois.
const umTique = () => act(() => new Promise((r) => setTimeout(r, 0)))
function clicarFora() {
  fireEvent.pointerDown(document.body, { button: 0 })
  fireEvent.click(document.body, { button: 0 })
}

function montar(travado: boolean) {
  const aoMudar = vi.fn<(v: boolean) => void>()
  render(
    <Dialogo aberto aoMudar={aoMudar} titulo="Códigos de recuperação" travado={travado}>
      <button type="button">Já guardei</button>
    </Dialogo>,
  )
  return aoMudar
}

describe('Dialogo', () => {
  // Controle positivo: sem travar, o clique fora e o Esc fecham. Prova que o
  // teste do travado dispara os eventos que o Radix ouve.
  it('sem travar, o clique fora e o Esc pedem para fechar', async () => {
    const aoMudar = montar(false)
    await screen.findByRole('dialog')
    await umTique()
    clicarFora()
    expect(aoMudar).toHaveBeenCalledWith(false)
    aoMudar.mockClear()
    await userEvent.keyboard('{Escape}')
    expect(aoMudar).toHaveBeenCalledWith(false)
  })

  it('travado ignora o clique fora e o Esc', async () => {
    const aoMudar = montar(true)
    await screen.findByRole('dialog')
    await umTique()
    clicarFora()
    await userEvent.keyboard('{Escape}')
    expect(aoMudar).not.toHaveBeenCalled()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })
})

// Revisão final (I1): o foco volta a quem abriu; se quem abriu sumiu com a
// ação, vai à reserva do chamador ou ao <h1> da tela — nunca ao <body>.
// Sabotagens: em useFocoDeVolta, trocar a lista por
// "[gatilho.current, reserva ? null : null]" → os dois últimos ficam
// vermelhos (foco no body); tirar o gatilho da lista → o primeiro.
describe('Dialogo: foco ao fechar', () => {
  function Tela({ sumir, comReserva }: { sumir: boolean; comReserva: boolean }) {
    const [aberto, setAberto] = useState(false)
    const [gatilho, setGatilho] = useState(true)
    const reserva = useRef<HTMLButtonElement>(null)
    return (
      <main>
        <h1 tabIndex={-1}>Tela</h1>
        {gatilho && (
          <button type="button" onClick={() => setAberto(true)}>
            Abrir
          </button>
        )}
        <button type="button" ref={reserva}>
          Reserva
        </button>
        <Dialogo
          aberto={aberto}
          aoMudar={setAberto}
          titulo="Diálogo"
          reserva={comReserva ? reserva : undefined}
        >
          <button
            type="button"
            onClick={() => {
              if (sumir) setGatilho(false)
              setAberto(false)
            }}
          >
            Fazer
          </button>
        </Dialogo>
      </main>
    )
  }

  it('volta a quem abriu', async () => {
    render(<Tela sumir={false} comReserva />)
    await userEvent.click(screen.getByRole('button', { name: 'Abrir' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Fazer' }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Abrir' })).toHaveFocus())
  })

  it('quem abriu sumiu: vai à reserva', async () => {
    render(<Tela sumir comReserva />)
    await userEvent.click(screen.getByRole('button', { name: 'Abrir' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Fazer' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    await waitFor(() => expect(screen.getByRole('button', { name: 'Reserva' })).toHaveFocus())
  })

  it('quem abriu sumiu, sem reserva: vai ao <h1> da tela', async () => {
    render(<Tela sumir comReserva={false} />)
    await userEvent.click(screen.getByRole('button', { name: 'Abrir' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Fazer' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Tela' })).toHaveFocus())
  })
})

// DI 4: na confirmação, a ação destrutiva é a primária e só ali fica
// vermelha. Antes dela, Apagar e Encerrar são terciários.
describe('Confirmar', () => {
  it('o botão da ação é o vermelho sólido; o Cancelar, secundário', async () => {
    render(
      <Confirmar aberto aoMudar={() => {}} titulo="Apagar o alvo?" descricao="Ele some do painel." acao="Apagar" aoConfirmar={() => {}} />,
    )
    const dialogo = await screen.findByRole('alertdialog', { name: 'Apagar o alvo?' })
    expect(within(dialogo).getByRole('button', { name: 'Apagar' })).toHaveClass('bg-perigo')
    expect(within(dialogo).getByRole('button', { name: 'Cancelar' })).toHaveClass('border-borda-controle')
  })
})
