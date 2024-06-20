// SPDX-License-Identifier: Unlicense OR MIT

package input

import (
	"image"
	"io"

	"gioui.org/f32"
	f32internal "gioui.org/internal/f32"
	"gioui.org/internal/ops"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/io/semantic"
	"gioui.org/io/system"
	"gioui.org/io/transfer"
)

type pointerQueue struct {
	hitTree []hitNode
	areas   []areaNode

	semantic struct {
		idsAssigned bool
		lastID      SemanticID
		// contentIDs maps semantic content to a list of semantic IDs
		// previously assigned. It is used to maintain stable IDs across
		// frames.
		contentIDs map[semanticContent][]semanticID
	}
}

type hitNode struct {
	next int
	area int

	// For handler nodes.
	tag  event.Tag
	pass bool
}

// pointerState is the input state related to pointer events.
type pointerState struct {
	cursor   pointer.Cursor
	pointers []pointerInfo
}

type pointerInfo struct {
	id       pointer.ID
	pressed  bool
	handlers []event.Tag
	// last tracks the last pointer event received,
	// used while processing frame events.
	last pointer.Event

	// entered tracks the tags that contain the pointer.
	entered []event.Tag

	dataSource event.Tag // dragging source tag
	dataTarget event.Tag // dragging target tag
}

type pointerHandler struct {
	// areaPlusOne is the index into the list of pointerQueue.areas, plus 1.
	areaPlusOne int
	// setup tracks whether the handler has received
	// the pointer.Cancel event that resets its state.
	setup bool
}

// pointerFilter represents the union of a set of pointer filters.
type pointerFilter struct {
	kinds pointer.Kind
	// min and max horizontal/vertical scroll
	scrollX, scrollY pointer.ScrollRange

	sourceMimes []string
	targetMimes []string
	scheme      []string
}

type areaOp struct {
	kind areaKind
	rect image.Rectangle
}

type areaNode struct {
	trans f32.Affine2D
	area  areaOp

	cursor pointer.Cursor

	// Tree indices, with -1 being the sentinel.
	parent     int
	firstChild int
	lastChild  int
	sibling    int

	semantic struct {
		valid   bool
		id      SemanticID
		content semanticContent
	}
	action system.Action
}

type areaKind uint8

// collectState represents the state for pointerCollector.
type collectState struct {
	t f32.Affine2D
	// nodePlusOne is the current node index, plus one to
	// make the zero value collectState the initial state.
	nodePlusOne int
	pass        int
}

// pointerCollector tracks the state needed to update an pointerQueue
// from pointer ops.
type pointerCollector struct {
	q         *pointerQueue
	state     collectState
	nodeStack []int
}

type semanticContent struct {
	tag      event.Tag
	label    string
	desc     string
	class    semantic.ClassOp
	gestures SemanticGestures
	selected bool
	disabled bool
}

type semanticID struct {
	id   SemanticID
	used bool
}

const (
	areaRect areaKind = iota
	areaEllipse
)

func (c *pointerCollector) resetState() {
	c.state = collectState{}
	c.nodeStack = c.nodeStack[:0]
	// Pop every node except the root.
	if len(c.q.hitTree) > 0 {
		c.state.nodePlusOne = 0 + 1
	}
}

func (c *pointerCollector) setTrans(t f32.Affine2D) {
	c.state.t = t
}

func (c *pointerCollector) clip(op ops.ClipOp) {
	kind := areaRect
	if op.Shape == ops.Ellipse {
		kind = areaEllipse
	}
	c.pushArea(kind, op.Bounds)
}

