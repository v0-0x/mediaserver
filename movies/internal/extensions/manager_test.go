package extensions

import (
	"context"
	"testing"

	"github.com/v0-0x/mediaserver/movies/internal/events"
)

// Extensions loaded at startup don't keep their code in memory: it's read
// from the database when they start, and saving their settings (which
// doesn't carry it) leaves it there.
func TestCodeStaysInDatabase(t *testing.T) {
	m := newTestManager(t)
	installTestExtension(t, m, providerManifest("stored"), `
class Provider {
  search(opts) { return [{ id: 'found ' + opts.query, title: '', url: '', subOrDub: 'sub' }]; }
}`, false)

	m2 := NewManager(m.db, m.settings, events.NewHub())
	m2.LoadAll()
	t.Cleanup(m2.Shutdown)
	l, ok := m2.get("stored")
	if !ok {
		t.Fatal("not loaded")
	}
	if l.payload != "" {
		t.Error("the code is kept in memory")
	}
	if err := m2.SetEnabled("stored", false); err != nil {
		t.Fatal(err)
	}
	if err := m2.SetEnabled("stored", true); err != nil {
		t.Fatal(err)
	}
	if err := m2.SaveUserConfig("stored", map[string]string{"mode": "custom"}); err != nil {
		t.Fatal(err)
	}
	p, err := m2.OnlineStreamProvider("stored")
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Search(context.Background(), nil, "it", false)
	if err != nil || len(res) != 1 || res[0].ID != "found it" {
		t.Fatalf("search = %+v, %v", res, err)
	}
}
