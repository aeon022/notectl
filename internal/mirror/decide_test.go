package mirror

import (
	"testing"
	"time"

	"github.com/aeon022/notectl/internal/models"
)

var fixedTime = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

func TestDecide_NewAppleNoteCreatesInObsidian(t *testing.T) {
	apple := []models.Note{{ID: "apple-1", Title: "Groceries", Body: "milk", Folder: "Shopping", ModTime: fixedTime}}
	d := Decide(apple, nil, nil, nil)
	if len(d.CreateInObsidian) != 1 || d.CreateInObsidian[0].ID != "apple-1" {
		t.Fatalf("CreateInObsidian = %+v, want the apple note", d.CreateInObsidian)
	}
	if len(d.CreateInApple) != 0 || len(d.PushToApple) != 0 || len(d.PushToObsidian) != 0 || len(d.NewLinks) != 0 {
		t.Fatalf("unexpected extra decisions: %+v", d)
	}
}

func TestDecide_NewObsidianNoteCreatesInApple(t *testing.T) {
	obs := []models.Note{{ID: "obs-1", Title: "Groceries", Body: "milk", Folder: "Shopping", ModTime: fixedTime}}
	d := Decide(nil, obs, nil, nil)
	if len(d.CreateInApple) != 1 || d.CreateInApple[0].ID != "obs-1" {
		t.Fatalf("CreateInApple = %+v, want the obsidian note", d.CreateInApple)
	}
	if len(d.CreateInObsidian) != 0 || len(d.PushToApple) != 0 || len(d.PushToObsidian) != 0 || len(d.NewLinks) != 0 {
		t.Fatalf("unexpected extra decisions: %+v", d)
	}
}

func TestDecide_BootstrapLinksMatchingTitleAndFolder(t *testing.T) {
	apple := []models.Note{{ID: "apple-1", Title: "Groceries", Body: "milk", Folder: "Shopping", ModTime: fixedTime}}
	obs := []models.Note{{ID: "obs-1", Title: "Groceries", Body: "milk", Folder: "Shopping", ModTime: fixedTime}}
	d := Decide(apple, obs, nil, nil)
	if len(d.NewLinks) != 1 {
		t.Fatalf("NewLinks = %+v, want 1", d.NewLinks)
	}
	l := d.NewLinks[0]
	if l.AppleID != "apple-1" || l.ObsidianID != "obs-1" {
		t.Fatalf("NewLinks[0] = %+v, want apple-1/obs-1", l)
	}
	if len(d.CreateInApple) != 0 || len(d.CreateInObsidian) != 0 || len(d.PushToApple) != 0 || len(d.PushToObsidian) != 0 {
		t.Fatalf("unexpected extra decisions: %+v", d)
	}
}

func TestDecide_BootstrapConflictNewerWins(t *testing.T) {
	older := fixedTime
	newer := fixedTime.Add(time.Hour)
	apple := []models.Note{{ID: "apple-1", Title: "Groceries", Body: "milk", Folder: "Shopping", ModTime: newer}}
	obs := []models.Note{{ID: "obs-1", Title: "Groceries", Body: "eggs", Folder: "Shopping", ModTime: older}}
	d := Decide(apple, obs, nil, nil)
	if len(d.PushToObsidian) != 1 {
		t.Fatalf("PushToObsidian = %+v, want 1 (apple is newer)", d.PushToObsidian)
	}
	p := d.PushToObsidian[0]
	if p.Body != "milk" || p.Link.AppleID != "apple-1" || p.Link.ObsidianID != "obs-1" {
		t.Fatalf("PushToObsidian[0] = %+v, want body=milk linking apple-1/obs-1", p)
	}
	if len(d.NewLinks) != 0 || len(d.PushToApple) != 0 {
		t.Fatalf("unexpected extra decisions: %+v", d)
	}
}

