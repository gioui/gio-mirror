// SPDX-License-Identifier: Unlicense OR MIT

package app

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"runtime"
	"time"

	"gioui.org/gpu"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/io/profile"
	"gioui.org/io/router"
	"gioui.org/io/system"
	"gioui.org/op"
	"gioui.org/unit"

	_ "gioui.org/app/internal/log"
)

// Option configures a window.
type Option func(cnf *config)

// Window represents an operating system window.
type Window struct {
	ctx context
	gpu gpu.GPU

	// driverFuncs is a channel of functions to run when
	// the Window has a valid driver.
	driverFuncs chan func(d driver)
	// wakeups wakes up the native event loop to send a
	// WakeupEvent that flushes driverFuncs.
	wakeups chan struct{}

	out         chan event.Event
	in          chan event.Event
	ack         chan struct{}
	invalidates chan struct{}
	frames      chan *op.Ops
	frameAck    chan struct{}
	// dead is closed when the window is destroyed.
	dead          chan struct{}
	notifyAnimate chan struct{}

	stage        system.Stage
	animating    bool
	hasNextFrame bool
	nextFrame    time.Time
	delayedDraw  *time.Timer

	queue  queue
	cursor pointer.CursorName

	callbacks callbacks

	nocontext bool
}

type callbacks struct {
	w *Window
	d driver
}

// queue is an event.Queue implementation that distributes system events
// to the input handlers declared in the most recent frame.
type queue struct {
	q router.Router
}

// driverEvent is sent when the underlying driver changes.
type driverEvent struct {
	wakeup func()
}

// Pre-allocate the ack event to avoid garbage.
var ackEvent event.Event

// NewWindow creates a new window for a set of window
// options. The options are hints; the platform is free to
// ignore or adjust them.
//
// If the current program is running on iOS and Android,
// NewWindow returns the window previously created by the
// platform.
//
// Calling NewWindow more than once is not supported on
// iOS, Android, WebAssembly.
func NewWindow(options ...Option) *Window {
	cnf := new(config)
	// Default options.
	Size(unit.Dp(800), unit.Dp(600))(cnf)
	Title("Gio")(cnf)

	for _, o := range options {
		o(cnf)
	}

	w := &Window{
		in:            make(chan event.Event),
		out:           make(chan event.Event),
		ack:           make(chan struct{}),
		invalidates:   make(chan struct{}, 1),
		frames:        make(chan *op.Ops),
		frameAck:      make(chan struct{}),
		driverFuncs:   make(chan func(d driver), 1),
		wakeups:       make(chan struct{}, 1),
		dead:          make(chan struct{}),
		notifyAnimate: make(chan struct{}, 1),
		nocontext:     cnf.CustomRenderer,
	}
	w.callbacks.w = w
	go w.run(cnf)
	return w
}

// Events returns the channel where events are delivered.
func (w *Window) Events() <-chan event.Event {
	return w.out
}

// update updates the window contents, input operations declare input handlers,
// and so on. The supplied operations list completely replaces the window state
// from previous calls.
func (w *Window) update(frame *op.Ops) {
	w.frames <- frame
	<-w.frameAck
}

func (w *Window) validateAndProcess(frameStart time.Time, size image.Point, sync bool, frame *op.Ops) error {
	for {
		if w.gpu == nil && !w.nocontext {
			var err error
			if w.ctx == nil {
				w.driverRun(func(d driver) {
					w.ctx, err = d.NewContext()
				})
				if err != nil {
					return err
				}
				sync = true
			}
		}
		if sync && w.ctx != nil {
			var err error
			w.driverRun(func(d driver) {
				err = w.ctx.Refresh()
			})
			if err != nil {
				w.destroyGPU()
				if err == errDeviceLost {
					continue
				}
				return err
			}
		}
		if w.gpu == nil && !w.nocontext {
			if err := w.ctx.Lock(); err != nil {
				w.destroyGPU()
				return err
			}
			gpu, err := gpu.New(w.ctx.API())
			w.ctx.Unlock()
			if err != nil {
				w.destroyGPU()
				return err
			}
			w.gpu = gpu
		}
		if w.gpu != nil {
			if err := w.render(frame, size); err != nil {
				w.destroyGPU()
				if err == errDeviceLost {
					continue
				}
				return err
			}
		}
		w.processFrame(frameStart, frame)
		return nil
	}
}

