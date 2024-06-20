// SPDX-License-Identifier: Unlicense OR MIT

package app

import (
	"errors"
	"fmt"
	"gioui.org/io/transfer"
	syscall "golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"image"
	"io"
	"net/url"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
	"unsafe"

	"gioui.org/app/internal/windows"
	"gioui.org/op"
	"gioui.org/unit"
	gowindows "golang.org/x/sys/windows"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/system"
)

type Win32ViewEvent struct {
	HWND uintptr
}

type window struct {
	hwnd        syscall.Handle
	hdc         syscall.Handle
	w           *callbacks
	pointerBtns pointer.Buttons

	// cursorIn tracks whether the cursor was inside the window according
	// to the most recent WM_SETCURSOR.
	cursorIn bool
	cursor   syscall.Handle

	// placement saves the previous window position when in full screen mode.
	placement *windows.WindowPlacement

	animating bool

	borderSize image.Point
	config     Config
	loop       *eventLoop

	// invMu avoids the race between destroying the window and Invalidate.
	invMu sync.Mutex
}

const _WM_WAKEUP = windows.WM_USER + iota

const copyDataURLType = 0xffffff00

type gpuAPI struct {
	priority    int
	initializer func(w *window) (context, error)
}

// drivers is the list of potential Context implementations.
var drivers []gpuAPI

// winMap maps win32 HWNDs to *windows.
var winMap sync.Map

// iconID is the ID of the icon in the resource file.
const iconID = 1

var resources struct {
	once sync.Once
	// handle is the module handle from GetModuleHandle.
	handle syscall.Handle
	// class is the Gio window class from RegisterClassEx.
	class uint16
	// cursor is the arrow cursor resource.
	cursor syscall.Handle
}

func osMain() {
	select {}
}

func newWindow(win *callbacks, options []Option) {
	done := make(chan struct{})
	go func() {
		// GetMessage and PeekMessage can filter on a window HWND, but
		// then thread-specific messages such as WM_QUIT are ignored.
		// Instead lock the thread so window messages arrive through
		// unfiltered GetMessage calls.
		runtime.LockOSThread()

		w := &window{
			w: win,
		}
		w.loop = newEventLoop(w.w, w.wakeup)
		w.w.SetDriver(w)
		err := w.init()
		done <- struct{}{}
		if err != nil {
			w.ProcessEvent(DestroyEvent{Err: err})
			return
		}
		winMap.Store(w.hwnd, w)
		defer winMap.Delete(w.hwnd)
		w.ProcessEvent(Win32ViewEvent{HWND: uintptr(w.hwnd)})
		if u := startupURI(); u != "" {
			w.onOpenURI(u)
		}
		w.Configure(options)
		windows.SetForegroundWindow(w.hwnd)
		windows.SetFocus(w.hwnd)
		// Since the window class for the cursor is null,
		// set it here to show the cursor.
		w.SetCursor(pointer.CursorDefault)
		w.runLoop()
	}()
	<-done
}

// initResources initializes the resources global.
func initResources() error {
	windows.SetProcessDPIAware()
	hInst, err := windows.GetModuleHandle()
	if err != nil {
		return err
	}
	resources.handle = hInst
	c, err := windows.LoadCursor(windows.IDC_ARROW)
	if err != nil {
		return err
	}
	resources.cursor = c
	icon, _ := windows.LoadImage(hInst, iconID, windows.IMAGE_ICON, 0, 0, windows.LR_DEFAULTSIZE|windows.LR_SHARED)
	wcls := windows.WndClassEx{
		CbSize:        uint32(unsafe.Sizeof(windows.WndClassEx{})),
		Style:         windows.CS_HREDRAW | windows.CS_VREDRAW | windows.CS_OWNDC,
		LpfnWndProc:   syscall.NewCallback(windowProc),
		HInstance:     hInst,
		HIcon:         icon,
		LpszClassName: syscall.StringToUTF16Ptr(ID),
	}
	cls, err := windows.RegisterClassEx(&wcls)
	if err != nil {
		return err
	}
	resources.class = cls
	return nil
}

const dwExStyle = windows.WS_EX_APPWINDOW | windows.WS_EX_WINDOWEDGE

func (w *window) init() error {
	var resErr error
	resources.once.Do(func() {
		resErr = initResources()
	})
	if resErr != nil {
		return resErr
	}
	const dwStyle = windows.WS_OVERLAPPEDWINDOW

	hwnd, err := windows.CreateWindowEx(
		dwExStyle,
		resources.class,
		"",
		dwStyle|windows.WS_CLIPSIBLINGS|windows.WS_CLIPCHILDREN,
		windows.CW_USEDEFAULT, windows.CW_USEDEFAULT,
		windows.CW_USEDEFAULT, windows.CW_USEDEFAULT,
		0,
		0,
		resources.handle,
		0)
	if err != nil {
		return err
	}
	w.hdc, err = windows.GetDC(hwnd)
	if err != nil {
		windows.DestroyWindow(hwnd)
		return err
	}
	w.hwnd = hwnd
	return nil
}

