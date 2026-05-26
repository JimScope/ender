// Package smsprovider defines the abstraction Vendel uses to talk to external
// SMS/RCS gateways such as AWS End User Messaging. Implementations must be
// pure I/O: persistence, quota and webhook dispatch live in package services,
// which imports this subpackage. Never import services from here — it would
// create a dependency cycle.
package smsprovider

import "context"

type Provider interface {
	Name() string
	IsConfigured() bool
	Send(ctx context.Context, req SendRequest) (*SendResult, error)
}

type SendRequest struct {
	To          string
	Body        string
	ChannelHint string
}

// SendResult is the provider's flat response. Status is intentionally
// restricted to StatusSent / StatusFailed; "delivered" is only reachable from
// the provider's delivery webhook, not from a synchronous send.
type SendResult struct {
	ProviderMessageID   string
	Status              string
	ErrorMessage        string
	ProviderChannel     string
	OriginationIdentity string
}

const (
	StatusSent      = "sent"
	StatusFailed    = "failed"
	StatusDelivered = "delivered"

	ChannelAuto    = "auto"
	ChannelSMS     = "sms"
	ChannelRCS     = "rcs"
	ChannelUnknown = "unknown"

	// DeviceTypeAEUM is the sms_devices.device_type value reserved for the
	// global AWS End User Messaging virtual device. Centralised here so hooks,
	// migrations, dispatch and setup all reference the same literal.
	DeviceTypeAEUM = "aws_aeum"
)
