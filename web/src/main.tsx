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

// O #root vem do index.html; sem ele, o erro diz o que falta.
const raiz = document.getElementById('root')
if (!raiz) throw new Error('index.html sem o elemento #root')

createRoot(raiz).render(
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
