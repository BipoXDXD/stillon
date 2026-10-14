import { Bell, LayoutDashboard, LogOut, Settings, ShieldCheck, TriangleAlert, type LucideIcon } from 'lucide-react'
import { useState } from 'react'
import { Link, Outlet, useLocation } from 'react-router'
import { ProvedorDeAnuncios } from '@/components/Anunciador'
import { AvisoDeContexto } from '@/components/AvisoDeContexto'
import { Marca } from '@/components/Marca'
import { useAvisar } from '@/components/ui/avisos'
import { Botao } from '@/components/ui/botao'
import { useAbertos, useMe } from '@/lib/api/consultas'
import { mensagemDe } from '@/lib/api/mensagens'
import { cn } from '@/lib/utils'
import { BotaoDeTema } from './BotaoDeTema'
import { useSessao } from './contexto'

// As cinco abas, com os mesmos nomes no topo (desktop) e na barra inferior
// (celular): o nome acessível não muda com a largura da tela (WCAG 3.2.4).
// `secoes`: os caminhos (e o que vem abaixo deles) em que a aba é a atual,
// além do próprio `para`. O detalhe, o cadastro e a edição de alvo saem do
// Painel; a raiz "/" não entra como prefixo, ou o Painel valeria em tudo.
type Aba = { para: string; texto: string; Icone: LucideIcon; secoes: string[]; contador?: boolean }

const ABAS: Aba[] = [
  { para: '/', texto: 'Painel', Icone: LayoutDashboard, secoes: ['/targets'] },
  { para: '/incidents', texto: 'Incidentes', Icone: TriangleAlert, secoes: ['/incidents'], contador: true },
  { para: '/channels', texto: 'Canais', Icone: Bell, secoes: ['/channels'] },
  { para: '/status-pages', texto: 'Status', Icone: ShieldCheck, secoes: ['/status-pages'] },
  { para: '/settings', texto: 'Conta', Icone: Settings, secoes: ['/settings'] },
]

function ehAAtual(aba: Aba, caminho: string): boolean {
  return caminho === aba.para || aba.secoes.some((s) => caminho === s || caminho.startsWith(`${s}/`))
}

export function Layout() {
  const me = useMe()
  return (
    <ProvedorDeAnuncios>
      <a
        href="#conteudo"
        className="sr-only focus:not-sr-only focus:absolute focus:top-2 focus:left-2 focus:z-50 focus:rounded-padrao focus:bg-superficie focus:p-2"
      >
        Pular para o conteúdo
      </a>
      <AvisoDeContexto />
      <header className="border-b border-borda bg-superficie">
        <div className="mx-auto flex min-h-16 max-w-6xl items-center gap-4 px-4 sm:px-6 lg:gap-8">
          <Marca />
          <NavegacaoPrincipal />
          <div className="ml-auto flex min-w-0 items-center gap-2">
            {/* O e-mail some no celular (auditoria L1); a conta está na aba Conta. */}
            {me.data && <span className="hidden max-w-64 truncate text-meta text-apagado lg:inline">{me.data.email}</span>}
            <BotaoDeTema />
            <BotaoSair />
          </div>
        </div>
      </header>
      {/* pb-16 no celular: o fim do conteúdo fica acima da barra inferior
          fixa (53 px). */}
      <main id="conteudo" className="mx-auto min-w-0 max-w-6xl space-y-8 px-4 pt-8 pb-16 sm:px-6 lg:pt-12 lg:pb-12">
        <Outlet />
      </main>
    </ProvedorDeAnuncios>
  )
}

// No celular a navegação é a barra inferior fixa, de 5 abas de 44 px
// (auditoria L1: antes, ~265 px de menu vinham antes do conteúdo); a partir
// de lg (1024 px) ela volta ao fluxo do cabeçalho. Um <nav> só nos dois
// casos: a ordem do Tab e o nome da região não mudam com a largura. O item
// atual usa a primária 100 e o peso 600, e o aria-current diz o mesmo ao
// leitor de tela.
function NavegacaoPrincipal() {
  const { pathname } = useLocation()
  const abertos = useAbertos().data
  const contador = abertos && abertos !== '0' ? abertos : null
  return (
    <nav
      aria-label="Principal"
      className="fixed inset-x-0 bottom-0 z-30 border-t border-borda bg-superficie px-2 py-1 lg:static lg:z-auto lg:border-t-0 lg:bg-transparent lg:p-0"
    >
      <ul className="grid grid-cols-5 gap-1 lg:flex">
        {ABAS.map((aba) => {
          const atual = ehAAtual(aba, pathname)
          return (
            <li key={aba.para}>
              <Link
                to={aba.para}
                aria-current={atual ? 'page' : undefined}
                aria-label={
                  aba.contador && contador ? `${aba.texto}, ${contador} ${contador === '1' ? 'aberto' : 'abertos'}` : undefined
                }
                className={cn(
                  'flex min-h-11 flex-col items-center justify-center gap-1 rounded-padrao px-2 text-xs text-suave hover:text-texto',
                  'lg:flex-row lg:gap-2 lg:px-3 lg:text-sm',
                  atual && 'bg-primaria-100 font-semibold text-primaria hover:text-primaria',
                )}
              >
                {/* No celular, o contador fica na linha do ícone, ao lado
                    dele; no desktop, lg:contents solta os dois na linha do
                    link e o order põe o contador depois do texto. Sempre em
                    fluxo, com gap: nunca por cima do texto. */}
                <span className="flex items-center gap-1 lg:contents">
                  <aba.Icone aria-hidden="true" className="size-5 lg:size-4" />
                  {aba.contador && contador && <ContadorDeAbertos n={contador} />}
                </span>
                <span>{aba.texto}</span>
              </Link>
            </li>
          )
        })}
      </ul>
    </nav>
  )
}

// O número de incidentes abertos junto da aba "Incidentes" (spec §4.9). Zero
// não aparece. O número é decorativo para o leitor de tela: o nome do link já
// diz "Incidentes, 2 abertos".
function ContadorDeAbertos({ n }: { n: string }) {
  return (
    <span aria-hidden="true" className="rounded-full bg-perigo px-1 text-xs font-semibold text-perigo-texto lg:order-last">
      {n}
    </span>
  )
}

// Ocupado enquanto o logout voa (até o prazo da sessão): um segundo clique
// não manda outro. aria-disabled, e não disabled, para o foco não cair no
// <body>.
function BotaoSair() {
  const { sessao } = useSessao()
  const avisar = useAvisar()
  const [saindo, setSaindo] = useState(false)
  return (
    <Botao
      tipo="terciario"
      tamanho="pequeno"
      aria-disabled={saindo || undefined}
      onClick={async () => {
        if (saindo) return
        setSaindo(true)
        try {
          await sessao.sair()
        } catch (e) {
          // O servidor não fez o logout (A9 do Plano 10): a sessão
          // continua, e o Sair pode ser tentado de novo.
          avisar(`Não foi possível sair. ${mensagemDe(e)}`, 'erro')
        } finally {
          setSaindo(false)
        }
      }}
    >
      <LogOut aria-hidden="true" className="size-4" />
      Sair
    </Botao>
  )
}
