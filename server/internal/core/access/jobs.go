package access

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

// River jobs of the module (design 2.2): access.project drains the queued
// sources of every site, access.reconcile is the nightly safety net (and runs
// on demand for one site), access.prune applies the retention.

// ProjectArgs are the arguments of access.project. The job drains the queue
// of every site that has queued sources, so one periodic instance covers
// vehicles, occupancies and visitor entries; an on-demand insert with a site
// projects just that site.
type ProjectArgs struct {
	OrgID  uuid.UUID `json:"org_id,omitempty"`
	SiteID uuid.UUID `json:"site_id,omitempty"`
}

// Kind implements river.JobArgs.
func (ProjectArgs) Kind() string { return "access.project" }

// ReconcileArgs are the arguments of access.reconcile; empty means every site
// that has access points. Nightly (the periodic run, hourly) narrows that to the
// sites whose local time is NightlyHour.
type ReconcileArgs struct {
	OrgID   uuid.UUID `json:"org_id,omitempty"`
	SiteID  uuid.UUID `json:"site_id,omitempty"`
	Nightly bool      `json:"nightly,omitempty"`
}

// NightlyHour is the site-local hour (0-23) at which the nightly reconcile runs.
const NightlyHour = 3

// ActivateArgs are the arguments of access.activate.
type ActivateArgs struct{}

// Kind implements river.JobArgs.
func (ActivateArgs) Kind() string { return "access.activate" }

// Kind implements river.JobArgs.
func (ReconcileArgs) Kind() string { return "access.reconcile" }

// PruneArgs are the arguments of access.prune.
type PruneArgs struct{}

// Kind implements river.JobArgs.
func (PruneArgs) Kind() string { return "access.prune" }

type projectWorker struct {
	river.WorkerDefaults[ProjectArgs]
	s *Service
}

func (w *projectWorker) Work(ctx context.Context, job *river.Job[ProjectArgs]) error {
	if a := job.Args; a.SiteID != uuid.Nil {
		_, err := w.s.ProjectDirty(ctx, Site(a))
		return err
	}
	sites, err := w.s.DirtySites(ctx)
	if err != nil {
		return err
	}
	var first error
	for _, st := range sites {
		if _, err := w.s.ProjectDirty(ctx, st); err != nil {
			w.s.cfg.Log.ErrorContext(ctx, "access.project failed", "site", st.SiteID, "error", err)
			if first == nil {
				first = err
			}
		}
	}
	return first
}

type reconcileWorker struct {
	river.WorkerDefaults[ReconcileArgs]
	s *Service
}

func (w *reconcileWorker) Work(ctx context.Context, job *river.Job[ReconcileArgs]) error {
	sites := []Site{{OrgID: job.Args.OrgID, SiteID: job.Args.SiteID}}
	if job.Args.SiteID == uuid.Nil {
		var err error
		if job.Args.Nightly {
			sites, err = w.s.SitesAtLocalHour(ctx, NightlyHour)
		} else {
			sites, err = w.s.ActiveSites(ctx)
		}
		if err != nil {
			return err
		}
	}
	var first error
	for _, st := range sites {
		res, err := w.s.Reconcile(ctx, st)
		if err != nil {
			w.s.cfg.Log.ErrorContext(ctx, "access.reconcile failed", "site", st.SiteID, "error", err)
			if first == nil {
				first = err
			}
			continue
		}
		if res.Changed > 0 || res.Expired > 0 {
			w.s.cfg.Log.InfoContext(ctx, "access.reconcile repaired the allow-list", "site", st.SiteID, "changed", res.Changed, "expired", res.Expired)
		}
	}
	return first
}

type activateWorker struct {
	river.WorkerDefaults[ActivateArgs]
	s *Service
}

func (w *activateWorker) Work(ctx context.Context, _ *river.Job[ActivateArgs]) error {
	if _, err := w.s.MarkDueOccupancies(ctx); err != nil {
		return err
	}
	_, err := w.s.ClearSealedRotations(ctx)
	return err
}

type pruneWorker struct {
	river.WorkerDefaults[PruneArgs]
	s *Service
}

func (w *pruneWorker) Work(ctx context.Context, _ *river.Job[PruneArgs]) error {
	sites, err := w.s.ActiveSites(ctx)
	if err != nil {
		return err
	}
	orgs := map[uuid.UUID]bool{}
	for _, st := range sites {
		if _, err := w.s.Prune(ctx, st); err != nil {
			return err
		}
		if !orgs[st.OrgID] {
			orgs[st.OrgID] = true
			if _, err := w.s.PruneEvents(ctx, st.OrgID); err != nil {
				return err
			}
		}
	}
	return nil
}

// Workers registers the module's River workers; pass it to jobs.Options.Register.
func (s *Service) Workers(w *river.Workers) {
	river.AddWorker(w, &projectWorker{s: s})
	river.AddWorker(w, &reconcileWorker{s: s})
	river.AddWorker(w, &activateWorker{s: s})
	river.AddWorker(w, &pruneWorker{s: s})
}

// PeriodicJobs are the module's schedules; pass them to jobs.Options.Periodic.
// River runs them from the elected leader only, so replicas do not repeat them.
func PeriodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(10*time.Second),
			func() (river.JobArgs, *river.InsertOpts) { return ProjectArgs{}, nil }, nil),
		// occupancies that start (or end) today change who may drive in: marked in
		// the first local hour of the day, every 10 minutes so a restart cannot skip it
		river.NewPeriodicJob(river.PeriodicInterval(10*time.Minute),
			func() (river.JobArgs, *river.InsertOpts) { return ActivateArgs{}, nil }, nil),
		// hourly; each site reconciles in its own local hour NightlyHour, whatever the server start time
		river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return ReconcileArgs{Nightly: true}, nil }, nil),
		river.NewPeriodicJob(river.PeriodicInterval(24*time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return PruneArgs{}, nil }, nil),
	}
}