func (w *Window) render(frame *op.Ops, viewport image.Point) error {
	if err := w.ctx.Lock(); err != nil {
		return err
	}
	defer w.ctx.Unlock()
	if runtime.GOOS == "js" {
		// Use transparent black when Gio is embedded, to allow mixing of Gio and
		// foreign content below.
		w.gpu.Clear(color.NRGBA{A: 0x00, R: 0x00, G: 0x00, B: 0x00})
	} else {
		w.gpu.Clear(color.NRGBA{A: 0xff, R: 0xff, G: 0xff, B: 0xff})
	}
	if err := w.gpu.Frame(frame, w.ctx.RenderTarget(), viewport); err != nil {
		return err
	}
	return w.ctx.Present()
}

func (w *Window) processFrame(frameStart time.Time, frame *op.Ops) {
	w.queue.q.Frame(frame)
	switch w.queue.q.TextInputState() {
	case router.TextInputOpen:
		w.driverRun(func(d driver) { d.ShowTextInput(true) })
	case router.TextInputClose:
		w.driverRun(func(d driver) { d.ShowTextInput(false) })
	}
	if hint, ok := w.queue.q.TextInputHint(); ok {
		w.driverRun(func(d driver) { d.SetInputHint(hint) })
	}
	if txt, ok := w.queue.q.WriteClipboard(); ok {
		w.WriteClipboard(txt)
	}
	if w.queue.q.ReadClipboard() {
		w.ReadClipboard()
	}
	if w.queue.q.Profiling() && w.gpu != nil {
		frameDur := time.Since(frameStart)
		frameDur = frameDur.Truncate(100 * time.Microsecond)
		q := 100 * time.Microsecond
		timings := fmt.Sprintf("tot:%7s %s", frameDur.Round(q), w.gpu.Profile())
		w.queue.q.Queue(profile.Event{Timings: timings})
	}
	if t, ok := w.queue.q.WakeupTime(); ok {
		w.setNextFrame(t)
	}
	// Opportunistically check whether Invalidate has been called, to avoid
	// stopping and starting animation mode.
	select {
	case <-w.invalidates:
		w.setNextFrame(time.Time{})
	default:
	}
	w.updateAnimation()
}

// Invalidate the window such that a FrameEvent will be generated immediately.
// If the window is inactive, the event is sent when the window becomes active.
//
// Note that Invalidate is intended for externally triggered updates, such as a
// response from a network request. InvalidateOp is more efficient for animation
// and similar internal updates.
//
// Invalidate is safe for concurrent use.
func (w *Window) Invalidate() {
	select {
	case w.invalidates <- struct{}{}:
	default:
	}
}

// Option applies the options to the window.
func (w *Window) Option(opts ...Option) {
	go w.driverRun(func(d driver) {
		c := new(config)
		for _, opt := range opts {
			opt(c)
		}
		d.Configure(c)
	})
}

// ReadClipboard initiates a read of the clipboard in the form
// of a clipboard.Event. Multiple reads may be coalesced
// to a single event.
func (w *Window) ReadClipboard() {
	go w.driverRun(func(d driver) {
		d.ReadClipboard()
	})
}

// WriteClipboard writes a string to the clipboard.
func (w *Window) WriteClipboard(s string) {
	go w.driverRun(func(d driver) {
		d.WriteClipboard(s)
	})
}

// SetCursorName changes the current window cursor to name.
func (w *Window) SetCursorName(name pointer.CursorName) {
	go w.driverRun(func(d driver) {
		d.SetCursor(name)
	})
}

// Close the window. The window's event loop should exit when it receives
// system.DestroyEvent.
//
// Currently, only macOS, Windows and X11 drivers implement this functionality,
// all others are stubbed.
func (w *Window) Close() {
	go w.driverRun(func(d driver) {
		d.Close()
	})
}

// Run f in the same thread as the native window event loop, and wait for f to
// return or the window to close. Run is guaranteed not to deadlock if it is
// invoked during the handling of a ViewEvent, system.FrameEvent,
// system.StageEvent; call Run in a separate goroutine to avoid deadlock in all
// other cases.
//
// Note that most programs should not call Run; configuring a Window with
// CustomRenderer is a notable exception.
func (w *Window) Run(f func()) {
	w.driverRun(func(_ driver) {
		f()
	})
}

func (w *Window) driverRun(f func(d driver)) {
	done := make(chan struct{})
	wrapper := func(d driver) {
		defer close(done)
		f(d)
	}
	select {
	case w.driverFuncs <- wrapper:
		w.wakeup()
		select {
		case <-done:
		case <-w.dead:
		}
	case <-w.dead:
	}
}

