package crawler

import (
	"context"
	"encoding/xml"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/sitewatch/sitewatch/internal/fetcher"
	"github.com/sitewatch/sitewatch/internal/normalize"
)

type Options struct {
	Depth, MaxPages, Concurrency int
	Rate                         float64
	IgnoreRobots                 bool
}
type Crawler struct {
	fetch *fetcher.Client
	opt   Options
	mu    sync.Mutex
	next  time.Time
}
type rule struct {
	allow bool
	path  string
}
type robots struct {
	rules    []rule
	sitemaps []string
}

func New(f *fetcher.Client, opt Options) *Crawler {
	if opt.Depth < 1 {
		opt.Depth = 1
	}
	if opt.MaxPages < 1 {
		opt.MaxPages = 50
	}
	if opt.Concurrency < 1 {
		opt.Concurrency = 10
	}
	if opt.Rate <= 0 {
		opt.Rate = 5
	}
	return &Crawler{fetch: f, opt: opt}
}

func (c *Crawler) Discover(ctx context.Context, raw string) ([]string, error) {
	rootText, err := normalize.URL(raw, nil)
	if err != nil {
		return nil, err
	}
	root, _ := url.Parse(rootText)
	rb := robots{}
	if !c.opt.IgnoreRobots {
		rb = c.loadRobots(ctx, root)
	}
	seen := map[string]bool{rootText: true}
	found := []string{rootText}
	frontier := []string{rootText}
	for _, sm := range append([]string{root.Scheme + "://" + root.Host + "/sitemap.xml"}, rb.sitemaps...) {
		for _, u := range c.sitemap(ctx, sm, root, 1) {
			if len(found) >= c.opt.MaxPages {
				break
			}
			if !seen[u] && (c.opt.IgnoreRobots || rb.allowed(mustURL(u).EscapedPath())) {
				seen[u] = true
				found = append(found, u)
			}
		}
	}
	for depth := 0; depth < c.opt.Depth && len(frontier) > 0 && len(found) < c.opt.MaxPages; depth++ {
		next := []string{}
		jobs := make(chan string)
		results := make(chan []string)
		var wg sync.WaitGroup
		workers := c.opt.Concurrency
		if workers > len(frontier) {
			workers = len(frontier)
		}
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for page := range jobs {
					select {
					case results <- c.links(ctx, page, root, rb):
					case <-ctx.Done():
						return
					}
				}
			}()
		}
		go func() {
			defer close(results)
			for _, u := range frontier {
				select {
				case <-ctx.Done():
					close(jobs)
					wg.Wait()
					return
				case jobs <- u:
				}
			}
			close(jobs)
			wg.Wait()
		}()
		for links := range results {
			for _, u := range links {
				if len(found) >= c.opt.MaxPages {
					break
				}
				if !seen[u] {
					seen[u] = true
					found = append(found, u)
					next = append(next, u)
				}
			}
		}
		frontier = next
	}
	return found, nil
}

func (c *Crawler) links(ctx context.Context, raw string, root *url.URL, rb robots) []string {
	if !c.wait(ctx) {
		return nil
	}
	resp, err := c.fetch.Get(ctx, raw, fetcher.Conditional{})
	if err != nil {
		return nil
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(resp.Body)))
	if err != nil {
		return nil
	}
	var out []string
	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		u, err := normalize.URL(s.AttrOr("href", ""), mustURL(raw))
		if err != nil {
			return
		}
		parsed := mustURL(u)
		if !strings.EqualFold(parsed.Hostname(), root.Hostname()) {
			return
		}
		if !c.opt.IgnoreRobots && !rb.allowed(parsed.EscapedPath()) {
			return
		}
		out = append(out, u)
	})
	return out
}
func (c *Crawler) loadRobots(ctx context.Context, root *url.URL) robots {
	if !c.wait(ctx) {
		return robots{}
	}
	resp, err := c.fetch.GetAny(ctx, root.Scheme+"://"+root.Host+"/robots.txt")
	if err != nil {
		return robots{}
	}
	return parseRobots(string(resp.Body), root)
}
func parseRobots(body string, base *url.URL) robots {
	var r robots
	active := false
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(v)
		switch k {
		case "user-agent":
			active = v == "*"
		case "allow", "disallow":
			if active && v != "" {
				r.rules = append(r.rules, rule{allow: k == "allow", path: v})
			}
		case "sitemap":
			if u, err := normalize.URL(v, base); err == nil {
				r.sitemaps = append(r.sitemaps, u)
			}
		}
	}
	return r
}
func (r robots) allowed(path string) bool {
	best := -1
	allow := true
	for _, x := range r.rules {
		if strings.HasPrefix(path, x.path) && len(x.path) > best {
			best = len(x.path)
			allow = x.allow
		}
	}
	return allow
}

type urlset struct {
	URLs     []string `xml:"url>loc"`
	Sitemaps []string `xml:"sitemap>loc"`
}

func (c *Crawler) sitemap(ctx context.Context, raw string, root *url.URL, depth int) []string {
	if depth > 2 {
		return nil
	}
	if !c.wait(ctx) {
		return nil
	}
	resp, err := c.fetch.GetAny(ctx, raw)
	if err != nil {
		return nil
	}
	var x urlset
	if xml.Unmarshal(resp.Body, &x) != nil {
		return nil
	}
	var out []string
	for _, loc := range x.URLs {
		u, err := normalize.URL(loc, nil)
		if err == nil && strings.EqualFold(mustURL(u).Hostname(), root.Hostname()) {
			out = append(out, u)
		}
	}
	for _, sm := range x.Sitemaps {
		if u, err := normalize.URL(sm, nil); err == nil && strings.EqualFold(mustURL(u).Hostname(), root.Hostname()) {
			out = append(out, c.sitemap(ctx, u, root, depth+1)...)
		}
	}
	return out
}
func mustURL(raw string) *url.URL {
	u, _ := url.Parse(raw)
	return u
}

func (c *Crawler) wait(ctx context.Context) bool {
	c.mu.Lock()
	now := time.Now()
	at := c.next
	if at.Before(now) {
		at = now
	}
	c.next = at.Add(time.Duration(float64(time.Second) / c.opt.Rate))
	c.mu.Unlock()
	timer := time.NewTimer(time.Until(at))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