func (c *pointerCollector) pushArea(kind areaKind, bounds image.Rectangle) {
	parentID := c.currentArea()
	areaID := len(c.q.areas)
	areaOp := areaOp{kind: kind, rect: bounds}
	if parentID != -1 {
		parent := &c.q.areas[parentID]
		if parent.firstChild == -1 {
			parent.firstChild = areaID
		}
		if siblingID := parent.lastChild; siblingID != -1 {
			c.q.areas[siblingID].sibling = areaID
		}
		parent.lastChild = areaID
	}
	an := areaNode{
		trans:      c.state.t,
		area:       areaOp,
		parent:     parentID,
		sibling:    -1,
		firstChild: -1,
		lastChild:  -1,
	}

	c.q.areas = append(c.q.areas, an)
	c.nodeStack = append(c.nodeStack, c.state.nodePlusOne-1)
	c.addHitNode(hitNode{
		area: areaID,
		pass: true,
	})
}

func (c *pointerCollector) popArea() {
	n := len(c.nodeStack)
	c.state.nodePlusOne = c.nodeStack[n-1] + 1
	c.nodeStack = c.nodeStack[:n-1]
}

func (c *pointerCollector) pass() {
	c.state.pass++
}

func (c *pointerCollector) popPass() {
	c.state.pass--
}

func (c *pointerCollector) currentArea() int {
	if i := c.state.nodePlusOne - 1; i != -1 {
		n := c.q.hitTree[i]
		return n.area
	}
	return -1
}

func (c *pointerCollector) currentAreaBounds() image.Rectangle {
	a := c.currentArea()
	if a == -1 {
		panic("no root area")
	}
	return c.q.areas[a].bounds()
}

func (c *pointerCollector) addHitNode(n hitNode) {
	n.next = c.state.nodePlusOne - 1
	c.q.hitTree = append(c.q.hitTree, n)
	c.state.nodePlusOne = len(c.q.hitTree) - 1 + 1
}

// newHandler returns the current handler or a new one for tag.
func (c *pointerCollector) newHandler(tag event.Tag, state *pointerHandler) {
	areaID := c.currentArea()
	c.addHitNode(hitNode{
		area: areaID,
		tag:  tag,
		pass: c.state.pass > 0,
	})
	state.areaPlusOne = areaID + 1
}

func (s *pointerHandler) Reset() {
	s.areaPlusOne = 0
}

func (c *pointerCollector) actionInputOp(act system.Action) {
	areaID := c.currentArea()
	area := &c.q.areas[areaID]
	area.action = act
}

func (q *pointerQueue) grab(state pointerState, req pointer.GrabCmd) (pointerState, []taggedEvent) {
	var evts []taggedEvent
	for _, p := range state.pointers {
		if !p.pressed || p.id != req.ID {
			continue
		}
		// Drop other handlers that lost their grab.
		for i := len(p.handlers) - 1; i >= 0; i-- {
			if tag := p.handlers[i]; tag != req.Tag {
				evts = append(evts, taggedEvent{
					tag:   tag,
					event: pointer.Event{Kind: pointer.Cancel},
				})
				state = dropHandler(state, tag)
			}
		}
		break
	}
	return state, evts
}

func (c *pointerCollector) inputOp(tag event.Tag, state *pointerHandler) {
	areaID := c.currentArea()
	area := &c.q.areas[areaID]
	area.semantic.content.tag = tag
	c.newHandler(tag, state)
}

func (p *pointerFilter) Add(f event.Filter) {
	switch f := f.(type) {
	case transfer.SourceFilter:
		for _, m := range p.sourceMimes {
			if m == f.Type {
				return
			}
		}
		p.sourceMimes = append(p.sourceMimes, f.Type)
	case transfer.TargetFilter:
		for _, m := range p.targetMimes {
			if m == f.Type {
				return
			}
		}
		p.targetMimes = append(p.targetMimes, f.Type)
	case transfer.URLFilter:
		for _, m := range p.scheme {
			if m == f.Scheme {
				return
			}
		}
		p.scheme = append(p.scheme, f.Scheme)

	case pointer.Filter:
		p.kinds = p.kinds | f.Kinds
		p.scrollX = p.scrollX.Union(f.ScrollX)
		p.scrollY = p.scrollY.Union(f.ScrollY)
	}
}

