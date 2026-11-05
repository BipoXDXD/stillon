import { Botao } from '@/components/ui/botao'
import { useCopiar } from '@/components/ui/copiar'
import { Dialogo } from '@/components/ui/dialogo'

const CURL = 'curl -fsS -m 10 --retry 3 -o /dev/null -X POST'

// A URL de ping aparece aqui e em mais lugar nenhum: é a credencial da tarefa,
// o servidor guarda só o hash, e ela vive no estado de quem abriu o diálogo
// (spec da tela, T8 e T9). Só "Já guardei" fecha.
export function DialogoDaUrlDePing({ url, aoFechar }: { url: string | null; aoFechar: () => void }) {
  return (
    <Dialogo
      aberto={url !== null}
      aoMudar={() => undefined}
      travado
      titulo="Guarde a URL de ping"
      descricao="Ela aparece só agora. Quem tem a URL pode mandar sinais em nome da tarefa: guarde junto dos segredos dela."
    >
      {url !== null && (
        <div className="space-y-4">
          <Copiavel titulo="URL de ping" texto={url} rotulo="Copiar a URL de ping" />
          <div className="space-y-3">
            <h3 className="text-sm font-semibold">Exemplos com curl</h3>
            <Copiavel titulo="Ao terminar bem" texto={`${CURL} ${url}`} rotulo="Copiar o exemplo de sucesso" />
            <Copiavel titulo="Ao começar (opcional, mede o tempo de execução)" texto={`${CURL} ${url}/start`} rotulo="Copiar o exemplo de início" />
            <Copiavel titulo="Ao dar erro (abre incidente)" texto={`${CURL} ${url}/fail`} rotulo="Copiar o exemplo de falha" />
          </div>
          <p className="text-sm text-suave">Só POST: um GET responde 405, para que prévia de link e scanner não contem como sinal.</p>
          <div className="flex justify-end">
            <Botao onClick={aoFechar}>Já guardei</Botao>
          </div>
        </div>
      )}
    </Dialogo>
  )
}

function Copiavel({ titulo, texto, rotulo }: { titulo: string; texto: string; rotulo: string }) {
  const { copiado, copiar } = useCopiar({ ok: 'Copiado.', falha: 'Não foi possível copiar: selecione o texto e copie à mão.' })
  return (
    <div className="space-y-1">
      <p className="text-meta text-suave">{titulo}</p>
      <div className="flex items-start gap-2">
        <code className="min-w-0 flex-1 rounded-padrao bg-painel p-2 text-meta break-all select-all">{texto}</code>
        <Botao tipo="secundario" tamanho="pequeno" aria-label={rotulo} onClick={() => copiar(texto)}>
          {copiado ? 'Copiado' : 'Copiar'}
        </Botao>
      </div>
    </div>
  )
}
