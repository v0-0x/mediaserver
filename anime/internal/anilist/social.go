package anilist

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// The profile page: an AniList user (the one logged in, or another), their
// statistics, favourites and who they follow, and activity feeds (theirs,
// and the people the logged-in user follows).

// UserLite is a user in a list (following, an activity's author).
type UserLite struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Avatar struct {
		Large  string `json:"large"`
		Medium string `json:"medium"`
	} `json:"avatar"`
}

// Profile is a user's page.
type Profile struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	About       string `json:"about"`
	SiteURL     string `json:"siteUrl"`
	CreatedAt   int64  `json:"createdAt"`
	BannerImage string `json:"bannerImage"`
	Avatar      struct {
		Large string `json:"large"`
	} `json:"avatar"`
	Options struct {
		ProfileColor string `json:"profileColor"`
	} `json:"options"`
	IsFollowing bool `json:"isFollowing"`
	IsFollower  bool `json:"isFollower"`
	Statistics  struct {
		Anime struct {
			Count           int     `json:"count"`
			EpisodesWatched int     `json:"episodesWatched"`
			MinutesWatched  int     `json:"minutesWatched"`
			MeanScore       float64 `json:"meanScore"`
			Genres          []struct {
				Genre string `json:"genre"`
				Count int    `json:"count"`
			} `json:"genres"`
			Statuses []struct {
				Status string `json:"status"`
				Count  int    `json:"count"`
			} `json:"statuses"`
		} `json:"anime"`
		Manga struct {
			Count        int     `json:"count"`
			ChaptersRead int     `json:"chaptersRead"`
			VolumesRead  int     `json:"volumesRead"`
			MeanScore    float64 `json:"meanScore"`
		} `json:"manga"`
	} `json:"statistics"`
	Favourites struct {
		Anime      []*Media     `json:"anime"`
		Manga      []*Media     `json:"manga"`
		Characters []*Character `json:"characters"`
	} `json:"favourites"`
	// Following and Followers: the first ones (by name), and how many in
	// all; the counts are null when AniList didn't say (FollowsError), and
	// the More ones mean "at least that many" (AniList gave no total).
	Following      []UserLite `json:"following"`
	Followers      []UserLite `json:"followers"`
	FollowingCount *int       `json:"followingCount"`
	FollowersCount *int       `json:"followersCount"`
	FollowingMore  bool       `json:"followingMore,omitempty"`
	FollowersMore  bool       `json:"followersMore,omitempty"`
	FollowsError   string     `json:"followsError,omitempty"`
	// Own: the logged-in user's own profile.
	Own bool `json:"own"`
}

// Character is a favourite character.
type Character struct {
	ID   int `json:"id"`
	Name struct {
		Full string `json:"full"`
	} `json:"name"`
	Image struct {
		Large string `json:"large"`
	} `json:"image"`
	SiteURL string `json:"siteUrl"`
}

// Activity is an entry of a feed: a list update (watched episode 5 of…) or
// a status post.
type Activity struct {
	ID         int       `json:"id"`
	Kind       string    `json:"kind"` // "list" or "text"
	Status     string    `json:"status,omitempty"`
	Progress   string    `json:"progress,omitempty"`
	Text       string    `json:"text,omitempty"`
	CreatedAt  int64     `json:"createdAt"`
	LikeCount  int       `json:"likeCount"`
	ReplyCount int       `json:"replyCount"`
	IsLiked    bool      `json:"isLiked"`
	SiteURL    string    `json:"siteUrl"`
	User       *UserLite `json:"user"`
	Media      *Media    `json:"media,omitempty"`
}

// ActivityPage is a page of a feed.
type ActivityPage struct {
	Items   []*Activity `json:"items"`
	HasMore bool        `json:"hasMore"`
}

// What a media in a feed or the favourites needs: its names and pictures.
const mediaLiteFields = `id type format status episodes chapters isAdult seasonYear averageScore
	title { romaji english native userPreferred }
	coverImage { extraLarge large medium color }
	bannerImage`

