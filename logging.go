package dominaite

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// NoticeHeader is the response header in which the API tells an SDK that
	// it is deprecated: deprecated; current="0.7.0"; sunset="2027-01-31"; info="https://...".
	NoticeHeader = "Dominaite-Sdk-Notice"

	// RequestIDHeader names the API's id for one call. Quote it to support.
	RequestIDHeader = "Dominaite-Request-Id"

	maxNoticeLength    = 512
	maxRequestIDLength = 128
	maxSeenNotices     = 32

	// clockSkewWarnSeconds is where Ping starts to warn. Requests fail at 300.
	clockSkewWarnSeconds = 60
)

// WithLogger sends the SDK's warnings to logger instead of slog.Default(), and
// turns on the debug log: one line per request at slog.LevelDebug, which your
// handler's level then shows or drops. The default logger never gets debug
// lines.
//
// Warnings are logged at slog.LevelWarn: an SDK notice from the API (once per
// distinct value), each retry of the client's retry policy, and a Ping clock
// skew above 60 seconds. No line ever holds the secret, the signature, the
// cashier token, a query string or a request or response body.
func WithLogger(logger *slog.Logger) Option {
	return func(c *Client) {
		if logger != nil {
			c.logger = logger
		}
	}
}

// noticeSet holds the notice values a client has already warned about.
type noticeSet struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

// warn logs one warning. The default logger is read at call time, so a later
// slog.SetDefault applies. A panicking handler never fails the request.
func (c *Client) warn(ctx context.Context, message string) {
	logger := c.logger
	if logger == nil {
		logger = slog.Default()
	}
	defer func() { _ = recover() }()
	logger.Log(contextOrBackground(ctx), slog.LevelWarn, message)
}

// debugLine logs one request at debug level, only to a logger passed with
// WithLogger. status is the HTTP status, or "error" on a transport failure.
func (c *Client) debugLine(ctx context.Context, method, path, status, requestID, idempotencyKey string, started time.Time) {
	if c.logger == nil {
		return
	}
	ctx = contextOrBackground(ctx)
	defer func() { _ = recover() }()
	if !c.logger.Enabled(ctx, slog.LevelDebug) {
		return
	}

	line := fmt.Sprintf("dominaite-go: %s %s %s %dms", method, path, status, time.Since(started).Milliseconds())
	if requestID != "" {
		line += " request_id=" + requestID
	}
	if idempotencyKey != "" {
		line += " idempotency_key=" + idempotencyKey
	}
	c.logger.Log(ctx, slog.LevelDebug, line)
}

// readNotice warns once per distinct Dominaite-Sdk-Notice value. A missing
// header does nothing.
func (c *Client) readNotice(ctx context.Context, header http.Header) {
	value := cleanHeaderValue(header.Get(NoticeHeader), maxNoticeLength)
	if value == "" {
		return
	}

	set := c.notices
	if set == nil {
		return
	}
	set.mu.Lock()
	_, seen := set.seen[value]
	full := len(set.seen) >= maxSeenNotices
	if !seen && !full {
		if set.seen == nil {
			set.seen = map[string]struct{}{}
		}
		set.seen[value] = struct{}{}
	}
	set.mu.Unlock()

	if seen || full {
		return
	}
	c.warn(ctx, noticeMessage(value))
}

// noticeMessage turns a notice header value into the warning text.
func noticeMessage(value string) string {
	parts := strings.Split(value, ";")
	kind := strings.TrimSpace(parts[0])
	if !strings.EqualFold(kind, "deprecated") {
		return "dominaite-go " + Version + ": " + value
	}

	params := map[string]string{}
	for _, part := range parts[1:] {
		key, val, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		if len(val) >= 2 && strings.HasPrefix(val, `"`) && strings.HasSuffix(val, `"`) {
			val = val[1 : len(val)-1]
		}
		params[strings.ToLower(strings.TrimSpace(key))] = val
	}

	message := "dominaite-go " + Version + " is deprecated"
	if current := params["current"]; current != "" {
		message += "; upgrade to " + current
	}
	if sunset := params["sunset"]; sunset != "" {
		message += " (sunset " + sunset + ")"
	}
	message += "."
	if info := params["info"]; info != "" {
		message += " " + info
	}
	return message
}

// cleanHeaderValue cuts a response header to max bytes and drops control
// characters, so a hostile value cannot forge log lines.
func cleanHeaderValue(value string, max int) string {
	if len(value) > max {
		value = value[:max]
	}
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, value))
}

func contextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// retryReason names a failure for the retry warning: the error code when
// there is one, otherwise the error type. Never a message or a body.
func retryReason(err error) string {
	switch e := err.(type) {
	case *RefusalError:
		if e.ErrorCode != "" {
			return e.ErrorCode
		}
		return "RefusalError"
	case *ChargeError:
		if e.ErrorCode != "" {
			return e.ErrorCode
		}
		return "ChargeError"
	case *TransportError:
		return "TransportError"
	}
	return fmt.Sprintf("%T", err)
}
