package api

import (
	"net/http"
	"strings"

	"github.com/v0-0x/mediaserver/movies/internal/extensions"
)

// What devices on the LAN may do with providers, extensions and plugins.
//
// They use them (streams, searches, plugin trays and buttons) and see what's
// installed, but providers, extensions and plugins are managed on this
// computer only: installing, updating, removing, turning them on or off,
// setting them up, ordering them, and the repositories, collections and
// marketplace they come from. A LAN device may be anyone's phone (LAN access
// can be on without a password), and what's installed runs code and holds
// keys on this computer.

var errLANManage = forbidden("providers, extensions and plugins can only be installed, set up, turned on or off and removed on this computer")

// thisComputer wraps a handler that manages providers, extensions or
// plugins: devices on the LAN are refused.
func thisComputer(fn func(r *http.Request) (any, error)) func(r *http.Request) (any, error) {
	return func(r *http.Request) (any, error) {
		if !isTrusted(r) {
			return nil, errLANManage
		}
		return fn(r)
	}
}

// lanMayFetch reports whether r may have this computer download u: this
// computer may download any address, devices on the LAN only those set up
// here (the marketplace, addon collections and repositories of the
// settings, which they can't change), so they can't make Moviko fetch, and
// keep in its cache, whatever they like.
func lanMayFetch(r *http.Request, u string, configured ...string) bool {
	if isTrusted(r) {
		return true
	}
	u = strings.TrimSpace(u)
	for _, c := range configured {
		if c = strings.TrimSpace(c); c != "" && c == u {
			return true
		}
	}
	return false
}

// extensionsFor leaves out of the installed extensions what devices on the
// LAN don't get: the values of their settings (API keys, accounts), which
// only this computer sets.
func extensionsFor(r *http.Request, list []extensions.Info) []extensions.Info {
	if isTrusted(r) {
		return list
	}
	out := make([]extensions.Info, len(list))
	for i, e := range list {
		e.UserConfig.Values = nil
		out[i] = e
	}
	return out
}
