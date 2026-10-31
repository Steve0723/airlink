package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gioui.org/app"
	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/io/system"
	"gioui.org/font/opentype"
	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	qrcode "github.com/skip2/go-qrcode"
)

// 主题色，和托盘图标保持一致。
var (
	uiGreen  = color.NRGBA{R: 0x1D, G: 0x9E, B: 0x75, A: 0xFF}
	uiDanger = color.NRGBA{R: 0xD1, G: 0x4B, B: 0x4B, A: 0xFF}
	uiInk   = color.NRGBA{R: 0x17, G: 0x1B, B: 0x19, A: 0xFF}
	uiMuted = color.NRGBA{R: 0x6B, G: 0x72, B: 0x80, A: 0xFF}
	uiPage  = color.NRGBA{R: 0xFA, G: 0xFB, B: 0xFA, A: 0xFF}
	uiCard  = color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
	uiTile  = color.NRGBA{R: 0xED, G: 0xF0, B: 0xEE, A: 0xFF}
	uiLine  = color.NRGBA{R: 0xE6, G: 0xEA, B: 0xE8, A: 0xFF}
)

// debugLayout 打开后会把卡片尺寸打到 stderr，排查布局问题时用。
var debugLayout = os.Getenv("AIRLINK_DEBUG_LAYOUT") != ""

// rowState 保存文件列表一行的交互状态。
// 这里没用 widget.Clickable：右键和左键要在同一个输入区域里区分，
// 而 Clickable 会独占 pointer 事件，右键拿不到。
type rowState struct {
	hovered bool
}

// mainUI 供托盘和单实例检测唤起窗口用。
var mainUI *ui

type ui struct {
	srv *Server
	th  *material.Theme
	w   *app.Window

	// 二维码按内容缓存，配对码变了才重新生成
	qrOp  paint.ImageOp
	qrKey string

	btnFile, btnText, btnClip, btnSettings widget.Clickable
	btnCopy, btnReset                     widget.Clickable

	// 每帧从 store 读一次文件列表，用文件数 + ID 判断是否需要换数据
	files    []FileEntry
	fileKey  string
	fileRows []rowState
	selected int // 当前选中的文件下标，-1 表示没选

	hwnd uintptr // 主窗口句柄，查光标位置用

	// interactive 表示当前正在画的那一层能不能交互。
	// 主界面被面板盖住时置 false，面板自己画的时候再置回 true。
	interactive bool

	list layout.List

	btnAddFile, btnOpen, btnReveal, btnDeleteFile widget.Clickable

	// 右键菜单
	ctxMenu                         contextMenu
	ctxOpen, ctxReveal, ctxCopyName widget.Clickable
	ctxDelete                       widget.Clickable

	// 文件保存位置
	btnPickDir, btnResetDir widget.Clickable

	// 最近记录条数选项
	limitChips [5]widget.Clickable

	// 文本面板的标签页：0=文字互传 1=剪贴板
	clipTab          int
	tabText, tabClip widget.Clickable

	// 设置面板中间的滚动区
	settingsScroll layout.List

	// 最近一次测到的真实客户区尺寸（物理像素），关窗时存进配置
	lastW, lastH int
	// 窗口尺寸稳定后才开始记录 lastW/lastH。
	// 否则会记下 app.Size 刚建出来的中间尺寸（比目标大很多）。
	sizeReady bool
	backdropTags                    [4]int // 菜单外四个方向的点击区 tag

	// ---------- 面板 ----------
	showText     bool
	showSettings bool

	// 文本面板：输入框、记录列表、每行的复制/删除按钮
	textEditor  widget.Editor
	btnSend     widget.Clickable
	btnClear    widget.Clickable
	btnCloseText widget.Clickable
	clipList   layout.List
	clips      []ClipItem
	clipKey    string
	clipRows   []clipRow

	// 设置面板
	btnCloseSettings  widget.Clickable
	btnCancelSettings widget.Clickable
	btnSaveSettings   widget.Clickable
	relayURL          widget.Editor
	relayKey          widget.Editor
	netChips          []widget.Clickable
	phoneList         layout.List
	phones            []PhoneInfo
	phoneRows         []phoneRow
	settingsError     string
}

