package dominaite

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// webhookVector is the canonical cross-SDK webhook vector from the wire
// contract. Every Dominaite SDK pins these exact bytes, so a failure here means
// this SDK has drifted from the gateway and from the other SDKs - not that the
// test needs updating. The secret is a dummy and authenticates nothing.
//
// The body is byte-exact. Do NOT reformat, re-indent, or re-marshal it: the
// signature covers the bytes, so a single changed space invalidates the vector.
var webhookVector = struct {
	Secret    string
	Timestamp string
	Body      string
	Header    string
}{
	Secret:    "whsec_abababababababababababababababababababababababababababababababab",
	Timestamp: "1755700000",
	Body:      `{"id":"7f9c24e5-1d1f-4c0a-9b6c-2f3a4d5e6f70","type":"payment.succeeded","createdAt":"2026-08-20T14:00:00Z","data":{"transactionId":"0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0","status":"succeeded","previousStatus":"pending","kind":"sale","amount":8440,"grossAmount":8701,"surchargeAmount":261,"currency":"EUR","originalTransactionId":null,"idempotencyKey":"order-123"}}`,
	Header:    "t=1755700000,v1=5305bcf1302fdaba8f8c19a20c899e916fb4d2a7d8d547c62529ff87c4697b72",
}

// atVectorTime pins the clock to the vector's own timestamp, so the freshness
// check passes no matter when the suite runs.
func atVectorTime() VerifyOption {
	return WithWebhookClock(func() time.Time { return time.Unix(1755700000, 0) })
}

// signWebhook reproduces the documented recipe independently of VerifyWebhook,
// so tests that need a valid MAC over other inputs do not borrow the
// implementation they are checking.
func signWebhook(secret, timestamp, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "." + body))
	return hex.EncodeToString(mac.Sum(nil))
}

// Case 0: the documented header is what the documented recipe produces. If this
// fails, the vector in the contract is wrong, and every case below is moot.
func TestWebhookVectorHeaderMatchesRecipe(t *testing.T) {
	want := "t=" + webhookVector.Timestamp + ",v1=" +
		signWebhook(webhookVector.Secret, webhookVector.Timestamp, webhookVector.Body)
	if want != webhookVector.Header {
		t.Fatalf("recomputed header = %s, want %s", want, webhookVector.Header)
	}
	if len(webhookVector.Body) != 363 {
		t.Fatalf("vector body is %d bytes, want 363 - it has been reformatted", len(webhookVector.Body))
	}
}

// Case 1: the canonical vector verifies, and the envelope parses as documented.
func TestVerifyWebhookAcceptsCanonicalVector(t *testing.T) {
	event, err := VerifyWebhook([]byte(webhookVector.Body), webhookVector.Header, webhookVector.Secret, atVectorTime())
	if err != nil {
		t.Fatalf("canonical vector must verify, got: %v", err)
	}

	if event.ID != "7f9c24e5-1d1f-4c0a-9b6c-2f3a4d5e6f70" {
		t.Errorf("ID = %q", event.ID)
	}
	if event.Type != EventPaymentSucceeded {
		t.Errorf("Type = %q, want %q", event.Type, EventPaymentSucceeded)
	}
	if event.CreatedAt != "2026-08-20T14:00:00Z" {
		t.Errorf("CreatedAt = %q", event.CreatedAt)
	}
	if event.Data.TransactionID != "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0" {
		t.Errorf("TransactionID = %q", event.Data.TransactionID)
	}
	if event.Data.Status != StatusSucceeded || event.Data.PreviousStatus != StatusPending {
		t.Errorf("status %q, previous %q", event.Data.Status, event.Data.PreviousStatus)
	}
	if event.Data.Kind != "sale" || event.Data.Currency != "EUR" {
		t.Errorf("kind %q, currency %q", event.Data.Kind, event.Data.Currency)
	}
	// Amount is what the merchant is paid; GrossAmount is the card movement.
	// Swapping these silently under-credits or over-credits every order.
	if event.Data.Amount != 8440 || event.Data.GrossAmount != 8701 {
		t.Errorf("amount %d, gross %d, want 8440 and 8701", event.Data.Amount, event.Data.GrossAmount)
	}
	if event.Data.SurchargeAmount == nil || *event.Data.SurchargeAmount != 261 {
		t.Errorf("SurchargeAmount = %v, want 261", event.Data.SurchargeAmount)
	}
	// null on the wire must arrive as empty, not as the string "null".
	if event.Data.OriginalTransactionID != "" {
		t.Errorf("OriginalTransactionID = %q, want empty", event.Data.OriginalTransactionID)
	}
	if event.Data.IdempotencyKey != "order-123" {
		t.Errorf("IdempotencyKey = %q", event.Data.IdempotencyKey)
	}
	if string(event.Raw) != webhookVector.Body {
		t.Error("Raw must be the verified bytes, unchanged")
	}
}

