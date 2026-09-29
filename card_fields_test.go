package dominaite

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func sentBody(t *testing.T, call recordedCall) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(call.Body), &body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	return body
}

// Asking for card fields must reach the gateway in the signed body, or the
// merchant gets a widget session and a page that cannot mount card fields.
func TestIntegrationFieldsIsSentInTheSignedBody(t *testing.T) {
	server, calls := newTestServer(t, successReply())
	client := newTestClient(t, server.URL)

	params := testParams()
	params.Integration = IntegrationFields
	if _, err := client.CreateCheckoutSession(context.Background(), params); err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}

	call := calls()[0]
	if got := sentBody(t, call)["integration"]; got != "fields" {
		t.Fatalf("integration = %v, want fields", got)
	}
	want := Sign(SignInput{
		Secret:         vector.Secret,
		Timestamp:      call.Header.Get("X-Timestamp"),
		Method:         http.MethodPost,
		Path:           SessionsPath,
		IdempotencyKey: vector.IdempotencyKey,
		Body:           call.Body,
	})
	if got := call.Header.Get("X-Signature"); got != want {
		t.Fatalf("X-Signature = %s, want %s", got, want)
	}
}

func TestIntegrationWidgetIsSentWhenSetExplicitly(t *testing.T) {
	server, calls := newTestServer(t, successReply())
	client := newTestClient(t, server.URL)

	params := testParams()
	params.Integration = IntegrationWidget
	if _, err := client.CreateCheckoutSession(context.Background(), params); err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}
	if got := sentBody(t, calls()[0])["integration"]; got != "widget" {
		t.Fatalf("integration = %v, want widget", got)
	}
}

// An unset Integration is omitted, not sent empty, so the signed bytes of every
// existing widget integration stay exactly the vector body.
func TestUnsetIntegrationIsOmittedFromTheBody(t *testing.T) {
	server, calls := newTestServer(t, successReply())
	client := newTestClient(t, server.URL)

	if _, err := client.CreateCheckoutSession(context.Background(), testParams()); err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}
	if got := calls()[0].Body; got != vector.Body {
		t.Fatalf("body = %s, want %s", got, vector.Body)
	}
}

func TestIntegrationVocabularyMatchesContract(t *testing.T) {
	contract := loadContract(t)

	got := make([]string, 0, len(Integrations))
	for _, value := range Integrations {
		got = append(got, string(value))
	}
	if len(contract.IntegrationVocabulary) == 0 {
		t.Fatal("the contract lists no integration values")
	}
	if len(got) != len(contract.IntegrationVocabulary) {
		t.Fatalf("Integrations = %v, contract says %v", got, contract.IntegrationVocabulary)
	}
	for i := range got {
		if got[i] != contract.IntegrationVocabulary[i] {
			t.Fatalf("Integrations = %v, contract says %v", got, contract.IntegrationVocabulary)
		}
	}
}

// The card fields session is what the drop-in is mounted with: every one of
// these values goes to the payer's page, so a dropped one is a dead checkout.
func TestCreateCheckoutSessionFieldsExampleMatchesContract(t *testing.T) {
	endpoint := loadContract(t).Endpoints.CreateCheckoutSession

	server, _ := newTestServer(t, reply{Body: string(endpoint.FieldsSuccessExample)})
	params := testParams()
	params.Integration = IntegrationFields
	session, err := newTestClient(t, server.URL).CreateCheckoutSession(context.Background(), params)
	if err != nil {
		t.Fatalf("the contract's fields example must parse: %v", err)
	}

	if session.Integration != IntegrationFields {
		t.Errorf("Integration = %q, want fields", session.Integration)
	}
	if session.ClientSecret != "cs_4f3e2d1c0b9a8f7e6d5c4b3a2f1e0d9c" {
		t.Errorf("ClientSecret = %q", session.ClientSecret)
	}
	if session.TransactionID != "7a6b5c4d-3e2f-4a1b-9c8d-7e6f5a4b3c2d" {
		t.Errorf("TransactionID = %q", session.TransactionID)
	}
	if session.CashierKey != "ck_live_blox_8c7d6e5f4a3b2c1d" || session.CashierToken != "ctok_blox_0a1b2c3d4e5f6a7b" {
		t.Errorf("cashier key %q, token %q", session.CashierKey, session.CashierToken)
	}
}

func TestCreateCheckoutSessionWidgetExampleEchoesWidgetWithoutASecret(t *testing.T) {
	endpoint := loadContract(t).Endpoints.CreateCheckoutSession

	server, _ := newTestServer(t, reply{Body: string(endpoint.SuccessExample)})
	session, err := newTestClient(t, server.URL).CreateCheckoutSession(context.Background(), testParams())
	if err != nil {
		t.Fatalf("the contract's success example must parse: %v", err)
	}
	if session.Integration != IntegrationWidget {
		t.Errorf("Integration = %q, want widget", session.Integration)
	}
	if session.ClientSecret != "" {
		t.Errorf("ClientSecret = %q, want empty on a widget session", session.ClientSecret)
	}
}

// The fields example carries every checkout field; the widget example carries
// all of them except clientSecret, which exists only for fields.
func TestCreateCheckoutSessionExamplesCarryTheCheckoutFields(t *testing.T) {
	endpoint := loadContract(t).Endpoints.CreateCheckoutSession

	checkoutOf := func(raw json.RawMessage) json.RawMessage {
		var envelope struct {
			Checkout json.RawMessage `json:"checkout"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatalf("example: %v", err)
		}
		return envelope.Checkout
	}

	assertSameFields(t, "createCheckoutSession.fieldsSuccessExample.checkout", jsonKeys(t, checkoutOf(endpoint.FieldsSuccessExample)), endpoint.CheckoutFields)

	widgetFields := []string{}
	for _, name := range endpoint.CheckoutFields {
		if name != "clientSecret" {
			widgetFields = append(widgetFields, name)
		}
	}
	assertSameFields(t, "createCheckoutSession.successExample.checkout", jsonKeys(t, checkoutOf(endpoint.SuccessExample)), widgetFields)
}
