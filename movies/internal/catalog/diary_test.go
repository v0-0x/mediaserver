package catalog

import (
	"context"
	"testing"
	"time"
)

func TestDiary(t *testing.T) {
	p := newTestPlatform(t, nil)
	ctx := context.Background()
	a, b := testMovie(1), testMovie(2)
	addLocal(t, p, a, "COMPLETED", 1)
	p.db.SetCache(liteKey(b.ID), b, time.Hour)

	mine, added, err := p.AddDiary(ctx, DiaryEntry{MediaID: a.ID, WatchedDate: "2020-10-01", Rating: 90, Liked: true})
	if err != nil || !added || mine.ID == 0 || mine.Source != DiaryKumoBox || mine.Media == nil || mine.Media.ID != a.ID {
		t.Fatalf("logged: %+v %v %v", mine, added, err)
	}
	imported := DiaryEntry{MediaID: b.ID, WatchedDate: "2020-09-12", Rating: 70, Rewatch: true, Review: "Again!", Tags: "cinema",
		Source: DiaryLetterboxd, LetterboxdID: "letterboxd-review-123"}
	got, added, err := p.AddDiary(ctx, imported)
	if err != nil || !added {
		t.Fatalf("imported: %v %v", added, err)
	}
	// Imported again (the next sync): kept once.
	again, added, err := p.AddDiary(ctx, imported)
	if err != nil || added || again.ID != got.ID || again.Review != "Again!" || again.Media == nil {
		t.Fatalf("imported again: %+v %v %v", again, added, err)
	}
	today, _, err := p.AddDiary(ctx, DiaryEntry{MediaID: a.ID, Rewatch: true})
	if err != nil || today.WatchedDate != time.Now().Format(time.DateOnly) {
		t.Fatalf("today: %+v %v", today, err)
	}
	if _, _, err := p.AddDiary(ctx, DiaryEntry{}); err == nil {
		t.Fatal("logged a viewing of nothing")
	}

	all, err := p.Diary(ctx, 0, 0)
	if err != nil || len(all) != 3 || all[0].ID != today.ID || all[1].ID != mine.ID || all[2].ID != got.ID {
		t.Fatalf("diary: %+v %v", all, err)
	}
	if all[2].Media == nil || all[2].Media.ID != b.ID || !all[2].Rewatch || all[2].Tags != "cinema" || all[2].Rating != 70 {
		t.Errorf("imported entry: %+v", all[2])
	}
	if ofA, _ := p.Diary(ctx, a.ID, 1); len(ofA) != 1 || ofA[0].ID != today.ID {
		t.Errorf("latest of a: %+v", ofA)
	}

	pending, err := p.PendingDiary(ctx)
	if err != nil || len(pending) != 2 || pending[0].ID != mine.ID || pending[1].ID != today.ID {
		t.Fatalf("pending: %+v %v", pending, err)
	}
	if err := p.MarkDiaryExported(ctx, []int64{mine.ID}, 1_800_000_000); err != nil {
		t.Fatal(err)
	}
	if pending, _ := p.PendingDiary(ctx); len(pending) != 1 || pending[0].ID != today.ID {
		t.Fatalf("pending after exporting: %+v", pending)
	}
	if err := p.RateDiary(ctx, today.ID, 150, true); err != nil {
		t.Fatal(err)
	}
	if ofA, _ := p.Diary(ctx, a.ID, 1); len(ofA) != 1 || ofA[0].Rating != 100 || !ofA[0].Liked || !ofA[0].Rewatch {
		t.Errorf("rated: %+v", ofA)
	}
	if err := p.DeleteDiary(ctx, today.ID); err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteDiary(ctx, today.ID); err != nil {
		t.Fatalf("deleting twice: %v", err)
	}
	if all, _ := p.Diary(ctx, 0, 0); len(all) != 2 || all[0].ExportedAt != 1_800_000_000 {
		t.Fatalf("after deleting: %+v", all)
	}
}

// A viewing Letterboxd has (imported from it, or sent to it) that the user
// deleted isn't imported again: not by its id (the feed's next read), nor
// as the same film the same day (the export's copy of it).
func TestDeletedDiaryStaysDeleted(t *testing.T) {
	p := newTestPlatform(t, nil)
	ctx := context.Background()
	a, b := testMovie(1), testMovie(2)
	p.db.SetCache(liteKey(a.ID), a, time.Hour)
	p.db.SetCache(liteKey(b.ID), b, time.Hour)
	add := func(e DiaryEntry) (*DiaryEntry, bool) {
		t.Helper()
		got, added, err := p.AddDiary(ctx, e)
		if err != nil {
			t.Fatal(err)
		}
		return got, added
	}
	del := func(id int64) {
		t.Helper()
		if err := p.DeleteDiary(ctx, id); err != nil {
			t.Fatal(err)
		}
	}

	fromFeed := DiaryEntry{MediaID: b.ID, WatchedDate: "2024-03-10", Rating: 90, Source: DiaryLetterboxd, LetterboxdID: "letterboxd-watch-1001"}
	imported, added := add(fromFeed)
	if !added {
		t.Fatal("not imported")
	}
	del(imported.ID)
	if got, added := add(fromFeed); added || got != nil {
		t.Fatalf("imported again after it was deleted: %+v", got)
	}
	fromExport := DiaryEntry{MediaID: b.ID, WatchedDate: "2024-03-10", Source: DiaryLetterboxd, LetterboxdID: "export:https://boxd.it/abc1:2024-03-10"}
	if _, added := add(fromExport); added {
		t.Fatal("the export's copy of a deleted viewing was imported")
	}
	if _, added := add(DiaryEntry{MediaID: b.ID, WatchedDate: "2024-05-01", Source: DiaryLetterboxd, LetterboxdID: "letterboxd-watch-1002"}); !added {
		t.Fatal("another viewing of the film wasn't imported")
	}

	// Logged here and sent to Letterboxd, then deleted: the feed's copy
	// stays out. Logged here again, it's logged.
	sent, _ := add(DiaryEntry{MediaID: a.ID, WatchedDate: "2024-06-01"})
	if err := p.MarkDiaryExported(ctx, []int64{sent.ID}, 1_800_000_000); err != nil {
		t.Fatal(err)
	}
	del(sent.ID)
	if _, added := add(DiaryEntry{MediaID: a.ID, WatchedDate: "2024-06-01", Source: DiaryLetterboxd, LetterboxdID: "letterboxd-watch-2001"}); added {
		t.Fatal("a deleted viewing came back from the feed")
	}
	if _, added := add(DiaryEntry{MediaID: a.ID, WatchedDate: "2024-06-01"}); !added {
		t.Fatal("logging the film here again was refused")
	}
	// Never sent: Letterboxd's own entry of that day is imported.
	unsent, _ := add(DiaryEntry{MediaID: a.ID, WatchedDate: "2024-07-01"})
	del(unsent.ID)
	if _, added := add(DiaryEntry{MediaID: a.ID, WatchedDate: "2024-07-01", Source: DiaryLetterboxd, LetterboxdID: "letterboxd-watch-2002"}); !added {
		t.Fatal("a Letterboxd entry was refused for a viewing deleted here before it was sent")
	}
	if all, _ := p.Diary(ctx, 0, 0); len(all) != 3 {
		t.Fatalf("diary: %+v", all)
	}
}
