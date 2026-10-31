package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

// Gio 注册窗口类时不设置图标（os_windows.go 里搜不到 hIcon / WM_SETICON），
// 所以窗口标题栏和任务栏都是空白。
// rsrc.syso 只把图标塞进 exe 资源，窗口类不主动 LoadIcon 就不会用它。
//
// 这里在窗口建好后补上：先改窗口类的图标（纯数据操作，不触发窗口过程），
// 再用 PostMessage 异步通知标题栏刷新 —— 不能用 SendMessage，
// 那会同步调进 Gio 的 Go 回调，跟之前子类化崩溃是同一个坑。

var (
	procLoadImageW       = user32.NewProc("LoadImageW")
	procPostMessageW     = user32.NewProc("PostMessageW")
	procSetClassLongPtrW = user32.NewProc("SetClassLongPtrW")
	procGetSystemMetrics2 = user32.NewProc("GetSystemMetrics")
)

const (
	imageIcon      = 1
	lrLoadFromFile = 0x0010
	lrShared       = 0x8000

	wmSetIcon = 0x0080
	iconSmall = 0
	iconBig   = 1

	smCXIcon     = 11 // SM_CXICON
	smCYIcon     = 12 // SM_CYICON
	smCXIconSm   = 49 // SM_CXSMICON
	smCYIconSm   = 50 // SM_CYSMICON
)

// setWindowIcon 给窗口设置标题栏和任务栏图标。
func setWindowIcon(hwnd uintptr, iconPath string) error {
	if hwnd == 0 {
		return fmt.Errorf("窗口句柄为空")
	}
	if iconPath == "" {
		return fmt.Errorf("图标路径为空")
	}

	p, err := syscall.UTF16PtrFromString(iconPath)
	if err != nil {
		return err
	}

	// 按系统实际图标尺寸加载，避免系统再缩放导致模糊
	bigW, _, _ := procGetSystemMetrics2.Call(smCXIcon)
	bigH, _, _ := procGetSystemMetrics2.Call(smCYIcon)
	smW, _, _ := procGetSystemMetrics2.Call(smCXIconSm)
	smH, _, _ := procGetSystemMetrics2.Call(smCYIconSm)

	hBig, _, _ := procLoadImageW.Call(
		0, uintptr(unsafe.Pointer(p)), imageIcon,
		bigW, bigH, lrLoadFromFile|lrShared,
	)
	hSmall, _, _ := procLoadImageW.Call(
		0, uintptr(unsafe.Pointer(p)), imageIcon,
		smW, smH, lrLoadFromFile|lrShared,
	)

	if hBig == 0 && hSmall == 0 {
		return fmt.Errorf("加载图标失败: %s", iconPath)
	}

	// 改窗口类，之后同类的窗口也带图标
	if hBig != 0 {
		procSetClassLongPtrW.Call(hwnd, ^uintptr(13), hBig) // GCLP_HICON = -14
	}
	if hSmall != 0 {
		procSetClassLongPtrW.Call(hwnd, ^uintptr(33), hSmall) // GCLP_HICONSM = -34
	}

	// 异步通知，标题栏立刻刷新。Post 不等待处理，所以不会嵌套进 Gio 的回调
	if hBig != 0 {
		procPostMessageW.Call(hwnd, wmSetIcon, iconBig, hBig)
	}
	if hSmall != 0 {
		procPostMessageW.Call(hwnd, wmSetIcon, iconSmall, hSmall)
	}

	return nil
}
