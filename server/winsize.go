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
	// 用户上次拖过的大小优先，但要卡上限：
	// 某次拖到接近全屏的话，之后每次打开都会溢出屏幕。
	if savedW > 300 && savedH > 240 {
		sw, _, _ := procGetSystemMetrics.Call(smCXScreen)
		sh, _, _ := procGetSystemMetrics.Call(smCYScreen)
		// 上限收到 60%：之前 75% 太松，用户拖到 72% 就顶满屏幕了
		maxW := int(float64(sw) * 0.60)
		maxH := int(float64(sh) * 0.60)
		if savedW > maxW {
			savedW = maxW
		}
		if savedH > maxH {
			savedH = maxH
		}
		return unit.Dp(savedW), unit.Dp(savedH)
	}

	// 实测 app.Size 建出来的窗口就是这么多物理像素（900 -> 912 物理），
	// 所以用物理屏幕尺寸算。0.577 = sqrt(1/3)，面积正好是屏幕三分之一。
	sw, _, _ := procGetSystemMetrics.Call(smCXScreen)
	sh, _, _ := procGetSystemMetrics.Call(smCYScreen)

	// 用户确认的尺寸：569x357 dp = 1138x714 物理，占屏幕 36%x34%
	w := int(float64(sw) * 0.365)
	h := int(float64(sh) * 0.343)

	// 下限只防呆：太小的窗口界面会挤成一团
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

	// SetWindowPos 在这台 200% 缩放的机器上会被 DPI 虚拟化：
	// 传 1138 实际只得到 569，正好差一半。所以这里乘以 2 补偿。
	// （闭环修正行不通——SetWindowPos 是异步的，紧接着 GetClientRect
	// 读到的还是旧值，按比例算会越改越小。）
	procSetWindowPos.Call(hwnd, 0, 0, 0,
		uintptr(targetW*2), uintptr(targetH*2),
		swpNoZOrder|swpNoMove)
}
