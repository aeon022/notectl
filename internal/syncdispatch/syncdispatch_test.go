package syncdispatch

import (
	"testing"

	"github.com/aeon022/notectl/internal/config"
)

// obsidian and markdown share one backend and must share one cache key,
// otherwise DeleteBySource would leave the other's rows behind.
func TestSourceKey(t *testing.T) {
	for src, want := range map[config.SourceType]string{
		config.SourceApple:    "apple",
		config.SourceJoplin:   "joplin",
		config.SourceObsidian: "obsidian",
		config.SourceMarkdown: "obsidian",
		"":                    "obsidian",
	} {
		if got := SourceKey(src); got != want {
			t.Errorf("SourceKey(%q) = %q, want %q", src, got, want)
		}
	}
}
