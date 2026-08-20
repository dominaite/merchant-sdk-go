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
// For the specific kind, use errors.As with *RefusalError, *AuthError,
// *APIError, *TransportError or *ValidationError, or errors.As with the Error
// interface to catch any of them while keeping the message.
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
// Branch on ErrorCode:
//   - PAYMENT_PROCESSING_UNAVAILABLE: card payments are off right now; retry later.
//   - DUPLICATE_REQUEST: a session for this idempotency key is already open.
//   - ALREADY_PROCESSED: this idempotency key's payment already completed.
//   - PRIOR_ATTEMPT_FAILED: a prior attempt with this key failed terminally; use a fresh key.
//   - IDEMPOTENCY_KEY_REUSED: same key sent with a DIFFERENT body; use a fresh key.
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

// APIError means the API answered, but with an unexpected or rejecting response.
// HTTPStatus carries the code. A 422 means an idempotency key was replayed with
// a different body; use a fresh key. A 404 from GetStatus means an unknown
// transaction id.
type APIError struct {
	baseError
	HTTPStatus int
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

func newAuthError(code, message string) *AuthError {
	return &AuthError{baseError: baseError{Message: message}, ErrorCode: code}
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
