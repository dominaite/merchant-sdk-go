package dominaite

import (
	"encoding/json"
	"errors"
)

// ErrDominaite matches every error this SDK returns, for a single errors.Is
// catch-all:
//
//	if errors.Is(err, dominaite.ErrDominaite) { ... }
//
// For the specific kind, use errors.As with *RefusalError, *ChargeError,
// *RevokeError, *RefundError, *AuthError, *RateLimitError, *APIError,
// *TransportError or *ValidationError, or errors.As with the Error interface to
// catch any of them while keeping the message.
var ErrDominaite = errors.New("dominaite")

// Error is implemented by every error this SDK returns. It is sealed: only the
// types in this package satisfy it, so a type switch over them is exhaustive.
//
//	var apiErr dominaite.Error
//	if errors.As(err, &apiErr) { ... }
type Error interface {
	error
	sdkError()
}

// baseError carries the message, the request id and the sealing method
// shared by every SDK error.
type baseError struct {
	Message string
	// RequestID is the API's id of the call that failed: the
	// Dominaite-Request-Id response header, or metadata.requestId in the body
	// when the header is absent. Quote it to support. Empty when no response
	// arrived, when the API sent neither, and on a *ValidationError or a
	// *WebhookVerificationError.
	RequestID string
}

func (e baseError) Error() string { return e.Message }

func (e baseError) sdkError() {}

// Is makes errors.Is(err, ErrDominaite) true for every SDK error.
func (e baseError) Is(target error) bool { return target == ErrDominaite }

// RefusalError means the gateway understood the request but refused to open a
// checkout session. The API answered with success: false.
//
// Branch on ErrorCode (the ErrorCode* constants):
//   - PAYMENT_PROCESSING_UNAVAILABLE: card payments are off right now; retry later.
//   - DUPLICATE_REQUEST: a session for this idempotency key exists but cannot
//     be handed back yet; re-POST the same key shortly.
//   - ALREADY_PROCESSED: this idempotency key's payment already completed.
//   - PRIOR_ATTEMPT_FAILED: a prior attempt with this key failed terminally; use a fresh key.
//   - IDEMPOTENCY_KEY_REUSED: same key sent with a different amount, currency
//     or card-saving choice; a re-priced order needs a new key.
//   - STOREFRONT_MISMATCH: the key was first used for a different storefront.
//
// On a replay refusal the API also names WHICH payment your key collided with, on
// TransactionID. That is the recovery path - read it back with GetStatus to find out
// what the earlier attempt did, instead of minting a second payment for the same
// order:
//
//	session, err := client.CreateCheckoutSession(ctx, params)
//	var refusal *dominaite.RefusalError
//	if errors.As(err, &refusal) && refusal.TransactionID != "" {
//		status, err := client.GetStatus(ctx, refusal.TransactionID)
//	}
//
// TransactionID is empty when the API did not name one - notably the concurrent-race
// DUPLICATE_REQUEST, which knows a key was taken but not yet by which row. Always
// check before using it.
//
// Never blind-retry a refusal. It will not change on its own.
type RefusalError struct {
	baseError
	ErrorCode string
	// TransactionID is the payment this idempotency key collided with, when the
	// API named one. Empty otherwise.
	TransactionID string
	// Raw is the unparsed refusal payload, for fields not modelled above.
	Raw json.RawMessage
}

// Error codes the checkout session route answers with, for branching on the
// ErrorCode of a *RefusalError or an *APIError instead of on message text.
// The same strings can also arrive on a *ChargeError (see the ChargeError*
// constants for what they mean there).
//
// The storefront codes mean the session was refused because of the online
// location (storefront) it would be filed under. They arrive as an *APIError
// with HTTPStatus set, not as a refusal, and a retry will not change them:
//
//	var apiErr *dominaite.APIError
//	if errors.As(err, &apiErr) && apiErr.ErrorCode == dominaite.ErrorCodeStorefrontNotWhitelisted {
//		// the site's domain is not whitelisted with the payment provider yet
//	}
const (
	// ErrorCodeStorefrontNotWhitelisted (HTTP 409): the storefront's domain is
	// not whitelisted with the payment provider yet, and the environment
	// requires it. Nothing was minted. Ask Dominaite support to finish the
	// domain whitelisting; retrying will not help until then.
	ErrorCodeStorefrontNotWhitelisted = "STOREFRONT_NOT_WHITELISTED"
	// ErrorCodeStorefrontInactive (HTTP 409): the storefront was deactivated
	// or deleted. Reactivate it in the dashboard or use another one.
	ErrorCodeStorefrontInactive = "STOREFRONT_INACTIVE"
	// ErrorCodeStorefrontMismatch (HTTP 400): the API key is bound to one
	// storefront and the request named another. A replay of a key minted for
	// a different storefront answers the same code as a *RefusalError.
	ErrorCodeStorefrontMismatch = "STOREFRONT_MISMATCH"

	// ErrorCodeAlreadyProcessed (refusal): the payment for this idempotency
	// key already moved money. Mark the order paid; the key is spent.
	ErrorCodeAlreadyProcessed = "ALREADY_PROCESSED"
	// ErrorCodePriorAttemptFailed (refusal): the attempt for this key ended
	// failed, cancelled or abandoned. The key is spent; try again under a
	// new one.
	ErrorCodePriorAttemptFailed = "PRIOR_ATTEMPT_FAILED"
	// ErrorCodeDuplicateRequest (refusal): a session for this key exists but
	// cannot be handed back yet. Re-POST the SAME key after about a second.
	ErrorCodeDuplicateRequest = "DUPLICATE_REQUEST"
	// ErrorCodePaymentProcessingUnavailable (refusal on session create; 503 on
	// a charge): card payments are off right now. Nothing was minted or
	// charged; retry later with the SAME key.
	ErrorCodePaymentProcessingUnavailable = "PAYMENT_PROCESSING_UNAVAILABLE"
	// ErrorCodeIdempotencyKeyReused (refusal): this key was first used for a
	// different amount, currency or card-saving choice. A re-priced order
	// needs a new key; OrderIdempotencyKey derives one.
	ErrorCodeIdempotencyKeyReused = "IDEMPOTENCY_KEY_REUSED"
)

