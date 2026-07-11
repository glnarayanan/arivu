package jobs

import (
	"context"
	"database/sql"
	"time"

	"github.com/glnarayanan/arivu/internal/ids"
)

type Queue struct {
	db *sql.DB
}

type Job struct {
	ID          string
	UserID      string
	Type        string
	Payload     string
	LeasedUntil string
}

const leaseEligiblePredicate = `((status='queued' AND run_after<=?) OR (status='leased' AND leased_until<=?))`

func New(db *sql.DB) *Queue {
	return &Queue{db: db}
}

func (q *Queue) Enqueue(ctx context.Context, userID, jobType, payload string) error {
	_, err := q.EnqueueWithID(ctx, userID, jobType, payload)
	return err
}

func (q *Queue) EnqueueWithID(ctx context.Context, userID, jobType, payload string) (string, error) {
	return q.EnqueueAt(ctx, userID, jobType, payload, time.Now().UTC())
}

func (q *Queue) EnqueueWithIDTx(ctx context.Context, tx *sql.Tx, userID, jobType, payload string) (string, error) {
	now := time.Now().UTC()
	id := ids.New()
	_, err := tx.ExecContext(ctx, `INSERT INTO jobs(id,user_id,type,status,payload_json,run_after,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, id, nullable(userID), jobType, "queued", payload, now.Format(time.RFC3339), now.Format(time.RFC3339), now.Format(time.RFC3339))
	return id, err
}

func (q *Queue) EnqueueAt(ctx context.Context, userID, jobType, payload string, runAfter time.Time) (string, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	id := ids.New()
	_, err := q.db.ExecContext(ctx, `INSERT INTO jobs(id,user_id,type,status,payload_json,run_after,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, id, nullable(userID), jobType, "queued", payload, runAfter.UTC().Format(time.RFC3339), now, now)
	return id, err
}

func (q *Queue) Lease(ctx context.Context, leaseFor time.Duration) (Job, bool, error) {
	now := time.Now().UTC()
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, false, err
	}
	defer tx.Rollback()

	var job Job
	nowText := now.Format(time.RFC3339)
	leasedUntil := now.Add(leaseFor).Format(time.RFC3339)
	err = tx.QueryRowContext(ctx, `SELECT id,COALESCE(user_id,''),type,payload_json FROM jobs WHERE `+leaseEligiblePredicate+` ORDER BY priority ASC, created_at ASC LIMIT 1`, nowText, nowText).Scan(&job.ID, &job.UserID, &job.Type, &job.Payload)
	if err == sql.ErrNoRows {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE jobs SET status='leased', leased_until=?, attempts=attempts+1, updated_at=? WHERE id=? AND `+leaseEligiblePredicate, leasedUntil, nowText, job.ID, nowText, nowText)
	if err != nil {
		return Job{}, false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Job{}, false, nil
	}
	if err := tx.Commit(); err != nil {
		return Job{}, false, err
	}
	job.LeasedUntil = leasedUntil
	return job, true, nil
}

func (q *Queue) Complete(ctx context.Context, job Job) (bool, error) {
	res, err := q.db.ExecContext(ctx, `UPDATE jobs SET status='completed', leased_until=NULL, updated_at=? WHERE id=? AND status='leased' AND leased_until=?`, time.Now().UTC().Format(time.RFC3339), job.ID, job.LeasedUntil)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (q *Queue) Fail(ctx context.Context, job Job, message string) (bool, bool, error) {
	now := time.Now().UTC()
	res, err := q.db.ExecContext(ctx, `UPDATE jobs SET status=CASE WHEN attempts>=max_attempts THEN 'failed' ELSE 'queued' END, leased_until=NULL, last_error=?, run_after=?, updated_at=? WHERE id=? AND status='leased' AND leased_until=?`, message, now.Add(2*time.Minute).Format(time.RFC3339), now.Format(time.RFC3339), job.ID, job.LeasedUntil)
	if err != nil {
		return false, false, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return false, false, nil
	}
	var status string
	if err := q.db.QueryRowContext(ctx, `SELECT status FROM jobs WHERE id=?`, job.ID).Scan(&status); err != nil {
		return false, false, err
	}
	return status == "failed", true, nil
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
