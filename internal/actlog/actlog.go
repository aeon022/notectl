// Package actlog records notectl's user actions in the suite activity log
// (missionctl-core/activity). Titles only, best-effort; sync and mirror
// paths never call it — only things the user asked for.
package actlog

import (
	"sync"
	"time"

	"github.com/aeon022/missionctl-core/activity"
)

const debounce = 10 * time.Minute

var (
	mu    sync.Mutex
	lastW = map[string]time.Time{} // title → last logged "wrote"
)

// Wrote logs "wrote <title>". Repeated saves of the same note within 10
// minutes (editor save spam, a daily note appended to all day) log once.
func Wrote(title string) {
	if title == "" {
		return
	}
	mu.Lock()
	if t, ok := lastW[title]; ok && time.Since(t) < debounce {
		mu.Unlock()
		return
	}
	lastW[title] = time.Now()
	mu.Unlock()
	activity.Log("notectl", "wrote", title)
}

// Deleted logs "deleted <title>". Deleting also clears the write debounce, so
// a note re-created right after is logged again.
func Deleted(title string) {
	if title == "" {
		return
	}
	mu.Lock()
	delete(lastW, title)
	mu.Unlock()
	activity.Log("notectl", "deleted", title)
}

// ResetForTest forgets the write debounce, so tests don't see each other's saves.
func ResetForTest() {
	mu.Lock()
	lastW = map[string]time.Time{}
	mu.Unlock()
}
