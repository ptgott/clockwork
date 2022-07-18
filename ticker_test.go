package clockwork

import (
	"testing"
	"testing/quick"
	"time"
)

func TestFakeTickerStop(t *testing.T) {
	fc := &fakeClock{}

	ft := fc.NewTicker(1)
	ft.Stop()
	select {
	case <-ft.Chan():
		t.Errorf("received unexpected tick!")
	default:
	}
}

func TestFakeTickerTick(t *testing.T) {
	fc := &fakeClock{}
	now := fc.Now()

	// The tick at now.Add(2) should not get through since we advance time by
	// two units below and the channel can hold at most one tick until it's
	// consumed.
	first := now.Add(1)
	second := now.Add(3)

	// We wrap the Advance() calls with blockers to make sure that the ticker
	// can go to sleep and produce ticks without time passing in parallel.
	ft := fc.NewTicker(1)
	fc.BlockUntil(1)
	fc.Advance(2)
	fc.BlockUntil(1)

	select {
	case tick := <-ft.Chan():
		if tick != first {
			t.Errorf("wrong tick time, got: %v, want: %v", tick, first)
		}
	case <-time.After(time.Millisecond):
		t.Errorf("expected tick!")
	}

	// Advance by one more unit, we should get another tick now.
	fc.Advance(1)
	fc.BlockUntil(1)

	select {
	case tick := <-ft.Chan():
		if tick != second {
			t.Errorf("wrong tick time, got: %v, want: %v", tick, second)
		}
	case <-time.After(time.Millisecond):
		t.Errorf("expected tick!")
	}
	ft.Stop()
}

func TestFakeTicker_Race(t *testing.T) {
	fc := NewFakeClock()

	tickTime := 1 * time.Millisecond
	ticker := fc.NewTicker(tickTime)
	defer ticker.Stop()

	fc.Advance(tickTime)

	timeout := time.NewTimer(500 * time.Millisecond)
	defer timeout.Stop()

	select {
	case <-ticker.Chan():
		// Pass
	case <-timeout.C:
		t.Fatalf("Ticker didn't detect the clock advance!")
	}
}

func TestFakeTicker_Race2(t *testing.T) {
	fc := NewFakeClock()
	ft := fc.NewTicker(5 * time.Second)
	for i := 0; i < 100; i++ {
		fc.Advance(5 * time.Second)
		<-ft.Chan()
	}
	ft.Stop()
}

// Issue 30
func TestFakeTickerMultipleTicks(t *testing.T) {

	// Simulate testing a minimal function. This simply counts the number of ticks
	// received until it receives from s, then returns the count of ticks to
	// result channel r.
	f := func(k Ticker, s chan struct{}, r chan int) {
		var i int
		for {
			select {
			case <-k.Chan():
				i++
			case <-s:
				r <- i
			}
		}
	}

	// Call the test function in a goroutine. The function receives a tick
	// every millisecond. While the function loops, advance the fake clock
	// an arbitrary number of milliseconds. The number of ticks receives
	// should be equal to the number of milliseconds we advance the clock.
	var a int
	if err := quick.Check(func(n uint) bool {
		fc := NewFakeClock()
		tk := fc.NewTicker(time.Duration(1) * time.Millisecond)
		s := make(chan struct{})
		r := make(chan int)
		go f(tk, s, r)
		fc.Advance(time.Duration(n) * time.Millisecond)
		s <- struct{}{}
		select {
		case a = <-r:
			if a != int(n) {
				return false
			}
		default:
			return false
		}
		return true
	}, &quick.Config{
		MaxCount: 1000,
	}); err != nil {
		ce := err.(*quick.CheckError)
		t.Fatalf("expected to receive %v ticks but got %v", ce.In[0], a)
	}

	// TODO: Limit the highest value of n in the quickcheck function

}
