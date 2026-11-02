package household

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// Every settings field is a decision: audited with its values, or (a secret) left out on purpose.
// A new field fails here until it is added to auditedFields or to notAudited below.
func TestEverySettingIsAuditedOrExcluded(t *testing.T) {
	notAudited := []string{} // secrets, whose values must never reach the audit log
	if !slices.IsSorted(auditedFields) {
		t.Errorf("auditedFields isn't sorted: %v", auditedFields)
	}
	typ := reflect.TypeFor[HouseholdSettings]()
	var settings []string
	for i := range typ.NumField() {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		settings = append(settings, name)
		if !slices.Contains(auditedFields, name) && !slices.Contains(notAudited, name) {
			t.Errorf("setting %q: add it to auditedFields, or to notAudited if its value is a secret", name)
		}
	}
	for _, name := range auditedFields {
		if !slices.Contains(settings, name) {
			t.Errorf("auditedFields lists %q, which isn't a setting", name)
		}
	}
}
