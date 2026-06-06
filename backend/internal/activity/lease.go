package activity

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (r *Repository) AcquireActivityLease(ctx context.Context, leaseName, ownerID string, ttl time.Duration) (bool, error) {
	if r.db == nil {
		return false, errors.New("activity repository: no db attached")
	}
	if ttl <= 0 {
		ttl = 2 * time.Minute
	}
	expires := r.now().Add(ttl)
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO t_activity_worker_lease (lease_name, owner, expires_at, heartbeat_at, created_at, updated_at)
		 VALUES (?, ?, ?, UTC_TIMESTAMP(3), UTC_TIMESTAMP(3), UTC_TIMESTAMP(3))
		 ON DUPLICATE KEY UPDATE
		   owner = IF(expires_at < UTC_TIMESTAMP(3) OR owner = VALUES(owner), VALUES(owner), owner),
		   expires_at = IF(expires_at < UTC_TIMESTAMP(3) OR owner = VALUES(owner), VALUES(expires_at), expires_at),
		   heartbeat_at = IF(expires_at < UTC_TIMESTAMP(3) OR owner = VALUES(owner), UTC_TIMESTAMP(3), heartbeat_at),
		   updated_at = UTC_TIMESTAMP(3)`,
		leaseName, ownerID, expires,
	)
	if err != nil {
		return false, err
	}
	var currentOwner string
	row := r.db.QueryRowContext(ctx, `SELECT owner FROM t_activity_worker_lease WHERE lease_name = ?`, leaseName)
	if err := row.Scan(&currentOwner); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return currentOwner == ownerID, nil
}

func (r *Repository) ReleaseActivityLease(ctx context.Context, leaseName, ownerID string) error {
	if r.db == nil {
		return errors.New("activity repository: no db attached")
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM t_activity_worker_lease WHERE lease_name = ? AND owner = ?`, leaseName, ownerID)
	return err
}
