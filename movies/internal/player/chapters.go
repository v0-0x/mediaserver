package player

import (
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/v0-0x/mediaserver/movies/internal/config"
)

// Skipping by chapters
//
// Releases of shows and movies often mark their parts with named chapters:
// "Previously On", "Opening Credits", "Episode", "End Credits", "Next
// Episode". The players skip the parts whose names say they are an intro, a
// recap, the credits or a preview of the next episode, as the settings say
// (Settings › Playback): intros and recaps with SkipIntro, credits and
// previews with SkipCredits.

// SkipInterval is a part of a video the players offer to skip.
type SkipInterval struct {
	Type  string  `json:"type"` // intro | recap | credits | preview
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// Chapter is a named chapter of a video. End is 0 when unknown (mpv only
// gives the start: the chapter ends where the next one starts).
type Chapter struct {
	Title string  `json:"title"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// What a chapter's name can say it is.
const (
	SkipIntro   = "intro"
	SkipRecap   = "recap"
	SkipCredits = "credits"
	SkipPreview = "preview"
)

// Words that may come with the name of an intro or of the end credits
// ("Opening Theme", "OP 2", "Ending Credits").
const skipTail = `(?: (?:\d+|theme|song|music|credits?|titles?|sequence|animation|opening|intro|op|part \d+))*`

// word matches one of the alternatives as whole words of a normalized name
// (normalizeChapter leaves single spaces between words; Go's \b only knows
// ASCII letters).
func word(alts string) string { return `(?:^| )(?:` + alts + `)(?: |$)` }

var (
	// Scenes after (or in the middle of) the credits are part of the movie.
	afterCreditsRe = regexp.MustCompile(word(`(?:post|mid|after)(?: the)? ?credits?`) + `|` +
		word(`credits? (?:scene|stinger|tag|cookie|bonus)`) + `|` + word(`stinger|bonus scene`))
	introRe = regexp.MustCompile(`^(?:` +
		`(?:intro|opening|op|vorspann|sigla(?: iniziale| di apertura)?|générique(?: de)? (?:début|d ouverture)|créditos iniciales|abertura|オープニング)` + skipTail +
		`|(?:main|opening|start) titles?(?: sequence)?|titles?(?: sequence| card)?|title sequence|theme(?: song)?|main theme` +
		`)$`)
	recapRe = regexp.MustCompile(word(`recap|recaps`) + `|^(?:previously|last time|the story so far|story so far|was bisher geschah|précédemment|anteriormente|nelle puntate precedenti|riassunto|前回)(?: |$)`)
	// Previews of the next episode (under the credits setting).
	previewRe = regexp.MustCompile(word(`preview|previews|next episode|next time|sneak peek|vorschau|avance|anteprima|prochain épisode|prochainement|próximo episodio|próximo episódio|prossimo episodio|予告`) +
		`|^next (?:week|on)(?: |$)`)
	creditsRe = regexp.MustCompile(word(`credits?|outro|abspann|créditos(?: finales| finais)?|titoli di coda|staff roll|スタッフロール|エンディング`) +
		`|^(?:ending|ed|closing|end titles?|end title sequence|sigla finale|générique(?: de)? fin|générique final)` + skipTail + `$`)

	// "Chapter 3 - Opening", "01. Intro": the numbering goes.
	chapterNumberRe = regexp.MustCompile(`^(?:(?:chapter|chap|ch|kapitel|chapitre|capítulo|capitolo) ?)?\d+ (.+)$`)
)

// ClassifyChapter tells what a chapter's name says it is: SkipIntro (an
// opening or title sequence), SkipRecap ("Previously on…"), SkipCredits (the
// end credits), SkipPreview (of the next episode), or "" for everything
// else: the episode or movie itself, and the scenes after the credits.
func ClassifyChapter(title string) string {
	t := normalizeChapter(title)
	switch {
	case t == "", afterCreditsRe.MatchString(t):
		return ""
	case introRe.MatchString(t):
		return SkipIntro
	case recapRe.MatchString(t):
		return SkipRecap
	case previewRe.MatchString(t):
		return SkipPreview
	case creditsRe.MatchString(t):
		return SkipCredits
	}
	return ""
}

// normalizeChapter lowercases a chapter name and keeps its words (letters
// and digits, numbers apart: "OP1" is "op 1"), separated by single spaces,
// without the chapter number in front.
func normalizeChapter(title string) string {
	var b strings.Builder
	prev := ' '
	for _, r := range strings.ToLower(title) {
		var kind rune // 'a' letter, '0' digit, ' ' anything else
		switch {
		case unicode.IsLetter(r):
			kind = 'a'
		case unicode.IsDigit(r):
			kind = '0'
		default:
			kind = ' '
		}
		if kind == ' ' {
			if prev != ' ' {
				b.WriteByte(' ')
			}
			prev = ' '
			continue
		}
		if prev != ' ' && prev != kind {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
		prev = kind
	}
	t := strings.TrimSpace(b.String())
	if m := chapterNumberRe.FindStringSubmatch(t); m != nil {
		t = m[1]
	}
	return t
}

// withEnds returns the chapters in order, each ending where the next one
// starts (or where it says it ends), the last one at duration (0: unknown).
func withEnds(chapters []Chapter, duration float64) []Chapter {
	out := make([]Chapter, 0, len(chapters))
	for _, c := range chapters {
		if !math.IsNaN(c.Start) && !math.IsInf(c.Start, 0) && c.Start >= 0 {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	for i := range out {
		end := out[i].End
		if i+1 < len(out) && (end <= out[i].Start || end > out[i+1].Start) {
			end = out[i+1].Start
		}
		if i+1 == len(out) && duration > 0 && (end <= out[i].Start || end > duration) {
			end = duration
		}
		out[i].End = end
	}
	return out
}

// ChapterSkips lists the parts of a video duration seconds long (0:
// unknown) that its chapters name as an intro, a recap, the credits or a
// preview, in order. Neighbouring chapters of the same kind ("Opening Part
// 1", "Opening Part 2") make one part. A chapter that's most of the video
// isn't skipped: those are chapter marks gone wrong, not an intro.
func ChapterSkips(chapters []Chapter, duration float64) []SkipInterval {
	var out []SkipInterval
	for _, c := range withEnds(chapters, duration) {
		typ := ClassifyChapter(c.Title)
		if typ == "" || c.End-c.Start < 1 || duration > 0 && c.End-c.Start > duration/2 {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Type == typ && math.Abs(out[n-1].End-c.Start) < 1 {
			out[n-1].End = c.End
			continue
		}
		out = append(out, SkipInterval{Type: typ, Start: c.Start, End: c.End})
	}
	return out
}

// skipSetting says whether the settings skip a kind of part, and how the
// players name it. The names AniSkip used (op, ed...) count too.
func skipSetting(p config.PlaybackSettings, typ string) (what string, on bool) {
	switch typ {
	case SkipIntro, "op", "mixed-op":
		return "intro", p.SkipIntro
	case SkipRecap:
		return "recap", p.SkipIntro
	case SkipCredits, "ed", "mixed-ed", "outro":
		return "credits", p.SkipCredits
	case SkipPreview:
		return "preview", p.SkipCredits
	}
	return "", false
}

// skipRun works out what to skip at t: the part of skips (in order) t is in,
// with the parts right after it the settings skip too (credits, then the
// preview). It returns their indexes, from and to; ok is false when t is in
// no part the settings skip.
func skipRun(skips []SkipInterval, t float64, on func(typ string) bool) (from, to int, ok bool) {
	from = -1
	for i, sk := range skips {
		if t >= sk.Start-0.5 && t < sk.End-1 && on(sk.Type) {
			from = i
			break
		}
	}
	if from < 0 {
		return 0, 0, false
	}
	to = from
	for to+1 < len(skips) && math.Abs(skips[to+1].Start-skips[to].End) < 1 && on(skips[to+1].Type) {
		to++
	}
	return from, to, true
}

// skipNames names the kinds of parts skipped: "intro", "credits and preview".
func skipNames(p config.PlaybackSettings, skips []SkipInterval) string {
	var names []string
	for _, sk := range skips {
		if what, _ := skipSetting(p, sk.Type); what != "" && !slices.Contains(names, what) {
			names = append(names, what)
		}
	}
	return strings.Join(names, " and ")
}
