package tenancy

import (
	"fmt"

	"github.com/google/uuid"
)

// ProjectScope é quem age e em qual projeto, com o papel que tem nele.
type ProjectScope struct {
	ProjectID, UserID uuid.UUID
	Role              Role
}

// Require devolve ErrForbidden quando o papel do escopo não concede p.
func (s ProjectScope) Require(p Permission) error {
	if !s.Role.Can(p) {
		return fmt.Errorf("%w: papel %q", ErrForbidden, s.Role)
	}
	return nil
}
