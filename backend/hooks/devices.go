package hooks

import (
	"vendel/services"
	"vendel/services/smsprovider"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

// RegisterDeviceHooks wires lifecycle hooks for sms_devices:
//   - blocks public creation of aws_aeum devices (server-side bootstrap only)
//   - skips quota and api_key generation for aws_aeum
//   - enforces user is set for physical devices
//   - blocks updates/deletes on aws_aeum via API as defense-in-depth
func RegisterDeviceHooks(app *pocketbase.PocketBase) {
	app.OnRecordCreateRequest("sms_devices").BindFunc(forbidAEUMRequest("AWS End User Messaging device is system-managed"))
	app.OnRecordUpdateRequest("sms_devices").BindFunc(forbidAEUMRequest("AWS End User Messaging device cannot be modified"))
	app.OnRecordDeleteRequest("sms_devices").BindFunc(forbidAEUMRequest("AWS End User Messaging device cannot be deleted"))

	app.OnRecordCreate("sms_devices").BindFunc(func(e *core.RecordEvent) error {
		if e.Record.GetString("device_type") == smsprovider.DeviceTypeAEUM {
			if e.Record.GetString("name") == "" {
				e.Record.Set("name", "AWS End User Messaging")
			}
			return e.Next()
		}

		userId := e.Record.GetString("user")
		if userId == "" {
			return apis.NewBadRequestError("user is required for physical devices", nil)
		}
		if err := services.CheckDeviceQuota(e.App, userId); err != nil {
			return err
		}
		if err := services.IncrementDeviceCount(e.App, userId); err != nil {
			return err
		}
		e.Record.Set("api_key", services.GenerateSecureKey(services.DeviceKeyPrefix, services.GeneratedKeyLen))
		if e.Record.GetString("device_type") == "" {
			e.Record.Set("device_type", "android")
		}
		e.Record.Unhide("api_key")
		if err := e.Next(); err != nil {
			_ = services.DecrementDeviceCount(e.App, userId)
			return err
		}
		return nil
	})

	app.OnRecordDelete("sms_devices").BindFunc(func(e *core.RecordEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		if e.Record.GetString("device_type") == smsprovider.DeviceTypeAEUM {
			return nil
		}
		return services.DecrementDeviceCount(e.App, e.Record.GetString("user"))
	})
}

func forbidAEUMRequest(message string) func(*core.RecordRequestEvent) error {
	return func(e *core.RecordRequestEvent) error {
		if e.Record.GetString("device_type") == smsprovider.DeviceTypeAEUM {
			return apis.NewForbiddenError(message, nil)
		}
		return e.Next()
	}
}
