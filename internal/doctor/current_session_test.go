package doctor

import (
	"deepzora/internal/agent"
	"deepzora/internal/provider"
	"encoding/json"
)

func saveDoctorMessages(path string, lines []string) error {
	s := agent.NewSession("")
	for _, line := range lines {
		var m provider.Message
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			return err
		}
		s.Add(m)
	}
	return s.Save(path)
}
