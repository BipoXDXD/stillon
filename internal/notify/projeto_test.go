package notify

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/storage/testdb"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// escopoNo é o escopo de usuario no projeto com o papel dado, montado à mão:
// o serviço tem de conferir o papel no banco, não confiar neste.
func escopoNo(projeto, usuario uuid.UUID, papel tenancy.Role) tenancy.ProjectScope {
	return tenancy.ProjectScope{ProjectID: projeto, UserID: usuario, Role: papel}
}

// escopoPadrao é o escopo de usuario no Projeto padrão dele, criado na
// primeira chamada (como faz a rota antiga). Sai de storage.InProjectTx, com o
// papel lido do banco, e não montado à mão.
func escopoPadrao(t *testing.T, pool *pgxpool.Pool, usuario uuid.UUID) tenancy.ProjectScope {
	t.Helper()
	projeto := testdb.ProjetoPadrao(t, usuario)
	var sc tenancy.ProjectScope
	err := storage.InProjectTx(context.Background(), pool, usuario, projeto, func(_ *db.Queries, lido tenancy.ProjectScope) error {
		sc = lido
		return nil
	})
	if err != nil {
		t.Fatalf("escopo de %s no Projeto padrão: %v", usuario, err)
	}
	return sc
}

// escopo é o escopo de usuario no Projeto padrão dele.
func (b *bancadaCanais) escopo(t *testing.T, usuario uuid.UUID) tenancy.ProjectScope {
	t.Helper()
	return escopoPadrao(t, b.svc.pool, usuario)
}

// canalNoBanco lê, como superusuário, o projeto, o enabled e o hash do token
// de verificação do canal.
func canalNoBanco(t *testing.T, id uuid.UUID) (projeto uuid.UUID, ligado bool, hash []byte) {
	t.Helper()
	if err := testdb.Admin(t).QueryRow(context.Background(),
		`SELECT project_id, enabled, verify_token_hash FROM notif_channels WHERE id = $1`, id,
	).Scan(&projeto, &ligado, &hash); err != nil {
		t.Fatalf("ler o canal %s como superusuário: %v", id, err)
	}
	return projeto, ligado, hash
}

// Dois projetos do MESMO usuário: o canal de B não existe no escopo de A, nem
// para listar, nem para pausar, nem para reenviar a verificação, nem para
// apagar.
func TestChannelsOfOtherProjectOfSameUserAreNotFound(t *testing.T) {
	b := novaBancadaCanais(t, true)
	ctx := context.Background()
	projetoA := testdb.ProjetoPadrao(t, b.dono)
	projetoB := testdb.NovoProjeto(t, b.dono, "B")
	emA, emB := escopoNo(projetoA, b.dono, tenancy.RoleOwner), escopoNo(projetoB, b.dono, tenancy.RoleOwner)
	deB, err := b.svc.Create(ctx, emB, ChannelInput{Kind: KindEmail, Email: "pessoa@exemplo.com"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, hashAntes := canalNoBanco(t, deB.ID)
	enviados := len(b.email.links)

	if cs, err := b.svc.List(ctx, emA); err != nil || len(cs) != 0 {
		t.Errorf("List no escopo de A = %+v, %v; queria vazio e sem erro", cs, err)
	}
	if _, err := b.svc.SetEnabled(ctx, emA, deB.ID, false); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetEnabled no escopo de A: erro %v, queria ErrNotFound", err)
	}
	if err := b.svc.ResendVerification(ctx, emA, deB.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("ResendVerification no escopo de A: erro %v, queria ErrNotFound", err)
	}
	if err := b.svc.Delete(ctx, emA, deB.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete no escopo de A: erro %v, queria ErrNotFound", err)
	}

	projeto, ligado, hashDepois := canalNoBanco(t, deB.ID)
	if projeto != projetoB || !ligado || string(hashAntes) != string(hashDepois) {
		t.Errorf("o canal de B ficou no projeto %s, enabled %v, token trocado %v; queria %s, ligado e sem troca",
			projeto, ligado, string(hashAntes) != string(hashDepois), projetoB)
	}
	if len(b.email.links) != enviados {
		t.Errorf("o reenvio no escopo de A mandou %d e-mails", len(b.email.links)-enviados)
	}
	if cs, err := b.svc.List(ctx, emB); err != nil || len(cs) != 1 || cs[0].ID != deB.ID {
		t.Errorf("List no escopo de B = %+v, %v; queria só o canal %s", cs, err, deB.ID)
	}
}

// O member não mexe em canais, e a recusa vem antes de qualquer envio. O
// escopo traz owner de propósito: o papel que vale é o lido na transação, e no
// banco ele é member.
func TestMemberCannotManageChannels(t *testing.T) {
	b := novaBancadaCanais(t, true)
	ctx := context.Background()
	projeto := testdb.ProjetoPadrao(t, b.dono)
	testdb.Membro(t, projeto, b.outro, "member")
	doDono, err := b.svc.Create(ctx, escopoNo(projeto, b.dono, tenancy.RoleOwner),
		ChannelInput{Kind: KindEmail, Email: "dono@exemplo.com"})
	if err != nil {
		t.Fatal(err)
	}
	b.email = &carteiroFalso{}
	b.svc.email = b.email
	_, _, hashAntes := canalNoBanco(t, doDono.ID)
	comoMember := escopoNo(projeto, b.outro, tenancy.RoleOwner)

	for _, in := range []ChannelInput{
		{Kind: KindEmail, Email: "member@exemplo.com"},
		{Kind: KindDiscord, URL: webhookValido},
	} {
		if _, err := b.svc.Create(ctx, comoMember, in); !errors.Is(err, tenancy.ErrForbidden) {
			t.Errorf("Create(%s) como member: erro %v, queria ErrForbidden", in.Kind, err)
		}
	}
	if err := b.svc.ResendVerification(ctx, comoMember, doDono.ID); !errors.Is(err, tenancy.ErrForbidden) {
		t.Errorf("ResendVerification como member: erro %v, queria ErrForbidden", err)
	}
	if _, err := b.svc.SetEnabled(ctx, comoMember, doDono.ID, false); !errors.Is(err, tenancy.ErrForbidden) {
		t.Errorf("SetEnabled como member: erro %v, queria ErrForbidden", err)
	}
	if err := b.svc.Delete(ctx, comoMember, doDono.ID); !errors.Is(err, tenancy.ErrForbidden) {
		t.Errorf("Delete como member: erro %v, queria ErrForbidden", err)
	}

	if n := len(b.email.links); n != 0 {
		t.Errorf("o member fez sair %d e-mails, queria nenhum", n)
	}
	if n := b.discord.quantos(); n != 0 {
		t.Errorf("o member fez sair %d mensagens de teste do Discord, queria nenhuma", n)
	}
	// O member lê (PermRead) o canal que o titular criou: a política é por
	// projeto.
	lista, err := b.svc.List(ctx, comoMember)
	if err != nil {
		t.Errorf("List como member: erro %v, queria sem erro (member lê)", err)
	}
	if len(lista) != 1 || lista[0].ID != doDono.ID {
		t.Errorf("List como member trouxe %+v, queria só o canal %s do titular", lista, doDono.ID)
	}
	if _, ligado, hashDepois := canalNoBanco(t, doDono.ID); !ligado || string(hashAntes) != string(hashDepois) {
		t.Errorf("o canal do dono ficou enabled %v, token trocado %v; queria ligado e sem troca",
			ligado, string(hashAntes) != string(hashDepois))
	}
}
