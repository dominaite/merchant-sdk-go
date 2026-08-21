package dominaite

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// SignInput is everything that goes into one request signature.
type SignInput struct {
	// Secret is your API secret (dms_...).
	Secret string
	// Timestamp is unix SECONDS, as sent in X-Timestamp.
	Timestamp string
	// Method is the HTTP method. Uppercased before signing.
	Method string
	// Path is the canonical path only: no host, no query string.
	Path string
	// IdempotencyKey is the Idempotency-Key header value. Empty string for GET.
	IdempotencyKey string
	// Body is the exact request body string that will be sent. Empty string for GET.
	Body string
}

// String keeps the secret out of your logs. Debugging an INVALID_SIGNATURE
// usually means printing the input that produced it, so printing a SignInput
// with %v, %+v or %#v (value or pointer) shows every field except the secret.
func (in SignInput) String() string {
	return fmt.Sprintf(
		"dominaite.SignInput{Secret: %s, Timestamp: %q, Method: %q, Path: %q, IdempotencyKey: %q, Body: %q}",
		redactSecret(in.Secret), in.Timestamp, in.Method, in.Path, in.IdempotencyKey, in.Body,
	)
}

// GoString covers %#v, which does not use String.
func (in SignInput) GoString() string { return in.String() }

// Sign builds the X-Signature value for one request: lowercase hex HMAC-SHA256
// over
//
//	"{timestamp}\n{METHOD}\n{path}\n{idempotencyKey}\n{sha256hex(body)}"
//
// The idempotency key is INSIDE the signature, so a captured request cannot be
// replayed with a different key to mint extra sessions. The server rejects
// timestamps more than 5 minutes off, so keep your server clock on NTP.
//
// The client signs for you. Sign is exported so you can pin the recipe against
// the offline test vector before calling the live API, and so you can debug an
// INVALID_SIGNATURE without reading this package's source.
func Sign(in SignInput) string {
	bodyHash := sha256.Sum256([]byte(in.Body))

	payload := strings.Join([]string{
		in.Timestamp,
		strings.ToUpper(in.Method),
		in.Path,
		in.IdempotencyKey,
		hex.EncodeToString(bodyHash[:]),
	}, "\n")

	mac := hmac.New(sha256.New, []byte(in.Secret))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}
