package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/xlab/at"
)

var version = "dev"

const (
	// sessionBackoffMax caps the modem reopen backoff.
	sessionBackoffMax = 60 * time.Second
	// maxUnhealthyFor: exit when no modem has been functional this long,
	// so a supervisor (docker restart policy, systemd) can restart us
	// instead of letting a zombie process fake healthiness.
	maxUnhealthyFor = 5 * time.Minute
	// shutdownGrace bounds how long we wait for in-flight AT exchanges
	// and reports on SIGTERM before exiting anyway.
	shutdownGrace = 15 * time.Second
)

func main() {
	loadDotEnv(".env")
	cfg := loadConfig()

	log.Printf("vendel-modem-agent %s starting with %d modem(s)", version, len(cfg.Modems))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	health := &healthTracker{}
	go health.monitor(ctx)

	var wg sync.WaitGroup
	for _, modemCfg := range cfg.Modems {
		wg.Add(1)
		go func(m ModemConfig) {
			defer wg.Done()
			runModem(ctx, m, cfg.VendelURL, health)
		}(modemCfg)
	}

	<-ctx.Done()
	log.Printf("received shutdown signal, closing modems")

	// Graceful shutdown: wait for sessions to unwind (deferred dev.Close,
	// in-flight sends/reports), but never hang past the grace period.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		log.Printf("shutdown complete")
	case <-time.After(shutdownGrace):
		log.Printf("shutdown grace period expired, exiting")
	}
}

// healthTracker counts modems with a fully initialized session. The monitor
// exits the process when the count stays at zero for too long.
type healthTracker struct {
	healthy atomic.Int32
}

func (h *healthTracker) markUp()   { h.healthy.Add(1) }
func (h *healthTracker) markDown() { h.healthy.Add(-1) }

func (h *healthTracker) monitor(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	zeroSince := time.Now()
	logged := false

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if h.healthy.Load() > 0 {
				zeroSince = time.Now()
				logged = false
				continue
			}
			if !logged {
				log.Printf("WARNING: no functional modems")
				logged = true
			}
			if time.Since(zeroSince) > maxUnhealthyFor {
				log.Fatalf("no functional modems for %s — exiting so the supervisor can restart the agent", maxUnhealthyFor)
			}
		}
	}
}

// runModem supervises one modem: it (re)opens a session whenever the
// previous one ends — USB unplug, init failure, unresponsive port — with
// exponential backoff between attempts.
func runModem(ctx context.Context, cfg ModemConfig, vendelURL string, health *healthTracker) {
	logPrefix := fmt.Sprintf("[%s]", cfg.CommandPort)
	backoff := time.Second

	for {
		start := time.Now()
		err := runModemSession(ctx, cfg, vendelURL, health, logPrefix)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Printf("%s session ended: %v — reopening in %s", logPrefix, err, backoff)
		} else {
			log.Printf("%s session ended — reopening in %s", logPrefix, backoff)
		}

		// A session that ran for a while was healthy; start fresh.
		if time.Since(start) > 60*time.Second {
			backoff = time.Second
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > sessionBackoffMax {
			backoff = sessionBackoffMax
		}
	}
}

