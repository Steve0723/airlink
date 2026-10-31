package main

import (
	"syscall"
	"time"
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
	procSetThreadDpiCtx  = user32.NewProc("SetThreadDpiAwarenessContext")
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
	// app.Size 收的就是物理像素，1:1。实测传 1138 得到 1138 物理宽。
	//
	// 之前所有的混乱都来自 forceWindowSize：它用 SetWindowPos 事后校正，
	// 但 Gio 的窗口管理每帧会把尺寸改回去，两边打架，结果时大时小。
	// 去掉之后这里就是唯一的尺寸来源，稳定可预测。
	//
	// 0.365 / 0.343 是用户确认的比例，落成约 569x357 dp（屏幕 36%x34%）。
	sw, _, _ := procGetSystemMetrics.Call(smCXScreen)
	sh, _, _ := procGetSystemMetrics.Call(smCYScreen)

	w := int(float64(sw) * 0.365)
	h := int(float64(sh) * 0.343)

	if minW := 1000; w < minW {
		w = minW
	}
	if minH := 700; h < minH {
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


// forceWindowSize 用 Win32 把窗口强制设成目标尺寸（物理像素）。
//
// app.Size 在这台 200% 缩放的机器上完全不可预测：
// 传 569 得 569、传 1138 得 1510、传 2276 得 3020，比例在 1.0~1.6 之间乱跳。
// 所以窗口建好之后用 SetWindowPos 设一次，再读回客户区尺寸按比例修正，
// 迭代几次就能收敛到目标值。
func forceWindowSize(hwnd uintptr, targetW, targetH int) {
	if hwnd == 0 || targetW <= 0 || targetH <= 0 {
		return
	}
	const swpNoZOrder = 0x0004
	const swpNoMove = 0x0002
	const flags = swpNoZOrder | swpNoMove

	// SetWindowPos 在这台 200% 缩放的机器上会被 DPI 虚拟化，
	// 而且比例不固定：传 1138 有时得到 569（0.5 倍），有时得到 1872（0.82 倍）。
	// 所以只能闭环：设一次、等一下、读回实际尺寸、按比例再设。
	// 关键是每次都要 sleep —— SetWindowPos 是异步的，
	// 不等待就读到旧值，按比例反算会越改越小。
	w, h := targetW, targetH
	for i := 0; i < 5; i++ {
		procSetWindowPos.Call(hwnd, 0, 0, 0, uintptr(w), uintptr(h), flags)
		time.Sleep(120 * time.Millisecond)

		var r winRect
		if ok, _, _ := procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ok == 0 {
			return
		}
		aw, ah := int(r.Right-r.Left), int(r.Bottom-r.Top)
		if aw <= 0 || ah <= 0 {
			return
		}

		dw, dh := aw-targetW, ah-targetH
		if dw < 0 {
			dw = -dw
		}
		if dh < 0 {
			dh = -dh
		}
		if dw <= 8 && dh <= 8 {
			return
		}

		// 按「目标/实际」的比例修正下一次的输入值
		w = w * targetW / aw
		h = h * targetH / ah
	}
}

// clampWindowSize 把尺寸夹到屏幕 60% 以内。
// app.Size 和 forceWindowSize 两条路径都必须过一遍，
// 之前只在 app.Size 那边做了限制，forceWindowSize 拿着未修正的
// 原始值直接设，上限形同虚设。
func clampWindowSize(w, h int) (int, int) {
	sw, _, _ := procGetSystemMetrics.Call(smCXScreen)
	sh, _, _ := procGetSystemMetrics.Call(smCYScreen)
	if maxW := int(float64(sw) * 0.60); w > maxW {
		w = maxW
	}
	if maxH := int(float64(sh) * 0.60); h > maxH {
		h = maxH
	}
	return w, h
}
