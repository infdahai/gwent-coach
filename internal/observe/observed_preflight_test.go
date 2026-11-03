package observe

import (
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// completeObservedFixture extends observedConfigFixture's lifecycle model so
// that all three result phases (victory, defeat, draw) are present.
func completeObservedFixture(t *testing.T) (string, ObservedMatchConfig, func(string, any) string) {
	t.Helper()
	path, cfg, write := observedConfigFixture(t)
	model, err := LoadPhases(filepath.Join(filepath.Dir(path), cfg.ModelPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"victory", "draw"} {
		model.Phases = append(model.Phases, Phase{Name: name, Anchors: model.Phases[0].Anchors})
	}
	cfg.ModelSHA256 = write(cfg.ModelPath, model)
	write("match.json", cfg)
	return path, cfg, write
}

func TestPreflightValidCompleteConfig(t *testing.T) {
	path, _, _ := completeObservedFixture(t)
	r := PreflightObservedMatch(path)
	if r.LoadResult != "ok" {
		t.Fatalf("load result: %s", r.LoadResult)
	}
	if !r.OfflineOnly || r.LiveQualified {
		t.Fatalf("offline_only=%v live_qualified=%v", r.OfflineOnly, r.LiveQualified)
	}
	if !r.Lifecycle.ResultCoverage.Ready {
		t.Fatal("expected ready result coverage")
	}
	if !r.ResourcesReady {
		t.Fatal("expected resources ready")
	}
	if got := PreflightCheckResult(r); got != "ok" {
		t.Fatalf("check result: %s", got)
	}
	if len(r.Plays) != 1 || r.Plays[0].ModelValid != "ok" {
		t.Fatalf("plays: %+v", r.Plays)
	}
	if r.Hand.NamesMode {
		t.Fatal("fixture is art mode")
	}
	if r.Hand.DBValid != "ok" || r.Hand.TmplValid != "ok" {
		t.Fatalf("hand: %+v", r.Hand)
	}
}

func TestPreflightResultCoverageRequiresAllThree(t *testing.T) {
	// The base fixture declares only defeat.
	path, _, _ := observedConfigFixture(t)
	r := PreflightObservedMatch(path)
	if r.LoadResult != "ok" {
		t.Fatalf("load result: %s", r.LoadResult)
	}
	if r.Lifecycle.ResultCoverage.Ready || r.ResourcesReady {
		t.Fatal("partial result coverage must not be ready")
	}
	blocking := PreflightCheckResult(r)
	for _, gap := range []string{"victory", "draw"} {
		if !strings.Contains(blocking, gap) {
			t.Fatalf("expected gap %q in %q", gap, blocking)
		}
	}
}

func TestPreflightBlocksBrokenResources(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ObservedMatchConfig)
		want   string
	}{
		{"bad database", func(c *ObservedMatchConfig) { c.Hand.Database = "missing.db" }, "missing.db"},
		{"bad template hash", func(c *ObservedMatchConfig) { c.Hand.TemplatesSHA256 = strings.Repeat("0", 64) }, "digest mismatch"},
		{"unknown schema", func(c *ObservedMatchConfig) { c.Kind = "unknown-v1" }, "complete observed-match configuration required"},
		{"bad board roi", func(c *ObservedMatchConfig) {
			c.Plays[0].BoardROI = image.Rect(0, 0, 10, 10)
		}, "invalid or unsupported safe play"},
		{"bad play model", func(c *ObservedMatchConfig) { c.Plays[0].ModelPath = "missing-play.json" }, "missing-play.json"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path, cfg, write := completeObservedFixture(t)
			tc.mutate(&cfg)
			write("match.json", cfg)
			r := PreflightObservedMatch(path)
			if r.LoadResult == "ok" {
				t.Fatalf("expected blocking load result, got ok (%+v)", r)
			}
			if !strings.Contains(r.LoadResult, tc.want) {
				t.Fatalf("load result %q does not mention %q", r.LoadResult, tc.want)
			}
			if got := PreflightCheckResult(r); got == "ok" {
				t.Fatal("expected non-ok check result")
			}
			if r.ResourcesReady {
				t.Fatal("resources must not be ready on blocking error")
			}
		})
	}
}

func TestPreflightInvalidNames(t *testing.T) {
	path, cfg, write := completeObservedFixture(t)
	cfg.Hand.Names = []ObservedCardName{
		{Name: "Wrong Name", CardID: 142306},
		{Name: "Also Wrong", CardID: 142315},
	}
	cfg.Hand.Templates, cfg.Hand.TemplatesSHA256 = "", ""
	cfg.Hand.GuardSlots = nil
	write("match.json", cfg)

	r := PreflightObservedMatch(path)
	if r.LoadResult == "ok" {
		t.Fatal("expected name validation failure")
	}
	if !strings.Contains(r.LoadResult, "does not match") && !strings.Contains(r.LoadResult, "competing") {
		t.Fatalf("unexpected error: %s", r.LoadResult)
	}
	if PreflightCheckResult(r) == "ok" {
		t.Fatal("expected blocking")
	}
}

func TestPreflightForeignRuntimePathClassification(t *testing.T) {
	path, cfg, write := completeObservedFixture(t)
	cfg.StopFile = `D:\gwent\STOP`
	cfg.StateDir = `D:\gwent\sessions`
	write("match.json", cfg)

	r := PreflightObservedMatch(path)
	var stopClass string
	for _, ap := range r.AssetPaths {
		if ap.Key == "stop_file" {
			stopClass = ap.Class
			if ap.ExistsHere {
				t.Fatal("must not claim a foreign path exists")
			}
		}
	}
	if stopClass != "foreign-absolute" {
		t.Fatalf("stop_file class=%q", stopClass)
	}
	if r.LoadResult != "ok" {
		t.Fatalf("load result: %s", r.LoadResult)
	}
	if !r.Lifecycle.ResultCoverage.Ready || !r.ResourcesReady {
		t.Fatalf("expected ready: %+v", r)
	}
}

func TestPreflightDoesNotCreateFiles(t *testing.T) {
	path, _, _ := completeObservedFixture(t)
	dir := filepath.Dir(path)
	before := dirEntries(t, dir)

	_ = PreflightObservedMatch(path)

	after := dirEntries(t, dir)
	if len(before) != len(after) {
		t.Fatalf("file set changed: before=%v after=%v", before, after)
	}
	for name := range before {
		if !after[name] {
			t.Fatalf("file disappeared: %s", name)
		}
	}
}

func TestPreflightMissingFile(t *testing.T) {
	r := PreflightObservedMatch(filepath.Join(t.TempDir(), "missing.json"))
	if r.LoadResult == "ok" {
		t.Fatal("expected read error")
	}
	if PreflightCheckResult(r) == "ok" {
		t.Fatal("expected blocking")
	}
}

func dirEntries(t *testing.T, dir string) map[string]bool {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, e := range ents {
		out[e.Name()] = true
	}
	return out
}
