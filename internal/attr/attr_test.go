package attr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sampleTable() Table {
	return Table{
		"aaaa1111": {
			CricsheetID: "aaaa1111", Name: "JJ Bumrah", CricinfoID: "625383",
			Bat: RightHandBat, Bowl: RightArmPace,
			BatRaw: "Right-handed", BowlRaw: "Right-arm fast",
			Provenance: Sourced, Source: "wikipedia:Jasprit Bumrah", Retrieved: "2026-08-27",
		},
		"bbbb2222": {
			CricsheetID: "bbbb2222", Name: "RA Jadeja", CricinfoID: "234675",
			Bat: LeftHandBat, Bowl: LeftArmOrthodox,
			BatRaw: "Left-handed", BowlRaw: "Slow left-arm orthodox",
			Provenance: Manual, Source: "hand-checked", Retrieved: "2026-08-27",
		},
	}
}

func TestTableRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "players.csv")
	want := sampleTable()
	if err := Save(want, path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("rows = %d, want %d", len(got), len(want))
	}
	for id, w := range want {
		g, ok := got[id]
		if !ok {
			t.Errorf("%s missing after round-trip", id)
			continue
		}
		if g != w {
			t.Errorf("%s round-tripped to %+v, want %+v", id, g, w)
		}
	}
}

// TestSaveIsStable matters because the table is checked in: an unstable row
// order would produce a meaningless diff on every regeneration.
func TestSaveIsStable(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.csv"), filepath.Join(dir, "b.csv")
	if err := Save(sampleTable(), a); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := Save(sampleTable(), b); err != nil {
		t.Fatalf("Save: %v", err)
	}
	ba, _ := os.ReadFile(a)
	bb, _ := os.ReadFile(b)
	if string(ba) != string(bb) {
		t.Error("Save is not byte-stable across runs")
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "absent.csv"))
	if err != nil {
		t.Fatalf("Load of a missing file: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("rows = %d, want 0", len(got))
	}
}

// TestLoadRejectsBadProvenance is the guard that keeps a guess from entering
// the table wearing a sourced label. Every row must declare where it came from.
func TestLoadRejectsBadProvenance(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "empty provenance",
			body: "cricsheet_id,batting_hand,bowling_class,provenance\naaa,RHB,RAP,\n",
			want: "provenance",
		},
		{
			name: "unknown provenance",
			body: "cricsheet_id,batting_hand,bowling_class,provenance\naaa,RHB,RAP,guessed\n",
			want: "provenance",
		},
		{
			name: "missing provenance column",
			body: "cricsheet_id,batting_hand,bowling_class\naaa,RHB,RAP\n",
			want: "missing required column",
		},
		{
			name: "empty identifier",
			body: "cricsheet_id,batting_hand,bowling_class,provenance\n,RHB,RAP,sourced\n",
			want: "empty cricsheet_id",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bad.csv")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil {
				t.Fatal("Load: want error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestLookupsRequireAKnownValue(t *testing.T) {
	tbl := Table{
		"known":   {CricsheetID: "known", Bat: LeftHandBat, Bowl: LegBreak, Provenance: Sourced},
		"partial": {CricsheetID: "partial", Bat: RightHandBat, Bowl: BowlUnknown, Provenance: Sourced},
	}

	if h, ok := tbl.BatOf("known"); !ok || h != LeftHandBat {
		t.Errorf("BatOf(known) = %v, %v", h, ok)
	}
	if c, ok := tbl.BowlOf("known"); !ok || c != LegBreak {
		t.Errorf("BowlOf(known) = %v, %v", c, ok)
	}
	// An unknown value must report as absent, never as a usable zero value.
	if _, ok := tbl.BowlOf("partial"); ok {
		t.Error("BowlOf: an unknown class must report as not known")
	}
	if _, ok := tbl.BatOf("absent"); ok {
		t.Error("BatOf: a missing player must report as not known")
	}
}

func TestAllIsSorted(t *testing.T) {
	got := sampleTable().All()
	if len(got) != 2 {
		t.Fatalf("All() returned %d rows, want 2", len(got))
	}
	if got[0].CricsheetID > got[1].CricsheetID {
		t.Error("All() is not sorted by identifier")
	}
}
