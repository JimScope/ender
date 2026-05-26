package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		devices, err := app.FindCollectionByNameOrId("sms_devices")
		if err != nil {
			return err
		}

		field := devices.Fields.GetByName("device_type")
		sel, ok := field.(*core.SelectField)
		if !ok {
			return nil
		}
		sel.Values = []string{"android", "modem", "smpp"}

		return app.Save(devices)
	}, func(app core.App) error {
		devices, err := app.FindCollectionByNameOrId("sms_devices")
		if err != nil {
			return err
		}

		field := devices.Fields.GetByName("device_type")
		sel, ok := field.(*core.SelectField)
		if !ok {
			return nil
		}
		sel.Values = []string{"android", "modem"}

		return app.Save(devices)
	})
}
