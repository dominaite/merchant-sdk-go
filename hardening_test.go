package dominaite

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestBaseURLMustBeHTTPS(t *testing.T) {
	allowed := []string{
		"https://api.example.test/payments",
		"https://localhost:8443",
		"http://localhost:3000",
		"http://127.0.0.1:8080/payments",
		"http://[::1]:8080",
		"HTTP://LOCALHOST:3000",
	}
	for _, baseURL := range allowed {
		t.Run("allow "+baseURL, func(t *testing.T) {
			if _, err := New(testKeyID, vector.Secret, WithBaseURL(baseURL)); err != nil {
				t.Fatalf("New(%q): %v", baseURL, err)
			}
		})
	}

	refused := []string{
		"http://api.example.test/payments",
		// Not loopback: the host only starts with the word.
		"http://localhost.attacker.test",
		"http://127.0.0.1.attacker.test",
		// A non-loopback IP that happens to be private is still clear text.
		"http://10.0.0.5:8080",
		"ftp://api.example.test",
		"//api.example.test",
		"api.example.test",
	}
	for _, baseURL := range refused {
		t.Run("refuse "+baseURL, func(t *testing.T) {
			_, err := New(testKeyID, vector.Secret, WithBaseURL(baseURL))
			var validationErr *ValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("New(%q) = %v, want *ValidationError", baseURL, err)
			}
		})
	}
}

// The default is production, and production is https.
func TestDefaultBaseURLIsHTTPS(t *testing.T) {
	if !strings.HasPrefix(DefaultBaseURL, "https://") {
		t.Fatalf("DefaultBaseURL = %q, want https://", DefaultBaseURL)
	}
	if _, err := New(testKeyID, vector.Secret); err != nil {
		t.Fatalf("New with the default base URL: %v", err)
	}
}

// Length limits are in characters, the unit merchants and the dashboard use.
// Counting bytes would reject a valid 100-character Cyrillic reference at 200
// bytes, before the API ever saw it.
func TestLengthLimitsCountCharactersNotBytes(t *testing.T) {
	server, calls := newTestServer(t, successReply())
	client := newTestClient(t, server.URL)

	cyrillic := strings.Repeat("б", 100) // 100 characters, 200 bytes
	params := testParams()
	params.OrderReference = cyrillic

	if _, err := client.CreateCheckoutSession(context.Background(), params); err != nil {
		t.Fatalf("a 100-character Cyrillic orderReference must be accepted: %v", err)
	}
	if len(calls()) != 1 {
		t.Fatal("the request never reached the server")
	}

	params.OrderReference = strings.Repeat("б", 101)
	_, err := client.CreateCheckoutSession(context.Background(), params)
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("101 characters: got %v, want *ValidationError", err)
	}
}

func TestIdempotencyKeyLimitCountsCharacters(t *testing.T) {
	server, _ := newTestServer(t, successReply())
	client := newTestClient(t, server.URL)

	params := testParams()
	params.IdempotencyKey = strings.Repeat("é", 100) // 100 characters, 200 bytes

	if _, err := client.CreateCheckoutSession(context.Background(), params); err != nil {
		t.Fatalf("a 100-character idempotency key must be accepted: %v", err)
	}

	params.IdempotencyKey = strings.Repeat("é", 101)
	_, err := client.CreateCheckoutSession(context.Background(), params)
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("101 characters: got %v, want *ValidationError", err)
	}
}
