package observe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gwentcoachmvp/internal/cardindex"
	"gwentcoachmvp/internal/cardmatch"
)

// PreflightReport summarises offline validation of an observed-match config.
// A successful preflight is never a live qualification.
type PreflightReport struct {
	ConfigPath    string `json:"config_path"`
	OfflineOnly   bool   `json:"offline_only"`
	LiveQualified bool   `json:"live_qualified"`

	// LoadResult is the authoritative result of LoadObservedMatchConfig.
	// "ok" means the config is valid on this host; any other value is blocking.
	LoadResult string `json:"load_result"`

	// ResourcesReady is true only when the config loads and the lifecycle
	// model covers all three result phases (victory, defeat, draw).
	ResourcesReady bool `json:"resources_ready"`

	Lifecycle PreflightLifecycle `json:"lifecycle"`
	Hand      PreflightHand      `json:"hand"`
	Plays     []PlayPreflight    `json:"plays"`

	AssetPaths []AssetPathInfo `json:"asset_paths,omitempty"`

	// ConfigErrors carries diagnostic detail. It never overrides LoadResult;
	// blocking status is LoadResult plus incomplete result coverage.
	ConfigErrors []string `json:"config_errors,omitempty"`
}

type PreflightLifecycle struct {
	RequiredPhases []string `json:"required_present"`
	ResultCoverage struct {
		Victory bool `json:"victory"`
		Defeat  bool `json:"defeat"`
		Draw    bool `json:"draw"`
		// Ready requires all three result phases.
		Ready bool `json:"ready"`
	} `json:"result_coverage"`
	Unsupported []string `json:"unsupported,omitempty"`
}

type PreflightHand struct {
	NamesMode bool     `json:"names_mode"`
	Names     []string `json:"names,omitempty"`
	DBPath    string   `json:"db_path,omitempty"`
	DBValid   string   `json:"db_valid,omitempty"`
	TmplPath  string   `json:"templates_path,omitempty"`
	TmplValid string   `json:"templates_valid,omitempty"`
}

type PlayPreflight struct {
	ID              string   `json:"id"`
	CardID          int      `json:"card_id"`
	ModelPath       string   `json:"model_path"`
	ModelValid      string   `json:"model_valid,omitempty"`
	Phases          []string `json:"phases,omitempty"`
	BoardROI        string   `json:"board_roi,omitempty"`
}

type AssetPathInfo struct {
	Key        string `json:"key"`
	Path       string `json:"path"`
	Class      string `json:"class"`
	ExistsHere bool   `json:"exists_here,omitempty"`
	Error      string `json:"error,omitempty"`
}

// PreflightObservedMatch validates an observed-match config offline. It always
// returns a complete report; callers decide exit status with
// PreflightCheckResult.
func PreflightObservedMatch(path string) *PreflightReport {
	r := &PreflightReport{
		ConfigPath:    path,
		OfflineOnly:   true,
		LiveQualified: false,
		LoadResult:    "ok",
	}

	cfg, err := LoadObservedMatchConfig(path)
	if err != nil {
		// The loader is the authority. Preserve its exact error and supply
		// best-effort module diagnostics without weakening the blocking result.
		r.LoadResult = err.Error()
		diagnosePreflight(r, path)
		return r
	}

	preflightFromConfig(r, cfg)
	return r
}

// PreflightCheckResult returns "ok" only when the config loads and the
// lifecycle model covers victory, defeat, and draw. It never implies live
// qualification.
func PreflightCheckResult(r *PreflightReport) string {
	if r.LoadResult != "ok" {
		return r.LoadResult
	}
	if !r.Lifecycle.ResultCoverage.Ready {
		var gaps []string
		if !r.Lifecycle.ResultCoverage.Victory {
			gaps = append(gaps, "victory")
		}
		if !r.Lifecycle.ResultCoverage.Defeat {
			gaps = append(gaps, "defeat")
		}
		if !r.Lifecycle.ResultCoverage.Draw {
			gaps = append(gaps, "draw")
		}
		return "incomplete result coverage: missing " + strings.Join(gaps, ", ")
	}
	return "ok"
}

