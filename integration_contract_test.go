package dominaite

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestGetIntegrationContractReadsTheContract(t *testing.T) {
	wire := loadWireContract(t)
	server, calls := newTestServer(t, reply{Body: map[string]any{
		"success": true,
		"data": map[string]any{
			"version":             "2026-09-16",
			"statuses":            wire.Statuses,
			"webhookEventCatalog": wire.WebhookEventCatalog,
			"aSectionAddedLater":  map[string]any{"value": 1},
		},
	}})
	client := newTestClient(t, server.URL)

	contract, err := client.GetIntegrationContract(context.Background())
	if err != nil {
		t.Fatalf("GetIntegrationContract: %v", err)
	}
	if contract.Version != "2026-09-16" {
		t.Errorf("Version = %q", contract.Version)
	}
	if !reflect.DeepEqual(contract.Statuses, Statuses) {
		t.Errorf("Statuses = %v", contract.Statuses)
	}
	if !reflect.DeepEqual(contract.WebhookEventCatalog, wire.WebhookEventCatalog) {
		t.Errorf("WebhookEventCatalog = %v", contract.WebhookEventCatalog)
	}
	if !strings.Contains(string(contract.Raw), "aSectionAddedLater") {
		t.Errorf("Raw must keep the sections the SDK does not model: %s", contract.Raw)
	}

	call := calls()[0]
	if call.Method != http.MethodGet || call.Path != IntegrationContractPath || call.Body != "" {
		t.Fatalf("got %s %s with body %q", call.Method, call.Path, call.Body)
	}
	if call.Header.Get("Idempotency-Key") != "" {
		t.Error("a GET sends no Idempotency-Key")
	}
	assertSignedWith(t, call, http.MethodGet, IntegrationContractPath, "")
}

func TestGetIntegrationContractWithoutAVersionIsAnAPIError(t *testing.T) {
	server, _ := newTestServer(t, reply{Body: map[string]any{"success": true, "data": map[string]any{"statuses": []string{}}}})
	client := newTestClient(t, server.URL)

	_, err := client.GetIntegrationContract(context.Background())

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("got %v, want *APIError", err)
	}
}
