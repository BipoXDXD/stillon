import { useQueryClient } from '@tanstack/react-query'
import { Check } from 'lucide-react'
import { useState } from 'react'
import { useApi, useSessao } from '@/app/contexto'
import { EstadoDaConsulta } from '@/components/EstadoDaConsulta'
import { Selo } from '@/components/SeloDeEstado'
import { useAvisar } from '@/components/ui/avisos'
import { Botao } from '@/components/ui/botao'
import { ErroDoFormulario } from '@/components/ui/campo'
import { Confirmar } from '@/components/ui/dialogo'
import { chaves, useSessoes } from '@/lib/api/consultas'
import { ApiError } from '@/lib/api/http'
import { mensagemDe } from '@/lib/api/mensagens'
import type { Sessao } from '@/lib/api/schemas'
import { dataHora, navegadorDe } from '@/lib/format'

export function SessoesAtivas() {
  const consulta = useSessoes()
  const api = useApi()
  const qc = useQueryClient()
  const avisar = useAvisar()
  const { sessao } = useSessao()
  const [alvo, setAlvo] = useState<Sessao | null>(null)
  const [erro, setErro] = useState<string | null>(null)
  // Um DELETE por vez: um segundo DELETE da sessão atual voltaria 401 (a
  // família já morreu) e trocaria o motivo do fim da sessão para "expirou".
  const [ocupado, setOcupado] = useState(false)

  async function encerrar() {
    if (!alvo || ocupado) return
    const atual = alvo.current
    setErro(null)
    setOcupado(true)
    try {
      await api.encerrarSessao(alvo.id)
      if (atual) {
        // O 204 já apagou o cookie e o servidor recusa novos acessos desta
        // família. O painel também larga o token em memória, sem chamar
        // refresh nem logout, e sem recarregar nada.
        sessao.encerrar('encerrada-aqui')
        return
      }
      avisar('Sessão encerrada.')
    } catch (e) {
      if (e instanceof ApiError && e.code === 'not_found') avisar('Esta sessão já tinha terminado.')
      else setErro(mensagemDe(e))
    }
    // A lista muda ANTES de o diálogo fechar: fechando antes, o foco voltaria
    // ao "Encerrar" da linha, que sumiria em seguida (e o foco, ao <body>).
    // Com a linha já fora, o diálogo manda o foco ao título da tela.
    await qc.invalidateQueries({ queryKey: chaves.sessoes })
    setAlvo(null)
    setOcupado(false)
  }

  return (
    <>
      <p className="text-sm text-suave">
        Encerrar uma sessão bloqueia novos acessos. Ações já em andamento podem ser concluídas.
      </p>
      <ErroDoFormulario>{erro}</ErroDoFormulario>
      <EstadoDaConsulta consulta={consulta}>
        {(d) => (
          <ul className="space-y-2">
            {d.items.map((s) => (
              <li
                key={s.id}
                className="flex flex-wrap items-center justify-between gap-3 rounded-padrao border border-borda p-4"
              >
                <div className="text-sm">
                  <p className="font-semibold">
                    {navegadorDe(s.user_agent)}
                    {s.current && (
                      <Selo tom="primaria" Icone={Check} className="ml-2">
                        Esta sessão
                      </Selo>
                    )}
                  </p>
                  <p className="text-suave">
                    Desde {dataHora(s.created_at)} · último acesso por volta de {dataHora(s.last_used_at)}
                  </p>
                </div>
                <Botao tipo="terciario" tamanho="pequeno" onClick={() => setAlvo(s)}>
                  Encerrar
                </Botao>
              </li>
            ))}
          </ul>
        )}
      </EstadoDaConsulta>
      <Confirmar
        aberto={alvo !== null}
        aoMudar={(v) => !v && setAlvo(null)}
        titulo={alvo?.current ? 'Sair deste navegador?' : 'Encerrar a sessão?'}
        descricao={
          alvo?.current
            ? 'Esta é a sessão que você está usando: o painel volta para a tela de entrada.'
            : 'O outro aparelho não consegue mais renovar o acesso.'
        }
        acao="Encerrar"
        ocupado={ocupado}
        aoConfirmar={() => void encerrar()}
      />
    </>
  )
}
