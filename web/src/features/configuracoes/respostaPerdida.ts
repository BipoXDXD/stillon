import { ApiError } from '@/lib/api/http'

// A resposta de um confirm se perdeu (E2 do Plano 10): sem rede, prazo
// estourado (os dois chegam como 'network'), um 5xx do proxy sem o envelope,
// ou um 2xx que não se lê. O servidor pode ter gravado. Um erro COM o
// envelope é a resposta do servidor, e ela diz o que aconteceu.
export const respostaPerdida = (e: unknown) =>
  e instanceof ApiError && (e.code === 'network' || (e.code === 'unexpected' && (e.status >= 500 || e.status < 400)))
