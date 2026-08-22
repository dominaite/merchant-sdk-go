package dominaite

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newRawServer serves one handler that gets to write its own status, headers
// and body. newTestServer always sends JSON with no extra headers, which is
// exactly what these tests need to get away from.
func newRawServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

// A 5xx from an overloaded edge is often an HTML error page or an empty body,
// not the API's JSON. It has to stay a retryable transport failure: classifying
// it on the body would turn a load-balancer blip into a permanent error the
// caller never retries, and the payment may well have landed.
func TestNonJSONServerErrorIsRetryable(t *testing.T) {
	bodies := map[string]string{
		"html":      "<html><head><title>503 Service Unavailable</title></head><body><h1>503</h1></body></html>",
		"empty":     "",
		"plaintext": "upstream connect error or disconnect/reset before headers",
		"jsonarray": `["not an object"]`,
	}

	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			server := newRawServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(body))
			})
			client := newTestClient(t, server.URL)

			_, err := client.CreateCheckoutSession(context.Background(), testParams())

			var transportErr *TransportError
			if !errors.As(err, &transportErr) {
				t.Fatalf("got %v, want *TransportError", err)
			}
			var apiErr *APIError
			if errors.As(err, &apiErr) {
				t.Fatal("a non-JSON 503 must not be reported as a permanent APIError")
			}
		})
	}
}

// The same body on a 200 stays an APIError: there is a real response expected
// here and nothing to retry.
func TestNonJSONSuccessIsAnAPIError(t *testing.T) {
	server := newRawServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html>hello</html>"))
	})
	client := newTestClient(t, server.URL)

	_, err := client.CreateCheckoutSession(context.Background(), testParams())

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("got %v, want *APIError", err)
	}
	if apiErr.HTTPStatus != http.StatusOK {
		t.Fatalf("HTTPStatus = %d, want 200", apiErr.HTTPStatus)
	}
}

// A non-JSON 401 is still an auth failure. Whatever the body is, the status is
// the API saying the credentials did not work.
func TestNonJSONAuthFailureIsAnAuthError(t *testing.T) {
	server := newRawServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("<html>401 Unauthorized</html>"))
	})
	client := newTestClient(t, server.URL)

	_, err := client.CreateCheckoutSession(context.Background(), testParams())

	var authErr *AuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("got %v, want *AuthError", err)
	}
}

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

// Whatever is answering does not get to decide how much memory this process
// uses. Past the cap the read fails, and it fails as retryable rather than
// handing a truncated payload to the JSON parser.
func TestOversizedResponseBodyIsRetryable(t *testing.T) {
	chunk := strings.Repeat("a", 1<<20)
	server := newRawServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true,"checkout":{"padding":"`))
		for i := 0; i < 11; i++ { // 11MB, past the 10MB cap
			if _, err := w.Write([]byte(chunk)); err != nil {
				return // the client hung up at the cap, which is the point
			}
		}
		_, _ = w.Write([]byte(`"}}`))
	})
	client := newTestClient(t, server.URL)

	_, err := client.CreateCheckoutSession(context.Background(), testParams())

	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("got %v, want *TransportError", err)
	}
	if !strings.Contains(err.Error(), "Could not read") {
		t.Fatalf("unexpected message: %v", err)
	}
}

// A response that fits under the cap is read whole, so the limit does not
// quietly truncate a large but legitimate payload.
func TestResponseUnderTheCapIsReadWhole(t *testing.T) {
	padding := strings.Repeat("a", 1<<20) // 1MB of filler inside a valid envelope
	server := newRawServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"success":true,"checkout":{"transactionId":%q,"cashierToken":"ct_1","padding":%q}}`,
			testCheckout["transactionId"], padding)
	})
	client := newTestClient(t, server.URL)

	session, err := client.CreateCheckoutSession(context.Background(), testParams())
	if err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}
	if session.CashierToken != "ct_1" {
		t.Fatalf("CashierToken = %q", session.CashierToken)
	}
}
