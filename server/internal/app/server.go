package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"time"

	"github.com/openbekci/openbekci/server/internal/core/access"
	"github.com/openbekci/openbekci/server/internal/core/community"
	"github.com/openbekci/openbekci/server/internal/core/files"
	"github.com/openbekci/openbekci/server/internal/core/governance"
	"github.com/openbekci/openbekci/server/internal/core/tenancy"
	"github.com/openbekci/openbekci/server/internal/httpapi"
	"github.com/openbekci/openbekci/server/internal/platform/db"
	"github.com/openbekci/openbekci/server/internal/platform/idempotency"
	"github.com/openbekci/openbekci/server/internal/platform/jobs"
	"github.com/riverqueue/river"
)

const shutdownTimeout = 20 * time.Second

// Serve runs the HTTP server, and the job worker unless withWorker is false,
// until ctx is cancelled. Config.Worker is the caller's concern: pass
// cfg.Worker && !noWorker.
func Serve(ctx context.Context, cfg Config, log *slog.Logger, withWorker bool) error {
	rt, err := start(ctx, cfg, log, withWorker)
	if err != nil {
		return err
	}
	defer rt.db.Close()

	ln, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		rt.stopJobs(log)
		return fmt.Errorf("listen on %s: %w", cfg.ListenAddr, err)
	}

	deps, err := apiDeps(cfg, log, rt.db, rt.idem, rt.files, rt.comm, rt.ops.late)
	if err != nil {
		_ = ln.Close()
		rt.stopJobs(log)
		return err
	}
	go pruneIdentity(ctx, log, deps.Identity)
	go rt.access.Run(ctx) // idle long-poll sessions
	deps.Jobs = rt.jobs
	deps.Ops = rt.ops.deps()
	deps.Governance = rt.gov
	deps.Access = rt.access
	router := httpapi.NewRouter(
		deps,
		httpapi.Options{TrustedProxies: cfg.TrustedProxies, MaxBodyBytes: cfg.MaxBodyBytes, MaxUploadBytes: uploadBodyLimit(cfg), Ready: readiness(cfg, rt.db)},
	)
	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		BaseContext:       func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
	}
	log.Info("http server listening", "addr", ln.Addr().String(), "worker", withWorker)

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case err := <-errc:
		rt.stopJobs(log)
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}

	log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	var errs []error
	if err := srv.Shutdown(sctx); err != nil {
		errs = append(errs, fmt.Errorf("http shutdown: %w", err))
	}
	if err := deps.Identity.Drain(sctx); err != nil { // code and link sends still in flight
		errs = append(errs, err)
	}
	if err := rt.stopJobsCtx(sctx); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// uploadBodyLimit is the request body cap of the upload route: the file limit
// plus multipart framing. Zero (limit unset) lets the HTTP layer use its default.
func uploadBodyLimit(cfg Config) int64 {
	if cfg.MaxUploadBytes <= 0 {
		return 0
	}
	return httpapi.UploadBodyLimit(cfg.MaxUploadBytes)
}

// RunWorker runs only the job worker until ctx is cancelled.
func RunWorker(ctx context.Context, cfg Config, log *slog.Logger) error {
	rt, err := start(ctx, cfg, log, true)
	if err != nil {
		return err
	}
	defer rt.db.Close()
	log.Info("worker running")
	<-ctx.Done()
	log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return rt.stopJobsCtx(sctx)
}

// runtime is what serve and worker share: pools and the River client.
type runtime struct {
	db      *db.DB
	jobs    *jobs.Client
	gov     *governance.Service
	comm    *community.Service
	idem    *idempotency.PG
	files   *files.Service
	ops     *opsServices
	access  *access.Service
	working bool
}

