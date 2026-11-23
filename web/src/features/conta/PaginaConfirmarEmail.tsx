import { useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useRef, useState } from 'react'
import { Link } from 'react-router'
import { useApi } from '@/app/contexto'
import { Titulo } from '@/components/Titulo'
import { Botao } from '@/components/ui/botao'
import { ErroDoFormulario } from '@/components/ui/campo'
import { chaves } from '@/lib/api/consultas'
import { ApiError } from '@/lib/api/http'
import { mensagemDe } from '@/lib/api/mensagens'
import { MolduraPublica, TextoDeApoio, useTokenDoFragmento } from './comum'

type Estado = { fase: 'confirmando' } | { fase: 'confirmado' } | { fase: 'invalido' } | { fase: 'falhou'; mensagem: string }

// /confirm-email#token=…: o POST sai sozinho na carga, com o token do
// fragmento. Um GET de scanner de e-mail não executa script e não consome
// nada.
export function PaginaConfirmarEmail() {
  const token = useTokenDoFragmento()
  const api = useApi()
  const cliente = useQueryClient()
  const [estado, setEstado] = useState<Estado>(token ? { fase: 'confirmando' } : { fase: 'invalido' })

  const confirmar = useCallback(
    async (t: string) => {
      setEstado({ fase: 'confirmando' })
      try {
        await api.confirmarEmail(t)
        // O /me tem staleTime Infinity: sem isto, o painel aberto seguiria
        // com o aviso de e-mail não confirmado.
        void cliente.invalidateQueries({ queryKey: chaves.me })
        setEstado({ fase: 'confirmado' })
      } catch (e) {
        setEstado(
          e instanceof ApiError && e.code === 'token_invalid'
            ? { fase: 'invalido' }
            : { fase: 'falhou', mensagem: mensagemDe(e) },
        )
      }
    },
    [api, cliente],
  )

  // Uma vez por carga: o token é de uso único, e o StrictMode roda o efeito
  // duas vezes (o segundo POST voltaria token_invalid).
  const enviado = useRef(false)
  useEffect(() => {
    if (!token || enviado.current) return
    enviado.current = true
    void confirmar(token)
  }, [token, confirmar])

  return (
    <MolduraPublica>
      {estado.fase === 'confirmando' && (
        <>
          <Titulo key="confirmando" aba="Confirmar e-mail">
            Confirmando seu e-mail
          </Titulo>
          <p aria-busy="true" className="text-sm text-suave">
            Só um instante.
          </p>
        </>
      )}
      {estado.fase === 'confirmado' && (
        <>
          <Titulo key="confirmado" aba="E-mail confirmado">
            E-mail confirmado
          </Titulo>
          <TextoDeApoio>Sua conta está pronta para criar monitores.</TextoDeApoio>
          {/* Sem sessão, o guarda do painel leva ao login e volta para cá. */}
          <Botao asChild className="w-full">
            <Link to="/">Ir para o painel</Link>
          </Botao>
        </>
      )}
      {estado.fase === 'invalido' && (
        <>
          <Titulo key="invalido" aba="Link inválido ou vencido">
            Link inválido ou vencido
          </Titulo>
          <TextoDeApoio>
            O link de confirmação vale 24 horas e só funciona uma vez. Entre no StillOn e peça outro pelo aviso no topo
            do painel.
          </TextoDeApoio>
          <Botao asChild className="w-full">
            <Link to="/login">Entrar</Link>
          </Botao>
        </>
      )}
      {estado.fase === 'falhou' && (
        <>
          <Titulo key="falhou" aba="Não foi possível confirmar">
            Não foi possível confirmar
          </Titulo>
          <ErroDoFormulario>{estado.mensagem}</ErroDoFormulario>
          {/* O token ficou em memória; ele não volta à barra. */}
          <Botao className="w-full" onClick={() => token && void confirmar(token)}>
            Tentar de novo
          </Botao>
        </>
      )}
    </MolduraPublica>
  )
}
