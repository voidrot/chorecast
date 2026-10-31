// Package devices pairs Android devices and runs the sessions on them. A parent creates a
// single-use, 5-minute pairing code (shown as a QR code with the server URL) for a kid or for a
// shared family tablet; the app trades it for a long-lived device token. A shared device shows a
// profile picker: picking a kid (with their PIN when required) starts a short kid session, and a
// parent can elevate with their parent PIN for two minutes. Five wrong parent PINs lock elevation
// on that device for 15 minutes. See docs/architecture.md (Flows 1-2).
package devices

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/apierr"
	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/db/sqlc"
	"github.com/voidrot/chorecast/server/internal/kids"
)

const (
	PairingTTL    = 5 * time.Minute
	KidSessionTTL = 8 * time.Hour // the app also locks back to the picker after inactivity
	ElevationTTL  = 2 * time.Minute

	elevateLimit  = 5
	elevateLock   = 15 * time.Minute
	pairingLength = 8 // characters, e.g. "k7mp-q2xd"
)

var (
	errInvalidCode = apierr.New(http.StatusBadRequest, apierr.CodeInvalidPairingCode,
		"this pairing code is wrong, already used or expired; create a new one")
	errInvalidPIN = apierr.New(http.StatusForbidden, apierr.CodeInvalidPIN, "wrong PIN")
	errNotDevice  = apierr.New(http.StatusForbidden, apierr.CodeNotADevice, "only a paired device can do this")
)

type Service struct {
	db     *db.DB
	clock  clock.Clock
	tokens *auth.Store
	pair   *auth.Limiter // failed pairing attempts per IP
	kidPIN *auth.Limiter // wrong kid PINs per device+kid
}

func New(d *db.DB, c clock.Clock) *Service {
	var q *sqlc.Queries
	if d != nil {
		q = d.Queries
	}
	return &Service{
		db: d, clock: c, tokens: auth.NewStore(q, c),
		pair:   auth.NewLimiter(c, 20, 15*time.Minute),
		kidPIN: auth.NewLimiter(c, elevateLimit, elevateLock),
	}
}

// DevicePairing is a newly created pairing code.
type DevicePairing struct {
	Code      string    `json:"code" example:"k7mp-q2xd" doc:"Shown once; type it into the app if the QR code can't be scanned"`
	Server    string    `json:"server" doc:"The server URL the app should use"`
	QR        string    `json:"qr" doc:"QR code content: JSON {\"server\":…,\"code\":…}"`
	ExpiresAt time.Time `json:"expiresAt"`
	KidID     int64     `json:"kidId,omitempty" doc:"The kid this device will belong to; absent for a shared family device"`
}

// CreatePairing makes a code for a kid's device (kidRef = id or slug) or, with kidRef empty, a
// shared family device.
func (s *Service) CreatePairing(ctx context.Context, by *auth.Principal, kidRef, server string) (DevicePairing, error) {
	var kidID sql.NullInt64
	if kidRef != "" {
		kid, err := kids.Resolve(ctx, s.db.Queries, kidRef)
		if err != nil {
			return DevicePairing{}, err
		}
		if kid.ArchivedAt.Valid {
			return DevicePairing{}, apierr.New(http.StatusConflict, apierr.CodeConflict, "unarchive this kid before pairing a device")
		}
		kidID = sql.NullInt64{Int64: kid.ID, Valid: true}
	}
	code, err := auth.NewShortCode(pairingLength)
	if err != nil {
		return DevicePairing{}, err
	}
	now := s.clock.Now()
	if _, err := s.db.CreatePairingCode(ctx, sqlc.CreatePairingCodeParams{
		CodeHash: auth.HashRecoveryCode(code), UserID: kidID, CreatedBy: sql.NullInt64{Int64: by.UserID, Valid: by.UserID != 0},
		CreatedAt: db.Timestamp(now), ExpiresAt: db.Timestamp(now.Add(PairingTTL)),
	}); err != nil {
		return DevicePairing{}, fmt.Errorf("create pairing code: %w", err)
	}
	server = strings.TrimRight(server, "/")
	return DevicePairing{
		Code: code, Server: server, ExpiresAt: now.Add(PairingTTL), KidID: kidID.Int64,
		QR: fmt.Sprintf(`{"server":%s,"code":%s}`, strconv.Quote(server), strconv.Quote(code)),
	}, nil
}

