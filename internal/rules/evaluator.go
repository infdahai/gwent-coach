package rules

import "fmt"

// Evaluator uses the integration rules and opaque-state restrictions without
// starting the legacy DecisionService worker. Candidate ordering is policy-owned.
type Evaluator struct{ engine *Engine }

func NewEvaluator(engine *Engine) *Evaluator { return &Evaluator{engine: engine} }

// ValidateAction is independent of candidate membership, ordering and deadline.
// In partially observed states it retains the explicitly enabled pass boundary.
func (e *Evaluator) ValidateAction(in DecisionInput, a Action) error {
	s, err := e.publicState(in)
	if err != nil {
		return err
	}
	return e.validateAction(in, s, a)
}
func (e *Evaluator) validateAction(in DecisionInput, s State, a Action) error {
	if a.Player != in.Player || (a.Kind != "play" && a.Kind != "pass" && a.Kind != "order") {
		return fmt.Errorf("unsupported action or player")
	}
	if hasOpaqueCards(in) {
		if hasUnknownEnemyObject(in) && a.Kind != "pass" {
			return fmt.Errorf("unknown enemy object restricts action")
		}
		switch a.Kind {
		case "pass":
			if !in.AllowPassWithCards || in.View.Passed[1-in.Player] {
				return fmt.Errorf("opaque pass boundary not satisfied")
			}
		case "order":
			i, target := index(s, a.Card), index(s, a.Target)
			if i < 0 || target < 0 || s.Cards[i].CardID != 202140 || s.Cards[target].Owner != in.Player {
				return fmt.Errorf("unsupported opaque order")
			}
		default:
			if !safeMageTarget(in, a) && !safeScholar(in, a) {
				i := index(s, a.Card)
				if a.Target != "" || i < 0 || s.Cards[i].CardID != 132403 {
					return fmt.Errorf("unsupported opaque play")
				}
			}
		}
	}
	_, _, err := e.engine.Apply(s, a)
	return err
}

// Compatibility for existing callers; the native loop constructs Evaluator
// directly and never starts DecisionService's asynchronous worker.
func (d *DecisionService) publicState(in DecisionInput) (State, error) {
	return NewEvaluator(d.engine).publicState(in)
}
func (d *DecisionService) Candidates(in DecisionInput) ([]Action, error) {
	return NewEvaluator(d.engine).Candidates(in)
}
func (d *DecisionService) ValidateAction(in DecisionInput, a Action) error {
	return NewEvaluator(d.engine).ValidateAction(in, a)
}
