package widget

import (
	"fmt"
	"math/bits"

	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op/clip"
)

// Decorations handles the states of window decorations.
type Decorations struct {
	// Maximized controls the look and behaviour of the maximize
	// button. It is the user's responsibility to set Maximized
	// according to the window state reported through [app.ConfigEvent].
	Maximized bool
	clicks    map[int]*Clickable
}

// LayoutMove lays out the widget that makes a window movable.
func (d *Decorations) LayoutMove(gtx layout.Context, w layout.Widget) layout.Dimensions {
	dims := w(gtx)
	defer clip.Rect{Max: dims.Size}.Push(gtx.Ops).Pop()
	system.ActionInputOp(system.ActionMove).Add(gtx.Ops)
	return dims
}

// Clickable returns the clickable for the given single action.
func (d *Decorations) Clickable(action system.Action) *Clickable {
	if bits.OnesCount(uint(action)) != 1 {
		panic(fmt.Errorf("not a single action"))
	}
	idx := bits.TrailingZeros(uint(action))
	click, found := d.clicks[idx]
	if !found {
		click = new(Clickable)
		if d.clicks == nil {
			d.clicks = make(map[int]*Clickable)
		}
		d.clicks[idx] = click
	}
	return click
}

// Update the state and return the set of actions activated by the user.
func (d *Decorations) Update(gtx layout.Context) system.Action {
	var actions system.Action
	for idx, clk := range d.clicks {
		if !clk.Clicked(gtx) {
			continue
		}
		action := system.Action(1 << idx)
		switch {
		case action == system.ActionMaximize && d.Maximized:
			action = system.ActionUnmaximize
		case action == system.ActionUnmaximize && !d.Maximized:
			action = system.ActionMaximize
		}
		actions |= action
	}
	return actions
}
