// Package util contains small helpers shared across packages.
package util

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/adrg/strutil"
	"github.com/adrg/strutil/metrics"
)

const UserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36"

// HTTP is the shared client for public services (TMDB, Letterboxd,
// providers, torrent sites, extension downloads). It refuses to connect to this
// computer or the LAN, so URLs coming from extensions or LAN clients can't
// be used to reach local services.
var HTTP = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		Proxy:                 PublicProxy,
		DialContext:           PublicDialContext(15 * time.Second),
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	},
}

// GetJSON performs a GET request and decodes the JSON response.
func GetJSON(ctx context.Context, url string, headers map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("GET %s: %s: %s", url, resp.Status, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// GetBytes performs a GET request and returns the body.
func GetBytes(ctx context.Context, url string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

// PostJSON sends a JSON body and decodes the JSON response.
func PostJSON(ctx context.Context, url string, headers map[string]string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ---------------------------------------------------------------------------
// String matching

var (
	reBrackets   = regexp.MustCompile(`[\[\(\{【].*?[\]\)\}】]`)
	reNonAlnum   = regexp.MustCompile(`[^\p{L}\p{N}]+`)
	reSeasonWord = regexp.MustCompile(`(?i)\b(season|s)\s*0*(\d+)\b`)
	reOrdinal    = regexp.MustCompile(`(?i)\b(\d+)(st|nd|rd|th)\s+season\b`)
)

// NormalizeTitle lowercases, strips brackets/punctuation and collapses
// whitespace so titles can be compared.
func NormalizeTitle(s string) string {
	s = strings.ToLower(s)
	s = reBrackets.ReplaceAllString(s, " ")
	s = strings.NewReplacer("&", " and ", "×", "x", "½", "1/2", "'", "", "’", "", "`", "").Replace(s)
	s = reOrdinal.ReplaceAllString(s, "season $1")
	s = reSeasonWord.ReplaceAllString(s, "season $2")
	s = reNonAlnum.ReplaceAllString(s, " ")
	s = removeDiacritics(s)
	return strings.Join(strings.Fields(s), " ")
}

func removeDiacritics(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case 'ā', 'á', 'à', 'â', 'ä':
			r = 'a'
		case 'ē', 'é', 'è', 'ê', 'ë':
			r = 'e'
		case 'ī', 'í', 'ì', 'î', 'ï':
			r = 'i'
		case 'ō', 'ó', 'ò', 'ô', 'ö':
			r = 'o'
		case 'ū', 'ú', 'ù', 'û', 'ü':
			r = 'u'
		}
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

var (
	dice    = metrics.NewSorensenDice()
	jw      = metrics.NewJaroWinkler()
	leven   = metrics.NewLevenshtein()
	ngramSz = 2
)

func init() {
	dice.NgramSize = ngramSz
	dice.CaseSensitive = false
	jw.CaseSensitive = false
	leven.CaseSensitive = false
}

// Similarity returns a 0-1 similarity score between two titles, combining a
// few metrics so both short and long titles compare sensibly.
func Similarity(a, b string) float64 {
	a, b = NormalizeTitle(a), NormalizeTitle(b)
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	d := strutil.Similarity(a, b, dice)
	j := strutil.Similarity(a, b, jw)
	l := strutil.Similarity(a, b, leven)
	score := d*0.5 + j*0.25 + l*0.25
	// Containment bonus: "one piece" vs "one piece film red" etc.
	if strings.HasPrefix(b, a+" ") || strings.HasPrefix(a, b+" ") {
		score = max(score, 0.75)
	}
	return score
}

// ---------------------------------------------------------------------------
// Misc

// LookPath resolves a program name or absolute path to an executable.
func LookPath(p string) (string, bool) {
	if p == "" {
		return "", false
	}
	if strings.ContainsRune(p, os.PathSeparator) || strings.ContainsRune(p, '/') {
		return lookAbs(p)
	}
	return lookBare(p)
}

// SanitizeFilename makes s a file or folder name valid on Windows, macOS
// and Linux (and on NTFS/exFAT drives): characters they refuse become
// "_" (a colon a look-alike, so titles like "Star Trek: Picard" stay
// readable; question marks go), no trailing dots or spaces (Windows drops
// them), no reserved device name (CON, NUL, COM1…), at most 180 bytes of
// valid UTF-8.
func SanitizeFilename(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == ':':
			return '：'
		case r == '?':
			return -1 // "Who Framed Roger Rabbit?"
		case r == '/' || r == '\\' || r == '*' || r == '"' || r == '<' || r == '>' || r == '|':
			return '_'
		case r < 0x20 || r == 0x7f:
			return '_'
		}
		return r
	}, strings.ToValidUTF8(s, ""))
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 180 {
		s = strings.ToValidUTF8(s[:180], "")
	}
	s = strings.Trim(s, ". ")
	if s == "" {
		return "untitled"
	}
	base, _, _ := strings.Cut(s, ".")
	switch strings.ToUpper(strings.TrimSpace(base)) {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		s = "_" + s
	}
	// Windows' device names (CON, NUL, COM1…), also with an extension,
	// aren't files there.
	stem, _, _ := strings.Cut(s, ".")
	if windowsDevices[strings.ToUpper(strings.TrimSpace(stem))] {
		s = "_" + s
	}
	return s
}

var windowsDevices = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// IsSubPath reports whether child is inside (or equal to) parent.
func IsSubPath(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

// FirstNonEmpty returns the first non empty string.
func FirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// Detach starts a command that outlives the request that launched it, on
// its own: Ctrl+C on Moviko's terminal doesn't also close it.
func Detach(name string, args ...string) error {
	return start(exec.Command(name, args...))
}

// Open opens a folder in the file manager, or a web address in the browser.
func Open(target string) error {
	return start(openCommand(target))
}

func start(cmd *exec.Cmd) error {
	cmd.Stdout = nil
	cmd.Stderr = nil
	detachAttrs(cmd)
	started, err := startDetached(cmd)
	if err != nil {
		return err
	}
	go func() { _ = started.Wait() }()
	return nil
}

func isWebAddress(s string) bool {
	return strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")
}
