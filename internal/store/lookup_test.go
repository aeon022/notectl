package store

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/aeon022/notectl/internal/models"
)

func note(id, title, source, account string, mod time.Time) *models.Note {
	return &models.Note{ID: id, Title: title, Source: source, Account: account, Tags: []string{"a", "b"}, ModTime: mod, Created: mod}
}

func TestGetByTitleMissingAndTags(t *testing.T) {
	s, ctx := newTestStore(t), context.Background()
	if n, err := s.GetByTitle(ctx, "nope"); n != nil || err != nil {
		t.Fatalf("missing title must be (nil, nil), got %v, %v", n, err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := s.Upsert(ctx, note("1", "Plan", "obsidian", "", now)); err != nil {
		t.Fatal(err)
	}
	n, err := s.GetByTitle(ctx, "Plan")
	if err != nil || n == nil {
		t.Fatalf("GetByTitle = %v, %v", n, err)
	}
	if !reflect.DeepEqual(n.Tags, []string{"a", "b"}) || !n.ModTime.Equal(now) {
		t.Errorf("round trip: %+v", n)
	}
}

// Deleting by title must be able to see ALL duplicates, newest first.
func TestFindByTitleReturnsAllDuplicatesNewestFirst(t *testing.T) {
	s, ctx := newTestStore(t), context.Background()
	t0 := time.Now().UTC().Truncate(time.Second)
	_ = s.Upsert(ctx, note("old", "Dup", "obsidian", "", t0.Add(-time.Hour)))
	_ = s.Upsert(ctx, note("new", "Dup", "apple", "iCloud", t0))
	_ = s.Upsert(ctx, note("other", "Other", "obsidian", "", t0))
	got, err := s.FindByTitle(ctx, "Dup")
	if err != nil || len(got) != 2 || got[0].ID != "new" || got[1].ID != "old" {
		t.Fatalf("FindByTitle = %+v, %v", got, err)
	}
}

func TestDeleteAndDeleteBySourceScope(t *testing.T) {
	s, ctx := newTestStore(t), context.Background()
	now := time.Now()
	_ = s.Upsert(ctx, note("1", "a", "apple", "iCloud", now))
	_ = s.Upsert(ctx, note("2", "b", "apple", "iCloud", now))
	_ = s.Upsert(ctx, note("3", "c", "joplin", "", now))
	if err := s.Delete(ctx, "1"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBySource(ctx, "apple"); err != nil {
		t.Fatal(err)
	}
	rest, _ := s.List(ctx, Filter{})
	if len(rest) != 1 || rest[0].ID != "3" {
		t.Errorf("only the joplin note may survive, got %+v", rest)
	}
}

func TestListAccountsAndCountByAccount(t *testing.T) {
	s, ctx := newTestStore(t), context.Background()
	now := time.Now()
	_ = s.Upsert(ctx, note("1", "a", "apple", "iCloud", now))
	_ = s.Upsert(ctx, note("2", "b", "apple", "iCloud", now))
	_ = s.Upsert(ctx, note("3", "c", "apple", "Work", now))
	_ = s.Upsert(ctx, note("4", "d", "joplin", "", now))
	accts, err := s.ListAccounts(ctx)
	if err != nil || !reflect.DeepEqual(accts, []string{"Work", "iCloud"}) {
		t.Errorf("ListAccounts = %v, %v (distinct, sorted, no empty account)", accts, err)
	}
	counts, err := s.CountByAccount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"iCloud": 2, "Work": 1, "": 4} // "" = grand total incl. account-less notes
	if !reflect.DeepEqual(counts, want) {
		t.Errorf("CountByAccount = %v, want %v", counts, want)
	}
}
