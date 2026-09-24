import { useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from 'react-router'
import { useApi } from '@/app/contexto'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { Titulo } from '@/components/Titulo'
import { useAvisar } from '@/components/ui/avisos'
import { useAlvo } from '@/lib/api/consultas'
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
  const api = useApi()
  const qc = useQueryClient()
  const navegar = useNavigate()
  const avisar = useAvisar()
  const consulta = useAlvo(id)
  return (
    <>
      <Titulo aba="Editar alvo">Editar alvo</Titulo>
      <EstadoDaConsulta consulta={consulta}>
        {(alvo) => {
          const iniciais = valoresDe(alvo)
          return (
            <FormularioAlvo
              iniciais={iniciais}
              nomesDeHeaders={alvo.header_names}
              rotuloDoBotao="Salvar"
              aoEnviar={async (v) => {
                const mudancas = paraAlteracao(iniciais, v)
                if (Object.keys(mudancas).length) await api.alterarAlvo(id, mudancas)
                await qc.invalidateQueries({ queryKey: ['targets'] })
                avisar('Alvo salvo.')
                navegar(`/targets/${id}`)
              }}
            />
          )
        }}
      </EstadoDaConsulta>
    </>
  )
}