// Device is a paired device.
type Device struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Shared     bool      `json:"shared" doc:"A shared family device with a profile picker; otherwise it belongs to kidId"`
	KidID      int64     `json:"kidId,omitempty"`
	AppVersion string    `json:"appVersion,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

func toDevice(d sqlc.Device) Device {
	created, _ := db.ParseTimestamp(d.CreatedAt)
	return Device{ID: d.ID, Name: d.Name, Shared: !d.UserID.Valid, KidID: d.UserID.Int64, AppVersion: d.AppVersion, CreatedAt: created}
}

// Pair trades a pairing code for a device token.
func (s *Service) Pair(ctx context.Context, code, name, appVersion string) (Device, string, error) {
	ip := auth.RequestInfoFrom(ctx).IP
	if blocked, _ := s.pair.Blocked(ip); blocked {
		return Device{}, "", apierr.New(http.StatusTooManyRequests, apierr.CodeRateLimited, "too many wrong pairing codes; try again later")
	}
	var (
		dev   sqlc.Device
		token string
	)
	err := s.inTx(ctx, func(q *sqlc.Queries) error {
		pc, err := q.GetPairingCode(ctx, auth.HashRecoveryCode(code))
		if errors.Is(err, sql.ErrNoRows) {
			return errInvalidCode
		} else if err != nil {
			return err
		}
		now := s.clock.Now()
		if n, err := q.UsePairingCode(ctx, sqlc.UsePairingCodeParams{
			UsedAt: sql.NullString{String: db.Timestamp(now), Valid: true}, ID: pc.ID, ExpiresAt: db.Timestamp(now),
		}); err != nil {
			return err
		} else if n == 0 {
			return errInvalidCode
		}
		if pc.UserID.Valid {
			if kid, err := q.GetKidByID(ctx, pc.UserID.Int64); err != nil || kid.ArchivedAt.Valid {
				return errInvalidCode
			}
		}
		name = strings.TrimSpace(name)
		if name == "" {
			name = "Android device"
		}
		if dev, err = q.CreateDevice(ctx, sqlc.CreateDeviceParams{
			UserID: pc.UserID, Name: truncate(name, 60), AppVersion: truncate(appVersion, 30), CreatedAt: db.Timestamp(now),
		}); err != nil {
			return err
		}
		token, _, err = auth.NewStore(q, s.clock).Issue(ctx, auth.IssueParams{
			Kind: auth.TokenDevice, UserID: pc.UserID.Int64, DeviceID: dev.ID, Label: dev.Name,
		})
		return err
	})
	if err != nil {
		if errors.Is(err, errInvalidCode) {
			s.pair.Fail(ip)
		}
		return Device{}, "", err
	}
	return toDevice(dev), token, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// KidProfile is a kid on a shared device's picker.
type KidProfile struct {
	ID          int64  `json:"id"`
	Slug        string `json:"slug"`
	DisplayName string `json:"displayName"`
	Color       string `json:"color"`
	Avatar      string `json:"avatar,omitempty"`
	PINRequired bool   `json:"pinRequired" doc:"Ask for the kid's PIN (only when one is set)"`
}

// ParentProfile is a parent who can elevate on a device (they have a parent PIN).
type ParentProfile struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"displayName"`
}

// DevicePicker is what a device shows: its own details, the kid profiles (shared devices) and the
// parents who can elevate.
type DevicePicker struct {
	Device  Device          `json:"device"`
	Kids    []KidProfile    `json:"kids" nullable:"false"`
	Parents []ParentProfile `json:"parents" nullable:"false"`
}

func (s *Service) device(ctx context.Context, p *auth.Principal) (sqlc.Device, error) {
	if p.DeviceID == 0 {
		return sqlc.Device{}, errNotDevice
	}
	d, err := s.db.GetDevice(ctx, p.DeviceID)
	if errors.Is(err, sql.ErrNoRows) {
		return sqlc.Device{}, errNotDevice
	}
	return d, err
}

// Picker returns the profile picker for the calling device.
func (s *Service) Picker(ctx context.Context, p *auth.Principal) (DevicePicker, error) {
	d, err := s.device(ctx, p)
	if err != nil {
		return DevicePicker{}, err
	}
	out := DevicePicker{Device: toDevice(d), Kids: []KidProfile{}, Parents: []ParentProfile{}}
	if !d.UserID.Valid {
		rows, err := s.db.ListProfileKids(ctx)
		if err != nil {
			return DevicePicker{}, err
		}
		for _, k := range rows {
			out.Kids = append(out.Kids, KidProfile{
				ID: k.ID, Slug: k.Slug.String, DisplayName: k.DisplayName, Color: k.Color.String, Avatar: k.Avatar.String,
				PINRequired: k.PinRequired == 1 && k.PinSet,
			})
		}
	}
	parents, err := s.db.ListElevatableParents(ctx)
	if err != nil {
		return DevicePicker{}, err
	}
	for _, pp := range parents {
		out.Parents = append(out.Parents, ParentProfile{ID: pp.ID, DisplayName: pp.DisplayName})
	}
	return out, nil
}

