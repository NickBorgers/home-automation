package vacuum

import "fmt"

// hold_while is a start gate: while any of its conditions holds, the planner starts no new run, so a run does
// not start while, for example, the TV is on. It never stops a run that is going. The conditions are read
// through the state manager, like the sleeper variables, and a change wakes the scheduler. A held plan is
// empty, so when the hold ends the plan must hold for stable_for again before it starts.

// holdVars lists the hold_while state variables, each once, in config order.
func (s *Scheduler) holdVars() []string {
	seen := map[string]bool{}
	var out []string
	for _, h := range s.cfg.HoldWhile {
		if !seen[h.Variable] {
			seen[h.Variable] = true
			out = append(out, h.Variable)
		}
	}
	return out
}

// holdReason says which hold_while condition holds new runs, such as "held: isTVPlaying is true", or "" when
// none does. A variable that cannot be read does not hold; it is warned about while it cannot be read. The
// caller holds tickMu.
func (s *Scheduler) holdReason() string {
	for _, h := range s.cfg.HoldWhile {
		v, err := s.stateManager.GetBool(h.Variable)
		if err != nil {
			s.warn("hold:"+h.Variable, "cannot read hold_while variable "+h.Variable+" ("+err.Error()+
				"); it does not hold runs")
			continue
		}
		if v == h.Value {
			return fmt.Sprintf("held: %s is %t", h.Variable, v)
		}
	}
	return ""
}
