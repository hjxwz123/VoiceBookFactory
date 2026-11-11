package jobs

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"voicebook/internal/db/dbtest"
	"voicebook/internal/model"
)

func testQueue(t *testing.T) (*Queue, *gorm.DB) {
	t.Helper()
	db := dbtest.Open(t) // SQLite in memory, or PostgreSQL in CI
	return New(db, map[string]int{"default": 1}), db
}

// A server shutdown must hand running jobs back to the queue rather than cancel them.
func TestShutdownRequeuesRunningJob(t *testing.T) {
	q, db := testQueue(t)
	started := make(chan struct{})
	q.Register("slow", "default", func(ctx context.Context, jc *Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	j := &model.Job{Type: "slow", Title: "slow"}
	if err := q.Enqueue(j); err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	q.Start(ctx)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("job did not start")
	}
	stop()
	q.Wait()
	var got model.Job
	db.First(&got, "id = ?", j.ID)
	if got.Status != model.JobQueued || got.Attempts != 0 {
		t.Fatalf("after shutdown: status=%s attempts=%d", got.Status, got.Attempts)
	}
}

// An explicit cancel still cancels.
func TestUserCancelStillCancels(t *testing.T) {
	q, db := testQueue(t)
	started := make(chan struct{})
	q.Register("slow", "default", func(ctx context.Context, jc *Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	j := &model.Job{Type: "slow", Title: "slow"}
	if err := q.Enqueue(j); err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	q.Start(ctx)
	<-started
	if err := q.Cancel(j.ID); err != nil {
		t.Fatal(err)
	}
	var got model.Job
	for i := 0; i < 50; i++ {
		db.First(&got, "id = ?", j.ID)
		if got.Status == model.JobCanceled {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("status=%s", got.Status)
}
