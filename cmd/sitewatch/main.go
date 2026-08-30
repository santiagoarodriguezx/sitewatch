package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/sitewatch/sitewatch/internal/app"
	"github.com/sitewatch/sitewatch/internal/config"
	"github.com/sitewatch/sitewatch/internal/output"
	"github.com/sitewatch/sitewatch/internal/scheduler"
	"github.com/sitewatch/sitewatch/internal/snapshot"
)

var version = "dev"

func main() {
	if err := newRoot().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func newRoot() *cobra.Command {
	c, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	var a *app.App
	getApp := func() (*app.App, error) {
		if a != nil {
			return a, nil
		}
		x, err := app.New(c)
		if err == nil {
			a = x
		}
		return x, err
	}
	root := &cobra.Command{Use: "sitewatch", Short: "Detect meaningful website changes, not HTML noise", SilenceUsage: true, SilenceErrors: true, PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if c.Verbose {
			slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
		}
	}}
	root.PersistentFlags().StringVar(&c.DB, "db", c.DB, "SQLite database path")
	root.PersistentFlags().StringVar(&c.UserAgent, "user-agent", c.UserAgent, "HTTP User-Agent")
	root.PersistentFlags().DurationVar(&c.Timeout, "timeout", c.Timeout, "request timeout")
	root.PersistentFlags().IntVar(&c.Concurrency, "concurrency", c.Concurrency, "maximum concurrent checks")
	root.PersistentFlags().Float64Var(&c.Rate, "rate", c.Rate, "maximum crawl requests per second per host")
	root.PersistentFlags().BoolVarP(&c.Verbose, "verbose", "v", c.Verbose, "enable debug logs")
	root.AddCommand(addCmd(getApp), listCmd(getApp), removeCmd(getApp), checkCmd(getApp), historyCmd(getApp), diffCmd(getApp), showCmd(getApp))
	root.AddCommand(&cobra.Command{Use: "version", Args: cobra.NoArgs, Run: func(*cobra.Command, []string) { fmt.Println("sitewatch", version) }})
	root.AddCommand(&cobra.Command{Use: "config", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		c.TimeoutText = c.Timeout.String()
		b, err := yaml.Marshal(c)
		if err == nil {
			fmt.Fprint(cmd.OutOrStdout(), string(b))
		}
		return err
	}})
	root.AddCommand(watchCmd(getApp), serveCmd(getApp))
	root.SetOut(os.Stdout)
	root.SetErr(os.Stderr)
	return root
}

type appGetter func() (*app.App, error)

func addCmd(get appGetter) *cobra.Command {
	var o app.AddOptions
	cmd := &cobra.Command{Use: "add <url>", Short: "Add a website and create its first snapshot", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		a, err := get()
		if err != nil {
			return err
		}
		defer a.Close()
		r, err := a.Add(cmd.Context(), args[0], o)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "✓ Monitor created\n  ID: %d\n  URL: %s\n  Check interval: %s\n  Initial snapshot created\n  Content hash: %.12s...\n", r.Monitor.ID, r.Monitor.NormalizedURL, r.Monitor.Interval, r.Snapshot.Fingerprints.Visible)
		if r.Discovered > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "  Discovered monitors: %d\n", r.Discovered)
		}
		if r.Warning != "" {
			fmt.Fprintln(cmd.OutOrStdout(), "warning:", r.Warning)
		}
		return nil
	}}
	f := cmd.Flags()
	f.StringVar(&o.Name, "name", "", "display name")
	f.DurationVar(&o.Interval, "interval", time.Hour, "check interval (minimum 1m)")
	f.BoolVar(&o.Crawl, "crawl", false, "discover same-site pages")
	f.BoolVar(&o.IgnoreRobots, "ignore-robots", false, "ignore robots.txt while crawling")
	f.IntVar(&o.Depth, "depth", 1, "crawl depth (1-3)")
	f.IntVar(&o.MaxPages, "max-pages", 50, "maximum pages to discover")
	f.BoolVar(&o.AllowPrivate, "allow-private", false, "allow private and local targets")
	f.StringVar(&o.Webhook, "webhook", "", "webhook URL for meaningful changes")
	f.IntVar(&o.Retention, "retention", 30, "snapshots to retain")
	return cmd
}
func listCmd(get appGetter) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "list", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		a, e := get()
		if e != nil {
			return e
		}
		defer a.Close()
		m, e := a.Store.ListMonitors(cmd.Context())
		if e != nil {
			return e
		}
		if asJSON {
			return output.JSON(cmd.OutOrStdout(), m)
		}
		output.MonitorTable(cmd.OutOrStdout(), m)
		return nil
	}}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}
