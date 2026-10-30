package main

import (
	"os"
	"path/filepath"

	"github.com/getlantern/systray"
)

// runTray 常驻托盘。
//
// 界面现在是 Edge 的独立窗口，用户把它关掉之后服务还在跑——
// 没有托盘的话程序既打不开也没法退出，只能去任务管理器杀进程。
func runTray(srv *Server, port int) {
	icon, err := os.ReadFile(filepath.Join(filepath.Dir(srv.cfg), "icon.ico"))
	if err != nil {
		icon, _ = os.ReadFile("icon.ico")
	}

	systray.Run(func() {
		if len(icon) > 0 {
			systray.SetIcon(icon)
		}
		systray.SetTitle("AirLink")
		systray.SetTooltip("AirLink · 手机电脑互传")

		mOpen := systray.AddMenuItem("打开窗口", "打开 AirLink 界面")

		addr := "未找到局域网地址"
		if eps := srv.orderedEndpoints(); len(eps) > 0 {
			addr = eps[0].Addr()
		}
		mAddr := systray.AddMenuItem(addr, "手机在这个地址访问")
		mAddr.Disable()

		systray.AddSeparator()
		mQuit := systray.AddMenuItem("退出", "停止服务并退出")

		go func() {
			for {
				select {
				case <-mOpen.ClickedCh:
					openConsoleWindow(port)
				case <-mQuit.ClickedCh:
					systray.Quit()
					return
				}
			}
		}()
	}, func() {})

	os.Exit(0)
}
