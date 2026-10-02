package dominaite

import "testing"

// The identification fields every payment.* event carries in data. Null or
// absent (older gateways, refunds, non-card methods) reads as "".

func TestVerifyWebhookParsesIdentificationFields(t *testing.T) {
	event := mustVerify(t, paymentEvent(`,"orderReference":"order-1042","orderId":"dom_9a8b7c6d5e4f","description":"Pro plan","paymentMethodBrand":"visa","paymentMethodLast4":"4242"`))
	data := event.Data
	if data.OrderReference != "order-1042" || data.OrderID != "dom_9a8b7c6d5e4f" || data.Description != "Pro plan" {
		t.Errorf("orderReference %q, orderId %q, description %q", data.OrderReference, data.OrderID, data.Description)
	}
	if data.PaymentMethodBrand != "visa" || data.PaymentMethodLast4 != "4242" {
		t.Errorf("paymentMethodBrand %q, paymentMethodLast4 %q", data.PaymentMethodBrand, data.PaymentMethodLast4)
	}
}

func TestVerifyWebhookReadsNullOrAbsentIdentificationFieldsAsEmpty(t *testing.T) {
	for name, data := range map[string]string{
		"null":   `,"orderReference":null,"orderId":null,"description":null,"paymentMethodBrand":null,"paymentMethodLast4":null`,
		"absent": ``,
	} {
		t.Run(name, func(t *testing.T) {
			event := mustVerify(t, paymentEvent(data))
			got := []string{event.Data.OrderReference, event.Data.OrderID, event.Data.Description, event.Data.PaymentMethodBrand, event.Data.PaymentMethodLast4}
			for _, value := range got {
				if value != "" {
					t.Errorf("identification fields = %q, want all empty", got)
					break
				}
			}
			if event.Data.TransactionID != "11111111-1111-4111-8111-111111111111" || event.Data.Amount != 2500 {
				t.Errorf("the rest of data must still parse: %+v", event.Data)
			}
		})
	}
}
