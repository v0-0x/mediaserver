package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/v0-0x/mediaserver/anime/internal/anilist"
	"github.com/v0-0x/mediaserver/anime/internal/config"
	"github.com/v0-0x/mediaserver/anime/internal/manga"
	fileup "github.com/v0-0x/mediaserver/anime/internal/upload"
)

// MangaChanged is the event the page hears when a manga download moves.
const MangaChanged = "manga-downloads"

// MangaDir is where downloaded manga go: the folder chosen in Settings, or
// ~/Manga on a computer, the data folder's manga folder on a TV or a
// server.
func (a *App) MangaDir() string {
	if d := strings.TrimSpace(a.Settings.Get().Manga.Dir); d != "" {
		return config.ExpandHome(d)
	}
	if runtime.GOOS == "android" || a.HubStore != nil {
		return filepath.Join(a.DataDir, "manga")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, "Manga")
	}
	return filepath.Join(a.DataDir, "manga")
}

// wireManga sets up the downloaded manga and their downloads.
func (a *App) wireManga() {
	a.MangaLib = &manga.Library{Dir: a.MangaDir}
	media := func(ctx context.Context, id int) (*anilist.Media, error) { return a.Platform.Media(ctx, id, false) }
	a.MangaDL = manga.NewDownloader(a.Manga, a.MangaLib, media, a.SendManga, filepath.Join(a.DataDir, "manga-sending"))
	a.MangaDL.OnChange = func() { a.Hub.Publish(MangaChanged, map[string]any{}) }
}

// SendManga sends a downloaded chapter's files to a media server's manga
// folder (the Aniko target), a piece at a time, as uploads go.
func (a *App) SendManga(ctx context.Context, target, folder string, files []string, progress func(int)) error {
	// The server says whether it takes manga (an older one doesn't).
	status, raw, err := a.Share.AskUpload(ctx, target, http.MethodGet, "", nil)
	if err != nil {
		return fmt.Errorf("the server doesn't answer: %w", err)
	}
	var folders struct {
		Manga bool   `json:"manga"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal(raw, &folders)
	if status != http.StatusOK {
		return errors.New(firstNonEmpty(folders.Error, http.StatusText(status)))
	}
	if !folders.Manga {
		return errors.New("the server needs an update to take manga")
	}
	var total, sent int64
	for _, f := range files {
		if st, err := os.Stat(f); err == nil {
			total += st.Size()
		}
	}
	for _, f := range files {
		n, err := a.sendFile(ctx, target, folder+"/"+filepath.Base(f), f, func(done int64) {
			if total > 0 {
				progress(int((sent + done) * 100 / total))
			}
		})
		if err != nil {
			return err
		}
		sent += n
	}
	return nil
}

// sendFile sends one file into the server's manga folder as rel, from
// where the server has it.
func (a *App) sendFile(ctx context.Context, target, rel, path string, progress func(int64)) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	total := st.Size()
	query := func(offset int64) string {
		q := url.Values{}
		q.Set("folder", "manga")
		q.Set("path", rel)
		q.Set("total", strconv.FormatInt(total, 10))
		q.Set("offset", strconv.FormatInt(offset, 10))
		return q.Encode()
	}
	type answer struct {
		Have  int64  `json:"have"`
		Done  bool   `json:"done"`
		Error string `json:"error"`
	}
	ask := func(method string, offset int64, piece []byte) (int, answer, error) {
		status, raw, err := a.Share.AskUpload(ctx, target, method, query(offset), piece)
		var ans answer
		_ = json.Unmarshal(raw, &ans)
		return status, ans, err
	}
	status, ans, err := ask(http.MethodGet, 0, nil)
	if err != nil {
		return 0, err
	}
	switch {
	case status == http.StatusConflict:
		// Already there (the series' details and cover, sent with its
		// first chapter; or this chapter, from another device): kept.
		return total, nil
	case status != http.StatusOK:
		return 0, errors.New(firstNonEmpty(ans.Error, http.StatusText(status)))
	case ans.Done:
		return total, nil
	}
	offset := ans.Have
	buf := make([]byte, fileup.Chunk)
	for offset < total {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return 0, err
		}
		n, err := io.ReadFull(f, buf[:min(int64(len(buf)), total-offset)])
		if err != nil {
			return 0, err
		}
		status, ans, err := ask(http.MethodPost, offset, buf[:n])
		if err != nil {
			return 0, fmt.Errorf("the server doesn't answer: %w", err)
		}
		switch {
		case status == http.StatusOK && ans.Done:
			progress(total)
			return total, nil
		case status == http.StatusOK || status == http.StatusConflict && ans.Have > 0:
			offset = ans.Have
		default:
			return 0, errors.New(firstNonEmpty(ans.Error, http.StatusText(status)))
		}
		progress(offset)
	}
	return total, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
