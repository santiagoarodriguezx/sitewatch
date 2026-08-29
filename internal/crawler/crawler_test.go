package crawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/sitewatch/sitewatch/internal/fetcher"
)

func TestRobotsLongestRule(t *testing.T) {
	u, _ := url.Parse("https://example.com")
	r := parseRobots("User-agent: *\nDisallow: /private\nAllow: /private/public\nSitemap: /map.xml", u)
	if r.allowed("/private/x") {
		t.Fatal("private allowed")
	}
	if !r.allowed("/private/public/x") {
		t.Fatal("specific allow ignored")
	}
	if len(r.sitemaps) != 1 || r.sitemaps[0] != "https://example.com/map.xml" {
		t.Fatalf("bad sitemap: %#v", r.sitemaps)
	}
}
func TestDiscoveryStaysOnHost(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/robots.txt":
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "User-agent: *\nDisallow: /no")
		case "/sitemap.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(w, "<urlset></urlset>")
		default:
			fmt.Fprintf(w, `<html><body>enough visible words for crawler testing <a href="/ok">ok</a><a href="/no/x">no</a><a href="https://other.test/x">away</a></body></html>`)
		}
	}))
	defer server.Close()
	c := New(fetcher.New(fetcher.Options{AllowPrivate: true}), Options{Depth: 1, MaxPages: 10, Rate: 100})
	got, err := c.Discover(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %#v", got)
	}
}
