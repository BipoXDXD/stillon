import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Botao } from './botao'

describe('Botao', () => {
  it('é type="button" por padrão, para não enviar o formulário em volta', () => {
    render(<Botao>Ação</Botao>)
    expect(screen.getByRole('button', { name: 'Ação' })).toHaveAttribute('type', 'button')
  })

  // Pirâmide (DI 4): primário sólido, secundário com contorno, terciário só
  // em texto; o perigo é o vermelho sólido da confirmação.
  it.each([
    ['primario', 'bg-primaria'],
    ['secundario', 'border-borda-controle'],
    ['terciario', 'text-primaria'],
    ['perigo', 'bg-perigo'],
  ] as const)('o tipo %s pinta com %s', (tipo, classe) => {
    render(<Botao tipo={tipo}>Ação</Botao>)
    expect(screen.getByRole('button', { name: 'Ação' })).toHaveClass(classe)
  })

  it.each(['primario', 'secundario', 'terciario'] as const)('o tipo %s não usa o vermelho', (tipo) => {
    render(<Botao tipo={tipo}>Ação</Botao>)
    expect(screen.getByRole('button', { name: 'Ação' }).className).not.toMatch(/perigo/)
  })

  it.each(['normal', 'pequeno', 'icone'] as const)('o tamanho %s tem 44 px de altura mínima', (tamanho) => {
    render(<Botao tamanho={tamanho}>Ação</Botao>)
    expect(screen.getByRole('button', { name: 'Ação' })).toHaveClass('min-h-11')
  })

  it('o tamanho icone é um quadrado de 44 px', () => {
    render(<Botao tamanho="icone" aria-label="Tema: Sistema" />)
    expect(screen.getByRole('button', { name: 'Tema: Sistema' })).toHaveClass('size-11')
  })

  it('asChild passa o estilo ao filho e não põe type', () => {
    render(
      <Botao asChild tipo="secundario">
        <a href="/channels">Configurar canais</a>
      </Botao>,
    )
    const link = screen.getByRole('link', { name: 'Configurar canais' })
    expect(link).toHaveClass('border-borda-controle')
    expect(link).not.toHaveAttribute('type')
  })

  it('não aceita mais o tipo fantasma (o tsc -b confere)', () => {
    // @ts-expect-error o "fantasma" virou "terciario" no redesign
    const elemento = <Botao tipo="fantasma">Ação</Botao>
    expect(elemento).toBeTruthy()
  })
})