func TestDecide_ChangedOnAppleOnlyPushesToObsidian(t *testing.T) {
	link := Link{AppleID: "apple-1", AppleHash: Hash("Groceries", "milk"), ObsidianID: "obs-1", ObsidianHash: Hash("Groceries", "milk")}
	apple := []models.Note{{ID: "apple-1", Title: "Groceries", Body: "milk and eggs", Folder: "Shopping", ModTime: fixedTime}}
	obs := []models.Note{{ID: "obs-1", Title: "Groceries", Body: "milk", Folder: "Shopping", ModTime: fixedTime}}
	d := Decide(apple, obs, []Link{link}, nil)
	if len(d.PushToObsidian) != 1 || d.PushToObsidian[0].Body != "milk and eggs" {
		t.Fatalf("PushToObsidian = %+v, want 1 pushing 'milk and eggs'", d.PushToObsidian)
	}
	if len(d.PushToApple) != 0 || len(d.CreateInApple) != 0 || len(d.CreateInObsidian) != 0 {
		t.Fatalf("unexpected extra decisions: %+v", d)
	}
}

func TestDecide_ChangedOnObsidianOnlyPushesToApple(t *testing.T) {
	link := Link{AppleID: "apple-1", AppleHash: Hash("Groceries", "milk"), ObsidianID: "obs-1", ObsidianHash: Hash("Groceries", "milk")}
	apple := []models.Note{{ID: "apple-1", Title: "Groceries", Body: "milk", Folder: "Shopping", ModTime: fixedTime}}
	obs := []models.Note{{ID: "obs-1", Title: "Groceries", Body: "milk and eggs", Folder: "Shopping", ModTime: fixedTime}}
	d := Decide(apple, obs, []Link{link}, nil)
	if len(d.PushToApple) != 1 || d.PushToApple[0].Body != "milk and eggs" {
		t.Fatalf("PushToApple = %+v, want 1 pushing 'milk and eggs'", d.PushToApple)
	}
	if len(d.PushToObsidian) != 0 || len(d.CreateInApple) != 0 || len(d.CreateInObsidian) != 0 {
		t.Fatalf("unexpected extra decisions: %+v", d)
	}
}

func TestDecide_ChangedOnBothSidesConflictNewerWins(t *testing.T) {
	link := Link{AppleID: "apple-1", AppleHash: Hash("Groceries", "milk"), ObsidianID: "obs-1", ObsidianHash: Hash("Groceries", "milk")}
	older := fixedTime
	newer := fixedTime.Add(time.Hour)
	apple := []models.Note{{ID: "apple-1", Title: "Groceries", Body: "milk and eggs", Folder: "Shopping", ModTime: older}}
	obs := []models.Note{{ID: "obs-1", Title: "Groceries", Body: "milk and bread", Folder: "Shopping", ModTime: newer}}
	d := Decide(apple, obs, []Link{link}, nil)
	if len(d.PushToApple) != 1 || d.PushToApple[0].Body != "milk and bread" {
		t.Fatalf("PushToApple = %+v, want 1 pushing the newer obsidian body", d.PushToApple)
	}
	if len(d.PushToObsidian) != 0 {
		t.Fatalf("PushToObsidian = %+v, want 0 (obsidian was newer)", d.PushToObsidian)
	}
}

func TestDecide_UnchangedPairProducesNoDecisions(t *testing.T) {
	link := Link{AppleID: "apple-1", AppleHash: Hash("Groceries", "milk"), ObsidianID: "obs-1", ObsidianHash: Hash("Groceries", "milk")}
	apple := []models.Note{{ID: "apple-1", Title: "Groceries", Body: "milk", Folder: "Shopping", ModTime: fixedTime}}
	obs := []models.Note{{ID: "obs-1", Title: "Groceries", Body: "milk", Folder: "Shopping", ModTime: fixedTime}}
	d := Decide(apple, obs, []Link{link}, nil)
	if len(d.PushToApple) != 0 || len(d.PushToObsidian) != 0 || len(d.CreateInApple) != 0 ||
		len(d.CreateInObsidian) != 0 || len(d.NewLinks) != 0 || len(d.NewPendingDeletes) != 0 {
		t.Fatalf("want no decisions for an unchanged pair, got %+v", d)
	}
}

