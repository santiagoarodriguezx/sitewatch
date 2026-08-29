package fetcher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Options struct {
	Timeout      time.Duration
	MaxBody      int64
	Retries      int
	UserAgent    string
	AllowPrivate bool
}
type Conditional struct{ ETag, LastModified string }
type Response struct {
	URL                             string
	Status                          int
	Body                            []byte
	ContentType, ETag, LastModified string
	NotModified                     bool
}
type Client struct {
	http *http.Client
	opt  Options
}

func New(opt Options) *Client {
	if opt.Timeout <= 0 {
		opt.Timeout = 15 * time.Second
	}
	if opt.MaxBody <= 0 {
		opt.MaxBody = 10 << 20
	}
	if opt.UserAgent == "" {
		opt.UserAgent = "SiteWatch/0.1"
	}
	dialer := &net.Dialer{Timeout: opt.Timeout / 2, KeepAlive: 30 * time.Second}
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, ForceAttemptHTTP2: true, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if !opt.AllowPrivate && blocked(ip) {
				return nil, fmt.Errorf("blocked private or reserved address: %s", ip)
			}
		}
		if len(ips) == 0 {
			return nil, errors.New("host resolved to no addresses")
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}}
	c := &Client{opt: opt}
	c.http = &http.Client{Timeout: opt.Timeout, Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		return ValidateURL(req.URL.String())
	}}
	return c
}

func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("invalid URL: scheme must be http or https")
	}
	if u.Hostname() == "" {
		return errors.New("invalid URL: host is required")
	}
	if u.User != nil {
		return errors.New("invalid URL: credentials are not allowed")
	}
	return nil
}

func blocked(ip netip.Addr) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	if ip.Is4() {
		a := ip.As4()
		return a[0] == 0 || a[0] >= 224 || (a[0] == 169 && a[1] == 254)
	}
	return false
}

func (c *Client) Get(ctx context.Context, raw string, cond Conditional) (Response, error) {
	return c.get(ctx, raw, cond, true)
}

func (c *Client) GetAny(ctx context.Context, raw string) (Response, error) {
	return c.get(ctx, raw, Conditional{}, false)
}

func (c *Client) PostJSON(ctx context.Context, raw string, body []byte) error {
	if err := ValidateURL(raw); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, raw, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.opt.UserAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook HTTP %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) get(ctx context.Context, raw string, cond Conditional, htmlOnly bool) (Response, error) {
	if err := ValidateURL(raw); err != nil {
		return Response{}, err
	}
	var last error
	for attempt := 0; attempt <= c.opt.Retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return Response{}, ctx.Err()
			case <-time.After(time.Duration(1<<uint(attempt-1)) * 250 * time.Millisecond):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
		if err != nil {
			return Response{}, err
		}
		req.Header.Set("User-Agent", c.opt.UserAgent)
		req.Header.Set("Accept", "text/html,application/xhtml+xml")
		if cond.ETag != "" {
			req.Header.Set("If-None-Match", cond.ETag)
		}
		if cond.LastModified != "" {
			req.Header.Set("If-Modified-Since", cond.LastModified)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			last = err
			continue
		}
		r := Response{URL: resp.Request.URL.String(), Status: resp.StatusCode, ContentType: resp.Header.Get("Content-Type"), ETag: resp.Header.Get("ETag"), LastModified: resp.Header.Get("Last-Modified"), NotModified: resp.StatusCode == http.StatusNotModified}
		if r.NotModified {
			resp.Body.Close()
			return r, nil
		}
		if resp.StatusCode >= 500 {
			resp.Body.Close()
			last = fmt.Errorf("HTTP %d", resp.StatusCode)
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			return r, fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		if htmlOnly && !strings.Contains(strings.ToLower(r.ContentType), "html") {
			resp.Body.Close()
			return r, fmt.Errorf("unsupported content type %q", r.ContentType)
		}
		if n := resp.ContentLength; n > c.opt.MaxBody {
			resp.Body.Close()
			return r, fmt.Errorf("response too large: %s bytes", strconv.FormatInt(n, 10))
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, c.opt.MaxBody+1))
		resp.Body.Close()
		if err != nil {
			return r, err
		}
		if int64(len(body)) > c.opt.MaxBody {
			return r, errors.New("response exceeds maximum body size")
		}
		r.Body = body
		return r, nil
	}
	return Response{}, fmt.Errorf("fetch failed after %d attempts: %w", c.opt.Retries+1, last)
}
