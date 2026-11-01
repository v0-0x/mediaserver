package config

// SharedPrefs are the settings a device keeps the same as the others on its
// AniList account, through an Aniko Hub: how Aniko looks, and how it plays.
// What depends on the device (the card size, Home's sections, the player
// and the paths) isn't.
type SharedPrefs struct {
	UI struct {
		AccentColor      string `json:"accentColor"`
		ColorTheme       string `json:"colorTheme"`
		Seasonal         string `json:"seasonal"`
		SeasonalEffects  bool   `json:"seasonalEffects"`
		ClassicLook      bool   `json:"classicLook"`
		TitleLanguage    string `json:"titleLanguage"`
		BannerType       string `json:"bannerType"`
		ShowEpisodeTitle bool   `json:"showEpisodeTitle"`
		BlurUnwatched    bool   `json:"blurUnwatched"`
	} `json:"ui"`
	Playback struct {
		AutoPlayNext       bool   `json:"autoPlayNext"`
		PreferredAudioLang string `json:"preferredAudioLang"`
		PreferredSubLang   string `json:"preferredSubLang"`
		SkipIntroAniSkip   bool   `json:"skipIntroAniSkip"`
		SkipOutroAniSkip   bool   `json:"skipOutroAniSkip"`
		SkipRecapAniSkip   bool   `json:"skipRecapAniSkip"`
	} `json:"playback"`
}

// SharedPrefs are these settings' shared ones.
func (s Settings) SharedPrefs() SharedPrefs {
	var p SharedPrefs
	u, pb := s.UI, s.Playback
	p.UI.AccentColor, p.UI.ColorTheme, p.UI.Seasonal, p.UI.SeasonalEffects = u.AccentColor, u.ColorTheme, u.Seasonal, u.SeasonalEffects
	p.UI.ClassicLook, p.UI.TitleLanguage, p.UI.BannerType = u.ClassicLook, u.TitleLanguage, u.BannerType
	p.UI.ShowEpisodeTitle, p.UI.BlurUnwatched = u.ShowEpisodeTitle, u.BlurUnwatched
	p.Playback.AutoPlayNext, p.Playback.PreferredAudioLang, p.Playback.PreferredSubLang = pb.AutoPlayNext, pb.PreferredAudioLang, pb.PreferredSubLang
	p.Playback.SkipIntroAniSkip, p.Playback.SkipOutroAniSkip, p.Playback.SkipRecapAniSkip = pb.SkipIntroAniSkip, pb.SkipOutroAniSkip, pb.SkipRecapAniSkip
	return p
}

// WithSharedPrefs are these settings with shared ones from another device
// (checked as they're saved, see sanitize).
func (s Settings) WithSharedPrefs(p SharedPrefs) Settings {
	u, pb := &s.UI, &s.Playback
	u.AccentColor, u.ColorTheme, u.Seasonal, u.SeasonalEffects = p.UI.AccentColor, p.UI.ColorTheme, p.UI.Seasonal, p.UI.SeasonalEffects
	u.ClassicLook, u.TitleLanguage, u.BannerType = p.UI.ClassicLook, p.UI.TitleLanguage, p.UI.BannerType
	u.ShowEpisodeTitle, u.BlurUnwatched = p.UI.ShowEpisodeTitle, p.UI.BlurUnwatched
	pb.AutoPlayNext, pb.PreferredAudioLang, pb.PreferredSubLang = p.Playback.AutoPlayNext, p.Playback.PreferredAudioLang, p.Playback.PreferredSubLang
	pb.SkipIntroAniSkip, pb.SkipOutroAniSkip, pb.SkipRecapAniSkip = p.Playback.SkipIntroAniSkip, p.Playback.SkipOutroAniSkip, p.Playback.SkipRecapAniSkip
	return s
}
