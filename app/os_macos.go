// SPDX-License-Identifier: Unlicense OR MIT

//go:build darwin && !ios
// +build darwin,!ios

package app

import (
	"errors"
	"image"
	"io"
	"runtime"
	"runtime/cgo"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gioui.org/internal/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/system"
	"gioui.org/io/transfer"
	"gioui.org/op"
	"gioui.org/unit"

	_ "gioui.org/internal/cocoainit"
)

/*
#cgo CFLAGS: -Werror -Wno-deprecated-declarations -fobjc-arc -x objective-c
#cgo LDFLAGS: -framework AppKit -framework QuartzCore

#include <AppKit/AppKit.h>

#define MOUSE_MOVE 1
#define MOUSE_UP 2
#define MOUSE_DOWN 3
#define MOUSE_SCROLL 4

__attribute__ ((visibility ("hidden"))) void gio_main(void);
__attribute__ ((visibility ("hidden"))) CFTypeRef gio_createView(int presentWithTrans);
__attribute__ ((visibility ("hidden"))) CFTypeRef gio_createWindow(CFTypeRef viewRef, CGFloat width, CGFloat height, CGFloat minWidth, CGFloat minHeight, CGFloat maxWidth, CGFloat maxHeight);
__attribute__ ((visibility ("hidden"))) void gio_viewSetHandle(CFTypeRef viewRef, uintptr_t handle);

static void writeClipboard(CFTypeRef str) {
	@autoreleasepool {
		NSString *s = (__bridge NSString *)str;
		NSPasteboard *p = NSPasteboard.generalPasteboard;
		[p declareTypes:@[NSPasteboardTypeString] owner:nil];
		[p setString:s forType:NSPasteboardTypeString];
	}
}

static CFTypeRef readClipboard(void) {
	@autoreleasepool {
		NSPasteboard *p = NSPasteboard.generalPasteboard;
		NSString *content = [p stringForType:NSPasteboardTypeString];
		return (__bridge_retained CFTypeRef)content;
	}
}

static CGFloat viewHeight(CFTypeRef viewRef) {
	@autoreleasepool {
		NSView *view = (__bridge NSView *)viewRef;
		return [view bounds].size.height;
	}
}

static CGFloat viewWidth(CFTypeRef viewRef) {
	@autoreleasepool {
		NSView *view = (__bridge NSView *)viewRef;
		return [view bounds].size.width;
	}
}

static CGFloat getScreenBackingScale(void) {
	@autoreleasepool {
		return [NSScreen.mainScreen backingScaleFactor];
	}
}

static CGFloat getViewBackingScale(CFTypeRef viewRef) {
	@autoreleasepool {
		NSView *view = (__bridge NSView *)viewRef;
		return [view.window backingScaleFactor];
	}
}

static void setNeedsDisplay(CFTypeRef viewRef) {
	@autoreleasepool {
		NSView *view = (__bridge NSView *)viewRef;
		[view setNeedsDisplay:YES];
	}
}

static NSPoint cascadeTopLeftFromPoint(CFTypeRef windowRef, NSPoint topLeft) {
	@autoreleasepool {
		NSWindow *window = (__bridge NSWindow *)windowRef;
		return [window cascadeTopLeftFromPoint:topLeft];
	}
}

static void makeKeyAndOrderFront(CFTypeRef windowRef) {
	@autoreleasepool {
		NSWindow *window = (__bridge NSWindow *)windowRef;
		[window makeKeyAndOrderFront:nil];
	}
}

static void toggleFullScreen(CFTypeRef windowRef) {
	@autoreleasepool {
		NSWindow *window = (__bridge NSWindow *)windowRef;
		[window toggleFullScreen:nil];
	}
}

static NSWindowStyleMask getWindowStyleMask(CFTypeRef windowRef) {
	@autoreleasepool {
		NSWindow *window = (__bridge NSWindow *)windowRef;
		return [window styleMask];
	}
}

static void setWindowStyleMask(CFTypeRef windowRef, NSWindowStyleMask mask) {
	@autoreleasepool {
		NSWindow *window = (__bridge NSWindow *)windowRef;
		window.styleMask = mask;
	}
}

static void setWindowTitleVisibility(CFTypeRef windowRef, NSWindowTitleVisibility state) {
	@autoreleasepool {
		NSWindow *window = (__bridge NSWindow *)windowRef;
		window.titleVisibility = state;
	}
}

static void setWindowTitlebarAppearsTransparent(CFTypeRef windowRef, int transparent) {
	@autoreleasepool {
		NSWindow *window = (__bridge NSWindow *)windowRef;
		window.titlebarAppearsTransparent = (BOOL)transparent;
	}
}

static void setWindowStandardButtonHidden(CFTypeRef windowRef, NSWindowButton btn, int hide) {
	@autoreleasepool {
		NSWindow *window = (__bridge NSWindow *)windowRef;
		[window standardWindowButton:btn].hidden = (BOOL)hide;
	}
}

static void performWindowDragWithEvent(CFTypeRef windowRef, CFTypeRef evt) {
	@autoreleasepool {
		NSWindow *window = (__bridge NSWindow *)windowRef;
		[window performWindowDragWithEvent:(__bridge NSEvent*)evt];
	}
}

static void closeWindow(CFTypeRef windowRef) {
	@autoreleasepool {
		NSWindow* window = (__bridge NSWindow *)windowRef;
		[window performClose:nil];
	}
}

static void setSize(CFTypeRef windowRef, CGFloat width, CGFloat height) {
	@autoreleasepool {
		NSWindow* window = (__bridge NSWindow *)windowRef;
		NSSize size = NSMakeSize(width, height);
		[window setContentSize:size];
	}
}

static void setMinSize(CFTypeRef windowRef, CGFloat width, CGFloat height) {
	@autoreleasepool {
		NSWindow* window = (__bridge NSWindow *)windowRef;
		window.contentMinSize = NSMakeSize(width, height);
	}
}

static void setMaxSize(CFTypeRef windowRef, CGFloat width, CGFloat height) {
	@autoreleasepool {
		NSWindow* window = (__bridge NSWindow *)windowRef;
		window.contentMaxSize = NSMakeSize(width, height);
	}
}

static void setScreenFrame(CFTypeRef windowRef, CGFloat x, CGFloat y, CGFloat w, CGFloat h) {
	@autoreleasepool {
		NSWindow* window = (__bridge NSWindow *)windowRef;
		NSRect r = NSMakeRect(x, y, w, h);
		[window setFrame:r display:YES];
	}
}

static void resetLayerFrame(CFTypeRef viewRef) {
	@autoreleasepool {
		NSView* view = (__bridge NSView *)viewRef;
		NSRect r = view.frame;
		view.layer.frame = r;
	}
}

static void hideWindow(CFTypeRef windowRef) {
	@autoreleasepool {
		NSWindow* window = (__bridge NSWindow *)windowRef;
		[window miniaturize:window];
	}
}

static void unhideWindow(CFTypeRef windowRef) {
	@autoreleasepool {
		NSWindow* window = (__bridge NSWindow *)windowRef;
		[window deminiaturize:window];
	}
}

static NSRect getScreenFrame(CFTypeRef windowRef) {
	@autoreleasepool {
		NSWindow* window = (__bridge NSWindow *)windowRef;
		return [[window screen] frame];
	}
}

static void setTitle(CFTypeRef windowRef, CFTypeRef titleRef) {
	@autoreleasepool {
		NSWindow *window = (__bridge NSWindow *)windowRef;
		window.title = (__bridge NSString *)titleRef;
	}
}

static int isWindowZoomed(CFTypeRef windowRef) {
	@autoreleasepool {
		NSWindow *window = (__bridge NSWindow *)windowRef;
		return window.zoomed ? 1 : 0;
	}
}

static void zoomWindow(CFTypeRef windowRef) {
	@autoreleasepool {
		NSWindow *window = (__bridge NSWindow *)windowRef;
		[window zoom:nil];
	}
}

static CFTypeRef layerForView(CFTypeRef viewRef) {
	@autoreleasepool {
		NSView *view = (__bridge NSView *)viewRef;
		return (__bridge CFTypeRef)view.layer;
	}
}

static CFTypeRef windowForView(CFTypeRef viewRef) {
	@autoreleasepool {
		NSView *view = (__bridge NSView *)viewRef;
		return (__bridge CFTypeRef)view.window;
	}
}

static void raiseWindow(CFTypeRef windowRef) {
	@autoreleasepool {
		NSRunningApplication *currentApp = [NSRunningApplication currentApplication];
		if (![currentApp isActive]) {
			[currentApp activateWithOptions:(NSApplicationActivateAllWindows | NSApplicationActivateIgnoringOtherApps)];
		}
		NSWindow* window = (__bridge NSWindow *)windowRef;
		[window makeKeyAndOrderFront:nil];
	}
}

static CFTypeRef createInputContext(CFTypeRef clientRef) {
	@autoreleasepool {
		id<NSTextInputClient> client = (__bridge id<NSTextInputClient>)clientRef;
		NSTextInputContext *ctx = [[NSTextInputContext alloc] initWithClient:client];
		return CFBridgingRetain(ctx);
	}
}

static void discardMarkedText(CFTypeRef viewRef) {
	@autoreleasepool {
		id<NSTextInputClient> view = (__bridge id<NSTextInputClient>)viewRef;
		NSTextInputContext *ctx = [NSTextInputContext currentInputContext];
		if (view == [ctx client]) {
			[ctx discardMarkedText];
		}
	}
}

static void invalidateCharacterCoordinates(CFTypeRef viewRef) {
	@autoreleasepool {
		id<NSTextInputClient> view = (__bridge id<NSTextInputClient>)viewRef;
		NSTextInputContext *ctx = [NSTextInputContext currentInputContext];
		if (view == [ctx client]) {
			[ctx invalidateCharacterCoordinates];
		}
	}
}
*/
import "C"

