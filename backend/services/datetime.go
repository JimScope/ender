package services

import (
	"time"

	"github.com/pocketbase/pocketbase/tools/types"
)

// PocketBase stores DateField values as "2006-01-02 15:04:05.000Z" (space
// separator, types.DefaultDateLayout). SQLite compares these strings
// lexicographically, so filter parameters MUST use that exact layout: an
// RFC3339 value ("...T...") sorts after every same-day stored value
// (' ' < 'T') and silently breaks <=/>= comparisons.

// FilterNow returns the current UTC time formatted in PocketBase's stored
// datetime layout, for use as a record filter parameter.
func FilterNow() string {
	return types.NowDateTime().String()
}

// FilterTime formats an arbitrary time in PocketBase's stored datetime
// layout, for use as a record filter parameter.
func FilterTime(t time.Time) string {
	dt, err := types.ParseDateTime(t.UTC())
	if err != nil {
		return t.UTC().Format(types.DefaultDateLayout)
	}
	return dt.String()
}
