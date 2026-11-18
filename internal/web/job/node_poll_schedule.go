package job

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
)

// The panel's node jobs run every five seconds. Each job owns its schedule
// and calls due from its serialized Run loop, before dispatching workers.
const nodePollTick = 5 * time.Second

type nodePollSchedule struct {
	interval map[int]time.Duration
	next     map[int]time.Time
}

func newNodePollSchedule(env string) nodePollSchedule {
	s := nodePollSchedule{interval: make(map[int]time.Duration), next: make(map[int]time.Time)}
	for entry := range strings.SplitSeq(os.Getenv(env), ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		key, value, ok := strings.Cut(entry, "=")
		id, idErr := strconv.Atoi(strings.TrimSpace(key))
		d, durationErr := time.ParseDuration(strings.TrimSpace(value))
		if !ok || idErr != nil || id < 1 || durationErr != nil || d < nodePollTick || d%nodePollTick != 0 {
			logger.Warningf("%s: ignoring invalid node interval %q (expected ID=duration in 5s increments)", env, entry)
			continue
		}
		s.interval[id] = d
	}
	return s
}

func (s *nodePollSchedule) due(id int, tick time.Time) bool {
	d := s.interval[id]
	if d <= nodePollTick {
		return true
	}
	if tick.Before(s.next[id]) {
		return false
	}
	s.next[id] = tick.Add(d)
	return true
}
