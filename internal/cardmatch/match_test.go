package cardmatch

import (
	"image"
	"image/color"
	"path/filepath"
	"testing"

	"gwentcoachmvp/internal/cardindex"
)

func pattern(reverse bool) Feature {
	var f Feature
	for y := 0; y < 8; y++ {
		for x := 0; x < 9; x++ {
			v := uint8(x * 28)
			if reverse {
				v = 255 - v
			}
			f.Gray[y*9+x] = v
		}
	}
	return f
}

func fixture(t testing.TB) *cardindex.Index {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cards.db")
	v := cardindex.Version{Data: cardindex.DataVersion, Rules: cardindex.RulesVersion}
	if err := cardindex.Import(path, v, []string{"../../data/cards/14.10.0/en/cards.json"}); err != nil {
		t.Fatal(err)
	}
	db, err := cardindex.Open(path, v)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestMatchContracts(t *testing.T) {
	db := fixture(t)
	f := File{Version: db.Version(), FeatureVersion: FeatureVersion, Templates: []Template{{113301, pattern(false)}, {113301, pattern(false)}, {113310, pattern(true)}}}
	i, err := New(f, db)
	if err != nil {
		t.Fatal(err)
	}
	thresholds := Thresholds{MaxDistance: 0, MinGap: 1}
	r, err := i.Match(pattern(false), nil, thresholds)
	if err != nil || r.CardID != 113301 || r.Status != "candidate" || r.RunnerDistance != 64 || r.Calibration != "unvalidated" {
		t.Fatal(r, err)
	}
	if _, err := i.Match(pattern(false), []int{}, thresholds); err == nil {
		t.Fatal("empty candidates accepted")
	}
	if _, err := i.Match(pattern(false), []int{999}, thresholds); err == nil {
		t.Fatal("unknown candidate accepted")
	}
	if _, err := i.Match(pattern(false), nil, Thresholds{}); err == nil {
		t.Fatal("implicit thresholds accepted")
	}
	if _, err := i.Match(Feature{}, nil, thresholds); err == nil {
		t.Fatal("blank feature accepted")
	}
	r, err = i.Match(pattern(false), []int{113310}, thresholds)
	if err != nil || r.Status != "unknown" || r.Reason != "absolute_distance" || r.CardID != 0 {
		t.Fatal(r, err)
	}
	// Distinct card IDs with repeated art cannot be disambiguated by art alone.
	f.Templates = append(f.Templates, Template{152405, pattern(false)})
	i, err = New(f, db)
	if err != nil {
		t.Fatal(err)
	}
	r, err = i.Match(pattern(false), nil, thresholds)
	if err != nil || r.Status != "unknown" || r.Reason != "ambiguous" || r.CardID != 0 {
		t.Fatal(r, err)
	}
	r, err = i.Match(pattern(false), []int{113301, 113301, 113310}, thresholds)
	if err != nil || r.CardID != 113301 || r.Candidates != 2 {
		t.Fatal(r, err)
	}
	// A nonzero near-collision gap also rejects, not just exact ties.
	near := pattern(false)
	near.Gray[0] = 255
	f.Templates = []Template{{113301, pattern(false)}, {113310, near}}
	i, err = New(f, db)
	if err != nil {
		t.Fatal(err)
	}
	r, err = i.Match(pattern(false), nil, Thresholds{MaxDistance: 2, MinGap: 2})
	if err != nil || r.Reason != "ambiguous" || r.RunnerDistance != 1 {
		t.Fatal(r, err)
	}
	f.Version.Data = "other"
	if _, err := New(f, db); err == nil {
		t.Fatal("version accepted")
	}
	f.Version = db.Version()
	f.FeatureVersion = "other"
	if _, err := New(f, db); err == nil {
		t.Fatal("feature version accepted")
	}
	f.FeatureVersion = FeatureVersion
	f.Templates = nil
	if _, err := New(f, db); err == nil {
		t.Fatal("empty templates accepted")
	}
	f.Templates = []Template{{999, pattern(false)}}
	if _, err := New(f, db); err == nil {
		t.Fatal("unknown template ID")
	}
	f.Templates = []Template{{113301, Feature{}}}
	if _, err := New(f, db); err == nil {
		t.Fatal("blank template")
	}
}

func gradient() *image.Gray {
	img := image.NewGray(image.Rect(0, 0, 180, 160))
	for y := 0; y < 160; y++ {
		for x := 0; x < 180; x++ {
			img.SetGray(x, y, color.Gray{Y: uint8(x)})
		}
	}
	return img
}

func TestNormalize(t *testing.T) {
	img := gradient()
	f, err := Normalize(img, ROI{0, 0, 180, 160})
	if err != nil || f.hash() != 0 || f.Gray[0] != 9 || f.Gray[8] != 169 {
		t.Fatal(f, err)
	}
	for _, roi := range []ROI{{-1, 0, 180, 160}, {0, 0, 181, 160}, {0, 0, 8, 8}, {0, 0, 0, 0}, {0, 0, 180, -1}, {180, 0, 9, 8}} {
		if _, err := Normalize(img, roi); err == nil {
			t.Fatal("invalid ROI", roi)
		}
	}
	if _, err := Normalize(nil, ROI{}); err == nil {
		t.Fatal("nil image accepted")
	}
	if _, err := Normalize(image.NewGray(img.Bounds()), ROI{0, 0, 180, 160}); err == nil {
		t.Fatal("blank accepted")
	}
	if _, err := Normalize(image.NewNRGBA(img.Bounds()), ROI{0, 0, 180, 160}); err == nil {
		t.Fatal("transparent accepted")
	}
	// Subimage bounds retain original coordinates.
	sub := img.SubImage(image.Rect(20, 20, 160, 140))
	if _, err := Normalize(sub, ROI{20, 20, 140, 120}); err != nil {
		t.Fatal(err)
	}
	if _, err := Normalize(sub, ROI{0, 0, 140, 120}); err == nil {
		t.Fatal("implicit coordinate remapping")
	}
}

func BenchmarkNormalize(b *testing.B) {
	img := gradient()
	roi := ROI{0, 0, 180, 160}
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		if _, err := Normalize(img, roi); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMatch(b *testing.B) {
	for _, count := range []int{25, 1381} {
		name := "deck25"
		if count == 1381 {
			name = "all1381"
		}
		b.Run(name, func(b *testing.B) {
			i := &Index{byID: map[int][]entry{}}
			ids := make([]int, count)
			for n := 0; n < count; n++ {
				ids[n] = n + 1
				i.byID[n+1] = []entry{{uint64(n) * 0x9e3779b97f4a7c15}}
			}
			f := pattern(false)
			thresholds := Thresholds{MaxDistance: 8, MinGap: 2}
			b.ReportAllocs()
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				if _, err := i.Match(f, ids, thresholds); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
