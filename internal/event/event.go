package event

import "encoding/json"

const (
	TopicOrderStatus      = "order.status"
	TopicPayoutStatus     = "payout.status"
	TopicSMSInbound       = "sms.inbound"
	TopicMerchantLog      = "merchant.api_log"
	TopicProviderCallback = "provider.callback"
)

// Topics are the worker subscriptions. Each one has its own consumer group.
func Topics() []string {
	return []string{TopicOrderStatus, TopicPayoutStatus, TopicSMSInbound, TopicMerchantLog, TopicProviderCallback}
}

// Message is the Kafka value written by the relay.
type Message struct {
	EventID     string          `json:"event_id"`
	Topic       string          `json:"topic"`
	AggregateID string          `json:"aggregate_id"`
	Body        json.RawMessage `json:"body"`
}

// OrderStatus is the frozen merchant callback for one status.
type OrderStatus struct {
	OrderID     int64           `json:"order_id"`
	OrderUUID   string          `json:"order_uuid"`
	Status      string          `json:"status"`
	Amount      string          `json:"amount"`
	CallbackURL string          `json:"callback_url"`
	AccessToken string          `json:"access_token"`
	Resource    json.RawMessage `json:"resource"`
}

// SMS names the log row the worker parses and matches.
type SMS struct {
	LogID int64 `json:"log_id"`
}

// ProviderCallback is a parsed provider webhook the worker applies once.
type ProviderCallback struct {
	OrderUUID string `json:"order_uuid"`
	Action    string `json:"action"`
	Sub       string `json:"sub"`
}

// MerchantLog is one POST /api/v1/order record.
type MerchantLog struct {
	RequestID    string          `json:"request_id"`
	MerchantID   int64           `json:"merchant_id"`
	ExternalID   string          `json:"external_id"`
	Amount       string          `json:"amount"`
	Currency     string          `json:"currency"`
	Gateway      string          `json:"payment_gateway"`
	DetailType   string          `json:"payment_detail_type"`
	RequestData  json.RawMessage `json:"request_data"`
	ResponseData json.RawMessage `json:"response_data"`
	IP           string          `json:"ip_address"`
	UserAgent    string          `json:"user_agent"`
	ExecutionMS  float64         `json:"execution_time"`
	Successful   bool            `json:"is_successful"`
	ErrorMessage string          `json:"error_message"`
	OrderID      *int64          `json:"order_id"`
}