func (p *pointerFilter) Matches(e event.Event) bool {
	switch e := e.(type) {
	case pointer.Event:
		return e.Kind&p.kinds == e.Kind
	case transfer.CancelEvent, transfer.InitiateEvent:
		return len(p.sourceMimes) > 0 || len(p.targetMimes) > 0
	case transfer.RequestEvent:
		for _, t := range p.sourceMimes {
			if t == e.Type {
				return true
			}
		}
	case transfer.DataEvent:
		for _, t := range p.targetMimes {
			if t == e.Type {
				return true
			}
		}
	case transfer.URLEvent:
		for _, t := range p.scheme {
			if t == "" || t == e.URL.Scheme {
				return true
			}
		}
	}
	return false
}

func (p *pointerFilter) Merge(p2 pointerFilter) {
	p.kinds = p.kinds | p2.kinds
	p.scrollX = p.scrollX.Union(p2.scrollX)
	p.scrollY = p.scrollY.Union(p2.scrollY)
	p.sourceMimes = append(p.sourceMimes, p2.sourceMimes...)
	p.targetMimes = append(p.targetMimes, p2.targetMimes...)
	p.scheme = append(p.scheme, p2.scheme...)
}

// clampScroll splits a scroll distance in the remaining scroll and the
// scroll accepted by the filter.
func (p *pointerFilter) clampScroll(scroll f32.Point) (left, scrolled f32.Point) {
	left.X, scrolled.X = clampSplit(scroll.X, p.scrollX.Min, p.scrollX.Max)
	left.Y, scrolled.Y = clampSplit(scroll.Y, p.scrollY.Min, p.scrollY.Max)
	return
}

func clampSplit(v float32, min, max int) (float32, float32) {
	if m := float32(max); v > m {
		return v - m, m
	}
	if m := float32(min); v < m {
		return v - m, m
	}
	return 0, v
}

func (s *pointerHandler) ResetEvent() (event.Event, bool) {
	if s.setup {
		return nil, false
	}
	s.setup = true
	return pointer.Event{Kind: pointer.Cancel}, true
}

func (c *pointerCollector) semanticLabel(lbl string) {
	areaID := c.currentArea()
	area := &c.q.areas[areaID]
	area.semantic.valid = true
	area.semantic.content.label = lbl
}

func (c *pointerCollector) semanticDesc(desc string) {
	areaID := c.currentArea()
	area := &c.q.areas[areaID]
	area.semantic.valid = true
	area.semantic.content.desc = desc
}

func (c *pointerCollector) semanticClass(class semantic.ClassOp) {
	areaID := c.currentArea()
	area := &c.q.areas[areaID]
	area.semantic.valid = true
	area.semantic.content.class = class
}

func (c *pointerCollector) semanticSelected(selected bool) {
	areaID := c.currentArea()
	area := &c.q.areas[areaID]
	area.semantic.valid = true
	area.semantic.content.selected = selected
}

func (c *pointerCollector) semanticEnabled(enabled bool) {
	areaID := c.currentArea()
	area := &c.q.areas[areaID]
	area.semantic.valid = true
	area.semantic.content.disabled = !enabled
}

func (c *pointerCollector) cursor(cursor pointer.Cursor) {
	areaID := c.currentArea()
	area := &c.q.areas[areaID]
	area.cursor = cursor
}

func (q *pointerQueue) offerData(handlers map[event.Tag]*handler, state pointerState, req transfer.OfferCmd) (pointerState, []taggedEvent) {
	var evts []taggedEvent
	for i, p := range state.pointers {
		if p.dataSource != req.Tag {
			continue
		}
		if p.dataTarget != nil {
			evts = append(evts, taggedEvent{tag: p.dataTarget, event: transfer.DataEvent{
				Type: req.Type,
				Open: func() io.ReadCloser {
					return req.Data
				},
			}})
		}
		state.pointers = append([]pointerInfo{}, state.pointers...)
		state.pointers[i], evts = q.deliverTransferCancelEvent(handlers, p, evts)
		break
	}
	return state, evts
}

