package dominaite

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// captured is one record a test handler saw.
type captured struct {
	Level   slog.Level
	Message string
}

// captureHandler records every record at or above its level.
type captureHandler struct {
	level   slog.Level
	mu      *sync.Mutex
	records *[]captured
	panics  bool
}

func newCapture(level slog.Level) (*slog.Logger, func() []captured) {
	mu := &sync.Mutex{}
	records := &[]captured{}
	handler := &captureHandler{level: level, mu: mu, records: records}
	return slog.New(handler), func() []captured {
		mu.Lock()
		defer mu.Unlock()
		return append([]captured(nil), (*records)...)
	}
}

func (h *captureHandler) Enabled(_ context.Context, level slog.Level) bool { return level >= h.level }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	if h.panics {
		panic("handler blew up")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	*h.records = append(*h.records, captured{Level: r.Level, Message: r.Message})
	return nil
}
func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func messages(records []captured, level slog.Level) []string {
	var out []string
	for _, r := range records {
		if r.Level == level {
			out = append(out, r.Message)
		}
	}
	return out
}

// headerServer answers every call with status and body, and with the headers
// that headers returns for the call's index (0-based).
func headerServer(t *testing.T, status int, body string, headers func(i int) map[string]string) (*httptest.Server, *[]http.Header) {
	t.Helper()
	var count int32
	var mu sync.Mutex
	sent := &[]http.Header{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		mu.Lock()
		*sent = append(*sent, r.Header.Clone())
		mu.Unlock()
		i := int(atomic.AddInt32(&count, 1)) - 1
		for name, value := range headers(i) {
			w.Header().Set(name, value)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return server, sent
}

const sessionBody = `{"success":true,"checkout":{"transactionId":"11111111-1111-4111-8111-111111111111","orderId":"ord_1","cashierKey":"ck_1","cashierToken":"ct_secret_token_9f2","amount":2500,"currency":"EUR","expiresAt":"2026-08-16T12:00:00Z"}}`

const pingBody = `{"success":true,"data":{"pong":true,"merchantId":"m_1","serverTime":"2026-10-17T10:00:00Z","serverUnixTime":1792231200,"clockSkewSeconds":%d}}`

func newLoggedClient(t *testing.T, baseURL string, logger *slog.Logger) *Client {
	t.Helper()
	client, err := New(testKeyID, vector.Secret, WithBaseURL(baseURL), WithLogger(logger))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

func TestNoticeWarnsOncePerDistinctValue(t *testing.T) {
	first := `deprecated; current="0.7.0"; sunset="2027-01-31"; info="https://docs.example/upgrade"`
	second := `deprecated; current="0.8.0"`
	server, _ := headerServer(t, http.StatusOK, sessionBody, func(i int) map[string]string {
		if i < 3 {
			return map[string]string{NoticeHeader: first}
		}
		return map[string]string{NoticeHeader: second}
	})
	logger, records := newCapture(slog.LevelWarn)
	client := newLoggedClient(t, server.URL, logger)

	for i := 0; i < 4; i++ {
		if _, err := client.CreateCheckoutSession(context.Background(), testParams()); err != nil {
			t.Fatalf("CreateCheckoutSession: %v", err)
		}
	}

	got := messages(records(), slog.LevelWarn)
	want := []string{
		"dominaite-go " + Version + " is deprecated; upgrade to 0.7.0 (sunset 2027-01-31). https://docs.example/upgrade",
		"dominaite-go " + Version + " is deprecated; upgrade to 0.8.0.",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("warnings = %q, want %q", got, want)
	}
}

func TestNoticeOnAnErrorResponseAlsoWarns(t *testing.T) {
	server, _ := headerServer(t, http.StatusBadRequest,
		`{"success":false,"error":{"code":"VALIDATION_ERROR","message":"bad"}}`,
		func(int) map[string]string { return map[string]string{NoticeHeader: `deprecated; current="0.7.0"`} })
	logger, records := newCapture(slog.LevelWarn)
	client := newLoggedClient(t, server.URL, logger)

	if _, err := client.CreateCheckoutSession(context.Background(), testParams()); err == nil {
		t.Fatal("want the 400 as an error")
	}
	got := messages(records(), slog.LevelWarn)
	if len(got) != 1 || got[0] != "dominaite-go "+Version+" is deprecated; upgrade to 0.7.0." {
		t.Fatalf("warnings = %q", got)
	}
}

func TestOtherNoticeKindsAreWarnedVerbatim(t *testing.T) {
	server, _ := headerServer(t, http.StatusOK, sessionBody,
		func(int) map[string]string { return map[string]string{NoticeHeader: "maintenance;\tat=\"02:00\""} })
	logger, records := newCapture(slog.LevelWarn)
	client := newLoggedClient(t, server.URL, logger)
	if _, err := client.CreateCheckoutSession(context.Background(), testParams()); err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}
	got := messages(records(), slog.LevelWarn)
	if len(got) != 1 || got[0] != "dominaite-go "+Version+`: maintenance;at="02:00"` {
		t.Fatalf("warnings = %q", got)
	}
}

func TestNoNoticeHeaderMeansNoWarning(t *testing.T) {
	server, _ := headerServer(t, http.StatusOK, sessionBody, func(int) map[string]string { return nil })
	logger, records := newCapture(slog.LevelWarn)
	client := newLoggedClient(t, server.URL, logger)
	if _, err := client.CreateCheckoutSession(context.Background(), testParams()); err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}
	if got := records(); len(got) != 0 {
		t.Fatalf("records = %v, want none", got)
	}
}

func TestWarningsGoToTheDefaultLoggerWithoutWithLogger(t *testing.T) {
	logger, records := newCapture(slog.LevelDebug)
	previous := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(previous) })

	server, _ := headerServer(t, http.StatusOK, sessionBody,
		func(int) map[string]string { return map[string]string{NoticeHeader: `deprecated; current="0.7.0"`} })
	client := newTestClient(t, server.URL)
	if _, err := client.CreateCheckoutSession(context.Background(), testParams()); err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}

	got := records()
	if len(messages(got, slog.LevelWarn)) != 1 {
		t.Fatalf("default logger records = %v, want one warning", got)
	}
	// Debug is off by default: even a debug-level default logger gets no debug line.
	if debug := messages(got, slog.LevelDebug); len(debug) != 0 {
		t.Fatalf("debug lines without WithLogger: %q", debug)
	}
}

