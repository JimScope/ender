package services

import (
	"context"
	"log/slog"
	"sync"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	"vendel/services/smsprovider"
)

// providerDispatchWorkers caps how many provider.Send round-trips run in
// parallel inside one batch. AWS PinpointSMSVoiceV2 has per-account TPS
// limits; staying under ~10 concurrent calls keeps us well below them while
// removing the serial round-trip latency that dominates a 100-recipient send.
const providerDispatchWorkers = 8

// partitionByProvider splits messages by sms_devices.device_type. Physical
// devices (Android/modem) go into `physical`; any device_type with an entry
// in the provider registry lands in `externalByType` keyed by that
// device_type so callers can fan out to the matching Provider.
//
// Device lookups are cached per call so a batch of N recipients sharing one
// device costs one query, not N.
func partitionByProvider(app core.App, messages []*core.Record) (physical []*core.Record, externalByType map[string][]*core.Record) {
	externalByType = make(map[string][]*core.Record)
	deviceTypeCache := make(map[string]string)

	for _, m := range messages {
		deviceId := m.GetString("device")
		if deviceId == "" {
			physical = append(physical, m)
			continue
		}
		deviceType, cached := deviceTypeCache[deviceId]
		if !cached {
			dev, err := app.FindRecordById("sms_devices", deviceId)
			if err != nil {
				physical = append(physical, m)
				continue
			}
			deviceType = dev.GetString("device_type")
			deviceTypeCache[deviceId] = deviceType
		}
		if smsprovider.Get(deviceType) != nil {
			externalByType[deviceType] = append(externalByType[deviceType], m)
		} else {
			physical = append(physical, m)
		}
	}
	return physical, externalByType
}

// DispatchProviderMessages hands messages off to an external provider. The
// UPDATE to provider_message_id is intentionally done BEFORE any outbound
// webhook so the SNS delivery callback can match by id without a race.
//
// Sends fan out across a bounded worker pool; PocketBase serialises SQLite
// writes so concurrency mostly buys network parallelism on provider.Send.
func DispatchProviderMessages(app core.App, provider smsprovider.Provider, messages []*core.Record) {
	if provider == nil || len(messages) == 0 {
		return
	}
	workers := providerDispatchWorkers
	if len(messages) < workers {
		workers = len(messages)
	}

	jobs := make(chan *core.Record)
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for m := range jobs {
				dispatchOne(app, provider, m)
			}
		}()
	}
	for _, m := range messages {
		jobs <- m
	}
	close(jobs)
	wg.Wait()
}

func dispatchOne(app core.App, provider smsprovider.Provider, m *core.Record) {
	// External providers receive plaintext; the row body is at-rest encrypted
	// by the encryption hooks, so we must decrypt here. GetRecordBody falls
	// back to the raw value if the row predates the encryption migration.
	req := smsprovider.SendRequest{
		To:          m.GetString("to"),
		Body:        GetRecordBody(m),
		ChannelHint: smsprovider.ChannelAuto,
	}
	result, err := provider.Send(context.Background(), req)
	if err != nil {
		app.Logger().Error("provider send failed", slog.String("provider", provider.Name()), slog.String("msgId", m.Id), slog.Any("err", err))
		return
	}

	m.Set("provider_message_id", result.ProviderMessageID)
	m.Set("provider_channel", result.ProviderChannel)
	m.Set("provider_origination_identity", result.OriginationIdentity)
	m.Set("status", result.Status)
	if result.Status == smsprovider.StatusSent {
		m.Set("sent_at", types.NowDateTime())
	} else {
		m.Set("error_message", result.ErrorMessage)
	}
	if err := app.Save(m); err != nil {
		app.Logger().Error("persist provider result failed", slog.String("provider", provider.Name()), slog.String("msgId", m.Id), slog.Any("err", err))
		return
	}

	switch result.Status {
	case smsprovider.StatusSent:
		TriggerWebhooks(app, m.GetString("user"), m, "sms_sent")
	case smsprovider.StatusFailed:
		TriggerWebhooks(app, m.GetString("user"), m, "sms_failed")
	}
}