func (c *pointerCollector) Reset() {
	c.q.reset()
	c.resetState()
	c.ensureRoot()
}

// Ensure implicit root area for semantic descriptions to hang onto.
func (c *pointerCollector) ensureRoot() {
	if len(c.q.areas) > 0 {
		return
	}
	c.pushArea(areaRect, image.Rect(-1e6, -1e6, 1e6, 1e6))
	// Make it semantic to ensure a single semantic root.
	c.q.areas[0].semantic.valid = true
}

func (q *pointerQueue) assignSemIDs() {
	if q.semantic.idsAssigned {
		return
	}
	q.semantic.idsAssigned = true
	for i, a := range q.areas {
		if a.semantic.valid {
			q.areas[i].semantic.id = q.semanticIDFor(a.semantic.content)
		}
	}
}

func (q *pointerQueue) AppendSemantics(nodes []SemanticNode) []SemanticNode {
	q.assignSemIDs()
	nodes = q.appendSemanticChildren(nodes, 0)
	nodes = q.appendSemanticArea(nodes, 0, 0)
	return nodes
}

func (q *pointerQueue) appendSemanticArea(nodes []SemanticNode, parentID SemanticID, nodeIdx int) []SemanticNode {
	areaIdx := nodes[nodeIdx].areaIdx
	a := q.areas[areaIdx]
	childStart := len(nodes)
	nodes = q.appendSemanticChildren(nodes, a.firstChild)
	childEnd := len(nodes)
	for i := childStart; i < childEnd; i++ {
		nodes = q.appendSemanticArea(nodes, a.semantic.id, i)
	}
	n := &nodes[nodeIdx]
	n.ParentID = parentID
	n.Children = nodes[childStart:childEnd]
	return nodes
}

func (q *pointerQueue) appendSemanticChildren(nodes []SemanticNode, areaIdx int) []SemanticNode {
	if areaIdx == -1 {
		return nodes
	}
	a := q.areas[areaIdx]
	if semID := a.semantic.id; semID != 0 {
		cnt := a.semantic.content
		nodes = append(nodes, SemanticNode{
			ID: semID,
			Desc: SemanticDesc{
				Bounds:      a.bounds(),
				Label:       cnt.label,
				Description: cnt.desc,
				Class:       cnt.class,
				Gestures:    cnt.gestures,
				Selected:    cnt.selected,
				Disabled:    cnt.disabled,
			},
			areaIdx: areaIdx,
		})
	} else {
		nodes = q.appendSemanticChildren(nodes, a.firstChild)
	}
	return q.appendSemanticChildren(nodes, a.sibling)
}

func (q *pointerQueue) semanticIDFor(content semanticContent) SemanticID {
	ids := q.semantic.contentIDs[content]
	for i, id := range ids {
		if !id.used {
			ids[i].used = true
			return id.id
		}
	}
	// No prior assigned ID; allocate a new one.
	q.semantic.lastID++
	id := semanticID{id: q.semantic.lastID, used: true}
	if q.semantic.contentIDs == nil {
		q.semantic.contentIDs = make(map[semanticContent][]semanticID)
	}
	q.semantic.contentIDs[content] = append(q.semantic.contentIDs[content], id)
	return id.id
}

func (q *pointerQueue) ActionAt(pos f32.Point) (action system.Action, hasAction bool) {
	q.hitTest(pos, func(n *hitNode) bool {
		area := q.areas[n.area]
		if area.action != 0 {
			action = area.action
			hasAction = true
			return false
		}
		return true
	})
	return action, hasAction
}

