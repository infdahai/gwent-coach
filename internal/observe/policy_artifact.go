package observe

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"gwentcoachmvp/internal/cardindex"
	"gwentcoachmvp/internal/strategy"
)

// selectMatchPolicy validates the configured file and pins one snapshot for a match.
// Rejection leaves the baseline active and is reported to the caller.
func selectMatchPolicy(path, expected string, baseline strategy.Policy) (strategy.MatchPolicy, error) {
	policies, err := strategy.NewExecutablePolicies("baseline-v1", baseline, strategy.Compatibility{RulesVersion: cardindex.RulesVersion, CatalogVersion: cardindex.DataVersion})
	if err != nil {
		return strategy.MatchPolicy{}, err
	}
	if path == "" && expected == "" {
		return policies.BeginMatch(), nil
	}
	if path == "" || len(expected) != 64 {
		return policies.BeginMatch(), fmt.Errorf("policy artifact path and SHA-256 required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return policies.BeginMatch(), fmt.Errorf("policy artifact: %w", err)
	}
	sum := sha256.Sum256(data)
	if !strings.EqualFold(expected, hex.EncodeToString(sum[:])) {
		return policies.BeginMatch(), fmt.Errorf("policy artifact SHA-256 mismatch")
	}
	if err := policies.Select(data); err != nil {
		return policies.BeginMatch(), err
	}
	return policies.BeginMatch(), nil
}
