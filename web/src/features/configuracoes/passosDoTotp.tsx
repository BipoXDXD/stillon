import { zodResolver } from '@hookform/resolvers/zod'
import { QRCodeSVG } from 'qrcode.react'
import { useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'
import { Botao } from '@/components/ui/botao'
import { useCopiar } from '@/components/ui/copiar'
import { CaixaDeMarcar, Campo, Entrada } from '@/components/ui/campo'
import { AvisoDeCredencial } from './credencial'
import type { ErroDeCredencial } from './credencial'

// Quanto a URL do .txt dos códigos vive depois do clique (o do FileSaver.js).
export const ATRASO_DA_REVOGACAO = 40_000

function segredoDe(uri: string): string {
  try {
    return new URL(uri).searchParams.get('secret') ?? ''
  } catch {
    return ''
  }
}

// Um envio vazio iria ao servidor e contaria como tentativa falha no
// bloqueio progressivo (R34). Só "não vazio": o formato do código (6
// dígitos ou os 16 de recuperação) quem decide é o servidor.
export const esquemaSenha = z.object({ senha: z.string().min(1, 'Informe a senha.') })
export const esquemaCodigo = z.object({ codigo: z.string().trim().min(1, 'Informe o código.') })

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

// O QR e a chave de um segredo novo, e o código que o app gera com ele: o
// passo 2 de ativar e o de trocar de autenticador (spec do Plano 9 §4.2).
export function PassoDoQr({
  uri,
  instrucao,
  acao,
  erro,
  aoMudarErro,
  aoEnviar,
}: {
  uri: string
  instrucao: string
  acao: string
  erro: ErroDeCredencial | null
  aoMudarErro: (e: ErroDeCredencial | null) => void
  aoEnviar: (codigo: string) => Promise<void>
}) {
  const codigo = useForm({ resolver: zodResolver(esquemaCodigo), defaultValues: { codigo: '' } })
  const { enviando, enviar } = useEnvioUnico()
  return (
    <form
      noValidate
      className="space-y-4"
      onSubmit={codigo.handleSubmit((v) => enviar(() => aoEnviar(v.codigo.replace(/\s+/g, ''))))}
    >
      <AvisoDeCredencial erro={erro} aoMudar={aoMudarErro} />
      <p className="text-sm">{instrucao}</p>
      {/* A zona de silêncio branca é do próprio SVG (marginSize, em
          módulos): o leitor de QR precisa dela também no tema escuro. */}
      <QRCodeSVG value={uri} size={176} marginSize={4} title="QR code do autenticador" className="rounded-padrao" />
      <p className="text-sm">
        Chave: <code className="break-all">{segredoDe(uri)}</code>
      </p>
      <Campo rotulo="Código de 6 dígitos" erro={codigo.formState.errors.codigo?.message}>
        {(a) => <Entrada {...a} inputMode="numeric" autoComplete="one-time-code" {...codigo.register('codigo')} />}
      </Campo>
      {/* aria-disabled, e não disabled: o botão desabilitado perderia o foco,
          que cairia no <body> dentro do diálogo. Quem segura o envio é o
          useEnvioUnico. */}
      <Botao type="submit" aria-disabled={enviando || undefined}>
        {acao}
      </Botao>
    </form>
  )
}

// Os códigos de recuperação novos, que só existem nesta tela: copiar,
// baixar, e "Concluir" só depois de "Guardei os códigos". O último passo de
// ativar e o de trocar de autenticador.
export function CodigosDeRecuperacao({ codigos, aoTerminar }: { codigos: string[]; aoTerminar: () => void }) {
  const [guardei, setGuardei] = useState(false)
  const { copiado, copiar } = useCopiar({
    ok: 'Códigos copiados.',
    falha: 'Não foi possível copiar. Selecione os códigos e copie à mão, ou baixe o .txt.',
  })
  return (
    <div className="space-y-4">
      <p className="text-sm">
        Guarde estes códigos de recuperação num lugar seguro. Cada um vale uma vez, e eles não aparecem de novo.
      </p>
      <ul className="grid grid-cols-2 gap-1 font-mono text-sm">
        {codigos.map((c) => (
          <li key={c}>{c}</li>
        ))}
      </ul>
      <div className="flex gap-2">
        <Botao tipo="secundario" tamanho="pequeno" onClick={() => copiar(codigos.join('\n'))}>
          {copiado ? 'Copiado' : 'Copiar'}
        </Botao>
        <Botao
          tipo="secundario"
          tamanho="pequeno"
          onClick={() => {
            const url = URL.createObjectURL(new Blob([codigos.join('\n') + '\n'], { type: 'text/plain' }))
            const a = document.createElement('a')
            a.href = url
            a.download = 'stillon-codigos-de-recuperacao.txt'
            a.click()
            // O Firefox cancela o download se a URL é revogada logo depois
            // do click(); este é o único arquivo dos códigos.
            window.setTimeout(() => URL.revokeObjectURL(url), ATRASO_DA_REVOGACAO)
          }}
        >
          Baixar .txt
        </Botao>
      </div>
      <CaixaDeMarcar checked={guardei} onChange={(e) => setGuardei(e.target.checked)}>
        Guardei os códigos
      </CaixaDeMarcar>
      <Botao disabled={!guardei} onClick={aoTerminar}>
        Concluir
      </Botao>
    </div>
  )
}

// Senha e posse do fator atual (código do app ou de recuperação): as
// provas do Desativar e as do passo 1 da troca de autenticador, que são as
// mesmas no servidor (spec do Plano 9 §3.2).
export function FormularioDeProvas({
  acao,
  tipo,
  erro,
  aoMudarErro,
  aoEnviar,
}: {
  acao: string
  tipo: 'perigo' | 'primario'
  erro: ErroDeCredencial | null
  aoMudarErro: (e: ErroDeCredencial | null) => void
  aoEnviar: (senha: string, codigo: string) => Promise<void>
}) {
  const f = useForm({ resolver: zodResolver(esquemaSenha.and(esquemaCodigo)), defaultValues: { senha: '', codigo: '' } })
  const { enviando, enviar } = useEnvioUnico()
  return (
    <form
      noValidate
      className="space-y-4"
      onSubmit={f.handleSubmit((v) => enviar(() => aoEnviar(v.senha, v.codigo.replace(/\s+/g, ''))))}
    >
      <AvisoDeCredencial erro={erro} aoMudar={aoMudarErro} />
      <Campo rotulo="Sua senha" erro={f.formState.errors.senha?.message}>
        {(a) => <Entrada {...a} type="password" autoComplete="current-password" {...f.register('senha')} />}
      </Campo>
      <Campo rotulo="Código do autenticador" ajuda="Ou um código de recuperação." erro={f.formState.errors.codigo?.message}>
        {(a) => <Entrada {...a} autoComplete="one-time-code" {...f.register('codigo')} />}
      </Campo>
      <Botao type="submit" tipo={tipo} aria-disabled={enviando || undefined}>
        {acao}
      </Botao>
    </form>
  )
}
