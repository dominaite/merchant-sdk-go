package dominaite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const (
	testWebhookID     = "7c9e6679-7425-40de-944b-e07fc1f90ae7"
	testWebhookKey    = "webhook-orders-2026"
	testWebhookSecret = "whsec_0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

func testWebhookEndpoint(withSecret bool) map[string]any {
	endpoint := map[string]any{
		"id":        testWebhookID,
		"url":       "https://shop.example/hooks/dominaite",
		"events":    []string{"payment.succeeded", "agreement.past_due"},
		"status":    "active",
		"createdAt": "2026-10-01T09:30:00Z",
	}
	if withSecret {
		endpoint["secret"] = testWebhookSecret
	}
	return endpoint
}

func testWebhookParams() CreateWebhookEndpointParams {
	return CreateWebhookEndpointParams{
		URL:            "https://shop.example/hooks/dominaite",
		Events:         []string{EventPaymentSucceeded, "agreement.past_due"},
		IdempotencyKey: testWebhookKey,
	}
}

func TestCreateWebhookEndpointSendsTheFieldsAndReturnsTheSecretOnce(t *testing.T) {
	server, calls := newTestServer(t, dataReply(http.StatusCreated, testWebhookEndpoint(true)))
	client := newTestClient(t, server.URL)

	endpoint, err := client.CreateWebhookEndpoint(context.Background(), testWebhookParams())
	if err != nil {
		t.Fatalf("CreateWebhookEndpoint: %v", err)
	}

	call := calls()[0]
	want := `{"url":"https://shop.example/hooks/dominaite","events":["payment.succeeded","agreement.past_due"]}`
	if call.Method != http.MethodPost || call.Path != WebhooksPath || call.Body != want {
		t.Fatalf("got %s %s %s\nwant POST %s %s", call.Method, call.Path, call.Body, WebhooksPath, want)
	}
	if call.Header.Get("Idempotency-Key") != testWebhookKey {
		t.Errorf("Idempotency-Key = %q", call.Header.Get("Idempotency-Key"))
	}
	assertSignedWith(t, call, http.MethodPost, WebhooksPath, testWebhookKey)

	if endpoint.ID != testWebhookID || endpoint.URL != "https://shop.example/hooks/dominaite" || endpoint.Status != WebhookEndpointStatusActive {
		t.Errorf("endpoint = %v", endpoint)
	}
	if len(endpoint.Events) != 2 || endpoint.Events[1] != "agreement.past_due" || endpoint.CreatedAt == "" {
		t.Errorf("endpoint = %v", endpoint)
	}
	if endpoint.Secret != testWebhookSecret {
		t.Errorf("Secret = %q, want the secret of the first answer", endpoint.Secret)
	}
}

// A replay of the same key answers the same endpoint without its secret.
func TestCreateWebhookEndpointReplayHasNoSecret(t *testing.T) {
	server, _ := newTestServer(t, dataReply(http.StatusCreated, testWebhookEndpoint(false)))
	client := newTestClient(t, server.URL)

	endpoint, err := client.CreateWebhookEndpoint(context.Background(), testWebhookParams())
	if err != nil || endpoint.ID != testWebhookID || endpoint.Secret != "" {
		t.Fatalf("got %v, %v", endpoint, err)
	}
}

func TestAWebhookEndpointNeverPrintsOrLogsItsSecret(t *testing.T) {
	server, _ := newTestServer(t, dataReply(http.StatusCreated, testWebhookEndpoint(true)))
	endpoint, err := newTestClient(t, server.URL).CreateWebhookEndpoint(context.Background(), testWebhookParams())
	if err != nil {
		t.Fatalf("CreateWebhookEndpoint: %v", err)
	}

	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		for name, value := range map[string]any{"value": *endpoint, "pointer": endpoint} {
			printed := fmt.Sprintf(verb, value)
			if strings.Contains(printed, testWebhookSecret[len("whsec_"):]) {
				t.Errorf("%s on a %s prints the secret: %s", verb, name, printed)
			}
			if !strings.Contains(printed, testWebhookID) || !strings.Contains(printed, "redacted") {
				t.Errorf("%s on a %s: %s", verb, name, printed)
			}
		}
	}
	logged, err := json.Marshal(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logged), "whsec_") || !strings.Contains(string(logged), testWebhookID) {
		t.Errorf("a JSON logger would write %s", logged)
	}
}

