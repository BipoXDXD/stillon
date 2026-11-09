import { render, screen, waitFor } from '@testing-library/react'
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

// M3 da revisão final: 404 não é "dados velhos": o que estava na tela não
// existe mais. Sabotagem: tirar o "|| naoEncontrado(consulta.error)" do
// ramo de erro → o aviso de dados velhos aparece com os dados.
it('404 com dados na tela é terminal, e não "dados velhos"', () => {
  render(
    <EstadoDaConsulta
      consulta={{
        data: 'os dados',
        error: new ApiError(404, 'not_found', ''),
        isPending: false,
        refetch: () => Promise.reject(new Error('não usado')),
        dataUpdatedAt: Date.parse('2026-09-24T12:00:00Z'),
      }}
    >
      {(d) => <p>{d}</p>}
    </EstadoDaConsulta>,
  )
  expect(screen.getByRole('alert')).toHaveTextContent('Não encontrado.')
  expect(screen.queryByText('os dados')).not.toBeInTheDocument()
  expect(screen.queryByRole('status')).not.toBeInTheDocument()
})


const naoUsado = () => Promise.reject(new Error('não usado'))
const falhou = { data: undefined, error: new ApiError(502, 'unexpected', ''), isPending: false, refetch: naoUsado, dataUpdatedAt: 0 }
const carregou = { data: 'os dados', error: null, isPending: false, refetch: naoUsado, dataUpdatedAt: Date.parse('2026-09-24T12:00:00Z') }
const dadosVelhos = { ...carregou, error: new ApiError(502, 'unexpected', '') }

// O "Tentar de novo" some quando dá certo, e o foco, que estava nele, cairia
// no <body>. Sabotagem: trocar BotaoQueSome por Botao no EstadoDaConsulta.
it.each([
  ['erro sem dados', falhou],
  ['dados velhos', dadosVelhos],
])('%s: o "Tentar de novo" que some leva o foco ao título da tela', async (_nome, comErro) => {
  const tela = (consulta: typeof carregou | typeof falhou | typeof dadosVelhos) => (
    <main>
      <h1 tabIndex={-1}>A tela</h1>
      <EstadoDaConsulta consulta={consulta}>{(d) => <p>{d}</p>}</EstadoDaConsulta>
    </main>
  )
  const { rerender } = render(tela(comErro))
  screen.getByRole('button', { name: 'Tentar de novo' }).focus()
  rerender(tela(carregou))
  expect(screen.queryByRole('button', { name: 'Tentar de novo' })).not.toBeInTheDocument()
  await waitFor(() => expect(screen.getByRole('heading', { name: 'A tela' })).toHaveFocus())
})

// O caminho real sem dados: o refetch põe a consulta em pending, e o esqueleto
// toma o lugar do botão antes de o <h1> existir. Sabotagem: tirar o <main> da
// reserva → o foco cai no <body> já no pending.
it('erro → pending → dados: sem <h1> na tela, o foco fica no <main>', async () => {
  const pendente = { data: undefined, error: null, isPending: true, refetch: naoUsado, dataUpdatedAt: 0 }
  const tela = (consulta: typeof carregou | typeof falhou | typeof pendente) => (
    <main tabIndex={-1}>
      <EstadoDaConsulta consulta={consulta}>{(d) => <p>{d}</p>}</EstadoDaConsulta>
    </main>
  )
  const { rerender } = render(tela(falhou))
  screen.getByRole('button', { name: 'Tentar de novo' }).focus()
  rerender(tela(pendente))
  expect(screen.queryByRole('button', { name: 'Tentar de novo' })).not.toBeInTheDocument()
  await waitFor(() => expect(screen.getByRole('main')).toHaveFocus())
  rerender(tela(carregou))
  expect(screen.getByText('os dados')).toBeInTheDocument()
  expect(screen.getByRole('main')).toHaveFocus()
})

// Sem passar por pending (os dados chegam ao cache por outro caminho), o <h1>
// nasce no mesmo commit em que o botão sai. Sabotagem: ler a reserva na hora
// da desmontagem, sem esperar o fim do commit → o <h1> ainda não existe.
it('o título que nasce junto com os dados também recebe o foco', async () => {
  const tela = (consulta: typeof carregou | typeof falhou) => (
    <main>
      <EstadoDaConsulta consulta={consulta}>{(d) => <h1 tabIndex={-1}>{d}</h1>}</EstadoDaConsulta>
    </main>
  )
  const { rerender } = render(tela(falhou))
  screen.getByRole('button', { name: 'Tentar de novo' }).focus()
  rerender(tela(carregou))
  await waitFor(() => expect(screen.getByRole('heading', { name: 'os dados' })).toHaveFocus())
})

// O esqueleto que imita a tela (P12 da auditoria): a tela passa o dela.
// Sabotagem: ignorar a prop e devolver sempre o <Esqueleto />.
it('carregando, mostra o esqueleto dado no lugar do padrão', () => {
  render(
    <EstadoDaConsulta
      consulta={{
        data: undefined,
        error: null,
        isPending: true,
        refetch: () => Promise.reject(new Error('não usado')),
        dataUpdatedAt: 0,
      }}
      esqueleto={<p>o esqueleto da tela</p>}
    >
      {() => <p>os dados</p>}
    </EstadoDaConsulta>,
  )
  expect(screen.getByText('o esqueleto da tela')).toBeInTheDocument()
  expect(screen.queryByText('Carregando…')).not.toBeInTheDocument()
})
