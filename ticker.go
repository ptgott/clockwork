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
	tickChanReady *sync.Cond
	// Used for locking tickChanReady
	mu *sync.Mutex
	// Queued ticks. fakeTicker sends these to a lazily initialized channel
	// when Chan is called. The fakeTicker is considered ready for receivers
	// from Chan when ticks is non-nil.
	ticks  []time.Time
	stop   chan bool
	clock  FakeClock
	period time.Duration
}

// Chan retrieves the fakeTicker's tick channel. The channel is lazily
// initialized and buffered to hold all of the ticks that elapsed while
// advancing the fake clock.
//
// If there are no ticks to send, the returned channel will be unbuffered
// with no senders.
func (ft *fakeTicker) Chan() <-chan time.Time {
	ft.mu.Lock()
	defer ft.mu.Unlock()

	// Loop to double-check the condition. See:
	// https://pkg.go.dev/sync#Cond.Wait
	for ft.ticks == nil {
		ft.tickChanReady.Wait()
	}

	c := make(chan time.Time, len(ft.ticks))

	for _, r := range ft.ticks {
		c <- r
	}
	return c
}

func (ft *fakeTicker) Stop() {
	ft.stop <- true
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
				// Initialize the tick channel with zero ticks
				ft.ticks = []time.Time{}
				ft.tickChanReady.Broadcast()
				return
			case <-next:

				now := ft.clock.Now()
				// We've advanced the fake clock, so round up
				// the ticks that would have elapsed during this
				// time and reset the tick thread. Send ticks
				// to the internal channel until we're past the
				// current time of the fake clock
				ft.ticks = []time.Time{}
				for !nextTick.After(now) {
					ft.ticks = append(ft.ticks, nextTick)
					nextTick = nextTick.Add(ft.period)
				}

				// Figure out how long between now and the next
				// scheduled tick, then wait that long.
				remaining := nextTick.Sub(ft.clock.Now())
				next = ft.clock.After(remaining)

				// Indicate that the tick channel is ready for
				// receivers
				ft.tickChanReady.Broadcast()
			}
		}
	}()
}