// Subscription session refusal codes, in the gateway's own order: the
// RefusalError.ErrorCode of a session created with AgreementID. They arrive
// as HTTP 200 with success false, like every session refusal. Nothing was
// created under any of them, and none changes on a retry with the same body.
const (
	// ErrorCodeAgreementNotFound: no agreement with this id under your
	// account. The agr_ id is case-sensitive; send it exactly as it was
	// returned. The agreement routes answer the same code as a 404 *APIError.
	ErrorCodeAgreementNotFound = "AGREEMENT_NOT_FOUND"
	// ErrorCodeAgreementNotPending: the agreement is already active,
	// cancelled, completed or failed, so there is no first payment left to
	// take.
	ErrorCodeAgreementNotPending = "AGREEMENT_NOT_PENDING"
	// ErrorCodeAgreementAmountMismatch: the amount or the currency is not the
	// agreement's own. On a plan without a trial the session must charge
	// exactly the agreement's amount; the currency must always match.
	ErrorCodeAgreementAmountMismatch = "AGREEMENT_AMOUNT_MISMATCH"
	// ErrorCodeAgreementConsentRequired: neither the session nor the
	// agreement carries consent to a version of the platform text. Send
	// Consent, with all three fields, on the session.
	ErrorCodeAgreementConsentRequired = "AGREEMENT_CONSENT_REQUIRED"
	// ErrorCodeAgreementSessionOpen: a session for this agreement is still
	// open, and TransactionID names it. Mint a new session with a new key
	// only after that one has expired.
	ErrorCodeAgreementSessionOpen = "AGREEMENT_SESSION_OPEN"
	// ErrorCodeAgreementAlreadyPaid: the session of this agreement was paid,
	// and the agreement is still pending. Read the status of TransactionID.
	// If the agreement stays pending, contact support with both ids. Never
	// mint again: that would charge the customer twice.
	ErrorCodeAgreementAlreadyPaid = "AGREEMENT_ALREADY_PAID"
	// ErrorCodeAgreementVariableAmountUnsupported: the agreement has a
	// MaxAmountPerPeriod, and a session can take the first payment of a
	// fixed-amount agreement only.
	ErrorCodeAgreementVariableAmountUnsupported = "AGREEMENT_VARIABLE_AMOUNT_UNSUPPORTED"
	// ErrorCodeSubscriptionsNotEnabled: subscriptions are not switched on for
	// your account yet. Plans and agreements can already be created. Contact
	// support; do not retry on a schedule.
	ErrorCodeSubscriptionsNotEnabled = "SUBSCRIPTIONS_NOT_ENABLED"
)

// SubscriptionSessionRefusalCodes are the refusal codes of a session created
// with AgreementID, in the gateway's own order. Treat a code you do not know
// as not retryable.
var SubscriptionSessionRefusalCodes = []string{
	ErrorCodeAgreementNotFound,
	ErrorCodeAgreementNotPending,
	ErrorCodeAgreementAmountMismatch,
	ErrorCodeAgreementConsentRequired,
	ErrorCodeAgreementSessionOpen,
	ErrorCodeAgreementAlreadyPaid,
	ErrorCodeAgreementVariableAmountUnsupported,
	ErrorCodeSubscriptionsNotEnabled,
}

// Codes of the plan and agreement routes. They arrive on APIError.ErrorCode,
// with the status on APIError.HTTPStatus. ErrorCodeAgreementNotFound (404),
// ErrorCodeAgreementConsentRequired (422: CreateAgreement names a stored
// payment method or a MaxAmountPerPeriod without Consent) and
// ErrorCodeIdempotencyKeyReused (422: the key was first used for a different
// plan or agreement) arrive there too. The list is additive: treat a 4xx code
// you do not know as not retryable. A 5xx is a *TransportError, with the code
// on TransportError.ErrorCode when there was one: nothing was created, so
// retry with the same key.
const (
	// ErrorCodePlanNotFound (404): no plan with this id under your account.
	// The plan_ id is case-sensitive; send it exactly as it was returned.
	ErrorCodePlanNotFound = "PLAN_NOT_FOUND"
	// ErrorCodePlanNotActive (409, CreateAgreement): the plan is archived.
	ErrorCodePlanNotActive = "PLAN_NOT_ACTIVE"
	// ErrorCodeAgreementEnded (409, CancelAgreement): the agreement already
	// completed or failed, so there is nothing left to cancel.
	ErrorCodeAgreementEnded = "AGREEMENT_ENDED"
	// ErrorCodePaymentMethodNotFound (404, CreateAgreement): no stored
	// payment method with this id under your account.
	ErrorCodePaymentMethodNotFound = "PAYMENT_METHOD_NOT_FOUND"
	// ErrorCodePaymentMethodNotActive (409, CreateAgreement): the stored
	// payment method is revoked, expired or retired. Leave it out and let the
	// subscription session save a fresh card.
	ErrorCodePaymentMethodNotActive = "PAYMENT_METHOD_NOT_ACTIVE"
	// ErrorCodePaymentMethodCurrencyMismatch (422, CreateAgreement): the card
	// was saved from a payment in another currency than the plan's.
	ErrorCodePaymentMethodCurrencyMismatch = "PAYMENT_METHOD_CURRENCY_MISMATCH"
	// ErrorCodePaymentMethodCurrencyUnknown (422, CreateAgreement): the
	// currency of the card cannot be established, so it cannot back an
	// agreement. Leave it out and let the subscription session save a fresh
	// card.
	ErrorCodePaymentMethodCurrencyUnknown = "PAYMENT_METHOD_CURRENCY_UNKNOWN"
	// ErrorCodeMaxAmountBelowPlanAmount (422, CreateAgreement):
	// MaxAmountPerPeriod is below the plan's amount.
	ErrorCodeMaxAmountBelowPlanAmount = "MAX_AMOUNT_BELOW_PLAN_AMOUNT"
)