// A nil surcharge must stay distinct from a zero surcharge: "we do not know"
// and "there was none" are different facts about the money.
func TestVerifyWebhookKeepsNilSurchargeDistinctFromZero(t *testing.T) {
	body := `{"id":"a","type":"payment.succeeded","data":{"amount":100,"surchargeAmount":null}}`
	event := mustVerify(t, body)
	if event.Data.SurchargeAmount != nil {
		t.Fatalf("null surcharge became %d", *event.Data.SurchargeAmount)
	}

	body = `{"id":"a","type":"payment.succeeded","data":{"amount":100,"surchargeAmount":0}}`
	event = mustVerify(t, body)
	if event.Data.SurchargeAmount == nil || *event.Data.SurchargeAmount != 0 {
		t.Fatalf("zero surcharge became %v", event.Data.SurchargeAmount)
	}
}

// mustVerify signs an arbitrary body with the vector secret and verifies it.
func mustVerify(t *testing.T, body string) *WebhookEvent {
	t.Helper()
	header := "t=" + webhookVector.Timestamp + ",v1=" +
		signWebhook(webhookVector.Secret, webhookVector.Timestamp, body)
	event, err := VerifyWebhook([]byte(body), header, webhookVector.Secret, atVectorTime())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	return event
}

// Case 2: any single-byte change to the body fails. Checked at every byte
// position rather than one hand-picked spot, so no region of the payload is
// accidentally outside the MAC.
func TestVerifyWebhookRejectsSingleByteBodyTamper(t *testing.T) {
	// The money field, spelled out: 8440 -> 8441 is one byte and one cent.
	tampered := strings.Replace(webhookVector.Body, `"amount":8440`, `"amount":8441`, 1)
	if tampered == webhookVector.Body {
		t.Fatal("tamper did not apply - the vector body changed")
	}
	assertRejected(t, tampered, webhookVector.Header, webhookVector.Secret, WebhookReasonSignatureMismatch)

	original := []byte(webhookVector.Body)
	for i := range original {
		mutated := append([]byte(nil), original...)
		mutated[i] ^= 0x01
		_, err := VerifyWebhook(mutated, webhookVector.Header, webhookVector.Secret, atVectorTime())
		if err == nil {
			t.Fatalf("byte %d could be flipped without failing verification", i)
		}
	}
}

// Case 3: the right payload with the wrong secret fails.
func TestVerifyWebhookRejectsWrongSecret(t *testing.T) {
	wrong := "whsec_" + strings.Repeat("cd", 32)
	if len(wrong) != len(webhookVector.Secret) {
		t.Fatalf("test secret should be the same shape as the real one")
	}
	assertRejected(t, webhookVector.Body, webhookVector.Header, wrong, WebhookReasonSignatureMismatch)

	// An empty secret is a configuration mistake, not a forgery. It must still
	// be an ordinary rejection rather than a panic or a silent pass.
	assertRejected(t, webhookVector.Body, webhookVector.Header, "", WebhookReasonMalformedSignature)
}

