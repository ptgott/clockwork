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
	c      chan time.Time
	stop   chan bool
	clock  FakeClock
	period time.Duration
}

func (ft *fakeTicker) Chan() <-chan time.Time {
	return ft.c
}

func (ft *fakeTicker) Stop() {
	ft.stop <- true
}

// runTickThread initializes a background goroutine to send the tick time to the ticker channel
// after every period. Tick events are discarded if the underlying ticker channel does not have
// enough capacity.
func (ft *fakeTicker) runTickThread() {
	fc := ft.clock.(*fakeClock)
	fc.addRepeatingSleeper(ft)
	go func() {
		for {
			select {
			case <-ft.stop:
				fc.stopTicker(ft)
				return
			}
		}
	}()
}
