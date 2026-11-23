import { Bell, LayoutDashboard, LogOut, Settings, ShieldCheck, TriangleAlert, type LucideIcon } from 'lucide-react'
import { useEffect, useState } from 'react'
import { Link, Outlet, useLocation, useNavigation } from 'react-router'
import { ProvedorDeAnuncios, useAnunciar } from '@/components/Anunciador'
import { AvisoDeContexto } from '@/components/AvisoDeContexto'
import { AvisoDeRede } from '@/components/AvisoDeRede'
import { Marca } from '@/components/Marca'
import { useAvisar } from '@/components/ui/avisos'
import { Botao } from '@/components/ui/botao'
import { useTarefasQuePedemAtencao } from '@/features/heartbeats/tarefas-que-pedem-atencao'
import { useAbertos, useMe } from '@/lib/api/consultas'
import { mensagemDe } from '@/lib/api/mensagens'
import { ATRASO_DO_SINAL_MS, useAposUmTempo } from '@/lib/atraso'
import { cn } from '@/lib/utils'
import { BotaoDeTema } from './BotaoDeTema'
import { useSessao } from './contexto'
import { NoProjeto, useProjetoDaMoldura, useRotaDoProjeto } from './projeto'
import { SeletorDeProjeto } from './SeletorDeProjeto'
import { aoApontarPreCarregar, carregarConfiguracoes, carregarDetalhe, preCarregarEmOcioso } from './rotas-sob-demanda'

// As cinco abas, com os mesmos nomes no topo (desktop) e na barra inferior
// (celular): o nome acessível não muda com a largura da tela (WCAG 3.2.4).
// `secoes`: os caminhos (e o que vem abaixo deles) em que a aba é a atual,
// além do próprio `para`. O detalhe, o cadastro e a edição de alvo e de
// heartbeat saem do Painel; a raiz "/" não entra como prefixo, ou o Painel valeria em tudo.
// `para` é relativo ao projeto da moldura, menos na aba da conta (`daConta`),
// que fica fora de qualquer projeto.
type Aba = {
  para: `/${string}`
  texto: string
  Icone: LucideIcon
  secoes: string[]
  contador?: boolean
  daConta?: boolean
  preCarga?: () => Promise<unknown>
}

const ABAS: Aba[] = [
  { para: '/', texto: 'Painel', Icone: LayoutDashboard, secoes: ['/targets', '/heartbeats'] },
  { para: '/incidents', texto: 'Incidentes', Icone: TriangleAlert, secoes: ['/incidents'], contador: true },
  { para: '/channels', texto: 'Canais', Icone: Bell, secoes: ['/channels'] },
  { para: '/status-pages', texto: 'Status', Icone: ShieldCheck, secoes: ['/status-pages'] },
  { para: '/settings', texto: 'Conta', Icone: Settings, secoes: ['/settings'], daConta: true, preCarga: carregarConfiguracoes },
]

const ESPERA_ANTES_DE_ANUNCIAR_MS = 1000

// As telas de recurso moram em /projects/{id}/…: a aba é a mesma em qualquer
// projeto, e o prefixo sai antes de comparar.
function ehAAtual(aba: Aba, caminhoCompleto: string): boolean {
  const caminho = caminhoCompleto.replace(/^\/projects\/[^/]+(?=\/|$)/, '') || '/'
  return caminho === aba.para || aba.secoes.some((s) => caminho === s || caminho.startsWith(`${s}/`))
}

export function Layout() {
  const me = useMe()
  useEffect(() => preCarregarEmOcioso(carregarDetalhe, carregarConfiguracoes), [])
  return (
    <ProvedorDeAnuncios>
      <SinalDeCargaDeRota />
      <a
        href="#conteudo"
        className="sr-only focus:not-sr-only focus:absolute focus:top-2 focus:left-2 focus:z-50 focus:rounded-padrao focus:bg-superficie focus:p-2"
      >
        Pular para o conteúdo
      </a>
      <AvisoDeContexto />
      <AvisoDeRede />
      <header className="border-b border-borda bg-superficie">
        {/* O seletor de projeto, um só nos dois tamanhos: no desktop, entre
            a Marca e as abas, entre filetes (StillOn, o projeto, a seção
            dele); no celular, numa linha própria abaixo do cabeçalho, na
            largura toda (order-last + w-full na quebra do flex-wrap). A
            altura de 64 px da linha vem do grupo da direita. */}
        <div className="mx-auto flex max-w-6xl flex-wrap items-center gap-x-4 px-4 sm:px-6 lg:flex-nowrap lg:gap-x-8">
          <Marca />
          <div className="order-last -mx-4 flex w-full border-t border-borda px-1 sm:-mx-6 sm:px-3 lg:order-none lg:mx-0 lg:w-auto lg:min-w-0 lg:border-t-0 lg:border-x lg:px-4">
            <SeletorDeProjeto />
          </div>
          <NavegacaoPrincipal />
          <div className="ml-auto flex min-h-16 min-w-0 items-center gap-2">
            {/* O e-mail some no celular; a conta está na aba Conta. */}
            {me.data && <span className="hidden max-w-64 truncate text-meta text-apagado lg:inline">{me.data.email}</span>}
            <BotaoDeTema />
            <BotaoSair />
          </div>
        </div>
      </header>
      {/* pb-16 no celular: o fim do conteúdo fica acima da barra inferior
          fixa (53 px). */}
      {/* tabIndex -1: a última reserva de foco, para quando o que tinha o foco
          some e a tela ainda não tem <h1>. */}
      <main id="conteudo" tabIndex={-1} className="mx-auto min-w-0 max-w-6xl space-y-8 px-4 pt-8 pb-16 outline-none sm:px-6 lg:pt-12 lg:pb-12">
        <Outlet />
      </main>
    </ProvedorDeAnuncios>
  )
}

