package normalize

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

var tracking = map[string]bool{"fbclid": true, "gclid": true, "dclid": true, "msclkid": true, "mc_cid": true, "mc_eid": true}

func Text(s string) string {
	return strings.Join(strings.FieldsFunc(norm.NFKC.String(s), unicode.IsSpace), " ")
}

var volatile = regexp.MustCompile(`(?i)\b\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}(?::\d{2})?(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?\b|(?:©|copyright\s*)\s*20\d{2}`)

// VolatileText drops machine-like timestamps and rolling copyright years; expand only when fixtures prove another stable heuristic.
func VolatileText(s string) string { return Text(volatile.ReplaceAllString(s, "<dynamic-time>")) }

func URL(raw string, base *url.URL) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", &url.Error{Op: "normalize", URL: raw, Err: errScheme}
	}
	if u.User != nil {
		return "", &url.Error{Op: "normalize", URL: raw, Err: errCredentials}
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Fragment = ""
	if (u.Scheme == "http" && u.Port() == "80") || (u.Scheme == "https" && u.Port() == "443") {
		host := u.Hostname()
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		u.Host = host
	}
	q := u.Query()
	for k := range q {
		if tracking[strings.ToLower(k)] || strings.HasPrefix(strings.ToLower(k), "utm_") {
			q.Del(k)
		}
	}
	u.RawQuery = q.Encode()
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String(), nil
}

type schemeError string

func (e schemeError) Error() string { return string(e) }

const errScheme schemeError = "scheme must be http or https"
const errCredentials schemeError = "credentials are not allowed"

func UniqueSorted(in []string) []string {
	m := make(map[string]struct{}, len(in))
	for _, s := range in {
		s = Text(s)
		if s != "" {
			m[s] = struct{}{}
		}
	}
	out := make([]string, 0, len(m))
	for s := range m {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
