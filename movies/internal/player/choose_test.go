package player

import "testing"

func TestLangCode(t *testing.T) {
	cases := map[string]string{
		"fre": "fr", "fra": "fr", "fr": "fr", "FR": "fr", "fr-FR": "fr", "French": "fr", "français": "fr",
		"ger": "de", "deu": "de", "de-DE": "de",
		"eng": "en", "en-US": "en", "en_GB": "en",
		"jpn": "ja", "jp": "ja",
		"kor": "ko", "spa": "es", "es-419": "es", "pt-BR": "pt", "por": "pt",
		"chi": "zh", "zho": "zh", "yue": "zh", "cn": "zh", "zh-Hant": "zh",
		"nob": "no", "dut": "nl", "per": "fa", "hin": "hi",
		"und": "", "mul": "", "xx": "", "": "", " ": "",
		"forced.en": "en", "en.sdh": "en", "fre.forced": "fr",
		"tlh": "tlh", // unknown codes stay
	}
	for in, want := range cases {
		if got := langCode(in); got != want {
			t.Errorf("langCode(%q) = %q, want %q", in, got, want)
		}
	}
	if !langIn("fra", "fre,fra,fr") || !langIn("fr-CA", "fr") || langIn("eng", "fre,fra,fr") || langIn("", "eng") || langIn("und", "und") {
		t.Error("langIn")
	}
	for title, want := range map[string]string{"English 5.1": "en", "Français (VFF)": "fr", "Latino": "es", "ENG": "en", "Commentary": "", "Track 2": "", "Deutsch DTS-HD": "de"} {
		if got := titleLang(title); got != want {
			t.Errorf("titleLang(%q) = %q, want %q", title, got, want)
		}
	}
}