// A navegação espera o chunk de uma rota sob demanda com a tela antiga
// parada. Abaixo de 300 ms, nada aparece; o leitor de tela só ouve se a espera
// passar de 1 s. aria-hidden: o filete é só desenho.
function SinalDeCargaDeRota() {
  const carregando = useNavigation().state === 'loading'
  const mostrarFilete = useAposUmTempo(carregando, ATRASO_DO_SINAL_MS)
  const avisarEspera = useAposUmTempo(carregando, ESPERA_ANTES_DE_ANUNCIAR_MS)
  const anunciar = useAnunciar()
  useEffect(() => {
    if (avisarEspera) anunciar('Carregando a tela…')
  }, [avisarEspera, anunciar])
  if (!mostrarFilete) return null
  return (
    <div
      aria-hidden="true"
      data-testid="filete-de-carga"
      className="fixed inset-x-0 top-0 z-50 h-0.5 animate-pulse bg-primaria motion-reduce:animate-none"
    />
  )
}

// No celular a navegação é a barra inferior fixa, de 5 abas de 44 px
// (antes, ~265 px de menu vinham antes do conteúdo); a partir
// de lg (1024 px) ela volta ao fluxo do cabeçalho. Um <nav> só nos dois
// casos: a ordem do Tab e o nome da região não mudam com a largura. O item
// atual usa a primária 100 e o peso 600, e o aria-current diz o mesmo ao
// leitor de tela.
//
// As abas apontam para o projeto da moldura (o da URL, ou o que a raiz
// escolheria nas telas da conta) e o contador é o dele. Sem projeto (a lista
// ainda vem, ou o da URL não é da pessoa), as abas apontam para os caminhos
// de antes, que redirecionam, e não há contador: nenhuma consulta de recurso
// sai para um projeto que não é dela.
function NavegacaoPrincipal() {
  const projeto = useProjetoDaMoldura()
  if (!projeto) return <Abas rota={(para) => para} contador={null} />
  return (
    <NoProjeto projeto={projeto}>
      <AbasDoProjeto />
    </NoProjeto>
  )
}

function AbasDoProjeto() {
  const noProjeto = useRotaDoProjeto()
  const abertos = useAbertos(useTarefasQuePedemAtencao().length)
  const contador = abertos && abertos !== '0' ? abertos : null
  return <Abas rota={noProjeto} contador={contador} />
}

function Abas({ rota, contador }: { rota: (para: `/${string}`) => string; contador: string | null }) {
  const { pathname } = useLocation()
  // O destino da navegação em voo: o clique marca o item na hora, enquanto o
  // aria-current segue dizendo em que tela a pessoa ainda está.
  const destino = useNavigation().location?.pathname ?? pathname
  return (
    <nav
      aria-label="Principal"
      className="fixed inset-x-0 bottom-0 z-30 border-t border-borda bg-superficie px-2 py-1 lg:static lg:z-auto lg:border-t-0 lg:bg-transparent lg:p-0"
    >
      <ul className="grid grid-cols-5 gap-1 lg:flex">
        {ABAS.map((aba) => {
          const atual = ehAAtual(aba, pathname)
          const marcada = ehAAtual(aba, destino)
          return (
            <li key={aba.para}>
              <Link
                to={aba.daConta ? aba.para : rota(aba.para)}
                {...(aba.preCarga && aoApontarPreCarregar(aba.preCarga))}
                aria-current={atual ? 'page' : undefined}
                aria-label={
                  aba.contador && contador ? `${aba.texto}, ${contador} ${contador === '1' ? 'aberto' : 'abertos'}` : undefined
                }
                className={cn(
                  'flex min-h-11 flex-col items-center justify-center gap-1 rounded-padrao px-2 text-xs text-suave hover:text-texto',
                  'lg:flex-row lg:gap-2 lg:px-3 lg:text-sm',
                  marcada && 'bg-primaria-100 font-semibold text-primaria hover:text-primaria',
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
          // O servidor não fez o logout: a sessão
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
