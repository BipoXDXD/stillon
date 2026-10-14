import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ProvedorDeTema } from '@/lib/tema'
import { BotaoDeTema } from './BotaoDeTema'

afterEach(() => document.documentElement.classList.remove('dark'))

// O jsdom do setup diz que o sistema é claro (matchMedia sem match).
function montar() {
  render(
    <ProvedorDeTema>
      <BotaoDeTema />
    </ProvedorDeTema>,
  )
  return userEvent.setup()
}

describe('BotaoDeTema', () => {
  it('diz o tema em vigor e, a cada clique, passa a Claro, a Escuro e de volta a Sistema', async () => {
    const usuario = montar()
    const botao = screen.getByRole('button', { name: 'Tema: Sistema' })
    await usuario.click(botao)
    expect(botao).toHaveAccessibleName('Tema: Claro')
    expect(document.documentElement).not.toHaveClass('dark')
    await usuario.click(botao)
    expect(botao).toHaveAccessibleName('Tema: Escuro')
    expect(document.documentElement).toHaveClass('dark')
    await usuario.click(botao)
    expect(botao).toHaveAccessibleName('Tema: Sistema')
    expect(document.documentElement).not.toHaveClass('dark')
  })

  it('guarda a escolha, como o seletor antigo', async () => {
    const usuario = montar()
    await usuario.click(screen.getByRole('button', { name: 'Tema: Sistema' }))
    expect(localStorage.getItem('stillon.tema')).toBe('claro')
  })

  it('o title diz para qual tema o clique leva', () => {
    montar()
    expect(screen.getByRole('button', { name: 'Tema: Sistema' })).toHaveAttribute('title', 'Trocar para Claro')
  })

  it('sem storage, a troca ainda vale nesta aba', async () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('storage indisponível')
    })
    const usuario = montar()
    await usuario.click(screen.getByRole('button', { name: 'Tema: Sistema' }))
    expect(screen.getByRole('button', { name: 'Tema: Claro' })).toBeInTheDocument()
  })

  it('é um botão de ícone de 44 px', () => {
    montar()
    expect(screen.getByRole('button', { name: 'Tema: Sistema' })).toHaveClass('size-11')
  })
})
