package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/sitewatch/sitewatch/internal/app"
	"github.com/sitewatch/sitewatch/internal/output"
	"github.com/spf13/cobra"
)

func serveCmd(get appGetter) *cobra.Command {
	addr := "127.0.0.1:8080"
	cmd := &cobra.Command{Use: "serve", Short: "Serve the local API and dashboard", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		a, err := get()
		if err != nil {
			return err
		}
		defer a.Close()
		h := &web{app: a}
		server := &http.Server{Addr: addr, Handler: h.routes(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
		ctx := getContext(cmd)
		go func() {
			<-ctx.Done()
			stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = server.Shutdown(stop)
		}()
		fmt.Fprintln(cmd.OutOrStdout(), "SiteWatch listening on http://"+addr)
		err = server.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}}
	cmd.Flags().StringVar(&addr, "addr", addr, "listen address")
	return cmd
}

type web struct{ app *app.App }

func (w *web) routes() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /health", func(rw http.ResponseWriter, _ *http.Request) {
		writeJSON(rw, http.StatusOK, map[string]string{"status": "ok"})
	})
	m.HandleFunc("GET /api/monitors", w.list)
	m.HandleFunc("POST /api/monitors", w.add)
	m.HandleFunc("/api/monitors/", w.monitor)
	m.HandleFunc("GET /", w.dashboard)
	m.HandleFunc("GET /monitors/", w.detail)
	return m
}
func (w *web) list(rw http.ResponseWriter, r *http.Request) {
	ms, err := w.app.Store.ListMonitors(r.Context())
	respond(rw, ms, err)
}
func (w *web) add(rw http.ResponseWriter, r *http.Request) {
	var x struct {
		URL       string `json:"url"`
		Name      string `json:"name"`
		Interval  string `json:"interval"`
		Webhook   string `json:"webhook"`
		Depth     int    `json:"depth"`
		MaxPages  int    `json:"max_pages"`
		Retention int    `json:"retention"`
		Crawl     bool   `json:"crawl"`
	}
	d := json.NewDecoder(http.MaxBytesReader(rw, r.Body, 1<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(&x); err != nil {
		writeError(rw, http.StatusBadRequest, err)
		return
	}
	interval := time.Hour
	var err error
	if x.Interval != "" {
		interval, err = time.ParseDuration(x.Interval)
		if err != nil {
			writeError(rw, http.StatusBadRequest, err)
			return
		}
	}
	result, err := w.app.Add(r.Context(), x.URL, app.AddOptions{Name: x.Name, Interval: interval, Crawl: x.Crawl, Depth: x.Depth, MaxPages: x.MaxPages, Webhook: x.Webhook, Retention: x.Retention})
	if err != nil {
		writeError(rw, http.StatusBadRequest, err)
		return
	}
	writeJSON(rw, http.StatusCreated, result)
}
func (w *web) monitor(rw http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/monitors/"), "/"), "/")
	if len(parts) < 1 || parts[0] == "" {
		http.NotFound(rw, r)
		return
	}
	m, err := w.app.Resolve(r.Context(), parts[0])
	if err != nil {
		writeError(rw, http.StatusNotFound, err)
		return
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			writeJSON(rw, http.StatusOK, m)
		case http.MethodDelete:
			respond(rw, map[string]bool{"removed": true}, w.app.Store.RemoveMonitor(r.Context(), m.ID))
		default:
			rw.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	switch parts[1] {
	case "check":
		if r.Method != http.MethodPost {
			rw.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		result, err := w.app.CheckMonitor(r.Context(), m)
		respond(rw, result, err)
	case "history":
		if r.Method != http.MethodGet {
			rw.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		h, err := w.app.Store.History(r.Context(), m.ID, m.Retention)
		respond(rw, h, err)
	case "changes":
		if r.Method != http.MethodGet {
			rw.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		_, c, err := w.app.Diff(r.Context(), parts[0], 0)
		respond(rw, c, err)
	default:
		http.NotFound(rw, r)
	}
}
func (w *web) dashboard(rw http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(rw, r)
		return
	}
	ms, err := w.app.Store.ListMonitors(r.Context())
	if err != nil {
		writeError(rw, 500, err)
		return
	}
	_ = dashboardTemplate.Execute(rw, ms)
}
func (w *web) detail(rw http.ResponseWriter, r *http.Request) {
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/monitors/"), "/")
	m, err := w.app.Resolve(r.Context(), id)
	if err != nil {
		http.NotFound(rw, r)
		return
	}
	changes, err := w.app.Store.LatestChanges(r.Context(), m.ID)
	if err != nil {
		writeError(rw, 500, err)
		return
	}
	_ = detailTemplate.Execute(rw, struct {
		Monitor any
		Changes any
	}{m, changes})
}
func respond(rw http.ResponseWriter, v any, err error) {
	if err != nil {
		writeError(rw, http.StatusInternalServerError, err)
		return
	}
	writeJSON(rw, http.StatusOK, v)
}
func writeJSON(rw http.ResponseWriter, status int, v any) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(status)
	_ = json.NewEncoder(rw).Encode(v)
}
func writeError(rw http.ResponseWriter, status int, err error) {
	writeJSON(rw, status, map[string]string{"error": err.Error()})
}

var dashboardTemplate = template.Must(template.New("dashboard").Funcs(template.FuncMap{"ago": output.Ago}).Parse(pageHead + `<main><header><h1>SiteWatch</h1><p>{{len .}} monitored websites</p></header>{{range .}}<a class="card" href="/monitors/{{.ID}}"><strong>{{.Name}}</strong><span>{{.NormalizedURL}}</span><small>Last check: {{ago .LastCheckedAt}} · {{.LastStatus}}</small></a>{{else}}<div class="card">No monitors yet. Run <code>sitewatch add URL</code>.</div>{{end}}</main>` + pageFoot))
var detailTemplate = template.Must(template.New("detail").Parse(pageHead + `<main><a href="/">← SiteWatch</a><header><h1>{{.Monitor.Name}}</h1><p>{{.Monitor.NormalizedURL}}</p></header><div class="card"><strong>Status: {{.Monitor.LastStatus}}</strong><span>Interval: {{.Monitor.Interval}}</span></div><h2>Latest changes</h2>{{range .Changes}}<div class="card"><strong>{{.Type}} {{.Entity}} · {{printf "%.2f" .Score}}</strong><span>{{.Context}}</span><small>{{.OldValue}} → {{.NewValue}}</small></div>{{else}}<div class="card">No meaningful changes.</div>{{end}}</main>` + pageFoot))

const pageHead = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>SiteWatch</title><style>:root{color-scheme:dark;font:16px system-ui;background:#0b1020;color:#e8edf8}body{margin:0}main{max-width:850px;margin:4rem auto;padding:0 1rem}header{margin:2rem 0}h1{font-size:2.5rem;margin-bottom:.25rem}p,span,small{color:#9caac4}.card{display:flex;flex-direction:column;gap:.4rem;padding:1.2rem;margin:.8rem 0;border:1px solid #26324a;border-radius:10px;background:#121a2c;color:inherit;text-decoration:none}.card:hover{border-color:#5bd1b7}a{color:#5bd1b7}code{color:#d6b4fc}</style></head><body>`
const pageFoot = `</body></html>`
