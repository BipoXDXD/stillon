import { zodResolver } from '@hookform/resolvers/zod'
import { type ChangeEvent, useState } from 'react'
import { flushSync } from 'react-dom'
import { useFieldArray, useForm, useWatch, type FieldErrors } from 'react-hook-form'
import { Botao } from '@/components/ui/botao'
import { estiloDeCartao } from '@/components/ui/cartao'
import { CaixaDeMarcar, Campo, Entrada, ErroDoFormulario, Opcao, Selecao } from '@/components/ui/campo'
import { errosDosCampos } from '@/lib/api/erros-de-campo'
import { ApiError } from '@/lib/api/http'
import { mensagemDe, mensagemDoCampo } from '@/lib/api/mensagens'
import {
  avancadoPersonalizado,
  campoDoServidor,
  esquemaDoAlvo,
  INTERVALOS,
  OUTRO,
  REGRA_COM_HEAD,
  type ValoresDoAlvo,
} from './esquema'
import { cn } from '@/lib/utils'

const CAMPOS_DA_REGRA: readonly (keyof ValoresDoAlvo)[] = ['modoDaRegra', 'valorDaRegra', 'ponteiroDaRegra']

// O que fica em "Avançado" (DI, formulário único com o raro recolhido): o
// comum é nome, URL, tags, intervalo e ativo. A verificação de conteúdo
// (regra) também mora ali.
const CAMPOS_AVANCADOS: readonly (keyof ValoresDoAlvo)[] = [
  'method',
  'timeout_ms',
  'expected_status',
  'lentidao',
  'degraded_threshold_ms',
  'modoHeaders',
  'headers',
  ...CAMPOS_DA_REGRA,
]

// Um erro escondido não se corrige: com erro num campo avançado, a seção abre.
function erroNoAvancado(erros: FieldErrors<ValoresDoAlvo>): boolean {
  return CAMPOS_AVANCADOS.some((campo) => erros[campo] !== undefined)
}

function ehCampoAvancado(campo: keyof ValoresDoAlvo): boolean {
  return CAMPOS_AVANCADOS.includes(campo)
}

const AVISO_DE_SEGREDO = 'Não use segredo na regra: o valor fica guardado sem criptografia, no banco e no backup.'

const ROTULO_DO_VALOR = {
  contains: 'Texto que deve aparecer',
  not_contains: 'Texto que não pode aparecer',
  json_equals: 'Valor esperado',
} as const

const AJUDA_DO_VALOR = {
  contains: AVISO_DE_SEGREDO,
  not_contains: AVISO_DE_SEGREDO,
  json_equals: `Compara o texto do valor, sem olhar o tipo: 1 e "1" são iguais. Números são comparados como escritos no JSON. ${AVISO_DE_SEGREDO}`,
} as const

