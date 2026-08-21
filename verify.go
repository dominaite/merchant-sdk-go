package dominaite

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// WebhookSignatureHeader is the header Dominaite signs each delivery with. An
// endpoint can be configured to use a different header name, but this is the
// default and the one the dashboard shows.
const WebhookSignatureHeader = "X-Webhook-Signature"

// DefaultWebhookTolerance is how far a delivery's timestamp may sit from your
// clock before VerifyWebhook rejects it. It matches the gateway's own window.
const DefaultWebhookTolerance = 5 * time.Minute

// VerifyOption tunes VerifyWebhook. The defaults are correct for production;
// you should not need any of these outside tests.
type VerifyOption func(*verifyConfig)

type verifyConfig struct {
	tolerance time.Duration
	now       func() time.Time
}

// WithWebhookTolerance overrides how far the delivery timestamp may sit from
// your clock. Defaults to DefaultWebhookTolerance (5 minutes).
//
// A zero or negative duration DISABLES the freshness check, which leaves you
// open to replay of any delivery ever captured. If you are reaching for it
// because deliveries keep failing, your server clock is wrong - fix NTP.
func WithWebhookTolerance(tolerance time.Duration) VerifyOption {
	return func(c *verifyConfig) { c.tolerance = tolerance }
}

// WithWebhookClock replaces the clock VerifyWebhook compares against. This
// exists so tests can pin a fixed instant against a recorded delivery; it has
// no use in production code. A nil function is ignored.
func WithWebhookClock(now func() time.Time) VerifyOption {
	return func(c *verifyConfig) {
		if now != nil {
			c.now = now
		}
	}
}

// VerifyWebhook authenticates one webhook delivery and returns the parsed
// event. Verification happens BEFORE parsing, and the only way to obtain a
// WebhookEvent is through this function, so an unverified payload cannot reach
// your business logic by accident.
//
// payload must be the RAW request body, exactly the bytes that arrived. Reading
// the body into a struct and re-encoding it will change them and the signature
// will not match. In net/http that means io.ReadAll(r.Body) before any
// json.Decode.
//
// signatureHeader is the WebhookSignatureHeader value: "t={unix_seconds},v1={hex}".
// secret is the endpoint's whsec_ secret, shown once when the endpoint was
// created.
//
// Every failure returns a *WebhookVerificationError carrying a WebhookReason*
// on .Reason. Nothing panics and no other error type comes out, so a malformed
// or hostile header is an ordinary rejection path:
//
//	body, err := io.ReadAll(r.Body)
//	if err != nil {
//		w.WriteHeader(http.StatusBadRequest)
//		return
//	}
//	event, err := dominaite.VerifyWebhook(body, r.Header.Get(dominaite.WebhookSignatureHeader), secret)
//	if err != nil {
//		w.WriteHeader(http.StatusBadRequest)
//		return
//	}
//	if !alreadySeen(event.ID) {   // delivery is at-least-once: dedupe on ID
//		enqueue(event)            // do the work off the request path
//	}
//	w.WriteHeader(http.StatusOK)  // answer fast; a slow 2xx counts as a failure
func VerifyWebhook(payload []byte, signatureHeader, secret string, opts ...VerifyOption) (*WebhookEvent, error) {
	cfg := verifyConfig{tolerance: DefaultWebhookTolerance, now: time.Now}
	for _, opt := range opts {
		opt(&cfg)
	}

	if secret == "" {
		return nil, newWebhookVerificationError(
			WebhookReasonMalformedSignature,
			"Webhook secret is empty - pass the endpoint's whsec_ secret.",
		)
	}

	timestamp, signature, err := parseSignatureHeader(signatureHeader)
	if err != nil {
		return nil, err
	}

	// Sign first, then compare in constant time. The signed payload is the
	// ASCII concatenation "{t}.{raw_body}", where {t} is the raw digits from
	// the header rather than a reparsed number.
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte{'.'})
	mac.Write(payload)
	expected := mac.Sum(nil)

	if !hmac.Equal(signature, expected) {
		return nil, newWebhookVerificationError(
			WebhookReasonSignatureMismatch,
			"Webhook signature does not match - wrong secret, or the payload is not the bytes that were signed.",
		)
	}

	// Only now is the timestamp trustworthy: it is covered by the MAC, so an
	// attacker cannot move it. Checking freshness after the MAC also keeps a
	// stale-but-authentic replay distinguishable from a forgery.
	if cfg.tolerance > 0 {
		seconds, convErr := strconv.ParseInt(timestamp, 10, 64)
		if convErr != nil {
			return nil, newWebhookVerificationError(
				WebhookReasonMalformedSignature,
				"Webhook signature timestamp is not an integer.",
			)
		}
		drift := cfg.now().Sub(time.Unix(seconds, 0))
		if drift < 0 {
			drift = -drift
		}
		if drift > cfg.tolerance {
			return nil, newWebhookVerificationError(
				WebhookReasonTimestampOutOfTolerance,
				"Webhook timestamp is outside the tolerance window - a replay, or your server clock has drifted.",
			)
		}
	}

	event := &WebhookEvent{}
	if err := json.Unmarshal(payload, event); err != nil {
		return nil, newWebhookVerificationError(
			WebhookReasonMalformedPayload,
			"Webhook signature was valid but the body is not valid JSON.",
		)
	}

	// Keep the verified bytes for fields the structs do not model yet.
	event.Raw = append(json.RawMessage(nil), payload...)
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &envelope); err == nil {
		event.Data.Raw = envelope.Data
	}

	return event, nil
}

