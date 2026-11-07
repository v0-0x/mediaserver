package update

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/v0-0x/mediaserver/anime/internal/lifecycle"
	"github.com/v0-0x/mediaserver/anime/internal/util"
)

const (
	buildTimeout = 30 * time.Minute
	// stallTimeout ends a download that stops receiving data.
	stallTimeout = time.Minute
	// buildDeps is what building Aniko needs (the PKGBUILD's makedepends and
	// makepkg's own tools).
	buildDeps = "sudo pacman -S --needed base-devel go nodejs npm"
)

// ---------------------------------------------------------------------------
// Arch Linux: build the new source with the PKGBUILD, install it with pacman

func (c *Checker) applyLinux(ctx context.Context, g *github, target Version) error {
	dir := c.CacheDir
	if err := removeAll(dir); err != nil {
		return fmt.Errorf("couldn't remove the last update's files: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	// The source of exactly the commit the check found. GitHub redirects
	// to codeload.github.com, with a token of its own in the address.
	archive := filepath.Join(dir, "source.tar.gz")
	err := c.download(ctx, g, g.root()+"/tarball/"+escapeRef(target.Commit), "application/vnd.github+json", archive, 0, func(done, total int64) {
		c.progress(StateDownloading, percent(done, total), fmt.Sprintf("Downloading Aniko %s… %s", display(target), megabytes(done)))
	})
	if err != nil {
		return fmt.Errorf("couldn't download the new version: %w", err)
	}

	c.step(StateBuilding, 0, "Unpacking…")
	top, err := extract(archive, filepath.Join(dir, "src"))
	_ = os.Remove(archive)
	if err != nil {
		return fmt.Errorf("couldn't unpack the new version: %w", err)
	}
	pkg, err := c.build(ctx, top, target)
	// The build tree (with the web UI's packages) is big; only the package
	// is needed now.
	_ = removeAll(filepath.Join(dir, "src"))
	if err != nil {
		return err
	}
	return c.install(ctx, pkg, target)
}

// build runs makepkg on the new source and returns the package it made.
func (c *Checker) build(ctx context.Context, top string, target Version) (string, error) {
	pkgbuild := filepath.Join(top, "packaging", "arch")
	if !isFile(filepath.Join(pkgbuild, "PKGBUILD")) {
		return "", errors.New("the new version has no packaging/arch/PKGBUILD")
	}
	pkgdest := filepath.Join(c.CacheDir, "pkg")
	if err := os.MkdirAll(pkgdest, 0o755); err != nil {
		return "", err
	}
	c.step(StateBuilding, 2, "Building Aniko "+display(target)+"…")
	runCtx, cancel := context.WithTimeout(ctx, buildTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "makepkg", "-f", "--noconfirm", "--nocheck")
	cmd.Dir = pkgbuild
	// The PKGBUILD stamps the version and commit into the server (the
	// source has no .git to read them from). PKGDEST: the package lands in
	// a folder of Aniko's, whatever makepkg.conf says. LC_ALL: makepkg's
	// messages in English, which the stages below go by.
	cmd.Env = append(os.Environ(), "ANIKO_COMMIT="+target.Commit, "ANIKO_VERSION="+target.Version, "PKGDEST="+pkgdest, "LC_ALL=C")
	// go, npm & co. run below makepkg: a process group of its own lets a
	// cancel or the timeout stop all of it.
	util.OwnProcessGroup(cmd)
	cmd.Cancel = func() error { return util.KillGroup(cmd.Process) }
	cmd.WaitDelay = 5 * time.Second

	var missing bool
	var lastError string
	progress := 2
	err := c.run(cmd, func(line string) {
		if msg, p, ok := buildStage(line); ok && p >= progress {
			progress = p
			c.step(StateBuilding, p, msg)
		}
		if strings.Contains(line, "Missing dependencies") || strings.Contains(line, "Could not resolve all dependencies") ||
			strings.HasPrefix(line, "==> ERROR: Cannot find the") {
			missing = true
		}
		if msg, ok := strings.CutPrefix(line, "==> ERROR: "); ok {
			lastError = msg
		}
	})
	switch {
	case ctx.Err() != nil:
		return "", ctx.Err()
	case runCtx.Err() != nil:
		return "", fmt.Errorf("the build didn't finish in %v and was stopped", buildTimeout)
	case missing:
		return "", &manualError{msg: "Building Aniko needs programs that aren't installed: install them, then try again.", command: buildDeps}
	case err != nil && lastError != "":
		return "", errors.New("the build failed: " + lastError)
	case err != nil:
		return "", fmt.Errorf("the build failed (%v): the last lines of its output are below", err)
	}
	return findPackage(pkgdest)
}

// buildStage turns the build's progress into a message, by what makepkg
// and the Makefile print.
func buildStage(line string) (string, int, bool) {
	switch {
	case strings.HasPrefix(line, "==> Checking runtime dependencies"), strings.HasPrefix(line, "==> Checking buildtime dependencies"):
		return "Checking what the build needs…", 4, true
	case strings.HasPrefix(line, "==> Starting build()"):
		return "Building…", 8, true
	case strings.Contains(line, "npm ci"):
		return "Downloading the web UI's packages…", 12, true
	case strings.Contains(line, "vite build"):
		return "Building the web UI…", 35, true
	case strings.Contains(line, "go build"):
		return "Building the server…", 60, true
	case strings.HasPrefix(line, "==> Starting package()"), strings.HasPrefix(line, "==> Entering fakeroot environment"):
		return "Making the package…", 90, true
	case strings.HasPrefix(line, "==> Finished making"):
		return "Built", 100, true
	}
	return "", 0, false
}

// findPackage returns the Aniko package makepkg left in dir.
func findPackage(dir string) (string, error) {
	matches, _ := filepath.Glob(filepath.Join(dir, "aniko-*.pkg.tar.*"))
	var found string
	var newest time.Time
	for _, m := range matches {
		name := filepath.Base(m)
		if strings.Contains(name, "-debug-") || strings.HasSuffix(name, ".sig") {
			continue
		}
		if st, err := os.Stat(m); err == nil && st.Mode().IsRegular() && (found == "" || st.ModTime().After(newest)) {
			found, newest = m, st.ModTime()
		}
	}
	if found == "" {
		return "", errors.New("the build finished, but made no package")
	}
	return found, nil
}

// install installs the package with pacman, through pkexec (polkit's
// password dialog). When that's not possible, the user gets the command.
func (c *Checker) install(ctx context.Context, pkg string, target Version) error {
	// --ask=4: yes to removing a conflicting package, the kumo one an
	// update from Kumo (Aniko's old name) installed (see the PKGBUILD).
	manual := "sudo pacman -U --noconfirm --ask=4 " + shellQuote(pkg)
	needsTerminal := func(why string) error {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.st.State, c.st.Progress, c.st.ManualCommand = StateReady, 100, manual
		c.st.Message = why + " Run this command in a terminal to install Aniko " + display(target) + ", then restart Aniko."
		c.publishLocked()
		return nil
	}
	pkexec, ok := util.LookPath("pkexec")
	if !ok {
		return needsTerminal("Aniko can't ask for your password (pkexec isn't installed).")
	}
	c.step(StateInstalling, -1, "Installing Aniko "+display(target)+": enter your password…")
	cmd := exec.CommandContext(ctx, pkexec, "pacman", "-U", "--noconfirm", "--ask=4", pkg)
	err := c.run(cmd, func(string) {})
	var exitErr *exec.ExitError
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case err == nil:
		c.mu.Lock()
		defer c.mu.Unlock()
		c.st.State, c.st.Progress = StateReady, 100
		c.st.Message = "Restart Aniko to use it."
		c.publishLocked()
		return nil
	case errors.As(err, &exitErr) && exitErr.ExitCode() == 126:
		// The password dialog was dismissed.
		return context.Canceled
	case errors.As(err, &exitErr) && exitErr.ExitCode() == 127:
		// Not authorized, or no authentication agent to ask with (e.g. Aniko
		// runs as a systemd service, outside the desktop session).
		return needsTerminal("Aniko couldn't get permission to install it.")
	case errors.As(err, &exitErr):
		return &manualError{msg: fmt.Sprintf("pacman couldn't install the update (exit code %d): see its output below.", exitErr.ExitCode()), command: manual}
	}
	return fmt.Errorf("couldn't run pkexec: %w", err)
}

// shellQuote quotes a path for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]|\x1b[()][A-Z0-9]|\x1b[>=]`)

// run runs cmd, feeding each line of its output (stdout and stderr) to the
// log the UI shows and to onLine.
func (c *Checker) run(cmd *exec.Cmd, onLine func(string)) error {
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	cmd.Stdin = strings.NewReader("")
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		sc.Split(scanLines)
		for sc.Scan() {
			line := strings.TrimSpace(ansi.ReplaceAllString(sc.Text(), ""))
			if line == "" {
				continue
			}
			c.output(line)
			onLine(line)
		}
		_, _ = io.Copy(io.Discard, pr) // a line too long: keep the program going
	}()
	err := cmd.Run()
	_ = pw.Close()
	wg.Wait()
	return err
}