func TestChooseTracks(t *testing.T) {
	// A French film, with an English dub.
	french := []Track{
		{ID: 1, Type: "video"},
		{ID: 1, Type: "audio", Lang: "fre", Title: "Français", Default: true},
		{ID: 2, Type: "audio", Lang: "eng", Title: "English Dub"},
		{ID: 3, Type: "audio", Lang: "fre", Title: "Director's Commentary"},
		{ID: 1, Type: "sub", Lang: "eng", Title: "English SDH", Default: true},
		{ID: 2, Type: "sub", Lang: "eng", Title: "English"},
		{ID: 3, Type: "sub", Lang: "fre", Title: "Forced", Forced: true},
		{ID: 4, Type: "sub", Lang: "fre", Title: "Français"},
	}
	// An English film.
	english := []Track{
		{ID: 1, Type: "audio", Lang: "eng", Title: "English 5.1"},
		{ID: 1, Type: "sub", Lang: "eng", Title: "Forced", Forced: true},
		{ID: 2, Type: "sub", Lang: "eng", Title: "English"},
		{ID: 3, Type: "sub", Lang: "spa", Title: "Español"},
	}
	// A stream: untagged audio, a subtitle file without a language.
	stream := []Track{{ID: 1, Type: "audio"}, {ID: 1, Type: "sub", Title: "https://cdn.example/subs.vtt", External: true}}

	audio := func(id int) TrackChoice { return TrackChoice{Audio: id, HasAudio: true} }
	with := func(c TrackChoice, edit func(*TrackChoice)) TrackChoice { edit(&c); return c }
	sub := func(id int) func(*TrackChoice) { return func(c *TrackChoice) { c.Sub, c.HasSub = id, true } }
	off := func(c *TrackChoice) { c.SubOff = true }

	cases := []struct {
		name   string
		tracks []Track
		rules  TrackRules
		want   TrackChoice
	}{
		// Original audio; it isn't the user's language (English by default):
		// English subtitles, not the SDH ones.
		{"foreign film", french, TrackRules{OriginalLang: "fr", ForeignSubs: true},
			with(audio(1), sub(2))},
		// A dub in the user's language: only forced subtitles in English (none).
		{"preferred audio", french, TrackRules{OriginalLang: "fr", AudioLang: "eng,en", ForeignSubs: true},
			with(audio(2), off)},
		// French is the user's language: the forced subtitles only.
		{"the user's language", french, TrackRules{OriginalLang: "fr", SubLang: "fre,fra,fr", ForeignSubs: true},
			with(audio(1), sub(3))},
		{"no subtitles wanted", french, TrackRules{OriginalLang: "fr"},
			with(audio(1), sub(3))},
		// Without ForeignSubs, the preferred subtitles whenever there are some.
		{"subtitles always", french, TrackRules{OriginalLang: "fr", SubLang: "eng"},
			with(audio(1), sub(2))},
		{"the dub version", french, TrackRules{OriginalLang: "fr", Mode: "dub", ForeignSubs: true},
			TrackChoice{Audio: 2, HasAudio: true, SubOff: true, Note: "Dub: English audio"}},
		{"the original version", french, TrackRules{OriginalLang: "fr", Mode: "sub", AudioLang: "eng", ForeignSubs: true},
			TrackChoice{Audio: 1, HasAudio: true, Sub: 2, HasSub: true, Note: "Original audio"}},
		// What the user picked for the title wins.
		{"remembered", french, TrackRules{OriginalLang: "fr", ForeignSubs: true,
			Prefs: &TrackPrefs{AudioLang: "eng", AudioTitle: "English Dub", AudioIndex: 2, SubOff: true}},
			TrackChoice{Audio: 2, HasAudio: true, SubOff: true, Restored: true}},
		{"remembered subtitles", french, TrackRules{OriginalLang: "fr", ForeignSubs: true,
			Prefs: &TrackPrefs{SubLang: "fre", SubTitle: "Français", SubIndex: 4}},
			TrackChoice{Audio: 1, HasAudio: true, Sub: 4, HasSub: true, Restored: true}},
		{"single audio track, the user's language", english, TrackRules{OriginalLang: "en", ForeignSubs: true},
			with(TrackChoice{}, sub(1))},
		{"single audio track, foreign to the user", english, TrackRules{OriginalLang: "en", SubLang: "spa,es", ForeignSubs: true},
			with(TrackChoice{}, sub(3))},
		{"no forced subtitles", english[:1:1], TrackRules{OriginalLang: "en", ForeignSubs: true}, TrackChoice{}},
		// Untagged audio is the original language; a subtitle file without
		// a language is better than none.
		{"stream of a foreign film", stream, TrackRules{OriginalLang: "ja", ForeignSubs: true},
			with(TrackChoice{}, sub(1))},
		{"stream of a film in the user's language", stream, TrackRules{OriginalLang: "en", ForeignSubs: true},
			with(TrackChoice{}, off)},
		{"dubbed stream", stream, TrackRules{OriginalLang: "ja", Mode: "dub", ForeignSubs: true},
			with(TrackChoice{}, off)},
		{"unknown original language", stream, TrackRules{ForeignSubs: true},
			with(TrackChoice{}, off)},
	}
	for _, tc := range cases {
		if got := ChooseTracks(tc.tracks, tc.rules); got != tc.want {
			t.Errorf("%s:\n got %+v\nwant %+v", tc.name, got, tc.want)
		}
	}
}

