// This file is part of clipsync (C)2023 by Marco Paganini
// Please see http://github.com/marcopaganini/clipsync for details.

package main

// The code below is a courtesy of Chris Down.
// Originally at: // https://github.com/cdown/clipnotify

/*
#cgo CFLAGS: -I/usr/X11R6/include
#cgo LDFLAGS: -lX11 -lXfixes -L/usr/X11R6/lib

#include <X11/Xatom.h>
#include <X11/Xlib.h>
#include <X11/extensions/Xfixes.h>
#include <stdio.h>
#include <stdlib.h>

int clipnotify(void) {
    Display *disp;
    Window root;
    Atom clip;
    XEvent evt;

    disp = XOpenDisplay(NULL);
    if (!disp) {
   	 return -1;
    }

    root = DefaultRootWindow(disp);

    clip = XInternAtom(disp, "CLIPBOARD", False);

    XFixesSelectSelectionInput(disp, root, XA_PRIMARY, XFixesSetSelectionOwnerNotifyMask);
    XFixesSelectSelectionInput(disp, root, clip, XFixesSetSelectionOwnerNotifyMask);

    XNextEvent(disp, &evt);
    XCloseDisplay(disp);
    return 0;
}
*/
import "C"

func cnotify() int {
	return int(C.clipnotify())
}