func init() {
	// Darwin requires that UI operations happen on the main thread only.
	runtime.LockOSThread()
}

// AppKitViewEvent notifies the client of changes to the window AppKit handles.
// The handles are retained until another AppKitViewEvent is sent.
type AppKitViewEvent struct {
	// View is a CFTypeRef for the NSView for the window.
	View uintptr
	// Layer is a CFTypeRef of the CALayer of View.
	Layer uintptr
}

type window struct {
	view        C.CFTypeRef
	w           *callbacks
	anim        bool
	visible     bool
	displayLink *displayLink
	// redraw is a single entry channel for making sure only one
	// display link redraw request is in flight.
	redraw      chan struct{}
	cursor      pointer.Cursor
	pointerBtns pointer.Buttons
	loop        *eventLoop

	scale  float32
	config Config
}

// launched is closed when applicationDidFinishLaunching is called.
var launched = make(chan struct{})

// nextTopLeft is the offset to use for the next window's call to
// cascadeTopLeftFromPoint.
var nextTopLeft C.NSPoint

func windowFor(h C.uintptr_t) *window {
	return cgo.Handle(h).Value().(*window)
}

func (w *window) contextView() C.CFTypeRef {
	return w.view
}

func (w *window) ReadClipboard() {
	cstr := C.readClipboard()
	if cstr != 0 {
		defer C.CFRelease(cstr)
	}
	content := nsstringToString(cstr)
	w.ProcessEvent(transfer.DataEvent{
		Type: "application/text",
		Open: func() io.ReadCloser {
			return io.NopCloser(strings.NewReader(content))
		},
	})
}

