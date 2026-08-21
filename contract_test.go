package dominaite

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// testdata/merchant-api-contract.json is the CANONICAL merchant-API response
// contract, vendored byte-identical into every Dominaite SDK. A failure in this
// file means this SDK has drifted from the gateway - fix the SDK, never the
// fixture. The fixture only moves after the matching gateway DTO change lands.
const contractPath = "testdata/merchant-api-contract.json"

type endpointContract struct {
	Method         string          `json:"method"`
	Path           string          `json:"path"`
	Fields         []string        `json:"fields"`
	CheckoutFields []string        `json:"checkoutFields"`
	Example        json.RawMessage `json:"example"`
	SuccessExample json.RawMessage `json:"successExample"`
	RefusalExample json.RawMessage `json:"refusalExample"`
}

type responseContract struct {
	Version                  string   `json:"version"`
	StatusVocabulary         []string `json:"statusVocabulary"`
	SessionRefusalErrorCodes []string `json:"sessionRefusalErrorCodes"`
	ValidationErrorCodes     []string `json:"validationErrorCodes"`
	Endpoints                struct {
		Ping                  endpointContract `json:"ping"`
		CreateCheckoutSession endpointContract `json:"createCheckoutSession"`
		GetStatus             endpointContract `json:"getStatus"`
	} `json:"endpoints"`
}

func loadContract(t *testing.T) responseContract {
	t.Helper()

	raw, err := os.ReadFile(contractPath)
	if err != nil {
		t.Fatalf("read %s: %v", contractPath, err)
	}
	var contract responseContract
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatalf("parse %s: %v", contractPath, err)
	}
	if contract.Version != "v1" {
		t.Fatalf("contract version = %q, want v1 - this suite pins v1", contract.Version)
	}
	return contract
}

// jsonFieldNames is the set of wire field names a struct models. Fields tagged
// "-" are SDK-side only (Raw) and are not part of the wire contract.
func jsonFieldNames(t *testing.T, value any) []string {
	t.Helper()

	structType := reflect.TypeOf(value)
	if structType.Kind() != reflect.Struct {
		t.Fatalf("%T is not a struct", value)
	}

	names := []string{}
	for i := 0; i < structType.NumField(); i++ {
		field := structType.Field(i)
		tag := field.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "" {
			// An untagged exported field still lands on the wire under its Go
			// name, so it counts against the contract.
			name = field.Name
		}
		names = append(names, name)
	}
	return names
}

