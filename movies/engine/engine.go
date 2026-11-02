// Package engine runs mediaserver's movies side: Moviko's server, for movies
// and TV shows. It plays nothing; it shares its library with the Moviko apps
// at home (as any Moviko does, through its sharing), streams to them and
// takes their downloads when allowed. Its own pages are Moviko's, on its own
// port, for whoever signed in to mediaserver's dashboard. Its data, library
// and devices are its own, apart from the anime side's.
package engine

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/v0-0x/mediaserver/movies/internal/api"
	"github.com/v0-0x/mediaserver/movies/internal/app"
	"github.com/v0-0x/mediaserver/movies/internal/config"
	"github.com/v0-0x/mediaserver/movies/internal/util"
	"github.com/v0-0x/mediaserver/movies/internal/webui"
)

// ProtocolVersion is the Moviko version this side's code comes from: what it
// tells the Moviko apps it is, not mediaserver's version.
const ProtocolVersion = "2.0.9"

// DefaultPort is where the Moviko apps and the movies pages reach this side
// (Moviko's own port).
const DefaultPort = config.DefaultPort

// Options say how the movies side runs.
type Options struct {
	DataDir string
	// Owner reports a browser signed in to mediaserver's dashboard, which
	// may use the movies pages; DashboardPort is where that dashboard is,
	// where the others are sent to sign in.
	Owner         func(r *http.Request) bool
	DashboardPort int
	// Uploaded is told of each file uploaded whole (see api.Server).
	Uploaded func(r *http.Request, device, rel, full string, size int64)
}

// Engine is the running movies side.
type Engine struct {
	app *app.App
	srv *api.Server
}

// Exit is what the movies side asks of the program: to stop, and start again
// (Settings › Restart) or end with Code. Before runs first, once everything
// has stopped.
type Exit struct {
	Restart bool
	Code    int
	Before  func() error
}

// Start opens the movies side's data and starts it.
func Start(o Options) (*Engine, error) {
	config.AppVersion = ProtocolVersion
	if err := os.MkdirAll(o.DataDir, 0o700); err != nil {
		return nil, err
	}
	util.SetToolsDir(filepath.Join(o.DataDir, "bin"))
	a, err := app.New(o.DataDir)
	if err != nil {
		return nil, err
	}
	// It shares with the network, always: that's what it's for.
	if err := a.Settings.Pin(func(c *config.Settings) {
		c.Sharing.Enabled, c.Server.AllowLAN, c.Server.WebUI = true, true, true
	}); err != nil {
		a.Shutdown()
		return nil, err
	}
	a.Share.Server = true
	// mediaserver updates itself, the movies side with it.
	a.Update.Managed = "mediaserver updates itself, the movies side with it: see Updates on its dashboard."
	srv := api.New(a, webui.FS())
	srv.ForceWebUI = true
	srv.Owner, srv.DashboardPort, srv.Uploaded = o.Owner, o.DashboardPort, o.Uploaded
	if err := srv.Start(); err != nil {
		a.Shutdown()
		return nil, fmt.Errorf("could not start the movies side: %v (is mediaserver, or Moviko, already running?)", err)
	}
	a.Start()
	log.Printf("movies: Moviko's pages at port %d, after signing in to the dashboard", a.Settings.Get().Server.Port)
	return &Engine{app: a, srv: srv}, nil
}

// Handler is the movies side's API, for the dashboard (see the anime
// side's SetMovies).
func (e *Engine) Handler() http.Handler { return e.srv.Handler() }

// Port is where the movies side answers.
func (e *Engine) Port() int { return e.app.Settings.Get().Server.Port }

// Exits gives what the movies side asks of the program (see Exit).
func (e *Engine) Exits() <-chan Exit {
	out := make(chan Exit, 1)
	go func() {
		x := <-e.app.Exits.C()
		out <- Exit{Restart: x.Restart, Code: x.Code, Before: x.Before}
	}()
	return out
}

// Shutdown stops the movies side.
func (e *Engine) Shutdown() {
	e.srv.Shutdown()
	e.app.Shutdown()
}

// Keep records log lines in the movies side's log (its Logs page) without
// writing them out: the anime side's log does.
func (e *Engine) Keep(p []byte) { e.app.Logs.Keep(p) }
