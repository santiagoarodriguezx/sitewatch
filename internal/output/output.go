package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/sitewatch/sitewatch/internal/snapshot"
	"github.com/sitewatch/sitewatch/internal/storage"
)

func JSON(w io.Writer, v any) error {
	e := json.NewEncoder(w)
	e.SetIndent("", "  ")
	return e.Encode(v)
}
func Ago(t *time.Time) string {
	if t == nil {
		return "never"
	}
	d := time.Since(*t).Round(time.Minute)
	if d < time.Minute {
		return "now"
	}
	return d.String() + " ago"
}
func MonitorTable(w io.Writer, ms []storage.Monitor) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	defer tw.Flush()
	fmt.Fprintln(tw, "ID\tNAME\tURL\tINTERVAL\tLAST CHECK\tSTATUS")
	for _, m := range ms {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\n", m.ID, m.Name, shortURL(m.NormalizedURL), m.Interval, Ago(m.LastCheckedAt), m.LastStatus)
	}
}
func Changes(w io.Writer, changes []snapshot.Change) {
	if len(changes) == 0 {
		fmt.Fprintln(w, "No meaningful changes")
		return
	}
	high := 0
	for _, c := range changes {
		if c.Score >= .8 {
			high++
		}
	}
	noun := "changes"
	if len(changes) == 1 {
		noun = "change"
	}
	fmt.Fprintf(w, "%d meaningful %s · Important: %d · Medium: %d\n\n", len(changes), noun, high, len(changes)-high)
	for _, c := range changes {
		level := "MEDIUM"
		if c.Score >= .8 {
			level = "HIGH"
		}
		symbol := map[string]string{"added": "+", "removed": "-", "modified": "~", "moved": "↕"}[c.Type]
		fmt.Fprintf(w, "%s  %.2f  %s %s", level, c.Score, symbol, c.Entity)
		if c.Context != "" {
			fmt.Fprintf(w, " — %s", c.Context)
		}
		fmt.Fprintln(w)
		if c.OldValue != "" {
			fmt.Fprintf(w, "  - %s\n", limit(c.OldValue, 180))
		}
		if c.NewValue != "" {
			fmt.Fprintf(w, "  + %s\n", limit(c.NewValue, 180))
		}
		fmt.Fprintln(w)
	}
}
func shortURL(s string) string {
	return strings.TrimPrefix(strings.TrimPrefix(strings.TrimSuffix(s, "/"), "https://"), "http://")
}
func limit(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
