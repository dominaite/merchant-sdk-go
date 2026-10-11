package dominaite

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"testing"
)

func testTransactionPage(nextCursor any) map[string]any {
	return map[string]any{
		"items": []any{
			map[string]any{
				"transactionId":  "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d",
				"orderReference": "order-1042",
				"type":           "payment",
				"status":         "refunded",
				"amount":         2500,
				"currency":       "EUR",
				"createdAt":      "2026-10-01T09:30:00Z",
				"completedAt":    "2026-10-01T09:31:00Z",
				"refundedAmount": 2500,
			},
			map[string]any{
				"transactionId":         "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5e",
				"type":                  "charge",
				"status":                "succeeded",
				"amount":                990,
				"currency":              "EUR",
				"createdAt":             "2026-10-02T09:30:00Z",
				"storedPaymentMethodId": "pm_0123456789abcdef0123456789abcdef",
			},
		},
		"nextCursor": nextCursor,
	}
}

// The query string carries the window, but the signature covers the path
// only: the API signs its canonical path, never the inbound URL.
func TestListTransactionsSignsThePathWithoutTheQuery(t *testing.T) {
	server, calls := newTestServer(t, dataReply(http.StatusOK, testTransactionPage(nil)))
	client := newTestClient(t, server.URL)

	params := ListTransactionsParams{
		From:   "2026-10-01T00:00:00Z",
		To:     "2026-10-08T00:00:00+02:00",
		Status: StatusSucceeded,
		Limit:  100,
		Cursor: "eyJjIjoiMjAyNi0xMC0wMSJ9+/=",
	}
	if _, err := client.ListTransactions(context.Background(), params); err != nil {
		t.Fatalf("ListTransactions: %v", err)
	}

	call := calls()[0]
	if call.Method != http.MethodGet || call.Path != TransactionsPath || call.Body != "" {
		t.Fatalf("got %s %s with body %q", call.Method, call.Path, call.Body)
	}
	if call.Header.Get("Idempotency-Key") != "" {
		t.Error("a GET sends no Idempotency-Key")
	}
	assertSignedWith(t, call, http.MethodGet, TransactionsPath, "")

	// Every value arrives intact, the + of an offset and the cursor's
	// base64 characters included.
	query, err := url.ParseQuery(call.Query)
	if err != nil {
		t.Fatalf("query %q: %v", call.Query, err)
	}
	want := map[string]string{
		"from":   params.From,
		"to":     params.To,
		"status": "succeeded",
		"limit":  "100",
		"cursor": params.Cursor,
	}
	for key, value := range want {
		if query.Get(key) != value {
			t.Errorf("%s = %q, want %q (query %q)", key, query.Get(key), value, call.Query)
		}
	}
}

func TestListTransactionsSendsOnlyTheWindowByDefault(t *testing.T) {
	server, calls := newTestServer(t, dataReply(http.StatusOK, testTransactionPage(nil)))
	client := newTestClient(t, server.URL)

	_, err := client.ListTransactions(context.Background(), ListTransactionsParams{From: "2026-10-01", To: "2026-10-08"})
	if err != nil {
		t.Fatalf("ListTransactions: %v", err)
	}
	if want := "from=2026-10-01&to=2026-10-08"; calls()[0].Query != want {
		t.Fatalf("query = %q, want %q", calls()[0].Query, want)
	}
}

