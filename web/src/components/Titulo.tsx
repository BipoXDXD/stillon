import { useEffect, useRef, type ReactNode } from 'react'

// A primeira tela da carga da página não recebe o foco: o primeiro Tab é o
// "Pular para o conteúdo" (spec do Plano 6 §4.9). Toda tela seguinte — uma
// navegação dentro do painel — recebe (§3.7). Um valor do módulo: uma carga
// de página, uma primeira tela.
let primeiraTela = true

// Para os testes, que montam o painel várias vezes no mesmo módulo.
export function esquecerAPrimeiraTela() {
  primeiraTela = true
}

// O <h1> de cada tela. Ao montar, muda o título da aba e, depois de uma
// navegação, recebe o foco: o React Router troca a tela sem recarregar, e
// sem isso o leitor de tela não percebe a navegação (spec do Plano 6 §3.7).
// Dois efeitos: o título da aba acompanha `aba`; o foco é da montagem, e uma
// `aba` que muda depois (a mesma tela com outro nome) não o rouba.
// descritoPor: o id de um texto que o leitor de tela lê junto com o título
// quando ele recebe o foco (o motivo do fim da sessão, no login).
export function Titulo({ children, aba, descritoPor }: { children: ReactNode; aba: string; descritoPor?: string }) {
  const ref = useRef<HTMLHeadingElement>(null)
  // Decidido uma vez por montagem: o StrictMode roda o efeito duas vezes, e
  // a segunda não pode achar que já não é a primeira tela.
  const focar = useRef<boolean | null>(null)
  useEffect(() => {
    document.title = `${aba} — Vigilume`
  }, [aba])
  useEffect(() => {
    if (focar.current === null) {
      focar.current = !primeiraTela
      primeiraTela = false
    }
    if (focar.current) ref.current?.focus()
  }, [])
  return (
    <h1 ref={ref} tabIndex={-1} aria-describedby={descritoPor} className="text-2xl font-semibold outline-none">
      {children}
    </h1>
  )
}
