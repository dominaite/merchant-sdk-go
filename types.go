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
	// Address is the billing street address, one line, at most 200
	// characters. The widget asks the payer for address, city and postal
	// code when the payment processor needs them, and skips a field you send.
	Address string `json:"address,omitempty"`
	// City is the billing city, at most 100 characters.
	City string `json:"city,omitempty"`
	// PostalCode is the billing postal code, at most 20 characters.
	PostalCode string `json:"postalCode,omitempty"`
	// State is the billing state or province code, at most 50 characters,
	// e.g. "CA" or "NSW". Only used for the United States, Canada and
	// Australia.
	State string `json:"state,omitempty"`
}

// CreateCheckoutSessionParams are the parameters for
// Client.CreateCheckoutSession. Amount, Currency and OrderReference are
// required. With VerifyOnly, leave Amount 0.
type CreateCheckoutSessionParams struct {
	// Amount is in MINOR units: 2500 is 25.00 EUR. Integers only. It must be
	// positive, except with VerifyOnly, where it must be 0: the platform sets
	// the verification amount.
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
	// The stored method shows up on CheckoutStatus.StoredPaymentMethod after the
	// payment succeeds; a declined first payment stores nothing. The card details
	// themselves never reach you: you get an id, a brand and the last four digits.
	// With VerifyOnly or AgreementID the card is always saved.
	SaveCard bool `json:"saveCard,omitempty"`

	// Integration picks how the payer enters the card: IntegrationWidget (the
	// default when empty) or IntegrationFields, card fields in your own page.
	// Card fields are enabled per merchant on request; asking for them on an
	// account without them is a 400 INVALID_SELECTION *APIError. It is part of
	// the idempotency identity: replaying a key with a different Integration is
	// IDEMPOTENCY_KEY_REUSED. Empty sends no integration field.
	Integration Integration `json:"integration,omitempty"`

	// LocationID is the id of the storefront (online location) this payment
	// belongs to. Empty uses your default storefront, or your only active one
	// when no default is set. An id that is not one of your storefronts is
	// refused with a 404 *APIError. An API key bound to one storefront refuses
	// another id with ErrorCodeStorefrontMismatch.
	LocationID string `json:"locationId,omitempty"`

	// VerifyOnly saves the payer's card without a payment. The checkout places
	// a small authorization to check the card and saves the card. The platform
	// releases the authorization within about 15 minutes, so nobody is
	// charged. Leave Amount 0. SaveCard is implied, Integration must be empty
	// or IntegrationWidget, and AgreementID must be empty.
	//
	// Verify-only sessions are off until we switch them on for your account.
	// Until then the session is refused with a 403 *AuthError, ErrorCode
	// ErrorCodeVerifyOnlyNotEnabled. The status read of a verify-only session
	// has VerifyOnly true, and a non-nil StoredPaymentMethod says the card is
	// verified. It fires verification.* webhook events, never payment.* ones.
	VerifyOnly bool `json:"verifyOnly,omitempty"`

	// AgreementID is the agr_ id of a PENDING agreement (CreateAgreement). It
	// makes this the subscription session: it takes the agreement's first
	// payment, saves the card and activates the agreement. Amount and Currency
	// must be the agreement's own; on a plan with a trial only the currency is
	// held. A refusal arrives as a *RefusalError with one of the
	// SubscriptionSessionRefusalCodes.
	AgreementID string `json:"agreementId,omitempty"`

	// Consent records that the payer accepted the platform's recurring payment
	// terms on your page. Send it with AgreementID only. Consent.TextVersion
	// must be a version of the platform text, e.g.
	// ConsentTextVersionRecurring202609.
	Consent *Consent `json:"consent,omitempty"`

	// IdempotencyKey is required. Derive it from the order with
	// OrderIdempotencyKey: the same order at the same amount must produce the
	// same key, so a reload, a Back button or a retry after a timeout replays
	// the open session instead of opening a second payment. It travels in the
	// header and in the signature, never in the body. 1 to 100 visible ASCII
	// characters; anything else is refused with a *ValidationError before
	// anything is sent.
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
	return mergeExtra(base, p.Extra)
}

// verifyOnly reports whether the body asks for a verify-only session. Extra
// wins over the field, as it does for every key.
func (p CreateCheckoutSessionParams) verifyOnly() bool {
	if value, ok := p.Extra["verifyOnly"]; ok {
		flag, isBool := value.(bool)
		return isBool && flag
	}
	return p.VerifyOnly
}

// mergeExtra merges extra into an encoded JSON object. A key in extra
// replaces the modelled field of the same name.
func mergeExtra(base []byte, extra map[string]any) ([]byte, error) {
	if len(extra) == 0 {
		return base, nil
	}

	merged := map[string]json.RawMessage{}
	if err := json.Unmarshal(base, &merged); err != nil {
		return nil, err
	}
	for key, value := range extra {
		raw, err := marshalNoEscape(value)
		if err != nil {
			return nil, err
		}
		merged[key] = raw
	}

	return marshalNoEscape(merged)
}

