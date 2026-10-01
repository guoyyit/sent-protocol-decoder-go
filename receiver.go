package sent

import (
	"errors"
	"sync"
)

// ChannelStats reports per-channel runtime counters.
type ChannelStats struct {
	Frames   int64
	Errors   int64
	Overflow int64
}

type managedChannel struct {
	cfg     ChannelConfig
	decoder *Decoder
	input   chan []Edge
	done    chan struct{}
	mu      sync.Mutex
	stats   ChannelStats
}

// Manager runs an independent receiver goroutine per sensor channel and
// merges their events onto one output stream.
type Manager struct {
	out chan Event

	mu       sync.Mutex
	channels map[string]*managedChannel
	wg       sync.WaitGroup
	closed   bool
}

// NewManager creates a manager delivering to out.
func NewManager(out chan Event) *Manager {
	return &Manager{out: out, channels: map[string]*managedChannel{}}
}

// Add starts a receiver for a dynamically added channel.
func (m *Manager) Add(cfg ChannelConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("sent: manager is closed")
	}
	if _, exists := m.channels[cfg.ID]; exists {
		return errors.New("sent: channel already exists: " + cfg.ID)
	}

	ch := &managedChannel{
		cfg:     cfg,
		decoder: NewDecoder(cfg),
		input:   make(chan []Edge, 16),
		done:    make(chan struct{}),
	}
	m.channels[cfg.ID] = ch
	m.wg.Add(1)
	go m.run(ch)
	return nil
}

// Remove stops a channel and fully reclaims its processing state.
func (m *Manager) Remove(id string) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("sent: manager is closed")
	}
	ch, ok := m.channels[id]
	if !ok {
		m.mu.Unlock()
		return errors.New("sent: unknown channel: " + id)
	}
	delete(m.channels, id)
	m.mu.Unlock()

	close(ch.input)
	<-ch.done
	return nil
}

// Push delivers edges from a channel's signal source.
func (m *Manager) Push(id string, edges []Edge) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("sent: manager is closed")
	}
	ch, ok := m.channels[id]
	m.mu.Unlock()
	if !ok {
		return errors.New("sent: unknown channel: " + id)
	}

	select {
	case ch.input <- edges:
		return nil
	default:
		// A backed-up channel must not block the caller or other channels.
		ch.mu.Lock()
		ch.stats.Overflow++
		ch.mu.Unlock()
		return ErrChannelBackpressure
	}
}

// Close stops all channels and releases every resource.
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("sent: manager already closed")
	}
	m.closed = true
	pending := make([]*managedChannel, 0, len(m.channels))
	for _, ch := range m.channels {
		pending = append(pending, ch)
	}
	m.channels = map[string]*managedChannel{}
	m.mu.Unlock()

	for _, ch := range pending {
		close(ch.input)
	}
	m.wg.Wait()
	return nil
}

// Stats returns a snapshot of per-channel counters.
func (m *Manager) Stats() map[string]ChannelStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]ChannelStats, len(m.channels))
	for id, ch := range m.channels {
		ch.mu.Lock()
		out[id] = ch.stats
		ch.mu.Unlock()
	}
	return out
}

func (m *Manager) run(ch *managedChannel) {
	defer m.wg.Done()
	defer close(ch.done)

	for batches := range ch.input {
		events := ch.decoder.PushEdges(batches)
		for _, ev := range events {
			ch.mu.Lock()
			if ev.Kind == EventReading {
				ch.stats.Frames++
			}
			if ev.Kind == EventFrameError {
				ch.stats.Errors++
			}
			// Never block the receiver loop on a slow downstream consumer:
			// drop this channel's event and count the overflow instead.
			select {
			case m.out <- ev:
			default:
				ch.stats.Overflow++
			}
			ch.mu.Unlock()
		}
	}
}

// ErrChannelBackpressure is returned when a channel cannot accept more edges.
var ErrChannelBackpressure = errors.New("sent: channel input backpressure")