// clipRow 保存一行记录的操作按钮状态。
// Gio 的 Clickable 必须有稳定的内存地址，不能每帧新建。
type clipRow struct {
	copy, del widget.Clickable
}

// runUI 启动原生界面。这是主线程，Gio 的事件循环在这里跑。
func runUI(srv *Server) {
	th := material.NewTheme()
	if coll := loadUIFont(); coll != nil {
		th.Shaper = text.NewShaper(text.WithCollection(coll))
	}

	u := &ui{srv: srv, th: th, selected: -1, interactive: true}
	u.list.Axis = layout.Vertical
	u.settingsScroll.Axis = layout.Vertical
	// layout.List 的 Axis 零值是 Horizontal，不设就是横向排！
	// 之前 runUI 漏了这一行，只有快照路径设了，所以离屏渲染正常、
	// 实际窗口里记录全横着排 —— 排查了半天才发现。
	u.clipList.Axis = layout.Vertical
	mainUI = u

	go func() {
		w := new(app.Window)
		u.w = w

		ww, wh := preferredWindowSize(srv.state.WinW, srv.state.WinH)
		w.Option(
			app.Title("AirLink"),
			app.Size(ww, wh),
			// 物理像素：500x400 逻辑 = 1000x800 物理。
			// 之前设 1520x1040 会把窗口硬撑到 760x520，压不下去。
			// 允许拖到比较小，面板会自适应（clamp + 滚动）
			app.MinSize(unit.Dp(560), unit.Dp(440)),
		)

		// 文件列表和配对码都可能被手机那边改动，定时重绘
		go func() {
			for range time.Tick(time.Second) {
				w.Invalidate()
			}
		}()

		var ops op.Ops
		dropReady := false
		for {
			e := w.Event()
			switch e := e.(type) {
			case app.DestroyEvent:
				// 窗口关掉前把尺寸存下来，下次打开还是这个大小
				os.Exit(0)
			case app.FrameEvent:
				// 第一帧时窗口才真正建好，这时候才能挂 OLE 拖放
				if !dropReady {
					dropReady = true
					hwnd := findOwnWindow()
					u.hwnd = hwnd
					if err := enableOLEDrop(hwnd); err != nil {
						verbosef("拖放注册失败: %v", err)
					}
					// Gio 不会设窗口图标，这里补上
					iconPath := filepath.Join(filepath.Dir(u.srv.cfg), "icon.ico")
					if err := setWindowIcon(hwnd, iconPath); err != nil {
						verbosef("设置窗口图标失败: %v", err)
					}
				}

				// 拖进来和选择框选的文件都在界面线程处理，避免和布局并发写状态
				for {
					var paths []string
					select {
					case paths = <-dropCh:
					case paths = <-pickedFiles:
					default:
					}
					if paths == nil {
						break
					}
					for _, p := range paths {
						if err := u.addFile(p); err != nil {
							verbosef("添加文件失败 %s: %v", p, err)
						}
					}
					u.fileKey = ""
				}
				select {
				case dir := <-pickedDir:
					u.applySaveDir(dir)
				default:
				}

				gtx := app.NewContext(&ops, e)
				u.layout(gtx)
				e.Frame(gtx.Ops)
			}
		}
	}()

	app.Main()
}

