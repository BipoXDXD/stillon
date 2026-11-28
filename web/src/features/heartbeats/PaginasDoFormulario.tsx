import { useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router'
import { useApiDoProjeto, useRotaDoProjeto } from '@/app/projeto'
import { SemPermissao } from '@/app/SemPermissao'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { Titulo } from '@/components/Titulo'
import { useAvisar } from '@/components/ui/avisos'
import { useChavesDoProjeto, useHeartbeat } from '@/lib/api/consultas'
import { usePode } from '@/lib/permissoes'
import { DialogoDaUrlDePing } from './DialogoDaUrlDePing'
import { comoEditavel, paraAlteracao, paraCriacao, valoresDe, valoresPadrao, type HeartbeatEditavel } from './esquema'
import { AGENDAMENTO_NAO_SUPORTADO } from './estado'
import { FormularioHeartbeat } from './FormularioHeartbeat'

// Sem edit_monitors (a URL digitada), o formulário nem monta: o envio daria 403.
export function PaginaNovoHeartbeat() {
  return usePode('edit_monitors') ? <NovoHeartbeat /> : <SemPermissao />
}

// A resposta do POST fica só aqui, no estado da tela: nada de cache, de URL nem
// de estado do router, que vai para o history.state.
function NovoHeartbeat() {
  const api = useApiDoProjeto()
  const chave = useChavesDoProjeto()
  const qc = useQueryClient()
  const navegar = useNavigate()
  const noProjeto = useRotaDoProjeto()
  const avisar = useAvisar()
  const [iniciais] = useState(valoresPadrao)
  const [criado, setCriado] = useState<{ id: string; url: string } | null>(null)
  // Navega depois de fechar: com a URL na tela, o diálogo segura a navegação.
  const [guardado, setGuardado] = useState<string | null>(null)
  useEffect(() => {
    if (guardado) void navegar(noProjeto(`/heartbeats/${guardado}`))
  }, [guardado, navegar, noProjeto])
  return (
    <>
      <Titulo aba="Novo heartbeat">Novo heartbeat</Titulo>
      <FormularioHeartbeat
        iniciais={iniciais}
        rotuloDoBotao="Cadastrar"
        aoEnviar={async (v) => {
          const { id, ping_url: url } = await api.criarHeartbeat(paraCriacao(v))
          setCriado({ id, url })
          await qc.invalidateQueries({ queryKey: chave.heartbeats })
          avisar('Heartbeat cadastrado.')
        }}
      />
      <DialogoDaUrlDePing
        url={criado?.url ?? null}
        aoFechar={() => {
          setGuardado(criado?.id ?? null)
          setCriado(null)
        }}
      />
    </>
  )
}

export function PaginaEditarHeartbeat() {
  return usePode('edit_monitors') ? <EditarHeartbeat /> : <SemPermissao />
}

function EditarHeartbeat() {
  const { id = '' } = useParams()
  const consulta = useHeartbeat(id)
  return (
    <>
      <Titulo aba="Editar heartbeat">Editar heartbeat</Titulo>
      <EstadoDaConsulta consulta={consulta}>
        {(hb) => {
          const editavel = comoEditavel(hb)
          return editavel === null ? <SemEdicao id={hb.id} /> : <EdicaoDoHeartbeat key={hb.id} hb={editavel} />
        }}
      </EstadoDaConsulta>
    </>
  )
}

// Quem chega pela URL a um agendamento de versão posterior: o formulário não
// sabe montá-lo, e o servidor recusaria o PATCH com 409 schedule_unsupported.
function SemEdicao({ id }: { id: string }) {
  const noProjeto = useRotaDoProjeto()
  return (
    <div className="space-y-4">
      <p>{`${AGENDAMENTO_NAO_SUPORTADO}.`}</p>
      <Link
        to={noProjeto(`/heartbeats/${id}`)}
        className="alvo-de-toque inline-flex items-center gap-1 text-sm font-semibold text-primaria underline-offset-4 hover:underline"
      >
        Voltar ao heartbeat
      </Link>
    </div>
  )
}

// Monta quando o heartbeat chega e congela ali a base do PATCH: a consulta
// volta a buscar, e uma base que andasse com ela reenviaria um campo não tocado.
function EdicaoDoHeartbeat({ hb }: { hb: HeartbeatEditavel }) {
  const api = useApiDoProjeto()
  const chave = useChavesDoProjeto()
  const qc = useQueryClient()
  const navegar = useNavigate()
  const noProjeto = useRotaDoProjeto()
  const avisar = useAvisar()
  const [iniciais] = useState(() => valoresDe(hb))
  return (
    <FormularioHeartbeat
      iniciais={iniciais}
      rotuloDoBotao="Salvar"
      aoEnviar={async (v) => {
        const alteracao = paraAlteracao(iniciais, v)
        if (Object.keys(alteracao).length > 0) {
          await api.alterarHeartbeat(hb.id, alteracao)
          await qc.invalidateQueries({ queryKey: chave.heartbeats })
          avisar('Heartbeat salvo.')
        }
        navegar(noProjeto(`/heartbeats/${hb.id}`))
      }}
    />
  )
}
