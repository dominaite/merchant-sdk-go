package dominaite

import (
	"context"
	"errors"
	"regexp"
	"testing"
)

func TestUserAgentNamesTheSDKAndTheGoRuntime(t *testing.T) {
	server, calls := newTestServer(t, successReply())
	client := newTestClient(t, server.URL)
	if _, err := client.CreateCheckoutSession(context.Background(), testParams()); err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}

	got := calls()[0].Header.Get("User-Agent")
	pattern := regexp.MustCompile(`^dominaite-go/` + regexp.QuoteMeta(Version) + ` \(go [^ ()]+\)$`)
	if !pattern.MatchString(got) {
		t.Fatalf("User-Agent = %q, want dominaite-go/%s (go <version>)", got, Version)
	}
	t.Logf("User-Agent: %s", got)
}

func TestUserAgentSuffixFollowsTheRuntime(t *testing.T) {
	server, calls := newTestServer(t, successReply())
	client, err := New(testKeyID, vector.Secret, WithBaseURL(server.URL), WithUserAgent("  shop/1.2  "))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := client.CreateCheckoutSession(context.Background(), testParams()); err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}

	got := calls()[0].Header.Get("User-Agent")
	pattern := regexp.MustCompile(`^dominaite-go/` + regexp.QuoteMeta(Version) + ` \(go [^ ()]+\) shop/1\.2$`)
	if !pattern.MatchString(got) {
		t.Fatalf("User-Agent = %q, want the suffix after the runtime", got)
	}
}

func TestUserAgentSuffixWithAControlCharacterIsRefused(t *testing.T) {
	for _, suffix := range []string{"shop\r\nX-Injected: 1", "shop\x00", "shop\x7f", "a\tb"} {
		client, err := New(testKeyID, vector.Secret, WithBaseURL(SandboxBaseURL), WithUserAgent(suffix))
		var validationErr *ValidationError
		if !errors.As(err, &validationErr) || client != nil {
			t.Fatalf("WithUserAgent(%q): New = %v, %v, want *ValidationError", suffix, client, err)
		}
	}
}