// runModemSession owns one open-init-serve cycle of a modem. It returns
// when the modem dies, the backend cannot be reached during setup, or the
// process shuts down.
func runModemSession(ctx context.Context, cfg ModemConfig, vendelURL string, health *healthTracker, logPrefix string) error {
	// Put the ttys in raw mode before the library opens them — it uses a
	// bare os.OpenFile and a port left in canonical/echo mode breaks AT
	// parsing. Best-effort: USB ACM devices often work without it.
	if err := configureSerialPort(cfg.CommandPort); err != nil {
		log.Printf("%s serial setup (non-fatal): %v", logPrefix, err)
	}
	if cfg.NotifyPort != cfg.CommandPort {
		if err := configureSerialPort(cfg.NotifyPort); err != nil {
			log.Printf("%s serial setup (non-fatal): %v", logPrefix, err)
		}
	}

	singlePort := cfg.CommandPort == cfg.NotifyPort

	// Auto-detect profile if not specified
	profileName := cfg.Profile
	if profileName == "" {
		probeDev := &at.Device{
			CommandPort: cfg.CommandPort,
			NotifyPort:  cfg.NotifyPort,
		}
		if err := probeDev.Open(); err != nil {
			return fmt.Errorf("open modem for detection: %w", err)
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
		return fmt.Errorf("open modem: %w", err)
	}
	defer dev.Close()

	// The shared mutex serializes every AT exchange on the command port:
	// our sends (CMGS) and Watch's reads (CMGR/CMGD) via the profile.
	portMu := &sync.Mutex{}
	if err := dev.Init(resolveProfile(profileName, cfg.SimPIN, singlePort, portMu)); err != nil {
		return fmt.Errorf("init modem: %w", err)
	}
	log.Printf("%s modem initialized (profile: %s)", logPrefix, profileName)

	health.markUp()
	defer health.markDown()

	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// End the session as soon as the device closes itself — Watch does
	// that when the USB port disappears.
	go func() {
		select {
		case <-dev.Closed():
			cancel()
		case <-sessionCtx.Done():
		}
	}()

	client := NewVendelClient(vendelURL, cfg.APIKey)

	// The work queue decouples SSE reading from modem I/O: a slow send
	// (up to 60s of AT timeout) must not block the event stream.
	work := make(chan PendingMessage, 64)
	enqueue := func(msgs []PendingMessage) {
		for _, m := range msgs {
			select {
			case work <- m:
			default:
				// The backend retry cron rescues dropped messages.
				log.Printf("%s work queue full, dropping %s", logPrefix, m.MessageID)
			}
		}
	}

	// First fetch resolves the device record ID and claims any backlog
	// assigned while we were offline. The backend marks returned messages
	// as "sending", so they MUST be enqueued, not discarded.
	pending, err := client.FetchPending(sessionCtx)
	if err != nil {
		return fmt.Errorf("resolve device: %w", err)
	}
	if client.deviceID == "" {
		return fmt.Errorf("backend did not return a device ID")
	}
	log.Printf("%s resolved device ID: %s", logPrefix, client.deviceID)
	if len(pending) > 0 {
		log.Printf("%s recovering %d pending message(s)", logPrefix, len(pending))
		enqueue(pending)
	}

	var wg sync.WaitGroup

	// Incoming SMS monitoring only on dual-port modems. On single-port
	// modems the profile disables +CMTI notifications entirely (nobody
	// would read them and they corrupt command replies).
	if !singlePort {
		go dev.Watch()
		wg.Add(1)
		go func() {
			defer wg.Done()
			incomingLoop(sessionCtx, dev, client, logPrefix)
		}()
	} else {
		log.Printf("%s single-port mode: incoming SMS monitoring disabled", logPrefix)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		sendWorker(sessionCtx, cancel, dev, client, work, logPrefix)
	}()

	// Blocks until the session context is canceled (modem death, port
	// errors detected by the worker, or process shutdown). Each (re)connect
	// re-claims pending messages so assignments made while the SSE link was
	// down are recovered.
	log.Printf("%s connecting to SSE for real-time dispatch", logPrefix)
	client.RunSSE(sessionCtx,
		func() {
			msgs, err := client.FetchPending(sessionCtx)
			if err != nil {
				log.Printf("%s pending re-fetch failed: %v", logPrefix, err)
				return
			}
			if len(msgs) > 0 {
				log.Printf("%s recovered %d pending message(s) after reconnect", logPrefix, len(msgs))
				enqueue(msgs)
			}
		},
		func(msg PendingMessage) {
			log.Printf("%s received message %s -> %s", logPrefix, msg.MessageID, msg.Recipient)
			enqueue([]PendingMessage{msg})
		},
	)

	cancel()
	wg.Wait()
	return nil
}

// sendWorker serializes outgoing sends for one modem. It deduplicates
// deliveries that arrive both via the pending re-fetch and the SSE event
// (a small race window on reconnect), and tears the session down when the
// error smells like a dead port rather than a per-message failure.
func sendWorker(ctx context.Context, cancelSession context.CancelFunc, dev *at.Device, client *VendelClient, work <-chan PendingMessage, logPrefix string) {
	const dedupWindow = 5 * time.Minute
	recent := make(map[string]time.Time)

	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-work:
			if t, ok := recent[msg.MessageID]; ok && time.Since(t) < dedupWindow {
				log.Printf("%s skipping duplicate delivery of %s", logPrefix, msg.MessageID)
				continue
			}
			if len(recent) > 1000 {
				for id, t := range recent {
					if time.Since(t) >= dedupWindow {
						delete(recent, id)
					}
				}
			}
			recent[msg.MessageID] = time.Now()

			if err := sendAndReport(ctx, dev, client, msg, logPrefix); isPortError(err) {
				log.Printf("%s port error, restarting modem session: %v", logPrefix, err)
				cancelSession()
				return
			}
		}
	}
}

