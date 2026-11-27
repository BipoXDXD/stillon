import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import type { Tema } from '@/lib/tema'

// O widget do Cloudflare Turnstile (cadastro e "esqueci a senha"). O api.js
// vem da Cloudflare, liberado na CSP do painel (internal/webui, script-src e
// frame-src). O token resolvido vale 300 s e uma verificação só: o servidor
// o confere no siteverify e não o guarda.

export type TemaDoTurnstile = 'light' | 'dark' | 'auto'
type TamanhoDoTurnstile = 'compact' | 'flexible'
export type ResultadoDoTurnstile = { fase: 'aguardando' | 'falhou' } | { fase: 'resolvido'; token: string }

export type OpcoesDoTurnstile = {
  sitekey: string
  action: string
  callback: (token: string) => void
  'expired-callback': () => void
  'error-callback': () => void
  theme: TemaDoTurnstile
  language: string
  size: TamanhoDoTurnstile
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

export const ID_DA_FALHA_DO_TURNSTILE = 'falha-da-verificacao'
const FALHA = 'Não foi possível carregar a verificação. Desative bloqueadores para este site e recarregue a página.'
// A Cloudflare documenta 300 px como largura mínima do modo flexível.
const MINIMO_DO_FLEXIVEL_PX = 300

// Mudar `reiniciar` reseta o widget depois de uma tentativa que gastou o token.
export function Turnstile({
  siteKey,
  acao,
  tema,
  aoMudar,
  falhou,
  reiniciar,
}: {
  siteKey: string
  acao: 'signup' | 'forgot'
  tema: TemaDoTurnstile
  aoMudar: (resultado: ResultadoDoTurnstile) => void
  falhou: boolean
  reiniciar: number
}) {
  const alvo = useRef<HTMLDivElement>(null)
  const id = useRef<string | undefined>(undefined)
  const [tamanho, setTamanho] = useState<TamanhoDoTurnstile | null>(null)
  // O callback mais recente, sem recriar o widget a cada render de quem usa.
  const mudar = useRef(aoMudar)
  useEffect(() => {
    mudar.current = aoMudar
  })

  useLayoutEffect(() => {
    const elemento = alvo.current
    if (!elemento) return
    const medir = () => setTamanho(elemento.clientWidth < MINIMO_DO_FLEXIVEL_PX ? 'compact' : 'flexible')
    medir()
    const observador = new ResizeObserver(medir)
    observador.observe(elemento)
    return () => observador.disconnect()
  }, [])

  // Chave, ação, tema e tamanho são opções de montagem do widget.
  useEffect(() => {
    if (tamanho === null) return
    let vivo = true
    function sinalizar(resultado: ResultadoDoTurnstile) {
      if (vivo) mudar.current(resultado)
    }
    function falharMontagem() {
      sinalizar({ fase: 'falhou' })
      vivo = false
    }

    void carregarScript().then(() => {
      if (!vivo || !alvo.current) return
      id.current = window.turnstile?.render(alvo.current, {
        sitekey: siteKey,
        action: acao,
        callback: (token) => sinalizar({ fase: 'resolvido', token }),
        'expired-callback': () => sinalizar({ fase: 'aguardando' }),
        'error-callback': () => sinalizar({ fase: 'falhou' }),
        theme: tema,
        language: 'pt-br',
        size: tamanho,
        appearance: 'always',
      }) || undefined
      if (id.current === undefined) falharMontagem()
    }).catch(falharMontagem)

    return () => {
      vivo = false
      if (id.current !== undefined) window.turnstile?.remove(id.current)
      id.current = undefined
      mudar.current({ fase: 'aguardando' })
    }
  }, [siteKey, acao, tema, tamanho])

  const anterior = useRef(reiniciar)
  useEffect(() => {
    if (reiniciar === anterior.current) return
    anterior.current = reiniciar
    mudar.current({ fase: 'aguardando' })
    if (id.current !== undefined) window.turnstile?.reset(id.current)
  }, [reiniciar])

  return (
    <div className="space-y-2">
      {/* O iframe da Cloudflare traz título próprio; o grupo diz o que é. */}
      <fieldset className="min-w-0">
        <legend className="sr-only">Verificação de segurança</legend>
        <div ref={alvo} className="w-full" />
      </fieldset>
      <p id={ID_DA_FALHA_DO_TURNSTILE} aria-live="assertive" aria-atomic="true" className="text-sm text-perigo empty:sr-only">
        {falhou ? FALHA : null}
      </p>
    </div>
  )
}
