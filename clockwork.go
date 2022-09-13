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
	kind  sleeperKind
	next  *sleeper
	// Associate the sleepr with a ticker so we can remove the sleepr when
	// we stop the ticker
	ticker *fakeTicker
	// A function to called in Advance when the sleeper is ready.
	// The function is called with the fake clock's current time.
	notify func(time.Time)
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
	t := make(chan time.Time, 1)
	if d.Nanoseconds() <= 0 {
		// special case: trigger immediately
		t <- fc.time
	}
	fc.addSleeper(
		&sleeper{
			until: fc.time.Add(d),
			kind:  oneShotSleeper,
			notify: func(m time.Time) {
				t <- m
			},
		})
	return (<-chan time.Time)(t)
}

// addRepeatingSleeper adds a sleeper that waits for fakeTicker k's period  to
// elapse, then sends to the returned channel. The *fakeClock then refreshes the
// sleeper with the same duration. Like time.NewTicker, this panics if d is not
// greater than zero.
//
// fc must be holding the lock on fc.l when calling addRepeatingSleeper.
func (fc *fakeClock) addRepeatingSleeper(k *fakeTicker) {
	if k.period.Nanoseconds() <= 0 {
		panic("a repeating sleeper must have a positive, nonzero duration")
	}
	fc.addSleeper(&sleeper{
		until:  fc.time.Add(k.period),
		kind:   repeatingSleeper,
		ticker: k,
		notify: func(m time.Time) {
			select {
			case k.c <- m:
			default:
				return
			}
		},
	})
	return
}

// addSleeper inserts a new sleeper into the fakeClock's list of sleepers,
// ordered by soonest to least soon.
//
// fc must be holding the lock on fc.l when calling addRepeatingSleeper.
func (fc *fakeClock) addSleeper(s *sleeper) <-chan time.Time {
	now := fc.time
	done := make(chan time.Time, 1)
	if s.until.Sub(fc.time).Nanoseconds() <= 0 {
		// special case - trigger immediately
		done <- now
	} else {
		var n int
		// Order the sleepers by their until field, smallest to largest.
		// Reassign the next sleeper to after s if necessary. Also count
		// all sleepers.
		for l := fc.sleepers; l != nil; l = l.next {
			n++
			if (s.until.Equal(l.until) || s.until.After(l.until)) &&
				(l.next == nil || l.next.until.After(s.until) || l.next.until.Equal(s.until)) {
				if l.next != nil {
					s.next = l.next
				}
				l.next = s
				n++
				break
			}
		}

		if fc.sleepers == nil {
			fc.sleepers = s
			n++
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
		c:      make(chan time.Time, 1),
		stop:   make(chan bool, 1),
		clock:  fc,
		period: d,
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

	// The latest tick we have simulated for each fake ticker. We track this
	// in order to send accurate times to each fake ticker's tick channel.
	lts := make(map[*fakeTicker]time.Time)
	// Notify all sleepers that have elapsed. Reassign the fake clock's
	// sleepers to those that have not elapsed.
	for s := fc.sleepers; s != nil; s = s.next {
		// Sleepers are ordered chronologically, so reset sleepers to
		// the first one that is after the fake clock's new time.
		if s.until.After(end) {
			fc.sleepers = s
			break
		}

		if s.kind == repeatingSleeper {
			// The sleeper is repeating, so increment our internal map
			// of each sleeper's latest time. This lets us assign the
			// `until` field of each sleeper accurately.
			lt, ok := lts[s.ticker]
			if ok {
				lts[s.ticker] = lt.Add(s.ticker.period)
			} else {
				lts[s.ticker] = s.until.Add(s.ticker.period)
			}
			fmt.Println("TICKTEST: Advance: Adding a repeating sleeper")
			// Simulate repeating ticker behavior by adding a new
			// repeating sleeper with an until time corresponding to
			// the next "tick".
			fc.addSleeper(&sleeper{
				until:  lts[s.ticker],
				kind:   repeatingSleeper,
				ticker: s.ticker,
				notify: func(m time.Time) {
					select {
					case s.ticker.c <- m:
					default:
						return
					}
				},
			})
		}
		s.notify(s.until)
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

// stopTicker removes any repeating sleepers originating from fakeTicker t from
// the fakeClock's sleepers list.
func (fc *fakeClock) stopTicker(t *fakeTicker) {
	fc.l.Lock()
	defer fc.l.Unlock()
	// Find the first sleeper that does not belong to t and assign
	// fc.sleepers to it.
	for fc.sleepers != nil && fc.sleepers.ticker == t {
		fc.sleepers = fc.sleepers.next
		continue
	}
	// Now that we have set the first sleeper (i.e., the head of the lsit)
	// to something that doesn't belong to t, let's remove all linked
	// sleepers that belong to t.
	for s := fc.sleepers; s != nil; s = s.next {
		if s.next != nil && s.next.ticker == t {
			s.next = s.next.next
		}
	}
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
