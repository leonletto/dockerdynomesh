package main

import "time"

// debouncer collapses bursts of events into a single fire after
// d of quiet. The internal timer is created stopped (we set a long
// initial delay and the consumer never reads fire() until after a
// bump()).
type debouncer struct {
	d     time.Duration
	timer *time.Timer
}

func newDebouncer(d time.Duration) *debouncer {
	t := time.NewTimer(time.Hour)
	if !t.Stop() {
		<-t.C
	}
	return &debouncer{d: d, timer: t}
}

func (b *debouncer) bump() {
	if !b.timer.Stop() {
		select {
		case <-b.timer.C:
		default:
		}
	}
	b.timer.Reset(b.d)
}

func (b *debouncer) fire() <-chan time.Time { return b.timer.C }
func (b *debouncer) stop()                  { b.timer.Stop() }
