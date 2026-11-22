package heartbeat

import (
	"reflect"
	"slices"
	"testing"

	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// Alarme para quem mexe em InsertHeartbeatParams: todo campo da struct tem de
// estar numa das listas abaixo, e só numa. Um campo novo faz este teste falhar
// até alguém decidir se cloneParams o copia do original ou o gera de propósito
// para a cópia.
var (
	// PingResumes é configuração, como Enabled: a cópia retoma por ping se a
	// original retomava.
	camposCopiadosNoClone = []string{
		"Name", "Description", "PeriodSec", "CronExpr", "Timezone", "GraceSec", "Enabled", "PingResumes",
	}
	// A cópia nasce como uma criação: id, token e instantes são dela, e ela
	// vive no projeto do escopo com user_id = quem duplicou.
	camposNaoCopiadosDePropositoNoClone = []string{
		"ID", "PingTokenHash", "CreatedAt", "DeadlineAt", "UserID", "ProjectID",
	}
)

// Sabotagens: acrescentar "Inexistente" a uma das listas, ou tirar "Enabled"
// dela, faz o teste falhar.
func TestCloneParamsDecideCadaCampoDeInsertHeartbeatParams(t *testing.T) {
	tipo := reflect.TypeFor[db.InsertHeartbeatParams]()
	naStruct := make([]string, 0, tipo.NumField())
	for campo := range tipo.Fields() {
		naStruct = append(naStruct, campo.Name)
	}
	decididos := slices.Concat(camposCopiadosNoClone, camposNaoCopiadosDePropositoNoClone)

	slices.Sort(naStruct)
	slices.Sort(decididos)
	if !slices.Equal(naStruct, decididos) {
		t.Fatalf("InsertHeartbeatParams tem os campos %v, mas cloneParams decide sobre %v: "+
			"classifique o campo novo como copiado ou não copiado de propósito", naStruct, decididos)
	}
}
