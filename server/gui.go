package main

import (
	"bytes"
	"embed"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	qrcode "github.com/skip2/go-qrcode"
)

type fileModel struct {
	walk.TableModelBase
	items []FileEntry
}

func (m *fileModel) RowCount() int { return len(m.items) }

func (m *fileModel) Value(row, col int) interface{} {
	if row < 0 || row >= len(m.items) {
		return ""
	}
	f := m.items[row]
	switch col {
	case 0:
		return f.Name
	case 1:
		return humanSize(f.Size)
	case 2:
		return f.ModTime.Format("01-02 15:04")
	}
	return ""
}

func (m *fileModel) set(items []FileEntry) {
	m.items = items
	m.PublishRowsReset()
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

//go:embed assets/*.png
var uiAssets embed.FS

// assetPath 把内置图标写到数据目录再返回路径。
// Walk 的按钮只能按文件路径加载图片，所以必须先落盘一次。
func (g *gui) assetPath(name string) string {
	dst := filepath.Join(filepath.Dir(g.srv.cfg), name)
	data, err := uiAssets.ReadFile("assets/" + name)
	if err != nil {
		return ""
	}
	// 内容一致就不动文件；不一致才覆盖，否则升级后还会显示上一版的图标。
	if cur, err := os.ReadFile(dst); err == nil && bytes.Equal(cur, data) {
		return dst
	}
	_ = os.WriteFile(dst, data, 0o644)
	return dst
}

// loadIcon 把内置图标加载成 Bitmap 交给按钮。
// 不能直接把路径传给 Image 字段——Walk 会把它拼到工作目录后面，
// 绝对路径拼完就成了非法路径，启动时报"找不到图片"。
func (g *gui) loadIcon(name string) *walk.Bitmap {
	if p := g.assetPath(name); p != "" {
		if bmp, err := walk.NewBitmapFromFile(p); err == nil {
			return bmp
		}
	}
	// 兜底：1×1 透明图。图标加载失败也不该让整个窗口起不来。
	bmp, _ := walk.NewBitmapFromImage(image.NewRGBA(image.Rect(0, 0, 1, 1)))
	return bmp
}

// 界面统一的字号与颜色。主窗口和各个对话框共用一套。
var (
	uiTitle = Font{Family: "Microsoft YaHei UI", PointSize: 16, Bold: true}
	uiBody  = Font{Family: "Microsoft YaHei UI", PointSize: 10}
	uiSect  = Font{Family: "Microsoft YaHei UI", PointSize: 10, Bold: true}
	uiHint  = Font{Family: "Microsoft YaHei UI", PointSize: 9}
	uiMuted = walk.RGB(0x6B, 0x72, 0x80)
	uiDark  = walk.RGB(0x37, 0x41, 0x51)
)

type gui struct {
	mw      *walk.MainWindow
	qrView  *walk.ImageView
	devLbl  *walk.Label
	hintLbl *walk.Label
	tv      *walk.TableView
	model   *fileModel

	// 手机发来的文字是否自动写进 Windows 剪贴板。开关在「文字」面板里。
	autoCopy bool

	srv     *Server
	eps     []Endpoint
	loading bool
}

func runGUI(s *Server) {
	g := &gui{srv: s, model: &fileModel{}, autoCopy: true}
	if err := g.build(); err != nil {
		fatal("界面创建失败: %v", err)
	}

	// 手机发来剪贴板时，直接写进 Windows 剪贴板——复制粘贴这个动作不该再点一次。
	s.onClip = func(item ClipItem) {
		g.mw.Synchronize(func() {
			if g.autoCopy {
				if err := walk.Clipboard().SetText(item.Content); err == nil {
					_ = g.hintLbl.SetText("已复制到剪贴板：" + clipPreview(item.Content))
					return
				}
			}
			_ = g.hintLbl.SetText("手机：" + clipPreview(item.Content))
		})
	}

	g.startWatcher()
	g.mw.Run()
}

// clipPreview 把多行文本压成一行短预览，列表里显示用。
func clipPreview(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " ")
	r := []rune(s)
	if len(r) > 36 {
		return string(r[:36]) + "…"
	}
	return s
}

