package extensions

import (
	"net/http"
	"net/url"
	"testing"
)

// Each extension's runtime must get its own cookie jar: the manager's shared
// Fetcher keeps a jar by default, so without isolation a site session set by
// one extension would ride along on another extension's requests to the same
// host.
func TestExtensionsDoNotShareCookieJar(t *testing.T) {
	m := newTestManager(t)
	installTestExtension(t, m, providerManifest("ext-a"), `class Provider {}`, false)
	installTestExtension(t, m, providerManifest("ext-b"), `class Provider {}`, false)
	a, ok := m.get("ext-a")
	if !ok {
		t.Fatal("ext-a not installed")
	}
	b, _ := m.get("ext-b")
	rtA, err := a.Runtime()
	if err != nil {
		t.Fatal(err)
	}
	rtB, err := b.Runtime()
	if err != nil {
		t.Fatal(err)
	}
	if rtA.fetcher == nil || rtB.fetcher == nil {
		t.Fatal("runtimes have no fetcher")
	}
	jarA := rtA.fetcher.follow.GetClient().Jar
	jarB := rtB.fetcher.follow.GetClient().Jar
	if jarA == nil || jarB == nil {
		t.Fatal("fetcher has no cookie jar")
	}
	if jarA == jarB {
		t.Fatal("two extensions share one cookie jar")
	}
	u, _ := url.Parse("https://example.com/")
	jarA.SetCookies(u, []*http.Cookie{{Name: "sid", Value: "secret"}})
	// ext-a keeps its own cookie (persistence within one extension).
	if got := jarA.Cookies(u); len(got) != 1 || got[0].Value != "secret" {
		t.Fatalf("ext-a lost its own cookie: %v", got)
	}
	// ext-b must not see ext-a's session cookie.
	if got := jarB.Cookies(u); len(got) != 0 {
		t.Fatalf("ext-a's cookie leaked to ext-b: %v", got)
	}
}
