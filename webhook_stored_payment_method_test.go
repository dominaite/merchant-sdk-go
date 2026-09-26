package dominaite

import (
	"testing"
)

// data.storedPaymentMethod on payment.* events: the same object as the
// status read's storedPaymentMethod, null or absent when there is none.

func paymentEvent(data string) string {
	return `{"id":"d6","type":"payment.succeeded","apiVersion":"2026-09-25","createdAt":"2026-09-25T10:00:00Z","data":{"transactionId":"11111111-1111-4111-8111-111111111111","status":"succeeded","amount":2500,"grossAmount":2500,"currency":"EUR"` + data + `}}`
}

func TestVerifyWebhookParsesAnActiveStoredPaymentMethod(t *testing.T) {
	event := mustVerify(t, paymentEvent(`,"storedPaymentMethod":{"id":"pm_0123456789abcdef0123456789abcdef","brand":"visa","last4":"4242","expiryMonth":12,"expiryYear":2030,"status":"active","retiredReason":null}`))

	want := StoredPaymentMethod{
		ID: "pm_0123456789abcdef0123456789abcdef", Brand: "visa", Last4: "4242",
		ExpiryMonth: 12, ExpiryYear: 2030, Status: StoredPaymentMethodStatusActive,
	}
	if event.Data.StoredPaymentMethod == nil || *event.Data.StoredPaymentMethod != want {
		t.Errorf("StoredPaymentMethod = %+v, want %+v", event.Data.StoredPaymentMethod, want)
	}
}

func TestVerifyWebhookReadsANullOrAbsentStoredPaymentMethodAsNil(t *testing.T) {
	for name, data := range map[string]string{
		"null":   `,"storedPaymentMethod":null`,
		"absent": ``,
	} {
		t.Run(name, func(t *testing.T) {
			event := mustVerify(t, paymentEvent(data))
			if event.Data.StoredPaymentMethod != nil {
				t.Errorf("StoredPaymentMethod = %+v, want nil", *event.Data.StoredPaymentMethod)
			}
			if event.Data.TransactionID != "11111111-1111-4111-8111-111111111111" || event.Data.Amount != 2500 {
				t.Errorf("the rest of data must still parse: %+v", event.Data)
			}
		})
	}
}

func TestVerifyWebhookParsesARetiredStoredPaymentMethod(t *testing.T) {
	event := mustVerify(t, paymentEvent(`,"storedPaymentMethod":{"id":"pm_0123456789abcdef0123456789abcdef","brand":"mastercard","last4":"5454","expiryMonth":1,"expiryYear":2031,"status":"retired","retiredReason":"hard_decline"}`))

	card := event.Data.StoredPaymentMethod
	if card == nil {
		t.Fatal("StoredPaymentMethod = nil, want the retired card")
	}
	if card.Status != StoredPaymentMethodStatusRetired || card.RetiredReason != RetiredReasonHardDecline {
		t.Errorf("status %q, retiredReason %q", card.Status, card.RetiredReason)
	}
}
