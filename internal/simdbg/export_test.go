package simdbg

// CheckpointEvents returns the statement counts of the session's
// checkpoints, in order.
func (s *Session) CheckpointEvents() []int64 {
	out := make([]int64, len(s.cps))
	for i, c := range s.cps {
		out[i] = c.events
	}
	return out
}
