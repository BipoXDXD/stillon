package heartbeat_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/heartbeat"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// escopoNo é o escopo de usuario no projeto com o papel dado, montado à mão:
// o serviço tem de conferir o papel no banco, não confiar neste.
func escopoNo(projeto, usuario uuid.UUID, papel tenancy.Role) tenancy.ProjectScope {
	return tenancy.ProjectScope{ProjectID: projeto, UserID: usuario, Role: papel}
}

// heartbeatNoBanco lê, como superusuário, o projeto, o hash do token e o
// último ping do heartbeat.
func heartbeatNoBanco(t *testing.T, id uuid.UUID) (projeto uuid.UUID, hash []byte, pingou bool) {
	t.Helper()
	if err := testdb.Admin(t).QueryRow(context.Background(),
		`SELECT project_id, ping_token_hash, last_ping_at IS NOT NULL FROM heartbeats WHERE id = $1`, id,
	).Scan(&projeto, &hash, &pingou); err != nil {
		t.Fatalf("ler o heartbeat %s como superusuário: %v", id, err)
	}
	return projeto, hash, pingou
}

// Dois projetos do MESMO usuário: o heartbeat de B não existe no escopo de A,
// nem para ler, nem para trocar o token, nem para sinalizar, nem para
// reconhecer o incidente dele.
func TestHeartbeatsOfOtherProjectOfSameUserAreNotFound(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	svc := heartbeat.NewService(a.pool, a.clk)
	projetoA := testdb.ProjetoPadrao(t, a.dono)
	projetoB := testdb.NovoProjeto(t, a.dono, "B")
	emA, emB := escopoNo(projetoA, a.dono, tenancy.RoleOwner), escopoNo(projetoB, a.dono, tenancy.RoleOwner)
	deB, _, err := svc.Create(ctx, emB, heartbeat.Input{Name: "de B", Schedule: hora, Grace: tolerancia, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	incidente := uuid.Must(uuid.NewV7())
	semearComoAdmin(t, `INSERT INTO incidents (id, heartbeat_id, started_at, cause) VALUES ($1, $2, $3, 'heartbeat_late')`,
		incidente, deB.ID, agora)
	_, hashAntes, _ := heartbeatNoBanco(t, deB.ID)

	if _, err := svc.Get(ctx, emA, deB.ID); !errors.Is(err, heartbeat.ErrNotFound) {
		t.Errorf("Get no escopo de A: erro %v, queria ErrNotFound", err)
	}
	if _, _, err := svc.RotateToken(ctx, emA, deB.ID); !errors.Is(err, heartbeat.ErrNotFound) {
		t.Errorf("RotateToken no escopo de A: erro %v, queria ErrNotFound", err)
	}
	if _, err := svc.PingNow(ctx, emA, deB.ID); !errors.Is(err, heartbeat.ErrNotFound) {
		t.Errorf("PingNow no escopo de A: erro %v, queria ErrNotFound", err)
	}
	if _, err := svc.Ack(ctx, emA, deB.ID, incidente); !errors.Is(err, heartbeat.ErrNotFound) {
		t.Errorf("Ack no escopo de A: erro %v, queria ErrNotFound", err)
	}

	projeto, hashDepois, pingou := heartbeatNoBanco(t, deB.ID)
	if projeto != projetoB || !bytes.Equal(hashAntes, hashDepois) || pingou {
		t.Errorf("o heartbeat de B ficou no projeto %s, hash trocado %v, pingado %v; queria %s, sem troca e sem ping",
			projeto, !bytes.Equal(hashAntes, hashDepois), pingou, projetoB)
	}
	if _, err := svc.Get(ctx, emB, deB.ID); err != nil {
		t.Errorf("Get no escopo de B: %v, queria sem erro", err)
	}
}

// O viewer não troca o token nem sinaliza. O escopo traz owner de propósito:
// o papel que vale é o lido na transação, e no banco ele é viewer.
func TestViewerCannotRotateOrPingHeartbeat(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	svc := heartbeat.NewService(a.pool, a.clk)
	v := a.outroDono(t)
	projeto := testdb.ProjetoPadrao(t, a.dono)
	testdb.Membro(t, projeto, v, "viewer")
	h, _, err := svc.Create(ctx, escopoNo(projeto, a.dono, tenancy.RoleOwner),
		heartbeat.Input{Name: "backup", Schedule: hora, Grace: tolerancia, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_, hashAntes, _ := heartbeatNoBanco(t, h.ID)
	comoViewer := escopoNo(projeto, v, tenancy.RoleOwner)

	if _, _, err := svc.RotateToken(ctx, comoViewer, h.ID); !errors.Is(err, tenancy.ErrForbidden) {
		t.Errorf("RotateToken como viewer: erro %v, queria ErrForbidden", err)
	}
	if _, err := svc.PingNow(ctx, comoViewer, h.ID); !errors.Is(err, tenancy.ErrForbidden) {
		t.Errorf("PingNow como viewer: erro %v, queria ErrForbidden", err)
	}

	_, hashDepois, pingou := heartbeatNoBanco(t, h.ID)
	if !bytes.Equal(hashAntes, hashDepois) {
		t.Error("o ping_token_hash mudou depois do RotateToken do viewer")
	}
	if pingou {
		t.Error("o heartbeat ganhou last_ping_at depois do PingNow do viewer")
	}
}

// A cópia nasce no projeto do escopo, que aqui não é o Projeto padrão do
// usuário (que também existe).
func TestCloneStaysInScopeProject(t *testing.T) {
	a := novoAmbiente(t)
	ctx := context.Background()
	svc := heartbeat.NewService(a.pool, a.clk)
	testdb.ProjetoPadrao(t, a.dono)
	projetoA := testdb.NovoProjeto(t, a.dono, "A")
	emA := escopoNo(projetoA, a.dono, tenancy.RoleOwner)
	original, _, err := svc.Create(ctx, emA, heartbeat.Input{Name: "backup", Schedule: hora, Grace: tolerancia, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	copia, _, err := svc.Clone(ctx, emA, original.ID)
	if err != nil {
		t.Fatal(err)
	}

	if projeto, _, _ := heartbeatNoBanco(t, copia.ID); projeto != projetoA {
		t.Errorf("a cópia nasceu no projeto %s, queria %s", projeto, projetoA)
	}
}

// escopo é o escopo do dono do ambiente no Projeto padrão dele.
func (a ambiente) escopo(t *testing.T) tenancy.ProjectScope {
	t.Helper()
	return heartbeat.EscopoDe(t, a.pool, a.dono)
}

// escopoDe é o escopo de usuario no Projeto padrão dele.
func (a ambiente) escopoDe(t *testing.T, usuario uuid.UUID) tenancy.ProjectScope {
	t.Helper()
	return heartbeat.EscopoDe(t, a.pool, usuario)
}
