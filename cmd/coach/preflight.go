package main

import (
	"encoding/json"
	"fmt"
	"os"

	"gwentcoachmvp/internal/observe"
)

// runPreflight implements "coach preflight-observed <match.json>". It always
// writes a complete JSON report and returns an error only for blocking
// deficiencies or report-encoding failures.
func runPreflight(args []string, output *os.File) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: coach preflight-observed <match.json>")
	}
	report := observe.PreflightObservedMatch(args[0])

	enc := json.NewEncoder(output)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		return err
	}

	if blocking := observe.PreflightCheckResult(report); blocking != "ok" {
		return fmt.Errorf("preflight: %s", blocking)
	}
	return nil
}
