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

// baseError carries the message and the sealing method shared by every SDK error.
type baseError struct {
	Message string
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

// AuthError means the API rejected your credentials or signature (HTTP 401/403).
// Not retryable: fix the key id, the secret, the server clock, or the caller
// allowlist.
//
// ErrorCode is one of INVALID_API_KEY, INVALID_SIGNATURE,
// TIMESTAMP_OUT_OF_RANGE, IP_NOT_ALLOWED.
type AuthError struct {
	baseError
	ErrorCode string
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
// whether this payment can wait. CreateCheckoutSessionWithRetry returns it
// immediately too. When you do retry, reuse the SAME idempotency key: the
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
// ErrorCode is the machine-readable code when the API named one. Empty otherwise.
type RateLimitError struct {
	baseError
	RetryAfterSeconds int
	HasRetryAfter     bool
	ErrorCode         string
}

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
	// ChargeErrorChargesDisabled (503): nothing was charged; retry later with
	// the SAME key.
	ChargeErrorChargesDisabled = "PAYMENT_METHOD_CHARGES_DISABLED"
	// ChargeErrorProcessingUnavailable (503): nothing was charged; retry later
	// with the SAME key.
	ChargeErrorProcessingUnavailable = "PAYMENT_PROCESSING_UNAVAILABLE"
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

// ChargeErrorCodes is the complete v1 vocabulary of ChargeError.ErrorCode.
// Unknown codes still arrive as a *ChargeError; branch on the ones you know.
var ChargeErrorCodes = []string{
	ChargeErrorPaymentMethodNotActive,
	ChargeErrorDuplicateRequest,
	ChargeErrorIdempotencyKeyReused,
	ChargeErrorOutcomeUnknown,
	ChargeErrorFailed,
	ChargeErrorChargesDisabled,
	ChargeErrorProcessingUnavailable,
}

// ChargeError means the gateway answered a charge with an error code instead
// of a charge result: HTTP 409, 422, 502 or 503 with one of the ChargeError*
// codes on ErrorCode (see each constant for what to do). HTTPStatus carries
// the status, Message the gateway's own text.
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
// Only authentication (401/403, *AuthError), an id that is not yours (404,
// *APIError), validation (400, *APIError), rate limiting (429,
// *RateLimitError) and network failures or a 5xx without a code
// (*TransportError) keep their generic errors on this route.
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
// with HTTPStatus 404.
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
// (*TransportError: nothing was queued, retry with the SAME key) keep their
// generic errors on these routes.
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
type APIError struct {
	baseError
	HTTPStatus int
	ErrorCode  string
}

// TransportError means a network-level failure, a timeout, or a 5xx. The request
// may or may not have reached the API, so retry WITH THE SAME idempotency key: a
// retried key never creates a second payment. CreateCheckoutSessionWithRetry does
// exactly that.
//
// Err is the underlying cause when there was one, reachable with errors.Unwrap
// or errors.As.
type TransportError struct {
	baseError
	Err error
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

func newAuthError(code, message string) *AuthError {
	return &AuthError{baseError: baseError{Message: message}, ErrorCode: code}
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
