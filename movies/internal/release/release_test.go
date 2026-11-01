package release

import (
	"reflect"
	"slices"
	"testing"
)

func ints(n ...int) []int       { return n }
func strs(s ...string) []string { return s }

// checkInfo compares the fields that must always match (title, year,
// episode numbers, group, extra...) and the technical fields want sets.
func checkInfo(t *testing.T, in string, got, want Info) {
	t.Helper()
	type field struct {
		name      string
		got, want any
		always    bool
	}
	fields := []field{
		{"Title", got.Title, want.Title, true},
		{"Year", got.Year, want.Year, true},
		{"Seasons", got.Seasons, want.Seasons, true},
		{"Episodes", got.Episodes, want.Episodes, true},
		{"Absolute", got.Absolute, want.Absolute, true},
		{"Date", got.Date, want.Date, true},
		{"EpisodeTitle", got.EpisodeTitle, want.EpisodeTitle, true},
		{"Complete", got.Complete, want.Complete, true},
		{"Extra", got.Extra, want.Extra, true},
		{"Group", got.Group, want.Group, true},
		{"Resolution", got.Resolution, want.Resolution, want.Resolution != ""},
		{"Source", got.Source, want.Source, want.Source != ""},
		{"Codec", got.Codec, want.Codec, want.Codec != ""},
		{"BitDepth", got.BitDepth, want.BitDepth, want.BitDepth != 0},
		{"HDR", got.HDR, want.HDR, want.HDR != nil},
		{"Audio", got.Audio, want.Audio, want.Audio != nil},
		{"Languages", got.Languages, want.Languages, want.Languages != nil},
		{"Edition", got.Edition, want.Edition, want.Edition != ""},
		{"Service", got.Service, want.Service, want.Service != ""},
		{"Repack", got.Repack, want.Repack, want.Repack},
		{"Proper", got.Proper, want.Proper, want.Proper},
		{"Part", got.Part, want.Part, want.Part != 0},
		{"Container", got.Container, want.Container, want.Container != ""},
		{"TMDBID", got.TMDBID, want.TMDBID, want.TMDBID != 0},
		{"IMDbID", got.IMDbID, want.IMDbID, want.IMDbID != ""},
		{"TVDBID", got.TVDBID, want.TVDBID, want.TVDBID != 0},
	}
	for _, f := range fields {
		if f.always && !reflect.DeepEqual(f.got, f.want) {
			t.Errorf("%q: %s = %#v, want %#v", in, f.name, f.got, f.want)
		}
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		in   string
		want Info
	}{
		// Scene movies.
		{"The.Matrix.1999.1080p.BluRay.x264-GROUP", Info{Title: "The Matrix", Year: 1999, Resolution: "1080p", Source: "BluRay", Codec: "x264", Group: "GROUP"}},
		{"The Matrix 1999 1080p BluRay x264-GROUP", Info{Title: "The Matrix", Year: 1999, Resolution: "1080p", Source: "BluRay", Codec: "x264", Group: "GROUP"}},
		{"The_Matrix_1999_1080p_BluRay_x264-GROUP", Info{Title: "The Matrix", Year: 1999, Resolution: "1080p", Source: "BluRay", Codec: "x264", Group: "GROUP"}},
		{"the.matrix.1999.1080p.bluray.x264-group", Info{Title: "the matrix", Year: 1999, Resolution: "1080p", Source: "BluRay", Codec: "x264", Group: "group"}},
		{"THE.MATRIX.1999.1080P.BLURAY.X264-GROUP", Info{Title: "THE MATRIX", Year: 1999, Resolution: "1080p", Source: "BluRay", Codec: "x264", Group: "GROUP"}},
		{"Avengers.Infinity.War.2018.1080p.BluRay.x264-SPARKS", Info{Title: "Avengers Infinity War", Year: 2018, Group: "SPARKS"}},
		{"Movie.Name.2019.1080p.WEBRip.x264-RARBG", Info{Title: "Movie Name", Year: 2019, Source: "WEBRip", Group: "RARBG"}},
		{"Spider-Man.No.Way.Home.2021.1080p.WEB-DL.DDP5.1.Atmos.H.264-CMRG", Info{Title: "Spider-Man No Way Home", Year: 2021, Source: "WEB-DL", Codec: "x264", Audio: strs("DDP5.1", "Atmos"), Group: "CMRG"}},
		{"Dune.Part.Two.2024.2160p.WEB-DL.DV.HDR10+.DDP5.1.Atmos.H.265-FLUX", Info{Title: "Dune Part Two", Year: 2024, Resolution: "2160p", Codec: "x265", HDR: strs("DV", "HDR10+"), Audio: strs("DDP5.1", "Atmos"), Group: "FLUX"}},
		{"Oppenheimer.2023.IMAX.2160p.WEB-DL.DDP5.1.Atmos.HDR.H.265-FLUX", Info{Title: "Oppenheimer", Year: 2023, Edition: "IMAX", HDR: strs("HDR"), Group: "FLUX"}},
		{"Movie.2019.2160p.WEB-DL.DDP5.1.Atmos.DV.HDR10.H.265-FLUX", Info{Title: "Movie", Year: 2019, HDR: strs("DV", "HDR10"), Audio: strs("DDP5.1", "Atmos"), Codec: "x265", Group: "FLUX"}},
		{"Parasite.2019.KOREAN.1080p.BluRay.x264.DTS-HD.MA.5.1-FGT", Info{Title: "Parasite", Year: 2019, Languages: strs("KOREAN"), Audio: strs("DTS-HD MA 5.1"), Group: "FGT"}},
		{"Movie Name 2019 1080p Remux AVC DTS-HD MA 5.1-GRP", Info{Title: "Movie Name", Year: 2019, Source: "Remux", Codec: "x264", Audio: strs("DTS-HD MA 5.1"), Group: "GRP"}},
		{"Movie Title 2019 4K UHD BluRay Remux HDR10 TrueHD Atmos 7.1-GRP", Info{Title: "Movie Title", Year: 2019, Resolution: "2160p", Source: "Remux", HDR: strs("HDR10"), Audio: strs("TrueHD7.1", "Atmos"), Group: "GRP"}},
		{"The Lord of the Rings The Fellowship of the Ring 2001 EXTENDED 2160p UHD BluRay x265 10bit HDR TrueHD 7.1 Atmos-GRP", Info{Title: "The Lord of the Rings The Fellowship of the Ring", Year: 2001, Edition: "Extended", Resolution: "2160p", Source: "BluRay", Codec: "x265", BitDepth: 10, HDR: strs("HDR"), Audio: strs("TrueHD7.1", "Atmos"), Group: "GRP"}},
		{"Movie.2019.COMPLETE.UHD.BLURAY-GRP", Info{Title: "Movie", Year: 2019, Resolution: "2160p", Source: "BluRay", Group: "GRP"}},
		{"Movie.2010.Part.2.DVDRip.XviD-GRP.avi", Info{Title: "Movie", Year: 2010, Part: 2, Source: "DVDRip", Codec: "XviD", Group: "GRP", Container: "avi"}},
		{"Movie (2010) CD1.avi", Info{Title: "Movie", Year: 2010, Part: 1, Container: "avi"}},
		{"Movie.Name.2019.1080p.BluRay.x264-GRP.mkv.part", Info{Title: "Movie Name", Year: 2019, Group: "GRP", Container: "mkv"}},
		{"Movie 1080p", Info{Title: "Movie", Resolution: "1080p"}},

		// Years in titles vs release years.
		{"Blade Runner 2049 (2017)", Info{Title: "Blade Runner 2049", Year: 2017}},
		{"Blade.Runner.2049.2017.1080p.BluRay.x264-SPARKS", Info{Title: "Blade Runner 2049", Year: 2017, Group: "SPARKS"}},
		{"2001 A Space Odyssey 1968", Info{Title: "2001 A Space Odyssey", Year: 1968}},
		{"2001.A.Space.Odyssey.1968.1080p.BluRay", Info{Title: "2001 A Space Odyssey", Year: 1968, Resolution: "1080p"}},
		{"1917 (2019)", Info{Title: "1917", Year: 2019}},
		{"1917.2019.1080p.BluRay.x264", Info{Title: "1917", Year: 2019}},
		{"1917.1080p.BluRay.x264", Info{Title: "1917", Resolution: "1080p"}},
		{"Se7en 1995", Info{Title: "Se7en", Year: 1995}},
		{"Se7en.1995.REMASTERED.1080p.BluRay", Info{Title: "Se7en", Year: 1995, Edition: "Remastered"}},
		{"2012 (2009)", Info{Title: "2012", Year: 2009}},
		{"1984 (1984)", Info{Title: "1984", Year: 1984}},
		{"Wonder Woman 1984 (2020)", Info{Title: "Wonder Woman 1984", Year: 2020}},
		{"Wonder.Woman.1984.2020.2160p.WEB-DL", Info{Title: "Wonder Woman 1984", Year: 2020}},
		{"Blade.Runner.1982.The.Final.Cut.REMASTERED.1080p.BluRay.x264-GRP", Info{Title: "Blade Runner", Year: 1982, Edition: "Final Cut, Remastered", Group: "GRP"}},
		{"Fallout 76", Info{Title: "Fallout 76"}},
		{"Godzilla x Kong The New Empire 2024", Info{Title: "Godzilla x Kong The New Empire", Year: 2024}},

		// Titles that look like tags or episodes.
		{"Ocean's Eleven", Info{Title: "Ocean's Eleven"}},
		{"Ocean's 8 (2018)", Info{Title: "Ocean's 8", Year: 2018}},
		{"Oceans.8.2018.1080p.BluRay", Info{Title: "Oceans 8", Year: 2018}},
		{"300 (2006)", Info{Title: "300", Year: 2006}},
		{"300.2006.1080p.BluRay", Info{Title: "300", Year: 2006}},
		{"Saw X (2023)", Info{Title: "Saw X", Year: 2023}},
		{"Fast X (2023)", Info{Title: "Fast X", Year: 2023}},
		{"Kill Bill Vol. 2 (2004)", Info{Title: "Kill Bill Vol. 2", Year: 2004}},
		{"Kill.Bill.Vol.2.2004.1080p.BluRay.x264", Info{Title: "Kill Bill Vol 2", Year: 2004}},
		{"Kill Bill Vol 2", Info{Title: "Kill Bill Vol 2"}},
		{"Harry Potter and the Deathly Hallows Part 1 (2010)", Info{Title: "Harry Potter and the Deathly Hallows Part 1", Year: 2010}},
		{"Star Wars Episode 4 A New Hope (1977)", Info{Title: "Star Wars Episode 4 A New Hope", Year: 1977}},
		{"Star.Wars.Episode.IV.A.New.Hope.1977.1080p", Info{Title: "Star Wars Episode IV A New Hope", Year: 1977}},
		{"Mission Impossible - Fallout (2018)", Info{Title: "Mission Impossible - Fallout", Year: 2018}},
		{"Mission: Impossible – Dead Reckoning Part One (2023)", Info{Title: "Mission: Impossible - Dead Reckoning Part One", Year: 2023}},
		{"Spider-Man (2002)", Info{Title: "Spider-Man", Year: 2002}},
		{"Rocky II (1979)", Info{Title: "Rocky II", Year: 1979}},
		{"Rocky.IV.1985.1080p", Info{Title: "Rocky IV", Year: 1985}},
		{"Rambo III 1988", Info{Title: "Rambo III", Year: 1988}},
		{"Jackass 2.5 (2007)", Info{Title: "Jackass 2.5", Year: 2007}},
		{"(500) Days of Summer (2009)", Info{Title: "(500) Days of Summer", Year: 2009}},
		{"Alien³ (1992)", Info{Title: "Alien³", Year: 1992}},
		{"Alien vs. Predator (2004)", Info{Title: "Alien vs. Predator", Year: 2004}},
		{"Wallace & Gromit 1989", Info{Title: "Wallace & Gromit", Year: 1989}},
		{"Amélie.2001.FRENCH.1080p.BluRay.x264-GRP", Info{Title: "Amélie", Year: 2001, Languages: strs("FRENCH"), Group: "GRP"}},
		{"The Big Short (2015)", Info{Title: "The Big Short", Year: 2015}},
		{"Charlotte's Web (2006)", Info{Title: "Charlotte's Web", Year: 2006}},
		{"charlotte's web 2006 720p", Info{Title: "charlotte's web", Year: 2006, Resolution: "720p"}},
		{"The Web (2020)", Info{Title: "The Web", Year: 2020}},
		{"The Interview (2014)", Info{Title: "The Interview", Year: 2014}},
		{"The French Connection (1971)", Info{Title: "The French Connection", Year: 1971}},
		{"The Italian Job (2003)", Info{Title: "The Italian Job", Year: 2003}},
		{"The Italian 2020 1080p", Info{Title: "The Italian", Year: 2020, Resolution: "1080p"}},
		{"Cam (2018)", Info{Title: "Cam", Year: 2018}},
		{"Cam.2018.1080p.NF.WEB-DL", Info{Title: "Cam", Year: 2018, Service: "NF", Source: "WEB-DL"}},
		{"Short Circuit (1986)", Info{Title: "Short Circuit", Year: 1986}},
		{"The Extra (2019)", Info{Title: "The Extra", Year: 2019}},
		{"Extras (2005)", Info{Title: "Extras", Year: 2005}},
		{"Proper Movie 2019 1080p", Info{Title: "Proper Movie", Year: 2019}},
		{"Unrated Movie 2019", Info{Title: "Unrated Movie", Year: 2019}},
		{"Apocalypse Now Redux (2001)", Info{Title: "Apocalypse Now Redux", Year: 2001}},
		{"Apocalypse.Now.1979.REDUX.1080p.BluRay.x264", Info{Title: "Apocalypse Now", Year: 1979, Edition: "Redux"}},
		{"Uncut Gems (2019)", Info{Title: "Uncut Gems", Year: 2019}},
		{"Extended Family (2023)", Info{Title: "Extended Family", Year: 2023}},
		{"Final Cut (2004)", Info{Title: "Final Cut", Year: 2004}},
		{"Love, Death & Robots S01E01", Info{Title: "Love, Death & Robots", Seasons: ints(1), Episodes: ints(1)}},
		{"S1m0ne (2002)", Info{Title: "S1m0ne", Year: 2002}},
		{"S Darko (2009)", Info{Title: "S Darko", Year: 2009}},
		{"S.O.S. Titanic 1979", Info{Title: "S.O.S. Titanic", Year: 1979}},
		{"E.T. the Extra-Terrestrial (1982)", Info{Title: "E.T. the Extra-Terrestrial", Year: 1982}},
		{"M3GAN (2022)", Info{Title: "M3GAN", Year: 2022}},
		{"Apollo 13 (1995)", Info{Title: "Apollo 13", Year: 1995}},
		{"The Season (2012)", Info{Title: "The Season", Year: 2012}},
		{"1883 S01E01", Info{Title: "1883", Seasons: ints(1), Episodes: ints(1)}},
		{"1923 (2022) S01E01", Info{Title: "1923", Year: 2022, Seasons: ints(1), Episodes: ints(1)}},
		{"The 4400 S01E01", Info{Title: "The 4400", Seasons: ints(1), Episodes: ints(1)}},

		// Acronyms.
		{"S.W.A.T.2017.S01E01.720p", Info{Title: "S.W.A.T.", Year: 2017, Seasons: ints(1), Episodes: ints(1)}},
		{"Marvel's.Agents.of.S.H.I.E.L.D.S01E01.720p.HDTV", Info{Title: "Marvel's Agents of S.H.I.E.L.D.", Seasons: ints(1), Episodes: ints(1), Source: "HDTV"}},
		{"Marvel's Agents of S.H.I.E.L.D. S01E01", Info{Title: "Marvel's Agents of S.H.I.E.L.D.", Seasons: ints(1), Episodes: ints(1)}},
		{"A.I.Artificial.Intelligence.2001.1080p", Info{Title: "A.I. Artificial Intelligence", Year: 2001}},
		{"The.A.Team.2010.1080p", Info{Title: "The A Team", Year: 2010}},
		{"House M.D. S01E01", Info{Title: "House M.D.", Seasons: ints(1), Episodes: ints(1)}},
		{"Mr. Robot S01E01", Info{Title: "Mr. Robot", Seasons: ints(1), Episodes: ints(1)}},
		{"Mr.Robot.S01E01.720p.WEB-DL", Info{Title: "Mr Robot", Seasons: ints(1), Episodes: ints(1)}},

		// Episodes.
		{"9-1-1 S01E01", Info{Title: "9-1-1", Seasons: ints(1), Episodes: ints(1)}},
		{"9-1-1.S07E05.1080p.WEB.h264-ETHEL", Info{Title: "9-1-1", Seasons: ints(7), Episodes: ints(5), Group: "ETHEL"}},
		{"The 100 S02E03", Info{Title: "The 100", Seasons: ints(2), Episodes: ints(3)}},
		{"Doctor Who (2005) S01E01", Info{Title: "Doctor Who", Year: 2005, Seasons: ints(1), Episodes: ints(1)}},
		{"Doctor.Who.2005.S01E01.Rose.720p", Info{Title: "Doctor Who", Year: 2005, Seasons: ints(1), Episodes: ints(1), EpisodeTitle: "Rose"}},
		{"House.of.the.Dragon.S02E01.2160p.WEB.H265-GROUP", Info{Title: "House of the Dragon", Seasons: ints(2), Episodes: ints(1), Resolution: "2160p", Source: "WEB-DL", Codec: "x265", Group: "GROUP"}},
		{"Game.of.Thrones.S08E06.The.Iron.Throne.1080p.AMZN.WEB-DL.DDP5.1.H.264-GoT", Info{Title: "Game of Thrones", Seasons: ints(8), Episodes: ints(6), EpisodeTitle: "The Iron Throne", Service: "AMZN", Audio: strs("DDP5.1"), Group: "GoT"}},
		{"Better.Call.Saul.S06E13.Saul.Gone.REPACK.1080p.AMC.WEB-DL.DDP5.1.H.264-NTb", Info{Title: "Better Call Saul", Seasons: ints(6), Episodes: ints(13), EpisodeTitle: "Saul Gone", Repack: true, Group: "NTb"}},
		{"The.Office.US.S05E14.Stress.Relief.720p.BluRay.x264", Info{Title: "The Office US", Seasons: ints(5), Episodes: ints(14), EpisodeTitle: "Stress Relief"}},
		{"The.Office.(US).S02E01.720p", Info{Title: "The Office (US)", Seasons: ints(2), Episodes: ints(1)}},
		{"The.Last.of.Us.S01E03.Long.Long.Time.2160p.HMAX.WEB-DL.DDPA5.1.DV.HDR.H.265-GRP", Info{Title: "The Last of Us", Seasons: ints(1), Episodes: ints(3), EpisodeTitle: "Long Long Time", Service: "HMAX", Audio: strs("DDP5.1", "Atmos"), HDR: strs("DV", "HDR"), Group: "GRP"}},
		{"Show.S01E05.Pilot.REPACK.1080p.WEB.x264-GRP", Info{Title: "Show", Seasons: ints(1), Episodes: ints(5), EpisodeTitle: "Pilot", Repack: true, Group: "GRP"}},
		{"Show.S01E05.The.Proper.Way.1080p.WEB.x264-GRP", Info{Title: "Show", Seasons: ints(1), Episodes: ints(5), EpisodeTitle: "The Proper Way", Group: "GRP"}},
		{"Show.S01E05.PROPER.1080p.WEB.x264-GRP", Info{Title: "Show", Seasons: ints(1), Episodes: ints(5), Proper: true, Group: "GRP"}},
		{"Show - S01E01 - Pilot [WEBDL-1080p][EAC3 5.1][h264]-GROUP", Info{Title: "Show", Seasons: ints(1), Episodes: ints(1), EpisodeTitle: "Pilot", Resolution: "1080p", Source: "WEB-DL", Audio: strs("DDP5.1"), Codec: "x264", Group: "GROUP"}},
		{"Show (2008) - S01E01 - Pilot.mkv", Info{Title: "Show", Year: 2008, Seasons: ints(1), Episodes: ints(1), EpisodeTitle: "Pilot", Container: "mkv"}},
		{"Show S01E01 - Spider-Man", Info{Title: "Show", Seasons: ints(1), Episodes: ints(1), EpisodeTitle: "Spider-Man"}},
		{"Show.S01E01.HDTV.XviD-LOL.avi", Info{Title: "Show", Seasons: ints(1), Episodes: ints(1), Source: "HDTV", Codec: "XviD", Group: "LOL", Container: "avi"}},
		{"Show.S01E01.720p.HDTV.x264-KILLERS[eztv]", Info{Title: "Show", Seasons: ints(1), Episodes: ints(1), Group: "KILLERS"}},
		{"Show S01E01 720p HDTV x264-SVA [eztv]", Info{Title: "Show", Seasons: ints(1), Episodes: ints(1), Group: "SVA"}},
		{"Show.Name.S01E01.1080p.WEB.h264-GRP[TGx]", Info{Title: "Show Name", Seasons: ints(1), Episodes: ints(1), Group: "GRP"}},
		{"Show S01E01 [1080p] [x265] [10bit]", Info{Title: "Show", Seasons: ints(1), Episodes: ints(1), Resolution: "1080p", Codec: "x265", BitDepth: 10}},
		{"Show.2024.S01E01.1080p", Info{Title: "Show", Year: 2024, Seasons: ints(1), Episodes: ints(1)}},
		{"Grey's Anatomy S20E01", Info{Title: "Grey's Anatomy", Seasons: ints(20), Episodes: ints(1)}},
		{"Law & Order: SVU S25E01", Info{Title: "Law & Order: SVU", Seasons: ints(25), Episodes: ints(1)}},
		{"Pokémon.S01E01.Pikachu.I.Choose.You.480p", Info{Title: "Pokémon", Seasons: ints(1), Episodes: ints(1), EpisodeTitle: "Pikachu I Choose You", Resolution: "480p"}},
		{"La.Casa.de.Papel.S01E01.SPANiSH.720p.WEB-DL", Info{Title: "La Casa de Papel", Seasons: ints(1), Episodes: ints(1), Languages: strs("SPANiSH")}},
		{"Dark.S01E01.GERMAN.DL.1080p.WEB.x264-GRP", Info{Title: "Dark", Seasons: ints(1), Episodes: ints(1), Languages: strs("GERMAN", "DUAL"), Group: "GRP"}},
		{"Extras S01E01", Info{Title: "Extras", Seasons: ints(1), Episodes: ints(1)}},
		{"Trailer Park Boys S01E01", Info{Title: "Trailer Park Boys", Seasons: ints(1), Episodes: ints(1)}},
		{"Show.S2024E05.1080p", Info{Title: "Show", Seasons: ints(2024), Episodes: ints(5)}},
		{"Show S00E03 Special", Info{Title: "Show", Seasons: ints(0), Episodes: ints(3), EpisodeTitle: "Special"}},
		{"Show.S01E05v2.720p", Info{Title: "Show", Seasons: ints(1), Episodes: ints(5)}},
		{"Show s01e05", Info{Title: "Show", Seasons: ints(1), Episodes: ints(5)}},
		{"Show S1E5", Info{Title: "Show", Seasons: ints(1), Episodes: ints(5)}},
		{"Show S01 E05", Info{Title: "Show", Seasons: ints(1), Episodes: ints(5)}},
		{"Show.S01.E05.720p", Info{Title: "Show", Seasons: ints(1), Episodes: ints(5)}},
		{"S01E05.mkv", Info{Seasons: ints(1), Episodes: ints(5), Container: "mkv"}},
		{"S01E05 - Pilot.mkv", Info{Seasons: ints(1), Episodes: ints(5), EpisodeTitle: "Pilot", Container: "mkv"}},
		{"Mr. Robot S01E01 eps1.0_hellofriend.mov 720p", Info{Title: "Mr. Robot", Seasons: ints(1), Episodes: ints(1), Resolution: "720p"}},

		// Other episode notations.
		{"Show 1x01-1x02 Title", Info{Title: "Show", Seasons: ints(1), Episodes: ints(1, 2), EpisodeTitle: "Title"}},
		{"Show - 1x05 - Title.avi", Info{Title: "Show", Seasons: ints(1), Episodes: ints(5), EpisodeTitle: "Title", Container: "avi"}},
		{"Show 01x05 Title", Info{Title: "Show", Seasons: ints(1), Episodes: ints(5), EpisodeTitle: "Title"}},
		{"Show.3x07-08.720p", Info{Title: "Show", Seasons: ints(3), Episodes: ints(7, 8)}},
		{"Show Season 2 Episode 5", Info{Title: "Show", Seasons: ints(2), Episodes: ints(5)}},
		{"Show.Season.2.Episode.5.720p", Info{Title: "Show", Seasons: ints(2), Episodes: ints(5)}},
		{"Show Saison 2 Episode 5 VOSTFR", Info{Title: "Show", Seasons: ints(2), Episodes: ints(5), Languages: strs("VOSTFR")}},
		{"Show Staffel 1 Folge 3", Info{Title: "Show", Seasons: ints(1), Episodes: ints(3)}},
		{"Show Temporada 2 Episodio 4", Info{Title: "Show", Seasons: ints(2), Episodes: ints(4)}},
		{"Show S2 - 05 [1080p]", Info{Title: "Show", Seasons: ints(2), Episodes: ints(5), Resolution: "1080p"}},
		{"Show Season 3 - 05 [1080p]", Info{Title: "Show", Seasons: ints(3), Episodes: ints(5)}},
		{"Show 2nd Season - 07 [1080p]", Info{Title: "Show", Seasons: ints(2), Episodes: ints(7)}},
		{"Show S01 Ep 05", Info{Title: "Show", Seasons: ints(1), Episodes: ints(5)}},

		// Multi-episode files.
		{"Show.S01E01E02.720p.HDTV.x264-GRP", Info{Title: "Show", Seasons: ints(1), Episodes: ints(1, 2), Group: "GRP"}},
		{"Show.S01E01-E03.1080p.WEB-DL", Info{Title: "Show", Seasons: ints(1), Episodes: ints(1, 2, 3)}},
		{"Show.S01E01-03.720p", Info{Title: "Show", Seasons: ints(1), Episodes: ints(1, 2, 3)}},
		{"Show.S01E01-02.720p", Info{Title: "Show", Seasons: ints(1), Episodes: ints(1, 2)}},
		{"Show S03E10E11E12 720p", Info{Title: "Show", Seasons: ints(3), Episodes: ints(10, 11, 12)}},
		{"Show.S01E01.S01E02.720p", Info{Title: "Show", Seasons: ints(1), Episodes: ints(1, 2)}},
		{"Show.S01E01-S01E03.720p", Info{Title: "Show", Seasons: ints(1), Episodes: ints(1, 2, 3)}},
		{"Show S01E01 E02", Info{Title: "Show", Seasons: ints(1), Episodes: ints(1, 2)}},
		{"Show S01E09-E10 1080p", Info{Title: "Show", Seasons: ints(1), Episodes: ints(9, 10)}},
		{"Show.S01E01.1080p-GRP", Info{Title: "Show", Seasons: ints(1), Episodes: ints(1), Group: "GRP"}},
		{"Show E05-E06", Info{Title: "Show", Episodes: ints(5, 6)}},
		{"Show E01-E03", Info{Title: "Show", Episodes: ints(1, 2, 3)}},
		{"Show Ep 01-12 [Batch]", Info{Title: "Show", Episodes: ints(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12), Complete: true}},

		// Season packs.
		{"Show.S01.1080p.BluRay.x264-GRP", Info{Title: "Show", Seasons: ints(1), Complete: true, Group: "GRP"}},
		{"Show S01", Info{Title: "Show", Seasons: ints(1), Complete: true}},
		{"Show.S01-S05.1080p", Info{Title: "Show", Seasons: ints(1, 2, 3, 4, 5), Complete: true}},
		{"Show S01-05 720p", Info{Title: "Show", Seasons: ints(1, 2, 3, 4, 5), Complete: true}},
		{"Show S01 - S03 720p", Info{Title: "Show", Seasons: ints(1, 2, 3), Complete: true}},
		{"Show S01 S02 S03", Info{Title: "Show", Seasons: ints(1, 2, 3), Complete: true}},
		{"Show Season 1-3 Complete 720p", Info{Title: "Show", Seasons: ints(1, 2, 3), Complete: true}},
		{"Show Season 1", Info{Title: "Show", Seasons: ints(1), Complete: true}},
		{"Show Seasons 1 to 4", Info{Title: "Show", Seasons: ints(1, 2, 3, 4), Complete: true}},
		{"Show Season 1 & 2", Info{Title: "Show", Seasons: ints(1, 2), Complete: true}},
		{"Seasons 1, 2 & 3", Info{Seasons: ints(1, 2, 3), Complete: true}},
		{"Show (2010) Seasons 1-6 1080p", Info{Title: "Show", Year: 2010, Seasons: ints(1, 2, 3, 4, 5, 6), Complete: true}},
		{"The Wire Season 1-5 Complete", Info{Title: "The Wire", Seasons: ints(1, 2, 3, 4, 5), Complete: true}},
		{"Breaking Bad S01-S05 + Extras", Info{Title: "Breaking Bad", Seasons: ints(1, 2, 3, 4, 5), Complete: true}},
		{"Planet Earth II (2016) S01 2160p", Info{Title: "Planet Earth II", Year: 2016, Seasons: ints(1), Complete: true}},
		{"Stranger Things S04 COMPLETE 1080p NF WEB-DL DDP5.1 x264-GRP", Info{Title: "Stranger Things", Seasons: ints(4), Complete: true, Service: "NF", Group: "GRP"}},
		{"Show.S01.COMPLETE.1080p.WEB", Info{Title: "Show", Seasons: ints(1), Complete: true}},
		{"Friends.The.Complete.Series.1080p.BluRay.x265-GRP", Info{Title: "Friends", Complete: true, Group: "GRP"}},
		{"Show Complete Series 720p", Info{Title: "Show", Complete: true}},
		{"Show.COMPLETE.720p.WEB-DL", Info{Title: "Show", Complete: true}},
		{"Show 1st Season", Info{Title: "Show", Seasons: ints(1), Complete: true}},
		{"[Judas] Show (Season 2) [1080p][HEVC x265 10bit][Eng-Subs]", Info{Title: "Show", Seasons: ints(2), Complete: true, Codec: "x265", BitDepth: 10, Group: "Judas"}},

		// Daily shows.
		{"The.Daily.Show.2024.01.02.720p", Info{Title: "The Daily Show", Date: "2024-01-02", Resolution: "720p"}},
		{"The.Daily.Show.2024.01.02.720p.WEB.h264-GRP", Info{Title: "The Daily Show", Date: "2024-01-02", Group: "GRP"}},
		{"The Daily Show 2024-01-02", Info{Title: "The Daily Show", Date: "2024-01-02"}},
		{"The.Tonight.Show.Starring.Jimmy.Fallon.2024.03.15.Guest.Name.720p.WEB.h264-GRP", Info{Title: "The Tonight Show Starring Jimmy Fallon", Date: "2024-03-15", EpisodeTitle: "Guest Name", Group: "GRP"}},
		{"Coronation.Street.15.03.2024.720p", Info{Title: "Coronation Street", Date: "2024-03-15"}},
		{"UFC.300.2024.04.13.PPV.1080p", Info{Title: "UFC 300", Date: "2024-04-13", Source: "PPV"}},
		{"Show.2023.02.30.720p", Info{Title: "Show", Year: 2023}},

		// Absolute numbers.
		{"One Piece - 1050 [1080p]", Info{Title: "One Piece", Absolute: 1050, Resolution: "1080p"}},
		{"[SubsPlease] Show - 05 (1080p) [ABCD1234].mkv", Info{Title: "Show", Absolute: 5, Resolution: "1080p", Group: "SubsPlease", Container: "mkv"}},
		{"[Erai-raws] Show - 01 [1080p][Multiple Subtitle].mkv", Info{Title: "Show", Absolute: 1, Group: "Erai-raws"}},
		{"[SubsPlease] Kaiju No. 8 - 05 (1080p)", Info{Title: "Kaiju No. 8", Absolute: 5, Group: "SubsPlease"}},
		{"Show - 05v2 [720p]", Info{Title: "Show", Absolute: 5}},
		{"Show - 105 - Title", Info{Title: "Show", Absolute: 105, EpisodeTitle: "Title"}},
		{"Show - Episode 105", Info{Title: "Show", Absolute: 105}},
		{"Show Ep 12", Info{Title: "Show", Absolute: 12}},
		{"Show Ep.12.720p", Info{Title: "Show", Absolute: 12}},
		{"Show.E05.720p", Info{Title: "Show", Absolute: 5}},
		{"Show Episode 5", Info{Title: "Show", Absolute: 5}},
		{"Show - 01-12 [Batch]", Info{Title: "Show", Episodes: ints(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12), Complete: true}},
		{"Movie - 2 (2019)", Info{Title: "Movie - 2", Year: 2019}},

		// Groups.
		{"The Matrix (1999) [1080p] [BluRay] [5.1] [YTS.MX]", Info{Title: "The Matrix", Year: 1999, Resolution: "1080p", Source: "BluRay", Audio: strs("5.1"), Group: "YTS.MX"}},
		{"Movie Name (2019) [2160p] [4K] [WEB] [5.1] [YTS.MX]", Info{Title: "Movie Name", Year: 2019, Resolution: "2160p", Source: "WEB-DL", Group: "YTS.MX"}},
		{"Movie (2019) [WEBRip] [1080p] [YTS.AM]", Info{Title: "Movie", Year: 2019, Source: "WEBRip", Group: "YTS.AM"}},
		{"Movie.2019.1080p.BluRay.x264-[YTS.AG]", Info{Title: "Movie", Year: 2019, Group: "YTS.AG"}},
		{"[YTS.MX] Movie (2019) [1080p]", Info{Title: "Movie", Year: 2019, Group: "YTS.MX"}},
		{"Inception (2010) 1080p BrRip x264 - YIFY", Info{Title: "Inception", Year: 2010, Source: "BDRip", Group: "YIFY"}},
		{"Movie (2019) (YIFY)", Info{Title: "Movie", Year: 2019, Group: "YIFY"}},
		{"Movie (2019) (Remake)", Info{Title: "Movie", Year: 2019}},
		{"Movie.2019.1080p.WEB-DL.H264.AC3-EVO[EtHD]", Info{Title: "Movie", Year: 2019, Audio: strs("DD"), Group: "EVO"}},
		{"Movie 2019 720p BluRay x264-[MkvCage.com]", Info{Title: "Movie", Year: 2019}},
		{"Movie.2019.1080p.WEB-DL", Info{Title: "Movie", Year: 2019, Source: "WEB-DL"}},
		{"Movie.2019.1080p.BluRay.DTS-HD", Info{Title: "Movie", Year: 2019, Audio: strs("DTS-HD")}},
		{"Movie.2019-GRP", Info{Title: "Movie", Year: 2019, Group: "GRP"}},
		{"Movie.2019.1080p.BluRay.x264-FiN", Info{Title: "Movie", Year: 2019, Group: "FiN"}},
		{"www.Torrenting.com - Movie.2019.1080p.WEBRip.x264-GRP", Info{Title: "Movie", Year: 2019, Group: "GRP"}},
		{"[ www.UIndex.org ] - Movie 2019 1080p", Info{Title: "Movie", Year: 2019}},
		{"[Torrent911.ws] Movie 2019 1080p", Info{Title: "Movie", Year: 2019}},

		// Languages, editions, flags.
		{"Movie.2019.MULTi.1080p.BluRay.x264-GRP", Info{Title: "Movie", Year: 2019, Languages: strs("MULTi"), Group: "GRP"}},
		{"Le.Film.FRENCH.1080p.WEB.x264-GRP", Info{Title: "Le Film", Languages: strs("FRENCH"), Group: "GRP"}},
		{"Movie.Name.2019.iTA.ENG.1080p.BluRay", Info{Title: "Movie Name", Year: 2019, Languages: strs("iTA", "ENGLISH")}},
		{"Movie.Name.2019.TRUEFRENCH.1080p", Info{Title: "Movie Name", Year: 2019, Languages: strs("TRUEFRENCH")}},
		{"Movie.Name.2019.VFF.1080p", Info{Title: "Movie Name", Year: 2019, Languages: strs("VFF")}},
		{"Movie.Name.2019.Dual.Audio.1080p", Info{Title: "Movie Name", Year: 2019, Languages: strs("DUAL")}},
		{"Movie.Name.2019.Hindi.1080p", Info{Title: "Movie Name", Year: 2019, Languages: strs("HINDI")}},
		{"Movie.Name.2019.LATiNO.1080p", Info{Title: "Movie Name", Year: 2019, Languages: strs("LATiNO")}},
		{"Movie (2019) 1080p H264 AC3 (Ita Eng)", Info{Title: "Movie", Year: 2019, Languages: strs("iTA", "ENGLISH")}},
		{"Movie.2009.EXTENDED.1080p.BluRay.x264-GRP", Info{Title: "Movie", Year: 2009, Edition: "Extended", Group: "GRP"}},
		{"Movie.EXTENDED.1080p.BluRay.x264-GRP", Info{Title: "Movie", Edition: "Extended", Group: "GRP"}},
		{"Movie Name 2019 Directors Cut 1080p", Info{Title: "Movie Name", Year: 2019, Edition: "Director's Cut"}},
		{"Movie Name 2019 Director's Cut 1080p", Info{Title: "Movie Name", Year: 2019, Edition: "Director's Cut"}},
		{"Movie Name (Director's Cut) (2019)", Info{Title: "Movie Name", Year: 2019, Edition: "Director's Cut"}},
		{"Movie.Name.2019.UNCUT.1080p", Info{Title: "Movie Name", Year: 2019, Edition: "Uncut"}},
		{"Movie.Name.2019.Theatrical.Cut.1080p", Info{Title: "Movie Name", Year: 2019, Edition: "Theatrical"}},
		{"Movie.Name.2019.Criterion.1080p", Info{Title: "Movie Name", Year: 2019, Edition: "Criterion"}},
		{"Movie.Name.2019.Open.Matte.1080p", Info{Title: "Movie Name", Year: 2019, Edition: "Open Matte"}},
		{"Movie.Name.2019.Anniversary.Edition.1080p", Info{Title: "Movie Name", Year: 2019, Edition: "Anniversary Edition"}},
		{"Aliens.1986.Special.Edition.1080p.BluRay", Info{Title: "Aliens", Year: 1986, Edition: "Special Edition"}},
		{"Movie.2019.PROPER.1080p", Info{Title: "Movie", Year: 2019, Proper: true}},
		{"Movie.2019.REAL.PROPER.1080p", Info{Title: "Movie", Year: 2019, Proper: true}},
		{"Movie.Name.2019.REPACK.1080p", Info{Title: "Movie Name", Year: 2019, Repack: true}},
		{"Movie.Name.2019.RERIP.1080p", Info{Title: "Movie Name", Year: 2019, Repack: true}},
		{"Movie.2019.iNTERNAL.1080p", Info{Title: "Movie", Year: 2019}},
		{"Movie.2019.LIMITED.1080p.BluRay", Info{Title: "Movie", Year: 2019, Source: "BluRay"}},

		// Sources, codecs, resolutions, audio.
		{"Movie.Name.2019.BDRemux.1080p", Info{Title: "Movie Name", Year: 2019, Source: "Remux"}},
		{"Movie.Name.2019.BRRip.720p", Info{Title: "Movie Name", Year: 2019, Source: "BDRip"}},
		{"Movie.Name.2019.BDRip.720p", Info{Title: "Movie Name", Year: 2019, Source: "BDRip"}},
		{"Movie.Name.2019.HDCAM.x264", Info{Title: "Movie Name", Year: 2019, Source: "CAM"}},
		{"Movie.Name.2019.CAM.x264", Info{Title: "Movie Name", Year: 2019, Source: "CAM"}},
		{"Movie.Name.2019.TELESYNC.x264", Info{Title: "Movie Name", Year: 2019, Source: "TS"}},
		{"Movie.Name.2019.HDTS.x264", Info{Title: "Movie Name", Year: 2019, Source: "TS"}},
		{"Movie.Name.2019.TS", Info{Title: "Movie Name", Year: 2019, Source: "TS"}},
		{"Movie.Name.2019.TC.XviD", Info{Title: "Movie Name", Year: 2019, Source: "TC", Codec: "XviD"}},
		{"Movie.Name.2019.DVDScr.XviD", Info{Title: "Movie Name", Year: 2019, Source: "SCR", Codec: "XviD"}},
		{"Movie.Name.2019.DVDRip.XviD", Info{Title: "Movie Name", Year: 2019, Source: "DVDRip", Codec: "XviD"}},
		{"Movie.Name.2019.HDRip.XviD", Info{Title: "Movie Name", Year: 2019, Source: "HDRip", Codec: "XviD"}},
		{"Movie.Name.2019.720p.HDTV.x264", Info{Title: "Movie Name", Year: 2019, Resolution: "720p", Source: "HDTV"}},
		{"Movie.Name.2019.576p.BluRay", Info{Title: "Movie Name", Year: 2019, Resolution: "576p"}},
		{"Movie.Name.2019.480p.DVD", Info{Title: "Movie Name", Year: 2019, Resolution: "480p", Source: "DVD"}},
		{"Movie.Name.2019.1080i.HDTV", Info{Title: "Movie Name", Year: 2019, Resolution: "1080p"}},
		{"Movie.Name.2019.1920x1080.BluRay", Info{Title: "Movie Name", Year: 2019, Resolution: "1080p"}},
		{"Movie.Name.2019.UHD.BluRay.2160p", Info{Title: "Movie Name", Year: 2019, Resolution: "2160p", Source: "BluRay"}},
		{"Movie Name 2019 4K HDR", Info{Title: "Movie Name", Year: 2019, Resolution: "2160p", HDR: strs("HDR")}},
		{"Movie.Name.2019.AV1.1080p", Info{Title: "Movie Name", Year: 2019, Codec: "AV1"}},
		{"Movie.Name.2019.1080p.VP9", Info{Title: "Movie Name", Year: 2019, Codec: "VP9"}},
		{"Movie.Name.2019.1080p.HEVC", Info{Title: "Movie Name", Year: 2019, Codec: "x265"}},
		{"Movie.Name.2019.1080p.WEB-DL.AAC2.0.H.264", Info{Title: "Movie Name", Year: 2019, Audio: strs("AAC2.0"), Codec: "x264"}},
		{"Movie.Name.2019.1080p.WEB-DL.DD5.1.H.264", Info{Title: "Movie Name", Year: 2019, Audio: strs("DD5.1"), Codec: "x264"}},
		{"Movie.Name.2019.1080p.WEB-DL.DD+5.1.H.264", Info{Title: "Movie Name", Year: 2019, Audio: strs("DDP5.1")}},
		{"Movie.Name.2019.1080p.BluRay.FLAC.2.0.x264", Info{Title: "Movie Name", Year: 2019, Audio: strs("FLAC2.0")}},
		{"Movie.Name.2019.1080p.BluRay.Opus.5.1.x265", Info{Title: "Movie Name", Year: 2019, Audio: strs("Opus5.1")}},
		{"Movie.Name.2019.1080p.BluRay.DTS.x264", Info{Title: "Movie Name", Year: 2019, Audio: strs("DTS")}},
		{"Movie.Name.2019.1080p.BluRay.DTS-X.7.1.x264", Info{Title: "Movie Name", Year: 2019, Audio: strs("DTS:X 7.1")}},
		{"Movie.Name.2019.2160p.HDR10Plus.x265", Info{Title: "Movie Name", Year: 2019, HDR: strs("HDR10+")}},
		{"Movie.Name.2019.2160p.DoVi.x265", Info{Title: "Movie Name", Year: 2019, HDR: strs("DV")}},
		{"Movie.Name.2019.2160p.Dolby.Vision.x265", Info{Title: "Movie Name", Year: 2019, HDR: strs("DV")}},
		{"Movie.Name.(2019).[1080p].[WEBRip].[x264].[AAC].[5.1]", Info{Title: "Movie Name", Year: 2019, Audio: strs("AAC5.1")}},
		{"Show.S01E01.TrueHD.Atmos.7.1.mkv", Info{Title: "Show", Seasons: ints(1), Episodes: ints(1), Audio: strs("TrueHD7.1", "Atmos")}},

		// Ids and Plex editions.
		{"The Matrix (1999) {tmdb-603}", Info{Title: "The Matrix", Year: 1999, TMDBID: 603}},
		{"The Matrix (1999) [tmdbid-603]", Info{Title: "The Matrix", Year: 1999, TMDBID: 603}},
		{"The Matrix (1999) [tmdbid=603]", Info{Title: "The Matrix", Year: 1999, TMDBID: 603}},
		{"The Matrix (1999) {imdb-tt0133093}", Info{Title: "The Matrix", Year: 1999, IMDbID: "tt0133093"}},
		{"The Matrix (1999) [imdbid-tt0133093]", Info{Title: "The Matrix", Year: 1999, IMDbID: "tt0133093"}},
		{"The Matrix (1999) [tt0133093]", Info{Title: "The Matrix", Year: 1999, IMDbID: "tt0133093"}},
		{"Show (2008) {tvdb-81189}", Info{Title: "Show", Year: 2008, TVDBID: 81189}},
		{"Show (2008) [tvdbid-81189] - S01E01", Info{Title: "Show", Year: 2008, TVDBID: 81189, Seasons: ints(1), Episodes: ints(1)}},
		{"Movie (2019) {edition-Director's Cut}", Info{Title: "Movie", Year: 2019, Edition: "Director's Cut"}},
		{"Movie (2019) {edition-Final Cut} {tmdb-1}.mkv", Info{Title: "Movie", Year: 2019, Edition: "Final Cut", TMDBID: 1, Container: "mkv"}},

		// Extras and samples.
		{"Movie.Name.2019.1080p.BluRay.x264-GRP-sample.mkv", Info{Title: "Movie Name", Year: 2019, Group: "GRP", Extra: "sample", Container: "mkv"}},
		{"grp-movie.name.2019.1080p-sample.mkv", Info{Title: "grp-movie name", Year: 2019, Extra: "sample"}},
		{"sample-movie.name.2019.mkv", Info{Title: "movie name", Year: 2019, Extra: "sample"}},
		{"sample.mkv", Info{Extra: "sample", Container: "mkv"}},
		{"Sample.mkv", Info{Extra: "sample"}},
		{"trailer.mp4", Info{Extra: "trailer", Container: "mp4"}},
		{"Deleted Scenes.mkv", Info{Extra: "deleted scene"}},
		{"Making Of.mkv", Info{Extra: "behind the scenes"}},
		{"The Matrix (1999)-trailer.mp4", Info{Title: "The Matrix", Year: 1999, Extra: "trailer"}},
		{"Arrival-behindthescenes.mkv", Info{Title: "Arrival", Extra: "behind the scenes"}},
		{"Arrival-deleted.mkv", Info{Title: "Arrival", Extra: "deleted scene"}},
		{"Arrival - featurette.mkv", Info{Title: "Arrival", Extra: "featurette"}},
		{"Arrival-interview.mkv", Info{Title: "Arrival", Extra: "interview"}},
		{"Arrival-short.mkv", Info{Title: "Arrival", Extra: "short"}},
		{"Arrival-scene.mkv", Info{Title: "Arrival", Extra: "scene"}},
		{"Arrival-other.mkv", Info{Title: "Arrival", Extra: "extra"}},
		{"Movie (2019) Trailer.mkv", Info{Title: "Movie", Year: 2019, Extra: "trailer"}},
		{"Movie Official Trailer.mkv", Info{Title: "Movie Official", Extra: "trailer"}},
		{"Movie.2019.1080p.SAMPLE.mkv", Info{Title: "Movie", Year: 2019, Extra: "sample"}},
		{"The Sample 2020", Info{Title: "The Sample", Year: 2020}},

		// Release-site layouts.
		{"Show Name (2019) - S01E01 - Episode Name [Bluray-1080p][AAC 5.1][x265]-GROUP", Info{Title: "Show Name", Year: 2019, Seasons: ints(1), Episodes: ints(1), EpisodeTitle: "Episode Name", Source: "BluRay", Audio: strs("AAC5.1"), Group: "GROUP"}},
		{"Show Name - S01E01 - Episode Name (1080p BluRay x265 Silence)", Info{Title: "Show Name", Seasons: ints(1), Episodes: ints(1), EpisodeTitle: "Episode Name", Codec: "x265"}},
		{"Movie Title (2019) (1080p BluRay x265 HEVC 10bit AAC 5.1 Tigole) [QxR]", Info{Title: "Movie Title", Year: 2019, BitDepth: 10, Audio: strs("AAC5.1"), Group: "QxR"}},
		{"Movie.Title.2019.MULTi.VFF.2160p.10bit.4KLight.HDR.BluRay.DDP.5.1.x265-QTZ", Info{Title: "Movie Title", Year: 2019, Languages: strs("MULTi", "VFF"), Audio: strs("DDP5.1"), Group: "QTZ"}},
		{"Il.Film.2019.iTALiAN.AC3.WEBRip.x264-GRP", Info{Title: "Il Film", Year: 2019, Languages: strs("iTA"), Audio: strs("DD"), Group: "GRP"}},
		{"Der.Film.2019.German.DL.1080p.BluRay.x264-GRP", Info{Title: "Der Film", Year: 2019, Languages: strs("GERMAN", "DUAL"), Group: "GRP"}},
		{"Show.S03.1080p.AMZN.WEB-DL.DDP5.1.H.264-NTb", Info{Title: "Show", Seasons: ints(3), Complete: true, Service: "AMZN", Group: "NTb"}},
		{"Show (2005) Season 1 S01 720p BluRay x264", Info{Title: "Show", Year: 2005, Seasons: ints(1), Complete: true}},
		{"Show.Name.S01E01.REPACK.PROPER.720p", Info{Title: "Show Name", Seasons: ints(1), Episodes: ints(1), Repack: true, Proper: true}},
		{"Show.Name.S01E01.Episode.Title.Part.1.720p", Info{Title: "Show Name", Seasons: ints(1), Episodes: ints(1), EpisodeTitle: "Episode Title Part 1"}},
		{"The.Daily.Show.2024.01.02.Some.Guest.EXTENDED.720p.WEB.h264-GRP", Info{Title: "The Daily Show", Date: "2024-01-02", EpisodeTitle: "Some Guest", Edition: "Extended", Group: "GRP"}},
		{"Movie (2019) - Part 1.mkv", Info{Title: "Movie", Year: 2019, Part: 1}},
		{"Movie (2019)-cd1.avi", Info{Title: "Movie", Year: 2019, Part: 1}},
		{"Movie.2019.DVD9.ISO", Info{Title: "Movie", Year: 2019, Source: "DVD"}},

		// Extensions.
		{"Show.S01E01.720p.mp4.torrent", Info{Title: "Show", Seasons: ints(1), Episodes: ints(1), Container: "mp4"}},
		{"Show.S01E01.720p.HDTV.x264-GRP.nfo", Info{Title: "Show", Seasons: ints(1), Episodes: ints(1), Group: "GRP"}},
		{"Movie.Name.2019.1080p.BluRay.x264-GRP.en.srt", Info{Title: "Movie Name", Year: 2019, Group: "GRP", Container: "srt"}},
		{"Movie.Name.2019.1080p.BluRay.x264-GRP.pt-BR.forced.srt", Info{Title: "Movie Name", Year: 2019, Group: "GRP", Container: "srt"}},
		{"movie.ts", Info{Title: "movie", Container: "ts"}},
		{"Movie.Name.2019.1080p.HEVC", Info{Title: "Movie Name", Year: 2019, Codec: "x265"}},
	}
	if len(tests) < 200 {
		t.Errorf("only %d cases", len(tests))
	}
	for _, tt := range tests {
		checkInfo(t, tt.in, Parse(tt.in), tt.want)
	}
}

