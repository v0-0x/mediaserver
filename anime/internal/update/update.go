// Package update finds newer versions of Aniko on GitHub and installs them.
//
// Aniko ships three ways, and updates each its own way:
//   - Arch Linux builds it from source with packaging/arch/PKGBUILD, so the
//     newest version is a commit: the newest stable release's, or on the
//     beta channel the head of the repository's default branch. The update
//     downloads that source, builds it with makepkg and installs the
//     package with pacman (through pkexec, which asks for the password).
//   - Windows installs the GitHub Release "Aniko <v> for Windows" (tag
//     windows-v<v>). The update downloads the newest one's installer and
//     runs it once Aniko has quit; the installer starts the new version.
//   - macOS has the app from the GitHub Release "Aniko <v> for macOS" (tag
//     macos-v<v>). The update downloads the newest one's zip of the app,
//     unpacks it next to the app and, once Aniko has quit, puts it in the
//     app's place and opens it (apply_mac.go).
//
// Each change is published as a beta first (a GitHub pre-release), and the
// ones meant for everyone as a stable release. Channel says which a copy
// gets: stable releases only (the default), or betas too.
//
// The requests carry the user's GitHub token when one can be found without
// asking (see githubToken); it raises GitHub's limit on requests.
package update

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/config"
	"github.com/v0-0x/mediaserver/anime/internal/events"
	"github.com/v0-0x/mediaserver/anime/internal/lifecycle"
	"github.com/v0-0x/mediaserver/anime/internal/util"
)

// Version is a build of Aniko.
type Version struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date,omitempty"` // when it was made (RFC 3339)
	URL     string `json:"url,omitempty"`  // its page on GitHub
	// Beta: a pre-release, or the default branch's head.
	Beta bool `json:"beta,omitempty"`
}

// States of an update being installed.
const (
	StateIdle        = "idle"
	StateDownloading = "downloading"
	StateBuilding    = "building"
	StateInstalling  = "installing"
	StateReady       = "ready" // installed: Aniko restarts into it
	StateFailed      = "failed"
)

// Status is what the UI shows: GET /api/update, and the update-status event
// at every change.
type Status struct {
	Current Version `json:"current"`
	// Latest is the newest version, once a check found it.
	Latest       *Version `json:"latest"`
	Available    bool     `json:"available"`
	Changes      []string `json:"changes"` // commit subjects, newest first
	ChangesTotal int      `json:"changesTotal"`
	Note         string   `json:"note,omitempty"`
	CheckedAt    int64    `json:"checkedAt"` // unix seconds, 0 before the first check
	Checking     bool     `json:"checking"`
	Error        string   `json:"error,omitempty"` // the last check failed
	Hint         string   `json:"hint,omitempty"`  // what to do about it

	// CanApply: this copy can update itself (ApplyNote says how), or not
	// (ApplyNote says why).
	CanApply  bool   `json:"canApply"`
	ApplyNote string `json:"applyNote,omitempty"`

	State    string   `json:"state"`
	Progress int      `json:"progress"` // 0-100, -1 when unknown
	Message  string   `json:"message,omitempty"`
	Log      []string `json:"log"` // the last output lines
	// ManualCommand is a command to run in a terminal: to install the
	// update when Aniko can't ask for the password, or what the build needs.
	ManualCommand string `json:"manualCommand,omitempty"`
	// InstallFile: on Android, the downloaded APK the app asks Android to
	// install (it's the app that can).
	InstallFile string `json:"installFile,omitempty"`
}

const (
	defaultAPI = "https://api.github.com"
	// packagedExe is where packaging/arch/PKGBUILD installs the server.
	packagedExe = "/usr/lib/aniko/aniko"
	// VersionBase is the number of commits of the commit that's 2.0.0
	// (desktop/package.json's anikoVersionBase): a build's version is
	// 2.0.<commits since then>, as the PKGBUILD and the release workflows
	// make it, and was 1.0.<commits> before.
	VersionBase = 147
	maxChanges  = 30
	maxLogLines = 20
	// failureFile keeps why an update failed after Aniko had quit for it
	// (in CacheDir), for the next run to show.
	failureFile = "last-failure.txt"
)

