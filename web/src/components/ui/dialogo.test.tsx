import { act, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { Dialogo } from './dialogo'

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
