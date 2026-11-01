package freeflix

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/v0-0x/mediaserver/movies/internal/providers"
)

// Xpass's embed page names a data link; what it answers is AES-256-GCM
// encrypted with a key worked out from the player's build, the link's path
// and its token. Decrypted, it lists playlists ({"name", "url"}), each with
// the streams' files.
var xpassSite = &Site{ID: "xpass", Name: "Xpass", DefaultURL: "https://play.xpass.top", streams: xpassStreams}

// XpassKeyPrefix starts the key of Xpass's data: it names the player's
// build, so it changes when Xpass ships a new one (FreeFlix's, 2026-10).
var XpassKeyPrefix = "spv3-data-response|spv3-build-1787821613-50e5fc97c9dce367|"

var (
	reXpassData  = regexp.MustCompile(`var dataUrl="([^"]+)"`)
	reXpassToken = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func xpassStreams(p *Provider, ctx context.Context, r providers.Request) ([]providers.Stream, error) {
	base := p.base()
	embed := fmt.Sprintf("%s/e/movie/%d", base, r.TMDBID)
	if r.IsTV() {
		embed = fmt.Sprintf("%s/e/tv/%d/%d/%d", base, r.TMDBID, r.Season, r.Episode)
	}
	page, err := p.fetch(ctx, http.MethodGet, embed, map[string]string{"Referer": base + "/"}, nil)
	if err != nil {
		return nil, err
	}
	m := reXpassData.FindSubmatch(page)
	if m == nil {
		return nil, nil // not on Xpass
	}
	dataURL := absolute(base, string(m[1]))
	body, err := p.fetch(ctx, http.MethodGet, dataURL, map[string]string{"Referer": embed}, nil)
	if err != nil {
		return nil, err
	}
	du, err := url.Parse(dataURL)
	if err != nil {
		return nil, err
	}
	var lists []xpassList
	if err := xpassDecrypt(body, du.EscapedPath(), du.Query().Get("token"), &lists); err != nil {
		return nil, err
	}
	headers := map[string]string{"Referer": base + "/"}
	return each(ctx, lists, func(ctx context.Context, l xpassList) ([]providers.Stream, error) {
		if l.URL == "" {
			return nil, nil
		}
		var v struct {
			Playlist []struct {
				Sources []struct {
					File  string `json:"file"`
					Label string `json:"label"`
					Type  string `json:"type"`
				} `json:"sources"`
			} `json:"playlist"`
		}
		if err := p.fetchJSON(ctx, http.MethodGet, absolute(base, l.URL), headers, nil, &v); err != nil {
			return nil, err
		}
		name := "Xpass"
		if n := strings.TrimSpace(l.Name); n != "" {
			name += " · " + n
		}
		var out []providers.Stream
		for _, item := range v.Playlist {
			for _, s := range item.Sources {
				if !isWebLink(s.File) {
					continue
				}
				title := name
				if lb := strings.TrimSpace(s.Label); lb != "" {
					title += " · " + lb
				}
				out = append(out, providers.Stream{
					Title: title, URL: s.File, Type: streamType(s.File, s.Type), Quality: providers.QualityFromText(s.Label),
					Headers: map[string]string{"Referer": base + "/"},
				})
			}
		}
		return out, nil
	})
}

// xpassList is a playlist of the decrypted data.
type xpassList struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// xpassDecrypt opens the data at path with token (base64url: the IV, the
// ciphertext and the tag) into out.
func xpassDecrypt(body []byte, path, token string, out any) error {
	_, sig, ok := strings.Cut(token, ".")
	if !ok || strings.Count(token, ".") != 1 || !reXpassToken.MatchString(sig) {
		return errors.New("data link without a token")
	}
	key := sha256.Sum256([]byte(XpassKeyPrefix + path + "|" + token))
	s := strings.TrimRight(strings.TrimSpace(string(body)), "=")
	s = strings.NewReplacer("+", "-", "/", "_").Replace(s)
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(raw) <= 12+16 {
		return errors.New("data isn't encrypted as expected")
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	plain, err := gcm.Open(nil, raw[:12], raw[12:], nil)
	if err != nil {
		return errors.New("data doesn't decrypt: Xpass may have a new player build")
	}
	if err := json.Unmarshal(plain, out); err != nil {
		return errors.New("decrypted data isn't JSON")
	}
	return nil
}
