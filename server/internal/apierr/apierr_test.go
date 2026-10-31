package apierr_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"

	"github.com/voidrot/chorecast/server/internal/apierr"
)

func decode(t *testing.T, b []byte) apierr.Problem {
	t.Helper()
	var p apierr.Problem
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatalf("not a problem body: %s (%v)", b, err)
	}
	return p
}

func TestDefaultAndExplicitCodes(t *testing.T) {
	tests := []struct {
		err  huma.StatusError
		code string
	}{
		{huma.Error404NotFound("nope"), apierr.CodeNotFound},
		{huma.Error401Unauthorized("who"), apierr.CodeUnauthenticated},
		{huma.Error422UnprocessableEntity("bad"), apierr.CodeValidationFailed},
		{huma.Error500InternalServerError("oops"), apierr.CodeInternal},
		{huma.NewError(418, "teapot"), "error"},
		{huma.NewError(507, "full"), apierr.CodeInternal},
		{apierr.New(http.StatusNotFound, apierr.CodeKidNotFound, "no kid"), apierr.CodeKidNotFound},
	}
	for _, tt := range tests {
		p, ok := tt.err.(*apierr.Problem)
		if !ok {
			t.Fatalf("%T is not *apierr.Problem: huma.NewError not installed", tt.err)
		}
		if p.Code != tt.code {
			t.Errorf("status %d: code = %q, want %q", p.Status, p.Code, tt.code)
		}
	}

	// The code marker never leaks into details; other errors become details.
	p := apierr.New(http.StatusBadRequest, "custom", "bad", errors.New("field x missing")).(*apierr.Problem)
	if len(p.Errors) != 1 || p.Errors[0].Message != "field x missing" || p.Code != "custom" {
		t.Errorf("problem = %+v", p)
	}
}

func TestProblemResponses(t *testing.T) {
	_, api := humatest.New(t)
	huma.Register(api, huma.Operation{OperationID: "missing", Method: http.MethodGet, Path: "/kids/{kid}"},
		func(context.Context, *struct {
			Kid string `path:"kid"`
		}) (*struct{}, error) {
			return nil, apierr.New(http.StatusNotFound, apierr.CodeKidNotFound, `no kid matches "sam"`)
		})
	type in struct {
		Body struct {
			Name string `json:"name" minLength:"1"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "create", Method: http.MethodPost, Path: "/things"},
		func(context.Context, *in) (*struct{}, error) { return nil, nil })

	resp := api.Get("/kids/sam")
	if resp.Code != 404 || resp.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("got %d %s", resp.Code, resp.Header().Get("Content-Type"))
	}
	if p := decode(t, resp.Body.Bytes()); p.Code != apierr.CodeKidNotFound || p.Title != "Not Found" || p.Detail == "" {
		t.Errorf("problem = %+v", p)
	}

	resp = api.Post("/things", map[string]any{"name": ""})
	if resp.Code != 422 {
		t.Fatalf("validation: got %d", resp.Code)
	}
	if p := decode(t, resp.Body.Bytes()); p.Code != apierr.CodeValidationFailed || len(p.Errors) == 0 {
		t.Errorf("validation problem = %+v", p)
	}
}

func TestHandler(t *testing.T) {
	rec := httptest.NewRecorder()
	apierr.Handler(http.StatusMethodNotAllowed).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	if rec.Code != 405 || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("got %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	if p := decode(t, rec.Body.Bytes()); p.Code != apierr.CodeMethodNotAllowed || p.Status != 405 {
		t.Errorf("problem = %+v", p)
	}
}
