import { defineConfig, devices } from '@playwright/test'

// O E2E roda contra o binário real (spec do Plano 6 §7.4; reconciliação,
// item 4). O globalSetup sobe a pilha (Postgres em container, o binário com
// o painel embutido, stillon user create) — ou, com E2E_BASE_URL,
// E2E_EMAIL e E2E_PASSWORD, usa uma pilha já de pé (o compose do Plano 7).
// O webServer do Playwright não serve: ele sobe ANTES do globalSetup, e o
// binário precisa do banco antes.
export default defineConfig({
  testDir: './e2e',
  // Uma pilha, um usuário: os testes dividem o limite por usuário da API
  // (60/min, burst 20) e o do login (10/min por IP). Em série.
  workers: 1,
  fullyParallel: false,
  // Sem retries: uma nova tentativa esconderia exatamente o teste que
  // depende de sorte (lições de teste do projeto).
  retries: 0,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  globalSetup: './e2e/global-setup.ts',
  globalTeardown: './e2e/global-teardown.ts',
  reporter: [['list']],
  use: { trace: 'retain-on-failure', locale: 'pt-BR', timezoneId: 'America/Sao_Paulo' },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
})
