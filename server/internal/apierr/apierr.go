// Package apierr defines the API's error format: RFC 9457 problem details
// (application/problem+json) plus a stable machine-readable "code". Clients branch on code,
// never on detail text. Importing this package installs it as Huma's error constructor, so
// every error (validation, auth, handler) uses the same shape.
package apierr

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// Error codes. Stable once released: add new ones, never rename.
const (
	CodeBadRequest           = "bad_request"
	CodeUnauthenticated      = "unauthenticated"
	CodeForbidden            = "forbidden"
	CodeInsufficientScope    = "insufficient_scope"
	CodeNoAccessPolicy       = "no_access_policy"
	CodeNotFound             = "not_found"
	CodeMethodNotAllowed     = "method_not_allowed"
	CodeConflict             = "conflict"
	CodePayloadTooLarge      = "payload_too_large"
	CodeUnsupportedMedia     = "unsupported_media_type"
	CodeValidationFailed     = "validation_failed"
	CodeRateLimited          = "rate_limited"
	CodeInternal             = "internal_error"
	CodeUnavailable          = "unavailable"
	CodeSetupRequired        = "setup_required" // first-run setup not done yet (503); clients go to setup
	CodeInvalidSetupToken    = "invalid_setup_token"
	CodeSetupComplete        = "setup_complete"         // setup endpoints after setup is done (409)
	CodeInvalidCredentials   = "invalid_credentials"    //nolint:gosec // error code, not a credential; login: wrong username or password (401)
	CodeTOTPRequired         = "totp_required"          // login: password ok, now send totpCode or recoveryCode (401)
	CodeInvalidTOTP          = "invalid_totp_code"      // wrong, reused or expired 2FA or recovery code
	CodeInvalidPassword      = "invalid_password"       // re-entered password is wrong (403)
	CodeTOTPAlreadyEnabled   = "totp_already_enabled"   // (409)
	CodeTOTPNotEnabled       = "totp_not_enabled"       // (409)
	CodeTOTPSetupNotStarted  = "totp_setup_not_started" // enable before setup (409)
	CodeOIDCDisabled         = "oidc_disabled"          // SSO isn't configured (404)
	CodeOIDCInvalidCode      = "oidc_invalid_code"      // app exchange: unknown, expired or mismatched code (400)
	CodeOIDCNotLinked        = "oidc_not_linked"        // unlink when no SSO identity is linked (409)
	CodeInvalidInvite        = "invalid_invite"         // unknown, used, revoked or expired invite (410)
	CodeUsernameTaken        = "username_taken"         // (409)
	CodeLastOwner            = "last_owner"             // the household must keep at least one owner (409)
	CodeParentNotFound       = "parent_not_found"       // (404)
	CodeInvalidPairingCode   = "invalid_pairing_code"   // unknown, used or expired pairing code (400)
	CodeInvalidPIN           = "invalid_pin"            // wrong kid or parent PIN on a device (403)
	CodePINLocked            = "pin_locked"             // too many wrong PINs on this device (429)
	CodeNotADevice           = "not_a_device"           // the caller isn't a paired device (403)
	CodeInvalidCursor        = "invalid_cursor"
	CodeKidNotFound          = "kid_not_found"
	CodeInvalidIdempotency   = "invalid_idempotency_key"
	CodeIdempotencyReuse     = "idempotency_key_reuse"
	CodeIdempotencyInFlight  = "idempotency_key_in_progress"
	CodePointsEntryNotFound  = "points_entry_not_found"     // (404)
	CodeAlreadyReversed      = "already_reversed"           // a points entry can be reversed only once (409)
	CodeAlreadyPosted        = "already_posted"             // a points entry for this kind and source exists (409)
	CodeChoreHasHistory      = "chore_has_history"          // a chore with history is disabled, not deleted (409)
	CodeDuplicateMedia       = "duplicate_media"            // a kid's photo matches earlier kid evidence exactly (409)
	CodeMediaNotFound        = "media_not_found"            // (404)
	CodeMediaExpired         = "media_expired"              // the media file was deleted, e.g. by retention (410)
	CodeInvalidMediaURL      = "invalid_media_url"          // a media URL's signature is wrong or has expired (403)
	CodeImageTooLarge        = "image_too_large"            // the image's dimensions exceed the side or pixel cap (413)
	CodeImageTooComplex      = "image_too_complex"          // decoding the image would need too much memory (413)
	CodeMediaBusy            = "media_busy"                 // too many uploads waiting to be processed; see Retry-After (503)
	CodeInvalidImage         = "invalid_image"              // recognized format, but the image doesn't decode (422)
	CodeInstanceNotFound     = "instance_not_found"         // (404)
	CodeInstanceNotOpen      = "instance_not_open"          // complete or reassign needs an open instance (409)
	CodeInstanceNotSubmitted = "instance_not_submitted"     // approve or reject needs a submitted instance (409)
	CodeInstanceNotApproved  = "instance_not_approved"      // reversing an approval needs an approved instance (409)
	CodeInstanceNotMissed    = "instance_not_missed"        // excuse needs a missed instance (409)
	CodeInstancePlanned      = "instance_planned"           // a later day's instance can't be completed yet (409)
	CodeInstanceShared       = "instance_shared"            // a shared instance has no single assignee to reassign (409)
	CodeInstanceTaken        = "instance_taken"             // reassign: the kid already has this occurrence (409)
	CodeInstanceClosed       = "instance_closed"            // complete: the instance's day (or week) has closed; day close takes it as missed (409)
	CodeVideoUnsupported     = "video_evidence_unsupported" // complete: the chore asks for a video, which the server can't take yet (422)
)

