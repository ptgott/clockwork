package clockwork

import (
	"fmt"
	"sync"
	"time"
)

// Clock provides an interface that packages can use instead of directly
// using the time module, so that chronology-related behavior can be tested
type Clock interface {
	After(d time.Duration) <-chan time.Time
	Sleep(d time.Duration)
	Now() time.Time
	Since(t time.Time) time.Duration
	NewTicker(d time.Duration) Ticker
	NewTimer(d time.Duration) Timer
}

// FakeClock provides an interface for a clock which can be
// manually advanced through time
type FakeClock interface {
	Clock
	// Advance advances the FakeClock to a new point in time, ensuring any existing
	// sleepers are notified appropriately before returning
	Advance(d time.Duration)
	// BlockUntil will block until the FakeClock has the given number of
	// sleepers (callers of Sleep or After)
	BlockUntil(n int)
}

// NewRealClock returns a Clock which simply delegates calls to the actual time
// package; it should be used by packages in production.
func NewRealClock() Clock {
	return &realClock{}
}

// NewFakeClock returns a FakeClock implementation which can be
// manually advanced through time for testing. The initial time of the
// FakeClock will be an arbitrary non-zero time.
func NewFakeClock() FakeClock {
	// use a fixture that does not fulfill Time.IsZero()
	return NewFakeClockAt(time.Date(1984, time.April, 4, 0, 0, 0, 0, time.UTC))
}

// NewFakeClockAt returns a FakeClock initialised at the given time.Time.
func NewFakeClockAt(t time.Time) FakeClock {
	return &fakeClock{
		time: t,
	}
}

type realClock struct{}

func (rc *realClock) After(d time.Duration) <-chan time.Time {
	return time.After(d)
}

func (rc *realClock) Sleep(d time.Duration) {
	time.Sleep(d)
}

func (rc *realClock) Now() time.Time {
	return time.Now()
}

func (rc *realClock) Since(t time.Time) time.Duration {
	return rc.Now().Sub(t)
}

func (rc *realClock) NewTicker(d time.Duration) Ticker {
	return &realTicker{time.NewTicker(d)}
}

func (rc *realClock) NewTimer(d time.Duration) Timer {
	return &realTimer{time.NewTimer(d)}
}

type fakeClock struct {
	// A linked list of sleepers sorted so that the head's until is earliest
	sleepers *sleeper
	blockers []*blocker
	time     time.Time

	l sync.RWMutex
}

type sleeperKind int

const (
	oneShotSleeper sleeperKind = iota
	repeatingSleeper
)

// sleeper represents a caller of After. The fake clock sends a time.Time to its
// done channel after advancing past its until time. When one sleeper is
// finished, it is possible to access the next sleeper via its next property.
type sleeper struct {
	until time.Time
	done  chan time.Time
	kind  sleeperKind
	next  *sleeper
}

// blocker represents a caller of BlockUntil
type blocker struct {
	count int
	ch    chan struct{}
}

// After mimics time.After; it waits for the given duration to elapse on the
// fakeClock, then sends the current time on the returned channel.
func (fc *fakeClock) After(d time.Duration) <-chan time.Time {
	fc.l.Lock()
	defer fc.l.Unlock()
	now := fc.time
	done := make(chan time.Time, 1)
	if d.Nanoseconds() <= 0 {
		// special case - trigger immediately
		done <- now
	} else {
		var n int
		// otherwise, add to the set of sleepers
		s := &sleeper{
			until: now.Add(d),
			done:  done,
			kind:  oneShotSleeper,
		}
		// Find the first sleeper that s comes after and insert s after
		// it. Reassign the next sleeper to after s if necessary. Also
		// count all sleepers.
		for l := fc.sleepers; l != nil; l = l.next {
			if s.until.After(l.until) {
				if l.next != nil {
					s.next = l.next
				}
				l.next = s
			}
			n++
		}

		if fc.sleepers == nil {
			fc.sleepers = s
		}
		// and notify any blockers
		fc.blockers = notifyBlockers(fc.blockers, n)
	}
	return done
}

