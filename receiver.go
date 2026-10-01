package sent

import (
	"errors"
	"sync"
)

// ErrChannelNotFound is returned for unknown channel names.
var ErrChannelNotFound = errors.New("sent: channel not found")

// ErrChannelExists is returned when adding a duplicate channel name.
var ErrChannelExists = errors.New("sent: channel already exists")

// ErrSlowConsumer is returned by Feed when a channel's input queue is full;
// the caller may retry or drop the sample for that channel only.
var ErrSlowConsumer = errors.New("sent: channel queue full (slow consumer)")

type channelState struct {
	spec     ChannelSpec
	pipeline *channelPipeline
	input    chan Sample
	local    chan Event
	done     chan struct{}
	wg       sync.WaitGroup

	mu       sync.Mutex
	overflow []Event
	stopped  bool
}

// Receiver runs concurrent, isolated per-channel receive pipelines.
//
// Every channel owns a private input queue, event queue, processing goroutine
// and forwarding goroutine. A stalled downstream consumer only causes that
// channel's events to queue (and, eventually, Feed to return
// ErrSlowConsumer for that channel); every other channel keeps processing.
// Removing a channel cancels and reclaims all of its goroutines and state.
type Receiver struct {
	mu       sync.Mutex
	channels map[string]*channelState
	events   chan Event
	closed   bool
	size     int
}

// NewReceiver creates a receiver with the given per-channel queue size.
func NewReceiver(eventBuffer int) *Receiver {
	if eventBuffer <= 0 {
		eventBuffer = 64
	}
	return &Receiver{
		channels: make(map[string]*channelState),
		events:   make(chan Event, eventBuffer),
		size:     eventBuffer,
	}
}

// AddChannel dynamically registers and starts a channel.
func (r *Receiver) AddChannel(spec ChannelSpec) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errors.New("sent: receiver closed")
	}
	if _, exists := r.channels[spec.Name]; exists {
		return ErrChannelExists
	}

	st := &channelState{
		spec:     spec,
		pipeline: newPipeline(spec),
		input:    make(chan Sample, r.size),
		local:    make(chan Event, r.size),
		done:     make(chan struct{}),
	}
	r.channels[spec.Name] = st

	st.wg.Add(2)
	go r.runProcessor(st)
	go r.runForwarder(st)
	return nil
}

// runProcessor turns edges into ordered events on the channel-local queue.
func (r *Receiver) runProcessor(st *channelState) {
	defer st.wg.Done()
	for {
		select {
		case sample, ok := <-st.input:
			if !ok {
				// Normal drain at receiver close: flush pending frame.
				for _, ev := range st.pipeline.flush() {
					select {
					case st.local <- ev:
					case <-st.done:
						return
					}
				}
				return
			}
			for _, ev := range st.pipeline.processSample(sample) {
				select {
				case st.local <- ev:
				case <-st.done:
					return
				}
			}
		case <-st.done:
			return
		}
	}
}

// runForwarder drains the local queue into the multiplexed output. If the
// shared output cannot accept immediately, events move to this channel's own
// overflow buffer, so processing is never blocked by other consumers.
func (r *Receiver) runForwarder(st *channelState) {
	defer st.wg.Done()

	for {
		// Deliver previously buffered overflow events first (in order).
		st.mu.Lock()
		if len(st.overflow) > 0 {
			ev := st.overflow[0]
			st.overflow = st.overflow[1:]
			st.mu.Unlock()
			select {
			case r.events <- ev:
				continue
			case <-st.done:
				return
			}
		}
		st.mu.Unlock()

		select {
		case ev, ok := <-st.local:
			if !ok {
				return
			}
			select {
			case r.events <- ev:
			default:
				st.mu.Lock()
				st.overflow = append(st.overflow, ev)
				st.mu.Unlock()
			}
		case <-st.done:
			return
		}
	}
}

// cancelOnce closes the cancellation signal exactly once.
func (st *channelState) cancelOnce() {
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.stopped {
		st.stopped = true
		close(st.done)
	}
}

// RemoveChannel stops a channel and reclaims all of its processing state.
// It blocks until the channel's goroutines have exited. Events still queued
// only for this channel are discarded with the channel.
func (r *Receiver) RemoveChannel(name string) error {
	r.mu.Lock()
	st, ok := r.channels[name]
	if ok {
		delete(r.channels, name)
	}
	r.mu.Unlock()
	if !ok {
		return ErrChannelNotFound
	}

	st.cancelOnce()
	st.wg.Wait()
	st.mu.Lock()
	st.overflow = nil
	st.mu.Unlock()
	return nil
}

// Feed enqueues one edge sample for a channel without blocking.
func (r *Receiver) Feed(name string, s Sample) error {
	r.mu.Lock()
	st, ok := r.channels[name]
	r.mu.Unlock()
	if !ok {
		return ErrChannelNotFound
	}
	select {
	case st.input <- s:
		return nil
	case <-st.done:
		return ErrChannelNotFound
	default:
		return ErrSlowConsumer
	}
}

// Events returns the multiplexed output channel.
func (r *Receiver) Events() <-chan Event { return r.events }

// Close stops and reclaims every channel, then closes the output channel
// after all in-flight events have been forwarded.
func (r *Receiver) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	sts := make([]*channelState, 0, len(r.channels))
	for _, st := range r.channels {
		sts = append(sts, st)
	}
	r.channels = map[string]*channelState{}
	r.mu.Unlock()

	for _, st := range sts {
		st.cancelOnce()
	}

	// Wait for all processors/forwarders, then close local queues and the
	// multiplexed output.
	var wg sync.WaitGroup
	for _, st := range sts {
		st := st
		wg.Add(1)
		go func() {
			defer wg.Done()
			st.wg.Wait()
		}()
	}
	wg.Wait()
	for _, st := range sts {
		close(st.local)
	}
	close(r.events)
	return nil
}
