//go:build !linux

package clock

// Follow runs only in a Linux guest.
func Follow(port uint32) { panic("clock: Follow runs only in a Linux guest") }