func TestCreateWebhookEndpointExtraFieldsAreSentAndSigned(t *testing.T) {
	server, calls := newTestServer(t, dataReply(http.StatusCreated, testWebhookEndpoint(true)))
	client := newTestClient(t, server.URL)

	params := testWebhookParams()
	params.Extra = map[string]any{"description": "orders & refunds"}
	if _, err := client.CreateWebhookEndpoint(context.Background(), params); err != nil {
		t.Fatalf("CreateWebhookEndpoint: %v", err)
	}
	call := calls()[0]
	if body := sentBody(t, call); body["description"] != "orders & refunds" || body["url"] == nil {
		t.Errorf("body = %s", call.Body)
	}
	assertSignedWith(t, call, http.MethodPost, WebhooksPath, testWebhookKey)
}

func TestCreateWebhookEndpointRefusesBadParamsBeforeSending(t *testing.T) {
	server, calls := newTestServer(t, dataReply(http.StatusCreated, testWebhookEndpoint(true)))
	client := newTestClient(t, server.URL)

	noURL, noEvents, noKey := testWebhookParams(), testWebhookParams(), testWebhookParams()
	noURL.URL = ""
	noEvents.Events = nil
	noKey.IdempotencyKey = ""

	for name, params := range map[string]CreateWebhookEndpointParams{
		"missing url":             noURL,
		"no events":               noEvents,
		"missing idempotency key": noKey,
	} {
		_, err := client.CreateWebhookEndpoint(context.Background(), params)
		var validationErr *ValidationError
		if !errors.As(err, &validationErr) {
			t.Errorf("%s: got %v, want *ValidationError", name, err)
		}
	}
	if len(calls()) != 0 {
		t.Fatal("a refused endpoint must never reach the network")
	}
}

func TestListWebhookEndpointsReadsEveryEndpoint(t *testing.T) {
	inactive := testWebhookEndpoint(false)
	inactive["id"] = "11111111-1111-4111-8111-111111111111"
	inactive["status"] = "inactive"
	server, calls := newTestServer(t, dataReply(http.StatusOK, []any{testWebhookEndpoint(false), inactive}))
	client := newTestClient(t, server.URL)

	endpoints, err := client.ListWebhookEndpoints(context.Background())
	if err != nil {
		t.Fatalf("ListWebhookEndpoints: %v", err)
	}

	call := calls()[0]
	if call.Method != http.MethodGet || call.Path != WebhooksPath || call.Body != "" || call.Header.Get("Idempotency-Key") != "" {
		t.Fatalf("got %s %s with body %q", call.Method, call.Path, call.Body)
	}
	assertSignedWith(t, call, http.MethodGet, WebhooksPath, "")

	if len(endpoints) != 2 || endpoints[0].ID != testWebhookID || endpoints[1].Status != WebhookEndpointStatusInactive {
		t.Fatalf("endpoints = %v", endpoints)
	}
	if endpoints[0].Secret != "" || len(endpoints[1].Raw) == 0 {
		t.Errorf("endpoints = %v", endpoints)
	}
}

// The API signs the lowercase hyphenated id, so an uppercase id is
// normalized before it goes into the path and the signature.
func TestDeleteWebhookEndpointSignsTheLowercaseID(t *testing.T) {
	server, calls := newTestServer(t, reply{Status: http.StatusNoContent, Body: ""})
	client := newTestClient(t, server.URL)

	if err := client.DeleteWebhookEndpoint(context.Background(), strings.ToUpper(testWebhookID)); err != nil {
		t.Fatalf("DeleteWebhookEndpoint: %v", err)
	}

	call := calls()[0]
	path := WebhooksPath + "/" + testWebhookID
	if call.Method != http.MethodDelete || call.Path != path || call.Body != "" || call.Header.Get("Idempotency-Key") != "" {
		t.Fatalf("got %s %s with body %q, want DELETE %s", call.Method, call.Path, call.Body, path)
	}
	assertSignedWith(t, call, http.MethodDelete, path, "")
}