func TestDecide_DeletedOnAppleQueuesPendingDelete(t *testing.T) {
	link := Link{AppleID: "apple-1", AppleHash: Hash("Groceries", "milk"), ObsidianID: "obs-1", ObsidianHash: Hash("Groceries", "milk")}
	obs := []models.Note{{ID: "obs-1", Title: "Groceries", Body: "milk", Folder: "Shopping", ModTime: fixedTime}}
	d := Decide(nil, obs, []Link{link}, nil) // apple-1 no longer present
	if len(d.NewPendingDeletes) != 1 || d.NewPendingDeletes[0].DeletedOn != "apple" {
		t.Fatalf("NewPendingDeletes = %+v, want 1 with DeletedOn=apple", d.NewPendingDeletes)
	}
	if len(d.RemoveLinks) != 1 || d.RemoveLinks[0].AppleID != "apple-1" {
		t.Fatalf("RemoveLinks = %+v, want the apple-1/obs-1 link", d.RemoveLinks)
	}
	if len(d.PushToApple) != 0 || len(d.PushToObsidian) != 0 {
		t.Fatalf("must not push when a side was deleted: %+v", d)
	}
}

func TestDecide_DeletedOnObsidianQueuesPendingDelete(t *testing.T) {
	link := Link{AppleID: "apple-1", AppleHash: Hash("Groceries", "milk"), ObsidianID: "obs-1", ObsidianHash: Hash("Groceries", "milk")}
	apple := []models.Note{{ID: "apple-1", Title: "Groceries", Body: "milk", Folder: "Shopping", ModTime: fixedTime}}
	d := Decide(apple, nil, []Link{link}, nil) // obs-1 no longer present
	if len(d.NewPendingDeletes) != 1 || d.NewPendingDeletes[0].DeletedOn != "obsidian" {
		t.Fatalf("NewPendingDeletes = %+v, want 1 with DeletedOn=obsidian", d.NewPendingDeletes)
	}
	if len(d.RemoveLinks) != 1 {
		t.Fatalf("RemoveLinks = %+v, want 1", d.RemoveLinks)
	}
}

func TestDecide_BothSidesDeletedDropsLinkSilently(t *testing.T) {
	link := Link{AppleID: "apple-1", AppleHash: Hash("Groceries", "milk"), ObsidianID: "obs-1", ObsidianHash: Hash("Groceries", "milk")}
	d := Decide(nil, nil, []Link{link}, nil)
	if len(d.NewPendingDeletes) != 0 {
		t.Fatalf("NewPendingDeletes = %+v, want 0 when both sides are already gone", d.NewPendingDeletes)
	}
	if len(d.RemoveLinks) != 1 {
		t.Fatalf("RemoveLinks = %+v, want the stale link removed", d.RemoveLinks)
	}
}

// The pass after a deletion was detected: the link row is already gone (the
// detecting pass put it in RemoveLinks), so the surviving Apple note looks
// brand new and unlinked. Without the pending-delete guard it would be
// recreated in the vault — resurrecting exactly the note the user deleted,
// every single sync, forever.
func TestDecide_SurvivingSideOfPendingDeleteIsLeftAlone(t *testing.T) {
	apple := []models.Note{{ID: "apple-1", Title: "Groceries", Body: "milk", Folder: "Shopping", ModTime: fixedTime}}
	pending := []PendingDelete{{AppleID: "apple-1", ObsidianID: "obs-1", Title: "Groceries", DeletedOn: "obsidian"}}
	d := Decide(apple, nil, nil, pending)
	if len(d.CreateInObsidian) != 0 {
		t.Fatalf("CreateInObsidian = %+v, want 0 — the note is awaiting --apply-deletes", d.CreateInObsidian)
	}
	if len(d.CreateInApple) != 0 || len(d.PushToApple) != 0 || len(d.PushToObsidian) != 0 ||
		len(d.NewLinks) != 0 || len(d.NewPendingDeletes) != 0 || len(d.RemoveLinks) != 0 {
		t.Fatalf("want no decisions at all for a note in the pending-delete queue, got %+v", d)
	}
}