// Checker looks for updates (just after Start, then every 6 hours, and when
// asked: the app asks each time it's opened) and installs them.
type Checker struct {
	// API is GitHub's REST API (tests use a fake one) and Repo the
	// repository, "owner/name"; RepoID its number, which the API is asked
	// by when set (a renamed repository's old name can be taken).
	API    string
	Repo   string
	RepoID string
	Client *http.Client
	Hub    *events.Hub
	// Exits ends Aniko when an update needs it to restart or to quit.
	Exits *lifecycle.Exits
	// The running build.
	Version string
	Commit  string
	// GOOS decides how Aniko updates: "windows" and "darwin" from
	// releases, otherwise from source (a stable release's, or the default
	// branch's head).
	GOOS string
	// Channel returns "beta" for betas too, anything else (or nil) for
	// stable releases only.
	Channel func() string
	// Executable is Aniko's server program, symlinks resolved.
	Executable func() (string, error)
	// PackagedExe is where the Arch package installs the server: only that
	// copy updates itself on Linux.
	PackagedExe string
	// CacheDir holds the downloads and the build (<user cache>/aniko/update).
	CacheDir string
	// Token returns a GitHub token, or "" (see githubToken).
	Token func(ctx context.Context) string
	// StartInstaller starts the Windows installer, on its own, outside
	// Aniko's job object (see startInstaller).
	StartInstaller func(path string, args ...string) error
	// macOS: StartHelper starts the program that opens the new app once
	// the desktop app (the process ParentPID) has quit, and Writable says
	// whether Aniko may put files in a folder.
	StartHelper func(name string, args ...string) error
	ParentPID   func() int
	Writable    func(dir string) bool
	// FirstCheck is how long after Start the first check runs, Interval the
	// time between checks.
	FirstCheck time.Duration
	Interval   time.Duration

	mu       sync.Mutex
	st       Status
	asset    *asset        // the newest release's installer (Windows) or app (macOS)
	inflight chan struct{} // closed when the running check ends
	applying bool
	pending  bool // a publish is scheduled (publishSoonLocked)

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New makes the checker for this copy of Aniko. It does nothing until Start
// or a call.
func New(hub *events.Hub, exits *lifecycle.Exits) *Checker {
	repo, id := config.UpdateRepo, config.UpdateRepoID
	if r := strings.TrimSpace(config.Getenv("UPDATE_REPO")); r != "" {
		repo, id = r, ""
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		cache = os.TempDir()
	}
	c := &Checker{
		API:            defaultAPI,
		Repo:           repo,
		RepoID:         id,
		Client:         defaultClient,
		Hub:            hub,
		Exits:          exits,
		Version:        config.AppVersion,
		Commit:         config.BuildCommit(),
		GOOS:           runtime.GOOS,
		Executable:     executable,
		PackagedExe:    packagedExe,
		CacheDir:       filepath.Join(cache, "aniko", "update"),
		Token:          githubToken,
		StartInstaller: startInstaller,
		StartHelper:    util.Detach,
		ParentPID:      os.Getppid,
		Writable:       writable,
		FirstCheck:     5 * time.Second,
		Interval:       6 * time.Hour,
	}
	c.st.State = StateIdle
	c.ctx, c.cancel = context.WithCancel(context.Background())
	return c
}

// defaultClient talks to GitHub (and the hosts it redirects downloads to):
// public addresses only, like util.HTTP, but without its 30-second limit,
// which a download could need more than. Contexts bound every request.
var defaultClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 util.PublicProxy,
		DialContext:           util.PublicDialContext(15 * time.Second),
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: time.Second,
	},
}

// executable is the server's program, symlinks resolved (/usr/bin/aniko-server
// is a link to /usr/lib/aniko/aniko).
func executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	// Linux names a program that was replaced while it ran "<path> (deleted)".
	exe = strings.TrimSuffix(exe, " (deleted)")
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return exe, nil
}

// Start runs the checks in the background until Stop. It also shows why an
// update failed after Aniko had quit for it.
func (c *Checker) Start() {
	if b, err := os.ReadFile(filepath.Join(c.CacheDir, failureFile)); err == nil {
		c.mu.Lock()
		c.st.State, c.st.Message, c.st.Progress = StateFailed, strings.TrimSpace(string(b)), 0
		c.mu.Unlock()
		_ = os.Remove(filepath.Join(c.CacheDir, failureFile))
	}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.loop()
	}()
}

