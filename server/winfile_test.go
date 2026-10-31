package main

import (
	"syscall"
	"testing"
)

// 这个 bug 值得留一条测试：syscall.UTF16FromString 遇到 NUL 会返回 nil，
// 之前整串硬拼过滤器，&filter[0] 直接 panic，点「添加文件」就闪退。
func TestBuildFilter(t *testing.T) {
	f := buildFilter(
		"所有文件", "*.*",
		"图片", "*.jpg",
	)

	if len(f) == 0 {
		t.Fatal("过滤器为空 —— UTF16FromString 又返回 nil 了")
	}

	// 格式必须是 描述\0模式\0描述\0模式\0\0
	if len(f) < 2 || f[len(f)-1] != 0 || f[len(f)-2] != 0 {
		t.Fatalf("结尾不是双 0，实际长度 %d，末尾 %v", len(f), f[len(f)-3:])
	}

	// 拆开看看每一段对不对
	var parts []string
	start := 0
	for i, v := range f {
		if v != 0 {
			continue
		}
		if i == start {
			break
		}
		parts = append(parts, syscall.UTF16ToString(f[start:i]))
		start = i + 1
	}

	want := []string{"所有文件", "*.*", "图片", "*.jpg"}
	if len(parts) != len(want) {
		t.Fatalf("段数不对：得到 %d 段 %v，期望 %d 段", len(parts), parts, len(want))
	}
	for i := range want {
		if parts[i] != want[i] {
			t.Fatalf("第 %d 段是 %q，期望 %q", i, parts[i], want[i])
		}
	}
}

// 普通字符串（不含 NUL）应该照常工作
func TestUTF16NoNul(t *testing.T) {
	u, err := syscall.UTF16FromString("选择要发送的文件")
	if err != nil {
		t.Fatalf("不含 NUL 的字符串不该报错: %v", err)
	}
	if len(u) == 0 || u[len(u)-1] != 0 {
		t.Fatal("UTF16FromString 结果应以 0 结尾")
	}
}
