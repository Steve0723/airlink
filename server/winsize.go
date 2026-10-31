package main

import (
	"syscall"
	"unsafe"

	"gioui.org/unit"
)

var (
	user32 = syscall.NewLazyDLL("user32.dll")
	gdi32  = syscall.NewLazyDLL("gdi32.dll")

	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
	procGetDC            = user32.NewProc("GetDC")
	procGetDeviceCaps    = gdi32.NewProc("GetDeviceCaps")
	procReleaseDC        = user32.NewProc("ReleaseDC")
	procSetWindowPos     = user32.NewProc("SetWindowPos")
	procGetWindowRect    = user32.NewProc("GetWindowRect")
	procGetClientRect    = user32.NewProc("GetClientRect")
)

const (
	smCXScreen = 0
	smCYScreen = 1
	logPixelsX = 88
)

// logicalScreen 返回屏幕的逻辑尺寸（96 DPI 基准）。
// Gio 的 app.Size 用的是逻辑像素，而 GetSystemMetrics 在高 DPI 下返回物理像素，
// 所以必须按 DPI 换算，否则 200% 缩放的机器上窗口会开成两倍大。
func logicalScreen() (int, int) {
	sw, _, _ := procGetSystemMetrics.Call(smCXScreen)
	sh, _, _ := procGetSystemMetrics.Call(smCYScreen)

	hdc, _, _ := procGetDC.Call(0)
	if hdc != 0 {
		defer procReleaseDC.Call(0, hdc)
	}
	dpi, _, _ := procGetDeviceCaps.Call(hdc, logPixelsX)
	if dpi == 0 {
		dpi = 96
	}

	return int(sw) * 96 / int(dpi), int(sh) * 96 / int(dpi)
}

// preferredWindowSize 按屏幕面积的三分之一算初始窗口。
// 宽高各取 sqrt(1/3)≈0.58，这样面积正好是三分之一。
//
// 注意：app.Size 的值会被当成**物理像素**（200% 缩放的机器上实测过：
// 传 904 得到 917 物理宽的窗口，也就是 458 逻辑宽）。
// 所以这里必须用屏幕的物理尺寸来算，不能先除 DPI，
// 否则窗口只有预期的一半大，面板会被裁掉。
func preferredWindowSize(savedW, savedH int) (unit.Dp, unit.Dp) {
	// 用户上次拖过的大小优先，这样每次打开都是他习惯的尺寸
	if savedW > 300 && savedH > 240 {
		return unit.Dp(savedW), unit.Dp(savedH)
	}

	// 实测 app.Size 建出来的窗口就是这么多物理像素（900 -> 912 物理），
	// 所以用物理屏幕尺寸算。0.577 = sqrt(1/3)，面积正好是屏幕三分之一。
	sw, _, _ := procGetSystemMetrics.Call(smCXScreen)
	sh, _, _ := procGetSystemMetrics.Call(smCYScreen)

	w := int(float64(sw) * 0.42)
	h := int(float64(sh) * 0.46)

	// 下限：文本面板 640x500dp = 1280x1000 物理
	if minW := 1300; w < minW {
		w = minW
	}
	if minH := 960; h < minH {
		h = minH
	}

	return unit.Dp(w), unit.Dp(h)
}

type winRect struct {
	Left, Top, Right, Bottom int32
}



// realClientSize 用 Win32 拿客户区的真实物理尺寸。
//
// Gio 在这台 200% 缩放的机器上，FrameEvent.Size 给的是实际客户区的两倍
// （实测 e.Size=2808x1872，实际客户区 1408x936）。
// 布局按 e.Size 算，面板就会超出窗口被裁掉 —— 这就是「最近记录只显示一条」
// 和「看不到复制删除」的根源。所以这里直接用系统给的尺寸覆盖。
func realClientSize(hwnd uintptr) (int, int) {
	if hwnd == 0 {
		return 0, 0
	}
	var r winRect
	if ok, _, _ := procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ok == 0 {
		return 0, 0
	}
	w, h := int(r.Right-r.Left), int(r.Bottom-r.Top)
	if w <= 0 || h <= 0 {
		return 0, 0
	}
	return w, h
}
