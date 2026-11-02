//go:build !darwin

package util

// addToolsDir: only macOS has Moviko put programs in its own folder.
func addToolsDir(string) {}