// snapshotUI 把界面离屏渲染成 PNG。
// 开发时用：机器锁屏或者没有显示器的情况下也能检查布局。
func snapshotUI(srv *Server, w, h int, scale float32, path, panel string) error {
	win, err := headless.NewWindow(w, h)
	if err != nil {
		return err
	}
	defer win.Release()

	th := material.NewTheme()
	if coll := loadUIFont(); coll != nil {
		th.Shaper = text.NewShaper(text.WithCollection(coll))
	}
	u := &ui{srv: srv, th: th, selected: -1, interactive: true}
	u.list.Axis = layout.Vertical
	u.settingsScroll.Axis = layout.Vertical
	u.clipList.Axis = layout.Vertical
	u.settingsScroll.Axis = layout.Vertical
	u.refreshFiles()
	switch panel {
	case "text":
		u.showText = true
	case "settings":
		u.showSettings = true
	case "menu":
		// 右键菜单需要交互才出现，快照时手动打开方便检查样式
		u.selected = 0
		u.ctxMenu = contextMenu{visible: true, row: 0, pos: image.Pt(520, 600)}
	}

	var ops op.Ops
	// 根约束用 Min=0 模拟真实窗口（Exact 会掩盖布局问题）
	gtx := layout.Context{
		Ops:         &ops,
		Constraints: layout.Constraints{Max: image.Pt(w, h)},
		Metric:      unit.Metric{PxPerDp: scale, PxPerSp: scale},
	}
	u.layout(gtx)

	if err := win.Frame(&ops); err != nil {
		return err
	}

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if err := win.Screenshot(img); err != nil {
		return err
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

// raise 把窗口提到前台。托盘点「打开窗口」和第二次启动 exe 都走这里。
func (u *ui) raise() {
	if u == nil || u.w == nil {
		return
	}
	u.w.Perform(system.ActionRaise)
}

// loadUIFont 加载系统中文字体。不把字体塞进二进制，省十几 MB。
func loadUIFont() []font.FontFace {
	paths := []string{
		`C:\Windows\Fonts\msyh.ttc`,   // 微软雅黑
		`C:\Windows\Fonts\msyh.ttf`,   // 老系统的雅黑
		`C:\Windows\Fonts\simhei.ttf`, // 黑体兜底
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		coll, err := opentype.ParseCollection(data)
		if err != nil {
			continue
		}
		return coll
	}
	return nil
}

func (u *ui) layout(gtx layout.Context) layout.Dimensions {
	// 记下真实客户区尺寸，窗口关闭时写进配置。
	// 只在尺寸稳定后记，否则会存下 forceWindowSize 生效前的中间值。
	if u.sizeReady {
		if w, h := realClientSize(u.hwnd); w > 0 {
			u.lastW, u.lastH = w, h
		}
	}

	u.handleClicks(gtx)

	paint.Fill(gtx.Ops, uiPage)

	// 主界面被面板盖住时不响应鼠标：不注册输入区域，hover 也不会变灰
	u.interactive = !u.modalOpen()
	dims := layout.UniformInset(unit.Dp(14)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return u.pairCard(gtx)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(14)}.Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return u.filesCard(gtx)
			}),
		)
	})

	// 面板里的控件要能正常用
	u.interactive = true

	// 面板最后画，才能盖在主界面上面
	if u.showText {
		u.textPanel(gtx)
	}
	if u.showSettings {
		u.settingsPanel(gtx)
	}

	// 右键菜单最后画，按鼠标位置绝对定位
	u.drawContextMenu(gtx)

	return dims
}

// cursorPos 返回鼠标在窗口内的位置（像素）。
// Gio 的 op.Offset 和 gtx.Constraints 都用像素，所以这里不能转成 dp，
// 否则菜单位置和约束会算错（之前就是这样，框被拉高还偏移）。
func (u *ui) cursorPos(gtx layout.Context) image.Point {
	pt, ok := cursorInClient(u.hwnd)
	if !ok {
		return image.Point{}
	}
	return image.Pt(int(pt.X), int(pt.Y))
}

// pickedDir 把文件夹选择框的结果从工作线程交回界面线程
var pickedDir = make(chan string, 1)

// pickSaveDir 让用户选一个新的保存目录。
// 和文件选择框一样，必须离开界面线程 —— 它是模态对话框。
func (u *ui) pickSaveDir() {
	go func() {
		dir, err := pickFolder("选择文件保存位置")
		if err != nil || dir == "" {
			return
		}
		select {
		case pickedDir <- dir:
		default:
		}
		if u.w != nil {
			u.w.Invalidate()
		}
	}()
}

