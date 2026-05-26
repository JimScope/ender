package main

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	agentcore "github.com/JimScope/vendel/agent-core"
	"github.com/linxGnu/gosmpp"
	"github.com/linxGnu/gosmpp/data"
	"github.com/linxGnu/gosmpp/pdu"
	"golang.org/x/time/rate"
)

const (
	// esm_class bit indicating the DeliverSM carries a delivery receipt
	esmClassDLR = 0x04

	defaultEnquireLinkSeconds = 30
	defaultThrottleTPS        = 10
)

// smppBind wraps a gosmpp session and correlates SubmitSMResp / DeliverSM
// responses back to Vendel message IDs.
type smppBind struct {
	cfg        *RemoteSMPPConfig
	client     *agentcore.Client
	session    *gosmpp.Session
	sourceAddr pdu.Address
	throttle   *rate.Limiter

	// pending holds Vendel message IDs awaiting a SubmitSMResp in FIFO order.
	// SMPP response ordering on a single TCP session matches submit order.
	pending chan string

	// smscToVendel maps SMSC-assigned message IDs back to Vendel message IDs,
	// used to correlate deliver_sm (DLR) to the original submission.
	mu           sync.Mutex
	smscToVendel map[string]string
}

func newSMPPBind(cfg *RemoteSMPPConfig, client *agentcore.Client) (*smppBind, error) {
	if cfg.UseTLS {
		log.Printf("[%s] WARNING: use_tls=true is not yet supported; connecting over plain TCP", cfg.DeviceID)
	}

	sourceAddr, err := pdu.NewAddressWithTonNpiAddr(
		byte(cfg.SourceTON), byte(cfg.SourceNPI), cfg.SourceAddr,
	)
	if err != nil {
		return nil, fmt.Errorf("invalid source address: %w", err)
	}

	tps := cfg.SubmitThrottleTPS
	if tps <= 0 {
		tps = defaultThrottleTPS
	}

	b := &smppBind{
		cfg:          cfg,
		client:       client,
		sourceAddr:   sourceAddr,
		throttle:     rate.NewLimiter(rate.Limit(tps), tps),
		pending:      make(chan string, 1000),
		smscToVendel: make(map[string]string),
	}

	enquireLink := time.Duration(cfg.EnquireLinkSeconds) * time.Second
	if enquireLink <= 0 {
		enquireLink = defaultEnquireLinkSeconds * time.Second
	}

	auth := gosmpp.Auth{
		SMSC:       fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		SystemID:   cfg.SystemID,
		Password:   cfg.Password,
		SystemType: cfg.SystemType,
	}

	connector := trxConnectorFor(cfg.BindMode, auth)

	settings := gosmpp.Settings{
		EnquireLink:  enquireLink,
		ReadTimeout:  enquireLink + 10*time.Second,
		WriteTimeout: 10 * time.Second,
		OnSubmitError: func(p pdu.PDU, err error) {
			b.handleSubmitError(p, err)
		},
		OnReceivingError: func(err error) {
			log.Printf("[%s] SMPP receiving error: %v", cfg.DeviceID, err)
		},
		OnRebindingError: func(err error) {
			log.Printf("[%s] SMPP rebinding error: %v", cfg.DeviceID, err)
		},
		OnPDU: func(p pdu.PDU, _ bool) {
			b.handlePDU(p)
		},
		OnClosed: func(state gosmpp.State) {
			log.Printf("[%s] SMPP session closed: state=%v", cfg.DeviceID, state)
		},
	}

	session, err := gosmpp.NewSession(connector, settings, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("open SMPP session: %w", err)
	}
	b.session = session
	return b, nil
}

// trxConnectorFor returns the right connector for the configured bind mode.
func trxConnectorFor(mode string, auth gosmpp.Auth) gosmpp.Connector {
	switch strings.ToLower(mode) {
	case "tx":
		return gosmpp.TXConnector(gosmpp.NonTLSDialer, auth)
	case "rx":
		return gosmpp.RXConnector(gosmpp.NonTLSDialer, auth)
	default: // trx
		return gosmpp.TRXConnector(gosmpp.NonTLSDialer, auth)
	}
}