func (w *window) WriteClipboard(mime string, s []byte) {
	cstr := stringToNSString(string(s))
	defer C.CFRelease(cstr)
	C.writeClipboard(cstr)
}

func (w *window) updateWindowMode() {
	style := int(C.getWindowStyleMask(C.windowForView(w.view)))
	if style&C.NSWindowStyleMaskFullScreen != 0 {
		w.config.Mode = Fullscreen
	} else {
		w.config.Mode = Windowed
	}
	w.config.Decorated = style&C.NSWindowStyleMaskFullSizeContentView == 0
}

func (w *window) Configure(options []Option) {
	screenScale := float32(C.getScreenBackingScale())
	cfg := configFor(screenScale)
	prev := w.config
	w.updateWindowMode()
	cnf := w.config
	cnf.apply(cfg, options)
	window := C.windowForView(w.view)

	switch cnf.Mode {
	case Fullscreen:
		switch prev.Mode {
		case Fullscreen:
		case Minimized:
			C.unhideWindow(window)
			fallthrough
		default:
			w.config.Mode = Fullscreen
			C.toggleFullScreen(window)
		}
	case Minimized:
		switch prev.Mode {
		case Minimized, Fullscreen:
		default:
			w.config.Mode = Minimized
			C.hideWindow(window)
		}
	case Maximized:
		switch prev.Mode {
		case Fullscreen:
		case Minimized:
			C.unhideWindow(window)
			fallthrough
		default:
			w.config.Mode = Maximized
			w.setTitle(prev, cnf)
			if C.isWindowZoomed(window) == 0 {
				C.zoomWindow(window)
			}
		}
	case Windowed:
		switch prev.Mode {
		case Fullscreen:
			C.toggleFullScreen(window)
		case Minimized:
			C.unhideWindow(window)
		case Maximized:
			if C.isWindowZoomed(window) != 0 {
				C.zoomWindow(window)
			}
		}
		w.config.Mode = Windowed
		w.setTitle(prev, cnf)
		if prev.Size != cnf.Size {
			w.config.Size = cnf.Size
			cnf.Size = cnf.Size.Div(int(screenScale))
			C.setSize(window, C.CGFloat(cnf.Size.X), C.CGFloat(cnf.Size.Y))
		}
		if prev.MinSize != cnf.MinSize {
			w.config.MinSize = cnf.MinSize
			cnf.MinSize = cnf.MinSize.Div(int(screenScale))
			C.setMinSize(window, C.CGFloat(cnf.MinSize.X), C.CGFloat(cnf.MinSize.Y))
		}
		if prev.MaxSize != cnf.MaxSize {
			w.config.MaxSize = cnf.MaxSize
			cnf.MaxSize = cnf.MaxSize.Div(int(screenScale))
			C.setMaxSize(window, C.CGFloat(cnf.MaxSize.X), C.CGFloat(cnf.MaxSize.Y))
		}
	}
	if cnf.Decorated != prev.Decorated || cnf.HiddenMinimizeButton != prev.HiddenMinimizeButton {
		hidden := toInt(!cnf.Decorated || cnf.HiddenMinimizeButton)
		C.setWindowStandardButtonHidden(window, C.NSWindowMiniaturizeButton, hidden)
	}
	if cnf.Decorated != prev.Decorated || cnf.HiddenMaximizeButton != prev.HiddenMaximizeButton {
		hidden := toInt(!cnf.Decorated || cnf.HiddenMaximizeButton)
		C.setWindowStandardButtonHidden(window, C.NSWindowZoomButton, hidden)
	}
	if cnf.Decorated != prev.Decorated {
		w.config.Decorated = cnf.Decorated
		mask := C.getWindowStyleMask(window)
		style := C.NSWindowStyleMask(C.NSWindowStyleMaskTitled | C.NSWindowStyleMaskResizable | C.NSWindowStyleMaskMiniaturizable | C.NSWindowStyleMaskClosable)
		style = C.NSWindowStyleMaskFullSizeContentView
		mask &^= style
		barTrans := C.int(C.NO)
		titleVis := C.NSWindowTitleVisibility(C.NSWindowTitleVisible)
		if !cnf.Decorated {
			mask |= style
			barTrans = C.YES
			titleVis = C.NSWindowTitleHidden
		}

		C.setWindowTitlebarAppearsTransparent(window, barTrans)
		C.setWindowTitleVisibility(window, titleVis)
		C.setWindowStyleMask(window, mask)
		C.setWindowStandardButtonHidden(window, C.NSWindowCloseButton, barTrans)
		// When toggling the titlebar, the layer doesn't update its frame
		// until the next resize. Force it.
		C.resetLayerFrame(w.view)
	}
	w.ProcessEvent(ConfigEvent{Config: w.config})
}

