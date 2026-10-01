package dominaite

import (
	"context"
	"testing"
)

// pspReference: the processor's reference on payment.* webhook data and on the
// status read. Null or absent (older gateways, unknown yet) reads as "".

func TestVerifyWebhookParsesPSPReference(t *testing.T) {
	event := mustVerify(t, paymentEvent(`,"pspReference":"psp_8f3a21c4"`))
	if event.Data.PSPReference != "psp_8f3a21c4" {
		t.Errorf("PSPReference = %q, want psp_8f3a21c4", event.Data.PSPReference)
	}
}

func TestVerifyWebhookReadsANullOrAbsentPSPReferenceAsEmpty(t *testing.T) {
	for name, data := range map[string]string{
		"null":   `,"pspReference":null`,
		"absent": ``,
	} {
		t.Run(name, func(t *testing.T) {
			event := mustVerify(t, paymentEvent(data))
			if event.Data.PSPReference != "" {
				t.Errorf("PSPReference = %q, want empty", event.Data.PSPReference)
			}
			if event.Data.TransactionID != "11111111-1111-4111-8111-111111111111" || event.Data.Amount != 2500 {
				t.Errorf("the rest of data must still parse: %+v", event.Data)
			}
		})
	}
}

func TestGetStatusReadsPSPReference(t *testing.T) {
	const id = "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0"
	base := `{"transactionId":"` + id + `","orderId":"dom_9a8b7c6d5e4f","status":"succeeded","amount":8440,"currency":"EUR","createdAt":"2026-08-21T09:15:30.000Z"`
	for name, tc := range map[string]struct{ extra, want string }{
		"set":    {`,"pspReference":"psp_8f3a21c4"`, "psp_8f3a21c4"},
		"null":   {`,"pspReference":null`, ""},
		"absent": {``, ""},
	} {
		t.Run(name, func(t *testing.T) {
			server, _ := newTestServer(t, reply{Body: base + tc.extra + `}`})
			status, err := newTestClient(t, server.URL).GetStatus(context.Background(), id)
			if err != nil {
				t.Fatalf("GetStatus: %v", err)
			}
			if status.PSPReference != tc.want {
				t.Errorf("PSPReference = %q, want %q", status.PSPReference, tc.want)
			}
		})
	}
}
