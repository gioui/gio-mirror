// SPDX-License-Identifier: Unlicense OR MIT

package input

import (
	"image"
	"io"
	"strings"
	"time"

	"gioui.org/f32"
	f32internal "gioui.org/internal/f32"
	"gioui.org/internal/ops"
	"gioui.org/io/clipboard"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/semantic"
	"gioui.org/io/system"
	"gioui.org/io/transfer"
	"gioui.org/op"
)

// Router tracks the [io/event.Tag] identifiers of user interface widgets
// and routes events to them. [Source] is its interface exposed to widgets.
type Router struct {
	savedTrans []f32.Affine2D
	transStack []f32.Affine2D
	handlers   map[event.Tag]*handler
	pointer    struct {
		queue     pointerQueue
		collector pointerCollector
	}
	key struct {
		queue keyQueue
		// The following fields have the same purpose as the fields in
		// type handler, but for key.Events.
		filter        keyFilter
		nextFilter    keyFilter
		scratchFilter keyFilter
	}
	cqueue clipboardQueue
	// states is the list of pending state changes resulting from
	// incoming events. The first element, if present, contains the state
	// and events for the current frame.
	changes []stateChange
	reader  ops.Reader
	// InvalidateCmd summary.
	wakeup     bool
	wakeupTime time.Time
	// Changes queued for next call to Frame.
	commands []Command
	// transfers is the pending transfer.DataEvent.Open functions.
	transfers []io.ReadCloser
	// deferring is set if command execution and event delivery is deferred
	// to the next frame.
	deferring bool
	// scratchFilters is for garbage-free construction of ephemeral filters.
	scratchFilters []taggedFilter
}

// Source implements the interface between a Router and user interface widgets.
// The zero-value Source is disabled.
type Source struct {
	r *Router
}

// Command represents a request such as moving the focus, or initiating a clipboard read.
// Commands are queued by calling [Source.Queue].
type Command interface {
	ImplementsCommand()
}

// SemanticNode represents a node in the tree describing the components
// contained in a frame.
type SemanticNode struct {
	ID       SemanticID
	ParentID SemanticID
	Children []SemanticNode
	Desc     SemanticDesc

	areaIdx int
}

// SemanticDesc provides a semantic description of a UI component.
type SemanticDesc struct {
	Class       semantic.ClassOp
	Description string
	Label       string
	Selected    bool
	Disabled    bool
	Gestures    SemanticGestures
	Bounds      image.Rectangle
}

// SemanticGestures is a bit-set of supported gestures.
type SemanticGestures int

const (
	ClickGesture SemanticGestures = 1 << iota
	ScrollGesture
)

// SemanticID uniquely identifies a SemanticDescription.
//
// By convention, the zero value denotes the non-existent ID.
type SemanticID uint

// SystemEvent is a marker for events that have platform specific
// side-effects. SystemEvents are never matched by catch-all filters.
type SystemEvent struct {
	Event event.Event
}

// handler contains the per-handler state tracked by a [Router].
type handler struct {
	// active tracks whether the handler was active in the current
	// frame. Router deletes state belonging to inactive handlers during Frame.
	active  bool
	pointer pointerHandler
	key     keyHandler
	// filter the handler has asked for through event handling
	// in the previous frame. It is used for routing events in the
	// current frame.
	filter filter
	// prevFilter is the filter being built in the current frame.
	nextFilter filter
	// processedFilter is the filters that have exhausted available events.
	processedFilter filter
}

// filter is the union of a set of [io/event.Filters].
type filter struct {
	pointer   pointerFilter
	focusable bool
}

// taggedFilter is a filter for a particular tag.
type taggedFilter struct {
	tag    event.Tag
	filter filter
}

// stateChange represents the new state and outgoing events
// resulting from an incoming event.
type stateChange struct {
	// event, if set, is the trigger for the change.
	event  event.Event
	state  inputState
	events []taggedEvent
}

// inputState represent a immutable snapshot of the state required
// to route events.
type inputState struct {
	clipboardState
	keyState
	pointerState
}

// taggedEvent represents an event and its target handler.
type taggedEvent struct {
	event event.Event
	tag   event.Tag
}

// Source returns a Source backed by this Router.
func (q *Router) Source() Source {
	return Source{r: q}
}

// Execute a command.
func (s Source) Execute(c Command) {
	if !s.enabled() {
		return
	}
	s.r.execute(c)
}

