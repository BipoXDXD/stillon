package tenancy

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/BipoXDXD/stillon/internal/rotulo"
)

// MaxProjectNameRunes é o mesmo teto do CHECK de projects.name (migration
// 00039): o domínio recusa antes de o banco recusar.
const MaxProjectNameRunes = 100

// Teto de projetos de que uma conta pode ser titular: o operador tem folga
// para administrar; os demais, uma cota pequena.
const (
	MaxProjectsPerTitular  = 3
	MaxProjectsForOperator = 100
)

var (
	ErrProjectNameRequired = errors.New("nome do projeto obrigatório")
	ErrProjectNameTooLong  = errors.New("nome do projeto longo demais")
	ErrProjectNameInvalid  = errors.New("nome do projeto com caractere invisível ou de controle")
	ErrProjectLimit        = errors.New("limite de projetos atingido")
)

// ParseProjectName apara o espaço das pontas e devolve o nome normalizado, ou
// o motivo da recusa, na mesma ordem do nome de alvo: vazio, longo demais,
// caractere invisível.
func ParseProjectName(s string) (string, error) {
	name := strings.TrimSpace(s)
	switch {
	case name == "":
		return "", ErrProjectNameRequired
	case utf8.RuneCountInString(name) > MaxProjectNameRunes:
		return "", ErrProjectNameTooLong
	case rotulo.TemInvisivel(name):
		return "", ErrProjectNameInvalid
	}
	return name, nil
}

// ProjectLimit diz quantos projetos a conta pode ter como titular.
func ProjectLimit(isOperator bool) int {
	if isOperator {
		return MaxProjectsForOperator
	}
	return MaxProjectsPerTitular
}
