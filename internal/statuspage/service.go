package statuspage

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/vigilume/internal/clock"
	"github.com/BipoXDXD/vigilume/internal/storage"
	"github.com/BipoXDXD/vigilume/internal/storage/db"
)

// Nomes que o Postgres dá aos UNIQUE da migration 00006.
const (
	constraintUser = "status_pages_user_id_key"
	constraintSlug = "status_pages_slug_key"
)

// Service é o cadastro do dono. Toda operação exige userID e roda sob
// storage.InUserTx (spec §5.5).
type Service struct {
	pool *pgxpool.Pool
	clk  clock.Clock
}

func NewService(pool *pgxpool.Pool, clk clock.Clock) *Service {
	return &Service{pool: pool, clk: clk}
}

// Get devolve a página do usuário, ou ErrNotFound se ele ainda não criou.
func (s *Service) Get(ctx context.Context, userID uuid.UUID) (Page, error) {
	var p Page
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		var err error
		p, err = ler(ctx, q, userID)
		return err
	})
	return p, err
}

// Create cria a página. A segunda do mesmo usuário é ErrExists, e o slug de
// outro é ErrSlugTaken: os dois vêm do UNIQUE do banco, e não de uma
// consulta antes, então valem também sob concorrência.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, in Input) (Page, error) {
	if verr := validate(&in); verr != nil {
		return Page{}, verr
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Page{}, err
	}
	var p Page
	err = storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		if err := q.InsertStatusPage(ctx, db.InsertStatusPageParams{
			ID: id, UserID: userID, Slug: in.Slug, Title: in.Title, Published: in.Published, CreatedAt: s.clk.Now(),
		}); err != nil {
			return traduzir(err)
		}
		if err := gravarItens(ctx, q, userID, id, in.Items); err != nil {
			return err
		}
		p, err = ler(ctx, q, userID)
		return err
	})
	return p, err
}

// Update aplica o patch à página id do usuário. A validação roda sobre o
// resultado, como se fosse um cadastro novo.
func (s *Service) Update(ctx context.Context, userID, id uuid.UUID, patch Patch) (Page, error) {
	var p Page
	err := storage.InUserTx(ctx, s.pool, userID, func(q *db.Queries) error {
		atual, err := ler(ctx, q, userID)
		if err != nil {
			return err
		}
		if atual.ID != id {
			return ErrNotFound
		}
		in := Input{Slug: atual.Slug, Title: atual.Title, Published: atual.Published, Items: atual.Items}
		if patch.Slug != nil {
			in.Slug = *patch.Slug
		}
		if patch.Title != nil {
			in.Title = *patch.Title
		}
		if patch.Published != nil {
			in.Published = *patch.Published
		}
		if patch.Items != nil {
			in.Items = *patch.Items
		}
		if verr := validate(&in); verr != nil {
			return verr
		}
		if _, err := q.UpdateStatusPage(ctx, db.UpdateStatusPageParams{
			UserID: userID, ID: id, Slug: in.Slug, Title: in.Title, Published: in.Published,
		}); err != nil {
			return traduzir(err)
		}
		if patch.Items != nil {
			if err := q.DeleteStatusPageItems(ctx, db.DeleteStatusPageItemsParams{UserID: userID, PageID: id}); err != nil {
				return fmt.Errorf("apagar itens: %w", err)
			}
			if err := gravarItens(ctx, q, userID, id, in.Items); err != nil {
				return err
			}
		}
		p, err = ler(ctx, q, userID)
		return err
	})
	return p, err
}

// gravarItens confere que cada alvo é do dono e grava na ordem dada. A RLS
// dos itens também recusa alvo alheio (WITH CHECK da migration 00006); esta
// checagem existe para dizer QUAL item está errado, em vez de um erro de
// política.
func gravarItens(ctx context.Context, q *db.Queries, userID, pageID uuid.UUID, items []Item) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(items))
	for i, it := range items {
		ids[i] = it.TargetID
	}
	meus, err := q.OwnTargetIDs(ctx, db.OwnTargetIDsParams{UserID: userID, Ids: ids})
	if err != nil {
		return fmt.Errorf("conferir alvos: %w", err)
	}
	dono := make(map[uuid.UUID]bool, len(meus))
	for _, id := range meus {
		dono[id] = true
	}
	var verr ValidationError
	for i, it := range items {
		if !dono[it.TargetID] {
			verr.add(fmt.Sprintf("items[%d].target_id", i), ReasonInvalid)
		}
	}
	if len(verr.Fields) > 0 {
		return &verr
	}
	for i, it := range items {
		if err := q.InsertStatusPageItem(ctx, db.InsertStatusPageItemParams{
			StatusPageID: pageID, TargetID: it.TargetID, DisplayName: it.DisplayName, Position: int32(i),
		}); err != nil {
			return fmt.Errorf("gravar item: %w", err)
		}
	}
	return nil
}

func ler(ctx context.Context, q *db.Queries, userID uuid.UUID) (Page, error) {
	row, err := q.GetStatusPage(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Page{}, ErrNotFound
	}
	if err != nil {
		return Page{}, fmt.Errorf("buscar status page: %w", err)
	}
	itens, err := q.ListStatusPageItems(ctx, db.ListStatusPageItemsParams{UserID: userID, PageID: row.ID})
	if err != nil {
		return Page{}, fmt.Errorf("listar itens: %w", err)
	}
	p := Page{
		ID: row.ID, Slug: row.Slug, Title: row.Title, Published: row.Published,
		Items: make([]Item, 0, len(itens)), CreatedAt: row.CreatedAt,
	}
	for _, it := range itens {
		p.Items = append(p.Items, Item{TargetID: it.TargetID, DisplayName: it.DisplayName})
	}
	return p, nil
}

// traduzir separa as duas violações de UNIQUE de status_pages. O resto sai
// como erro interno.
func traduzir(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		switch pgErr.ConstraintName {
		case constraintUser:
			return ErrExists
		case constraintSlug:
			return ErrSlugTaken
		}
	}
	return fmt.Errorf("gravar status page: %w", err)
}
