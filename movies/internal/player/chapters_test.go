package player

import (
	"reflect"
	"testing"

	"github.com/v0-0x/mediaserver/movies/internal/config"
)

func TestClassifyChapter(t *testing.T) {
	cases := map[string]string{
		// Intros
		"Intro":                      SkipIntro,
		"INTRO":                      SkipIntro,
		"intro.":                     SkipIntro,
		"Opening":                    SkipIntro,
		"Opening Credits":            SkipIntro,
		"Opening Titles":             SkipIntro,
		"Opening Theme":              SkipIntro,
		"Opening Song":               SkipIntro,
		"OP":                         SkipIntro,
		"OP1":                        SkipIntro,
		"OP 2":                       SkipIntro,
		"Op":                         SkipIntro,
		"Intro / Opening Credits":    SkipIntro,
		"Title Sequence":             SkipIntro,
		"Main Titles":                SkipIntro,
		"Main Title":                 SkipIntro,
		"Titles":                     SkipIntro,
		"Theme Song":                 SkipIntro,
		"Chapter 2 - Opening":        SkipIntro,
		"02. Intro":                  SkipIntro,
		"Générique de début":         SkipIntro,
		"Vorspann":                   SkipIntro,
		"Sigla":                      SkipIntro,
		"オープニング":                     SkipIntro,
		"Opening Part 2":             SkipIntro,
		"Intro (Extended)":           "", // not only an intro's words: could be anything
		"Introduction":               "",
		"Intro to Chemistry":         "",
		"Opening Scene":              "",
		"The Opening":                "",
		"Cold Open":                  "",
		"Prologue":                   "",
		"Avant":                      "",
		"Teaser":                     "",
		"Previously On":              SkipRecap,
		"Previously on Breaking Bad": SkipRecap,
		"Previously":                 SkipRecap,
		"Recap":                      SkipRecap,
		"Episode Recap":              SkipRecap,
		"Last Time On":               SkipRecap,
		"The Story So Far":           SkipRecap,
		"Was bisher geschah":         SkipRecap,
		"Précédemment":               SkipRecap,
		// Credits
		"Credits":             SkipCredits,
		"End Credits":         SkipCredits,
		"End credits":         SkipCredits,
		"Closing Credits":     SkipCredits,
		"Ending Credits":      SkipCredits,
		"Credits Roll":        SkipCredits,
		"Ending":              SkipCredits,
		"Ending Theme":        SkipCredits,
		"ED":                  SkipCredits,
		"ED2":                 SkipCredits,
		"Outro":               SkipCredits,
		"End Titles":          SkipCredits,
		"Closing":             SkipCredits,
		"Générique de fin":    SkipCredits,
		"Abspann":             SkipCredits,
		"Créditos finales":    SkipCredits,
		"Titoli di coda":      SkipCredits,
		"エンディング":              SkipCredits,
		"Chapter 12: Credits": SkipCredits,
		"The Ending":          "",
		"End":                 "",
		// Previews of the next episode
		"Preview":                SkipPreview,
		"Next Episode":           SkipPreview,
		"Next Episode Preview":   SkipPreview,
		"Next Time":              SkipPreview,
		"Next Time On":           SkipPreview,
		"Next Week":              SkipPreview,
		"Next On Doctor Who":     SkipPreview,
		"Sneak Peek":             SkipPreview,
		"ED + Preview":           SkipPreview,
		"Vorschau":               SkipPreview,
		"予告":                     SkipPreview,
		"Post-Credits Scene":     "",
		"Post Credits Scene":     "",
		"Post-credits":           "",
		"Mid-Credits Scene":      "",
		"After the Credits":      "",
		"End Credits Scene":      "",
		"Credits (with stinger)": "",
		"Stinger":                "",
		// The episode or movie itself
		"Episode":      "",
		"Chapter 1":    "",
		"Chapter 01":   "",
		"Part A":       "",
		"Part B":       "",
		"Part 1":       "",
		"Act One":      "",
		"Scene 4":      "",
		"Eyecatch":     "",
		"":             "",
		"   ":          "",
		"00:05:00.000": "",
		"The Next Day": "",
	}
	for title, want := range cases {
		if got := ClassifyChapter(title); got != want {
			t.Errorf("ClassifyChapter(%q) = %q, want %q", title, got, want)
		}
	}
}

