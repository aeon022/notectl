package models

import "testing"

func TestHasTagAndFilter(t *testing.T) {
	a := Note{Title: "a", Tags: []string{"Work", "idea"}}
	b := Note{Title: "b", Tags: []string{"workshop"}}
	c := Note{Title: "c"}
	if !a.HasTag("work") {
		t.Error("tag match must be case-insensitive")
	}
	if b.HasTag("work") {
		t.Error("tag match must be exact, not a prefix")
	}
	if c.HasTag("x") {
		t.Error("no tags")
	}
	got := FilterByTag([]Note{a, b, c}, "IDEA")
	if len(got) != 1 || got[0].Title != "a" {
		t.Errorf("FilterByTag = %+v", got)
	}
	if FilterByTag([]Note{a}, "nope") != nil {
		t.Error("no match must return nil")
	}
}