// Once --apply-deletes has cleared the queue row, the same note is an
// ordinary unlinked note again.
func TestDecide_ClearedPendingDeleteReleasesTheNote(t *testing.T) {
	apple := []models.Note{{ID: "apple-1", Title: "Groceries", Body: "milk", Folder: "Shopping", ModTime: fixedTime}}
	d := Decide(apple, nil, nil, nil)
	if len(d.CreateInObsidian) != 1 || d.CreateInObsidian[0].ID != "apple-1" {
		t.Fatalf("CreateInObsidian = %+v, want the apple note back in play", d.CreateInObsidian)
	}
}

// The other direction: a surviving Obsidian note whose Apple counterpart was
// deleted must not be recreated in Apple Notes either.
func TestDecide_SurvivingObsidianSideOfPendingDeleteIsLeftAlone(t *testing.T) {
	obs := []models.Note{{ID: "obs-1", Title: "Groceries", Body: "milk", Folder: "Shopping", ModTime: fixedTime}}
	pending := []PendingDelete{{AppleID: "apple-1", ObsidianID: "obs-1", Title: "Groceries", DeletedOn: "apple"}}
	d := Decide(nil, obs, nil, pending)
	if len(d.CreateInApple) != 0 {
		t.Fatalf("CreateInApple = %+v, want 0 — the note is awaiting --apply-deletes", d.CreateInApple)
	}
	if len(d.CreateInObsidian) != 0 || len(d.PushToApple) != 0 || len(d.PushToObsidian) != 0 || len(d.NewLinks) != 0 {
		t.Fatalf("want no decisions at all for a note in the pending-delete queue, got %+v", d)
	}
}

// A queued pending delete must not stop an unrelated note from bootstrapping.
func TestDecide_PendingDeleteDoesNotBlockUnrelatedNotes(t *testing.T) {
	apple := []models.Note{
		{ID: "apple-1", Title: "Groceries", Body: "milk", Folder: "Shopping", ModTime: fixedTime},
		{ID: "apple-2", Title: "Ideas", Body: "a plan", Folder: "Notes", ModTime: fixedTime},
	}
	obs := []models.Note{{ID: "obs-2", Title: "Ideas", Body: "a plan", Folder: "Notes", ModTime: fixedTime}}
	pending := []PendingDelete{{AppleID: "apple-1", ObsidianID: "obs-1", Title: "Groceries", DeletedOn: "obsidian"}}
	d := Decide(apple, obs, nil, pending)
	if len(d.NewLinks) != 1 || d.NewLinks[0].AppleID != "apple-2" {
		t.Fatalf("NewLinks = %+v, want apple-2/obs-2 bootstrap-linked", d.NewLinks)
	}
	if len(d.CreateInObsidian) != 0 || len(d.CreateInApple) != 0 {
		t.Fatalf("unexpected creates: %+v", d)
	}
}

func TestDecide_BootstrapMultipleCandidatesFirstMatchWins(t *testing.T) {
	apple := []models.Note{
		{ID: "apple-1", Title: "Groceries", Body: "milk", Folder: "Shopping", ModTime: fixedTime},
		{ID: "apple-2", Title: "Groceries", Body: "eggs", Folder: "Shopping", ModTime: fixedTime},
	}
	obs := []models.Note{
		{ID: "obs-1", Title: "Groceries", Body: "milk", Folder: "Shopping", ModTime: fixedTime},
	}
	d := Decide(apple, obs, nil, nil)
	if len(d.NewLinks) != 1 {
		t.Fatalf("NewLinks = %+v, want 1 (apple-1 claims obs-1)", d.NewLinks)
	}
	if d.NewLinks[0].AppleID != "apple-1" || d.NewLinks[0].ObsidianID != "obs-1" {
		t.Fatalf("NewLinks[0] = %+v, want apple-1/obs-1", d.NewLinks[0])
	}
	if len(d.CreateInObsidian) != 1 || d.CreateInObsidian[0].ID != "apple-2" {
		t.Fatalf("CreateInObsidian = %+v, want apple-2 (no remaining match)", d.CreateInObsidian)
	}
	if len(d.CreateInApple) != 0 || len(d.PushToApple) != 0 || len(d.PushToObsidian) != 0 {
		t.Fatalf("unexpected extra decisions: %+v", d)
	}
}
