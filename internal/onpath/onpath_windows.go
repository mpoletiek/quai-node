//go:build windows

package onpath

import (
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Add appends target's directory to the user's PATH (HKCU\Environment),
// which new terminals pick up.
func Add(target string) (Result, error) {
	dir := filepath.Dir(target)
	r := Result{Path: dir, Note: "Open a new terminal to use quai-node there."}
	if inPath(dir) {
		r.Note = ""
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return r, err
	}
	defer k.Close()
	cur, kind, err := k.GetStringValue("Path")
	if err != nil && err != registry.ErrNotExist {
		return r, err
	}
	for _, p := range filepath.SplitList(cur) {
		if strings.EqualFold(filepath.Clean(p), filepath.Clean(dir)) {
			return r, nil
		}
	}
	next := dir
	if trim(cur) != "" {
		next = strings.TrimRight(cur, ";") + ";" + dir
	}
	if kind == registry.EXPAND_SZ {
		err = k.SetExpandStringValue("Path", next)
	} else {
		err = k.SetStringValue("Path", next)
	}
	if err != nil {
		return r, err
	}
	broadcast()
	return r, nil
}

// Remove takes target's directory off the user's PATH.
func Remove(target string) error {
	dir := filepath.Dir(target)
	k, err := registry.OpenKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	cur, kind, err := k.GetStringValue("Path")
	if err != nil {
		return nil
	}
	var keep []string
	for _, p := range filepath.SplitList(cur) {
		if p != "" && !strings.EqualFold(filepath.Clean(p), filepath.Clean(dir)) {
			keep = append(keep, p)
		}
	}
	next := strings.Join(keep, ";")
	if next == cur {
		return nil
	}
	if kind == registry.EXPAND_SZ {
		err = k.SetExpandStringValue("Path", next)
	} else {
		err = k.SetStringValue("Path", next)
	}
	broadcast()
	return err
}

// broadcast tells running programs (Explorer, so new terminals) that the
// environment changed.
func broadcast() {
	env, _ := windows.UTF16PtrFromString("Environment")
	const hwndBroadcast, wmSettingChange, smtoAbortIfHung = 0xffff, 0x001A, 0x0002
	var res uintptr
	_, _, _ = sendMessageTimeout.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(env)), smtoAbortIfHung, 5000, uintptr(unsafe.Pointer(&res)))
}

var sendMessageTimeout = windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")

// quai-dash doesn't run on Windows: nothing to link.
func LinkDashConfig(target string) (string, bool, error) { return "", false, nil }
func UnlinkDashConfig(target string) error               { return nil }
