package dominaite

import (
	"testing"
)

// Payload shapes follow the gateway's webhook payload builders as of
// apiVersion 2026-09-25. Webhooks send every field, null when empty.

func TestAPaymentEventCarriesHowThePayerPaid(t *testing.T) {
	body := `{"id":"d1","type":"payment.succeeded","apiVersion":"2026-09-25","createdAt":"2026-09-25T10:00:00Z","data":{` +
		`"transactionId":"tx","status":"succeeded","previousStatus":"processing","kind":"sale","amount":100,"grossAmount":100,` +
		`"surchargeAmount":null,"currency":"EUR","paymentMethod":"wallet","walletType":"apple_pay","originalTransactionId":null,` +
		`"idempotencyKey":"k","orderReference":"o","orderId":"ord","pspReference":null,"description":null,` +
		`"paymentMethodBrand":"visa","paymentMethodLast4":"4242","storedPaymentMethod":null,"verifyOnly":false,"declineClass":null}}`
	event := mustVerify(t, body)

	if event.Data.PaymentMethod != PaymentMethodWallet || event.Data.WalletType != WalletTypeApplePay {
		t.Fatalf("PaymentMethod %q, WalletType %q, want wallet and apple_pay", event.Data.PaymentMethod, event.Data.WalletType)
	}
	if event.Data.Charge != nil || event.Data.Agreement != nil {
		t.Fatal("a payment event has no charge or agreement data")
	}
}

func TestAVerificationEventCarriesHowThePayerPaid(t *testing.T) {
	body := `{"id":"d1","type":"verification.succeeded","createdAt":"2026-09-25T10:00:00Z","data":{` +
		`"transactionId":"tx","status":"cancelled","amount":0,"grossAmount":0,"currency":"EUR",` +
		`"paymentMethod":"card","walletType":null,"verifyOnly":true}}`
	event := mustVerify(t, body)

	if event.Data.PaymentMethod != PaymentMethodCard || event.Data.WalletType != "" {
		t.Fatalf("PaymentMethod %q, WalletType %q, want card and empty", event.Data.PaymentMethod, event.Data.WalletType)
	}
}

func TestANullPaymentMethodReadsAsEmpty(t *testing.T) {
	body := `{"id":"d1","type":"payment.abandoned","createdAt":"2026-09-25T10:00:00Z","data":{` +
		`"transactionId":"tx","status":"abandoned","amount":100,"grossAmount":100,"currency":"EUR","paymentMethod":null,"walletType":null}}`
	event := mustVerify(t, body)

	if event.Data.PaymentMethod != "" || event.Data.WalletType != "" {
		t.Fatalf("PaymentMethod %q, WalletType %q, want both empty", event.Data.PaymentMethod, event.Data.WalletType)
	}
}

