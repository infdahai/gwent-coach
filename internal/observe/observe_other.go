//go:build !windows

package observe

import (
	"fmt"
	"log"
)

func navigatePlatform(_ string, _ *log.Logger, _ Navigation, _ *PhaseModel) error {
	return fmt.Errorf("navigation requires Windows interactive desktop")
}

func Execute(_ string, _ *log.Logger, _ Action) error {
	return fmt.Errorf("input requires Windows interactive desktop")
}

func runSample(out string, logger *log.Logger, _ Sampling) error {
	return Run(out, logger)
}

func Run(_ string, _ *log.Logger) error {
	return fmt.Errorf("desktop observation requires Windows; cross-compile and run in the game's interactive session")
}
