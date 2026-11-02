// Command mediaserver is the home server for v0-0x's apps: one program with a
// side for each. The anime side is Aniko's server, for the Aniko apps (port
// 43211, with the dashboard); the movies side is Moviko's, for the Moviko
// apps (port 43311); the music side is Utako's, for the Utako apps (port
// 43411). Each keeps its own data, library, devices and logins: they share
// the program, its updates, and the dashboard's sign-in.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"

	anime "github.com/v0-0x/mediaserver/anime/engine"
	movies "github.com/v0-0x/mediaserver/movies/engine"
	music "github.com/v0-0x/mediaserver/music/engine"
)

// Set when building (-ldflags "-X main.version=… -X main.commit=…"):
// 1.0.<number of commits> for a release.
var (
	version = "1.0.0"
	commit  = ""
)

// codeRestart is the exit code that asks for mediaserver to be started
// again, where it can't do it itself.
const codeRestart = 75

func main() {
	var (
		dataDir       = flag.String("data-dir", defaultDataDir(), "where mediaserver keeps its data (anime/, movies/ and music/ in it)")
		showVersion   = flag.Bool("version", false, "print the version")
		setupCode     = flag.Bool("setup-code", false, "print the setup code (to set the dashboard's password) and stop")
		resetPassword = flag.Bool("reset-password", false, "remove the dashboard's password (set a new one with the setup code) and stop")
		setChannel    = flag.String("set-channel", "", "save which updates to take, stable or beta, and stop")
	)
	// Aniko Hub's service (until mediaserver's install script replaces it)
	// runs the program it updated to, mediaserver, as it ran Aniko Hub:
	// aniko-hub --hub --data-dir ~/.local/share/aniko-hub.
	flag.Bool("hub", true, "ignored (Aniko Hub's)")
	flag.Parse()
	if *showVersion {
		fmt.Println("mediaserver", version)
		return
	}
	root, old := *dataDir, anime.OldDataDir()
	if isHubData(root) {
		root, old = defaultDataDir(), root
	}
	animeDir := moveOldHub(root, old)
	moviesDir := filepath.Join(root, "movies")
	musicDir := filepath.Join(root, "music")
	switch {
	case *setupCode:
		code, err := anime.SetupCode(animeDir)
		if err != nil {
			log.Fatal(err)
		}
		if code == "" {
			fmt.Println("The dashboard's password is set already: sign in with it (or use --reset-password).")
			return
		}
		fmt.Println("Setup code:", code)
		return
	case *resetPassword:
		code, err := anime.ResetPassword(animeDir)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("The password is removed. Restart mediaserver (systemctl --user restart mediaserver), then set a new one on the dashboard with the setup code", code)
		return
	case *setChannel != "":
		if err := anime.SetChannel(animeDir, *setChannel); err != nil {
			log.Fatal(err)
		}
		fmt.Println("mediaserver takes", *setChannel, "updates.")
		return
	}
	limitMemory()

	a, err := anime.Start(anime.Options{DataDir: animeDir, Version: version, Commit: commit, MoviesPort: movies.DefaultPort, MusicPort: music.DefaultPort})
	if err != nil {
		log.Fatal(err)
	}
	m, err := movies.Start(movies.Options{DataDir: moviesDir, Owner: a.IsOwner, DashboardPort: a.Port()})
	if err != nil {
		a.Shutdown()
		log.Fatal(err)
	}
	// The dashboard's movies and TV pages ask the movies side through it.
	a.SetMovies(m.Handler())
	// One log: each side's Logs page shows all of it.
	log.SetOutput(teeLog{a.Log(), m.Keep})
	u, err := music.Start(music.Options{DataDir: musicDir, Version: version, DashboardPort: a.Port()})
	if err != nil {
		m.Shutdown()
		a.Shutdown()
		log.Fatal(err)
	}
	// And its Music pages the music side.
	a.SetMusic(u.Handler())
	log.Printf("mediaserver %s ready: anime (Aniko) at port %d, movies and shows (Moviko) at port %d, music (Utako) at port %d — data: %s", version, a.Port(), m.Port(), u.Port(), root)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	var exit anime.Exit
	select {
	case <-sig:
	case exit = <-a.Exits():
	case x := <-m.Exits():
		exit = anime.Exit{Restart: x.Restart, Code: x.Code, Before: x.Before}
	}
	log.Printf("shutting down…")
	u.Shutdown()
	m.Shutdown()
	a.Shutdown()
	end(exit)
}

// teeLog writes the log out (with the anime side's, which keeps it on disk
// too) and keeps it for the movies side's Logs page.
type teeLog struct {
	out  interface{ Write([]byte) (int, error) }
	keep func([]byte)
}

func (t teeLog) Write(p []byte) (int, error) {
	t.keep(p)
	return t.out.Write(p)
}

// end finishes the process the way exit asks, once both sides have stopped.
func end(exit anime.Exit) {
	restart := exit.Restart
	if exit.Before != nil {
		if err := exit.Before(); err != nil {
			log.Printf("%v; starting mediaserver again", err)
			restart = true
		}
	}
	if !restart {
		if exit.Code != 0 {
			os.Exit(exit.Code)
		}
		return
	}
	// The program runs itself again, in place (systemd sees no exit).
	if canReexec {
		err := reexec()
		log.Printf("could not start mediaserver again: %v", err)
	}
	os.Exit(codeRestart)
}

// defaultDataDir is $MEDIASERVER_DATA_DIR, or ~/.local/share/mediaserver.
func defaultDataDir() string {
	if d := os.Getenv("MEDIASERVER_DATA_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "mediaserver")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "mediaserver")
}

// isHubData reports Aniko Hub's data folder (it has Aniko's database), which
// the old service names: mediaserver's own folder has anime/ and movies/.
func isHubData(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, "aniko.db"))
	return err == nil && st.Mode().IsRegular()
}

// moveOldHub returns the anime side's data folder: anime/ in root, where
// Aniko Hub's data in old (its library, AniList logins, devices and
// password) is moved the first time, with a link left where it was, so
// paths saved in it still work. When it can't be moved, it's used where it
// is.
func moveOldHub(root, old string) string {
	dir := filepath.Join(root, "anime")
	if _, err := os.Lstat(dir); !errors.Is(err, fs.ErrNotExist) {
		return dir
	}
	st, err := os.Lstat(old)
	if err != nil || !st.IsDir() {
		return dir
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		log.Printf("Aniko Hub's data stays in %s: %v", old, err)
		return old
	}
	if err := os.Rename(old, dir); err != nil {
		log.Printf("Aniko Hub's data stays in %s: %v", old, err)
		return old
	}
	if err := os.Symlink(dir, old); err != nil {
		log.Printf("could not leave a link to %s at %s: %v", dir, old, err)
	}
	log.Printf("moved Aniko Hub's data from %s to %s", old, dir)
	return dir
}

// limitMemory sets a soft memory limit for both sides (a Pi has little): the
// garbage collector works harder past it instead of letting the heap grow.
// GOMEMLIMIT and GOGC, when set, are left to Go.
func limitMemory() {
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(512 << 20)
	}
	if os.Getenv("GOGC") == "" {
		debug.SetGCPercent(50)
	}
}
