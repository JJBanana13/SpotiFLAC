// Package uiruntime mirrors the parts of the Wails runtime used by SpotiFLAC.
// In the desktop build every call is forwarded to Wails. When a Bridge is
// registered (web server mode) events and dialogs are routed to the browser
// instead, because there is no Wails context available.
package uiruntime

import (
	"context"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

type OpenDialogOptions = wailsRuntime.OpenDialogOptions
type SaveDialogOptions = wailsRuntime.SaveDialogOptions
type FileFilter = wailsRuntime.FileFilter

type Bridge interface {
	EventsEmit(eventName string, optionalData ...interface{})
	OpenDirectoryDialog(options OpenDialogOptions) (string, error)
	OpenFileDialog(options OpenDialogOptions) (string, error)
	OpenMultipleFilesDialog(options OpenDialogOptions) ([]string, error)
	SaveFileDialog(options SaveDialogOptions) (string, error)
	BrowserOpenURL(url string)
}

var bridge Bridge

// SetBridge must be called before any other function of this package is used.
func SetBridge(b Bridge) {
	bridge = b
}

func IsWeb() bool {
	return bridge != nil
}

func EventsEmit(ctx context.Context, eventName string, optionalData ...interface{}) {
	if bridge != nil {
		bridge.EventsEmit(eventName, optionalData...)
		return
	}
	wailsRuntime.EventsEmit(ctx, eventName, optionalData...)
}

func OpenDirectoryDialog(ctx context.Context, options OpenDialogOptions) (string, error) {
	if bridge != nil {
		return bridge.OpenDirectoryDialog(options)
	}
	return wailsRuntime.OpenDirectoryDialog(ctx, options)
}

func OpenFileDialog(ctx context.Context, options OpenDialogOptions) (string, error) {
	if bridge != nil {
		return bridge.OpenFileDialog(options)
	}
	return wailsRuntime.OpenFileDialog(ctx, options)
}

func OpenMultipleFilesDialog(ctx context.Context, options OpenDialogOptions) ([]string, error) {
	if bridge != nil {
		return bridge.OpenMultipleFilesDialog(options)
	}
	return wailsRuntime.OpenMultipleFilesDialog(ctx, options)
}

func SaveFileDialog(ctx context.Context, options SaveDialogOptions) (string, error) {
	if bridge != nil {
		return bridge.SaveFileDialog(options)
	}
	return wailsRuntime.SaveFileDialog(ctx, options)
}

func BrowserOpenURL(ctx context.Context, url string) {
	if bridge != nil {
		bridge.BrowserOpenURL(url)
		return
	}
	wailsRuntime.BrowserOpenURL(ctx, url)
}

func WindowShow(ctx context.Context) {
	if bridge != nil {
		return
	}
	wailsRuntime.WindowShow(ctx)
}

func WindowUnminimise(ctx context.Context) {
	if bridge != nil {
		return
	}
	wailsRuntime.WindowUnminimise(ctx)
}
