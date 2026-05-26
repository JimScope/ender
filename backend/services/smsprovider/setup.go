package smsprovider

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// EnsureAEUMDevice creates the global aws_aeum sms_devices record when AEUM is
// enabled by env vars. Idempotent: skips if already present, or if disabled.
func EnsureAEUMDevice(app core.App) error {
	if !aeumEnabledFromEnv() {
		return nil
	}
	existing, _ := app.FindFirstRecordByFilter(
		"sms_devices",
		"device_type = {:t}",
		dbx.Params{"t": DeviceTypeAEUM},
	)
	if existing != nil {
		return nil
	}
	collection, err := app.FindCollectionByNameOrId("sms_devices")
	if err != nil {
		return fmt.Errorf("sms_devices collection not found: %w", err)
	}
	deviceName := os.Getenv("AEUM_DEVICE_NAME")
	if deviceName == "" {
		deviceName = "AWS End User Messaging"
	}
	record := core.NewRecord(collection)
	record.Set("device_type", DeviceTypeAEUM)
	record.Set("name", deviceName)
	record.Set("phone_number", deriveAEUMPhoneLabel())
	// user is intentionally empty; hooks/devices.go skips user validation when
	// device_type == DeviceTypeAEUM.
	if err := app.Save(record); err != nil {
		return fmt.Errorf("failed to bootstrap AEUM device: %w", err)
	}
	app.Logger().Info("AEUM device bootstrapped", "name", deviceName)
	return nil
}

func aeumEnabledFromEnv() bool {
	v, _ := strconv.ParseBool(strings.TrimSpace(os.Getenv("AEUM_ENABLED")))
	return v
}

// deriveAEUMPhoneLabel produces a cosmetic identifier for the AEUM device row.
// phone_number is NOT NULL but never used at dispatch time, so we just pick
// something human-readable from the configured ARN.
func deriveAEUMPhoneLabel() string {
	if id := os.Getenv("AEUM_ORIGINATION_IDENTITY_ARN"); id != "" {
		if i := strings.LastIndex(id, "/"); i >= 0 && i < len(id)-1 {
			return id[i+1:]
		}
		return id
	}
	if id := os.Getenv("AEUM_ORIGINATION_POOL_ARN"); id != "" {
		return id
	}
	return "aws-aeum"
}