// pushText 把一段文字存进记录，手机下次刷新就能看到。
func (g *gui) pushText(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	g.srv.store.AddClip(text, "电脑")
	return true
}

// pushClipboard 把 Windows 剪贴板里的文本推过去。
func (g *gui) pushClipboard() (string, bool) {
	text, err := walk.Clipboard().Text()
	if err != nil || strings.TrimSpace(text) == "" {
		return "", false
	}
	g.srv.store.AddClip(text, "电脑")
	return text, true
}

func (g *gui) build() error {
	white := SolidColorBrush{Color: walk.RGB(0xFF, 0xFF, 0xFF)}
	pageBg, err := walk.NewSolidColorBrush(walk.RGB(0xF4, 0xF5, 0xF7))
	if err != nil {
		return err
	}

	card := func(children ...Widget) Widget {
		return Composite{
			Layout:     VBox{Margins: Margins{Left: 18, Top: 16, Right: 18, Bottom: 16}, Spacing: 10},
			Background: white,
			Children:   children,
		}
	}

	if err := (MainWindow{
		AssignTo:    &g.mw,
		Title:       "AirLink",
		Size:        Size{Width: 880, Height: 680},
		MinSize:     Size{Width: 720, Height: 560},
		Layout:      VBox{Margins: Margins{Left: 14, Top: 14, Right: 14, Bottom: 14}, Spacing: 16},
		OnDropFiles: g.addPaths,
		Children: []Widget{
			// 顶部：二维码 + 状态 + 两个入口。文字互传和配对细节都收进对话框。
			card(
				Composite{
					Layout: HBox{MarginsZero: true, Spacing: 28},
					Children: []Widget{
						ImageView{
							AssignTo: &g.qrView,
							MinSize:  Size{Width: 156, Height: 156},
							MaxSize:  Size{Width: 156, Height: 156},
						},
						Composite{
							Layout: VBox{MarginsZero: true, Spacing: 6},
							Children: []Widget{
								Label{Text: "AirLink", Font: uiTitle},
								Label{AssignTo: &g.devLbl, Text: "", Font: uiBody, TextColor: uiMuted},
								Label{AssignTo: &g.hintLbl, Text: "", Font: uiHint, TextColor: uiMuted},
								Composite{
									Layout: HBox{MarginsZero: true, Spacing: 8},
									Children: []Widget{
										PushButton{Text: "文字", Image: g.loadIcon("ui-text.png"), MinSize: Size{Width: 110, Height: 36}, OnClicked: g.openTextPanel},
										PushButton{Text: "设置", Image: g.loadIcon("ui-settings.png"), MinSize: Size{Width: 110, Height: 36}, OnClicked: g.openSettings},
										HSpacer{},
									},
								},
							},
						},
						HSpacer{},
					},
				},
			),
			// 主体：文件列表占满剩余空间
			card(
				Label{Text: "收到的文件", Font: uiSect, TextColor: uiDark},
				TableView{
					AssignTo:         &g.tv,
					Model:            g.model,
					Font:             uiBody,
					AlternatingRowBG: true,
					CustomRowHeight:  28,
					ContextMenuItems: []MenuItem{
						Action{Text: "打开", OnTriggered: g.openSelected},
						Action{Text: "复制文件名", OnTriggered: g.copySelectedName},
						Action{Text: "在文件夹中显示", OnTriggered: g.revealSelected},
						Separator{},
						Action{Text: "删除", OnTriggered: g.deleteSelected},
					},
					Columns: []TableViewColumn{
						{Title: "文件名", Width: 420},
						{Title: "大小", Width: 110},
						{Title: "接收时间", Width: 140},
					},
					StretchFactor: 1,
				},
				Composite{
					Layout: HBox{MarginsZero: true, Spacing: 8},
					Children: []Widget{
						PushButton{Text: "添加文件", Image: g.loadIcon("ui-add.png"), MinSize: Size{Width: 124, Height: 34}, OnClicked: g.pickFiles},
						PushButton{Text: "删除选中", Image: g.loadIcon("ui-delete.png"), MinSize: Size{Width: 124, Height: 34}, OnClicked: g.deleteSelected},
						PushButton{Text: "发送到手机", Image: g.loadIcon("ui-send.png"), MinSize: Size{Width: 124, Height: 34}, OnClicked: g.sendToPhone},
						HSpacer{},
						PushButton{Text: "刷新", Image: g.loadIcon("ui-refresh.png"), MinSize: Size{Width: 100, Height: 34}, OnClicked: g.refreshFiles},
						PushButton{Text: "打开文件夹", Image: g.loadIcon("ui-folder.png"), MinSize: Size{Width: 124, Height: 34}, OnClicked: g.openFolder},
					},
				},
			),
		},
	}).Create(); err != nil {
		return err
	}

	g.mw.SetBackground(pageBg)

	if ic, err := walk.NewIconFromFile(filepath.Join(filepath.Dir(g.srv.cfg), "icon.ico")); err == nil {
		g.mw.SetIcon(ic)
	}

	g.reloadNet()
	g.refreshFiles()
	return nil
}