func TestChooseAudio(t *testing.T) {
	cases := []struct {
		name  string
		audio []Track
		rules TrackRules
		want  int // 0: keep the player's
	}{
		{"by title", []Track{{ID: 1, Type: "audio", Title: "English 5.1"}, {ID: 2, Type: "audio", Title: "Japanese 2.0 FLAC"}},
			TrackRules{OriginalLang: "ja"}, 2},
		{"not a commentary", []Track{{ID: 1, Type: "audio", Lang: "eng", Title: "Commentary"}, {ID: 2, Type: "audio", Lang: "eng"}},
			TrackRules{OriginalLang: "en"}, 2},
		{"the default one of a language", []Track{{ID: 1, Type: "audio", Lang: "eng", Title: "Stereo"}, {ID: 2, Type: "audio", Lang: "eng", Default: true}, {ID: 3, Type: "audio", Lang: "ger"}},
			TrackRules{OriginalLang: "en"}, 2},
		{"BCP 47 tags", []Track{{ID: 1, Type: "audio", Lang: "en"}, {ID: 2, Type: "audio", Lang: "pt-BR"}},
			TrackRules{OriginalLang: "pt"}, 2},
		{"Cantonese (TMDB cn)", []Track{{ID: 1, Type: "audio", Lang: "eng"}, {ID: 2, Type: "audio", Lang: "chi"}},
			TrackRules{OriginalLang: "cn"}, 2},
		{"preferred audio missing: the original", []Track{{ID: 1, Type: "audio", Lang: "eng"}, {ID: 2, Type: "audio", Lang: "kor"}},
			TrackRules{OriginalLang: "ko", AudioLang: "fre,fra,fr"}, 2},
		{"nothing known", []Track{{ID: 1, Type: "audio"}, {ID: 2, Type: "audio"}}, TrackRules{OriginalLang: "ko"}, 0},
		{"original version unknown", []Track{{ID: 1, Type: "audio", Lang: "eng"}, {ID: 2, Type: "audio", Lang: "ger"}},
			TrackRules{Mode: "sub", AudioLang: "ger"}, 0},
		{"a dub in the original language is the original", []Track{{ID: 1, Type: "audio", Lang: "fre"}, {ID: 2, Type: "audio", Lang: "eng"}},
			TrackRules{OriginalLang: "en", Mode: "dub"}, 2},
	}
	for _, tc := range cases {
		c := ChooseTracks(tc.audio, tc.rules)
		if got := map[bool]int{true: c.Audio}[c.HasAudio]; got != tc.want {
			t.Errorf("%s: audio %d (%+v), want %d", tc.name, got, c, tc.want)
		}
	}
}

func TestChooseSubtitles(t *testing.T) {
	audio := Track{ID: 1, Type: "audio", Lang: "jpn"}
	cases := []struct {
		name  string
		subs  []Track
		rules TrackRules
		want  int // 0: off
	}{
		{"full before signs and SDH", []Track{
			{ID: 1, Type: "sub", Lang: "eng", Title: "Signs & Songs"},
			{ID: 2, Type: "sub", Lang: "eng", Title: "English [CC]"},
			{ID: 3, Type: "sub", Lang: "eng", Title: "Dialogue"},
		}, TrackRules{ForeignSubs: true}, 3},
		{"the language's default", []Track{
			{ID: 1, Type: "sub", Lang: "eng", Title: "Commentary"},
			{ID: 2, Type: "sub", Lang: "eng", Default: true},
		}, TrackRules{ForeignSubs: true}, 2},
		{"none in the language: forced in the audio's", []Track{
			{ID: 1, Type: "sub", Lang: "ger"},
			{ID: 2, Type: "sub", Lang: "jpn", Forced: true},
		}, TrackRules{ForeignSubs: true}, 2},
		{"forced in another language stay off", []Track{
			{ID: 1, Type: "sub", Lang: "eng", Forced: true},
		}, TrackRules{SubLang: "jpn", ForeignSubs: true}, 0},
		{"an untagged forced track", []Track{
			{ID: 1, Type: "sub", Title: "Forced"},
			{ID: 2, Type: "sub", Lang: "eng"},
		}, TrackRules{SubLang: "jpn", ForeignSubs: true}, 1},
		{"preferred audio is the user's language too", []Track{
			{ID: 1, Type: "sub", Lang: "eng"},
		}, TrackRules{AudioLang: "jpn", ForeignSubs: true}, 0},
		{"by the title's language", []Track{
			{ID: 1, Type: "sub", Title: "Español"},
			{ID: 2, Type: "sub", Title: "English"},
		}, TrackRules{ForeignSubs: true}, 2},
	}
	for _, tc := range cases {
		c := ChooseTracks(append([]Track{audio}, tc.subs...), tc.rules)
		got := 0
		if c.HasSub {
			got = c.Sub
		}
		if got != tc.want || got == 0 && !c.SubOff {
			t.Errorf("%s: %+v, want subtitles %d", tc.name, c, tc.want)
		}
	}
}
