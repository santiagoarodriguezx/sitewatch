package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/sitewatch/sitewatch/internal/config"
	"github.com/sitewatch/sitewatch/internal/crawler"
	"github.com/sitewatch/sitewatch/internal/diff"
	"github.com/sitewatch/sitewatch/internal/extractor"
	"github.com/sitewatch/sitewatch/internal/fetcher"
	"github.com/sitewatch/sitewatch/internal/normalize"
	"github.com/sitewatch/sitewatch/internal/notifier"
	"github.com/sitewatch/sitewatch/internal/snapshot"
	"github.com/sitewatch/sitewatch/internal/storage"
	"github.com/sitewatch/sitewatch/internal/summary"
)

type App struct {
	Config config.Config
	Store  *storage.DB
}
type AddOptions struct {
	Name            string
	Interval        time.Duration
	Crawl           bool
	Depth, MaxPages int
	AllowPrivate    bool
	Webhook         string
	Retention       int
	IgnoreRobots    bool
}
type CheckResult struct {
	Monitor    storage.Monitor   `json:"monitor"`
	Snapshot   *snapshot.Page    `json:"snapshot,omitempty"`
	Changes    []snapshot.Change `json:"changes,omitempty"`
	Status     string            `json:"status"`
	Warning    string            `json:"warning,omitempty"`
	Ignored    int               `json:"ignored_noise"`
	Discovered int               `json:"discovered,omitempty"`
}

func New(c config.Config) (*App, error) {
	db, err := storage.Open(c.DB)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	return &App{Config: c, Store: db}, nil
}
func (a *App) Close() error { return a.Store.Close() }

func (a *App) Add(ctx context.Context, raw string, opt AddOptions) (CheckResult, error) {
	if err := fetcher.ValidateURL(raw); err != nil {
		return CheckResult{}, err
	}
	normalized, err := normalize.URL(raw, nil)
	if err != nil {
		return CheckResult{}, fmt.Errorf("invalid URL: %w", err)
	}
	if opt.Interval == 0 {
		opt.Interval = time.Hour
	}
	if opt.Interval < time.Minute {
		return CheckResult{}, errors.New("interval must be at least 1m")
	}
	if opt.Depth == 0 {
		opt.Depth = 1
	}
	if opt.Depth < 1 || opt.Depth > 3 {
		return CheckResult{}, errors.New("depth must be between 1 and 3")
	}
	if opt.MaxPages == 0 {
		opt.MaxPages = 50
	}
	if opt.MaxPages < 1 || opt.MaxPages > 1000 {
		return CheckResult{}, errors.New("max-pages must be between 1 and 1000")
	}
	if opt.Retention == 0 {
		opt.Retention = 30
	}
	if opt.Retention < 2 {
		return CheckResult{}, errors.New("retention must be at least 2")
	}
	u, _ := url.Parse(normalized)
	name := opt.Name
	if name == "" {
		name = u.Hostname()
	}
	m, err := a.Store.AddMonitor(ctx, storage.Monitor{Name: name, URL: raw, NormalizedURL: normalized, Interval: opt.Interval, Crawl: opt.Crawl, Depth: opt.Depth, MaxPages: opt.MaxPages, AllowPrivate: opt.AllowPrivate, Webhook: opt.Webhook, Retention: opt.Retention})
	if err != nil {
		if storage.IsUnique(err) {
			return CheckResult{}, errors.New("URL is already monitored")
		}
		return CheckResult{}, err
	}
	result, err := a.CheckMonitor(ctx, m)
	if err != nil {
		_ = a.Store.RemoveMonitor(ctx, m.ID)
		return CheckResult{}, fmt.Errorf("create initial snapshot: %w", err)
	}
	if opt.Crawl {
		f := fetcher.New(fetcher.Options{Timeout: a.Config.Timeout, MaxBody: a.Config.MaxBody, Retries: a.Config.Retries, UserAgent: a.Config.UserAgent, AllowPrivate: a.Config.AllowPrivate || opt.AllowPrivate})
		urls, e := crawler.New(f, crawler.Options{Depth: opt.Depth, MaxPages: opt.MaxPages, Concurrency: a.Config.Concurrency, Rate: a.Config.Rate, IgnoreRobots: opt.IgnoreRobots}).Discover(ctx, normalized)
		if e != nil {
			result.Warning = e.Error()
			return result, nil
		}
		for _, u := range urls[1:] {
			if _, e = a.Add(ctx, u, AddOptions{Interval: opt.Interval, Depth: 1, MaxPages: opt.MaxPages, AllowPrivate: opt.AllowPrivate, Webhook: opt.Webhook, Retention: opt.Retention}); e == nil {
				result.Discovered++
			}
		}
		if opt.IgnoreRobots {
			if result.Warning != "" {
				result.Warning += "; "
			}
			result.Warning += "robots.txt ignored by explicit request"
		}
	}
	return result, nil
}