// Consent records that your customer accepted recurring payment terms: which
// version of the text, when, and from which IP address. Send all three
// together.
type Consent struct {
	// TextVersion is the version of the consent text the customer accepted,
	// at most 50 characters. On a subscription session it must be a version
	// of the platform text, e.g. ConsentTextVersionRecurring202609.
	TextVersion string `json:"textVersion,omitempty"`
	// AcceptedAt is the ISO 8601 UTC instant the customer accepted it, e.g.
	// "2026-10-01T09:30:00Z". Not in the future.
	AcceptedAt string `json:"acceptedAt,omitempty"`
	// IP is the IPv4 or IPv6 address the customer accepted it from.
	IP string `json:"ip,omitempty"`
}

// ConsentTextVersionRecurring202609 is the version of the platform's recurring
// payment consent text that the subscription session accepts. Show this
// version's text on your page. Older versions stay accepted for agreements that
// were shown them.
const ConsentTextVersionRecurring202609 = "recurring-2026-09"

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
	// Integration echoes what the session was created for: IntegrationWidget or
	// IntegrationFields. For fields, CashierKey and CashierToken are the card
	// fields key and session token.
	Integration Integration `json:"integration"`
	// ClientSecret is set only for IntegrationFields: the browser credential
	// the checkout.js drop-in sends for this session. Opaque, at most 128
	// characters, the same on every replay. Hand it to the payer's page with the
	// other session values; never log it or keep it past the session.
	ClientSecret string `json:"clientSecret,omitempty"`
	// VerifyOnly is true on a verify-only session: Amount is the verification
	// hold, which is released once the card is saved. False on a payment
	// session.
	VerifyOnly bool `json:"verifyOnly,omitempty"`
	// AgreementID is the agr_ id of the agreement this subscription session
	// takes the first payment for. Empty on a one-off checkout.
	AgreementID string `json:"agreementId,omitempty"`

	// RequestID is the API's id of the call that returned this: the
	// Dominaite-Request-Id response header, or metadata.requestId in the body
	// when the header is absent. Quote it to support. Empty when the API sent
	// neither.
	RequestID string `json:"-"`

	// Raw is the unparsed payload, for fields this struct does not model yet.
	Raw json.RawMessage `json:"-"`
}

// Integration is how the payer enters the card on a checkout session.
type Integration string

// Integration values, in the contract's order.
const (
	// IntegrationWidget is the hosted cashier widget. The default.
	IntegrationWidget Integration = "widget"
	// IntegrationFields is card fields rendered in your own page by the
	// checkout.js drop-in. Enabled per merchant on request.
	IntegrationFields Integration = "fields"
)

// Integrations is the complete v1 integration vocabulary.
var Integrations = []Integration{
	IntegrationWidget,
	IntegrationFields,
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

	// RequestID is the API's id of the call that returned this: the
	// Dominaite-Request-Id response header, or metadata.requestId in the body
	// when the header is absent. Quote it to support. Empty when the API sent
	// neither.
	RequestID string `json:"-"`

	// Raw is the unparsed payload, for fields this struct does not model yet.
	Raw json.RawMessage `json:"-"`
}