// assertSameFields compares two field sets by content, ignoring order, and
// names both directions of the difference so a failure says what to do.
func assertSameFields(t *testing.T, what string, got, want []string) {
	t.Helper()

	gotSet, wantSet := map[string]bool{}, map[string]bool{}
	for _, name := range got {
		if gotSet[name] {
			t.Errorf("%s: duplicate field %q", what, name)
		}
		gotSet[name] = true
	}
	for _, name := range want {
		wantSet[name] = true
	}

	var missing, extra []string
	for name := range wantSet {
		if !gotSet[name] {
			missing = append(missing, name)
		}
	}
	for name := range gotSet {
		if !wantSet[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)

	if len(missing) > 0 {
		t.Errorf("%s: the contract has fields this SDK does not model: %v", what, missing)
	}
	if len(extra) > 0 {
		t.Errorf("%s: this SDK models fields the contract does not have: %v", what, extra)
	}
}

// The status vocabulary is the whole point of the enum: a value that exists in
// one SDK and not another is how a merchant's switch statement silently stops
// covering a real payment state.
func TestStatusVocabularyMatchesContract(t *testing.T) {
	contract := loadContract(t)

	if len(contract.StatusVocabulary) != 10 {
		t.Fatalf("contract has %d statuses, want 10", len(contract.StatusVocabulary))
	}
	assertSameFields(t, "Statuses", Statuses, contract.StatusVocabulary)

	// Each named constant is pinned to its wire value, so renaming a constant
	// or editing its string fails here rather than at a merchant's switch.
	named := map[string]string{
		"StatusPending":           StatusPending,
		"StatusProcessing":        StatusProcessing,
		"StatusSucceeded":         StatusSucceeded,
		"StatusFailed":            StatusFailed,
		"StatusRefunded":          StatusRefunded,
		"StatusPartiallyRefunded": StatusPartiallyRefunded,
		"StatusCancelled":         StatusCancelled,
		"StatusDisputed":          StatusDisputed,
		"StatusRequiresCapture":   StatusRequiresCapture,
		"StatusAbandoned":         StatusAbandoned,
	}
	inVocabulary := map[string]bool{}
	for _, status := range contract.StatusVocabulary {
		inVocabulary[status] = true
	}
	for constant, value := range named {
		if !inVocabulary[value] {
			t.Errorf("%s = %q, which is not in the contract vocabulary", constant, value)
		}
	}
	if len(named) != len(contract.StatusVocabulary) {
		t.Errorf("%d Status* constants for %d contract statuses", len(named), len(contract.StatusVocabulary))
	}

	// Statuses must stay in sync with the constants, not just with the fixture.
	assertSameFields(t, "Statuses vs Status* constants", Statuses, values(named))
}

func values(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, value := range m {
		out = append(out, value)
	}
	return out
}

func TestPingResponseMatchesContract(t *testing.T) {
	contract := loadContract(t)
	endpoint := contract.Endpoints.Ping

	if endpoint.Path != PingPath {
		t.Errorf("PingPath = %q, contract says %q", PingPath, endpoint.Path)
	}
	assertSameFields(t, "Ping", jsonFieldNames(t, Ping{}), endpoint.Fields)

	// The example goes through the real client, so the parsing path under test
	// is the one merchants actually run.
	server, _ := newTestServer(t, reply{Body: string(endpoint.Example)})
	ping, err := newTestClient(t, server.URL).Ping(context.Background())
	if err != nil {
		t.Fatalf("the contract's ping example must parse: %v", err)
	}

	if !ping.Pong {
		t.Error("Pong = false")
	}
	if ping.MerchantID != "6f2b6a1e-0c4d-4e8a-9b1c-2d3e4f5a6b70" {
		t.Errorf("MerchantID = %q", ping.MerchantID)
	}
	if ping.ServerTime != "2026-08-21T09:15:30.000Z" {
		t.Errorf("ServerTime = %q", ping.ServerTime)
	}
	if ping.ServerUnixTime != 1755767730 {
		t.Errorf("ServerUnixTime = %d", ping.ServerUnixTime)
	}
	// Skew is the number merchants act on; it must survive as a number, not
	// arrive as a zero because the field was typed wrong.
	if ping.ClockSkewSeconds != 2 {
		t.Errorf("ClockSkewSeconds = %d, want 2", ping.ClockSkewSeconds)
	}
}

func TestCreateCheckoutSessionResponseMatchesContract(t *testing.T) {
	contract := loadContract(t)
	endpoint := contract.Endpoints.CreateCheckoutSession

	if endpoint.Path != SessionsPath {
		t.Errorf("SessionsPath = %q, contract says %q", SessionsPath, endpoint.Path)
	}
	assertSameFields(t, "create-session envelope", jsonFieldNames(t, checkoutSessionEnvelope{}), endpoint.Fields)
	assertSameFields(t, "CheckoutSession", jsonFieldNames(t, CheckoutSession{}), endpoint.CheckoutFields)

	server, _ := newTestServer(t, reply{Body: string(endpoint.SuccessExample)})
	session, err := newTestClient(t, server.URL).CreateCheckoutSession(context.Background(), testParams())
	if err != nil {
		t.Fatalf("the contract's success example must parse: %v", err)
	}

	if session.TransactionID != "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0" {
		t.Errorf("TransactionID = %q", session.TransactionID)
	}
	if session.OrderID != "dom_9a8b7c6d5e4f" {
		t.Errorf("OrderID = %q", session.OrderID)
	}
	// These two are what the widget is rendered with. An empty one is a blank
	// checkout page.
	if session.CashierKey != "ck_live_2f3a4d5e6f708192" || session.CashierToken != "ctok_5e4f3a2b1c0d9e8f" {
		t.Errorf("cashier key %q, token %q", session.CashierKey, session.CashierToken)
	}
	if session.Amount != 8440 || session.Currency != "EUR" {
		t.Errorf("amount %d %s, want 8440 EUR", session.Amount, session.Currency)
	}
	if session.ExpiresAt != "2026-08-21T11:15:30.000Z" {
		t.Errorf("ExpiresAt = %q", session.ExpiresAt)
	}
}

// A refusal is HTTP 200 with success false. Reading it as a success is how a
// merchant ships an order nobody paid for, so it must come back as an error.
func TestCreateCheckoutSessionRefusalMatchesContract(t *testing.T) {
	contract := loadContract(t)
	endpoint := contract.Endpoints.CreateCheckoutSession

	server, _ := newTestServer(t, reply{Body: string(endpoint.RefusalExample)})
	session, err := newTestClient(t, server.URL).CreateCheckoutSession(context.Background(), testParams())
	if session != nil {
		t.Fatal("a refusal must not produce a session")
	}

	var refusal *RefusalError
	if !errors.As(err, &refusal) {
		t.Fatalf("got %T (%v), want *RefusalError", err, err)
	}
	if refusal.ErrorCode != "DUPLICATE_REQUEST" {
		t.Errorf("ErrorCode = %q, want DUPLICATE_REQUEST", refusal.ErrorCode)
	}
	if refusal.Error() != "An identical request was already processed." {
		t.Errorf("message = %q", refusal.Error())
	}
	// The named transaction is the reconciliation path: read it back with
	// GetStatus instead of minting a second payment for the same order.
	if refusal.TransactionID != "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0" {
		t.Errorf("TransactionID = %q, want the collided transaction", refusal.TransactionID)
	}
}

// Every refusal code in the contract must reach the caller unchanged. Codes are
// what merchants branch on; one collapsed to UNKNOWN turns a "retry later" into
// an unexplained failure.
func TestSessionRefusalErrorCodesAreRecognized(t *testing.T) {
	contract := loadContract(t)

	if len(contract.SessionRefusalErrorCodes) == 0 {
		t.Fatal("the contract lists no refusal codes")
	}

	for _, code := range contract.SessionRefusalErrorCodes {
		t.Run(code, func(t *testing.T) {
			body := map[string]any{
				"success":      false,
				"checkout":     nil,
				"errorCode":    code,
				"errorMessage": "refused",
			}
			server, _ := newTestServer(t, reply{Body: body})
			_, err := newTestClient(t, server.URL).CreateCheckoutSession(context.Background(), testParams())

			var refusal *RefusalError
			if !errors.As(err, &refusal) {
				t.Fatalf("got %T (%v), want *RefusalError", err, err)
			}
			if refusal.ErrorCode != code {
				t.Fatalf("ErrorCode = %q, want %q", refusal.ErrorCode, code)
			}
			if !errors.Is(err, ErrDominaite) {
				t.Error("must match errors.Is(err, ErrDominaite)")
			}
		})
	}
}

// Validation codes are a DIFFERENT shape from refusals: HTTP 400, not a 200
// with success false. They must still arrive as a machine-readable code, or the
// only way to tell "you forgot the idempotency key" from any other 400 is to
// string-match the message.
func TestValidationErrorCodesAreRecognized(t *testing.T) {
	contract := loadContract(t)

	if len(contract.ValidationErrorCodes) == 0 {
		t.Fatal("the contract lists no validation codes")
	}

	for _, code := range contract.ValidationErrorCodes {
		// Both error shapes the gateway uses: the flat one and the nested
		// envelope. A code must survive either.
		shapes := map[string]any{
			"flat": map[string]any{
				"errorCode":    code,
				"errorMessage": "rejected",
			},
			"envelope": map[string]any{
				"success": false,
				"error":   map[string]any{"code": code, "message": "rejected"},
			},
		}

		for shape, body := range shapes {
			t.Run(code+"/"+shape, func(t *testing.T) {
				server, _ := newTestServer(t, reply{Status: 400, Body: body})
				_, err := newTestClient(t, server.URL).CreateCheckoutSession(context.Background(), testParams())

				var apiErr *APIError
				if !errors.As(err, &apiErr) {
					t.Fatalf("got %T (%v), want *APIError", err, err)
				}
				if apiErr.HTTPStatus != 400 {
					t.Errorf("HTTPStatus = %d, want 400", apiErr.HTTPStatus)
				}
				if apiErr.ErrorCode != code {
					t.Errorf("ErrorCode = %q, want %q", apiErr.ErrorCode, code)
				}
				// A validation failure is not a refusal: the request never
				// created anything, so retrying the same key cannot help.
				var refusal *RefusalError
				if errors.As(err, &refusal) {
					t.Error("a 400 must not surface as a *RefusalError")
				}
				if !errors.Is(err, ErrDominaite) {
					t.Error("must match errors.Is(err, ErrDominaite)")
				}
			})
		}
	}
}

func TestGetStatusResponseMatchesContract(t *testing.T) {
	contract := loadContract(t)
	endpoint := contract.Endpoints.GetStatus

	assertSameFields(t, "CheckoutStatus", jsonFieldNames(t, CheckoutStatus{}), endpoint.Fields)

	server, _ := newTestServer(t, reply{Body: string(endpoint.Example)})
	status, err := newTestClient(t, server.URL).GetStatus(context.Background(), "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0")
	if err != nil {
		t.Fatalf("the contract's status example must parse: %v", err)
	}

	if status.TransactionID != "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0" {
		t.Errorf("TransactionID = %q", status.TransactionID)
	}
	if status.OrderID != "dom_9a8b7c6d5e4f" || status.OrderReference != "order-1042" {
		t.Errorf("orderId %q, orderReference %q", status.OrderID, status.OrderReference)
	}
	if status.Status != StatusSucceeded {
		t.Errorf("Status = %q, want %q", status.Status, StatusSucceeded)
	}
	if status.Amount != 8440 || status.Currency != "EUR" {
		t.Errorf("amount %d %s, want 8440 EUR", status.Amount, status.Currency)
	}
	if status.CreatedAt != "2026-08-21T09:15:30.000Z" || status.UpdatedAt != "2026-08-21T09:16:05.000Z" {
		t.Errorf("createdAt %q, updatedAt %q", status.CreatedAt, status.UpdatedAt)
	}
	// Nulls must land as zero values, never as the string "null" or an error.
	if status.RefundedAmount != 0 {
		t.Errorf("RefundedAmount = %d, want 0 for a null", status.RefundedAmount)
	}
	if status.ExpiresAt != "" {
		t.Errorf("ExpiresAt = %q, want empty for a null", status.ExpiresAt)
	}
}
