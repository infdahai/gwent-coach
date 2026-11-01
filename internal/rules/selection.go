package rules

import "sort"

// OrderCandidates is the baseline policy: prefer supported orders, then plays in confirmed hand order,
// then pass; break ties by target and row. It copies the supplied candidates and
// neither establishes nor filters legality. Callers must validate before use.
func OrderCandidates(in DecisionInput) []Action {
	order := make(map[string]int, len(in.HandOrder))
	for i, id := range in.HandOrder {
		order[id] = i
	}
	priority := func(a Action) int {
		if a.Kind == "order" {
			return -1
		}
		if a.Kind == "play" {
			if i, ok := order[a.Card]; ok {
				return i
			}
		} else if a.Kind == "pass" {
			return len(in.HandOrder)
		}
		return len(in.HandOrder) + 1
	}
	candidates := append([]Action(nil), in.Candidates...)
	sort.SliceStable(candidates, func(i, j int) bool {
		if a, b := priority(candidates[i]), priority(candidates[j]); a != b {
			return a < b
		}
		if candidates[i].Target != candidates[j].Target {
			return candidates[i].Target < candidates[j].Target
		}
		return candidates[i].Row < candidates[j].Row
	})
	return candidates
}
