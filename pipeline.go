package sent

// ChannelSpec fully declares one channel's decoding configuration.
type ChannelSpec struct {
	Name     string
	Frame    FrameConfig
	Physical PhysicalConfig
	Fault    FaultTrackerConfig
}

func slowFormatFor(variant Variant) SlowFormat {
	if variant == VariantB {
		return SlowShort
	}
	return SlowEnhanced
}

// channelPipeline is the deterministic, goroutine-free per-channel core.
//
// It chains edge-to-pulse extraction, frame reconstruction, CRC/variant
// validation, physical decoding, fault debouncing and slow-message assembly.
// Every output item is produced synchronously in input order; the concurrent
// Receiver (receiver.go) merely hosts one pipeline per channel.
type channelPipeline struct {
	spec            ChannelSpec
	decoder         *Decoder
	faults          *FaultTracker
	slow            *SlowAssembler
	lastEdge        *Sample
	variantReported bool
}

// newPipeline builds a pipeline from a channel specification.
func newPipeline(spec ChannelSpec) *channelPipeline {
	return &channelPipeline{
		spec:    spec,
		decoder: NewDecoder(spec.Frame),
		faults:  NewFaultTracker(spec.Fault),
		slow:    NewSlowAssembler(spec.Frame.Variant, slowFormatFor(spec.Frame.Variant)),
	}
}

// Pipeline is the deterministic, single-channel receive core.
type Pipeline = channelPipeline

// NewPipeline builds a standalone (goroutine-free) channel pipeline. It is
// the same core used by Receiver and by Recording.Replay.
func NewPipeline(spec ChannelSpec) *Pipeline { return newPipeline(spec) }

// ProcessSample feeds one edge and returns all events it emitted in order.
func (p *channelPipeline) ProcessSample(s Sample) []Event { return p.processSample(s) }

// Flush reports unresolved state at end of input.
func (p *channelPipeline) FlushEvents() []Event { return p.flush() }

func (p *channelPipeline) tag(events []Event) []Event {
	for i := range events {
		events[i].Channel = p.spec.Name
	}
	return events
}

// processSample feeds one edge and returns emitted events in order.
func (p *channelPipeline) processSample(s Sample) []Event {
	if s.Edge != EdgeFalling {
		return nil
	}
	if p.lastEdge != nil {
		pulse := Pulse{At: s.At, Width: s.At - p.lastEdge.At, FrameSeq: s.FrameSeq}
		p.lastEdge = &s
		return p.processPulse(pulse)
	}
	p.lastEdge = &s
	return nil
}

// processPulse handles one completed falling-edge interval.
func (p *channelPipeline) processPulse(pulse Pulse) []Event {
	var events []Event
	for _, result := range p.decoder.Push(pulse) {
		events = append(events, p.processResult(result)...)
	}
	return p.tag(events)
}

func (p *channelPipeline) processResult(result DecoderResult) []Event {
	if result.Kind == ResultError {
		return []Event{{Kind: EvtFrameError, FrameErr: result.Err}}
	}
	return p.processFrame(result.Frame)
}

func (p *channelPipeline) processFrame(frame *RawFrame) []Event {
	nibbles := make([]uint8, 0, 8)
	nibbles = append(nibbles, frame.Status)
	nibbles = append(nibbles, frame.Data[:]...)
	nibbles = append(nibbles, frame.CRC)

	if !verifyCRC(p.spec.Frame.Variant, nibbles) {
		other := VariantA
		if p.spec.Frame.Variant == VariantA {
			other = VariantB
		}
		if verifyCRC(other, nibbles) {
			events := []Event{}
			if !p.variantReported {
				p.variantReported = true
				events = append(events, Event{
					Kind: EvtVariantMismatch,
					FrameErr: &FrameError{
						Kind:     ErrVariantMismatch,
						At:       frame.StartAt,
						FrameSeq: frame.FrameSeq,
						Detail:   "frame validates under the other declared variant",
					},
				})
			}
			events = append(events, Event{
				Kind: EvtFrameError,
				FrameErr: &FrameError{
					Kind:     ErrVariantMismatch,
					At:       frame.StartAt,
					FrameSeq: frame.FrameSeq,
					Detail:   "declared variant does not match arriving frame",
				},
			})
			if drop := p.slow.Reset(SlowResetOnVariant, frame.StartAt, frame.FrameSeq); drop != nil {
				events = append(events, Event{Kind: EvtSlowDrop, SlowDrop: drop})
			}
			return events
		}

		return []Event{{
			Kind: EvtFrameError,
			FrameErr: &FrameError{
				Kind:     ErrCRC,
				At:       frame.StartAt,
				FrameSeq: frame.FrameSeq,
				Detail:   "checksum nibble mismatch",
			},
		}}
	}

	return p.emitReading(frame)
}

func (p *channelPipeline) emitReading(frame *RawFrame) []Event {
	code := frame.Status >> 2 & 0x03
	status, changed := p.faults.Observe(code, frame.StartAt)

	reading := &ChannelReading{
		FrameSeq: frame.FrameSeq,
		At:       frame.StartAt,
		Status:   frame.Status,
		Raw:      frame.Data,
		Fast:     FastValue(frame.Data),
		Physical: p.spec.Physical.Decode(frame.Data),
		Sensor: SensorStatus{
			Code:       code,
			Transient:  code == SensorTransient,
			Persistent: code == SensorHard || code == SensorPersist,
		},
	}

	events := []Event{{Kind: EvtReading, Reading: reading}}

	if changed {
		st := status
		events = append(events, Event{Kind: EvtFaultChange, Fault: &st})
	}

	if msg, drop := p.slow.Observe(frame.FrameSeq, frame.Status&0x03, frame.StartAt); msg != nil {
		events = append(events, Event{Kind: EvtSlowMessage, Slow: msg})
	} else if drop != nil {
		events = append(events, Event{Kind: EvtSlowDrop, SlowDrop: drop})
	}
	return events
}

// flush reports unresolved decoder state at end of stream.
func (p *channelPipeline) flush() []Event {
	var events []Event
	for _, result := range p.decoder.Flush() {
		events = append(events, p.processResult(result)...)
	}
	return events
}
