package dominaite

import (
	"bytes"
	"encoding/json"
)

// Customer holds optional payer details. Prefilled fields are hidden from the
// payer in the widget, so the checkout form stays short.
type Customer struct {
	FirstName string `json:"firstName,omitempty"`
	LastName  string `json:"lastName,omitempty"`
	Email     string `json:"email,omitempty"`
	Phone     string `json:"phone,omitempty"`
}

// CreateCheckoutSessionParams are the parameters for
// Client.CreateCheckoutSession. Amount, Currency and OrderReference are required.
type CreateCheckoutSessionParams struct {
	// Amount is in MINOR units: 2500 is 25.00 EUR. Integers only.
	Amount int64 `json:"amount"`
	// Currency is ISO 4217, e.g. "EUR".
	Currency string `json:"currency"`
	// OrderReference is your own order id, at most 100 characters. It shows up
	// in your dashboard.
	OrderReference string `json:"orderReference"`

	Customer *Customer `json:"customer,omitempty"`
	// Country is ISO 3166-1 alpha-2.
	Country string `json:"country,omitempty"`
	// Language is the ISO 639-1 widget UI language.
	Language string `json:"language,omitempty"`
	// Theme is "light", "dark" or "bright".
	Theme       string `json:"theme,omitempty"`
	Description string `json:"description,omitempty"`

	// IdempotencyKey is auto-generated when empty. It travels in the header and
	// in the signature, never in the body. Retrying with the same key never
	// creates a second payment, so on a timeout retry with the same key.
	IdempotencyKey string `json:"-"`

	// Extra carries any additional field the API accepts that this struct does
	// not model yet. Keys here are merged into the JSON body.
	Extra map[string]any `json:"-"`
}

// MarshalJSON emits the request body: the modelled fields in declaration order,
// with Extra merged in. IdempotencyKey is excluded; it is a header, not a body
// field.
func (p CreateCheckoutSessionParams) MarshalJSON() ([]byte, error) {
	// The local type drops this method, so json.Marshal does not recurse.
	type params CreateCheckoutSessionParams

	base, err := marshalNoEscape(params(p))
	if err != nil {
		return nil, err
	}
	if len(p.Extra) == 0 {
		return base, nil
	}

	merged := map[string]json.RawMessage{}
	if err := json.Unmarshal(base, &merged); err != nil {
		return nil, err
	}
	for key, value := range p.Extra {
		raw, err := marshalNoEscape(value)
		if err != nil {
			return nil, err
		}
		merged[key] = raw
	}

	return marshalNoEscape(merged)
}

// marshalNoEscape encodes without Go's default HTML escaping, so an ampersand in
// a customer name stays an ampersand. The signature covers the exact bytes we
// send either way; this only keeps the body readable and consistent with the
// other Dominaite SDKs.
func marshalNoEscape(value any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	// Encode appends a newline; the body must not carry it.
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// CheckoutSession is what CreateCheckoutSession returns.
type CheckoutSession struct {
	TransactionID string `json:"transactionId"`
	OrderID       string `json:"orderId"`
	// CashierKey feeds the widget's data-cashier-key. A per-payment value, not
	// a credential.
	CashierKey string `json:"cashierKey"`
	// CashierToken feeds the widget's data-cashier-token. A per-payment value,
	// not a credential.
	CashierToken string `json:"cashierToken"`
	// Amount is in MINOR units.
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	// ExpiresAt is ISO 8601. Sessions are valid for 2 hours.
	ExpiresAt string `json:"expiresAt"`

	// Raw is the unparsed payload, for fields this struct does not model yet.
	Raw json.RawMessage `json:"-"`
}

// Ping is what Ping returns: proof that your key, secret, signing and clock are
// all good, without creating anything.
type Ping struct {
	// Pong is always true on a 200.
	Pong bool `json:"pong"`
	// MerchantID is the merchant your key authenticated as.
	MerchantID string `json:"merchantId"`
	// ServerTime is ISO 8601.
	ServerTime string `json:"serverTime,omitempty"`
	// ServerUnixTime is the server clock in unix seconds.
	ServerUnixTime int64 `json:"serverUnixTime,omitempty"`
	// ClockSkewSeconds is server time minus your X-Timestamp. If its absolute
	// value creeps toward 300, fix NTP now - requests start failing at 300.
	ClockSkewSeconds int64 `json:"clockSkewSeconds"`

	// Raw is the unparsed payload, for fields this struct does not model yet.
	Raw json.RawMessage `json:"-"`
}

// Transaction status values returned by GetStatus.
const (
	StatusPending           = "pending"
	StatusProcessing        = "processing"
	StatusSucceeded         = "succeeded"
	StatusFailed            = "failed"
	StatusRefunded          = "refunded"
	StatusPartiallyRefunded = "partially_refunded"
	StatusCancelled         = "cancelled"
	StatusDisputed          = "disputed"
	StatusRequiresCapture   = "requires_capture"
	StatusAbandoned         = "abandoned"
)

// CheckoutStatus is what GetStatus returns.
type CheckoutStatus struct {
	TransactionID  string `json:"transactionId"`
	OrderID        string `json:"orderId"`
	OrderReference string `json:"orderReference,omitempty"`
	// Status is one of the Status* constants.
	Status string `json:"status"`
	// Amount is in MINOR units.
	Amount         int64  `json:"amount"`
	Currency       string `json:"currency"`
	RefundedAmount int64  `json:"refundedAmount,omitempty"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt,omitempty"`
	// ExpiresAt is present while the session is still payable.
	ExpiresAt string `json:"expiresAt,omitempty"`

	// Raw is the unparsed payload, for fields this struct does not model yet.
	Raw json.RawMessage `json:"-"`
}