// enabled reports whether the source is enabled. Only enabled
// Sources deliver events and respond to commands.
func (s Source) enabled() bool {
	return s.r != nil
}

// Focused reports whether tag is focused, according to the most recent
// [key.FocusEvent] delivered.
func (s Source) Focused(tag event.Tag) bool {
	if !s.enabled() {
		return false
	}
	return s.r.state().keyState.focus == tag
}

// Event returns the next event that matches at least one of filters.
func (s Source) Event(filters ...event.Filter) (event.Event, bool) {
	if !s.enabled() {
		return nil, false
	}
	return s.r.Event(filters...)
}

func (q *Router) Event(filters ...event.Filter) (event.Event, bool) {
	// Merge filters into scratch filters.
	q.scratchFilters = q.scratchFilters[:0]
	q.key.scratchFilter = q.key.scratchFilter[:0]
	for _, f := range filters {
		var t event.Tag
		switch f := f.(type) {
		case key.Filter:
			q.key.scratchFilter = append(q.key.scratchFilter, f)
			continue
		case transfer.SourceFilter:
			t = f.Target
		case transfer.TargetFilter:
			t = f.Target
		case key.FocusFilter:
			t = f.Target
		case pointer.Filter:
			t = f.Target
		}
		if t == nil {
			continue
		}
		var filter *filter
		for i := range q.scratchFilters {
			s := &q.scratchFilters[i]
			if s.tag == t {
				filter = &s.filter
				break
			}
		}
		if filter == nil {
			n := len(q.scratchFilters)
			if n < cap(q.scratchFilters) {
				// Re-use previously allocated filter.
				q.scratchFilters = q.scratchFilters[:n+1]
				tf := &q.scratchFilters[n]
				tf.tag = t
				filter = &tf.filter
				filter.Reset()
			} else {
				q.scratchFilters = append(q.scratchFilters, taggedFilter{tag: t})
				filter = &q.scratchFilters[n].filter
			}
		}
		filter.Add(f)
	}
	for _, tf := range q.scratchFilters {
		h := q.stateFor(tf.tag)
		h.filter.Merge(tf.filter)
		h.nextFilter.Merge(tf.filter)
	}
	q.key.filter = append(q.key.filter, q.key.scratchFilter...)
	q.key.nextFilter = append(q.key.nextFilter, q.key.scratchFilter...)
	// Deliver reset event, if any.
	for _, f := range filters {
		switch f := f.(type) {
		case key.FocusFilter:
			if f.Target == nil {
				break
			}
			h := q.stateFor(f.Target)
			if reset, ok := h.key.ResetEvent(); ok {
				return reset, true
			}
		case pointer.Filter:
			if f.Target == nil {
				break
			}
			h := q.stateFor(f.Target)
			if reset, ok := h.pointer.ResetEvent(); ok && h.filter.pointer.Matches(reset) {
				return reset, true
			}
		}
	}
	for i := range q.changes {
		if q.deferring && i > 0 {
			break
		}
		change := &q.changes[i]
		for j, evt := range change.events {
			match := false
			switch e := evt.event.(type) {
			case key.Event:
				match = q.key.scratchFilter.Matches(change.state.keyState.focus, e, false)
			default:
				for _, tf := range q.scratchFilters {
					if evt.tag == tf.tag && tf.filter.Matches(evt.event) {
						match = true
						break
					}
				}
			}
			if match {
				change.events = append(change.events[:j], change.events[j+1:]...)
				// Fast forward state to last matched.
				q.collapseState(i)
				return evt.event, true
			}
		}
	}
	for _, tf := range q.scratchFilters {
		h := q.stateFor(tf.tag)
		h.processedFilter.Merge(tf.filter)
	}
	return nil, false
}

// collapseState in the interval [1;idx] into q.changes[0].
func (q *Router) collapseState(idx int) {
	if idx == 0 {
		return
	}
	first := &q.changes[0]
	first.state = q.changes[idx].state
	for _, ch := range q.changes[1 : idx+1] {
		first.events = append(first.events, ch.events...)
	}
	q.changes = append(q.changes[:1], q.changes[idx+1:]...)
}