// update() handles changes done by the user, and updates the configuration.
// It reads the window style and size/position and updates w.config.
// If anything has changed it emits a ConfigEvent to notify the application.
func (w *window) update() {
	cr := windows.GetClientRect(w.hwnd)
	w.config.Size = image.Point{
		X: int(cr.Right - cr.Left),
		Y: int(cr.Bottom - cr.Top),
	}

	w.borderSize = image.Pt(
		windows.GetSystemMetrics(windows.SM_CXSIZEFRAME),
		windows.GetSystemMetrics(windows.SM_CYSIZEFRAME),
	)
	w.ProcessEvent(ConfigEvent{Config: w.config})
}

func windowProc(hwnd syscall.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	win, exists := winMap.Load(hwnd)
	if !exists {
		return windows.DefWindowProc(hwnd, msg, wParam, lParam)
	}

	w := win.(*window)

	switch msg {
	case windows.WM_UNICHAR:
		if wParam == windows.UNICODE_NOCHAR {
			// Tell the system that we accept WM_UNICHAR messages.
			return windows.TRUE
		}
		fallthrough
	case windows.WM_CHAR:
		if r := rune(wParam); unicode.IsPrint(r) {
			w.w.EditorInsert(string(r))
		}
		// The message is processed.
		return windows.TRUE
	case windows.WM_DPICHANGED:
		// Let Windows know we're prepared for runtime DPI changes.
		return windows.TRUE
	case windows.WM_ERASEBKGND:
		// Avoid flickering between GPU content and background color.
		return windows.TRUE
	case windows.WM_KEYDOWN, windows.WM_KEYUP, windows.WM_SYSKEYDOWN, windows.WM_SYSKEYUP:
		if n, ok := convertKeyCode(wParam); ok {
			e := key.Event{
				Name:      n,
				Modifiers: getModifiers(),
				State:     key.Press,
			}
			if msg == windows.WM_KEYUP || msg == windows.WM_SYSKEYUP {
				e.State = key.Release
			}

			w.ProcessEvent(e)

			if (wParam == windows.VK_F10) && (msg == windows.WM_SYSKEYDOWN || msg == windows.WM_SYSKEYUP) {
				// Reserve F10 for ourselves, and don't let it open the system menu. Other Windows programs
				// such as cmd.exe and graphical debuggers also reserve F10.
				return 0
			}
		}
	case windows.WM_LBUTTONDOWN:
		w.pointerButton(pointer.ButtonPrimary, true, lParam, getModifiers())
	case windows.WM_LBUTTONUP:
		w.pointerButton(pointer.ButtonPrimary, false, lParam, getModifiers())
	case windows.WM_RBUTTONDOWN:
		w.pointerButton(pointer.ButtonSecondary, true, lParam, getModifiers())
	case windows.WM_RBUTTONUP:
		w.pointerButton(pointer.ButtonSecondary, false, lParam, getModifiers())
	case windows.WM_MBUTTONDOWN:
		w.pointerButton(pointer.ButtonTertiary, true, lParam, getModifiers())
	case windows.WM_MBUTTONUP:
		w.pointerButton(pointer.ButtonTertiary, false, lParam, getModifiers())
	case windows.WM_CANCELMODE:
		w.ProcessEvent(pointer.Event{
			Kind: pointer.Cancel,
		})
	case windows.WM_SETFOCUS:
		w.config.Focused = true
		w.ProcessEvent(ConfigEvent{Config: w.config})
	case windows.WM_KILLFOCUS:
		w.config.Focused = false
		w.ProcessEvent(ConfigEvent{Config: w.config})
	case windows.WM_NCHITTEST:
		if w.config.Decorated {
			// Let the system handle it.
			break
		}
		x, y := coordsFromlParam(lParam)
		np := windows.Point{X: int32(x), Y: int32(y)}
		windows.ScreenToClient(w.hwnd, &np)
		return w.hitTest(int(np.X), int(np.Y))
	case windows.WM_MOUSEMOVE:
		x, y := coordsFromlParam(lParam)
		p := f32.Point{X: float32(x), Y: float32(y)}
		w.ProcessEvent(pointer.Event{
			Kind:      pointer.Move,
			Source:    pointer.Mouse,
			Position:  p,
			Buttons:   w.pointerBtns,
			Time:      windows.GetMessageTime(),
			Modifiers: getModifiers(),
		})
	case windows.WM_MOUSEWHEEL:
		w.scrollEvent(wParam, lParam, false, getModifiers())
	case windows.WM_MOUSEHWHEEL:
		w.scrollEvent(wParam, lParam, true, getModifiers())
	case windows.WM_DESTROY:
		w.ProcessEvent(Win32ViewEvent{})
		w.ProcessEvent(DestroyEvent{})
		if w.hdc != 0 {
			windows.ReleaseDC(w.hdc)
			w.hdc = 0
		}
		w.invMu.Lock()
		// The system destroys the HWND for us.
		w.hwnd = 0
		w.invMu.Unlock()
		windows.PostQuitMessage(0)
	case windows.WM_NCCALCSIZE:
		if w.config.Decorated {
			// Let Windows handle decorations.
			break
		}
		// No client areas; we draw decorations ourselves.
		if wParam != 1 {
			return 0
		}
		// lParam contains an NCCALCSIZE_PARAMS for us to adjust.
		place := windows.GetWindowPlacement(w.hwnd)
		if !place.IsMaximized() {
			// Nothing do adjust.
			return 0
		}
		// Adjust window position to avoid the extra padding in maximized
		// state. See https://devblogs.microsoft.com/oldnewthing/20150304-00/?p=44543.
		// Note that trying to do the adjustment in WM_GETMINMAXINFO is ignored by Windows.
		szp := (*windows.NCCalcSizeParams)(unsafe.Pointer(lParam))
		mi := windows.GetMonitorInfo(w.hwnd)
		szp.Rgrc[0] = mi.WorkArea
		return 0
	case windows.WM_PAINT:
		w.draw(true)
	case windows.WM_SIZE:
		w.update()
		switch wParam {
		case windows.SIZE_MINIMIZED:
			w.config.Mode = Minimized
		case windows.SIZE_MAXIMIZED:
			w.config.Mode = Maximized
		case windows.SIZE_RESTORED:
			if w.config.Mode != Fullscreen {
				w.config.Mode = Windowed
			}
		}
	case windows.WM_GETMINMAXINFO:
		mm := (*windows.MinMaxInfo)(unsafe.Pointer(lParam))
		var bw, bh int32
		if w.config.Decorated {
			r := windows.GetWindowRect(w.hwnd)
			cr := windows.GetClientRect(w.hwnd)
			bw = r.Right - r.Left - (cr.Right - cr.Left)
			bh = r.Bottom - r.Top - (cr.Bottom - cr.Top)
		}
		if p := w.config.MinSize; p.X > 0 || p.Y > 0 {
			mm.PtMinTrackSize = windows.Point{
				X: int32(p.X) + bw,
				Y: int32(p.Y) + bh,
			}
		}
		if p := w.config.MaxSize; p.X > 0 || p.Y > 0 {
			mm.PtMaxTrackSize = windows.Point{
				X: int32(p.X) + bw,
				Y: int32(p.Y) + bh,
			}
		}
		return 0
	case windows.WM_SETCURSOR:
		w.cursorIn = (lParam & 0xffff) == windows.HTCLIENT
		if w.cursorIn {
			windows.SetCursor(w.cursor)
			return windows.TRUE
		}
	case _WM_WAKEUP:
		w.loop.Wakeup()
		w.loop.FlushEvents()
	case windows.WM_IME_STARTCOMPOSITION:
		imc := windows.ImmGetContext(w.hwnd)
		if imc == 0 {
			return windows.TRUE
		}
		defer windows.ImmReleaseContext(w.hwnd, imc)
		sel := w.w.EditorState().Selection
		caret := sel.Transform.Transform(sel.Caret.Pos.Add(f32.Pt(0, sel.Caret.Descent)))
		icaret := image.Pt(int(caret.X+.5), int(caret.Y+.5))
		windows.ImmSetCompositionWindow(imc, icaret.X, icaret.Y)
		windows.ImmSetCandidateWindow(imc, icaret.X, icaret.Y)
	case windows.WM_IME_COMPOSITION:
		imc := windows.ImmGetContext(w.hwnd)
		if imc == 0 {
			return windows.TRUE
		}
		defer windows.ImmReleaseContext(w.hwnd, imc)
		state := w.w.EditorState()
		rng := state.compose
		if rng.Start == -1 {
			rng = state.Selection.Range
		}
		if rng.Start > rng.End {
			rng.Start, rng.End = rng.End, rng.Start
		}
		var replacement string
		switch {
		case lParam&windows.GCS_RESULTSTR != 0:
			replacement = windows.ImmGetCompositionString(imc, windows.GCS_RESULTSTR)
		case lParam&windows.GCS_COMPSTR != 0:
			replacement = windows.ImmGetCompositionString(imc, windows.GCS_COMPSTR)
		}
		end := rng.Start + utf8.RuneCountInString(replacement)
		w.w.EditorReplace(rng, replacement)
		state = w.w.EditorState()
		comp := key.Range{
			Start: rng.Start,
			End:   end,
		}
		if lParam&windows.GCS_DELTASTART != 0 {
			start := windows.ImmGetCompositionValue(imc, windows.GCS_DELTASTART)
			comp.Start = state.RunesIndex(state.UTF16Index(comp.Start) + start)
		}
		w.w.SetComposingRegion(comp)
		pos := end
		if lParam&windows.GCS_CURSORPOS != 0 {
			rel := windows.ImmGetCompositionValue(imc, windows.GCS_CURSORPOS)
			pos = state.RunesIndex(state.UTF16Index(rng.Start) + rel)
		}
		w.w.SetEditorSelection(key.Range{Start: pos, End: pos})
		return windows.TRUE
	case windows.WM_IME_ENDCOMPOSITION:
		w.w.SetComposingRegion(key.Range{Start: -1, End: -1})
		return windows.TRUE
	case windows.WM_COPYDATA:
		data := (*windows.CopyDataStruct)(unsafe.Pointer(lParam))
		switch data.DwData {
		case copyDataURLType:
			if schemesURI == "" {
				return windows.TRUE
			}

			uri := syscall.UTF16PtrToString((*uint16)(unsafe.Pointer(data.LpData)))
			if w.onOpenURI(uri) {
				w.Perform(system.ActionRaise)
			}
			return windows.TRUE
		}
	}

	return windows.DefWindowProc(hwnd, msg, wParam, lParam)
}

