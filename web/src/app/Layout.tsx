import { NavLink, Outlet } from 'react-router'
import { ProvedorDeAnuncios } from '@/components/Anunciador'
import { AvisoDeContexto } from '@/components/AvisoDeContexto'
import { Botao } from '@/components/ui/botao'
import { useAbertos, useMe } from '@/lib/api/consultas'
import { useTema, type Tema } from '@/lib/tema'
import { cn } from '@/lib/utils'
import { useSessao } from './contexto'

const links = [
  { para: '/', texto: 'Painel', fim: true },
  { para: '/incidents', texto: 'Incidentes', contador: true },
  { para: '/channels', texto: 'Canais' },
  { para: '/status-pages', texto: 'Status page' },
  { para: '/settings', texto: 'Configurações' },
]

function SeletorDeTema() {
  const { tema, mudar } = useTema()
  return (
    <label className="flex items-center gap-2 text-sm">
      Tema
      <select
        value={tema}
        onChange={(e) => mudar(e.target.value as Tema)}
        className="rounded-md border border-borda bg-fundo px-2 py-1"
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
    <span aria-hidden="true" className="ml-1.5 rounded-full bg-fora px-1.5 text-xs font-semibold text-fundo">
      {n}
    </span>
  )
}

export function Layout() {
  const { sessao } = useSessao()
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
      <AvisoDeContexto />
      <header className="border-b border-borda">
        <div className="mx-auto flex max-w-6xl flex-wrap items-center gap-4 px-4 py-3">
          <span className="font-semibold">Vigilume</span>
          <nav aria-label="Principal" className="flex flex-wrap gap-1">
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
                  cn('rounded-md px-3 py-1.5 text-sm hover:bg-painel', isActive && 'bg-painel font-semibold')
                }
              >
                {l.texto}
                {l.contador && contador && <ContadorDeAbertos n={contador} />}
              </NavLink>
            ))}
          </nav>
          <div className="ml-auto flex items-center gap-3">
            <SeletorDeTema />
            {me.data && <span className="text-sm text-suave">{me.data.email}</span>}
            <Botao tipo="secundario" tamanho="pequeno" onClick={() => void sessao.sair()}>
              Sair
            </Botao>
          </div>
        </div>
      </header>
      <main id="conteudo" className="mx-auto max-w-6xl space-y-6 px-4 py-6">
        <Outlet />
      </main>
    </ProvedorDeAnuncios>
  )
}
