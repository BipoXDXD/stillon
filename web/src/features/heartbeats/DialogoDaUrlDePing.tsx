import { Check } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useBlocker } from 'react-router'
import { useAvisar } from '@/components/ui/avisos'
import { Botao } from '@/components/ui/botao'
import { useCopiar } from '@/components/ui/copiar'
import { Dialogo } from '@/components/ui/dialogo'
import { cn } from '@/lib/utils'

const CURL = 'curl -fsS -m 10 --retry 3 -o /dev/null -X POST'

type Exemplo = { titulo: string; texto: string; rotulo: string; nota?: string }

// Todo exemplo manda POST (D6): um GET responde 405.
const EXEMPLOS = {
  curl: (url) => [
    { titulo: 'Ao terminar bem', texto: `${CURL} ${url}`, rotulo: 'Copiar o exemplo de sucesso' },
    { titulo: 'Ao começar (opcional, mede o tempo de execução)', texto: `${CURL} ${url}/start`, rotulo: 'Copiar o exemplo de início' },
    { titulo: 'Ao dar erro (abre incidente)', texto: `${CURL} ${url}/fail`, rotulo: 'Copiar o exemplo de falha' },
    {
      titulo: 'Numa linha só, depois da tarefa: 0 é sucesso, o resto é falha',
      texto: `${CURL} ${url}/$?`,
      rotulo: 'Copiar o exemplo com o código de saída',
    },
    // Entre aspas: sem elas, o `?` da URL é glob no zsh.
    {
      titulo: 'Com run id (execuções sobrepostas)',
      texto: `RID=$(uuidgen); ${CURL} "${url}/start?rid=$RID"; tarefa.sh; ${CURL} "${url}/$??rid=$RID"`,
      rotulo: 'Copiar o exemplo com run id',
    },
    // O corpo é descartado (D14 ainda não decidida): o exemplo não promete guardá-lo.
    {
      titulo: 'Registrar uma linha, sem mexer no prazo',
      texto: `${CURL} --data-binary @- "${url}/log"`,
      rotulo: 'Copiar o exemplo de registro',
      nota: 'O conteúdo não é guardado.',
    },
  ],
  crontab: (url) => [
    {
      titulo: 'Uma linha do crontab: o sinal só sai se a tarefa terminar bem',
      texto: `0 3 * * * /usr/local/bin/backup.sh && ${CURL} ${url}`,
      rotulo: 'Copiar o exemplo de crontab',
    },
  ],
  wget: (url) => [
    {
      titulo: 'Sem curl, com wget 1.15 ou mais novo',
      texto: `wget -q -T 10 -t 3 -O /dev/null --method=POST ${url}`,
      rotulo: 'Copiar o exemplo de wget',
    },
  ],
  Python: (url) => [
    {
      titulo: 'Python 3, numa linha do shell, só com a biblioteca padrão',
      texto: `python3 -c 'import urllib.request; urllib.request.urlopen(urllib.request.Request("${url}", method="POST"), timeout=10)'`,
      rotulo: 'Copiar o exemplo de Python',
    },
  ],
  PowerShell: (url) => [
    { titulo: 'PowerShell 3 ou mais novo', texto: `Invoke-RestMethod -Method Post -TimeoutSec 10 -Uri ${url}`, rotulo: 'Copiar o exemplo de PowerShell' },
  ],
} satisfies Record<string, (url: string) => readonly Exemplo[]>

type Aba = keyof typeof EXEMPLOS
const ABAS: readonly Aba[] = ['curl', 'crontab', 'wget', 'Python', 'PowerShell']

// A URL de ping aparece aqui e em mais lugar nenhum: é a credencial da tarefa,
// o servidor guarda só o hash, e ela vive no estado de quem abriu o diálogo
// (spec da tela, T8 e T9). Só "Já guardei" fecha.
export function DialogoDaUrlDePing({ url, aoFechar }: { url: string | null; aoFechar: () => void }) {
  useSegurarNavegacao(url !== null)
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
          <ExemplosDeUso url={url} />
          <p className="text-sm text-suave">Só POST: um GET responde 405, para que prévia de link e scanner não contem como sinal.</p>
          <div className="flex justify-end">
            <Botao onClick={aoFechar}>Já guardei</Botao>
          </div>
        </div>
      )}
    </Dialogo>
  )
}

// Abas como botões com aria-pressed, no padrão dos filtros do painel: a
// escolhida tem fundo, borda e check, nunca só a cor (DI 8).
function ExemplosDeUso({ url }: { url: string }) {
  const [aba, setAba] = useState<Aba>('curl')
  return (
    <div className="space-y-3">
      <h3 className="text-sm font-semibold">Exemplos de uso</h3>
      <fieldset className="flex min-w-0 flex-wrap gap-2">
        <legend className="sr-only">Escolha onde chamar a URL</legend>
        {ABAS.map((a) => (
          <Botao
            key={a}
            tipo="secundario"
            tamanho="pequeno"
            aria-pressed={a === aba}
            onClick={() => setAba(a)}
            className={cn(a === aba && 'border-primaria bg-primaria-100 text-primaria hover:bg-primaria-100')}
          >
            {a === aba && <Check aria-hidden="true" className="size-3.5" strokeWidth={2.5} />}
            {a}
          </Botao>
        ))}
      </fieldset>
      {EXEMPLOS[aba](url).map((e) => (
        <Copiavel key={e.rotulo} {...e} />
      ))}
    </div>
  )
}

// O Voltar do navegador não passa pelo diálogo, e a URL sumiria com a tela.
function useSegurarNavegacao(ativo: boolean) {
  const avisar = useAvisar()
  const bloqueio = useBlocker(ativo)
  useEffect(() => {
    if (bloqueio.state !== 'blocked') return
    bloqueio.reset()
    avisar('Guarde a URL antes de sair: ela não aparece de novo.')
  }, [bloqueio, avisar])
}

function Copiavel({ titulo, texto, rotulo, nota }: { titulo: string; texto: string; rotulo: string; nota?: string }) {
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
      {nota !== undefined && <p className="text-meta text-suave">{nota}</p>}
    </div>
  )
}
