package agentcore

// PendingMessage is the payload pushed to an agent for outbound SMS dispatch.
// The same shape is returned by GET /api/sms/pending and by SSE events on
// <agentTopic>/<deviceID>.
type PendingMessage struct {
	MessageID string `json:"message_id"`
	Recipient string `json:"recipient"`
	Body      string `json:"body"`
}
