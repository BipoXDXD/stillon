import { Activity, Bell, CircleUserRound, LayoutDashboard, LogOut, Settings, ShieldCheck, TriangleAlert } from 'lucide-react'
import { useState } from 'react'
import { NavLink, Outlet } from 'react-router'
import { ProvedorDeAnuncios } from '@/components/Anunciador'
import { AvisoDeContexto } from '@/components/AvisoDeContexto'
import { useAvisar } from '@/components/ui/avisos'
import { Botao } from '@/components/ui/botao'
import { useAbertos, useMe } from '@/lib/api/consultas'
import { mensagemDe } from '@/lib/api/mensagens'
import { useTema, type Tema } from '@/lib/tema'
import { cn } from '@/lib/utils'
import { useSessao } from './contexto'

const links = [
  { para: '/', texto: 'Painel', fim: true, Icone: LayoutDashboard },
  { para: '/incidents', texto: 'Incidentes', contador: true, Icone: TriangleAlert },
  { para: '/channels', texto: 'Canais', Icone: Bell },
  { para: '/status-pages', texto: 'Status page', Icone: ShieldCheck },
  { para: '/settings', texto: 'Configurações', Icone: Settings },
]

function SeletorDeTema() {
  const { tema, mudar } = useTema()
  return (
    <label className="flex items-center gap-2 text-sm text-suave">
      Tema
      <select
        value={tema}
        onChange={(e) => mudar(e.target.value as Tema)}
        className="min-h-10 rounded-lg border border-borda bg-superficie px-3 text-texto"
      >
        <option value="sistema">Sistema</option>
        <option value="claro">Claro</option>
        <option value="escuro">Escuro</option>
      </select>
    </label>
  )
}

// O número de incidentes abertos ao lado de "Incidentes" (spec §4.9). Zero
// não aparece. O número é decorativo para o leitor de tela: o nome do link
// já diz "Incidentes, 2 abertos".
function ContadorDeAbertos({ n }: { n: string }) {
  return (
    <span aria-hidden="true" className="ml-auto rounded-full bg-fora px-2 py-0.5 text-xs font-semibold text-fundo">
      {n}
    </span>
  )
}

export function Layout() {
  const { sessao } = useSessao()
  const avisar = useAvisar()
  const [saindo, setSaindo] = useState(false)
  const me = useMe()
  const abertos = useAbertos().data
  const contador = abertos && abertos !== '0' ? abertos : null
  return (
    <ProvedorDeAnuncios>
      <a
        href="#conteudo"
        className="sr-only focus:not-sr-only focus:absolute focus:top-2 focus:left-2 focus:z-50 focus:rounded focus:bg-fundo focus:p-2"
      >
        Pular para o conteúdo
      </a>
      {/* A lateral fixa ocupa 240 px só no desktop. O aviso compartilha
          o mesmo espaço do conteúdo; no login o componente não tem offset. */}
      <div className="lg:ml-60">
        <AvisoDeContexto />
      </div>
      <header className="border-b border-borda bg-superficie lg:fixed lg:inset-y-0 lg:left-0 lg:z-20 lg:w-60 lg:overflow-y-auto lg:border-r lg:border-b-0">
        <div className="flex min-w-0 flex-col gap-4 px-4 py-4 lg:h-full lg:gap-8 lg:px-5 lg:py-7">
          <div className="flex items-center gap-3">
            <span className="flex size-10 items-center justify-center rounded-xl bg-primaria text-primaria-texto"><Activity aria-hidden="true" className="size-6" /></span>
            <div><span className="text-xl font-semibold tracking-tight">StillOn</span><p className="text-xs text-suave">Monitor de disponibilidade</p></div>
          </div>
          <nav aria-label="Principal" className="flex flex-wrap gap-1 lg:flex-col lg:gap-2">
            {links.map((l) => (
              <NavLink
                key={l.para}
                to={l.para}
                end={l.fim}
                aria-label={
                  l.contador && contador
                    ? `${l.texto}, ${contador} ${contador === '1' ? 'aberto' : 'abertos'}`
                    : undefined
                }
                className={({ isActive }) =>
                  cn('inline-flex min-h-10 items-center gap-2 rounded-lg px-3 py-2 text-sm text-suave hover:bg-painel hover:text-texto lg:w-full', isActive && 'bg-painel font-semibold text-texto')
                }
              >
                <l.Icone aria-hidden="true" className="size-4 shrink-0" />
                {l.texto}
                {l.contador && contador && <ContadorDeAbertos n={contador} />}
              </NavLink>
            ))}
          </nav>
          <div className="flex min-w-0 flex-wrap items-center gap-3 border-t border-borda pt-4 lg:mt-auto lg:flex-col lg:items-stretch lg:gap-4">
            <SeletorDeTema />
            {me.data && <div className="flex min-w-0 items-center gap-2 lg:order-first"><CircleUserRound aria-hidden="true" className="size-4 shrink-0 text-suave" /><span className="min-w-0 break-all text-xs text-suave">{me.data.email}</span></div>}
            {/* Ocupado enquanto o logout voa (até o prazo da sessão): um
                segundo clique não manda outro. aria-disabled, e não disabled,
                para o foco não cair no <body>. */}
            <Botao
              tipo="secundario"
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
              <LogOut aria-hidden="true" className="size-4" />Sair
            </Botao>
          </div>
        </div>
      </header>
      <main id="conteudo" className="mx-auto min-w-0 max-w-[100rem] space-y-6 px-4 py-7 sm:px-6 lg:ml-60 lg:px-8 lg:py-10 xl:px-10">
        <Outlet />
      </main>
    </ProvedorDeAnuncios>
  )
}