// applySaveDir 切换保存目录并落盘。空串表示恢复默认。
func (u *ui) applySaveDir(dir string) {
	if err := u.srv.setSaveDir(dir); err != nil {
		u.settingsError = "设置保存位置失败：" + err.Error()
		return
	}
	u.settingsError = ""
	u.fileKey = ""
}

// modalOpen 有浮层挡着时，主界面的按钮和列表都不该响应点击。
// 面板只是「画」在上面，不会自动拦住事件，得自己判断。
func (u *ui) modalOpen() bool {
	return u.showText || u.showSettings || u.ctxMenu.visible
}

// handleClicks 统一处理所有按钮，避免逻辑散在布局代码里。
func (u *ui) handleClicks(gtx layout.Context) {
	if !u.modalOpen() {
		if u.btnCopy.Clicked(gtx) {
			_ = setClipboard(u.srv.token())
		}
		if u.btnReset.Clicked(gtx) {
			u.srv.rotateToken()
		}

		// 四个入口
		if u.btnFile.Clicked(gtx) {
			u.pickFiles()
		}
		if u.btnText.Clicked(gtx) {
			u.showText = true
		}
		if u.btnClip.Clicked(gtx) {
			u.pushClipboard()
		}
		if u.btnSettings.Clicked(gtx) {
			u.openSettings()
		}

		// 文件操作
		if u.btnAddFile.Clicked(gtx) {
			u.pickFiles()
		}
		if u.btnOpen.Clicked(gtx) {
			u.openSelected()
		}
		if u.btnReveal.Clicked(gtx) {
			u.revealSelected()
		}
		if u.btnDeleteFile.Clicked(gtx) {
			u.deleteSelected()
		}
	}

	// 文本面板内的按钮
	if u.showText {
		if u.btnCloseText.Clicked(gtx) {
			u.showText = false
		}
		if u.btnSend.Clicked(gtx) {
			if text := strings.TrimSpace(u.textEditor.Text()); text != "" {
				u.srv.store.AddClip(text, "电脑", KindText)
				u.textEditor.SetText("")
				u.clipKey = ""
			}
		}
		if u.btnClear.Clicked(gtx) {
			u.srv.store.ClearClips()
			u.clipKey = ""
		}
	}

	if u.showSettings {
		if u.btnCloseSettings.Clicked(gtx) || u.btnCancelSettings.Clicked(gtx) {
			u.showSettings = false
			u.settingsError = ""
		}
		if u.btnSaveSettings.Clicked(gtx) {
			u.saveSettings()
		}
		if u.btnPickDir.Clicked(gtx) {
			u.pickSaveDir()
		}
		if u.btnResetDir.Clicked(gtx) {
			u.applySaveDir("")
		}
	}
}

// selectedPath 返回当前选中文件的条目和磁盘路径。
func (u *ui) selectedPath() (FileEntry, string, bool) {
	if u.selected < 0 || u.selected >= len(u.files) {
		return FileEntry{}, "", false
	}
	f := u.files[u.selected]
	_, p, err := u.srv.store.Find(f.ID)
	if err != nil {
		return FileEntry{}, "", false
	}
	return f, p, true
}

// openSelected 用系统默认程序打开选中的文件。
func (u *ui) openSelected() {
	_, p, ok := u.selectedPath()
	if !ok {
		return
	}
	if err := openPath(p); err != nil {
		verbosef("打开失败: %v", err)
	}
}

// revealSelected 在资源管理器里定位到该文件。
func (u *ui) revealSelected() {
	_, p, ok := u.selectedPath()
	if !ok {
		return
	}
	if err := revealPath(p); err != nil {
		verbosef("定位失败: %v", err)
	}
}

// deleteSelected 删掉选中的文件。文件本身也会从磁盘删掉，
// 因为 store.Delete 会把 data/files 下的那份一起清理。
func (u *ui) deleteSelected() {
	f, _, ok := u.selectedPath()
	if !ok {
		return
	}
	if err := u.srv.store.Delete(f.ID); err != nil {
		verbosef("删除失败: %v", err)
		return
	}
	u.selected = -1
	u.fileKey = ""
}

