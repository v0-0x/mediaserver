package share

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"time"
)

// Uploads: a Aniko that takes this one's downloads (it shares its library
// with it and lets it download there: a server, say) takes the files and
// folders sent from it too, into its library (package upload). This one
// only passes them on, a piece at a time, signed like any request to it.

// UploadHost is a Aniko this one may upload to.
type UploadHost struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Online bool   `json:"online"`
}

// UploadHosts are the Anikoes this one may upload to, by name.
func (s *Service) UploadHosts() []UploadHost {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []UploadHost{}
	for _, p := range s.peers {
		if p.shares && p.hostDownloads {
			out = append(out, UploadHost{ID: p.id, Name: s.view(p, now).Name, Online: p.online(now)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// AskUpload passes a request about an upload on to the Aniko id (GET: its
// folders, or where a file stands; POST: a piece of a file), and returns
// its answer as it is: the status and the body.
func (s *Service) AskUpload(ctx context.Context, id, method, query string, piece []byte) (int, []byte, error) {
	p := s.host(id)
	if p == nil {
		return 0, nil, errNotShared
	}
	s.mu.Lock()
	takes := p.hostDownloads
	name := s.view(p, time.Now()).Name
	s.mu.Unlock()
	if !takes {
		raw, _ := json.Marshal(map[string]string{"error": name + " doesn't take files from this device: turn on Let it download here for it there"})
		return http.StatusForbidden, raw, nil
	}
	var body io.Reader
	if method == http.MethodPost {
		body = bytes.NewReader(piece)
	}
	req, err := s.request(ctx, p, method, "/api/peer/upload?"+query, body)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	// A piece takes as long as the network needs: no time limit but the
	// caller's.
	resp, err := s.do(s.media, req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, raw, err
}
