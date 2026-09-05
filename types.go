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

// Statuses is the complete v1 status vocabulary. A value outside this list means
// the gateway grew a status this SDK does not know yet: keep polling, never
// treat it as terminal.
var Statuses = []string{
	StatusPending,
	StatusProcessing,
	StatusSucceeded,
	StatusFailed,
	StatusRefunded,
	StatusPartiallyRefunded,
	StatusCancelled,
	StatusDisputed,
	StatusRequiresCapture,
	StatusAbandoned,
}

// checkoutSessionEnvelope is the create-session response as it arrives on the
// wire. Business refusals come back as HTTP 200 with success false, so the
// branch is on Success, not on the status code. Checkout is present only on
// success; TransactionID, ErrorCode and ErrorMessage only on a refusal.
type checkoutSessionEnvelope struct {
	Success       *bool           `json:"success"`
	Checkout      json.RawMessage `json:"checkout"`
	TransactionID string          `json:"transactionId"`
	ErrorCode     string          `json:"errorCode"`
	ErrorMessage  string          `json:"errorMessage"`
}

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

// Webhook event types. This is the complete v1 catalog - endpoint registration
// rejects anything outside it, and the match is case-sensitive.
//
// EventPaymentSucceeded is the ONLY signal that means money is in hand. The
// in-flight states (pending, processing) are deliberately not webhooked; poll
// GetStatus if you want to drive UX off them.
const (
	EventPaymentSucceeded       = "payment.succeeded"
	EventPaymentFailed          = "payment.failed"
	EventPaymentRequiresCapture = "payment.requires_capture"
	EventPaymentCancelled       = "payment.cancelled"
	EventPaymentAbandoned       = "payment.abandoned"
	EventPaymentRefunded        = "payment.refunded"
	EventPaymentDisputed        = "payment.disputed"
)

// WebhookEvent is one verified webhook delivery. VerifyWebhook returns it; there
// is no other way to get one, which is the point - the signature is checked
// before the body is parsed.
//
// The envelope is FLAT. There is no ApiResponse wrapper and no `success` field,
// so do not branch on one.
type WebhookEvent struct {
	// ID is the delivery id and YOUR DEDUPE KEY. Delivery is at-least-once, so
	// the same ID can arrive more than once and you must ignore the repeats.
	ID string `json:"id"`
	// Type is one of the Event* constants. Treat an unrecognised type as a
	// no-op rather than an error; the catalog can grow.
	Type string `json:"type"`
	// CreatedAt is the ISO 8601 UTC instant of the transition, not of delivery.
	// On a retry it still carries the original transition time.
	CreatedAt string      `json:"createdAt"`
	Data      WebhookData `json:"data"`

	// Raw is the exact verified payload, byte for byte as signed. Use it for
	// fields this struct does not model yet, or to store the delivery verbatim.
	Raw json.RawMessage `json:"-"`
}

// WebhookData is the payload of a WebhookEvent.
type WebhookData struct {
	TransactionID string `json:"transactionId"`
	// OrderReference is your own order id, as sent on create session. Match a
	// delivery to your order on this field. Empty only for payments that did not
	// start through the API; payment.refunded carries the original payment's.
	OrderReference string `json:"orderReference"`
	// OrderID is the hosted checkout order id, the same value the status GET
	// returns. Empty on refunds and reversals.
	OrderID string `json:"orderId"`
	// Description is the description sent on create session. Empty when none
	// was given and on refunds and reversals.
	Description string `json:"description"`
	// Status is the wire status of the row, one of the Status* constants.
	Status string `json:"status"`
	// PreviousStatus is the status the row moved from. Nullable on the wire;
	// empty string when absent.
	PreviousStatus string `json:"previousStatus"`
	// Kind is the movement kind, e.g. "sale". Nullable on the wire; empty
	// string when absent.
	Kind string `json:"kind"`

	// Amount is in MINOR units. For payment.* it is what the merchant is PAID
	// (the base amount). For payment.refunded it is what was returned to the
	// customer. It is NOT the card movement - that is GrossAmount.
	Amount int64 `json:"amount"`
	// GrossAmount is the card movement in MINOR units: what the customer's card
	// was actually charged.
	GrossAmount int64 `json:"grossAmount"`
	// SurchargeAmount is in MINOR units and nil when no surcharge is known.
	// A pointer, so "no surcharge information" stays distinct from "zero".
	SurchargeAmount *int64 `json:"surchargeAmount"`
	// Currency is ISO 4217.
	Currency string `json:"currency"`

	// PaymentMethod is how the payer paid: card, wallet, bank_transfer or sepa.
	// Empty while the payment is still open.
	PaymentMethod string `json:"paymentMethod"`
	// WalletType names the wallet when PaymentMethod is wallet (apple_pay,
	// google_pay, ...). Empty for non-wallet payments.
	WalletType string `json:"walletType"`
	// PaymentMethodBrand is the lower-cased card brand (visa, mastercard, ...)
	// once a card payment was attempted. Empty otherwise.
	PaymentMethodBrand string `json:"paymentMethodBrand"`
	// PaymentMethodLast4 is the card's last four digits once a card payment was
	// attempted. Empty otherwise.
	PaymentMethodLast4 string `json:"paymentMethodLast4"`

	// OriginalTransactionID is the parent transaction for refunds and
	// reversals. Empty on an original payment.
	OriginalTransactionID string `json:"originalTransactionId"`
	// IdempotencyKey is your own mint key when the gateway knows it - the
	// cheapest way to match a delivery back to your order without a lookup.
	// Empty when unknown, which today includes every refund.
	IdempotencyKey string `json:"idempotencyKey"`

	// Raw is the unparsed data object, for fields not modelled above.
	Raw json.RawMessage `json:"-"`
}