// pushClipboard 把 Windows 剪贴板里的文字存进记录，手机下次刷新就能看到。
func (u *ui) pushClipboard() {
	text := strings.TrimSpace(getClipboardText())
	if text == "" {
		return
	}
	u.srv.store.AddClip(text, "电脑", KindClip)
	u.clipKey = ""
}

// pickedFiles 把文件选择框的结果从工作线程交回界面线程
var pickedFiles = make(chan []string, 1)

// pickFiles 打开系统文件选择框。
//
// GetOpenFileNameW 是模态对话框，内部跑自己的消息循环，并给 owner 窗口发消息。
// 在 Gio 的界面线程里直接调，那些消息会调进 Gio 的 Go 回调 —— 嵌套调用直接崩。
// 所以丢到独立 goroutine，owner 传 0（没有 owner 就不会给 Gio 的窗口发消息）。
func (u *ui) pickFiles() {
	go func() {
		paths, err := openFileDialog()
		if err != nil || len(paths) == 0 {
			return
		}
		select {
		case pickedFiles <- paths:
		default:
		}
		if u.w != nil {
			u.w.Invalidate()
		}
	}()
}

// addFile 把本地文件读进 store，和手机上传走同一条路径。
func (u *ui) addFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return err
	}
	_, err = u.srv.store.SaveStream(filepath.Base(path), f, st.Size())
	return err
}

// card 画一张白色圆角卡片，内容带统一内边距。
func (u *ui) card(gtx layout.Context, content layout.Widget) layout.Dimensions {
	inner := func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(unit.Dp(14)).Layout(gtx, content)
	}

	// 先排版拿到尺寸，再回头画背景，否则不知道圆角矩形要多大
	m := op.Record(gtx.Ops)
	dims := inner(gtx)
	call := m.Stop()

	size := dims.Size
	r := gtx.Dp(unit.Dp(16))

	// 外圈浅灰当 1dp 边框，里面再铺白色
	outline := clip.RRect{
		Rect: image.Rect(0, 0, size.X, size.Y),
		NW:   r, NE: r, SW: r, SE: r,
	}
	paint.FillShape(gtx.Ops, uiLine, outline.Op(gtx.Ops))

	inner1 := gtx.Dp(1)
	fill := clip.RRect{
		Rect: image.Rect(inner1, inner1, size.X-inner1, size.Y-inner1),
		NW:   r, NE: r, SW: r, SE: r,
	}
	paint.FillShape(gtx.Ops, uiCard, fill.Op(gtx.Ops))

	call.Add(gtx.Ops)
	if debugLayout {
		fmt.Fprintf(os.Stderr, "[card] size=%v\n", dims.Size)
	}
	return dims
}

func (u *ui) pairCard(gtx layout.Context) layout.Dimensions {
	return u.card(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			// 二维码
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				side := gtx.Dp(unit.Dp(132))
				gtx.Constraints = layout.Exact(image.Pt(side, side))
				img := widget.Image{Src: u.qr(), Fit: widget.Contain}
				return img.Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Width: unit.Dp(22)}.Layout),
			// 配对信息
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						l := material.H6(u.th, "手机扫码配对")
						l.Color = uiInk
						return l.Layout(gtx)
					}),
					layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								l := material.Body2(u.th, "配对码")
								l.Color = uiMuted
								return l.Layout(gtx)
							}),
							layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								l := material.Body1(u.th, shortToken(u.srv.token()))
								l.Color = uiInk
								return l.Layout(gtx)
							}),
							layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
							layout.Rigid(u.linkButton(&u.btnCopy, "复制")),
							layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
							layout.Rigid(u.linkButton(&u.btnReset, "重置")),
						)
					}),
					layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						l := material.Body2(u.th, u.pairHint())
						l.Color = uiMuted
						return l.Layout(gtx)
					}),
				)
			}),
			layout.Rigid(layout.Spacer{Width: unit.Dp(20)}.Layout),
			// 三个操作入口
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
					layout.Rigid(u.tile(&u.btnFile, "文件")),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(u.tile(&u.btnText, "文本")),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(u.tile(&u.btnClip, "剪贴板")),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(u.tile(&u.btnSettings, "设置")),
				)
			}),
		)
	})
}