// Codes of the webhook endpoint routes. They arrive on APIError.ErrorCode,
// with the status on APIError.HTTPStatus. ErrorCodeIdempotencyKeyReused (422:
// the key was first used for a different URL or events) and
// ErrorCodeDuplicateRequest (409: a request with this key is in progress;
// retry with the same key after a second) arrive there too. Nothing was
// registered under any of them.
const (
	// ErrorCodeWebhookURLRejected (400): the URL is not https, is malformed,
	// does not resolve, or points at a private, loopback or link-local host.
	// The message says which.
	ErrorCodeWebhookURLRejected = "WEBHOOK_URL_REJECTED"
	// ErrorCodeWebhookLimitReached (409): you already have the maximum of 25
	// active endpoints. Delete one first.
	ErrorCodeWebhookLimitReached = "WEBHOOK_LIMIT_REACHED"
	// ErrorCodeWebhookAlreadyRegistered (409): this key registered an endpoint
	// more than a day ago, and that endpoint still exists. Its id is at
	// error.params.webhookId in APIError.Raw. Use a fresh key for a new
	// endpoint.
	ErrorCodeWebhookAlreadyRegistered = "WEBHOOK_ALREADY_REGISTERED"
	// ErrorCodeWebhookNotFound (404, DeleteWebhookEndpoint): no endpoint with
	// this id under your account. After a lost answer, treat it as done.
	ErrorCodeWebhookNotFound = "WEBHOOK_NOT_FOUND"
)

// RecurringErrorCodes are the codes of the plan and agreement routes, in the
// gateway's own order. They arrive on APIError.ErrorCode.
var RecurringErrorCodes = []string{
	ErrorCodePlanNotFound,
	ErrorCodePlanNotActive,
	ErrorCodeAgreementNotFound,
	ErrorCodeAgreementEnded,
	ErrorCodePaymentMethodNotFound,
	ErrorCodePaymentMethodNotActive,
	ErrorCodePaymentMethodCurrencyMismatch,
	ErrorCodePaymentMethodCurrencyUnknown,
	ErrorCodeMaxAmountBelowPlanAmount,
	ErrorCodeAgreementConsentRequired,
	ErrorCodeIdempotencyKeyReused,
}

// WebhookErrorCodes are the codes of the webhook endpoint routes, in the
// gateway's own order. They arrive on APIError.ErrorCode.
var WebhookErrorCodes = []string{
	ErrorCodeWebhookNotFound,
	ErrorCodeWebhookLimitReached,
	ErrorCodeWebhookAlreadyRegistered,
	ErrorCodeWebhookURLRejected,
	ErrorCodeIdempotencyKeyReused,
	ErrorCodeDuplicateRequest,
}

// ErrorCodeInsufficientScope (HTTP 403, on every route that needs a scope):
// the request was signed correctly, but the API key does not hold the scope
// the route needs. It arrives as an *AuthError with HTTPStatus 403. A key's
// scopes are fixed when it is issued, so a retry with the same key gets the
// same answer: call the route with a key that holds its scope.
//
// The scopes are payments (sessions, charges, revokes, refunds, plans and
// agreements), transactions:read (ListTransactions) and webhooks:manage (the
// webhook endpoint routes). A key holds either payments, or one or both of
// the other two: use a separate reporting key for transactions and webhook
// endpoints.
const ErrorCodeInsufficientScope = "INSUFFICIENT_SCOPE"

// ErrorCodeVerifyOnlyNotEnabled (HTTP 403, CreateCheckoutSession): the
// session asked for VerifyOnly, and verify-only sessions are not enabled for
// your account. It arrives as an *AuthError with HTTPStatus 403. Nothing was
// created and the idempotency key is not spent. We switch it on for your
// account: ask Dominaite support.
const ErrorCodeVerifyOnlyNotEnabled = "VERIFY_ONLY_NOT_ENABLED"

// AuthorizationErrorCodes are the codes a correctly signed request can be
// refused with because of what the key may do, in the gateway's own order.
// None is retryable.
var AuthorizationErrorCodes = []string{
	ErrorCodeInsufficientScope,
}

// Authentication codes (HTTP 401), on AuthError.ErrorCode. None is
// retryable: fix the key id, the secret, the server clock or the allowlist.
const (
	// ErrorCodeInvalidAPIKey: the key id is unknown, malformed or revoked, or
	// it belongs to another environment.
	ErrorCodeInvalidAPIKey = "INVALID_API_KEY"
	// ErrorCodeInvalidSignature: the signature does not match. Check the
	// secret and that the signed body is the body sent.
	ErrorCodeInvalidSignature = "INVALID_SIGNATURE"
	// ErrorCodeTimestampOutOfRange: X-Timestamp is more than 300 seconds
	// from the server clock. Sync this server's clock.
	ErrorCodeTimestampOutOfRange = "TIMESTAMP_OUT_OF_RANGE"
	// ErrorCodeIPNotAllowed: the caller's IP address is not on the key's
	// allowlist.
	ErrorCodeIPNotAllowed = "IP_NOT_ALLOWED"
)

// AuthenticationErrorCodes are the HTTP 401 codes, in the gateway's own
// order.
var AuthenticationErrorCodes = []string{
	ErrorCodeInvalidAPIKey,
	ErrorCodeInvalidSignature,
	ErrorCodeTimestampOutOfRange,
	ErrorCodeIPNotAllowed,
}

// ErrorCodeIdempotencyKeyRequired (HTTP 400, every keyed write): the
// Idempotency-Key header was missing or too long. The SDK refuses such a key
// before sending, so this only arrives if something between you and the API
// dropped the header.
const ErrorCodeIdempotencyKeyRequired = "IDEMPOTENCY_KEY_REQUIRED"