func TestParsePath(t *testing.T) {
	tests := []struct {
		in   string
		want Info
	}{
		{"Breaking Bad (2008)/Season 01/01 - Pilot.mkv", Info{Title: "Breaking Bad", Year: 2008, Seasons: ints(1), Episodes: ints(1), EpisodeTitle: "Pilot", Container: "mkv"}},
		{"Breaking Bad (2008)/Season 01/Breaking.Bad.S01E02.720p.BluRay.x264-GRP.mkv", Info{Title: "Breaking Bad", Year: 2008, Seasons: ints(1), Episodes: ints(2), Group: "GRP", Resolution: "720p"}},
		{"Movies/The Matrix (1999)/movie.mkv", Info{Title: "The Matrix", Year: 1999, Container: "mkv"}},
		{"Movies/The Matrix (1999)/The.Matrix.1999.1080p.BluRay.x264-GRP.mkv", Info{Title: "The Matrix", Year: 1999, Group: "GRP", Resolution: "1080p"}},
		{"Movies/The Matrix (1999)/the matrix.mkv", Info{Title: "The Matrix", Year: 1999}},
		{"Movies/Some Collection/The.Matrix.1999.1080p.mkv", Info{Title: "The Matrix", Year: 1999}},
		{"Movies/The Matrix Collection/The.Matrix.Reloaded.2003.mkv", Info{Title: "The Matrix Reloaded", Year: 2003}},
		{"Movies/300 (2006)/300.mkv", Info{Title: "300", Year: 2006}},
		{"Movies/1917 (2019)/1917.2019.1080p.BluRay.mkv", Info{Title: "1917", Year: 2019, Source: "BluRay"}},
		{"Movies/Inception.2010.1080p.BluRay.x264-SPARKS/sparks-inception.mkv", Info{Title: "Inception", Year: 2010, Resolution: "1080p", Source: "BluRay", Codec: "x264", Group: "SPARKS"}},
		{"Movies/Inception (2010)/CD1/inception.avi", Info{Title: "Inception", Year: 2010, Part: 1, Container: "avi"}},
		{"Movies/Inception (2010)/inception - cd2.avi", Info{Title: "Inception", Year: 2010, Part: 2}},
		{"Movies/Inception (2010)/VIDEO_TS/VTS_01_1.VOB", Info{Title: "Inception", Year: 2010, Container: "vob"}},
		{"Movies/Inception (2010)/BDMV/STREAM/00001.m2ts", Info{Title: "Inception", Year: 2010, Container: "m2ts"}},
		{"Movies/Avatar (2009) [tmdbid-19995]/Avatar (2009) - 1080p.mkv", Info{Title: "Avatar", Year: 2009, TMDBID: 19995, Resolution: "1080p"}},
		{"Movies/The Matrix (1999) {imdb-tt0133093}/The Matrix (1999).mkv", Info{Title: "The Matrix", Year: 1999, IMDbID: "tt0133093"}},
		{"Movies/Amélie (2001)/Amelie.2001.mkv", Info{Title: "Amélie", Year: 2001}},
		{"Movies/The.Matrix.1999.1080p.BluRay.x264-GRP/The.Matrix.1999.1080p.BluRay.x264-GRP.en.srt", Info{Title: "The Matrix", Year: 1999, Group: "GRP", Container: "srt"}},
		{"Movie Name (2019).mkv", Info{Title: "Movie Name", Year: 2019}},

		// Specials and extras.
		{"The Office (2005)/Specials/The Office - S00E02.mkv", Info{Title: "The Office", Year: 2005, Seasons: ints(0), Episodes: ints(2)}},
		{"The Office (2005)/Specials/Making Of.mkv", Info{Title: "The Office", Year: 2005, Seasons: ints(0), Extra: "behind the scenes"}},
		{"The Office (2005)/Specials/Christmas Special.mkv", Info{Title: "The Office", Year: 2005, Seasons: ints(0), EpisodeTitle: "Christmas Special"}},
		{"The Office (2005)/Season 00/The Office - S00E01.mkv", Info{Title: "The Office", Year: 2005, Seasons: ints(0), Episodes: ints(1)}},
		{"The Matrix (1999)/Featurettes/Making the Matrix.mkv", Info{Title: "The Matrix", Year: 1999, Extra: "featurette", EpisodeTitle: "Making the Matrix"}},
		{"Movies/Inception (2010)/Trailers/Official Trailer.mkv", Info{Title: "Inception", Year: 2010, Extra: "trailer", EpisodeTitle: "Official"}},
		{"Movies/Inception (2010)/Behind The Scenes/clip.mkv", Info{Title: "Inception", Year: 2010, Extra: "behind the scenes", EpisodeTitle: "clip"}},
		{"Movies/Inception (2010)/Deleted Scenes/Alternate Ending.mkv", Info{Title: "Inception", Year: 2010, Extra: "deleted scene", EpisodeTitle: "Alternate Ending"}},
		{"Movies/Inception (2010)/Interviews/Nolan.mkv", Info{Title: "Inception", Year: 2010, Extra: "interview", EpisodeTitle: "Nolan"}},
		{"Movies/Inception (2010)/Shorts/Short One.mkv", Info{Title: "Inception", Year: 2010, Extra: "short", EpisodeTitle: "Short One"}},
		{"Movies/Inception (2010)/Other/Gag Reel.mkv", Info{Title: "Inception", Year: 2010, Extra: "extra", EpisodeTitle: "Gag Reel"}},
		{"Movies/Inception (2010)/Extras/Gag Reel.mkv", Info{Title: "Inception", Year: 2010, Extra: "extra", EpisodeTitle: "Gag Reel"}},
		{"Movies/Inception (2010)/Inception (2010)-trailer.mkv", Info{Title: "Inception", Year: 2010, Extra: "trailer"}},
		{"Movies/Inception.2010.1080p.BluRay.x264-SPARKS/Sample/sparks-inception-sample.mkv", Info{Title: "Inception", Year: 2010, Extra: "sample", Group: "SPARKS"}},
		{"Lost/Season 1/Extras/Bloopers.mkv", Info{Title: "Lost", Seasons: ints(1), Extra: "extra", EpisodeTitle: "Bloopers"}},

		// Episode files in season folders.
		{"Lost/Season 1/E05 - Walkabout.mkv", Info{Title: "Lost", Seasons: ints(1), Episodes: ints(5), Absolute: 5, EpisodeTitle: "Walkabout"}},
		{"Lost/Season 1/Episode 5.mkv", Info{Title: "Lost", Seasons: ints(1), Episodes: ints(5), Absolute: 5}},
		{"Lost/Season 1/S01E05.mkv", Info{Title: "Lost", Seasons: ints(1), Episodes: ints(5)}},
		{"Lost/Season 1/01.mkv", Info{Title: "Lost", Seasons: ints(1), Episodes: ints(1)}},
		{"Lost/Season 1/05. Walkabout.mkv", Info{Title: "Lost", Seasons: ints(1), Episodes: ints(5), EpisodeTitle: "Walkabout"}},
		{"Lost/S01/Lost.S01E05.mkv", Info{Title: "Lost", Seasons: ints(1), Episodes: ints(5)}},
		{"Lost (2004)/Season 01/Lost - 1x01 - Pilot (1).mkv", Info{Title: "Lost", Year: 2004, Seasons: ints(1), Episodes: ints(1), EpisodeTitle: "Pilot (1)"}},
		{"TV/Fargo (2014)/Season 02/Fargo - 05.mkv", Info{Title: "Fargo", Year: 2014, Seasons: ints(2), Episodes: ints(5), Absolute: 5}},
		{"Anime/One Piece/Season 21/One Piece - 1050.mkv", Info{Title: "One Piece", Seasons: ints(21), Episodes: ints(1050), Absolute: 1050}},
		{"TV/The Simpsons/Season 10/The Simpsons - S10E01-E02 - Lard of the Dance.mkv", Info{Title: "The Simpsons", Seasons: ints(10), Episodes: ints(1, 2), EpisodeTitle: "Lard of the Dance"}},
		{"TV/Mythbusters/Season 2024/Mythbusters - S2024E01.mkv", Info{Title: "Mythbusters", Seasons: ints(2024), Episodes: ints(1)}},
		{"TV/Fargo/Staffel 2/Fargo.S02E03.German.mkv", Info{Title: "Fargo", Seasons: ints(2), Episodes: ints(3), EpisodeTitle: "German"}},
		{"TV Shows/Shogun (2024)/Season 1/Shogun.2024.S01E01.Anjin.2160p.mkv", Info{Title: "Shogun", Year: 2024, Seasons: ints(1), Episodes: ints(1), EpisodeTitle: "Anjin", Resolution: "2160p"}},
		{"Shows/Breaking Bad (2008)/BB.S01E01.mkv", Info{Title: "Breaking Bad", Year: 2008, Seasons: ints(1), Episodes: ints(1)}},
		{"Show (2008) {tvdb-81189}/Season 01/Show S01E01.mkv", Info{Title: "Show", Year: 2008, Seasons: ints(1), Episodes: ints(1), TVDBID: 81189}},
		{"Breaking.Bad.S01.1080p.WEB-DL.DDP5.1.H.264-GRP/Breaking.Bad.S01E01.mkv", Info{Title: "Breaking Bad", Seasons: ints(1), Episodes: ints(1), Resolution: "1080p", Source: "WEB-DL", Codec: "x264", Audio: strs("DDP5.1"), Group: "GRP"}},
		{"Downloads/Show.S01E01.720p.HDTV.x264-GRP.mkv", Info{Title: "Show", Seasons: ints(1), Episodes: ints(1), Group: "GRP"}},
		{"Daily/The Daily Show/The.Daily.Show.2024.01.02.mkv", Info{Title: "The Daily Show", Date: "2024-01-02"}},

		{"TV/Breaking Bad/Breaking Bad S01 1080p/Breaking Bad S01E02 1080p.mkv", Info{Title: "Breaking Bad", Seasons: ints(1), Episodes: ints(2), Resolution: "1080p"}},
		{"TV/Breaking Bad/S01/E02.mkv", Info{Title: "Breaking Bad", Seasons: ints(1), Episodes: ints(2), Absolute: 2}},
		{"TV/Breaking Bad/Season One/E02.mkv", Info{Title: "Breaking Bad", Seasons: ints(1), Episodes: ints(2), Absolute: 2}},
		{"TV/Breaking Bad/Season 1 (2008)/E02.mkv", Info{Title: "Breaking Bad", Seasons: ints(1), Episodes: ints(2), Absolute: 2}},
		{"TV/Breaking Bad/Breaking.Bad.Season.1.1080p/Breaking.Bad.1x02.mkv", Info{Title: "Breaking Bad", Seasons: ints(1), Episodes: ints(2), Resolution: "1080p"}},
		{"Movies/Heat (1995)/Heat (1995) {edition-Director's Cut}.mkv", Info{Title: "Heat", Year: 1995, Edition: "Director's Cut"}},
		{"Movies/Heat (1995) {edition-Director's Cut}/Heat.mkv", Info{Title: "Heat", Year: 1995, Edition: "Director's Cut"}},
		{"Movies/The Lord of the Rings Trilogy/The.Fellowship.of.the.Ring.2001.EXTENDED.mkv", Info{Title: "The Fellowship of the Ring", Year: 2001, Edition: "Extended"}},
		{"Movies/Kill Bill Vol. 2 (2004)/Kill Bill Vol. 2 (2004).mkv", Info{Title: "Kill Bill Vol. 2", Year: 2004}},
		{"media/movies/Saw X (2023)/Saw.X.2023.1080p.mkv", Info{Title: "Saw X", Year: 2023}},
		{"Movies/Dune (2021)/Extras/Dune-trailer.mkv", Info{Title: "Dune", Year: 2021, Extra: "trailer"}},

		// Windows and odd paths.
		{`C:\Users\me\Videos\Movies\The Matrix (1999)\movie.mkv`, Info{Title: "The Matrix", Year: 1999, Container: "mkv"}},
		{`D:\TV\The Expanse\Season 3\The.Expanse.S03E01.Fight.or.Flight.1080p.AMZN.WEB-DL.DDP5.1.H.264-NTb.mkv`, Info{Title: "The Expanse", Seasons: ints(3), Episodes: ints(1), EpisodeTitle: "Fight or Flight", Service: "AMZN", Group: "NTb"}},
		{`\\nas\media\TV\Breaking Bad\Season 2\Breaking.Bad.S02E05.720p.mkv`, Info{Title: "Breaking Bad", Seasons: ints(2), Episodes: ints(5)}},
		{`Movies\Avatar (2009) [tmdbid-19995]\Avatar (2009) - 1080p.mkv`, Info{Title: "Avatar", Year: 2009, TMDBID: 19995}},
		{"/mnt/media/TV Shows/Doctor Who (2005)/Season 1/Doctor.Who.2005.S01E01.Rose.mkv", Info{Title: "Doctor Who", Year: 2005, Seasons: ints(1), Episodes: ints(1), EpisodeTitle: "Rose"}},
		{"Movies//The Matrix (1999)/./movie.mkv", Info{Title: "The Matrix", Year: 1999}},
		{"movie.mkv", Info{Container: "mkv"}},
		{"Movies/Heat.1995.DVDRip.XviD-GRP/heat.xvid", Info{Title: "Heat", Year: 1995, Source: "DVDRip", Codec: "XviD", Group: "GRP", Container: "xvid"}},
		{"", Info{}},
	}
	for _, tt := range tests {
		checkInfo(t, tt.in, ParsePath(tt.in), tt.want)
	}
}

