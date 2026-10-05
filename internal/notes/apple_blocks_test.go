package notes

import (
	"reflect"
	"strings"
	"testing"
)

func TestSplitTopLevelElements(t *testing.T) {
	cases := []struct {
		name, in string
		want     []string
	}{
		{"flat divs", `<div>a</div><div>b</div>`, []string{`<div>a</div>`, `<div>b</div>`}},
		{"nesting stays in one block", `<ul><li>x</li><li><div>y</div></li></ul><div>z</div>`,
			[]string{`<ul><li>x</li><li><div>y</div></li></ul>`, `<div>z</div>`}},
		{"void tags don't open depth", `<div>a<br>b</div><div>c</div>`, []string{`<div>a<br>b</div>`, `<div>c</div>`}},
		{"bare text run between elements", `<div>a</div>tail<div>b</div>`, []string{`<div>a</div>`, `tail`, `<div>b</div>`}},
		{"top-level void", `<div>a</div><br><div>b</div>`, []string{`<div>a</div>`, `<br>`, `<div>b</div>`}},
		{"unterminated tail is kept", `<div>a</div><div>open`, []string{`<div>a</div>`, `<div>open`}},
		{"empty", ``, nil},
	}
	for _, c := range cases {
		if got := splitTopLevelElements(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestParseBlocksDropsEmptyAndKeepsRaw(t *testing.T) {
	html := `<div><h1>Titel</h1></div><div><br></div><ul><li>eins</li><li>zwei</li></ul>`
	blocks := ParseBlocks(html)
	if len(blocks) != 2 {
		t.Fatalf("blocks = %+v (the blank <div><br></div> must be dropped)", blocks)
	}
	if blocks[1].RawHTML != `<ul><li>eins</li><li>zwei</li></ul>` {
		t.Errorf("RawHTML must be byte-exact, got %q", blocks[1].RawHTML)
	}
	if !strings.Contains(blocks[1].Plain, "eins") || !strings.Contains(blocks[1].Plain, "zwei") {
		t.Errorf("Plain = %q", blocks[1].Plain)
	}
	if !strings.Contains(BlocksToPlain(blocks), "\n\n") {
		t.Error("blocks must be separated by a blank line")
	}
}

func TestLCSIndices(t *testing.T) {
	a := []string{"A", "B", "C", "D"}
	b := []string{"A", "X", "C", "D", "D"}
	got := lcsIndices(a, b)
	want := map[int]int{0: 0, 2: 2, 3: 3} // segment idx -> original idx
	if !reflect.DeepEqual(got, want) {
		t.Errorf("lcsIndices = %v, want %v", got, want)
	}
	if len(lcsIndices(nil, b)) != 0 || len(lcsIndices(a, nil)) != 0 {
		t.Error("empty side must match nothing")
	}
}

// The whole point of Block: an edit elsewhere must not downgrade an
// untouched checklist/table back to regenerated markup.
func TestReconcileKeepsUntouchedRawHTML(t *testing.T) {
	checklist := `<ul class="checklist"><li>milch</li><li>brot</li></ul>`
	orig := ParseBlocks(`<div>Alt</div>` + checklist)
	if len(orig) != 2 {
		t.Fatalf("setup: %d blocks", len(orig))
	}
	edited := "Neu\n\n" + orig[1].Plain
	got := ReconcileBlocks(orig, edited)
	if !strings.Contains(got, checklist) {
		t.Errorf("untouched checklist lost its original markup:\n%s", got)
	}
	if strings.Contains(got, "Alt") || !strings.Contains(got, "Neu") {
		t.Errorf("edited block not regenerated:\n%s", got)
	}
}

func TestReconcileDeleteAndEmpty(t *testing.T) {
	orig := ParseBlocks(`<div>eins</div><div>zwei</div>`)
	got := ReconcileBlocks(orig, orig[1].Plain) // user deleted the first block
	if strings.Contains(got, "eins") || !strings.Contains(got, "zwei") {
		t.Errorf("deleted block came back or kept block lost: %q", got)
	}
	if ReconcileBlocks(orig, " \n\n ") != "" {
		t.Error("blank edit must yield an empty body")
	}
	if got := ReconcileBlocks(nil, "neu"); !strings.Contains(got, "neu") {
		t.Errorf("no originals: %q", got)
	}
}

func TestSplitPlainBlocksNormalizesCRLF(t *testing.T) {
	got := splitPlainBlocks("a\r\n\r\n\r\nb\n \nc")
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q want %q", got, want)
	}
}
