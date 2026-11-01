package player

import (
	"strings"
	"unicode"
)

// Which audio and subtitle tracks play
//
// A title plays with the tracks the user picked for it last time (track
// prefs, remembered per movie or show). Otherwise:
//
//   - audio: the track in the preferred audio language when one is set (a
//     dub), else the one in the title's original language. The version
//     chosen for the title overrides both: "dub" is the preferred audio
//     language (English when none is set), "sub" the original language.
//   - subtitles: with SubtitlesForForeign (the default), in the preferred
//     subtitle language (English when none is set) when the audio isn't in
//     a language of the user's: that one, or the preferred audio language.
//     Without it, in the preferred subtitle language whenever one is set.
//     Otherwise off, except forced subtitles (the foreign parts of the
//     dialogue) in the audio's language, which are on.
//
// Files with one audio track keep it; files without subtitles, theirs.

// TrackRules is what chooses a title's tracks.
type TrackRules struct {
	// Prefs is the choice remembered for the title (nil: none, or the
	// settings don't remember choices).
	Prefs *TrackPrefs
	// Mode is the version chosen for the title: "dub", "sub" (original
	// audio) or "" (the settings decide).
	Mode string
	// OriginalLang is the title's original language (TMDB: ISO 639-1).
	OriginalLang string
	// AudioLang and SubLang are the preferred languages, comma separated
	// lists of codes ("fre,fra,fr"), "" for none.
	AudioLang, SubLang string
	// ForeignSubs: subtitles only for audio in a foreign language.
	ForeignSubs bool
}

// TrackChoice is what ChooseTracks picked. The player keeps its own audio
// unless HasAudio, its own subtitles unless HasSub or SubOff.
type TrackChoice struct {
	Audio    int  `json:"audio"` // track id
	HasAudio bool `json:"hasAudio"`
	Sub      int  `json:"sub"`
	HasSub   bool `json:"hasSub"`
	SubOff   bool `json:"subOff"`
	// Restored: the choice remembered for the title was applied.
	Restored bool `json:"restored"`
	// Note says which version plays ("Dub: French audio"), "" when the
	// title's version didn't decide.
	Note string `json:"note,omitempty"`
}

// ChooseTracks picks the audio and subtitle tracks to play among tracks
// (mpv's track list, or the in-app player's) by the rules above.
func ChooseTracks(tracks []Track, r TrackRules) TrackChoice {
	var audio, subs []Track
	for _, t := range tracks {
		switch t.Type {
		case "audio":
			audio = append(audio, t)
		case "sub":
			subs = append(subs, t)
		}
	}
	var c TrackChoice
	p := r.Prefs
	if p != nil && (p.AudioLang != "" || p.AudioTitle != "" || p.AudioIndex > 0) {
		if id, ok := PickTrack(tracks, "audio", p.AudioLang, p.AudioTitle, p.AudioIndex); ok {
			c.Audio, c.HasAudio, c.Restored = id, true, true
		}
	}
	if !c.HasAudio && len(audio) > 1 {
		if t, note, ok := defaultAudio(audio, r); ok {
			c.Audio, c.HasAudio, c.Note = t.ID, true, note
		}
	}
	lang := audioLanguage(audio, c, r)

	if len(subs) == 0 {
		return c
	}
	if p != nil {
		if p.SubOff {
			c.SubOff, c.Restored = true, true
			return c
		}
		if p.SubLang != "" || p.SubTitle != "" || p.SubIndex > 0 {
			if id, ok := PickTrack(tracks, "sub", p.SubLang, p.SubTitle, p.SubIndex); ok {
				c.Sub, c.HasSub, c.Restored = id, true, true
				return c
			}
		}
	}
	subLang := r.SubLang
	if firstLang(subLang) == "" {
		subLang = "en"
	}
	want := firstLang(r.SubLang) != ""
	if r.ForeignSubs {
		want = lang != "" && !langIn(lang, subLang) && !langIn(lang, r.AudioLang)
	}
	if want {
		if t, ok := fullSub(subs, func(t Track) bool { return langIn(trackLang(t), subLang) }); ok {
			c.Sub, c.HasSub = t.ID, true
			return c
		}
		// Subtitles without a language (a stream's subtitle files) may be
		// the ones: better than none.
		if t, ok := fullSub(subs, func(t Track) bool { return trackLang(t) == "" }); ok {
			c.Sub, c.HasSub = t.ID, true
			return c
		}
	}
	if t, ok := forcedSub(subs, lang); ok {
		c.Sub, c.HasSub = t.ID, true
		return c
	}
	c.SubOff = true
	return c
}