var defaultCodes = map[int]string{
	http.StatusBadRequest:            CodeBadRequest,
	http.StatusUnauthorized:          CodeUnauthenticated,
	http.StatusForbidden:             CodeForbidden,
	http.StatusNotFound:              CodeNotFound,
	http.StatusMethodNotAllowed:      CodeMethodNotAllowed,
	http.StatusConflict:              CodeConflict,
	http.StatusRequestEntityTooLarge: CodePayloadTooLarge,
	http.StatusUnsupportedMediaType:  CodeUnsupportedMedia,
	http.StatusUnprocessableEntity:   CodeValidationFailed,
	http.StatusTooManyRequests:       CodeRateLimited,
	http.StatusInternalServerError:   CodeInternal,
	http.StatusServiceUnavailable:    CodeUnavailable,
}

// Problem is the JSON body of every error response.
type Problem struct {
	Type     string              `json:"type,omitempty" format:"uri" default:"about:blank" doc:"A URI reference identifying the problem type."`
	Title    string              `json:"title,omitempty" example:"Not Found" doc:"Short summary of the problem type."`
	Status   int                 `json:"status,omitempty" example:"404" doc:"HTTP status code."`
	Code     string              `json:"code" example:"kid_not_found" doc:"Stable machine-readable error code. Clients should branch on this, not on detail."`
	Detail   string              `json:"detail,omitempty" example:"No kid matches \"sam\"." doc:"Human-readable explanation of this occurrence."`
	Instance string              `json:"instance,omitempty" format:"uri" doc:"A URI reference identifying this occurrence."`
	Errors   []*huma.ErrorDetail `json:"errors,omitempty" doc:"Individual problems, e.g. one per invalid field."`
}

func (p *Problem) Error() string  { return p.Detail }
func (p *Problem) GetStatus() int { return p.Status }

// ContentType makes Huma send application/problem+json (or +cbor).
func (p *Problem) ContentType(ct string) string {
	switch ct {
	case "application/json":
		return "application/problem+json"
	case "application/cbor":
		return "application/problem+cbor"
	}
	return ct
}

// codeErr carries an explicit code through Huma's NewError(status, msg, errs...) signature.
type codeErr string

func (c codeErr) Error() string { return string(c) }

// WithCode sets an error's code when passed among the errs of huma.NewError or huma.WriteErr.
func WithCode(code string) error { return codeErr(code) }

// New returns an error response with an explicit code.
func New(status int, code, detail string, errs ...error) huma.StatusError {
	return huma.NewError(status, detail, append(errs, WithCode(code))...)
}

func newProblem(status int, msg string, errs ...error) huma.StatusError {
	p := &Problem{Status: status, Title: http.StatusText(status), Detail: msg}
	for _, e := range errs {
		var c codeErr
		switch {
		case e == nil:
		case errors.As(e, &c):
			p.Code = string(c)
		default:
			if d, ok := e.(huma.ErrorDetailer); ok {
				p.Errors = append(p.Errors, d.ErrorDetail())
			} else {
				p.Errors = append(p.Errors, &huma.ErrorDetail{Message: e.Error()})
			}
		}
	}
	if p.Code == "" {
		p.Code = DefaultCode(status)
	}
	return p
}

// DefaultCode is the code used when an error doesn't set one.
func DefaultCode(status int) string {
	if c, ok := defaultCodes[status]; ok {
		return c
	}
	if status >= 500 {
		return CodeInternal
	}
	return "error"
}

func init() { huma.NewError = newProblem }

// Handler returns a plain net/http handler that writes a problem response, for routes outside
// Huma (unknown paths and methods under the API prefix).
func Handler(status int) http.Handler {
	body, _ := json.Marshal(&Problem{Title: http.StatusText(status), Status: status, Code: DefaultCode(status)})
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	})
}
