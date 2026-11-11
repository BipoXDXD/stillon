package notify

import "github.com/BipoXDXD/stillon/internal/incident"

// Os tipos de canal e os eventos que este binário sabe entregar. A
// reivindicação da outbox só pega avisos que casam com as duas listas: depois
// de um rollback, um aviso de tipo ou evento gravado por uma versão mais nova
// fica pending, sem gastar tentativa, até essa versão voltar.
//
// Para entregar um tipo ou evento novo, acrescente-o aqui junto com o código
// que o entrega; esquecer deixa o aviso pendente para sempre. Quando o
// filtro de incident_id sair da reivindicação, "tls_expiring" entra em
// eventosConhecidos.
var (
	kindsConhecidos   = []Kind{KindDiscord, KindEmail}
	eventosConhecidos = []string{incident.EventOpened, incident.EventResolved, incident.EventReminder}
)

// kindsParaReivindicar converte kindsConhecidos para o parâmetro text[] da
// consulta.
func kindsParaReivindicar() []string {
	s := make([]string, len(kindsConhecidos))
	for i, k := range kindsConhecidos {
		s[i] = string(k)
	}
	return s
}
