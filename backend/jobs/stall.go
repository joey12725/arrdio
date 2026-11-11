package jobs

import (
	"context"
	"database/sql"
	"time"
)

// A job that leaves the lane without progress waits longer each consecutive
// time, so no number of unfindable or blocked requests can keep the lane busy
// re-trying them: each costs at most one attempt per backoff period.
const (
	missingFirstRetry = time.Hour
	missingGiveUp     = 7 * 24 * time.Hour
	stallFirstRetry   = time.Minute
	stallMaxRetry     = time.Hour
)

// stalledStates make no progress toward a placed file. A job polling an
// active transfer is progressing and keeps the one-minute cadence.
var stalledStates = map[string]bool{"missing": true, "waiting_provider": true, "waiting_library": true, "waiting_allowance": true, "retryable_failure": true, "importing": true}

// stallBackoff records a stalled outcome and returns when the job may run
// again. The seventh day of failed searches ends in not_found, which leaves
// the queue until the request is retried or requested again.
func stallBackoff(ctx context.Context, tx *sql.Tx, job int64, state, message *string, now time.Time) (time.Time, error) {
	if !stalledStates[*state] {
		_, e := tx.ExecContext(ctx, `DELETE FROM job_stalls WHERE job_id=?`, job)
		return now.Add(time.Minute), e
	}
	count, first := 0, now.Unix()
	var previous string
	if tx.QueryRowContext(ctx, `SELECT state,count,first FROM job_stalls WHERE job_id=?`, job).Scan(&previous, &count, &first) == nil && previous != *state {
		count, first = 0, now.Unix()
	}
	count++
	base, ceiling := stallFirstRetry, stallMaxRetry
	if *state == "missing" {
		base, ceiling = missingFirstRetry, missingGiveUp
	}
	delay := ceiling
	if count <= 20 {
		delay = min(base<<(count-1), ceiling)
	}
	next := now.Add(delay)
	if *state == "missing" {
		deadline := time.Unix(first, 0).Add(missingGiveUp)
		if count > 1 && !now.Before(deadline) {
			*state = "not_found"
			*message = "Not found on any source after 7 days of searching; removed from the queue. Retry to search again."
			_, e := tx.ExecContext(ctx, `DELETE FROM job_stalls WHERE job_id=?`, job)
			return now, e
		}
		if next.After(deadline) {
			next = deadline
		}
	}
	_, e := tx.ExecContext(ctx, `INSERT INTO job_stalls VALUES(?,?,?,?) ON CONFLICT(job_id) DO UPDATE SET state=excluded.state,count=excluded.count,first=excluded.first`, job, *state, count, first)
	return next, e
}
