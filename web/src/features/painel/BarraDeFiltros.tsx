import { Check } from 'lucide-react'
import { useEffect, useId, useRef, useState, type ReactNode, type RefObject } from 'react'
import { FormaDoEstado } from '@/components/SeloDeEstado'
import { BotaoQueSome } from '@/components/ui/botao'
import { Entrada } from '@/components/ui/campo'
import { ESTADOS_DO_FILTRO, type EstadoDoFiltro } from '@/lib/api/cliente'
import type { Heartbeat, ItemDoResumo } from '@/lib/api/schemas'
import { textoDoEstado, type EstadoExibido } from '@/lib/estado'
import { cn } from '@/lib/utils'
import { alternar, contarTags, estadoExibidoDoFiltro, SEM_FILTROS, temFiltro, type Filtros } from './filtros'

// A barra acima da tabela. As contagens vêm do conjunto inteiro, nunca do
// filtrado: o botão "Fora do ar 2" continua lá com qualquer filtro ligado. Um
// estado sem alvos some, salvo se estiver ligado: senão não haveria como
// desligá-lo. Quem some com o próprio clique manda o foco à busca.
export function BarraDeFiltros({
  itens,
  heartbeats,
  contagem,
  filtros,
  resultado,
  semResultado,
  busca,
  aoMudar,
}: {
  itens: readonly ItemDoResumo[]
  heartbeats: readonly Heartbeat[]
  contagem: Record<EstadoExibido, number>
  filtros: Filtros
  // "3 de 5 monitores", "Filtrando…", ou nada se a consulta falhou.
  resultado: string | null
  semResultado: boolean
  busca: RefObject<HTMLInputElement | null>
  aoMudar: (f: Filtros) => void
}) {
  const quantosDoEstado = (e: EstadoDoFiltro) => contagem[estadoExibidoDoFiltro(e)]
  const estados = ESTADOS_DO_FILTRO.filter((e) => quantosDoEstado(e) > 0 || filtros.estados.includes(e))
  const tags = contarTags([...itens, ...heartbeats])
  return (
    <section aria-label="Filtros" className="space-y-3">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-3">
        <CampoDeBusca ref={busca} valor={filtros.busca} aoMudar={(texto) => aoMudar({ ...filtros, busca: texto })} />
        <Grupo nome="Estado">
          {estados.map((e) => (
            <BotaoDeFiltro
              key={e}
              ligado={filtros.estados.includes(e)}
              quantos={quantosDoEstado(e)}
              reserva={busca}
              aoClicar={() => aoMudar({ ...filtros, estados: alternar(filtros.estados, e) })}
            >
              <FormaDoEstado estado={estadoExibidoDoFiltro(e)} />
              {textoDoEstado(estadoExibidoDoFiltro(e))}
            </BotaoDeFiltro>
          ))}
        </Grupo>
      </div>
      {tags.length > 0 && (
        <Grupo nome="Tags">
          {tags.map(({ tag, quantos }) => (
            <BotaoDeFiltro
              key={tag}
              ligado={filtros.tags.includes(tag)}
              quantos={quantos}
              reserva={busca}
              aoClicar={() => aoMudar({ ...filtros, tags: alternar(filtros.tags, tag) })}
            >
              {tag}
            </BotaoDeFiltro>
          ))}
        </Grupo>
      )}
      {temFiltro(filtros) && (
        <div className="flex flex-wrap items-center gap-3">
          <output className="text-meta text-suave">{resultado}</output>
          {!semResultado && (
            <BotaoQueSome tipo="terciario" tamanho="pequeno" reserva={busca} onClick={() => aoMudar(SEM_FILTROS)}>
              Limpar filtros
            </BotaoQueSome>
          )}
        </div>
      )}
    </section>
  )
}

// O texto do campo mora aqui, e a URL é o espelho. O router aplica a URL numa
// transição: um campo controlado por ela perderia o cursor e poderia perder
// tecla. `pendentes` guarda o que este campo mandou e ainda não voltou; um
// valor que chega fora dessa lista veio de fora (Limpar, navegação) e manda.
function CampoDeBusca({
  valor,
  aoMudar,
  ref,
}: {
  valor: string
  aoMudar: (texto: string) => void
  ref: RefObject<HTMLInputElement | null>
}) {
  const id = useId()
  const [texto, setTexto] = useState(valor)
  const pendentes = useRef<string[]>([])
  useEffect(() => {
    const i = pendentes.current.indexOf(valor)
    if (i >= 0) {
      pendentes.current.splice(0, i + 1)
      return
    }
    pendentes.current = []
    setTexto(valor)
  }, [valor])
  return (
    <div className="w-full sm:max-w-64">
      <label htmlFor={id} className="sr-only">
        Buscar por nome ou tag
      </label>
      <Entrada
        ref={ref}
        id={id}
        type="search"
        placeholder="Buscar por nome ou tag"
        maxLength={100}
        value={texto}
        onChange={(ev) => {
          setTexto(ev.target.value)
          pendentes.current.push(ev.target.value)
          aoMudar(ev.target.value)
        }}
      />
    </div>
  )
}

function Grupo({ nome, children }: { nome: string; children: ReactNode }) {
  return (
    <fieldset className="min-w-0 max-sm:w-full">
      <legend className="sr-only">{nome}</legend>
      {/* No celular, uma faixa que rola de lado: quebrar linha empurraria a
          tabela para a segunda tela. O respiro de 8 px, compensado pela margem
          negativa, é para o rolador não cortar o anel de foco. */}
      <div className="flex flex-wrap gap-2 max-sm:-m-2 max-sm:flex-nowrap max-sm:overflow-x-auto max-sm:p-2">{children}</div>
    </fieldset>
  )
}

// Ligado tem fundo, borda e check: nunca só a cor (DI 8).
function BotaoDeFiltro({
  ligado,
  quantos,
  reserva,
  aoClicar,
  children,
}: {
  ligado: boolean
  quantos: number
  reserva: RefObject<HTMLElement | null>
  aoClicar: () => void
  children: ReactNode
}) {
  return (
    <BotaoQueSome
      tipo="secundario"
      tamanho="pequeno"
      reserva={reserva}
      aria-pressed={ligado}
      onClick={aoClicar}
      className={cn(ligado && 'border-primaria bg-primaria-100 text-primaria hover:bg-primaria-100')}
    >
      {ligado && <Check aria-hidden="true" className="size-3.5" strokeWidth={2.5} />}
      {children}
      <span className="font-normal text-suave">{quantos}</span>
    </BotaoQueSome>
  )
}
