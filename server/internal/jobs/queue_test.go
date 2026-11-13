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

// Pools take only the jobs of their plans: a plan served by two pools gets both, a plan served by one
// waits for it even while another pool is idle. Changing the pools never interrupts a running job.
func TestPoolsServeTheirPlans(t *testing.T) {
	q, db := testQueue(t)
	q.PlanOf = func(studioID string) string { return studioID } // the studio id names its plan here
	release := make(chan struct{})
	started := make(chan string, 10)
	q.Register("work", "default", func(ctx context.Context, jc *Context) error {
		started <- jc.Job.StudioID + "@" + jc.Job.QueueID
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	q.SetPools([]Pool{
		{ID: "members", Size: 1, Lanes: []string{"default"}, Plans: []string{"vip"}},
		{ID: "everyone", Size: 1, Lanes: []string{"default"}},
	})
	ctx, stop := context.WithCancel(context.Background())
	defer func() { stop(); q.Wait() }()
	q.Start(ctx)
	wait := func() string {
		t.Helper()
		select {
		case s := <-started:
			return s
		case <-time.After(5 * time.Second):
			t.Fatal("no job started")
			return ""
		}
	}
	quiet := func() {
		t.Helper()
		select {
		case s := <-started:
			t.Fatalf("%s started although its plan's queues are busy", s)
		case <-time.After(2500 * time.Millisecond): // longer than a worker's poll
		}
	}
	free1 := &model.Job{Type: "work", StudioID: "free"}
	q.Enqueue(free1)
	if got := wait(); got != "free@everyone" {
		t.Fatalf("free job ran as %s", got)
	}
	// the shared pool is busy: another free job waits although the members' pool is idle
	q.Enqueue(&model.Job{Type: "work", StudioID: "free"})
	quiet()
	// a member's job runs at once on the members' pool
	q.Enqueue(&model.Job{Type: "work", StudioID: "vip"})
	if got := wait(); got != "vip@members" {
		t.Fatalf("member job ran as %s", got)
	}
	var j model.Job
	db.First(&j, "id = ?", free1.ID)
	if j.PlanCode != "free" {
		t.Fatalf("plan recorded as %q", j.PlanCode)
	}
	// open the members' pool to everyone: the waiting free job starts, the running ones go on
	q.SetPools([]Pool{
		{ID: "members", Size: 1, Lanes: []string{"default"}},
		{ID: "everyone", Size: 1, Lanes: []string{"default"}},
		{ID: "extra", Size: 1, Lanes: []string{"default"}},
	})
	if got := wait(); got != "free@members" && got != "free@everyone" && got != "free@extra" {
		t.Fatalf("waiting job ran as %s", got)
	}
	db.First(&j, "id = ?", free1.ID)
	if j.Status != model.JobRunning {
		t.Fatalf("a running job was disturbed by the change: %s", j.Status)
	}
	close(release)
	for i := 0; i < 100; i++ {
		var left int64
		db.Model(&model.Job{}).Where("status <> ?", model.JobSucceeded).Count(&left)
		if left == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("jobs did not finish")
}