func sendAndReport(ctx context.Context, dev *at.Device, client *VendelClient, msg PendingMessage, logPrefix string) error {
	err := sendSMS(dev, msg.Body, msg.Recipient)
	if err != nil {
		log.Printf("%s send failed for %s: %v", logPrefix, msg.MessageID, err)
		if reportErr := reportWithRetry(ctx, func() error {
			return client.ReportStatus(ctx, msg.MessageID, "failed", err.Error())
		}); reportErr != nil {
			log.Printf("%s giving up reporting failed status for %s: %v", logPrefix, msg.MessageID, reportErr)
		}
		return err
	}

	log.Printf("%s sent %s to %s", logPrefix, msg.MessageID, msg.Recipient)
	if reportErr := reportWithRetry(ctx, func() error {
		return client.ReportStatus(ctx, msg.MessageID, "sent", "")
	}); reportErr != nil {
		// Loud on purpose: an unreported sent message will be re-delivered
		// by the backend retry cron → duplicate SMS for the recipient.
		log.Printf("%s GIVING UP reporting sent status for %s — backend may re-deliver it: %v", logPrefix, msg.MessageID, reportErr)
	}
	return nil
}

// isPortError distinguishes a dead/unresponsive serial port (session-fatal)
// from per-message modem errors like CMS ERROR (message-fatal only).
func isPortError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, at.ErrClosed) ||
		errors.Is(err, os.ErrClosed) ||
		os.IsTimeout(err) ||
		errors.Is(err, syscall.EIO) ||
		errors.Is(err, syscall.ENODEV) ||
		errors.Is(err, syscall.ENXIO)
}

// incomingLoop consumes inbound SMS from the modem, reassembling
// concatenated parts by their UDH, and reports them to the backend with
// the SMSC timestamp. It exits with the session (the library never closes
// the IncomingSms channel, so selecting on ctx prevents a goroutine leak).
func incomingLoop(ctx context.Context, dev *at.Device, client *VendelClient, logPrefix string) {
	reasm := newReassembler()
	flushTicker := time.NewTicker(time.Minute)
	defer flushTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-flushTicker.C:
			// A lost part must not black-hole the rest of the message:
			// report stale partials with whatever arrived, in order.
			for _, m := range reasm.flushStale(5 * time.Minute) {
				log.Printf("%s reporting incomplete multipart SMS from %s", logPrefix, m.from)
				reportIncoming(ctx, client, m, logPrefix)
			}
		case msg, ok := <-dev.IncomingSms():
			if !ok || msg == nil {
				return
			}
			ts := time.Time(msg.ServiceCenterTime)
			if ts.IsZero() {
				ts = time.Now()
			}
			from := string(msg.Address)

			if msg.UserDataStartsWithHeader && msg.UserDataHeader.TotalNumber > 1 {
				complete, done := reasm.add(from, msg.Text, msg.UserDataHeader, ts)
				if !done {
					continue
				}
				log.Printf("%s incoming multipart SMS from %s reassembled", logPrefix, from)
				reportIncoming(ctx, client, complete, logPrefix)
				continue
			}

			log.Printf("%s incoming SMS from %s", logPrefix, from)
			reportIncoming(ctx, client, incomingMessage{from: from, text: msg.Text, ts: ts}, logPrefix)
		}
	}
}

func reportIncoming(ctx context.Context, client *VendelClient, m incomingMessage, logPrefix string) {
	if err := reportWithRetry(ctx, func() error {
		return client.ReportIncoming(ctx, m.from, m.text, m.ts.UTC().Format(time.RFC3339))
	}); err != nil {
		// The modem already deleted the message from storage (CMGD), so a
		// lost report means a lost message — log loudly.
		log.Printf("%s GIVING UP reporting incoming SMS from %s: %v", logPrefix, m.from, err)
	}
}
