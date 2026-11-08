package integrations

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/openbekci/openbekci/server/internal/core/audit"
	"github.com/openbekci/openbekci/server/internal/core/notifications"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
)

// DeliverArgs is the arguments of webhooks.deliver: one job per (event,
// endpoint). The event body lives in the delivery row, so retries send exactly
// the same JSON; the job is unique on (endpoint, event).
type DeliverArgs struct {
	OrgID      uuid.UUID `json:"org_id"`
	EndpointID uuid.UUID `json:"endpoint_id" river:"unique"`
	EventID    uuid.UUID `json:"event_id" river:"unique"`
	DeliveryID uuid.UUID `json:"delivery_id"`
}

// Kind implements river.JobArgs.
func (DeliverArgs) Kind() string { return "webhooks.deliver" }

// InsertOpts implements river.JobArgsWithInsertOpts. MaxAttempts is set per
// insert from WEBHOOK_MAX_ATTEMPTS; this is the fallback.
func (DeliverArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: DefaultMaxAttempts, UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

type deliverWorker struct {
	river.WorkerDefaults[DeliverArgs]
	s *Service
}

// NextRetry implements river.Worker: the delay after a failed attempt is
// RetryDelay (exponential, from 30 seconds), not River's default curve.
func (w *deliverWorker) NextRetry(job *river.Job[DeliverArgs]) time.Time {
	return w.s.now().Add(w.s.backoff(job.Attempt))
}

func (w *deliverWorker) Work(ctx context.Context, job *river.Job[DeliverArgs]) error {
	return w.s.Deliver(ctx, job.Args, job.Attempt, job.MaxAttempts)
}

// Workers registers the module's job workers.
func (s *Service) Workers(w *river.Workers) {
	river.AddWorker(w, &deliverWorker{s: s})
}

// Insert returns the InsertFunc over a River client, for SetInsert.
func Insert(c *jobs.Client) InsertFunc {
	return func(ctx context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) error {
		_, err := c.InsertTx(ctx, tx, args, opts)
		return err
	}
}

// attemptResult is the outcome of one POST: err is empty on success.
type attemptResult struct {
	code int
	err  string
}

// Deliver makes one attempt to deliver a webhook: it loads the delivery and
// its endpoint, signs the stored body with the endpoint's secret and POSTs it
// through the guarded client. A 2xx response succeeds. Anything else (a
// refused address, a timeout, a redirect, a non-2xx status) is a failed
// attempt: the delivery stays pending with next_retry_at while attempts
// remain, and is marked failed on the last one, which also marks the endpoint
// failing and tells the organization's administrators (once per failing
// streak). An error is returned for failed attempts so River schedules the
// retry; a delivery that no longer needs sending returns nil.
func (s *Service) Deliver(ctx context.Context, a DeliverArgs, attempt, maxAttempts int) error {
	if maxAttempts <= 0 {
		maxAttempts = s.maxTry
	}
	var (
		body     []byte
		event    string
		url      string
		sealed   []byte
		prev     []byte // the rotated-out secret while its overlap lasts
		active   bool
		status   string
		proceed  bool
		endpoint uuid.UUID
	)
	err := s.db.InTenantTx(ctx, a.OrgID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT d.status, d.event_type, d.body, e.url, e.secret_sealed,
				CASE WHEN e.secret_previous_expires_at > now() THEN e.secret_previous_sealed END, e.active, e.id
			FROM webhook_delivery d JOIN webhook_endpoint e ON e.org_id = d.org_id AND e.id = d.endpoint_id
			WHERE d.org_id = $1 AND d.id = $2`, a.OrgID, a.DeliveryID).Scan(&status, &event, &body, &url, &sealed, &prev, &active, &endpoint)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // the endpoint (and with it the delivery) was deleted
		}
		if err != nil {
			return err
		}
		proceed = status == "pending"
		if proceed && !active && event != EventPing {
			// switched off since the event was queued: nothing is sent, and the log says why
			proceed = false
			_, err = tx.Exec(ctx, `UPDATE webhook_delivery SET status = 'failed', last_error = 'endpoint_inactive', next_retry_at = NULL, updated_at = now()
				WHERE org_id = $1 AND id = $2`, a.OrgID, a.DeliveryID)
		}
		return err
	})
	if err != nil {
		return fmt.Errorf("load webhook delivery: %w", err)
	}
	if !proceed {
		return nil
	}
	if s.cipher == nil {
		return errors.New("webhooks: no encryption key configured")
	}
	secret, err := s.cipher.Open(sealed, secretAAD(a.OrgID, endpoint))
	if err != nil {
		return fmt.Errorf("open webhook secret: %w", err)
	}
	secrets := []string{string(secret)}
	if prev != nil {
		old, err := s.cipher.Open(prev, secretAAD(a.OrgID, endpoint))
		if err != nil {
			return fmt.Errorf("open previous webhook secret: %w", err)
		}
		secrets = append(secrets, string(old))
	}
	res := s.post(ctx, url, secrets, a.DeliveryID, event, body)

	var failErr error
	err = s.db.InTenantTx(ctx, a.OrgID, func(tx pgx.Tx) error {
		now := s.now()
		if res.err == "" {
			if _, err := tx.Exec(ctx, `UPDATE webhook_delivery SET status = 'succeeded', attempts = $3, response_code = $4, last_error = NULL,
					next_retry_at = NULL, delivered_at = $5, updated_at = $5 WHERE org_id = $1 AND id = $2`, a.OrgID, a.DeliveryID, attempt, res.code, now); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `UPDATE webhook_endpoint SET failing_since = NULL, consecutive_failures = 0
				WHERE org_id = $1 AND id = $2 AND (failing_since IS NOT NULL OR consecutive_failures <> 0)`, a.OrgID, a.EndpointID)
			return err
		}
		failErr = fmt.Errorf("webhook delivery %s attempt %d: %s", a.DeliveryID, attempt, res.err)
		var code *int
		if res.code != 0 {
			code = &res.code
		}
		if attempt < maxAttempts {
			retry := now.Add(s.backoff(attempt))
			_, err := tx.Exec(ctx, `UPDATE webhook_delivery SET attempts = $3, response_code = $4, last_error = $5, next_retry_at = $6, updated_at = $7
				WHERE org_id = $1 AND id = $2`, a.OrgID, a.DeliveryID, attempt, code, res.err, retry, now)
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE webhook_delivery SET status = 'failed', attempts = $3, response_code = $4, last_error = $5, next_retry_at = NULL, updated_at = $6
			WHERE org_id = $1 AND id = $2`, a.OrgID, a.DeliveryID, attempt, code, res.err, now); err != nil {
			return err
		}
		return s.markFailing(ctx, tx, a, url, now)
	})
	if err != nil {
		return fmt.Errorf("record webhook delivery: %w", err)
	}
	return failErr
}