func start(ctx context.Context, cfg Config, log *slog.Logger, withWorker bool) (*runtime, error) {
	if err := cfg.ValidateServe(); err != nil {
		return nil, err
	}
	if err := CheckDataDir(cfg.DataDir); err != nil {
		return nil, err
	}
	if cfg.InsecureDev {
		log.Warn("INSECURE_DEV is enabled; never use it on a reachable host")
	}
	if cfg.AutoMigrate {
		if err := Migrate(ctx, cfg, log, "up", io.Discard); err != nil {
			return nil, err
		}
	}

	pooler, err := db.DetectPooler(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("detect connection pooler: %w", err)
	}
	log.Info("database reachable", "server_version", pooler.ServerVersion, "pooler", pooler.Detected, "pooler_reason", pooler.Reason)
	if pooler.Detected {
		log.Warn("connection pooler detected; using describe-based statement caching and River PollOnly")
	}

	d, err := db.Open(ctx, db.Options{
		AppURL:      cfg.DatabaseURL,
		PlatformURL: cfg.PlatformDatabaseURL,
		Pooled:      pooler.Detected,
	})
	if err != nil {
		return nil, err
	}
	if err := d.CheckAppRole(ctx); err != nil {
		d.Close()
		return nil, err
	}
	if err := d.CheckPlatformRole(ctx); err != nil {
		d.Close()
		return nil, err
	}
	d.WarnIfPlatformSuperuser(ctx, log)

	idem := idempotency.NewPG(d)
	fs, err := newFiles(ctx, cfg, log, d)
	if err != nil {
		d.Close()
		return nil, err
	}
	ops, err := newOps(cfg, log, d, fs)
	if err != nil {
		d.Close()
		return nil, err
	}
	gov := newGovernance(cfg, log, d, fs)
	registerGov, periodicGov := gov.JobOptions()
	comm, err := newCommunity(cfg, log, d, ops.late)
	if err != nil {
		d.Close()
		return nil, err
	}
	registerMoney, periodicMoney := moneyJobs(d, log)
	acc, err := newAccess(cfg, log, d, comm)
	if err != nil {
		d.Close()
		return nil, err
	}
	client, err := jobs.New(d.WorkerPool(), log, jobs.Options{Work: withWorker, PollOnly: pooler.Detected, Idempotency: idem, ExpireArchives: tenancy.ArchivePurger{DB: d}, Files: fs,
		Notifications: ops.notifications, Tasks: ops.tasks(), Register: func(w *river.Workers) { registerGov(w); comm.RegisterWorkers(w); acc.Workers(w); registerMoney(w) },
		Periodic: slices.Concat(periodicGov, community.PeriodicJobs(), access.PeriodicJobs(), periodicMoney)})
	if err != nil {
		d.Close()
		return nil, err
	}
	ops.late.c.Store(client)
	gov.SetNotifier(governance.RiverNotifier{Client: client})
	if withWorker {
		if err := client.Start(context.WithoutCancel(ctx)); err != nil {
			d.Close()
			return nil, fmt.Errorf("start river: %w", err)
		}
	}
	return &runtime{db: d, jobs: client, gov: gov, comm: comm, access: acc, idem: idem, files: fs, ops: ops, working: withWorker}, nil
}

func (rt *runtime) stopJobsCtx(ctx context.Context) error {
	if !rt.working {
		return nil
	}
	if err := rt.jobs.Stop(ctx); err != nil {
		return fmt.Errorf("stop river: %w", err)
	}
	return nil
}

func (rt *runtime) stopJobs(log *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := rt.stopJobsCtx(ctx); err != nil {
		log.Error("stop jobs", "error", err)
	}
}

// readiness are the /readyz probes.
func readiness(cfg Config, d *db.DB) []httpapi.Check {
	return []httpapi.Check{
		{Name: "database", Fn: d.PingApp},
		{Name: "migrations", Fn: d.MigrationsCurrent},
		{Name: "platform_role", Fn: d.CheckPlatformRole},
		{Name: "data_dir", Fn: func(context.Context) error { return CheckDataDir(cfg.DataDir) }},
	}
}

// Migrate runs a migrate subcommand (up, down or status) as the migrator role.
// Status output goes to out.
func Migrate(ctx context.Context, cfg Config, log *slog.Logger, action string, out io.Writer) error {
	if err := cfg.ValidateMigrate(); err != nil {
		return err
	}
	d, err := db.Open(ctx, db.Options{MigratorURL: cfg.MigratorDatabaseURL})
	if err != nil {
		return err
	}
	defer d.Close()

	switch action {
	case "up":
		log.Info("applying migrations")
		return d.MigrateUp(ctx)
	case "down":
		log.Info("rolling back one migration")
		return d.MigrateDown(ctx)
	case "status":
		return d.MigrateStatus(ctx, out)
	}
	return fmt.Errorf("unknown migrate action %q", action)
}
