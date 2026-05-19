package session

import "testing"

func TestParseAiTmuxVersion(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"ai-tmux 0.9.1\n", "0.9.1"},
		{"ai-tmux dev\n", "dev"},
		{"ai-tmux 1.0.0", "1.0.0"},
		{"", ""},
		{"onlyone", ""},
	}
	for _, tc := range cases {
		got := parseAiTmuxVersion(tc.in)
		if got != tc.want {
			t.Errorf("parseAiTmuxVersion(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSemverAtLeast(t *testing.T) {
	cases := []struct {
		v, min string
		want   bool
	}{
		{"0.9.1", "0.9.0", true},
		{"0.9.0", "0.9.0", true},
		{"0.8.9", "0.9.0", false},
		{"1.0.0", "0.9.0", true},
		{"0.9.0", "1.0.0", false},
		{"dev", "0.9.0", true},   // dev builds skip check
		{"0.9.0", "dev", true},   // dev minimum skips check
		{"0.10.0", "0.9.0", true},
		{"0.9.0", "0.10.0", false},
		{"2.0.0", "1.9.9", true},
	}
	for _, tc := range cases {
		got := semverAtLeast(tc.v, tc.min)
		if got != tc.want {
			t.Errorf("semverAtLeast(%q, %q) = %v, want %v", tc.v, tc.min, got, tc.want)
		}
	}
}
