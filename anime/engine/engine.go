// Package engine runs mediaserver's anime side: Aniko's server as a hub. It
// plays nothing; it shares its library with the Aniko apps at home, streams
// to them, keeps the devices on each AniList account in step, takes their
// downloads when allowed, and has the anime dashboard. Its data, library and
// devices are its own, apart from the movies side's.
package engine

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/v0-0x/mediaserver/anime/internal/anikohub"
	"github.com/v0-0x/mediaserver/anime/internal/api"
	"github.com/v0-0x/mediaserver/anime/internal/app"
	"github.com/v0-0x/mediaserver/anime/internal/config"
	"github.com/v0-0x/mediaserver/anime/internal/db"
	"github.com/v0-0x/mediaserver/anime/internal/util"
	"github.com/v0-0x/mediaserver/anime/internal/webui"
)

// ProtocolVersion is the Aniko version this side's code comes from: what it
// tells the Aniko apps it is (they compare it with their own), not
// mediaserver's version.
const ProtocolVersion = "2.0.42"

// DefaultPort is where the Aniko apps and the dashboard reach this side
// (Aniko Hub's port: a Pi that came from it keeps its address).
const DefaultPort = 43211

// Options say how the anime side runs.
type Options struct {
	DataDir string
	// Version and Commit are mediaserver's: its updater compares them with
	// mediaserver's releases.
	Version, Commit string
	// MoviesPort is where the movies side answers, for the dashboard.
	MoviesPort int
}

// Engine is the running anime side.
type Engine struct {
	app *app.App
	srv *api.Server
}

// Exit is what the anime side asks of the program: to stop, and start
// again (its updater installed a new version) or end with Code. Before
// runs first, once everything has stopped.
type Exit struct {
	Restart bool
	Code    int
	Before  func() error
}

// Start opens the anime side's data and starts it.
func Start(o Options) (*Engine, error) {
	config.AppVersion = ProtocolVersion
	config.MoveOldFiles(o.DataDir)
	if err := os.MkdirAll(o.DataDir, 0o700); err != nil {
		return nil, err
	}
	util.SetToolsDir(filepath.Join(o.DataDir, "bin"))
	a, err := app.New(o.DataDir, app.Options{Hub: true})
	if err != nil {
		return nil, err
	}
	a.Update.Version, a.Update.Commit = o.Version, o.Commit
	srv := api.New(a, webui.FS())
	srv.ForceWebUI = true
	srv.MoviesPort = o.MoviesPort
	if err := srv.Start(); err != nil {
		a.Shutdown()
		return nil, fmt.Errorf("could not start the anime side: %v (is mediaserver, or Aniko Hub, already running?)", err)
	}
	a.Start()
	e := &Engine{app: a, srv: srv}
	cfg := a.Settings.Get()
	log.Printf("anime: dashboard at http://%s:%d", hostName(), cfg.Server.Port)
	if cfg.Server.Password == "" {
		log.Printf("anime: setup code %s (to set the dashboard's password)", a.HubStore.SetupCode())
	}
	return e, nil
}

// SetMovies gives the dashboard the movies side's API, which its movies
// and TV pages ask through the anime side.
func (e *Engine) SetMovies(h http.Handler) { e.srv.SetMovies(h) }

// Port is where the anime side answers.
func (e *Engine) Port() int { return e.app.Settings.Get().Server.Port }

// Exits gives what the anime side asks of the program (see Exit).
func (e *Engine) Exits() <-chan Exit {
	out := make(chan Exit, 1)
	go func() {
		x := <-e.app.Exits.C()
		out <- Exit{Restart: x.Restart, Code: x.Code, Before: x.Before}
	}()
	return out
}

// Shutdown stops the anime side.
func (e *Engine) Shutdown() {
	e.srv.Shutdown()
	e.app.Shutdown()
}

// Log is where the log goes: the anime side's buffer (the dashboard's log,
// kept on disk too), which writes it out.
func (e *Engine) Log() interface{ Write([]byte) (int, error) } { return e.app.Logs }

// IsOwner reports a browser signed in to the dashboard: the movies side
// takes the same sign-in.
func (e *Engine) IsOwner(r *http.Request) bool { return e.srv.IsOwner(r) }

// hostName is how the server is reached on the network: its name (a Pi is
// raspberrypi.local), for the log.
func hostName() string {
	n, err := os.Hostname()
	if err != nil || n == "" {
		return "<this computer>"
	}
	if !strings.Contains(n, ".") {
		n += ".local"
	}
	return n
}

// openSettings opens the database in dataDir and its settings, for the
// flags that only read or save one and stop.
func openSettings(dataDir string) (*db.DB, *config.Store, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, nil, err
	}
	d, err := db.Open(filepath.Join(dataDir, "aniko.db"))
	if err != nil {
		return nil, nil, err
	}
	settings, err := config.NewStore(d)
	if err != nil {
		d.Close()
		return nil, nil, err
	}
	return d, settings, nil
}

// SetupCode is the code that sets the dashboard's password, or "" when it's
// set already.
func SetupCode(dataDir string) (string, error) {
	d, settings, err := openSettings(dataDir)
	if err != nil {
		return "", err
	}
	defer d.Close()
	if settings.Get().Server.Password != "" {
		return "", nil
	}
	h, err := anikohub.New(d)
	if err != nil {
		return "", err
	}
	return h.SetupCode(), nil
}

// ResetPassword removes the dashboard's password (for its owner who forgot
// it) and returns the setup code that sets a new one.
func ResetPassword(dataDir string) (string, error) {
	d, settings, err := openSettings(dataDir)
	if err != nil {
		return "", err
	}
	defer d.Close()
	cfg := settings.Get()
	cfg.Server.Password = ""
	if _, err := settings.Save(cfg); err != nil {
		return "", err
	}
	h, err := anikohub.New(d)
	if err != nil {
		return "", err
	}
	return h.SetupCode(), nil
}

// SetChannel saves which updates mediaserver takes: stable or beta.
func SetChannel(dataDir, channel string) error {
	if !slices.Contains(config.UpdateChannels, channel) {
		return fmt.Errorf("the channel is %s, not %q", strings.Join(config.UpdateChannels, " or "), channel)
	}
	d, settings, err := openSettings(dataDir)
	if err != nil {
		return err
	}
	defer d.Close()
	cfg := settings.Get()
	cfg.Updates.Channel = channel
	_, err = settings.Save(cfg)
	return err
}

// OldDataDir is where Aniko Hub kept its data (aniko-hub, or kumo-hub from
// before Aniko's rename): mediaserver moves it into its own folder, so a Pi
// that ran Aniko Hub keeps its library, logins, devices and password.
func OldDataDir() string { return config.HubDataDir() }