// reloadNet 重新枚举网卡，并尽量保留用户已经选中的那个地址。
func (g *gui) reloadNet() {
	g.loading = true
	defer func() { g.loading = false }()

	g.eps = Candidates(g.srv.port)
	_ = g.devLbl.SetText("设备：" + g.srv.deviceName())
	g.updateQR()
	g.updateHint()
}

// setNetwork 手动指定对外地址。多网卡机器（装过虚拟机、VPN、组网工具）
// 靠这个避开自动挑错网卡的问题。
func (g *gui) setNetwork(ip string) {
	g.srv.setPreferredIP(ip)
	g.updateQR()
	g.updateHint()
}



func (g *gui) updateQR() {
	pngBytes, err := qrcode.Encode(g.srv.qrContent(), qrcode.Medium, 220)
	if err != nil {
		return
	}
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return
	}
	bmp, err := walk.NewBitmapFromImage(img)
	if err != nil {
		return
	}
	_ = g.qrView.SetImage(bmp)
}

func (g *gui) updateHint() {
	files := g.model.items
	var hint string
	switch {
	case len(g.eps) == 0:
		hint = "没有找到局域网地址，请检查 WiFi 或网线连接"
	case g.srv.preferredIP() != "":
		hint = fmt.Sprintf("已指定地址 %s · 端口 %d · %d 个文件",
			g.srv.preferredIP(), g.srv.port, len(files))
	default:
		hint = fmt.Sprintf("端口 %d · 已收到 %d 个文件", g.srv.port, len(files))
	}
	if g.srv.relayURL() != "" {
		hint += " · 公网兜底已开启"
	}
	_ = g.hintLbl.SetText(hint)
}

func (g *gui) refreshFiles() {
	files := g.srv.store.Files()
	g.model.set(files)
	g.updateHint()
}

// refreshClips 重建「最近记录」列表。电脑和手机发的都在这一个列表里，
// 因为两边读写的是同一份 clips 存储。
// clipLabels 把记录格式化成列表行，供「文字」面板显示。
func clipLabels(items []ClipItem) []string {
	labels := make([]string, len(items))
	for i, c := range items {
		arrow := "→"
		if c.From == "手机" {
			arrow = "←"
		}
		labels[i] = fmt.Sprintf("%s %s  %s  %s",
			arrow, c.From, clipPreview(c.Content), c.At.Format("01-02 15:04"))
	}
	if len(labels) == 0 {
		labels = []string{"（还没有记录）"}
	}
	return labels
}