func (w *window) setTitle(prev, cnf Config) {
	if prev.Title != cnf.Title {
		w.config.Title = cnf.Title
		title := stringToNSString(cnf.Title)
		defer C.CFRelease(title)
		C.setTitle(C.windowForView(w.view), title)
	}
}

func (w *window) Perform(acts system.Action) {
	window := C.windowForView(w.view)
	walkActions(acts, func(a system.Action) {
		switch a {
		case system.ActionCenter:
			r := C.getScreenFrame(window) // the screen size of the window
			screenScale := float32(C.getScreenBackingScale())
			sz := w.config.Size.Div(int(screenScale))
			x := (int(r.size.width) - sz.X) / 2
			y := (int(r.size.height) - sz.Y) / 2
			C.setScreenFrame(window, C.CGFloat(x), C.CGFloat(y), C.CGFloat(sz.X), C.CGFloat(sz.Y))
		case system.ActionRaise:
			C.raiseWindow(window)
		}
	})
	if acts&system.ActionClose != 0 {
		C.closeWindow(window)
	}
}

func (w *window) SetCursor(cursor pointer.Cursor) {
	w.cursor = windowSetCursor(w.cursor, cursor)
}

func (w *window) EditorStateChanged(old, new editorState) {
	if old.Selection.Range != new.Selection.Range || old.Snippet != new.Snippet {
		C.discardMarkedText(w.view)
		w.w.SetComposingRegion(key.Range{Start: -1, End: -1})
	}
	if old.Selection.Caret != new.Selection.Caret || old.Selection.Transform != new.Selection.Transform {
		C.invalidateCharacterCoordinates(w.view)
	}
}

