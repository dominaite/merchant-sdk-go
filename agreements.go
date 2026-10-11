package dominaite

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// Agreement status values, in the gateway's own order. pending: created, not
// paid yet. active: with scheduled charges on, the platform charges the card
// each period. past_due: the platform gave up on the current period on this
// card (PastDueReason says why); it stays past_due until the period is paid
// on another card, or until you cancel it. cancelled, completed and failed
// are final.
const (
	AgreementStatusPending   = "pending"
	AgreementStatusActive    = "active"
	AgreementStatusPastDue   = "past_due"
	AgreementStatusCancelled = "cancelled"
	AgreementStatusCompleted = "completed"
	AgreementStatusFailed    = "failed"
)

// AgreementStatuses is the agreement status vocabulary, in the gateway's order.
var AgreementStatuses = []string{
	AgreementStatusPending,
	AgreementStatusActive,
	AgreementStatusPastDue,
	AgreementStatusCancelled,
	AgreementStatusCompleted,
	AgreementStatusFailed,
}

// Why an agreement is past_due, on Agreement.PastDueReason, in the gateway's
// own order. PastDueReasonSCARequired: the issuer asked for customer
// authentication, which no unattended retry can pass.
const (
	PastDueReasonCardExpired      = "card_expired"
	PastDueReasonCardRevoked      = "card_revoked"
	PastDueReasonHardDecline      = "hard_decline"
	PastDueReasonDunningExhausted = "dunning_exhausted"
	PastDueReasonSCARequired      = "sca_required"
	PastDueReasonUnprocessable    = "unprocessable"
)

// PastDueReasons is the past-due reason vocabulary, in the gateway's order.
var PastDueReasons = []string{
	PastDueReasonCardExpired,
	PastDueReasonCardRevoked,
	PastDueReasonHardDecline,
	PastDueReasonDunningExhausted,
	PastDueReasonSCARequired,
	PastDueReasonUnprocessable,
}

// CreateAgreementParams is what CreateAgreement sends.
type CreateAgreementParams struct {
	// PlanID is the plan_ id of one of your active plans.
	PlanID string `json:"planId"`
	// CustomerReference is your own id for the customer, 1 to 100
	// characters, stable per customer.
	CustomerReference string `json:"customerReference"`
	// StoredPaymentMethodID is the pm_ id of an active card this customer
	// already saved, to bill it instead of saving a new card in the
	// subscription session. The card must have been saved from a payment in
	// the plan's currency. Consent is required with it.
	StoredPaymentMethodID string `json:"storedPaymentMethodId,omitempty"`
	// Consent is the customer's acceptance of your recurring terms: your own
	// label for the wording as TextVersion, when, and from which IP address.
	// It is required with StoredPaymentMethodID or MaxAmountPerPeriod.
	// Without them, leave it nil and let the subscription session capture it.
	Consent *Consent `json:"consent,omitempty"`
	// MaxAmountPerPeriod is for a variable-amount subscription only: the most
	// one charge may be, in MINOR units, at least the plan's amount. Consent
	// is required with it. 0 sends nothing: a fixed agreement, where every
	// charge is exactly the plan's amount.
	MaxAmountPerPeriod int64 `json:"maxAmountPerPeriod,omitempty"`

	// IdempotencyKey is required. It is sent as the Idempotency-Key header and
	// signed. Derive it from your own subscription record, never per attempt.
	IdempotencyKey string `json:"-"`
	// Extra holds request fields this struct does not model yet. They are
	// sent as given, and a key in Extra replaces the modelled field of the
	// same name.
	Extra map[string]any `json:"-"`
}

// MarshalJSON writes the modelled fields, then the Extra fields.
func (p CreateAgreementParams) MarshalJSON() ([]byte, error) {
	// The local type drops this method, so json.Marshal does not recurse.
	type params CreateAgreementParams

	base, err := marshalNoEscape(params(p))
	if err != nil {
		return nil, err
	}
	return mergeExtra(base, p.Extra)
}