// IdempotencyErrorCodes are the idempotency codes, in the gateway's own
// order: ErrorCodeIdempotencyKeyRequired (HTTP 400), then the replay
// refusals of a session create (HTTP 200 with success false).
var IdempotencyErrorCodes = []string{
	ErrorCodeIdempotencyKeyRequired,
	ErrorCodeDuplicateRequest,
	ErrorCodeAlreadyProcessed,
	ErrorCodePriorAttemptFailed,
	ErrorCodeIdempotencyKeyReused,
}

// Transient codes: nothing was created and a retry with the SAME key can
// succeed.
const (
	// ErrorCodeMerchantAPIUnavailable (HTTP 503): the API or a provider
	// behind it is unavailable. A *TransportError carries it on ErrorCode; a
	// revoke carries it on a *RevokeError.
	ErrorCodeMerchantAPIUnavailable = "MERCHANT_API_UNAVAILABLE"
	// ErrorCodeInternalError (HTTP 500): the API failed before it could
	// answer. A *TransportError carries it on ErrorCode.
	ErrorCodeInternalError = "INTERNAL_ERROR"
)

// TransientErrorCodes are the codes the gateway asks you to retry, in its
// own order. The client retries both for you (see WithMaxRetries).
var TransientErrorCodes = []string{
	ErrorCodeMerchantAPIUnavailable,
	ErrorCodePaymentProcessingUnavailable,
}

// SessionRefusalErrorCodes are every RefusalError.ErrorCode a session create
// can answer with HTTP 200 and success false: the five replay and
// availability codes of the published contract, ErrorCodeStorefrontMismatch
// (a replay of a key minted for another storefront), then the
// SubscriptionSessionRefusalCodes. Treat a code you do not know as not
// retryable.
var SessionRefusalErrorCodes = []string{
	ErrorCodePaymentProcessingUnavailable,
	ErrorCodeDuplicateRequest,
	ErrorCodeAlreadyProcessed,
	ErrorCodeIdempotencyKeyReused,
	ErrorCodePriorAttemptFailed,
	ErrorCodeStorefrontMismatch,
	ErrorCodeAgreementNotFound,
	ErrorCodeAgreementNotPending,
	ErrorCodeAgreementAmountMismatch,
	ErrorCodeAgreementConsentRequired,
	ErrorCodeAgreementSessionOpen,
	ErrorCodeAgreementAlreadyPaid,
	ErrorCodeAgreementVariableAmountUnsupported,
	ErrorCodeSubscriptionsNotEnabled,
}

// ErrorCodeValidationError (HTTP 400) is the envelope code of a request that
// failed validation. error.validationErrors on APIError.Raw names each field
// and its field code (the ValidationErrorCodes).
const ErrorCodeValidationError = "VALIDATION_ERROR"

// Field codes of a validation failure: the code of each entry in
// error.validationErrors on APIError.Raw. A plan create also answers
// ErrorCodeInvalidCurrency and ErrorCodeInvalidSelection as the envelope code.
const (
	ErrorCodeRequired         = "REQUIRED"
	ErrorCodeMinValue         = "MIN_VALUE"
	ErrorCodeMaxLength        = "MAX_LENGTH"
	ErrorCodeInvalidFormat    = "INVALID_FORMAT"
	ErrorCodeInvalidSelection = "INVALID_SELECTION"
	ErrorCodeDateInFuture     = "DATE_IN_FUTURE"
	ErrorCodeInvalidEmail     = "INVALID_EMAIL"
	ErrorCodeInvalidCurrency  = "INVALID_CURRENCY"
)

// ValidationErrorCodes are the field codes of a validation failure, in the
// gateway's own order. The list can grow: treat a code you do not know as a
// rejected field.
var ValidationErrorCodes = []string{
	ErrorCodeRequired,
	ErrorCodeMinValue,
	ErrorCodeMaxLength,
	ErrorCodeInvalidFormat,
	ErrorCodeInvalidSelection,
	ErrorCodeDateInFuture,
	ErrorCodeInvalidEmail,
	ErrorCodeInvalidCurrency,
}

// AuthError means the API refused the request before it did anything: HTTP 401
// or 403, on HTTPStatus. A retry with the same key and config gets the same
// answer.
//
// HTTP 401: the API could not authenticate the request. ErrorCode is one of
// INVALID_API_KEY, INVALID_SIGNATURE, TIMESTAMP_OUT_OF_RANGE or
// IP_NOT_ALLOWED. Fix the key id, the secret, the server clock or the caller
// allowlist.
//
// HTTP 403: the request was signed correctly, so the key id, the secret and
// the clock are fine, but the key may not do this. ErrorCode is
// ErrorCodeInsufficientScope (the key does not hold the scope the route
// needs) or ErrorCodeVerifyOnlyNotEnabled (a verify-only session on an
// account without verify-only sessions). Message says what to do.
type AuthError struct {
	baseError
	// HTTPStatus is 401 or 403.
	HTTPStatus int
	ErrorCode  string
}

// RateLimitError means the API answered HTTP 429: you sent requests faster than
// your allowance. Nothing was created and nothing is wrong with the request.
//
// The platform limits are 60 requests per minute per API key and 120 per minute
// per IP address. Both are enforced, so several keys behind one egress IP share
// the second budget.
//
// The SDK does NOT retry this for you, on purpose. A client that retries into a
// rate limit makes the queue longer for everyone, and only your code knows
// whether this payment can wait. The client's retry policy (WithMaxRetries)
// returns it at once. When you do retry, reuse the SAME idempotency key: the
// request may have been rejected at the edge, but that is not guaranteed.
//
// RetryAfterSeconds carries the Retry-After header when the API sent one as
// integer seconds. HasRetryAfter distinguishes "wait 0 seconds" from "the API
// did not say" - a Retry-After in the HTTP-date form is reported as absent.
//
//	var limited *dominaite.RateLimitError
//	if errors.As(err, &limited) {
//		wait := 5 * time.Second
//		if limited.HasRetryAfter {
//			wait = time.Duration(limited.RetryAfterSeconds) * time.Second
//		}
//		// reschedule with the same idempotency key
//	}
//
// ErrorCode is the machine-readable code when the API named one, empty
// otherwise: ErrorCodeRateLimitExceeded for the request limits, and
// ErrorCodeChargeVelocityExceeded or ErrorCodeChargeDeclineRateExceeded on
// ChargePaymentMethod. Raw is the whole envelope; the charge limits put their
// window and limit under error.params.
type RateLimitError struct {
	baseError
	RetryAfterSeconds int
	HasRetryAfter     bool
	ErrorCode         string
	// Raw is the whole envelope the gateway sent, for fields not modelled
	// above. Empty when the 429 had no body.
	Raw json.RawMessage
}