func getModifiers() key.Modifiers {
	var kmods key.Modifiers
	if windows.GetKeyState(windows.VK_LWIN)&0x1000 != 0 || windows.GetKeyState(windows.VK_RWIN)&0x1000 != 0 {
		kmods |= key.ModSuper
	}
	if windows.GetKeyState(windows.VK_MENU)&0x1000 != 0 {
		kmods |= key.ModAlt
	}
	if windows.GetKeyState(windows.VK_CONTROL)&0x1000 != 0 {
		kmods |= key.ModCtrl
	}
	if windows.GetKeyState(windows.VK_SHIFT)&0x1000 != 0 {
		kmods |= key.ModShift
	}
	return kmods
}

// hitTest returns the non-client area hit by the point, needed to
// process WM_NCHITTEST.
func (w *window) hitTest(x, y int) uintptr {
	if w.config.Mode == Fullscreen {
		return windows.HTCLIENT
	}
	if w.config.Mode != Windowed {
		// Only windowed mode should allow resizing.
		return windows.HTCLIENT
	}
	// Check for resize handle before system actions; otherwise it can be impossible to
	// resize a custom-decorations window when the system move area is flush with the
	// edge of the window.
	top := y <= w.borderSize.Y
	bottom := y >= w.config.Size.Y-w.borderSize.Y
	left := x <= w.borderSize.X
	right := x >= w.config.Size.X-w.borderSize.X
	switch {
	case top && left:
		return windows.HTTOPLEFT
	case top && right:
		return windows.HTTOPRIGHT
	case bottom && left:
		return windows.HTBOTTOMLEFT
	case bottom && right:
		return windows.HTBOTTOMRIGHT
	case top:
		return windows.HTTOP
	case bottom:
		return windows.HTBOTTOM
	case left:
		return windows.HTLEFT
	case right:
		return windows.HTRIGHT
	}
	p := f32.Pt(float32(x), float32(y))
	if a, ok := w.w.ActionAt(p); ok && a == system.ActionMove {
		return windows.HTCAPTION
	}
	return windows.HTCLIENT
}

