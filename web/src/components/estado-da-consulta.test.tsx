import { render, screen } from '@testing-library/react'
import { expect, it } from 'vitest'
import { ApiError } from '@/lib/api/http'
import { EstadoDaConsulta } from './EstadoDaConsulta'

// Dados velhos na tela com o aviso (D17): o aviso é uma região de status,
// para o leitor de tela saber que a atualização falhou sem o foco sair do
// lugar. Sabotagem prevista: trocar o <output> do aviso por <div>.
it('o aviso de dados velhos é anunciado como status', () => {
  render(
    <EstadoDaConsulta
      consulta={{
        data: 'os dados',
        error: new ApiError(502, 'unexpected', ''),
        isPending: false,
        refetch: () => Promise.reject(new Error('não usado')),
        dataUpdatedAt: Date.parse('2026-09-24T12:00:00Z'),
      }}
    >
      {(d) => <p>{d}</p>}
    </EstadoDaConsulta>,
  )
  expect(screen.getByRole('status')).toHaveTextContent('Não foi possível atualizar agora')
  expect(screen.getByText('os dados')).toBeInTheDocument()
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
})