// AgreementConsent is the consent recorded on an agreement. TextVersion,
// AcceptedAt and IP are stored exactly as they were sent, on CreateAgreement
// or on the subscription session. ReceivedAt and ReceivedFromIP are the
// platform's own receipt: when the consent reached the platform, and from
// which address.
type AgreementConsent struct {
	TextVersion    string `json:"textVersion"`
	AcceptedAt     string `json:"acceptedAt"`
	IP             string `json:"ip"`
	ReceivedAt     string `json:"receivedAt,omitempty"`
	ReceivedFromIP string `json:"receivedFromIp,omitempty"`
}

// Agreement is one customer's subscription to a plan. The price and the
// interval are copied from the plan when the agreement is created; a later
// plan change never re-prices a running agreement.
type Agreement struct {
	// ID is agr_ followed by 32 hex characters. It is case-sensitive: store it
	// and send it back exactly as it is.
	ID                string `json:"id"`
	PlanID            string `json:"planId"`
	CustomerReference string `json:"customerReference"`
	// StoredPaymentMethodID is the pm_ id of the card on file, empty until one
	// is saved.
	StoredPaymentMethodID string `json:"storedPaymentMethodId,omitempty"`
	// Status is one of the AgreementStatus* values.
	Status string `json:"status"`
	// Amount is the charge per period, in MINOR units of Currency.
	Amount int64 `json:"amount"`
	// MaxAmountPerPeriod is the most one charge may be on a variable
	// agreement, in MINOR units. nil on a fixed agreement.
	MaxAmountPerPeriod *int64 `json:"maxAmountPerPeriod,omitempty"`
	Currency           string `json:"currency"`
	// IntervalUnit is one of the IntervalUnit* values.
	IntervalUnit  string `json:"intervalUnit"`
	IntervalCount int    `json:"intervalCount"`
	// PeriodCount is nil when the agreement bills until it is cancelled.
	PeriodCount *int `json:"periodCount,omitempty"`
	TrialDays   int  `json:"trialDays"`
	// NextChargeAt is ISO 8601 UTC, empty while pending and once the
	// agreement has ended.
	NextChargeAt string `json:"nextChargeAt,omitempty"`
	// PastDueReason is one of the PastDueReason* values while the agreement
	// is past_due, empty otherwise.
	PastDueReason string `json:"pastDueReason,omitempty"`
	// PastDueAt is ISO 8601 UTC, when the agreement went past_due.
	PastDueAt string `json:"pastDueAt,omitempty"`
	// Consent is nil until consent is captured.
	Consent *AgreementConsent `json:"consent,omitempty"`
	// Version starts at 1 and rises when the price changes for a future
	// period.
	Version int `json:"version"`
	// Sequence is the agreement's transition counter as of this read: the
	// Data.Sequence of the latest agreement.* event, 0 before the first
	// transition. Compare it with the highest sequence you processed for the
	// agreement.
	Sequence int64 `json:"sequence"`
	// CreatedAt is ISO 8601 UTC.
	CreatedAt string `json:"createdAt"`
	// ActivatedAt is ISO 8601 UTC, empty while pending.
	ActivatedAt string `json:"activatedAt,omitempty"`
	// CancelledAt is ISO 8601 UTC, empty unless cancelled.
	CancelledAt string `json:"cancelledAt,omitempty"`

	// Raw is the unparsed agreement object, for fields this struct does not
	// model yet.
	Raw json.RawMessage `json:"-"`
}