func (w *Window) updateAnimation() {
	animate := false
	if w.delayedDraw != nil {
		w.delayedDraw.Stop()
		w.delayedDraw = nil
	}
	if w.stage >= system.StageRunning && w.hasNextFrame {
		if dt := time.Until(w.nextFrame); dt <= 0 {
			animate = true
		} else {
			w.delayedDraw = time.NewTimer(dt)
		}
	}
	if animate != w.animating {
		w.animating = animate
		select {
		case w.notifyAnimate <- struct{}{}:
			w.wakeup()
		default:
		}
	}
}

func (w *Window) wakeup() {
	select {
	case w.wakeups <- struct{}{}:
	default:
	}
}

func (w *Window) setNextFrame(at time.Time) {
	if !w.hasNextFrame || at.Before(w.nextFrame) {
		w.hasNextFrame = true
		w.nextFrame = at
	}
}

func (c *callbacks) SetDriver(d driver) {
	c.d = d
	var wakeup func()
	if d != nil {
		wakeup = d.Wakeup
	}
	c.Event(driverEvent{wakeup})
}

func (c *callbacks) Event(e event.Event) {
	select {
	case c.w.in <- e:
		c.w.runFuncs(c.d)
	case <-c.w.dead:
	}
}

func (w *Window) runFuncs(d driver) {
	// Don't run driver functions if there's no driver.
	if d == nil {
		<-w.ack
		return
	}
	// Flush pending runnnables.
loop:
	for {
		select {
		case <-w.notifyAnimate:
			d.SetAnimating(w.animating)
		case f := <-w.driverFuncs:
			f(d)
		default:
			break loop
		}
	}
	// Wait for ack while running incoming runnables.
	for {
		select {
		case <-w.notifyAnimate:
			d.SetAnimating(w.animating)
		case f := <-w.driverFuncs:
			f(d)
		case <-w.ack:
			return
		}
	}
}

func (w *Window) waitAck() {
	// Send a dummy event; when it gets through we
	// know the application has processed the previous event.
	w.out <- ackEvent
}

// Prematurely destroy the window and wait for the native window
// destroy event.
func (w *Window) destroy(err error) {
	w.destroyGPU()
	// Ack the current event.
	w.ack <- struct{}{}
	w.out <- system.DestroyEvent{Err: err}
	close(w.dead)
	close(w.out)
	for e := range w.in {
		w.ack <- struct{}{}
		if _, ok := e.(system.DestroyEvent); ok {
			return
		}
	}
}

func (w *Window) destroyGPU() {
	if w.gpu != nil {
		w.ctx.Lock()
		w.gpu.Release()
		w.ctx.Unlock()
		w.gpu = nil
	}
	if w.ctx != nil {
		w.ctx.Release()
		w.ctx = nil
	}
}

// waitFrame waits for the client to either call FrameEvent.Frame
// or to continue event handling. It returns whether the client
// called Frame or not.
func (w *Window) waitFrame() (*op.Ops, bool) {
	select {
	case frame := <-w.frames:
		// The client called FrameEvent.Frame.
		return frame, true
	case w.out <- ackEvent:
		// The client ignored FrameEvent and continued processing
		// events.
		return nil, false
	}
}

func (w *Window) run(cnf *config) {
	// Some OpenGL drivers don't like being made current on many different
	// OS threads. Force the Go runtime to map the event loop goroutine to
	// only one thread.
	runtime.LockOSThread()

	defer close(w.out)
	defer close(w.dead)
	if err := newWindow(&w.callbacks, cnf); err != nil {
		w.out <- system.DestroyEvent{Err: err}
		return
	}
	var wakeup func()
	for {
		var (
			wakeups <-chan struct{}
			timer   <-chan time.Time
		)
		if wakeup != nil {
			wakeups = w.wakeups
		}
		if w.delayedDraw != nil {
			timer = w.delayedDraw.C
		}
		select {
		case <-timer:
			w.setNextFrame(time.Time{})
			w.updateAnimation()
		case <-w.invalidates:
			w.setNextFrame(time.Time{})
			w.updateAnimation()
		case <-wakeups:
			wakeup()
		case e := <-w.in:
			switch e2 := e.(type) {
			case system.StageEvent:
				if e2.Stage < system.StageRunning {
					if w.gpu != nil {
						w.ctx.Lock()
						w.gpu.Release()
						w.gpu = nil
						w.ctx.Unlock()
					}
				}
				w.stage = e2.Stage
				w.updateAnimation()
				w.out <- e
				w.waitAck()
			case frameEvent:
				if e2.Size == (image.Point{}) {
					panic(errors.New("internal error: zero-sized Draw"))
				}
				if w.stage < system.StageRunning {
					// No drawing if not visible.
					break
				}
				frameStart := time.Now()
				w.hasNextFrame = false
				e2.Frame = w.update
				e2.Queue = &w.queue
				w.out <- e2.FrameEvent
				frame, gotFrame := w.waitFrame()
				err := w.validateAndProcess(frameStart, e2.Size, e2.Sync, frame)
				if gotFrame {
					// We're done with frame, let the client continue.
					w.frameAck <- struct{}{}
				}
				if err != nil {
					w.destroyGPU()
					w.destroy(err)
					return
				}
				w.updateCursor()
			case *system.CommandEvent:
				w.out <- e
				w.waitAck()
			case driverEvent:
				wakeup = e2.wakeup
			case system.DestroyEvent:
				w.destroyGPU()
				w.out <- e2
				w.ack <- struct{}{}
				return
			case ViewEvent:
				w.out <- e2
				w.waitAck()
			case wakeupEvent:
			case event.Event:
				if w.queue.q.Queue(e2) {
					w.setNextFrame(time.Time{})
					w.updateAnimation()
				}
				w.updateCursor()
				w.out <- e
			}
			w.ack <- struct{}{}
		}
	}
}

