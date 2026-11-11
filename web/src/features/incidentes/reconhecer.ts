import { useQueryClient, type QueryKey } from '@tanstack/react-query'
import { useRef, useState } from 'react'
import { naoEncontrado } from '@/lib/api/http'
import { mensagemDe } from '@/lib/api/mensagens'

type Opcoes = {
  // O POST do ack do incidente.
  enviar: (id: string) => Promise<unknown>
  // O prefixo da consulta que mostra os incidentes: relida depois de cada ack.
  consulta: QueryKey
  // 404: o incidente sumiu com o alvo ou com o heartbeat, apagado em outra aba.
  aoNaoEncontrar: () => void
  // Chamado com null ao começar e com a mensagem quando o ack falha.
  aoErrar: (mensagem: string | null) => void
  // Depois da releitura, com o incidente ainda em "reconhecendo": é onde a
  // tela pede o foco, já que o Reconhecer some quando o ack vale.
  aoTerminar?: (id: string) => void
}

// O reconhecer de um incidente, comum às telas que o oferecem. Devolve os
// incidentes com ack em voo e a função que o envia. Um segundo pedido do mesmo
// incidente enquanto o primeiro voa é ignorado: o aria-disabled do botão não
// bloqueia o clique, e o estado só muda no próximo render, então a guarda é um
// ref, que um segundo clique no mesmo ciclo já enxerga.
export function useReconhecer({
  enviar,
  consulta,
  aoNaoEncontrar,
  aoErrar,
  aoTerminar,
}: Opcoes) {
  const qc = useQueryClient()
  const [reconhecendo, setReconhecendo] = useState<ReadonlySet<string>>(new Set())
  const emVoo = useRef(new Set<string>())

  async function reconhecer(id: string) {
    if (emVoo.current.has(id)) return
    emVoo.current.add(id)
    aoErrar(null)
    setReconhecendo((s) => new Set(s).add(id))
    try {
      await enviar(id)
    } catch (e) {
      if (naoEncontrado(e)) aoNaoEncontrar()
      else aoErrar(mensagemDe(e))
    } finally {
      // Mesmo depois de uma falha: o ack pode ter chegado ao servidor.
      try {
        await qc.invalidateQueries({ queryKey: consulta })
        aoTerminar?.(id)
      } finally {
        // Mesmo se o refetch ou o aoTerminar lançarem: id preso no ref
        // bloquearia o botão para sempre.
        emVoo.current.delete(id)
        setReconhecendo((s) => {
          const n = new Set(s)
          n.delete(id)
          return n
        })
      }
    }
  }

  return { reconhecendo, reconhecer }
}
