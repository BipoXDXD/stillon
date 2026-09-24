import { setNonce } from 'get-nonce'

// O nonce desta resposta, que o handler Go (internal/webui) escreveu no
// <meta property="csp-nonce">. O navegador esconde o atributo de
// getAttribute (nonce hiding); a propriedade .nonce continua legível.
//
// Quem injeta <style> em runtime precisa dele, ou a CSP bloqueia o estilo:
// o react-remove-scroll (Dialog e AlertDialog do Radix) lê o nonce do
// get-nonce, e é para isso o setNonce abaixo. Sem ele, abrir um diálogo não
// trava a rolagem da página (spec do Plano 6, §8.1). Em desenvolvimento o
// valor é o marcador literal, sem efeito: o Vite não aplica CSP.
const meta = document.querySelector<HTMLMetaElement>('meta[property="csp-nonce"]')

export const nonce: string | undefined = meta?.nonce || undefined

if (nonce) setNonce(nonce)