const userLiteFields = `id name avatar { large medium }`

const qProfile = `query ($id: Int, $name: String) {
	User(id: $id, name: $name) {
		id name about(asHtml: false) siteUrl createdAt bannerImage
		avatar { large }
		options { profileColor }
		isFollowing isFollower
		statistics {
			anime {
				count episodesWatched minutesWatched meanScore
				genres(limit: 8, sort: COUNT_DESC) { genre count }
				statuses { status count }
			}
			manga { count chaptersRead volumesRead meanScore }
		}
		favourites {
			anime(perPage: 18) { nodes { ` + mediaLiteFields + ` } }
			manga(perPage: 18) { nodes { ` + mediaLiteFields + ` } }
			characters(perPage: 18) { nodes { id name { full } image { large } siteUrl } }
		}
	}
}`

// following and followers take userId: Int!, so $id must be declared
// non-null: AniList turns the whole query down otherwise.
const qFollows = `query ($id: Int!) {
	following: Page(perPage: 50) { pageInfo { total hasNextPage } following(userId: $id, sort: USERNAME) { ` + userLiteFields + ` } }
	followers: Page(perPage: 50) { pageInfo { total hasNextPage } followers(userId: $id, sort: USERNAME) { ` + userLiteFields + ` } }
}`

const qActivity = `query ($page: Int, $userId: Int, $following: Boolean) {
	Page(page: $page, perPage: 25) {
		pageInfo { hasNextPage }
		activities(userId: $userId, isFollowing: $following, type_in: [ANIME_LIST, MANGA_LIST, TEXT], sort: ID_DESC) {
			__typename
			... on ListActivity {
				id status progress createdAt likeCount replyCount isLiked siteUrl
				user { ` + userLiteFields + ` }
				media { ` + mediaLiteFields + ` }
			}
			... on TextActivity {
				id text(asHtml: false) createdAt likeCount replyCount isLiked siteUrl
				user { ` + userLiteFields + ` }
			}
		}
	}
}`

const mToggleLike = `mutation ($id: Int) {
	ToggleLikeV2(id: $id, type: ACTIVITY) {
		__typename
		... on ListActivity { id likeCount isLiked }
		... on TextActivity { id likeCount isLiked }
	}
}`

const mToggleFollow = `mutation ($id: Int) { ToggleFollow(userId: $id) { id isFollowing } }`

// ErrNotLoggedIn: the profile page needs an AniList account.
var ErrNotLoggedIn = errors.New("log in with AniList to see profiles and activity")

// socialCache keeps what the profile page asked AniList for a short while:
// going back to it, or between its tabs, doesn't ask again.
type socialCache struct {
	mu    sync.Mutex
	items map[string]socialItem
}

type socialItem struct {
	at   time.Time
	data any
}

const socialFresh = 90 * time.Second

func (c *socialCache) get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	it, ok := c.items[key]
	if !ok || time.Since(it.at) > socialFresh {
		return nil, false
	}
	return it.data, true
}

func (c *socialCache) put(key string, data any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil || len(c.items) > 200 {
		c.items = map[string]socialItem{}
	}
	c.items[key] = socialItem{time.Now(), data}
}

// forget drops what was kept (a like or a follow changed it).
func (c *socialCache) forget() {
	c.mu.Lock()
	c.items = nil
	c.mu.Unlock()
}

