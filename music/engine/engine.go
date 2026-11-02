// Package engine runs mediaserver's music side: the server of the Utako
// apps. It plays nothing; it shares the songs in its folders with the
// Utako apps let in on the dashboard (Music › Devices), streams them, keeps
// the settings and logins a device sends for the others, and takes the songs
// the devices allowed to upload send. Its data, library and devices are its
// own, apart from the other sides'. The dashboard's Music pages reach it
// through the anime side (Handler).
package engine

import (
	"net/http"

	"github.com/v0-0x/mediaserver/music/internal/api"
	"github.com/v0-0x/mediaserver/music/internal/util"
)

// DefaultPort is where the Utako apps reach the music side (discovery on
// 43412/udp).
const DefaultPort = api.DefaultPort

// Options say how the music side runs.
type Options struct {
	DataDir string
	// Version is mediaserver's.
	Version string
	// DashboardPort is where a browser opening the music side's port is
	// sent.
	DashboardPort int
	// Uploaded is told of each file uploaded whole (see api.Options).
	Uploaded func(r *http.Request, device, rel, full string, size int64)
}

// Engine is the running music side.
type Engine struct{ srv *api.Server }

// Start opens the music side's data and starts it.
func Start(o Options) (*Engine, error) {
	srv, err := api.New(api.Options{DataDir: o.DataDir, Version: o.Version, DashboardPort: o.DashboardPort, Free: util.Free, Uploaded: o.Uploaded})
	if err != nil {
		return nil, err
	}
	if err := srv.Start(); err != nil {
		return nil, err
	}
	return &Engine{srv: srv}, nil
}

// Handler is the music side's API for the dashboard (see the anime side's
// SetMusic).
func (e *Engine) Handler() http.Handler { return e.srv.Handler() }

// Port is where the Utako apps reach the music side.
func (e *Engine) Port() int { return e.srv.Port() }

// Shutdown stops the music side.
func (e *Engine) Shutdown() { e.srv.Shutdown() }
