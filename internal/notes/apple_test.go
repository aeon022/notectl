package notes

import (
	"strings"
	"testing"
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

func TestStripHTMLEmojiSpaces(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{`<div><font face=".AppleColorEmojiUI">🛏️</font><b>Schlafen</b></div>`, `🛏️ **Schlafen**`},
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

