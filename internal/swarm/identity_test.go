package swarm

import (
	"strings"
	"testing"
)

func TestParseAddress(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in    string
		ok    bool
		color string
		anim  string
		hash  string
		sid   string
	}{
		{"aliceblue-tiger", true, "aliceblue", "tiger", "", ""},
		{"aliceblue-tiger-1a2b", true, "aliceblue", "tiger", "1a2b", ""},
		{"AliceBlue-Tiger-1A2B", true, "aliceblue", "tiger", "1a2b", ""},
		// Hyphenated animal name, no shorthash.
		{"red-polar-bear", true, "red", "polar-bear", "", ""},
		// Hyphenated animal with shorthash.
		{"red-polar-bear-abcd", true, "red", "polar-bear", "abcd", ""},
		// Long color name shouldn't be mistaken for a UUID (regression).
		{"lightgoldenrodyellow-polar-bear-abcd", true, "lightgoldenrodyellow", "polar-bear", "abcd", ""},
		{"mediumaquamarine-saltwater-crocodile", true, "mediumaquamarine", "saltwater-crocodile", "", ""},
		// Canonical UUID.
		{"123e4567-e89b-12d3-a456-426614174000", true, "", "", "", "123e4567-e89b-12d3-a456-426614174000"},
		// Bare 32-hex.
		{"0123456789abcdef0123456789abcdef", true, "", "", "", "0123456789abcdef0123456789abcdef"},
		// Non-hex 4-char tail is NOT a shorthash.
		{"red-polar-bear-zzzz", true, "red", "polar-bear-zzzz", "", ""},
		// Malformed.
		{"", false, "", "", "", ""},
		{"onlyone", false, "", "", "", ""},
	}
	for _, c := range cases {
		got, ok := ParseAddress(c.in)
		if ok != c.ok {
			t.Errorf("ParseAddress(%q) ok=%v want %v", c.in, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if got.Color != c.color || got.Animal != c.anim || got.ShortHash != c.hash || got.SessionID != c.sid {
			t.Errorf("ParseAddress(%q) = %+v", c.in, got)
		}
	}
}

func TestAssignStable(t *testing.T) {
	t.Parallel()
	cfg := Default()
	id := "123e4567-e89b-12d3-a456-426614174000"
	a := Assign(id, cfg)
	b := Assign(id, cfg)
	if a != b {
		t.Fatalf("Assign not deterministic: %+v vs %+v", a, b)
	}
	if a.Color == "" || a.Animal == "" {
		t.Fatalf("Assign returned empty identity: %+v", a)
	}
}

func TestPatrioticDefaults(t *testing.T) {
	t.Parallel()
	list := animalList(Default())
	if len(list) < 100 {
		t.Fatalf("patriotic identity list has only %d entries", len(list))
	}
	seen := make(map[string]bool, len(list))
	for _, name := range list {
		if strings.Contains(name, "-") {
			t.Fatalf("patriotic identity %q is not a single word", name)
		}
		if seen[name] {
			t.Fatalf("duplicate patriotic identity %q", name)
		}
		seen[name] = true
		if err := ValidateAnimalName(name); err != nil {
			t.Fatalf("invalid patriotic identity %q: %v", name, err)
		}
	}

	for i := range 1_000 {
		identity := Assign(string(rune(i)), Default())
		if !seen[identity.Color] || !seen[identity.Animal] {
			t.Fatalf("Assign returned identity outside patriotic list: %+v", identity)
		}
		if identity.Color == identity.Animal {
			t.Fatalf("Assign returned duplicate words: %+v", identity)
		}
	}
}

func TestShortHash(t *testing.T) {
	t.Parallel()
	if got := ShortHash("abcdefghij"); got != "ghij" {
		t.Errorf("ShortHash long got %q", got)
	}
	if got := ShortHash("XY"); got != "xy" {
		t.Errorf("ShortHash short got %q", got)
	}
}