func TestKinds(t *testing.T) {
	tests := []struct {
		in                       string
		movie, episode, pack, tv bool
		keys                     []int
	}{
		{"The.Matrix.1999.1080p.BluRay.x264-GROUP", true, false, false, false, nil},
		{"Kill Bill Vol. 2 (2004)", true, false, false, false, nil},
		{"Avengers.Infinity.War.2018", true, false, false, false, nil},
		{"Saw X (2023)", true, false, false, false, nil},
		{"Show.S02E05.720p", false, true, false, true, ints(2005)},
		{"Show.S01E01E02", false, true, false, true, ints(1001, 1002)},
		{"Show S00E03", false, true, false, true, ints(3)},
		{"Show.S01.1080p", false, false, true, true, nil},
		{"Show.S01-S03", false, false, true, true, nil},
		{"Show - 105", false, true, false, true, nil},
		{"The.Daily.Show.2024.01.02", false, true, false, true, nil},
		{"Friends.The.Complete.Series", false, false, false, false, nil},
		{"Movie.2019-sample.mkv", false, false, false, false, nil},
		{"Show E01-E03", false, true, false, true, nil},
	}
	for _, tt := range tests {
		i := Parse(tt.in)
		if i.IsMovie() != tt.movie || i.IsEpisode() != tt.episode || i.IsSeasonPack() != tt.pack || i.IsShow() != tt.tv {
			t.Errorf("%q: movie/episode/pack/show = %v/%v/%v/%v, want %v/%v/%v/%v", tt.in,
				i.IsMovie(), i.IsEpisode(), i.IsSeasonPack(), i.IsShow(), tt.movie, tt.episode, tt.pack, tt.tv)
		}
		if !slices.Equal(i.EpisodeKeys(), tt.keys) {
			t.Errorf("%q: EpisodeKeys = %v, want %v", tt.in, i.EpisodeKeys(), tt.keys)
		}
	}
	if k := (Info{Seasons: ints(1), Episodes: ints(5, 1200)}).EpisodeKeys(); !slices.Equal(k, ints(1005)) {
		t.Errorf("episode 1200 has a key: %v", k)
	}
	if k := (Info{Seasons: ints(1, 2), Episodes: ints(5)}).EpisodeKeys(); k != nil {
		t.Errorf("two seasons give keys: %v", k)
	}
}

