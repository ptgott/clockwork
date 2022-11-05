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
	fc.sleepers = addSleeper(
		fc.sleepers,
		&sleeper{
			until: fc.time.Add(d),
			kind:  oneShotSleeper,
			notify: func(m time.Time) {
				fmt.Println("MULTITICKS: NOTIFYING ONESHOT SLEEPER with until ", fc.time.Add(d))
				t <- m
			},
		})

	fc.blockers = notifyBlockers(fc.blockers, countSleepers(fc.sleepers))

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
	fc.sleepers = addSleeper(
		fc.sleepers,
		&sleeper{
			until:  fc.time.Add(k.period),
			kind:   repeatingSleeper,
			ticker: k,
			notify: func(m time.Time) {
				k.c <- m
				return
			},
		})
	fc.blockers = notifyBlockers(fc.blockers, countSleepers(fc.sleepers))
	return
}

// addSleeper inserts a new sleeper s into the list of sleepers beginning at
// h, then returns the head of the newly reorganized list of sleepers.
//
// addSleeper does not manage any locks, so callers must ensure that this
// operation is goroutine safe.
func addSleeper(h, s *sleeper) *sleeper {

	if h == nil {
		return s
	}

	// Order the sleepers by their until field, smallest to largest.
	// Reassign the next sleeper to after s if necessary. Also count
	// all sleepers.
	var b *sleeper // The previous sleeper
	for l := h; l != nil; l = l.next {
		if s == l {
			// Don't allow duplicate sleepers
			break
		}
		if s.until.Before(l.until) ||
			s.until.Equal(l.until) {
			s.next = l
			if b != nil {
				b.next = s
			} else {
				h = s
			}
			break
		}
		// We're at the last sleeper in the chain and the
		// candidate sleeper doesn't come before it and isn't
		// equal to it, so we'll place the candidate last.
		if l.next == nil {
			l.next = s
			break
		}
		b = l
	}
	return h

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

// sleeperSet is used to track operations on linked lists of sleepers. It
// contains the first in a list of elapsed sleepers as well as the first in a
// list of unelapsed sleepers.
type sleeperSet struct {
	elapsed   *sleeper
	unelapsed *sleeper
}

// advanceSleepers refreshes s so that it contains only sleepers that elapse
// after t. If s or any of its successors is a repeating sleeper,
// advanceSleepers adds repetitions of the sleeper. It returns the earlest
// sleeper in the newly refreshed list of sleepers, plus the earlest elapsed
// sleeper, in a sleeperSet.
func advanceSleepers(s *sleeper, t time.Time) sleeperSet {
	fmt.Printf("length of sleeper list s at the top of advanceSleepers: %v\n", countSleepers(s))
	ss := sleeperSet{}

	// The latest tick we have simulated for each fake ticker. We track this
	// in order to send accurate times to each fake ticker's tick channel.
	lts := make(map[*fakeTicker]time.Time)

	for r := s; r != nil; r = r.next {
		// Create a copy of the sleeper and unset its next sleeper so we
		// don't alter the original list
		p := *r
		p.next = nil

		// The sleeper hasn't elapsed yet, so don't process any
		// repetitions and continue to the next sleeper.
		if r.until.After(t) {
			fmt.Printf("adding a sleeper to the unelapsed list: %+v\n", p)
			ss.unelapsed = addSleeper(ss.unelapsed, &p)
			fmt.Printf("length of ss.unelapsed after adding a sleeper: %v\n", countSleepers(ss.unelapsed))
			continue
		}

		// We consider a sleeper elapsed if it'p "until" time is before
		// or equal to the provided time.
		ss.elapsed = addSleeper(ss.elapsed, &p)
		fmt.Printf("adding a sleeper to the elapsed list: %+v\n", p)
		fmt.Printf("length of ss.elapsed after adding a sleeper: %v\n", countSleepers(ss.elapsed))

		// TODO: Rethink the logic of processing repeating sleepers,
		// which is kind of borked right now. We always add a repeating
		// sleeper to the unelapsed list, which may not be correct.

		// We're processing a repeating sleeper, so see if there are any
		// repetitions we need to handle as well.
		if r.kind == repeatingSleeper {
			// Increment our internal map of each sleeper'r latest
			// time. This lets us assign the `until` field of each
			// sleeper accurately.
			lt, ok := lts[r.ticker]
			if ok {
				lts[r.ticker] = lt.Add(r.ticker.period)
			} else {
				lts[r.ticker] = r.until.Add(r.ticker.period)
			}
			// Simulate repeating ticker behavior by adding a new
			// repeating sleeper with an until time corresponding to
			// the next "tick".
			ss.unelapsed = addSleeper(ss.unelapsed, &sleeper{
				until:  lts[r.ticker],
				kind:   repeatingSleeper,
				ticker: r.ticker,
				notify: func(m time.Time) {
					r.ticker.c <- m
					return
				},
			})
		}
	}
	return ss
}

// Advance advances fakeClock to a new point in time, ensuring channels from any
// previous invocations of After are notified appropriately before returning
func (fc *fakeClock) Advance(d time.Duration) {
	fc.l.Lock()
	defer fc.l.Unlock()
	end := fc.time.Add(d)
	ss := advanceSleepers(fc.sleepers, end)
	fc.sleepers = ss.unelapsed

	for r := ss.elapsed; r != nil; r = r.next {
		r.notify(r.until)
	}
	fc.blockers = notifyBlockers(fc.blockers, countSleepers(fc.sleepers))
	fc.time = end
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

// countSleepers counts all sleepers in the list with head at r and returns the
// number of sleepers counted.
func countSleepers(r *sleeper) int {
	var p int
	for s := r; s != nil; s = s.next {
		p++
	}
	return p
}

// BlockUntil will block until the fakeClock has the given number of sleepers
// (callers of Sleep or After)
func (fc *fakeClock) BlockUntil(n int) {
	fc.l.Lock()
	p := countSleepers(fc.sleepers)
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
