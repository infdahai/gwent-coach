package observe

import "testing"

func TestActionKinds(t *testing.T) {
	for _, tc := range []struct {
		kind    string
		allowed bool
	}{
		{"click", true}, {"hold", true}, {"hover", true}, {"escape", true},
		{"scroll-down", false}, {"", false}, {"wheel", false},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			a := Action{Kind: tc.kind, Reference: "report.json", StopFile: "pause"}
			if got := a.Validate() == nil; got != tc.allowed {
				t.Fatalf("Validate(%q) allowed=%v", tc.kind, got)
			}
		})
	}
}
