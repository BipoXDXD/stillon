import { zodResolver } from '@hookform/resolvers/zod'
import { useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { useFieldArray, useForm, useWatch, type FieldPath } from 'react-hook-form'
import { ArrowDown, ArrowUp } from 'lucide-react'
import { Link } from 'react-router'
import { z } from 'zod'
import { useApi } from '@/app/contexto'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { Titulo } from '@/components/Titulo'
import { useAvisar } from '@/components/ui/avisos'
import { Botao, BotaoQueSome } from '@/components/ui/botao'
import { estiloDeCartao } from '@/components/ui/cartao'
import { useCopiar } from '@/components/ui/copiar'
import { CaixaDeMarcar, Campo, Entrada, ErroDoFormulario, Selecao } from '@/components/ui/campo'
import { chaves, useAlvos, usePaginas, useResumo } from '@/lib/api/consultas'
import { errosDosCampos, type ErroDoCampo } from '@/lib/api/erros-de-campo'
import { ApiError, type ErroDeCampo, naoEncontrado } from '@/lib/api/http'
import { mensagemDe, mensagemDoCampo } from '@/lib/api/mensagens'
import type { Alvo, PaginaDeStatus } from '@/lib/api/schemas'
import { MENSAGEM_INVISIVEL, temInvisivel } from '@/lib/rotulo'
import { cn } from '@/lib/utils'
import { PreviaDaPagina } from './PreviaDaPagina'

// O CHECK de status_pages.slug.
const SLUG = /^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$/
const MAX_ITENS = 20

const esquema = z.object({
  title: z
    .string()
    .trim()
    .min(1, 'Obrigatório.')
    .refine((s) => [...s].length <= 100, 'Até 100 caracteres.')
    .refine((s) => !temInvisivel(s), MENSAGEM_INVISIVEL),
  slug: z.string().regex(SLUG, 'De 3 a 40 caracteres: letras minúsculas, números e hífens, sem hífen nas pontas.'),
  published: z.boolean(),
  items: z
    .array(
      z.object({
        target_id: z.string().min(1),
        display_name: z
          .string()
          .trim()
          .min(1, 'Obrigatório.')
          .refine((s) => [...s].length <= 100, 'Até 100 caracteres.')
          .refine((s) => !temInvisivel(s), MENSAGEM_INVISIVEL),
      }),
    )
    .max(MAX_ITENS, `Até ${MAX_ITENS} alvos.`),
})
type Valores = z.infer<typeof esquema>

// O campo do 400 que a tela não tem vai para o erro do topo do formulário.
const NO_TOPO = 'topo'

// Onde cai um campo do 400: title e slug, o nome de exibição de cada item
// (items[1].display_name vira items.1.display_name), o resto da lista em items.
function campoDoErro(fe: ErroDeCampo): ErroDoCampo<FieldPath<Valores> | typeof NO_TOPO> {
  const mensagem = mensagemDoCampo(fe.field, fe.reason)
  const item = /^items\[(\d+)\]\.display_name$/.exec(fe.field)
  if (fe.field === 'title' || fe.field === 'slug') return { campo: fe.field, mensagem }
  if (item) return { campo: `items.${Number(item[1])}.display_name`, mensagem }
  if (fe.field === 'items' || fe.field.startsWith('items[')) return { campo: 'items', mensagem }
  return { campo: NO_TOPO, mensagem }
}

export function sugerirSlug(titulo: string): string {
  return titulo
    .normalize('NFD')
    .replace(/\p{M}/gu, '')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 40)
    .replace(/-+$/, '')
}

