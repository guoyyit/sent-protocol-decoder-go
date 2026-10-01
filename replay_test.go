package sent

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestDeterministicSeedOutput(t *testing.T) {
	cfg := testConfig()
	specs := cleanSpecs(30)
	ops := []InjectOp{
		{Type: OpCorruptCRC, FrameIndex: 4},
		{Type: OpDropFrame, FrameIndex: 10},
		{Type: OpIllegalNibble, FrameIndex: 15, With: 2},
	}

	enc := func() []Edge {
		return NewEncoder(cfg, 12345).Build(specs, ops)
	}
	edges1 := enc()
	edges2 := enc()
	if !reflect.DeepEqual(edges1, edges2) {
		t.Fatal("fixed seed must produce identical edge streams")
	}

	r1 := Decode(cfg, edges1)
	r2 := Decode(cfg, edges2)
	if len(r1) != len(r2) {
		t.Fatalf("event count differs: %d vs %d", len(r1), len(r2))
	}
	for i := range r1 {
		if !eventsEqual(r1[i], r2[i]) {
			t.Fatalf("event %d differs between runs:\n%+v\n%+v", i, r1[i], r2[i])
		}
	}
}

func eventsEqual(a, b Event) bool {
	if a.ChannelID != b.ChannelID || a.Time != b.Time || a.Kind != b.Kind {
		return false
	}
	if (a.FrameErr == nil) != (b.FrameErr == nil) {
		return false
	}
	if a.FrameErr != nil && *a.FrameErr != *b.FrameErr {
		return false
	}
	if (a.Discard == nil) != (b.Discard == nil) {
		return false
	}
	if a.Discard != nil && *a.Discard != *b.Discard {
		return false
	}
	if (a.Fault == nil) != (b.Fault == nil) {
		return false
	}
	if a.Fault != nil && *a.Fault != *b.Fault {
		return false
	}
	if (a.Slow == nil) != (b.Slow == nil) {
		return false
	}
	if a.Slow != nil && *a.Slow != *b.Slow {
		return false
	}
	if (a.Reading == nil) != (b.Reading == nil) {
		return false
	}
	if a.Reading != nil {
		ar, br := a.Reading, b.Reading
		if ar.RawA != br.RawA || ar.RawB != br.RawB ||
			ar.PhysA != br.PhysA || ar.PhysB != br.PhysB ||
			ar.ErrA != br.ErrA || ar.ErrB != br.ErrB {
			return false
		}
	}
	return true
}

func TestRecordingRoundTrip(t *testing.T) {
	cfg := testConfig()
	specs := cleanSpecs(12)
	ops := []InjectOp{{Type: OpPulseWidth, FrameIndex: 5, With: 1}}
	seed := int64(777)
	edges := NewEncoder(cfg, seed).Build(specs, ops)

	rec := Recording{Config: cfg, Specs: specs, Ops: ops, Seed: seed, Edges: edges}
	path := filepath.Join(t.TempDir(), "sample.json")
	if err := SaveRecording(path, rec); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadRecording(path)
	if err != nil {
		t.Fatal(err)
	}

	live := Decode(cfg, edges)
	replayed := Replay(loaded)
	if len(live) != len(replayed) {
		t.Fatalf("replay event count %d != live %d", len(replayed), len(live))
	}
	for i := range live {
		if !eventsEqual(live[i], replayed[i]) {
			t.Fatalf("replay diverges at event %d", i)
		}
	}

	// A recording with specs but no precomputed edges regenerates identically.
	bare := Recording{Config: cfg, Specs: specs, Ops: ops, Seed: seed}
	regenerated := Replay(&bare)
	if len(regenerated) != len(live) {
		t.Fatal("spec-based replay must regenerate the same stream")
	}
}

func TestPipeLoopback(t *testing.T) {
	cfg := testConfig()
	edges := NewEncoder(cfg, 99).Build(cleanSpecs(3), nil)
	pipe := NewPipe(len(edges))
	pipe.Write(edges)
	pipe.Close()

	var got []Edge
	for e := range pipe.Edges() {
		got = append(got, e)
	}
	if len(got) != len(edges) {
		t.Fatalf("pipe delivered %d edges, want %d", len(got), len(edges))
	}
	if countReadings(Decode(cfg, got)) != 3 {
		t.Fatal("loopback stream must decode cleanly")
	}
}
