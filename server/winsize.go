package main

import (
	"syscall"

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
func preferredWindowSize() (unit.Dp, unit.Dp) {
	sw, _, _ := procGetSystemMetrics.Call(smCXScreen)
	sh, _, _ := procGetSystemMetrics.Call(smCYScreen)

	w := int(float64(sw) * 0.45)
	h := int(float64(sh) * 0.45)

	// 下限保证设置面板（602dp 高 = 1204 物理）放得下，
	// 但不能超过屏幕的 90%，否则小屏上反而溢出。
	// 上限不需要：0.58 本身就把窗口限制在屏幕面积的三分之一。
	minW, minH := 1120, 900
	if capW := int(float64(sw) * 0.9); minW > capW {
		minW = capW
	}
	if capH := int(float64(sh) * 0.9); minH > capH {
		minH = capH
	}
	if w < minW {
		w = minW
	}
	if h < minH {
		h = minH
	}

	return unit.Dp(w), unit.Dp(h)
}

type winRect struct {
	Left, Top, Right, Bottom int32
}

// applyWindowSize 用 Win32 直接把窗口设成指定物理尺寸。
//
// app.Size 的单位在这台机器上换算得不对（传 904 得到 917 物理宽的窗口，
// 传 1400 又是 1573，不成比例），结果窗口只有预期的一半，面板被裁掉。
// 直接在窗口建好后 SetWindowPos 最可靠。
//
// 必须在独立线程调用：SetWindowPos 会同步发 WM_SIZE，
// 而窗口过程是 Gio 的 Go 回调，在界面线程里调会嵌套。
func applyWindowSize(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	sw, _, _ := procGetSystemMetrics.Call(smCXScreen)
	sh, _, _ := procGetSystemMetrics.Call(smCYScreen)

	// 屏幕的 45%（再大就显笨重）
	w := int(float64(sw) * 0.45)
	h := int(float64(sh) * 0.45)

	// 下限只保证面板能放下（文本面板 640x500dp = 1280x1000 物理）
	if minW := 1120; w < minW {
		w = minW
	}
	if minH := 900; h < minH {
		h = minH
	}
	// 别超出屏幕
	if capW := int(float64(sw) * 0.92); w > capW {
		w = capW
	}
	if capH := int(float64(sh) * 0.92); h > capH {
		h = capH
	}

	const swpNoZOrder = 0x0004
	const swpNoMove = 0x0002
	procSetWindowPos.Call(hwnd, 0, 0, 0, uintptr(w), uintptr(h), swpNoZOrder|swpNoMove)
}

