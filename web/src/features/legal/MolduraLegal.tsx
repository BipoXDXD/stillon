import type { ReactNode } from 'react'
import { NaoEncontrada } from '@/app/NaoEncontrada'
import { Titulo } from '@/components/Titulo'
import { CarregandoConfig, LinkTerciario, MolduraPublica } from '@/features/conta/comum'
import { useConfigDoCadastro } from '@/lib/api/consultas'
import versaoDosTermos from './versao.json'

// A moldura dos textos legais: a do login e do cadastro, na versão larga,
// porque o texto é longo. A versão vem do versao.json, a mesma que o
// cadastro envia como aceita.
//
// Os textos só aparecem com o cadastro aberto: eles prometem exportar e
// excluir a conta pelo painel (que chegam no 6.3 e no 6.4) e ainda têm os
// marcadores do operador (preenchidos no 6.6, que abre o cadastro). Fechado,
// ou sem a config, a rota é a página não encontrada; enquanto ela carrega,
// nada do texto aparece.
export function MolduraLegal({ titulo, children }: { titulo: string; children: ReactNode }) {
  const config = useConfigDoCadastro()
  if (config.isPending) {
    return (
      <MolduraPublica larga>
        <CarregandoConfig />
      </MolduraPublica>
    )
  }
  if (config.data?.enabled !== true) {
    return (
      <MolduraPublica larga>
        <NaoEncontrada />
      </MolduraPublica>
    )
  }
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
