package anilist

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// The profile page needs a login.
func TestSocialNeedsLogin(t *testing.T) {
	p := newTestPlatform(t, nil)
	ctx := context.Background()
	if _, err := p.Profile(ctx, "", false); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("Profile logged out: %v", err)
	}
	if _, err := p.Activity(ctx, true, 0, 1, false); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("Activity logged out: %v", err)
	}
}

// The own profile is asked by id, another's by name; with who they follow,
// how many follow them, the favourites' lists flattened, and kept a moment.
func TestProfile(t *testing.T) {
	var asked atomic.Int32
	p := newTestPlatform(t, func(w http.ResponseWriter, req gqlRequest) {
		asked.Add(1)
		switch {
		case strings.Contains(req.Query, "User("):
			if req.Variables["id"] == nil && req.Variables["name"] != "Friend" {
				t.Errorf("profile asked with %v", req.Variables)
			}
			id, name := 7, "tester"
			if req.Variables["name"] == "Friend" {
				id, name = 9, "Friend"
			}
			reply(w, 200, data(map[string]any{"User": map[string]any{
				"id": id, "name": name, "isFollowing": id == 9,
				"statistics": map[string]any{"anime": map[string]any{"count": 12, "episodesWatched": 300, "minutesWatched": 7200}},
				"favourites": map[string]any{
					"anime":      map[string]any{"nodes": []any{map[string]any{"id": 21, "title": map[string]any{"romaji": "One Piece"}}}},
					"characters": map[string]any{"nodes": []any{map[string]any{"id": 40, "name": map[string]any{"full": "Luffy"}}}},
				},
			}}))
		case strings.Contains(req.Query, "following(userId"):
			if !followsDeclared(req.Query) {
				reply(w, 400, followsRejected)
				return
			}
			reply(w, 200, data(map[string]any{
				"following": map[string]any{"pageInfo": map[string]any{"total": 3}, "following": []any{map[string]any{"id": 9, "name": "Friend"}}},
				"followers": map[string]any{"pageInfo": map[string]any{"total": 5}, "followers": []any{map[string]any{"id": 11, "name": "Fan"}}},
			}))
		default:
			t.Errorf("unexpected query %s", op(req.Query))
		}
	})
	setLoggedIn(p, 7)
	ctx := context.Background()

	own, err := p.Profile(ctx, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !own.Own || own.Statistics.Anime.EpisodesWatched != 300 || len(own.Favourites.Anime) != 1 || own.Favourites.Anime[0].ID != 21 {
		t.Fatalf("own profile: %+v", own)
	}
	if len(own.Favourites.Manga) != 0 || own.Favourites.Manga == nil || len(own.Favourites.Characters) != 1 {
		t.Fatalf("favourites: %+v", own.Favourites)
	}
	if own.FollowsError != "" || own.FollowingCount == nil || own.FollowersCount == nil {
		t.Fatalf("follows not loaded: %q", own.FollowsError)
	}
	if *own.FollowingCount != 3 || *own.FollowersCount != 5 || own.FollowingMore || own.FollowersMore || len(own.Following) != 1 || own.Following[0].Name != "Friend" || len(own.Followers) != 1 || own.Followers[0].Name != "Fan" {
		t.Fatalf("follows: %+v %d %d", own.Following, *own.FollowingCount, *own.FollowersCount)
	}
	// Kept: no new question.
	before := asked.Load()
	if _, err := p.Profile(ctx, "tester", false); err != nil || asked.Load() != before {
		t.Fatalf("own profile by its name asked again (%v)", err)
	}

	friend, err := p.Profile(ctx, "Friend", false)
	if err != nil {
		t.Fatal(err)
	}
	if friend.Own || friend.ID != 9 || !friend.IsFollowing {
		t.Fatalf("friend: %+v", friend)
	}
}

// AniList checks a query's variables against its schema: following and
// followers take userId: Int!, and a nullable $id fails the whole query.
func followsDeclared(q string) bool { return strings.Contains(q, "$id: Int!") }

var followsRejected = map[string]any{"data": nil, "errors": []any{map[string]any{"message": `Variable "$id" of type "Int" used in position expecting type "Int!".`, "status": 400}}}

// Who they follow couldn't be asked: the profile still shows, its counts
// unknown (not 0), and it isn't kept. Without a total from AniList, the
// counts are what's on the page, "or more" when there's a next one.
func TestProfileFollows(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	var asked atomic.Int32
	p := newTestPlatform(t, func(w http.ResponseWriter, req gqlRequest) {
		switch {
		case strings.Contains(req.Query, "User("):
			reply(w, 200, data(map[string]any{"User": map[string]any{"id": 7, "name": "tester"}}))
		case strings.Contains(req.Query, "following(userId"):
			asked.Add(1)
			if fail.Load() {
				reply(w, 500, map[string]any{"data": nil, "errors": []any{map[string]any{"message": "boom", "status": 500}}})
				return
			}
			following := make([]any, 50)
			for i := range following {
				following[i] = map[string]any{"id": 100 + i, "name": "U"}
			}
			reply(w, 200, data(map[string]any{
				"following": map[string]any{"pageInfo": map[string]any{"hasNextPage": true}, "following": following},
				"followers": map[string]any{"pageInfo": map[string]any{"hasNextPage": false}, "followers": []any{map[string]any{"id": 1, "name": "A"}, map[string]any{"id": 2, "name": "B"}}},
			}))
		default:
			t.Errorf("unexpected query %s", op(req.Query))
		}
	})
	setLoggedIn(p, 7)
	ctx := context.Background()

	prof, err := p.Profile(ctx, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if prof.FollowsError == "" || prof.FollowingCount != nil || prof.FollowersCount != nil || prof.Following == nil || prof.Followers == nil {
		t.Fatalf("failed follows: %q %v %v %v", prof.FollowsError, prof.FollowingCount, prof.FollowersCount, prof.Following)
	}
	fail.Store(false)
	prof, err = p.Profile(ctx, "", false)
	if err != nil || asked.Load() != 2 {
		t.Fatalf("not asked again after a failure: %d (%v)", asked.Load(), err)
	}
	if prof.FollowsError != "" || *prof.FollowingCount != 50 || !prof.FollowingMore || *prof.FollowersCount != 2 || prof.FollowersMore {
		t.Fatalf("counts without totals: %d more=%v, %d more=%v", *prof.FollowingCount, prof.FollowingMore, *prof.FollowersCount, prof.FollowersMore)
	}
}

// A feed: list updates and posts, adult ones only when shown, messages and
// unknown kinds left out; a like changes what's kept.
func TestActivityFeed(t *testing.T) {
	var feeds atomic.Int32
	p := newTestPlatform(t, func(w http.ResponseWriter, req gqlRequest) {
		switch {
		case strings.Contains(req.Query, "activities("):
			feeds.Add(1)
			if req.Variables["following"] != true || req.Variables["userId"] != nil {
				t.Errorf("following feed asked with %v", req.Variables)
			}
			reply(w, 200, data(map[string]any{"Page": map[string]any{
				"pageInfo": map[string]any{"hasNextPage": true},
				"activities": []any{
					map[string]any{"__typename": "ListActivity", "id": 1, "status": "watched episode", "progress": "5", "user": map[string]any{"id": 9, "name": "Friend"}, "media": map[string]any{"id": 21}},
					map[string]any{"__typename": "ListActivity", "id": 2, "status": "watched episode", "progress": "1", "user": map[string]any{"id": 9, "name": "Friend"}, "media": map[string]any{"id": 22, "isAdult": true}},
					map[string]any{"__typename": "TextActivity", "id": 3, "text": "hello", "likeCount": 2, "user": map[string]any{"id": 9, "name": "Friend"}},
					map[string]any{"__typename": "MessageActivity"},
				},
			}}))
		case strings.Contains(req.Query, "ToggleLikeV2"):
			reply(w, 200, data(map[string]any{"ToggleLikeV2": map[string]any{"id": 3, "likeCount": 3, "isLiked": true}}))
		default:
			t.Errorf("unexpected query %s", op(req.Query))
		}
	})
	setLoggedIn(p, 7)
	ctx := context.Background()

	page, err := p.Activity(ctx, true, 0, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if !page.HasMore || len(page.Items) != 2 {
		t.Fatalf("feed: %+v", page)
	}
	if a := page.Items[0]; a.Kind != "list" || a.Progress != "5" || a.Media == nil || a.Media.ID != 21 {
		t.Fatalf("list activity: %+v", a)
	}
	if a := page.Items[1]; a.Kind != "text" || a.Text != "hello" || a.LikeCount != 2 {
		t.Fatalf("text activity: %+v", a)
	}
	// Kept a moment.
	if _, err := p.Activity(ctx, true, 0, 1, false); err != nil || feeds.Load() != 1 {
		t.Fatalf("asked again: %d (%v)", feeds.Load(), err)
	}
	// A like forgets what was kept: the feed is asked again (here with
	// adult entries shown).
	count, liked, err := p.ToggleLike(ctx, 3)
	if err != nil || count != 3 || !liked {
		t.Fatalf("like: %d %v %v", count, liked, err)
	}
	page, err = p.Activity(ctx, true, 0, 1, true)
	if err != nil || feeds.Load() != 2 || len(page.Items) != 3 {
		t.Fatalf("after a like, with adult entries: %d items, %d asked (%v)", len(page.Items), feeds.Load(), err)
	}
}

// Following oneself isn't a thing.
func TestToggleFollowSelf(t *testing.T) {
	p := newTestPlatform(t, nil)
	setLoggedIn(p, 7)
	if _, err := p.ToggleFollow(context.Background(), 7); err == nil {
		t.Fatal("followed oneself")
	}
}
