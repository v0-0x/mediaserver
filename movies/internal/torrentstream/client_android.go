//go:build android

package torrentstream

import "log/slog"

// No BitTorrent client on Android: its build doesn't link there yet. The TV
// downloads torrents (onto its computer) instead.
func newClient(string, *slog.Logger) (client, error) { return nil, ErrUnavailable }