// startWatcher 每 2 秒比对一次文件列表，手机传完文件后窗口自动更新。
func (g *gui) startWatcher() {
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		last := ""
		for range ticker.C {
			files := g.srv.store.Files()
			sig := fmt.Sprint(len(files))
			for _, f := range files {
				sig += f.ID
			}
			if sig == last {
				continue
			}
			last = sig
			snapshot := files
			g.mw.Synchronize(func() {
				g.model.set(snapshot)
				g.updateHint()
			})
		}
	}()
}

func (g *gui) copyToken() {
	_ = walk.Clipboard().SetText(g.srv.token())
	_ = g.hintLbl.SetText("配对码已复制到剪贴板")
}

// rotateToken 重置配对码，返回新码前 8 位（给设置对话框显示用）。
func (g *gui) rotateToken() string {
	if walk.MsgBox(g.mw, "重置配对码",
		"重新生成后，已配对的手机需要重新扫码。继续？",
		walk.MsgBoxIconQuestion|walk.MsgBoxYesNo) != walk.DlgCmdYes {
		return ""
	}
	tok := g.srv.rotateToken()
	g.updateQR()
	_ = g.hintLbl.SetText("配对码已重置")
	return tok
}

// selectedFile 返回列表里当前选中的文件及其磁盘路径。
func (g *gui) selectedFile() (FileEntry, string, bool) {
	i := g.tv.CurrentIndex()
	if i < 0 || i >= len(g.model.items) {
		return FileEntry{}, "", false
	}
	f := g.model.items[i]
	_, p, err := g.srv.store.Find(f.ID)
	if err != nil {
		return FileEntry{}, "", false
	}
	return f, p, true
}

// openSelected 用系统默认程序打开选中的文件。
func (g *gui) openSelected() {
	_, p, ok := g.selectedFile()
	if !ok {
		_ = g.hintLbl.SetText("先选中一个文件")
		return
	}
	if err := exec.Command("explorer", p).Start(); err != nil {
		_ = g.hintLbl.SetText("打开失败：" + err.Error())
	}
}

// copySelectedName 复制文件名（不是完整路径，文件名才是常被粘贴的）。
func (g *gui) copySelectedName() {
	f, _, ok := g.selectedFile()
	if !ok {
		_ = g.hintLbl.SetText("先选中一个文件")
		return
	}
	_ = walk.Clipboard().SetText(f.Name)
	_ = g.hintLbl.SetText("已复制文件名：" + f.Name)
}

// revealSelected 在资源管理器里定位到该文件。
func (g *gui) revealSelected() {
	_, p, ok := g.selectedFile()
	if !ok {
		_ = g.hintLbl.SetText("先选中一个文件")
		return
	}
	_ = exec.Command("explorer", "/select,"+p).Start()
}

func (g *gui) pickFiles() {
	dlg := &walk.FileDialog{
		Title:  "选择要发送到手机的文件",
		Filter: "所有文件 (*.*)|*.*",
	}
	ok, err := dlg.ShowOpenMultiple(g.mw)
	if err != nil || !ok {
		return
	}
	g.addPaths(dlg.FilePaths)
}

// addPaths 把本地文件放进共享目录，手机端即可下载。
func (g *gui) addPaths(paths []string) {
	added := 0
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil || info.IsDir() {
			continue
		}
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		_, err = g.srv.store.SaveStream(filepath.Base(p), f, info.Size())
		f.Close()
		if err == nil {
			added++
		}
	}
	g.refreshFiles()
	if added > 0 {
		g.hintLbl.SetText(fmt.Sprintf("已添加 %d 个文件", added))
	}
}

