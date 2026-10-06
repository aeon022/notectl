package actlog

import (
	"testing"
	"time"

	"github.com/aeon022/missionctl-core/activity"
)

func sandbox(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MISSIONCTL_DATA_DIR", t.TempDir())
	t.Setenv("MISSIONCTL_ACTIVITY", "")
	ResetForTest()
}

func today(t *testing.T) []activity.Event {
	t.Helper()
	from, to := activity.Day(time.Now())
	evs, err := activity.Read(from, to)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

func TestWroteDebouncesSameTitleButNotOthers(t *testing.T) {
	sandbox(t)
	Wrote("Plan")
	Wrote("Plan") // editor save spam
	Wrote("Other")
	evs := today(t)
	if len(evs) != 2 || evs[0].Title != "Plan" || evs[1].Title != "Other" || evs[0].Tool != "notectl" || evs[0].Action != "wrote" {
		t.Fatalf("events = %+v", evs)
	}
}

func TestDebounceExpiresAfterTenMinutes(t *testing.T) {
	sandbox(t)
	Wrote("Plan")
	mu.Lock()
	lastW["Plan"] = time.Now().Add(-11 * time.Minute)
	mu.Unlock()
	Wrote("Plan")
	if n := len(today(t)); n != 2 {
		t.Errorf("a save after the window must log again, got %d events", n)
	}
}

func TestDeletedLogsAndClearsDebounce(t *testing.T) {
	sandbox(t)
	Wrote("Plan")
	Deleted("Plan")
	Wrote("Plan") // re-created right after deleting: a new write
	evs := today(t)
	if len(evs) != 3 || evs[1].Action != "deleted" || evs[2].Action != "wrote" {
		t.Fatalf("events = %+v", evs)
	}
}

func TestEmptyTitleAndDisabled(t *testing.T) {
	sandbox(t)
	Wrote("")
	Deleted("")
	t.Setenv("MISSIONCTL_ACTIVITY", "off")
	Wrote("Plan")
	if n := len(today(t)); n != 0 {
		t.Errorf("nothing should be logged, got %d", n)
	}
}
