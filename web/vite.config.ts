/// <reference types="vitest/config" />
import { mkdirSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig, type Plugin } from 'vite'

// O build sai dentro do pacote Go que o embute (contrato dos Planos 5–7).
const saida = path.resolve(import.meta.dirname, '../internal/webui/dist')

// emptyOutDir apaga o dist inteiro, inclusive o .gitkeep que mantém o
// diretório versionado para o //go:embed compilar sem build. Sem esvaziar, o
// Vite não limpa um outDir fora da raiz, e os bundles velhos iriam para o
// binário. Este plugin devolve o .gitkeep depois de cada build.
function manterGitkeep(): Plugin {
  return {
    name: 'stillon:gitkeep',
    apply: 'build',
    closeBundle() {
      mkdirSync(saida, { recursive: true })
      writeFileSync(path.join(saida, '.gitkeep'), '')
    },
  }
}

export default defineConfig({
  plugins: [react(), tailwindcss(), manterGitkeep()],
  resolve: { alias: { '@': path.resolve(import.meta.dirname, 'src') } },
  // O Vite põe este marcador em <meta property="csp-nonce">, nos <script> e
  // nos <link> do index.html; o handler Go (internal/webui) troca cada
  // ocorrência pelo nonce da resposta. O valor é webui.Marcador.
  html: { cspNonce: 'STILLON_CSP_NONCE' },
  // O limite de aviso sobe para o chunk principal (React, Router, Query, Radix,
  // Zod): o Recharts, que era metade do bundle, já saiu dele (lazy).
  build: { outDir: saida, emptyOutDir: true, sourcemap: false, chunkSizeWarningLimit: 700 },
  server: {
    // Em desenvolvimento, o navegador só vê localhost:5173: o cookie
    // SameSite=Strict de /api/v1/auth funciona sem CORS.
    proxy: { '/api': 'http://localhost:8080' },
  },
  test: {
    environment: 'jsdom',
    environmentOptions: { jsdom: { url: 'http://localhost:5173/' } },
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.test.{ts,tsx}'],
    restoreMocks: true,
  },
})
