package storage_test

import (
	"testing"
	"time"

	"github.com/BipoXDXD/stillon/internal/storage"
)

func TestTimeOrZeroNilGivesZeroTime(t *testing.T) {
	if got := storage.TimeOrZero(nil); !got.IsZero() {
		t.Fatalf("TimeOrZero(nil) = %v, queria o instante zero", got)
	}
}

func TestTimeOrZeroReturnsTheValue(t *testing.T) {
	saoPaulo, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatalf("carregar fuso: %v", err)
	}
	casos := []struct {
		nome string
		em   time.Time
	}{
		{"UTC", time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)},
		{"com fuso", time.Date(2026, 11, 11, 9, 30, 15, 123, saoPaulo)},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			em := c.em
			got := storage.TimeOrZero(&em)
			if !got.Equal(c.em) {
				t.Fatalf("TimeOrZero = %v, queria %v", got, c.em)
			}
			if got.Location() != c.em.Location() {
				t.Fatalf("fuso = %v, queria %v", got.Location(), c.em.Location())
			}
		})
	}
}

func TestTimeOrZeroPointerToZeroStaysZero(t *testing.T) {
	var zero time.Time
	if got := storage.TimeOrZero(&zero); !got.IsZero() {
		t.Fatalf("TimeOrZero(&zero) = %v, queria o instante zero", got)
	}
}

func TestTimeOrZeroReturnsACopy(t *testing.T) {
	original := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	em := original
	got := storage.TimeOrZero(&em)
	em = em.Add(time.Hour)
	if !got.Equal(original) {
		t.Fatalf("resultado mudou com o original: %v, queria %v", got, original)
	}
}