func (b *smppBind) Close() error {
	if b.session == nil {
		return nil
	}
	return b.session.Close()
}

// Send submits an SMS over the SMPP bind and records it for later correlation.
func (b *smppBind) Send(msg agentcore.PendingMessage) {
	if err := b.throttle.Wait(context.Background()); err != nil {
		log.Printf("[%s] throttle error: %v", b.cfg.DeviceID, err)
		return
	}

	destAddr, err := pdu.NewAddressWithTonNpiAddr(
		byte(b.cfg.DestTON), byte(b.cfg.DestNPI), msg.Recipient,
	)
	if err != nil {
		log.Printf("[%s] invalid destination %s: %v", b.cfg.DeviceID, msg.Recipient, err)
		_ = b.client.ReportStatus(msg.MessageID, "failed", "invalid destination: "+err.Error())
		return
	}

	encoding := pickEncoding(msg.Body, b.cfg.DefaultDataCoding)
	shortMsg, err := pdu.NewShortMessageWithEncoding(msg.Body, encoding)
	if err != nil {
		log.Printf("[%s] encode message failed: %v", b.cfg.DeviceID, err)
		_ = b.client.ReportStatus(msg.MessageID, "failed", "encode failed: "+err.Error())
		return
	}

	submit := pdu.NewSubmitSM().(*pdu.SubmitSM)
	submit.SourceAddr = b.sourceAddr
	submit.DestAddr = destAddr
	submit.Message = shortMsg
	submit.RegisteredDelivery = 1 // request DLR

	// Record BEFORE Submit so the FIFO is populated when the response arrives.
	select {
	case b.pending <- msg.MessageID:
	default:
		log.Printf("[%s] pending buffer full, dropping message %s", b.cfg.DeviceID, msg.MessageID)
		_ = b.client.ReportStatus(msg.MessageID, "failed", "agent pending buffer full")
		return
	}

	trans := b.session.Transceiver()
	if trans == nil {
		// Fall back: TX bind has only Transmitter (still exposes Submit).
		tx := b.session.Transmitter()
		if tx == nil {
			<-b.pending
			_ = b.client.ReportStatus(msg.MessageID, "failed", "no SMPP transmitter available")
			return
		}
		if err := tx.Submit(submit); err != nil {
			<-b.pending
			log.Printf("[%s] submit failed: %v", b.cfg.DeviceID, err)
			_ = b.client.ReportStatus(msg.MessageID, "failed", err.Error())
		}
		return
	}

	if err := trans.Submit(submit); err != nil {
		<-b.pending
		log.Printf("[%s] submit failed: %v", b.cfg.DeviceID, err)
		_ = b.client.ReportStatus(msg.MessageID, "failed", err.Error())
	}
}

// handlePDU dispatches incoming PDUs: SubmitSMResp closes the loop on sends,
// DeliverSM is either an incoming SMS or a delivery receipt.
func (b *smppBind) handlePDU(p pdu.PDU) {
	switch v := p.(type) {
	case *pdu.SubmitSMResp:
		b.handleSubmitResp(v)
	case *pdu.DeliverSM:
		b.handleDeliver(v)
	}
}

func (b *smppBind) handleSubmitResp(resp *pdu.SubmitSMResp) {
	var vendelID string
	select {
	case vendelID = <-b.pending:
	default:
		log.Printf("[%s] submit_sm_resp without matching pending entry (smsc_id=%s)",
			b.cfg.DeviceID, resp.MessageID)
		return
	}

	if !resp.IsOk() {
		errMsg := fmt.Sprintf("SMSC rejected submit (smsc_id=%q)", resp.MessageID)
		log.Printf("[%s] %s", b.cfg.DeviceID, errMsg)
		_ = b.client.ReportStatus(vendelID, "failed", errMsg)
		return
	}

	if resp.MessageID != "" {
		b.mu.Lock()
		b.smscToVendel[resp.MessageID] = vendelID
		b.mu.Unlock()
	}

	if err := b.client.ReportStatus(vendelID, "sent", ""); err != nil {
		log.Printf("[%s] failed to report 'sent': %v", b.cfg.DeviceID, err)
	}
}

