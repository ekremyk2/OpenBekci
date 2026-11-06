package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// PoolerInfo is the result of DetectPooler.
type PoolerInfo struct {
	Detected      bool
	Reason        string
	ServerVersion string
}

// DetectPooler checks whether url points at a transaction pooler (PgBouncer and
// the like) rather than directly at PostgreSQL. It uses two signals: PgBouncer's
// SHOW POOL_MODE, which plain PostgreSQL rejects, and a LISTEN/NOTIFY round trip
// on one connection, which a transaction pooler breaks.
func DetectPooler(ctx context.Context, url string) (PoolerInfo, error) {
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return PoolerInfo{}, fmt.Errorf("parse url: %w", err)
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return PoolerInfo{}, fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	var info PoolerInfo
	if err := conn.QueryRow(ctx, `SHOW server_version`).Scan(&info.ServerVersion); err != nil {
		return PoolerInfo{}, fmt.Errorf("show server_version: %w", err)
	}

	var mode string
	if err := conn.QueryRow(ctx, `SHOW pool_mode`).Scan(&mode); err == nil {
		info.Detected, info.Reason = true, "pgbouncer pool_mode="+mode
		return info, nil
	}
	// The failed SHOW is expected on plain PostgreSQL; the connection is still usable.

	if _, err := conn.Exec(ctx, `LISTEN openbekci_probe`); err != nil {
		return info, fmt.Errorf("listen: %w", err)
	}
	if _, err := conn.Exec(ctx, `SELECT pg_notify('openbekci_probe', '1')`); err != nil {
		return info, fmt.Errorf("notify: %w", err)
	}
	wctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if _, err := conn.WaitForNotification(wctx); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			info.Detected, info.Reason = true, "LISTEN/NOTIFY does not round-trip on one connection"
			return info, nil
		}
		return info, fmt.Errorf("wait for notification: %w", err)
	}
	return info, nil
}