// tile 是圆角方块按钮。
func (u *ui) tile(c *widget.Clickable, label string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		size := image.Pt(gtx.Dp(unit.Dp(92)), gtx.Dp(unit.Dp(64)))
		gtx.Constraints = layout.Exact(size)

		// 不可交互时只画静态外观：不注册输入区域，鼠标移上去也不会变灰
		if !u.interactive {
			return u.tileBody(gtx, label, size, false, false)
		}
		return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return u.tileBody(gtx, label, size, c.Hovered(), c.Pressed())
		})
	}
}

func (u *ui) tileBody(gtx layout.Context, label string, size image.Point, hovered, pressed bool) layout.Dimensions {
	bg := uiTile
	if hovered {
		bg = color.NRGBA{R: 0xE2, G: 0xE6, B: 0xE3, A: 0xFF}
	}
	if pressed {
		bg = color.NRGBA{R: 0xD4, G: 0xDA, B: 0xD6, A: 0xFF}
	}
	r := gtx.Dp(unit.Dp(12))
	rr := clip.RRect{
		Rect: image.Rectangle{Max: size},
		NW:   r, NE: r, SW: r, SE: r,
	}
	paint.FillShape(gtx.Ops, bg, rr.Op(gtx.Ops))

	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				side := gtx.Dp(unit.Dp(21))
				gtx.Constraints = layout.Exact(image.Pt(side, side))
				return iconWidget(gtx, label, side, uiGreen)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(5)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(u.th, label)
				l.Color = uiGreen
				return l.Layout(gtx)
			}),
		)
	})
}

// iconWidget 用矢量路径画操作入口的图标。
// 不用位图是因为缩放窗口时位图会糊，矢量在任意 DPI 下都清晰。
func iconWidget(gtx layout.Context, kind string, size int, c color.NRGBA) layout.Dimensions {
	f := float32(size)
	at := func(x, y float32) f32.Point { return f32.Pt(x*f, y*f) }

	var p clip.Path
	p.Begin(gtx.Ops)

	switch kind {
	case "文件":
		// 纸张轮廓 + 右上折角
		p.MoveTo(at(0.16, 0.08))
		p.LineTo(at(0.60, 0.08))
		p.LineTo(at(0.84, 0.32))
		p.LineTo(at(0.84, 0.92))
		p.LineTo(at(0.16, 0.92))
		p.Close()
		p.MoveTo(at(0.60, 0.08))
		p.LineTo(at(0.60, 0.32))
		p.LineTo(at(0.84, 0.32))
	case "文本":
		// 三条横线，最后一行短一些
		p.MoveTo(at(0.15, 0.26))
		p.LineTo(at(0.85, 0.26))
		p.MoveTo(at(0.15, 0.50))
		p.LineTo(at(0.85, 0.50))
		p.MoveTo(at(0.15, 0.74))
		p.LineTo(at(0.60, 0.74))
	case "设置":
		// 齿轮走描边，和另外三个图标保持一致。
		// 关键是线要够粗：19-21dp 下细线会被抗锯齿糊掉。
		cx, cy := 0.5*f, 0.5*f
		deg := math.Pi / 180

		// 外圈做大，齿只探出一小截 —— 这样整体是"带凸起的圆"，
		// 而不是放射状的星形，小尺寸下才不会糊。
		addCircle(&p, cx, cy, 0.31*f, 28)
		for i := 0; i < 8; i++ {
			a := float64(i)*45*deg - math.Pi/2
			cos, sin := float32(math.Cos(a)), float32(math.Sin(a))
			p.MoveTo(f32.Pt(cx+0.315*f*cos, cy+0.315*f*sin))
			p.LineTo(f32.Pt(cx+0.45*f*cos, cy+0.45*f*sin))
		}
		// 中心孔
		addCircle(&p, cx, cy, 0.10*f, 16)

		paint.FillShape(gtx.Ops, c, clip.Stroke{
			Path:  p.End(),
			Width: float32(size) * 0.105,
		}.Op())
		return layout.Dimensions{Size: image.Pt(size, size)}

	case "剪贴板":
		// 板身 + 顶部夹子
		p.MoveTo(at(0.24, 0.22))
		p.LineTo(at(0.24, 0.92))
		p.LineTo(at(0.76, 0.92))
		p.LineTo(at(0.76, 0.22))
		p.LineTo(at(0.24, 0.22))
		p.MoveTo(at(0.38, 0.22))
		p.LineTo(at(0.38, 0.08))
		p.LineTo(at(0.62, 0.08))
		p.LineTo(at(0.62, 0.22))
	}

	paint.FillShape(gtx.Ops, c, clip.Stroke{
		Path:  p.End(),
		Width: float32(size) * 0.075,
	}.Op())

	return layout.Dimensions{Size: image.Pt(size, size)}
}