func (w *window) ShowTextInput(show bool) {}

func (w *window) SetInputHint(_ key.InputHint) {}

func (w *window) SetAnimating(anim bool) {
	w.anim = anim
	if w.anim && w.visible {
		w.displayLink.Start()
	} else {
		w.displayLink.Stop()
	}
}

func (w *window) runOnMain(f func()) {
	runOnMain(func() {
		// Make sure the view is still valid. The window might've been closed
		// during the switch to the main thread.
		if w.view != 0 {
			f()
		}
	})
}

//export gio_onKeys
func gio_onKeys(h C.uintptr_t, cstr C.CFTypeRef, ti C.double, mods C.NSUInteger, keyDown C.bool) {
	str := nsstringToString(cstr)
	kmods := convertMods(mods)
	ks := key.Release
	if keyDown {
		ks = key.Press
	}
	w := windowFor(h)
	for _, k := range str {
		if n, ok := convertKey(k); ok {
			w.ProcessEvent(key.Event{
				Name:      n,
				Modifiers: kmods,
				State:     ks,
			})
		}
	}
}

//export gio_onText
func gio_onText(h C.uintptr_t, cstr C.CFTypeRef) {
	str := nsstringToString(cstr)
	w := windowFor(h)
	w.w.EditorInsert(str)
}

//export gio_onMouse
func gio_onMouse(h C.uintptr_t, evt C.CFTypeRef, cdir C.int, cbtn C.NSInteger, x, y, dx, dy C.CGFloat, ti C.double, mods C.NSUInteger) {
	w := windowFor(h)
	t := time.Duration(float64(ti)*float64(time.Second) + .5)
	xf, yf := float32(x)*w.scale, float32(y)*w.scale
	dxf, dyf := float32(dx)*w.scale, float32(dy)*w.scale
	pos := f32.Point{X: xf, Y: yf}
	var btn pointer.Buttons
	switch cbtn {
	case 0:
		btn = pointer.ButtonPrimary
	case 1:
		btn = pointer.ButtonSecondary
	case 2:
		btn = pointer.ButtonTertiary
	}
	var typ pointer.Kind
	switch cdir {
	case C.MOUSE_MOVE:
		typ = pointer.Move
	case C.MOUSE_UP:
		typ = pointer.Release
		w.pointerBtns &^= btn
	case C.MOUSE_DOWN:
		typ = pointer.Press
		w.pointerBtns |= btn
		act, ok := w.w.ActionAt(pos)
		if ok && w.config.Mode != Fullscreen {
			switch act {
			case system.ActionMove:
				C.performWindowDragWithEvent(C.windowForView(w.view), evt)
				return
			}
		}
	case C.MOUSE_SCROLL:
		typ = pointer.Scroll
	default:
		panic("invalid direction")
	}
	w.ProcessEvent(pointer.Event{
		Kind:      typ,
		Source:    pointer.Mouse,
		Time:      t,
		Buttons:   w.pointerBtns,
		Position:  pos,
		Scroll:    f32.Point{X: dxf, Y: dyf},
		Modifiers: convertMods(mods),
	})
}

//export gio_onDraw
func gio_onDraw(h C.uintptr_t) {
	w := windowFor(h)
	w.draw()
}

//export gio_onFocus
func gio_onFocus(h C.uintptr_t, focus C.int) {
	w := windowFor(h)
	w.SetCursor(w.cursor)
	w.config.Focused = focus == 1
	w.ProcessEvent(ConfigEvent{Config: w.config})
}

//export gio_onChangeScreen
func gio_onChangeScreen(h C.uintptr_t, did uint64) {
	w := windowFor(h)
	w.displayLink.SetDisplayID(did)
	C.setNeedsDisplay(w.view)
}

//export gio_hasMarkedText
func gio_hasMarkedText(h C.uintptr_t) C.int {
	w := windowFor(h)
	state := w.w.EditorState()
	if state.compose.Start != -1 {
		return 1
	}
	return 0
}

//export gio_markedRange
func gio_markedRange(h C.uintptr_t) C.NSRange {
	w := windowFor(h)
	state := w.w.EditorState()
	rng := state.compose
	start, end := rng.Start, rng.End
	if start == -1 {
		return C.NSMakeRange(C.NSNotFound, 0)
	}
	u16start := state.UTF16Index(start)
	return C.NSMakeRange(
		C.NSUInteger(u16start),
		C.NSUInteger(state.UTF16Index(end)-u16start),
	)
}

