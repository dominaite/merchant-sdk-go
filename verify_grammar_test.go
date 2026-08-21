package dominaite

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// The signature header grammar is normative and shared by every Dominaite SDK
// (WEBHOOKS-CONTRACT.md, "Header grammar", 2026-08-21). The ten vectors below
// are pinned verbatim in all of them, so a failure here means this SDK has
// drifted from the wire contract and from its siblings.
//
// The shape that motivated the grammar is vector 6: "t=,v1=garbage,v1=<valid>".
// A parser that collects candidates and accepts if any one matches will happily
// verify that header, which lets a sender smuggle attacker-chosen elements past
// verification alongside the real MAC.
func TestVerifyWebhookHeaderGrammarVectors(t *testing.T) {
	mac := signWebhook(webhookVector.Secret, webhookVector.Timestamp, webhookVector.Body)
	ts := webhookVector.Timestamp

	mustFail := []struct {
		name   string
		header string
	}{
		{"1 missing v1", "t=" + ts},
		{"2 missing t", "v1=" + mac},
		{"3 uppercase hex", "t=" + ts + ",v1=" + strings.ToUpper(mac)},
		{"4 repeated v1", "t=" + ts + ",v1=" + mac + ",v1=" + mac},
		{"5 repeated t", "t=" + ts + ",t=" + ts + ",v1=" + mac},
		{"6 empty t plus repeated v1", "t=,v1=garbage,v1=" + mac},
		{"7 whitespace after comma", "t=" + ts + ", v1=" + mac},
		{"8 non-digit in t", "t=+" + ts + ",v1=" + mac},
		{"9 element without equals", "garbage"},
	}

	for _, tc := range mustFail {
		t.Run(tc.name, func(t *testing.T) {
			_, err := VerifyWebhook([]byte(webhookVector.Body), tc.header, webhookVector.Secret, atVectorTime())
			if err == nil {
				t.Fatalf("header %q must be rejected", tc.header)
			}
			// Same error type as a bad signature: a hostile header is an
			// ordinary rejection, never a different exception for the handler
			// to special-case or leak.
			var verr *WebhookVerificationError
			if !errors.As(err, &verr) {
				t.Fatalf("got %T (%v), want *WebhookVerificationError", err, err)
			}
			if verr.Reason != WebhookReasonMalformedSignature {
				t.Fatalf("Reason = %q, want %q", verr.Reason, WebhookReasonMalformedSignature)
			}
		})
	}

	// 10: an unknown key is ignored, so a future scheme version shipped
	// alongside v1 does not break deployed merchants.
	t.Run("10 unknown key ignored", func(t *testing.T) {
		header := "t=" + ts + ",v1=" + mac + ",v9=deadbeef"
		if _, err := VerifyWebhook([]byte(webhookVector.Body), header, webhookVector.Secret, atVectorTime()); err != nil {
			t.Fatalf("header %q must verify: %v", header, err)
		}
	})
}

// The MAC covers the raw t substring, not a number parsed out of it and printed
// back. A leading zero survives a byte-for-byte parser and disappears in one
// that round-trips through an integer, which is the divergence the grammar
// closes: the two would disagree about the same header.
func TestVerifyWebhookSignsRawTimestampSubstring(t *testing.T) {
	const padded = "01755700000"
	clock := WithWebhookClock(func() time.Time { return time.Unix(1755700000, 0) })

	// Signed as the raw text "01755700000." + body, it verifies.
	header := "t=" + padded + ",v1=" + signWebhook(webhookVector.Secret, padded, webhookVector.Body)
	if _, err := VerifyWebhook([]byte(webhookVector.Body), header, webhookVector.Secret, clock); err != nil {
		t.Fatalf("raw padded timestamp must verify: %v", err)
	}

	// Signed as the reformatted "1755700000", it does not.
	header = "t=" + padded + ",v1=" + signWebhook(webhookVector.Secret, "1755700000", webhookVector.Body)
	_, err := VerifyWebhook([]byte(webhookVector.Body), header, webhookVector.Secret, clock)
	assertReason(t, err, WebhookReasonSignatureMismatch)
}