// IntegrationContract is what GetIntegrationContract returns: the API's own
// description of its routes, fields, codes and vocabularies.
type IntegrationContract struct {
	// Version is the dated version of the contract, e.g. "2026-09-16".
	Version string `json:"version"`
	// Statuses is the transaction status vocabulary, in the gateway's order.
	Statuses []string `json:"statuses"`
	// WebhookEventCatalog is the payment.* webhook event types. The recurring
	// and verification event types are in their own sections of Raw.
	WebhookEventCatalog []string `json:"webhookEventCatalog"`

	// RequestID is the API's id of the call that returned this: the
	// Dominaite-Request-Id response header, or metadata.requestId in the body
	// when the header is absent. Quote it to support. Empty when the API sent
	// neither.
	RequestID string `json:"-"`

	// Raw is the whole contract as the API sent it.
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

// Payment method categories reported in CheckoutStatus.PaymentMethod.
const (
	PaymentMethodCard         = "card"
	PaymentMethodWallet       = "wallet"
	PaymentMethodBankTransfer = "bank_transfer"
	PaymentMethodSepa         = "sepa"
)

// PaymentMethodCategories is every payment method category the merchant API
// reports, in the gateway's own order.
//
// Reporting data, not a money-flow switch: a wallet payment refunds, captures
// and disputes exactly like a card payment.
var PaymentMethodCategories = []string{
	PaymentMethodCard,
	PaymentMethodWallet,
	PaymentMethodBankTransfer,
	PaymentMethodSepa,
}

// Wallets reported in CheckoutStatus.WalletType.
const (
	WalletTypeApplePay   = "apple_pay"
	WalletTypeGooglePay  = "google_pay"
	WalletTypeSamsungPay = "samsung_pay"
)

// WalletTypes is the wallets the gateway names today, pinned against the
// published contract. The field can carry a lower-cased identifier not in this
// list yet: treat an unknown value as a valid wallet, not an error.
var WalletTypes = []string{
	WalletTypeApplePay,
	WalletTypeGooglePay,
	WalletTypeSamsungPay,
}

// IsPaid reports whether a status means the payment is complete: true for
// StatusSucceeded only. StatusRequiresCapture is not paid yet (the funds are
// held, not captured), and neither is anything this SDK does not recognise.
func IsPaid(status string) bool {
	return status == StatusSucceeded
}

// IsTerminal reports whether a status is final, so polling can stop:
// succeeded, failed, cancelled, abandoned, refunded and partially_refunded.
// Everything else is still open and worth polling, including pending,
// processing, requires_capture, disputed and any status this SDK does not
// recognise: a value the API adds later must never close a live order.
func IsTerminal(status string) bool {
	switch status {
	case StatusSucceeded, StatusFailed, StatusCancelled, StatusAbandoned, StatusRefunded, StatusPartiallyRefunded:
		return true
	}
	return false
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
	// PSPReference is the payment processor's reference for the transaction.
	// Empty (null or absent on the wire) until known, and on a sale settled by
	// reconciliation without a processor webhook. Older gateways omit it.
	PSPReference string `json:"pspReference,omitempty"`
	// Status is one of the Status* constants.
	Status string `json:"status"`
	// Amount is in MINOR units.
	Amount         int64  `json:"amount"`
	Currency       string `json:"currency"`
	RefundedAmount int64  `json:"refundedAmount,omitempty"`
	// PaymentMethod is how the payer paid, one of PaymentMethodCategories.
	// Empty (null or absent on the wire) while the payment is still open, with
	// no method chosen yet, and on older transactions. Reporting data only.
	PaymentMethod string `json:"paymentMethod,omitempty"`
	// WalletType is which wallet, when PaymentMethod is PaymentMethodWallet.
	// A value outside WalletTypes is a valid wallet this SDK does not name yet,
	// not an error. Empty (null or absent on the wire) for non-wallet payments.
	WalletType string `json:"walletType,omitempty"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt,omitempty"`
	// ExpiresAt is present while the session is still payable.
	ExpiresAt string `json:"expiresAt,omitempty"`
	// StoredPaymentMethod is the card kept on file by a session created with
	// SaveCard. Present once the payment is approved, and it stays present
	// after a revoke with Status StoredPaymentMethodStatusRevoked; nil (absent
	// on the wire) until then, for sessions without SaveCard, and for declined
	// or abandoned ones. Store StoredPaymentMethod.ID against your customer -
	// it is what Client.ChargePaymentMethod takes.
	//
	// Not to be confused with PaymentMethod, which is the string category of
	// how the payer paid ("card", "wallet", ...).
	StoredPaymentMethod *StoredPaymentMethod `json:"storedPaymentMethod,omitempty"`
	// VerifyOnly is true on a verify-only session: a card check, not a
	// payment. Status stays the raw status (requires_capture, then cancelled
	// once the hold is released). A non-nil StoredPaymentMethod is what says
	// the card is verified. False on every other session.
	VerifyOnly bool `json:"verifyOnly,omitempty"`
	// DeclineClass is one of the DeclineClass* constants on a failed payment.
	// Empty while the payment is open and on every other status. It can be
	// empty on a failed payment too, for example when nobody declined the
	// payment. A value outside DeclineClasses is a class this SDK does not name
	// yet, not an error.
	DeclineClass string `json:"declineClass,omitempty"`
	// IsRecurring is true for a charge on a stored card without the payer
	// present (a recurring charge), false for a checkout payment.
	IsRecurring bool `json:"isRecurring,omitempty"`
	// Agreement is the agreement the session took the first payment for, or
	// the agreement a charge was placed under. nil on a one-off checkout or
	// charge.
	Agreement *CheckoutStatusAgreement `json:"agreement,omitempty"`
	// Sequence is the data.sequence of the latest charge.* event about this
	// transaction: compare it with the events you processed. 0 on a checkout
	// payment, which sends payment.* events only, and before the first
	// charge.* event.
	Sequence int64 `json:"sequence,omitempty"`

	// RequestID is the API's id of the call that returned this: the
	// Dominaite-Request-Id response header, or metadata.requestId in the body
	// when the header is absent. Quote it to support. Empty when the API sent
	// neither.
	RequestID string `json:"-"`

	// Raw is the unparsed payload, for fields this struct does not model yet.
	Raw json.RawMessage `json:"-"`
}

// CheckoutStatusAgreement is the agreement on a status read: enough to know
// whether the subscription started. Read the whole agreement with
// GetAgreement.
type CheckoutStatusAgreement struct {
	// ID is the agr_ id, case-sensitive.
	ID string `json:"id"`
	// Status is one of the AgreementStatus* values. It stays pending after a
	// declined or abandoned subscription session: the subscription did not
	// start.
	Status string `json:"status"`
	// NextChargeAt is ISO 8601 UTC. Empty while pending and once the
	// agreement has ended.
	NextChargeAt string `json:"nextChargeAt,omitempty"`
}

// Stored payment method status values, in the gateway's own order.
//
// Only active methods can be charged. StoredPaymentMethodStatusRevoked is what
// Client.RevokePaymentMethod leaves behind; StoredPaymentMethodStatusExpired
// means the card's expiry date has passed; StoredPaymentMethodStatusRetired
// means the platform stopped the card on its own (RetiredReason says why) and
// it never becomes active again, so ask the customer to save a card again.
const (
	StoredPaymentMethodStatusActive  = "active"
	StoredPaymentMethodStatusRevoked = "revoked"
	StoredPaymentMethodStatusExpired = "expired"
	StoredPaymentMethodStatusRetired = "retired"
)

// StoredPaymentMethodStatuses is the complete v1 stored-payment-method status
// vocabulary. Treat a value outside this list as not chargeable.
var StoredPaymentMethodStatuses = []string{
	StoredPaymentMethodStatusActive,
	StoredPaymentMethodStatusRevoked,
	StoredPaymentMethodStatusExpired,
	StoredPaymentMethodStatusRetired,
}

// Why the platform retired a stored payment method, in the gateway's own order.
//
// RetiredReasonHardDecline: a charge on the card was declined as final.
// RetiredReasonChargeback: a charge on it was disputed.
// RetiredReasonSourceSaleReversed: the payment that saved it was fully
// refunded or disputed.
const (
	RetiredReasonHardDecline        = "hard_decline"
	RetiredReasonChargeback         = "chargeback"
	RetiredReasonSourceSaleReversed = "source_sale_reversed"
)

// StoredPaymentMethodRetiredReasons is the complete v1 retired-reason
// vocabulary. Treat a value outside this list as retired for an unknown reason.
var StoredPaymentMethodRetiredReasons = []string{
	RetiredReasonHardDecline,
	RetiredReasonChargeback,
	RetiredReasonSourceSaleReversed,
}

// StoredPaymentMethod is a card kept on file. Never the card number, never the
// PSP token - only what you may show a customer. Brand, Last4 and the expiry
// are zero when the provider did not report them (the gateway omits null
// fields on the wire).
type StoredPaymentMethod struct {
	// ID is opaque: pm_ followed by 32 hex characters, case-sensitive. The
	// handle you charge and revoke with.
	ID string `json:"id"`
	// Brand is the card brand as the gateway reports it, e.g. "visa", "mastercard".
	Brand string `json:"brand,omitempty"`
	// Last4 is the last four digits of the card number, for display only.
	Last4 string `json:"last4,omitempty"`
	// ExpiryMonth is 1 to 12.
	ExpiryMonth int `json:"expiryMonth,omitempty"`
	// ExpiryYear is four digits, e.g. 2029.
	ExpiryYear int `json:"expiryYear,omitempty"`
	// Status is one of the StoredPaymentMethodStatus* constants. Treat any value
	// you do not recognise as not chargeable.
	Status string `json:"status"`
	// RetiredReason is one of the RetiredReason* constants when Status is
	// StoredPaymentMethodStatusRetired, and kept if the card is revoked
	// afterwards. Empty on every other card (the gateway sends null or omits it).
	RetiredReason string `json:"retiredReason,omitempty"`
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

	// AgreementID is the agr_ id of the agreement this charge is made under.
	// Empty charges the card on your own authority, as with any card on file:
	// only the per-charge cap and the volume limits apply. When set, the
	// charge must be on the agreement's terms: its payment method, its
	// consent, its currency and its amount rule. The gateway refuses anything
	// else with a 422 *ChargeError, or a 404 *APIError (AGREEMENT_NOT_FOUND)
	// for an agreement that is not yours. The charge records the agreement,
	// so a dispute can be answered with the consent the customer gave.
	AgreementID string `json:"agreementId,omitempty"`

	// IdempotencyKey is required. Derive it from what you are charging for
	// (the order, the billing period), never per attempt: retrying with the
	// same key never charges the card twice, so on a timeout retry with the
	// same key. It travels in the header and in the signature, never in the
	// body. 1 to 100 visible ASCII characters; anything else is refused with a
	// *ValidationError before anything is sent.
	IdempotencyKey string `json:"-"`

	// Extra carries any additional field the API accepts that this struct does
	// not model yet. Keys here are merged into the JSON body and replace a
	// modelled field of the same name.
	Extra map[string]any `json:"-"`
}

// MarshalJSON emits the request body: the modelled fields in declaration order,
// with Extra merged in. IdempotencyKey is excluded; it is a header, not a body
// field.
func (p ChargePaymentMethodParams) MarshalJSON() ([]byte, error) {
	// The local type drops this method, so json.Marshal does not recurse.
	type params ChargePaymentMethodParams

	base, err := marshalNoEscape(params(p))
	if err != nil {
		return nil, err
	}
	return mergeExtra(base, p.Extra)
}

// Charge status values returned by ChargePaymentMethod, in the gateway's own
// order. ChargeStatusSucceeded: the money moved. ChargeStatusFailed: it did
// not; DeclineClass says why, when the class is known. ChargeStatusPending is
// not terminal:
// keep polling GetStatus on the charge's TransactionID. ChargeStatusCancelled:
// an authorization voided before capture, no money moved.
const (
	ChargeStatusSucceeded = "succeeded"
	ChargeStatusFailed    = "failed"
	ChargeStatusPending   = "pending"
	ChargeStatusCancelled = "cancelled"
)

// ChargeStatuses is the complete v1 charge status vocabulary. Treat a value
// outside this list as still open.
var ChargeStatuses = []string{
	ChargeStatusSucceeded,
	ChargeStatusFailed,
	ChargeStatusPending,
	ChargeStatusCancelled,
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

// PaymentMethodCharge is what ChargePaymentMethod returns, for a placed charge
// (HTTP 201) and for a provider decline (HTTP 402, Status ChargeStatusFailed)
// alike. Also carried on a *ChargeError when the gateway attached the charge
// row to its answer.
type PaymentMethodCharge struct {
	// ChargeID is ch_ followed by 32 hex characters. Store it against the
	// order; it is what support asks for.
	ChargeID string `json:"chargeId"`
	// Status is one of the ChargeStatus* constants. Treat anything you do not
	// recognise as still open.
	Status string `json:"status"`
	// DeclineClass is one of the DeclineClass* constants on a failed charge. It
	// can be empty, for example when nobody declined the payment. An empty
	// class is not hard: if you retry, retry as for DeclineClassSoftOther. The
	// status read (GetStatus on TransactionID) carries the latest class. Empty
	// on every status other than failed.
	DeclineClass string `json:"declineClass,omitempty"`
	// DeclineCode is the issuer's decline code, verbatim, for your logs;
	// branch on DeclineClass instead. Empty unless the charge failed.
	DeclineCode string `json:"declineCode,omitempty"`
	// TransactionID is the transaction the charge created; readable with
	// GetStatus.
	TransactionID string `json:"transactionId"`
	// AgreementID is the agr_ id the charge was placed under, when the request
	// named one. Empty otherwise.
	AgreementID string `json:"agreementId,omitempty"`
	// Sequence is the charge's outcome counter as of this answer: on a charge
	// decided at once, the data.sequence of its charge.succeeded or
	// charge.failed event. 0 while pending. A replay returns the first answer
	// unchanged, so it can be lower than a later event's; GetStatus carries
	// the live value.
	Sequence int64 `json:"sequence,omitempty"`

	// RequestID is the API's id of the call that returned this: the
	// Dominaite-Request-Id response header, or metadata.requestId in the body
	// when the header is absent. Quote it to support. Empty when the API sent
	// neither.
	RequestID string `json:"-"`

	// Raw is the unparsed charge object, for fields this struct does not model yet.
	Raw json.RawMessage `json:"-"`
}

// CreateRefundParams are the parameters for Client.CreateRefund. Only
// IdempotencyKey is required.
type CreateRefundParams struct {
	// Amount is what to refund, in MINOR units of the payment's currency (2500
	// is 25.00 EUR, 1500 is 1,500 HUF), at least 1. nil refunds everything still
	// refundable: no amount is sent at all. A pointer, so a partial refund that
	// computed to zero is refused as a *ValidationError instead of quietly
	// turning into a full refund. ToMinorUnits converts a decimal amount.
	Amount *int64 `json:"amount,omitempty"`
	// Reason is free text stored with the refund, at most 500 characters.
	Reason string `json:"reason,omitempty"`

	// IdempotencyKey is required. Derive it from YOUR refund (the return or
	// credit-note id), never per attempt: the same key always names the same
	// refund and never refunds twice, so on a timeout retry with the same key.
	// It travels in the header and in the signature, never in the body. 1 to
	// 100 visible ASCII characters; anything else is refused with a
	// *ValidationError before anything is sent.
	IdempotencyKey string `json:"-"`

	// Extra carries any additional field the API accepts that this struct does
	// not model yet. Keys here are merged into the JSON body and replace a
	// modelled field of the same name.
	Extra map[string]any `json:"-"`
}

// MarshalJSON emits the request body: the modelled fields, with Extra merged
// in. A nil Amount is left out. IdempotencyKey is excluded; it is a header,
// not a body field.
func (p CreateRefundParams) MarshalJSON() ([]byte, error) {
	// The local type drops this method, so json.Marshal does not recurse.
	type params CreateRefundParams

	base, err := marshalNoEscape(params(p))
	if err != nil {
		return nil, err
	}
	return mergeExtra(base, p.Extra)
}

// Refund status values, in the gateway's own order. RefundStatusPending: queued.
// RefundStatusProcessing: with the payment provider now. RefundStatusSucceeded:
// the money was returned to the payer. RefundStatusFailed: the refund did not
// happen, read FailureCode; final for that idempotency key, so a new attempt
// needs a new key.
const (
	RefundStatusPending    = "pending"
	RefundStatusProcessing = "processing"
	RefundStatusSucceeded  = "succeeded"
	RefundStatusFailed     = "failed"
)

// RefundStatuses is the complete v1 refund status vocabulary. Treat a value
// outside this list as still open.
var RefundStatuses = []string{
	RefundStatusPending,
	RefundStatusProcessing,
	RefundStatusSucceeded,
	RefundStatusFailed,
}

// IsRefundTerminal reports whether a refund status is final, so polling can
// stop: succeeded and failed. pending, processing and any status this SDK does
// not recognise are still open.
func IsRefundTerminal(status string) bool {
	return status == RefundStatusSucceeded || status == RefundStatusFailed
}

// Refund is what CreateRefund and GetRefund return. The gateway omits null
// fields on the wire; they read as nil or empty here.
type Refund struct {
	// RefundID is re_ followed by 32 hex characters. The same idempotency key
	// on the same payment always names the same refund.
	RefundID string `json:"refundId"`
	// TransactionID is the payment being refunded.
	TransactionID string `json:"transactionId"`
	// Status is one of the RefundStatus* constants.
	Status string `json:"status"`
	// Amount is in MINOR units. On pending it is the amount requested, nil for a
	// full refund; on processing it is the amount being refunded, nil until a
	// full refund has been sized; on succeeded it is the amount actually
	// refunded; on failed it is always nil.
	Amount *int64 `json:"amount,omitempty"`
	// Currency is the ISO 4217 code of the payment. A refund is always in the
	// payment's currency.
	Currency string `json:"currency"`
	// FailureCode is set on a failed refund only: one of the RefundFailure*
	// constants. Treat a value you do not recognise as RefundFailureFailed.
	FailureCode string `json:"failureCode,omitempty"`
	// FailureMessage is a fixed English explanation of FailureCode, for your
	// logs. Empty unless the refund failed.
	FailureMessage string `json:"failureMessage,omitempty"`
	// CompletedAt is the ISO 8601 UTC instant the refund reached succeeded or
	// failed. Empty before that.
	CompletedAt string `json:"completedAt,omitempty"`

	// RequestID is the API's id of the call that returned this: the
	// Dominaite-Request-Id response header, or metadata.requestId in the body
	// when the header is absent. Quote it to support. Empty when the API sent
	// neither.
	RequestID string `json:"-"`

	// Raw is the unparsed refund object, for fields this struct does not model yet.
	Raw json.RawMessage `json:"-"`
}

// RefundList is what ListRefunds returns: the refunds of one payment created
// through this API, and the amounts that decide the next refund. Every amount
// is in MINOR units of Currency.
type RefundList struct {
	// TransactionID is the payment whose refunds these are.
	TransactionID string `json:"transactionId"`
	// Currency is the ISO 4217 code of the payment and of every amount here.
	Currency string `json:"currency"`
	// RefundedAmount is what was refunded so far, from every source: this
	// API, the dashboard and the provider.
	RefundedAmount int64 `json:"refundedAmount"`
	// RefundableAmount is what a new refund could take right now: what is not
	// refunded yet, minus refunds still in progress. 0 when the payment
	// cannot be refunded.
	RefundableAmount int64 `json:"refundableAmount"`
	// Refunds are the refunds created through this API: pending first, then
	// processing, then succeeded and failed, newest first within each group.
	// Empty when there are none.
	Refunds []Refund `json:"refunds"`

	// RequestID is the API's id of the call that returned this: the
	// Dominaite-Request-Id response header, or metadata.requestId in the body
	// when the header is absent. Quote it to support. Empty when the API sent
	// neither.
	RequestID string `json:"-"`

	// Raw is the unparsed refund list, for fields this struct does not model yet.
	Raw json.RawMessage `json:"-"`
}

// Webhook event types of payments: the contract's webhookEventCatalog. With
// the agreement, charge and verification types below, they are every type
// that endpoint registration accepts. It refuses any other type, and the match
// is case-sensitive. The list can grow: treat a type you do not know as a
// no-op.
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

// Webhook event types of agreements and saved-card charges. They carry
// Data.Sequence: order them by it, never by CreatedAt.
//
// EventAgreementPastDue: the platform gave up on the current period on the
// agreement's card. Nothing cancels the agreement automatically.
// EventChargeFailed: nothing more is tried for this charge. For a one-off
// charge it can arrive again for the same chargeId with a new decline class;
// keep the latest. EventChargeRetrying: an agreement period was declined
// softly, and the platform tries the same card again.
const (
	EventAgreementActivated = "agreement.activated"
	EventAgreementPastDue   = "agreement.past_due"
	EventAgreementCancelled = "agreement.cancelled"
	EventChargeSucceeded    = "charge.succeeded"
	EventChargeFailed       = "charge.failed"
	EventChargeRetrying     = "charge.retrying"
)

// Webhook event types of verify-only sessions. A verify-only session never
// fires payment.* events. EventVerificationFailed fires when the issuer
// declined or the payer abandoned the checkout; no card is saved.
const (
	EventVerificationSucceeded = "verification.succeeded"
	EventVerificationFailed    = "verification.failed"
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
	// APIVersion is the dated version of the payload shape the event was
	// rendered in, e.g. "2026-09-25". Fields are only ever added under a
	// version, never renamed or removed. A retry resends the bytes of the first
	// attempt, so a redelivered event keeps its original APIVersion. Empty on
	// deliveries from a gateway that predates the field.
	APIVersion string `json:"apiVersion"`
	// CreatedAt is the ISO 8601 UTC instant of the transition, not of delivery.
	// On a retry it still carries the original transition time. It can repeat
	// across events, so never order agreement.* or charge.* events by it; use
	// Data.Sequence.
	CreatedAt string      `json:"createdAt"`
	Data      WebhookData `json:"data"`

	// Raw is the exact verified payload, byte for byte as signed. Use it for
	// fields this struct does not model yet, or to store the delivery verbatim.
	Raw json.RawMessage `json:"-"`
}

// WebhookData is the payload of a WebhookEvent.
type WebhookData struct {
	TransactionID string `json:"transactionId"`
	// OrderReference is your own order reference as stored on the
	// transaction: what you sent on create session, unless you replaced it
	// later. Match deliveries to your orders on it. Refund and cancel events
	// carry the original payment's. Nullable on the wire; empty string when
	// absent, which includes payments not started through the API.
	OrderReference string `json:"orderReference"`
	// OrderID is the hosted checkout order id, the same value as
	// CheckoutStatus.OrderID. Nullable on the wire; empty string on refund and
	// cancel events and for payments taken outside hosted checkout.
	OrderID string `json:"orderId"`
	// Description is the description you sent on create session. Nullable on
	// the wire; empty string when none was given and on refund and cancel
	// events.
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

	// PSPReference is the payment processor's reference for the transaction,
	// on payment.* events. Empty (null or absent on the wire) until known, and
	// on a sale settled by reconciliation without a processor webhook. Refund
	// and cancel events carry the original sale's reference. charge.* events do
	// not have it. Older gateways omit it.
	PSPReference string `json:"pspReference"`

	// OriginalTransactionID is the parent transaction for refunds and
	// reversals. Empty on an original payment.
	OriginalTransactionID string `json:"originalTransactionId"`
	// IdempotencyKey is your own mint key when the gateway knows it - the
	// cheapest way to match a delivery back to your order without a lookup.
	// Empty when unknown, which today includes every refund.
	IdempotencyKey string `json:"idempotencyKey"`

	// PaymentMethod is how the payer paid, one of PaymentMethodCategories, on
	// payment.* and verification.* events: the same value as
	// CheckoutStatus.PaymentMethod. Nullable on the wire; empty string while
	// no method is known. On charge.* events the wire field is an object
	// instead: read it from Charge.PaymentMethod.
	PaymentMethod string `json:"paymentMethod"`
	// WalletType is which wallet, when PaymentMethod is PaymentMethodWallet. A
	// value outside WalletTypes is a valid wallet this SDK does not name yet.
	// Nullable on the wire; empty string for non-wallet payments.
	WalletType string `json:"walletType"`

	// PaymentMethodBrand is the lower-cased card brand (visa, mastercard, ...)
	// once a card payment has been attempted. Nullable on the wire; empty
	// string while the payment is open, for non-card methods, and when the
	// provider did not report a brand.
	PaymentMethodBrand string `json:"paymentMethodBrand"`
	// PaymentMethodLast4 is the card's last four digits once a card payment
	// has been attempted. Empty under the same conditions as
	// PaymentMethodBrand.
	PaymentMethodLast4 string `json:"paymentMethodLast4"`

	// StoredPaymentMethod is the card a SaveCard session stored, on payment.*
	// events: the same object as CheckoutStatus.StoredPaymentMethod, so its ID
	// is what ChargePaymentMethod takes. Set on payment.succeeded (and
	// payment.requires_capture for an authorization) when the card was stored
	// together with the approval.
	//
	// nil (null or absent on the wire) when no card was saved, and on every
	// other event. It can ALSO be nil when a card was saved: on server-to-server
	// sales that succeeded synchronously and on sales settled by the
	// background sweep, the card is stored after the approval was announced.
	// GetStatus is the source of truth: on a SaveCard session whose event has
	// StoredPaymentMethod nil, read the status to pick the card up.
	//
	// charge.* events carry the id instead, on Charge.StoredPaymentMethodID.
	StoredPaymentMethod *StoredPaymentMethod `json:"storedPaymentMethod,omitempty"`

	// VerifyOnly is true on every event of a verify-only session. False (or
	// absent on the wire) on every other event.
	VerifyOnly bool `json:"verifyOnly"`
	// DeclineClass is one of the DeclineClass* constants on payment.failed,
	// verification.failed and the charge.failed and charge.retrying events.
	// It can be empty, for example when nobody declined the payment. Empty
	// (null or absent on the wire) on every other event. A value outside
	// DeclineClasses is a class this SDK does not name yet, not an error.
	DeclineClass string `json:"declineClass"`

	// Sequence orders agreement.* and charge.* events for one object: it counts
	// the announced changes of that object, only ever rises, and a redelivery
	// carries the same number. The object is the agreement (Agreement.ID) for
	// agreement.*, the agreement period (Charge.AgreementID plus
	// Charge.PeriodNumber) for a charge the platform placed for an agreement,
	// and Charge.ChargeID for a one-off charge. Keep the highest Sequence you
	// have processed per object and discard any event whose Sequence is not
	// higher. Zero when absent (payment.* events do not carry it) and on
	// events recorded before the counter existed; treat zero as older than any
	// positive number.
	Sequence int64 `json:"sequence"`

	// Charge is the typed data of a charge.* event, nil on every other event.
	// The fields above that a charge event shares (TransactionID, Amount,
	// Currency, OrderReference, Description, DeclineClass, Sequence) are set
	// too; Status is empty, because a charge event says Charge.Outcome.
	Charge *ChargeWebhookData `json:"-"`
	// Agreement is the typed data of an agreement.* event, nil on every other
	// event. Status, PreviousStatus, Amount, Currency and Sequence above are
	// set too, with the agreement's values.
	Agreement *AgreementWebhookData `json:"-"`

	// Raw is the unparsed data object, for fields not modelled above.
	Raw json.RawMessage `json:"-"`
}

// UnmarshalJSON reads the data object of any event type. The paymentMethod
// field is a string on payment.* and verification.* events and an object on
// charge.* events; only the string lands on PaymentMethod.
func (d *WebhookData) UnmarshalJSON(raw []byte) error {
	// The local type drops this method, so json.Unmarshal does not recurse.
	type data WebhookData
	var wire struct {
		data
		PaymentMethod json.RawMessage `json:"paymentMethod"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return err
	}
	*d = WebhookData(wire.data)
	var category *string
	if len(wire.PaymentMethod) > 0 && wire.PaymentMethod[0] == '"' {
		if err := json.Unmarshal(wire.PaymentMethod, &category); err != nil {
			return err
		}
		d.PaymentMethod = *category
	}
	d.Raw = append(json.RawMessage(nil), raw...)
	return nil
}

// Outcomes of a charge on charge.* events, on ChargeWebhookData.Outcome, in
// the gateway's own order. ChargeOutcomeRetrying: an agreement period was
// declined softly and the platform tries the same card again, at
// NextAttemptAt.
const (
	ChargeOutcomeSucceeded = "succeeded"
	ChargeOutcomeRetrying  = "retrying"
	ChargeOutcomeFailed    = "failed"
)

// ChargeOutcomes is the charge outcome vocabulary, in the gateway's order.
// Treat a value you do not know as not final.
var ChargeOutcomes = []string{
	ChargeOutcomeSucceeded,
	ChargeOutcomeRetrying,
	ChargeOutcomeFailed,
}

// ChargeWebhookData is the data of a charge.* event: a charge on a stored
// card, one-off (ChargePaymentMethod) or placed by the platform for an
// agreement period. Webhooks send every field, null when empty: a null string
// reads as "", a null number as nil.
type ChargeWebhookData struct {
	// ChargeID is ch_ followed by 32 hex characters: the same id
	// ChargePaymentMethod returned for a one-off charge.
	ChargeID string `json:"chargeId"`
	// TransactionID is the transaction the charge created; readable with
	// GetStatus.
	TransactionID string `json:"transactionId"`
	// StoredPaymentMethodID is the pm_ id of the card that was charged.
	StoredPaymentMethodID string `json:"storedPaymentMethodId"`
	// AgreementID is the agr_ id the charge belongs to. Empty for a one-off
	// charge without an agreement.
	AgreementID string `json:"agreementId"`
	// CustomerReference is the agreement's customer reference. Empty without
	// an agreement.
	CustomerReference string `json:"customerReference"`
	// Outcome is one of the ChargeOutcome* values.
	Outcome string `json:"outcome"`
	// PeriodNumber is the agreement period the charge pays, from 1. nil for a
	// one-off charge.
	PeriodNumber *int `json:"periodNumber"`
	// AttemptNumber counts the attempts on this period, from 1. nil when the
	// gateway did not count one.
	AttemptNumber *int `json:"attemptNumber"`
	// Amount is in MINOR units of Currency.
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	// PaymentMethod is the brand and last four digits of the card. nil when
	// the provider reported neither.
	PaymentMethod *ChargeWebhookPaymentMethod `json:"paymentMethod"`
	// OrderReference and Description are what the charge was sent with.
	OrderReference string `json:"orderReference"`
	Description    string `json:"description"`
	// DeclineClass is one of the DeclineClass* constants on charge.failed and
	// charge.retrying, empty otherwise. DeclineCode is the issuer's code,
	// verbatim, for your logs.
	DeclineClass string `json:"declineClass"`
	DeclineCode  string `json:"declineCode"`
	// NextAttemptAt is when the platform tries the card again, on
	// charge.retrying. NextChargeAt is the agreement's next charge, when
	// there is one. Both are as the API sent them, empty when null.
	NextAttemptAt string `json:"nextAttemptAt"`
	NextChargeAt  string `json:"nextChargeAt"`
	// Sequence is the same value as WebhookData.Sequence.
	Sequence int64 `json:"sequence"`

	// Raw is the unparsed data object, for fields not modelled above.
	Raw json.RawMessage `json:"-"`
}

// ChargeWebhookPaymentMethod is the card on a charge.* event.
type ChargeWebhookPaymentMethod struct {
	// Brand is the lower-cased card brand (visa, mastercard, ...). Empty when
	// the provider did not report one.
	Brand string `json:"brand"`
	// Last4 is the card's last four digits. Empty when unknown.
	Last4 string `json:"last4"`
}

// AgreementWebhookData is the data of an agreement.* event: the agreement
// after the change. The fields are the ones GetAgreement returns, plus
// PreviousStatus. Webhooks send every field, null when empty: a null string
// reads as "", a null number as nil.
type AgreementWebhookData struct {
	// ID is the agr_ id: the ordering key of agreement.* events.
	ID                string `json:"id"`
	PlanID            string `json:"planId"`
	CustomerReference string `json:"customerReference"`
	// StoredPaymentMethodID is the pm_ id of the card on file, empty until one
	// is saved.
	StoredPaymentMethodID string `json:"storedPaymentMethodId"`
	// Status is one of the AgreementStatus* values; PreviousStatus is the
	// status it moved from.
	Status         string `json:"status"`
	PreviousStatus string `json:"previousStatus"`
	// Amount is the charge per period, in MINOR units of Currency.
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	// IntervalUnit is one of the IntervalUnit* values.
	IntervalUnit  string `json:"intervalUnit"`
	IntervalCount int    `json:"intervalCount"`
	// PeriodCount is nil when the agreement bills until it is cancelled.
	PeriodCount *int `json:"periodCount"`
	TrialDays   int  `json:"trialDays"`
	// NextChargeAt, ActivatedAt and CancelledAt are as the API sent them,
	// empty when null.
	NextChargeAt string `json:"nextChargeAt"`
	ActivatedAt  string `json:"activatedAt"`
	CancelledAt  string `json:"cancelledAt"`
	// Version starts at 1 and rises when the price changes for a future
	// period.
	Version int `json:"version"`
	// Sequence is the same value as WebhookData.Sequence.
	Sequence int64 `json:"sequence"`

	// Raw is the unparsed data object, for fields not modelled above.
	Raw json.RawMessage `json:"-"`
}
