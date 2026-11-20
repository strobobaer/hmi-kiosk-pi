//go:build windows

package winsignal

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

const pollEvery = 500 // ms: so oft prüft Listen, ob ctx endete

// Listen legt das Ereignis name an und ruft fn bei jedem Signal, bis ctx endet. Es blockiert.
func Listen(ctx context.Context, name string, fn func()) error {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	h, err := windows.CreateEvent(nil, 0, 0, p) // automatisch zurückgesetzt
	if h == 0 {
		return fmt.Errorf("Ereignis %s: %w", name, err)
	}
	defer windows.CloseHandle(h)
	for ctx.Err() == nil {
		r, err := windows.WaitForSingleObject(h, pollEvery)
		switch r {
		case windows.WAIT_OBJECT_0:
			fn()
		case uint32(windows.WAIT_TIMEOUT):
		default:
			return fmt.Errorf("Ereignis %s: %w", name, err)
		}
	}
	return nil
}

// Notify löst das Ereignis aus. Es meldet false, wenn in dieser Sitzung niemand lauscht (oder der Zugriff
// verweigert ist).
func Notify(name string) (bool, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return false, err
	}
	h, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, p)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return false, nil
		}
		return false, err
	}
	defer windows.CloseHandle(h)
	if err := windows.SetEvent(h); err != nil {
		return false, err
	}
	return true, nil
}