func TestListTransactionsReadsThePage(t *testing.T) {
	server, _ := newTestServer(t, dataReply(http.StatusOK, testTransactionPage("next-page")))
	client := newTestClient(t, server.URL)

	page, err := client.ListTransactions(context.Background(), ListTransactionsParams{From: "2026-10-01T00:00:00Z", To: "2026-10-08T00:00:00Z"})
	if err != nil {
		t.Fatalf("ListTransactions: %v", err)
	}
	if page.NextCursor != "next-page" || len(page.Items) != 2 || len(page.Raw) == 0 {
		t.Fatalf("page = %+v", page)
	}
	payment, charge := page.Items[0], page.Items[1]
	if payment.Type != TransactionTypePayment || payment.Status != StatusRefunded || payment.RefundedAmount != 2500 ||
		payment.OrderReference != "order-1042" || payment.CompletedAt == "" || payment.StoredPaymentMethodID != "" {
		t.Errorf("payment = %+v", payment)
	}
	if charge.Type != TransactionTypeCharge || charge.Amount != 990 || charge.Currency != "EUR" ||
		charge.StoredPaymentMethodID != "pm_0123456789abcdef0123456789abcdef" || charge.RefundedAmount != 0 || charge.CompletedAt != "" {
		t.Errorf("charge = %+v", charge)
	}
	if len(charge.Raw) == 0 || string(charge.Raw) == string(payment.Raw) {
		t.Errorf("each item keeps its own Raw")
	}
}

func TestTheLastTransactionPageHasNoCursor(t *testing.T) {
	server, _ := newTestServer(t, dataReply(http.StatusOK, map[string]any{"items": []any{}, "nextCursor": nil}))
	client := newTestClient(t, server.URL)

	page, err := client.ListTransactions(context.Background(), ListTransactionsParams{From: "2026-10-01T00:00:00Z", To: "2026-10-08T00:00:00Z"})
	if err != nil {
		t.Fatalf("ListTransactions: %v", err)
	}
	if page.NextCursor != "" || page.Items == nil || len(page.Items) != 0 {
		t.Fatalf("page = %+v", page)
	}
}

func TestListTransactionsWithoutAWindowIsRefused(t *testing.T) {
	server, calls := newTestServer(t, dataReply(http.StatusOK, testTransactionPage(nil)))
	client := newTestClient(t, server.URL)

	for name, params := range map[string]ListTransactionsParams{
		"no from": {To: "2026-10-08T00:00:00Z"},
		"no to":   {From: "2026-10-01T00:00:00Z"},
	} {
		_, err := client.ListTransactions(context.Background(), params)
		var validationErr *ValidationError
		if !errors.As(err, &validationErr) {
			t.Errorf("%s: got %v, want *ValidationError", name, err)
		}
	}
	if len(calls()) != 0 {
		t.Fatal("a refused list must never reach the network")
	}
}

func TestListTransactionsRejectionsKeepTheirErrors(t *testing.T) {
	window := ListTransactionsParams{From: "2026-10-01T00:00:00Z", To: "2026-12-01T00:00:00Z"}

	refused := gatewayError(http.StatusBadRequest, "VALIDATION_ERROR", "from and to may be at most 31 days apart", nil)
	refused.Body.(map[string]any)["error"].(map[string]any)["validationErrors"] = []any{
		map[string]any{"field": "to", "code": "VALUE_OUT_OF_RANGE", "message": "from and to may be at most 31 days apart"},
	}
	tooLong, _ := newTestServer(t, refused)
	_, err := newTestClient(t, tooLong.URL).ListTransactions(context.Background(), window)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.HTTPStatus != http.StatusBadRequest || apiErr.ErrorCode != "VALIDATION_ERROR" {
		t.Fatalf("got %v, want a 400 *APIError", err)
	}
	var envelope struct {
		Error struct {
			ValidationErrors []struct {
				Field string `json:"field"`
				Code  string `json:"code"`
			} `json:"validationErrors"`
		} `json:"error"`
	}
	if json.Unmarshal(apiErr.Raw, &envelope) != nil || len(envelope.Error.ValidationErrors) != 1 ||
		envelope.Error.ValidationErrors[0].Code != "VALUE_OUT_OF_RANGE" {
		t.Fatalf("Raw = %s, want the field errors", apiErr.Raw)
	}

	noScope, _ := newTestServer(t, gatewayError(http.StatusForbidden, ErrorCodeInsufficientScope, "This API key does not have the transactions:read scope.", nil))
	_, err = newTestClient(t, noScope.URL).ListTransactions(context.Background(), window)
	var authErr *AuthError
	if !errors.As(err, &authErr) || authErr.ErrorCode != ErrorCodeInsufficientScope {
		t.Fatalf("got %v, want a 403 *AuthError", err)
	}
}