// scanLines splits at \n and at \r (progress lines redraw with \r).
func scanLines(data []byte, atEOF bool) (int, []byte, error) {
	for i, b := range data {
		if b == '\n' || b == '\r' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// ---------------------------------------------------------------------------
// Windows: run the new release's installer once Aniko has quit

func (c *Checker) applyWindows(ctx context.Context, g *github, target Version, inst *asset) error {
	if inst == nil {
		return errors.New("the newest release has no installer")
	}
	dir := c.CacheDir
	if err := removeAll(dir); err != nil {
		return fmt.Errorf("couldn't remove the last update's files: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, filepath.Base(inst.Name))
	err := c.download(ctx, g, g.root()+"/releases/assets/"+strconv.FormatInt(inst.ID, 10), "application/octet-stream", path, inst.Size, func(done, total int64) {
		c.progress(StateDownloading, percent(done, total), "Downloading Aniko "+display(target)+"…")
	})
	if err != nil {
		return fmt.Errorf("couldn't download the installer: %w", err)
	}
	if err := verifyDigest(path, inst.Digest); err != nil {
		_ = os.Remove(path)
		return err
	}
	c.step(StateInstalling, -1, "Installing Aniko "+display(target)+": Aniko closes now and opens again when it's done.")
	// The installer starts once Aniko has shut down: it then finds nothing
	// of Aniko's running (it would close it, or kill it after a few
	// seconds). These electron-builder flags install silently where Aniko
	// is installed and start the new version.
	args := []string{"--updated", "/S", "--force-run"}
	exit := lifecycle.Exit{Code: lifecycle.CodeInstalling, Before: func() error {
		if err := c.StartInstaller(path, args...); err != nil {
			c.saveFailure("Aniko couldn't start the installer (" + err.Error() + "). Run it yourself: " + path)
			return fmt.Errorf("starting the installer: %w", err)
		}
		return nil
	}}
	if c.Exits == nil || !c.Exits.Request(exit) {
		return errors.New("Aniko is already shutting down")
	}
	return nil
}

// applyWindowsPortable updates the portable build (unzipped into root):
// the new zip is downloaded and unpacked beside it, and a script started
// once Aniko has shut down moves the new files over the old ones and
// starts Aniko again. Nothing is installed; the data stays in %APPDATA%.
func (c *Checker) applyWindowsPortable(ctx context.Context, g *github, target Version, zipAsset *asset, root string) error {
	if zipAsset == nil {
		return errors.New("the newest release has no portable zip")
	}
	dir := c.CacheDir
	if err := removeAll(dir); err != nil {
		return fmt.Errorf("couldn't remove the last update's files: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, filepath.Base(zipAsset.Name))
	err := c.download(ctx, g, g.root()+"/releases/assets/"+strconv.FormatInt(zipAsset.ID, 10), "application/octet-stream", path, zipAsset.Size, func(done, total int64) {
		c.progress(StateDownloading, percent(done, total), "Downloading Aniko "+display(target)+"…")
	})
	if err != nil {
		return fmt.Errorf("couldn't download the portable zip: %w", err)
	}
	if err := verifyDigest(path, zipAsset.Digest); err != nil {
		_ = os.Remove(path)
		return err
	}
	c.progress(StateDownloading, -1, "Unpacking Aniko "+display(target)+"…")
	fresh := filepath.Join(dir, "new")
	if err := unzipPortable(path, fresh); err != nil {
		return fmt.Errorf("couldn't unpack the portable zip: %w", err)
	}
	if !isFile(filepath.Join(fresh, "Aniko.exe")) {
		return errors.New("the portable zip doesn't hold Aniko.exe")
	}
	_ = os.Remove(path)
	script := filepath.Join(dir, "update.cmd")
	if err := os.WriteFile(script, []byte(portableScript(fresh, root)), 0o700); err != nil {
		return err
	}
	c.step(StateInstalling, -1, "Updating Aniko to "+display(target)+": Aniko closes now and opens again when it's done.")
	// The script runs once Aniko has shut down (it waits for Aniko.exe to
	// be gone anyway), outside Aniko's job object, so it outlives it.
	exit := lifecycle.Exit{Code: lifecycle.CodeInstalling, Before: func() error {
		if err := c.StartInstaller("cmd.exe", "/c", script); err != nil {
			c.saveFailure("Aniko couldn't start the update script (" + err.Error() + "). Run it yourself: " + script)
			return fmt.Errorf("starting the update script: %w", err)
		}
		return nil
	}}
	if c.Exits == nil || !c.Exits.Request(exit) {
		return errors.New("Aniko is already shutting down")
	}
	return nil
}

// portableScript is the batch script that puts the new portable build
// (fresh) in place of the old one (root) once Aniko.exe is gone, and
// starts it. robocopy /MOVE leaves fresh empty; files the old build had
// and the new one hasn't stay (nothing of the user's is in there).
func portableScript(fresh, root string) string {
	return "@echo off\r\n" +
		"rem Aniko's portable update: waits for Aniko to close, puts the new files in place, starts it.\r\n" +
		"set \"NEW=" + fresh + "\"\r\n" +
		"set \"APP=" + root + "\"\r\n" +
		"set /a tries=0\r\n" +
		":wait\r\n" +
		"tasklist /FI \"IMAGENAME eq Aniko.exe\" 2>NUL | find /I \"Aniko.exe\" >NUL\r\n" +
		"if not errorlevel 1 (\r\n" +
		"  set /a tries+=1\r\n" +
		"  if %tries% GEQ 60 goto start\r\n" +
		"  timeout /t 1 /nobreak >NUL\r\n" +
		"  goto wait\r\n" +
		")\r\n" +
		"robocopy \"%NEW%\" \"%APP%\" /E /MOVE /R:10 /W:1 /NFL /NDL /NJH /NJS /NP >NUL\r\n" +
		":start\r\n" +
		"start \"\" \"%APP%\\Aniko.exe\" --updated\r\n"
}

// unzipPortable unpacks the portable build's zip (Aniko.exe and its
// folders at the top) into dir, which must not exist yet. No links; a
// path that would land outside dir fails it.
func unzipPortable(archive, dir string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	if err := os.Mkdir(dir, 0o755); err != nil {
		return err
	}
	var written int64
	const maxSize = 2 << 30 // far more than the app
	for _, f := range zr.File {
		rel, err := safePath(f.Name)
		if err != nil {
			return err
		}
		if rel == "" {
			continue
		}
		target := filepath.Join(dir, rel)
		mode := f.Mode()
		switch {
		case mode.IsDir():
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case mode.IsRegular():
			if written += int64(f.UncompressedSize64); written > maxSize {
				return errors.New("the archive is too big")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			r, err := f.Open()
			if err != nil {
				return err
			}
			err = writeFile(target, r, int64(f.UncompressedSize64), 0o644)
			r.Close()
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("unexpected entry in the archive: %q", f.Name)
		}
	}
	return nil
}

// applyAndroid downloads the new APK: the Android app asks Android to
// install it (Status.InstallFile), which then replaces the app.
func (c *Checker) applyAndroid(ctx context.Context, g *github, target Version, apk *asset) error {
	if apk == nil {
		return errors.New("the newest release has no APK")
	}
	dir := c.CacheDir
	if err := removeAll(dir); err != nil {
		return fmt.Errorf("couldn't remove the last update's files: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, filepath.Base(apk.Name))
	err := c.download(ctx, g, g.root()+"/releases/assets/"+strconv.FormatInt(apk.ID, 10), "application/octet-stream", path, apk.Size, func(done, total int64) {
		c.progress(StateDownloading, percent(done, total), "Downloading Aniko "+display(target)+"…")
	})
	if err != nil {
		return fmt.Errorf("couldn't download the APK: %w", err)
	}
	if err := verifyDigest(path, apk.Digest); err != nil {
		_ = os.Remove(path)
		return err
	}
	c.mu.Lock()
	c.st.State, c.st.Progress, c.st.InstallFile = StateInstalling, -1, path
	c.st.Message = "Installing Aniko " + display(target) + ": confirm on the screen Android shows."
	c.publishLocked()
	c.mu.Unlock()
	return nil
}

// verifyDigest checks a download against GitHub's digest of the asset
// ("sha256:<hex>"). Older assets have none: nothing to check then.
func verifyDigest(path, digest string) error {
	algo, want, ok := strings.Cut(strings.TrimSpace(digest), ":")
	if !ok || want == "" || !strings.EqualFold(algo, "sha256") {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, want) {
		return fmt.Errorf("the download is damaged (its SHA-256 doesn't match GitHub's): try again")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helpers

var errStalled = errors.New("the download stopped receiving data")

// download saves the answer to an API path in file. size is the expected
// size when known (0 otherwise); onProgress hears how far it got.
func (c *Checker) download(ctx context.Context, g *github, path, accept, file string, size int64, onProgress func(done, total int64)) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	// A download that stops getting data fails after a minute instead of
	// hanging.
	stall := time.AfterFunc(stallTimeout, func() { cancel(errStalled) })
	defer stall.Stop()
	resp, err := g.request(ctx, path, accept)
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return cause // stalled, or the caller's context ended
		}
		return err
	}
	defer resp.Body.Close()
	total := resp.ContentLength
	if total <= 0 {
		total = size
	}
	part := file + ".part"
	f, err := os.Create(part)
	if err != nil {
		return err
	}
	var done int64
	buf := make([]byte, 256<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			stall.Reset(stallTimeout)
			if _, err := f.Write(buf[:n]); err != nil {
				f.Close()
				return err
			}
			done += int64(n)
			onProgress(done, total)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			_ = os.Remove(part)
			if cause := context.Cause(ctx); cause != nil {
				return cause
			}
			return cleanError(rerr)
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	if size > 0 && done != size {
		_ = os.Remove(part)
		return fmt.Errorf("the download is incomplete (%s of %s)", megabytes(done), megabytes(size))
	}
	return os.Rename(part, file)
}

// progress updates the state while data comes in; the UI hears of it at
// most every 250 ms.
func (c *Checker) progress(state string, p int, msg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.st.State, c.st.Progress, c.st.Message = state, p, msg
	c.publishSoonLocked()
}

func percent(done, total int64) int {
	if total <= 0 {
		return -1
	}
	return int(min(100, done*100/total))
}

func megabytes(n int64) string {
	return fmt.Sprintf("%.1f MB", float64(n)/1e6)
}

// extract unpacks a .tar.gz of the source into dir, which must not exist
// yet, and returns the folder it's all in (GitHub puts it in
// <owner>-<repo>-<commit>). A path that would land outside dir fails it;
// links and special files are skipped (the source has none), executable
// files stay executable.
func extract(archive, dir string) (string, error) {
	f, err := os.Open(archive)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	if err := os.Mkdir(dir, 0o755); err != nil {
		return "", err
	}
	tr := tar.NewReader(gz)
	tops := map[string]bool{}
	var written int64
	const maxSize = 2 << 30 // far more than the source
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if h.Typeflag == tar.TypeXGlobalHeader || h.Typeflag == tar.TypeXHeader {
			continue // metadata (GitHub's names the commit)
		}
		rel, err := safePath(h.Name)
		if err != nil {
			return "", err
		}
		if rel == "" {
			continue
		}
		target := filepath.Join(dir, rel)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return "", err
			}
		case tar.TypeReg:
			if written += h.Size; written > maxSize {
				return "", errors.New("the archive is too big")
			}
			mode := os.FileMode(0o644)
			if h.Mode&0o111 != 0 {
				mode = 0o755
			}
			if err := writeFile(target, tr, h.Size, mode); err != nil {
				return "", err
			}
		default:
			continue // symlinks, hard links, devices, FIFOs
		}
		top, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
		tops[top] = true
	}
	if len(tops) != 1 {
		return "", fmt.Errorf("unexpected archive layout (%d top-level entries)", len(tops))
	}
	for top := range tops {
		if st, err := os.Stat(filepath.Join(dir, top)); err != nil || !st.IsDir() {
			return "", errors.New("unexpected archive layout (no top-level folder)")
		}
		return filepath.Join(dir, top), nil
	}
	return "", nil
}

// safePath turns an archive entry's name into a path below the folder it's
// unpacked in: absolute paths and ".." are refused.
func safePath(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) || filepath.IsAbs(name) || filepath.VolumeName(name) != "" {
		return "", fmt.Errorf("unsafe path in the archive: %q", name)
	}
	for _, part := range strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return "", fmt.Errorf("unsafe path in the archive: %q", name)
		}
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." {
		return "", nil
	}
	return clean, nil
}

func writeFile(path string, r io.Reader, size int64, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.CopyN(f, r, size); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
