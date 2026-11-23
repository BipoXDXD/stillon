import { useEffect, useRef, useState } from 'react'
import type { Tema } from '@/lib/tema'

// O widget do Cloudflare Turnstile (cadastro e "esqueci a senha"). O api.js
// vem da Cloudflare, liberado na CSP do painel (internal/webui, script-src e
// frame-src). O token resolvido vale 300 s e uma verificação só: o servidor
// o confere no siteverify e não o guarda.

export type TemaDoTurnstile = 'light' | 'dark' | 'auto'

export type OpcoesDoTurnstile = {
  sitekey: string
  action: string
  callback: (token: string) => void
  'expired-callback': () => void
  'error-callback': () => void
  theme: TemaDoTurnstile
  language: string
  size: 'flexible'
  appearance: 'always'
}

type ApiDoTurnstile = {
  render: (alvo: HTMLElement, o: OpcoesDoTurnstile) => string | undefined
  reset: (id: string) => void
  remove: (id: string) => void
}

declare global {
  interface Window {
    turnstile?: ApiDoTurnstile
  }
}

export const URL_DO_TURNSTILE = 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit'

// Uma carga por página, dividida por todos os widgets. Um script que falha
// (bloqueador) não se repete: o aviso pede para recarregar a página.
let carga: Promise<void> | null = null

// Para os testes, que montam o widget várias vezes no mesmo módulo.
export function esquecerOScriptDoTurnstile() {
  carga = null
}

function carregarScript(): Promise<void> {
  if (window.turnstile) return Promise.resolve()
  carga ??= new Promise<void>((resolve, reject) => {
    const s = document.createElement('script')
    s.src = URL_DO_TURNSTILE
    s.async = true
    s.addEventListener('load', () => resolve(), { once: true })
    s.addEventListener('error', () => reject(new Error('o api.js do Turnstile não carregou')), { once: true })
    document.head.append(s)
  })
  return carga
}

// O tema do painel no vocabulário do widget: "Sistema" é o auto da
// Cloudflare (prefers-color-scheme), como no painel.
export function temaDoTurnstile(t: Tema): TemaDoTurnstile {
  if (t === 'claro') return 'light'
  return t === 'escuro' ? 'dark' : 'auto'
}

const FALHA = 'Não foi possível carregar a verificação. Desative bloqueadores para este site e recarregue a página.'

// aoResolver recebe o token, ou null quando ele deixa de valer (expirou,
// erro, widget recriado ou reiniciado). Mudar `reiniciar` reseta o widget:
// depois de um envio recusado, o token já foi gasto.
export function Turnstile({
  siteKey,
  acao,
  tema,
  aoResolver,
  reiniciar,
}: {
  siteKey: string
  acao: 'signup' | 'forgot'
  tema: TemaDoTurnstile
  aoResolver: (token: string | null) => void
  reiniciar: number
}) {
  const alvo = useRef<HTMLDivElement>(null)
  const id = useRef<string | undefined>(undefined)
  const [falhou, setFalhou] = useState(false)
  // O callback mais recente, sem recriar o widget a cada render de quem usa.
  const resolver = useRef(aoResolver)
  useEffect(() => {
    resolver.current = aoResolver
  })

  // Recriado quando a chave, a ação ou o tema mudam (o tema do widget só se
  // escolhe no render).
  useEffect(() => {
    let vivo = true
    carregarScript().then(
      () => {
        if (!vivo || !alvo.current || !window.turnstile) return
        id.current = window.turnstile.render(alvo.current, {
          sitekey: siteKey,
          action: acao,
          callback: (token) => {
            setFalhou(false)
            resolver.current(token)
          },
          'expired-callback': () => resolver.current(null),
          'error-callback': () => {
            setFalhou(true)
            resolver.current(null)
          },
          theme: tema,
          language: 'pt-br',
          size: 'flexible',
          appearance: 'always',
        })
      },
      () => {
        if (vivo) setFalhou(true)
      },
    )
    return () => {
      vivo = false
      if (id.current !== undefined) window.turnstile?.remove(id.current)
      id.current = undefined
      resolver.current(null)
    }
  }, [siteKey, acao, tema])

  const anterior = useRef(reiniciar)
  useEffect(() => {
    if (reiniciar === anterior.current) return
    anterior.current = reiniciar
    if (id.current !== undefined) window.turnstile?.reset(id.current)
    resolver.current(null)
  }, [reiniciar])

  return (
    <div className="space-y-2">
      {/* O iframe da Cloudflare traz título próprio; o grupo diz o que é. */}
      <fieldset className="min-w-0">
        <legend className="sr-only">Verificação de segurança</legend>
        <div ref={alvo} />
      </fieldset>
      {falhou && <p className="text-sm text-perigo">{FALHA}</p>}
    </div>
  )
}