func (c *Checker) loop() {
	select {
	case <-c.ctx.Done():
		return
	case <-time.After(c.FirstCheck):
	}
	c.cleanup()
	for {
		ctx, cancel := context.WithTimeout(c.ctx, 2*time.Minute)
		c.Check(ctx)
		cancel()
		select {
		case <-c.ctx.Done():
			return
		case <-time.After(c.Interval):
		}
	}
}

// cleanup removes what the last update left (the source, the build, the
// installer, the new app), unless an update is using it: being installed,
// Aniko quitting for it, or its package waiting to be installed by hand.
func (c *Checker) cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.applying || c.st.State == StateReady || c.st.State == StateInstalling {
		return
	}
	// The Windows installer may still be finishing: whatever it holds
	// stays until the next update.
	_ = removeAll(c.CacheDir)
	// macOS: what an update left next to the app (see apply_mac.go).
	if c.GOOS == "darwin" {
		if exe, err := c.Executable(); err == nil {
			if app := macBundle(exe); app != "" {
				_ = removeAll(macStaging(app))
			}
		}
	}
}

// Stop ends the checks and an update being installed (a build is stopped).
func (c *Checker) Stop() {
	c.cancel()
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		log.Printf("update: still stopping, not waiting any longer")
	}
}

// Status is the current status.
func (c *Checker) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statusLocked()
}

func (c *Checker) statusLocked() Status {
	s := c.st
	s.Current = Version{Version: c.Version, Commit: c.Commit}
	if s.Latest != nil {
		latest := *s.Latest
		s.Latest = &latest
	}
	s.Changes = append([]string{}, s.Changes...)
	s.Log = append([]string{}, s.Log...)
	s.CanApply, s.ApplyNote = c.applyable()
	if s.CanApply && fromReleases(c.GOOS) && s.Available && c.asset == nil {
		s.CanApply, s.ApplyNote = false, "The newest release has no "+releaseFile(c.GOOS)+": download Aniko from its page."
	}
	return s
}

// applyable says whether this copy of Aniko can install updates itself, and
// how it does, or why it can't.
func (c *Checker) applyable() (bool, string) {
	exe, err := c.Executable()
	switch c.GOOS {
	case "linux":
		if err != nil || exe != c.PackagedExe {
			return false, "This copy of Aniko isn't the Arch package: update it with git pull and make (or build the PKGBUILD again)."
		}
		for _, tool := range []string{"makepkg", "pacman"} {
			if _, ok := util.LookPath(tool); !ok {
				return false, "Updating needs " + tool + ", which isn't installed: sudo pacman -S --needed base-devel go nodejs npm"
			}
		}
		return true, "Aniko downloads the new version, builds it with makepkg and installs it with pacman, which asks for your password."
	case "windows":
		// <install dir>\resources\aniko.exe, next to <install dir>\Uninstall Aniko.exe.
		if err != nil || !isFile(filepath.Join(filepath.Dir(filepath.Dir(exe)), "Uninstall Aniko.exe")) {
			return false, "This is a portable copy of Aniko: to update it, download the new portable zip."
		}
		return true, "Aniko downloads the installer and runs it: Aniko closes, the new version installs and Aniko opens again."
	case "darwin":
		return c.macApplyable(exe, err)
	case "android":
		return true, "Aniko downloads the new version and Android asks you to install it."
	case HubSystem:
		if err != nil || !c.Writable(filepath.Dir(exe)) {
			return false, "mediaserver's program can't be replaced where it is: install it again with its install script."
		}
		return true, "mediaserver downloads the new version, swaps it in and starts again (a few seconds)."
	}
	return false, "Aniko can't update itself on this system."
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// publishLocked sends the status to the UI. Under c.mu, so that the UI gets
// the changes in order; Hub.Publish never blocks.
func (c *Checker) publishLocked() {
	if c.Hub != nil {
		c.Hub.Publish(events.UpdateStatus, c.statusLocked())
	}
}

// publishSoonLocked publishes within 250 ms: a burst of output lines or
// download progress becomes one event.
func (c *Checker) publishSoonLocked() {
	if c.pending {
		return
	}
	c.pending = true
	time.AfterFunc(250*time.Millisecond, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.pending = false
		c.publishLocked()
	})
}

