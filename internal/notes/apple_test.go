package notes

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	runewidth "github.com/mattn/go-runewidth"
)

func TestTextToHTMLMarkdownBullets(t *testing.T) {
	got := TextToHTML("- one\n* two\n• three")
	want := "<ul><li>one</li><li>two</li><li>three</li></ul>"
	if got != want {
		t.Errorf("TextToHTML bullets = %q, want %q", got, want)
	}
}

func TestTextToHTMLMarkdownCheckboxes(t *testing.T) {
	got := TextToHTML("- [ ] open\n- [x] done")
	if !strings.Contains(got, `Apple-unchecked">☐ open`) {
		t.Errorf("unchecked item not converted: %q", got)
	}
	if !strings.Contains(got, `Apple-checked">☑ done`) {
		t.Errorf("checked item not converted: %q", got)
	}
	if strings.Contains(got, "[ ]") || strings.Contains(got, "[x]") {
		t.Errorf("raw checkbox marker leaked into HTML: %q", got)
	}
}

func TestTextToHTMLInlineStyles(t *testing.T) {
	got := TextToHTML("a **bold** and *italic* and `code` word")
	want := "<div>a <b>bold</b> and <i>italic</i> and <tt>code</tt> word</div>"
	if got != want {
		t.Errorf("TextToHTML inline = %q, want %q", got, want)
	}
}

func TestTextToHTMLInlineInsideListAndHeading(t *testing.T) {
	got := TextToHTML("# The **plan**\n- buy *milk*")
	if !strings.Contains(got, "<h1>The <b>plan</b></h1>") {
		t.Errorf("heading inline not converted: %q", got)
	}
	if !strings.Contains(got, "<li>buy <i>milk</i></li>") {
		t.Errorf("list inline not converted: %q", got)
	}
}

func TestTextToHTMLEscapesInsideInline(t *testing.T) {
	got := TextToHTML("**a<b>** & `x<y`")
	if strings.Contains(got, "<b>a<b>") || !strings.Contains(got, "&lt;") || !strings.Contains(got, "&amp;") {
		t.Errorf("HTML not escaped inside inline spans: %q", got)
	}
}

func TestTextToHTMLUnclosedMarkersStayLiteral(t *testing.T) {
	got := TextToHTML("2 ** 3 is not bold")
	if strings.Contains(got, "<b>") {
		t.Errorf("unpaired ** must not become bold: %q", got)
	}
}

func TestStripHTMLInlineStyles(t *testing.T) {
	got := StripHTML("<div>a <b>bold</b> and <i>italic</i> and <tt>code</tt></div>")
	want := "a **bold** and *italic* and `code`"
	if got != want {
		t.Errorf("StripHTML inline = %q, want %q", got, want)
	}
}

func TestStripHTMLBoldInsideListItem(t *testing.T) {
	got := StripHTML("<ul><li><b>bold</b> item</li></ul>")
	want := "• **bold** item"
	if got != want {
		t.Errorf("StripHTML list bold = %q, want %q", got, want)
	}
}

func TestTextToHTMLStrikethrough(t *testing.T) {
	got := TextToHTML("a ~~done~~ task")
	want := "<div>a <strike>done</strike> task</div>"
	if got != want {
		t.Errorf("TextToHTML strike = %q, want %q", got, want)
	}
}

func TestStripHTMLStrikethrough(t *testing.T) {
	for _, tag := range []string{"strike", "s", "del"} {
		got := StripHTML("<div>a <" + tag + ">done</" + tag + "> task</div>")
		want := "a ~~done~~ task"
		if got != want {
			t.Errorf("StripHTML <%s> = %q, want %q", tag, got, want)
		}
	}
}

func TestRoundTripTextHTMLText(t *testing.T) {
	orig := "# Title\nplain **bold** line\na ~~struck~~ word\n• first\n• second\n☐ open\n☑ done"
	back := StripHTML(TextToHTML(orig))
	if back != orig {
		t.Errorf("round trip mismatch:\norig: %q\nback: %q", orig, back)
	}
}

func TestAppleChecklistStripHTML(t *testing.T) {
	// When Apple Notes imports HTML, it strips class="Apple-checked-list" and leaves <ul><li>☑ item</li></ul>
	html := `<ul><li>☑ done item</li><li><font face="AppleSymbols">☐</font> open item</li></ul>`
	got := StripHTML(html)
	want := "☑ done item\n☐ open item"
	if got != want {
		t.Errorf("StripHTML Apple checklist = %q, want %q", got, want)
	}
}

func TestStripVariationSelectorsAgreeOnWidth(t *testing.T) {
	// "\U0001F6CF\uFE0F" (bed + variation selector) is measured one column
	// wider by lipgloss/charmbracelet-x's ansi width package than by
	// go-runewidth — real disagreement that surfaced as a scrollbar
	// rendering bug on a real note (every downstream fix that tried to
	// paper over the mismatch just moved it somewhere else, since both
	// paths ultimately call the same width function on the same string).
	// Stripping the selector removes the disagreement at its source: both
	// libraries then agree on the width, because there's nothing left for
	// them to disagree about.
	in := "\U0001F6CF\uFE0F Schlafen"
	got := stripVariationSelectors(in)
	want := "\U0001F6CF Schlafen"
	if got != want {
		t.Errorf("stripVariationSelectors(%q) = %q, want %q", in, got, want)
	}
	if lipgloss.Width(got) != runewidth.StringWidth(got) {
		t.Errorf("after stripping, lipgloss.Width(%q)=%d and runewidth.StringWidth=%d should agree, but don't", got, lipgloss.Width(got), runewidth.StringWidth(got))
	}
}

func TestStripHTMLIgnoresFormattingWhitespaceBetweenTags(t *testing.T) {
	// Apple Notes' actual HTML (confirmed on a real note) puts a literal
	// newline between list items purely for markup readability:
	// "<li>a</li>\n<li>b</li>". That whitespace is not content — every real
	// HTML renderer collapses it — but StripHTML was writing it out as a
	// second, spurious blank line between every single list item.
	html := "<ul>\n<li>Item A</li>\n<li>Item B</li>\n<li>Item C</li>\n</ul>"
	got := StripHTML(html)
	want := "• Item A\n• Item B\n• Item C"
	if got != want {
		t.Errorf("StripHTML(%q) = %q, want %q", html, got, want)
	}
}

func TestStripHTMLEmojiSpaces(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{`<div><font face=".AppleColorEmojiUI">🛏️</font><b>Schlafen</b></div>`, "🛏 **Schlafen**"}, // variation selector (U+FE0F) stripped — see stripVariationSelectors
		{`<li><font face=".AppleColorEmojiUI">❌</font>KEINE Decke</li>`, `• ❌ KEINE Decke`},
		{`<div>📋<b>Orga</b></div>`, `📋 **Orga**`},
		{`<div>☑done</div>`, `☑ done`},
		{`<div>☐open</div>`, `☐ open`},
	}
	for _, tc := range tests {
		got := StripHTML(tc.in)
		if got != tc.want {
			t.Errorf("StripHTML(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCleanLineMarkers(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"• ☐ open item", "☐ open item"},
		{"• ☑ done item", "☑ done item"},
		{"☐ ☐ double uncheck", "☐ double uncheck"},
		{"☑ ☑ double check", "☑ double check"},
		{"• • double bullet", "• double bullet"},
		{"☐ • check bullet", "☐ check bullet"},
		{"☑ • check bullet", "☑ check bullet"},
	}
	for _, tc := range tests {
		got := cleanLineMarkers(tc.in)
		if got != tc.want {
			t.Errorf("cleanLineMarkers(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

