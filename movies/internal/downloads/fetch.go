package downloads

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/v0-0x/mediaserver/movies/internal/stream"
	"github.com/v0-0x/mediaserver/movies/internal/util"
)

// Fetching what providers link to
//
// A provider's link may name any address, this computer and the LAN
// included, and HLS playlists name more. yt-dlp and ffmpeg would fetch
// them all, wherever they are. So downloads fetch like the in-app
// player's streams do (package stream): video files in Go with the
// stream proxy's guarded client, HLS streams with ffmpeg reading them
// through the stream relay, which rewrites their playlists; only web
// pages go to yt-dlp, once their address is known to be on the internet.

const (
	// stallTimeout: a download that receives nothing for this long is
	// reconnected.
	stallTimeout = time.Minute
	// maxReconnects is how many times in a row a broken download is picked
	// up again (the count starts over once data comes in).
	maxReconnects = 5
)

// reconnectDelay is how long to wait before reconnecting the nth time
// (replaced in tests).
var reconnectDelay = func(n int) time.Duration { return time.Duration(n) * 2 * time.Second }

// requestHeaders are the headers a resolved link must be fetched with.
func requestHeaders(res *Resolved) map[string]string {
	h := map[string]string{}
	for k, v := range res.Headers {
		h[k] = v
	}
	if res.Referrer != "" {
		for k := range h {
			if strings.EqualFold(k, "referer") {
				delete(h, k)
			}
		}
		h["Referer"] = res.Referrer
	}
	return h
}

// attempt is one request for a file, which is canceled when it stalls.
type attempt struct {
	resp   *http.Response
	body   io.Reader
	cancel context.CancelFunc
	timer  *time.Timer
}

// open requests link (from byte from on, with the validator ifRange).
func open(ctx context.Context, link string, headers map[string]string, from int64, ifRange string) (*attempt, error) {
	actx, cancel := context.WithCancel(ctx)
	h := headers
	rng := ""
	if from > 0 {
		h = make(map[string]string, len(headers)+1)
		for k, v := range headers {
			h[k] = v
		}
		if ifRange != "" {
			h["If-Range"] = ifRange
		}
		rng = fmt.Sprintf("bytes=%d-", from)
	}
	resp, err := stream.Fetch(actx, link, h, rng)
	if err != nil {
		cancel()
		return nil, err
	}
	a := &attempt{resp: resp, cancel: cancel, timer: time.AfterFunc(stallTimeout, cancel)}
	a.body = &stallReader{r: resp.Body, timer: a.timer}
	return a, nil
}

func (a *attempt) close() {
	a.timer.Stop()
	a.cancel()
	_ = a.resp.Body.Close()
}

// stallReader cancels its attempt when nothing comes for stallTimeout.
type stallReader struct {
	r     io.Reader
	timer *time.Timer
}

func (s *stallReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if n > 0 {
		s.timer.Reset(stallTimeout)
	}
	return n, err
}

// file saves the video file a provider's link answered with (a, whose body
// may have been peeked at) into out. When the connection breaks, it picks
// up where it stopped, with a byte range from there (and If-Range, so that
// a file changed meanwhile starts over).
func (m *Manager) file(r *run, res *Resolved, headers map[string]string, a *attempt, out string) error {
	f, err := os.Create(out)
	if err != nil {
		a.close()
		return err
	}
	defer f.Close()
	validator := a.resp.Header.Get("ETag")
	if strings.HasPrefix(validator, "W/") {
		validator = "" // If-Range takes strong ones only
	}
	if validator == "" {
		validator = a.resp.Header.Get("Last-Modified")
	}
	total := a.resp.ContentLength
	meter := &meter{report: m.progress(r), total: total}
	var written int64
	tries := 0
	for {
		n, err := io.Copy(f, &meterReader{r: a.body, m: meter})
		a.close()
		written += n
		if err == nil && (total < 0 || written >= total) {
			return f.Close()
		}
		if r.ctx.Err() != nil {
			return r.ctx.Err()
		}
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		if n > 0 {
			tries = 0
		}
		// Reconnect, from where it stopped.
		for {
			tries++
			if tries > maxReconnects {
				return fmt.Errorf("the download broke off: %w", err)
			}
			select {
			case <-r.ctx.Done():
				return r.ctx.Err()
			case <-time.After(reconnectDelay(tries)):
			}
			a, err = open(r.ctx, res.URL, headers, written, validator)
			if err != nil {
				continue
			}
			if a.resp.StatusCode == http.StatusPartialContent && rangeStart(a.resp) == written {
				if t := rangeTotal(a.resp); t > 0 {
					total = t
				}
				break
			}
			if a.resp.StatusCode == http.StatusOK {
				// No byte ranges here, or the file changed: from the start.
				if err = f.Truncate(0); err == nil {
					_, err = f.Seek(0, io.SeekStart)
				}
				if err != nil {
					a.close()
					return err
				}
				written, total = 0, a.resp.ContentLength
				meter.restart(total)
				break
			}
			status := a.resp.StatusCode
			err = fmt.Errorf("the provider's link answered %s", a.resp.Status)
			a.close()
			if status < 500 && status != http.StatusTooManyRequests && status != http.StatusRequestTimeout {
				return err
			}
		}
	}
}