func (q *pointerQueue) SemanticAt(pos f32.Point) (semID SemanticID, hasSemID bool) {
	q.assignSemIDs()
	q.hitTest(pos, func(n *hitNode) bool {
		area := q.areas[n.area]
		if area.semantic.id != 0 {
			semID = area.semantic.id
			hasSemID = true
			return false
		}
		return true
	})
	return semID, hasSemID
}

// hitTest searches the hit tree for nodes matching pos. Any node matching pos will
// have the onNode func invoked on it to allow the caller to extract whatever information
// is necessary for further processing. onNode may return false to terminate the walk of
// the hit tree, or true to continue. Providing this algorithm in this generic way
// allows normal event routing and system action event routing to share the same traversal
// logic even though they are interested in different aspects of hit nodes.
func (q *pointerQueue) hitTest(pos f32.Point, onNode func(*hitNode) bool) pointer.Cursor {
	// Track whether we're passing through hits.
	pass := true
	idx := len(q.hitTree) - 1
	cursor := pointer.CursorDefault
	for idx >= 0 {
		n := &q.hitTree[idx]
		hit, c := q.hit(n.area, pos)
		if !hit {
			idx--
			continue
		}
		if cursor == pointer.CursorDefault {
			cursor = c
		}
		pass = pass && n.pass
		if pass {
			idx--
		} else {
			idx = n.next
		}
		if !onNode(n) {
			break
		}
	}
	return cursor
}

func (q *pointerQueue) invTransform(areaIdx int, p f32.Point) f32.Point {
	if areaIdx == -1 {
		return p
	}
	return q.areas[areaIdx].trans.Invert().Transform(p)
}

func (q *pointerQueue) hit(areaIdx int, p f32.Point) (bool, pointer.Cursor) {
	c := pointer.CursorDefault
	for areaIdx != -1 {
		a := &q.areas[areaIdx]
		if c == pointer.CursorDefault {
			c = a.cursor
		}
		p := a.trans.Invert().Transform(p)
		if !a.area.Hit(p) {
			return false, c
		}
		areaIdx = a.parent
	}
	return true, c
}

func (q *pointerQueue) reset() {
	q.hitTree = q.hitTree[:0]
	q.areas = q.areas[:0]
	q.semantic.idsAssigned = false
	for k, ids := range q.semantic.contentIDs {
		for i := len(ids) - 1; i >= 0; i-- {
			if !ids[i].used {
				ids = append(ids[:i], ids[i+1:]...)
			} else {
				ids[i].used = false
			}
		}
		if len(ids) > 0 {
			q.semantic.contentIDs[k] = ids
		} else {
			delete(q.semantic.contentIDs, k)
		}
	}
}

func (q *pointerQueue) Frame(handlers map[event.Tag]*handler, state pointerState) (pointerState, []taggedEvent) {
	for _, h := range handlers {
		if h.pointer.areaPlusOne != 0 {
			area := &q.areas[h.pointer.areaPlusOne-1]
			if h.filter.pointer.kinds&(pointer.Press|pointer.Release) != 0 {
				area.semantic.content.gestures |= ClickGesture
			}
			if h.filter.pointer.kinds&pointer.Scroll != 0 {
				area.semantic.content.gestures |= ScrollGesture
			}
			area.semantic.valid = area.semantic.content.gestures != 0
		}
	}
	var evts []taggedEvent
	for i, p := range state.pointers {
		changed := false
		p, evts, state.cursor, changed = q.deliverEnterLeaveEvents(handlers, state.cursor, p, evts, p.last)
		if changed {
			state.pointers = append([]pointerInfo{}, state.pointers...)
			state.pointers[i] = p
		}
	}
	return state, evts
}

func dropHandler(state pointerState, tag event.Tag) pointerState {
	pointers := state.pointers
	state.pointers = nil
	for _, p := range pointers {
		handlers := p.handlers
		p.handlers = nil
		for _, h := range handlers {
			if h != tag {
				p.handlers = append(p.handlers, h)
			}
		}
		entered := p.entered
		p.entered = nil
		for _, h := range entered {
			if h != tag {
				p.entered = append(p.entered, h)
			}
		}
		state.pointers = append(state.pointers, p)
	}
	return state
}

