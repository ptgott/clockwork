package clockwork

import (
	"time"
)

// Ticker provides an interface which can be used instead of directly
// using the ticker within the time module. The real-time ticker t
// provides ticks through t.C which becomes now t.Chan() to make
// this channel requirement definable in this interface.
type Ticker interface {
	Chan() <-chan time.Time
	Stop()
}

type realTicker struct{ *time.Ticker }

func (rt *realTicker) Chan() <-chan time.Time {
	return rt.C
}

type fakeTicker struct {
	// Internal channel used for queuing batches of ticks for when a caller
	// advances the fake clock. Should be unbuffered.
	c      chan time.Time
	stop   chan bool
	clock  FakeClock
	period time.Duration
}

func (ft *fakeTicker) Chan() <-chan time.Time {
	s := []time.Time{}

	// Buffer ticks from the internal tick channel until waking the tick thread
	// sleeper closes the channel.
	for {
		if t, ok := <-ft.c; ok {
			s = append(s, t)
			continue
		}
		break
	}

	c := make(chan time.Time, len(s))

	for _, r := range s {
		c <- r
	}
	return c
}

func (ft *fakeTicker) Stop() {
	ft.stop <- true
}

// runTickThread initializes a background goroutine to send the tick time to the ticker channel
// after every period. Tick events are discarded if the underlying ticker channel does not have
// enough capacity.
func (ft *fakeTicker) runTickThread() {
	nextTick := ft.clock.Now().Add(ft.period)
	next := ft.clock.After(ft.period)
	go func() {
		for {
			select {
			case <-ft.stop:
				return
			case <-next:
				// Before sending the tick, we'll compute the next tick time and star the clock.After call.
				now := ft.clock.Now()
				// Send ticks to the internal channel until
				// we're past the current time of the fake clock
				for !nextTick.After(now) {
					nextTick = nextTick.Add(ft.period)
					ft.c <- nextTick
				}
				// Indicate to consumers of ft's external tick
				// channel that the channel is ready
				close(ft.c)
			}
		}
	}()
}
