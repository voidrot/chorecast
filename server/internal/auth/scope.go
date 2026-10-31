package auth

// Scope names a capability an API operation requires. Operations declare scopes; principals
// carry them. Data-level limits (a kid only sees their own chores) are applied separately via
// Principal.CanAccessKid.
type Scope string

const (
	ScopeHouseholdRead   Scope = "household:read"   // household info and kid profiles (names, avatars)
	ScopeHouseholdManage Scope = "household:manage" // household settings, kids, parents
	ScopeServerManage    Scope = "server:manage"    // owner only: SSO, storage, AI, backups, audit log
	ScopeDevicesManage   Scope = "devices:manage"   // pair, list and revoke devices
	ScopeProfilesSelect  Scope = "profiles:select"  // shared device: pick a kid profile to start a session
	ScopeChoresRead      Scope = "chores:read"
	ScopeChoresManage    Scope = "chores:manage"
	ScopeChoresComplete  Scope = "chores:complete"
	ScopeChoresReview    Scope = "chores:review"
	ScopePointsRead      Scope = "points:read"
	ScopePointsManage    Scope = "points:manage"
	ScopeAccountManage   Scope = "account:manage" // a parent's own password, 2FA, PIN and sessions
	ScopeParentsManage   Scope = "parents:manage" // owner only: invite, promote/demote, remove, reset other parents
)

// Kind is what kind of caller a principal is.
type Kind string

const (
	KindParent       Kind = "parent"
	KindKid          Kind = "kid"
	KindSharedDevice Kind = "shared_device" // a paired family tablet with no kid session active
	KindDisplay      Kind = "display"       // read-only family dashboard
	KindAPI          Kind = "api"           // integration API key (scopes are granted per key; see integrations.md)
)

var (
	parentScopes = []Scope{
		ScopeHouseholdRead, ScopeHouseholdManage, ScopeDevicesManage, ScopeProfilesSelect,
		ScopeChoresRead, ScopeChoresManage, ScopeChoresComplete, ScopeChoresReview,
		ScopePointsRead, ScopePointsManage, ScopeAccountManage,
	}
	ownerScopes        = append(append([]Scope{}, parentScopes...), ScopeServerManage, ScopeParentsManage)
	kidScopes          = []Scope{ScopeHouseholdRead, ScopeChoresRead, ScopeChoresComplete, ScopePointsRead}
	sharedDeviceScopes = []Scope{ScopeHouseholdRead, ScopeProfilesSelect}
	displayScopes      = []Scope{ScopeHouseholdRead, ScopeChoresRead, ScopePointsRead}
)

// scopesFor returns the scopes a principal kind holds. API keys get none here: until per-key
// grants exist they are denied everything (fail closed).
func scopesFor(kind Kind, owner bool) []Scope {
	switch kind {
	case KindParent:
		if owner {
			return ownerScopes
		}
		return parentScopes
	case KindKid:
		return kidScopes
	case KindSharedDevice:
		return sharedDeviceScopes
	case KindDisplay:
		return displayScopes
	default:
		return nil
	}
}
