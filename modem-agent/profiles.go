package main

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/xlab/at"
)

// lockedProfile serializes every AT exchange on the command port with a
// mutex. The xlab/at library has no internal locking, and on dual-port
// modems Watch() issues CMGR/CMGD (via d.Commands) concurrently with our
// CMGS sends — interleaved AT exchanges corrupt each other's replies.
// Overriding the command methods here covers both callers without forking
// the library, because Device.handleReport dispatches through d.Commands.
type lockedProfile struct {
	at.DefaultProfile
	mu *sync.Mutex
}

func (p *lockedProfile) CMGR(index uint16) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.DefaultProfile.CMGR(index)
}

func (p *lockedProfile) CMGD(index uint16, option at.Opt) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.DefaultProfile.CMGD(index, option)
}

func (p *lockedProfile) CMGS(length int, octets []byte) (byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.DefaultProfile.CMGS(length, octets)
}

func (p *lockedProfile) BOOT(token uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.DefaultProfile.BOOT(token)
}

// GenericProfile works with any modem that supports standard 3GPP AT commands.
// It skips Huawei-specific init (AT^SYSINFO, AT+COPS format, AT+GMM, AT+GSN)
// and optionally unlocks the SIM with a PIN.
type GenericProfile struct {
	lockedProfile
	simPIN     string
	singlePort bool
}

func (p *GenericProfile) Init(d *at.Device) error {
	p.Dev = d
	d.State = &at.DeviceState{}

	// Flush
	d.Send(at.NoopCmd)

	// Unlock SIM if PIN is provided
	if p.simPIN != "" {
		if _, err := d.Send("AT+CPIN=" + p.simPIN); err != nil {
			return fmt.Errorf("SIM PIN unlock failed: %w", err)
		}
		time.Sleep(2 * time.Second)
	}

	// Standard SMS init sequence (no vendor-specific commands)
	if err := p.CMGF(false); err != nil {
		return fmt.Errorf("at init: unable to switch message format to PDU: %w", err)
	}
	if err := p.CPMS(at.MemoryTypes.NvRAM, at.MemoryTypes.NvRAM, at.MemoryTypes.NvRAM); err != nil {
		return fmt.Errorf("at init: unable to set messages storage: %w", err)
	}

	if p.singlePort {
		// Single-port modems run no Watch() loop: nobody reads unsolicited
		// +CMTI/+CLIP lines, and they would corrupt command replies on the
		// shared port. Keep notifications off entirely.
		if err := p.CNMI(0, 0, 0, 0, 0); err != nil {
			return fmt.Errorf("at init: unable to turn off message notifications: %w", err)
		}
		return p.FetchInbox()
	}

	if err := p.CNMI(1, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("at init: unable to turn on message notifications: %w", err)
	}
	if err := p.CLIP(true); err != nil {
		return fmt.Errorf("at init: unable to turn on calling party ID notifications: %w", err)
	}

	return p.FetchInbox()
}

// Huawei-specific no-ops — these are called by handleReport (via Watch)
// but have no meaning on generic modems.
func (p *GenericProfile) SYSINFO() (*at.SystemInfoReport, error) { return nil, nil }
func (p *GenericProfile) BOOT(token uint64) error                { return nil }
func (p *GenericProfile) SYSCFG(roaming, cellular bool) error    { return nil }
func (p *GenericProfile) COPS(auto bool, text bool) error        { return nil }

// HuaweiProfile extends DefaultProfile with SIM PIN support.
// After optional PIN unlock it delegates to the full Huawei init (AT^SYSINFO, etc.).
type HuaweiProfile struct {
	lockedProfile
	simPIN     string
	singlePort bool
}

func (p *HuaweiProfile) Init(d *at.Device) error {
	if p.simPIN != "" {
		p.Dev = d
		d.Send(at.NoopCmd) // flush
		if _, err := d.Send("AT+CPIN=" + p.simPIN); err != nil {
			return fmt.Errorf("SIM PIN unlock failed: %w", err)
		}
		time.Sleep(2 * time.Second)
	}
	if err := p.DefaultProfile.Init(d); err != nil {
		return err
	}
	if p.singlePort {
		// Same rationale as GenericProfile: without Watch() nobody consumes
		// unsolicited reports, so silence them after the vendor init.
		if err := p.CNMI(0, 0, 0, 0, 0); err != nil {
			return fmt.Errorf("at init: unable to turn off message notifications: %w", err)
		}
	}
	return nil
}

// probeProfile is a minimal profile used only for modem detection.
// It initialises just enough for Send() to work without running any AT init sequence.
type probeProfile struct {
	at.DefaultProfile
}

func (p *probeProfile) Init(d *at.Device) error {
	p.Dev = d
	d.State = &at.DeviceState{}
	return nil
}

// resolveProfile returns the appropriate DeviceProfile for the given name.
// All profiles share the per-modem command-port mutex.
func resolveProfile(name, simPIN string, singlePort bool, mu *sync.Mutex) at.DeviceProfile {
	switch name {
	case "huawei-e173":
		return &HuaweiProfile{lockedProfile: lockedProfile{mu: mu}, simPIN: simPIN, singlePort: singlePort}
	default:
		return &GenericProfile{lockedProfile: lockedProfile{mu: mu}, simPIN: simPIN, singlePort: singlePort}
	}
}

// detectProfile probes the modem with ATI and returns a profile name
// based on the manufacturer/model response.
func detectProfile(dev *at.Device) string {
	if err := dev.Init(&probeProfile{}); err != nil {
		log.Printf("probe init (non-fatal): %v", err)
	}

	reply, err := dev.Send("ATI")
	if err != nil {
		return "generic"
	}

	upper := strings.ToUpper(reply)
	if strings.Contains(upper, "HUAWEI") || strings.Contains(upper, "E173") {
		return "huawei-e173"
	}
	return "generic"
}