// ---------------------------------------------------------------------------
// Checks

// result is what a check found.
type result struct {
	latest       *Version
	available    bool
	changes      []string
	changesTotal int
	note         string
	asset        *asset
}

// asset is a release file.
type asset struct {
	ID     int64
	Name   string
	Size   int64
	Digest string // "sha256:<hex>", or "" when GitHub has none
}

// Check looks for a newer version now and returns the status. When a check
// is already running, it waits for that one instead.
func (c *Checker) Check(ctx context.Context) Status {
	c.mu.Lock()
	if running := c.inflight; running != nil {
		c.mu.Unlock()
		select {
		case <-running:
		case <-ctx.Done():
		}
		return c.Status()
	}
	done := make(chan struct{})
	c.inflight = done
	c.st.Checking = true
	c.publishLocked()
	c.mu.Unlock()

	res, err := c.check(ctx)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.inflight = nil
	close(done)
	c.st.Checking = false
	if errors.Is(err, context.Canceled) {
		// Aniko is shutting down: that says nothing about updates.
		c.publishLocked()
		return c.statusLocked()
	}
	c.st.CheckedAt = time.Now().Unix()
	if err != nil {
		// What the last good check found still stands.
		c.st.Error, c.st.Hint = c.describe(err)
		log.Printf("update check: %s", c.st.Error)
	} else {
		c.st.Error, c.st.Hint = "", ""
		c.st.Latest, c.st.Available, c.st.Note = res.latest, res.available, res.note
		c.st.Changes, c.st.ChangesTotal = res.changes, res.changesTotal
		c.asset = res.asset
	}
	c.publishLocked()
	return c.statusLocked()
}

// describe turns a failed check into a message and a hint at what to do.
func (c *Checker) describe(err error) (string, string) {
	var ae *apiError
	switch {
	case errors.As(err, &ae) && (ae.status == http.StatusNotFound || ae.status == http.StatusUnauthorized):
		hint := "If the repository is private, sign in with the GitHub CLI (gh auth login) and check again."
		if c.GOOS == "windows" {
			hint = "If the repository is private, sign in to GitHub: Settings › Desktop app & programs › Install missing programs signs you in, or run gh auth login."
		}
		return fmt.Sprintf("mediaserver can't read github.com/%s (HTTP %d).", c.Repo, ae.status), hint
	case errors.As(err, &ae) && ae.rateLimited:
		return "GitHub's limit on requests was reached: mediaserver checks again later.", "Signing in to GitHub (gh auth login) raises the limit."
	case errors.Is(err, context.DeadlineExceeded):
		return "GitHub didn't answer in time.", ""
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "mediaserver can't reach GitHub: is this computer online?", ""
	}
	// Anything else: said plainly, what happened under it as a detail.
	return "mediaserver couldn't check for updates just now. It tries again later.", "Details: " + err.Error()
}

func (c *Checker) check(ctx context.Context) (result, error) {
	g := &github{api: strings.TrimRight(c.API, "/"), repo: c.Repo, repoID: c.RepoID, client: c.Client, token: c.Token(ctx), userAgent: "mediaserver/" + c.Version}
	if fromReleases(c.GOOS) {
		return c.checkReleases(ctx, g)
	}
	if c.beta() {
		return c.checkBranch(ctx, g)
	}
	return c.checkStable(ctx, g)
}

// beta: this copy gets betas too.
func (c *Checker) beta() bool {
	return c.Channel != nil && c.Channel() == "beta"
}