// pointerOf returns the pointerInfo index corresponding to the pointer in e.
func (s pointerState) pointerOf(e pointer.Event) (pointerState, int) {
	for i, p := range s.pointers {
		if p.id == e.PointerID {
			return s, i
		}
	}
	n := len(s.pointers)
	s.pointers = append(s.pointers[:n:n], pointerInfo{id: e.PointerID})
	return s, len(s.pointers) - 1
}

// Deliver is like Push, but delivers an event to a particular area.
func (q *pointerQueue) Deliver(handlers map[event.Tag]*handler, areaIdx int, e pointer.Event) []taggedEvent {
	scroll := e.Scroll
	idx := len(q.hitTree) - 1
	// Locate first potential receiver.
	for idx != -1 {
		n := &q.hitTree[idx]
		if n.area == areaIdx {
			break
		}
		idx--
	}
	var evts []taggedEvent
	for idx != -1 {
		n := &q.hitTree[idx]
		idx = n.next
		h, ok := handlers[n.tag]
		if !ok || !h.filter.pointer.Matches(e) {
			continue
		}
		e := e
		if e.Kind == pointer.Scroll {
			if scroll == (f32.Point{}) {
				break
			}
			scroll, e.Scroll = h.filter.pointer.clampScroll(scroll)
		}
		e.Position = q.invTransform(h.pointer.areaPlusOne-1, e.Position)
		evts = append(evts, taggedEvent{tag: n.tag, event: e})
		if e.Kind != pointer.Scroll {
			break
		}
	}
	return evts
}

// SemanticArea returns the sematic content for area, and its parent area.
func (q *pointerQueue) SemanticArea(areaIdx int) (semanticContent, int) {
	for areaIdx != -1 {
		a := &q.areas[areaIdx]
		areaIdx = a.parent
		if !a.semantic.valid {
			continue
		}
		return a.semantic.content, areaIdx
	}
	return semanticContent{}, -1
}

func (q *pointerQueue) Push(handlers map[event.Tag]*handler, state pointerState, e pointer.Event) (pointerState, []taggedEvent) {
	var evts []taggedEvent
	if e.Kind == pointer.Cancel {
		for k := range handlers {
			evts = append(evts, taggedEvent{
				event: pointer.Event{Kind: pointer.Cancel},
				tag:   k,
			})
		}
		state.pointers = nil
		return state, evts
	}
	state, pidx := state.pointerOf(e)
	p := state.pointers[pidx]

	switch e.Kind {
	case pointer.Press:
		p, evts, state.cursor, _ = q.deliverEnterLeaveEvents(handlers, state.cursor, p, evts, e)
		p.pressed = true
		evts = q.deliverEvent(handlers, p, evts, e)
	case pointer.Move:
		if p.pressed {
			e.Kind = pointer.Drag
		}
		p, evts, state.cursor, _ = q.deliverEnterLeaveEvents(handlers, state.cursor, p, evts, e)
		evts = q.deliverEvent(handlers, p, evts, e)
		if p.pressed {
			p, evts = q.deliverDragEvent(handlers, p, evts)
		}
	case pointer.Release:
		evts = q.deliverEvent(handlers, p, evts, e)
		p.pressed = false
		p, evts, state.cursor, _ = q.deliverEnterLeaveEvents(handlers, state.cursor, p, evts, e)
		p, evts = q.deliverDropEvent(handlers, p, evts)
	case pointer.Scroll:
		p, evts, state.cursor, _ = q.deliverEnterLeaveEvents(handlers, state.cursor, p, evts, e)
		evts = q.deliverEvent(handlers, p, evts, e)
	default:
		panic("unsupported pointer event type")
	}

	p.last = e

	if !p.pressed && len(p.entered) == 0 {
		// No longer need to track pointer.
		state.pointers = append(state.pointers[:pidx:pidx], state.pointers[pidx+1:]...)
	} else {
		state.pointers = append([]pointerInfo{}, state.pointers...)
		state.pointers[pidx] = p
	}
	return state, evts
}