// markFailing counts a used-up delivery against the endpoint. The first one of
// a streak sets failing_since and notifies the organization's administrators;
// a later success clears it, so a flapping endpoint notifies once per streak.
func (s *Service) markFailing(ctx context.Context, tx pgx.Tx, a DeliverArgs, url string, now time.Time) error {
	var first bool
	err := tx.QueryRow(ctx, `UPDATE webhook_endpoint SET consecutive_failures = consecutive_failures + 1,
			failing_since = COALESCE(failing_since, $3)
		WHERE org_id = $1 AND id = $2 RETURNING failing_since = $3`, a.OrgID, a.EndpointID, now).Scan(&first)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil || !first {
		return err
	}
	diff, err := audit.Diff("webhook_endpoint", map[string]any{"failing": false}, map[string]any{"failing": true})
	if err != nil {
		return err
	}
	if err := audit.Record(ctx, tx, audit.Event{OrgID: a.OrgID, ActorKind: audit.ActorSystem, Action: "webhook.failing", TargetType: "webhook_endpoint", TargetID: a.EndpointID, Diff: diff}); err != nil {
		return err
	}
	if s.cfg.Notifier == nil {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT person_id FROM membership WHERE org_id = $1 AND role = 'org_admin' AND scope_type = 'org'
		AND valid_from <= now() AND (valid_to IS NULL OR valid_to > now()) ORDER BY person_id`, a.OrgID)
	if err != nil {
		return err
	}
	var rcpt []jobs.Recipient
	for rows.Next() {
		var p uuid.UUID
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return err
		}
		rcpt = append(rcpt, jobs.Recipient{PersonID: &p})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	return s.cfg.Notifier.Notify(ctx, tx, jobs.NotifyArgs{
		OrgID: a.OrgID, Type: notifications.TypeWebhookFailing, Recipients: rcpt,
		Payload: map[string]any{"host": hostOf(url), "endpoint_id": a.EndpointID},
		Key:     "webhook.failing:" + a.EndpointID.String() + ":" + now.Format(time.RFC3339Nano),
	})
}

// post sends one signed request and classifies the outcome. It never returns
// the response body: at most MaxResponseBytes are read and dropped.
func (s *Service) post(ctx context.Context, url string, secrets []string, deliveryID uuid.UUID, event string, body []byte) attemptResult {
	ctx, cancel := context.WithTimeout(ctx, DeliveryTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return attemptResult{err: "bad_request: " + clip(err.Error())}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "OpenBekci-Webhooks/1")
	req.Header.Set(HeaderSignature, SignAll(secrets, s.now(), body))
	req.Header.Set(HeaderDeliveryID, deliveryID.String())
	req.Header.Set(HeaderEvent, event)
	resp, err := s.client.Do(req)
	if err != nil {
		if errors.Is(err, ErrForbiddenTarget) {
			return attemptResult{err: "forbidden_target: " + clip(err.Error())}
		}
		return attemptResult{err: "request_failed: " + clip(errText(err))}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, MaxResponseBytes))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return attemptResult{code: resp.StatusCode}
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return attemptResult{code: resp.StatusCode, err: fmt.Sprintf("redirect_not_followed: status %d", resp.StatusCode)}
	}
	return attemptResult{code: resp.StatusCode, err: fmt.Sprintf("http_status: %d", resp.StatusCode)}
}

// errText keeps transport errors short and free of the request URL (which may
// carry a token in its query).
func errText(err error) string {
	var ue *neturl.Error
	if errors.As(err, &ue) && ue.Err != nil {
		return ue.Err.Error()
	}
	return err.Error()
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		return s[:300]
	}
	return s
}