// Frame completes the current frame and starts a new with the
// handlers from the frame argument. Remaining events are discarded,
// unless they were deferred by a command.
func (q *Router) Frame(frame *op.Ops) {
	var remaining []event.Event
	if n := len(q.changes); n > 0 {
		if q.deferring {
			// Collect events for replay.
			for _, ch := range q.changes[1:] {
				remaining = append(remaining, ch.event)
			}
			q.changes = append(q.changes[:0], stateChange{state: q.changes[0].state})
		} else {
			// Collapse state.
			state := q.changes[n-1].state
			q.changes = append(q.changes[:0], stateChange{state: state})
		}
	}
	for _, rc := range q.transfers {
		if rc != nil {
			rc.Close()
		}
	}
	q.transfers = nil
	q.deferring = false
	for _, h := range q.handlers {
		h.filter, h.nextFilter = h.nextFilter, h.filter
		h.nextFilter.Reset()
		h.processedFilter.Reset()
		h.pointer.Reset()
		h.key.Reset()
	}
	q.key.filter, q.key.nextFilter = q.key.nextFilter, q.key.filter
	q.key.nextFilter = q.key.nextFilter[:0]
	var ops *ops.Ops
	if frame != nil {
		ops = &frame.Internal
	}
	q.reader.Reset(ops)
	q.collect()
	for k, h := range q.handlers {
		if !h.active {
			delete(q.handlers, k)
		} else {
			h.active = false
		}
	}
	q.executeCommands()
	q.Queue(remaining...)
	st := q.lastState()
	pst, evts := q.pointer.queue.Frame(q.handlers, st.pointerState)
	st.pointerState = pst
	st.keyState = q.key.queue.Frame(q.handlers, q.lastState().keyState)
	q.changeState(nil, st, evts)

	// Collapse state and events.
	q.collapseState(len(q.changes) - 1)
}

// Queue events to be routed.
func (q *Router) Queue(events ...event.Event) {
	for _, e := range events {
		se, system := e.(SystemEvent)
		if system {
			e = se.Event
		}
		q.processEvent(e, system)
	}
}

func (f *filter) Add(flt event.Filter) {
	switch flt := flt.(type) {
	case key.FocusFilter:
		f.focusable = true
	case pointer.Filter:
		f.pointer.Add(flt)
	case transfer.SourceFilter, transfer.TargetFilter:
		f.pointer.Add(flt)
	}
}

// Merge f2 into f.
func (f *filter) Merge(f2 filter) {
	f.focusable = f.focusable || f2.focusable
	f.pointer.Merge(f2.pointer)
}

func (f *filter) Matches(e event.Event) bool {
	switch e.(type) {
	case key.FocusEvent, key.SnippetEvent, key.EditEvent, key.SelectionEvent:
		return f.focusable
	default:
		return f.pointer.Matches(e)
	}
}

func (f *filter) Reset() {
	*f = filter{
		pointer: pointerFilter{
			sourceMimes: f.pointer.sourceMimes[:0],
			targetMimes: f.pointer.targetMimes[:0],
		},
	}
}

func (q *Router) processEvent(e event.Event, system bool) {
	state := q.lastState()
	switch e := e.(type) {
	case pointer.Event:
		pstate, evts := q.pointer.queue.Push(q.handlers, state.pointerState, e)
		state.pointerState = pstate
		q.changeState(e, state, evts)
	case key.Event:
		var evts []taggedEvent
		if q.key.filter.Matches(state.keyState.focus, e, system) {
			evts = append(evts, taggedEvent{event: e})
		}
		q.changeState(e, state, evts)
	case key.SnippetEvent:
		// Expand existing, overlapping snippet.
		if r := state.content.Snippet.Range; rangeOverlaps(r, key.Range(e)) {
			if e.Start > r.Start {
				e.Start = r.Start
			}
			if e.End < r.End {
				e.End = r.End
			}
		}
		var evts []taggedEvent
		if f := state.focus; f != nil {
			evts = append(evts, taggedEvent{tag: f, event: e})
		}
		q.changeState(e, state, evts)
	case key.EditEvent, key.FocusEvent, key.SelectionEvent:
		var evts []taggedEvent
		if f := state.focus; f != nil {
			evts = append(evts, taggedEvent{tag: f, event: e})
		}
		q.changeState(e, state, evts)
	case transfer.DataEvent:
		cstate, evts := q.cqueue.Push(state.clipboardState, e)
		state.clipboardState = cstate
		q.changeState(e, state, evts)
	default:
		panic("unknown event type")
	}
}

