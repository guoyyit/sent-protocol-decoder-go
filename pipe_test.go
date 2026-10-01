package sent

import "testing"

func TestPipePushAndDrain(t *testing.T) {
	p := NewPipe(3)
	p.Push(Sample{FrameSeq: 1})
	p.Push(Sample{FrameSeq: 2})
	if !p.TryPush(Sample{FrameSeq: 3}) {
		t.Fatal("TryPush should succeed with one free slot")
	}
	if p.TryPush(Sample{FrameSeq: 4}) {
		t.Fatal("TryPush must fail when full")
	}

	got := 0
	for range p.Samples() {
		got++
		if got == 3 {
			p.Close()
		}
	}
	if got != 3 {
		t.Fatalf("drained %d, want 3", got)
	}
}
