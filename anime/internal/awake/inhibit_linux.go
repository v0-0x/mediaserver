//go:build linux && !android

package awake

// systemd-inhibit holds logind's sleep lock while its program runs: the
// system doesn't suspend, by itself or from the desktop's idle timer; the
// screen may still turn off. Its program (cat) reads from Aniko: it ends when
// Aniko does, also when Aniko crashes, and the lock with it.
func newInhibitor() inhibitor {
	return &cmdInhibitor{args: []string{"systemd-inhibit", "--what=sleep", "--who=Aniko", "--why=A device is playing this computer's anime", "--mode=block", "cat"}, stdin: true}
}
