package job

import (
	"testing"
	"time"
)

func TestNodePollSchedule(t *testing.T) {
	t.Setenv("XUI_TEST_NODE_INTERVALS", "2=30s,3=5s,4=7s,invalid")
	s := newNodePollSchedule("XUI_TEST_NODE_INTERVALS")
	start := time.Unix(0, 0)
	for tick := 0; tick <= 12; tick++ {
		now := start.Add(time.Duration(tick) * nodePollTick)
		if got := s.due(2, now); got != (tick%6 == 0) {
			t.Errorf("node 2 tick %d: due %t", tick, got)
		}
		for _, id := range []int{1, 3, 4} {
			if !s.due(id, now) {
				t.Errorf("node %d skipped default tick %d", id, tick)
			}
		}
	}
}
