// SPDX-License-Identifier: Unlicense OR MIT

package layout

import (
	"image"

	"gioui.org/ui"
	"gioui.org/ui/f32"
)

type Flex struct {
	Constraints Constraints

	Axis               Axis
	MainAxisAlignment  MainAxisAlignment
	CrossAxisAlignment CrossAxisAlignment
	MainAxisSize       MainAxisSize

	taken       int
	maxCross    int
	maxBaseline int
}

type FlexChild struct {
	block ui.OpBlock
	dims  Dimens
}

type MainAxisSize uint8

type FlexMode uint8
type MainAxisAlignment uint8
type CrossAxisAlignment uint8

const (
	Loose FlexMode = iota
	Fit
)

const (
	Max MainAxisSize = iota
	Min
)

const (
	Start = 100 + iota
	End
	Center

	SpaceAround MainAxisAlignment = iota
	SpaceBetween
	SpaceEvenly

	Baseline CrossAxisAlignment = iota
	Stretch
)

func (f *Flex) Rigid(ops *ui.Ops, w Widget) FlexChild {
	mainc := axisMainConstraint(f.Axis, f.Constraints)
	mainMax := mainc.Max
	if mainc.Max != ui.Inf {
		mainMax -= f.taken
	}
	cs := axisConstraints(f.Axis, Constraint{Max: mainMax}, f.crossConstraintChild(f.Constraints))
	ops.Begin()
	ui.OpLayer{}.Add(ops)
	dims := w(ops, cs)
	block := ops.End()
	f.taken += axisMain(f.Axis, dims.Size)
	if c := axisCross(f.Axis, dims.Size); c > f.maxCross {
		f.maxCross = c
	}
	if b := dims.Baseline; b > f.maxBaseline {
		f.maxBaseline = b
	}
	return FlexChild{block, dims}
}

func (f *Flex) Flexible(ops *ui.Ops, flex float32, mode FlexMode, w Widget) FlexChild {
	mainc := axisMainConstraint(f.Axis, f.Constraints)
	var flexSize int
	if mainc.Max != ui.Inf && mainc.Max > f.taken {
		flexSize = mainc.Max - f.taken
	}
	submainc := Constraint{Max: int(float32(flexSize) * flex)}
	if mode == Fit {
		submainc.Min = submainc.Max
	}
	cs := axisConstraints(f.Axis, submainc, f.crossConstraintChild(f.Constraints))
	ops.Begin()
	ui.OpLayer{}.Add(ops)
	dims := w(ops, cs)
	block := ops.End()
	f.taken += axisMain(f.Axis, dims.Size)
	if c := axisCross(f.Axis, dims.Size); c > f.maxCross {
		f.maxCross = c
	}
	if b := dims.Baseline; b > f.maxBaseline {
		f.maxBaseline = b
	}
	return FlexChild{block, dims}
}

func (f *Flex) Layout(ops *ui.Ops, children ...FlexChild) Dimens {
	mainc := axisMainConstraint(f.Axis, f.Constraints)
	crossSize := axisCrossConstraint(f.Axis, f.Constraints).Constrain(f.maxCross)
	var space int
	if mainc.Max != ui.Inf && f.MainAxisSize == Max {
		if mainc.Max > f.taken {
			space = mainc.Max - f.taken
		}
	} else if mainc.Min > f.taken {
		space = mainc.Min - f.taken
	}
	var mainSize int
	var baseline int
	switch f.MainAxisAlignment {
	case Center:
		mainSize += space / 2
	case End:
		mainSize += space
	case SpaceEvenly:
		mainSize += space / (1 + len(children))
	case SpaceAround:
		mainSize += space / (len(children) * 2)
	}
	for _, child := range children {
		dims := child.dims
		b := dims.Baseline
		var cross int
		switch f.CrossAxisAlignment {
		case End:
			cross = crossSize - axisCross(f.Axis, dims.Size)
		case Center:
			cross = (crossSize - axisCross(f.Axis, dims.Size)) / 2
		case Baseline:
			if f.Axis == Horizontal {
				cross = f.maxBaseline - b
			}
		}
		ops.Begin()
		ui.OpTransform{
			Transform: ui.Offset(toPointF(axisPoint(f.Axis, mainSize, cross))),
		}.Add(ops)
		child.block.Add(ops)
		ops.End().Add(ops)
		mainSize += axisMain(f.Axis, dims.Size)
		switch f.MainAxisAlignment {
		case SpaceEvenly:
			mainSize += space / (1 + len(children))
		case SpaceAround:
			mainSize += space / len(children)
		case SpaceBetween:
			mainSize += space / (len(children) - 1)
		}
		if b != dims.Size.Y {
			baseline = b
		}
	}
	switch f.MainAxisAlignment {
	case Start:
		mainSize += space
	case SpaceEvenly:
		mainSize += space / (1 + len(children))
	case SpaceAround:
		mainSize += space / (len(children) * 2)
	}
	sz := axisPoint(f.Axis, mainSize, crossSize)
	if baseline == 0 {
		baseline = sz.Y
	}
	return Dimens{Size: sz, Baseline: baseline}
}

func axisPoint(a Axis, main, cross int) image.Point {
	if a == Horizontal {
		return image.Point{main, cross}
	} else {
		return image.Point{cross, main}
	}
}

func axisMain(a Axis, sz image.Point) int {
	if a == Horizontal {
		return sz.X
	} else {
		return sz.Y
	}
}

func axisCross(a Axis, sz image.Point) int {
	if a == Horizontal {
		return sz.Y
	} else {
		return sz.X
	}
}

func axisMainConstraint(a Axis, cs Constraints) Constraint {
	if a == Horizontal {
		return cs.Width
	} else {
		return cs.Height
	}
}

func axisCrossConstraint(a Axis, cs Constraints) Constraint {
	if a == Horizontal {
		return cs.Height
	} else {
		return cs.Width
	}
}

func (f *Flex) crossConstraintChild(cs Constraints) Constraint {
	c := axisCrossConstraint(f.Axis, cs)
	switch f.CrossAxisAlignment {
	case Stretch:
		c.Min = c.Max
	default:
		c.Min = 0
	}
	return c
}

func axisConstraints(a Axis, mainc, crossc Constraint) Constraints {
	if a == Horizontal {
		return Constraints{mainc, crossc}
	} else {
		return Constraints{crossc, mainc}
	}
}

func toPointF(p image.Point) f32.Point {
	return f32.Point{X: float32(p.X), Y: float32(p.Y)}
}