func (a *App) Resolve(ctx context.Context, ref string) (storage.Monitor, error) {
	if strings.Contains(ref, "://") {
		if u, err := normalize.URL(ref, nil); err == nil {
			ref = u
		}
	}
	return a.Store.GetMonitor(ctx, ref)
}
func (a *App) Check(ctx context.Context, ref string) (CheckResult, error) {
	m, err := a.Resolve(ctx, ref)
	if err != nil {
		return CheckResult{}, err
	}
	return a.CheckMonitor(ctx, m)
}
func (a *App) CheckMonitor(ctx context.Context, m storage.Monitor) (CheckResult, error) {
	f := fetcher.New(fetcher.Options{Timeout: a.Config.Timeout, MaxBody: a.Config.MaxBody, Retries: a.Config.Retries, UserAgent: a.Config.UserAgent, AllowPrivate: a.Config.AllowPrivate || m.AllowPrivate})
	resp, err := f.Get(ctx, m.NormalizedURL, fetcher.Conditional{ETag: m.ETag, LastModified: m.LastModified})
	if err != nil {
		_ = a.Store.UpdateCheck(ctx, m.ID, m.ETag, m.LastModified, "error", err.Error())
		return CheckResult{}, err
	}
	if resp.NotModified {
		_ = a.Store.UpdateCheck(ctx, m.ID, m.ETag, m.LastModified, "unchanged", "")
		return CheckResult{Monitor: m, Status: "not modified"}, nil
	}
	p, insufficient, err := extractor.Page(resp.URL, resp.Body, resp.Status, time.Now().UTC())
	if err != nil {
		return CheckResult{}, err
	}
	warning := ""
	if insufficient {
		warning = "page appears to require JavaScript rendering"
	}
	previous, err := a.Store.Latest(ctx, m.ID)
	first := errors.Is(err, sql.ErrNoRows)
	if err != nil && !first {
		return CheckResult{}, err
	}
	if !first && previous.Fingerprints.HTML == p.Fingerprints.HTML {
		_ = a.Store.UpdateCheck(ctx, m.ID, resp.ETag, resp.LastModified, "unchanged", "")
		return CheckResult{Monitor: m, Status: "no changes", Warning: warning}, nil
	}
	if !first && diff.MeaningfulEqual(previous, p) {
		_ = a.Store.UpdateCheck(ctx, m.ID, resp.ETag, resp.LastModified, "unchanged", "")
		return CheckResult{Monitor: m, Status: "technical changes only", Warning: warning, Ignored: 1}, nil
	}
	var changes []snapshot.Change
	if !first {
		changes = diff.Compare(previous, p)
	}
	p, err = a.Store.StoreSnapshot(ctx, m.ID, p, changes, m.Retention)
	if err != nil {
		return CheckResult{}, err
	}
	status := "snapshot created"
	meaningful := diff.Filter(changes, a.Config.MinScore)
	if !first {
		status, _ = (summary.Deterministic{}).Summarize(ctx, meaningful)
	}
	ignored := len(changes) - len(meaningful)
	_ = a.Store.UpdateCheck(ctx, m.ID, resp.ETag, resp.LastModified, "ok", "")
	if m.Webhook != "" && len(meaningful) > 0 {
		if e := (notifier.Webhook{URL: m.Webhook, Client: f}).Notify(ctx, notifier.Notification{Monitor: m.Name, URL: m.NormalizedURL, Changes: meaningful, Timestamp: p.FetchedAt}); e != nil {
			if warning != "" {
				warning += "; "
			}
			warning += "webhook: " + e.Error()
		}
	}
	return CheckResult{Monitor: m, Snapshot: &p, Changes: changes, Status: status, Warning: warning, Ignored: ignored}, nil
}

func (a *App) Diff(ctx context.Context, ref string, min float64) (storage.Monitor, []snapshot.Change, error) {
	m, err := a.Resolve(ctx, ref)
	if err != nil {
		return m, nil, err
	}
	c, err := a.Store.LatestChanges(ctx, m.ID)
	if err != nil {
		return m, nil, err
	}
	return m, diff.Filter(c, min), nil
}
