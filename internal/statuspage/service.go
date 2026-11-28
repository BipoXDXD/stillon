package statuspage

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BipoXDXD/stillon/internal/clock"
	"github.com/BipoXDXD/stillon/internal/storage"
	"github.com/BipoXDXD/stillon/internal/storage/db"
	"github.com/BipoXDXD/stillon/internal/tenancy"
)

// Nomes que o Postgres dá aos UNIQUE da migration 00006, mais o índice único
// por projeto da 00040.
const (
	constraintUser    = "status_pages_user_id_key"
	constraintSlug    = "status_pages_slug_key"
	constraintProjeto = "status_pages_project_key"
)

// Service é o cadastro da página. Todo método do dono recebe o escopo de
// projeto logo depois de ctx e roda sob storage.InProjectTx, que confere a
// pertença e liga a RLS para aquele projeto: as queries filtram por project_id
// E a política filtra de novo. A permissão vale pelo papel lido NA transação
// (o escopo que InProjectTx entrega), nunca pelo que quem chamou trouxe. A
// página pública (public.go) não sabe nada de projeto.
type Service struct {
	pool *pgxpool.Pool
	clk  clock.Clock
	// A página pública (public.go): cache da projeção por slug, e voo para
	// que N visitantes com o cache vencido façam uma consulta só.
	cache cachePublico
	voo   singleflightGroup
	// depoisDeProjetar é um gancho de teste: roda entre a projeção e a
	// gravação no cache. Nil em produção.
	depoisDeProjetar func(slug string)
}

func NewService(pool *pgxpool.Pool, clk clock.Clock) *Service {
	return &Service{pool: pool, clk: clk}
}

// Get (PermRead) devolve a página do projeto, ou ErrNotFound se ainda não
// foi criada.
func (s *Service) Get(ctx context.Context, scope tenancy.ProjectScope) (Page, error) {
	var p Page
	err := storage.InProjectTx(ctx, s.pool, scope.UserID, scope.ProjectID, func(q *db.Queries, atual tenancy.ProjectScope) error {
		if err := atual.Require(tenancy.PermRead); err != nil {
			return err
		}
		var err error
		p, err = ler(ctx, q, atual.ProjectID)
		return err
	})
	return p, err
}

// Create (PermManageStatusPage) cria a página no projeto do escopo, com
// user_id = quem criou. A segunda do mesmo projeto é ErrExists, e também a
// segunda do mesmo usuário enquanto o UNIQUE de user_id existir (até o
// contract); o slug de outro é ErrSlugTaken. Os três vêm do UNIQUE do banco,
// e não de uma consulta antes, então valem também sob concorrência.
func (s *Service) Create(ctx context.Context, scope tenancy.ProjectScope, in Input) (Page, error) {
	if verr := validate(&in); verr != nil {
		return Page{}, verr
	}
	if slugReservado(in.Slug) {
		return Page{}, ErrSlugTaken
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Page{}, err
	}
	var p Page
	err = storage.InProjectTx(ctx, s.pool, scope.UserID, scope.ProjectID, func(q *db.Queries, atual tenancy.ProjectScope) error {
		if err := atual.Require(tenancy.PermManageStatusPage); err != nil {
			return err
		}
		if err := q.InsertStatusPage(ctx, db.InsertStatusPageParams{
			ID: id, UserID: atual.UserID, ProjectID: atual.ProjectID,
			Slug: in.Slug, Title: in.Title, Published: in.Published, CreatedAt: s.clk.Now(),
		}); err != nil {
			return traduzir(err)
		}
		if err := gravarItens(ctx, q, atual.ProjectID, id, in.Items); err != nil {
			return err
		}
		p, err = ler(ctx, q, atual.ProjectID)
		return err
	})
	if err == nil {
		s.cache.forget(in.Slug)
	}
	return p, err
}

// Update (PermManageStatusPage) aplica o patch à página id do projeto. A
// validação roda sobre o resultado, como se fosse um cadastro novo.
func (s *Service) Update(ctx context.Context, scope tenancy.ProjectScope, id uuid.UUID, patch Patch) (Page, error) {
	var p Page
	var slugAntigo string
	err := storage.InProjectTx(ctx, s.pool, scope.UserID, scope.ProjectID, func(q *db.Queries, atual tenancy.ProjectScope) error {
		if err := atual.Require(tenancy.PermManageStatusPage); err != nil {
			return err
		}
		antes, err := lerTravada(ctx, q, atual.ProjectID, id)
		if err != nil {
			return err
		}
		slugAntigo = antes.Slug
		in := aplicarPatch(antes, patch)
		if verr := validate(&in); verr != nil {
			return verr
		}
		// A página que já tem um slug hoje reservado fica com ele; só a
		// troca para um reservado é recusada.
		if in.Slug != antes.Slug && slugReservado(in.Slug) {
			return ErrSlugTaken
		}
		if err := regravar(ctx, q, atual.ProjectID, id, in, patch.Items); err != nil {
			return err
		}
		p, err = ler(ctx, q, atual.ProjectID)
		return err
	})
	if err == nil {
		// O slug antigo pode ser tomado por outro dono logo em seguida, e a
		// página despublicada tem de sair na hora: o cache deste processo
		// não pode servir a versão velha por mais 30s.
		s.cache.forget(slugAntigo)
		s.cache.forget(p.Slug)
	}
	return p, err
}

