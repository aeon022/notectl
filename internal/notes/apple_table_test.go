package notes

import (
	"reflect"
	"strings"
	"testing"
)

func TestTableRoundTrip(t *testing.T) {
	md := []string{"| Name | Wert |", "| --- | --- |", `| a\|b | 1 |`, "| leer |  |"}
	if !looksLikeMarkdownTable(md) {
		t.Fatal("not recognised as table")
	}
	html := tableMarkdownToHTML(md)
	back := tableBlockToMarkdown(html)
	want := strings.Join([]string{"| Name | Wert |", "| --- | --- |", `| a\|b | 1 |`, "| leer |  |"}, "\n")
	if back != want {
		t.Errorf("round trip changed table:\n got: %q\nwant: %q", back, want)
	}
}

func TestTableRaggedRowsArePadded(t *testing.T) {
	html := `<table><tr><th>A</th><th>B</th><th>C</th></tr><tr><td>1</td></tr></table>`
	got := tableBlockToMarkdown(html)
	want := "| A | B | C |\n| --- | --- | --- |\n| 1 |  |  |"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
	if tableBlockToMarkdown(`<table></table>`) != "" {
		t.Error("table without rows must be empty")
	}
}

func TestLooksLikeMarkdownTable(t *testing.T) {
	cases := []struct {
		in   []string
		want bool
	}{
		{[]string{"| a |", "| - |"}, true},
		{[]string{"| a |", "| --- |", "| 1 |"}, true},
		{[]string{"| a |"}, false},                    // no separator row
		{[]string{"| a |", "| b |"}, false},           // second row isn't a separator
		{[]string{"| a |", "| --- |", "text"}, false}, // a non-pipe line
		{[]string{"", "| a |", "", "| :-: |"}, true},  // blanks ignored, alignment colon ok
	}
	for _, c := range cases {
		if got := looksLikeMarkdownTable(c.in); got != c.want {
			t.Errorf("looksLikeMarkdownTable(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	if isTableSeparator("| | |") || !isTableSeparator("|:--|--:|") {
		t.Error("isTableSeparator needs at least one dash and only |-: chars")
	}
}

func TestSplitTableCells(t *testing.T) {
	got := splitTableCells(`| a | b\|c | |`)
	if want := []string{"a", "b|c", ""}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q want %q", got, want)
	}
}
