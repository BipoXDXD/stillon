import { zodResolver } from '@hookform/resolvers/zod'
import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useFieldArray, useForm, useWatch } from 'react-hook-form'
import { z } from 'zod'
import { useApi } from '@/app/contexto'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { estadoExibido, SeloDeEstado } from '@/components/SeloDeEstado'
import { Titulo } from '@/components/Titulo'
import { useAvisar } from '@/components/ui/avisos'
import { Botao } from '@/components/ui/botao'
import { Campo, Entrada, ErroDoFormulario, Selecao } from '@/components/ui/campo'
import { useAlvos, usePaginas, useResumo } from '@/lib/api/consultas'
import { ApiError } from '@/lib/api/http'
import { mensagemDe, mensagemDoCampo } from '@/lib/api/mensagens'
import type { Alvo, PaginaDeStatus } from '@/lib/api/schemas'

// O CHECK de status_pages.slug (Plano 4, D13).
const SLUG = /^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$/
const MAX_ITENS = 20

const esquema = z.object({
  title: z.string().trim().min(1, 'Obrigatório.').refine((s) => [...s].length <= 100, 'Até 100 caracteres.'),
  slug: z.string().regex(SLUG, 'De 3 a 40 caracteres: letras minúsculas, números e hífens, sem hífen nas pontas.'),
  published: z.boolean(),
  items: z
    .array(
      z.object({
        target_id: z.string().min(1),
        display_name: z.string().trim().min(1, 'Obrigatório.').refine((s) => [...s].length <= 100, 'Até 100 caracteres.'),
      }),
    )
    .max(MAX_ITENS, `Até ${MAX_ITENS} alvos.`),
})
type Valores = z.infer<typeof esquema>

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
  const usados = new Set(escolhidos.map((i) => i.target_id))
  const livres = alvos.filter((a) => !usados.has(a.id))

  async function enviar(v: Valores) {
    setErro(null)
    try {
      if (pagina) await api.alterarPagina(pagina.id, v)
      else await api.criarPagina(v)
      await qc.invalidateQueries({ queryKey: ['status-pages'] })
      avisar('Status page salva. A página pública muda em até 30 segundos.')
    } catch (err) {
      if (err instanceof ApiError && err.code === 'slug_taken') {
        f.setError('slug', { message: 'Este endereço já está em uso.' }, { shouldFocus: true })
      } else if (err instanceof ApiError && err.code === 'status_page_exists') {
        await qc.invalidateQueries({ queryKey: ['status-pages'] })
        setErro('Já existe uma status page; recarregamos a que está salva.')
      } else if (err instanceof ApiError && err.fields.length) {
        for (const fe of err.fields) {
          const raiz = fe.field.replace(/\[.*$/, '')
          if (raiz === 'title' || raiz === 'slug') f.setError(raiz, { message: mensagemDoCampo(fe.field, fe.reason) })
          else setErro(`${fe.field}: ${mensagemDoCampo(fe.field, fe.reason)}`)
        }
      } else {
        setErro(mensagemDe(err))
      }
    }
  }

  return (
    <form noValidate className="max-w-2xl space-y-5" onSubmit={f.handleSubmit(enviar)}>
      <ErroDoFormulario>{erro}</ErroDoFormulario>
      <Campo rotulo="Título" erro={e.title?.message}>
        {(a) => (
          <Entrada
            {...a}
            {...f.register('title', {
              onBlur: (ev: { target: { value: string } }) => {
                if (!f.getValues('slug')) f.setValue('slug', sugerirSlug(ev.target.value), { shouldValidate: true })
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
      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" {...f.register('published')} />
        Publicada (visível para qualquer pessoa com o link)
      </label>

      <fieldset className="space-y-3">
        <legend className="text-sm font-medium">Alvos na página ({itens.fields.length} de {MAX_ITENS})</legend>
        {itens.fields.map((it, i) => (
          <div key={it.id} className="grid grid-cols-[1fr_auto] items-end gap-2 rounded-md border border-borda p-3">
            <Campo
              rotulo={`Nome de exibição de ${nomeDoAlvo.get(it.target_id) ?? 'alvo apagado'}`}
              erro={e.items?.[i]?.display_name?.message}
            >
              {(a) => <Entrada {...a} {...f.register(`items.${i}.display_name`)} />}
            </Campo>
            {/* Ordem por botões, não arrastar: teclado e leitor de tela. */}
            <div className="flex gap-1">
              <Botao tipo="fantasma" tamanho="pequeno" disabled={i === 0} onClick={() => itens.move(i, i - 1)}
                aria-label={`Subir ${nomeDoAlvo.get(it.target_id) ?? 'item'}`}>
                Subir
              </Botao>
              <Botao tipo="fantasma" tamanho="pequeno" disabled={i === itens.fields.length - 1}
                onClick={() => itens.move(i, i + 1)} aria-label={`Descer ${nomeDoAlvo.get(it.target_id) ?? 'item'}`}>
                Descer
              </Botao>
              <Botao tipo="fantasma" tamanho="pequeno" onClick={() => itens.remove(i)}
                aria-label={`Tirar ${nomeDoAlvo.get(it.target_id) ?? 'item'} da página`}>
                Tirar
              </Botao>
            </div>
          </div>
        ))}
        {e.items?.message && <p className="text-sm text-perigo">{e.items.message}</p>}
        {livres.length > 0 && itens.fields.length < MAX_ITENS && (
          <div className="flex items-end gap-2">
            <Campo rotulo="Acrescentar alvo">
              {(a) => (
                <Selecao {...a} value={escolhido} onChange={(ev) => setEscolhido(ev.target.value)}>
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

      {/* Prévia aproximada (spec do Plano 6, D23): a página real é HTML do
          servidor, só acessível depois de publicada. */}
      {escolhidos.length > 0 && (
        <section aria-labelledby="titulo-previa" className="space-y-2 rounded-md border border-dashed border-borda p-4">
          <h2 id="titulo-previa" className="font-semibold">
            Prévia aproximada
          </h2>
          <ul className="space-y-1">
            {escolhidos.map((it) => {
              const r = resumo.data?.items.find((x) => x.id === it.target_id)
              return (
                <li key={it.target_id} className="flex justify-between gap-4 text-sm">
                  <span>{it.display_name || '—'}</span>
                  {r ? <SeloDeEstado estado={estadoExibido(r.state, r.enabled)} /> : <span className="text-suave">—</span>}
                </li>
              )
            })}
          </ul>
        </section>
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
  const [copiado, setCopiado] = useState(false)
  return (
    <>
      <Titulo aba="Status page">Status page</Titulo>
      <EstadoDaConsulta consulta={paginas}>
        {(d) => {
          const pagina = d.items[0] ?? null
          return (
            <>
              {pagina && (
                <div className="space-y-2 rounded-lg border border-borda p-4">
                  <p className="text-sm font-medium">Link público</p>
                  <p className="break-all">{pagina.public_url}</p>
                  {pagina.published ? (
                    <div className="flex gap-2">
                      <Botao
                        tipo="secundario"
                        tamanho="pequeno"
                        onClick={() => {
                          void navigator.clipboard?.writeText(pagina.public_url)
                          setCopiado(true)
                        }}
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
