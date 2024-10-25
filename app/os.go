// SPDX-License-Identifier: Unlicense OR MIT

package app

import (
	"errors"
	"image"
	"image/color"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/op"

	"gioui.org/gpu"
	"gioui.org/io/pointer"
	"gioui.org/io/system"
	"gioui.org/unit"
)

// errOutOfDate is reported when the GPU surface dimensions or properties no
// longer match the window.
var errOutOfDate = errors.New("app: GPU surface out of date")

// Config describes a Window configuration.
type Config struct {
	// Size is the window dimensions (Width, Height).
	Size image.Point
	// MaxSize is the window maximum allowed dimensions.
	MaxSize image.Point
	// MinSize is the window minimum allowed dimensions.
	MinSize image.Point
	// Title is the window title displayed in its decoration bar.
	Title string
	// WindowMode is the window mode.
	Mode WindowMode
	// StatusColor is the color of the Android status bar.
	StatusColor color.NRGBA
	// NavigationColor is the color of the navigation bar
	// on Android, or the address bar in browsers.
	NavigationColor color.NRGBA
	// Orientation is the current window orientation.
	Orientation Orientation
	// CustomRenderer is true when the window content is rendered by the
	// client.
	CustomRenderer bool
	// Decorated reports whether window decorations are provided automatically.
	Decorated bool

	// HiddenMinimizeButton hide the window's minimize button (only works for windows and macOS)
	HiddenMinimizeButton bool
	// HiddenMaximizeButton hide the window's maximize button (only works for windows and macOS)
	HiddenMaximizeButton bool

	// Focused reports whether has the keyboard focus.
	Focused bool
	// decoHeight is the height of the fallback decoration for platforms such
	// as Wayland that may need fallback client-side decorations.
	decoHeight unit.Dp
}

// ConfigEvent is sent whenever the configuration of a Window changes.
type ConfigEvent struct {
	Config Config
}

func (c *Config) apply(m unit.Metric, options []Option) {
	for _, o := range options {
		o(m, c)
	}
}

type wakeupEvent struct{}

// WindowMode is the window mode (WindowMode.Option sets it).
// Note that mode can be changed programatically as well as by the user
// clicking on the minimize/maximize buttons on the window's title bar.
type WindowMode uint8

const (
	// Windowed is the normal window mode with OS specific window decorations.
	Windowed WindowMode = iota
	// Fullscreen is the full screen window mode.
	Fullscreen
	// Minimized is for systems where the window can be minimized to an icon.
	Minimized
	// Maximized is for systems where the window can be made to fill the available monitor area.
	Maximized
)

// Option changes the mode of a Window.
func (m WindowMode) Option() Option {
	return func(_ unit.Metric, cnf *Config) {
		cnf.Mode = m
	}
}

// String returns the mode name.
func (m WindowMode) String() string {
	switch m {
	case Windowed:
		return "windowed"
	case Fullscreen:
		return "fullscreen"
	case Minimized:
		return "minimized"
	case Maximized:
		return "maximized"
	}
	return ""
}

// Orientation is the orientation of the app (Orientation.Option sets it).
//
// Supported platforms are Android and JS.
type Orientation uint8

const (
	// AnyOrientation allows the window to be freely orientated.
	AnyOrientation Orientation = iota
	// LandscapeOrientation constrains the window to landscape orientations.
	LandscapeOrientation
	// PortraitOrientation constrains the window to portrait orientations.
	PortraitOrientation
)

func (o Orientation) Option() Option {
	return func(_ unit.Metric, cnf *Config) {
		cnf.Orientation = o
	}
}

func (o Orientation) String() string {
	switch o {
	case AnyOrientation:
		return "any"
	case LandscapeOrientation:
		return "landscape"
	case PortraitOrientation:
		return "portrait"
	}
	return ""
}

// eventLoop implements the functionality required for drivers where
// window event loops must run on a separate thread.
type eventLoop struct {
	win *callbacks
	// wakeup is the callback to wake up the event loop.
	wakeup func()
	// driverFuncs is a channel of functions to run the next
	// time the window loop waits for events.
	driverFuncs chan func()
	// invalidates is notified when an invalidate is requested by the client.
	invalidates chan struct{}
	// immediateInvalidates is an optimistic invalidates that doesn't require a wakeup.
	immediateInvalidates chan struct{}
	// events is where the platform backend delivers events bound for the
	// user program.
	events   chan event.Event
	frames   chan *op.Ops
	frameAck chan struct{}
	// delivering avoids re-entrant event delivery.
	delivering bool
}

type frameEvent struct {
	FrameEvent

	Sync bool
}

type context interface {
	API() gpu.API
	RenderTarget() (gpu.RenderTarget, error)
	Present() error
	Refresh() error
	Release()
	Lock() error
	Unlock()
}

