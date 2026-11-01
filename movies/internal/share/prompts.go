package share

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/config"
)

// Prompt is what Home asks about another KumoBox on the network: whether to
// share this KumoBox's library with one that's new ("new"), or to update one
// of the two, their versions differing ("older": its KumoBox is older than
// this one, "newer": this one is).
type Prompt struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform,omitempty"`
	Version  string `json:"version"`
}

// Prompts are Home's questions about the other KumoBoxes, for the app on this
// computer (not a device on the network: it can't change sharing).
func (s *Service) Prompts() []Prompt {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running == nil {
		return nil
	}
	out := []Prompt{}
	for _, p := range s.peers {
		if !p.online(now) {
			continue
		}
		// A mediaserver: it shares its library with this KumoBox when its
		// owner says so, and is updated from its dashboard.
		if p.platform == PlatformServer {
			continue
		}
		pr := Prompt{ID: p.id, Name: s.view(p, now).Name, Platform: p.platform, Version: p.version}
		// One that shares its library with this KumoBox isn't new here.
		if !p.allowed && !p.shares && !p.declined {
			pr.Kind = "new"
			out = append(out, pr)
			continue
		}
		if !p.paired() {
			continue
		}
		switch c, ok := compareVersions(p.version, config.AppVersion); {
		case !ok || c == 0:
		case c < 0:
			pr.Kind = "older"
			out = append(out, pr)
		default:
			pr.Kind = "newer"
			out = append(out, pr)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// compareVersions compares two KumoBox versions (2.0.<commits since 2.0.0's>,
// 1.0.<number of commits> before):
// ok is false when either isn't one, or is a build's default (1.0.0: built
// by hand, its version unknown).
func compareVersions(a, b string) (int, bool) {
	pa, oka := versionParts(a)
	pb, okb := versionParts(b)
	if !oka || !okb {
		return 0, false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1, true
			}
			return 1, true
		}
	}
	return 0, true
}

func versionParts(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 || v == "1.0.0" {
		return out, false
	}
	for i, s := range parts {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
