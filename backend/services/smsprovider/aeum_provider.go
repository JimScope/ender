package smsprovider

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/pinpointsmsvoicev2"
	"github.com/aws/aws-sdk-go-v2/service/pinpointsmsvoicev2/types"
	"github.com/aws/smithy-go"
)

// AEUMConfig holds the AWS End User Messaging provider configuration.
type AEUMConfig struct {
	Enabled                bool
	OriginationIdentityARN string
	ConfigurationSetName   string
	Region                 string
	Client                 AEUMClient
}

// AEUMClient is the minimal interface we depend on from the AWS SDK so we
// can mock it in tests without spinning up the real client.
type AEUMClient interface {
	SendTextMessage(ctx context.Context, in *pinpointsmsvoicev2.SendTextMessageInput, opts ...func(*pinpointsmsvoicev2.Options)) (*pinpointsmsvoicev2.SendTextMessageOutput, error)
}

type AEUMProvider struct {
	client                 AEUMClient
	originationIdentityARN string
	configurationSetName   string
	enabled                bool
}

// NewAEUMProvider builds a provider from config. If cfg.Client is nil the
// caller must wire a real pinpointsmsvoicev2 client elsewhere (we keep this
// constructor pure so it can be used from tests).
func NewAEUMProvider(cfg AEUMConfig) *AEUMProvider {
	return &AEUMProvider{
		client:                 cfg.Client,
		originationIdentityARN: cfg.OriginationIdentityARN,
		configurationSetName:   cfg.ConfigurationSetName,
		enabled:                cfg.Enabled,
	}
}

func (p *AEUMProvider) Name() string { return "aws_aeum" }

func (p *AEUMProvider) IsConfigured() bool {
	return p != nil && p.enabled && p.originationIdentityARN != "" && p.client != nil
}

func (p *AEUMProvider) Send(ctx context.Context, req SendRequest) (*SendResult, error) {
	if !p.IsConfigured() {
		return &SendResult{Status: StatusFailed, ErrorMessage: "AEUM provider not configured"}, nil
	}

	input := &pinpointsmsvoicev2.SendTextMessageInput{
		DestinationPhoneNumber: aws.String(req.To),
		OriginationIdentity:    aws.String(p.originationIdentityARN),
		MessageBody:            aws.String(req.Body),
		MessageType:            types.MessageTypeTransactional,
	}
	if p.configurationSetName != "" {
		input.ConfigurationSetName = aws.String(p.configurationSetName)
	}

	out, err := p.client.SendTextMessage(ctx, input)
	if err != nil {
		return &SendResult{
			Status:       StatusFailed,
			ErrorMessage: simplifyAWSError(err),
		}, nil
	}

	return &SendResult{
		ProviderMessageID:   aws.ToString(out.MessageId),
		Status:              StatusSent,
		ProviderChannel:     ChannelAuto,
		OriginationIdentity: p.originationIdentityARN,
	}, nil
}

// simplifyAWSError maps common smithy/AWS error codes to short human-readable
// strings so we don't dump SDK internals into sms_messages.error_message.
func simplifyAWSError(err error) string {
	if err == nil {
		return ""
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "ValidationException":
			return fmt.Sprintf("AWS validation error: %s", apiErr.ErrorMessage())
		case "ThrottlingException":
			return "AWS throttling: too many requests"
		case "ConflictException":
			return fmt.Sprintf("AWS conflict: %s", apiErr.ErrorMessage())
		case "AccessDeniedException":
			return "AWS access denied — check IAM policy"
		case "ResourceNotFoundException":
			return "AWS resource not found — check pool/identity ARN"
		default:
			return fmt.Sprintf("AWS error (%s)", apiErr.ErrorCode())
		}
	}
	return err.Error()
}
