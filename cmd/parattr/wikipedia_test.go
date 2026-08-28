package main

import "testing"

// TestCleanWikitext covers the markup that actually appears in cricketer
// infoboxes. The list-template case is the one that matters most: deleting
// {{ubl|...}} instead of unwrapping it silently empties the field, which
// presents as a missing attribute rather than as a parse failure.
func TestCleanWikitext(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"plain", "Right-handed", "Right-handed"},
		{"wikilink", "[[Right-arm fast]]", "Right-arm fast"},
		{"piped wikilink", "[[Off spin|off break]]", "off break"},
		{
			name: "unbulleted list is unwrapped, not deleted",
			raw:  "{{ubl|Right-arm medium|Right-arm [[off break]]}}",
			want: "Right-arm medium, Right-arm off break",
		},
		{
			name: "piped links inside a list survive the split",
			raw:  "{{ubl|Right-arm [[Off spin|off break]], [[Leg spin|leg break]]}}",
			want: "Right-arm off break, leg break",
		},
		{"plainlist", "{{plainlist|Left-arm fast|Left-arm medium}}", "Left-arm fast, Left-arm medium"},
		{"nowrap", "{{nowrap|Slow left-arm orthodox}}", "Slow left-arm orthodox"},
		{"named parameters are dropped", "{{ubl|class=nowrap|Right-arm fast}}", "Right-arm fast"},
		{"unknown template is dropped", "Right-arm fast{{efn|a note}}", "Right-arm fast"},
		{"citation is dropped", "Right-arm fast{{cite web|url=http://x}}", "Right-arm fast"},
		{"ref tag", "Right-arm fast<ref>Source</ref>", "Right-arm fast"},
		{"self-closing ref", "Right-arm fast<ref name=x/>", "Right-arm fast"},
		{"comment", "Right-arm fast<!-- check this -->", "Right-arm fast"},
		{"html tag", "Right-arm fast<br/>", "Right-arm fast"},
		{"bold", "'''Right-arm fast'''", "Right-arm fast"},
		{"collapsed whitespace", "Right-arm    fast", "Right-arm fast"},
		{"nested wrappers", "{{ubl|{{nowrap|Right-arm fast}}|Left-arm spin}}", "Right-arm fast, Left-arm spin"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := cleanWikitext(tc.raw); got != tc.want {
				t.Errorf("cleanWikitext(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestInfoboxField(t *testing.T) {
	const article = `{{Infobox cricketer
| name = Test Player
| country = India
| batting = Right-handed
| bowling = {{ubl|Right-arm medium|Right-arm [[off break]]}}
| role = [[All-rounder]]
}}

Some prose.`

	tests := []struct {
		field string
		want  string
	}{
		{"batting", "Right-handed"},
		{"bowling", "Right-arm medium, Right-arm off break"},
		{"country", "India"},
		{"role", "All-rounder"},
		{"absent", ""},
	}
	for _, tc := range tests {
		t.Run(tc.field, func(t *testing.T) {
			if got := infoboxField(article, tc.field); got != tc.want {
				t.Errorf("infoboxField(%q) = %q, want %q", tc.field, got, tc.want)
			}
		})
	}
}

// TestInfoboxFieldToleratesSpacing covers the alignment styles editors use.
func TestInfoboxFieldToleratesSpacing(t *testing.T) {
	for _, article := range []string{
		"{{Infobox cricketer\n| bowling = Right-arm fast\n}}",
		"{{Infobox cricketer\n|bowling=Right-arm fast\n}}",
		"{{Infobox cricketer\n|          bowling = Right-arm fast\n}}",
		"{{Infobox cricketer\n|  Bowling  =  Right-arm fast  \n}}",
	} {
		if got := infoboxField(article, "bowling"); got != "Right-arm fast" {
			t.Errorf("infoboxField over %q = %q, want %q", article, got, "Right-arm fast")
		}
	}
}
