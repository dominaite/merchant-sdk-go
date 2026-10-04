package dominaite

import (
	"context"
	"encoding/json"
	"testing"
)

// paymentMethod and walletType: how the payer paid, on the status read. Null or
// absent reads as "", and a wallet this SDK does not name yet is a valid value,
// not an error.

func TestGetStatusReadsWalletReportingFields(t *testing.T) {
	const id = "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0"
	base := `{"transactionId":"` + id + `","orderId":"dom_9a8b7c6d5e4f","status":"succeeded","amount":8440,"currency":"EUR","createdAt":"2026-08-21T09:15:30.000Z"`
	for name, tc := range map[string]struct{ extra, method, wallet string }{
		"wallet":         {`,"paymentMethod":"wallet","walletType":"apple_pay"`, PaymentMethodWallet, WalletTypeApplePay},
		"unknown wallet": {`,"paymentMethod":"wallet","walletType":"future_wallet"`, PaymentMethodWallet, "future_wallet"},
		"card":           {`,"paymentMethod":"card","walletType":null`, PaymentMethodCard, ""},
		"null":           {`,"paymentMethod":null,"walletType":null`, "", ""},
		"absent":         {``, "", ""},
	} {
		t.Run(name, func(t *testing.T) {
			server, _ := newTestServer(t, reply{Body: base + tc.extra + `}`})
			status, err := newTestClient(t, server.URL).GetStatus(context.Background(), id)
			if err != nil {
				t.Fatalf("GetStatus: %v", err)
			}
			if status.PaymentMethod != tc.method || status.WalletType != tc.wallet {
				t.Errorf("paymentMethod %q, walletType %q, want %q, %q", status.PaymentMethod, status.WalletType, tc.method, tc.wallet)
			}
		})
	}
}

func TestGetStatusExamplesCarryWalletReportingFields(t *testing.T) {
	endpoint := loadContract(t).Endpoints.GetStatus
	for name, tc := range map[string]struct {
		example        json.RawMessage
		method, wallet string
	}{
		"example":          {endpoint.Example, PaymentMethodWallet, WalletTypeApplePay},
		"savedCardExample": {endpoint.SavedCardExample, PaymentMethodCard, ""},
	} {
		t.Run(name, func(t *testing.T) {
			bothWireForms(t, tc.example, func(t *testing.T, body json.RawMessage) {
				server, _ := newTestServer(t, reply{Body: string(body)})
				status, err := newTestClient(t, server.URL).GetStatus(context.Background(), "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0")
				if err != nil {
					t.Fatalf("the contract's %s must parse: %v", name, err)
				}
				if status.PaymentMethod != tc.method || status.WalletType != tc.wallet {
					t.Errorf("paymentMethod %q, walletType %q, want %q, %q", status.PaymentMethod, status.WalletType, tc.method, tc.wallet)
				}
			})
		})
	}
}

func TestPaymentMethodCategories(t *testing.T) {
	want := []string{"card", "wallet", "bank_transfer", "sepa"}
	assertSameFields(t, "PaymentMethodCategories", PaymentMethodCategories, want)
}
