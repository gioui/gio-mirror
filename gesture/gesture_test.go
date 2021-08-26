package gesture

import (
	"testing"
	"time"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/io/router"
	"gioui.org/op"
)

func TestMouseClicks(t *testing.T) {
	for _, tc := range []struct {
		label  string
		events []event.Event
		clicks []int // number of combined clicks per click (single, double...)
	}{
		{
			label:  "single click",
			events: mouseClickEvents(200 * time.Millisecond),
			clicks: []int{1},
		},
		{
			label: "double click",
			events: mouseClickEvents(
				100*time.Millisecond,
				100*time.Millisecond+doubleClickDuration-1),
			clicks: []int{1, 2},
		},
		{
			label: "two single clicks",
			events: mouseClickEvents(
				100*time.Millisecond,
				100*time.Millisecond+doubleClickDuration+1),
			clicks: []int{1, 1},
		},
		{
			label: "left and right clicks mixed",
			events: mouseMultiButtonClickEvents(
				100*time.Millisecond,
				100*time.Millisecond+doubleClickDuration*1+1,
				100*time.Millisecond+doubleClickDuration*2+1,
				100*time.Millisecond+doubleClickDuration*3+1,
				100*time.Millisecond+doubleClickDuration*4+1,
				100*time.Millisecond+doubleClickDuration*5+1,
				100*time.Millisecond+doubleClickDuration*6+1,
				100*time.Millisecond+doubleClickDuration*7+1,
				100*time.Millisecond+doubleClickDuration*8+1,
			),
			clicks: []int{1, 1, 1, 1, 1, 1, 1, 1, 1},
		},
	} {
		t.Run(tc.label, func(t *testing.T) {
			var click Click
			var ops op.Ops
			click.Add(&ops)

			var r router.Router
			r.Frame(&ops)
			r.Queue(tc.events...)

			events := click.Events(&r)
			clicks := filterMouseClicks(events)
			if got, want := len(clicks), len(tc.clicks); got != want {
				t.Fatalf("got %d mouse clicks, expected %d", got, want)
			}

			for i, click := range clicks {
				if got, want := click.NumClicks, tc.clicks[i]; got != want {
					t.Errorf("got %d combined mouse clicks, expected %d", got, want)
				}
			}
		})
	}
}

func mouseClickEvents(times ...time.Duration) []event.Event {
	press := pointer.Event{
		Type:    pointer.Press,
		Source:  pointer.Mouse,
		Buttons: pointer.ButtonPrimary,
	}
	events := make([]event.Event, 0, 2*len(times))
	for _, t := range times {
		release := press
		release.Type = pointer.Release
		release.Time = t
		events = append(events, press, release)
	}
	return events
}

func filterMouseClicks(events []ClickEvent) []ClickEvent {
	var clicks []ClickEvent
	for _, ev := range events {
		if ev.Type == TypeClick {
			clicks = append(clicks, ev)
		}
	}
	return clicks
}

func mouseMultiButtonClickEvents(times ...time.Duration) []event.Event {
	events := make([]event.Event, 0)
	numSecondaryClick := 0
	numPrimaryClick := 0
	for i, _ := range times {
		if i%2 == 0 {
			press := pointer.Event{
				Type:    pointer.Press,
				Source:  pointer.Mouse,
				Buttons: pointer.ButtonPrimary,
			}
			numPrimaryClick++
			events = append(events, press)
		} else {
			press := pointer.Event{
				Type:    pointer.Press,
				Source:  pointer.Mouse,
				Buttons: pointer.ButtonSecondary,
			}
			numSecondaryClick++
			events = append(events, press)
		}
	}
	i := 0
	for ; i < numPrimaryClick; i++ {
		release := pointer.Event{
			Type:    pointer.Release,
			Source:  pointer.Mouse,
			Buttons: pointer.ButtonPrimary,
			Time:    times[i],
		}
		events = append(events, release)
	}
	for ; i < numSecondaryClick+numPrimaryClick; i++ {
		release := pointer.Event{
			Type:    pointer.Release,
			Source:  pointer.Mouse,
			Buttons: pointer.ButtonSecondary,
			Time:    times[i],
		}

		events = append(events, release)
	}
	return events
}
