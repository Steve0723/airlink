package main

import (
	"unsafe"
)

// Gio 的 pointer 事件只给「相对当前输入区域」的坐标，
// 拿不到窗口坐标 —— 所以右键菜单没法直接定位到鼠标。
// 这里直接用 Win32 查光标位置再换算成客户区坐标。

var (
	procGetCursorPos   = user32.NewProc("GetCursorPos")
	procScreenToClient = user32.NewProc("ScreenToClient")
)

type winPoint struct {
	X, Y int32
}

// cursorInClient 返回鼠标在窗口客户区的位置（物理像素）。
func cursorInClient(hwnd uintptr) (winPoint, bool) {
	if hwnd == 0 {
		return winPoint{}, false
	}
	var pt winPoint
	if r, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt))); r == 0 {
		return winPoint{}, false
	}
	if r, _, _ := procScreenToClient.Call(hwnd, uintptr(unsafe.Pointer(&pt))); r == 0 {
		return winPoint{}, false
	}
	return pt, true
}
