package clockwork

import (
	"errors"
	"fmt"
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
	// Queue of ticks to send. Must be accessed via SendTick and GetTick.
	nextTicks []time.Time
	mu        *sync.Mutex
	stop      chan bool
	clock     FakeClock
	period    time.Duration
}

// SendTick adds a tick to the fake ticker's tick queue
func (ft *fakeTicker) SendTick(k []time.Time) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	fmt.Println("TICKTEST; SendTick: appending to nextTicks")
	ft.nextTicks = append(ft.nextTicks, k...)
}

// GetTick returns the next tick from the tick queue or, if there are none
// available, an error.
func (ft *fakeTicker) GetTick() (time.Time, error) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	fmt.Println("TICKTEST: GetTick: getting the next tick")
	if len(ft.nextTicks) == 0 {
		fmt.Println("TICKTEST: GetTick: returning an error")
		return time.Now(), errors.New("there are no ticks remaining in the queue")
	}
	fmt.Println("TICKTEST: GetTick: Retrieving the first tick")
	n := ft.nextTicks[0]
	ft.nextTicks = ft.nextTicks[1:]
	return n, nil
}

// Chan retrieves the fakeTicker's tick channel, which is always one-buffered
// and contains the latest tick. Callers must always access the tick channel by
// calling Chan, as the channel is replaced with each call.
func (ft *fakeTicker) Chan() <-chan time.Time {
	fmt.Println("TICKTEST: Chan: at the top of the function")
	ft.mu.Lock()
	defer ft.mu.Unlock()
	c := make(chan time.Time, 1)
	fmt.Println("TICKTEST: GetTick: getting the next tick")
	if len(ft.nextTicks) == 0 {
		fmt.Println("TICKTEST: Chan: returning empty channel")
		return c
	}
	fmt.Println("TICKTEST: GetTick: Retrieving the first tick")
	m := ft.nextTicks[0]
	ft.nextTicks = ft.nextTicks[1:]
	c <- m
	fmt.Println("TICKTEST: Chan: just sent to the channel to return")
	fmt.Println("TICKTEST: Chan: returning a channel")
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
				return
			case <-next:
				ft.mu.Lock()
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
				fmt.Println(time.Now(), "TICKTEST: runTickThread: about to send to nextTicks")
				ft.nextTicks = append(ft.nextTicks, ticks...)
				fmt.Println("TICKTEST: runTickThread: just returned from SendTicks")
				ft.mu.Unlock()
				next = ft.clock.After(remaining)
			}
		}
	}()
}
