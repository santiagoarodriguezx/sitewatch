package tests

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sitewatch/sitewatch/internal/app"
	"github.com/sitewatch/sitewatch/internal/config"
	"github.com/sitewatch/sitewatch/internal/diff"
	"github.com/sitewatch/sitewatch/internal/extractor"
)

func page(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMeaningfulPriceChangeIgnoresTechnicalNoise(t *testing.T) {
	a, _, err := extractor.Page("https://example.com/pricing", page(t, "pricing_v1.html"), 200, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := extractor.Page("https://example.com/pricing", page(t, "pricing_v2.html"), 200, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	changes := diff.Filter(diff.Compare(a, b), .4)
	if len(changes) != 1 {
		t.Fatalf("wanted only price change, got %#v", changes)
	}
	c := changes[0]
	if c.Entity != "price" || c.OldValue != "$20/month" || c.NewValue != "$25/month" || c.Score < .9 {
		t.Fatalf("bad price change: %#v", c)
	}
}

func TestDynamicNoiseIsNotMeaningful(t *testing.T) {
	a, _, _ := extractor.Page("https://example.com", page(t, "dynamic_noise_v1.html"), 200, time.Now())
	b, _, _ := extractor.Page("https://example.com", page(t, "dynamic_noise_v2.html"), 200, time.Now())
	if a.Fingerprints.HTML == b.Fingerprints.HTML {
		t.Fatal("raw HTML should differ")
	}
	if !diff.MeaningfulEqual(a, b) || len(diff.Compare(a, b)) != 0 {
		t.Fatal("technical noise became meaningful")
	}
}
func TestEnterpriseHeadingIsHigh(t *testing.T) {
	a, _, _ := extractor.Page("https://example.com", page(t, "products_v1.html"), 200, time.Now())
	b, _, _ := extractor.Page("https://example.com", page(t, "products_v2.html"), 200, time.Now())
	changes := diff.Filter(diff.Compare(a, b), .8)
	if len(changes) != 1 || changes[0].Entity != "heading" || changes[0].NewValue != "Enterprise" {
		t.Fatalf("got %#v", changes)
	}
}
func TestTitleOnlyChange(t *testing.T) {
	a, _, _ := extractor.Page("https://example.com", []byte(`<html><head><title>Old</title></head><body><p>This visible body is deliberately long enough to avoid the JavaScript shell warning during this focused title test.</p></body></html>`), 200, time.Now())
	b, _, _ := extractor.Page("https://example.com", []byte(`<html><head><title>New</title></head><body><p>This visible body is deliberately long enough to avoid the JavaScript shell warning during this focused title test.</p></body></html>`), 200, time.Now())
	c := diff.Compare(a, b)
	if len(c) != 1 || c[0].Entity != "title" {
		t.Fatalf("got %#v", c)
	}
}
func TestJSONLDAndJavaScriptSignal(t *testing.T) {
	p, small, err := extractor.Page("https://example.com", page(t, "jsonld_product.html"), 200, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if small || len(p.StructuredData) != 2 || p.StructuredData[0].Type != "Product" || len(p.Prices) != 1 {
		t.Fatalf("bad extraction: %#v small=%v", p, small)
	}
	_, small, err = extractor.Page("https://example.com", page(t, "react_shell.html"), 200, time.Now())
	if err != nil || !small {
		t.Fatalf("react shell should warn: %v %v", small, err)
	}
}

func TestJSONLDPriceChangesWithoutVisibleText(t *testing.T) {
	a, _, _ := extractor.Page("https://example.com", page(t, "jsonld_price_v1.html"), 200, time.Now())
	b, _, _ := extractor.Page("https://example.com", page(t, "jsonld_price_v2.html"), 200, time.Now())
	changes := diff.Filter(diff.Compare(a, b), .4)
	if len(changes) != 1 || changes[0].Entity != "price" || changes[0].Context != "Pro Plan" {
		t.Fatalf("got %#v", changes)
	}
}

func TestAppFlowAndWebhook(t *testing.T) {
	body := page(t, "pricing_v1.html")
	hooks := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hook" {
			hooks++
			w.WriteHeader(204)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(body)
	}))
	defer s.Close()
	c := config.Config{DB: filepath.Join(t.TempDir(), "sitewatch.db"), Timeout: time.Second, MaxBody: 1 << 20, UserAgent: "test", MinScore: .4, AllowPrivate: true, Concurrency: 2, Rate: 100}
	a, err := app.New(c)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	created, err := a.Add(context.Background(), s.URL, app.AddOptions{Interval: time.Minute, AllowPrivate: true, Webhook: s.URL + "/hook", Retention: 3})
	if err != nil {
		t.Fatal(err)
	}
	body = []byte(strings.ReplaceAll(string(body), "bundle.921821.js", "bundle.000001.js"))
	technical, err := a.Check(context.Background(), fmt.Sprint(created.Monitor.ID))
	if err != nil || technical.Status != "technical changes only" {
		t.Fatalf("technical: %#v %v", technical, err)
	}
	body = page(t, "pricing_v2.html")
	changed, err := a.Check(context.Background(), fmt.Sprint(created.Monitor.ID))
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Filter(changed.Changes, .4)) != 1 || hooks != 1 {
		t.Fatalf("changes=%#v hooks=%d", changed.Changes, hooks)
	}
	history, err := a.Store.History(context.Background(), created.Monitor.ID, 10)
	if err != nil || len(history) != 2 {
		t.Fatalf("history=%d %v", len(history), err)
	}
}

func BenchmarkExtractPage(b *testing.B) {
	body, err := os.ReadFile(filepath.Join("testdata", "pricing_v1.html"))
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < b.N; i++ {
		if _, _, err := extractor.Page("https://example.com", body, 200, time.Now()); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkDiffSnapshots(b *testing.B) {
	a, _, _ := extractor.Page("https://example.com", pageForBench(b, "pricing_v1.html"), 200, time.Now())
	z, _, _ := extractor.Page("https://example.com", pageForBench(b, "pricing_v2.html"), 200, time.Now())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		diff.Compare(a, z)
	}
}
func pageForBench(b *testing.B, name string) []byte {
	b.Helper()
	v, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		b.Fatal(err)
	}
	return v
}