func (q *Router) execute(c Command) {
	// The command can be executed immediately if event delivery is not frozen, and
	// no event receiver has completed their event handling.
	if !q.deferring {
		ch := q.executeCommand(c)
		immediate := true
		for _, e := range ch.events {
			h, ok := q.handlers[e.tag]
			immediate = immediate && (!ok || !h.processedFilter.Matches(e.event))
		}
		if immediate {
			// Hold on to the remaining events for state replay.
			var evts []event.Event
			for _, ch := range q.changes {
				if ch.event != nil {
					evts = append(evts, ch.event)
				}
			}
			if len(q.changes) > 1 {
				q.changes = q.changes[:1]
			}
			q.changeState(nil, ch.state, ch.events)
			q.Queue(evts...)
			return
		}
	}
	q.deferring = true
	q.commands = append(q.commands, c)
}

func (q *Router) state() inputState {
	if len(q.changes) > 0 {
		return q.changes[0].state
	}
	return inputState{}
}

func (q *Router) lastState() inputState {
	if n := len(q.changes); n > 0 {
		return q.changes[n-1].state
	}
	return inputState{}
}

func (q *Router) executeCommands() {
	for _, c := range q.commands {
		ch := q.executeCommand(c)
		q.changeState(nil, ch.state, ch.events)
	}
	q.commands = nil
}

// executeCommand the command and return the resulting state change along with the
// tag the state change depended on, if any.
func (q *Router) executeCommand(c Command) stateChange {
	state := q.state()
	var evts []taggedEvent
	switch req := c.(type) {
	case key.SelectionCmd:
		state.keyState = q.key.queue.setSelection(state.keyState, req)
	case key.FocusCmd:
		state.keyState, evts = q.key.queue.Focus(q.handlers, state.keyState, req.Tag)
	case key.SoftKeyboardCmd:
		state.keyState = state.keyState.softKeyboard(req.Show)
	case key.SnippetCmd:
		state.keyState = q.key.queue.setSnippet(state.keyState, req)
	case transfer.OfferCmd:
		state.pointerState, evts = q.pointer.queue.offerData(q.handlers, state.pointerState, req)
	case clipboard.WriteCmd:
		q.cqueue.ProcessWriteClipboard(req)
	case clipboard.ReadCmd:
		state.clipboardState = q.cqueue.ProcessReadClipboard(state.clipboardState, req.Tag)
	case pointer.GrabCmd:
		state.pointerState, evts = q.pointer.queue.grab(state.pointerState, req)
	case op.InvalidateCmd:
		if !q.wakeup || req.At.Before(q.wakeupTime) {
			q.wakeup = true
			q.wakeupTime = req.At
		}
	}
	return stateChange{state: state, events: evts}
}

func (q *Router) changeState(e event.Event, state inputState, evts []taggedEvent) {
	// Wrap pointer.DataEvent.Open functions to detect them not being called.
	for i := range evts {
		e := &evts[i]
		if de, ok := e.event.(transfer.DataEvent); ok {
			transferIdx := len(q.transfers)
			data := de.Open()
			q.transfers = append(q.transfers, data)
			de.Open = func() io.ReadCloser {
				q.transfers[transferIdx] = nil
				return data
			}
			e.event = de
		}
	}
	// Initialize the first change to contain the current state
	// and events that are bound for the current frame.
	if len(q.changes) == 0 {
		q.changes = append(q.changes, stateChange{})
	}
	if e != nil && len(evts) > 0 {
		// An event triggered events bound for user receivers. Add a state change to be
		// able to redo the change in case of a command execution.
		q.changes = append(q.changes, stateChange{event: e, state: state, events: evts})
	} else {
		// Otherwise, merge with previous change.
		prev := &q.changes[len(q.changes)-1]
		prev.state = state
		prev.events = append(prev.events, evts...)
	}
}

func rangeOverlaps(r1, r2 key.Range) bool {
	r1 = rangeNorm(r1)
	r2 = rangeNorm(r2)
	return r1.Start <= r2.Start && r2.Start < r1.End ||
		r1.Start <= r2.End && r2.End < r1.End
}

func rangeNorm(r key.Range) key.Range {
	if r.End < r.Start {
		r.End, r.Start = r.Start, r.End
	}
	return r
}

func (q *Router) MoveFocus(dir key.FocusDirection) {
	state := q.lastState()
	kstate, evts := q.key.queue.MoveFocus(q.handlers, state.keyState, dir)
	state.keyState = kstate
	q.changeState(nil, state, evts)
}