// CreateAgreement creates a pending agreement that subscribes one customer to
// one of your active plans. Nothing is charged. To take the first payment,
// save the card and activate the agreement, open a subscription session:
// CreateCheckoutSession with AgreementID.
//
// params.IdempotencyKey is required and signed. The same key with the same
// body answers the agreement it created, with HTTP 200 instead of 201. The
// same key with a different body is refused with IDEMPOTENCY_KEY_REUSED.
//
// Errors it returns:
//   - *ValidationError: a missing plan id or customer reference, or a bad
//     IdempotencyKey; nothing was sent.
//   - *APIError: a 4xx with ErrorCode, e.g. 404 PLAN_NOT_FOUND, 409
//     PLAN_NOT_ACTIVE, 422 AGREEMENT_CONSENT_REQUIRED (see the codes next to
//     ErrorCodePlanNotFound). Nothing was created.
//   - *AuthError, *RateLimitError: as everywhere else.
//   - *TransportError: network failure or 5xx. Nothing was created on a 503;
//     retry WITH the same key.
func (c *Client) CreateAgreement(ctx context.Context, params CreateAgreementParams) (*Agreement, error) {
	if strings.TrimSpace(params.PlanID) == "" {
		return nil, newValidationError("Missing required parameter: planId")
	}
	if strings.TrimSpace(params.CustomerReference) == "" {
		return nil, newValidationError("Missing required parameter: customerReference")
	}
	idempotencyKey, err := normalizeIdempotencyKey(params.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	body, err := marshalNoEscape(params)
	if err != nil {
		return nil, newValidationError("Request parameters are not JSON-encodable: " + err.Error())
	}

	reply, err := c.send(ctx, http.MethodPost, AgreementsPath, string(body), idempotencyKey)
	if err != nil {
		return nil, err
	}
	return reply.agreement()
}

// GetAgreement reads one of your agreements.
//
// Errors it returns: an *APIError with HTTPStatus 404 and ErrorCode
// AGREEMENT_NOT_FOUND for an id that is not yours, or 400 INVALID_FORMAT for
// an id that is not an agreement id; *ValidationError for an id that cannot
// be a path segment; the generic errors otherwise.
func (c *Client) GetAgreement(ctx context.Context, agreementID string) (*Agreement, error) {
	id, err := normalizeRecurringID(agreementID, "agreementId must be the agreement id CreateAgreement returned")
	if err != nil {
		return nil, err
	}

	// GET signs an EMPTY idempotency key and an EMPTY body.
	reply, err := c.send(ctx, http.MethodGet, AgreementsPath+"/"+id, "", "")
	if err != nil {
		return nil, err
	}
	return reply.agreement()
}

// CancelAgreement cancels an agreement from pending, active or past_due: no
// further charge is scheduled. A charge already placed is not reversed.
// Cancelling a cancelled agreement succeeds again, so a retry after a timeout
// is safe.
//
// Errors it returns: as GetAgreement, and an *APIError with HTTPStatus 409
// and ErrorCode AGREEMENT_ENDED when the agreement already completed or
// failed.
func (c *Client) CancelAgreement(ctx context.Context, agreementID string) error {
	id, err := normalizeRecurringID(agreementID, "agreementId must be the agreement id CreateAgreement returned")
	if err != nil {
		return err
	}

	// DELETE signs an EMPTY idempotency key and an EMPTY body, like GET.
	reply, err := c.send(ctx, http.MethodDelete, AgreementsPath+"/"+id, "", "")
	if err != nil {
		return err
	}
	if reply.status >= 400 {
		return reply.rejection()
	}
	return nil
}

// agreement reads an agreement route's reply: the agreement on a 2xx, the
// generic errors otherwise (a coded 4xx is an *APIError, a 5xx a
// *TransportError).
func (r *response) agreement() (*Agreement, error) {
	if r.status >= 400 {
		return nil, r.rejection()
	}
	agreement := &Agreement{Raw: r.data}
	if !r.parsed || !isJSONObject(r.data) || json.Unmarshal(r.data, agreement) != nil || agreement.ID == "" {
		return nil, newAPIError(r.status, "The API returned an unexpected agreement object")
	}
	return agreement, nil
}