// lerTravada trava a página do projeto antes de ler, para que dois PATCH ao
// mesmo tempo não se sobreponham, e recusa a que não tem o id pedido.
func lerTravada(ctx context.Context, q *db.Queries, projectID, id uuid.UUID) (Page, error) {
	if _, err := q.LockStatusPage(ctx, projectID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Page{}, ErrNotFound
		}
		return Page{}, fmt.Errorf("travar status page: %w", err)
	}
	atual, err := ler(ctx, q, projectID)
	if err != nil {
		return Page{}, err
	}
	if atual.ID != id {
		return Page{}, ErrNotFound
	}
	return atual, nil
}

// aplicarPatch devolve o cadastro que resulta de pôr por cima da página atual
// só os campos que vieram no patch.
func aplicarPatch(atual Page, patch Patch) Input {
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
	return in
}

// regravar grava o cadastro já validado. A lista de itens só é trocada
// quando o patch a trouxe (novosItens não nulo): sem ela, os itens atuais
// ficam como estão. novosItens é o mesmo slice já validado em in.Items.
func regravar(ctx context.Context, q *db.Queries, projectID, id uuid.UUID, in Input, novosItens *[]Item) error {
	if _, err := q.UpdateStatusPage(ctx, db.UpdateStatusPageParams{
		ProjectID: projectID, ID: id, Slug: in.Slug, Title: in.Title, Published: in.Published,
	}); err != nil {
		return traduzir(err)
	}
	if novosItens == nil {
		return nil
	}
	// Os alvos são travados antes de apagar os itens antigos: é a ordem do
	// DELETE de um alvo (o alvo, depois os itens no cascade). Na ordem
	// inversa, um PATCH e a remoção de um alvo da lista se esperam (40P01).
	if err := conferirPosse(ctx, q, projectID, in.Items); err != nil {
		return err
	}
	if err := q.DeleteStatusPageItems(ctx, db.DeleteStatusPageItemsParams{ProjectID: projectID, PageID: id}); err != nil {
		return fmt.Errorf("apagar itens: %w", err)
	}
	return inserirItens(ctx, q, id, in.Items)
}

// gravarItens confere a posse dos alvos e grava os itens na ordem dada.
func gravarItens(ctx context.Context, q *db.Queries, projectID, pageID uuid.UUID, items []Item) error {
	if err := conferirPosse(ctx, q, projectID, items); err != nil {
		return err
	}
	return inserirItens(ctx, q, pageID, items)
}

// conferirPosse confere que cada alvo é do projeto e o trava até o fim da
// transação (OwnTargetIDs). Alvo de outro projeto, mesmo do mesmo usuário, é
// recusado. A RLS dos itens também recusa alvo alheio (WITH CHECK da
// migration 00006); esta checagem existe para dizer QUAL item está errado, em
// vez de um erro de política.
func conferirPosse(ctx context.Context, q *db.Queries, projectID uuid.UUID, items []Item) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(items))
	for i, it := range items {
		ids[i] = it.TargetID
	}
	meus, err := q.OwnTargetIDs(ctx, db.OwnTargetIDsParams{ProjectID: projectID, Ids: ids})
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
	return nil
}

func inserirItens(ctx context.Context, q *db.Queries, pageID uuid.UUID, items []Item) error {
	for i, it := range items {
		if err := q.InsertStatusPageItem(ctx, db.InsertStatusPageItemParams{
			StatusPageID: pageID, TargetID: it.TargetID, DisplayName: it.DisplayName, Position: int32(i),
		}); err != nil {
			return fmt.Errorf("gravar item: %w", err)
		}
	}
	return nil
}

func ler(ctx context.Context, q *db.Queries, projectID uuid.UUID) (Page, error) {
	row, err := q.GetStatusPage(ctx, projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Page{}, ErrNotFound
	}
	if err != nil {
		return Page{}, fmt.Errorf("buscar status page: %w", err)
	}
	itens, err := q.ListStatusPageItems(ctx, db.ListStatusPageItemsParams{ProjectID: projectID, PageID: row.ID})
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

// traduzir separa as violações de UNIQUE de status_pages. Página repetida é
// ErrExists pelo índice único de project_id. O UNIQUE de user_id saiu na
// 00046, mas fica aqui até o PR 7: num rollback para antes dela, o Down o
// devolve. O resto sai como erro interno.
func traduzir(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		switch pgErr.ConstraintName {
		case constraintUser, constraintProjeto:
			return ErrExists
		case constraintSlug:
			return ErrSlugTaken
		}
	}
	return fmt.Errorf("gravar status page: %w", err)
}
