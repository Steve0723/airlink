package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

// 在 Gio 的窗口上补一个 OLE 拖放接收器。
//
// 为什么不用窗口子类化：我们的 WndProc 要 CallWindowProc 转发给 Gio 的 WndProc，
// 两个都是 Go callback，嵌套调用直接崩（试过，进程静默退出）。
//
// OLE 的 IDropTarget 不走窗口过程 —— 系统在拖放时直接调用这个 COM 对象的方法，
// 调用栈里没有 Gio 的 callback，所以不会嵌套。
// 实现参考 winit 的 drop_handler.rs（Rust 那边就是这么做的）。

var (
	ole32   = syscall.NewLazyDLL("ole32.dll")
	shell32 = syscall.NewLazyDLL("shell32.dll")

	procOleInitialize    = ole32.NewProc("OleInitialize")
	procRegisterDragDrop = ole32.NewProc("RegisterDragDrop")
	procRevokeDragDrop   = ole32.NewProc("RevokeDragDrop")
	procReleaseStgMedium = ole32.NewProc("ReleaseStgMedium")

	procDragQueryFileW = shell32.NewProc("DragQueryFileW")

	procEnumWindows        = user32.NewProc("EnumWindows")
	procGetWindowThreadPID = user32.NewProc("GetWindowThreadProcessId")
	procGetClassNameW      = user32.NewProc("GetClassNameW")
)

const (
	cfHDrop         = 15
	dvAspectContent = 1
	tymedHGlobal    = 1

	sOK          = 0
	eNoInterface  = 0x80004002
	dvEFormatEtc  = 0x80040064
	dropEffectCopy = 1
	dropEffectNone = 0
)

// formatEtc 对应 Win32 的 FORMATETC，64 位下是 24 字节。
type formatEtc struct {
	cfFormat uint16
	_        uint16 // 对齐填充
	ptd      uintptr
	dwAspect uint32
	lindex   int32
	tymed    uint32
}

// stgMedium 对应 STGMEDIUM，64 位下是 16 字节。
type stgMedium struct {
	tymed          uint32
	_              uint32 // 对齐填充
	hGlobal        uintptr
	pUnkForRelease uintptr
}

// dropTarget 是给 OLE 用的 COM 对象。
// 第一个字段必须是 vtable 指针，COM 就是这么约定的。
type dropTarget struct {
	vtbl     *dropTargetVtbl
	refCount int32
}

type dropTargetVtbl struct {
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr
	DragEnter      uintptr
	DragOver       uintptr
	DragLeave      uintptr
	Drop           uintptr
}

// 这两个必须放包级：COM 对象和 vtable 被系统长期持有，
// 局部变量会被 GC 回收，之后系统再调方法就访问野指针了。
var (
	gDropTarget *dropTarget
	gDropVtbl   *dropTargetVtbl
)

// dropCh 把拖进来的路径交给界面线程处理
var dropCh = make(chan []string, 4)

// ---------- IDropTarget 的七个方法 ----------

func dtQueryInterface(this, riid, ppv uintptr) uintptr {
	if ppv == 0 {
		return eNoInterface
	}
	// 简化处理：IUnknown 和 IDropTarget 都返回自己，够用
	*(*uintptr)(unsafe.Pointer(ppv)) = this
	dt := (*dropTarget)(unsafe.Pointer(this))
	dt.refCount++
	return sOK
}

func dtAddRef(this uintptr) uintptr {
	dt := (*dropTarget)(unsafe.Pointer(this))
	dt.refCount++
	return uintptr(dt.refCount)
}

func dtRelease(this uintptr) uintptr {
	dt := (*dropTarget)(unsafe.Pointer(this))
	dt.refCount--
	return uintptr(dt.refCount)
}

func dtDragEnter(this, pDataObj, keyState, pt, pdwEffect uintptr) uintptr {
	setDropEffect(pdwEffect, dropEffectCopy)
	return sOK
}

func dtDragOver(this, keyState, pt, pdwEffect uintptr) uintptr {
	setDropEffect(pdwEffect, dropEffectCopy)
	return sOK
}

func dtDragLeave(this uintptr) uintptr {
	return sOK
}

func dtDrop(this, pDataObj, keyState, pt, pdwEffect uintptr) uintptr {
	setDropEffect(pdwEffect, dropEffectCopy)

	if paths := dropPaths(pDataObj); len(paths) > 0 {
		select {
		case dropCh <- paths:
		default: // 队列满就丢，不要卡住 OLE
		}
		// 让界面立刻刷新一帧来处理
		if mainUI != nil && mainUI.w != nil {
			mainUI.w.Invalidate()
		}
	}
	return sOK
}