// openSettings 配置公网兜底。之前只能靠命令行参数，普通用户根本配不了。
// openTextPanel 文字互传面板：发剪贴板、手动输入、查看和删除记录。
// 这些是低频操作，放在主窗口里会把界面挤满，收进面板更清爽。
func (g *gui) openTextPanel() {
	var dlg *walk.Dialog
	var input *walk.LineEdit
	var list *walk.ListBox
	var status *walk.Label
	var autoBox *walk.CheckBox
	var closePB *walk.PushButton

	var items []ClipItem
	rebuild := func() {
		items = g.srv.store.Clips()
		if len(items) > 30 {
			items = items[:30]
		}
		_ = list.SetModel(clipLabels(items))
	}

	err := Dialog{
		AssignTo: &dlg,
		Title:    "文字互传",
		Icon:     walk.IconInformation(),
		MinSize:  Size{Width: 580, Height: 500},
		Layout:   VBox{Margins: Margins{Left: 20, Top: 16, Right: 20, Bottom: 16}, Spacing: 10},
		Children: []Widget{
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					PushButton{
						Text:    "发送剪贴板",
						MinSize: Size{Width: 118, Height: 32},
						OnClicked: func() {
							if text, ok := g.pushClipboard(); ok {
								_ = status.SetText("已发送：" + clipPreview(text))
							} else {
								_ = status.SetText("剪贴板里没有文本")
							}
							rebuild()
						},
					},
					CheckBox{
						AssignTo: &autoBox,
						Text:     "自动复制手机发来的文字",
						Checked:  g.autoCopy,
						OnCheckedChanged: func() {
							g.autoCopy = autoBox.Checked()
						},
					},
					HSpacer{},
					Label{AssignTo: &status, Text: "", Font: uiHint, TextColor: uiMuted},
				},
			},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					LineEdit{AssignTo: &input, StretchFactor: 1},
					PushButton{
						Text:    "发送文字",
						MinSize: Size{Width: 96, Height: 32},
						OnClicked: func() {
							if g.pushText(input.Text()) {
								_ = status.SetText("已发送：" + clipPreview(input.Text()))
								_ = input.SetText("")
								rebuild()
							} else {
								_ = status.SetText("输入框是空的")
							}
						},
					},
				},
			},
			Label{Text: "最近记录（双击复制）", Font: uiSect, TextColor: uiDark},
			ListBox{
				AssignTo:      &list,
				Model:         []string{},
				StretchFactor: 1,
				OnItemActivated: func() {
					i := list.CurrentIndex()
					if i < 0 || i >= len(items) {
						return
					}
					if err := walk.Clipboard().SetText(items[i].Content); err != nil {
						_ = status.SetText("复制失败：" + err.Error())
						return
					}
					_ = status.SetText("已复制：" + clipPreview(items[i].Content))
				},
			},
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 6},
				Children: []Widget{
					PushButton{
						Text:    "删除选中",
						MinSize: Size{Width: 96, Height: 30},
						OnClicked: func() {
							i := list.CurrentIndex()
							if i < 0 || i >= len(items) {
								_ = status.SetText("先在列表里选中一条")
								return
							}
							if g.srv.store.DeleteClip(items[i].ID) {
								_ = status.SetText("已删除一条记录")
							} else {
								_ = status.SetText("这条记录已经不在了")
							}
							rebuild()
						},
					},
					PushButton{
						Text:    "清空",
						MinSize: Size{Width: 84, Height: 30},
						OnClicked: func() {
							if len(items) == 0 {
								_ = status.SetText("记录已经是空的")
								return
							}
							if walk.MsgBox(dlg, "清空记录",
								"确定清空全部文字记录吗？这个操作不能撤销。",
								walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != walk.DlgCmdYes {
								return
							}
							g.srv.store.ClearClips()
							_ = status.SetText("记录已清空")
							rebuild()
						},
					},
					PushButton{
						Text:    "刷新",
						MinSize: Size{Width: 84, Height: 30},
						OnClicked: func() {
							rebuild()
							_ = status.SetText("已刷新")
						},
					},
					HSpacer{},
					PushButton{
						AssignTo:  &closePB,
						Text:      "关闭",
						MinSize:   Size{Width: 88, Height: 30},
						OnClicked: func() { dlg.Cancel() },
					},
				},
			},
		},
	}.Create(g.mw)
	if err != nil {
		walk.MsgBox(g.mw, "打开面板失败", err.Error(), walk.MsgBoxIconError)
		return
	}

	dlg.SetCancelButton(closePB)
	rebuild()
	dlg.Run()
}

