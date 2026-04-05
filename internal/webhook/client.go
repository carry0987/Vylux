package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"Vylux/internal/db/dbq"
	"Vylux/internal/signature"
	apptracing "Vylux/internal/tracing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

const maxRetries = 5
const requestTimeout = 10 * time.Second

// CallbackPayload is the JSON body sent to the callback URL.
type CallbackPayload struct {
	JobID   string `json:"job_id"`
	Type    string `json:"type"`
	Hash    string `json:"hash"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
	Results any    `json:"results,omitempty"`
}

// Client delivers webhook callbacks to the caller's endpoint.
type Client struct {
	httpClient *http.Client
	secret     string
	queries    *dbq.Queries
}

// NewClient creates a webhook Client.
func NewClient(secret string, queries *dbq.Queries) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: requestTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		secret:  secret,
		queries: queries,
	}
}

// Deliver sends a webhook callback with exponential backoff retries.
func (c *Client) Deliver(ctx context.Context, jobID string, callbackURL string, payload CallbackPayload) {
	if callbackURL == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	ctx, cancel := context.WithTimeout(apptracing.BackgroundContext(ctx), 5*time.Minute)
	defer cancel()

	body, err := json.Marshal(payload)
	if err != nil {
		slog.Error("webhook marshal failed", apptracing.LogFields(ctx, "job_id", jobID, "error", err)...)
		c.markStatus(ctx, jobID, "callback_failed")
		return
	}

	sig := signature.SignWebhook(c.secret, body)
	backoff := 1 * time.Second

	for attempt := 1; attempt <= maxRetries; attempt++ {
		err := c.send(ctx, callbackURL, body, sig)
		if err == nil {
			slog.Info("webhook delivered",
				apptracing.LogFields(ctx,
					"job_id", jobID,
					"url", callbackURL,
					"attempt", attempt,
				)...,
			)
			c.markStatus(ctx, jobID, "sent")
			return
		}

		slog.Warn("webhook delivery failed",
			apptracing.LogFields(ctx,
				"job_id", jobID,
				"url", callbackURL,
				"attempt", fmt.Sprintf("%d/%d", attempt, maxRetries),
				"error", err,
			)...,
		)

		if attempt < maxRetries {
			select {
			case <-ctx.Done():
				slog.Error("webhook delivery cancelled", apptracing.LogFields(ctx, "job_id", jobID)...)
				c.markStatus(apptracing.BackgroundContext(ctx), jobID, "callback_failed")
				return
			case <-time.After(backoff):
				backoff *= 2
			}
		}
	}

	slog.Error("webhook delivery exhausted retries",
		apptracing.LogFields(ctx,
			"job_id", jobID,
			"url", callbackURL,
			"retries", maxRetries,
		)...,
	)
	c.markStatus(ctx, jobID, "callback_failed")
}

func (c *Client) send(ctx context.Context, url string, body []byte, sig string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Signature", sig)
	req.Header.Set("User-Agent", "Vylux-Webhook/1.0")
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))
	if traceID := apptracing.TraceID(ctx); traceID != "" {
		req.Header.Set(apptracing.HeaderTraceID, traceID)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("callback returned HTTP %d", resp.StatusCode)
}

func (c *Client) markStatus(ctx context.Context, jobID, status string) {
	if err := c.queries.UpdateCallbackStatus(ctx, dbq.UpdateCallbackStatusParams{
		ID:             jobID,
		CallbackStatus: status,
	}); err != nil {
		slog.Error("update callback status failed",
			apptracing.LogFields(ctx,
				"job_id", jobID,
				"status", status,
				"error", err,
			)...,
		)
	}
}
