package sent

import (
	"bytes"
	"encoding/gob"
	"io"
)

// ChannelRecording is one channel's configuration plus its edge history.
type ChannelRecording struct {
	Spec    ChannelSpec
	Samples []Sample
}

// Recording is a replayable, serializable multi-channel sample history.
type Recording struct {
	Channels []ChannelRecording
}

// NewRecording creates an empty recording.
func NewRecording() *Recording { return &Recording{} }

// Add appends one channel of history.
func (rec *Recording) Add(recCh ChannelRecording) {
	rec.Channels = append(rec.Channels, recCh)
}

// recordingWire is the serialized form. time.Duration is an int64 in gob, so
// timestamps survive byte-for-byte across processes.
type recordingWire struct {
	Channels []ChannelRecording
}

func init() {
	gob.Register(FrameConfig{})
	gob.Register(PhysicalConfig{})
	gob.Register(SignalDef{})
	gob.Register(FaultTrackerConfig{})
}

// Save writes the recording in a deterministic binary form.
func (rec *Recording) Save(w io.Writer) error {
	enc := gob.NewEncoder(w)
	return enc.Encode(recordingWire{Channels: rec.Channels})
}

// Load restores a recording previously written with Save.
func LoadRecording(r io.Reader) (*Recording, error) {
	var wire recordingWire
	if err := gob.NewDecoder(r).Decode(&wire); err != nil {
		return nil, err
	}
	return &Recording{Channels: wire.Channels}, nil
}

// Replay decodes all channels synchronously and returns ordered events.
//
// Samples are merged by (timestamp, channel index, input order): each
// channel keeps its own ordering, and equal timestamps resolve by stable
// channel index. The result for an identical recording is therefore a
// deterministic event slice suitable for golden comparisons.
func (rec *Recording) Replay() []Event {
	pipelines := make([]*channelPipeline, len(rec.Channels))
	for i := range rec.Channels {
		pipelines[i] = newPipeline(rec.Channels[i].Spec)
	}

	positions := make([]int, len(rec.Channels))
	var events []Event

	for {
		best := -1
		for i := range rec.Channels {
			if positions[i] >= len(rec.Channels[i].Samples) {
				continue
			}
			if best == -1 {
				best = i
				continue
			}
			cur := rec.Channels[i].Samples[positions[i]]
			win := rec.Channels[best].Samples[positions[best]]
			if cur.At < win.At || (cur.At == win.At && i < best) {
				best = i
			}
		}
		if best == -1 {
			break
		}
		sample := rec.Channels[best].Samples[positions[best]]
		positions[best]++
		events = append(events, pipelines[best].processSample(sample)...)
	}

	// Flush in stable channel order as well.
	for i := range pipelines {
		events = append(events, pipelines[i].flush()...)
	}
	return events
}

// Encode returns an in-memory copy of Save's output.
func (rec *Recording) Encode() ([]byte, error) {
	var buf bytes.Buffer
	if err := rec.Save(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
