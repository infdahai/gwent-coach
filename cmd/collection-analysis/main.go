// Command collection-analysis summarizes unverified read-only collector records.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"gwentcoachmvp/internal/observe"
)

const candidateTrust = "unverified_candidate_not_for_actions"

type scalarSummary struct {
	Candidates map[int]int `json:"candidate_values"`
	Unknown    int         `json:"unknown_frames"`
	Agreed     int         `json:"consecutive_candidate_agreements"`
}

type evaluation struct {
	Correct   int `json:"correct"`
	Incorrect int `json:"incorrect"`
	Unknown   int `json:"unknown"`
}

type annotation struct {
	Reference string         `json:"reference"`
	Scalars   map[string]int `json:"scalars"`
}

type summary struct {
	Trust      string                    `json:"trust"`
	Frames     int                       `json:"frames"`
	Scalars    map[string]*scalarSummary `json:"scalars"`
	Evaluation map[string]*evaluation    `json:"evaluation,omitempty"`
}

func analyze(input io.Reader) (summary, error) {
	return analyzeLabeled(input, nil)
}

func analyzeLabeled(input io.Reader, labels []annotation) (summary, error) {
	result := summary{Trust: candidateTrust, Scalars: map[string]*scalarSummary{}}
	byReference := map[string]map[string]int{}
	if labels != nil {
		if len(labels) == 0 {
			return summary{}, fmt.Errorf("no annotations")
		}
		result.Evaluation = map[string]*evaluation{}
		for _, label := range labels {
			if label.Reference == "" || len(label.Scalars) == 0 || byReference[label.Reference] != nil {
				return summary{}, fmt.Errorf("invalid or duplicate annotation reference %q", label.Reference)
			}
			for name := range label.Scalars {
				if name == "" {
					return summary{}, fmt.Errorf("empty annotated scalar name")
				}
			}
			byReference[label.Reference] = label.Scalars
		}
	}
	seenReferences := map[string]bool{}
	get := func(name string) *scalarSummary {
		if result.Scalars[name] == nil {
			result.Scalars[name] = &scalarSummary{Candidates: map[int]int{}}
		}
		return result.Scalars[name]
	}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 16<<20)
	for scanner.Scan() {
		line := result.Frames + 1
		var record observe.CollectionRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return summary{}, fmt.Errorf("line %d: %w", line, err)
		}
		if record.Trust != candidateTrust || record.Reference == "" || record.Captured.IsZero() || record.Scalars == nil || record.Unknown == nil {
			return summary{}, fmt.Errorf("line %d: invalid collector record or trust marker", line)
		}
		seen := map[string]bool{}
		for name, value := range record.Scalars {
			if name == "" {
				return summary{}, fmt.Errorf("line %d: empty scalar name", line)
			}
			seen[name] = true
			get(name).Candidates[value]++
		}
		for _, name := range record.Unknown {
			if name == "" || seen[name] {
				return summary{}, fmt.Errorf("line %d: invalid or duplicate unknown scalar", line)
			}
			seen[name] = true
			get(name).Unknown++
		}
		for name, value := range record.Agreed {
			if candidate, ok := record.Scalars[name]; !ok || candidate != value {
				return summary{}, fmt.Errorf("line %d: agreement does not match candidate", line)
			}
			get(name).Agreed++
		}
		if labelsForFrame, ok := byReference[record.Reference]; ok {
			if seenReferences[record.Reference] {
				return summary{}, fmt.Errorf("line %d: duplicate annotated reference %q", line, record.Reference)
			}
			seenReferences[record.Reference] = true
			for name, expected := range labelsForFrame {
				if !seen[name] {
					return summary{}, fmt.Errorf("line %d: annotated scalar %q missing from record", line, name)
				}
				if result.Evaluation[name] == nil {
					result.Evaluation[name] = &evaluation{}
				}
				stats := result.Evaluation[name]
				if actual, ok := record.Scalars[name]; !ok {
					stats.Unknown++
				} else if actual == expected {
					stats.Correct++
				} else {
					stats.Incorrect++
				}
			}
		}
		result.Frames++
	}
	if err := scanner.Err(); err != nil {
		return summary{}, err
	}
	if result.Frames == 0 {
		return summary{}, fmt.Errorf("no collector records")
	}
	for ref := range byReference {
		if !seenReferences[ref] {
			return summary{}, fmt.Errorf("annotation reference %q not found", ref)
		}
	}
	return result, nil
}

func run(args []string, stdout io.Writer) error {
	if len(args) > 0 && args[0] == "import" {
		if len(args) != 3 {
			return fmt.Errorf("usage: collection-analysis import SOURCE_DIR DEST_DIR")
		}
		return importCollections(args[1], args[2], stdout)
	}
	if len(args) < 1 || len(args) > 2 {
		return fmt.Errorf("usage: collection-analysis recognition.jsonl [annotations.json]")
	}
	file, err := os.Open(args[0])
	if err != nil {
		return err
	}
	defer file.Close()
	var labels []annotation
	if len(args) == 2 {
		data, err := os.ReadFile(args[1])
		if err != nil {
			return err
		}
		var raw []struct {
			Reference string          `json:"reference"`
			Scalars   map[string]*int `json:"scalars"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("annotations: %w", err)
		}
		for _, item := range raw {
			label := annotation{Reference: item.Reference, Scalars: map[string]int{}}
			for name, value := range item.Scalars {
				if value == nil {
					return fmt.Errorf("annotation %q scalar %q has null value", item.Reference, name)
				}
				label.Scalars[name] = *value
			}
			labels = append(labels, label)
		}
		if labels == nil {
			return fmt.Errorf("annotations must be a nonempty array")
		}
	}
	result, err := analyzeLabeled(file, labels)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