// checkStable: on Linux, stable copies build the newest stable release's
// commit (that of any system's: they're made from the same commits).
func (c *Checker) checkStable(ctx context.Context, g *github) (result, error) {
	var releases []release
	if _, err := g.getJSON(ctx, g.root()+"/releases?per_page=100", &releases); err != nil {
		return result{}, err
	}
	var best *release
	var version string
	for _, prefix := range []string{"windows-v", "macos-v", "android-v"} {
		if r := pickRelease(releases, prefix, false); r != nil {
			if v := strings.TrimPrefix(r.TagName, prefix); best == nil || compareVersions(v, version) > 0 {
				best, version = r, v
			}
		}
	}
	if best == nil {
		return result{note: "There's no stable release of mediaserver yet."}, nil
	}
	commit, err := g.commit(ctx, best.TagName)
	if err != nil {
		return result{}, err
	}
	latest := &Version{Version: version, Commit: commit.SHA, Date: commit.Commit.Committer.Date, URL: best.HTMLURL}
	return c.against(ctx, g, latest, "the newest stable release")
}

// checkBranch: on Linux, beta copies build the head of the default branch,
// where each change lands first.
func (c *Checker) checkBranch(ctx context.Context, g *github) (result, error) {
	var repo struct {
		DefaultBranch string `json:"default_branch"`
	}
	if _, err := g.getJSON(ctx, g.root(), &repo); err != nil {
		return result{}, err
	}
	if repo.DefaultBranch == "" {
		return result{}, errors.New("GitHub didn't say which branch is the repository's main one")
	}
	head, err := g.commit(ctx, repo.DefaultBranch)
	if err != nil {
		return result{}, err
	}
	latest := &Version{Commit: head.SHA, Date: head.Commit.Committer.Date, URL: head.HTMLURL, Beta: true}
	// The version is only for showing, so a failure here doesn't matter.
	if n := g.commitCount(ctx, head.SHA); n > 0 {
		latest.Version = VersionOf(n)
	}
	return c.against(ctx, g, latest, "GitHub's "+repo.DefaultBranch)
}

// against compares this build with latest (a commit), named what for the
// notes: whether it's newer, and what changed.
func (c *Checker) against(ctx context.Context, g *github, latest *Version, what string) (result, error) {
	head := latest.Commit
	res := result{latest: latest}
	if c.Commit == "" {
		res.available = true
		res.note = "mediaserver can't tell which version this copy is (it was built without its git commit), so it offers the newest one."
		return res, nil
	}
	// Against the head's commit, not the branch: the version, the changes
	// and the source an update builds then all match, even if the branch
	// moves meanwhile.
	cmp, err := g.compare(ctx, c.Commit, head)
	var ae *apiError
	if errors.As(err, &ae) && (ae.status == http.StatusNotFound || ae.status == http.StatusUnprocessableEntity) {
		res.available = true
		res.note = "This copy was built from a commit GitHub doesn't have (" + short(c.Commit) + "), so mediaserver can't tell what changed."
		return res, nil
	}
	if err != nil {
		return result{}, err
	}
	switch cmp.Status {
	case "ahead":
		res.available = cmp.AheadBy > 0
	case "diverged":
		res.available = cmp.AheadBy > 0
		res.note = "This copy has changes that aren't on GitHub: updating replaces them with " + what + "."
	case "behind":
		res.note = "This copy is newer than " + what + "."
		if !c.beta() {
			res.note += " It stays until a newer stable release comes out."
		}
	}
	if res.available {
		res.changes, res.changesTotal = cmp.changes(), cmp.AheadBy
		if len(cmp.Commits) < cmp.AheadBy {
			// GitHub lists the first 250 commits of a comparison: the newest
			// ones come from the branch's history instead.
			if recent, err := g.recentSubjects(ctx, head); err == nil {
				res.changes = recent
			}
		}
	}
	return res, nil
}

// fromReleases: Windows, macOS and Android update from GitHub Releases,
// Linux from the default branch.
func fromReleases(goos string) bool {
	return goos == "windows" || goos == "darwin" || goos == "android" || goos == HubSystem
}

// HubSystem is what mediaserver updates as (Checker.GOOS): its own releases,
// server-v<version>, one archive per processor (see applyHub).
const HubSystem = "hub"

// releasePrefix starts the tags of a system's releases: <prefix><version>.
func releasePrefix(goos string) string {
	switch goos {
	case "darwin":
		return "macos-v"
	case "android":
		return "android-v"
	case HubSystem:
		return "server-v"
	}
	return "windows-v"
}

