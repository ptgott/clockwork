package clockwork

import (
	"reflect"
	"testing"
	"time"
)

func TestFakeClockAfter(t *testing.T) {
	fc := &fakeClock{}

	neg := fc.After(-1)
	select {
	case <-neg:
	default:
		t.Errorf("negative did not return!")
	}

	zero := fc.After(0)
	select {
	case <-zero:
	default:
		t.Errorf("zero did not return!")
	}
	one := fc.After(1)
	two := fc.After(2)
	six := fc.After(6)
	ten := fc.After(10)
	fc.Advance(1)
	select {
	case <-one:
	default:
		t.Errorf("one did not return!")
	}
	select {
	case <-two:
		t.Errorf("two returned prematurely!")
	case <-six:
		t.Errorf("six returned prematurely!")
	case <-ten:
		t.Errorf("ten returned prematurely!")
	default:
	}
	fc.Advance(1)
	select {
	case <-two:
	default:
		t.Errorf("two did not return!")
	}
	select {
	case <-six:
		t.Errorf("six returned prematurely!")
	case <-ten:
		t.Errorf("ten returned prematurely!")
	default:
	}
	fc.Advance(1)
	select {
	case <-six:
		t.Errorf("six returned prematurely!")
	case <-ten:
		t.Errorf("ten returned prematurely!")
	default:
	}
	fc.Advance(3)
	select {
	case <-six:
	default:
		t.Errorf("six did not return!")
	}
	select {
	case <-ten:
		t.Errorf("ten returned prematurely!")
	default:
	}
	fc.Advance(100)
	select {
	case <-ten:
	default:
		t.Errorf("ten did not return!")
	}
}

func TestNotifyBlockers(t *testing.T) {
	b1 := &blocker{1, make(chan struct{})}
	b2 := &blocker{2, make(chan struct{})}
	b3 := &blocker{5, make(chan struct{})}
	b4 := &blocker{10, make(chan struct{})}
	b5 := &blocker{10, make(chan struct{})}
	bs := []*blocker{b1, b2, b3, b4, b5}
	bs1 := notifyBlockers(bs, 2)
	if n := len(bs1); n != 3 {
		t.Fatalf("got %d blockers, want %d", n, 3)
	}
	select {
	case <-b1.ch:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for channel close!")
	}
	select {
	case <-b2.ch:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for channel close!")
	}
	bs2 := notifyBlockers(bs1, 10)
	if n := len(bs2); n != 0 {
		t.Fatalf("got %d blockers, want %d", n, 0)
	}
	select {
	case <-b3.ch:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for channel close!")
	}
	select {
	case <-b4.ch:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for channel close!")
	}
	select {
	case <-b5.ch:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for channel close!")
	}
}

func TestNewFakeClock(t *testing.T) {
	fc := NewFakeClock()
	now := fc.Now()
	if now.IsZero() {
		t.Fatalf("fakeClock.Now() fulfills IsZero")
	}

	now2 := fc.Now()
	if !reflect.DeepEqual(now, now2) {
		t.Fatalf("fakeClock.Now() returned different value: want=%#v got=%#v", now, now2)
	}
}

func TestNewFakeClockAt(t *testing.T) {
	t1 := time.Date(1999, time.February, 3, 4, 5, 6, 7, time.UTC)
	fc := NewFakeClockAt(t1)
	now := fc.Now()
	if !reflect.DeepEqual(now, t1) {
		t.Fatalf("fakeClock.Now() returned unexpected non-initialised value: want=%#v, got %#v", t1, now)
	}
}

func TestFakeClockSince(t *testing.T) {
	fc := NewFakeClock()
	now := fc.Now()
	elapsedTime := time.Second
	fc.Advance(elapsedTime)
	if fc.Since(now) != elapsedTime {
		t.Fatalf("fakeClock.Since() returned unexpected duration, got: %d, want: %d", fc.Since(now), elapsedTime)
	}
}

// This used to result in a deadlock.
// https://github.com/jonboulle/clockwork/issues/35
func TestTwoBlockersOneBlock(t *testing.T) {
	fc := &fakeClock{}

	ft1 := fc.NewTicker(time.Second)
	ft2 := fc.NewTicker(time.Second)

	fc.BlockUntil(1)
	fc.BlockUntil(2)
	ft1.Stop()
	ft2.Stop()
}

func TestAddSleeper(t *testing.T) {
	m := time.Now()
	s1 := sleeper{
		until: m.Add(10),
	}
	s2 := sleeper{
		until: m.Add(3),
	}
	s3 := sleeper{
		until: m.Add(7),
	}
	s4 := sleeper{
		until: m.Add(15),
	}
	fc := &fakeClock{}
	fc.addSleeper(&s1)
	fc.addSleeper(&s2) // Adding the earliest sleeper second
	fc.addSleeper(&s3) // We expect this one to sit somewhere in the middle
	fc.addSleeper(&s4) // Adding the latest sleeper last

	// Adding an already added sleeper should be idempotent
	fc.addSleeper(&s2)

	if fc.sleepers != &s2 {
		t.Fatal("expected the earliest sleeper to come first")
	}
	if fc.sleepers.next != &s3 {
		t.Fatal("expected the middle sleeper to come second")
	}
	if fc.sleepers.next.next != &s1 {
		t.Fatal("expected the second-latest sleeper to come next-to-last")
	}
	if fc.sleepers.next.next.next != &s4 {
		t.Fatal("expected the latest sleeper to come last")
	}

	var i int
	for s := fc.sleepers; s != nil; s = s.next {
		i++
	}

	exp := 4

	if i != exp {
		t.Fatalf("unepxected number of sleepers: got %v, wanted %v", i, exp)
	}

}
