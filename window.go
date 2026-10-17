//go:build !nowebview

package main

/*
#cgo darwin CFLAGS: -x objective-c
#cgo darwin LDFLAGS: -framework Cocoa
#ifdef __APPLE__
#import <Cocoa/Cocoa.h>

// WKWebView only gets copy/paste/undo shortcuts when the app has an Edit menu.
static void lpInstallMenu(void) {
	NSMenu *bar = [[NSMenu alloc] init];
	NSMenuItem *appItem = [[NSMenuItem alloc] init];
	[bar addItem:appItem];
	NSMenu *appMenu = [[NSMenu alloc] init];
	[appMenu addItemWithTitle:@"Hide Lightpost" action:@selector(hide:) keyEquivalent:@"h"];
	[appMenu addItem:[NSMenuItem separatorItem]];
	[appMenu addItemWithTitle:@"Quit Lightpost" action:@selector(terminate:) keyEquivalent:@"q"];
	[appItem setSubmenu:appMenu];

	NSMenuItem *editItem = [[NSMenuItem alloc] init];
	[bar addItem:editItem];
	NSMenu *edit = [[NSMenu alloc] initWithTitle:@"Edit"];
	[edit addItemWithTitle:@"Undo" action:@selector(undo:) keyEquivalent:@"z"];
	NSMenuItem *redo = [edit addItemWithTitle:@"Redo" action:@selector(redo:) keyEquivalent:@"z"];
	[redo setKeyEquivalentModifierMask:NSEventModifierFlagCommand | NSEventModifierFlagShift];
	[edit addItem:[NSMenuItem separatorItem]];
	[edit addItemWithTitle:@"Cut" action:@selector(cut:) keyEquivalent:@"x"];
	[edit addItemWithTitle:@"Copy" action:@selector(copy:) keyEquivalent:@"c"];
	[edit addItemWithTitle:@"Paste" action:@selector(paste:) keyEquivalent:@"v"];
	[edit addItemWithTitle:@"Select All" action:@selector(selectAll:) keyEquivalent:@"a"];
	[editItem setSubmenu:edit];

	NSMenuItem *winItem = [[NSMenuItem alloc] init];
	[bar addItem:winItem];
	NSMenu *win = [[NSMenu alloc] initWithTitle:@"Window"];
	[win addItemWithTitle:@"Minimize" action:@selector(performMiniaturize:) keyEquivalent:@"m"];
	[win addItemWithTitle:@"Zoom" action:@selector(performZoom:) keyEquivalent:@""];
	[winItem setSubmenu:win];
	[NSApp setMainMenu:bar];
}
#else
static void lpInstallMenu(void) {}
#endif
*/
import "C"

import webview "github.com/webview/webview_go"

const hasWindow = true

func runWindow(url string) {
	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("Lightpost")
	w.SetSize(1360, 860, webview.HintNone)
	C.lpInstallMenu()
	w.Navigate(url)
	w.Run()
}
