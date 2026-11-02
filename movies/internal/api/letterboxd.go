package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/letterboxd"
)

// The Letterboxd account (see package letterboxd): connecting it by
// username, syncing its diary, importing its data export, exporting what's
// logged here for letterboxd.com/import, the diary itself and the optional
// API sign-in.
//
// Devices on the LAN see the account and the diary, sync, export and send
// what's logged, like they change the list; connecting or disconnecting the
// account, importing files and signing in to the API are done on this
// computer, as the other account and host settings are.

func (s *Server) letterboxdRoutes() {
	m := s.mux
	m.HandleFunc("GET /api/letterboxd/status", h(s.lbStatus))
	m.HandleFunc("POST /api/letterboxd/connect", h(s.lbConnect))
	m.HandleFunc("POST /api/letterboxd/disconnect", h(s.lbDisconnect))
	m.HandleFunc("POST /api/letterboxd/sync", h(s.lbSync))
	m.HandleFunc("POST /api/letterboxd/import", h(s.lbImport))
	m.HandleFunc("GET /api/letterboxd/export.csv", s.lbExport)
	m.HandleFunc("POST /api/letterboxd/mark-exported", h(s.lbMarkExported))
	m.HandleFunc("GET /api/letterboxd/watchlist.csv", s.lbWatchlist)
	m.HandleFunc("GET /api/letterboxd/diary", h(s.lbDiary))
	m.HandleFunc("DELETE /api/letterboxd/diary/{id}", h(s.lbDeleteDiary))
	m.HandleFunc("POST /api/letterboxd/push", h(s.lbPush))
	m.HandleFunc("POST /api/letterboxd/api-login", h(s.lbAPILogin))
	m.HandleFunc("DELETE /api/letterboxd/api-login", h(s.lbAPILogout))
}

// maxImportSize bounds an upload of export files: a whole export is a few
// MB.
const maxImportSize = 50 << 20

// Diary listing limits.
const (
	defaultDiaryLimit = 50
	maxDiaryLimit     = 1000
)

// onThisComputer refuses what only this computer may do.
func onThisComputer(r *http.Request, what string) error {
	if !isTrusted(r) {
		return forbidden(what + " on the computer Moviko runs on")
	}
	return nil
}

// lbMessage is an error of the sync as the UI shows it, without the
// package's prefix: "Letterboxd refused the request…".
func lbMessage(err error) string {
	msg := strings.TrimPrefix(err.Error(), "letterboxd: ")
	if msg == "" {
		return err.Error()
	}
	r, n := utf8.DecodeRuneInString(msg)
	return string(unicode.ToUpper(r)) + msg[n:]
}

// lbError gives the sync's errors an HTTP status and a readable message.
func lbError(err error) error {
	status := http.StatusInternalServerError
	var unreachable *url.Error
	switch {
	case err == nil:
		return nil
	case errors.As(err, &unreachable):
		return &apiError{http.StatusBadGateway, "Couldn't reach Letterboxd: " + unreachable.Err.Error()}
	case errors.Is(err, letterboxd.ErrBadUsername), errors.Is(err, letterboxd.ErrNotExport):
		status = http.StatusBadRequest
	case errors.Is(err, letterboxd.ErrNoSuchMember):
		return notFound("Letterboxd has no member by that username (or their profile is private)")
	case errors.Is(err, letterboxd.ErrNotConnected):
		return &apiError{http.StatusConflict, "Connect a Letterboxd account first"}
	case errors.Is(err, letterboxd.ErrNoAPIAccess):
		status = http.StatusConflict
	case errors.Is(err, letterboxd.ErrAPIUnauthorized):
		status = http.StatusUnauthorized
	}
	return &apiError{status, lbMessage(err)}
}

func (s *Server) lbStatus(r *http.Request) (any, error) {
	return s.app.Letterboxd.Status(r.Context()), nil
}

// lbConnected answers a connection: the status, with the member and what
// the first read of their diary did (SyncError when it failed: the member
// is connected all the same).
type lbConnected struct {
	letterboxd.Status
	Viewer    *catalog.Viewer    `json:"viewer"`
	Result    *letterboxd.Result `json:"result"`
	SyncError string             `json:"syncError,omitempty"`
}