// O campo do formulário de um campo do 400 (headers[2].nome vira headers).
const campoDoErro = (campo: string): keyof ValoresDoAlvo | undefined => campoDoServidor[campo.replace(/\[.*$/, '')]

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
  // Na edição, o esquema conhece a base: a URL e os nomes dos headers guardados.
  const [esquema] = useState(() => esquemaDoAlvo(editando ? { url: iniciais.url, nomesDeHeaders } : undefined))
  const f = useForm<ValoresDoAlvo>({ resolver: zodResolver(esquema), defaultValues: iniciais })
  const headers = useFieldArray({ control: f.control, name: 'headers' })
  const [erroGeral, setErroGeral] = useState<string | null>(null)
  // Na edição, um alvo com o raro fora do padrão já abre com ele à vista.
  // O <details> é controlado de verdade: a prop open só segue este estado, e o
  // toggle o sincroniza com o que o usuário faz. Derivar open do erro deixava a
  // prop presa em true depois que o usuário fechava a seção, e o React não a
  // reabria no reenvio.
  // A regra de conteúdo mora no mesmo "Avançado": um alvo que já tem regra
  // também abre com a seção à vista.
  const [avancadoAberto, setAvancadoAberto] = useState(
    () => avancadoPersonalizado(iniciais, nomesDeHeaders ?? []) || iniciais.modoDaRegra !== 'desligada',
  )
  const e = f.formState.errors
  const lentidao = useWatch({ control: f.control, name: 'lentidao' })
  const modoHeaders = useWatch({ control: f.control, name: 'modoHeaders' })
  const intervaloEscolhido = useWatch({ control: f.control, name: 'intervaloEscolhido' })
  const metodo = useWatch({ control: f.control, name: 'method' })
  const modoDaRegra = useWatch({ control: f.control, name: 'modoDaRegra' })
  // Um erro da lista de headers inteira: com linhas registradas, o resolver o
  // põe em headers.root; sem nenhuma, em headers.
  const erroDosHeaders = e.headers?.message ?? e.headers?.root?.message

  async function enviar(v: ValoresDoAlvo) {
    setErroGeral(null)
    try {
      await aoEnviar(v)
    } catch (err) {
      // O teto de regras por host (409) é da regra: a seção abre antes do
      // setError, porque o foco não entra num campo escondido.
      if (err instanceof ApiError && err.code === 'content_rule_host_limit_reached') {
        flushSync(() => setAvancadoAberto(true))
        f.setError('valorDaRegra', { message: mensagemDe(err) }, { shouldFocus: true })
        return
      }
      // O 400 com fields vai para o campo certo; o resto, para o topo.
      const erros = errosDosCampos(err, (fe) => {
        const campo = campoDoErro(fe.field)
        return campo && { campo, mensagem: mensagemDoCampo(fe.field, fe.reason) }
      })
      const primeiro = erros[0]?.campo
      if (primeiro) {
        // flushSync: um erro avançado abre o "Avançado" já neste commit, e o
        // foco, logo depois, acha o campo à vista (num <details> fechado, o
        // navegador não o foca).
        flushSync(() => {
          for (const { campo, mensagem } of erros) {
            f.setError(campo, { message: mensagem })
            if (ehCampoAvancado(campo)) setAvancadoAberto(true)
          }
        })
        // Com um intervalo da lista, o campo em segundos não está na tela:
        // o erro aparece (e o foco vai) no seletor.
        const noSeletor = primeiro === 'interval_sec' && f.getValues('intervaloEscolhido') !== OUTRO
        f.setFocus(noSeletor ? 'intervaloEscolhido' : primeiro)
        return
      }
      setErroGeral(mensagemDe(err))
    }
  }

  // Num erro de validação, o react-hook-form chama isto e só depois foca o
  // primeiro campo com erro: o flushSync abre o "Avançado" antes do foco.
  function abrirOAvancadoComErro(erros: FieldErrors<ValoresDoAlvo>) {
    if (erroNoAvancado(erros)) flushSync(() => setAvancadoAberto(true))
  }

  const numero = { valueAsNumber: true } as const
  return (
    <form
      noValidate
      className={cn(estiloDeCartao, 'max-w-2xl space-y-6')}
      onSubmit={f.handleSubmit(enviar, abrirOAvancadoComErro)}
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
        open={avancadoAberto}
        onToggle={(ev) => setAvancadoAberto(ev.currentTarget.open)}
        className="rounded-padrao border border-borda p-4"
      >
        {/* min-h-12, e não 11: com a seção aberta, o conteúdo encosta no
            summary, e o hit-test do Chromium arredonda o ponto da borda de
            baixo (a 21,5 px do centro) para o pixel do conteúdo. */}
        <summary className="flex min-h-12 cursor-pointer items-center font-semibold">
          Avançado
          <span className="ml-2 text-sm font-normal text-suave">método, timeout, status, lentidão, headers e conteúdo</span>
        </summary>
        <div className="space-y-6 pt-4">
          <div className="grid gap-4 sm:grid-cols-3">
            <Campo rotulo="Método" erro={e.method?.message}>
              {(a) => (
                <Selecao
                  {...a}
                  {...f.register('method', {
                    // Com HEAD a regra não vale: um erro dela não fica no seletor travado.
                    onChange: (ev: ChangeEvent<HTMLSelectElement>) => {
                      if (ev.target.value === 'HEAD') f.clearErrors(CAMPOS_DA_REGRA)
                    },
                  })}
                >
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

          <fieldset className="space-y-4">
            <legend className="text-sm font-semibold">Verificar o conteúdo da resposta</legend>
            <Campo
              rotulo="Verificação"
              erro={e.modoDaRegra?.message}
              ajuda={metodo === 'HEAD' ? REGRA_COM_HEAD : 'Compara bytes em UTF-8 nos primeiros 64 KiB da resposta.'}
            >
              {(a) =>
                // Com HEAD, um seletor travado em "Desligada" toma o lugar do
                // registrado: a regra fica no estado (I1 da revisão) e volta
                // como estava quando o método deixa de ser HEAD. As keys forçam
                // um <select> novo: no mesmo nó, o RHF não reescreveria o valor.
                metodo === 'HEAD' ? (
                  <Selecao key="travado" {...a} disabled value="desligada">
                    <OpcoesDaRegra />
                  </Selecao>
                ) : (
                  <Selecao key="regra" {...a} {...f.register('modoDaRegra')}>
                    <OpcoesDaRegra />
                  </Selecao>
                )
              }
            </Campo>
            {metodo !== 'HEAD' && modoDaRegra !== 'desligada' && (
              <Campo rotulo={ROTULO_DO_VALOR[modoDaRegra]} erro={e.valorDaRegra?.message} ajuda={AJUDA_DO_VALOR[modoDaRegra]}>
                {(a) => <Entrada {...a} {...f.register('valorDaRegra')} />}
              </Campo>
            )}
            {metodo !== 'HEAD' && modoDaRegra === 'json_equals' && (
              <Campo rotulo="Ponteiro JSON" erro={e.ponteiroDaRegra?.message} ajuda="O caminho até o campo, como /status.">
                {(a) => <Entrada {...a} placeholder="/status" {...f.register('ponteiroDaRegra')} />}
              </Campo>
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

function OpcoesDaRegra() {
  return (
    <>
      <option value="desligada">Desligada</option>
      <option value="contains">Contém</option>
      <option value="not_contains">Não contém</option>
      <option value="json_equals">JSON igual a</option>
    </>
  )
}
