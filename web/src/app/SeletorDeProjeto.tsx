import { ChevronDown } from 'lucide-react'
import { useRef, useState } from 'react'
import { useLocation, useNavigate } from 'react-router'
import { useAnunciar } from '@/components/Anunciador'
import { Botao } from '@/components/ui/botao'
import {
  ConteudoDoMenu,
  GatilhoDoMenu,
  GrupoDeOpcoesDoMenu,
  ItemDoMenu,
  Menu,
  OpcaoDoMenu,
  SeparadorDoMenu,
} from '@/components/ui/menu'
import { DialogoNovoProjeto } from '@/features/projetos/DialogoNovoProjeto'
import { useProjetos } from '@/lib/api/consultas'
import { mensagemDe } from '@/lib/api/mensagens'
import { ATRASO_DO_SINAL_MS, useAposUmTempo } from '@/lib/atraso'
import { caminhoNoProjeto, useProjetoDaMoldura } from './projeto'

// O projeto da moldura e a troca de projeto. O botão mostra o nome (Inter
// 600, truncado em 12 rem no desktop) e o menu lista os projetos na ordem do
// servidor, com o atual marcado, e o "Novo projeto". O papel e o selo
// "Padrão" ficam para o PR 4, com as ações por papel e o renomear.
//
// Estados do botão: a lista vindo (só fora das telas de projeto, como na
// conta; nelas, a tela toda espera) guarda a largura e, passados 300 ms,
// diz "Carregando…", sem abrir; a lista em erro diz "Projetos
// indisponíveis", e o menu diz o porquê e tenta de novo; sem projeto na
// moldura (a lista vazia, ou o da URL não é da pessoa), "Escolher projeto".
export function SeletorDeProjeto() {
  const projetos = useProjetos()
  const atual = useProjetoDaMoldura()
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const anunciar = useAnunciar()
  const [aberto, setAberto] = useState(false)
  const [criando, setCriando] = useState(false)
  const botao = useRef<HTMLButtonElement>(null)
  const conteudo = useRef<HTMLDivElement>(null)
  // Quem fecha o menu para levar o foco a outro lugar (o <h1> da tela do
  // outro projeto, o campo do diálogo) não deixa o Radix devolvê-lo ao botão.
  const semDevolverFoco = useRef(false)

  const items = projetos.data?.items
  const carregando = items === undefined && projetos.isPending
  const mostrarCarregando = useAposUmTempo(carregando, ATRASO_DO_SINAL_MS)

  function escolher(id: string) {
    const projeto = items?.find((p) => p.id === id)
    if (!projeto || id === atual?.id) return
    semDevolverFoco.current = true
    // A seção pode ter o mesmo nome antes e depois: sem o anúncio, a troca
    // passaria em silêncio.
    anunciar(`Projeto ${projeto.name}.`)
    void navigate(caminhoNoProjeto(pathname, id))
  }

  // Sem projeto escolhido, o nome acessível é o próprio texto visível
  // (WCAG 2.5.3). Na carga, o botão tem nome desde o início, mesmo antes de o
  // "Carregando…" aparecer.
  let texto = 'Escolher projeto'
  let nome: string | undefined
  if (carregando) {
    texto = mostrarCarregando ? 'Carregando…' : ''
    nome = 'Projeto: carregando'
  } else if (items === undefined) {
    texto = 'Projetos indisponíveis'
  } else if (atual) {
    texto = atual.name
    nome = `Projeto: ${atual.name}`
  }

  return (
    <>
      <Menu
        open={aberto}
        onOpenChange={(v) => {
          if (!carregando) setAberto(v)
        }}
      >
        <GatilhoDoMenu asChild>
          <Botao
            ref={botao}
            tipo="terciario"
            aria-label={nome}
            aria-disabled={carregando || undefined}
            className="w-full min-w-32 justify-between px-3 lg:w-auto"
          >
            <span className={carregando ? 'truncate text-apagado' : 'truncate lg:max-w-48'}>{texto}</span>
            <ChevronDown aria-hidden="true" className="size-4 shrink-0" />
          </Botao>
        </GatilhoDoMenu>
        <ConteudoDoMenu
          ref={conteudo}
          align="start"
          className="w-[calc(100vw-2rem)] lg:w-auto lg:max-w-xs"
          // O foco abre no projeto atual, e não no primeiro da lista. Ao
          // abrir, o Radix foca a caixa e, pelo teclado, passa ao primeiro
          // item; o preventDefault aqui, que roda antes, fica com o marcado.
          onFocus={(e) => {
            if (e.target !== e.currentTarget) return
            const marcado = conteudo.current?.querySelector<HTMLElement>('[role="menuitemradio"][aria-checked="true"]')
            if (!marcado) return
            e.preventDefault()
            marcado.focus()
          }}
          onCloseAutoFocus={(e) => {
            if (!semDevolverFoco.current) return
            semDevolverFoco.current = false
            e.preventDefault()
          }}
          // O Radix segura o Tab dentro do menu; aqui ele fecha, e o foco
          // volta ao botão.
          onKeyDown={(e) => {
            if (e.key === 'Tab') setAberto(false)
          }}
        >
          {items === undefined ? (
            <>
              <p className="px-3 py-2 text-sm text-suave">{mensagemDe(projetos.error)}</p>
              <ItemDoMenu onSelect={() => void projetos.refetch()}>Tentar de novo</ItemDoMenu>
            </>
          ) : (
            <>
              {items.length > 0 && (
                <>
                  <GrupoDeOpcoesDoMenu value={atual?.id ?? ''} onValueChange={escolher}>
                    {items.map((p) => (
                      <OpcaoDoMenu key={p.id} value={p.id}>
                        <span className="min-w-0 break-words">{p.name}</span>
                      </OpcaoDoMenu>
                    ))}
                  </GrupoDeOpcoesDoMenu>
                  <SeparadorDoMenu />
                </>
              )}
              <ItemDoMenu
                onSelect={() => {
                  semDevolverFoco.current = true
                  setCriando(true)
                }}
              >
                Novo projeto
              </ItemDoMenu>
            </>
          )}
        </ConteudoDoMenu>
      </Menu>
      <DialogoNovoProjeto aberto={criando} aoFechar={() => setCriando(false)} reserva={botao} />
    </>
  )
}
