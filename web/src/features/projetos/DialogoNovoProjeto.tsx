import { zodResolver } from '@hookform/resolvers/zod'
import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { useNavigate } from 'react-router'
import { useApi } from '@/app/contexto'
import { useAvisar } from '@/components/ui/avisos'
import { Botao } from '@/components/ui/botao'
import { Campo, Entrada, ErroDoFormulario } from '@/components/ui/campo'
import { Dialogo, type Reserva } from '@/components/ui/dialogo'
import { useEnvioUnico } from '@/features/configuracoes/useEnvioUnico'
import { chaves } from '@/lib/api/consultas'
import { errosDosCampos } from '@/lib/api/erros-de-campo'
import { mensagemDe } from '@/lib/api/mensagens'
import type { ListaDeProjetos } from '@/lib/api/schemas'
import { EsquemaDoProjeto, mensagemDoNome, type ValoresDoProjeto } from './esquema'

type Envio = ReturnType<typeof useEnvioUnico>

// O diálogo "Novo projeto". Enquanto o POST voa, nada fecha o diálogo (nem
// Esc, nem Cancelar, nem o clique fora): sem isso existiria o "fechei, mas
// foi criado". reserva: para onde o foco volta quando quem abriu sumiu (o
// item do menu do seletor some com o menu).
export function DialogoNovoProjeto({ aberto, aoFechar, reserva }: { aberto: boolean; aoFechar: () => void; reserva?: Reserva }) {
  const envio = useEnvioUnico()
  return (
    <Dialogo
      aberto={aberto}
      aoMudar={(v) => {
        if (!v) aoFechar()
      }}
      titulo="Novo projeto"
      travado={envio.enviando}
      reserva={reserva}
    >
      <Formulario envio={envio} aoFechar={aoFechar} />
    </Dialogo>
  )
}

// Validação só ao enviar (reValidateMode onSubmit): o erro não muda a cada
// letra. O erro de campo do servidor (o caractere invisível) vai ao campo; o
// resto (o teto de projetos, o 405 do binário anterior, a rede), ao topo do
// diálogo, que continua aberto e com o valor: com o diálogo aberto, um aviso
// flutuante ficaria inerte para o leitor de tela.
function Formulario({ envio, aoFechar }: { envio: Envio; aoFechar: () => void }) {
  const api = useApi()
  const qc = useQueryClient()
  const navigate = useNavigate()
  const avisar = useAvisar()
  const [erro, setErro] = useState<string | null>(null)
  const f = useForm<ValoresDoProjeto>({
    resolver: zodResolver(EsquemaDoProjeto),
    defaultValues: { name: '' },
    reValidateMode: 'onSubmit',
  })

  const enviar = (v: ValoresDoProjeto) =>
    envio.enviar(async () => {
      setErro(null)
      let novo
      try {
        novo = await api.criarProjeto({ name: v.name })
      } catch (e) {
        const [doNome] = errosDosCampos(e, (fe) =>
          fe.field === 'name' ? { campo: 'name' as const, mensagem: mensagemDoNome(fe.reason) } : undefined,
        )
        if (doNome) f.setError('name', { message: doNome.mensagem }, { shouldFocus: true })
        else setErro(mensagemDe(e))
        return
      }
      // O projeto novo entra na lista antes da navegação: a rota do projeto
      // só monta com ele na lista (ProvedorDoProjeto), e uma releitura que
      // falhe não pode dar a página não encontrada. A releitura traz a ordem
      // do servidor.
      qc.setQueryData<ListaDeProjetos>(chaves.projetos, (lista) =>
        lista && !lista.items.some((p) => p.id === novo.id) ? { items: [...lista.items, novo] } : lista,
      )
      await qc.invalidateQueries({ queryKey: chaves.projetos })
      aoFechar()
      await navigate(`/projects/${encodeURIComponent(novo.id)}/`)
      avisar('Projeto criado.')
    })

  return (
    <form noValidate className="space-y-4" onSubmit={f.handleSubmit(enviar)}>
      <ErroDoFormulario>{erro}</ErroDoFormulario>
      <Campo rotulo="Nome do projeto" erro={f.formState.errors.name?.message}>
        {(a) => <Entrada {...a} autoComplete="off" {...f.register('name')} />}
      </Campo>
      <div className="flex justify-end gap-2">
        <Botao
          tipo="secundario"
          aria-disabled={envio.enviando || undefined}
          onClick={() => {
            if (!envio.enviando) aoFechar()
          }}
        >
          Cancelar
        </Botao>
        {/* aria-disabled, e não disabled: o foco não cai no <body>. */}
        <Botao type="submit" aria-disabled={envio.enviando || undefined}>
          {envio.enviando ? 'Criando…' : 'Criar projeto'}
        </Botao>
      </div>
    </form>
  )
}
