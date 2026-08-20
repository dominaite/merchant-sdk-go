package dominaite

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// vector is the known-answer vector shared with the gateway's
// MerchantApiRequestAuthenticator and the dashboard's Website integration tab
// (web-platform SIGNING_TEST_VECTOR). The secret is a dummy and authenticates
// nothing.
//
// If these tests fail, the signing recipe drifted from the gateway and every
// merchant integration built on this SDK is broken.
var vector = struct {
	Secret         string
	Timestamp      string
	Method         string
	Path           string
	IdempotencyKey string
	Body           string
	BodySHA256     string
	Signature      string
}{
	Secret:         "dms_0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	Timestamp:      "1755302400",
	Method:         "POST",
	Path:           "/merchant-api/checkout/sessions",
	IdempotencyKey: "00000000-0000-4000-8000-000000000001",
	Body:           `{"amount":2500,"currency":"EUR","orderReference":"order-1042"}`,
	BodySHA256:     "aa3edd72cd1829f4e053abb048b08c1ae91c2d67b08955997c4b6c4dab4f98ff",
	Signature:      "95759958a0a0a9bd3e6e37101c01e8e7fee1166406e4ac2ff488764f5f742cbf",
}

func vectorInput() SignInput {
	return SignInput{
		Secret:         vector.Secret,
		Timestamp:      vector.Timestamp,
		Method:         vector.Method,
		Path:           vector.Path,
		IdempotencyKey: vector.IdempotencyKey,
		Body:           vector.Body,
	}
}

func TestVectorBodyHash(t *testing.T) {
	sum := sha256.Sum256([]byte(vector.Body))
	if got := hex.EncodeToString(sum[:]); got != vector.BodySHA256 {
		t.Fatalf("body sha256 = %s, want %s", got, vector.BodySHA256)
	}
}

func TestVectorSignature(t *testing.T) {
	if got := Sign(vectorInput()); got != vector.Signature {
		t.Fatalf("signature = %s, want %s", got, vector.Signature)
	}
}

func TestSignUppercasesMethod(t *testing.T) {
	in := vectorInput()
	in.Method = "post"
	if got := Sign(in); got != vector.Signature {
		t.Fatalf("lowercase method changed the signature: %s", got)
	}
}

func TestSignCoversTheIdempotencyKey(t *testing.T) {
	in := vectorInput()
	in.IdempotencyKey = "00000000-0000-4000-8000-000000000002"
	if got := Sign(in); got == vector.Signature {
		t.Fatal("a different idempotency key must produce a different signature")
	}
}

func TestSignGetShapeUsesEmptyKeyAndEmptyBody(t *testing.T) {
	const emptySHA = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	sum := sha256.Sum256(nil)
	if got := hex.EncodeToString(sum[:]); got != emptySHA {
		t.Fatalf("sha256 of the empty string = %s, want %s", got, emptySHA)
	}

	path := vector.Path + "/11111111-1111-4111-8111-111111111111"
	got := Sign(SignInput{
		Secret:    vector.Secret,
		Timestamp: vector.Timestamp,
		Method:    "GET",
		Path:      path,
	})

	// Recompute the documented payload independently rather than trusting Sign.
	payload := strings.Join([]string{vector.Timestamp, "GET", path, "", emptySHA}, "\n")
	mac := hmac.New(sha256.New, []byte(vector.Secret))
	mac.Write([]byte(payload))
	if want := hex.EncodeToString(mac.Sum(nil)); got != want {
		t.Fatalf("GET signature = %s, want %s", got, want)
	}
}