function Formulario({ pagina, alvos }: { pagina: PaginaDeStatus | null; alvos: Alvo[] }) {
  const api = useApi()
  const qc = useQueryClient()
  const avisar = useAvisar()
  const resumo = useResumo()
  const [erro, setErro] = useState<string | null>(null)
  const [escolhido, setEscolhido] = useState('')
  const f = useForm<Valores>({
    resolver: zodResolver(esquema),
    defaultValues: pagina
      ? { title: pagina.title, slug: pagina.slug, published: pagina.published, items: pagina.items }
      : { title: '', slug: '', published: false, items: [] },
  })
  const itens = useFieldArray({ control: f.control, name: 'items' })
  const e = f.formState.errors
  const nomeDoAlvo = new Map(alvos.map((a) => [a.id, a.name]))
  const escolhidos = useWatch({ control: f.control, name: 'items' })
  const tituloAtual = useWatch({ control: f.control, name: 'title' })
  const usados = new Set(escolhidos.map((i) => i.target_id))
  const livres = alvos.filter((a) => !usados.has(a.id))
  // O "Tirar" some com a linha, e o foco iria ao <body>. Vai ao "Tirar"
  // que ocupou o lugar (o da linha de baixo), ou ao da última linha, ou ao
  // seletor de acrescentar, ou ao título da tela. Um efeito a cada render:
  // o que o pede é o render da remoção.
  const lista = useRef<HTMLFieldSetElement>(null)
  const seletorDeAlvo = useRef<HTMLSelectElement>(null)
  const focarDepoisDeTirar = useRef<number | null>(null)
  useEffect(() => {
    const i = focarDepoisDeTirar.current
    if (i === null) return
    focarDepoisDeTirar.current = null
    const tirar = [...(lista.current?.querySelectorAll<HTMLElement>('[data-tirar]') ?? [])]
    const destino =
      tirar[Math.min(i, tirar.length - 1)] ??
      lista.current?.querySelector<HTMLElement>('select') ??
      document.querySelector<HTMLElement>('main h1')
    destino?.focus()
  })

  async function enviar(v: Valores) {
    setErro(null)
    try {
      if (pagina) await api.alterarPagina(pagina.id, v)
      else await api.criarPagina(v)
      await qc.invalidateQueries({ queryKey: chaves.paginas })
      avisar('Status page salva. A página pública muda em até 30 segundos.')
    } catch (err) {
      if (err instanceof ApiError && err.code === 'slug_taken') {
        f.setError('slug', { message: 'Este endereço já está em uso.' }, { shouldFocus: true })
      } else if (naoEncontrado(err)) {
        // A página sumiu entre carregar e salvar. Como no 409 abaixo, o
        // aviso vai pelo toast: a recarga remonta o formulário.
        avisar('A status page já tinha sido apagada, talvez em outra aba.')
        await qc.invalidateQueries({ queryKey: chaves.paginas })
      } else if (err instanceof ApiError && err.code === 'status_page_exists') {
        // Corrida entre abas: a recarga troca a `key` do formulário e o
        // remonta, então o aviso sai pelo toast, que sobrevive à troca.
        avisar('Já existe uma status page; recarregamos a que está salva.')
        await qc.invalidateQueries({ queryKey: chaves.paginas })
      } else if (err instanceof ApiError && err.fields.length) {
        // Esta tela não move o foco, ao contrário do alvo e do heartbeat.
        for (const { campo, mensagem } of errosDosCampos(err, campoDoErro)) {
          if (campo === NO_TOPO) setErro(mensagem)
          else f.setError(campo, { message: mensagem })
        }
      } else {
        setErro(mensagemDe(err))
      }
    }
  }

  return (
    <form noValidate className={cn(estiloDeCartao, 'max-w-2xl space-y-6')} onSubmit={f.handleSubmit(enviar)}>
      <ErroDoFormulario>{erro}</ErroDoFormulario>
      <Campo rotulo="Título" erro={e.title?.message}>
        {(a) => (
          <Entrada
            {...a}
            {...f.register('title', {
              onBlur: (ev: { target: { value: string } }) => {
                if (f.getValues('slug')) return
                const sugestao = sugerirSlug(ev.target.value)
                // Sugestão vazia ou curta demais não acusa erro antes de a pessoa mexer no campo.
                if (sugestao) f.setValue('slug', sugestao, { shouldValidate: sugestao.length >= 3 })
              },
            })}
          />
        )}
      </Campo>
      <Campo
        rotulo="Endereço (slug)"
        erro={e.slug?.message}
        ajuda="De 3 a 40 caracteres: letras minúsculas, números e hífens."
      >
        {(a) => <Entrada {...a} autoCapitalize="none" spellCheck={false} {...f.register('slug')} />}
      </Campo>
      <CaixaDeMarcar {...f.register('published')}>Publicada (visível para qualquer pessoa com o link)</CaixaDeMarcar>

      <fieldset ref={lista} className="space-y-3">
        <legend className="text-sm font-semibold">
          Alvos na página{itens.fields.length > 0 && ` (${itens.fields.length} de ${MAX_ITENS})`}
        </legend>
        {alvos.length === 0 && itens.fields.length === 0 && (
          // Nova aba, porque salvar um alvo leva ao detalhe dele e não há volta
          // para cá: o que já foi digitado neste formulário fica como está.
          <div className="space-y-2">
            <p className="text-sm text-suave">Você ainda não tem alvos para mostrar nesta página.</p>
            {/* Na volta da nova aba, o alvo novo chega e o link some: o foco
                vai ao seletor, que nasce no mesmo commit. */}
            <BotaoQueSome tipo="secundario" tamanho="pequeno" asChild reserva={seletorDeAlvo}>
              <Link to="/targets/new" target="_blank" rel="noopener">
                Cadastrar primeiro alvo (abre em nova aba)
              </Link>
            </BotaoQueSome>
          </div>
        )}
        {itens.fields.map((it, i) => (
          <div key={it.id} className="grid grid-cols-1 items-end gap-3 sm:grid-cols-[minmax(0,1fr)_auto] rounded-padrao border border-borda p-3">
            <Campo
              rotulo={`Nome de exibição de ${nomeDoAlvo.get(it.target_id) ?? 'alvo apagado'}`}
              erro={e.items?.[i]?.display_name?.message}
            >
              {(a) => <Entrada {...a} {...f.register(`items.${i}.display_name`)} />}
            </Campo>
            {/* Ordem por botões, não arrastar: teclado e leitor de tela.
                Reordenar é secundário; Tirar, que remove, é terciário.
                aria-disabled, e não disabled, nas pontas:
                o botão que leva o item à ponta está com o foco, e
                desabilitado o perderia para o <body> (como o Reconhecer).
                A guarda fica no clique. */}
            <div className="flex flex-wrap items-center gap-2">
              <Botao
                tipo="secundario"
                tamanho="icone"
                title="Subir"
                aria-disabled={i === 0 || undefined}
                onClick={() => {
                  if (i > 0) itens.move(i, i - 1)
                }}
                aria-label={`Subir ${nomeDoAlvo.get(it.target_id) ?? 'item'}`}
              >
                <ArrowUp aria-hidden="true" className="size-5" />
              </Botao>
              <Botao
                tipo="secundario"
                tamanho="icone"
                title="Descer"
                aria-disabled={i === itens.fields.length - 1 || undefined}
                onClick={() => {
                  if (i < itens.fields.length - 1) itens.move(i, i + 1)
                }}
                aria-label={`Descer ${nomeDoAlvo.get(it.target_id) ?? 'item'}`}
              >
                <ArrowDown aria-hidden="true" className="size-5" />
              </Botao>
              <Botao
                tipo="terciario"
                tamanho="pequeno"
                data-tirar=""
                onClick={() => {
                  focarDepoisDeTirar.current = i
                  itens.remove(i)
                }}
                aria-label={`Tirar ${nomeDoAlvo.get(it.target_id) ?? 'item'} da página`}
              >
                Tirar
              </Botao>
            </div>
          </div>
        ))}
        {(e.items?.message ?? e.items?.root?.message) && (
          <p className="text-sm text-perigo">{e.items?.message ?? e.items?.root?.message}</p>
        )}
        {livres.length > 0 && itens.fields.length < MAX_ITENS && (
          <div className="flex flex-wrap items-end gap-2">
            <Campo rotulo="Acrescentar alvo">
              {(a) => (
                <Selecao {...a} ref={seletorDeAlvo} value={escolhido} onChange={(ev) => setEscolhido(ev.target.value)}>
                  <option value="">Escolha…</option>
                  {livres.map((al) => (
                    <option key={al.id} value={al.id}>
                      {al.name}
                    </option>
                  ))}
                </Selecao>
              )}
            </Campo>
            <Botao
              tipo="secundario"
              disabled={!escolhido}
              onClick={() => {
                itens.append({ target_id: escolhido, display_name: nomeDoAlvo.get(escolhido) ?? '' })
                setEscolhido('')
              }}
            >
              Acrescentar
            </Botao>
          </div>
        )}
      </fieldset>

      {escolhidos.length > 0 && (
        <PreviaDaPagina
          titulo={tituloAtual}
          resumo={resumo.data ? 'pronto' : resumo.isError ? 'erro' : 'carregando'}
          itens={escolhidos.map((it) => {
            const r = resumo.data?.items.find((x) => x.id === it.target_id)
            return { id: it.target_id, nome: it.display_name, estado: r?.state ?? null, ativo: r?.enabled ?? true }
          })}
        />
      )}

      <Botao type="submit" disabled={f.formState.isSubmitting}>
        {pagina ? 'Salvar' : 'Criar status page'}
      </Botao>
    </form>
  )
}