// parseSignatureHeader splits "t={unix_seconds},v1={hex}" into the raw
// timestamp text and the decoded MAC, following the normative grammar every
// Dominaite SDK implements:
//
//   - comma-separated key=value elements, no whitespace anywhere. An element
//     with no "=" rejects the whole header.
//   - exactly one t and exactly one v1. Any repeat rejects, even when one of
//     the candidates carries a valid MAC.
//   - unknown keys are ignored, so a future scheme version shipped alongside v1
//     does not break deployed merchants.
//   - t is one or more ASCII digits and nothing else. The raw substring is what
//     gets signed, never a number parsed out of it and printed back.
//   - v1 is exactly 64 lowercase hex characters.
//
// Collecting several v1 candidates and accepting if any of them matches used to
// look like support for overlapping secret rotation. The platform does not do
// that: there is never more than one valid candidate on the wire, and the
// permissive read let a sender attach arbitrary extra elements next to the real
// MAC and still verify.
func parseSignatureHeader(header string) (string, []byte, error) {
	malformed := func() (string, []byte, error) {
		return "", nil, newWebhookVerificationError(
			WebhookReasonMalformedSignature,
			"Webhook signature header is missing or malformed - expected \"t=<unix_seconds>,v1=<hex>\".",
		)
	}

	if header == "" {
		return malformed()
	}

	var timestamp, signature string
	var haveTimestamp, haveSignature bool

	for _, element := range strings.Split(header, ",") {
		key, value, found := strings.Cut(element, "=")
		if !found {
			return malformed()
		}
		switch key {
		case "t":
			if haveTimestamp {
				return malformed()
			}
			timestamp, haveTimestamp = value, true
		case "v1":
			if haveSignature {
				return malformed()
			}
			signature, haveSignature = value, true
		}
	}

	if !haveTimestamp || !haveSignature {
		return malformed()
	}
	if !isASCIIDigits(timestamp) {
		return malformed()
	}
	if len(signature) != hex.EncodedLen(sha256.Size) || !isLowerHex(signature) {
		return malformed()
	}

	decoded, err := hex.DecodeString(signature)
	if err != nil {
		return malformed()
	}
	return timestamp, decoded, nil
}

func isASCIIDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// isLowerHex is deliberately case-sensitive. The platform only ever emits
// lowercase, so folding case would widen what this SDK accepts for nothing.
func isLowerHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
