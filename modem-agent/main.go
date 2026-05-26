package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	agentcore "github.com/JimScope/vendel/agent-core"
	"github.com/xlab/at"
	"github.com/xlab/at/sms"
)

var version = "dev"

func main() {
	cfg := loadConfig()

	log.Printf("vendel-modem-agent %s starting with %d modem(s)", version, len(cfg.Modems))

	var wg sync.WaitGroup
	for _, modemCfg := range cfg.Modems {
		wg.Add(1)
		go func(m ModemConfig) {
			defer wg.Done()
			runModem(m, cfg.VendelURL)
		}(modemCfg)
	}

	// Wait for signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Printf("received signal %s, shutting down", sig)
}

func runModem(cfg ModemConfig, vendelURL string) {
	logPrefix := fmt.Sprintf("[%s]", cfg.CommandPort)
	log.Printf("%s starting modem on command=%s notify=%s", logPrefix, cfg.CommandPort, cfg.NotifyPort)

	// Auto-detect profile if not specified
	profileName := cfg.Profile
	if profileName == "" {
		probeDev := &at.Device{
			CommandPort: cfg.CommandPort,
			NotifyPort:  cfg.NotifyPort,
		}
		if err := probeDev.Open(); err != nil {
			log.Printf("%s failed to open modem for detection: %v", logPrefix, err)
			return
		}
		profileName = detectProfile(probeDev)
		probeDev.Close()
		log.Printf("%s auto-detected profile: %s", logPrefix, profileName)
	}

	// Open modem via xlab/at
	dev := &at.Device{
		CommandPort: cfg.CommandPort,
		NotifyPort:  cfg.NotifyPort,
	}
	if err := dev.Open(); err != nil {
		log.Printf("%s failed to open modem: %v", logPrefix, err)
		return
	}
	defer dev.Close()

	if err := dev.Init(resolveProfile(profileName, cfg.SimPIN)); err != nil {
		log.Printf("%s failed to init modem: %v", logPrefix, err)
		return
	}
	log.Printf("%s modem initialized (profile: %s)", logPrefix, profileName)

	client := agentcore.New(vendelURL, cfg.APIKey)

	// Recover any pending messages and resolve the device record ID
	pending, err := client.FetchPending()
	if err != nil {
		log.Printf("%s failed to fetch pending messages: %v", logPrefix, err)
		return
	}
	if client.DeviceID() == "" {
		log.Printf("%s failed to resolve device ID from backend", logPrefix)
		return
	}
	log.Printf("%s resolved device ID: %s", logPrefix, client.DeviceID())

	if len(pending) > 0 {
		log.Printf("%s processing %d pending message(s)", logPrefix, len(pending))
		for _, msg := range pending {
			sendAndReport(dev, client, msg, logPrefix)
		}
	}

	// Start incoming SMS monitoring only on dual-port modems.
	// On single-port modems Watch would conflict with Send on the same fd.
	if cfg.CommandPort != cfg.NotifyPort {
		go dev.Watch()
		go func() {
			for msg := range dev.IncomingSms() {
				log.Printf("%s incoming SMS from %s", logPrefix, msg.Address)
				if reportErr := client.ReportIncoming(
					string(msg.Address),
					msg.Text,
					time.Now().UTC().Format(time.RFC3339),
				); reportErr != nil {
					log.Printf("%s failed to report incoming SMS: %v", logPrefix, reportErr)
				}
			}
		}()
	} else {
		log.Printf("%s single-port mode: incoming SMS monitoring disabled", logPrefix)
	}

	// Subscribe to SSE for real-time message assignment.
	// ConnectSSE reconnects automatically on disconnect.
	log.Printf("%s connecting to SSE for real-time dispatch", logPrefix)
	client.ConnectSSE("modem", func(msg agentcore.PendingMessage) {
		log.Printf("%s received message %s -> %s", logPrefix, msg.MessageID, msg.Recipient)
		sendAndReport(dev, client, msg, logPrefix)
	})
}

func sendAndReport(dev *at.Device, client *agentcore.Client, msg agentcore.PendingMessage, logPrefix string) {
	if err := dev.SendSMS(msg.Body, sms.PhoneNumber(msg.Recipient)); err != nil {
		log.Printf("%s send failed for %s: %v", logPrefix, msg.MessageID, err)
		if reportErr := client.ReportStatus(msg.MessageID, "failed", err.Error()); reportErr != nil {
			log.Printf("%s failed to report status: %v", logPrefix, reportErr)
		}
	} else {
		log.Printf("%s sent %s to %s", logPrefix, msg.MessageID, msg.Recipient)
		if reportErr := client.ReportStatus(msg.MessageID, "sent", ""); reportErr != nil {
			log.Printf("%s failed to report status: %v", logPrefix, reportErr)
		}
	}
}
