import { zodResolver } from '@hookform/resolvers/zod'
import { useState } from 'react'
import { flushSync } from 'react-dom'
import { useFieldArray, useForm, useWatch, type FieldErrors } from 'react-hook-form'
import { Botao } from '@/components/ui/botao'
import { estiloDeCartao } from '@/components/ui/cartao'
import { CaixaDeMarcar, Campo, Entrada, ErroDoFormulario, Opcao, Selecao } from '@/components/ui/campo'
import { ApiError } from '@/lib/api/http'
import { mensagemDe, mensagemDoCampo } from '@/lib/api/mensagens'
import { avancadoPersonalizado, campoDoServidor, esquemaDoAlvo, INTERVALOS, OUTRO, type ValoresDoAlvo } from './esquema'
import { cn } from '@/lib/utils'

// O que fica em "Avançado" (DI, formulário único com o raro recolhido): o
// comum é nome, URL, tags, intervalo e ativo.
const CAMPOS_AVANCADOS = ['method', 'timeout_ms', 'expected_status', 'lentidao', 'degraded_threshold_ms', 'modoHeaders', 'headers'] as const

// Um erro escondido não se corrige: com erro num campo avançado, a seção abre.
function erroNoAvancado(erros: FieldErrors<ValoresDoAlvo>): boolean {
  return CAMPOS_AVANCADOS.some((campo) => erros[campo] !== undefined)
}