// defaultAudio picks the audio of a file with several audio tracks when the
// user hasn't picked one for the title, and says why when the title's
// version decided.
func defaultAudio(audio []Track, r TrackRules) (Track, string, bool) {
	orig := langCode(r.OriginalLang)
	switch r.Mode {
	case "dub":
		want := r.AudioLang
		if firstLang(want) == "" {
			want = "en"
		}
		if !langIn(orig, want) {
			if t, ok := audioIn(audio, want); ok {
				return t, "Dub: " + languageName(firstLang(want)) + " audio", true
			}
		}
	case "sub":
		if t, ok := audioIn(audio, orig); ok {
			return t, "Original audio", true
		}
		return Track{}, "", false // not the dub: the file's own pick stays
	}
	if t, ok := audioIn(audio, r.AudioLang); ok {
		return t, "", true
	}
	if t, ok := audioIn(audio, orig); ok {
		return t, "", true
	}
	return Track{}, "", false
}

// audioIn is the audio track in one of the languages of list: the default
// one if several are, never a commentary.
func audioIn(audio []Track, list string) (Track, bool) {
	if firstLang(list) == "" {
		return Track{}, false
	}
	var found []Track
	for _, t := range audio {
		if langIn(trackLang(t), list) && !isCommentary(t.Title) {
			found = append(found, t)
		}
	}
	for _, t := range found {
		if t.Default {
			return t, true
		}
	}
	if len(found) > 0 {
		return found[0], true
	}
	return Track{}, false
}

// audioLanguage is the language of the audio that will play: the chosen
// track's, else the one the player selected (or will: the default, the
// first). Untagged audio is the title's original language when it's the
// only track, the dub's language with a dub.
func audioLanguage(audio []Track, c TrackChoice, r TrackRules) string {
	var playing *Track
	for i, t := range audio {
		if c.HasAudio && t.ID == c.Audio || !c.HasAudio && t.Selected {
			playing = &audio[i]
			break
		}
	}
	if playing == nil && !c.HasAudio {
		for i, t := range audio {
			if t.Default {
				playing = &audio[i]
				break
			}
		}
		if playing == nil && len(audio) > 0 {
			playing = &audio[0]
		}
	}
	if playing != nil {
		if l := trackLang(*playing); l != "" {
			return l
		}
	}
	if r.Mode == "dub" {
		if l := firstLang(r.AudioLang); l != "" {
			return l
		}
		return "en"
	}
	if len(audio) <= 1 {
		return langCode(r.OriginalLang)
	}
	return ""
}

// fullSub is the best subtitle track that match accepts, forced ones
// aside: one that isn't for the deaf and hard of hearing, the default one.
func fullSub(subs []Track, match func(Track) bool) (Track, bool) {
	best, bestRank := Track{}, -1
	for _, t := range subs {
		if isForcedSub(t) || !match(t) {
			continue
		}
		rank := 0
		if !isSDH(t.Title) {
			rank += 2
		}
		if t.Default {
			rank++
		}
		if rank > bestRank {
			best, bestRank = t, rank
		}
	}
	return best, bestRank >= 0
}

// forcedSub is the forced subtitle track in the audio's language (lang; ""
// unknown): one tagged with it before one without a language.
func forcedSub(subs []Track, lang string) (Track, bool) {
	var untagged *Track
	for i, t := range subs {
		if !isForcedSub(t) {
			continue
		}
		switch l := trackLang(t); {
		case l == "":
			if untagged == nil {
				untagged = &subs[i]
			}
		case lang == "" || l == lang:
			return t, true
		}
	}
	if untagged != nil {
		return *untagged, true
	}
	return Track{}, false
}

// isForcedSub reports a forced subtitle track: flagged so, or named so (or
// "Signs & Songs", the forced subtitles of anime releases).
func isForcedSub(t Track) bool { return t.Forced || isSigns(t.Title) }

func isSDH(title string) bool {
	t := strings.ToLower(title)
	return hasWord(t, "sdh") || hasWord(t, "cc") || strings.Contains(t, "hearing")
}

func isCommentary(title string) bool {
	t := strings.ToLower(title)
	return strings.Contains(t, "commentary") || strings.Contains(t, "audio description") || strings.Contains(t, "descriptive")
}

func hasWord(s, w string) bool {
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if f == w {
			return true
		}
	}
	return false
}

// languageName is the English name of an ISO 639-1 code.
func languageName(code string) string {
	for _, l := range languages {
		if l.code == code && len(l.names) > 0 {
			return strings.ToUpper(l.names[0][:1]) + l.names[0][1:]
		}
	}
	return strings.ToUpper(code)
}
