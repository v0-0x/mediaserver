package catalog

import (
	"context"
	"reflect"
	"testing"

	"github.com/v0-0x/mediaserver/movies/internal/config"
)

// Where to watch: every tier of the region in the settings (subscription,
// free, free with ads, rent, buy), each by TMDB's display order; nothing for
// a region without any.
func TestWatchProviderTiers(t *testing.T) {
	names := func(list []WatchProvider) []string {
		var out []string
		for _, p := range list {
			out = append(out, p.Name)
		}
		return out
	}
	for _, tc := range []struct {
		region                         string
		flatrate, free, ads, rent, buy []string
		none                           bool
	}{
		{region: "US", flatrate: []string{"Shudder"}, free: []string{"Hoopla", "Kanopy"}, ads: []string{"Tubi TV", "The Roku Channel", "Pluto TV"}, buy: []string{"Apple TV"}},
		{region: "GB", ads: []string{"Tubi TV"}},
		{region: "DE", free: []string{"Arte"}},
		{region: "JP", none: true},
	} {
		t.Run(tc.region, func(t *testing.T) {
			f := newFakeTMDB(t, nil)
			p, _ := newTestPlatformSettings(t, f, func(s *config.Settings) { s.TMDB.APIKey, s.TMDB.Region = testKey, tc.region })
			m, err := p.Media(context.Background(), 10331, false)
			if err != nil {
				t.Fatal(err)
			}
			wp := m.WatchProviders
			if tc.none {
				if wp != nil {
					t.Fatalf("providers in %s: %+v", tc.region, wp)
				}
				return
			}
			if wp == nil || wp.Region != tc.region || wp.Link == "" {
				t.Fatalf("providers: %+v", wp)
			}
			for _, tier := range []struct {
				name      string
				got, want []string
			}{
				{"flatrate", names(wp.Flatrate), tc.flatrate},
				{"free", names(wp.Free), tc.free},
				{"ads", names(wp.Ads), tc.ads},
				{"rent", names(wp.Rent), tc.rent},
				{"buy", names(wp.Buy), tc.buy},
			} {
				if !reflect.DeepEqual(tier.got, tier.want) {
					t.Errorf("%s: %v, want %v", tier.name, tier.got, tier.want)
				}
			}
			if tc.region == "US" && (wp.Ads[0].ID != 73 || wp.Ads[0].Logo != img("w92", "/tubi.jpg")) {
				t.Errorf("first ad-supported service: %+v", wp.Ads[0])
			}
		})
	}
}