func TestDeleteWebhookEndpointRefusesANonUUID(t *testing.T) {
	server, calls := newTestServer(t, reply{Status: http.StatusNoContent, Body: ""})
	client := newTestClient(t, server.URL)

	for _, id := range []string{"", "not-a-uuid", testWebhookID + "/x"} {
		err := client.DeleteWebhookEndpoint(context.Background(), id)
		var validationErr *ValidationError
		if !errors.As(err, &validationErr) {
			t.Errorf("%q: got %v, want *ValidationError", id, err)
		}
	}
	if len(calls()) != 0 {
		t.Fatal("a refused id must never reach the network")
	}
}

func TestWebhookEndpointRejectionsAreAPIErrors(t *testing.T) {
	create := func(c *Client) error {
		_, err := c.CreateWebhookEndpoint(context.Background(), testWebhookParams())
		return err
	}
	cases := []struct {
		name   string
		status int
		code   string
		call   func(*Client) error
	}{
		{"url rejected", http.StatusBadRequest, ErrorCodeWebhookURLRejected, create},
		{"limit reached", http.StatusConflict, ErrorCodeWebhookLimitReached, create},
		{"key reused", http.StatusUnprocessableEntity, ErrorCodeIdempotencyKeyReused, create},
		{"in progress", http.StatusConflict, ErrorCodeDuplicateRequest, create},
		{"unknown endpoint", http.StatusNotFound, ErrorCodeWebhookNotFound, func(c *Client) error {
			return c.DeleteWebhookEndpoint(context.Background(), testWebhookID)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, _ := newTestServer(t, gatewayError(tc.status, tc.code, "refused", nil))
			err := tc.call(newTestClient(t, server.URL))

			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.HTTPStatus != tc.status || apiErr.ErrorCode != tc.code {
				t.Fatalf("got %v, want *APIError %d %s", err, tc.status, tc.code)
			}
		})
	}
}

// WEBHOOK_ALREADY_REGISTERED names the existing endpoint in error.params.
func TestAlreadyRegisteredCarriesTheExistingEndpointID(t *testing.T) {
	server, _ := newTestServer(t, gatewayError(http.StatusConflict, ErrorCodeWebhookAlreadyRegistered, "Already registered.",
		map[string]any{"webhookId": testWebhookID}))
	client := newTestClient(t, server.URL)

	_, err := client.CreateWebhookEndpoint(context.Background(), testWebhookParams())

	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode != ErrorCodeWebhookAlreadyRegistered {
		t.Fatalf("got %v", err)
	}
	var envelope struct {
		Error struct {
			Params struct {
				WebhookID string `json:"webhookId"`
			} `json:"params"`
		} `json:"error"`
	}
	if err := json.Unmarshal(apiErr.Raw, &envelope); err != nil || envelope.Error.Params.WebhookID != testWebhookID {
		t.Fatalf("Raw = %s", apiErr.Raw)
	}
}

func TestAWebhookEndpointKeyWithoutTheScopeIsAnAuthError(t *testing.T) {
	server, _ := newTestServer(t, gatewayError(http.StatusForbidden, ErrorCodeInsufficientScope,
		"This API key does not have the webhooks:manage scope.", nil))
	client := newTestClient(t, server.URL)

	_, err := client.ListWebhookEndpoints(context.Background())

	var authErr *AuthError
	if !errors.As(err, &authErr) || authErr.HTTPStatus != http.StatusForbidden || authErr.ErrorCode != ErrorCodeInsufficientScope {
		t.Fatalf("got %v, want a 403 *AuthError", err)
	}
	if !strings.Contains(authErr.Message, "webhooks:manage") {
		t.Errorf("Message = %q, want the scope named", authErr.Message)
	}
}
