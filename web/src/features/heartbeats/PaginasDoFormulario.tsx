import { useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router'
import { useApi } from '@/app/contexto'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { Titulo } from '@/components/Titulo'
import { useAvisar } from '@/components/ui/avisos'
import { chaves, useHeartbeat } from '@/lib/api/consultas'
import type { Heartbeat } from '@/lib/api/schemas'
import { DialogoDaUrlDePing } from './DialogoDaUrlDePing'
import { paraAlteracao, paraCriacao, valoresDe, valoresPadrao } from './esquema'
import { FormularioHeartbeat } from './FormularioHeartbeat'

// A resposta do POST fica só aqui, no estado da tela: nada de cache, de URL nem
// de estado do router, que vai para o history.state (spec da tela, T8).
export function PaginaNovoHeartbeat() {
  const api = useApi()
  const qc = useQueryClient()
  const navegar = useNavigate()
  const avisar = useAvisar()
  const [iniciais] = useState(valoresPadrao)
  const [criado, setCriado] = useState<{ id: string; url: string } | null>(null)
  // Navega depois de fechar: com a URL na tela, o diálogo segura a navegação.
  const [guardado, setGuardado] = useState<string | null>(null)
  useEffect(() => {
    if (guardado) void navegar(`/heartbeats/${guardado}`)
  }, [guardado, navegar])
  return (
    <>
      <Titulo aba="Novo heartbeat">Novo heartbeat</Titulo>
      <FormularioHeartbeat
        iniciais={iniciais}
        rotuloDoBotao="Cadastrar"
        aoEnviar={async (v) => {
          const { id, ping_url: url } = await api.criarHeartbeat(paraCriacao(v))
          setCriado({ id, url })
          await qc.invalidateQueries({ queryKey: chaves.heartbeats })
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
  const { id = '' } = useParams()
  const consulta = useHeartbeat(id)
  return (
    <>
      <Titulo aba="Editar heartbeat">Editar heartbeat</Titulo>
      <EstadoDaConsulta consulta={consulta}>{(hb) => <EdicaoDoHeartbeat key={hb.id} hb={hb} />}</EstadoDaConsulta>
    </>
  )
}

// Monta quando o heartbeat chega e congela ali a base do PATCH: a consulta
// volta a buscar, e uma base que andasse com ela reenviaria um campo não tocado.
function EdicaoDoHeartbeat({ hb }: { hb: Heartbeat }) {
  const api = useApi()
  const qc = useQueryClient()
  const navegar = useNavigate()
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
          await qc.invalidateQueries({ queryKey: chaves.heartbeats })
          avisar('Heartbeat salvo.')
        }
        navegar(`/heartbeats/${hb.id}`)
      }}
    />
  )
}
