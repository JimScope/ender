package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Allow app superusers to update other users (the admin panel edits names,
// quotas, and the is_superuser flag). Escalation of is_superuser by
// non-superusers is blocked by the request hooks in hooks/users.go.
func init() {
	m.Register(func(app core.App) error {
		users, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			return err
		}

		users.UpdateRule = ptrStr("id = @request.auth.id || @request.auth.is_superuser = true")

		return app.Save(users)
	}, func(app core.App) error {
		users, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			return err
		}

		users.UpdateRule = ptrStr("id = @request.auth.id")

		return app.Save(users)
	})
}