// releaseAsset names the release file an update installs.
func releaseAsset(goos, version string) string {
	switch goos {
	case "darwin":
		return "Aniko-" + version + "-macos-universal.zip"
	case "android":
		return "Aniko-" + version + "-android.apk"
	case HubSystem:
		return HubArchive(version, runtime.GOARCH)
	}
	return "Aniko-Setup-" + version + "-windows-x64.exe"
}

// releaseFile says what that file is, for messages.
func releaseFile(goos string) string {
	switch goos {
	case "darwin":
		return "zip of the app"
	case "android":
		return "APK"
	case HubSystem:
		return "archive for this computer"
	}
	return "installer"
}

// checkReleases: on Windows the newest version is the newest release tagged
// windows-v<version>, on macOS macos-v<version> (betas only on the beta
// channel). 100 at a time: each system's newest betas and stable releases.
func (c *Checker) checkReleases(ctx context.Context, g *github) (result, error) {
	var releases []release
	if _, err := g.getJSON(ctx, g.root()+"/releases?per_page=100", &releases); err != nil {
		return result{}, err
	}
	prefix := releasePrefix(c.GOOS)
	best := pickRelease(releases, prefix, c.beta())
	if best == nil {
		if c.GOOS == HubSystem {
			return result{note: "There's no mediaserver release yet."}, nil
		}
		system := map[string]string{"darwin": "macOS", "android": "Android"}[c.GOOS]
		if system == "" {
			system = "Windows"
		}
		return result{note: "There's no Aniko release for " + system + " yet."}, nil
	}
	version := strings.TrimPrefix(best.TagName, prefix)
	latest := &Version{Version: version, Date: best.PublishedAt, URL: best.HTMLURL, Beta: best.Prerelease}
	if isSHA(best.TargetCommitish) {
		latest.Commit = best.TargetCommitish
	}
	res := result{latest: latest, available: compareVersions(version, c.Version) > 0}
	if !c.beta() && compareVersions(version, c.Version) < 0 {
		res.note = "This copy is a beta, newer than the newest stable release (" + version + "). It stays until a newer stable release comes out."
	}
	file := releaseAsset(c.GOOS, version)
	for _, a := range best.Assets {
		if a.Name == file {
			res.asset = &asset{ID: a.ID, Name: a.Name, Size: a.Size, Digest: a.Digest}
		}
	}
	// What changed since this build, when it knows its commit: a bonus,
	// which doesn't fail the check.
	if res.available && c.Commit != "" {
		if cmp, err := g.compare(ctx, c.Commit, best.TagName); err == nil && cmp.AheadBy > 0 {
			res.changes, res.changesTotal = cmp.changes(), cmp.AheadBy
		}
	}
	return res, nil
}

// VersionOf is the version of the build with n commits.
func VersionOf(n int) string {
	if n >= VersionBase {
		return fmt.Sprintf("2.0.%d", n-VersionBase)
	}
	return fmt.Sprintf("1.0.%d", n)
}

// pickRelease returns the published release with the highest version
// tagged <prefix><version> (windows-v1.0.9), or nil; pre-releases (betas)
// only with beta.
func pickRelease(releases []release, prefix string, beta bool) *release {
	var best *release
	for i := range releases {
		r := &releases[i]
		v, ok := strings.CutPrefix(r.TagName, prefix)
		if r.Draft || (r.Prerelease && !beta) || !ok || !releaseVersion.MatchString(v) {
			continue
		}
		if best == nil || compareVersions(v, strings.TrimPrefix(best.TagName, prefix)) > 0 {
			best = r
		}
	}
	return best
}

