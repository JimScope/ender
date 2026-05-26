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

		cfg := core.NewBaseCollection("smpp_configs")
		cfg.Fields.Add(
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
			&core.RelationField{
				Name:          "device",
				CollectionId:  devices.Id,
				Required:      true,
				MaxSelect:     1,
				CascadeDelete: true,
			},
			&core.TextField{Name: "host", Required: true, Max: 255},
			&core.NumberField{Name: "port", Required: true},
			&core.TextField{Name: "system_id", Required: true, Max: 255},
			&core.TextField{Name: "password", Max: 500, Hidden: true},
			&core.TextField{Name: "system_type", Max: 32},
			&core.SelectField{
				Name:      "bind_mode",
				Values:    []string{"tx", "rx", "trx"},
				MaxSelect: 1,
			},
			&core.NumberField{Name: "source_ton"},
			&core.NumberField{Name: "source_npi"},
			&core.NumberField{Name: "dest_ton"},
			&core.NumberField{Name: "dest_npi"},
			&core.BoolField{Name: "use_tls"},
			&core.NumberField{Name: "enquire_link_seconds"},
			&core.NumberField{Name: "default_data_coding"},
			&core.NumberField{Name: "submit_throttle_tps"},
		)

		// Only the device's owner can list/view/manage config.
		// Writes happen via the owning user — hook cross-checks the relation.
		ownerRule := "device.user = @request.auth.id"
		cfg.ListRule = ptrStr(ownerRule)
		cfg.ViewRule = ptrStr(ownerRule)
		cfg.CreateRule = ptrStr(ownerRule)
		cfg.UpdateRule = ptrStr(ownerRule)
		cfg.DeleteRule = ptrStr(ownerRule)
		cfg.AddIndex("idx_smpp_configs_device", true, "device", "")

		return app.Save(cfg)
	}, func(app core.App) error {
		col, err := app.FindCollectionByNameOrId("smpp_configs")
		if err != nil {
			return nil
		}
		return app.Delete(col)
	})
}
