//go:build !android

package torrentstream

import (
	"context"
	"log/slog"
	"path/filepath"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

// anacrolix/torrent: the client, a torrent of it and a file of that.

type anaClient struct{ cl *torrent.Client }

func newClient(dir string, logger *slog.Logger) (client, error) {
	cfg := torrent.NewDefaultClientConfig()
	cfg.Slogger = logger
	// Each torrent in its own folder (by its info hash), dropped with it;
	// what's complete is known in memory only (the cache doesn't outlive
	// a run).
	cfg.DefaultStorage = storage.NewFileOpts(storage.NewFileClientOpts{
		ClientBaseDir: dir,
		TorrentDirMaker: func(base string, _ *metainfo.Info, h metainfo.Hash) string {
			return filepath.Join(base, h.HexString())
		},
		PieceCompletion: storage.NewMapPieceCompletion(),
	})
	// Any free port: another client on this computer (qBittorrent) keeps
	// its own. It uploads while it downloads (peers give more to those
	// that do), but doesn't stay to seed: a torrent stops with its play.
	cfg.ListenPort = 0
	cfg.Seed = false
	cl, err := torrent.NewClient(cfg)
	if err != nil {
		// A computer without IPv6 (turned off, as on some Raspberry Pis):
		// IPv4 only.
		cfg.DisableIPv6 = true
		var err4 error
		if cl, err4 = torrent.NewClient(cfg); err4 != nil {
			return nil, err
		}
	}
	return &anaClient{cl}, nil
}

func (c *anaClient) addMagnet(magnet string) (torrentHandle, error) {
	t, err := c.cl.AddMagnet(magnet)
	if err != nil {
		return nil, err
	}
	return &anaTorrent{t}, nil
}

func (c *anaClient) close() { c.cl.Close() }

type anaTorrent struct{ t *torrent.Torrent }

func (t *anaTorrent) gotInfo() <-chan struct{} { return t.t.GotInfo() }
func (t *anaTorrent) infoHash() string         { return t.t.InfoHash().HexString() }
func (t *anaTorrent) name() string             { return t.t.Name() }
func (t *anaTorrent) drop()                    { t.t.Drop() }

func (t *anaTorrent) peers() (int, int) {
	st := t.t.Stats()
	return st.ActivePeers, st.ConnectedSeeders
}

func (t *anaTorrent) files() []fileHandle {
	var out []fileHandle
	for _, f := range t.t.Files() {
		out = append(out, &anaFile{f})
	}
	return out
}

type anaFile struct{ f *torrent.File }

func (f *anaFile) path() string          { return f.f.DisplayPath() }
func (f *anaFile) length() int64         { return f.f.Length() }
func (f *anaFile) bytesCompleted() int64 { return f.f.BytesCompleted() }
func (f *anaFile) download()             { f.f.Download() }

func (f *anaFile) newReader(ctx context.Context) readSeekCloser {
	r := f.f.NewReader()
	r.SetContext(ctx)
	// The pieces where it's read first, and a few seconds ahead of it.
	r.SetResponsive()
	r.SetReadahead(readahead)
	return r
}
