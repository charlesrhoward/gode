package event

import "sync"

// Bus is a simple typed event bus.
type Bus struct {
	listeners map[string][]func(interface{})
	mu        sync.RWMutex
}

func NewBus() *Bus {
	return &Bus{listeners: make(map[string][]func(interface{}))}
}

func (b *Bus) On(event string, fn func(interface{})) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.listeners[event] = append(b.listeners[event], fn)
}

func (b *Bus) Emit(event string, data interface{}) {
	b.mu.RLock()
	fns := b.listeners[event]
	b.mu.RUnlock()

	for _, fn := range fns {
		fn(data)
	}
}
