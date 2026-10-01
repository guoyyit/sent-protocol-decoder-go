package sent

import (
	"encoding/json"
	"os"
)

// Recording is a deterministically replayable historical sample.
type Recording struct {
	Config ChannelConfig `json:"config"`
	Specs  []FrameSpec   `json:"specs,omitempty"`
	Ops    []InjectOp    `json:"ops,omitempty"`
	Seed   int64         `json:"seed"`
	Edges  []Edge        `json:"edges"`
}

// SaveRecording writes a recording as JSON.
func SaveRecording(path string, r Recording) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// LoadRecording reads a JSON recording.
func LoadRecording(path string) (*Recording, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Recording
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Replay decodes a recording; identical recordings yield identical events.
func Replay(r *Recording) []Event {
	if len(r.Edges) == 0 && r.Specs != nil {
		r.Edges = NewEncoder(r.Config, r.Seed).Build(r.Specs, r.Ops)
	}
	return Decode(r.Config, r.Edges)
}