// addCircle 用折线近似一个圆。
// clip.Path 没有画圆的接口，段数够多时肉眼分不出来。
func addCircle(p *clip.Path, cx, cy, r float32, seg int) {
	for i := 0; i <= seg; i++ {
		a := 2 * math.Pi * float64(i) / float64(seg)
		x := cx + r*float32(math.Cos(a))
		y := cy + r*float32(math.Sin(a))
		if i == 0 {
			p.MoveTo(f32.Pt(x, y))
		} else {
			p.LineTo(f32.Pt(x, y))
		}
	}
}

// linkButton 是配对码旁边那种小文字按钮。
func (u *ui) linkButton(c *widget.Clickable, label string) layout.Widget {
	return u.linkButtonColored(c, label, uiGreen)
}

// linkButtonColored 同 linkButton，但可以指定颜色（删除用红色）
func (u *ui) linkButtonColored(c *widget.Clickable, label string, col color.NRGBA) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(4)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(u.th, label)
				l.Color = col
				return l.Layout(gtx)
			})
		})
	}
}

func (u *ui) filesCard(gtx layout.Context) layout.Dimensions {
	u.refreshFiles()

	return u.card(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						l := material.Body1(u.th, "收到的文件")
						l.Color = uiInk
						return l.Layout(gtx)
					}),
					layout.Flexed(1, spacerX),
					layout.Rigid(u.pillButton(&u.btnAddFile, "添加文件", false)),
				)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
			// 表头，和下面的列宽对齐
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return u.headerText(gtx, "文件名")
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(unit.Dp(90)), gtx.Dp(unit.Dp(18))))
						return u.headerText(gtx, "大小")
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(unit.Dp(120)), gtx.Dp(unit.Dp(18))))
						return u.headerText(gtx, "接收时间")
					}),
				)
				})
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				if len(u.files) == 0 {
					return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						l := material.Body2(u.th, "还没有收到文件")
						l.Color = uiMuted
						return l.Layout(gtx)
					})
				}
				return u.list.Layout(gtx, len(u.files), func(gtx layout.Context, i int) layout.Dimensions {
					return u.fileRow(gtx, i, u.files[i])
				})
			}),
			// 底部操作
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(u.pillButton(&u.btnOpen, "打开", false)),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(u.pillButton(&u.btnReveal, "在文件夹中显示", false)),
					layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
					layout.Rigid(u.pillButton(&u.btnDeleteFile, "删除", false)),
					layout.Flexed(1, spacerX),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if u.selected < 0 || u.selected >= len(u.files) {
							return layout.Dimensions{}
						}
						l := material.Body2(u.th, "已选 "+u.files[u.selected].Name)
						l.Color = uiMuted
						l.MaxLines = 1
						return l.Layout(gtx)
					}),
				)
			}),
		)
	})
}

func (u *ui) headerText(gtx layout.Context, text string) layout.Dimensions {
	l := material.Body2(u.th, text)
	l.Color = uiMuted
	l.TextSize = unit.Sp(12)
	return l.Layout(gtx)
}

