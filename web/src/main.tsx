import './lib/nonce'
import './index.css'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { createBrowserRouter } from 'react-router'
import { RouterProvider } from 'react-router/dom'
import { ProvedorDeAvisos } from '@/components/ui/avisos'
import { api, sessao } from '@/lib/api'
import { ProvedorDeTema } from '@/lib/tema'
import { ProvedorDoPainel } from './app/contexto'
import { rotas } from './app/rotas'

// O boot é disparado aqui, fora da árvore React: uma promessa por carga de
// página, que o StrictMode não duplica (spec do Plano 6, D9).
void sessao.iniciar()

const router = createBrowserRouter(rotas)

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ProvedorDoPainel sessao={sessao} api={api}>
      <ProvedorDeTema>
        <ProvedorDeAvisos>
          <RouterProvider router={router} />
        </ProvedorDeAvisos>
      </ProvedorDeTema>
    </ProvedorDoPainel>
  </StrictMode>,
)
