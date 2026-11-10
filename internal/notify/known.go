package notify

import "github.com/BipoXDXD/stillon/internal/incident"

// Os tipos de canal e os eventos que este binário sabe entregar. A
// reivindicação da outbox só pega avisos que casam com as duas listas: depois
// de um rollback, um aviso de tipo ou evento gravado por uma versão mais nova
// fica pending, sem gastar tentativa, até essa versão voltar (D-13=a da SP2).
//
// Para entregar um tipo ou evento novo, acrescente-o aqui junto com o código
// que o entrega; esquecer deixa o aviso pendente para sempre. O PR 7 da SP1
// acrescenta "tls_expiring" a eventosConhecidos quando remove o filtro de
// incident_id da reivindicação.
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
