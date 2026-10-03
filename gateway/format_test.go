package gateway

import "testing"

func TestInitials(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, email, want string }{
		{"Mara Linden", "mara@example.com", "ML"},
		{"GitHub", "noreply@github.com", "G"},
		{"", "mara@example.com", "M"},
		{"", "", ""},
		{"  lowercase  ", "", "L"},
	}
	for _, tc := range cases {
		if got := initials(tc.name, tc.email); got != tc.want {
			t.Errorf("initials(%q, %q) = %q, want %q", tc.name, tc.email, got, tc.want)
		}
	}
}

func TestAccountShort(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"me@example.com": "me@",
		"me":             "me",
		"":               "",
		"@example.com":   "@",
	}
	for in, want := range cases {
		if got := accountShort(in); got != want {
			t.Errorf("accountShort(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHumanSize(t *testing.T) {
	t.Parallel()
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{640 * 1024, "640 KB"},
		{1150 * 1024, "1.1 MB"},
		{2 * 1024 * 1024 * 1024, "2.0 GB"},
	}
	for _, tc := range cases {
		if got := humanSize(tc.n); got != tc.want {
			t.Errorf("humanSize(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestParagraphs(t *testing.T) {
	t.Parallel()
	got := paragraphs("one\n\ntwo\nstill two\n\n\nthree")
	want := []string{"one", "two still two", "three"}
	if len(got) != len(want) {
		t.Fatalf("paragraphs = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("paragraphs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if got := paragraphs("   \n\n  "); len(got) != 0 {
		t.Errorf("blank text = %q, want none", got)
	}
}

func TestSlotOf(t *testing.T) {
	t.Parallel()
	cases := map[int]int{0: 1, 1: 2, 4: 5, 9: 5, -3: 1}
	for in, want := range cases {
		if got := slotOf(in); got != want {
			t.Errorf("slotOf(%d) = %d, want %d", in, got, want)
		}
	}
}
