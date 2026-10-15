package dominaite

import (
	"context"
	"net/http"
	"testing"
)

// The billing address on the customer: the widget does not ask the payer for
// a field the session already carries, so each field must reach the gateway
// under its wire name, and an unset field must not be sent at all.

func TestCustomerBillingAddressIsSentInTheSignedBody(t *testing.T) {
	server, calls := newTestServer(t, successReply())
	client := newTestClient(t, server.URL)

	params := testParams()
	params.Customer = &Customer{
		FirstName:  "Ana",
		LastName:   "Kirova",
		Email:      "ana@example.com",
		Phone:      "+359888123456",
		Address:    "12 Vitosha Blvd",
		City:       "Sofia",
		PostalCode: "1000",
		State:      "CA",
	}
	if _, err := client.CreateCheckoutSession(context.Background(), params); err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}

	call := calls()[0]
	want := `{"amount":2500,"currency":"EUR","orderReference":"order-1042",` +
		`"customer":{"firstName":"Ana","lastName":"Kirova","email":"ana@example.com","phone":"+359888123456",` +
		`"address":"12 Vitosha Blvd","city":"Sofia","postalCode":"1000","state":"CA"}}`
	if call.Body != want {
		t.Fatalf("body = %s\nwant   %s", call.Body, want)
	}
	assertSignedWith(t, call, http.MethodPost, SessionsPath, vector.IdempotencyKey)
}

func TestCustomerOmitsBillingAddressFieldsThatAreNotSet(t *testing.T) {
	server, calls := newTestServer(t, successReply())
	client := newTestClient(t, server.URL)

	params := testParams()
	params.Customer = &Customer{FirstName: "Ana", City: "Sofia"}
	if _, err := client.CreateCheckoutSession(context.Background(), params); err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}

	want := `{"amount":2500,"currency":"EUR","orderReference":"order-1042",` +
		`"customer":{"firstName":"Ana","city":"Sofia"}}`
	if body := calls()[0].Body; body != want {
		t.Fatalf("body = %s\nwant   %s", body, want)
	}
}
