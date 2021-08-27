// SPDX-License-Identifier: Unlicense OR MIT

//go:build !nometal
// +build !nometal

package wm

import (
	"errors"
	"unsafe"

	"gioui.org/gpu"
)

/*
#cgo CFLAGS: -Werror -xobjective-c -fmodules -fobjc-arc

@import Metal;
@import QuartzCore.CAMetalLayer;

#include <CoreFoundation/CoreFoundation.h>

static CFTypeRef createMetalDevice(void) {
	@autoreleasepool {
		id<MTLDevice> dev = MTLCreateSystemDefaultDevice();
		return CFBridgingRetain(dev);
	}
}

static void setupLayer(CFTypeRef layerRef, CFTypeRef devRef) {
	@autoreleasepool {
		CAMetalLayer *layer = (__bridge CAMetalLayer *)layerRef;
		id<MTLDevice> dev = (__bridge id<MTLDevice>)devRef;
		layer.device = dev;
		// Package gpu assumes an sRGB-encoded framebuffer.
		layer.pixelFormat = MTLPixelFormatBGRA8Unorm_sRGB;
		if (@available(iOS 11.0, *)) {
			// Never let nextDrawable time out and return nil.
			layer.allowsNextDrawableTimeout = NO;
		}
	}
}

static CFTypeRef nextDrawable(CFTypeRef layerRef) {
	@autoreleasepool {
		CAMetalLayer *layer = (__bridge CAMetalLayer *)layerRef;
		return CFBridgingRetain([layer nextDrawable]);
	}
}

static CFTypeRef drawableTexture(CFTypeRef drawableRef) {
	@autoreleasepool {
		id<CAMetalDrawable> drawable = (__bridge id<CAMetalDrawable>)drawableRef;
		return CFBridgingRetain(drawable.texture);
	}
}

static CFTypeRef newCommandQueue(CFTypeRef devRef) {
	@autoreleasepool {
		id<MTLDevice> dev = (__bridge id<MTLDevice>)devRef;
		return CFBridgingRetain([dev newCommandQueue]);
	}
}
*/
import "C"

type mtlContext struct {
	dev      C.CFTypeRef
	view     C.CFTypeRef
	layer    C.CFTypeRef
	queue    C.CFTypeRef
	drawable C.CFTypeRef
	texture  C.CFTypeRef
}

func newMtlContext(w *window) (*mtlContext, error) {
	dev := C.createMetalDevice()
	if dev == 0 {
		return nil, errors.New("metal: MTLCreateSystemDefaultDevice failed")
	}
	view := w.contextView()
	layer := getMetalLayer(view)
	if layer == 0 {
		C.CFRelease(dev)
		return nil, errors.New("metal: CAMetalLayer construction failed")
	}
	queue := C.newCommandQueue(dev)
	if layer == 0 {
		C.CFRelease(dev)
		C.CFRelease(layer)
		return nil, errors.New("metal: [MTLDevice newCommandQueue] failed")
	}
	C.setupLayer(layer, dev)
	c := &mtlContext{
		dev:   dev,
		view:  view,
		layer: layer,
		queue: queue,
	}
	return c, nil
}

func (c *mtlContext) RenderTarget() gpu.RenderTarget {
	if c.drawable != 0 || c.texture != 0 {
		panic("a previous RenderTarget wasn't Presented")
	}
	c.drawable = C.nextDrawable(c.layer)
	if c.drawable == 0 {
		panic("metal: [CAMetalLayer nextDrawable] failed")
	}
	c.texture = C.drawableTexture(c.drawable)
	if c.texture == 0 {
		panic("metal: CADrawable.texture is nil")
	}
	return gpu.MetalRenderTarget{
		Texture:  unsafe.Pointer(c.texture),
		Drawable: unsafe.Pointer(c.drawable),
	}
}

func (c *mtlContext) API() gpu.API {
	return gpu.Metal{
		Device:      unsafe.Pointer(c.dev),
		Queue:       unsafe.Pointer(c.queue),
		PixelFormat: int(C.MTLPixelFormatBGRA8Unorm_sRGB),
	}
}

func (c *mtlContext) Release() {
	C.CFRelease(c.queue)
	C.CFRelease(c.dev)
	C.CFRelease(c.layer)
	if c.drawable != 0 {
		C.CFRelease(c.drawable)
	}
	if c.texture != 0 {
		C.CFRelease(c.texture)
	}
	*c = mtlContext{}
}

func (c *mtlContext) Present() error {
	C.CFRelease(c.texture)
	c.texture = 0
	C.CFRelease(c.drawable)
	c.drawable = 0
	return nil
}

func (c *mtlContext) Lock() error {
	return nil
}

func (c *mtlContext) Unlock() {}

func (c *mtlContext) Refresh() error {
	resizeDrawable(c.view, c.layer)
	return nil
}

func (w *window) NewContext() (Context, error) {
	return newMtlContext(w)
}