func (w *window) pointerButton(btn pointer.Buttons, press bool, lParam uintptr, kmods key.Modifiers) {
	if !w.config.Focused {
		windows.SetFocus(w.hwnd)
	}

	var kind pointer.Kind
	if press {
		kind = pointer.Press
		if w.pointerBtns == 0 {
			windows.SetCapture(w.hwnd)
		}
		w.pointerBtns |= btn
	} else {
		kind = pointer.Release
		w.pointerBtns &^= btn
		if w.pointerBtns == 0 {
			windows.ReleaseCapture()
		}
	}
	x, y := coordsFromlParam(lParam)
	p := f32.Point{X: float32(x), Y: float32(y)}
	w.ProcessEvent(pointer.Event{
		Kind:      kind,
		Source:    pointer.Mouse,
		Position:  p,
		Buttons:   w.pointerBtns,
		Time:      windows.GetMessageTime(),
		Modifiers: kmods,
	})
}

func coordsFromlParam(lParam uintptr) (int, int) {
	x := int(int16(lParam & 0xffff))
	y := int(int16((lParam >> 16) & 0xffff))
	return x, y
}

func (w *window) scrollEvent(wParam, lParam uintptr, horizontal bool, kmods key.Modifiers) {
	x, y := coordsFromlParam(lParam)
	// The WM_MOUSEWHEEL coordinates are in screen coordinates, in contrast
	// to other mouse events.
	np := windows.Point{X: int32(x), Y: int32(y)}
	windows.ScreenToClient(w.hwnd, &np)
	p := f32.Point{X: float32(np.X), Y: float32(np.Y)}
	dist := float32(int16(wParam >> 16))
	var sp f32.Point
	if horizontal {
		sp.X = dist
	} else {
		// support horizontal scroll (shift + mousewheel)
		if kmods == key.ModShift {
			sp.X = -dist
		} else {
			sp.Y = -dist
		}
	}
	w.ProcessEvent(pointer.Event{
		Kind:      pointer.Scroll,
		Source:    pointer.Mouse,
		Position:  p,
		Buttons:   w.pointerBtns,
		Scroll:    sp,
		Modifiers: kmods,
		Time:      windows.GetMessageTime(),
	})
}

