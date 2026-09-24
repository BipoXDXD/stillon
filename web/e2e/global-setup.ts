import { EMAIL, SENHA, subir } from './pilha'

export default async function globalSetup() {
  if (process.env.E2E_BASE_URL) {
    // Pilha externa (o compose do Plano 7): o usuário já existe.
    if (!process.env.E2E_EMAIL || !process.env.E2E_PASSWORD) {
      throw new Error('E2E: com E2E_BASE_URL, informe também E2E_EMAIL e E2E_PASSWORD')
    }
    return
  }
  const { baseURL } = await subir()
  // As variáveis do globalSetup chegam aos workers.
  process.env.E2E_BASE_URL = baseURL
  process.env.E2E_EMAIL = EMAIL
  process.env.E2E_PASSWORD = SENHA
  process.env.E2E_PILHA_PROPRIA = '1'
}