func (w *Window) updateCursor() {
	if c := w.queue.q.Cursor(); c != w.cursor {
		w.cursor = c
		w.SetCursorName(c)
	}
}

func (q *queue) Events(k event.Tag) []event.Event {
	return q.q.Events(k)
}

var (
	// Windowed is the normal window mode with OS specific window decorations.
	Windowed Option = modeOption(windowed)
	// Fullscreen is the full screen window mode.
	Fullscreen Option = modeOption(fullscreen)
)

// WindowMode sets the window mode.
//
// Supported platforms are macOS, X11, Windows and JS.
func modeOption(mode windowMode) Option {
	return func(cnf *config) {
		cnf.WindowMode = &mode
	}
}

var (
	// AnyOrientation allows the window to be freely orientated.
	AnyOrientation Option = orientationOption(anyOrientation)
	// LandscapeOrientation constrains the window to landscape orientations.
	LandscapeOrientation Option = orientationOption(landscapeOrientation)
	// PortraitOrientation constrains the window to portrait orientations.
	PortraitOrientation Option = orientationOption(portraitOrientation)
)

// orientation sets the orientation of the app.
//
// Supported platforms are Android and JS.
func orientationOption(mode orientation) Option {
	return func(cnf *config) {
		cnf.Orientation = &mode
	}
}

// Title sets the title of the window.
func Title(t string) Option {
	return func(cnf *config) {
		cnf.Title = &t
	}
}

// Size sets the size of the window.
func Size(w, h unit.Value) Option {
	if w.V <= 0 {
		panic("width must be larger than or equal to 0")
	}
	if h.V <= 0 {
		panic("height must be larger than or equal to 0")
	}
	return func(cnf *config) {
		cnf.Size = &size{
			Width:  w,
			Height: h,
		}
	}
}

// MaxSize sets the maximum size of the window.
func MaxSize(w, h unit.Value) Option {
	if w.V <= 0 {
		panic("width must be larger than or equal to 0")
	}
	if h.V <= 0 {
		panic("height must be larger than or equal to 0")
	}
	return func(cnf *config) {
		cnf.MaxSize = &size{
			Width:  w,
			Height: h,
		}
	}
}

// MinSize sets the minimum size of the window.
func MinSize(w, h unit.Value) Option {
	if w.V <= 0 {
		panic("width must be larger than or equal to 0")
	}
	if h.V <= 0 {
		panic("height must be larger than or equal to 0")
	}
	return func(cnf *config) {
		cnf.MinSize = &size{
			Width:  w,
			Height: h,
		}
	}
}

// StatusColor sets the color of the Android status bar.
func StatusColor(color color.NRGBA) Option {
	return func(cnf *config) {
		cnf.StatusColor = &color
	}
}

// NavigationColor sets the color of the navigation bar on Android, or the address bar in browsers.
func NavigationColor(color color.NRGBA) Option {
	return func(cnf *config) {
		cnf.NavigationColor = &color
	}
}

// CustomRenderer controls whether the the window contents is
// rendered by the client. If true, no GPU context is created.
func CustomRenderer(custom bool) Option {
	return func(cnf *config) {
		cnf.CustomRenderer = custom
	}
}

func (driverEvent) ImplementsEvent() {}
