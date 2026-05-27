package listing

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// AcquireLease attempts to claim leaseName for ownerID via a CAS
// against t_listing_worker_lease. The insert path sets the owner;
// the ON DUPLICATE KEY UPDATE branch only takes over the lease when
// the existing lease has expired or the existing owner matches.
// Returns true when the calling process is the current owner, false
// otherwise. Callers should refresh the lease periodically while
// holding it; the long-run engine path acquires a fresh lease at
// each tick.
func (r *Repository) AcquireLease(ctx context.Context, leaseName, ownerID string, ttl time.Duration) (bool, error) {
	if r.db == nil {
		return false, errors.New("listing repository: no db attached")
	}
	if ttl <= 0 {
		ttl = 2 * time.Minute
	}
	expires := r.now().Add(ttl)
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO t_listing_worker_lease (lease_name, owner_id, expires_at)
		 VALUES (?, ?, ?)
		 ON DUPLICATE KEY UPDATE
		   owner_id = IF(expires_at < UTC_TIMESTAMP(6) OR owner_id = VALUES(owner_id), VALUES(owner_id), owner_id),
		   expires_at = IF(expires_at < UTC_TIMESTAMP(6) OR owner_id = VALUES(owner_id), VALUES(expires_at), expires_at)`,
		leaseName, ownerID, expires,
	)
	if err != nil {
		return false, err
	}
	var currentOwner string
	row := r.db.QueryRowContext(ctx, `SELECT owner_id FROM t_listing_worker_lease WHERE lease_name = ?`, leaseName)
	if err := row.Scan(&currentOwner); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return currentOwner == ownerID, nil
}

// ReleaseLease deletes the lease only when the caller still owns it.
// Mismatched owners return nil without error so failed take-overs
// (e.g. lease already expired and taken by another instance) do not
// abort cleanup.
func (r *Repository) ReleaseLease(ctx context.Context, leaseName, ownerID string) error {
	if r.db == nil {
		return errors.New("listing repository: no db attached")
	}
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM t_listing_worker_lease WHERE lease_name = ? AND owner_id = ?`,
		leaseName, ownerID,
	)
	return err
}
