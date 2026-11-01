//go:build android || !(linux || darwin || windows)

package awake

// Elsewhere (Android: a TV plays, it isn't played from): nothing to do.
func newInhibitor() inhibitor { return nil }
