package mcpserver

import (
	"strings"
	"testing"
	"time"

	"github.com/aeon022/missionctl-core/activity"
	"github.com/aeon022/notectl/internal/actlog"
)

func activitySandbox(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MISSIONCTL_DATA_DIR", t.TempDir())
	t.Setenv("MISSIONCTL_ACTIVITY", "")
	actlog.ResetForTest()
}

func loggedToday(t *testing.T) []activity.Event {
	t.Helper()
	from, to := activity.Day(time.Now())
	evs, err := activity.Read(from, to)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

func TestWriteToolLogsOnceAndDebounces(t *testing.T) {
	setupTest(t)
	activitySandbox(t)
	args := map[string]any{"title": "Ideen", "body": "erste Fassung"}
	callTool(t, handleWrite, args)
	args["body"] = "zweite Fassung" // same note saved again → still one entry
	callTool(t, handleWrite, args)

	evs := loggedToday(t)
	if len(evs) != 1 || evs[0].Tool != "notectl" || evs[0].Action != "wrote" || evs[0].Title != "Ideen" {
		t.Fatalf("events = %+v", evs)
	}
	if evs[0].Title == "erste Fassung" {
		t.Error("bodies must never be logged")
	}
}

func TestAppendDailyLogsTheDay(t *testing.T) {
	setupTest(t)
	activitySandbox(t)
	callTool(t, handleAppendDailyNote, map[string]any{"content": "x"})
	evs := loggedToday(t)
	if len(evs) != 1 || evs[0].Title != time.Now().Format("2006-01-02") || evs[0].Action != "wrote" {
		t.Fatalf("events = %+v", evs)
	}
}

func TestDeleteToolLogsTitleOnlyAfterSuccess(t *testing.T) {
	vault := setupTest(t)
	activitySandbox(t)
	// not found / ambiguous deletes must not log
	callTool(t, handleDelete, map[string]any{"title": "Gibt es nicht"})
	if n := len(loggedToday(t)); n != 0 {
		t.Fatalf("a delete that deleted nothing logged %d events", n)
	}

	seedDup(t, vault, "9", "Solo")
	callTool(t, handleDelete, map[string]any{"title": "Dup", "folder": "Solo"})
	evs := loggedToday(t)
	if len(evs) != 1 || evs[0].Action != "deleted" || evs[0].Title != "Dup" {
		t.Fatalf("events = %+v", evs)
	}
}

func TestActionStillWorksWhenLoggingIsOff(t *testing.T) {
	setupTest(t)
	activitySandbox(t)
	t.Setenv("MISSIONCTL_ACTIVITY", "off")
	res := callTool(t, handleWrite, map[string]any{"title": "Still saved", "body": "b"}) // fails the test on an error result
	if text := resultText(t, res); !strings.HasPrefix(text, "Wrote: Still saved") {
		t.Errorf("the write itself must work with logging off: %q", text)
	}
	if n := len(loggedToday(t)); n != 0 {
		t.Errorf("logged %d events while off", n)
	}
}
