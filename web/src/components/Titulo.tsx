import { useEffect, useRef, type ReactNode } from 'react'
import { useProjetoSeHouver } from '@/app/projeto'

// A primeira tela da carga da página não recebe o foco: o primeiro Tab é o
// "Pular para o conteúdo". Toda tela seguinte — uma
// navegação dentro do painel — recebe (§3.7). Um valor do módulo: uma carga
// de página, uma primeira tela.
let primeiraTela = true

// Para os testes, que montam o painel várias vezes no mesmo módulo.
export function esquecerAPrimeiraTela() {
  primeiraTela = true
}

// O <h1> de cada tela. Ao montar, muda o título da aba e, depois de uma
// navegação, recebe o foco: o React Router troca a tela sem recarregar, e
// sem isso o leitor de tela não percebe a navegação.
// Dois efeitos: o título da aba acompanha `aba`; o foco é da montagem, e uma
// `aba` que muda depois (a mesma tela com outro nome) não o rouba.
// descritoPor: o id de um texto que o leitor de tela lê junto com o título
// quando ele recebe o foco (o motivo do fim da sessão, no login).
// Numa tela de projeto, o título da aba leva o nome dele ("Incidentes —
// Clientes — StillOn"): duas abas do navegador em projetos diferentes não
// ficam iguais. semProjeto: a tela que não é do projeto, mesmo dentro da rota
// dele (a página não encontrada).
export function Titulo({
  children,
  aba,
  descritoPor,
  semProjeto = false,
}: {
  children: ReactNode
  aba: string
  descritoPor?: string
  semProjeto?: boolean
}) {
  const ref = useRef<HTMLHeadingElement>(null)
  // Decidido uma vez por montagem: o StrictMode roda o efeito duas vezes, e
  // a segunda não pode achar que já não é a primeira tela.
  const focar = useRef<boolean | null>(null)
  const projeto = useProjetoSeHouver()
  const doProjeto = semProjeto ? undefined : projeto?.nome
  useEffect(() => {
    document.title = doProjeto ? `${aba} — ${doProjeto} — StillOn` : `${aba} — StillOn`
  }, [aba, doProjeto])
  useEffect(() => {
    if (focar.current === null) {
      // O foco no <main> só chega lá pela reserva (TITULO_DA_TELA): quem o
      // tinha sumiu antes de a tela ter <h1> (o splash, enquanto a lista de
      // projetos carrega). Ele espera por este título, mesmo na primeira
      // tela; no <body>, a regra da primeira tela vale (titulo.test.tsx).
      focar.current = !primeiraTela || document.activeElement?.tagName === 'MAIN'
      primeiraTela = false
    }
    if (focar.current) ref.current?.focus()
  }, [])
  return (
    <h1 ref={ref} tabIndex={-1} aria-describedby={descritoPor} className="font-display text-3xl font-semibold outline-none sm:text-4xl">
      {children}
    </h1>
  )
}
