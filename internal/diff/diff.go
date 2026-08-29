package diff

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/sitewatch/sitewatch/internal/snapshot"
)

func Compare(a, b snapshot.Page) []snapshot.Change {
	if MeaningfulEqual(a, b) {
		return nil
	}
	var out []snapshot.Change
	if a.Title != b.Title {
		out = append(out, change("modified", "title", "seo", a.Title, b.Title, "", .60))
	}
	if a.Description != b.Description {
		out = append(out, change("modified", "description", "seo", a.Description, b.Description, "", .45))
	}
	out = append(out, diffHeadings(a.Headings, b.Headings)...)
	out = append(out, diffPrices(a.Prices, b.Prices)...)
	out = append(out, diffLinks(a.Links, b.Links)...)
	out = append(out, diffStrings("button", "content", a.Buttons, b.Buttons, .60)...)
	out = append(out, diffStrings("paragraph", "content", withoutPriceOnly(a.Paragraphs, a.Prices), withoutPriceOnly(b.Paragraphs, b.Prices), .35)...)
	out = append(out, diffStructured(a.StructuredData, b.StructuredData, len(a.Prices) == 0 || len(b.Prices) == 0)...)
	if len(out) == 0 && a.VisibleText != b.VisibleText {
		out = append(out, change("modified", "text", "content", short(a.VisibleText), short(b.VisibleText), "", .40))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

func MeaningfulEqual(a, b snapshot.Page) bool {
	return a.Fingerprints.Visible == b.Fingerprints.Visible && a.Fingerprints.Headings == b.Fingerprints.Headings && a.Fingerprints.Links == b.Fingerprints.Links && a.Fingerprints.Prices == b.Fingerprints.Prices && a.Fingerprints.Structured == b.Fingerprints.Structured && a.Fingerprints.Metadata == b.Fingerprints.Metadata
}

func change(t, e, cat, old, new, ctx string, score float64) snapshot.Change {
	return snapshot.Change{Type: t, Entity: e, Category: cat, OldValue: old, NewValue: new, Context: ctx, Score: score}
}
func diffHeadings(a, b []snapshot.Heading) []snapshot.Change {
	aa := make([]string, len(a))
	bb := make([]string, len(b))
	for i, x := range a {
		aa[i] = x.Text
	}
	for i, x := range b {
		bb[i] = x.Text
	}
	return diffStrings("heading", "content", aa, bb, .82)
}
func diffLinks(a, b []snapshot.Link) []snapshot.Change {
	aa := make([]string, len(a))
	bb := make([]string, len(b))
	for i, x := range a {
		aa[i] = x.Text + " → " + x.URL
	}
	for i, x := range b {
		bb[i] = x.Text + " → " + x.URL
	}
	return diffStrings("link", "navigation", aa, bb, .45)
}
func diffStrings(entity, cat string, a, b []string, score float64) []snapshot.Change {
	am := counts(a)
	bm := counts(b)
	var out []snapshot.Change
	ak := make([]string, 0, len(am))
	for s := range am {
		ak = append(ak, s)
	}
	sort.Strings(ak)
	bk := make([]string, 0, len(bm))
	for s := range bm {
		bk = append(bk, s)
	}
	sort.Strings(bk)
	for _, s := range ak {
		n := am[s]
		for i := 0; i < n-bm[s]; i++ {
			out = append(out, change("removed", entity, cat, s, "", "", score))
		}
	}
	for _, s := range bk {
		n := bm[s]
		for i := 0; i < n-am[s]; i++ {
			out = append(out, change("added", entity, cat, "", s, "", score))
		}
	}
	return out
}
func short(s string) string {
	r := []rune(s)
	if len(r) > 240 {
		return string(r[:240]) + "…"
	}
	return s
}
func counts(a []string) map[string]int {
	m := map[string]int{}
	for _, s := range a {
		if s != "" {
			m[s]++
		}
	}
	return m
}
func diffPrices(a, b []snapshot.Price) []snapshot.Change {
	var out []snapshot.Change
	used := make([]bool, len(b))
	for _, old := range a {
		best := -1
		for j, n := range b {
			if !used[j] && old.Currency == n.Currency && strings.EqualFold(old.Context, n.Context) {
				best = j
				break
			}
		}
		if best >= 0 {
			used[best] = true
			n := b[best]
			if math.Abs(old.Amount-n.Amount) > .0001 {
				out = append(out, change("modified", "price", "pricing", old.Raw, n.Raw, old.Context, .96))
			}
		} else {
			out = append(out, change("removed", "price", "pricing", old.Raw, "", old.Context, .95))
		}
	}
	for i, n := range b {
		if !used[i] {
			out = append(out, change("added", "price", "pricing", "", n.Raw, n.Context, .90))
		}
	}
	return out
}
func diffStructured(a, b []snapshot.StructuredItem, includePrice bool) []snapshot.Change {
	key := func(x snapshot.StructuredItem) string { return x.Type + ":" + fmt.Sprint(x.Data["name"]) }
	var aa, bb []string
	bm := map[string]snapshot.StructuredItem{}
	for _, x := range a {
		if x.Type == "Offer" && x.Data["name"] == nil {
			continue
		}
		aa = append(aa, key(x))
	}
	for _, x := range b {
		bm[key(x)] = x
		if x.Type == "Offer" && x.Data["name"] == nil {
			continue
		}
		bb = append(bb, key(x))
	}
	out := diffStrings("structured_data", "product", aa, bb, .90)
	if includePrice {
		for _, old := range a {
			if old.Type != "Product" && old.Data["name"] == nil {
				continue
			}
			k := key(old)
			next, ok := bm[k]
			if !ok {
				continue
			}
			op, ook := nestedNumber(old.Data, "price")
			np, nok := nestedNumber(next.Data, "price")
			if ook && nok && op != np {
				out = append(out, change("modified", "price", "pricing", fmt.Sprint(op), fmt.Sprint(np), fmt.Sprint(old.Data["name"]), .98))
			}
		}
	}
	return out
}
func nestedNumber(v any, want string) (float64, bool) {
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			if k == want {
				if n, ok := v.(float64); ok {
					return n, true
				}
			}
			if n, ok := nestedNumber(v, want); ok {
				return n, true
			}
		}
	case []any:
		for _, v := range x {
			if n, ok := nestedNumber(v, want); ok {
				return n, true
			}
		}
	}
	return 0, false
}

func Filter(changes []snapshot.Change, min float64) []snapshot.Change {
	out := make([]snapshot.Change, 0, len(changes))
	for _, c := range changes {
		if c.Score >= min {
			out = append(out, c)
		}
	}
	return out
}

func withoutPriceOnly(paragraphs []string, prices []snapshot.Price) []string {
	out := make([]string, 0, len(paragraphs))
	for _, p := range paragraphs {
		priceOnly := false
		for _, x := range prices {
			if strings.TrimSpace(p) == x.Raw {
				priceOnly = true
				break
			}
		}
		if !priceOnly {
			out = append(out, p)
		}
	}
	return out
}
