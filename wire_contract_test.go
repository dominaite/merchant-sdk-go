package dominaite

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// testdata/merchant-api-wire-contract.json is the machine-relevant projection of
// the gateway's GET /merchant-api/integration/contract, refreshed by
// .github/workflows/contract-drift.yml. These tests pin the enumerations this SDK
// hardcodes against it. When one fails the gateway moved: fix the SDK and release,
// never the fixture.
const wireContractPath = "testdata/merchant-api-wire-contract.json"

type wireContract struct {
	Statuses            []string `json:"statuses"`
	WebhookEventCatalog []string `json:"webhookEventCatalog"`
	SDKs                []string `json:"sdks"`
}

func loadWireContract(t *testing.T) wireContract {
	t.Helper()
	raw, err := os.ReadFile(wireContractPath)
	if err != nil {
		t.Fatalf("read %s: %v", wireContractPath, err)
	}
	var wire wireContract
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("parse %s: %v", wireContractPath, err)
	}
	return wire
}

func TestWireContractStatusVocabulary(t *testing.T) {
	wire := loadWireContract(t)
	if !reflect.DeepEqual(Statuses, wire.Statuses) {
		t.Fatalf("Statuses drifted from the gateway contract\n  sdk:     %v\n  gateway: %v", Statuses, wire.Statuses)
	}
}

func TestWireContractWebhookEventCatalog(t *testing.T) {
	wire := loadWireContract(t)
	events := []string{
		EventPaymentSucceeded,
		EventPaymentFailed,
		EventPaymentRequiresCapture,
		EventPaymentCancelled,
		EventPaymentAbandoned,
		EventPaymentRefunded,
		EventPaymentDisputed,
	}
	if !reflect.DeepEqual(events, wire.WebhookEventCatalog) {
		t.Fatalf("Event* constants drifted from the gateway contract\n  sdk:     %v\n  gateway: %v", events, wire.WebhookEventCatalog)
	}
}

func TestWireContractStillListsThisSDK(t *testing.T) {
	wire := loadWireContract(t)
	for _, language := range wire.SDKs {
		if language == "go" {
			return
		}
	}
	t.Fatalf("the gateway contract no longer lists go among its SDKs: %v", wire.SDKs)
}
