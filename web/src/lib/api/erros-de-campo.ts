import { ApiError, type ErroDeCampo } from './http'

export type ErroDoCampo<C> = { campo: C; mensagem: string }

// Os erros do 400 com fields que a tela sabe pôr num campo, na ordem do
// servidor. Quem chama decide o que é "o campo" (seletor, índice de item) e a
// mensagem; o que `mapear` devolver como undefined é descartado. Lista vazia
// quer dizer "nada para marcar": a tela mostra o erro geral.
export function errosDosCampos<C>(
  err: unknown,
  mapear: (campoDoServidor: ErroDeCampo) => ErroDoCampo<C> | undefined,
): ErroDoCampo<C>[] {
  if (!(err instanceof ApiError)) return []
  return err.fields.flatMap((fe) => mapear(fe) ?? [])
}
