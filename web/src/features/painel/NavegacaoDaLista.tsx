import { ArrowLeft, ChevronLeft, ChevronRight } from 'lucide-react'
import { Link, useSearchParams } from 'react-router'
import { useHeartbeats, useResumo, useResumoFiltrado } from '@/lib/api/consultas'
import { lerFiltros, temFiltroDeApi } from './filtros'
import { buscaDaLista, linhasDaLista, vizinhosNaLista } from './lista'
import type { LinhaDeMonitor } from './linhas'
import { lerOrdem } from './ordem'

const LINK = 'alvo-de-toque inline-flex items-center gap-1 text-sm font-semibold text-primaria underline-offset-4 hover:underline'

// O topo do detalhe: a volta ao painel e, quando a lista da URL (filtros e
// ordem, que a tabela passa adiante) tem o monitor, o Anterior e o Próximo
// nela. Sem a resposta das consultas, só a volta: um vizinho chutado levaria
// a outro lugar que não o da lista na tela.
export function NavegacaoDaLista({ chave }: { chave: string }) {
  const [parametros] = useSearchParams()
  const busca = buscaDaLista(parametros)
  const linhas = useLinhasDaLista(parametros)
  const vizinhos = linhas && vizinhosNaLista(linhas, chave)
  return (
    <nav aria-label="Lista de monitores" className="flex flex-wrap items-center justify-between gap-4">
      {/* "Painel" visível, "Voltar ao painel" no nome acessível: não colide
          com a aba "Painel" da navegação. */}
      <Link to={{ pathname: '/', search: busca }} aria-label="Voltar ao painel" className={LINK}>
        <ArrowLeft aria-hidden="true" className="size-4" />
        Painel
      </Link>
      {vizinhos && (vizinhos.anterior || vizinhos.proxima) && (
        <div className="flex gap-4">
          {vizinhos.anterior && <LinkDoVizinho linha={vizinhos.anterior} busca={busca} sentido="anterior" />}
          {vizinhos.proxima && <LinkDoVizinho linha={vizinhos.proxima} busca={busca} sentido="proxima" />}
        </div>
      )}
    </nav>
  )
}

// O nome acessível começa pelo texto visível (WCAG 2.5.3) e diz para onde vai.
function LinkDoVizinho({ linha, busca, sentido }: { linha: LinhaDeMonitor; busca: string; sentido: 'anterior' | 'proxima' }) {
  const texto = sentido === 'anterior' ? 'Anterior' : 'Próximo'
  return (
    <Link to={{ pathname: linha.href, search: busca }} aria-label={`${texto}: ${linha.nome}`} className={LINK}>
      {sentido === 'anterior' && <ChevronLeft aria-hidden="true" className="size-4" />}
      {texto}
      {sentido === 'proxima' && <ChevronRight aria-hidden="true" className="size-4" />}
    </Link>
  )
}

function useLinhasDaLista(parametros: URLSearchParams): LinhaDeMonitor[] | null {
  const filtros = lerFiltros(parametros)
  const resumo = useResumo()
  const heartbeats = useHeartbeats()
  const filtraNaApi = temFiltroDeApi(filtros)
  const filtrada = useResumoFiltrado(filtros, filtraNaApi, { items: [] })
  // Os itens e o relógio vêm da mesma consulta, como na tabela do painel.
  const fonte = filtraNaApi ? filtrada : resumo
  const itens = filtraNaApi && filtrada.isPlaceholderData ? undefined : fonte.data?.items
  if (!itens || !heartbeats.data) return null
  return linhasDaLista({ itens, heartbeats: heartbeats.data.items }, filtros, lerOrdem(parametros), new Date(fonte.dataUpdatedAt))
}
