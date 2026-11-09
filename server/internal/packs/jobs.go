package packs

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/openbekci/openbekci/sdk"
)

// JobArgs is the River arguments of every pack job: one River kind carries all
// packs' jobs, the pack and its job kind are in the arguments.
type JobArgs struct {
	Pack    string `json:"pack"`
	JobKind string `json:"kind"`
	// Org is empty for the periodic trigger, which fans out to every
	// organization that has the pack enabled.
	Org     string `json:"org,omitempty"`
	Payload []byte `json:"payload,omitempty"`
}

// Kind implements river.JobArgs.
func (JobArgs) Kind() string { return "pack.job" }

// EventArgs is the River arguments of an event delivery: one job per (event, subscriber).
type EventArgs struct {
	Pack  string            `json:"pack"`
	Sub   string            `json:"sub"`
	Org   string            `json:"org"`
	Type  string            `json:"type"`
	Ver   int               `json:"version"`
	IDs   map[string]string `json:"ids,omitempty"`
	Nonce string            `json:"nonce,omitempty"`
}

// Kind implements river.JobArgs.
func (EventArgs) Kind() string { return "pack.event" }

type jobWorker struct {
	river.WorkerDefaults[JobArgs]
	h *Host
}

func (w *jobWorker) Work(ctx context.Context, job *river.Job[JobArgs]) error {
	a := job.Args
	if a.Org == "" {
		return w.h.fanOut(ctx, a)
	}
	org, err := uuid.Parse(a.Org)
	if err != nil {
		return river.JobCancel(fmt.Errorf("bad organization id %q", a.Org))
	}
	err = w.h.RunJob(ctx, org, a.Pack, a.JobKind, a.Payload, job.Attempt)
	if errors.Is(err, ErrDisabled) {
		return nil // jobs of a disabled pack are skipped, never errored
	}
	return err
}

// ErrDisabled is returned by RunJob and the event delivery when the pack is not enabled for the organization.
var ErrDisabled = errors.New("packs: the pack is not enabled for the organization")

