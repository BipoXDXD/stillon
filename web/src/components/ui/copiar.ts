import { useEffect, useState } from 'react'
import { useAvisar } from './avisos'

// Copiar para a área de transferência com um feedback só, igual em toda
// tela (M4 da revisão final): um aviso no sucesso e outro na falha
// (clipboard ausente fora de contexto seguro, ou recusado pelo navegador),
// e o rótulo "Copiado" por 2 s. Uma falha logo depois de um sucesso devolve
// o rótulo a "Copiar": "Copiado" ali mentiria.
export function useCopiar(textos: { ok: string; falha: string }) {
  const avisar = useAvisar()
  const [copiado, setCopiado] = useState(false)
  useEffect(() => {
    if (!copiado) return
    const t = window.setTimeout(() => setCopiado(false), 2000)
    return () => window.clearTimeout(t)
  }, [copiado])

  function copiar(texto: string) {
    const falhou = () => {
      setCopiado(false)
      avisar(textos.falha, 'erro')
    }
    // Fora de contexto seguro (ou num navegador antigo) não há clipboard.
    if (!navigator.clipboard) return falhou()
    navigator.clipboard.writeText(texto).then(() => {
      setCopiado(true)
      avisar(textos.ok)
    }, falhou)
  }

  return { copiado, copiar }
}
