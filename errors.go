package dominaite

import "errors"

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
//   - IDEMPOTENCY_KEY_REUSED: same key sent with a DIFFERENT body; use a fresh key.
//
// Never blind-retry a refusal. It will not change on its own.
type RefusalError struct {
	baseError
	ErrorCode string
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
