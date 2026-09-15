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

	// SaveCard asks the gateway to keep the card on file once this payment is
	// approved, so you can charge it again later with Client.ChargePaymentMethod.
	// The stored method shows up on CheckoutStatus.PaymentMethod after the
	// payment succeeds; a declined first payment stores nothing. The card details
	// themselves never reach you: you get an id, a brand and the last four digits.
	SaveCard bool `json:"saveCard,omitempty"`

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
	// PaymentMethod is the card kept on file for this payment. Present once a
	// session created with SaveCard has succeeded; nil otherwise. Store
	// PaymentMethod.ID against your customer - it is what
	// Client.ChargePaymentMethod takes.
	PaymentMethod *PaymentMethod `json:"paymentMethod,omitempty"`

	// Raw is the unparsed payload, for fields this struct does not model yet.
	Raw json.RawMessage `json:"-"`
}

// Stored payment method status values, in the gateway's own order.
//
// Only active methods can be charged. PaymentMethodStatusRevoked is what
// Client.RevokePaymentMethod leaves behind; PaymentMethodStatusExpired means the
// card's expiry date has passed.
const (
	PaymentMethodStatusActive  = "active"
	PaymentMethodStatusRevoked = "revoked"
	PaymentMethodStatusExpired = "expired"
)

// PaymentMethodStatuses is the complete v1 stored-payment-method status
// vocabulary. Treat a value outside this list as not chargeable.
var PaymentMethodStatuses = []string{
	PaymentMethodStatusActive,
	PaymentMethodStatusRevoked,
	PaymentMethodStatusExpired,
}

// PaymentMethod is a card kept on file. Never the card number, never the PSP
// token - only what you may show a customer.
type PaymentMethod struct {
	// ID is opaque (pm_...) - the handle you charge and revoke with.
	ID string `json:"id"`
	// Brand is the card brand as the gateway reports it, e.g. "visa", "mastercard".
	Brand string `json:"brand"`
	// Last4 is the last four digits of the card number, for display only.
	Last4 string `json:"last4"`
	// ExpiryMonth is 1 to 12.
	ExpiryMonth int `json:"expiryMonth"`
	// ExpiryYear is four digits, e.g. 2029.
	ExpiryYear int `json:"expiryYear"`
	// Status is one of the PaymentMethodStatus* constants. Treat any value you
	// do not recognise as not chargeable.
	Status string `json:"status"`
}

// ChargePaymentMethodParams are the parameters for Client.ChargePaymentMethod.
// Amount, Currency and OrderReference are required.
type ChargePaymentMethodParams struct {
	// Amount is in MINOR units: 2500 is 25.00 EUR. Integers only.
	Amount int64 `json:"amount"`
	// Currency is ISO 4217, e.g. "EUR".
	Currency string `json:"currency"`
	// OrderReference is your own order id, at most 100 characters. It shows up
	// in your dashboard.
	OrderReference string `json:"orderReference"`
	Description    string `json:"description,omitempty"`

	// IdempotencyKey is auto-generated when empty. It travels in the header and
	// in the signature, never in the body. Retrying with the same key never
	// charges the card twice, so on a timeout retry with the same key.
	IdempotencyKey string `json:"-"`
}

// Charge status values returned by ChargePaymentMethod. ChargeStatusPending is
// not terminal: keep polling GetStatus on the charge's TransactionID.
const (
	ChargeStatusSucceeded = "succeeded"
	ChargeStatusFailed    = "failed"
	ChargeStatusPending   = "pending"
)

// ChargeStatuses is the complete v1 charge status vocabulary. Treat a value
// outside this list as still open.
var ChargeStatuses = []string{
	ChargeStatusSucceeded,
	ChargeStatusFailed,
	ChargeStatusPending,
}

// Decline classes, coarse enough to act on without reading the issuer's code:
//   - DeclineClassHard: do not retry this card, ask the customer for another one.
//   - DeclineClassSoftFunds: insufficient funds; retry later (after the
//     customer's payday, not in a loop).
//   - DeclineClassSoftSCARequired: the issuer wants the customer present; send
//     them through a hosted checkout session with SaveCard instead of charging
//     off-session again.
//   - DeclineClassSoftOther: a transient issuer or network condition; one retry
//     later is reasonable.
const (
	DeclineClassHard            = "hard"
	DeclineClassSoftFunds       = "soft_funds"
	DeclineClassSoftSCARequired = "soft_sca_required"
	DeclineClassSoftOther       = "soft_other"
)

// DeclineClasses is the complete v1 decline class vocabulary.
var DeclineClasses = []string{
	DeclineClassHard,
	DeclineClassSoftFunds,
	DeclineClassSoftSCARequired,
	DeclineClassSoftOther,
}

// PaymentMethodCharge is what ChargePaymentMethod returns. A decline is a
// result, not an error: Status is ChargeStatusFailed and DeclineClass says what
// to do next.
type PaymentMethodCharge struct {
	ChargeID string `json:"chargeId"`
	// Status is one of the ChargeStatus* constants. Treat anything you do not
	// recognise as still open.
	Status string `json:"status"`
	// DeclineClass is one of the DeclineClass* constants, present when Status is
	// failed. Empty otherwise.
	DeclineClass string `json:"declineClass,omitempty"`
	// DeclineCode is the raw decline code, for your logs; branch on
	// DeclineClass instead. Empty unless the charge failed.
	DeclineCode string `json:"declineCode,omitempty"`
	// TransactionID is the transaction the charge created; readable with
	// GetStatus.
	TransactionID string `json:"transactionId"`

	// Raw is the unparsed payload, for fields this struct does not model yet.
	Raw json.RawMessage `json:"-"`
}

// chargeRefusalProbe reads just enough of a charge response to tell a refusal
// from a result. Business refusals (replayed key, payments off, method not
// chargeable) reuse the create endpoint's HTTP 200 success=false shape, so
// the branch is on Success and the presence of a chargeId, not on the status
// code.
type chargeRefusalProbe struct {
	Success       *bool  `json:"success"`
	ChargeID      string `json:"chargeId"`
	TransactionID string `json:"transactionId"`
	ErrorCode     string `json:"errorCode"`
	ErrorMessage  string `json:"errorMessage"`
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
