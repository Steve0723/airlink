package main

import (
	"syscall"
	"unsafe"
)

// Windows 剪贴板直接走 Win32 API。
// 之前用 walk.Clipboard()，但为了一个剪贴板把整个 GUI 库拉进来不值当——
// 换成 Gio 之后 walk 就只剩这一处用途，干脆自己实现，依赖可以彻底去掉。
var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procOpenClipboard    = user32.NewProc("OpenClipboard")
	procCloseClipboard   = user32.NewProc("CloseClipboard")
	procEmptyClipboard   = user32.NewProc("EmptyClipboard")
	procSetClipboardData = user32.NewProc("SetClipboardData")
	procGetClipboardData = user32.NewProc("GetClipboardData")

	procGlobalAlloc  = kernel32.NewProc("GlobalAlloc")
	procGlobalLock   = kernel32.NewProc("GlobalLock")
	procGlobalUnlock = kernel32.NewProc("GlobalUnlock")
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002
)

// setClipboard 把文本写进 Windows 剪贴板。
func setClipboard(text string) error {
	utf16, err := syscall.UTF16FromString(text)
	if err != nil {
		return err
	}

	if r, _, _ := procOpenClipboard.Call(0); r == 0 {
		return syscall.GetLastError()
	}
	defer procCloseClipboard.Call()

	procEmptyClipboard.Call()

	size := uintptr(len(utf16) * 2)
	h, _, _ := procGlobalAlloc.Call(gmemMoveable, size)
	if h == 0 {
		return syscall.GetLastError()
	}

	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return syscall.GetLastError()
	}
	// 复制 UTF-16 数据到全局内存块
	dst := unsafe.Slice((*uint16)(unsafe.Pointer(p)), len(utf16))
	copy(dst, utf16)
	procGlobalUnlock.Call(h)

	// 成功后所有权归系统，不能再 GlobalFree
	if r, _, _ := procSetClipboardData.Call(cfUnicodeText, h); r == 0 {
		return syscall.GetLastError()
	}
	return nil
}

// getClipboardText 读 Windows 剪贴板里的文本，没有则返回空串。
func getClipboardText() string {
	if r, _, _ := procOpenClipboard.Call(0); r == 0 {
		return ""
	}
	defer procCloseClipboard.Call()

	h, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return ""
	}

	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return ""
	}
	defer procGlobalUnlock.Call(h)

	// 按 UTF-16 扫到结尾的 0
	var units []uint16
	for i := uintptr(0); ; i++ {
		v := *(*uint16)(unsafe.Pointer(p + i*2))
		if v == 0 {
			break
		}
		units = append(units, v)
		if i > 1<<20 { // 防止异常数据把内存读穿
			break
		}
	}
	return syscall.UTF16ToString(units)
}
