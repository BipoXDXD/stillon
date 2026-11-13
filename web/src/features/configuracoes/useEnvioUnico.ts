import { useRef, useState } from 'react'

// Um envio por vez (revisão final do Plano 9). O handleSubmit do
// react-hook-form não impede reentrada: um clique duplo (ou Enter duas vezes)
// mandava dois rotate, ou dois confirm — e o 2º confirm, com o pendente já
// consumido pelo 1º, voltava 403 e tirava da tela os códigos novos. A ref
// segura a reentrada no mesmo tique; o estado alimenta o aria-disabled.
export function useEnvioUnico() {
  const emVoo = useRef(false)
  const [enviando, setEnviando] = useState(false)
  const enviar = async (fn: () => Promise<void>) => {
    if (emVoo.current) return
    emVoo.current = true
    setEnviando(true)
    try {
      await fn()
    } finally {
      emVoo.current = false
      setEnviando(false)
    }
  }
  return { enviando, enviar }
}