// DeviceSession is a short-lived token on a device.
type DeviceSession struct {
	Token     string    `json:"token" doc:"Bearer token for this session; the device token stays stored"`
	UserID    int64     `json:"userId"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// StartKidSession opens a kid's session on a shared device. A kid who must use a PIN (and has
// one) is checked; 5 wrong PINs for that kid on this device lock it for 15 minutes.
func (s *Service) StartKidSession(ctx context.Context, p *auth.Principal, kidRef, pin string) (DeviceSession, error) {
	d, err := s.device(ctx, p)
	if err != nil {
		return DeviceSession{}, err
	}
	if d.UserID.Valid || p.Kind != auth.KindSharedDevice {
		return DeviceSession{}, apierr.New(http.StatusForbidden, apierr.CodeForbidden, "kid sessions start from a shared device's own token")
	}
	kid, err := kids.Resolve(ctx, s.db.Queries, kidRef)
	if err != nil {
		return DeviceSession{}, err
	}
	if kid.ArchivedAt.Valid {
		return DeviceSession{}, apierr.New(http.StatusNotFound, apierr.CodeKidNotFound, "no such kid")
	}
	if kid.PinRequired == 1 && kid.PinHash.Valid {
		key := fmt.Sprintf("%d|%d", d.ID, kid.ID)
		if blocked, wait := s.kidPIN.Blocked(key); blocked {
			return DeviceSession{}, locked(wait)
		}
		ok, _, err := auth.VerifySecret(pin, kid.PinHash.String)
		if err != nil {
			return DeviceSession{}, err
		}
		if !ok {
			s.kidPIN.Fail(key)
			return DeviceSession{}, errInvalidPIN
		}
		s.kidPIN.Reset(key)
	}
	return s.issue(ctx, kid.ID, d.ID, KidSessionTTL, "kid session")
}

// Elevate gives a parent a two-minute session on this device after checking their parent PIN.
func (s *Service) Elevate(ctx context.Context, p *auth.Principal, parentID int64, pin string) (DeviceSession, error) {
	d, err := s.device(ctx, p)
	if err != nil {
		return DeviceSession{}, err
	}
	now := s.clock.Now()
	if d.ElevateLockedUntil.Valid {
		if until, err := db.ParseTimestamp(d.ElevateLockedUntil.String); err == nil && now.Before(until) {
			return DeviceSession{}, locked(until.Sub(now))
		}
	}
	parent, err := s.db.GetParent(ctx, parentID)
	if err != nil || !parent.ParentPinHash.Valid {
		return DeviceSession{}, s.elevateFailed(ctx, d)
	}
	ok, _, err := auth.VerifySecret(pin, parent.ParentPinHash.String)
	if err != nil {
		return DeviceSession{}, err
	}
	if !ok {
		return DeviceSession{}, s.elevateFailed(ctx, d)
	}
	if d.ElevateFailures != 0 {
		if err := s.db.SetDeviceElevateFailures(ctx, sqlc.SetDeviceElevateFailuresParams{ID: d.ID}); err != nil {
			return DeviceSession{}, err
		}
	}
	return s.issue(ctx, parent.ID, d.ID, ElevationTTL, "parent elevation")
}

// elevateFailed counts a wrong parent PIN and locks elevation on the 5th.
func (s *Service) elevateFailed(ctx context.Context, d sqlc.Device) error {
	failures := d.ElevateFailures + 1
	var until sql.NullString
	if failures >= elevateLimit {
		until = sql.NullString{String: db.Timestamp(s.clock.Now().Add(elevateLock)), Valid: true}
		failures = 0
	}
	if err := s.db.SetDeviceElevateFailures(ctx, sqlc.SetDeviceElevateFailuresParams{
		ElevateFailures: failures, ElevateLockedUntil: until, ID: d.ID,
	}); err != nil {
		return err
	}
	if until.Valid {
		return locked(elevateLock)
	}
	return errInvalidPIN
}

func locked(wait time.Duration) error {
	secs := max(int(wait.Round(time.Second)/time.Second), 1)
	return huma.ErrorWithHeaders(
		apierr.New(http.StatusTooManyRequests, apierr.CodePINLocked,
			fmt.Sprintf("too many wrong PINs on this device; try again in %d minutes", (secs+59)/60)),
		http.Header{"Retry-After": {strconv.Itoa(secs)}})
}

func (s *Service) issue(ctx context.Context, userID, deviceID int64, ttl time.Duration, label string) (DeviceSession, error) {
	token, _, err := s.tokens.Issue(ctx, auth.IssueParams{
		Kind: auth.TokenDeviceSession, UserID: userID, DeviceID: deviceID, TTL: ttl, Label: label,
	})
	if err != nil {
		return DeviceSession{}, err
	}
	return DeviceSession{Token: token, UserID: userID, ExpiresAt: s.clock.Now().Add(ttl)}, nil
}

// Lock ends the calling kid session or parent elevation (the device goes back to its picker).
// Calling it with the device token itself does nothing: that would unpair the device.
func (s *Service) Lock(ctx context.Context, p *auth.Principal) error {
	if p.DeviceID == 0 {
		return errNotDevice
	}
	row, err := s.db.GetTokenForUser(ctx, sqlc.GetTokenForUserParams{ID: p.TokenID, UserID: sql.NullInt64{Int64: p.UserID, Valid: p.UserID != 0}})
	if err == nil && row.Kind == string(auth.TokenDeviceSession) {
		return s.tokens.Revoke(ctx, p.TokenID)
	}
	return nil
}

// ListedDevice is a device as the devices page shows it.
type ListedDevice struct {
	Device
	KidName  string    `json:"kidName,omitempty"`
	LastSeen time.Time `json:"lastSeen" doc:"Last request from the device or one of its sessions (to the minute)"`
	// SessionUser is who has a kid session or parent elevation open on the device right now.
	SessionUser string `json:"sessionUser,omitempty"`
}

// List returns every paired device, newest first.
func (s *Service) List(ctx context.Context) ([]ListedDevice, error) {
	rows, err := s.db.ListDevices(ctx, sql.NullString{String: db.Timestamp(s.clock.Now()), Valid: true})
	if err != nil {
		return nil, err
	}
	out := make([]ListedDevice, 0, len(rows))
	for _, r := range rows {
		created, _ := db.ParseTimestamp(r.CreatedAt)
		seen, _ := db.ParseTimestamp(r.LastSeen)
		out = append(out, ListedDevice{
			Device:  Device{ID: r.ID, Name: r.Name, Shared: !r.UserID.Valid, KidID: r.UserID.Int64, AppVersion: r.AppVersion, CreatedAt: created},
			KidName: r.KidName, LastSeen: seen, SessionUser: r.SessionUser,
		})
	}
	return out, nil
}

var errNoDevice = apierr.New(http.StatusNotFound, apierr.CodeNotFound, "no such device")

// Revoke unpairs a device: its token and sessions stop working at once, and the app returns to
// pairing the next time it contacts the server.
func (s *Service) Revoke(ctx context.Context, id int64) error {
	n, err := s.db.DeleteDevice(ctx, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return errNoDevice
	}
	return nil
}

// LockRemote ends any kid session or parent elevation on a device, sending it back to its
// picker. The app notices on its next contact (it checks every 30 s while a session is open).
func (s *Service) LockRemote(ctx context.Context, id int64) (int64, error) {
	if _, err := s.db.GetDevice(ctx, id); errors.Is(err, sql.ErrNoRows) {
		return 0, errNoDevice
	} else if err != nil {
		return 0, err
	}
	return s.db.DeleteDeviceSessions(ctx, sql.NullInt64{Int64: id, Valid: true})
}

// ReportVersion records the app version a device runs (sent with each picker request).
func (s *Service) ReportVersion(ctx context.Context, p *auth.Principal, version string) error {
	if p.DeviceID == 0 || version == "" {
		return nil
	}
	return s.db.SetDeviceAppVersion(ctx, sqlc.SetDeviceAppVersionParams{AppVersion: truncate(version, 30), ID: p.DeviceID})
}

// PurgeExpiredCodes deletes expired pairing codes (daily maintenance).
func (s *Service) PurgeExpiredCodes(ctx context.Context) (int64, error) {
	return s.db.DeleteExpiredPairingCodes(ctx, db.Timestamp(s.clock.Now()))
}

func (s *Service) inTx(ctx context.Context, fn func(q *sqlc.Queries) error) error {
	tx, err := s.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(s.db.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit()
}
