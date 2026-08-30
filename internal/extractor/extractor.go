package extractor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/sitewatch/sitewatch/internal/normalize"
	"github.com/sitewatch/sitewatch/internal/snapshot"
	"golang.org/x/net/html"
)

var priceRE = regexp.MustCompile(`(?i)([$€£¥]|USD|EUR|GBP|JPY|COP)\s*([0-9]+(?:[.,][0-9]{1,2})?)(?:\s*/\s*(?:mo(?:nth)?|yr|year|mes|año))?`)
var jsonTypes = map[string]bool{"Product": true, "Offer": true, "Organization": true, "Service": true, "Article": true, "SoftwareApplication": true, "FAQPage": true, "Event": true}

func Page(rawURL string, body []byte, status int, fetched time.Time) (snapshot.Page, bool, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return snapshot.Page{}, false, fmt.Errorf("parse HTML: %w", err)
	}
	base, _ := url.Parse(rawURL)
	p := snapshot.Page{URL: rawURL, CanonicalURL: rawURL, HTTPStatus: status, FetchedAt: fetched, Meta: map[string]string{}, OpenGraph: map[string]string{}}
	p.Title = normalize.Text(doc.Find("title").First().Text())
	p.Description = normalize.Text(doc.Find(`meta[name="description"]`).AttrOr("content", ""))
	if c := doc.Find(`link[rel="canonical"]`).AttrOr("href", ""); c != "" {
		if u, e := normalize.URL(c, base); e == nil {
			p.CanonicalURL = u
		}
	}
	doc.Find("meta").Each(func(_ int, s *goquery.Selection) {
		name := strings.ToLower(s.AttrOr("name", s.AttrOr("property", "")))
		content := normalize.Text(s.AttrOr("content", ""))
		if name != "" && content != "" {
			if strings.HasPrefix(name, "og:") {
				p.OpenGraph[name] = content
			} else {
				p.Meta[name] = content
			}
		}
	})
	doc.Find(`script[type="application/ld+json"]`).Each(func(_ int, s *goquery.Selection) { extractJSONLD([]byte(s.Text()), &p.StructuredData) })
	doc.Find("script,style,noscript,svg,canvas,template,iframe,[hidden],[aria-hidden=true],input[type=hidden]").Remove()
	doc.Find("h1,h2,h3,h4,h5,h6").Each(func(_ int, s *goquery.Selection) {
		t := normalize.Text(s.Text())
		if t != "" {
			level, _ := strconv.Atoi(goquery.NodeName(s)[1:])
			p.Headings = append(p.Headings, snapshot.Heading{Level: level, Text: t})
		}
	})
	doc.Find("p,li").Each(func(_ int, s *goquery.Selection) {
		if t := normalize.VolatileText(s.Text()); t != "" {
			p.Paragraphs = append(p.Paragraphs, t)
		}
	})
	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		if u, e := normalize.URL(s.AttrOr("href", ""), base); e == nil {
			p.Links = append(p.Links, snapshot.Link{Text: normalize.Text(s.Text()), URL: u})
		}
	})
	doc.Find("button,input[type=button],input[type=submit]").Each(func(_ int, s *goquery.Selection) {
		if t := normalize.Text(s.Text() + " " + s.AttrOr("value", "")); t != "" {
			p.Buttons = append(p.Buttons, t)
		}
	})
	doc.Find("img[src]").Each(func(_ int, s *goquery.Selection) {
		if u, e := normalize.URL(s.AttrOr("src", ""), base); e == nil {
			p.Images = append(p.Images, snapshot.Image{Src: u, Alt: normalize.Text(s.AttrOr("alt", ""))})
		}
	})
	extractPrices(doc, &p)
	p.VisibleText = normalize.VolatileText(visibleText(doc.Find("body")))
	insufficient := len([]rune(p.VisibleText)) < 80
	p.Fingerprints = snapshot.Fingerprints{HTML: hash(body), Visible: hash([]byte(p.VisibleText)), Headings: hashJSON(p.Headings), Links: hashJSON(sortedLinks(p.Links)), Prices: hashJSON(p.Prices), Structured: hashJSON(p.StructuredData), Metadata: hashJSON([]any{p.Title, p.Description, p.Meta, p.OpenGraph})}
	return p, insufficient, nil
}

func extractPrices(doc *goquery.Document, p *snapshot.Page) {
	lastHeading := ""
	seen := map[string]bool{}
	doc.Find("h1,h2,h3,h4,h5,h6,p,li,span,div").Each(func(_ int, s *goquery.Selection) {
		tag := goquery.NodeName(s)
		t := directText(s)
		if strings.HasPrefix(tag, "h") && len(tag) == 2 {
			if x := normalize.Text(s.Text()); x != "" {
				lastHeading = x
			}
		}
		for _, m := range priceRE.FindAllStringSubmatch(t, -1) {
			raw := normalize.Text(m[0])
			key := raw + "\x00" + lastHeading
			if seen[key] {
				continue
			}
			seen[key] = true
			amount, _ := strconv.ParseFloat(strings.Replace(m[2], ",", ".", -1), 64)
			p.Prices = append(p.Prices, snapshot.Price{Raw: raw, Currency: currency(m[1]), Amount: amount, Context: lastHeading})
		}
	})
}
func directText(s *goquery.Selection) string {
	var b strings.Builder
	for _, n := range s.Nodes {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.TextNode {
				b.WriteString(" ")
				b.WriteString(c.Data)
			}
		}
	}
	return normalize.Text(b.String())
}
func visibleText(s *goquery.Selection) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range s.Nodes {
		walk(n)
	}
	return b.String()
}
func currency(s string) string {
	switch strings.ToUpper(s) {
	case "$", "USD":
		return "USD"
	case "€", "EUR":
		return "EUR"
	case "£", "GBP":
		return "GBP"
	case "¥", "JPY":
		return "JPY"
	default:
		return strings.ToUpper(s)
	}
}

func extractJSONLD(b []byte, out *[]snapshot.StructuredItem) {
	var v any
	if json.Unmarshal(b, &v) != nil {
		return
	}
	walkJSON(v, out)
}
func walkJSON(v any, out *[]snapshot.StructuredItem) {
	switch x := v.(type) {
	case []any:
		for _, v := range x {
			walkJSON(v, out)
		}
	case map[string]any:
		if g, ok := x["@graph"]; ok {
			walkJSON(g, out)
		}
		types := typeNames(x["@type"])
		for _, t := range types {
			if jsonTypes[t] {
				*out = append(*out, snapshot.StructuredItem{Type: t, Data: x})
				break
			}
		}
		for k, v := range x {
			if k != "@graph" {
				walkJSON(v, out)
			}
		}
	}
}
func typeNames(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		r := []string{}
		for _, v := range x {
			if s, ok := v.(string); ok {
				r = append(r, s)
			}
		}
		return r
	}
	return nil
}
func hash(b []byte) string  { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func hashJSON(v any) string { b, _ := json.Marshal(v); return hash(b) }
func sortedLinks(in []snapshot.Link) []snapshot.Link {
	out := append([]snapshot.Link(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].URL == out[j].URL {
			return out[i].Text < out[j].Text
		}
		return out[i].URL < out[j].URL
	})
	return out
}