// driver is the interface for the platform implementation
// of a window.
type driver interface {
	// DriverName get driver anme
	DriverName() string
	// Event blocks until an event is available and returns it.
	Event() event.Event
	// Invalidate requests a FrameEvent.
	Invalidate()
	// SetAnimating sets the animation flag. When the window is animating,
	// FrameEvents are delivered as fast as the display can handle them.
	SetAnimating(anim bool)
	// ShowTextInput updates the virtual keyboard state.
	ShowTextInput(show bool)
	SetInputHint(mode key.InputHint)
	NewContext() (context, error)
	// ReadClipboard requests the clipboard content.
	ReadClipboard()
	// WriteClipboard requests a clipboard write.
	WriteClipboard(mime string, s []byte)
	// Configure the window.
	Configure([]Option)
	// SetCursor updates the current cursor to name.
	SetCursor(cursor pointer.Cursor)
	// Wakeup wakes up the event loop and sends a WakeupEvent.
	// Wakeup()
	// Perform actions on the window.
	Perform(system.Action)
	// EditorStateChanged notifies the driver that the editor state changed.
	EditorStateChanged(old, new editorState)
	// Run a function on the window thread.
	Run(f func())
	// Frame receives a frame.
	Frame(frame *op.Ops)
	// ProcessEvent processes an event.
	ProcessEvent(e event.Event)
}

type windowRendezvous struct {
	in      chan windowAndConfig
	out     chan windowAndConfig
	windows chan struct{}
}

type windowAndConfig struct {
	window  *callbacks
	options []Option
}

func newWindowRendezvous() *windowRendezvous {
	wr := &windowRendezvous{
		in:      make(chan windowAndConfig),
		out:     make(chan windowAndConfig),
		windows: make(chan struct{}),
	}
	go func() {
		in := wr.in
		var window windowAndConfig
		var out chan windowAndConfig
		for {
			select {
			case w := <-in:
				window = w
				out = wr.out
			case out <- window:
			}
		}
	}()
	return wr
}

func newEventLoop(w *callbacks, wakeup func()) *eventLoop {
	return &eventLoop{
		win:                  w,
		wakeup:               wakeup,
		events:               make(chan event.Event),
		invalidates:          make(chan struct{}, 1),
		immediateInvalidates: make(chan struct{}),
		frames:               make(chan *op.Ops),
		frameAck:             make(chan struct{}),
		driverFuncs:          make(chan func(), 1),
	}
}

// Frame receives a frame and waits for its processing. It is called by
// the client goroutine.
func (e *eventLoop) Frame(frame *op.Ops) {
	e.frames <- frame
	<-e.frameAck
}

// Event returns the next available event. It is called by the client
// goroutine.
func (e *eventLoop) Event() event.Event {
	for {
		evt := <-e.events
		// Receiving a flushEvent indicates to the platform backend that
		// all previous events have been processed by the user program.
		if _, ok := evt.(flushEvent); ok {
			continue
		}
		return evt
	}
}

// Invalidate requests invalidation of the window. It is called by the client
// goroutine.
func (e *eventLoop) Invalidate() {
	select {
	case e.immediateInvalidates <- struct{}{}:
		// The event loop was waiting, no need for a wakeup.
	case e.invalidates <- struct{}{}:
		// The event loop is sleeping, wake it up.
		e.wakeup()
	default:
		// A redraw is pending.
	}
}

// Run f in the window loop thread. It is called by the client goroutine.
func (e *eventLoop) Run(f func()) {
	e.driverFuncs <- f
	e.wakeup()
}

// FlushEvents delivers pending events to the client.
func (e *eventLoop) FlushEvents() {
	if e.delivering {
		return
	}
	e.delivering = true
	defer func() { e.delivering = false }()
	for {
		evt, ok := e.win.nextEvent()
		if !ok {
			break
		}
		e.deliverEvent(evt)
	}
}

func (e *eventLoop) deliverEvent(evt event.Event) {
	var frames <-chan *op.Ops
	for {
		select {
		case f := <-e.driverFuncs:
			f()
		case frame := <-frames:
			// The client called FrameEvent.Frame.
			frames = nil
			e.win.ProcessFrame(frame, e.frameAck)
		case e.events <- evt:
			switch evt.(type) {
			case flushEvent, DestroyEvent:
				// DestroyEvents are not flushed.
				return
			case FrameEvent:
				frames = e.frames
			}
			evt = theFlushEvent
		case <-e.invalidates:
			e.win.Invalidate()
		case <-e.immediateInvalidates:
			e.win.Invalidate()
		}
	}
}

func (e *eventLoop) Wakeup() {
	for {
		select {
		case f := <-e.driverFuncs:
			f()
		case <-e.invalidates:
			e.win.Invalidate()
		case <-e.immediateInvalidates:
			e.win.Invalidate()
		default:
			return
		}
	}
}

func walkActions(actions system.Action, do func(system.Action)) {
	for a := system.Action(1); actions != 0; a <<= 1 {
		if actions&a != 0 {
			actions &^= a
			do(a)
		}
	}
}

func (wakeupEvent) ImplementsEvent() {}
func (ConfigEvent) ImplementsEvent() {}