// On a charge event paymentMethod is an object. It must not break the
// payload, and it lands on Charge.PaymentMethod.
func TestAChargeEventIsTyped(t *testing.T) {
	body := `{"id":"d3","type":"charge.retrying","apiVersion":"2026-09-25","createdAt":"2026-09-25T12:00:00Z","data":{` +
		`"chargeId":"ch_0123456789abcdef0123456789abcdef","transactionId":"tx-2","storedPaymentMethodId":"pm_0123456789abcdef0123456789abcdef",` +
		`"agreementId":"agr_0123456789abcdef0123456789abcdef","customerReference":"cust-1","outcome":"retrying","periodNumber":2,` +
		`"attemptNumber":1,"amount":990,"currency":"EUR","paymentMethod":{"brand":"visa","last4":"4242"},"orderReference":"order-9",` +
		`"description":null,"declineClass":"soft_funds","declineCode":"51","nextAttemptAt":"2026-09-26T12:00:00Z","nextChargeAt":null,"sequence":7}}`
	event := mustVerify(t, body)

	charge := event.Data.Charge
	if charge == nil {
		t.Fatal("Charge = nil on a charge event")
	}
	if charge.ChargeID != "ch_0123456789abcdef0123456789abcdef" || charge.TransactionID != "tx-2" ||
		charge.StoredPaymentMethodID != "pm_0123456789abcdef0123456789abcdef" ||
		charge.AgreementID != "agr_0123456789abcdef0123456789abcdef" || charge.CustomerReference != "cust-1" {
		t.Errorf("ids: %+v", charge)
	}
	if charge.Outcome != ChargeOutcomeRetrying || charge.Amount != 990 || charge.Currency != "EUR" || charge.Sequence != 7 {
		t.Errorf("outcome %q, amount %d %s, sequence %d", charge.Outcome, charge.Amount, charge.Currency, charge.Sequence)
	}
	if charge.PeriodNumber == nil || *charge.PeriodNumber != 2 || charge.AttemptNumber == nil || *charge.AttemptNumber != 1 {
		t.Errorf("period %v, attempt %v, want 2 and 1", charge.PeriodNumber, charge.AttemptNumber)
	}
	if charge.PaymentMethod == nil || charge.PaymentMethod.Brand != "visa" || charge.PaymentMethod.Last4 != "4242" {
		t.Errorf("PaymentMethod = %+v, want visa 4242", charge.PaymentMethod)
	}
	if charge.DeclineClass != DeclineClassSoftFunds || charge.DeclineCode != "51" ||
		charge.NextAttemptAt != "2026-09-26T12:00:00Z" || charge.NextChargeAt != "" || charge.Description != "" {
		t.Errorf("decline %q/%q, next attempt %q, next charge %q, description %q",
			charge.DeclineClass, charge.DeclineCode, charge.NextAttemptAt, charge.NextChargeAt, charge.Description)
	}
	if len(charge.Raw) == 0 {
		t.Error("Charge.Raw is empty")
	}
	// The object never lands on the payment category.
	if event.Data.PaymentMethod != "" || event.Data.Agreement != nil {
		t.Errorf("Data.PaymentMethod %q, Agreement %+v, want empty and nil", event.Data.PaymentMethod, event.Data.Agreement)
	}
	if event.Data.TransactionID != "tx-2" || event.Data.Amount != 990 {
		t.Errorf("shared fields: transactionId %q, amount %d", event.Data.TransactionID, event.Data.Amount)
	}
}

// A one-off charge has no agreement, no period and maybe no card details:
// the nulls read as empty and nil.
func TestAOneOffChargeEventReadsItsNulls(t *testing.T) {
	body := `{"id":"d4","type":"charge.succeeded","createdAt":"2026-09-25T12:00:00Z","data":{` +
		`"chargeId":"ch_1","transactionId":"tx-3","storedPaymentMethodId":"pm_1","agreementId":null,"customerReference":null,` +
		`"outcome":"succeeded","periodNumber":null,"attemptNumber":null,"amount":500,"currency":"EUR","paymentMethod":null,` +
		`"orderReference":"o","description":null,"declineClass":null,"declineCode":null,"nextAttemptAt":null,"nextChargeAt":null,"sequence":1}}`
	event := mustVerify(t, body)

	charge := event.Data.Charge
	if charge == nil {
		t.Fatal("Charge = nil on a charge event")
	}
	if charge.AgreementID != "" || charge.PeriodNumber != nil || charge.AttemptNumber != nil || charge.PaymentMethod != nil {
		t.Errorf("got %+v, want empty agreement and nil period, attempt and payment method", charge)
	}
	if charge.Outcome != ChargeOutcomeSucceeded {
		t.Errorf("Outcome = %q", charge.Outcome)
	}
}

