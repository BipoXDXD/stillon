import { zodResolver } from '@hookform/resolvers/zod'
import { useState } from 'react'
import { useForm, useWatch, type UseFormRegisterReturn } from 'react-hook-form'
import { Botao } from '@/components/ui/botao'
import { estiloDeCartao } from '@/components/ui/cartao'
import { AreaDeTexto, Campo, Entrada, ErroDoFormulario, Opcao, Selecao } from '@/components/ui/campo'
import { ApiError } from '@/lib/api/http'
import { mensagemDe } from '@/lib/api/mensagens'
import { cn } from '@/lib/utils'
import { campoDoServidor, esquemaDoHeartbeat, mensagemDoHeartbeat, type ValoresDoHeartbeat } from './esquema'
import { PreviaDoCron } from './PreviaDoCron'

// Sugestões do campo de fuso: o navegador conhece a lista IANA, e o servidor
// continua julgando o que vier digitado.
const FUSOS: readonly string[] = Intl.supportedValuesOf('timeZone')
const ID_DOS_FUSOS = 'fusos-iana'

export function FormularioHeartbeat({
  iniciais,
  aoEnviar,
  rotuloDoBotao,
}: {
  iniciais: ValoresDoHeartbeat
  aoEnviar: (v: ValoresDoHeartbeat) => Promise<void>
  rotuloDoBotao: string
}) {
  const f = useForm<ValoresDoHeartbeat>({ resolver: zodResolver(esquemaDoHeartbeat), defaultValues: iniciais })
  const [erroGeral, setErroGeral] = useState<string | null>(null)
  const e = f.formState.errors
  const tipo = useWatch({ control: f.control, name: 'tipo' })
  const expr = useWatch({ control: f.control, name: 'expr' })
  const timezone = useWatch({ control: f.control, name: 'timezone' })
  const numero = { valueAsNumber: true } as const

  async function enviar(v: ValoresDoHeartbeat) {
    setErroGeral(null)
    try {
      await aoEnviar(v)
    } catch (err) {
      if (!errosNosCampos(err)) setErroGeral(mensagemDe(err))
    }
  }

  // O 400 com fields vai para o campo certo; o resto, para o topo.
  function errosNosCampos(err: unknown): boolean {
    if (!(err instanceof ApiError)) return false
    const conhecidos = err.fields.flatMap((fe) => {
      const campo = campoDoServidor[fe.field.replace(/\[.*$/, '')]
      return campo ? [{ campo, mensagem: mensagemDoHeartbeat(fe, f.getValues('description')) }] : []
    })
    conhecidos.forEach(({ campo, mensagem }, i) => f.setError(campo, { message: mensagem }, { shouldFocus: i === 0 }))
    return conhecidos.length > 0
  }

  return (
    <form noValidate className={cn(estiloDeCartao, 'max-w-2xl space-y-6')} onSubmit={f.handleSubmit(enviar)}>
      <ErroDoFormulario>{erroGeral}</ErroDoFormulario>
      <Campo rotulo="Nome" erro={e.name?.message}>
        {(a) => <Entrada {...a} {...f.register('name')} />}
      </Campo>
      <Campo rotulo="Tags" erro={e.tagsTexto?.message} ajuda="Separadas por vírgula, até 8. Ex.: prod, backup.">
        {(a) => <Entrada {...a} {...f.register('tagsTexto')} />}
      </Campo>
      <Campo
        rotulo="Descrição"
        erro={e.description?.message}
        ajuda="Opcional, até 10000 caracteres. O que a tarefa faz, onde roda e o que fazer quando falhar. Aparece só no detalhe, nunca nos avisos."
      >
        {(a) => <AreaDeTexto {...a} {...f.register('description')} />}
      </Campo>
      <fieldset>
        <legend className="mb-2 text-sm font-semibold">Agendamento</legend>
        <div className="flex flex-wrap gap-x-6">
          <Opcao value="period" {...f.register('tipo')}>
            A cada período
          </Opcao>
          <Opcao value="cron" {...f.register('tipo')}>
            Expressão cron
          </Opcao>
        </div>
      </fieldset>
      {tipo === 'period' ? (
        <CampoDeDuracao
          rotulo="Período"
          ajuda="De 1 minuto a 365 dias."
          erro={e.periodoQuantidade?.message}
          quantidade={f.register('periodoQuantidade', numero)}
          unidade={f.register('periodoUnidade')}
        />
      ) : (
        <div className="grid gap-4 sm:grid-cols-2">
          <Campo rotulo="Expressão" erro={e.expr?.message} ajuda="Cinco campos: minuto, hora, dia, mês e dia da semana. Domingo é 0 ou SUN, não 7.">
            {(a) => <Entrada {...a} spellCheck={false} placeholder="0 3 * * *" {...f.register('expr')} />}
          </Campo>
          <Campo rotulo="Fuso" erro={e.timezone?.message} ajuda="Nome IANA, como America/Sao_Paulo.">
            {(a) => <Entrada {...a} spellCheck={false} list={ID_DOS_FUSOS} {...f.register('timezone')} />}
          </Campo>
          <datalist id={ID_DOS_FUSOS}>
            {FUSOS.map((nome) => (
              <option key={nome} value={nome}>{nome}</option>
            ))}
          </datalist>
          <PreviaDoCron expr={expr} timezone={timezone} />
        </div>
      )}
      <CampoDeDuracao
        rotulo="Tolerância"
        ajuda="Folga depois do prazo antes de abrir incidente, de 30 segundos a 365 dias. Ela cobre o tempo de execução da tarefa."
        erro={e.toleranciaQuantidade?.message}
        quantidade={f.register('toleranciaQuantidade', numero)}
        unidade={f.register('toleranciaUnidade')}
      />
      <Botao type="submit" disabled={f.formState.isSubmitting}>
        {rotuloDoBotao}
      </Botao>
    </form>
  )
}

function CampoDeDuracao({
  rotulo,
  ajuda,
  erro,
  quantidade,
  unidade,
}: {
  rotulo: string
  ajuda: string
  erro?: string
  quantidade: UseFormRegisterReturn
  unidade: UseFormRegisterReturn
}) {
  return (
    <Campo rotulo={rotulo} ajuda={ajuda} erro={erro}>
      {(a) => (
        <div className="grid grid-cols-2 gap-2 sm:max-w-64">
          <Entrada {...a} type="number" inputMode="numeric" min={1} {...quantidade} />
          <Selecao aria-label={`Unidade de ${rotulo.toLowerCase()}`} {...unidade}>
            <option value="segundos">segundos</option>
            <option value="minutos">minutos</option>
            <option value="horas">horas</option>
            <option value="dias">dias</option>
          </Selecao>
        </div>
      )}
    </Campo>
  )
}