// Rate limit codes, on RateLimitError.ErrorCode. Nothing was created or
// charged under any of them; retry with the SAME idempotency key once the
// wait is over.
const (
	// ErrorCodeRateLimitExceeded: the key or the IP address sent more
	// requests than it may. Retry-After says how long to wait. The API sends
	// no Retry-After when it locks an address out after repeated failed
	// sign-ins; fix the credentials before you retry.
	ErrorCodeRateLimitExceeded = "RATE_LIMIT_EXCEEDED"
	// ErrorCodeChargeVelocityExceeded (ChargePaymentMethod): a volume limit
	// refused the charge before anything was placed. error.params.window and
	// error.params.limit on Raw say which limit. A charge that keeps hitting
	// it is a pattern to look into, not to retry around.
	ErrorCodeChargeVelocityExceeded = "CHARGE_VELOCITY_EXCEEDED"
	// ErrorCodeChargeDeclineRateExceeded (ChargePaymentMethod): the key
	// collected too many declined charges in a short window, so its charge
	// route is locked for a while. The other routes keep working. If this is
	// a genuine billing run with many declines, contact support.
	ErrorCodeChargeDeclineRateExceeded = "CHARGE_DECLINE_RATE_EXCEEDED"
)

// Charge error codes, in the gateway's own order: the codes ChargePaymentMethod
// returns as a *ChargeError. CHARGE_DECLINED (HTTP 402) is deliberately not one
// of them: a decline is a charge result with Status ChargeStatusFailed, not an
// error.
const (
	// ChargeErrorPaymentMethodNotActive (409): the method is revoked, expired
	// or retired; ask the customer for another card via a hosted session with
	// SaveCard.
	ChargeErrorPaymentMethodNotActive = "PAYMENT_METHOD_NOT_ACTIVE"
	// ChargeErrorDuplicateRequest (409): a request with this key is still in
	// flight; retry with the SAME key in a moment.
	ChargeErrorDuplicateRequest = "DUPLICATE_REQUEST"
	// ChargeErrorIdempotencyKeyReused (422): same key, different body or
	// method; a bug on your side.
	ChargeErrorIdempotencyKeyReused = "IDEMPOTENCY_KEY_REUSED"
	// ChargeErrorOutcomeUnknown (502): the provider gave no verdict and the
	// charge MAY have happened. Charge is set: poll
	// GetStatus(Charge.TransactionID) or wait for the webhook. Never retry
	// under a new key.
	ChargeErrorOutcomeUnknown = "CHARGE_OUTCOME_UNKNOWN"
	// ChargeErrorFailed (502): nothing was charged. Charge is set when a row
	// exists (its DeclineClass and DeclineCode are empty), nil when the
	// provider refused before one.
	ChargeErrorFailed = "CHARGE_FAILED"
	// ChargeErrorChargesDisabled (503): nothing was charged. The API answers
	// it until we switch saved-card charges on for your account, and when we
	// pause charges for a short time. The code is the same in both cases. If
	// we never switched saved-card charges on for your account, a retry does
	// not help: ask us. If they are on, retry later with the SAME key.
	ChargeErrorChargesDisabled = "PAYMENT_METHOD_CHARGES_DISABLED"
	// ChargeErrorProcessingUnavailable (503): nothing was charged; retry later
	// with the SAME key.
	ChargeErrorProcessingUnavailable = "PAYMENT_PROCESSING_UNAVAILABLE"

	// ChargeErrorPaymentMethodNotFound (404): no stored payment method with
	// this id under your account.
	ChargeErrorPaymentMethodNotFound = "PAYMENT_METHOD_NOT_FOUND"
	// ChargeErrorInProgress (409): another charge of this payment method is
	// still running. Retry with the SAME key in a moment.
	ChargeErrorInProgress = "CHARGE_IN_PROGRESS"
	// ChargeErrorCurrencyMismatch (422): the currency is not the currency the
	// card was saved in.
	ChargeErrorCurrencyMismatch = "CHARGE_CURRENCY_MISMATCH"
	// ChargeErrorAgreementNotFound (404): no agreement with the AgreementID
	// under your account.
	ChargeErrorAgreementNotFound = "AGREEMENT_NOT_FOUND"
	// ChargeErrorAgreementMethodMismatch (422): the payment method is not the
	// card of the agreement.
	ChargeErrorAgreementMethodMismatch = "AGREEMENT_METHOD_MISMATCH"
	// ChargeErrorAgreementConsentMissing (422): the agreement has no recorded
	// consent, so it cannot be charged.
	ChargeErrorAgreementConsentMissing = "AGREEMENT_CONSENT_MISSING"
	// ChargeErrorAgreementCurrencyMismatch (422): the currency is not the
	// agreement's currency.
	ChargeErrorAgreementCurrencyMismatch = "AGREEMENT_CURRENCY_MISMATCH"
	// ChargeErrorAgreementAmountMismatch (422): the amount is not the amount
	// of a fixed agreement.
	ChargeErrorAgreementAmountMismatch = "AGREEMENT_AMOUNT_MISMATCH"
	// ChargeErrorAmountAboveAgreement (422): the amount is above the
	// MaxAmountPerPeriod of a variable agreement.
	ChargeErrorAmountAboveAgreement = "CHARGE_AMOUNT_ABOVE_AGREEMENT"
	// ChargeErrorAgreementPeriodAlreadyCharged (422): this period of the
	// agreement was already charged.
	ChargeErrorAgreementPeriodAlreadyCharged = "AGREEMENT_PERIOD_ALREADY_CHARGED"
	// ChargeErrorAgreementEnded (422): the agreement is cancelled, completed or
	// failed.
	ChargeErrorAgreementEnded = "AGREEMENT_ENDED"
	// ChargeErrorAgreementNotStarted (422): the agreement is not active yet.
	ChargeErrorAgreementNotStarted = "AGREEMENT_NOT_STARTED"
	// ChargeErrorAmountAboveCap (422): the amount is above the per-charge cap
	// of your account. error.params.amount and error.params.cap on Raw say
	// both values.
	ChargeErrorAmountAboveCap = "CHARGE_AMOUNT_ABOVE_CAP"
)

