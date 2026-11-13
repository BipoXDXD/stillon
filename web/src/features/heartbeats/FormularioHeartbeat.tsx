import { zodResolver } from '@hookform/resolvers/zod'
import { useId, useState, type ReactNode } from 'react'
import { flushSync } from 'react-dom'
import { useForm, useWatch, type FieldError, type UseFormRegisterReturn } from 'react-hook-form'
import { Botao } from '@/components/ui/botao'
import { estiloDeCartao } from '@/components/ui/cartao'
import { AreaDeTexto, CaixaDeMarcar, Campo, Entrada, ErroDoFormulario, Opcao, Selecao } from '@/components/ui/campo'
import { errosDosCampos } from '@/lib/api/erros-de-campo'
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
  // Quem já ligou uma opção do Avançado a vê ao editar.
  const [avancadoAberto, setAvancadoAberto] = useState(iniciais.pingResumes)
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
    const conhecidos = errosDosCampos(err, (fe) => {
      const campo = campoDoServidor[fe.field.replace(/\[.*$/, '')]
      return campo && { campo, mensagem: mensagemDoHeartbeat(fe, f.getValues('description')) }
    })
    // O erro dentro do Avançado fechado ficaria escondido, e o foco do setError
    // não chega a um campo ali: abre antes de marcar.
    if (conhecidos.some(({ campo }) => campo === 'pingResumes')) flushSync(() => setAvancadoAberto(true))
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
      <Avancado aberto={avancadoAberto} aoAlternar={setAvancadoAberto}>
        <CampoDeRetomada erro={e.pingResumes} registro={f.register('pingResumes')} />
      </Avancado>
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

// O summary tem 48 px, não 44: com o conteúdo logo abaixo, a medição do alvo de
// toque no e2e caía no vizinho.
function Avancado({ aberto, aoAlternar, children }: { aberto: boolean; aoAlternar: (aberto: boolean) => void; children: ReactNode }) {
  return (
    <details open={aberto} onToggle={(ev) => aoAlternar(ev.currentTarget.open)}>
      <summary className="min-h-12 cursor-pointer py-3 text-sm leading-6 font-semibold">Avançado</summary>
      <div className="pt-2">{children}</div>
    </details>
  )
}

function CampoDeRetomada({ erro, registro }: { erro?: FieldError; registro: UseFormRegisterReturn }) {
  const id = useId()
  const idAjuda = `${id}-ajuda`
  const idErro = `${id}-erro`
  const mensagem = erro?.message
  return (
    <div className="space-y-2">
      <CaixaDeMarcar aria-describedby={mensagem ? `${idAjuda} ${idErro}` : idAjuda} aria-invalid={mensagem ? true : undefined} {...registro}>
        Retomar sozinho quando chegar um sucesso
      </CaixaDeMarcar>
      <p id={idAjuda} className="text-sm text-suave">
        Uma tarefa pausada volta a ser vigiada no primeiro sinal de sucesso.
      </p>
      {mensagem && (
        <p id={idErro} className="text-sm text-perigo">
          {mensagem}
        </p>
      )}
    </div>
  )
}