// rangeStart is where a 206 answer's byte range starts (-1 when unknown).
func rangeStart(resp *http.Response) int64 {
	cr, ok := strings.CutPrefix(resp.Header.Get("Content-Range"), "bytes ")
	if !ok {
		return -1
	}
	start, _, _ := strings.Cut(cr, "-")
	n, err := strconv.ParseInt(strings.TrimSpace(start), 10, 64)
	if err != nil {
		return -1
	}
	return n
}

// rangeTotal is the whole file's size a 206 answer gives (-1 when unknown).
func rangeTotal(resp *http.Response) int64 {
	cr := resp.Header.Get("Content-Range")
	i := strings.LastIndexByte(cr, '/')
	if i < 0 {
		return -1
	}
	n, err := strconv.ParseInt(strings.TrimSpace(cr[i+1:]), 10, 64)
	if err != nil {
		return -1
	}
	return n
}

// meter reports a file download's progress, speed and time left.
type meter struct {
	report func(fn func(it *Item))
	total  int64 // -1 when unknown
	done   int64
	mark   time.Time
	marked int64
	rate   float64 // bytes per second
}

func (mt *meter) restart(total int64) {
	mt.total, mt.done, mt.marked, mt.mark = total, 0, 0, time.Now()
}

func (mt *meter) add(n int) {
	now := time.Now()
	if mt.mark.IsZero() {
		mt.mark = now
	}
	mt.done += int64(n)
	dt := now.Sub(mt.mark).Seconds()
	if dt < 0.5 {
		return
	}
	got := float64(mt.done-mt.marked) / dt
	if mt.rate == 0 {
		mt.rate = got
	} else {
		mt.rate = 0.7*mt.rate + 0.3*got
	}
	mt.mark, mt.marked = now, mt.done
	progress, speed, eta := 0.0, formatRate(mt.rate), ""
	if mt.total > 0 {
		progress = min(float64(mt.done)/float64(mt.total), 0.99)
		if mt.rate > 0 {
			eta = formatETA(float64(mt.total-mt.done) / mt.rate)
		}
	}
	mt.report(func(it *Item) { it.Progress, it.Speed, it.ETA = progress, speed, eta })
}

type meterReader struct {
	r io.Reader
	m *meter
}

func (mr *meterReader) Read(p []byte) (int, error) {
	n, err := mr.r.Read(p)
	if n > 0 {
		mr.m.add(n)
	}
	return n, err
}

// formatRate writes a speed the way yt-dlp does ("1.50MiB/s").
func formatRate(bps float64) string {
	switch {
	case bps >= 1<<30:
		return fmt.Sprintf("%.2fGiB/s", bps/(1<<30))
	case bps >= 1<<20:
		return fmt.Sprintf("%.2fMiB/s", bps/(1<<20))
	default:
		return fmt.Sprintf("%.2fKiB/s", bps/(1<<10))
	}
}

// formatETA writes a time left as mm:ss, or h:mm:ss.
func formatETA(sec float64) string {
	s := int(sec + 0.5)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

// lookupIP resolves host names (replaced in tests).
var lookupIP = net.DefaultResolver.LookupIPAddr

var errLocalPage = errors.New("this link is a page on this computer or the local network: it's not downloaded")

// checkPublicPage refuses a web page on this computer or the LAN, by its
// name (localhost, a private address, a local domain) or the addresses
// it resolves to: yt-dlp fetches it by itself, past Moviko's guard.
func checkPublicPage(ctx context.Context, link string) error {
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return errors.New("this link isn't a web link")
	}
	host := u.Hostname()
	if util.IsLocalHostname(host) {
		return errLocalPage
	}
	addrs, err := lookupIP(ctx, host)
	if err != nil {
		return fmt.Errorf("couldn't find %s: %w", host, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("couldn't find %s", host)
	}
	for _, a := range addrs {
		if util.IsPrivateIP(a.IP) {
			return errLocalPage
		}
	}
	return nil
}

// containerOf tells a video file's container by its first bytes ("" when
// it can't): what a provider's link says it serves isn't always what it
// does, and a file saved as it came is named after what it holds.
func containerOf(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	switch {
	case bytes.HasPrefix(head, []byte{0x1a, 0x45, 0xdf, 0xa3}):
		// Matroska; WebM says so in its header (its DocType).
		if bytes.Contains(head[:min(len(head), 64)], []byte("webm")) {
			return "webm"
		}
		return "mkv"
	case len(head) >= 8 && string(head[4:8]) == "ftyp":
		return "mp4"
	case len(head) >= 12 && string(head[:4]) == "RIFF" && string(head[8:12]) == "AVI ":
		return "avi"
	case len(head) > 376 && head[0] == 0x47 && head[188] == 0x47 && head[376] == 0x47:
		return "ts" // MPEG-TS: a sync byte every 188 bytes
	}
	return ""
}