// StorefrontErrorCodes are the storefront codes a session create can be
// refused with, in the gateway's own order: ErrorCodeStorefrontMismatch
// (HTTP 400), ErrorCodeStorefrontInactive (409) and
// ErrorCodeStorefrontNotWhitelisted (409). None is retryable. They arrive as an
// *APIError, not as the HTTP 200 refusal shape, so they are not session
// refusal codes.
var StorefrontErrorCodes = []string{
	ErrorCodeStorefrontMismatch,
	ErrorCodeStorefrontInactive,
	ErrorCodeStorefrontNotWhitelisted,
}

// ChargeErrorCodes is the complete v1 vocabulary of ChargeError.ErrorCode:
// the seven codes of the published contract first, then the 404, 409 and 422
// codes of the charge route. Unknown 4xx codes still arrive as a
// *ChargeError; branch on the ones you know.
var ChargeErrorCodes = []string{
	ChargeErrorPaymentMethodNotActive,
	ChargeErrorDuplicateRequest,
	ChargeErrorIdempotencyKeyReused,
	ChargeErrorOutcomeUnknown,
	ChargeErrorFailed,
	ChargeErrorChargesDisabled,
	ChargeErrorProcessingUnavailable,
	ChargeErrorPaymentMethodNotFound,
	ChargeErrorInProgress,
	ChargeErrorCurrencyMismatch,
	ChargeErrorAgreementNotFound,
	ChargeErrorAgreementMethodMismatch,
	ChargeErrorAgreementConsentMissing,
	ChargeErrorAgreementCurrencyMismatch,
	ChargeErrorAgreementAmountMismatch,
	ChargeErrorAmountAboveAgreement,
	ChargeErrorAgreementPeriodAlreadyCharged,
	ChargeErrorAgreementEnded,
	ChargeErrorAgreementNotStarted,
	ChargeErrorAmountAboveCap,
}

// chargeServerErrorCodes are the 5xx codes that are the charge route's own
// answer. Any other 5xx, coded or not, is a *TransportError.
var chargeServerErrorCodes = []string{
	ChargeErrorOutcomeUnknown,
	ChargeErrorFailed,
	ChargeErrorChargesDisabled,
	ChargeErrorProcessingUnavailable,
}

// ChargeError means the gateway answered a charge with an error code instead
// of a charge result: HTTP 404, 409, 422, 502 or 503 with one of the
// ChargeError* codes on ErrorCode (see each constant for what to do).
// HTTPStatus carries the status, Message the gateway's own text.
//
// Charge is the charge row the gateway attached to its answer, when it did:
// always for CHARGE_OUTCOME_UNKNOWN, sometimes for CHARGE_FAILED, never for
// the rest. TransactionID is a shortcut for Charge.TransactionID, empty when
// there is no charge:
//
//	charge, err := client.ChargePaymentMethod(ctx, id, params)
//	var chargeErr *dominaite.ChargeError
//	if errors.As(err, &chargeErr) && chargeErr.ErrorCode == dominaite.ChargeErrorOutcomeUnknown {
//		// poll client.GetStatus(ctx, chargeErr.TransactionID); do not retry under a new key
//	}
//
// Only authentication (401/403, *AuthError), validation (400, *APIError),
// rate limiting (429, *RateLimitError), network failures and a 5xx without
// one of the charge route's own 5xx codes (*TransportError, with the code
// when there was one) keep their generic errors on this route. A 404 with a
// code, PAYMENT_METHOD_NOT_FOUND or AGREEMENT_NOT_FOUND, is a *ChargeError.
type ChargeError struct {
	baseError
	HTTPStatus int
	ErrorCode  string
	// Charge is the charge row the gateway attached to its answer, when it did.
	Charge *PaymentMethodCharge
	// TransactionID is Charge.TransactionID, for polling GetStatus. Empty
	// without a charge.
	TransactionID string
	// Raw is the whole envelope the gateway sent, for fields not modelled above.
	Raw json.RawMessage
}

// Revoke error codes, in the gateway's own order: the codes RevokePaymentMethod
// returns as a *RevokeError. Nothing changed under either.
const (
	// RevokeErrorUpstreamContract (502): the provider refused the deletion for
	// a reason a retry will not fix; contact support with the payment method id.
	RevokeErrorUpstreamContract = "UPSTREAM_CONTRACT_ERROR"
	// RevokeErrorMerchantAPIUnavailable (503): the provider is unavailable or
	// throttling; retry later.
	RevokeErrorMerchantAPIUnavailable = "MERCHANT_API_UNAVAILABLE"
)

// RevokeErrorCodes is the complete v1 vocabulary of RevokeError.ErrorCode.
var RevokeErrorCodes = []string{
	RevokeErrorUpstreamContract,
	RevokeErrorMerchantAPIUnavailable,
}

// RevokeError means the gateway refused to revoke a stored payment method:
// HTTP 502 or 503 with one of the RevokeError* codes on ErrorCode. Nothing
// changed either way. An id that is not yours is still the generic *APIError
// with HTTPStatus 404. Any other 5xx is a *TransportError, with the code when
// there was one.
type RevokeError struct {
	baseError
	HTTPStatus int
	ErrorCode  string
	// Raw is the whole envelope the gateway sent, for fields not modelled above.
	Raw json.RawMessage
}

