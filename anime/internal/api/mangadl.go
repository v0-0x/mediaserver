package api

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/v0-0x/mediaserver/anime/internal/extensions"
	"github.com/v0-0x/mediaserver/anime/internal/manga"
)

// LocalProvider reads a downloaded chapter (its file in the manga folder,
// as the chapter's id) from the manga folder.
const LocalProvider = "local"

// Downloaded manga (manga.Library) and their downloads (manga.Downloader).
func (s *Server) mangaDownloadRoutes() {
	m := s.mux
	m.HandleFunc("GET /api/manga-downloads", h(func(r *http.Request) (any, error) { return s.app.MangaDL.List(), nil }))
	m.HandleFunc("POST /api/manga-downloads", h(func(r *http.Request) (any, error) {
		var body struct {
			MediaID  int                `json:"mediaId"`
			Title    string             `json:"title"`
			Provider string             `json:"provider"`
			Chapters []manga.ChapterRef `json:"chapters"`
			// Target: "" this device, else the media server's id.
			Target string `json:"target"`
		}
		if err := decode(r, &body); err != nil {
			return nil, err
		}
		if body.Provider == LocalProvider {
			return nil, badRequest("these chapters are downloaded already")
		}
		name := ""
		if body.Target != "" {
			for _, h := range s.app.Share.UploadHosts() {
				if h.ID == body.Target {
					name = h.Name
				}
			}
			if name == "" {
				return nil, badRequest("that server doesn't take files from this device")
			}
		}
		n, err := s.app.MangaDL.Add(body.MediaID, body.Title, body.Provider, body.Chapters, body.Target, name)
		if err != nil {
			return nil, badRequest(err.Error())
		}
		return map[string]int{"added": n}, nil
	}))
	m.HandleFunc("DELETE /api/manga-downloads/{id}", h(func(r *http.Request) (any, error) {
		if !s.app.MangaDL.Remove(r.PathValue("id")) {
			return nil, notFound("no such download")
		}
		return nil, nil
	}))
	m.HandleFunc("POST /api/manga-downloads/{id}/retry", h(func(r *http.Request) (any, error) {
		if !s.app.MangaDL.Retry(r.PathValue("id")) {
			return nil, notFound("no such failed download")
		}
		return nil, nil
	}))
	m.HandleFunc("POST /api/manga-downloads/clear", h(func(r *http.Request) (any, error) {
		s.app.MangaDL.Clear()
		return nil, nil
	}))

	m.HandleFunc("GET /api/manga-library", h(func(r *http.Request) (any, error) {
		list, err := s.app.MangaLib.List()
		if err != nil {
			return nil, err
		}
		return map[string]any{"dir": s.app.MangaDir(), "series": list}, nil
	}))
	m.HandleFunc("GET /api/manga-library/{id}", h(func(r *http.Request) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		series, chapters, err := s.app.MangaLib.Series(id)
		if errors.Is(err, manga.ErrNotFound) {
			// Nothing downloaded of it: an empty list, not an error.
			return map[string]any{"series": nil, "chapters": []manga.LocalChapter{}}, nil
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{"series": series, "chapters": chapters}, nil
	}))
	m.HandleFunc("DELETE /api/manga-library/{id}", h(trusted(func(r *http.Request) (any, error) {
		id, err := pathID(r, "id")
		if err != nil {
			return nil, err
		}
		if err := s.app.MangaLib.Delete(id, r.URL.Query().Get("file")); err != nil {
			if errors.Is(err, manga.ErrNotFound) {
				return nil, notFound(err.Error())
			}
			return nil, err
		}
		s.app.Hub.Publish("manga-downloads", map[string]any{})
		return nil, nil
	})))
	m.HandleFunc("GET /api/manga-library/{id}/cover", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		p, err := s.app.MangaLib.Cover(id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "private, max-age=600")
		http.ServeFile(w, r, p)
	})
	m.HandleFunc("GET /api/manga-library/page", func(w http.ResponseWriter, r *http.Request) {
		n, err := strconv.Atoi(r.URL.Query().Get("n"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		rc, ct, err := s.app.MangaLib.Page(r.URL.Query().Get("file"), n)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer rc.Close()
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Cache-Control", "private, max-age=86400")
		_, _ = io.Copy(w, rc)
	})
}

// localPages lists a downloaded chapter's pages, for the reader.
func (s *Server) localPages(file string) ([]extensions.MangaPage, error) {
	n, err := s.app.MangaLib.Pages(file)
	if err != nil {
		return nil, notFound("this chapter isn't in the manga folder any more")
	}
	out := make([]extensions.MangaPage, n)
	for i := range out {
		q := url.Values{"file": {file}, "n": {strconv.Itoa(i)}}
		out[i] = extensions.MangaPage{URL: "/api/manga-library/page?" + q.Encode(), Index: float64(i), Headers: map[string]string{}}
	}
	return out, nil
}
