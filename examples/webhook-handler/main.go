// Command webhook-handler is a runnable webhook receiver. It is the runnable
// version of the README's Webhooks section.
//
//	export DOMINAITE_WEBHOOK_SECRET=whsec_...
//	go run ./examples/webhook-handler
//
// Then point a tunnel (or your dashboard endpoint) at http://localhost:8080/webhooks
// and send a test delivery from the Webhooks tab.
//
// The dedupe store here is an in-memory map, which is enough to demonstrate the
// shape and NOT enough for production: retries can arrive hours apart and a
// restart would forget every id it had seen. Use your database.
package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"

	dominaite "github.com/dominaite/merchant-sdk-go"
)

func main() {
	secret := os.Getenv("DOMINAITE_WEBHOOK_SECRET")
	if secret == "" {
		fmt.Fprintln(os.Stderr, "error: set DOMINAITE_WEBHOOK_SECRET to the endpoint's whsec_ secret")
		os.Exit(1)
	}

	http.Handle("/webhooks", &handler{secret: secret, seen: map[string]bool{}})

	log.Println("listening on :8080/webhooks")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type handler struct {
	secret string

	mu   sync.Mutex
	seen map[string]bool
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// The RAW bytes, before any decoding. The signature covers exactly these.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	event, err := dominaite.VerifyWebhook(body, r.Header.Get(dominaite.WebhookSignatureHeader), h.secret)
	if err != nil {
		// Log the reason for yourself. Never tell the caller which check failed.
		var verr *dominaite.WebhookVerificationError
		if errors.As(err, &verr) {
			log.Printf("rejected delivery: %s", verr.Reason)
		}
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// At-least-once delivery: the same id will sometimes arrive twice.
	if !h.firstTime(event.ID) {
		log.Printf("duplicate delivery %s ignored", event.ID)
		w.WriteHeader(http.StatusOK)
		return
	}

	switch event.Type {
	case dominaite.EventPaymentSucceeded:
		// The only signal that means money is in hand. Credit the order from
		// Amount (what you are paid), not GrossAmount (what the card was charged).
		log.Printf("paid: transaction=%s amount=%d %s order=%s",
			event.Data.TransactionID, event.Data.Amount, event.Data.Currency, event.Data.IdempotencyKey)
	case dominaite.EventPaymentRefunded:
		log.Printf("refunded: transaction=%s parent=%s amount=%d %s",
			event.Data.TransactionID, event.Data.OriginalTransactionID, event.Data.Amount, event.Data.Currency)
	case dominaite.EventPaymentFailed, dominaite.EventPaymentCancelled, dominaite.EventPaymentAbandoned:
		log.Printf("closed unpaid: transaction=%s type=%s", event.Data.TransactionID, event.Type)
	case dominaite.EventPaymentRequiresCapture:
		// Already paid, funds held awaiting capture. Not an abandoned order.
		log.Printf("awaiting capture: transaction=%s", event.Data.TransactionID)
	case dominaite.EventPaymentDisputed:
		log.Printf("disputed: transaction=%s", event.Data.TransactionID)
	default:
		// The catalog can grow. An unknown type is a no-op, and still a 2xx:
		// a 400 here would count as a failed delivery and trip the breaker.
		log.Printf("ignoring unknown type %q", event.Type)
	}

	// Real work belongs on a queue. Answer fast; a slow 2xx counts as a failure.
	w.WriteHeader(http.StatusOK)
}

// firstTime reports whether this delivery id has not been handled before.
func (h *handler) firstTime(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.seen[id] {
		return false
	}
	h.seen[id] = true
	return true
}
