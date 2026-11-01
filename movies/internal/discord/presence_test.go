package discord

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPresence(t *testing.T) {
	now := time.Date(2026, 10, 9, 21, 0, 0, 0, time.UTC)
	poster := "https://image.tmdb.org/t/p/w500/ggFHVNu6YYI5L9pCfOacjizRGt.jpg"
	cases := []struct {
		name string
		w    Watching
		want Activity
	}{
		{
			name: "an episode playing",
			w: Watching{Title: "Breaking Bad", Year: 2008, Show: true, Episode: 1002, EpisodeTitle: "Cat's in the Bag...",
				Poster: poster, Position: 600, Duration: 2880},
			want: Activity{Details: "Breaking Bad", State: "S01E02 · Cat's in the Bag...", LargeImage: poster, LargeText: "Breaking Bad",
				SmallImage: AppAsset, SmallText: "KumoBox", Start: now.Add(-10 * time.Minute), End: now.Add(38 * time.Minute)},
		},
		{
			name: "an episode without its title, paused",
			w:    Watching{Title: "Severance", Show: true, Episode: 2010, Poster: poster, Position: 600, Duration: 2880, Paused: true},
			want: Activity{Details: "Severance", State: "S02E10", LargeImage: poster, LargeText: "Severance", SmallImage: AppAsset, SmallText: "KumoBox"},
		},
		{
			name: "a movie",
			w:    Watching{Title: "Inception", Year: 2010, Episode: 1, Poster: poster, Position: 90.5, Duration: 8880},
			want: Activity{Details: "Inception", State: "2010", LargeImage: poster, LargeText: "Inception (2010)", SmallImage: AppAsset, SmallText: "KumoBox",
				Start: now.Add(-90500 * time.Millisecond), End: now.Add(-90500 * time.Millisecond).Add(8880 * time.Second)},
		},
		{
			// No poster (or one Discord can't fetch): the application's image.
			name: "no poster",
			w:    Watching{Title: "Home video", Poster: "http://127.0.0.1:43311/api/image?x"},
			want: Activity{Details: "Home video", LargeImage: AppAsset, LargeText: "Home video", SmallImage: AppAsset, SmallText: "KumoBox"},
		},
	}
	for _, tc := range cases {
		if got := Presence(tc.w, now); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", tc.name, got, tc.want)
		}
	}
}

func TestActivityPayload(t *testing.T) {
	start := time.UnixMilli(1_760_000_000_000)
	a := Activity{Details: "Breaking Bad", State: "S01E02 · Cat's in the Bag...", LargeImage: "https://image.tmdb.org/p.jpg", LargeText: "Breaking Bad",
		SmallImage: AppAsset, SmallText: "KumoBox", Start: start, End: start.Add(48 * time.Minute)}
	want := map[string]any{
		"type": typeWatching, "status_display_type": statusShowsDetails,
		"details": "Breaking Bad", "state": "S01E02 · Cat's in the Bag...",
		"assets": map[string]any{
			"large_image": "https://image.tmdb.org/p.jpg", "large_text": "Breaking Bad",
			"small_image": AppAsset, "small_text": "KumoBox",
		},
		"timestamps": map[string]any{"start": start.UnixMilli(), "end": start.Add(48 * time.Minute).UnixMilli()},
	}
	if got := a.payload(true); !reflect.DeepEqual(got, want) {
		t.Errorf("payload:\n got %v\nwant %v", got, want)
	}

	// Empty fields are left out (Discord refuses strings shorter than 2),
	// one letter is padded, long ones are cut.
	got := Activity{Details: "X", LargeText: "no image"}.payload(false)
	if got["details"] != "X " || got["state"] != nil || got["status_display_type"] != nil || got["timestamps"] != nil {
		t.Errorf("short fields: %v", got)
	}
	long := Activity{State: strings.Repeat("é", 200)}
	if s, _ := long.payload(false)["state"].(string); len([]rune(s)) != 120 {
		t.Errorf("long state: %d characters", len([]rune(s)))
	}
	if assets := got["assets"].(map[string]any); len(assets) != 0 {
		t.Errorf("assets without images: %v", assets)
	}
}