func TestAnAgreementEventIsTyped(t *testing.T) {
	body := `{"id":"d2","type":"agreement.past_due","apiVersion":"2026-09-25","createdAt":"2026-09-25T11:00:00Z","data":{` +
		`"id":"agr_0123456789abcdef0123456789abcdef","planId":"plan_1","customerReference":"cust-1",` +
		`"storedPaymentMethodId":"pm_0123456789abcdef0123456789abcdef","status":"past_due","previousStatus":"active",` +
		`"amount":990,"currency":"EUR","intervalUnit":"month","intervalCount":1,"periodCount":null,"trialDays":0,` +
		`"nextChargeAt":"2026-09-25T00:00:00Z","activatedAt":"2026-08-25T00:00:00Z","cancelledAt":null,"version":4,"sequence":3}}`
	event := mustVerify(t, body)

	agreement := event.Data.Agreement
	if agreement == nil {
		t.Fatal("Agreement = nil on an agreement event")
	}
	if agreement.ID != "agr_0123456789abcdef0123456789abcdef" || agreement.PlanID != "plan_1" ||
		agreement.CustomerReference != "cust-1" || agreement.StoredPaymentMethodID != "pm_0123456789abcdef0123456789abcdef" {
		t.Errorf("ids: %+v", agreement)
	}
	if agreement.Status != AgreementStatusPastDue || agreement.PreviousStatus != AgreementStatusActive {
		t.Errorf("status %q, previous %q", agreement.Status, agreement.PreviousStatus)
	}
	if agreement.Amount != 990 || agreement.Currency != "EUR" || agreement.IntervalUnit != IntervalUnitMonth ||
		agreement.IntervalCount != 1 || agreement.PeriodCount != nil || agreement.TrialDays != 0 {
		t.Errorf("terms: %+v", agreement)
	}
	if agreement.NextChargeAt != "2026-09-25T00:00:00Z" || agreement.ActivatedAt != "2026-08-25T00:00:00Z" || agreement.CancelledAt != "" {
		t.Errorf("dates: next %q, activated %q, cancelled %q", agreement.NextChargeAt, agreement.ActivatedAt, agreement.CancelledAt)
	}
	if agreement.Version != 4 || agreement.Sequence != 3 {
		t.Errorf("version %d, sequence %d", agreement.Version, agreement.Sequence)
	}
	if event.Data.Charge != nil {
		t.Error("an agreement event has no charge data")
	}
}

func TestChargeOutcomeConstantsAreTheWireStrings(t *testing.T) {
	want := []string{"succeeded", "retrying", "failed"}
	if len(ChargeOutcomes) != len(want) {
		t.Fatalf("ChargeOutcomes = %v, want %v", ChargeOutcomes, want)
	}
	for i := range want {
		if ChargeOutcomes[i] != want[i] {
			t.Errorf("ChargeOutcomes[%d] = %q, want %q", i, ChargeOutcomes[i], want[i])
		}
	}
}

// A correctly signed event never fails on its data. Data that does not fit
// the typed struct leaves it nil and stays on Raw, as 0.6.0 accepted it.
func TestAWrongShapedChargeEventStillVerifies(t *testing.T) {
	body := `{"id":"d5","type":"charge.succeeded","createdAt":"2026-09-25T12:00:00Z","data":{` +
		`"chargeId":"ch_0123456789abcdef0123456789abcdef","transactionId":"tx-5","outcome":"succeeded",` +
		`"periodNumber":"two","amount":990,"currency":"EUR","sequence":3}}`
	event := mustVerify(t, body)

	if event.Data.Charge != nil {
		t.Errorf("Charge = %+v, want nil for data that does not fit", event.Data.Charge)
	}
	if string(event.Data.Raw) == "" || event.Data.TransactionID != "tx-5" || event.Data.Amount != 990 {
		t.Errorf("Raw %q, TransactionID %q, Amount %d: want the data kept", event.Data.Raw, event.Data.TransactionID, event.Data.Amount)
	}
}

func TestAWrongShapedAgreementEventStillVerifies(t *testing.T) {
	body := `{"id":"d6","type":"agreement.activated","createdAt":"2026-09-25T12:00:00Z","data":{` +
		`"id":"agr_0123456789abcdef0123456789abcdef","planId":"plan_1","status":"active",` +
		`"intervalCount":"monthly","amount":990,"currency":"EUR","sequence":1}}`
	event := mustVerify(t, body)

	if event.Data.Agreement != nil {
		t.Errorf("Agreement = %+v, want nil for data that does not fit", event.Data.Agreement)
	}
	if string(event.Data.Raw) == "" || event.Data.Status != "active" {
		t.Errorf("Raw %q, Status %q: want the data kept", event.Data.Raw, event.Data.Status)
	}
}
