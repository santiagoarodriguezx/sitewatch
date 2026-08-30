package normalize

import "testing"

func TestURL(t *testing.T) {
	got, err := URL("HTTPS://Example.COM:443/a?utm_source=x&b=2&fbclid=z#top", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://example.com/a?b=2" {
		t.Fatalf("got %q", got)
	}
}
func TestRejectScheme(t *testing.T) {
	if _, err := URL("file:///etc/passwd", nil); err == nil {
		t.Fatal("expected error")
	}
}
func TestIPv6DefaultPort(t *testing.T) {
	got, err := URL("https://[2001:db8::1]:443/a", nil)
	if err != nil || got != "https://[2001:db8::1]/a" {
		t.Fatalf("got %q %v", got, err)
	}
}