func (u *ui) fileRow(gtx layout.Context, i int, f FileEntry) layout.Dimensions {
	row := &u.fileRows[i]

	// 面板打开时不响应列表上的任何指针事件
	blocked := u.modalOpen()

	// 处理指针事件。InputOp 在上一帧末尾注册，这里读到的是那之后发生的事件。
	// 左键选中，右键弹出菜单。
	for !blocked {
		ev, ok := gtx.Event(pointer.Filter{
			Target: row,
			Kinds:  pointer.Press | pointer.Enter | pointer.Leave,
		})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch e.Kind {
		case pointer.Press:
			switch e.Buttons {
			case pointer.ButtonSecondary:
				u.selected = i
				u.ctxMenu = contextMenu{visible: true, row: i, pos: u.cursorPos(gtx)}
			case pointer.ButtonPrimary:
				u.selected = i
				u.ctxMenu.visible = false
			}
		case pointer.Enter:
			row.hovered = true
		case pointer.Leave:
			row.hovered = false
		}
	}

	selected := u.selected == i

	m := op.Record(gtx.Ops)
	dims := layout.Inset{
		Top: unit.Dp(8), Bottom: unit.Dp(8),
		Left: unit.Dp(8), Right: unit.Dp(8),
	}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(u.th, f.Name)
				l.Color = uiInk
				l.MaxLines = 1
				return l.Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(unit.Dp(90)), gtx.Dp(unit.Dp(22))))
				l := material.Body2(u.th, humanSize(f.Size))
				l.Color = uiMuted
				return l.Layout(gtx)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(unit.Dp(120)), gtx.Dp(unit.Dp(22))))
				l := material.Body2(u.th, f.ModTime.Format("01-02 15:04"))
				l.Color = uiMuted
				return l.Layout(gtx)
			}),
		)
	})
	call := m.Stop()

	// 选中 / 悬停时铺一层圆角底色
	if selected || row.hovered {
		bg := color.NRGBA{R: 0xF2, G: 0xF7, B: 0xF4, A: 0xFF}
		if selected {
			bg = color.NRGBA{R: 0xE6, G: 0xF3, B: 0xED, A: 0xFF}
		}
		r := gtx.Dp(unit.Dp(8))
		rr := clip.RRect{
			Rect: image.Rect(0, 0, dims.Size.X, dims.Size.Y),
			NW:   r, NE: r, SW: r, SE: r,
		}
		paint.FillShape(gtx.Ops, bg, rr.Op(gtx.Ops))
	}
	call.Add(gtx.Ops)

	// 注册本行的输入区域，供下一帧读事件。
	// 面板打开时不注册，免得底下的行还在响应悬停。
	if !blocked {
		stack := clip.Rect{Max: dims.Size}.Push(gtx.Ops)
		event.Op(gtx.Ops, row)
		stack.Pop()
	}

	return dims
}

// refreshFiles 只在文件集合变化时才重建切片，避免每帧分配。
func (u *ui) refreshFiles() {
	files := u.srv.store.Files()
	key := fmt.Sprint(len(files))
	for _, f := range files {
		key += f.ID
	}
	if key == u.fileKey {
		return
	}
	u.fileKey = key
	u.files = files
	u.fileRows = make([]rowState, len(files))
	if u.selected >= len(files) {
		u.selected = -1
	}
}

// qr 按内容缓存二维码，配对码或地址变了才重新编码。
func (u *ui) qr() paint.ImageOp {
	content := u.srv.qrContent()
	if content == u.qrKey {
		return u.qrOp
	}

	data, err := qrcode.Encode(content, qrcode.Medium, 400)
	if err != nil {
		return u.qrOp
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return u.qrOp
	}

	u.qrOp = paint.NewImageOp(img)
	u.qrKey = content
	return u.qrOp
}

func (u *ui) pairHint() string {
	eps := u.srv.orderedEndpoints()
	if len(eps) == 0 {
		return "没有可用网络"
	}
	return eps[0].Addr()
}

func shortToken(t string) string {
	if len(t) > 8 {
		return t[:8]
	}
	return t
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n/div >= unit && exp < 3 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}
