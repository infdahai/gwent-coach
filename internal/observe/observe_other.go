//go:build !windows

package observe

import (
	"fmt"
	"io"
	"log"
	"time"

	"gwentcoachmvp/internal/strategy"
)

func navigatePlatform(_ string, _ *log.Logger, _ Navigation, _ *PhaseModel) error {
	return fmt.Errorf("navigation requires Windows interactive desktop")
}

func RunReadOnlyCollection(_ string, _ *log.Logger, _, _ string, _ int, _ time.Duration) error {
	return fmt.Errorf("live collection requires Windows interactive desktop")
}

func RunGuardProbe(_ string, _ *log.Logger, _ string) error {
	return fmt.Errorf("guard probe requires Windows interactive desktop")
}

func RecognizeText(_ string, _ io.Writer) (string, error) {
	return "", fmt.Errorf("OCR requires Windows")
}

func RunHoverAnnotation(_ string, _ *log.Logger, _ string, _ io.Writer) error {
	return fmt.Errorf("hover annotation requires Windows interactive desktop")
}

func RunNativeSession(_ string, _ *log.Logger, _ string, _ io.Writer) error {
	return fmt.Errorf("native session requires Windows interactive desktop")
}

func RunNativeLoop(_ string, _ *log.Logger, _ string, _ io.Writer) error {
	return fmt.Errorf("native loop requires Windows interactive desktop")
}

func RunNativeLoopWithStrategy(_ string, _ *log.Logger, _ string, _ io.Writer, _ strategy.Policy) error {
	return fmt.Errorf("native loop requires Windows interactive desktop")
}

func RunNativeSlice(_ string, _ *log.Logger, _ string, _ io.Writer) error {
	return fmt.Errorf("native slice requires Windows interactive desktop")
}

func RunHandScan(_ string, _ *log.Logger, _, _ string, _ int, _ time.Duration, _ io.Writer) error {
	return fmt.Errorf("hand scan requires Windows interactive desktop")
}

func RunTraining(_ string, _ *log.Logger, _ Training) error {
	return fmt.Errorf("training requires Windows interactive desktop")
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