//export gio_selectedRange
func gio_selectedRange(h C.uintptr_t) C.NSRange {
	w := windowFor(h)
	state := w.w.EditorState()
	rng := state.Selection
	start, end := rng.Start, rng.End
	if start > end {
		start, end = end, start
	}
	u16start := state.UTF16Index(start)
	return C.NSMakeRange(
		C.NSUInteger(u16start),
		C.NSUInteger(state.UTF16Index(end)-u16start),
	)
}

//export gio_unmarkText
func gio_unmarkText(h C.uintptr_t) {
	w := windowFor(h)
	w.w.SetComposingRegion(key.Range{Start: -1, End: -1})
}

//export gio_setMarkedText
func gio_setMarkedText(h C.uintptr_t, cstr C.CFTypeRef, selRange C.NSRange, replaceRange C.NSRange) {
	w := windowFor(h)
	str := nsstringToString(cstr)
	state := w.w.EditorState()
	rng := state.compose
	if rng.Start == -1 {
		rng = state.Selection.Range
	}
	if replaceRange.location != C.NSNotFound {
		// replaceRange is relative to marked (or selected) text.
		offset := state.UTF16Index(rng.Start)
		start := state.RunesIndex(int(replaceRange.location) + offset)
		end := state.RunesIndex(int(replaceRange.location+replaceRange.length) + offset)
		rng = key.Range{
			Start: start,
			End:   end,
		}
	}
	w.w.EditorReplace(rng, str)
	comp := key.Range{
		Start: rng.Start,
		End:   rng.Start + utf8.RuneCountInString(str),
	}
	w.w.SetComposingRegion(comp)

	sel := key.Range{Start: comp.End, End: comp.End}
	if selRange.location != C.NSNotFound {
		// selRange is relative to inserted text.
		offset := state.UTF16Index(rng.Start)
		start := state.RunesIndex(int(selRange.location) + offset)
		end := state.RunesIndex(int(selRange.location+selRange.length) + offset)
		sel = key.Range{
			Start: start,
			End:   end,
		}
	}
	w.w.SetEditorSelection(sel)
}

//export gio_substringForProposedRange
func gio_substringForProposedRange(h C.uintptr_t, crng C.NSRange, actual C.NSRangePointer) C.CFTypeRef {
	w := windowFor(h)
	state := w.w.EditorState()
	start, end := state.Snippet.Start, state.Snippet.End
	if start > end {
		start, end = end, start
	}
	rng := key.Range{
		Start: state.RunesIndex(int(crng.location)),
		End:   state.RunesIndex(int(crng.location + crng.length)),
	}
	if rng.Start < start || end < rng.End {
		w.w.SetEditorSnippet(rng)
	}
	u16start := state.UTF16Index(start)
	actual.location = C.NSUInteger(u16start)
	actual.length = C.NSUInteger(state.UTF16Index(end) - u16start)
	return stringToNSString(state.Snippet.Text)
}

//export gio_insertText
func gio_insertText(h C.uintptr_t, cstr C.CFTypeRef, crng C.NSRange) {
	w := windowFor(h)
	state := w.w.EditorState()
	rng := state.compose
	if rng.Start == -1 {
		rng = state.Selection.Range
	}
	if crng.location != C.NSNotFound {
		rng = key.Range{
			Start: state.RunesIndex(int(crng.location)),
			End:   state.RunesIndex(int(crng.location + crng.length)),
		}
	}
	str := nsstringToString(cstr)
	w.w.EditorReplace(rng, str)
	w.w.SetComposingRegion(key.Range{Start: -1, End: -1})
	start := rng.Start
	if rng.End < start {
		start = rng.End
	}
	pos := start + utf8.RuneCountInString(str)
	w.w.SetEditorSelection(key.Range{Start: pos, End: pos})
}

//export gio_characterIndexForPoint
func gio_characterIndexForPoint(h C.uintptr_t, p C.NSPoint) C.NSUInteger {
	return C.NSNotFound
}