// Adapted from https://blogs.msdn.microsoft.com/oldnewthing/20060126-00/?p=32513/
func (w *window) runLoop() {
	msg := new(windows.Msg)
loop:
	for {
		anim := w.animating
		if anim && !windows.PeekMessage(msg, 0, 0, 0, windows.PM_NOREMOVE) {
			w.draw(false)
			continue
		}
		switch ret := windows.GetMessage(msg, 0, 0, 0); ret {
		case -1:
			panic(errors.New("GetMessage failed"))
		case 0:
			// WM_QUIT received.
			break loop
		}
		windows.TranslateMessage(msg)
		windows.DispatchMessage(msg)
	}
}

func (w *window) EditorStateChanged(old, new editorState) {
	imc := windows.ImmGetContext(w.hwnd)
	if imc == 0 {
		return
	}
	defer windows.ImmReleaseContext(w.hwnd, imc)
	if old.Selection.Range != new.Selection.Range || old.Snippet != new.Snippet {
		windows.ImmNotifyIME(imc, windows.NI_COMPOSITIONSTR, windows.CPS_CANCEL, 0)
	}
}

func (w *window) SetAnimating(anim bool) {
	w.animating = anim
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

func (w *window) wakeup() {
	w.invMu.Lock()
	defer w.invMu.Unlock()
	if w.hwnd == 0 {
		w.loop.Wakeup()
		w.loop.FlushEvents()
		return
	}
	if err := windows.PostMessage(w.hwnd, _WM_WAKEUP, 0, 0); err != nil {
		panic(err)
	}
}

func (w *window) draw(sync bool) {
	if w.config.Size.X == 0 || w.config.Size.Y == 0 {
		return
	}
	dpi := windows.GetWindowDPI(w.hwnd)
	cfg := configForDPI(dpi)
	w.ProcessEvent(frameEvent{
		FrameEvent: FrameEvent{
			Now:    time.Now(),
			Size:   w.config.Size,
			Metric: cfg,
		},
		Sync: sync,
	})
}

func (w *window) NewContext() (context, error) {
	sort.Slice(drivers, func(i, j int) bool {
		return drivers[i].priority < drivers[j].priority
	})
	var errs []string
	for _, b := range drivers {
		ctx, err := b.initializer(w)
		if err == nil {
			return ctx, nil
		}
		errs = append(errs, err.Error())
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("NewContext: failed to create a GPU device, tried: %s", strings.Join(errs, ", "))
	}
	return nil, errors.New("NewContext: no available GPU drivers")
}

func (w *window) ReadClipboard() {
	w.readClipboard()
}

func (w *window) readClipboard() error {
	if err := windows.OpenClipboard(w.hwnd); err != nil {
		return err
	}
	defer windows.CloseClipboard()
	mem, err := windows.GetClipboardData(windows.CF_UNICODETEXT)
	if err != nil {
		return err
	}
	ptr, err := windows.GlobalLock(mem)
	if err != nil {
		return err
	}
	defer windows.GlobalUnlock(mem)
	content := gowindows.UTF16PtrToString((*uint16)(unsafe.Pointer(ptr)))
	w.ProcessEvent(transfer.DataEvent{
		Type: "application/text",
		Open: func() io.ReadCloser {
			return io.NopCloser(strings.NewReader(content))
		},
	})
	return nil
}

func (w *window) Configure(options []Option) {
	dpi := windows.GetSystemDPI()
	metric := configForDPI(dpi)
	w.config.apply(metric, options)
	windows.SetWindowText(w.hwnd, w.config.Title)

	style := windows.GetWindowLong(w.hwnd, windows.GWL_STYLE)
	var showMode int32
	var x, y, width, height int32
	swpStyle := uintptr(windows.SWP_NOZORDER | windows.SWP_FRAMECHANGED)
	winStyle := uintptr(windows.WS_OVERLAPPEDWINDOW)
	style &^= winStyle
	switch w.config.Mode {
	case Minimized:
		style |= winStyle
		swpStyle |= windows.SWP_NOMOVE | windows.SWP_NOSIZE
		showMode = windows.SW_SHOWMINIMIZED

	case Maximized:
		style |= winStyle
		swpStyle |= windows.SWP_NOMOVE | windows.SWP_NOSIZE
		showMode = windows.SW_SHOWMAXIMIZED

	case Windowed:
		style |= winStyle
		showMode = windows.SW_SHOWNORMAL
		// Get target for client area size.
		width = int32(w.config.Size.X)
		height = int32(w.config.Size.Y)
		// Get the current window size and position.
		wr := windows.GetWindowRect(w.hwnd)
		x = wr.Left
		y = wr.Top
		if w.config.Decorated {
			// Compute client size and position. Note that the client size is
			// equal to the window size when we are in control of decorations.
			r := windows.Rect{
				Right:  width,
				Bottom: height,
			}
			windows.AdjustWindowRectEx(&r, uint32(style), 0, dwExStyle)
			width = r.Right - r.Left
			height = r.Bottom - r.Top
		}
		if !w.config.Decorated {
			// Enable drop shadows when we draw decorations.
			windows.DwmExtendFrameIntoClientArea(w.hwnd, windows.Margins{-1, -1, -1, -1})
		}

	case Fullscreen:
		swpStyle |= windows.SWP_NOMOVE | windows.SWP_NOSIZE
		mi := windows.GetMonitorInfo(w.hwnd)
		x, y = mi.Monitor.Left, mi.Monitor.Top
		width = mi.Monitor.Right - mi.Monitor.Left
		height = mi.Monitor.Bottom - mi.Monitor.Top
		showMode = windows.SW_SHOWMAXIMIZED
	}
	windows.SetWindowLong(w.hwnd, windows.GWL_STYLE, style)
	windows.SetWindowPos(w.hwnd, 0, x, y, width, height, swpStyle)
	windows.ShowWindow(w.hwnd, showMode)

	w.update()
}

func (w *window) WriteClipboard(mime string, s []byte) {
	w.writeClipboard(string(s))
}

func (w *window) writeClipboard(s string) error {
	if err := windows.OpenClipboard(w.hwnd); err != nil {
		return err
	}
	defer windows.CloseClipboard()
	if err := windows.EmptyClipboard(); err != nil {
		return err
	}
	u16, err := gowindows.UTF16FromString(s)
	if err != nil {
		return err
	}
	n := len(u16) * int(unsafe.Sizeof(u16[0]))
	mem, err := windows.GlobalAlloc(n)
	if err != nil {
		return err
	}
	ptr, err := windows.GlobalLock(mem)
	if err != nil {
		windows.GlobalFree(mem)
		return err
	}
	u16v := unsafe.Slice((*uint16)(ptr), len(u16))
	copy(u16v, u16)
	windows.GlobalUnlock(mem)
	if err := windows.SetClipboardData(windows.CF_UNICODETEXT, mem); err != nil {
		windows.GlobalFree(mem)
		return err
	}
	return nil
}

func (w *window) SetCursor(cursor pointer.Cursor) {
	c, err := loadCursor(cursor)
	if err != nil {
		c = resources.cursor
	}
	w.cursor = c
	if w.cursorIn {
		windows.SetCursor(w.cursor)
	}
}

// windowsCursor contains mapping from pointer.Cursor to an IDC.
var windowsCursor = [...]uint16{
	pointer.CursorDefault:                  windows.IDC_ARROW,
	pointer.CursorNone:                     0,
	pointer.CursorText:                     windows.IDC_IBEAM,
	pointer.CursorVerticalText:             windows.IDC_IBEAM,
	pointer.CursorPointer:                  windows.IDC_HAND,
	pointer.CursorCrosshair:                windows.IDC_CROSS,
	pointer.CursorAllScroll:                windows.IDC_SIZEALL,
	pointer.CursorColResize:                windows.IDC_SIZEWE,
	pointer.CursorRowResize:                windows.IDC_SIZENS,
	pointer.CursorGrab:                     windows.IDC_SIZEALL,
	pointer.CursorGrabbing:                 windows.IDC_SIZEALL,
	pointer.CursorNotAllowed:               windows.IDC_NO,
	pointer.CursorWait:                     windows.IDC_WAIT,
	pointer.CursorProgress:                 windows.IDC_APPSTARTING,
	pointer.CursorNorthWestResize:          windows.IDC_SIZENWSE,
	pointer.CursorNorthEastResize:          windows.IDC_SIZENESW,
	pointer.CursorSouthWestResize:          windows.IDC_SIZENESW,
	pointer.CursorSouthEastResize:          windows.IDC_SIZENWSE,
	pointer.CursorNorthSouthResize:         windows.IDC_SIZENS,
	pointer.CursorEastWestResize:           windows.IDC_SIZEWE,
	pointer.CursorWestResize:               windows.IDC_SIZEWE,
	pointer.CursorEastResize:               windows.IDC_SIZEWE,
	pointer.CursorNorthResize:              windows.IDC_SIZENS,
	pointer.CursorSouthResize:              windows.IDC_SIZENS,
	pointer.CursorNorthEastSouthWestResize: windows.IDC_SIZENESW,
	pointer.CursorNorthWestSouthEastResize: windows.IDC_SIZENWSE,
}

func loadCursor(cursor pointer.Cursor) (syscall.Handle, error) {
	switch cursor {
	case pointer.CursorDefault:
		return resources.cursor, nil
	case pointer.CursorNone:
		return 0, nil
	default:
		return windows.LoadCursor(windowsCursor[cursor])
	}
}

func (w *window) ShowTextInput(show bool) {}

func (w *window) SetInputHint(_ key.InputHint) {}

func (w *window) HDC() syscall.Handle {
	return w.hdc
}

func (w *window) HWND() (syscall.Handle, int, int) {
	return w.hwnd, w.config.Size.X, w.config.Size.Y
}

func (w *window) Perform(acts system.Action) {
	walkActions(acts, func(a system.Action) {
		switch a {
		case system.ActionCenter:
			if w.config.Mode != Windowed {
				break
			}
			r := windows.GetWindowRect(w.hwnd)
			dx := r.Right - r.Left
			dy := r.Bottom - r.Top
			// Calculate center position on current monitor.
			mi := windows.GetMonitorInfo(w.hwnd).Monitor
			x := (mi.Right - mi.Left - dx) / 2
			y := (mi.Bottom - mi.Top - dy) / 2
			windows.SetWindowPos(w.hwnd, 0, x, y, dx, dy, windows.SWP_NOZORDER|windows.SWP_FRAMECHANGED)
		case system.ActionRaise:
			w.raise()
		case system.ActionClose:
			windows.PostMessage(w.hwnd, windows.WM_CLOSE, 0, 0)
		}
	})
}

func (w *window) raise() {
	windows.SetForegroundWindow(w.hwnd)
	windows.SetWindowPos(w.hwnd, windows.HWND_TOPMOST, 0, 0, 0, 0,
		windows.SWP_NOMOVE|windows.SWP_NOSIZE|windows.SWP_SHOWWINDOW)
}

func (w *window) onOpenURI(uri string) bool {
	u, err := url.Parse(uri)
	if err != nil {
		return false
	}

	for _, scheme := range strings.Split(schemesURI, ",") {
		if u.Scheme == scheme {
			w.ProcessEvent(transfer.URLEvent{URL: u})
			return true
		}
	}

	return false
}

func convertKeyCode(code uintptr) (key.Name, bool) {
	if '0' <= code && code <= '9' || 'A' <= code && code <= 'Z' {
		return key.Name(rune(code)), true
	}
	var r key.Name

	switch code {
	case windows.VK_ESCAPE:
		r = key.NameEscape
	case windows.VK_LEFT:
		r = key.NameLeftArrow
	case windows.VK_RIGHT:
		r = key.NameRightArrow
	case windows.VK_RETURN:
		r = key.NameReturn
	case windows.VK_UP:
		r = key.NameUpArrow
	case windows.VK_DOWN:
		r = key.NameDownArrow
	case windows.VK_HOME:
		r = key.NameHome
	case windows.VK_END:
		r = key.NameEnd
	case windows.VK_BACK:
		r = key.NameDeleteBackward
	case windows.VK_DELETE:
		r = key.NameDeleteForward
	case windows.VK_PRIOR:
		r = key.NamePageUp
	case windows.VK_NEXT:
		r = key.NamePageDown
	case windows.VK_F1:
		r = key.NameF1
	case windows.VK_F2:
		r = key.NameF2
	case windows.VK_F3:
		r = key.NameF3
	case windows.VK_F4:
		r = key.NameF4
	case windows.VK_F5:
		r = key.NameF5
	case windows.VK_F6:
		r = key.NameF6
	case windows.VK_F7:
		r = key.NameF7
	case windows.VK_F8:
		r = key.NameF8
	case windows.VK_F9:
		r = key.NameF9
	case windows.VK_F10:
		r = key.NameF10
	case windows.VK_F11:
		r = key.NameF11
	case windows.VK_F12:
		r = key.NameF12
	case windows.VK_TAB:
		r = key.NameTab
	case windows.VK_SPACE:
		r = key.NameSpace
	case windows.VK_OEM_1:
		r = ";"
	case windows.VK_OEM_PLUS:
		r = "+"
	case windows.VK_OEM_COMMA:
		r = ","
	case windows.VK_OEM_MINUS:
		r = "-"
	case windows.VK_OEM_PERIOD:
		r = "."
	case windows.VK_OEM_2:
		r = "/"
	case windows.VK_OEM_3:
		r = "`"
	case windows.VK_OEM_4:
		r = "["
	case windows.VK_OEM_5, windows.VK_OEM_102:
		r = "\\"
	case windows.VK_OEM_6:
		r = "]"
	case windows.VK_OEM_7:
		r = "'"
	case windows.VK_CONTROL:
		r = key.NameCtrl
	case windows.VK_SHIFT:
		r = key.NameShift
	case windows.VK_MENU:
		r = key.NameAlt
	case windows.VK_LWIN, windows.VK_RWIN:
		r = key.NameSuper
	default:
		return "", false
	}
	return r, true
}

func configForDPI(dpi int) unit.Metric {
	const inchPrDp = 1.0 / 96.0
	ppdp := float32(dpi) * inchPrDp
	return unit.Metric{
		PxPerDp: ppdp,
		PxPerSp: ppdp,
	}
}

func (Win32ViewEvent) implementsViewEvent() {}
func (Win32ViewEvent) ImplementsEvent()     {}

// schemesURI is a list of schemes, comma separated, that must be
// defined using -X compiler ldflag, that used in gogio.
var schemesURI string

func init() {
	if schemesURI == "" {
		return
	}

	currentSchemes := strings.Split(schemesURI, ",")
	oldSchemes := registeredSchemes(ID)

	for _, s := range currentSchemes {
		for i, o := range oldSchemes {
			if s == o {
				oldSchemes = append(oldSchemes[:i], oldSchemes[i+1:]...)
				break
			}
		}
	}

	if len(oldSchemes) > 0 {
		go unregisterSchemes(ID, oldSchemes)
	}

	if len(currentSchemes) == 0 {
		return
	}

	// On Windows, launching the app using a URI will start a new instance of the app,
	// a new window. That behavior, by default, doesn't align with iOS/Android/macOS, where
	// the deeplink sends the event to the running app (if any). We are emulating it.
	if hwnd, _ := windows.FindWindow(ID); hwnd != 0 {
		if u := startupURI(); u != "" {
			broadcastURI(hwnd, u)
		}
		os.Exit(0)
		return
	}

	go registerSchemes(ID, currentSchemes)
}

func startupURI() string {
	if len(os.Args) == 3 && os.Args[1] == "-gio_launch_url" {
		return os.Args[2]
	}
	return ""
}

func broadcastURI(hwnd syscall.Handle, uri string) {
	u := syscall.StringToUTF16Ptr(uri)
	msg := &windows.CopyDataStruct{
		DwData: copyDataURLType,
		CbData: uint32(len(uri)*2 + 1),
		LpData: uintptr(unsafe.Pointer(u)),
	}

	windows.SendMessage(hwnd, windows.WM_COPYDATA, 0, uintptr(unsafe.Pointer(msg)))
	runtime.KeepAlive(u)
	runtime.KeepAlive(msg)
}

func registeredSchemes(appid string) []string {
	meta, err := registry.OpenKey(registry.CURRENT_USER, `Software\\`+appid, registry.ALL_ACCESS)
	if err != nil {
		return nil
	}
	defer meta.Close()

	schemes, _, _ := meta.GetStringsValue("URISchemes")
	return schemes
}

func registerSchemes(appid string, schemes []string) error {
	reg := func(scheme string) error {
		key, existent, err := registry.CreateKey(registry.CURRENT_USER, `Software\\Classes\\`+scheme, registry.ALL_ACCESS)
		if err != nil {
			return err
		}
		defer key.Close()

		if existent {
			// Check if the existent key belongs to the current application
			id, _, err := key.GetStringValue("appid")
			if err != nil || id != appid {
				return fmt.Errorf("scheme %s already registered by another application", scheme)
			}
		}

		path, err := os.Executable()
		if err != nil {
			return err
		}

		if err = key.SetStringValue("", "URL:"+scheme+" Protocol"); err != nil {
			return err
		}
		if err = key.SetStringValue("URL Protocol", ""); err != nil {
			return err
		}
		if err = key.SetStringValue("appid", appid); err != nil {
			return err
		}

		icon, _, err := registry.CreateKey(key, `DefaultIcon`, registry.ALL_ACCESS)
		if err != nil {
			return err
		}
		defer icon.Close()

		if err = icon.SetStringValue("", `"`+path+`",1`); err != nil {
			return err
		}

		cmd, _, err := registry.CreateKey(key, `shell\\open\\command`, registry.ALL_ACCESS)
		if err != nil {
			return err
		}
		defer cmd.Close()

		if err = cmd.SetStringValue("", `"`+path+`" -gio_launch_url "%1"`); err != nil {
			return err
		}

		return nil
	}

	for _, scheme := range schemes {
		if scheme == "" {
			continue // just in case
		}
		if err := reg(scheme); err != nil {
			return err
		}
	}

	meta, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\\`+appid, registry.ALL_ACCESS)
	if err != nil {
		return err
	}
	defer meta.Close()

	if err = meta.SetStringsValue("URISchemes", schemes); err != nil {
		return err
	}

	return nil
}

func unregisterSchemes(appid string, schemes []string) {
	classes, err := registry.OpenKey(registry.CURRENT_USER, `Software\\Classes`, registry.ALL_ACCESS)
	if err != nil {
		return
	}
	defer classes.Close()

	for _, scheme := range schemes {
		if scheme == "" {
			continue // just in case
		}

		key, err := registry.OpenKey(classes, scheme, registry.ALL_ACCESS)
		if err != nil {
			continue
		}

		id, _, err := key.GetStringValue("appid")
		if err == nil && id != appid {
			continue
		}

		for _, k := range []string{`DefaultIcon`, `shell\\open\\command`, `shell\\open`, `shell`} {
			registry.DeleteKey(key, k)
		}

		if err := key.Close(); err != nil {
			continue
		}

		registry.DeleteKey(classes, scheme)
	}
}