func (b *smppBind) handleDeliver(deliver *pdu.DeliverSM) {
	body, _ := deliver.Message.GetMessage()

	if deliver.EsmClass&esmClassDLR != 0 {
		smscID, stat := parseDLR(body)
		if smscID == "" {
			log.Printf("[%s] DLR without id field: %q", b.cfg.DeviceID, body)
			return
		}
		b.mu.Lock()
		vendelID, ok := b.smscToVendel[smscID]
		if ok {
			delete(b.smscToVendel, smscID)
		}
		b.mu.Unlock()
		if !ok {
			log.Printf("[%s] DLR for unknown smsc_id=%s stat=%s", b.cfg.DeviceID, smscID, stat)
			return
		}
		status := mapDLRStatus(stat)
		errMsg := ""
		if status == "failed" {
			errMsg = "SMSC status: " + stat
		}
		if err := b.client.ReportStatus(vendelID, status, errMsg); err != nil {
			log.Printf("[%s] failed to report DLR: %v", b.cfg.DeviceID, err)
		}
		return
	}

	// Incoming SMS (MO)
	from := deliver.SourceAddr.Address()
	log.Printf("[%s] incoming SMS from %s", b.cfg.DeviceID, from)
	if err := b.client.ReportIncoming(from, body, time.Now().UTC().Format(time.RFC3339)); err != nil {
		log.Printf("[%s] failed to report incoming SMS: %v", b.cfg.DeviceID, err)
	}
}

// handleSubmitError is invoked by gosmpp when a submit fails at the transport
// layer (TCP write, encoding, etc.). The PDU's sequence isn't reliable here
// since it may not have been assigned yet; we drop from the FIFO and report.
func (b *smppBind) handleSubmitError(_ pdu.PDU, err error) {
	log.Printf("[%s] submit transport error: %v", b.cfg.DeviceID, err)
	select {
	case vendelID := <-b.pending:
		_ = b.client.ReportStatus(vendelID, "failed", err.Error())
	default:
	}
}

// pickEncoding chooses GSM7 for ASCII-friendly bodies and UCS2 otherwise.
// If the operator overrode default_data_coding to a non-zero value we honour
// it for UCS-2 (8) and fall back to GSM7 for 0.
func pickEncoding(body string, override int) data.Encoding {
	if override == 8 {
		return data.UCS2
	}
	if override == 0 && isGSM7Safe(body) {
		return data.GSM7BIT
	}
	if !isGSM7Safe(body) {
		return data.UCS2
	}
	return data.GSM7BIT
}

func isGSM7Safe(s string) bool {
	for _, r := range s {
		if r > 0x7F {
			return false
		}
	}
	return true
}

// dlrIDRegex and dlrStatRegex match the conventional SMPP 3.4 DLR text format:
//
//	id:<smsc_id> sub:001 dlvrd:001 submit date:... done date:... stat:DELIVRD err:000 text:...
var dlrIDRegex = regexp.MustCompile(`(?i)\bid:([A-Za-z0-9._\-]+)`)
var dlrStatRegex = regexp.MustCompile(`(?i)\bstat:([A-Z]+)`)

func parseDLR(body string) (id, stat string) {
	if m := dlrIDRegex.FindStringSubmatch(body); len(m) == 2 {
		id = m[1]
	}
	if m := dlrStatRegex.FindStringSubmatch(body); len(m) == 2 {
		stat = strings.ToUpper(m[1])
	}
	return
}

// mapDLRStatus normalises the SMPP stat field to Vendel's internal status set.
func mapDLRStatus(stat string) string {
	switch stat {
	case "DELIVRD", "DELIVERED":
		return "delivered"
	case "ENROUTE", "ACCEPTD":
		return "sent"
	default:
		// UNDELIV, REJECTD, EXPIRED, DELETED, UNKNOWN, FAILED, ...
		return "failed"
	}
}
