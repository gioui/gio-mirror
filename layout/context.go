// SPDX-License-Identifier: Unlicense OR MIT

package layout

import (
	"time"

	"gioui.org/io/event"
	"gioui.org/io/input"
	"gioui.org/io/system"
	"gioui.org/op"
	"gioui.org/unit"
)

// Context carries the state needed by almost all layouts and widgets.
// A zero value Context never returns events, map units to pixels
// with a scale of 1.0, and returns the zero time from Now.
type Context struct {
	// Constraints track the constraints for the active widget or
	// layout.
	Constraints Constraints

	Metric unit.Metric
	// Now is the animation time.
	Now time.Time

	// Locale provides information on the system's language preferences.
	// BUG(whereswaldon): this field is not currently populated automatically.
	// Interested users must look up and populate these values manually.
	Locale system.Locale

	disabled bool
	input.Source
	*op.Ops
}

// Dp converts v to pixels.
func (c Context) Dp(v unit.Dp) int {
	return c.Metric.Dp(v)
}

// Sp converts v to pixels.
func (c Context) Sp(v unit.Sp) int {
	return c.Metric.Sp(v)
}

func (c Context) Event(filters ...event.Filter) (event.Event, bool) {
	if c.disabled {
		return nil, false
	}
	return c.Source.Event(filters...)
}

// Enabled reports whether this context is enabled. Disabled contexts
// don't report events.
func (c Context) Enabled() bool {
	return !c.disabled
}

// Disabled returns a copy of this context that don't deliver any events.
func (c Context) Disabled() Context {
	c.disabled = true
	return c
}