// Profile returns the profile of the user called name, or of the logged-in
// user ("").
func (p *Platform) Profile(ctx context.Context, name string, refresh bool) (*Profile, error) {
	session, v := p.state()
	if v == nil {
		return nil, ErrNotLoggedIn
	}
	name = strings.TrimSpace(name)
	own := name == "" || strings.EqualFold(name, v.Name)
	key := fmt.Sprintf("profile:%d:%s", session, strings.ToLower(name))
	if own {
		key = fmt.Sprintf("profile:%d:", session)
	}
	if refresh {
		// Asked again from the page: its feeds too.
		p.social.forget()
	} else if d, ok := p.social.get(key); ok {
		return d.(*Profile), nil
	}
	vars := map[string]any{}
	if own {
		vars["id"] = v.ID
	} else {
		vars["name"] = name
	}
	var raw struct {
		User *struct {
			Profile
			Favourites struct {
				Anime      struct{ Nodes []*Media }     `json:"anime"`
				Manga      struct{ Nodes []*Media }     `json:"manga"`
				Characters struct{ Nodes []*Character } `json:"characters"`
			} `json:"favourites"`
		} `json:"User"`
	}
	if err := p.client.Query(ctx, qProfile, vars, &raw); err != nil {
		if errors.Is(err, ErrUnauthorized) {
			p.expire(session)
		}
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			return nil, fmt.Errorf("there's no AniList user called %q", name)
		}
		return nil, err
	}
	if raw.User == nil {
		return nil, fmt.Errorf("there's no AniList user called %q", name)
	}
	prof := raw.User.Profile
	prof.Favourites.Anime = raw.User.Favourites.Anime.Nodes
	prof.Favourites.Manga = raw.User.Favourites.Manga.Nodes
	prof.Favourites.Characters = raw.User.Favourites.Characters.Nodes
	prof.Own = prof.ID == v.ID
	if prof.Favourites.Anime == nil {
		prof.Favourites.Anime = []*Media{}
	}
	if prof.Favourites.Manga == nil {
		prof.Favourites.Manga = []*Media{}
	}
	if prof.Favourites.Characters == nil {
		prof.Favourites.Characters = []*Character{}
	}

	// Who they follow, and how many follow them: a second question (by id).
	// Without it the profile still shows, without the counts, and isn't
	// kept: the next look asks again.
	type pageInfo struct {
		Total       int  `json:"total"`
		HasNextPage bool `json:"hasNextPage"`
	}
	var follows struct {
		Following struct {
			PageInfo  pageInfo   `json:"pageInfo"`
			Following []UserLite `json:"following"`
		} `json:"following"`
		Followers struct {
			PageInfo  pageInfo   `json:"pageInfo"`
			Followers []UserLite `json:"followers"`
		} `json:"followers"`
	}
	err := p.client.Query(ctx, qFollows, map[string]any{"id": prof.ID}, &follows)
	if err != nil {
		if errors.Is(err, ErrUnauthorized) {
			p.expire(session)
		}
		log.Printf("anilist: who %s follows: %v", prof.Name, err)
		prof.FollowsError = "AniList didn't answer just now. Try again in a moment."
		prof.Following, prof.Followers = []UserLite{}, []UserLite{}
		return &prof, nil
	}
	prof.Following, prof.Followers = follows.Following.Following, follows.Followers.Followers
	if prof.Following == nil {
		prof.Following = []UserLite{}
	}
	if prof.Followers == nil {
		prof.Followers = []UserLite{}
	}
	// The total, or at least the ones on the page when AniList leaves the
	// total out.
	count := func(pi pageInfo, n int) (*int, bool) {
		c := max(pi.Total, n)
		return &c, pi.HasNextPage && c <= n
	}
	prof.FollowingCount, prof.FollowingMore = count(follows.Following.PageInfo, len(prof.Following))
	prof.FollowersCount, prof.FollowersMore = count(follows.Followers.PageInfo, len(prof.Followers))
	p.social.put(key, &prof)
	return &prof, nil
}