// openSettings 配对码、网络地址、公网中转、已配对手机都放这里。
func (g *gui) openSettings() {
	var dlg *walk.Dialog
	var urlEdit, keyEdit *walk.LineEdit
	var tokLbl *walk.Label
	var netBox *walk.ComboBox
	var okPB, cancelPB *walk.PushButton

	phones := g.srv.phones()
	var phoneList *walk.ListBox

	// 每次打开都重新扫一遍网卡，多网卡机器可以在这里手动指定
	eps := Candidates(g.srv.port)
	netLabels := make([]string, 0, len(eps))
	for _, e := range eps {
		netLabels = append(netLabels, fmt.Sprintf("%s  ·  %s", e.Addr(), e.Iface))
	}
	if len(netLabels) == 0 {
		netLabels = []string{"未找到可用网络"}
	}
	curIdx := 0
	pref := g.srv.preferredIP()
	for i, e := range eps {
		if e.IP == pref {
			curIdx = i
			break
		}
	}

	err := Dialog{
		AssignTo: &dlg,
		Title:    "设置",
		Icon:     walk.IconInformation(),
		MinSize:  Size{Width: 640, Height: 580},
		Layout:   VBox{Margins: Margins{Left: 22, Top: 18, Right: 22, Bottom: 18}, Spacing: 10},
		Children: []Widget{
			Label{Text: "配对", Font: uiSect, TextColor: uiDark},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					Label{Text: "配对码", Font: uiBody},
					Label{
						AssignTo: &tokLbl,
						Text:     g.srv.token()[:8],
						MinSize:  Size{Width: 170},
						Font:     Font{Family: "Consolas", PointSize: 12, Bold: true},
					},
					HSpacer{},
					PushButton{
						Text:    "复制",
						MinSize: Size{Width: 84, Height: 30},
						OnClicked: func() {
							_ = walk.Clipboard().SetText(g.srv.token())
							_ = tokLbl.SetText("已复制")
						},
					},
					PushButton{
						Text:    "重置",
						MinSize: Size{Width: 84, Height: 30},
						OnClicked: func() {
							if tok := g.rotateToken(); tok != "" {
								_ = tokLbl.SetText(tok[:8])
							}
						},
					},
				},
			},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					Label{Text: "网络地址", Font: uiBody},
					ComboBox{
						AssignTo:     &netBox,
						Model:        netLabels,
						CurrentIndex: curIdx,
						OnCurrentIndexChanged: func() {
							i := netBox.CurrentIndex()
							if i >= 0 && i < len(eps) {
								g.setNetwork(eps[i].IP)
							}
						},
					},
					PushButton{
						Text:    "重新检测",
						MinSize: Size{Width: 96, Height: 30},
						OnClicked: func() {
							g.reloadNet()
							_ = tokLbl.SetText(g.srv.token()[:8])
						},
					},
				},
			},
			Label{Text: "公网中转", Font: uiSect, TextColor: uiDark},
			Label{
				Text:      "填了之后，手机不在同一 WiFi 时文件会自动走中转服务器。两项留空表示不用。",
				Font:      uiHint,
				TextColor: uiMuted,
			},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					Label{Text: "地址", Font: uiBody, MinSize: Size{Width: 52}},
					LineEdit{AssignTo: &urlEdit, Text: g.srv.relayURL(), StretchFactor: 1},
				},
			},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					Label{Text: "密钥", Font: uiBody, MinSize: Size{Width: 52}},
					LineEdit{AssignTo: &keyEdit, Text: g.srv.relayKey(), StretchFactor: 1},
				},
			},
			Label{Text: "已配对的手机", Font: uiSect, TextColor: uiDark},
			ListBox{AssignTo: &phoneList, Model: phoneLabels(phones), MaxSize: Size{Height: 88}},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					PushButton{
						Text:    "移除选中",
						MinSize: Size{Width: 96, Height: 30},
						OnClicked: func() {
							i := phoneList.CurrentIndex()
							if i < 0 || i >= len(phones) {
								return
							}
							g.srv.removePhone(phones[i].ID)
							phones = append(phones[:i], phones[i+1:]...)
							_ = phoneList.SetModel(phoneLabels(phones))
						},
					},
					HSpacer{},
				},
			},
			Composite{
				Layout: HBox{},
				Children: []Widget{
					HSpacer{},
					PushButton{AssignTo: &okPB, Text: "保存", MinSize: Size{Width: 88, Height: 30}, OnClicked: func() { dlg.Accept() }},
					PushButton{AssignTo: &cancelPB, Text: "取消", MinSize: Size{Width: 88, Height: 30}, OnClicked: func() { dlg.Cancel() }},
				},
			},
		},
	}.Create(g.mw)
	if err != nil {
		walk.MsgBox(g.mw, "打开设置失败", err.Error(), walk.MsgBoxIconError)
		return
	}
	dlg.SetDefaultButton(okPB)
	dlg.SetCancelButton(cancelPB)

	if dlg.Run() != walk.DlgCmdOK {
		return
	}

	url := strings.TrimSpace(urlEdit.Text())
	key := strings.TrimSpace(keyEdit.Text())
	if (url == "") != (key == "") {
		walk.MsgBox(g.mw, "配置不完整",
			"中转地址和密钥要么都填，要么都留空。", walk.MsgBoxIconWarning)
		return
	}
	if url != "" && !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		walk.MsgBox(g.mw, "地址格式不对",
			"中转地址要以 http:// 或 https:// 开头。", walk.MsgBoxIconWarning)
		return
	}

	g.srv.setRelay(url, key)
	g.updateQR()
	if url == "" {
		_ = g.hintLbl.SetText("已关闭公网兜底")
	} else {
		_ = g.hintLbl.SetText("公网兜底已开启，手机请重新扫码")
	}
}