func TestDebugIsOffUnlessTheLoggerEnablesIt(t *testing.T) {
	server, _ := headerServer(t, http.StatusOK, sessionBody,
		func(int) map[string]string { return map[string]string{RequestIDHeader: "req-1"} })
	logger, records := newCapture(slog.LevelWarn)
	client := newLoggedClient(t, server.URL, logger)
	if _, err := client.CreateCheckoutSession(context.Background(), testParams()); err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}
	if got := records(); len(got) != 0 {
		t.Fatalf("records at warn level = %v, want none", got)
	}
}

func TestDebugLineHoldsMethodPathStatusRequestIDAndKey(t *testing.T) {
	server, _ := headerServer(t, http.StatusOK, sessionBody,
		func(int) map[string]string { return map[string]string{RequestIDHeader: "req-\t42"} })
	logger, records := newCapture(slog.LevelDebug)
	client := newLoggedClient(t, server.URL, logger)

	if _, err := client.CreateCheckoutSession(context.Background(), testParams()); err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}

	lines := messages(records(), slog.LevelDebug)
	if len(lines) != 1 {
		t.Fatalf("debug lines = %q, want exactly one", lines)
	}
	prefix := "dominaite-go: POST " + SessionsPath + " 200 "
	suffix := "ms request_id=req-42 idempotency_key=" + vector.IdempotencyKey
	if !strings.HasPrefix(lines[0], prefix) || !strings.HasSuffix(lines[0], suffix) {
		t.Fatalf("debug line = %q, want %q<ms>%q", lines[0], prefix, suffix)
	}
}

func TestDebugLineOnATransportFailure(t *testing.T) {
	logger, records := newCapture(slog.LevelDebug)
	client := newLoggedClient(t, "http://127.0.0.1:1", logger)
	if _, err := client.Ping(context.Background()); err == nil {
		t.Fatal("want a transport error")
	}
	lines := messages(records(), slog.LevelDebug)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "dominaite-go: GET "+PingPath+" error ") {
		t.Fatalf("debug lines = %q", lines)
	}
}