func preflightFromConfig(r *PreflightReport, cfg ObservedMatchConfig) {
	if cfg.model != nil {
		preflightLifecycle(r, cfg.model)
	}
	preflightHand(r, cfg.Hand)
	for _, p := range cfg.Plays {
		pp := PlayPreflight{
			ID:         p.ID,
			CardID:     p.CardID,
			ModelPath:  p.ModelPath,
			ModelValid: "ok",
			BoardROI:   fmt.Sprintf("%+v", p.BoardROI),
		}
		if p.model != nil {
			for _, phase := range p.model.Phases {
				pp.Phases = append(pp.Phases, phase.Name)
			}
		}
		r.Plays = append(r.Plays, pp)
	}
	classifyAssetPaths(r, cfg)
	reportResultGaps(r)
	r.ResourcesReady = r.Lifecycle.ResultCoverage.Ready
}

func preflightLifecycle(r *PreflightReport, model *PhaseModel) {
	for _, p := range model.Phases {
		switch p.Name {
		case "victory":
			r.Lifecycle.ResultCoverage.Victory = true
		case "defeat":
			r.Lifecycle.ResultCoverage.Defeat = true
		case "draw":
			r.Lifecycle.ResultCoverage.Draw = true
		case "training", "mulligan", "mulligan-confirm", "turn", "passed", "opponent", "transition":
			r.Lifecycle.RequiredPhases = append(r.Lifecycle.RequiredPhases, p.Name)
		default:
			r.Lifecycle.Unsupported = append(r.Lifecycle.Unsupported, p.Name)
		}
	}
	r.Lifecycle.ResultCoverage.Ready = r.Lifecycle.ResultCoverage.Victory &&
		r.Lifecycle.ResultCoverage.Defeat &&
		r.Lifecycle.ResultCoverage.Draw
}

func preflightHand(r *PreflightReport, h ObservedHandConfig) {
	r.Hand.NamesMode = len(h.Names) > 0
	for _, n := range h.Names {
		r.Hand.Names = append(r.Hand.Names, n.Name)
	}
	r.Hand.DBPath = h.Database
	r.Hand.DBValid = "ok"
	if !r.Hand.NamesMode {
		r.Hand.TmplPath = h.Templates
		r.Hand.TmplValid = "ok"
	}
}

func reportResultGaps(r *PreflightReport) {
	if !r.Lifecycle.ResultCoverage.Victory {
		r.ConfigErrors = append(r.ConfigErrors, "lifecycle model missing victory phase")
	}
	if !r.Lifecycle.ResultCoverage.Defeat {
		r.ConfigErrors = append(r.ConfigErrors, "lifecycle model missing defeat phase")
	}
	if !r.Lifecycle.ResultCoverage.Draw {
		r.ConfigErrors = append(r.ConfigErrors, "lifecycle model missing draw phase")
	}
}

