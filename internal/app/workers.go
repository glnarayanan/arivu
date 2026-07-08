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
	switch job.Type {
	case "reminder.email":
		err = a.processReminderEmailJob(ctx, job.UserID, job.Payload)
	default:
		err = a.bookmarks.ProcessJob(ctx, job.Type, job.Payload)
	}
	if err != nil {
		log.Printf("job %s failed: %v", job.ID, err)
		terminal, failErr := a.jobs.Fail(ctx, job.ID, err.Error())
		if failErr != nil {
			log.Printf("job %s failure update: %v", job.ID, failErr)
			return
		}
		if terminal {
			a.bookmarks.RecordJobTerminalFailure(ctx, job.Type, job.Payload)
		}
		return
	}
	_ = a.jobs.Complete(ctx, job.ID)
}
