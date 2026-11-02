package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/streaks"
)

func registerStreaks(hapi huma.API, h *handlers) {
	huma.Register(hapi, huma.Operation{
		OperationID: "getKidStreaks",
		Method:      http.MethodGet,
		Path:        "/kids/{kid}/streaks",
		Summary:     "A kid's daily and per-chore streaks",
		Description: "As of the kid's last closed day. Kids can read only their own.",
		Tags:        []string{"points"},
		Security:    auth.Require(auth.ScopePointsRead),
		Errors:      []int{http.StatusForbidden, http.StatusNotFound},
	}, h.getKidStreaks)
}

type KidStreaksOutput struct {
	Body streaks.KidStreaks
}

func (h *handlers) getKidStreaks(ctx context.Context, in *KidRefInput) (*KidStreaksOutput, error) {
	kidID, _, err := h.pointsKid(ctx, in.Kid)
	if err != nil {
		return nil, err
	}
	s, err := h.streaks.Get(ctx, kidID)
	if err != nil {
		return nil, err
	}
	return &KidStreaksOutput{Body: s}, nil
}
