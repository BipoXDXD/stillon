package heartbeat_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/heartbeat"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// outroDono grava um segundo usuário, dono de nada.
func (a ambiente) outroDono(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := a.pool.Exec(context.Background(),
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, 'x')`, id, id.String()+"@exemplo.com"); err != nil {
		t.Fatal(err)
	}
	return id
}

// contarHeartbeats relê no banco quantas tarefas o usuário tem.
func (a ambiente) contarHeartbeats(t *testing.T, dono uuid.UUID) int64 {
	t.Helper()
	ctx := context.Background()
	var n int64
	err := storage.InUserTx(ctx, a.pool, dono, func(q *db.Queries) error {
		var err error
		n, err = q.CountHeartbeats(ctx, dono)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// A validação do cadastro mora só no Service.Create: o handler entrega o
// corpo sem validar nome, descrição, tolerância nem tags. Cadastro recusado
// não grava linha nenhuma.
func TestCreateRejectsInvalidInputAndWritesNothing(t *testing.T) {
	a := novoAmbiente(t)
	svc := heartbeat.NewService(a.pool, a.clk)
	valido := heartbeat.Input{Name: "backup", Schedule: hora, Grace: tolerancia, Enabled: true}
	casos := []struct {
		nome   string
		mudar  func(*heartbeat.Input)
		campo  string
		motivo string
	}{
		{"nome vazio", func(in *heartbeat.Input) { in.Name = "" }, "name", heartbeat.ReasonRequired},
		{"nome com um caractere a mais que o teto", func(in *heartbeat.Input) {
			in.Name = strings.Repeat("é", heartbeat.MaxNameLength+1)
		}, "name", heartbeat.ReasonOutOfRange},
		{"descrição com um caractere a mais que o teto", func(in *heartbeat.Input) {
			in.Description = strings.Repeat("é", heartbeat.MaxDescriptionLength+1)
		}, "description", heartbeat.ReasonOutOfRange},
		{"tolerância um segundo abaixo do piso", func(in *heartbeat.Input) {
			in.Grace = heartbeat.MinGrace - time.Second
		}, "grace_sec", heartbeat.ReasonOutOfRange},
		{"tolerância um segundo acima do teto", func(in *heartbeat.Input) {
			in.Grace = heartbeat.MaxGrace + time.Second
		}, "grace_sec", heartbeat.ReasonOutOfRange},
		{"nove tags", func(in *heartbeat.Input) {
			in.Tags = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}
		}, "tags", "out_of_range"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			in := valido
			c.mudar(&in)
			got, token, err := svc.Create(t.Context(), a.dono, in)
			var ve *heartbeat.ValidationError
			if !errors.As(err, &ve) || ve.Field != c.campo || ve.Reason != c.motivo {
				t.Fatalf("Create = (%v, %v); queria %s %s", got.ID, err, c.campo, c.motivo)
			}
			if token != "" {
				t.Errorf("token %q devolvido num cadastro recusado", token)
			}
			if n := a.contarHeartbeats(t, a.dono); n != 0 {
				t.Errorf("%d heartbeats gravados depois do cadastro recusado", n)
			}
		})
	}
}

// A cópia leva a configuração do original, inclusive os valores que não são o
// padrão do cadastro (pausado, retomada por ping, cron com fuso, tolerância de
// 10 min), e nasce sem nada do que o original viveu, com token próprio.
func TestCloneCopiesTheConfigurationAndStartsFresh(t *testing.T) {
	a := novoAmbiente(t)
	svc := heartbeat.NewService(a.pool, a.clk)
	ctx := t.Context()
	cron, err := heartbeat.NewCron("0 3 * * *", "America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}
	original, tokenOriginal, err := svc.Create(ctx, a.dono, heartbeat.Input{
		Name: "backup", Description: "dump noturno", Tags: []string{"banco", "noite"},
		Schedule: cron, Grace: 10 * time.Minute, Enabled: false, PingResumes: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	a.pingar(t, tokenOriginal, heartbeat.SignalFail)
	if linha := a.linha(t, original.ID); linha.LastPingAt == nil || linha.FailingSince == nil {
		t.Fatalf("o original devia ter vivido um ping de falha: %+v", linha)
	}

	// 13:00 UTC = 10:00 em São Paulo; o próximo disparo é 03:00 de amanhã
	// (06:00 UTC), mais os 10 min de tolerância.
	a.clk.Advance(time.Hour)
	copia, tokenCopia, err := svc.Clone(ctx, a.dono, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tokenCopia == "" || tokenCopia == tokenOriginal {
		t.Fatalf("token da cópia %q; queria um novo, diferente do original", tokenCopia)
	}

	lida, err := svc.Get(ctx, a.dono, copia.ID)
	if err != nil {
		t.Fatal(err)
	}
	if lida.Name != "backup (cópia)" || lida.Description != "dump noturno" ||
		!slices.Equal(lida.Tags, []string{"banco", "noite"}) || lida.Grace != 10*time.Minute ||
		lida.Enabled || !lida.PingResumes || !lida.Schedule.Equal(cron) {
		t.Errorf("cópia relida: %+v; queria a configuração do original com o sufixo no nome", lida)
	}
	if want := time.Date(2026, 11, 6, 6, 10, 0, 0, time.UTC); !lida.DeadlineAt.Equal(want) {
		t.Errorf("prazo da cópia %v, queria %v", lida.DeadlineAt, want)
	}
	if want := time.Date(2026, 11, 5, 13, 0, 0, 0, time.UTC); !lida.CreatedAt.Equal(want) {
		t.Errorf("criação da cópia %v, queria %v", lida.CreatedAt, want)
	}

	linha := a.linha(t, copia.ID)
	if linha.LastPingAt != nil || linha.LastSuccessAt != nil || linha.LastStartAt != nil ||
		linha.FailingSince != nil || linha.LastSignal != nil {
		t.Errorf("a cópia herdou estado do original: %+v", linha)
	}
	hash, ok := heartbeat.HashPingToken(tokenCopia)
	if !ok || !bytes.Equal(linha.PingTokenHash, hash) {
		t.Error("a linha da cópia não guarda o hash do token devolvido")
	}
	if ev := a.eventos(t, copia.ID); len(ev) != 0 {
		t.Errorf("a cópia nasceu com %d eventos do original", len(ev))
	}
}

// Duplicar a tarefa de outro usuário é ErrNotFound, e ninguém ganha linha.
func TestCloneOfAnotherUsersHeartbeatIsNotFoundAndWritesNothing(t *testing.T) {
	a := novoAmbiente(t)
	svc := heartbeat.NewService(a.pool, a.clk)
	alheio := a.outroDono(t)
	id, _ := a.novoHeartbeat(t)

	_, token, err := svc.Clone(t.Context(), alheio, id)
	if !errors.Is(err, heartbeat.ErrNotFound) || token != "" {
		t.Fatalf("Clone alheio = (%q, %v); queria ErrNotFound sem token", token, err)
	}
	if n := a.contarHeartbeats(t, alheio); n != 0 {
		t.Errorf("o outro usuário ganhou %d heartbeats", n)
	}
	if n := a.contarHeartbeats(t, a.dono); n != 1 {
		t.Errorf("o dono tem %d heartbeats, queria só o original", n)
	}
}

// A listagem traz só as tarefas de quem pede, cada uma com as suas tags; a
// sem tags vem com lista vazia, não nil. A tarefa e as tags do outro usuário
// ficam de fora.
func TestListShowsOnlyTheOwnersHeartbeatsWithTheirOwnTags(t *testing.T) {
	a := novoAmbiente(t)
	svc := heartbeat.NewService(a.pool, a.clk)
	ctx := t.Context()
	alheio := a.outroDono(t)
	criar := func(dono uuid.UUID, nome string, tags []string) uuid.UUID {
		t.Helper()
		h, _, err := svc.Create(ctx, dono, heartbeat.Input{Name: nome, Tags: tags, Schedule: hora, Grace: tolerancia, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		return h.ID
	}
	comBanco := criar(a.dono, "backup", []string{"banco"})
	semTags := criar(a.dono, "relatório", nil)
	comFila := criar(a.dono, "fila", []string{"fila", "noite"})
	criar(alheio, "do outro", []string{"segredo"})

	lista, err := svc.List(ctx, a.dono, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Ordem do id decrescente: o UUIDv7 mais novo primeiro.
	want := map[uuid.UUID][]string{comFila: {"fila", "noite"}, semTags: {}, comBanco: {"banco"}}
	ids := make([]uuid.UUID, 0, len(lista))
	for _, h := range lista {
		ids = append(ids, h.ID)
		tags, ok := want[h.ID]
		if !ok {
			t.Errorf("a lista do dono trouxe %s (%q), que não é dele", h.ID, h.Name)
			continue
		}
		if h.Tags == nil || !slices.Equal(h.Tags, tags) {
			t.Errorf("%q com tags %#v, queria %#v", h.Name, h.Tags, tags)
		}
	}
	if !slices.Equal(ids, []uuid.UUID{comFila, semTags, comBanco}) {
		t.Errorf("ids %v, queria %v", ids, []uuid.UUID{comFila, semTags, comBanco})
	}
}

// O cursor e o tamanho da página chegam à consulta: a página de 2 traz as duas
// mais novas, e depois do cursor vem só a mais velha.
func TestListPagesByCursorAndLimit(t *testing.T) {
	a := novoAmbiente(t)
	svc := heartbeat.NewService(a.pool, a.clk)
	ctx := t.Context()
	var ids []uuid.UUID
	for _, nome := range []string{"um", "dois", "três"} {
		h, _, err := svc.Create(ctx, a.dono, heartbeat.Input{Name: nome, Schedule: hora, Grace: tolerancia, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, h.ID)
	}
	nomes := func(hs []heartbeat.Heartbeat) []string {
		out := make([]string, 0, len(hs))
		for _, h := range hs {
			out = append(out, h.Name)
		}
		return out
	}

	primeira, err := svc.List(ctx, a.dono, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := nomes(primeira); !slices.Equal(got, []string{"três", "dois"}) {
		t.Errorf("primeira página %v, queria [três dois]", got)
	}
	segunda, err := svc.List(ctx, a.dono, &ids[1], 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := nomes(segunda); !slices.Equal(got, []string{"um"}) {
		t.Errorf("página depois de %q: %v, queria [um]", "dois", got)
	}
}

// O detalhe de uma tarefa sem tags traz lista vazia, não nil, e a tarefa de
// outro usuário é ErrNotFound.
func TestGetWithoutTagsAndOfAnotherUser(t *testing.T) {
	a := novoAmbiente(t)
	svc := heartbeat.NewService(a.pool, a.clk)
	id, _ := a.novoHeartbeat(t)

	h, err := svc.Get(t.Context(), a.dono, id)
	if err != nil {
		t.Fatal(err)
	}
	if h.Tags == nil || len(h.Tags) != 0 {
		t.Errorf("tags %#v, queria lista vazia não nil", h.Tags)
	}
	if _, err := svc.Get(t.Context(), a.outroDono(t), id); !errors.Is(err, heartbeat.ErrNotFound) {
		t.Errorf("Get alheio = %v; queria ErrNotFound", err)
	}
}
