package dominaite

import (
	"encoding/json"
	"net/http"
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

type wireErrorCode struct {
	Code       string `json:"code"`
	HTTPStatus int    `json:"httpStatus"`
	Retry      *bool  `json:"retry"`
}

type wireContract struct {
	Statuses   []string `json:"statuses"`
	ErrorCodes struct {
		Storefront    []wireErrorCode `json:"storefront"`
		Authorization []wireErrorCode `json:"authorization"`
	} `json:"errorCodes"`
	WebhookEventCatalog    []string `json:"webhookEventCatalog"`
	RecurringWebhookEvents []string `json:"recurringWebhookEvents"`
	Wallets                struct {
		WalletTypes     []string `json:"walletTypes"`
		ReportingFields []struct {
			Path     string `json:"path"`
			Type     string `json:"type"`
			Required bool   `json:"required"`
		} `json:"reportingFields"`
	} `json:"wallets"`
	SDKs []string `json:"sdks"`
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

// The agreement and charge events sit outside the event catalog, under the
// contract's recurring section.
func TestWireContractRecurringWebhookEvents(t *testing.T) {
	wire := loadWireContract(t)
	events := []string{
		EventAgreementActivated,
		EventAgreementPastDue,
		EventAgreementCancelled,
		EventChargeSucceeded,
		EventChargeFailed,
		EventChargeRetrying,
	}
	if !reflect.DeepEqual(events, wire.RecurringWebhookEvents) {
		t.Fatalf("EventAgreement* and EventCharge* constants drifted from the gateway contract\n  sdk:     %v\n  gateway: %v", events, wire.RecurringWebhookEvents)
	}
}

func TestWireContractWalletTypes(t *testing.T) {
	wire := loadWireContract(t)
	if !reflect.DeepEqual(WalletTypes, wire.Wallets.WalletTypes) {
		t.Fatalf("WalletTypes drifted from the gateway contract\n  sdk:     %v\n  gateway: %v", WalletTypes, wire.Wallets.WalletTypes)
	}
}

// The wallet reporting fields sit on the status read. CheckoutStatus models
// each one as an optional string.
func TestWireContractWalletReportingFields(t *testing.T) {
	wire := loadWireContract(t)
	if len(wire.Wallets.ReportingFields) == 0 {
		t.Fatal("the wire contract lists no wallet reporting fields")
	}
	modelled := jsonFieldNames(t, CheckoutStatus{})
	for _, field := range wire.Wallets.ReportingFields {
		if !contains(modelled, field.Path) {
			t.Errorf("CheckoutStatus does not model the wallet reporting field %q", field.Path)
		}
		if field.Type != "string" || field.Required {
			t.Errorf("%s: type %q, required %v; this SDK models it as an optional string", field.Path, field.Type, field.Required)
		}
	}
}

func TestWireContractStorefrontErrorCodes(t *testing.T) {
	wire := loadWireContract(t)
	var codes []string
	for _, entry := range wire.ErrorCodes.Storefront {
		codes = append(codes, entry.Code)
		if entry.Retry == nil || *entry.Retry {
			t.Errorf("%s: retry = %v, want false", entry.Code, entry.Retry)
		}
	}
	if !reflect.DeepEqual(StorefrontErrorCodes, codes) {
		t.Fatalf("StorefrontErrorCodes drifted from the gateway contract\n  sdk:     %v\n  gateway: %v", StorefrontErrorCodes, codes)
	}
	want := map[string]int{
		ErrorCodeStorefrontMismatch:       400,
		ErrorCodeStorefrontInactive:       409,
		ErrorCodeStorefrontNotWhitelisted: 409,
	}
	for _, entry := range wire.ErrorCodes.Storefront {
		if want[entry.Code] != entry.HTTPStatus {
			t.Errorf("%s: HTTP %d, want %d", entry.Code, entry.HTTPStatus, want[entry.Code])
		}
	}
}

func TestWireContractAuthorizationErrorCodes(t *testing.T) {
	wire := loadWireContract(t)
	var codes []string
	for _, entry := range wire.ErrorCodes.Authorization {
		codes = append(codes, entry.Code)
		if entry.HTTPStatus != http.StatusForbidden {
			t.Errorf("%s: HTTP %d, want 403", entry.Code, entry.HTTPStatus)
		}
		if entry.Retry == nil || *entry.Retry {
			t.Errorf("%s: retry = %v, want false", entry.Code, entry.Retry)
		}
	}
	if !reflect.DeepEqual(AuthorizationErrorCodes, codes) {
		t.Fatalf("AuthorizationErrorCodes drifted from the gateway contract\n  sdk:     %v\n  gateway: %v", AuthorizationErrorCodes, codes)
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
