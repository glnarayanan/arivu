package app

import (
	"context"
	"log"
	"time"
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
	job, ok, err := a.jobs.Lease(ctx, 2*time.Minute)
	if err != nil {
		log.Printf("job lease: %v", err)
		return
	}
	if !ok {
		return
	}
	if err := a.bookmarks.ProcessJob(ctx, job.Type, job.Payload); err != nil {
		log.Printf("job %s failed: %v", job.ID, err)
		_ = a.jobs.Fail(ctx, job.ID, err.Error())
		return
	}
	_ = a.jobs.Complete(ctx, job.ID)
}
