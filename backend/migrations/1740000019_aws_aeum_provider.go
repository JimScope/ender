package migrations

import (
	"slices"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		// ── sms_devices: relax user.Required, extend device_type, adjust rules ──
		devices, err := app.FindCollectionByNameOrId("sms_devices")
		if err != nil {
			return err
		}

		if userField, ok := devices.Fields.GetByName("user").(*core.RelationField); ok {
			userField.Required = false
		}

		if dtField, ok := devices.Fields.GetByName("device_type").(*core.SelectField); ok {
			if !slices.Contains(dtField.Values, "aws_aeum") {
				dtField.Values = append(dtField.Values, "aws_aeum")
			}
		}

		// All rules require auth. The disjunction in List/View is wrapped in an
		// auth check because anonymous requests would otherwise match the AEUM
		// row (its user field is empty, so `user = @request.auth.id` evaluates
		// to "" = "" = true; the `device_type = 'aws_aeum'` arm is unguarded).
		devices.ListRule = ptrStr("@request.auth.id != '' && (user = @request.auth.id || device_type = 'aws_aeum')")
		devices.ViewRule = ptrStr("@request.auth.id != '' && (user = @request.auth.id || device_type = 'aws_aeum')")
		devices.CreateRule = ptrStr("@request.auth.id != '' && user = @request.auth.id")
		devices.UpdateRule = ptrStr("@request.auth.id != '' && user = @request.auth.id && device_type != 'aws_aeum'")
		devices.DeleteRule = ptrStr("@request.auth.id != '' && user = @request.auth.id && device_type != 'aws_aeum'")

		if err := app.Save(devices); err != nil {
			return err
		}

		// ── sms_messages: provider metadata fields + index ──
		messages, err := app.FindCollectionByNameOrId("sms_messages")
		if err != nil {
			return err
		}

		messages.Fields.Add(
			&core.TextField{Name: "provider_message_id", Max: 255},
			&core.TextField{Name: "provider_channel", Max: 32},
			&core.TextField{Name: "provider_origination_identity", Max: 512},
		)
		messages.AddIndex("idx_sms_messages_provider_message_id", false, "provider_message_id", "")

		return app.Save(messages)
	}, func(app core.App) error {
		// ── Rollback sms_messages ──
		if messages, err := app.FindCollectionByNameOrId("sms_messages"); err == nil {
			messages.RemoveIndex("idx_sms_messages_provider_message_id")
			messages.Fields.RemoveByName("provider_message_id")
			messages.Fields.RemoveByName("provider_channel")
			messages.Fields.RemoveByName("provider_origination_identity")
			_ = app.Save(messages)
		}

		// ── Rollback sms_devices ──
		devices, err := app.FindCollectionByNameOrId("sms_devices")
		if err != nil {
			return err
		}

		if userField, ok := devices.Fields.GetByName("user").(*core.RelationField); ok {
			userField.Required = true
		}

		if dtField, ok := devices.Fields.GetByName("device_type").(*core.SelectField); ok {
			dtField.Values = slices.DeleteFunc(dtField.Values, func(v string) bool { return v == "aws_aeum" })
		}

		devices.ListRule = ptrStr("user = @request.auth.id")
		devices.ViewRule = ptrStr("user = @request.auth.id")
		devices.CreateRule = ptrStr("user = @request.auth.id")
		devices.UpdateRule = ptrStr("user = @request.auth.id")
		devices.DeleteRule = ptrStr("user = @request.auth.id")

		return app.Save(devices)
	})
}
