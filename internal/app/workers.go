package app

import (
	"context"
	"log"
	"time"

	"github.com/glnarayanan/arivu/internal/providers"
	"github.com/glnarayanan/arivu/internal/safefetch"
)

const (
	jobLease                 = 4 * time.Minute
	bookmarkLeaseMargin      = 90 * time.Second
	bookmarkProcessingBudget = safefetch.RequestTimeout + providers.SummaryGenerationBudget + providers.EmbeddingRequestTimeout
)

func (a *App) startWorkers(ctx context.Context) {
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.runOneJob(ctx)
			}
		}
	}()
}

func (a *App) runOneJob(ctx context.Context) {
	job, ok, err := a.jobs.Lease(ctx, jobLease)
	if err != nil {
		log.Printf("job lease: %v", err)
		return
	}
	if !ok {
		return
	}
	switch job.Type {
	case "reminder.email":
		err = a.processReminderEmailJob(ctx, job.UserID, job.Payload)
	default:
		err = a.bookmarks.ProcessJob(ctx, job.Type, job.Payload)
	}
	if err != nil {
		log.Printf("job %s failed: %v", job.ID, err)
		terminal, active, failErr := a.jobs.Fail(ctx, job, err.Error())
		if failErr != nil {
			log.Printf("job %s failure update: %v", job.ID, failErr)
			return
		}
		if terminal && active {
			a.bookmarks.RecordJobTerminalFailure(ctx, job.Type, job.Payload)
		}
		return
	}
	if completed, err := a.jobs.Complete(ctx, job); err != nil {
		log.Printf("job %s complete update: %v", job.ID, err)
	} else if !completed {
		log.Printf("job %s complete skipped: stale lease", job.ID)
	}
}