func TestNoLogLineHoldsSecretsTokensOrBodies(t *testing.T) {
	notice := `deprecated; current="0.7.0"`
	var mu sync.Mutex
	var signatures []string
	calls := int32(0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		mu.Lock()
		signatures = append(signatures, r.Header.Get("X-Signature"))
		mu.Unlock()
		w.Header().Set(NoticeHeader, notice)
		w.Header().Set(RequestIDHeader, "req-7")
		w.Header().Set("Content-Type", "application/json")
		if atomic.AddInt32(&calls, 1) == 1 {
			_, _ = io.WriteString(w, sessionBody)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"success":false,"error":{"code":"VALIDATION_ERROR","message":"body-fragment-xyz"}}`)
	}))
	t.Cleanup(server.Close)

	logger, records := newCapture(slog.LevelDebug)
	client := newLoggedClient(t, server.URL, logger)
	if _, err := client.CreateCheckoutSession(context.Background(), testParams()); err != nil {
		t.Fatalf("CreateCheckoutSession: %v", err)
	}
	if _, err := client.CreateCheckoutSession(context.Background(), testParams()); err == nil {
		t.Fatal("want the 400")
	}

	got := records()
	if len(got) < 3 {
		t.Fatalf("records = %v, want 2 debug lines and a warning", got)
	}
	forbidden := append([]string{vector.Secret, "ct_secret_token_9f2", "body-fragment-xyz", `"amount"`, "orderReference"}, signatures...)
	for _, r := range got {
		for _, bad := range forbidden {
			if bad != "" && strings.Contains(r.Message, bad) {
				t.Fatalf("log line %q holds %q", r.Message, bad)
			}
		}
	}
}

func TestAPanickingHandlerDoesNotFailTheRequest(t *testing.T) {
	server, _ := headerServer(t, http.StatusOK, sessionBody,
		func(int) map[string]string { return map[string]string{NoticeHeader: `deprecated; current="0.7.0"`} })
	handler := &captureHandler{level: slog.LevelDebug, mu: &sync.Mutex{}, records: &[]captured{}, panics: true}
	client := newLoggedClient(t, server.URL, slog.New(handler))
	if _, err := client.CreateCheckoutSession(context.Background(), testParams()); err != nil {
		t.Fatalf("CreateCheckoutSession with a panicking handler: %v", err)
	}
}

func TestPingWarnsAboveSixtySecondsOfClockSkew(t *testing.T) {
	for _, tc := range []struct {
		skew  int
		warns bool
	}{{61, true}, {-61, true}, {60, false}, {-60, false}, {0, false}} {
		t.Run(fmt.Sprint(tc.skew), func(t *testing.T) {
			server, _ := headerServer(t, http.StatusOK, fmt.Sprintf(pingBody, tc.skew), func(int) map[string]string { return nil })
			logger, records := newCapture(slog.LevelWarn)
			client := newLoggedClient(t, server.URL, logger)
			if _, err := client.Ping(context.Background()); err != nil {
				t.Fatalf("Ping: %v", err)
			}
			got := messages(records(), slog.LevelWarn)
			want := fmt.Sprintf("dominaite-go: clock skew is %d s; requests fail at 300 s. Sync this server's clock.", tc.skew)
			if tc.warns && (len(got) != 1 || got[0] != want) {
				t.Fatalf("warnings = %q, want %q", got, want)
			}
			if !tc.warns && len(got) != 0 {
				t.Fatalf("warnings = %q, want none", got)
			}
		})
	}
}

func TestRetryWarnsBeforeEachRetry(t *testing.T) {
	server, _ := headerServer(t, http.StatusServiceUnavailable, `<html>busy</html>`, func(int) map[string]string { return nil })
	logger, records := newCapture(slog.LevelWarn)
	client := newLoggedClient(t, server.URL, logger)

	_, err := client.CreateCheckoutSessionWithRetry(context.Background(), testParams(), RetryOptions{Attempts: 3, BaseDelay: 1})
	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("err = %v, want *TransportError", err)
	}
	got := messages(records(), slog.LevelWarn)
	want := []string{
		"dominaite-go: retrying CreateCheckoutSessionWithRetry after TransportError (attempt 2 of 3)",
		"dominaite-go: retrying CreateCheckoutSessionWithRetry after TransportError (attempt 3 of 3)",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("warnings = %q, want %q", got, want)
	}
}