// sendToPhone 把选中的文件投递到公网中转，手机打开 App 就能收到。
// 这条路径只在"手机不在同一 WiFi"时才需要，局域网内直接用手机来拉更快。
func (g *gui) sendToPhone() {
	i := g.tv.CurrentIndex()
	if i < 0 || i >= len(g.model.items) {
		_ = g.hintLbl.SetText("先在列表里选中一个文件")
		return
	}
	file := g.model.items[i]

	if g.srv.relayURL() == "" {
		walk.MsgBox(g.mw, "还没配置公网兜底",
			"「发送到手机」需要中转服务器。\r\n\r\n请先在「设置」里填写中转地址和密钥。",
			walk.MsgBoxIconWarning)
		return
	}

	phones := g.srv.phones()
	if len(phones) == 0 {
		walk.MsgBox(g.mw, "还没有配对过的手机",
			"请先用手机扫一次二维码连接。\r\n\r\n之后手机打开 App 就能收到电脑发来的文件。",
			walk.MsgBoxIconWarning)
		return
	}

	_, path, err := g.srv.store.Find(file.ID)
	if err != nil {
		walk.MsgBox(g.mw, "文件不存在", err.Error(), walk.MsgBoxIconError)
		return
	}

	phone := phones[0]
	if len(phones) > 1 {
		chosen, ok := g.pickPhone(phones)
		if !ok {
			return
		}
		phone = chosen
	}
	_ = g.hintLbl.SetText("正在发送到 " + phone.Name + "…")

	go func() {
		f, openErr := os.Open(path)
		if openErr != nil {
			g.mw.Synchronize(func() {
				_ = g.hintLbl.SetText("发送失败")
				walk.MsgBox(g.mw, "打开文件失败", openErr.Error(), walk.MsgBoxIconError)
			})
			return
		}
		info, statErr := f.Stat()
		if statErr != nil {
			f.Close()
			g.mw.Synchronize(func() {
				_ = g.hintLbl.SetText("发送失败")
				walk.MsgBox(g.mw, "读取文件失败", statErr.Error(), walk.MsgBoxIconError)
			})
			return
		}

		putErr := g.srv.relayPut(phone.ID, file.Name, info.Size(), f)
		f.Close()

		g.mw.Synchronize(func() {
			if putErr != nil {
				_ = g.hintLbl.SetText("发送失败")
				walk.MsgBox(g.mw, "发送到手机失败", putErr.Error(), walk.MsgBoxIconError)
				return
			}
			_ = g.hintLbl.SetText("已发送到 " + phone.Name + "，打开 App 即可接收")
			walk.MsgBox(g.mw, "已发送",
				file.Name+" 已投递到中转。\r\n\r\n手机打开 AirLink，在「待接收」里点下载即可。",
				walk.MsgBoxIconInformation)
		})
	}()
}

