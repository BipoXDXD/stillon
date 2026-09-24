import { zodResolver } from '@hookform/resolvers/zod'
import { useState } from 'react'
import { useFieldArray, useForm, useWatch } from 'react-hook-form'
import { Botao } from '@/components/ui/botao'
import { Campo, Entrada, ErroDoFormulario, Selecao } from '@/components/ui/campo'
import { ApiError } from '@/lib/api/http'
import { mensagemDe, mensagemDoCampo } from '@/lib/api/mensagens'
import { campoDoServidor, esquemaDoAlvo, INTERVALOS, OUTRO, type ValoresDoAlvo } from './esquema'

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
  const e = f.formState.errors
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
        let algum = false
        for (const fe of err.fields) {
          const campo = campoDoServidor[fe.field.replace(/\[.*$/, '')]
          if (campo) {
            // Com um intervalo da lista, o campo em segundos não está na
            // tela: o erro aparece (e o foco vai) no seletor.
            const noSeletor = campo === 'interval_sec' && f.getValues('intervaloEscolhido') !== OUTRO
            f.setError(campo, { message: mensagemDoCampo(fe.field, fe.reason) }, { shouldFocus: !algum && !noSeletor })
            if (noSeletor && !algum) f.setFocus('intervaloEscolhido')
            algum = true
          }
        }
        if (algum) return
      }
      setErroGeral(mensagemDe(err))
    }
  }

  const numero = { valueAsNumber: true } as const
  return (
    <form noValidate className="max-w-2xl space-y-5" onSubmit={f.handleSubmit(enviar)}>
      <ErroDoFormulario>{erroGeral}</ErroDoFormulario>
      <Campo rotulo="Nome" erro={e.name?.message}>
        {(a) => <Entrada {...a} {...f.register('name')} />}
      </Campo>
      <Campo rotulo="URL" erro={e.url?.message} ajuda="http:// ou https://. Endereços internos não são aceitos.">
        {(a) => <Entrada {...a} type="url" inputMode="url" {...f.register('url')} />}
      </Campo>
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
        <Campo rotulo="Timeout (ms)" erro={e.timeout_ms?.message}>
          {(a) => <Entrada {...a} type="number" min={1000} max={30000} {...f.register('timeout_ms', numero)} />}
        </Campo>
      </div>
      {intervaloEscolhido === OUTRO && (
        <Campo rotulo="Intervalo em segundos" erro={e.interval_sec?.message} ajuda="De 30 a 86400 (1 dia).">
          {(a) => <Entrada {...a} type="number" min={30} max={86400} {...f.register('interval_sec', numero)} />}
        </Campo>
      )}
      <Campo rotulo="Status HTTP esperado" erro={e.expected_status?.message}>
        {(a) => <Entrada {...a} type="number" min={100} max={599} {...f.register('expected_status', numero)} />}
      </Campo>

      <fieldset className="space-y-2">
        <legend className="text-sm font-medium">Limiar de lentidão</legend>
        <p className="text-sm text-suave">Acima dele, uma resposta boa conta como “degradada”.</p>
        {(
          [
            ['padrao', 'Padrão (2000 ms)'],
            ['personalizado', 'Personalizado'],
            ['desativado', 'Desativado'],
          ] as const
        ).map(([v, t]) => (
          <label key={v} className="flex items-center gap-2 text-sm">
            <input type="radio" value={v} {...f.register('lentidao')} />
            {t}
          </label>
        ))}
        {lentidao === 'personalizado' && (
          <Campo rotulo="Limiar (ms)" erro={e.degraded_threshold_ms?.message}>
            {(a) => (
              <Entrada {...a} type="number" min={1} max={60000} {...f.register('degraded_threshold_ms', numero)} />
            )}
          </Campo>
        )}
      </fieldset>

      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" {...f.register('enabled')} />
        Ativo (sondar este alvo)
      </label>

      <fieldset className="space-y-3">
        <legend className="text-sm font-medium">Headers</legend>
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
            <div className="flex flex-wrap gap-4">
              {(
                [
                  ['manter', 'Manter como está'],
                  ['substituir', 'Substituir todos'],
                  ['remover', 'Remover todos'],
                ] as const
              ).map(([v, t]) => (
                <label key={v} className="flex items-center gap-2 text-sm">
                  <input type="radio" value={v} {...f.register('modoHeaders')} />
                  {t}
                </label>
              ))}
            </div>
          </>
        )}
        {modoHeaders === 'substituir' && (
          <div className="space-y-2">
            {headers.fields.map((h, i) => (
              <div key={h.id} className="grid grid-cols-[1fr_1fr_auto] items-end gap-2">
                <Campo rotulo={`Nome do header ${i + 1}`} erro={e.headers?.[i]?.nome?.message}>
                  {(a) => <Entrada {...a} {...f.register(`headers.${i}.nome`)} />}
                </Campo>
                <Campo rotulo={`Valor do header ${i + 1}`} erro={e.headers?.[i]?.valor?.message}>
                  {(a) => <Entrada {...a} {...f.register(`headers.${i}.valor`)} />}
                </Campo>
                <Botao tipo="fantasma" onClick={() => headers.remove(i)} aria-label={`Remover o header ${i + 1}`}>
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

      <Botao type="submit" disabled={f.formState.isSubmitting}>
        {rotuloDoBotao}
      </Botao>
    </form>
  )
}
