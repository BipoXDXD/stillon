import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useNavigate, useParams } from 'react-router'
import { useApi } from '@/app/contexto'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { Titulo } from '@/components/Titulo'
import { useAvisar } from '@/components/ui/avisos'
import { useAlvo } from '@/lib/api/consultas'
import type { Alvo } from '@/lib/api/schemas'
import { FormularioAlvo } from './FormularioAlvo'
import { paraAlteracao, paraCriacao, valoresDe, valoresPadrao } from './esquema'

export function PaginaNovoAlvo() {
  const api = useApi()
  const qc = useQueryClient()
  const navegar = useNavigate()
  const avisar = useAvisar()
  return (
    <>
      <Titulo aba="Novo alvo">Novo alvo</Titulo>
      <FormularioAlvo
        iniciais={valoresPadrao}
        rotuloDoBotao="Cadastrar"
        aoEnviar={async (v) => {
          const a = await api.criarAlvo(paraCriacao(v))
          await qc.invalidateQueries({ queryKey: ['targets'] })
          avisar('Alvo cadastrado.')
          navegar(`/targets/${a.id}`)
        }}
      />
    </>
  )
}

export function PaginaEditarAlvo() {
  const { id = '' } = useParams()
  const consulta = useAlvo(id)
  return (
    <>
      <Titulo aba="Editar alvo">Editar alvo</Titulo>
      {/* key: voltar pelo histórico de /targets/B/edit a /targets/A/edit, com A
          no cache, reaproveitaria o formulário (e a base do PATCH) de B. */}
      <EstadoDaConsulta consulta={consulta}>{(alvo) => <EdicaoDoAlvo key={alvo.id} alvo={alvo} />}</EstadoDaConsulta>
    </>
  )
}

// Monta quando o alvo chega, e congela ali a base do PATCH: o useAlvo volta a
// buscar a cada 60 s e no foco, e uma base que andasse com ele faria o PATCH
// reenviar, com o valor antigo do formulário, um campo que o usuário nem
// tocou (e que outra pessoa mudou nesse meio-tempo).
function EdicaoDoAlvo({ alvo }: { alvo: Alvo }) {
  const api = useApi()
  const qc = useQueryClient()
  const navegar = useNavigate()
  const avisar = useAvisar()
  const [iniciais] = useState(() => valoresDe(alvo))
  return (
    <FormularioAlvo
      iniciais={iniciais}
      nomesDeHeaders={alvo.header_names}
      rotuloDoBotao="Salvar"
      aoEnviar={async (v) => {
        const mudancas = paraAlteracao(iniciais, v)
        if (Object.keys(mudancas).length) await api.alterarAlvo(alvo.id, mudancas)
        await qc.invalidateQueries({ queryKey: ['targets'] })
        avisar('Alvo salvo.')
        navegar(`/targets/${alvo.id}`)
      }}
    />
  )
}
