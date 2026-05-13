package services

import "testing"

func TestWebhookHost_LowercasesHostname(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://Example.com/path", "example.com"},
		{"https://EXAMPLE.COM/path", "example.com"},
		{"https://example.com/path", "example.com"},
		{"https://Api.Example.com:8443/x", "api.example.com"},
	}
	for _, tc := range cases {
		got := webhookHost(tc.in)
		if got != tc.want {
			t.Errorf("webhookHost(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestWebhookHost_UnparseableReturnsSentinel(t *testing.T) {
	cases := []string{
		"",
		"://broken",
		"not a url",
		"http:///no-host",
	}
	for _, in := range cases {
		got := webhookHost(in)
		if got != webhookUnparseableHostBucket {
			t.Errorf("webhookHost(%q) = %q, want sentinel %q", in, got, webhookUnparseableHostBucket)
		}
	}
}
