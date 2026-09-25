package ingest

import "testing"

func TestScrubReplacesPersonalDataAndKeepsTheLineReadable(t *testing.T) {
	cases := map[string]string{
		"Login failed for emma.stone+test@example.co.uk.":                "Login failed for [email].",
		`Loading C:\Users\Emma\AppData\save.dat`:                         `Loading C:\Users\[user]\AppData\save.dat`,
		"at /Users/emma/game/Build/Player.cs:42":                         "at /Users/[user]/game/Build/Player.cs:42",
		"GET /v1 Authorization: Bearer abc.def-123 failed":               "GET /v1 Authorization: Bearer [token] failed",
		"retry password=hunter2 now":                                     "retry password=[redacted] now",
		"paid with 4242 4242 4242 4242 ok":                               "paid with [card] ok",
		"connect to 192.168.1.20:443 timed out":                          "connect to [ip]:443 timed out",
		"token eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.abcdefghijk expired": "token [token] expired",
		"/Users/Shared/Game/log.txt":                                     "/Users/Shared/Game/log.txt",
	}
	for in, want := range cases {
		if got := Scrub(in); got != want {
			t.Errorf("Scrub(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScrubLeavesWhatIsNotPersonalAlone(t *testing.T) {
	for _, text := range []string{
		"NullReferenceException: Object reference not set to an instance of an object",
		"Loaded scene Arena_01 in 1.25s",
		"version 1.4.2 build 1234567890123",
		"version 10.0.19045.3693",
		"order GPA.3372-0015-5540-12345 refused",
		"UnityEngine.Debug:LogError (object) (at Assets/Scripts/Shop.cs:88)",
		"a@b is not an address",
		"",
	} {
		if got := Scrub(text); got != text {
			t.Errorf("Scrub(%q) changed it to %q", text, got)
		}
	}
}

func BenchmarkScrubACleanLine(b *testing.B) {
	line := "NullReferenceException: Object reference not set to an instance of an object"
	for b.Loop() {
		Scrub(line)
	}
}
