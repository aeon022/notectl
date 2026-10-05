package cmd

import (
	"testing"

	"github.com/aeon022/notectl/internal/config"
	"github.com/aeon022/notectl/internal/syncdispatch"
)

func TestSyncLabel(t *testing.T) {
	cases := []struct {
		src  config.SourceType
		p    syncdispatch.Params
		want string
	}{
		{config.SourceApple, syncdispatch.Params{}, "Syncing Apple Notes"},
		{config.SourceApple, syncdispatch.Params{AppleFolder: "Work"}, "Syncing Apple Notes (folder: Work)"},
		{config.SourceJoplin, syncdispatch.Params{}, "Syncing Joplin"},
		{config.SourceJoplin, syncdispatch.Params{JoplinFolder: "Ideas"}, "Syncing Joplin (notebook: Ideas)"},
		{config.SourceObsidian, syncdispatch.Params{VaultPath: "/v"}, "Syncing vault: /v"},
		{config.SourceMarkdown, syncdispatch.Params{VaultPath: "/m"}, "Syncing vault: /m"},
	}
	for _, c := range cases {
		if got := syncLabel(c.src, c.p); got != c.want {
			t.Errorf("syncLabel(%v) = %q, want %q", c.src, got, c.want)
		}
	}
}
