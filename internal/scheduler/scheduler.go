package scheduler

import (
	"context"
	"sync"
	"time"

	"github.com/sitewatch/sitewatch/internal/app"
	"github.com/sitewatch/sitewatch/internal/storage"
)

type Event struct {
	Monitor storage.Monitor
	Result  app.CheckResult
	Err     error
}
type Scheduler struct {
	Store       *storage.DB
	Concurrency int
	Poll        time.Duration
	Check       func(context.Context, storage.Monitor) (app.CheckResult, error)
	Events      chan<- Event
	mu          sync.Mutex
	active      map[int64]bool
	wg          sync.WaitGroup
}

func (s *Scheduler) Run(ctx context.Context) error {
	if s.Concurrency < 1 {
		s.Concurrency = 10
	}
	if s.Poll <= 0 {
		s.Poll = time.Second
	}
	s.active = map[int64]bool{}
	sem := make(chan struct{}, s.Concurrency)
	tick := time.NewTicker(s.Poll)
	defer tick.Stop()
	for {
		if err := s.runDue(ctx, sem); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			s.wg.Wait()
			return nil
		case <-tick.C:
		}
	}
}
func (s *Scheduler) runDue(ctx context.Context, sem chan struct{}) error {
	ms, err := s.Store.ListMonitors(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, m := range ms {
		if !m.Enabled || m.LastCheckedAt != nil && now.Before(m.LastCheckedAt.Add(m.Interval)) {
			continue
		}
		s.mu.Lock()
		busy := s.active[m.ID]
		if !busy {
			s.active[m.ID] = true
		}
		s.mu.Unlock()
		if busy {
			continue
		}
		s.wg.Add(1)
		go func(m storage.Monitor) {
			defer s.wg.Done()
			defer func() { s.mu.Lock(); delete(s.active, m.ID); s.mu.Unlock() }()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			r, err := s.Check(ctx, m)
			if s.Events != nil {
				select {
				case s.Events <- Event{Monitor: m, Result: r, Err: err}:
				case <-ctx.Done():
				}
			}
		}(m)
	}
	return nil
}
