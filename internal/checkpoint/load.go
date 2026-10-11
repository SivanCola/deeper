package checkpoint

import "sort"

func (s *Store) load() {
	s.done = s.loadV3Turns()
	sort.Slice(s.done, func(i, j int) bool { return s.done[i].Turn < s.done[j].Turn })
}
