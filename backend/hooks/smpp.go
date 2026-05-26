package hooks

import (
	"fmt"
	"vendel/services"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
)

// RegisterSMPPHooks validates smpp_configs ownership and encrypts the password
// at rest on create/update.
func RegisterSMPPHooks(app *pocketbase.PocketBase) {
	app.OnRecordCreate("smpp_configs").BindFunc(validateAndEncryptSMPP)
	app.OnRecordUpdate("smpp_configs").BindFunc(validateAndEncryptSMPP)
}

func validateAndEncryptSMPP(e *core.RecordEvent) error {
	deviceId := e.Record.GetString("device")
	if deviceId == "" {
		return fmt.Errorf("device is required")
	}

	device, err := e.App.FindFirstRecordByFilter(
		"sms_devices",
		"id = {:id}",
		dbx.Params{"id": deviceId},
	)
	if err != nil {
		return fmt.Errorf("referenced device not found")
	}
	if device.GetString("device_type") != "smpp" {
		return fmt.Errorf("smpp_configs can only be attached to devices of type 'smpp'")
	}

	if e.Record.GetInt("port") == 0 {
		e.Record.Set("port", 2775)
	}
	if e.Record.GetString("bind_mode") == "" {
		e.Record.Set("bind_mode", "trx")
	}
	if e.Record.GetInt("enquire_link_seconds") == 0 {
		e.Record.Set("enquire_link_seconds", 30)
	}

	if password := e.Record.GetString("password"); password != "" {
		encrypted, err := services.EncryptSecret(password)
		if err != nil {
			return err
		}
		e.Record.Set("password", encrypted)
	}

	return e.Next()
}
