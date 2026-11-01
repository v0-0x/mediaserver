package stream

import (
	"net/url"
	"testing"
)

// The links ask for what they did, and Go sends them as written.
func TestCleanLink(t *testing.T) {
	for in, want := range map[string]string{
		"https://a.workers.dev/0:/Show%2FSeason 1/[UHD] Ep 1.mkv":       "/0:/Show%2FSeason%201/[UHD]%20Ep%201.mkv",
		"https://a.workers.dev/0:/The Sopranos (1999)/S01/Ep.1.mkv#t=1": "/0:/The%20Sopranos%20(1999)/S01/Ep.1.mkv",
		"https://h.example/dl/abc?token=a+b%2Bc&file=The Sopranos.mkv":  "/dl/abc?token=a+b%2Bc&file=The%20Sopranos.mkv",
		"https://h.example/v/Thé Film|x.mkv":                            "/v/Th%C3%A9%20Film%7Cx.mkv",
		"https://h.example/v/a%zzb 100%.mkv":                            "/v/a%25zzb%20100%25.mkv",
		"https://h.example/v/already%20fine%5B1%5D.mkv?x=%2F":           "/v/already%20fine%5B1%5D.mkv?x=%2F",
		"https://h.example?x=a b":                                       "/?x=a%20b",
	} {
		u, err := url.Parse(CleanLink(in))
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if got := u.RequestURI(); got != want {
			t.Errorf("%q asks for %q, want %q", in, got, want)
		}
	}
	if got := CleanLink("https://h.example"); got != "https://h.example" {
		t.Errorf("no path: %q", got)
	}
}
