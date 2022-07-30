package clockwork

import (
	"sync"
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
	// Used for blocking access to the tick channel until it is ready, i.e.,
	// until all ticks to be sent are queued.
	tickChanReady *sync.Mutex
	// Queued ticks. fakeTicker sends these to a lazily initialized channel
	// when Chan is called.
	ticks  []time.Time
	stop   chan bool
	clock  FakeClock
	period time.Duration
}

// Chan retrieves the fakeTicker's tick channel. The channel is lazily
// initialized and buffered to hold all of the ticks that elapsed while
// advancing the fake clock.
func (ft *fakeTicker) Chan() <-chan time.Time {
	ft.tickChanReady.Lock()
	defer ft.tickChanReady.Unlock()
	c := make(chan time.Time, len(ft.ticks))

	for _, r := range ft.ticks {
		c <- r
	}
	return c
}

func (ft *fakeTicker) Stop() {
	ft.stop <- true
}

// loadTicks holds the tick channel readiness lock and populates the
// fakeTicker's internal tick queue, sending all ticks that would have elapsed
// between the start time and the fake clock's current time. During an Advance()
// call, this simulates sending ticks to the fakeTicker's channel over the
// course of the elapsed time.
// Returns the time.Time of the latest tick so we can re-initialize the fake
// Ticker.
func (ft *fakeTicker) loadTicks(start time.Time) time.Time {
	ft.tickChanReady.Lock()
	defer ft.tickChanReady.Unlock()

	now := ft.clock.Now()
	// Send ticks to the internal channel until
	// we're past the current time of the fake clock
	for !start.After(now) {
		start = start.Add(ft.period)
		ft.ticks = append(ft.ticks, start)
	}
	return start
}

// runTickThread initializes a background goroutine to send the tick time to the ticker channel
// after every period.
func (ft *fakeTicker) runTickThread() {
	nextTick := ft.clock.Now().Add(ft.period)
	next := ft.clock.After(ft.period)
	go func() {
		for {
			select {
			case <-ft.stop:
				return
				// We've advanced the fake clock, so round up
				// the ticks that would have elapsed during this
				// time and reset the tick thread.
			case <-next:
				nextTick := ft.loadTicks(nextTick)
				// Figure out how long between now and the next
				// scheduled tick, then wait that long.
				remaining := nextTick.Sub(ft.clock.Now())
				next = ft.clock.After(remaining)
			}
		}
	}()
}
