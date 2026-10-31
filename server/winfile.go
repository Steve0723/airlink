package main

import (
	"path/filepath"
	"syscall"
	"unsafe"
)

// 文件选择框直接调 Win32 的 GetOpenFileNameW。
// 用系统对话框而不是自绘，是因为它带完整的路径记忆、快捷方式、网络位置，
// 自绘一个根本追不上。

var (
	comdlg32 = syscall.NewLazyDLL("comdlg32.dll")

	procGetOpenFileNameW = comdlg32.NewProc("GetOpenFileNameW")
)

type openFileNameW struct {
	lStructSize       uint32
	hwndOwner         uintptr
	hInstance         uintptr
	lpstrFilter       *uint16
	lpstrCustomFilter *uint16
	nMaxCustFilter    uint32
	nFilterIndex      uint32
	lpstrFile         *uint16
	nMaxFile          uint32
	lpstrFileTitle    *uint16
	nMaxFileTitle     uint32
	lpstrInitialDir   *uint16
	lpstrTitle        *uint16
	flags             uint32
	nFileOffset       uint16
	nFileExtension    uint16
	lpstrDefExt       *uint16
	lCustData         uintptr
	lpfnHook          uintptr
	lpTemplateName    *uint16
	pvReserved        uintptr
	dwReserved        uint32
	flagsEx           uint32
}

const (
	ofnReadOnly     = 0x00000001
	ofnOverwrite    = 0x00000002
	ofnHideReadOnly = 0x00000004
	ofnNoChangeDir  = 0x00000008
	ofnAllowMulti   = 0x00000200
	ofnPathMustExist = 0x00000800
	ofnFileMustExist = 0x00001000
	ofnExplorer     = 0x00080000
)

// buildFilter 拼出 GetOpenFileNameW 需要的过滤器：
// 描述\0模式\0描述\0模式\0\0
//
// 不能直接 UTF16FromString 整串：那个函数遇到 NUL 会返回 nil（它把 NUL 当非法字符），
// 于是 &filter[0] 直接 index out of range 崩掉。必须逐段转换再拼。
func buildFilter(pairs ...string) []uint16 {
	out := make([]uint16, 0, 256)
	for _, s := range pairs {
		u, err := syscall.UTF16FromString(s)
		if err != nil {
			continue
		}
		// UTF16FromString 已经把结尾的 0 带上了
		out = append(out, u...)
	}
	out = append(out, 0) // 整个过滤器以双 0 结尾
	return out
}

// openFileDialog 弹出系统文件选择框，返回选中的完整路径。
// 用户取消时返回空切片，不是错误。
func openFileDialog() ([]string, error) {
	// 多选时系统把结果写成 "目录\0文件1\0文件2\0\0"
	buf := make([]uint16, 32768)

	filter := buildFilter(
		"所有文件", "*.*",
		"图片", "*.jpg;*.jpeg;*.png;*.gif;*.webp",
		"文档", "*.pdf;*.doc;*.docx;*.txt;*.md",
		"压缩包", "*.zip;*.rar;*.7z",
	)
	title, err := syscall.UTF16FromString("选择要发送的文件")
	if err != nil {
		return nil, err
	}

	ofn := openFileNameW{
		lpstrFilter:  &filter[0],
		lpstrFile:    &buf[0],
		nMaxFile:     uint32(len(buf)),
		lpstrTitle:   &title[0],
		flags:        ofnExplorer | ofnFileMustExist | ofnPathMustExist | ofnHideReadOnly | ofnAllowMulti,
		lStructSize:  uint32(unsafe.Sizeof(openFileNameW{})),
		nFilterIndex: 1,
	}

	r, _, _ := procGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	if r == 0 {
		// 用户取消也会走到这里，不当成错误
		return nil, nil
	}

	return parseMultiSelect(buf), nil
}

// parseMultiSelect 解析多选结果。
// 只选一个文件时，buf 里直接就是完整路径；
// 选多个时，第一段是目录，后面每段是文件名。
func parseMultiSelect(buf []uint16) []string {
	var parts []string
	start := 0
	for i, v := range buf {
		if v != 0 {
			continue
		}
		if i == start {
			break // 连续两个 0，结束
		}
		parts = append(parts, syscall.UTF16ToString(buf[start:i]))
		start = i + 1
	}

	if len(parts) == 0 {
		return nil
	}
	if len(parts) == 1 {
		return parts
	}

	dir := parts[0]
	out := make([]string, 0, len(parts)-1)
	for _, name := range parts[1:] {
		out = append(out, filepath.Join(dir, name))
	}
	return out
}
