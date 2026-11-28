import type { ReactNode } from 'react'
import { Titulo } from '@/components/Titulo'
import { LinkTerciario, MolduraPublica } from '@/features/conta/comum'
import versaoDosTermos from './versao.json'

// A moldura dos textos legais: a do login e do cadastro, na versão larga,
// porque o texto é longo. A versão vem do versao.json, a mesma que o
// cadastro envia como aceita.
export function MolduraLegal({ titulo, children }: { titulo: string; children: ReactNode }) {
  return (
    <MolduraPublica
      larga
      rodape={
        <>
          <LinkTerciario para="/signup">Criar conta</LinkTerciario> ·{' '}
          <LinkTerciario para="/login">Entrar</LinkTerciario>
        </>
      }
    >
      <div className="space-y-2">
        <Titulo aba={titulo}>{titulo}</Titulo>
        <p className="text-sm text-suave">Versão {versaoDosTermos.versao}</p>
      </div>
      <div className="space-y-6 text-sm leading-relaxed">{children}</div>
    </MolduraPublica>
  )
}

// Uma seção do texto: o título (h2) e os parágrafos ou listas.
export function Secao({ titulo, children }: { titulo: string; children: ReactNode }) {
  return (
    <section className="space-y-2">
      <h2 className="font-display text-xl font-semibold">{titulo}</h2>
      {children}
    </section>
  )
}

export function Lista({ children }: { children: ReactNode }) {
  return <ul className="list-disc space-y-1 pl-6">{children}</ul>
}
