import { useEffect, useState } from 'react'
import { usePreviaDoCron } from '@/lib/api/consultas'
import { ApiError } from '@/lib/api/http'
import { mensagemDoCampo } from '@/lib/api/mensagens'
import { dataHora } from '@/lib/format'

// Uma consulta por pausa na digitação, e não por tecla: o limite é de 60 por
// minuto, dividido com o resto do painel.
export const ESPERA_DA_PREVIA_MS = 400

function useValorAdiado<T>(valor: T, espera: number): T {
  const [adiado, setAdiado] = useState(valor)
  useEffect(() => {
    const id = setTimeout(() => setAdiado(valor), espera)
    return () => clearTimeout(id)
  }, [valor, espera])
  return adiado
}

// No fuso declarado, que é o que importa ao dono: é a hora em que a máquina da
// tarefa dispara. O fuso veio aceito pelo servidor; se o navegador não o
// conhecer, a data sai no fuso dele, com o rótulo do campo ainda à vista.
export function horaNoFuso(iso: string, timezone: string): string {
  try {
    return new Intl.DateTimeFormat('pt-BR', { dateStyle: 'short', timeStyle: 'short', timeZone: timezone }).format(new Date(iso))
  } catch {
    return dataHora(iso)
  }
}

function textoDoErro(erro: unknown): string {
  const campo = erro instanceof ApiError ? erro.fields[0] : undefined
  return campo ? mensagemDoCampo(campo.field, campo.reason) : 'Não foi possível calcular os próximos disparos agora.'
}

// A prévia informa e não trava: quem julga é o servidor, no envio.
export function PreviaDoCron({ expr, timezone }: { expr: string; timezone: string }) {
  const exprAdiada = useValorAdiado(expr.trim(), ESPERA_DA_PREVIA_MS)
  const fusoAdiado = useValorAdiado(timezone.trim(), ESPERA_DA_PREVIA_MS)
  const consulta = usePreviaDoCron(exprAdiada, fusoAdiado)
  if (exprAdiada === '' || fusoAdiado === '') return null
  return (
    <div aria-live="polite" className="text-sm sm:col-span-2">
      {consulta.isError && <p className="text-suave">{textoDoErro(consulta.error)}</p>}
      {consulta.data && (
        <>
          <p className="text-meta text-suave">Próximos disparos, em {fusoAdiado}</p>
          <ul className="tabular-nums">
            {consulta.data.next_runs.map((iso) => (
              <li key={iso}>{horaNoFuso(iso, fusoAdiado)}</li>
            ))}
          </ul>
        </>
      )}
    </div>
  )
}
