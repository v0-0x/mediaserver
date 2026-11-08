package share

import (
	"net"
	"testing"
	"time"
)

// A shared file's image subtitles are drawn by its host from Aniko 2.0.63
// and mediaserver 1.0.22 on; an older host is named, an unknown version
// (a build from source) is taken as able.
func TestBurnsSubtitles(t *testing.T) {
	s := hostWithSharing(t)
	now := time.Now()
	add := func(id, name, version, role string) {
		s.peers[id] = &peer{id: id, name: name, version: version, role: role, shares: true, seen: now, addr: net.ParseIP("192.168.1.40"), port: 43211}
	}
	s.mu.Lock()
	add("aaaaaaaaaaaaaaaa", "Old PC", "2.0.62", "")
	add("bbbbbbbbbbbbbbbb", "New PC", "2.0.63", "")
	add("cccccccccccccccc", "Old Pi", "1.0.21", RoleHub)
	add("dddddddddddddddd", "New Pi", "1.0.22", RoleHub)
	add("eeeeeeeeeeeeeeee", "Source", "1.0.0", "")
	s.mu.Unlock()
	for _, tc := range []struct {
		id   string
		want bool
	}{{"aaaaaaaaaaaaaaaa", false}, {"bbbbbbbbbbbbbbbb", true}, {"cccccccccccccccc", false}, {"dddddddddddddddd", true}, {"eeeeeeeeeeeeeeee", true}} {
		ok, name := s.BurnsSubtitles(RemotePath(tc.id, "/a/x.mkv"))
		if ok != tc.want || name == "" {
			t.Errorf("%s: %v %q, want %v", tc.id, ok, name, tc.want)
		}
	}
}
