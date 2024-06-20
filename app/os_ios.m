// SPDX-License-Identifier: Unlicense OR MIT

// +build darwin,ios

@import UIKit;

#include <stdint.h>
#include "_cgo_export.h"
#include "framework_ios.h"

__attribute__ ((visibility ("hidden"))) Class gio_layerClass(void);

@interface GioView: UIView <UIKeyInput>
@property uintptr_t handle;
@end

@implementation GioViewController

CGFloat _keyboardHeight;

- (void)loadView {
	gio_runMain();

	CGRect zeroFrame = CGRectMake(0, 0, 0, 0);
	self.view = [[UIView alloc] initWithFrame:zeroFrame];
	self.view.layoutMargins = UIEdgeInsetsMake(0, 0, 0, 0);
	UIView *drawView = [[GioView alloc] initWithFrame:zeroFrame];
	[self.view addSubview: drawView];
#if !TARGET_OS_TV
	drawView.multipleTouchEnabled = YES;
#endif
	drawView.preservesSuperviewLayoutMargins = YES;
	drawView.layoutMargins = UIEdgeInsetsMake(0, 0, 0, 0);
	onCreate((__bridge CFTypeRef)drawView, (__bridge CFTypeRef)self);
#if !TARGET_OS_TV
	[[NSNotificationCenter defaultCenter] addObserver:self
											 selector:@selector(keyboardWillChange:)
												 name:UIKeyboardWillShowNotification
											   object:nil];
	[[NSNotificationCenter defaultCenter] addObserver:self
											 selector:@selector(keyboardWillChange:)
												 name:UIKeyboardWillChangeFrameNotification
											   object:nil];
	[[NSNotificationCenter defaultCenter] addObserver:self
											 selector:@selector(keyboardWillHide:)
												 name:UIKeyboardWillHideNotification
											   object:nil];
#endif
	[[NSNotificationCenter defaultCenter] addObserver: self
											 selector: @selector(applicationDidEnterBackground:)
												 name: UIApplicationDidEnterBackgroundNotification
											   object: nil];
	[[NSNotificationCenter defaultCenter] addObserver: self
											 selector: @selector(applicationWillEnterForeground:)
												 name: UIApplicationWillEnterForegroundNotification
											   object: nil];
}

- (void)applicationWillEnterForeground:(UIApplication *)application {
	GioView *view = (GioView *)self.view.subviews[0];
	if (view != nil) {
		onStart(view.handle);
	}
}

- (void)applicationDidEnterBackground:(UIApplication *)application {
	GioView *view = (GioView *)self.view.subviews[0];
	if (view != nil) {
		onStop(view.handle);
	}
}

- (void)viewDidDisappear:(BOOL)animated {
	[super viewDidDisappear:animated];
	GioView *view = (GioView *)self.view.subviews[0];
	onDestroy(view.handle);
}

- (void)viewDidLayoutSubviews {
	[super viewDidLayoutSubviews];
	GioView *view = (GioView *)self.view.subviews[0];
	CGRect frame = self.view.bounds;
	// Adjust view bounds to make room for the keyboard.
	frame.size.height -= _keyboardHeight;
	view.frame = frame;
	gio_onDraw(view.handle);
}

- (void)didReceiveMemoryWarning {
	onLowMemory();
	[super didReceiveMemoryWarning];
}

#if !TARGET_OS_TV
- (void)keyboardWillChange:(NSNotification *)note {
	NSDictionary *userInfo = note.userInfo;
	CGRect f = [userInfo[UIKeyboardFrameEndUserInfoKey] CGRectValue];
	_keyboardHeight = f.size.height;
	[self.view setNeedsLayout];
}

- (void)keyboardWillHide:(NSNotification *)note {
	_keyboardHeight = 0.0;
	[self.view setNeedsLayout];
}

- (BOOL)onOpenURI:(NSString *)url {
    gio_onOpenURI((__bridge CFTypeRef)url);
    return YES;
}
#endif
@end

static void handleTouches(int last, GioView *view, NSSet<UITouch *> *touches, UIEvent *event) {
	CGFloat scale = view.contentScaleFactor;
	NSUInteger i = 0;
	NSUInteger n = [touches count];
	for (UITouch *touch in touches) {
		CFTypeRef touchRef = (__bridge CFTypeRef)touch;
		i++;
		NSArray<UITouch *> *coalescedTouches = [event coalescedTouchesForTouch:touch];
		NSUInteger j = 0;
		NSUInteger m = [coalescedTouches count];
		for (UITouch *coalescedTouch in [event coalescedTouchesForTouch:touch]) {
			CGPoint loc = [coalescedTouch locationInView:view];
			j++;
			int lastTouch = last && i == n && j == m;
			onTouch(view.handle, lastTouch, touchRef, touch.phase, loc.x*scale, loc.y*scale, [coalescedTouch timestamp]);
		}
	}
}

@implementation GioView
NSArray<UIKeyCommand *> *_keyCommands;
+ (void)onFrameCallback:(CADisplayLink *)link {
       gio_onFrameCallback((__bridge CFTypeRef)link);
}
+ (Class)layerClass {
    return gio_layerClass();
}
- (void)willMoveToWindow:(UIWindow *)newWindow {
	if (self.window != nil) {
		[[NSNotificationCenter defaultCenter] removeObserver:self
														name:UIWindowDidBecomeKeyNotification
													  object:self.window];
		[[NSNotificationCenter defaultCenter] removeObserver:self
														name:UIWindowDidResignKeyNotification
													  object:self.window];
	}
	self.contentScaleFactor = newWindow.screen.nativeScale;
	[[NSNotificationCenter defaultCenter] addObserver:self
											 selector:@selector(onWindowDidBecomeKey:)
												 name:UIWindowDidBecomeKeyNotification
											   object:newWindow];
	[[NSNotificationCenter defaultCenter] addObserver:self
											 selector:@selector(onWindowDidResignKey:)
												 name:UIWindowDidResignKeyNotification
											   object:newWindow];
}