func (q *pointerQueue) deliverEvent(handlers map[event.Tag]*handler, p pointerInfo, evts []taggedEvent, e pointer.Event) []taggedEvent {
	foremost := true
	if p.pressed && len(p.handlers) == 1 {
		e.Priority = pointer.Grabbed
		foremost = false
	}
	scroll := e.Scroll
	for _, k := range p.handlers {
		h, ok := handlers[k]
		if !ok {
			continue
		}
		f := h.filter.pointer
		if !f.Matches(e) {
			continue
		}
		if e.Kind == pointer.Scroll {
			if scroll == (f32.Point{}) {
				return evts
			}
			scroll, e.Scroll = f.clampScroll(scroll)
		}
		e := e
		if foremost {
			foremost = false
			e.Priority = pointer.Foremost
		}
		e.Position = q.invTransform(h.pointer.areaPlusOne-1, e.Position)
		evts = append(evts, taggedEvent{event: e, tag: k})
	}
	return evts
}

func (q *pointerQueue) deliverEnterLeaveEvents(handlers map[event.Tag]*handler, cursor pointer.Cursor, p pointerInfo, evts []taggedEvent, e pointer.Event) (pointerInfo, []taggedEvent, pointer.Cursor, bool) {
	changed := false
	var hits []event.Tag
	if e.Source != pointer.Mouse && !p.pressed && e.Kind != pointer.Press {
		// Consider non-mouse pointers leaving when they're released.
	} else {
		var transSrc *pointerFilter
		if p.dataSource != nil {
			transSrc = &handlers[p.dataSource].filter.pointer
		}
		cursor = q.hitTest(e.Position, func(n *hitNode) bool {
			h, ok := handlers[n.tag]
			if !ok {
				return true
			}
			add := true
			if p.pressed {
				add = false
				// Filter out non-participating handlers,
				// except potential transfer targets when a transfer has been initiated.
				if _, found := searchTag(p.handlers, n.tag); found {
					add = true
				}
				if transSrc != nil {
					if _, ok := firstMimeMatch(transSrc, &h.filter.pointer); ok {
						add = true
					}
				}
			}
			if add {
				hits = addHandler(hits, n.tag)
			}
			return true
		})
		if !p.pressed {
			changed = true
			p.handlers = hits
		}
	}
	// Deliver Leave events.
	for _, k := range p.entered {
		if _, found := searchTag(hits, k); found {
			continue
		}
		h, ok := handlers[k]
		if !ok {
			continue
		}
		changed = true
		e := e
		e.Kind = pointer.Leave

		if h.filter.pointer.Matches(e) {
			e.Position = q.invTransform(h.pointer.areaPlusOne-1, e.Position)
			evts = append(evts, taggedEvent{tag: k, event: e})
		}
	}
	// Deliver Enter events.
	for _, k := range hits {
		if _, found := searchTag(p.entered, k); found {
			continue
		}
		h, ok := handlers[k]
		if !ok {
			continue
		}
		changed = true
		e := e
		e.Kind = pointer.Enter

		if h.filter.pointer.Matches(e) {
			e.Position = q.invTransform(h.pointer.areaPlusOne-1, e.Position)
			evts = append(evts, taggedEvent{tag: k, event: e})
		}
	}
	p.entered = hits
	return p, evts, cursor, changed
}