// compareVersions compares dotted version numbers ("1.0.10" > "1.0.9"); a
// missing part counts as 0, and only a part's leading digits count.
func compareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(pa), len(pb)); i++ {
		var x, y int
		if i < len(pa) {
			x = leadingNumber(pa[i])
		}
		if i < len(pb) {
			y = leadingNumber(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func leadingNumber(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' || n > 1e8 {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func isSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	return !strings.ContainsFunc(s, func(r rune) bool { return !strings.ContainsRune("0123456789abcdef", r) })
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// ---------------------------------------------------------------------------
// Installing

// Reasons Apply refuses.
var (
	ErrRunning  = errors.New("an update is already being installed")
	ErrNoUpdate = errors.New("there's no newer version to install")
)

// Apply starts installing the newest version, in the background: the
// status follows it. On Linux it ends "ready" (restart Aniko to use it); on
// Windows Aniko quits for the installer, which starts the new version, and on
// macOS to swap in the new app, which then opens.
func (c *Checker) Apply() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Installing: on Windows and macOS, Aniko is quitting for the new
	// version.
	// Android: the APK is there, Android asks again (the last time was
	// cancelled).
	if c.GOOS == "android" && c.st.State == StateInstalling && c.st.InstallFile != "" && !c.applying {
		c.publishLocked()
		return nil
	}
	if c.applying || c.st.State == StateInstalling {
		return ErrRunning
	}
	if !c.st.Available || c.st.Latest == nil {
		return ErrNoUpdate
	}
	st := c.statusLocked()
	if !st.CanApply {
		return errors.New(st.ApplyNote)
	}
	target := *c.st.Latest
	var inst *asset
	if c.asset != nil {
		a := *c.asset
		inst = &a
	}
	c.applying = true
	c.st.State, c.st.Progress, c.st.Message = StateDownloading, -1, "Downloading mediaserver "+display(target)+"…"
	c.st.Log, c.st.ManualCommand, c.st.InstallFile = nil, "", ""
	c.publishLocked()
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		g := &github{api: strings.TrimRight(c.API, "/"), repo: c.Repo, repoID: c.RepoID, client: c.Client, token: c.Token(c.ctx), userAgent: "mediaserver/" + c.Version}
		var err error
		switch c.GOOS {
		case "windows":
			err = c.applyWindows(c.ctx, g, target, inst)
		case "darwin":
			err = c.applyMac(c.ctx, g, target, inst)
		case "android":
			err = c.applyAndroid(c.ctx, g, target, inst)
		case HubSystem:
			err = c.applyHub(c.ctx, g, target, inst)
		default:
			err = c.applyLinux(c.ctx, g, target)
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		c.applying = false
		if err != nil {
			c.st.State, c.st.Progress = StateFailed, 0
			c.st.Message = failureMessage(err)
			var me *manualError
			if errors.As(err, &me) {
				c.st.ManualCommand = me.command
			}
			log.Printf("update: %s", c.st.Message)
		}
		c.publishLocked()
	}()
	return nil
}

// display names a version: its number, or else its commit.
func display(v Version) string {
	if v.Version != "" {
		return v.Version
	}
	return short(v.Commit)
}

func failureMessage(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "Cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "The update took too long and was stopped."
	}
	return err.Error()
}

// manualError is a failure the user can fix with a command.
type manualError struct {
	msg     string
	command string
}

func (e *manualError) Error() string { return e.msg }

// step moves the update to a new state and tells the UI. progress is
// 0-100, or -1 when unknown.
func (c *Checker) step(state string, progress int, msg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.st.State, c.st.Progress = state, progress
	if msg != "" {
		c.st.Message = msg
	}
	c.publishLocked()
}

// output adds a line of a program's output to the log the UI shows.
func (c *Checker) output(line string) {
	if len(line) > 300 {
		line = strings.ToValidUTF8(line[:300], "") + "…"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.st.Log = append(c.st.Log, line)
	if n := len(c.st.Log); n > maxLogLines {
		c.st.Log = slices.Clone(c.st.Log[n-maxLogLines:])
	}
	c.publishSoonLocked()
}

// saveFailure keeps why an update failed after Aniko had shut down for it,
// for the next run to show (see Start).
func (c *Checker) saveFailure(msg string) {
	if err := os.MkdirAll(c.CacheDir, 0o700); err == nil {
		_ = os.WriteFile(filepath.Join(c.CacheDir, failureFile), []byte(msg), 0o600)
	}
}

// removeAll removes a folder, also when the build made parts of it
// read-only.
func removeAll(dir string) error {
	if err := os.RemoveAll(dir); err == nil {
		return nil
	}
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o700)
		}
		return nil
	})
	return os.RemoveAll(dir)
}
