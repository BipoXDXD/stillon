import { useQueryClient, type QueryKey } from '@tanstack/react-query'
import { useState } from 'react'
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
  // Ignora um segundo pedido do mesmo incidente enquanto o primeiro voa.
  // As telas que já travam o clique no botão não precisam dela.
  ignorarEmAndamento?: boolean
}

// O reconhecer de um incidente, comum às telas que o oferecem. Devolve os
// incidentes com ack em voo e a função que o envia.
export function useReconhecer({
  enviar,
  consulta,
  aoNaoEncontrar,
  aoErrar,
  aoTerminar,
  ignorarEmAndamento = false,
}: Opcoes) {
  const qc = useQueryClient()
  const [reconhecendo, setReconhecendo] = useState<ReadonlySet<string>>(new Set())

  async function reconhecer(id: string) {
    if (ignorarEmAndamento && reconhecendo.has(id)) return
    aoErrar(null)
    setReconhecendo((s) => new Set(s).add(id))
    try {
      await enviar(id)
    } catch (e) {
      if (naoEncontrado(e)) aoNaoEncontrar()
      else aoErrar(mensagemDe(e))
    } finally {
      // Mesmo depois de uma falha: o ack pode ter chegado ao servidor.
      await qc.invalidateQueries({ queryKey: consulta })
      aoTerminar?.(id)
      setReconhecendo((s) => {
        const n = new Set(s)
        n.delete(id)
        return n
      })
    }
  }

  return { reconhecendo, reconhecer }
}
