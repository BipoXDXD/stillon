import { WifiOff } from 'lucide-react'
import { useEffect } from 'react'
import { useAnunciar } from '@/components/Anunciador'
import { useSemRede } from '@/lib/api/consultas'

const PROBLEMA = 'Sem conexão com a internet.'
const CONSEQUENCIA = 'O que está na tela pode estar desatualizado.'

// Um aviso só, no layout, e não um por consulta: a queda é do navegador, e
// vale para a tela inteira. O leitor de tela ouve pela região do Anunciador,
// que fica sempre montada: uma região que já nasce preenchida muitas vezes
// não é lida. Por isso o aviso visível não é região viva.
export function AvisoDeRede() {
  const semRede = useSemRede()
  const anunciar = useAnunciar()
  useEffect(() => {
    if (semRede) anunciar(`${PROBLEMA} ${CONSEQUENCIA}`)
  }, [semRede, anunciar])
  if (!semRede) return null
  return (
    <div className="border-b border-perigo bg-painel px-4 py-3 text-sm">
      <div className="mx-auto flex max-w-6xl items-start gap-3">
        <WifiOff aria-hidden="true" className="mt-1 size-4 shrink-0 text-suave" />
        <p>
          <strong>{PROBLEMA}</strong> {CONSEQUENCIA} O painel volta a atualizar sozinho quando a conexão voltar.
        </p>
      </div>
    </div>
  )
}
