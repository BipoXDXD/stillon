import { Check } from 'lucide-react'
import { useId, type ReactNode } from 'react'
import { FormaDoEstado } from '@/components/SeloDeEstado'
import { Botao } from '@/components/ui/botao'
import { Entrada } from '@/components/ui/campo'
import { ESTADOS_DO_FILTRO, type EstadoDoFiltro } from '@/lib/api/cliente'
import type { ItemDoResumo } from '@/lib/api/schemas'
import { contarPorEstado, textoDoEstado } from '@/lib/estado'
import { cn } from '@/lib/utils'
import { alternar, contarTags, estadoExibidoDoFiltro, SEM_FILTROS, temFiltro, type Filtros } from './filtros'

// A barra acima da tabela. As contagens vêm do conjunto inteiro, nunca do
// filtrado: o botão "Fora do ar 2" continua lá com qualquer filtro ligado. Um
// estado sem alvos some, salvo se estiver ligado: senão não haveria como
// desligá-lo.
export function BarraDeFiltros({
  itens,
  filtros,
  quantosNaTabela,
  aoMudar,
}: {
  itens: readonly ItemDoResumo[]
  filtros: Filtros
  // null enquanto a resposta do filtro não chega.
  quantosNaTabela: number | null
  aoMudar: (f: Filtros) => void
}) {
  const idDaBusca = useId()
  const conta = contarPorEstado(itens)
  const quantosDoEstado = (e: EstadoDoFiltro) => conta[estadoExibidoDoFiltro(e)]
  const estados = ESTADOS_DO_FILTRO.filter((e) => quantosDoEstado(e) > 0 || filtros.estados.includes(e))
  const tags = contarTags(itens)
  const semResultado = quantosNaTabela === 0
  return (
    <section aria-label="Filtros" className="space-y-3">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-3">
        <div className="w-full sm:max-w-64">
          <label htmlFor={idDaBusca} className="sr-only">
            Buscar alvo ou tag
          </label>
          <Entrada
            id={idDaBusca}
            type="search"
            placeholder="Buscar alvo ou tag"
            maxLength={100}
            value={filtros.busca}
            onChange={(ev) => aoMudar({ ...filtros, busca: ev.target.value })}
          />
        </div>
        <Grupo nome="Estado">
          {estados.map((e) => (
            <BotaoDeFiltro
              key={e}
              ligado={filtros.estados.includes(e)}
              quantos={quantosDoEstado(e)}
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
              aoClicar={() => aoMudar({ ...filtros, tags: alternar(filtros.tags, tag) })}
            >
              {tag}
            </BotaoDeFiltro>
          ))}
        </Grupo>
      )}
      {temFiltro(filtros) && (
        <div className="flex flex-wrap items-center gap-3">
          <output className="text-meta text-suave">
            {quantosNaTabela === null ? 'Filtrando…' : `${quantosNaTabela} de ${itens.length} alvos`}
          </output>
          {!semResultado && (
            <Botao tipo="terciario" tamanho="pequeno" onClick={() => aoMudar(SEM_FILTROS)}>
              Limpar filtros
            </Botao>
          )}
        </div>
      )}
    </section>
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
  aoClicar,
  children,
}: {
  ligado: boolean
  quantos: number
  aoClicar: () => void
  children: ReactNode
}) {
  return (
    <Botao
      tipo="secundario"
      tamanho="pequeno"
      aria-pressed={ligado}
      onClick={aoClicar}
      className={cn(ligado && 'border-primaria bg-primaria-100 text-primaria hover:bg-primaria-100')}
    >
      {ligado && <Check aria-hidden="true" className="size-3.5" strokeWidth={2.5} />}
      {children}
      <span className="font-normal text-suave">{quantos}</span>
    </Botao>
  )
}
