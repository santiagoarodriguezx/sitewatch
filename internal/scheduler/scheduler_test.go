package scheduler

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sitewatch/sitewatch/internal/app"
	"github.com/sitewatch/sitewatch/internal/storage"
)

func TestNoOverlappingCheck(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.AddMonitor(context.Background(), storage.Monitor{Name: "x", URL: "https://example.com", NormalizedURL: "https://example.com/", Interval: time.Minute, Depth: 1, MaxPages: 1, Retention: 2})
	if err != nil {
		t.Fatal(err)
	}
	var running, max atomic.Int32
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	s := Scheduler{Store: db, Poll: 5 * time.Millisecond, Check: func(context.Context, storage.Monitor) (app.CheckResult, error) {
		n := running.Add(1)
		if n > max.Load() {
			max.Store(n)
		}
		started <- struct{}{}
		<-release
		running.Add(-1)
		return app.CheckResult{}, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = s.Run(ctx); close(done) }()
	<-started
	time.Sleep(30 * time.Millisecond)
	close(release)
	cancel()
	<-done
	if max.Load() != 1 {
		t.Fatalf("overlap: %d", max.Load())
	}
}