// Refund error codes, in the gateway's own order: the codes CreateRefund and
// GetRefund return as a *RefundError.
const (
	// RefundErrorPaymentNotFound (404): no card-not-present payment with this
	// id under your account. Not retryable.
	RefundErrorPaymentNotFound = "PAYMENT_NOT_FOUND"
	// RefundErrorRefundNotFound (404, GetRefund only): no refund with this id
	// on this payment. Right after CreateRefund the refund may not be picked
	// up yet: poll again for up to 60 seconds, after that the id is unknown.
	RefundErrorRefundNotFound = "REFUND_NOT_FOUND"
	// RefundErrorPaymentNotRefundable (422): the payment is not paid, already
	// fully refunded, or everything left on it is already being refunded.
	// Nothing was queued and the key is not burnt.
	RefundErrorPaymentNotRefundable = "PAYMENT_NOT_REFUNDABLE"
	// RefundErrorAmountExceeded (422): the amount is more than what is left to
	// refund, counting refunds still in progress; the message names the
	// amount left. Nothing was queued and the key is not burnt.
	RefundErrorAmountExceeded = "REFUND_AMOUNT_EXCEEDED"
	// RefundErrorIdempotencyKeyReused (422): the key was first used for a
	// different amount, reason or payment. Use a fresh key for a genuinely
	// new refund.
	RefundErrorIdempotencyKeyReused = "IDEMPOTENCY_KEY_REUSED"
	// RefundErrorDuplicateRequest (409): a request with this key is being
	// processed right now. Retry with the SAME key after a second, for up to
	// 120 seconds.
	RefundErrorDuplicateRequest = "DUPLICATE_REQUEST"
	// RefundErrorIdempotencyKeyRequired (400): the Idempotency-Key header was
	// missing or too long. The SDK refuses such a key before sending, so this
	// only arrives if something between you and the API dropped the header.
	RefundErrorIdempotencyKeyRequired = "IDEMPOTENCY_KEY_REQUIRED"
)

// RefundErrorCodes is the complete v1 vocabulary of RefundError.ErrorCode.
// Unknown codes still arrive as a *RefundError; branch on the ones you know.
var RefundErrorCodes = []string{
	RefundErrorPaymentNotFound,
	RefundErrorRefundNotFound,
	RefundErrorPaymentNotRefundable,
	RefundErrorAmountExceeded,
	RefundErrorIdempotencyKeyReused,
	RefundErrorDuplicateRequest,
	RefundErrorIdempotencyKeyRequired,
}

// Refund failure codes, in the gateway's own order: the values of
// Refund.FailureCode on a failed refund. They are not errors: CreateRefund and
// GetRefund return the refund with Status RefundStatusFailed. Treat a value you
// do not recognise as RefundFailureFailed.
const (
	// RefundFailureAmountExceeded: by the time the refund ran, less was left
	// to refund than it asked for.
	RefundFailureAmountExceeded = "REFUND_AMOUNT_EXCEEDED"
	// RefundFailurePaymentNotRefundable: by the time the refund ran, the
	// payment could no longer be refunded.
	RefundFailurePaymentNotRefundable = "PAYMENT_NOT_REFUNDABLE"
	// RefundFailureFailed: the refund could not be completed. Retry with a new
	// idempotency key if it is still wanted.
	RefundFailureFailed = "REFUND_FAILED"
)

// RefundFailureCodes is the complete v1 vocabulary of Refund.FailureCode.
var RefundFailureCodes = []string{
	RefundFailureAmountExceeded,
	RefundFailurePaymentNotRefundable,
	RefundFailureFailed,
}

// RefundError means a refund route answered with one of the RefundError*
// codes instead of a refund: HTTP 400, 404, 409 or 422 (see each constant for
// what to do). HTTPStatus carries the status, Message the gateway's own text.
//
// Retryable is true for the two codes the gateway asks you to retry with the
// SAME idempotency key: DUPLICATE_REQUEST (for up to 120 seconds) and, on
// GetRefund, REFUND_NOT_FOUND (for up to 60 seconds after CreateRefund).
// Everything else will not change on a retry.
//
//	refund, err := client.CreateRefund(ctx, transactionID, params)
//	var refundErr *dominaite.RefundError
//	if errors.As(err, &refundErr) && refundErr.ErrorCode == dominaite.RefundErrorAmountExceeded {
//		// less is left to refund than you asked for; nothing was queued
//	}
//
// A failed refund is not a *RefundError: it comes back as a Refund with Status
// RefundStatusFailed and a FailureCode. Authentication (401/403, *AuthError),
// rate limiting (429, *RateLimitError) and network failures or any 5xx
// (*TransportError: nothing was queued, retry with the SAME key; a 503
// MERCHANT_API_UNAVAILABLE keeps its code on TransportError.ErrorCode) keep
// their generic errors on these routes.
type RefundError struct {
	baseError
	HTTPStatus int
	ErrorCode  string
	// Retryable reports whether retrying with the same idempotency key can
	// succeed: DUPLICATE_REQUEST and REFUND_NOT_FOUND.
	Retryable bool
	// Raw is the whole envelope the gateway sent, for fields not modelled above.
	Raw json.RawMessage
}

// APIError means the API answered, but with an unexpected or rejecting response.
// HTTPStatus carries the code. A 404 from GetStatus means an unknown
// transaction id; a 404 from ChargePaymentMethod or RevokePaymentMethod an id
// that is not yours. A 3xx means something in front of the API answered with a
// redirect: the SDK never follows one and never treats its body as a real
// response, because the API itself does not redirect.
//
// A key replayed with a different body does NOT arrive here. The API answers
// HTTP 200 with success false and IDEMPOTENCY_KEY_REUSED, so it reaches you as
// a *RefusalError. A 429 does not arrive here either: it is a *RateLimitError.
//
// ErrorCode is the machine-readable code when the API sent one, so input
// rejections can be branched on rather than string-matched. A 400 carrying
// IDEMPOTENCY_KEY_REQUIRED means the Idempotency-Key header was missing or
// empty. A 409 carrying ErrorCodeStorefrontNotWhitelisted or
// ErrorCodeStorefrontInactive, or a 400 carrying ErrorCodeStorefrontMismatch,
// means the storefront refused the session. Empty when the API named no code.
//
// Raw is the whole envelope the API sent with a rejection, for fields not
// modelled above: error.params, error.validationErrors and error.traceId. It
// is nil when there was no JSON envelope, and on an unexpected 2xx.
type APIError struct {
	baseError
	HTTPStatus int
	ErrorCode  string
	// Raw is the whole envelope the API sent, for fields not modelled above.
	Raw json.RawMessage
}

