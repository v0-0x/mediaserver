package config

import (
	"path/filepath"
	"testing"

	"github.com/v0-0x/mediaserver/anime/internal/db"
)

func TestMigrateDefaultPlayer(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "aniko.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	// Settings saved by a version before schemaVersion existed.
	old := map[string]any{
		"library":  map[string]any{"dir": "/mnt/big/Media/Anime"},
		"playback": map[string]any{"defaultPlayer": "mpv"},
		"aniCli":   map[string]any{"player": "mpv"},
	}
	if err := d.SetKV(settingsKey, old); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	got := s.Get()
	if got.Playback.DefaultPlayer != "builtin" || got.AniCli.Player != "builtin" || got.Library.Dir != "/mnt/big/Media/Anime" || got.SchemaVersion != schemaVersion {
		t.Fatalf("not migrated: %+v %+v %q v%d", got.Playback.DefaultPlayer, got.AniCli.Player, got.Library.Dir, got.SchemaVersion)
	}
	// Choosing mpv again afterwards sticks.
	got.Playback.DefaultPlayer = "mpv"
	if _, err := s.Save(got); err != nil {
		t.Fatal(err)
	}
	s2, err := NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Get().Playback.DefaultPlayer != "mpv" {
		t.Fatalf("mpv choice lost after restart: %q", s2.Get().Playback.DefaultPlayer)
	}
	// Fresh installs default to the in-app player.
	if Defaults().Playback.DefaultPlayer != "builtin" || Defaults().AniCli.Player != "builtin" {
		t.Fatal("defaults should use the in-app player")
	}
}

func TestMigrateMangaReadingMode(t *testing.T) {
	for mode, want := range map[string]string{"long-strip": "double", "paged": "paged", "double": "double"} {
		d, err := db.Open(filepath.Join(t.TempDir(), "aniko.db"))
		if err != nil {
			t.Fatal(err)
		}
		// Saved by version 2, when the long strip was the default.
		if err := d.SetKV(settingsKey, map[string]any{"schemaVersion": 2, "manga": map[string]any{"enabled": true, "readingMode": mode}}); err != nil {
			t.Fatal(err)
		}
		s, err := NewStore(d)
		if err != nil {
			t.Fatal(err)
		}
		if got := s.Get().Manga.ReadingMode; got != want {
			t.Errorf("%s: got %q, want %q", mode, got, want)
		}
		// The long strip chosen again afterwards sticks.
		cur := s.Get()
		cur.Manga.ReadingMode = "long-strip"
		if _, err := s.Save(cur); err != nil {
			t.Fatal(err)
		}
		if s2, err := NewStore(d); err != nil || s2.Get().Manga.ReadingMode != "long-strip" {
			t.Errorf("%s: long strip lost after restart (%v)", mode, err)
		}
		d.Close()
	}
	if Defaults().Manga.ReadingMode != "double" {
		t.Error("new installs should read two pages side by side")
	}
}

func TestMigrateMangaDirection(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "aniko.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	// Saved by version 3, when left to right was the default.
	if err := d.SetKV(settingsKey, map[string]any{"schemaVersion": 3, "manga": map[string]any{"enabled": true, "readingMode": "double", "direction": "ltr"}}); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Get().Manga.Direction; got != "rtl" {
		t.Fatalf("got %q, want rtl", got)
	}
	// Left to right chosen again afterwards sticks.
	cur := s.Get()
	cur.Manga.Direction = "ltr"
	if _, err := s.Save(cur); err != nil {
		t.Fatal(err)
	}
	if s2, err := NewStore(d); err != nil || s2.Get().Manga.Direction != "ltr" {
		t.Fatalf("left to right lost after restart (%v)", err)
	}
	if Defaults().Manga.Direction != "rtl" {
		t.Error("new installs should read manga right to left")
	}
}

func TestMigrateSkipEndings(t *testing.T) {
	for _, intro := range []bool{true, false} {
		d, err := db.Open(filepath.Join(t.TempDir(), "aniko.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = d.Close() })
		// Saved by version 4, before endings could be skipped.
		if err := d.SetKV(settingsKey, map[string]any{"schemaVersion": 4, "playback": map[string]any{"skipIntroAniSkip": intro}}); err != nil {
			t.Fatal(err)
		}
		s, err := NewStore(d)
		if err != nil {
			t.Fatal(err)
		}
		if p := s.Get().Playback; p.SkipIntroAniSkip != intro || p.SkipOutroAniSkip != intro {
			t.Errorf("skipping openings %v: got openings %v, endings %v; want endings like openings", intro, p.SkipIntroAniSkip, p.SkipOutroAniSkip)
		}
		// Changed afterwards, it sticks.
		cur := s.Get()
		cur.Playback.SkipOutroAniSkip = !intro
		if _, err := s.Save(cur); err != nil {
			t.Fatal(err)
		}
		if s2, err := NewStore(d); err != nil || s2.Get().Playback.SkipOutroAniSkip != !intro {
			t.Errorf("skipping endings set to %v is lost after a restart (%v)", !intro, err)
		}
	}
}

func TestThemeSettings(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "aniko.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	// Saved before themes: the defaults (holiday themes on, by month).
	if err := d.SetKV(settingsKey, map[string]any{"ui": map[string]any{"accentColor": "#22c3a6"}}); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(d)
	if err != nil {
		t.Fatal(err)
	}
	ui := s.Get().UI
	if ui.ColorTheme != "aniko" || ui.Seasonal != "auto" || !ui.SeasonalEffects || ui.AccentColor != "#22c3a6" || ui.ClassicLook {
		t.Fatalf("old settings: %+v", ui)
	}
	// The classic look is kept.
	next := s.Get()
	next.UI.ClassicLook = true
	if got, err := s.Save(next); err != nil || !got.UI.ClassicLook || !s.Get().UI.ClassicLook {
		t.Fatalf("classic look: %+v, %v", got.UI, err)
	}
	for _, c := range []struct{ theme, seasonal, wantTheme, wantSeasonal string }{
		{"midnight", "off", "midnight", "off"},
		{"black", "christmas", "black", "christmas"},
		{"nope", "easter", "aniko", "auto"},
		{"", "", "aniko", "auto"},
	} {
		next := s.Get()
		next.UI.ColorTheme, next.UI.Seasonal, next.UI.SeasonalEffects = c.theme, c.seasonal, false
		got, err := s.Save(next)
		if err != nil {
			t.Fatal(err)
		}
		if got.UI.ColorTheme != c.wantTheme || got.UI.Seasonal != c.wantSeasonal || got.UI.SeasonalEffects {
			t.Fatalf("%q/%q saved as %q/%q effects=%v", c.theme, c.seasonal, got.UI.ColorTheme, got.UI.Seasonal, got.UI.SeasonalEffects)
		}
	}
}
