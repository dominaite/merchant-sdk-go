package dominaite

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestOrderIdempotencyKeyShape(t *testing.T) {
	key, err := OrderIdempotencyKey("checkout", "order-1042", 2500, "eur")
	if err != nil {
		t.Fatalf("OrderIdempotencyKey: %v", err)
	}
	if key != "checkout-order-1042-2500-EUR" {
		t.Fatalf("key = %q, want checkout-order-1042-2500-EUR", key)
	}
}

// The point of the helper: re-entering the same order gives the same key, so
// the gateway replays the open session; re-pricing it gives a new one.
func TestOrderIdempotencyKeyIsStablePerOrderAndAmount(t *testing.T) {
	first, _ := OrderIdempotencyKey("checkout", "order-1042", 2500, "EUR")
	again, _ := OrderIdempotencyKey("checkout", "order-1042", 2500, "eur")
	if first != again {
		t.Fatalf("same order and amount gave %q and %q", first, again)
	}

	for name, other := range map[string][4]any{
		"amount":   {"checkout", "order-1042", int64(2600), "EUR"},
		"currency": {"checkout", "order-1042", int64(2500), "BGN"},
		"order":    {"checkout", "order-1043", int64(2500), "EUR"},
		"scope":    {"checkout-save", "order-1042", int64(2500), "EUR"},
	} {
		key, err := OrderIdempotencyKey(other[0].(string), other[1].(string), other[2].(int64), other[3].(string))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if key == first {
			t.Fatalf("a different %s produced the same key %q", name, key)
		}
	}
}

func TestOrderIdempotencyKeyRejectsBadParts(t *testing.T) {
	cases := map[string]func() (string, error){
		"blank scope":        func() (string, error) { return OrderIdempotencyKey(" ", "order-1", 100, "EUR") },
		"blank order":        func() (string, error) { return OrderIdempotencyKey("checkout", "", 100, "EUR") },
		"zero amount":        func() (string, error) { return OrderIdempotencyKey("checkout", "order-1", 0, "EUR") },
		"negative amount":    func() (string, error) { return OrderIdempotencyKey("checkout", "order-1", -5, "EUR") },
		"short currency":     func() (string, error) { return OrderIdempotencyKey("checkout", "order-1", 100, "EU") },
		"digit currency":     func() (string, error) { return OrderIdempotencyKey("checkout", "order-1", 100, "E1R") },
		"non-ascii currency": func() (string, error) { return OrderIdempotencyKey("checkout", "order-1", 100, "eıu") },
		"over 100 chars":     func() (string, error) { return OrderIdempotencyKey("checkout", strings.Repeat("x", 90), 100, "EUR") },
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			key, err := build()
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("got key %q, err %v; want *ValidationError", key, err)
			}
		})
	}
}

// The limit is the SDK's own key rule, so a key exactly at 100 characters is
// accepted by the helper and by CreateCheckoutSession alike.
func TestOrderIdempotencyKeyAtTheLimitIsSendable(t *testing.T) {
	orderID := strings.Repeat("x", 100-len("checkout--2500-EUR"))
	key, err := OrderIdempotencyKey("checkout", orderID, 2500, "EUR")
	if err != nil {
		t.Fatalf("OrderIdempotencyKey: %v", err)
	}
	if len(key) != 100 {
		t.Fatalf("len(key) = %d, want 100", len(key))
	}

	server, calls := newTestServer(t, successReply())
	params := testParams()
	params.IdempotencyKey = key
	if _, err := newTestClient(t, server.URL).CreateCheckoutSession(context.Background(), params); err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}
	if got := calls()[0].Header.Get("Idempotency-Key"); got != key {
		t.Fatalf("Idempotency-Key = %q, want %q", got, key)
	}
}
