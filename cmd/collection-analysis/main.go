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

type summary struct {
	Trust   string                    `json:"trust"`
	Frames  int                       `json:"frames"`
	Scalars map[string]*scalarSummary `json:"scalars"`
}

func analyze(input io.Reader) (summary, error) {
	result := summary{Trust: candidateTrust, Scalars: map[string]*scalarSummary{}}
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
		result.Frames++
	}
	if err := scanner.Err(); err != nil {
		return summary{}, err
	}
	if result.Frames == 0 {
		return summary{}, fmt.Errorf("no collector records")
	}
	return result, nil
}

func run(args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: collection-analysis recognition.jsonl")
	}
	file, err := os.Open(args[0])
	if err != nil {
		return err
	}
	defer file.Close()
	result, err := analyze(file)
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