// TransportError means a network-level failure, a timeout, or a 5xx. The request
// may or may not have reached the API, so retry WITH THE SAME idempotency key: a
// retried key never creates a second payment. The client retries it for you on
// the keyed writes and the reads (see WithMaxRetries) before you see it.
//
// Err is the underlying cause when there was one, reachable with errors.Unwrap
// or errors.As.
//
// HTTPStatus is the 5xx status, 0 when no response arrived. ErrorCode is the
// API's code when the 5xx carried one, for example
// ErrorCodeMerchantAPIUnavailable or ErrorCodeInternalError, and empty
// otherwise. Raw is the envelope of that 5xx, nil when it was not JSON.
type TransportError struct {
	baseError
	Err        error
	HTTPStatus int
	ErrorCode  string
	// Raw is the whole envelope the API sent with the 5xx, for fields not
	// modelled above.
	Raw json.RawMessage
}

// Unwrap exposes the underlying network error, if any.
func (e *TransportError) Unwrap() error { return e.Err }

// ValidationError means the SDK rejected the call before sending anything.
// Nothing reached the network; fix the arguments.
type ValidationError struct {
	baseError
}

func newRefusalError(code, message string) *RefusalError {
	return &RefusalError{baseError: baseError{Message: message}, ErrorCode: code}
}

func newChargeError(status int, code, message string, charge *PaymentMethodCharge, raw json.RawMessage) *ChargeError {
	chargeErr := &ChargeError{baseError: baseError{Message: message}, HTTPStatus: status, ErrorCode: code, Charge: charge, Raw: raw}
	if charge != nil {
		chargeErr.TransactionID = charge.TransactionID
	}
	return chargeErr
}

func newRefundError(status int, code, message string, raw json.RawMessage) *RefundError {
	retryable := code == RefundErrorDuplicateRequest || code == RefundErrorRefundNotFound
	return &RefundError{baseError: baseError{Message: message}, HTTPStatus: status, ErrorCode: code, Retryable: retryable, Raw: raw}
}

func newRevokeError(status int, code, message string, raw json.RawMessage) *RevokeError {
	return &RevokeError{baseError: baseError{Message: message}, HTTPStatus: status, ErrorCode: code, Raw: raw}
}

func newAuthError(status int, code, message string) *AuthError {
	return &AuthError{baseError: baseError{Message: message}, HTTPStatus: status, ErrorCode: code}
}

func newRateLimitError(message string) *RateLimitError {
	return &RateLimitError{baseError: baseError{Message: message}}
}

func newAPIError(status int, message string) *APIError {
	return &APIError{baseError: baseError{Message: message}, HTTPStatus: status}
}

func newTransportError(message string, cause error) *TransportError {
	return &TransportError{baseError: baseError{Message: message}, Err: cause}
}

func newValidationError(message string) *ValidationError {
	return &ValidationError{baseError: baseError{Message: message}}
}

// Reasons reported on WebhookVerificationError.Reason.
const (
	// WebhookReasonMalformedSignature means the signature header was missing,
	// empty, or not in the "t=...,v1=..." shape (no t, no v1, or a v1 that is
	// not hex). Nothing could be checked.
	WebhookReasonMalformedSignature = "MALFORMED_SIGNATURE"
	// WebhookReasonSignatureMismatch means the MAC did not match. Either the
	// secret is wrong or the payload is not the bytes that were signed. Treat
	// the request as hostile.
	WebhookReasonSignatureMismatch = "SIGNATURE_MISMATCH"
	// WebhookReasonTimestampOutOfTolerance means the MAC was valid but the
	// timestamp is too far from now: a replay of a genuine old delivery, or
	// your server clock has drifted. Check NTP before widening the tolerance.
	WebhookReasonTimestampOutOfTolerance = "TIMESTAMP_OUT_OF_TOLERANCE"
	// WebhookReasonMalformedPayload means the signature was good but the body
	// was not valid JSON. Surprising - the bytes are authentic - so log it
	// rather than silently dropping it.
	WebhookReasonMalformedPayload = "MALFORMED_PAYLOAD"
	// WebhookReasonInvalidTolerance means the caller passed a negative
	// tolerance to WithWebhookTolerance. That is your bug, not the sender's:
	// nothing about the delivery was checked. Use 0 to require an exact
	// timestamp, or a positive window.
	WebhookReasonInvalidTolerance = "INVALID_TOLERANCE"
)

// WebhookVerificationError means VerifyWebhook rejected a delivery. Nothing was
// parsed and nothing should be acted on.
//
// Answer the HTTP request with 400 and no detail. Never echo the reason back to
// the caller: whoever sent an unverified request does not get to learn whether
// their secret or their timestamp was the problem.
//
//	event, err := dominaite.VerifyWebhook(body, r.Header.Get("X-Webhook-Signature"), secret)
//	var verr *dominaite.WebhookVerificationError
//	if errors.As(err, &verr) {
//		log.Printf("rejected webhook: %s", verr.Reason) // your logs only
//		w.WriteHeader(http.StatusBadRequest)
//		return
//	}
type WebhookVerificationError struct {
	baseError
	// Reason is one of the WebhookReason* constants.
	Reason string
}

func newWebhookVerificationError(reason, message string) *WebhookVerificationError {
	return &WebhookVerificationError{baseError: baseError{Message: message}, Reason: reason}
}
