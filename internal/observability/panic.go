package observability

import (
	"fmt"
	"runtime"
)

// DescreverPanic devolve o que do valor de um panic pode ir para o log. A
// mensagem vai só quando o tipo implementa runtime.Error, o que na prática quer
// dizer que ela saiu do próprio runtime (índice, tipo, nil) e não carrega dado;
// qualquer outro valor traz o que o código pôs nele, como a URL do webhook com
// o token, a chave do Resend ou a URL de um alvo com credencial, e dele só vai
// o tipo. Quem registra põe a pilha ao lado, para achar a origem.
func DescreverPanic(r any) string {
	if erro, ok := r.(runtime.Error); ok {
		return erro.Error()
	}
	return fmt.Sprintf("valor %T omitido", r)
}
