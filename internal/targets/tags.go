package targets

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"

	"github.com/BipoXDXD/stillon/internal/rotulo"
	"github.com/BipoXDXD/stillon/internal/storage/db"
)

// MaxTagsPerTarget é o teto de rotulo.MaxTags, no vocabulário deste pacote.
const MaxTagsPerTarget = rotulo.MaxTags

// EstadoPausado é o estado de filtro do alvo desativado. O resumo diz
// "unknown" para ele, e o painel o distingue pelo enabled.
const EstadoPausado = "paused"

var estadosDoFiltro = []string{"up", "degraded", "down", "unknown", EstadoPausado}

// SummaryFilter restringe o resumo. Vazio, nada é filtrado.
type SummaryFilter struct {
	Tags   []string // todas têm de estar no alvo (E)
	States []string // qualquer um serve (OU)
}

// ParseSummaryFilter é a fronteira do filtro: devolve um filtro válido, ou o
// campo e o motivo. Todo valor passa pela mesma regra da escrita.
func ParseSummaryFilter(tags, estados []string) (SummaryFilter, *ValidationError) {
	var v ValidationError
	f := SummaryFilter{}
	if len(tags) > MaxTagsPerTarget {
		v.add("tag", ReasonOutOfRange)
	} else {
		for _, cru := range tags {
			tag, motivo := rotulo.ParseTag(cru)
			if motivo != "" {
				v.add("tag", motivo)
				break
			}
			f.Tags = append(f.Tags, tag)
		}
	}
	if len(estados) > len(estadosDoFiltro) {
		v.add("state", ReasonOutOfRange)
	} else {
		for _, e := range estados {
			if !slices.Contains(estadosDoFiltro, e) {
				v.add("state", ReasonInvalid)
				break
			}
			f.States = append(f.States, e)
		}
	}
	if len(v.Fields) > 0 {
		return SummaryFilter{}, &v
	}
	return f, nil
}

func (f SummaryFilter) accepts(s Summary) bool {
	for _, tag := range f.Tags {
		if !slices.Contains(s.Tags, tag) {
			return false
		}
	}
	return len(f.States) == 0 || slices.Contains(f.States, estadoDoFiltro(s))
}

func estadoDoFiltro(s Summary) string {
	if !s.Enabled {
		return EstadoPausado
	}
	return string(s.State)
}

func lerTags(ctx context.Context, q *db.Queries, userID, targetID uuid.UUID) ([]string, error) {
	tags, err := q.ListTargetTags(ctx, db.ListTargetTagsParams{UserID: userID, TargetID: targetID})
	if err != nil {
		return nil, fmt.Errorf("ler tags do alvo: %w", err)
	}
	if tags == nil {
		return []string{}, nil
	}
	return tags, nil
}

func tagsDoDono(ctx context.Context, q *db.Queries, userID uuid.UUID) (map[uuid.UUID][]string, error) {
	linhas, err := q.ListUserTargetTags(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("ler tags do dono: %w", err)
	}
	tags := make(map[uuid.UUID][]string, len(linhas))
	for _, l := range linhas {
		tags[l.TargetID] = append(tags[l.TargetID], l.Tag)
	}
	return tags, nil
}

func tagsDoAlvo(tags map[uuid.UUID][]string, id uuid.UUID) []string {
	if t, ok := tags[id]; ok {
		return t
	}
	return []string{}
}

func trocarTags(ctx context.Context, q *db.Queries, userID, targetID uuid.UUID, tags []string) error {
	if err := q.DeleteTargetTags(ctx, db.DeleteTargetTagsParams{UserID: userID, TargetID: targetID}); err != nil {
		return fmt.Errorf("apagar tags do alvo: %w", err)
	}
	err := q.InsertTargetTags(ctx, db.InsertTargetTagsParams{Tags: tags, UserID: userID, TargetID: targetID})
	if err != nil {
		return fmt.Errorf("gravar tags do alvo: %w", err)
	}
	return nil
}
