package smsprovider

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/pinpointsmsvoicev2"
	"github.com/aws/smithy-go"
)

type mockAEUMClient struct {
	output *pinpointsmsvoicev2.SendTextMessageOutput
	err    error
}

func (m *mockAEUMClient) SendTextMessage(ctx context.Context, in *pinpointsmsvoicev2.SendTextMessageInput, opts ...func(*pinpointsmsvoicev2.Options)) (*pinpointsmsvoicev2.SendTextMessageOutput, error) {
	return m.output, m.err
}

func newTestRequest() SendRequest {
	return SendRequest{
		To:   "+15551234567",
		Body: "Hi",
	}
}

func newTestProvider(client AEUMClient) *AEUMProvider {
	return NewAEUMProvider(AEUMConfig{
		Enabled:                true,
		OriginationIdentityARN: "arn:aws:sms-voice:us-east-1:111122223333:pool/pool-id",
		Client:                 client,
	})
}

func TestAEUMProvider_Send_Success(t *testing.T) {
	mock := &mockAEUMClient{
		output: &pinpointsmsvoicev2.SendTextMessageOutput{
			MessageId: aws.String("aws-msg-id-123"),
		},
	}
	provider := newTestProvider(mock)

	res, err := provider.Send(context.Background(), newTestRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("expected SendResult, got nil")
	}
	if res.ProviderMessageID != "aws-msg-id-123" {
		t.Errorf("ProviderMessageID = %q, want %q", res.ProviderMessageID, "aws-msg-id-123")
	}
	if res.Status != StatusSent {
		t.Errorf("Status = %q, want %q", res.Status, StatusSent)
	}
	if res.ProviderChannel != ChannelAuto {
		t.Errorf("ProviderChannel = %q, want %q", res.ProviderChannel, ChannelAuto)
	}
	if res.ErrorMessage != "" {
		t.Errorf("ErrorMessage = %q, want empty", res.ErrorMessage)
	}
}

func TestAEUMProvider_Send_ValidationException(t *testing.T) {
	mock := &mockAEUMClient{
		err: &smithy.GenericAPIError{
			Code:    "ValidationException",
			Message: "DestinationPhoneNumber is invalid",
		},
	}
	provider := newTestProvider(mock)

	res, err := provider.Send(context.Background(), newTestRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != StatusFailed {
		t.Errorf("Status = %q, want %q", res.Status, StatusFailed)
	}
	if !strings.Contains(strings.ToLower(res.ErrorMessage), "validation") {
		t.Errorf("ErrorMessage = %q, want to contain %q", res.ErrorMessage, "validation")
	}
	if res.ProviderMessageID != "" {
		t.Errorf("ProviderMessageID = %q, want empty on failure", res.ProviderMessageID)
	}
}

func TestAEUMProvider_Send_ThrottlingException(t *testing.T) {
	mock := &mockAEUMClient{
		err: &smithy.GenericAPIError{
			Code:    "ThrottlingException",
			Message: "Rate exceeded",
		},
	}
	provider := newTestProvider(mock)

	res, err := provider.Send(context.Background(), newTestRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != StatusFailed {
		t.Errorf("Status = %q, want %q", res.Status, StatusFailed)
	}
	if !strings.Contains(strings.ToLower(res.ErrorMessage), "throttling") {
		t.Errorf("ErrorMessage = %q, want to mention throttling", res.ErrorMessage)
	}
}

func TestAEUMProvider_IsConfigured(t *testing.T) {
	mock := &mockAEUMClient{}

	tests := []struct {
		name string
		cfg  AEUMConfig
		want bool
	}{
		{
			name: "fully configured",
			cfg: AEUMConfig{
				Enabled:                true,
				OriginationIdentityARN: "arn:aws:sms-voice:us-east-1:111122223333:pool/pool-id",
				Client:                 mock,
			},
			want: true,
		},
		{
			name: "disabled",
			cfg: AEUMConfig{
				Enabled:                false,
				OriginationIdentityARN: "arn:aws:sms-voice:us-east-1:111122223333:pool/pool-id",
				Client:                 mock,
			},
			want: false,
		},
		{
			name: "missing origination identity ARN",
			cfg: AEUMConfig{
				Enabled:                true,
				OriginationIdentityARN: "",
				Client:                 mock,
			},
			want: false,
		},
		{
			name: "nil client",
			cfg: AEUMConfig{
				Enabled:                true,
				OriginationIdentityARN: "arn:aws:sms-voice:us-east-1:111122223333:pool/pool-id",
				Client:                 nil,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := NewAEUMProvider(tt.cfg)
			if got := provider.IsConfigured(); got != tt.want {
				t.Errorf("IsConfigured() = %v, want %v", got, tt.want)
			}
		})
	}
}