// Case 4: a timestamp outside tolerance fails even though the MAC is valid.
// This is the replay defence, so it is tested with genuinely signed payloads.
func TestVerifyWebhookRejectsTimestampOutsideTolerance(t *testing.T) {
	// The vector's own MAC, checked far in the future: authentic, but stale.
	late := WithWebhookClock(func() time.Time { return time.Unix(1755700000+301, 0) })
	_, err := VerifyWebhook([]byte(webhookVector.Body), webhookVector.Header, webhookVector.Secret, late)
	assertReason(t, err, WebhookReasonTimestampOutOfTolerance)

	// Symmetric: a timestamp too far in the FUTURE is equally rejected, so a
	// forward-drifted sender cannot mint deliveries that stay valid for hours.
	early := WithWebhookClock(func() time.Time { return time.Unix(1755700000-301, 0) })
	_, err = VerifyWebhook([]byte(webhookVector.Body), webhookVector.Header, webhookVector.Secret, early)
	assertReason(t, err, WebhookReasonTimestampOutOfTolerance)

	// Just inside the window still passes, both directions.
	for _, skew := range []int64{-299, 299} {
		clock := WithWebhookClock(func() time.Time { return time.Unix(1755700000+skew, 0) })
		if _, err := VerifyWebhook([]byte(webhookVector.Body), webhookVector.Header, webhookVector.Secret, clock); err != nil {
			t.Errorf("skew %ds is inside the 300s window but was rejected: %v", skew, err)
		}
	}

	// A tampered body outside the window reports the forgery, not the clock:
	// the MAC is checked first, so a bad secret can never be misread as drift.
	tampered := strings.Replace(webhookVector.Body, `"amount":8440`, `"amount":9999`, 1)
	_, err = VerifyWebhook([]byte(tampered), webhookVector.Header, webhookVector.Secret, late)
	assertReason(t, err, WebhookReasonSignatureMismatch)
}

func TestVerifyWebhookToleranceIsConfigurable(t *testing.T) {
	late := WithWebhookClock(func() time.Time { return time.Unix(1755700000+3600, 0) })

	// An hour out fails at the default.
	_, err := VerifyWebhook([]byte(webhookVector.Body), webhookVector.Header, webhookVector.Secret, late)
	assertReason(t, err, WebhookReasonTimestampOutOfTolerance)

	// ...and passes when the caller widens the window.
	if _, err := VerifyWebhook([]byte(webhookVector.Body), webhookVector.Header, webhookVector.Secret, late, WithWebhookTolerance(2*time.Hour)); err != nil {
		t.Errorf("widened tolerance should accept: %v", err)
	}

	// A non-positive tolerance disables the freshness check entirely. This is
	// documented and deliberate, so it is pinned rather than left to drift.
	if _, err := VerifyWebhook([]byte(webhookVector.Body), webhookVector.Header, webhookVector.Secret, late, WithWebhookTolerance(0)); err != nil {
		t.Errorf("zero tolerance should disable the check: %v", err)
	}

	if DefaultWebhookTolerance != 300*time.Second {
		t.Errorf("DefaultWebhookTolerance = %v, want 300s per the wire contract", DefaultWebhookTolerance)
	}
}

// Case 5: malformed headers are rejected as ordinary errors. The point is that
// none of these panic and none come back as some other error type - a hostile
// sender must not be able to crash the handler with a crafted header.
func TestVerifyWebhookRejectsMalformedHeaders(t *testing.T) {
	validMAC := signWebhook(webhookVector.Secret, webhookVector.Timestamp, webhookVector.Body)

	headers := map[string]string{
		"empty":                  "",
		"whitespace only":        "   ",
		"garbage":                "not-a-signature",
		"missing t":              "v1=" + validMAC,
		"missing v1":             "t=" + webhookVector.Timestamp,
		"t only, no value":       "t=,v1=" + validMAC,
		"v1 only, no value":      "t=" + webhookVector.Timestamp + ",v1=",
		"v1 not hex":             "t=" + webhookVector.Timestamp + ",v1=zzzz",
		"v1 wrong length":        "t=" + webhookVector.Timestamp + ",v1=abcd",
		"non-numeric timestamp":  "t=yesterday,v1=" + validMAC,
		"conflicting timestamps": "t=1755700000,t=1755700001,v1=" + validMAC,
		"no separators":          "t1755700000v1" + validMAC,
		"reversed order":         "v1=" + validMAC + ";t=" + webhookVector.Timestamp,
		"json":                   `{"t":1755700000,"v1":"` + validMAC + `"}`,
		"only commas":            ",,,,",
	}

	for name, header := range headers {
		t.Run(name, func(t *testing.T) {
			_, err := VerifyWebhook([]byte(webhookVector.Body), header, webhookVector.Secret, atVectorTime())
			if err == nil {
				t.Fatalf("header %q must be rejected", header)
			}
			var verr *WebhookVerificationError
			if !errors.As(err, &verr) {
				t.Fatalf("got %T, want *WebhookVerificationError", err)
			}
			if verr.Reason != WebhookReasonMalformedSignature {
				t.Fatalf("Reason = %q, want %q", verr.Reason, WebhookReasonMalformedSignature)
			}
			// Sealed into the SDK's error family like every other error here.
			if !errors.Is(err, ErrDominaite) {
				t.Error("must match errors.Is(err, ErrDominaite)")
			}
			var sdkErr Error
			if !errors.As(err, &sdkErr) {
				t.Error("must satisfy the dominaite.Error interface")
			}
		})
	}
}