// fanOut runs a periodic job for every organization with the pack enabled.
func (h *Host) fanOut(ctx context.Context, a JobArgs) error {
	var orgs []uuid.UUID
	err := h.cfg.DB.InPlatformTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT org_id FROM pack_enablement WHERE pack_id = $1 AND enabled ORDER BY org_id`, a.Pack)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var o uuid.UUID
			if err := rows.Scan(&o); err != nil {
				return err
			}
			orgs = append(orgs, o)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, o := range orgs {
		if err := h.RunJob(ctx, o, a.Pack, a.JobKind, nil, 1); err != nil && !errors.Is(err, ErrDisabled) {
			h.log.ErrorContext(ctx, "pack job failed", "pack", a.Pack, "kind", a.JobKind, "org", o, "error", err)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// RunJob runs one job of a pack for an organization now, on the caller's
// goroutine: the worker's entry point, also used by sdktest. It returns
// ErrDisabled when the pack is not enabled for the organization.
func (h *Host) RunJob(ctx context.Context, org uuid.UUID, packID, kind string, payload []byte, attempt int) error {
	pk, ok := h.byID[packID]
	if !ok {
		return fmt.Errorf("packs: unknown pack %q", packID)
	}
	var def *sdk.JobDefinition
	for i := range pk.C.Jobs {
		if pk.C.Jobs[i].Kind == kind {
			def = &pk.C.Jobs[i]
		}
	}
	if def == nil {
		return fmt.Errorf("packs: pack %q has no job %q", packID, kind)
	}
	if on, err := h.Enabled(ctx, org, packID); err != nil {
		return err
	} else if !on {
		return ErrDisabled
	}
	return h.call(ctx, org, packID, "job "+kind, func(ctx context.Context) error {
		return def.Handler(ctx, sdk.Job{Kind: kind, Org: sdk.ID(org.String()), Payload: payload, Attempt: attempt})
	})
}

type eventWorker struct {
	river.WorkerDefaults[EventArgs]
	h *Host
}

func (w *eventWorker) Work(ctx context.Context, job *river.Job[EventArgs]) error {
	a := job.Args
	org, err := uuid.Parse(a.Org)
	if err != nil {
		return river.JobCancel(fmt.Errorf("bad organization id %q", a.Org))
	}
	err = w.h.Deliver(ctx, org, a)
	if errors.Is(err, ErrDisabled) {
		return nil
	}
	return err
}

// Deliver runs one subscriber for one event (the worker's entry point, also used by sdktest).
func (h *Host) Deliver(ctx context.Context, org uuid.UUID, a EventArgs) error {
	pk, ok := h.byID[a.Pack]
	if !ok {
		return fmt.Errorf("packs: unknown pack %q", a.Pack)
	}
	var sub *Subscription
	for i := range pk.C.Subscriptions {
		if pk.C.Subscriptions[i].Name == a.Sub {
			sub = &pk.C.Subscriptions[i]
		}
	}
	if sub == nil {
		return fmt.Errorf("packs: pack %q has no subscription %q", a.Pack, a.Sub)
	}
	// pack.disabled is queued while the pack is enabled and delivered after it was disabled
	if a.Type != sdk.EventPackDisabled {
		if on, err := h.Enabled(ctx, org, a.Pack); err != nil {
			return err
		} else if !on {
			return ErrDisabled
		}
	}
	ev := sdk.Event{Type: a.Type, Version: a.Ver, Org: sdk.ID(org.String()), IDs: map[string]sdk.ID{}}
	for k, v := range a.IDs {
		ev.IDs[k] = sdk.ID(v)
	}
	return h.call(ctx, org, a.Pack, "subscription "+a.Sub, func(ctx context.Context) error { return sub.Handler(ctx, ev) })
}

// RegisterWorkers adds the pack job and event workers to River.
func (h *Host) RegisterWorkers(w *river.Workers) {
	river.AddWorker(w, &jobWorker{h: h})
	river.AddWorker(w, &eventWorker{h: h})
}

// PeriodicJobs are the periodic triggers of the pack jobs that declare Every.
func (h *Host) PeriodicJobs() []*river.PeriodicJob {
	var out []*river.PeriodicJob
	for _, p := range h.packs {
		for _, j := range p.C.Jobs {
			if j.Every <= 0 {
				continue
			}
			args := JobArgs{Pack: p.Manifest.ID, JobKind: j.Kind}
			out = append(out, river.NewPeriodicJob(river.PeriodicInterval(j.Every),
				func() (river.JobArgs, *river.InsertOpts) { return args, nil }, &river.PeriodicJobOpts{}))
		}
	}
	return out
}

// Emit delivers a core event to the subscribers of the packs enabled for org,
// one River job per (event, subscriber), inserted in tx so the job exists
// exactly when the change committed. Identical events (same type and ids) are
// deduplicated by River's unique-job check.
func (h *Host) Emit(ctx context.Context, tx pgx.Tx, org uuid.UUID, ev sdk.Event) error {
	if len(h.packs) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT pack_id FROM pack_enablement WHERE org_id = $1 AND enabled`, org)
	if err != nil {
		return err
	}
	on := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		on[id] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range h.packs {
		if !on[p.Manifest.ID] {
			continue
		}
		for _, s := range p.C.Subscriptions {
			if s.EventType != ev.Type {
				continue
			}
			a := EventArgs{Pack: p.Manifest.ID, Sub: s.Name, Org: org.String(), Type: ev.Type, Ver: 1, IDs: map[string]string{}}
			for k, v := range ev.IDs {
				a.IDs[k] = string(v)
			}
			if ev.Type == sdk.EventPackEnabled || ev.Type == sdk.EventPackDisabled {
				a.Nonce = uuid.NewString()
			}
			c := h.client.Load()
			if c == nil {
				h.log.WarnContext(ctx, "pack event dropped: the job client is not started yet", "type", ev.Type, "pack", a.Pack)
				continue
			}
			if _, err := c.InsertTx(ctx, tx, a, &river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}}); err != nil {
				return fmt.Errorf("enqueue pack event: %w", err)
			}
		}
	}
	return nil
}

type jobQueue struct {
	h    *Host
	pack string
}

func (q jobQueue) Enqueue(ctx context.Context, kind string, payload []byte) error {
	org, err := orgFromCtx(ctx)
	if err != nil {
		return err
	}
	pk := q.h.byID[q.pack]
	known := false
	for _, j := range pk.C.Jobs {
		known = known || j.Kind == kind
	}
	if !known {
		return fmt.Errorf("packs: pack %q has no job %q", q.pack, kind)
	}
	c := q.h.client.Load()
	if c == nil {
		return errors.New("packs: the job queue is not started")
	}
	_, err = c.Insert(ctx, JobArgs{Pack: q.pack, JobKind: kind, Org: org.String(), Payload: payload}, nil)
	return err
}