func TestChapterSkips(t *testing.T) {
	cases := []struct {
		name     string
		chapters []Chapter
		duration float64
		want     []SkipInterval
	}{
		{
			name: "an episode, ends from the next starts (mpv)",
			chapters: []Chapter{
				{"Previously On", 0, 0}, {"Opening Credits", 52.4, 0}, {"Episode", 112.4, 0},
				{"End Credits", 2731, 0}, {"Next Episode", 2790, 0},
			},
			duration: 2823,
			want: []SkipInterval{
				{SkipRecap, 0, 52.4}, {SkipIntro, 52.4, 112.4}, {SkipCredits, 2731, 2790}, {SkipPreview, 2790, 2823},
			},
		},
		{
			name: "ends given (ffprobe), out of order",
			chapters: []Chapter{
				{"Episode", 90, 1300}, {"Intro", 0, 90}, {"Credits", 1300, 1400},
			},
			duration: 1400,
			want:     []SkipInterval{{SkipIntro, 0, 90}, {SkipCredits, 1300, 1400}},
		},
		{
			name:     "the last chapter's end isn't known yet",
			chapters: []Chapter{{"Intro", 0, 0}, {"Episode", 60, 0}, {"Credits", 1300, 0}},
			want:     []SkipInterval{{SkipIntro, 0, 60}},
		},
		{
			name:     "neighbouring chapters of one kind make one part",
			chapters: []Chapter{{"Opening Part 1", 0, 0}, {"Opening Part 2", 40, 0}, {"Episode", 95, 0}},
			duration: 1400,
			want:     []SkipInterval{{SkipIntro, 0, 95}},
		},
		{
			name:     "the credits before a post-credits scene",
			chapters: []Chapter{{"Chapter 1", 0, 0}, {"End Credits", 5000, 0}, {"Post-Credits Scene", 5600, 0}},
			duration: 5700,
			want:     []SkipInterval{{SkipCredits, 5000, 5600}},
		},
		{
			name:     "chapters gone wrong: an intro that's most of the file",
			chapters: []Chapter{{"Intro", 0, 0}, {"Credits", 1300, 0}},
			duration: 1400,
			want:     []SkipInterval{{SkipCredits, 1300, 1400}},
		},
		{
			name:     "too short to skip, and chapters past the end",
			chapters: []Chapter{{"Intro", 0, 0.5}, {"Episode", 0.5, 0}, {"Credits", 1500, 0}},
			duration: 1400,
			want:     nil,
		},
		{name: "no chapters", duration: 1400, want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ChapterSkips(tc.chapters, tc.duration); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestSkipRun(t *testing.T) {
	skips := []SkipInterval{{SkipRecap, 0, 50}, {SkipIntro, 50, 110}, {SkipCredits, 1300, 1390}, {SkipPreview, 1390, 1420}}
	all := func(string) bool { return true }
	cases := []struct {
		name     string
		t        float64
		on       func(string) bool
		from, to int
		ok       bool
	}{
		{"recap, then the intro", 0, all, 0, 1, true},
		{"in the intro", 70, all, 1, 1, true},
		{"the episode", 500, all, 0, 0, false},
		{"credits and preview", 1300, all, 2, 3, true},
		{"the last second of a part plays", 109.5, all, 0, 0, false},
		{"only credits skipped", 1300, func(typ string) bool { return typ == SkipCredits }, 2, 2, true},
		{"recap not skipped, intro is", 0, func(typ string) bool { return typ == SkipIntro }, 0, 0, false},
	}
	for _, tc := range cases {
		from, to, ok := skipRun(skips, tc.t, tc.on)
		if ok != tc.ok || ok && (from != tc.from || to != tc.to) {
			t.Errorf("%s: skipRun at %v = %d, %d, %v; want %d, %d, %v", tc.name, tc.t, from, to, ok, tc.from, tc.to, tc.ok)
		}
	}
}

func TestSkipSettingAndNames(t *testing.T) {
	p := config.PlaybackSettings{SkipIntro: true}
	for typ, want := range map[string]bool{SkipIntro: true, SkipRecap: true, "op": true, SkipCredits: false, SkipPreview: false, "ed": false, "": false} {
		if _, on := skipSetting(p, typ); on != want {
			t.Errorf("SkipIntro only: %q skipped = %v", typ, on)
		}
	}
	p = config.PlaybackSettings{SkipCredits: true}
	for typ, want := range map[string]bool{SkipCredits: true, SkipPreview: true, "mixed-ed": true, SkipIntro: false, SkipRecap: false} {
		if _, on := skipSetting(p, typ); on != want {
			t.Errorf("SkipCredits only: %q skipped = %v", typ, on)
		}
	}
	got := skipNames(p, []SkipInterval{{Type: SkipRecap}, {Type: SkipIntro}, {Type: "op"}})
	if got != "recap and intro" {
		t.Errorf("skipNames = %q", got)
	}
}