// A timestamp of pure digits is well-formed however absurd its value, so the
// parser passes it through and the MAC is what judges it. Previously the parser
// rejected anything that would not fit an int64, which is a value judgement it
// has no business making.
func TestVerifyWebhookOversizedTimestampFailsOnTheMAC(t *testing.T) {
	header := "t=99999999999999999999999,v1=" +
		signWebhook(webhookVector.Secret, webhookVector.Timestamp, webhookVector.Body)
	_, err := VerifyWebhook([]byte(webhookVector.Body), header, webhookVector.Secret, atVectorTime())
	assertReason(t, err, WebhookReasonSignatureMismatch)
}

// Elements the scheme does not define yet are ignored rather than rejected, so
// adding a v2 alongside v1 server-side will not break deployed merchants.
// Element order does not matter either; whitespace and repeats do, and are
// pinned in TestVerifyWebhookHeaderGrammarVectors.
func TestVerifyWebhookIgnoresUnknownHeaderElements(t *testing.T) {
	validMAC := signWebhook(webhookVector.Secret, webhookVector.Timestamp, webhookVector.Body)
	headers := []string{
		"t=" + webhookVector.Timestamp + ",v1=" + validMAC + ",v2=deadbeef",
		"v1=" + validMAC + ",t=" + webhookVector.Timestamp,
		// An unknown key may itself repeat, and its value is never inspected.
		"t=" + webhookVector.Timestamp + ",v2=,v1=" + validMAC + ",v2=zzz",
	}
	for _, header := range headers {
		if _, err := VerifyWebhook([]byte(webhookVector.Body), header, webhookVector.Secret, atVectorTime()); err != nil {
			t.Errorf("header %q should verify: %v", header, err)
		}
	}
}

// A valid signature over bytes that are not JSON is authentic but unusable. It
// must surface as its own reason, never as a signature failure.
func TestVerifyWebhookReportsMalformedPayloadSeparately(t *testing.T) {
	body := "not json at all"
	header := "t=" + webhookVector.Timestamp + ",v1=" + signWebhook(webhookVector.Secret, webhookVector.Timestamp, body)
	_, err := VerifyWebhook([]byte(body), header, webhookVector.Secret, atVectorTime())
	assertReason(t, err, WebhookReasonMalformedPayload)
}

// An empty body is a legitimate thing to receive and must not panic.
func TestVerifyWebhookHandlesEmptyAndNilPayloads(t *testing.T) {
	header := "t=" + webhookVector.Timestamp + ",v1=" + signWebhook(webhookVector.Secret, webhookVector.Timestamp, "")
	for _, payload := range [][]byte{nil, {}} {
		_, err := VerifyWebhook(payload, header, webhookVector.Secret, atVectorTime())
		assertReason(t, err, WebhookReasonMalformedPayload)
	}
}

func assertRejected(t *testing.T, body, header, secret, wantReason string) {
	t.Helper()
	_, err := VerifyWebhook([]byte(body), header, secret, atVectorTime())
	assertReason(t, err, wantReason)
}

func assertReason(t *testing.T, err error, wantReason string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a %s rejection, got nil", wantReason)
	}
	var verr *WebhookVerificationError
	if !errors.As(err, &verr) {
		t.Fatalf("got %T (%v), want *WebhookVerificationError", err, err)
	}
	if verr.Reason != wantReason {
		t.Fatalf("Reason = %q, want %q", verr.Reason, wantReason)
	}
}

// The doc example must stay compilable and correct.
func ExampleVerifyWebhook() {
	event, err := VerifyWebhook(
		[]byte(webhookVector.Body),
		webhookVector.Header,
		webhookVector.Secret,
		atVectorTime(),
	)
	if err != nil {
		fmt.Println("rejected:", err)
		return
	}
	fmt.Println(event.Type, event.Data.Amount, event.Data.Currency)
	// Output: payment.succeeded 8440 EUR
}
