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
// 太小的屏幕上界面会挤，所以有下限；超大屏上别开成巨无霸，所以有上限。
func preferredWindowSize() (unit.Dp, unit.Dp) {
	sw, sh := logicalScreen()

	w := int(float64(sw) * 0.58)
	h := int(float64(sh) * 0.58)

	if w < 880 {
		w = 880
	}
	if h < 620 {
		h = 620
	}
	if w > 1400 {
		w = 1400
	}
	if h > 1000 {
		h = 1000
	}

	return unit.Dp(w), unit.Dp(h)
}