// runTicker adds a repeating sleeper to fc, which the fakeClock refreshes once
// it has reached its until time. addRepeatingSleeper has the same interface for
// callers as *fakeClock.After. The returned time.Time channel receives whenever
// a new period of the repeating sleeper elapses.
//
// Like time.NewTicker, addRepeatingSleeper will panic if d is less than or
// equal to zero.
func (fc *fakeClock) addRepeatingSleeper(d time.Duration) <-chan time.Time {
	fc.l.Lock()
	defer fc.l.Unlock()
	now := fc.time
	done := make(chan time.Time, 1)
	if d.Nanoseconds() <= 0 {
		panic(
			"the duration of the repeating sleeper must be greater than zero",
		)
	} else {
		var n int
		s := &sleeper{
			until: now.Add(d),
			done:  done,
			kind:  repeatingSleeper,
		}
		// Find the first sleeper that s comes after and insert s after
		// it. Reassign the next sleeper to after s if necessary. Also
		// count all sleepers.
		for l := fc.sleepers; l != nil; l = l.next {
			if s.next == nil && s.until.After(l.until) {
				if l.next != nil {
					s.next = l.next
				}
				l.next = s
			}
			n++
		}

		if fc.sleepers == nil {
			fc.sleepers = s
		}
		// and notify any blockers
		fc.blockers = notifyBlockers(fc.blockers, n)
	}
	return done
}

// notifyBlockers notifies all the blockers waiting until the at least the given
// number of sleepers are waiting on the fakeClock. It returns an updated slice
// of blockers (i.e. those still waiting)
func notifyBlockers(blockers []*blocker, count int) (newBlockers []*blocker) {
	for _, b := range blockers {
		if b.count <= count {
			close(b.ch)
		} else {
			newBlockers = append(newBlockers, b)
		}
	}
	return
}

// Sleep blocks until the given duration has passed on the fakeClock
func (fc *fakeClock) Sleep(d time.Duration) {
	<-fc.After(d)
}

// Now returns the current time of the fakeClock
func (fc *fakeClock) Now() time.Time {
	fc.l.RLock()
	t := fc.time
	fc.l.RUnlock()
	return t
}

// Since returns the duration that has passed since the given time on the fakeClock
func (fc *fakeClock) Since(t time.Time) time.Duration {
	return fc.Now().Sub(t)
}

// NewTicker returns a ticker that will expire only after calls to fakeClock
// Advance have moved the clock passed the given duration
func (fc *fakeClock) NewTicker(d time.Duration) Ticker {
	ft := &fakeTicker{
		nextTicks: []time.Time{},
		mu:        &sync.Mutex{},
		stop:      make(chan bool, 1),
		clock:     fc,
		period:    d,
	}
	ft.runTickThread()
	return ft
}

// NewTimer returns a timer that will fire only after calls to fakeClock
// Advance have moved the clock passed the given duration
func (fc *fakeClock) NewTimer(d time.Duration) Timer {
	stopped := uint32(0)
	if d <= 0 {
		stopped = 1
	}
	ft := &fakeTimer{
		c:       make(chan time.Time, 1),
		stop:    make(chan struct{}, 1),
		reset:   make(chan reset, 1),
		clock:   fc,
		stopped: stopped,
	}

	ft.run(d)
	return ft
}

// Advance advances fakeClock to a new point in time, ensuring channels from any
// previous invocations of After are notified appropriately before returning
func (fc *fakeClock) Advance(d time.Duration) {
	fmt.Println(time.Now(), "TICKTEST: Advance: locking fc.l")
	fc.l.Lock()
	defer fc.l.Unlock()
	fmt.Println(time.Now(), "TICKTEST: Advance: calling fc.time.Add")
	end := fc.time.Add(d)
	// Notify all sleepers that have elapsed. Reassign the fake clock's
	// sleepers to those that have not elapsed.
	for s := fc.sleepers; s != nil; s = s.next {
		if s.until.After(end) {
			fc.sleepers = s
			break
		}
		fmt.Println(time.Now(), "TICKTEST: Advance: sending end to a sleeper's done channel")
		// This sleeper has elapsed, so notify it.
		s.done <- end
	}
	var n int
	// Count the unelapsed sleepers
	for s := fc.sleepers; s != nil; s = s.next {
		n++
	}
	fmt.Println(time.Now(), "TICKTEST: Advance: calling notifyBlockers")
	fc.blockers = notifyBlockers(fc.blockers, n)
	fc.time = end
	fmt.Println(time.Now(), "TICKTEST: Advance: end of the function. About to unlock fc.l")
}

// BlockUntil will block until the fakeClock has the given number of sleepers
// (callers of Sleep or After)
func (fc *fakeClock) BlockUntil(n int) {
	fc.l.Lock()
	var p int
	// Count the sleepers
	for s := fc.sleepers; s != nil; s = s.next {
		p++
	}
	// Fast path: we already have >= n sleepers.
	if p >= n {
		fc.l.Unlock()
		return
	}
	// Otherwise, we have < n sleepers. Set up a new blocker to wait for more.
	b := &blocker{
		count: n,
		ch:    make(chan struct{}),
	}
	fc.blockers = append(fc.blockers, b)
	fc.l.Unlock()
	<-b.ch
}
