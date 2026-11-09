import type { RouteObject } from 'react-router'
import { PaginaCanais } from '@/features/canais/PaginaCanais'
import { PaginaHeartbeat } from '@/features/heartbeats/PaginaHeartbeat'
import { PaginaEditarHeartbeat, PaginaNovoHeartbeat } from '@/features/heartbeats/PaginasDoFormulario'
import { PaginaIncidentes } from '@/features/incidentes/PaginaIncidentes'
import { PaginaLogin } from '@/features/login/PaginaLogin'
import { PaginaPainel } from '@/features/painel/PaginaPainel'
import { PaginaEditarAlvo, PaginaNovoAlvo } from '@/features/alvos/PaginasDoFormulario'
import { PaginaStatusPage } from '@/features/statuspage/PaginaStatusPage'
import { ErroDeRota } from './ErroDeRota'
import { ExigirSessao, SplashDoPainel } from './ExigirSessao'
import { Layout } from './Layout'
import { NaoEncontrada } from './NaoEncontrada'
import { carregarConfiguracoes, carregarDetalhe } from './rotas-sob-demanda'

// As rotas da spec §11.3. O detalhe do alvo e as configurações chegam sob
// demanda.
export const rotas: RouteObject[] = [
  { path: '/login', Component: PaginaLogin, ErrorBoundary: ErroDeRota },
  {
    Component: ExigirSessao,
    ErrorBoundary: ErroDeRota,
    // Na carga direta de uma rota lazy, o que aparece até o chunk chegar.
    HydrateFallback: SplashDoPainel,
    children: [
      {
        Component: Layout,
        children: [
          { index: true, Component: PaginaPainel },
          { path: 'targets/new', Component: PaginaNovoAlvo },
          {
            path: 'targets/:id',
            lazy: async () => ({ Component: (await carregarDetalhe()).PaginaDetalhe }),
          },
          { path: 'targets/:id/edit', Component: PaginaEditarAlvo },
          { path: 'heartbeats/new', Component: PaginaNovoHeartbeat },
          { path: 'heartbeats/:id', Component: PaginaHeartbeat },
          { path: 'heartbeats/:id/edit', Component: PaginaEditarHeartbeat },
          { path: 'incidents', Component: PaginaIncidentes },
          { path: 'channels', Component: PaginaCanais },
          { path: 'status-pages', Component: PaginaStatusPage },
          {
            path: 'settings',
            lazy: async () => ({ Component: (await carregarConfiguracoes()).PaginaConfiguracoes }),
          },
          { path: '*', Component: NaoEncontrada },
        ],
      },
    ],
  },
]