//export gio_firstRectForCharacterRange
func gio_firstRectForCharacterRange(h C.uintptr_t, crng C.NSRange, actual C.NSRangePointer) C.NSRect {
	w := windowFor(h)
	state := w.w.EditorState()
	sel := state.Selection
	u16start := state.UTF16Index(sel.Start)
	actual.location = C.NSUInteger(u16start)
	actual.length = 0
	// Transform to NSView local coordinates (lower left origin, undo backing scale).
	scale := 1. / float32(C.getViewBackingScale(w.view))
	height := float32(C.viewHeight(w.view))
	local := f32.Affine2D{}.Scale(f32.Pt(0, 0), f32.Pt(scale, -scale)).Offset(f32.Pt(0, height))
	t := local.Mul(sel.Transform)
	bounds := f32.Rectangle{
		Min: t.Transform(sel.Pos.Sub(f32.Pt(0, sel.Ascent))),
		Max: t.Transform(sel.Pos.Add(f32.Pt(0, sel.Descent))),
	}.Canon()
	sz := bounds.Size()
	return C.NSMakeRect(
		C.CGFloat(bounds.Min.X), C.CGFloat(bounds.Min.Y),
		C.CGFloat(sz.X), C.CGFloat(sz.Y),
	)
}

func (w *window) draw() {
	select {
	case <-w.redraw:
	default:
	}
	w.visible = true
	if w.anim {
		w.SetAnimating(w.anim)
	}
	w.scale = float32(C.getViewBackingScale(w.view))
	wf, hf := float32(C.viewWidth(w.view)), float32(C.viewHeight(w.view))
	sz := image.Point{
		X: int(wf*w.scale + .5),
		Y: int(hf*w.scale + .5),
	}
	if sz != w.config.Size {
		w.config.Size = sz
		w.ProcessEvent(ConfigEvent{Config: w.config})
	}
	if sz.X == 0 || sz.Y == 0 {
		return
	}
	cfg := configFor(w.scale)
	w.ProcessEvent(frameEvent{
		FrameEvent: FrameEvent{
			Now:    time.Now(),
			Size:   w.config.Size,
			Metric: cfg,
		},
		Sync: true,
	})
}

func (w *window) ProcessEvent(e event.Event) {
	w.w.ProcessEvent(e)
	w.loop.FlushEvents()
}

func (w *window) Event() event.Event {
	return w.loop.Event()
}

func (w *window) Invalidate() {
	w.loop.Invalidate()
}

func (w *window) Run(f func()) {
	w.loop.Run(f)
}

func (w *window) Frame(frame *op.Ops) {
	w.loop.Frame(frame)
}

func configFor(scale float32) unit.Metric {
	return unit.Metric{
		PxPerDp: scale,
		PxPerSp: scale,
	}
}

//export gio_onAttached
func gio_onAttached(h C.uintptr_t, attached C.int) {
	w := windowFor(h)
	if attached != 0 {
		layer := C.layerForView(w.view)
		w.ProcessEvent(AppKitViewEvent{View: uintptr(w.view), Layer: uintptr(layer)})
	} else {
		w.ProcessEvent(AppKitViewEvent{})
		w.visible = false
		w.SetAnimating(w.anim)
	}
}

//export gio_onDestroy
func gio_onDestroy(h C.uintptr_t) {
	w := windowFor(h)
	w.ProcessEvent(DestroyEvent{})
	w.displayLink.Close()
	w.displayLink = nil
	cgo.Handle(h).Delete()
	w.view = 0
}

//export gio_onHide
func gio_onHide(h C.uintptr_t) {
	w := windowFor(h)
	w.visible = false
	w.SetAnimating(w.anim)
}

//export gio_onShow
func gio_onShow(h C.uintptr_t) {
	w := windowFor(h)
	w.draw()
}

//export gio_onFullscreen
func gio_onFullscreen(h C.uintptr_t) {
	w := windowFor(h)
	w.config.Mode = Fullscreen
	w.ProcessEvent(ConfigEvent{Config: w.config})
}

//export gio_onWindowed
func gio_onWindowed(h C.uintptr_t) {
	w := windowFor(h)
	w.config.Mode = Windowed
	w.ProcessEvent(ConfigEvent{Config: w.config})
}

//export gio_onFinishLaunching
func gio_onFinishLaunching() {
	close(launched)
}

func newWindow(win *callbacks, options []Option) {
	<-launched
	res := make(chan struct{})
	runOnMain(func() {
		w := &window{
			redraw: make(chan struct{}, 1),
			w:      win,
		}
		w.loop = newEventLoop(w.w, w.wakeup)
		win.SetDriver(w)
		res <- struct{}{}
		var cnf Config
		cnf.apply(unit.Metric{}, options)
		if err := w.init(cnf.CustomRenderer); err != nil {
			w.ProcessEvent(DestroyEvent{Err: err})
			return
		}
		window := C.gio_createWindow(w.view, 0, 0, 0, 0, 0, 0)
		// Release our reference now that the NSWindow has it.
		C.CFRelease(w.view)
		w.updateWindowMode()
		w.Configure(options)
		if nextTopLeft.x == 0 && nextTopLeft.y == 0 {
			// cascadeTopLeftFromPoint treats (0, 0) as a no-op,
			// and just returns the offset we need for the first window.
			nextTopLeft = C.cascadeTopLeftFromPoint(window, nextTopLeft)
		}
		nextTopLeft = C.cascadeTopLeftFromPoint(window, nextTopLeft)
		// makeKeyAndOrderFront assumes ownership of our window reference.
		C.makeKeyAndOrderFront(window)
	})
	<-res
}

