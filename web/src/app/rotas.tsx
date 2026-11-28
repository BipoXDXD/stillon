import type { RouteObject } from 'react-router'
import { PaginaCanais } from '@/features/canais/PaginaCanais'
import { PaginaCadastro } from '@/features/conta/PaginaCadastro'
import { PaginaConfirmarEmail } from '@/features/conta/PaginaConfirmarEmail'
import { PaginaEsqueciSenha } from '@/features/conta/PaginaEsqueciSenha'
import { PaginaRedefinirSenha } from '@/features/conta/PaginaRedefinirSenha'
import { PaginaHeartbeat } from '@/features/heartbeats/PaginaHeartbeat'
import { PaginaEditarHeartbeat, PaginaNovoHeartbeat } from '@/features/heartbeats/PaginasDoFormulario'
import { PaginaIncidentes } from '@/features/incidentes/PaginaIncidentes'
import { PaginaPrivacidade } from '@/features/legal/PaginaPrivacidade'
import { PaginaTermos } from '@/features/legal/PaginaTermos'
import { PaginaLogin } from '@/features/login/PaginaLogin'
import { PaginaPainel } from '@/features/painel/PaginaPainel'
import { PaginaEditarAlvo, PaginaNovoAlvo } from '@/features/alvos/PaginasDoFormulario'
import { PaginaStatusPage } from '@/features/statuspage/PaginaStatusPage'
import { ErroDeRota } from './ErroDeRota'
import { ExigirSessao, SplashDoPainel } from './ExigirSessao'
import { Layout } from './Layout'
import { NaoEncontrada } from './NaoEncontrada'
import { ProvedorDoProjeto } from './projeto'
import { RedirecionarAoEscolhido, RedirecionarAoProjeto } from './RedirecionarAoProjeto'
import { carregarConfiguracoes, carregarDetalhe } from './rotas-sob-demanda'

// As rotas da spec §11.3, com as telas de recurso dentro do projeto da URL.
// O detalhe do alvo e as configurações chegam sob demanda.
export const rotas: RouteObject[] = [
  { path: '/login', Component: PaginaLogin, ErrorBoundary: ErroDeRota },
  // O cadastro público (PR 3): sem sessão, como o login. Os links dos e-mails
  // levam o token no fragmento (#token=).
  { path: '/signup', Component: PaginaCadastro, ErrorBoundary: ErroDeRota },
  { path: '/confirm-email', Component: PaginaConfirmarEmail, ErrorBoundary: ErroDeRota },
  { path: '/forgot-password', Component: PaginaEsqueciSenha, ErrorBoundary: ErroDeRota },
  { path: '/reset-password', Component: PaginaRedefinirSenha, ErrorBoundary: ErroDeRota },
  // Os textos legais: públicos, abertos pela caixa de aceite do cadastro.
  { path: '/termos', Component: PaginaTermos, ErrorBoundary: ErroDeRota },
  { path: '/privacidade', Component: PaginaPrivacidade, ErrorBoundary: ErroDeRota },
  {
    Component: ExigirSessao,
    ErrorBoundary: ErroDeRota,
    // Na carga direta de uma rota lazy, o que aparece até o chunk chegar.
    HydrateFallback: SplashDoPainel,
    children: [
      {
        Component: Layout,
        children: [
          { index: true, Component: RedirecionarAoEscolhido },
          {
            // /p/ é da página pública; o painel do projeto fica em /projects.
            path: 'projects/:projectId',
            Component: ProvedorDoProjeto,
            children: [
              { index: true, Component: PaginaPainel },
              { path: 'targets/new', Component: PaginaNovoAlvo },
              { path: 'targets/:id/edit', Component: PaginaEditarAlvo },
              { path: 'heartbeats/new', Component: PaginaNovoHeartbeat },
              { path: 'heartbeats/:id', Component: PaginaHeartbeat },
              { path: 'heartbeats/:id/edit', Component: PaginaEditarHeartbeat },
              { path: 'incidents', Component: PaginaIncidentes },
              { path: 'channels', Component: PaginaCanais },
              { path: 'status-pages', Component: PaginaStatusPage },
              {
                // O chunk que falha fica dentro do Layout. O boundary vem numa
                // rota acima da lazy: nela mesma, a rota ainda sem módulo faria
                // o router cortar a árvore no HydrateFallback, e a tela ficaria
                // em branco.
                ErrorBoundary: ErroDeRota,
                children: [
                  {
                    path: 'targets/:id',
                    lazy: async () => ({ Component: (await carregarDetalhe()).PaginaDetalhe }),
                  },
                ],
              },
              { path: '*', Component: NaoEncontrada },
            ],
          },
          {
            // As configurações são da conta, fora de qualquer projeto.
            ErrorBoundary: ErroDeRota,
            children: [
              {
                path: 'settings',
                lazy: async () => ({ Component: (await carregarConfiguracoes()).PaginaConfiguracoes }),
              },
            ],
          },
          // Os links de antes dos projetos, no Projeto padrão.
          ...['targets/*', 'heartbeats/*', 'incidents', 'channels', 'status-pages'].map((path) => ({
            path,
            Component: RedirecionarAoProjeto,
          })),
          { path: '*', Component: NaoEncontrada },
        ],
      },
    ],
  },
]