// Activity returns a page of a feed: the logged-in user's following
// ("following"), or one user's own (userID).
func (p *Platform) Activity(ctx context.Context, following bool, userID, page int, showAdult bool) (*ActivityPage, error) {
	session, v := p.state()
	if v == nil {
		return nil, ErrNotLoggedIn
	}
	page = clamp(page, 1, 50, 1)
	if !following && userID <= 0 {
		userID = v.ID
	}
	key := fmt.Sprintf("activity:%d:%v:%d:%d:%v", session, following, userID, page, showAdult)
	if d, ok := p.social.get(key); ok {
		return d.(*ActivityPage), nil
	}
	vars := map[string]any{"page": page}
	if following {
		vars["following"] = true
	} else {
		vars["userId"] = userID
	}
	var raw struct {
		Page struct {
			PageInfo struct {
				HasNextPage bool `json:"hasNextPage"`
			} `json:"pageInfo"`
			Activities []struct {
				Typename   string    `json:"__typename"`
				ID         int       `json:"id"`
				Status     string    `json:"status"`
				Progress   *string   `json:"progress"`
				Text       string    `json:"text"`
				CreatedAt  int64     `json:"createdAt"`
				LikeCount  int       `json:"likeCount"`
				ReplyCount int       `json:"replyCount"`
				IsLiked    bool      `json:"isLiked"`
				SiteURL    string    `json:"siteUrl"`
				User       *UserLite `json:"user"`
				Media      *Media    `json:"media"`
			} `json:"activities"`
		} `json:"Page"`
	}
	if err := p.client.Query(ctx, qActivity, vars, &raw); err != nil {
		if errors.Is(err, ErrUnauthorized) {
			p.expire(session)
		}
		return nil, err
	}
	out := &ActivityPage{Items: []*Activity{}, HasMore: raw.Page.PageInfo.HasNextPage}
	for _, a := range raw.Page.Activities {
		if a.ID == 0 || a.User == nil {
			continue // a kind of activity not asked for (a message)
		}
		it := &Activity{ID: a.ID, Status: a.Status, Text: a.Text, CreatedAt: a.CreatedAt, LikeCount: a.LikeCount, ReplyCount: a.ReplyCount, IsLiked: a.IsLiked, SiteURL: a.SiteURL, User: a.User, Media: a.Media}
		if a.Progress != nil {
			it.Progress = *a.Progress
		}
		switch a.Typename {
		case "ListActivity":
			if a.Media == nil || (a.Media.IsAdult && !showAdult) {
				continue
			}
			it.Kind = "list"
		case "TextActivity":
			it.Kind = "text"
		default:
			continue
		}
		out.Items = append(out.Items, it)
	}
	p.social.put(key, out)
	return out, nil
}

// ToggleLike likes an activity, or takes the like back; it returns the
// activity's likes now.
func (p *Platform) ToggleLike(ctx context.Context, id int) (likeCount int, isLiked bool, err error) {
	session, v := p.state()
	if v == nil {
		return 0, false, ErrNotLoggedIn
	}
	var out struct {
		ToggleLikeV2 struct {
			LikeCount int  `json:"likeCount"`
			IsLiked   bool `json:"isLiked"`
		} `json:"ToggleLikeV2"`
	}
	if err := p.client.Query(ctx, mToggleLike, map[string]any{"id": id}, &out); err != nil {
		if errors.Is(err, ErrUnauthorized) {
			p.expire(session)
		}
		return 0, false, err
	}
	p.social.forget()
	return out.ToggleLikeV2.LikeCount, out.ToggleLikeV2.IsLiked, nil
}

// ToggleFollow follows a user, or stops; it reports whether they're
// followed now.
func (p *Platform) ToggleFollow(ctx context.Context, userID int) (bool, error) {
	session, v := p.state()
	if v == nil {
		return false, ErrNotLoggedIn
	}
	if userID == v.ID {
		return false, errors.New("that's you")
	}
	var out struct {
		ToggleFollow struct {
			IsFollowing bool `json:"isFollowing"`
		} `json:"ToggleFollow"`
	}
	if err := p.client.Query(ctx, mToggleFollow, map[string]any{"id": userID}, &out); err != nil {
		if errors.Is(err, ErrUnauthorized) {
			p.expire(session)
		}
		return false, err
	}
	p.social.forget()
	return out.ToggleFollow.IsFollowing, nil
}
