import type { RouteObject } from 'react-router'
import { PaginaIncidentes } from '@/features/incidentes/PaginaIncidentes'
import { PaginaLogin } from '@/features/login/PaginaLogin'
import { PaginaPainel } from '@/features/painel/PaginaPainel'
import { PaginaEditarAlvo, PaginaNovoAlvo } from '@/features/alvos/PaginasDoFormulario'
import { ErroDeRota } from './ErroDeRota'
import { ExigirSessao } from './ExigirSessao'
import { Layout } from './Layout'
import { NaoEncontrada } from './NaoEncontrada'

// As rotas da spec §11.3. O detalhe do alvo (Recharts, quase metade do
// bundle) e as configurações (QR code) chegam sob demanda.
export const rotas: RouteObject[] = [
  { path: '/login', Component: PaginaLogin, ErrorBoundary: ErroDeRota },
  {
    Component: ExigirSessao,
    ErrorBoundary: ErroDeRota,
    children: [
      {
        Component: Layout,
        children: [
          { index: true, Component: PaginaPainel },
          { path: 'targets/new', Component: PaginaNovoAlvo },
          {
            path: 'targets/:id',
            lazy: async () => ({ Component: (await import('@/features/alvos/PaginaDetalhe')).PaginaDetalhe }),
          },
          { path: 'targets/:id/edit', Component: PaginaEditarAlvo },
          { path: 'incidents', Component: PaginaIncidentes },
          { path: '*', Component: NaoEncontrada },
        ],
      },
    ],
  },
]