func (w *window) init(customRenderer bool) error {
	presentWithTrans := 1
	if customRenderer {
		presentWithTrans = 0
	}
	view := C.gio_createView(C.int(presentWithTrans))
	if view == 0 {
		return errors.New("newOSWindow: failed to create view")
	}
	scale := float32(C.getViewBackingScale(view))
	w.scale = scale
	dl, err := newDisplayLink(func() {
		select {
		case w.redraw <- struct{}{}:
		default:
			return
		}
		w.runOnMain(func() {
			if w.visible {
				C.setNeedsDisplay(w.view)
			}
		})
	})
	w.displayLink = dl
	if err != nil {
		C.CFRelease(view)
		return err
	}
	C.gio_viewSetHandle(view, C.uintptr_t(cgo.NewHandle(w)))
	w.view = view
	return nil
}

func osMain() {
	if !isMainThread() {
		panic("app.Main must run on the main goroutine")
	}
	C.gio_main()
}

func convertKey(k rune) (key.Name, bool) {
	var n key.Name
	switch k {
	case 0x1b:
		n = key.NameEscape
	case C.NSLeftArrowFunctionKey:
		n = key.NameLeftArrow
	case C.NSRightArrowFunctionKey:
		n = key.NameRightArrow
	case C.NSUpArrowFunctionKey:
		n = key.NameUpArrow
	case C.NSDownArrowFunctionKey:
		n = key.NameDownArrow
	case 0xd:
		n = key.NameReturn
	case 0x3:
		n = key.NameEnter
	case C.NSHomeFunctionKey:
		n = key.NameHome
	case C.NSEndFunctionKey:
		n = key.NameEnd
	case 0x7f:
		n = key.NameDeleteBackward
	case C.NSDeleteFunctionKey:
		n = key.NameDeleteForward
	case C.NSPageUpFunctionKey:
		n = key.NamePageUp
	case C.NSPageDownFunctionKey:
		n = key.NamePageDown
	case C.NSF1FunctionKey:
		n = key.NameF1
	case C.NSF2FunctionKey:
		n = key.NameF2
	case C.NSF3FunctionKey:
		n = key.NameF3
	case C.NSF4FunctionKey:
		n = key.NameF4
	case C.NSF5FunctionKey:
		n = key.NameF5
	case C.NSF6FunctionKey:
		n = key.NameF6
	case C.NSF7FunctionKey:
		n = key.NameF7
	case C.NSF8FunctionKey:
		n = key.NameF8
	case C.NSF9FunctionKey:
		n = key.NameF9
	case C.NSF10FunctionKey:
		n = key.NameF10
	case C.NSF11FunctionKey:
		n = key.NameF11
	case C.NSF12FunctionKey:
		n = key.NameF12
	case 0x09, 0x19:
		n = key.NameTab
	case 0x20:
		n = key.NameSpace
	default:
		k = unicode.ToUpper(k)
		if !unicode.IsPrint(k) {
			return "", false
		}
		n = key.Name(k)
	}
	return n, true
}

func convertMods(mods C.NSUInteger) key.Modifiers {
	var kmods key.Modifiers
	if mods&C.NSAlternateKeyMask != 0 {
		kmods |= key.ModAlt
	}
	if mods&C.NSControlKeyMask != 0 {
		kmods |= key.ModCtrl
	}
	if mods&C.NSCommandKeyMask != 0 {
		kmods |= key.ModCommand
	}
	if mods&C.NSShiftKeyMask != 0 {
		kmods |= key.ModShift
	}
	return kmods
}

// toInt golang bool to C.int
func toInt(v bool) C.int {
	if v {
		return C.int(C.YES)
	}
	return C.int(C.NO)
}

func (AppKitViewEvent) implementsViewEvent() {}
func (AppKitViewEvent) ImplementsEvent()     {}
func (a AppKitViewEvent) Valid() bool {
	return a != (AppKitViewEvent{})
}