// RevealFocus scrolls the current focus (if any) into viewport
// if there are scrollable parent handlers.
func (q *Router) RevealFocus(viewport image.Rectangle) {
	state := q.lastState()
	focus := state.focus
	if focus == nil {
		return
	}
	kh := &q.handlers[focus].key
	bounds := q.key.queue.BoundsFor(kh)
	area := q.key.queue.AreaFor(kh)
	viewport = q.pointer.queue.ClipFor(area, viewport)

	topleft := bounds.Min.Sub(viewport.Min)
	topleft = max(topleft, bounds.Max.Sub(viewport.Max))
	topleft = min(image.Pt(0, 0), topleft)
	bottomright := bounds.Max.Sub(viewport.Max)
	bottomright = min(bottomright, bounds.Min.Sub(viewport.Min))
	bottomright = max(image.Pt(0, 0), bottomright)
	s := topleft
	if s.X == 0 {
		s.X = bottomright.X
	}
	if s.Y == 0 {
		s.Y = bottomright.Y
	}
	q.ScrollFocus(s)
}

// ScrollFocus scrolls the focused widget, if any, by dist.
func (q *Router) ScrollFocus(dist image.Point) {
	state := q.lastState()
	focus := state.focus
	if focus == nil {
		return
	}
	kh := &q.handlers[focus].key
	area := q.key.queue.AreaFor(kh)
	q.changeState(nil, q.lastState(), q.pointer.queue.Deliver(q.handlers, area, pointer.Event{
		Kind:   pointer.Scroll,
		Source: pointer.Touch,
		Scroll: f32internal.FPt(dist),
	}))
}

func max(p1, p2 image.Point) image.Point {
	m := p1
	if p2.X > m.X {
		m.X = p2.X
	}
	if p2.Y > m.Y {
		m.Y = p2.Y
	}
	return m
}

func min(p1, p2 image.Point) image.Point {
	m := p1
	if p2.X < m.X {
		m.X = p2.X
	}
	if p2.Y < m.Y {
		m.Y = p2.Y
	}
	return m
}

func (q *Router) ActionAt(p f32.Point) (system.Action, bool) {
	return q.pointer.queue.ActionAt(p)
}

func (q *Router) ClickFocus() {
	focus := q.lastState().focus
	if focus == nil {
		return
	}
	kh := &q.handlers[focus].key
	bounds := q.key.queue.BoundsFor(kh)
	center := bounds.Max.Add(bounds.Min).Div(2)
	e := pointer.Event{
		Position: f32.Pt(float32(center.X), float32(center.Y)),
		Source:   pointer.Touch,
	}
	area := q.key.queue.AreaFor(kh)
	e.Kind = pointer.Press
	state := q.lastState()
	q.changeState(nil, state, q.pointer.queue.Deliver(q.handlers, area, e))
	e.Kind = pointer.Release
	q.changeState(nil, state, q.pointer.queue.Deliver(q.handlers, area, e))
}

// TextInputState returns the input state from the most recent
// call to Frame.
func (q *Router) TextInputState() TextInputState {
	state := q.state()
	kstate, s := state.InputState()
	state.keyState = kstate
	q.changeState(nil, state, nil)
	return s
}

// TextInputHint returns the input mode from the most recent key.InputOp.
func (q *Router) TextInputHint() (key.InputHint, bool) {
	return q.key.queue.InputHint(q.handlers, q.state().keyState)
}

// WriteClipboard returns the most recent content to be copied
// to the clipboard, if any.
func (q *Router) WriteClipboard() (mime string, content []byte, ok bool) {
	return q.cqueue.WriteClipboard()
}

// ClipboardRequested reports if any new handler is waiting
// to read the clipboard.
func (q *Router) ClipboardRequested() bool {
	return q.cqueue.ClipboardRequested(q.lastState().clipboardState)
}

// Cursor returns the last cursor set.
func (q *Router) Cursor() pointer.Cursor {
	return q.state().cursor
}

// SemanticAt returns the first semantic description under pos, if any.
func (q *Router) SemanticAt(pos f32.Point) (SemanticID, bool) {
	return q.pointer.queue.SemanticAt(pos)
}

// AppendSemantics appends the semantic tree to nodes, and returns the result.
// The root node is the first added.
func (q *Router) AppendSemantics(nodes []SemanticNode) []SemanticNode {
	q.pointer.collector.q = &q.pointer.queue
	q.pointer.collector.ensureRoot()
	return q.pointer.queue.AppendSemantics(nodes)
}

