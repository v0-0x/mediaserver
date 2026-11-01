package stream

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/v0-0x/mediaserver/movies/internal/catalog"
	"github.com/v0-0x/mediaserver/movies/internal/providers"
)

// Subtitles for online streams
//
// Subtitle addons (OpenSubtitles...) find the subtitles made for the very
// file playing by its size and OpenSubtitles hash: the size plus the sum
// of the little-endian 64-bit words of its first and last 64 KiB. For an
// online stream those are read with two byte ranges, fetched like the
// stream (with its headers, never from this computer or the LAN).

const hashChunk = 64 << 10

// VideoOf is what subtitle addons match an online stream (a "remote:"
// path) with: its size, and its hash when the host takes byte ranges.
// An HLS stream has neither.
func VideoOf(ctx context.Context, path string) (providers.Video, error) {
	src, _, ok := remoteSourceOf(path)
	if !ok {
		return providers.Video{}, ErrRemoteGone
	}
	if src.hls {
		return providers.Video{}, nil
	}
	head, size, err := readRange(ctx, src, 0, hashChunk)
	if err != nil {
		return providers.Video{}, err
	}
	v := providers.Video{Size: max(size, 0)}
	if size < hashChunk || len(head) < hashChunk {
		return v, nil
	}
	tail, _, err := readRange(ctx, src, size-hashChunk, hashChunk)
	if err != nil || len(tail) < hashChunk {
		return v, nil // the size alone
	}
	v.Hash = openSubtitlesHash(size, head, tail)
	return v, nil
}

// openSubtitlesHash is the OpenSubtitles hash of a file of size bytes
// that starts with head and ends with tail (64 KiB each).
func openSubtitlesHash(size int64, head, tail []byte) string {
	h := uint64(size)
	for _, b := range [][]byte{head, tail} {
		for i := 0; i+8 <= len(b); i += 8 {
			h += binary.LittleEndian.Uint64(b[i:])
		}
	}
	return fmt.Sprintf("%016x", h)
}

// readRange reads n bytes of a stream from off on, and the whole file's
// size (-1 when the host doesn't tell). A host that doesn't take byte
// ranges only gives the start of the file.
func readRange(ctx context.Context, src *remoteSource, off, n int64) ([]byte, int64, error) {
	resp, err := fetchRemote(ctx, http.MethodGet, src, fmt.Sprintf("bytes=%d-%d", off, off+n-1))
	if err != nil {
		return nil, -1, err
	}
	defer resp.Body.Close()
	size := int64(-1)
	switch {
	case resp.StatusCode == http.StatusPartialContent:
		cr, _ := strings.CutPrefix(resp.Header.Get("Content-Range"), "bytes ")
		start, rest, _ := strings.Cut(cr, "-")
		if s, err := strconv.ParseInt(strings.TrimSpace(start), 10, 64); err != nil || s != off {
			return nil, -1, fmt.Errorf("the provider's link answered another range (%q)", cr)
		}
		if _, total, ok := strings.Cut(rest, "/"); ok {
			if t, err := strconv.ParseInt(strings.TrimSpace(total), 10, 64); err == nil {
				size = t
			}
		}
	case resp.StatusCode == http.StatusOK && off == 0:
		size = resp.ContentLength
	default:
		return nil, -1, fmt.Errorf("the provider's link answered %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, n))
	return b, size, err
}

// VideoSubtitles asks the subtitle providers for the subtitles of a movie
// or a show's episode (key) matching the online stream path (a "remote:"
// path) plays: its size and hash, worked out from the stream, and its
// file name go along (VideoOf).
func (s *Service) VideoSubtitles(ctx context.Context, media *catalog.Media, episode int, path, filename string) ([]providers.Subtitle, error) {
	episode, err := checkEpisode(media, episode)
	if err != nil {
		return nil, err
	}
	var sps []providers.SubtitleProvider
	if s.SubtitleProviders != nil {
		sps = s.SubtitleProviders()
	}
	if len(sps) == 0 {
		return []providers.Subtitle{}, nil
	}
	v, err := VideoOf(ctx, path)
	if err == ErrRemoteGone {
		return nil, err
	}
	// A stream that can't be read now still gets the title's subtitles.
	v.Filename = filename
	subs, _ := providers.AggregateVideoSubtitles(ctx, sps, RequestFor(media, episode), v, s.options().Timeout)
	if subs == nil {
		subs = []providers.Subtitle{}
	}
	return subs, nil
}
