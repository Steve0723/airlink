package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

// 打开文件 / 在资源管理器里定位，都走 ShellExecuteW。
//
// 之前用 exec.Command("explorer", ...)：explorer 是单实例的，
// 路径带空格或中文时参数经常被它丢掉，表现就是"点了没反应"。
// ShellExecute 直接走 shell 的动词解析，不经过命令行拼接。

var procShellExecuteW = shell32.NewProc("ShellExecuteW")

const swShowNormal = 1

// shellExec 调 ShellExecuteW，返回值 <= 32 表示失败。
func shellExec(op, file, params string) error {
	opP, err := syscall.UTF16PtrFromString(op)
	if err != nil {
		return err
	}
	fileP, err := syscall.UTF16PtrFromString(file)
	if err != nil {
		return err
	}

	var paramsP *uint16
	if params != "" {
		if paramsP, err = syscall.UTF16PtrFromString(params); err != nil {
			return err
		}
	}

	r, _, _ := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(opP)),
		uintptr(unsafe.Pointer(fileP)),
		uintptr(unsafe.Pointer(paramsP)),
		0,
		swShowNormal,
	)
	if r <= 32 {
		return fmt.Errorf("ShellExecute 失败，返回码 %d", r)
	}
	return nil
}

// openPath 用系统默认程序打开文件。
func openPath(path string) error {
	return shellExec("open", path, "")
}

// revealPath 在资源管理器里打开所在文件夹并选中该文件。
func revealPath(path string) error {
	return shellExec("open", "explorer.exe", "/select,"+path)
}
