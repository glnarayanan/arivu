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
	ID      string
	UserID  string
	Type    string
	Payload string
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
	err = tx.QueryRowContext(ctx, `SELECT id,COALESCE(user_id,''),type,payload_json FROM jobs WHERE `+leaseEligiblePredicate+` ORDER BY priority ASC, created_at ASC LIMIT 1`, nowText, nowText).Scan(&job.ID, &job.UserID, &job.Type, &job.Payload)
	if err == sql.ErrNoRows {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE jobs SET status='leased', leased_until=?, attempts=attempts+1, updated_at=? WHERE id=? AND `+leaseEligiblePredicate, now.Add(leaseFor).Format(time.RFC3339), nowText, job.ID, nowText, nowText)
	if err != nil {
		return Job{}, false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Job{}, false, nil
	}
	if err := tx.Commit(); err != nil {
		return Job{}, false, err
	}
	return job, true, nil
}

func (q *Queue) Complete(ctx context.Context, id string) error {
	_, err := q.db.ExecContext(ctx, `UPDATE jobs SET status='completed', updated_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

func (q *Queue) Fail(ctx context.Context, id string, message string) (bool, error) {
	now := time.Now().UTC()
	_, err := q.db.ExecContext(ctx, `UPDATE jobs SET status=CASE WHEN attempts>=max_attempts THEN 'failed' ELSE 'queued' END, last_error=?, run_after=?, updated_at=? WHERE id=?`, message, now.Add(2*time.Minute).Format(time.RFC3339), now.Format(time.RFC3339), id)
	if err != nil {
		return false, err
	}
	var status string
	if err := q.db.QueryRowContext(ctx, `SELECT status FROM jobs WHERE id=?`, id).Scan(&status); err != nil {
		return false, err
	}
	return status == "failed", nil
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
