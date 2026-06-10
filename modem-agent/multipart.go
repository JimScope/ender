package main

import (
	"bytes"
	"fmt"
	"sort"
	"sync/atomic"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/xlab/at"
	"github.com/xlab/at/pdu"
	"github.com/xlab/at/sms"
)

const (
	// Single-PDU capacity per encoding.
	maxGsm7SingleLen = 160 // septets
	maxUcs2SingleLen = 70  // UTF-16 code units (140 bytes)

	// Concatenated UCS-2 part capacity: 140 bytes - 6-byte UDH = 134 bytes.
	maxUcs2PartLen = 67 // UTF-16 code units

	// Hard cap so a single API call cannot tie up the modem for minutes.
	maxParts = 10
)

// msgRef is the concatenation reference counter shared by all modems in
// the process; receivers only compare it per-sender so a byte wrap is fine.
var msgRef atomic.Uint32

// sendSMS sends a text message, transparently splitting long texts into
// concatenated parts. Short messages use the library's single-PDU path.
//
// Long messages are always encoded as UCS-2: the library cannot emit a UDH
// (encodedUserData ignores Message.UserDataHeader), so parts are built here
// as raw TPDUs — and UCS-2 is byte-aligned, avoiding the septet fill-bit
// packing that GSM-7 concatenation would require. The cost is more parts
// for long pure-GSM texts (67 vs 153 chars per part); the gain is PDU
// construction simple enough to be verifiably correct.
func sendSMS(dev *at.Device, text, recipient string) error {
	if pdu.Is7BitEncodable(text) && utf8.RuneCountInString(text) <= maxGsm7SingleLen {
		return dev.SendSMS(text, sms.PhoneNumber(recipient))
	}

	units := utf16.Encode([]rune(text))
	if len(units) <= maxUcs2SingleLen {
		return dev.SendSMS(text, sms.PhoneNumber(recipient))
	}

	parts := splitUTF16(units, maxUcs2PartLen)
	if len(parts) > maxParts {
		return fmt.Errorf("message too long: %d parts needed, max %d (%d UTF-16 units)", len(parts), maxParts, len(units))
	}

	ref := byte(msgRef.Add(1))
	for i, part := range parts {
		n, octets, err := buildConcatPDU(recipient, part, ref, len(parts), i+1)
		if err != nil {
			return fmt.Errorf("build part %d/%d: %w", i+1, len(parts), err)
		}
		// Commands is our locked profile, so this serializes with Watch.
		if _, err := dev.Commands.CMGS(n, octets); err != nil {
			return fmt.Errorf("send part %d/%d: %w", i+1, len(parts), err)
		}
	}
	return nil
}

// splitUTF16 chunks UTF-16 code units without splitting a surrogate pair
// across two parts (a high surrogate at a cut point moves to the next part).
func splitUTF16(units []uint16, maxLen int) [][]uint16 {
	var parts [][]uint16
	for len(units) > 0 {
		n := min(maxLen, len(units))
		if n < len(units) && units[n-1] >= 0xD800 && units[n-1] <= 0xDBFF {
			n--
		}
		parts = append(parts, units[:n])
		units = units[n:]
	}
	return parts
}

// buildConcatPDU assembles a raw SMS-SUBMIT TPDU carrying one UCS-2 part of
// a concatenated message (3GPP TS 23.040). Returns the TPDU length expected
// by AT+CMGS and the full octet string (zero-length SMSC prefix + TPDU).
func buildConcatPDU(recipient string, units []uint16, ref byte, total, seq int) (int, []byte, error) {
	addrLen, addrOctets, err := sms.PhoneNumber(recipient).PDU()
	if err != nil {
		return 0, nil, fmt.Errorf("encode address %q: %w", recipient, err)
	}

	// TP-UD: concatenation UDH (IEI 0x00) followed by UCS-2 text.
	var ud bytes.Buffer
	ud.Write([]byte{0x05, 0x00, 0x03, ref, byte(total), byte(seq)})
	for _, u := range units {
		ud.WriteByte(byte(u >> 8))
		ud.WriteByte(byte(u))
	}

	var tpdu bytes.Buffer
	// SMS-SUBMIT (0x01) | VPF relative (0x02<<3) | UDHI (1<<6) — mirrors the
	// header the library builds for its single-part submits, plus UDHI.
	tpdu.WriteByte(0x51)
	tpdu.WriteByte(0x00)          // TP-MR: assigned by the modem
	tpdu.WriteByte(byte(addrLen)) // TP-DA: number of digits
	tpdu.Write(addrOctets)        // TP-DA: type-of-address + semi-octets
	tpdu.WriteByte(0x00)          // TP-PID: standard short message
	tpdu.WriteByte(0x08)          // TP-DCS: UCS-2
	// Same 4-day relative validity the library uses in Device.SendSMS.
	tpdu.WriteByte(sms.RelativeValidityPeriod(24 * time.Hour * 4).Octet())
	tpdu.WriteByte(byte(ud.Len())) // TP-UDL: octets for UCS-2
	tpdu.Write(ud.Bytes())

	octets := append([]byte{0x00}, tpdu.Bytes()...) // zero-length SMSC: use SIM default
	return tpdu.Len(), octets, nil
}

// incomingMessage is a fully assembled inbound SMS ready to report.
type incomingMessage struct {
	from string
	text string
	ts   time.Time
}

type partialIncoming struct {
	parts     map[int]string // sequence -> text
	total     int
	from      string
	ts        time.Time
	firstSeen time.Time
}

// reassembler joins concatenated inbound parts by (sender, UDH reference).
// It is only touched from the per-modem incoming loop goroutine, so it
// needs no locking.
type reassembler struct {
	pending map[string]*partialIncoming
}

func newReassembler() *reassembler {
	return &reassembler{pending: make(map[string]*partialIncoming)}
}

// add registers one part and returns the assembled message once all parts
// arrived.
func (r *reassembler) add(from, text string, header sms.UserDataHeader, ts time.Time) (incomingMessage, bool) {
	key := fmt.Sprintf("%s/%d", from, header.Tag)
	p, ok := r.pending[key]
	if !ok {
		p = &partialIncoming{
			parts:     make(map[int]string),
			total:     header.TotalNumber,
			from:      from,
			ts:        ts,
			firstSeen: time.Now(),
		}
		r.pending[key] = p
	}
	p.parts[header.Sequence] = text

	if len(p.parts) < p.total {
		return incomingMessage{}, false
	}
	delete(r.pending, key)
	return p.assemble(), true
}

// flushStale returns partially received messages older than maxAge, joined
// from whatever parts arrived, so a lost part doesn't black-hole the rest.
func (r *reassembler) flushStale(maxAge time.Duration) []incomingMessage {
	var flushed []incomingMessage
	for key, p := range r.pending {
		if time.Since(p.firstSeen) > maxAge {
			delete(r.pending, key)
			flushed = append(flushed, p.assemble())
		}
	}
	return flushed
}

func (p *partialIncoming) assemble() incomingMessage {
	seqs := make([]int, 0, len(p.parts))
	for seq := range p.parts {
		seqs = append(seqs, seq)
	}
	sort.Ints(seqs)

	var text bytes.Buffer
	for _, seq := range seqs {
		text.WriteString(p.parts[seq])
	}
	return incomingMessage{from: p.from, text: text.String(), ts: p.ts}
}
