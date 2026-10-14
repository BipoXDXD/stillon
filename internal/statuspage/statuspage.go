// Package statuspage é a status page pública (spec §10): o cadastro do dono
// (GET/POST/PATCH /status-pages) e a projeção pública de /p/{slug}.
//
// Uma página por usuário (spec §2.1). O dono escolhe quais alvos entram,
// com que nome de exibição e em que ordem. O que sai para o público é só o
// que a spec §5.6 permite; a projeção pública tem tipos próprios, e nenhum
// deles carrega URL, id ou mensagem de erro.
package statuspage

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/rotulo"
)

var (
	ErrNotFound = errors.New("status page não encontrada")
	// ErrExists: o usuário já tem a página dele (uma por usuário).
	ErrExists = errors.New("o usuário já tem uma status page")
	// ErrSlugTaken: outro usuário já publicou (ou reservou) este slug.
	ErrSlugTaken = errors.New("slug em uso")
)

// Motivos de validação: o mesmo vocabulário fechado de targets.
const (
	ReasonRequired   = "required"
	ReasonInvalid    = "invalid"
	ReasonOutOfRange = "out_of_range"
	ReasonTooLong    = "too_long"
)

// Limites. Os de texto têm par num CHECK da migration 00006.
const (
	MaxItems        = 20
	maxTitleRunes   = 100
	maxDisplayRunes = 100
)

// slugRE é o CHECK de status_pages.slug: 3 a 40 caracteres, minúsculas,
// dígitos e hífen, sem hífen nas pontas.
var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$`)

type Item struct {
	TargetID    uuid.UUID
	DisplayName string
}

type Page struct {
	ID        uuid.UUID
	Slug      string
	Title     string
	Published bool
	Items     []Item // na ordem de exibição
	CreatedAt time.Time
}

type Input struct {
	Slug      string
	Title     string
	Published bool
	Items     []Item
}

// Patch muda só o que vier preenchido. Items, quando vem, troca a lista
// inteira: a ordem é a posição na lista, e um PATCH por item exigiria
// reordenar os outros.
type Patch struct {
	Slug, Title *string
	Published   *bool
	Items       *[]Item
}

type FieldError struct {
	Field  string
	Reason string
}

type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string { return "dados da status page inválidos" }

func (e *ValidationError) add(field, reason string) {
	e.Fields = append(e.Fields, FieldError{Field: field, Reason: reason})
}

// validate confere tudo e acumula os erros, como targets. Normaliza no
// lugar: título e nomes de exibição sem espaço nas pontas. O slug NÃO é
// normalizado: ele vira URL pública, e o dono tem de ver exatamente o que
// publica.
func validate(in *Input) *ValidationError {
	var v ValidationError
	switch {
	case in.Slug == "":
		v.add("slug", ReasonRequired)
	case !slugRE.MatchString(in.Slug):
		v.add("slug", ReasonInvalid)
	}
	in.Title = strings.TrimSpace(in.Title)
	if motivo := texto(in.Title, maxTitleRunes); motivo != "" {
		v.add("title", motivo)
	}
	if len(in.Items) > MaxItems {
		v.add("items", ReasonOutOfRange)
		return &v
	}
	vistos := map[uuid.UUID]bool{}
	for i := range in.Items {
		it := &in.Items[i]
		it.DisplayName = strings.TrimSpace(it.DisplayName)
		if motivo := texto(it.DisplayName, maxDisplayRunes); motivo != "" {
			v.add(fmt.Sprintf("items[%d].display_name", i), motivo)
		}
		if it.TargetID == uuid.Nil || vistos[it.TargetID] {
			v.add(fmt.Sprintf("items[%d].target_id", i), ReasonInvalid)
		}
		vistos[it.TargetID] = true
	}
	if len(v.Fields) > 0 {
		return &v
	}
	return nil
}

func texto(s string, max int) string {
	switch {
	case s == "":
		return ReasonRequired
	case utf8.RuneCountInString(s) > max:
		return ReasonTooLong
	// A inversão bidi e o espaço de largura zero fariam o título ou o nome
	// mentir na página pública.
	case rotulo.TemInvisivel(s):
		return ReasonInvalid
	}
	return ""
}