- (void)onWindowDidBecomeKey:(NSNotification *)note {
	if (self.isFirstResponder) {
		onFocus(self.handle, YES);
	}
}

- (void)onWindowDidResignKey:(NSNotification *)note {
	if (self.isFirstResponder) {
		onFocus(self.handle, NO);
	}
}

- (void)touchesBegan:(NSSet<UITouch *> *)touches withEvent:(UIEvent *)event {
	handleTouches(0, self, touches, event);
}

- (void)touchesMoved:(NSSet<UITouch *> *)touches withEvent:(UIEvent *)event {
	handleTouches(0, self, touches, event);
}

- (void)touchesEnded:(NSSet<UITouch *> *)touches withEvent:(UIEvent *)event {
	handleTouches(1, self, touches, event);
}

- (void)touchesCancelled:(NSSet<UITouch *> *)touches withEvent:(UIEvent *)event {
	handleTouches(1, self, touches, event);
}

- (void)insertText:(NSString *)text {
	onText(self.handle, (__bridge CFTypeRef)text);
}

- (BOOL)canBecomeFirstResponder {
	return YES;
}

- (BOOL)hasText {
	return YES;
}

- (void)deleteBackward {
	onDeleteBackward(self.handle);
}

- (void)onUpArrow {
	onUpArrow(self.handle);
}

- (void)onDownArrow {
	onDownArrow(self.handle);
}

- (void)onLeftArrow {
	onLeftArrow(self.handle);
}

- (void)onRightArrow {
	onRightArrow(self.handle);
}

- (NSArray<UIKeyCommand *> *)keyCommands {
	if (_keyCommands == nil) {
		_keyCommands = @[
			[UIKeyCommand keyCommandWithInput:UIKeyInputUpArrow
								modifierFlags:0
									   action:@selector(onUpArrow)],
			[UIKeyCommand keyCommandWithInput:UIKeyInputDownArrow
								modifierFlags:0
									   action:@selector(onDownArrow)],
			[UIKeyCommand keyCommandWithInput:UIKeyInputLeftArrow
								modifierFlags:0
									   action:@selector(onLeftArrow)],
			[UIKeyCommand keyCommandWithInput:UIKeyInputRightArrow
								modifierFlags:0
									   action:@selector(onRightArrow)]
		];
	}
	return _keyCommands;
}
@end

CFTypeRef gio_createDisplayLink(void) {
	CADisplayLink *dl = [CADisplayLink displayLinkWithTarget:[GioView class] selector:@selector(onFrameCallback:)];
	dl.paused = YES;
	NSRunLoop *runLoop = [NSRunLoop mainRunLoop];
	[dl addToRunLoop:runLoop forMode:[runLoop currentMode]];
	return (__bridge_retained CFTypeRef)dl;
}

int gio_startDisplayLink(CFTypeRef dlref) {
	CADisplayLink *dl = (__bridge CADisplayLink *)dlref;
	dl.paused = NO;
	return 0;
}

int gio_stopDisplayLink(CFTypeRef dlref) {
	CADisplayLink *dl = (__bridge CADisplayLink *)dlref;
	dl.paused = YES;
	return 0;
}

void gio_releaseDisplayLink(CFTypeRef dlref) {
	CADisplayLink *dl = (__bridge CADisplayLink *)dlref;
	[dl invalidate];
	CFRelease(dlref);
}

void gio_setDisplayLinkDisplay(CFTypeRef dl, uint64_t did) {
	// Nothing to do on iOS.
}

void gio_hideCursor() {
	// Not supported.
}

void gio_showCursor() {
	// Not supported.
}

void gio_setCursor(NSUInteger curID) {
	// Not supported.
}

void gio_viewSetHandle(CFTypeRef viewRef, uintptr_t handle) {
	GioView *v = (__bridge GioView *)viewRef;
	v.handle = handle;
}

@interface _gioAppDelegate : UIResponder <UIApplicationDelegate>
@property (strong, nonatomic) UIWindow *window;
@property (strong, nonatomic) GioViewController *controller;
@end

@implementation _gioAppDelegate
- (BOOL)application:(UIApplication *)application didFinishLaunchingWithOptions:(NSDictionary *)launchOptions {
	self.window = [[UIWindow alloc] initWithFrame:[[UIScreen mainScreen] bounds]];
	self.controller = [[GioViewController alloc] initWithNibName:nil bundle:nil];
	self.window.rootViewController = self.controller;
	[self.window makeKeyAndVisible];
	return YES;
}
- (BOOL)application:(UIApplication *)app openURL:(NSURL *)url options:(NSDictionary<UIApplicationOpenURLOptionsKey, id> *)options {
	return [self.controller onOpenURI:url.absoluteString];
}
@end

int gio_applicationMain(int argc, char *argv[]) {
	@autoreleasepool {
		return UIApplicationMain(argc, argv, nil, NSStringFromClass([_gioAppDelegate class]));
	}
}