export function FormularioAlvo({
  iniciais,
  nomesDeHeaders,
  aoEnviar,
  rotuloDoBotao,
}: {
  iniciais: ValoresDoAlvo
  // Na edição: os nomes dos headers já definidos (os valores nunca voltam).
  nomesDeHeaders?: string[]
  aoEnviar: (v: ValoresDoAlvo) => Promise<void>
  rotuloDoBotao: string
}) {
  const editando = nomesDeHeaders !== undefined
  // Na edição, o esquema conhece a base (a URL guardada, headers vazios).
  const [esquema] = useState(() => esquemaDoAlvo(editando ? iniciais : undefined))
  const f = useForm<ValoresDoAlvo>({ resolver: zodResolver(esquema), defaultValues: iniciais })
  const headers = useFieldArray({ control: f.control, name: 'headers' })
  const [erroGeral, setErroGeral] = useState<string | null>(null)
  // Na edição, um alvo com o raro fora do padrão já abre com ele à vista.
  const [avancadoAberto, setAvancadoAberto] = useState(() => avancadoPersonalizado(iniciais, nomesDeHeaders ?? []))
  const e = f.formState.errors
  const avancadoVisivel = avancadoAberto || erroNoAvancado(e)
  const lentidao = useWatch({ control: f.control, name: 'lentidao' })
  const modoHeaders = useWatch({ control: f.control, name: 'modoHeaders' })
  const intervaloEscolhido = useWatch({ control: f.control, name: 'intervaloEscolhido' })
  // Um erro da lista de headers inteira: com linhas registradas, o resolver o
  // põe em headers.root; sem nenhuma, em headers.
  const erroDosHeaders = e.headers?.message ?? e.headers?.root?.message

  async function enviar(v: ValoresDoAlvo) {
    setErroGeral(null)
    try {
      await aoEnviar(v)
    } catch (err) {
      // O 400 com fields vai para o campo certo; o resto, para o topo.
      if (err instanceof ApiError && err.fields.length) {
        let primeiro: keyof ValoresDoAlvo | null = null
        // flushSync: um erro avançado abre o "Avançado" já neste commit, e o
        // foco, logo depois, acha o campo à vista (num <details> fechado, o
        // navegador não o foca).
        flushSync(() => {
          for (const fe of err.fields) {
            const campo = campoDoServidor[fe.field.replace(/\[.*$/, '')]
            if (!campo) continue
            f.setError(campo, { message: mensagemDoCampo(fe.field, fe.reason) })
            primeiro ??= campo
          }
        })
        if (primeiro) {
          // Com um intervalo da lista, o campo em segundos não está na tela:
          // o erro aparece (e o foco vai) no seletor.
          const noSeletor = primeiro === 'interval_sec' && f.getValues('intervaloEscolhido') !== OUTRO
          f.setFocus(noSeletor ? 'intervaloEscolhido' : primeiro)
          return
        }
      }
      setErroGeral(mensagemDe(err))
    }
  }

  const numero = { valueAsNumber: true } as const
  return (
    <form
      noValidate
      className={cn(estiloDeCartao, 'max-w-2xl space-y-6')}
      // Num erro de validação, o erro abre o "Avançado" no render seguinte, e
      // o react-hook-form repete o foco num setTimeout, que já o acha à vista.
      onSubmit={f.handleSubmit(enviar)}
    >
      <ErroDoFormulario>{erroGeral}</ErroDoFormulario>
      <Campo rotulo="Nome" erro={e.name?.message}>
        {(a) => <Entrada {...a} {...f.register('name')} />}
      </Campo>
      <Campo rotulo="URL" erro={e.url?.message} ajuda="http:// ou https://. Endereços internos não são aceitos.">
        {(a) => <Entrada {...a} type="url" inputMode="url" {...f.register('url')} />}
      </Campo>
      <Campo rotulo="Tags" erro={e.tagsTexto?.message} ajuda="Separadas por vírgula, até 8. Ex.: prod, api, cliente-a.">
        {(a) => <Entrada {...a} {...f.register('tagsTexto')} />}
      </Campo>
      <div className="grid gap-4 sm:grid-cols-2">
        <Campo rotulo="Intervalo" erro={intervaloEscolhido === OUTRO ? undefined : e.interval_sec?.message}>
          {(a) => (
            <Selecao
              {...a}
              {...f.register('intervaloEscolhido', {
                // Um intervalo da lista vira o número; "outro" mantém o que
                // estava, para o campo em segundos começar dele.
                onChange: (ev: { target: { value: string } }) => {
                  if (ev.target.value !== OUTRO)
                    f.setValue('interval_sec', Number(ev.target.value), { shouldDirty: true })
                },
              })}
            >
              {INTERVALOS.map(([s, t]) => (
                <option key={s} value={s}>
                  {t}
                </option>
              ))}
              <option value={OUTRO}>Outro…</option>
            </Selecao>
          )}
        </Campo>
        {intervaloEscolhido === OUTRO && (
          <Campo rotulo="Intervalo em segundos" erro={e.interval_sec?.message} ajuda="De 30 a 86400 (1 dia).">
            {(a) => <Entrada {...a} type="number" min={30} max={86400} {...f.register('interval_sec', numero)} />}
          </Campo>
        )}
      </div>

      <CaixaDeMarcar {...f.register('enabled')}>Ativo (sondar este alvo)</CaixaDeMarcar>

      {/* <details>: o navegador cuida de abrir e fechar pelo teclado, e o
          leitor de tela diz se está aberto. */}
      <details
        open={avancadoVisivel}
        onToggle={(ev) => setAvancadoAberto(ev.currentTarget.open)}
        className="rounded-padrao border border-borda p-4"
      >
        <summary className="flex min-h-11 cursor-pointer items-center font-semibold">
          Avançado
          <span className="ml-2 text-sm font-normal text-suave">método, timeout, status, lentidão e headers</span>
        </summary>
        <div className="space-y-6 pt-4">
          <div className="grid gap-4 sm:grid-cols-3">
            <Campo rotulo="Método" erro={e.method?.message}>
              {(a) => (
                <Selecao {...a} {...f.register('method')}>
                  <option value="GET">GET</option>
                  <option value="HEAD">HEAD</option>
                  <option value="POST">POST</option>
                </Selecao>
              )}
            </Campo>
            <Campo rotulo="Timeout (ms)" erro={e.timeout_ms?.message}>
              {(a) => <Entrada {...a} type="number" min={1000} max={30000} {...f.register('timeout_ms', numero)} />}
            </Campo>
            <Campo rotulo="Status HTTP esperado" erro={e.expected_status?.message}>
              {(a) => <Entrada {...a} type="number" min={100} max={599} {...f.register('expected_status', numero)} />}
            </Campo>
          </div>

          <fieldset className="space-y-2">
            <legend className="text-sm font-semibold">Limiar de lentidão</legend>
            <p className="text-sm text-suave">Acima dele, uma resposta boa conta como “degradada”.</p>
            {(
              [
                ['padrao', 'Padrão (2000 ms)'],
                ['personalizado', 'Personalizado'],
                ['desativado', 'Desativado'],
              ] as const
            ).map(([v, t]) => (
              <Opcao key={v} value={v} {...f.register('lentidao')}>
                {t}
              </Opcao>
            ))}
            {lentidao === 'personalizado' && (
              <Campo rotulo="Limiar (ms)" erro={e.degraded_threshold_ms?.message}>
                {(a) => (
                  <Entrada {...a} type="number" min={1} max={60000} {...f.register('degraded_threshold_ms', numero)} />
                )}
              </Campo>
            )}
          </fieldset>

          <fieldset className="space-y-3">
            <legend className="text-sm font-semibold">Headers</legend>
            <p className="text-sm text-suave">
              Os headers não acompanham redirecionamento para outro host ou porta, nem de https para http.
            </p>
            {editando && (
              <>
                <p className="text-sm">
                  {nomesDeHeaders.length
                    ? `Headers definidos: ${nomesDeHeaders.join(', ')} (valores ocultos).`
                    : 'Nenhum header definido.'}
                </p>
                <div className="flex flex-wrap gap-x-6">
                  {(
                    [
                      ['manter', 'Manter como está'],
                      ['substituir', 'Substituir todos'],
                      ['remover', 'Remover todos'],
                    ] as const
                  ).map(([v, t]) => (
                    <Opcao key={v} value={v} {...f.register('modoHeaders')}>
                      {t}
                    </Opcao>
                  ))}
                </div>
              </>
            )}
            {modoHeaders === 'substituir' && (
              <div className="space-y-2">
                {headers.fields.map((h, i) => (
                  <div key={h.id} className="grid grid-cols-1 items-end gap-3 rounded-padrao border border-borda p-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto]">
                    <Campo rotulo={`Nome do header ${i + 1}`} erro={e.headers?.[i]?.nome?.message}>
                      {(a) => <Entrada {...a} {...f.register(`headers.${i}.nome`)} />}
                    </Campo>
                    <Campo rotulo={`Valor do header ${i + 1}`} erro={e.headers?.[i]?.valor?.message}>
                      {(a) => <Entrada {...a} {...f.register(`headers.${i}.valor`)} />}
                    </Campo>
                    <Botao tipo="terciario" onClick={() => headers.remove(i)} aria-label={`Remover o header ${i + 1}`}>
                      Remover
                    </Botao>
                  </div>
                ))}
                {erroDosHeaders && <p className="text-sm text-perigo">{erroDosHeaders}</p>}
                <Botao tipo="secundario" tamanho="pequeno" onClick={() => headers.append({ nome: '', valor: '' })}>
                  Adicionar header
                </Botao>
              </div>
            )}
          </fieldset>
        </div>
      </details>

      <Botao type="submit" disabled={f.formState.isSubmitting}>
        {rotuloDoBotao}
      </Botao>
    </form>
  )
}
