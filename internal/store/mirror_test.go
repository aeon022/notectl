package store

import (
	"context"
	"testing"
	"time"
)

func TestUpsertMirrorLink_RoundTrips(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	link := MirrorLink{AppleID: "apple-1", AppleHash: "h1", ObsidianID: "obs-1", ObsidianHash: "h1", LastSynced: time.Now()}
	if err := s.UpsertMirrorLink(ctx, link); err != nil {
		t.Fatalf("UpsertMirrorLink() error = %v", err)
	}
	links, err := s.ListMirrorLinks(ctx)
	if err != nil {
		t.Fatalf("ListMirrorLinks() error = %v", err)
	}
	if len(links) != 1 || links[0].AppleID != "apple-1" || links[0].ObsidianID != "obs-1" {
		t.Fatalf("ListMirrorLinks() = %+v, want one link apple-1/obs-1", links)
	}
}

// A rename on the Obsidian side gives that note a new ID (it's a hash of
// the file's relative path). Re-upserting under the same AppleID must
// update the existing row's ObsidianID in place, not leave the old one
// behind as an orphan.
func TestUpsertMirrorLink_UpdatesObsidianIDInPlace(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_ = s.UpsertMirrorLink(ctx, MirrorLink{AppleID: "apple-1", AppleHash: "h1", ObsidianID: "obs-1", ObsidianHash: "h1", LastSynced: time.Now()})
	if err := s.UpsertMirrorLink(ctx, MirrorLink{AppleID: "apple-1", AppleHash: "h2", ObsidianID: "obs-2", ObsidianHash: "h2", LastSynced: time.Now()}); err != nil {
		t.Fatalf("second UpsertMirrorLink() error = %v", err)
	}
	links, err := s.ListMirrorLinks(ctx)
	if err != nil {
		t.Fatalf("ListMirrorLinks() error = %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("want 1 link after re-upsert, got %d", len(links))
	}
	if links[0].ObsidianID != "obs-2" {
		t.Fatalf("ObsidianID = %q, want obs-2", links[0].ObsidianID)
	}
}

func TestDeleteMirrorLink_Removes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_ = s.UpsertMirrorLink(ctx, MirrorLink{AppleID: "apple-1", AppleHash: "h1", ObsidianID: "obs-1", ObsidianHash: "h1", LastSynced: time.Now()})
	if err := s.DeleteMirrorLink(ctx, "apple-1"); err != nil {
		t.Fatalf("DeleteMirrorLink() error = %v", err)
	}
	links, err := s.ListMirrorLinks(ctx)
	if err != nil {
		t.Fatalf("ListMirrorLinks() error = %v", err)
	}
	if len(links) != 0 {
		t.Fatalf("want 0 links after delete, got %d", len(links))
	}
}

func TestMirrorPendingDeletes_AddListCountDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p := MirrorPendingDelete{AppleID: "apple-1", ObsidianID: "obs-1", Title: "Groceries", DeletedOn: "apple", DetectedAt: time.Now()}
	if err := s.AddMirrorPendingDelete(ctx, p); err != nil {
		t.Fatalf("AddMirrorPendingDelete() error = %v", err)
	}
	// Re-detecting the same pending delete before it's applied must not
	// create a second row.
	if err := s.AddMirrorPendingDelete(ctx, p); err != nil {
		t.Fatalf("duplicate AddMirrorPendingDelete() error = %v", err)
	}
	pending, err := s.ListMirrorPendingDeletes(ctx)
	if err != nil {
		t.Fatalf("ListMirrorPendingDeletes() error = %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("want 1 pending delete (deduped), got %d", len(pending))
	}
	n, err := s.CountMirrorPendingDeletes(ctx)
	if err != nil {
		t.Fatalf("CountMirrorPendingDeletes() error = %v", err)
	}
	if n != 1 {
		t.Fatalf("CountMirrorPendingDeletes() = %d, want 1", n)
	}
	if err := s.DeleteMirrorPendingDelete(ctx, pending[0].ID); err != nil {
		t.Fatalf("DeleteMirrorPendingDelete() error = %v", err)
	}
	n, err = s.CountMirrorPendingDeletes(ctx)
	if err != nil {
		t.Fatalf("CountMirrorPendingDeletes() error = %v", err)
	}
	if n != 0 {
		t.Fatalf("CountMirrorPendingDeletes() after delete = %d, want 0", n)
	}
}
