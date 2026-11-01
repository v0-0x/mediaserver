//go:build !darwin

package util

// addToolsDir: only macOS has KumoBox put programs in its own folder.
func addToolsDir(string) {}