func removeCmd(get appGetter) *cobra.Command {
	return &cobra.Command{Use: "remove <id>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		id, e := strconv.ParseInt(args[0], 10, 64)
		if e != nil {
			return fmt.Errorf("invalid monitor ID: %w", e)
		}
		a, e := get()
		if e != nil {
			return e
		}
		defer a.Close()
		if e = a.Store.RemoveMonitor(cmd.Context(), id); e == nil {
			fmt.Fprintln(cmd.OutOrStdout(), "✓ Monitor removed")
		}
		return e
	}}
}
func checkCmd(get appGetter) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "check <id-or-url>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		a, e := get()
		if e != nil {
			return e
		}
		defer a.Close()
		r, e := a.Check(cmd.Context(), args[0])
		if e != nil {
			return e
		}
		if asJSON {
			return output.JSON(cmd.OutOrStdout(), r)
		}
		fmt.Fprintln(cmd.OutOrStdout(), r.Status)
		if r.Warning != "" {
			fmt.Fprintln(cmd.OutOrStdout(), "warning:", r.Warning)
		}
		output.Changes(cmd.OutOrStdout(), filterDefault(r.Changes, a.Config.MinScore))
		if r.Ignored > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "Ignored noise: %d lower-score changes\n", r.Ignored)
		}
		return nil
	}}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}
func historyCmd(get appGetter) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "history <id-or-url>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		a, e := get()
		if e != nil {
			return e
		}
		defer a.Close()
		m, e := a.Resolve(cmd.Context(), args[0])
		if e != nil {
			return e
		}
		h, e := a.Store.History(cmd.Context(), m.ID, m.Retention)
		if e != nil {
			return e
		}
		if asJSON {
			return output.JSON(cmd.OutOrStdout(), h)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Snapshots for %s\n", m.Name)
		for _, s := range h {
			fmt.Fprintf(cmd.OutOrStdout(), "%d  %s  HTTP %d  %.12s...\n", s.ID, s.FetchedAt.Local().Format(time.RFC3339), s.HTTPStatus, s.Fingerprints.Visible)
		}
		return nil
	}}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}
func diffCmd(get appGetter) *cobra.Command {
	var asJSON, all bool
	var min float64
	cmd := &cobra.Command{Use: "diff <id-or-url>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		a, e := get()
		if e != nil {
			return e
		}
		defer a.Close()
		threshold := min
		if all {
			threshold = 0
		}
		if !cmd.Flags().Changed("min-score") && !all {
			threshold = a.Config.MinScore
		}
		m, c, e := a.Diff(cmd.Context(), args[0], threshold)
		if e != nil {
			return e
		}
		if asJSON {
			return output.JSON(cmd.OutOrStdout(), map[string]any{"monitor": m, "changes": c})
		}
		fmt.Fprintln(cmd.OutOrStdout(), m.Name)
		output.Changes(cmd.OutOrStdout(), c)
		return nil
	}}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	cmd.Flags().BoolVar(&all, "all", false, "include noise")
	cmd.Flags().Float64Var(&min, "min-score", .4, "minimum significance score")
	return cmd
}
func showCmd(get appGetter) *cobra.Command {
	return &cobra.Command{Use: "show <id>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		a, e := get()
		if e != nil {
			return e
		}
		defer a.Close()
		m, e := a.Resolve(cmd.Context(), args[0])
		if e != nil {
			return e
		}
		return output.JSON(cmd.OutOrStdout(), m)
	}}
}
func filterDefault(in []snapshot.Change, min float64) []snapshot.Change {
	out := in[:0]
	for _, c := range in {
		if c.Score >= min {
			out = append(out, c)
		}
	}
	return out
}

func watchCmd(get appGetter) *cobra.Command {
	return &cobra.Command{Use: "watch", Short: "Watch all monitors until interrupted", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		a, err := get()
		if err != nil {
			return err
		}
		defer a.Close()
		ms, err := a.Store.ListMonitors(cmd.Context())
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "SiteWatch started\n\nWatching %d websites\n\n", len(ms))
		ctx := getContext(cmd)
		events := make(chan scheduler.Event)
		s := scheduler.Scheduler{Store: a.Store, Concurrency: a.Config.Concurrency, Check: a.CheckMonitor, Events: events}
		done := make(chan error, 1)
		go func() { done <- s.Run(ctx) }()
		for {
			select {
			case e := <-events:
				stamp := time.Now().Format("15:04:05")
				if e.Err != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "[%s] %-30s error: %v\n", stamp, e.Monitor.NormalizedURL, e.Err)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "[%s] %-30s %s\n", stamp, e.Monitor.NormalizedURL, e.Result.Status)
				}
			case err := <-done:
				return err
			}
		}
	}}
}
func getContext(cmd *cobra.Command) context.Context {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	go func() { <-ctx.Done(); stop() }()
	return ctx
}
