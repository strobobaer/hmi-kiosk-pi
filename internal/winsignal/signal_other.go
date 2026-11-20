//go:build !windows

package winsignal

import (
	"context"
	"sync"
)

// Außerhalb von Windows (nur für Tests): ein Namensverzeichnis im Prozess.
var (
	mu        sync.Mutex
	listeners = map[string]chan struct{}{}
)

// Listen legt das Ereignis name an und ruft fn bei jedem Signal, bis ctx endet. Es blockiert.
func Listen(ctx context.Context, name string, fn func()) error {
	ch := make(chan struct{}, 1)
	mu.Lock()
	listeners[name] = ch
	mu.Unlock()
	defer func() {
		mu.Lock()
		if listeners[name] == ch {
			delete(listeners, name)
		}
		mu.Unlock()
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ch:
			fn()
		}
	}
}

// Notify löst das Ereignis aus; false, wenn niemand lauscht.
func Notify(name string) (bool, error) {
	mu.Lock()
	ch := listeners[name]
	mu.Unlock()
	if ch == nil {
		return false, nil
	}
	select {
	case ch <- struct{}{}:
	default: // schon ein Signal offen
	}
	return true, nil
}
