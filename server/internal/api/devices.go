package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/voidrot/chorecast/server/internal/auth"
	"github.com/voidrot/chorecast/server/internal/devices"
)

func registerDevices(hapi huma.API, h *handlers) {
	huma.Register(hapi, huma.Operation{
		OperationID: "createPairing",
		Method:      http.MethodPost,
		Path:        "/devices/pairing",
		Summary:     "Start pairing a device",
		Description: "Creates a single-use code valid for 5 minutes, for a kid's own device (`kid`) or a shared " +
			"family device (no `kid`). Show `qr` as a QR code for the app to scan.",
		Tags:     []string{"devices"},
		Security: auth.Require(auth.ScopeDevicesManage),
		Errors:   []int{http.StatusNotFound, http.StatusConflict},
	}, h.createPairing)
	huma.Register(hapi, huma.Operation{
		OperationID: "pairDevice",
		Method:      http.MethodPost,
		Path:        "/devices/pair",
		Summary:     "Pair this device with a pairing code",
		Description: "Returns the long-lived device token. A kid's device acts as that kid; a shared device " +
			"shows a profile picker (GET /devices/picker).",
		Tags:     []string{"devices"},
		Metadata: auth.Public(),
		Errors:   []int{http.StatusBadRequest, http.StatusTooManyRequests},
	}, h.pairDevice)
	huma.Register(hapi, huma.Operation{
		OperationID: "getDevicePicker",
		Method:      http.MethodGet,
		Path:        "/devices/picker",
		Summary:     "This device and its profile picker",
		Description: "Kid profiles (shared devices only) and the parents who can elevate with a parent PIN.",
		Tags:        []string{"devices"},
		Security:    auth.Require(),
		Errors:      []int{http.StatusForbidden},
	}, h.devicePicker)
	huma.Register(hapi, huma.Operation{
		OperationID: "startKidSession",
		Method:      http.MethodPost,
		Path:        "/devices/session",
		Summary:     "Start a kid's session on a shared device",
		Description: "Checks the kid's PIN when they must use one. The session lasts 8 hours at most; the app " +
			"locks back to the picker after inactivity (POST /devices/lock).",
		Tags:     []string{"devices"},
		Security: auth.Require(auth.ScopeProfilesSelect),
		Errors:   []int{http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests},
	}, h.startKidSession)
	huma.Register(hapi, huma.Operation{
		OperationID: "elevateParent",
		Method:      http.MethodPost,
		Path:        "/devices/elevate",
		Summary:     "Unlock parent actions on this device with a parent PIN",
		Description: "A two-minute parent session for daily operations (no account or server settings). " +
			"Five wrong PINs lock elevation on this device for 15 minutes.",
		Tags:     []string{"devices"},
		Security: auth.Require(),
		Errors:   []int{http.StatusForbidden, http.StatusTooManyRequests},
	}, h.elevateParent)
	huma.Register(hapi, huma.Operation{
		OperationID:   "lockDevice",
		Method:        http.MethodPost,
		Path:          "/devices/lock",
		Summary:       "End the current kid session or parent elevation on this device",
		Tags:          []string{"devices"},
		Security:      auth.Require(),
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusForbidden},
	}, h.lockDevice)
}

type CreatePairingInput struct {
	Body struct {
		Kid string `json:"kid,omitempty" doc:"Kid id or slug; omit for a shared family device"`
	}
}

type PairingOutput struct {
	Body devices.DevicePairing
}

func (h *handlers) createPairing(ctx context.Context, in *CreatePairingInput) (*PairingOutput, error) {
	p, _ := auth.PrincipalFrom(ctx)
	pairing, err := h.devices.CreatePairing(ctx, p, in.Body.Kid, auth.RequestInfoFrom(ctx).BaseURL(h.cfg.PublicURL))
	if err != nil {
		return nil, err
	}
	return &PairingOutput{Body: pairing}, nil
}

type PairDeviceInput struct {
	Body struct {
		Code       string `json:"code" minLength:"1" maxLength:"20"`
		Name       string `json:"name,omitempty" maxLength:"60" doc:"Device name, e.g. Pixel Tablet"`
		AppVersion string `json:"appVersion,omitempty" maxLength:"30"`
	}
}

type PairDeviceOutput struct {
	Body struct {
		Device devices.Device `json:"device"`
		Token  string         `json:"token" doc:"Long-lived device token; store it securely"`
	}
}

func (h *handlers) pairDevice(ctx context.Context, in *PairDeviceInput) (*PairDeviceOutput, error) {
	dev, token, err := h.devices.Pair(ctx, in.Body.Code, in.Body.Name, in.Body.AppVersion)
	if err != nil {
		return nil, err
	}
	out := &PairDeviceOutput{}
	out.Body.Device, out.Body.Token = dev, token
	return out, nil
}

type PickerOutput struct {
	Body devices.DevicePicker
}

func (h *handlers) devicePicker(ctx context.Context, _ *struct{}) (*PickerOutput, error) {
	p, _ := auth.PrincipalFrom(ctx)
	picker, err := h.devices.Picker(ctx, p)
	if err != nil {
		return nil, err
	}
	return &PickerOutput{Body: picker}, nil
}

type KidSessionInput struct {
	Body struct {
		Kid string `json:"kid" minLength:"1" doc:"Kid id or slug"`
		PIN string `json:"pin,omitempty" maxLength:"8"`
	}
}

type DeviceSessionOutput struct {
	Body devices.DeviceSession
}

func (h *handlers) startKidSession(ctx context.Context, in *KidSessionInput) (*DeviceSessionOutput, error) {
	p, _ := auth.PrincipalFrom(ctx)
	s, err := h.devices.StartKidSession(ctx, p, in.Body.Kid, in.Body.PIN)
	if err != nil {
		return nil, err
	}
	return &DeviceSessionOutput{Body: s}, nil
}

type ElevateInput struct {
	Body struct {
		ParentID int64  `json:"parentId"`
		PIN      string `json:"pin" minLength:"1" maxLength:"8"`
	}
}

func (h *handlers) elevateParent(ctx context.Context, in *ElevateInput) (*DeviceSessionOutput, error) {
	p, _ := auth.PrincipalFrom(ctx)
	s, err := h.devices.Elevate(ctx, p, in.Body.ParentID, in.Body.PIN)
	if err != nil {
		return nil, err
	}
	return &DeviceSessionOutput{Body: s}, nil
}

func (h *handlers) lockDevice(ctx context.Context, _ *struct{}) (*struct{}, error) {
	p, _ := auth.PrincipalFrom(ctx)
	return nil, h.devices.Lock(ctx, p)
}