// pickPhone 有多台配对手机时让用户选，避免总是发给列表第一个。
func (g *gui) pickPhone(phones []PhoneInfo) (PhoneInfo, bool) {
	var dlg *walk.Dialog
	var box *walk.ComboBox
	var okPB, cancelPB *walk.PushButton

	labels := make([]string, len(phones))
	for i, p := range phones {
		labels[i] = fmt.Sprintf("%s · 最近 %s", p.Name, p.LastSeen.Format("01-02 15:04"))
	}

	err := Dialog{
		AssignTo: &dlg,
		Title:    "发送到哪台手机",
		Icon:     walk.IconInformation(),
		MinSize:  Size{Width: 420, Height: 190},
		Layout:   VBox{},
		Children: []Widget{
			Label{Text: "这台文件会投递到中转，对应手机打开 App 即可接收。"},
			ComboBox{AssignTo: &box, Model: labels, CurrentIndex: 0},
			Composite{
				Layout: HBox{},
				Children: []Widget{
					HSpacer{},
					PushButton{AssignTo: &okPB, Text: "发送", OnClicked: func() { dlg.Accept() }},
					PushButton{AssignTo: &cancelPB, Text: "取消", OnClicked: func() { dlg.Cancel() }},
				},
			},
		},
	}.Create(g.mw)
	if err != nil {
		return PhoneInfo{}, false
	}
	dlg.SetDefaultButton(okPB)
	dlg.SetCancelButton(cancelPB)

	if dlg.Run() != walk.DlgCmdOK {
		return PhoneInfo{}, false
	}
	i := box.CurrentIndex()
	if i < 0 || i >= len(phones) {
		return PhoneInfo{}, false
	}
	return phones[i], true
}

func phoneLabels(phones []PhoneInfo) []string {
	if len(phones) == 0 {
		return []string{"（还没有配对过的手机）"}
	}
	out := make([]string, len(phones))
	for i, p := range phones {
		out[i] = fmt.Sprintf("%s · 最近 %s", p.Name, p.LastSeen.Format("01-02 15:04"))
	}
	return out
}

func (g *gui) openFolder() {
	_ = exec.Command("explorer", g.srv.store.filesDir).Start()
}

func (g *gui) deleteSelected() {
	i := g.tv.CurrentIndex()
	if i < 0 || i >= len(g.model.items) {
		return
	}
	f := g.model.items[i]
	if walk.MsgBox(g.mw, "删除文件", "确定删除「"+f.Name+"」？",
		walk.MsgBoxIconQuestion|walk.MsgBoxYesNo) != walk.DlgCmdYes {
		return
	}
	_ = g.srv.store.Delete(f.ID)
	g.refreshFiles()
}
