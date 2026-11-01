//go:build !darwin

package util

// addToolsDir: only macOS has Aniko put programs in its own folder.
func addToolsDir(string) {}
