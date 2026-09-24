import './lib/nonce'
import './index.css'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

// Provisório (Task 2): a árvore do painel (rotas, sessão, provedores) chega
// na Task 3. Este arquivo existe para o build produzir um dist de verdade.
createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <main>
      <h1>Vigilume</h1>
    </main>
  </StrictMode>,
)
