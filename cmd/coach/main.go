package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"gwentcoachmvp/internal/observe"
)

func main() {
	out := flag.String("out", "runs", "directory for timestamped observation reports")
	action := flag.String("action", "", "single reviewed input: click, hold (1.5s), hover, or escape (default observe only)")
	x := flag.Int("x", 0, "click x in client pixels")
	y := flag.Int("y", 0, "click y in client pixels")
	reference := flag.String("reference", "", "recent successful observation report.json")
	stopFile := flag.String("stop-file", "", "existing file pauses input; also hold F8 or Ctrl")
	count := flag.Int("samples", 1, "bounded observation count; never repeats inputs")
	interval := flag.Duration("interval", time.Second, "minimum interval between observation starts")
	width := flag.Int("max-width", 0, "observation image maximum width; zero keeps native pixels")
	quality := flag.Int("jpeg-quality", 0, "JPEG quality 1-100; zero uses PNG")
	model := flag.String("phase-model", "", "local validated UI phase model")
	modelSHA := flag.String("phase-model-sha256", "", "expected SHA-256 of phase model")
	labels := flag.String("build-phases", "", "label manifest to compile into -phase-model")
	diagnosePhase := flag.String("diagnose-phase", "", "image for per-anchor phase diagnostics")
	recognize := flag.String("recognize", "", "image to classify without input")
	replay := flag.String("replay-phases", "", "labelled frame manifest for observation-only JSONL replay")
	rehearsal := flag.String("rehearse-turn", "", "offline turn manifest: recognize, decide and map coordinates without input")
	handScan := flag.String("observe-hand", "", "capture two Windows frames and recognize calibrated hand regions without input")
	ocrImage := flag.String("ocr-image", "", "read Chinese text from a local image with Windows OCR")
	guardProbe := flag.String("guard-probe", "", "capture guarded cursor transition without mouse button input")
	hoverAnnotate := flag.String("annotate-hand", "", "bounded Windows hover/OCR pass with calibrated slot regions")
	collect := flag.String("collect-readonly", "", "bounded Windows capture and numeric recognition config; never sends input")
	nativeSession := flag.String("native-session", "", "run a pinned Windows multi-match session and export confirmed training samples")
	nativeLoop := flag.String("native-loop", "", "run calibrated Windows recognition, rules and input loop")
	nativeSlice := flag.String("native-opening", "", "run one calibrated Windows training opening with a 10s budget")
	navigate := flag.Bool("navigate-training", false, "bounded guarded navigation; never starts a match")
	training := flag.Bool("training-pass-only", false, "experimental local training game using a pass-only policy")
	trainingState := flag.String("training-state", "", "new directory for the training attempt ledger")
	trainingTimeout := flag.Duration("training-timeout", 10*time.Minute, "training attempt timeout")
	resumePhase := flag.String("training-resume", "", "reconciled resume phase: mulligan or confirm")
	state := flag.String("navigation-state", "", "new persistent session directory; never reuse after uncertain input")
	steps := flag.Int("steps", 5, "navigation transition bound, 1..8")
	flag.Parse()
	if *ocrImage != "" {
		text, err := observe.RecognizeText(*ocrImage, os.Stderr)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(text)
		return
	}
	if *rehearsal != "" {
		if err := rehearseTurn(*rehearsal, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if *labels != "" {
		if err := observe.BuildPhases(*labels, *model); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if *replay != "" {
		if err := observe.ReplayPhases(*model, *replay, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if *diagnosePhase != "" {
		if err := observe.DiagnosePhaseFile(*model, *diagnosePhase, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if *recognize != "" {
		if err := observe.ClassifyFile(*model, *recognize, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if *collect != "" && (*nativeSession != "" || *nativeLoop != "" || *guardProbe != "" || *hoverAnnotate != "" || *nativeSlice != "" || *handScan != "" || *training || *navigate || *action != "") {
		fmt.Fprintln(os.Stderr, "read-only collection cannot be combined with input modes")
		os.Exit(1)
	}
	if *nativeSession != "" && (*nativeLoop != "" || *guardProbe != "" || *hoverAnnotate != "" || *nativeSlice != "" || *handScan != "" || *training || *navigate || *action != "" || *count != 1 || *width != 0 || *quality != 0) {
		fmt.Fprintln(os.Stderr, "native session cannot be combined with other input or sampling modes")
		os.Exit(1)
	}
	if *guardProbe != "" && (*hoverAnnotate != "" || *nativeLoop != "" || *nativeSlice != "" || *handScan != "" || *training || *navigate || *action != "" || *count != 1 || *width != 0 || *quality != 0) {
		fmt.Fprintln(os.Stderr, "guard probe cannot be combined with other input or sampling modes")
		os.Exit(1)
	}
	if *hoverAnnotate != "" && (*nativeLoop != "" || *nativeSlice != "" || *handScan != "" || *training || *navigate || *action != "" || *count != 1 || *width != 0 || *quality != 0) {
		fmt.Fprintln(os.Stderr, "hand annotation cannot be combined with other input or sampling modes")
		os.Exit(1)
	}
	if *nativeLoop != "" && (*nativeSlice != "" || *handScan != "" || *training || *navigate || *action != "" || *count != 1 || *width != 0 || *quality != 0) {
		fmt.Fprintln(os.Stderr, "native loop cannot be combined with other input or sampling modes")
		os.Exit(1)
	}
	if *nativeSlice != "" && (*handScan != "" || *training || *navigate || *action != "" || *count != 1 || *width != 0 || *quality != 0) {
		fmt.Fprintln(os.Stderr, "native opening cannot be combined with other input or sampling modes")
		os.Exit(1)
	}
	if *handScan != "" && (*training || *navigate || *action != "" || *stopFile == "" || *width != 0 || *quality != 0) {
		fmt.Fprintln(os.Stderr, "hand scan requires a stop file and no other input or sampling mode")
		os.Exit(1)
	}
	if *training && (*navigate || *action != "" || *count != 1 || *model == "" || *state != "" || *stopFile == "" || *trainingState == "") {
		fmt.Fprintln(os.Stderr, "training requires a model, stop file, new training state, and no other input mode")
		os.Exit(1)
	}
	if *navigate && (*action != "" || *count != 1 || *model == "") {
		fmt.Fprintln(os.Stderr, "navigation requires model and no manual action/sampling")
		os.Exit(1)
	}
	if *action != "" && (*count != 1 || *width != 0 || *quality != 0) {
		fmt.Fprintln(os.Stderr, "sampling options cannot be combined with input")
		os.Exit(1)
	}
	if err := os.MkdirAll(*out, 0755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	f, err := os.OpenFile(filepath.Join(*out, "coach.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	logger := log.New(f, "", log.LstdFlags|log.LUTC)
	logger.Printf("starting pid=%d action=%q", os.Getpid(), *action)
	var runErr error
	if *collect != "" {
		runErr = observe.RunReadOnlyCollection(*out, logger, *collect, *stopFile, *count, *interval)
	} else if *nativeSession != "" {
		runErr = observe.RunNativeSession(*out, logger, *nativeSession, os.Stdout)
	} else if *guardProbe != "" {
		runErr = observe.RunGuardProbe(*out, logger, *guardProbe)
	} else if *hoverAnnotate != "" {
		runErr = observe.RunHoverAnnotation(*out, logger, *hoverAnnotate, os.Stdout)
	} else if *nativeLoop != "" {
		runErr = observe.RunNativeLoop(*out, logger, *nativeLoop, os.Stdout)
	} else if *nativeSlice != "" {
		runErr = observe.RunNativeSlice(*out, logger, *nativeSlice, os.Stdout)
	} else if *handScan != "" {
		runErr = observe.RunHandScan(*out, logger, *handScan, *stopFile, *count, *interval, os.Stdout)
	} else if *training {
		runErr = observe.RunTraining(*out, logger, observe.Training{ModelPath: *model, ModelSHA256: *modelSHA, StopFile: *stopFile, StateDir: *trainingState, Timeout: *trainingTimeout, ResumePhase: *resumePhase})
	} else if *navigate {
		runErr = observe.Navigate(*out, logger, observe.Navigation{ModelPath: *model, ModelSHA256: *modelSHA, StopFile: *stopFile, StateDir: *state, Steps: *steps})
	} else if *action == "" {
		runErr = observe.Sample(*out, logger, observe.Sampling{Count: *count, Interval: *interval, MaxWidth: *width, JPEGQuality: *quality, StopFile: *stopFile})
	} else {
		runErr = observe.Execute(*out, logger, observe.Action{Kind: *action, X: *x, Y: *y, Reference: *reference, StopFile: *stopFile})
	}
	if err := runErr; err != nil {
		logger.Printf("run failed: %v", err)
		fmt.Fprintln(os.Stderr, err)
		f.Close()
		os.Exit(1)
	}
	logger.Print("observation complete")
	f.Close()
}