// EditorState returns the editor state for the focused handler, or the
// zero value if there is none.
func (q *Router) EditorState() EditorState {
	return q.key.queue.editorState(q.handlers, q.state().keyState)
}

func (q *Router) stateFor(tag event.Tag) *handler {
	if tag == nil {
		panic("internal error: nil tag")
	}
	s, ok := q.handlers[tag]
	if !ok {
		s = new(handler)
		if q.handlers == nil {
			q.handlers = make(map[event.Tag]*handler)
		}
		q.handlers[tag] = s
	}
	s.active = true
	return s
}

func (q *Router) collect() {
	q.transStack = q.transStack[:0]
	pc := &q.pointer.collector
	pc.q = &q.pointer.queue
	pc.Reset()
	kq := &q.key.queue
	q.key.queue.Reset()
	var t f32.Affine2D
	for encOp, ok := q.reader.Decode(); ok; encOp, ok = q.reader.Decode() {
		switch ops.OpType(encOp.Data[0]) {
		case ops.TypeSave:
			id := ops.DecodeSave(encOp.Data)
			if extra := id - len(q.savedTrans) + 1; extra > 0 {
				q.savedTrans = append(q.savedTrans, make([]f32.Affine2D, extra)...)
			}
			q.savedTrans[id] = t
		case ops.TypeLoad:
			id := ops.DecodeLoad(encOp.Data)
			t = q.savedTrans[id]
			pc.resetState()
			pc.setTrans(t)

		case ops.TypeClip:
			var op ops.ClipOp
			op.Decode(encOp.Data)
			pc.clip(op)
		case ops.TypePopClip:
			pc.popArea()
		case ops.TypeTransform:
			t2, push := ops.DecodeTransform(encOp.Data)
			if push {
				q.transStack = append(q.transStack, t)
			}
			t = t.Mul(t2)
			pc.setTrans(t)
		case ops.TypePopTransform:
			n := len(q.transStack)
			t = q.transStack[n-1]
			q.transStack = q.transStack[:n-1]
			pc.setTrans(t)

		case ops.TypeInput:
			tag := encOp.Refs[0].(event.Tag)
			s := q.stateFor(tag)
			pc.inputOp(tag, &s.pointer)
			a := pc.currentArea()
			b := pc.currentAreaBounds()
			if s.filter.focusable {
				kq.inputOp(tag, &s.key, t, a, b)
			}

		// Pointer ops.
		case ops.TypePass:
			pc.pass()
		case ops.TypePopPass:
			pc.popPass()
		case ops.TypeCursor:
			name := pointer.Cursor(encOp.Data[1])
			pc.cursor(name)
		case ops.TypeActionInput:
			act := system.Action(encOp.Data[1])
			pc.actionInputOp(act)
		case ops.TypeKeyInputHint:
			op := key.InputHintOp{
				Tag:  encOp.Refs[0].(event.Tag),
				Hint: key.InputHint(encOp.Data[1]),
			}
			s := q.stateFor(op.Tag)
			s.key.inputHint(op.Hint)

		// Semantic ops.
		case ops.TypeSemanticLabel:
			lbl := *encOp.Refs[0].(*string)
			pc.semanticLabel(lbl)
		case ops.TypeSemanticDesc:
			desc := *encOp.Refs[0].(*string)
			pc.semanticDesc(desc)
		case ops.TypeSemanticClass:
			class := semantic.ClassOp(encOp.Data[1])
			pc.semanticClass(class)
		case ops.TypeSemanticSelected:
			if encOp.Data[1] != 0 {
				pc.semanticSelected(true)
			} else {
				pc.semanticSelected(false)
			}
		case ops.TypeSemanticEnabled:
			if encOp.Data[1] != 0 {
				pc.semanticEnabled(true)
			} else {
				pc.semanticEnabled(false)
			}
		}
	}
}

// WakeupTime returns the most recent time for doing another frame,
// as determined from the last call to Frame.
func (q *Router) WakeupTime() (time.Time, bool) {
	t, w := q.wakeupTime, q.wakeup
	q.wakeup = false
	// Pending events always trigger wakeups.
	if len(q.changes) > 1 || len(q.changes) == 1 && len(q.changes[0].events) > 0 {
		t, w = time.Time{}, true
	}
	return t, w
}

func (s SemanticGestures) String() string {
	var gestures []string
	if s&ClickGesture != 0 {
		gestures = append(gestures, "Click")
	}
	return strings.Join(gestures, ",")
}

func (SystemEvent) ImplementsEvent() {}
