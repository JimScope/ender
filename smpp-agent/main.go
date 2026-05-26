package main

import (
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	agentcore "github.com/JimScope/vendel/agent-core"
)

var version = "dev"

func main() {
	cfg := loadConfig()

	log.Printf("vendel-smpp-agent %s starting with %d bind(s)", version, len(cfg.Binds))

	var wg sync.WaitGroup
	for _, bindCfg := range cfg.Binds {
		wg.Add(1)
		go func(b BindConfig) {
			defer wg.Done()
			runBind(b, cfg.VendelURL)
		}(bindCfg)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	log.Printf("received signal %s, shutting down", sig)
}

func runBind(bindCfg BindConfig, vendelURL string) {
	client := agentcore.New(vendelURL, bindCfg.APIKey)

	remoteCfg, err := FetchSMPPConfig(client)
	if err != nil {
		log.Printf("failed to fetch SMPP config: %v", err)
		return
	}
	log.Printf("[%s] fetched SMPP config: %s@%s:%d (bind_mode=%s)",
		remoteCfg.DeviceID, remoteCfg.SystemID, remoteCfg.Host, remoteCfg.Port, remoteCfg.BindMode)

	bind, err := newSMPPBind(remoteCfg, client)
	if err != nil {
		log.Printf("[%s] failed to open SMPP bind: %v", remoteCfg.DeviceID, err)
		return
	}
	defer bind.Close()
	log.Printf("[%s] SMPP bind established", remoteCfg.DeviceID)

	pending, err := client.FetchPending()
	if err != nil {
		log.Printf("[%s] failed to fetch pending messages: %v", remoteCfg.DeviceID, err)
	} else if len(pending) > 0 {
		log.Printf("[%s] processing %d pending message(s)", remoteCfg.DeviceID, len(pending))
		for _, msg := range pending {
			bind.Send(msg)
		}
	}

	log.Printf("[%s] connecting to SSE for real-time dispatch", remoteCfg.DeviceID)
	client.ConnectSSE("smpp", func(msg agentcore.PendingMessage) {
		log.Printf("[%s] received message %s -> %s", remoteCfg.DeviceID, msg.MessageID, msg.Recipient)
		bind.Send(msg)
	})
}