func (q *pointerQueue) deliverDragEvent(handlers map[event.Tag]*handler, p pointerInfo, evts []taggedEvent) (pointerInfo, []taggedEvent) {
	if p.dataSource != nil {
		return p, evts
	}
	// Identify the data source.
	for _, k := range p.entered {
		src := &handlers[k].filter.pointer
		if len(src.sourceMimes) == 0 {
			continue
		}
		// One data source handler per pointer.
		p.dataSource = k
		// Notify all potential targets.
		for k, tgt := range handlers {
			if _, ok := firstMimeMatch(src, &tgt.filter.pointer); ok {
				evts = append(evts, taggedEvent{tag: k, event: transfer.InitiateEvent{}})
			}
		}
		break
	}
	return p, evts
}

func (q *pointerQueue) deliverDropEvent(handlers map[event.Tag]*handler, p pointerInfo, evts []taggedEvent) (pointerInfo, []taggedEvent) {
	if p.dataSource == nil {
		return p, evts
	}
	// Request data from the source.
	src := &handlers[p.dataSource].filter.pointer
	for _, k := range p.entered {
		h := handlers[k]
		if m, ok := firstMimeMatch(src, &h.filter.pointer); ok {
			p.dataTarget = k
			evts = append(evts, taggedEvent{tag: p.dataSource, event: transfer.RequestEvent{Type: m}})
			return p, evts
		}
	}
	// No valid target found, abort.
	return q.deliverTransferCancelEvent(handlers, p, evts)
}

func (q *pointerQueue) deliverTransferCancelEvent(handlers map[event.Tag]*handler, p pointerInfo, evts []taggedEvent) (pointerInfo, []taggedEvent) {
	evts = append(evts, taggedEvent{tag: p.dataSource, event: transfer.CancelEvent{}})
	// Cancel all potential targets.
	src := &handlers[p.dataSource].filter.pointer
	for k, h := range handlers {
		if _, ok := firstMimeMatch(src, &h.filter.pointer); ok {
			evts = append(evts, taggedEvent{tag: k, event: transfer.CancelEvent{}})
		}
	}
	p.dataSource = nil
	p.dataTarget = nil
	return p, evts
}

// ClipFor clips r to the parents of area.
func (q *pointerQueue) ClipFor(area int, r image.Rectangle) image.Rectangle {
	a := &q.areas[area]
	parent := a.parent
	for parent != -1 {
		a := &q.areas[parent]
		r = r.Intersect(a.bounds())
		parent = a.parent
	}
	return r
}

func searchTag(tags []event.Tag, tag event.Tag) (int, bool) {
	for i, t := range tags {
		if t == tag {
			return i, true
		}
	}
	return 0, false
}

// addHandler adds tag to the slice if not present.
func addHandler(tags []event.Tag, tag event.Tag) []event.Tag {
	for _, t := range tags {
		if t == tag {
			return tags
		}
	}
	return append(tags, tag)
}

// firstMimeMatch returns the first type match between src and tgt.
func firstMimeMatch(src, tgt *pointerFilter) (first string, matched bool) {
	for _, m1 := range tgt.targetMimes {
		for _, m2 := range src.sourceMimes {
			if m1 == m2 {
				return m1, true
			}
		}
	}
	return "", false
}

func (op *areaOp) Hit(pos f32.Point) bool {
	pos = pos.Sub(f32internal.FPt(op.rect.Min))
	size := f32internal.FPt(op.rect.Size())
	switch op.kind {
	case areaRect:
		return 0 <= pos.X && pos.X < size.X &&
			0 <= pos.Y && pos.Y < size.Y
	case areaEllipse:
		rx := size.X / 2
		ry := size.Y / 2
		xh := pos.X - rx
		yk := pos.Y - ry
		// The ellipse function works in all cases because
		// 0/0 is not <= 1.
		return (xh*xh)/(rx*rx)+(yk*yk)/(ry*ry) <= 1
	default:
		panic("invalid area kind")
	}
}

func (a *areaNode) bounds() image.Rectangle {
	return f32internal.Rectangle{
		Min: a.trans.Transform(f32internal.FPt(a.area.rect.Min)),
		Max: a.trans.Transform(f32internal.FPt(a.area.rect.Max)),
	}.Round()
}