export function PaginaStatusPage() {
  const paginas = usePaginas()
  const alvos = useAlvos()
  const { copiado, copiar } = useCopiar({
    ok: 'Link copiado.',
    falha: 'Não foi possível copiar. Selecione o link e copie à mão.',
  })
  return (
    <>
      <Titulo aba="Status page">Status page</Titulo>
      <EstadoDaConsulta consulta={paginas}>
        {(d) => {
          const pagina = d.items[0] ?? null
          return (
            <>
              {pagina && (
                <div className={cn(estiloDeCartao, 'max-w-2xl space-y-2')}>
                  <p className="text-sm font-semibold">Link público</p>
                  <p className="break-all">{pagina.public_url}</p>
                  {pagina.published ? (
                    <div className="flex flex-wrap gap-2">
                      <Botao
                        tipo="secundario"
                        tamanho="pequeno"
                        onClick={() => copiar(pagina.public_url)}
                      >
                        {copiado ? 'Copiado' : 'Copiar'}
                      </Botao>
                      <Botao tipo="secundario" tamanho="pequeno" asChild>
                        <a href={pagina.public_url} target="_blank" rel="noopener noreferrer">
                          Abrir
                        </a>
                      </Botao>
                    </div>
                  ) : (
                    <p className="text-sm text-suave">A página só fica acessível depois de publicada.</p>
                  )}
                </div>
              )}
              <EstadoDaConsulta consulta={alvos}>
                {(a) => <Formulario key={pagina?.id ?? 'nova'} pagina={pagina} alvos={a.items} />}
              </EstadoDaConsulta>
            </>
          )
        }}
      </EstadoDaConsulta>
    </>
  )
}
