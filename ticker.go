package clockwork

import (
	"fmt"
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
	// Used for blocking access to the tick channel until there are ticks
	// ready to be queued. Indended to have a buffer of 1 so sends do not
	// block.
	nextTicks chan []time.Time
	stop      chan bool
	clock     FakeClock
	period    time.Duration
}

// Chan retrieves the fakeTicker's tick channel. The channel is lazily
// initialized and buffered to hold all of the ticks that elapsed while
// advancing the fake clock.
//
// If there are no ticks to send, the returned channel will be unbuffered
// with no senders.
func (ft *fakeTicker) Chan() <-chan time.Time {
	ticks := <-ft.nextTicks
	fmt.Println(time.Now(), "TICKTEST: Chan(): just received from nextTicks")

	c := make(chan time.Time, len(ticks))

	for _, r := range ticks {
		c <- r
	}
	fmt.Println(time.Now(), "TICKTEST: Chan(): returning a buffered channel")
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
	fmt.Println(time.Now(), "TICKTEST: runTickThread: just called After. Initializing the tick thread goroutine")
	go func() {
		for {
			select {
			case <-ft.stop:
				fmt.Println(time.Now(), "TICKTEST: runTickThread: received from the stop channel")
				// Initialize the tick channel with zero ticks
				// so calling `select` statements with `default`
				// conditions can bail.
				ft.nextTicks <- []time.Time{}
				return
			case <-next:
				fmt.Println(time.Now(), "TICKTEST: runTickThread: received from the next channel")

				now := ft.clock.Now()
				// We've advanced the fake clock, so round up
				// the ticks that would have elapsed during this
				// time and reset the tick thread. Send ticks
				// to the internal channel until we're past the
				// current time of the fake clock
				ticks := []time.Time{}
				for !nextTick.After(now) {
					ticks = append(ticks, nextTick)
					nextTick = nextTick.Add(ft.period)
				}
				fmt.Println(time.Now(), "TICKTEST: runTickThread: finished creating a slice of ticks to send")
				// Figure out how long between now and the next
				// scheduled tick, then wait that long.
				remaining := nextTick.Sub(ft.clock.Now())
				fmt.Println(time.Now(), "TICKTEST: runTickThread: reassigning next to After")
				next = ft.clock.After(remaining)
				fmt.Println(time.Now(), "TICKTEST: runTickThread: about to send to nextTicks")

				ft.nextTicks <- ticks

			}
		}
	}()
}
