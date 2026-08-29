package fetcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPrivateBlocked(t *testing.T) {
	c := New(Options{Timeout: time.Second})
	if _, err := c.Get(context.Background(), "http://127.0.0.1", Conditional{}); err == nil {
		t.Fatal("expected private address rejection")
	}
}
func TestCredentialsRejected(t *testing.T) {
	if err := ValidateURL("https://user:secret@example.com"); err == nil {
		t.Fatal("expected rejection")
	}
}
func TestConditional(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"one"` {
			w.WriteHeader(304)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("ETag", `"one"`)
		w.Write([]byte("<html><body>enough content for this tiny conditional request test page to pass extraction</body></html>"))
	}))
	defer s.Close()
	c := New(Options{AllowPrivate: true})
	first, err := c.Get(context.Background(), s.URL, Conditional{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Get(context.Background(), s.URL, Conditional{ETag: first.ETag})
	if err != nil || !second.NotModified {
		t.Fatalf("conditional failed: %+v %v", second, err)
	}
}