// diagnosePreflight fills module-level detail when the authoritative loader
// rejected the config. Errors here are informative; LoadResult stays blocking.
func diagnosePreflight(r *PreflightReport, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var cfg ObservedMatchConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		r.ConfigErrors = append(r.ConfigErrors, "diagnostics: "+err.Error())
		return
	}
	resolve := func(p string) string {
		if hostAbsolutePath(p) {
			return p
		}
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(filepath.Dir(path), p)
	}

	// Lifecycle diagnostics.
	if cfg.ModelSHA256 != "" {
		mPath := resolve(cfg.ModelPath)
		if isForeign(cfg.ModelPath) {
			r.ConfigErrors = append(r.ConfigErrors, "lifecycle model is on a foreign host path; not verified here")
		} else if model, err := loadBoundPhases(mPath, cfg.ModelSHA256); err == nil {
			preflightLifecycle(r, model)
		} else {
			r.ConfigErrors = append(r.ConfigErrors, "lifecycle model: "+err.Error())
		}
	}

	// Hand diagnostics.
	r.Hand.NamesMode = len(cfg.Hand.Names) > 0
	for _, n := range cfg.Hand.Names {
		r.Hand.Names = append(r.Hand.Names, n.Name)
	}
	r.Hand.DBPath = cfg.Hand.Database
	if isForeign(cfg.Hand.Database) {
		r.Hand.DBValid = "not verified on this host"
	} else if _, err := openHandDatabase(resolve(cfg.Hand.Database)); err != nil {
		r.Hand.DBValid = err.Error()
	} else {
		r.Hand.DBValid = "ok"
	}
	if !r.Hand.NamesMode {
		r.Hand.TmplPath = cfg.Hand.Templates
		if isForeign(cfg.Hand.Templates) {
			r.Hand.TmplValid = "not verified on this host"
		} else if err := checkHandTemplates(resolve(cfg.Hand.Templates), cfg.Hand.TemplatesSHA256); err != nil {
			r.Hand.TmplValid = err.Error()
		} else {
			r.Hand.TmplValid = "ok"
		}
	}

	// Play diagnostics.
	for _, p := range cfg.Plays {
		pp := PlayPreflight{
			ID:        p.ID,
			CardID:    p.CardID,
			ModelPath: p.ModelPath,
			BoardROI:  fmt.Sprintf("%+v", p.BoardROI),
		}
		if isForeign(p.ModelPath) {
			pp.ModelValid = "not verified on this host"
		} else if model, err := loadBoundPhases(resolve(p.ModelPath), p.ModelSHA256); err != nil {
			pp.ModelValid = err.Error()
		} else {
			pp.ModelValid = "ok"
			for _, phase := range model.Phases {
				pp.Phases = append(pp.Phases, phase.Name)
			}
		}
		r.Plays = append(r.Plays, pp)
	}
	classifyAssetPaths(r, cfg)
	reportResultGaps(r)
}

// classifyAssetPaths records whether each referenced path is host-absolute,
// Windows-absolute on this host, or relative, and never claims existence for
// paths the host cannot access.
func classifyAssetPaths(r *PreflightReport, cfg ObservedMatchConfig) {
	dir := filepath.Dir(r.ConfigPath)
	add := func(key, p string) {
		if p == "" {
			return
		}
		info := AssetPathInfo{Key: key, Path: p}
		switch {
		case hostAbsolutePath(p) && !filepath.IsAbs(p):
			// Windows drive/UNC absolute: foreign to this host.
			info.Class = "foreign-absolute"
			info.Error = "Windows path syntax; existence not checked on this host"
		case filepath.IsAbs(p):
			info.Class = "host-absolute"
			info.ExistsHere = pathExists(p)
			if !info.ExistsHere {
				info.Error = "not found on this host"
			}
		default:
			info.Class = "relative"
			info.Path = filepath.Join(dir, p)
			info.ExistsHere = pathExists(info.Path)
			if !info.ExistsHere {
				info.Error = "not found on this host"
			}
		}
		r.AssetPaths = append(r.AssetPaths, info)
	}
	add("model", cfg.ModelPath)
	add("stop_file", cfg.StopFile)
	add("state_dir", cfg.StateDir)
	add("hand_database", cfg.Hand.Database)
	if len(cfg.Hand.Names) == 0 {
		add("hand_templates", cfg.Hand.Templates)
	}
	for i, p := range cfg.Plays {
		add(fmt.Sprintf("play[%d]_model", i), p.ModelPath)
	}
}

func isForeign(p string) bool {
	return hostAbsolutePath(p) && !filepath.IsAbs(p)
}

func openHandDatabase(path string) (*cardindex.Index, error) {
	return cardindex.Open(path, cardindex.Version{Data: cardindex.DataVersion, Rules: cardindex.RulesVersion})
}

func checkHandTemplates(path, wantSHA256 string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != wantSHA256 {
		return fmt.Errorf("hand template digest mismatch")
	}
	var templates cardmatch.File
	if err := json.Unmarshal(data, &templates); err != nil {
		return err
	}
	if len(templates.Templates) < 2 {
		return fmt.Errorf("hand identity requires competing card templates")
	}
	return nil
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
