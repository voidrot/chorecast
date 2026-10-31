package chores

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
)

// CatalogItem is a location or a piece of equipment.
type CatalogItem struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// CatalogInput creates or renames a catalog item.
type CatalogInput struct {
	Name        string `json:"name" minLength:"1" maxLength:"60"`
	Description string `json:"description,omitempty" maxLength:"500"`
}

// Catalog is CRUD over one of the two catalogs (locations or equipment).
type Catalog struct {
	kind   string // for messages: "location" or "equipment"
	list   func(context.Context) ([]CatalogItem, error)
	create func(context.Context, CatalogInput, string) (CatalogItem, error)
	update func(context.Context, int64, CatalogInput) (int64, error)
	remove func(context.Context, int64) (int64, error)
	now    func() string
}

func (s *Service) Locations() *Catalog {
	q := s.db.Queries
	return &Catalog{
		kind: "location",
		list: func(ctx context.Context) ([]CatalogItem, error) {
			rows, err := q.ListLocations(ctx)
			out := make([]CatalogItem, 0, len(rows))
			for _, r := range rows {
				out = append(out, CatalogItem{ID: r.ID, Name: r.Name, Description: r.Description})
			}
			return out, err
		},
		create: func(ctx context.Context, in CatalogInput, now string) (CatalogItem, error) {
			r, err := q.CreateLocation(ctx, sqlc.CreateLocationParams{Name: in.Name, Description: in.Description, CreatedAt: now})
			return CatalogItem{ID: r.ID, Name: r.Name, Description: r.Description}, err
		},
		update: func(ctx context.Context, id int64, in CatalogInput) (int64, error) {
			return q.UpdateLocation(ctx, sqlc.UpdateLocationParams{Name: in.Name, Description: in.Description, ID: id})
		},
		remove: q.DeleteLocation,
		now:    func() string { return db.Timestamp(s.clock.Now()) },
	}
}

func (s *Service) Equipment() *Catalog {
	q := s.db.Queries
	return &Catalog{
		kind: "equipment",
		list: func(ctx context.Context) ([]CatalogItem, error) {
			rows, err := q.ListEquipment(ctx)
			out := make([]CatalogItem, 0, len(rows))
			for _, r := range rows {
				out = append(out, CatalogItem{ID: r.ID, Name: r.Name, Description: r.Description})
			}
			return out, err
		},
		create: func(ctx context.Context, in CatalogInput, now string) (CatalogItem, error) {
			r, err := q.CreateEquipment(ctx, sqlc.CreateEquipmentParams{Name: in.Name, Description: in.Description, CreatedAt: now})
			return CatalogItem{ID: r.ID, Name: r.Name, Description: r.Description}, err
		},
		update: func(ctx context.Context, id int64, in CatalogInput) (int64, error) {
			return q.UpdateEquipment(ctx, sqlc.UpdateEquipmentParams{Name: in.Name, Description: in.Description, ID: id})
		},
		remove: q.DeleteEquipment,
		now:    func() string { return db.Timestamp(s.clock.Now()) },
	}
}

func (c *Catalog) List(ctx context.Context) ([]CatalogItem, error) { return c.list(ctx) }

func (c *Catalog) clean(in CatalogInput) (CatalogInput, error) {
	in.Name, in.Description = strings.TrimSpace(in.Name), strings.TrimSpace(in.Description)
	if in.Name == "" {
		return in, apierr.New(http.StatusUnprocessableEntity, apierr.CodeValidationFailed, "a name is required",
			&huma.ErrorDetail{Location: "body.name", Message: "a name is required"})
	}
	return in, nil
}

func (c *Catalog) conflict(err error) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return apierr.New(http.StatusConflict, apierr.CodeConflict, "there's already a "+c.kind+" with that name")
	}
	return err
}

func (c *Catalog) Create(ctx context.Context, in CatalogInput) (CatalogItem, error) {
	in, err := c.clean(in)
	if err != nil {
		return CatalogItem{}, err
	}
	item, err := c.create(ctx, in, c.now())
	return item, c.conflict(err)
}

func (c *Catalog) Update(ctx context.Context, id int64, in CatalogInput) (CatalogItem, error) {
	in, err := c.clean(in)
	if err != nil {
		return CatalogItem{}, err
	}
	n, err := c.update(ctx, id, in)
	if err = c.conflict(err); err != nil {
		return CatalogItem{}, err
	}
	if n == 0 {
		return CatalogItem{}, apierr.New(http.StatusNotFound, apierr.CodeNotFound, "no such "+c.kind)
	}
	return CatalogItem{ID: id, Name: in.Name, Description: in.Description}, nil
}

// Delete removes the item; chores using it lose the link.
func (c *Catalog) Delete(ctx context.Context, id int64) error {
	n, err := c.remove(ctx, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return apierr.New(http.StatusNotFound, apierr.CodeNotFound, "no such "+c.kind)
	}
	return nil
}
