package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gioui.org/app"
	"gioui.org/f32"
	"gioui.org/font"
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
	uiGreen = color.NRGBA{R: 0x1D, G: 0x9E, B: 0x75, A: 0xFF}
	uiInk   = color.NRGBA{R: 0x17, G: 0x1B, B: 0x19, A: 0xFF}
	uiMuted = color.NRGBA{R: 0x6B, G: 0x72, B: 0x80, A: 0xFF}
	uiPage  = color.NRGBA{R: 0xFA, G: 0xFB, B: 0xFA, A: 0xFF}
	uiCard  = color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
	uiTile  = color.NRGBA{R: 0xED, G: 0xF0, B: 0xEE, A: 0xFF}
	uiLine  = color.NRGBA{R: 0xE6, G: 0xEA, B: 0xE8, A: 0xFF}
)

// debugLayout 打开后会把卡片尺寸打到 stderr，排查布局问题时用。
var debugLayout = os.Getenv("AIRLINK_DEBUG_LAYOUT") != ""

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
	fileRows []widget.Clickable
	selected int // 当前选中的文件下标，-1 表示没选

	list layout.List

	btnAddFile, btnOpen, btnReveal, btnDeleteFile widget.Clickable

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

	u := &ui{srv: srv, th: th, selected: -1}
	u.list.Axis = layout.Vertical
	mainUI = u

	go func() {
		w := new(app.Window)
		u.w = w

		ww, wh := preferredWindowSize()
		w.Option(
			app.Title("AirLink"),
			app.Size(ww, wh),
			app.MinSize(unit.Dp(760), unit.Dp(520)),
		)

		// 文件列表和配对码都可能被手机那边改动，定时重绘
		go func() {
			for range time.Tick(time.Second) {
				w.Invalidate()
			}
		}()

		var ops op.Ops
		for {
			e := w.Event()
			switch e := e.(type) {
			case app.DestroyEvent:
				os.Exit(0)
			case app.FrameEvent:
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
	u := &ui{srv: srv, th: th, selected: -1}
	u.list.Axis = layout.Vertical
	u.clipList.Axis = layout.Vertical
	switch panel {
	case "text":
		u.showText = true
	case "settings":
		u.showSettings = true
	}

	var ops op.Ops
	gtx := layout.Context{
		Ops:         &ops,
		Constraints: layout.Exact(image.Pt(w, h)),
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
	u.handleClicks(gtx)

	paint.Fill(gtx.Ops, uiPage)

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

	// 面板最后画，才能盖在主界面上面
	if u.showText {
		u.textPanel(gtx)
	}
	if u.showSettings {
		u.settingsPanel(gtx)
	}

	return dims
}

// handleClicks 统一处理所有按钮，避免逻辑散在布局代码里。
func (u *ui) handleClicks(gtx layout.Context) {
	if u.btnCopy.Clicked(gtx) {
		_ = setClipboard(u.srv.token())
	}
	if u.btnReset.Clicked(gtx) {
		u.srv.rotateToken()
	}

	// 三个入口
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

	// 文本面板内的按钮
	if u.showText {
		if u.btnCloseText.Clicked(gtx) {
			u.showText = false
		}
		if u.btnSend.Clicked(gtx) {
			if text := strings.TrimSpace(u.textEditor.Text()); text != "" {
				u.srv.store.AddClip(text, "电脑")
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
	if err := exec.Command("explorer", p).Start(); err != nil {
		verbosef("打开失败: %v", err)
	}
}

// revealSelected 在资源管理器里定位到该文件。
func (u *ui) revealSelected() {
	_, p, ok := u.selectedPath()
	if !ok {
		return
	}
	if err := exec.Command("explorer", "/select,"+p).Start(); err != nil {
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
	u.srv.store.AddClip(text, "电脑")
	u.clipKey = ""
}

// pickFiles 打开系统文件选择框，把选中的文件加进列表。
func (u *ui) pickFiles() {
	paths, err := openFileDialog()
	if err != nil || len(paths) == 0 {
		return
	}
	for _, p := range paths {
		if err := u.addFile(p); err != nil {
			verbosef("添加文件失败 %s: %v", p, err)
		}
	}
	u.fileKey = ""
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
		return layout.UniformInset(unit.Dp(18)).Layout(gtx, content)
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

		return material.Clickable(gtx, c, func(gtx layout.Context) layout.Dimensions {
			bg := uiTile
			if c.Hovered() {
				bg = color.NRGBA{R: 0xE2, G: 0xE6, B: 0xE3, A: 0xFF}
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
						side := gtx.Dp(unit.Dp(19))
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
		})
	}
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

// linkButton 是配对码旁边那种小文字按钮。
func (u *ui) linkButton(c *widget.Clickable, label string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return material.Clickable(gtx, c, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(4)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(u.th, label)
				l.Color = uiGreen
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
	selected := u.selected == i

	// Clickable 必须先 Layout 才会更新内部状态，所以在回调里判断点击
	return material.Clickable(gtx, row, func(gtx layout.Context) layout.Dimensions {
		if row.Clicked(gtx) {
			u.selected = i
			selected = true
		}

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

		// 选中和悬停时铺一层底色，鼠标移过去能看出哪行被选中
		if selected || row.Hovered() {
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
		return dims
	})
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
	u.fileRows = make([]widget.Clickable, len(files))
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
		return "没有找到可用网络，请检查 WiFi"
	}
	return eps[0].Addr() + " · 同一 WiFi 下扫码"
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