// Flags that must not be set by title words.
func TestNoFalseFlags(t *testing.T) {
	for _, in := range []string{
		"Show.S01E05.The.Proper.Way.1080p",
		"Proper Movie 2019 1080p",
		"Uncut Gems (2019)",
		"The Italian Job (2003)",
		"Charlotte's Web (2006)",
		"Extended Family (2023)",
		"Final Cut (2004)",
		"Cam (2018)",
	} {
		i := Parse(in)
		if i.Proper || i.Repack || i.Edition != "" || len(i.Languages) > 0 || i.Source != "" || i.Extra != "" {
			t.Errorf("%q: unexpected flags %+v", in, i)
		}
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct{ in, want string }{
		{"The Lord of the Rings: The Two Towers", "the lord of the rings the two towers"},
		{"Ocean's Eleven", "oceans eleven"},
		{"Amélie", "amelie"},
		{"Æon Flux", "aeon flux"},
		{"Law & Order", "law and order"},
		{"S.W.A.T.", "swat"},
		{"Mr. Robot", "mr robot"},
		{"Mr.Robot", "mr robot"},
		{"Spider-Man: No Way Home", "spider man no way home"},
		{"9-1-1", "9 1 1"},
		{"  WALL·E  ", "wall e"},
		{"Pokémon", "pokemon"},
		{"Ça", "ca"},
		{"Straße", "strasse"},
		{"Léon: The Professional", "leon the professional"},
		{"Tron 2.0", "tron 20"},
		{"Fast & Furious 6", "fast and furious 6"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := Normalize(tt.in); got != tt.want {
			t.Errorf("Normalize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSimilarity(t *testing.T) {
	high := [][2]string{
		{"The Matrix", "The Matrix"},
		{"The Matrix", "Matrix"},
		{"Matrix, The", "The Matrix"},
		{"Ocean's Eleven", "Oceans 11"},
		{"Rocky II", "Rocky 2"},
		{"Saw X", "Saw 10"},
		{"Spider-Man", "Spiderman"},
		{"S.W.A.T.", "SWAT"},
		{"Law & Order", "Law and Order"},
		{"Mission: Impossible - Fallout", "Mission Impossible Fallout"},
		{"Breaking Bad", "Breakin Bad"},
		{"Amélie", "Amelie"},
		{"Doctor Who", "Dr Who"},
		{"The Lord of the Rings The Two Towers", "Lord of the Rings Two Towers"},
		{"Harry Potter and the Sorcerer's Stone", "Harry Potter and the Sorcerers Stone"},
		{"Reloaded Matrix", "Matrix Reloaded"},
	}
	for _, p := range high {
		if s := Similarity(p[0], p[1]); s < 0.85 {
			t.Errorf("Similarity(%q, %q) = %.2f, want >= 0.85", p[0], p[1], s)
		}
	}
	low := [][2]string{
		{"Star Wars", "Star Trek"},
		{"Iron Man", "Iron Man 2"},
		{"Blade Runner", "Blade Runner 2049"},
		{"The Office", "Office Space"},
		{"Dune", "Dune Part Two"},
		{"Up", "Us"},
		{"Alien", "Aliens vs Predator"},
		{"", "The Matrix"},
	}
	for _, p := range low {
		if s := Similarity(p[0], p[1]); s >= 0.75 {
			t.Errorf("Similarity(%q, %q) = %.2f, want < 0.75", p[0], p[1], s)
		}
	}
	// Ranking: the exact sequel beats the original.
	if Similarity("Iron Man 2", "Iron Man 2") <= Similarity("Iron Man 2", "Iron Man") {
		t.Error("Iron Man 2 should match itself best")
	}
	if s := Similarity("The Matrix", "The Matrix"); s != 1 {
		t.Errorf("identical titles score %v", s)
	}
}

func TestExtensions(t *testing.T) {
	for _, p := range []string{"a.mkv", "A.MKV", `C:\x\a.mp4`, "dir/a.b.m2ts", "x.webm", "x.ts", "x.avi"} {
		if !IsVideo(p) {
			t.Errorf("IsVideo(%q) = false", p)
		}
	}
	for _, p := range []string{"a.srt", "a.en.ass", `C:\x\a.SSA`, "a.vtt", "a.sub", "a.idx", "a.sup"} {
		if !IsSubtitle(p) {
			t.Errorf("IsSubtitle(%q) = false", p)
		}
	}
	for _, p := range []string{"a.nfo", "a.txt", "a.srt.bak", "Mr. Robot", "folder.mkv/file", "noext", "a."} {
		if IsVideo(p) || (IsSubtitle(p) && p != "a.srt.bak") {
			t.Errorf("%q counted as media", p)
		}
	}
	if IsSubtitle("a.srt.bak") {
		t.Error("a.srt.bak is a subtitle")
	}
	if e := Ext(`C:\dir.name\file`); e != "" {
		t.Errorf("Ext of a Windows path = %q", e)
	}
}