func setDropEffect(pdwEffect uintptr, effect uint32) {
	if pdwEffect != 0 {
		*(*uint32)(unsafe.Pointer(pdwEffect)) = effect
	}
}

// dropPaths 从 IDataObject 里取出拖进来的文件路径。
func dropPaths(pDataObj uintptr) []string {
	if pDataObj == 0 {
		return nil
	}

	fe := formatEtc{
		cfFormat: cfHDrop,
		dwAspect: dvAspectContent,
		lindex:   -1,
		tymed:    tymedHGlobal,
	}
	var sm stgMedium

	// IDataObject 的 vtable：QueryInterface/AddRef/Release/GetData/...
	// GetData 是第 4 个（索引 3）
	vtbl := *(*uintptr)(unsafe.Pointer(pDataObj))
	getData := *(*uintptr)(unsafe.Pointer(vtbl + 3*unsafe.Sizeof(uintptr(0))))

	hr, _, _ := syscall.SyscallN(getData,
		pDataObj,
		uintptr(unsafe.Pointer(&fe)),
		uintptr(unsafe.Pointer(&sm)),
	)
	if int32(hr) != sOK {
		return nil
	}
	defer procReleaseStgMedium.Call(uintptr(unsafe.Pointer(&sm)))

	hdrop := sm.hGlobal
	if hdrop == 0 {
		return nil
	}

	n, _, _ := procDragQueryFileW.Call(hdrop, 0xFFFFFFFF, 0, 0)
	count := int(n)
	if count <= 0 {
		return nil
	}

	paths := make([]string, 0, count)
	for i := 0; i < count; i++ {
		// 先问长度，再按长度分配，避免固定 260 截断长路径
		ln, _, _ := procDragQueryFileW.Call(hdrop, uintptr(i), 0, 0)
		buf := make([]uint16, int(ln)+1)
		procDragQueryFileW.Call(
			hdrop,
			uintptr(i),
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(len(buf)),
		)
		if p := syscall.UTF16ToString(buf); p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// enableOLEDrop 给窗口注册 OLE 拖放接收器。
// 必须在窗口所在线程调用（STA 要求），所以放在第一帧之后。
func enableOLEDrop(hwnd uintptr) error {
	if hwnd == 0 {
		return fmt.Errorf("窗口句柄为空")
	}

	hr, _, _ := procOleInitialize.Call(0)
	// S_OK(0) 或 S_FALSE(1) 都算成功，S_FALSE 表示之前已经初始化过
	if uint32(hr) != 0 && uint32(hr) != 1 {
		return fmt.Errorf("OleInitialize 失败: %#x", hr)
	}

	gDropVtbl = &dropTargetVtbl{
		QueryInterface: syscall.NewCallback(dtQueryInterface),
		AddRef:         syscall.NewCallback(dtAddRef),
		Release:        syscall.NewCallback(dtRelease),
		DragEnter:      syscall.NewCallback(dtDragEnter),
		DragOver:       syscall.NewCallback(dtDragOver),
		DragLeave:      syscall.NewCallback(dtDragLeave),
		Drop:           syscall.NewCallback(dtDrop),
	}
	gDropTarget = &dropTarget{vtbl: gDropVtbl, refCount: 1}

	hr, _, _ = procRegisterDragDrop.Call(hwnd, uintptr(unsafe.Pointer(gDropTarget)))
	if int32(hr) != 0 {
		return fmt.Errorf("RegisterDragDrop 失败: %#x", hr)
	}
	return nil
}

// findOwnWindow 找出本进程的主窗口。
// Gio 不暴露 HWND，只能按「进程 ID + 窗口类名是 exe 名」来认。
func findOwnWindow() uintptr {
	pid := uint32(os.Getpid())
	var found uintptr

	cb := syscall.NewCallback(func(hwnd, lparam uintptr) uintptr {
		var wpid uint32
		procGetWindowThreadPID.Call(hwnd, uintptr(unsafe.Pointer(&wpid)))
		if wpid != pid {
			return 1 // 继续枚举
		}
		cls := make([]uint16, 256)
		procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&cls[0])), 256)
		name := strings.ToLower(syscall.UTF16ToString(cls))
		if strings.HasSuffix(name, ".exe") {
			found = hwnd
			return 0 // 找到就停
		}
		return 1
	})

	procEnumWindows.Call(cb, 0)
	return found
}
