import { defineConfig } from '@playwright/test'
import base from './playwright.config'

// As capturas do README (docs/design/painel-depois), fora do CI: o
// playwright.config.ts as ignora, e só este config as roda. Mesma pilha e
// mesmo navegador do E2E. O comando está no comentário de
// e2e/capturas.spec.ts.
export default defineConfig({
  ...base,
  testIgnore: undefined,
  testMatch: '**/capturas.spec.ts',
})
