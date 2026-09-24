import { derrubar } from './pilha'

export default function globalTeardown() {
  if (process.env.E2E_PILHA_PROPRIA) derrubar()
}