func (s *Server) lbConnect(r *http.Request) (any, error) {
	if err := onThisComputer(r, "Connect Letterboxd"); err != nil {
		return nil, err
	}
	var body struct {
		Username string `json:"username"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	if strings.TrimSpace(body.Username) == "" {
		return nil, badRequest("a Letterboxd username is required")
	}
	v, res, err := s.app.Letterboxd.Connect(r.Context(), body.Username)
	if v == nil {
		return nil, lbError(err)
	}
	out := lbConnected{Status: s.app.Letterboxd.Status(r.Context()), Viewer: v, Result: res}
	if err != nil {
		out.SyncError = lbMessage(err)
	}
	return out, nil
}

func (s *Server) lbDisconnect(r *http.Request) (any, error) {
	if err := onThisComputer(r, "Disconnect Letterboxd"); err != nil {
		return nil, err
	}
	if err := s.app.Letterboxd.Disconnect(); err != nil {
		return nil, err
	}
	return s.app.Letterboxd.Status(r.Context()), nil
}

// lbSync reads the diary now.
func (s *Server) lbSync(r *http.Request) (any, error) {
	res, err := s.app.Letterboxd.Pull(r.Context())
	if err != nil {
		return nil, lbError(err)
	}
	return res, nil
}

// lbImported answers an import: what it did, and the export's files read.
type lbImported struct {
	letterboxd.Result
	Files []string `json:"files"`
}

// lbImport imports a data export: its ZIP, or one or more of its CSV files,
// as a multipart form (any field name).
func (s *Server) lbImport(r *http.Request) (any, error) {
	if err := onThisComputer(r, "Import Letterboxd data"); err != nil {
		return nil, err
	}
	r.Body = http.MaxBytesReader(nil, r.Body, maxImportSize)
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, badRequest("expected the export's files as a multipart form")
	}
	ex := &letterboxd.Export{}
	files := 0
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, uploadError(err)
		}
		name := part.FileName()
		if name == "" {
			part.Close()
			continue // not a file
		}
		files++
		one, err := letterboxd.ReadExportFile(name, part)
		part.Close()
		if err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				return nil, uploadError(err)
			}
			return nil, badRequest(name + ": " + lbMessage(err))
		}
		ex.Merge(one)
	}
	if files == 0 {
		return nil, badRequest("no file was uploaded")
	}
	res, err := s.app.Letterboxd.Import(r.Context(), ex)
	if err != nil {
		return nil, lbError(err)
	}
	out := lbImported{Result: *res, Files: ex.Files}
	if out.Files == nil {
		out.Files = []string{}
	}
	return out, nil
}

// uploadError is the error of a failed upload: too big, or cut short.
func uploadError(err error) error {
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		return &apiError{http.StatusRequestEntityTooLarge, "the files are too big (at most 50 MB at once)"}
	}
	return badRequest("the upload failed: " + err.Error())
}

// lbExport is the CSV of the diary entries logged in Moviko for
// letterboxd.com/import: those not on Letterboxd yet, or all of them with
// all=1. X-Diary-Ids lists the entries it has, to mark them exported once
// imported (mark-exported).
func (s *Server) lbExport(w http.ResponseWriter, r *http.Request) {
	all := r.URL.Query().Get("all")
	var buf bytes.Buffer
	ids, err := s.app.Letterboxd.Export(r.Context(), &buf, all == "1" || all == "true")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	list := make([]string, len(ids))
	for i, id := range ids {
		list[i] = strconv.FormatInt(id, 10)
	}
	w.Header().Set("X-Diary-Ids", strings.Join(list, ","))
	w.Header().Set("X-Diary-Count", strconv.Itoa(len(ids)))
	csvAttachment(w, "moviko-letterboxd-"+time.Now().Format("2006-01-02")+".csv", buf.Bytes())
}

// lbWatchlist is the CSV of the Moviko watchlist, for Letterboxd's
// watchlist importer. X-Film-Count says how many films it has.
func (s *Server) lbWatchlist(w http.ResponseWriter, r *http.Request) {
	var buf bytes.Buffer
	n, err := s.app.Letterboxd.ExportWatchlist(r.Context(), &buf)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	w.Header().Set("X-Film-Count", strconv.Itoa(n))
	csvAttachment(w, "moviko-watchlist-"+time.Now().Format("2006-01-02")+".csv", buf.Bytes())
}

func csvAttachment(w http.ResponseWriter, name string, data []byte) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// lbMarkExported records that the member imported the CSV: the entries ids
// (from the CSV's X-Diary-Ids) are on Letterboxd now, or, without ids, every
// entry still to send. Entries sent already are left as they are.
func (s *Server) lbMarkExported(r *http.Request) (any, error) {
	var body struct {
		IDs []int64 `json:"ids"`
	}
	if err := decodeOptional(r, &body); err != nil {
		return nil, err
	}
	pending, err := s.app.Platform.PendingDiary(r.Context())
	if err != nil {
		return nil, err
	}
	isPending := make(map[int64]bool, len(pending))
	for _, e := range pending {
		isPending[e.ID] = true
	}
	ids := []int64{}
	if body.IDs == nil {
		for _, e := range pending {
			ids = append(ids, e.ID)
		}
	} else {
		seen := map[int64]bool{}
		for _, id := range body.IDs {
			if isPending[id] && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	if err := s.app.Letterboxd.MarkExported(r.Context(), ids); err != nil {
		return nil, err
	}
	return map[string]int{"marked": len(ids)}, nil
}

// decodeOptional decodes a JSON body that may be missing (or empty).
func decodeOptional(r *http.Request, v any) error {
	if r.Body == nil || r.ContentLength == 0 {
		return nil
	}
	if ct := strings.ToLower(r.Header.Get("Content-Type")); !strings.HasPrefix(strings.TrimSpace(ct), "application/json") {
		return badRequest("expected a JSON body (Content-Type: application/json)")
	}
	err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 8<<20)).Decode(v)
	if err != nil && !errors.Is(err, io.EOF) {
		return badRequest("invalid JSON: " + err.Error())
	}
	return nil
}

// lbDiary lists the diary, newest first: of one title with mediaId, at most
// limit entries (50 by default).
func (s *Server) lbDiary(r *http.Request) (any, error) {
	mediaID, limit := queryInt(r, "mediaId"), queryInt(r, "limit")
	if mediaID < 0 {
		return nil, badRequest("invalid mediaId")
	}
	if limit <= 0 {
		limit = defaultDiaryLimit
	}
	return s.app.Platform.Diary(r.Context(), mediaID, min(limit, maxDiaryLimit))
}

func (s *Server) lbDeleteDiary(r *http.Request) (any, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return nil, badRequest("invalid id")
	}
	if err := s.app.Platform.DeleteDiary(r.Context(), id); err != nil {
		return nil, err
	}
	return s.app.Letterboxd.Status(r.Context()), nil
}

// lbPush sends the entries still to send with the API.
func (s *Server) lbPush(r *http.Request) (any, error) {
	res, err := s.app.Letterboxd.Push(r.Context())
	if err != nil {
		return nil, lbError(err)
	}
	return res, nil
}

// lbSignedIn answers an API sign-in: the status, with the member.
type lbSignedIn struct {
	letterboxd.Status
	Member *letterboxd.Member `json:"member"`
}

func (s *Server) lbAPILogin(r *http.Request) (any, error) {
	if err := onThisComputer(r, "Sign in to the Letterboxd API"); err != nil {
		return nil, err
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decode(r, &body); err != nil {
		return nil, err
	}
	if strings.TrimSpace(body.Username) == "" || body.Password == "" {
		return nil, badRequest("the username and password are required")
	}
	if !s.app.Letterboxd.Status(r.Context()).APIAvailable {
		return nil, &apiError{http.StatusConflict, "Add the API client ID and secret Letterboxd gave you, and save, first"}
	}
	m, err := s.app.Letterboxd.APILogin(r.Context(), strings.TrimSpace(body.Username), body.Password)
	if errors.Is(err, letterboxd.ErrAPIUnauthorized) {
		return nil, &apiError{http.StatusUnauthorized, "Letterboxd refused the sign-in: check the username and password"}
	}
	if err != nil {
		return nil, lbError(err)
	}
	return lbSignedIn{Status: s.app.Letterboxd.Status(r.Context()), Member: m}, nil
}

func (s *Server) lbAPILogout(r *http.Request) (any, error) {
	if err := onThisComputer(r, "Sign out of the Letterboxd API"); err != nil {
		return nil, err
	}
	s.app.Letterboxd.APILogout()
	return s.app.Letterboxd.Status(r.Context()), nil
}
