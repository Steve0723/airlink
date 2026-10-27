package main

import (
	"bytes"
	"fmt"
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

type gui struct {
	mw      *walk.MainWindow
	qrView  *walk.ImageView
	devLbl  *walk.Label
	tokLbl  *walk.Label
	netBox  *walk.ComboBox
	hintLbl *walk.Label
	tv      *walk.TableView
	model   *fileModel

	autoBox  *walk.CheckBox
	clipLbl  *walk.Label
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
					_ = g.clipLbl.SetText("已复制到剪贴板：" + clipPreview(item.Content))
					return
				}
			}
			_ = g.clipLbl.SetText("手机：" + clipPreview(item.Content))
		})
	}

	g.startWatcher()
	g.mw.Run()
}

func (g *gui) toggleAutoCopy() {
	g.autoCopy = g.autoBox.Checked()
}

// sendClipboard 把 Windows 剪贴板里的文本推到服务端，手机端点一下就能拿走。
func (g *gui) sendClipboard() {
	text, err := walk.Clipboard().Text()
	if err != nil || strings.TrimSpace(text) == "" {
		_ = g.clipLbl.SetText("剪贴板里没有文本")
		return
	}
	g.srv.store.AddClip(text, "电脑")
	_ = g.clipLbl.SetText("已发送：" + clipPreview(text))
}

func clipPreview(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " ")
	r := []rune(s)
	if len(r) > 36 {
		return string(r[:36]) + "…"
	}
	return s
}

func (g *gui) build() error {
	if err := (MainWindow{
		AssignTo:    &g.mw,
		Title:       "AirLink · 手机电脑互传",
		Size:        Size{Width: 800, Height: 640},
		MinSize:     Size{Width: 700, Height: 540},
		Layout:      VBox{},
		OnDropFiles: g.addPaths,
		Children: []Widget{
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					ImageView{
						AssignTo: &g.qrView,
						MinSize:  Size{Width: 220, Height: 220},
						MaxSize:  Size{Width: 220, Height: 220},
					},
					Composite{
						Layout: VBox{},
						Children: []Widget{
							Label{
								Text: "手机扫码配对",
								Font: Font{PointSize: 15, Bold: true},
							},
							Label{AssignTo: &g.devLbl, Text: ""},
							Composite{
								Layout: HBox{MarginsZero: true},
								Children: []Widget{
									Label{Text: "配对码"},
									Label{AssignTo: &g.tokLbl, Text: "", MinSize: Size{Width: 150}, Font: Font{Family: "Consolas", PointSize: 11, Bold: true}},
									HSpacer{},
									PushButton{Text: "复制", OnClicked: g.copyToken},
									PushButton{Text: "重置", OnClicked: g.rotateToken},
								},
							},
							Composite{
								Layout: HBox{MarginsZero: true},
								Children: []Widget{
									Label{Text: "网络地址"},
									ComboBox{
										AssignTo:              &g.netBox,
										Model:                 []string{},
										OnCurrentIndexChanged: g.onPickIP,
									},
									PushButton{Text: "重新检测", OnClicked: g.reloadNet},
								},
							},
							Label{AssignTo: &g.hintLbl, Text: ""},
						},
					},
				},
			},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					PushButton{Text: "发送剪贴板", OnClicked: g.sendClipboard},
					CheckBox{
						AssignTo:         &g.autoBox,
						Text:             "自动复制手机发来的文字",
						Checked:          true,
						OnCheckedChanged: g.toggleAutoCopy,
					},
					HSpacer{},
					Label{AssignTo: &g.clipLbl, Text: ""},
				},
			},
			TableView{
				AssignTo: &g.tv,
				Model:    g.model,
				Columns: []TableViewColumn{
					{Title: "文件名", Width: 420},
					{Title: "大小", Width: 100},
					{Title: "接收时间", Width: 130},
				},
				StretchFactor: 1,
			},
			Composite{
				Layout: HBox{MarginsZero: true},
				Children: []Widget{
					PushButton{Text: "添加文件", OnClicked: g.pickFiles},
					PushButton{Text: "打开文件夹", OnClicked: g.openFolder},
					PushButton{Text: "删除选中", OnClicked: g.deleteSelected},
					PushButton{Text: "分享选中", OnClicked: g.shareSelected},
					HSpacer{},
					PushButton{Text: "刷新", OnClicked: g.refreshFiles},
				},
			},
		},
	}).Create(); err != nil {
		return err
	}

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
	labels := make([]string, 0, len(g.eps))
	for _, e := range g.eps {
		labels = append(labels, fmt.Sprintf("%s  ·  %s", e.Addr(), e.Iface))
	}
	if len(labels) == 0 {
		labels = []string{"未找到可用网络"}
	}
	g.netBox.SetModel(labels)

	idx := 0
	pref := g.srv.preferredIP()
	for i, e := range g.eps {
		if e.IP == pref {
			idx = i
			break
		}
	}
	if len(g.eps) > 0 {
		g.netBox.SetCurrentIndex(idx)
	}

	g.devLbl.SetText("设备：" + g.srv.deviceName())
	g.tokLbl.SetText(g.srv.token()[:8])
	g.updateQR()
	g.updateHint()
}

func (g *gui) onPickIP() {
	if g.loading {
		return
	}
	i := g.netBox.CurrentIndex()
	if i < 0 || i >= len(g.eps) {
		return
	}
	g.srv.setPreferredIP(g.eps[i].IP)
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
	hint := fmt.Sprintf("端口 %d · 已收到 %d 个文件", g.srv.port, len(files))
	if len(g.eps) == 0 {
		hint = "没有找到局域网地址，请检查 WiFi 或网线连接"
	} else if g.srv.preferredIP() != "" {
		hint = fmt.Sprintf("已指定地址 %s · 端口 %d · %d 个文件", g.srv.preferredIP(), g.srv.port, len(files))
	}
	_ = g.hintLbl.SetText(hint)
}

func (g *gui) refreshFiles() {
	files := g.srv.store.Files()
	g.model.set(files)
	g.updateHint()
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
	g.hintLbl.SetText("配对码已复制到剪贴板")
}

func (g *gui) rotateToken() {
	if walk.MsgBox(g.mw, "重置配对码",
		"重新生成后，已配对的手机需要重新扫码。继续？",
		walk.MsgBoxIconQuestion|walk.MsgBoxYesNo) != walk.DlgCmdYes {
		return
	}
	g.srv.rotateToken()
	g.tokLbl.SetText(g.srv.token()[:8])
	g.updateQR()
	g.hintLbl.SetText("配对码已重置")
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

// shareSelected 生成一条 4 位短链，别人不装 App 也能在浏览器里下载。
func (g *gui) shareSelected() {
	i := g.tv.CurrentIndex()
	if i < 0 || i >= len(g.model.items) {
		_ = g.clipLbl.SetText("先在列表里选中一个文件")
		return
	}
	file := g.model.items[i]

	ttlLabels := []string{"1 小时", "24 小时", "7 天"}
	ttlHours := []int{1, 24, 168}

	var dlg *walk.Dialog
	var ttlBox *walk.ComboBox
	var pwEdit *walk.LineEdit
	var okPB, cancelPB *walk.PushButton

	err := Dialog{
		AssignTo: &dlg,
		Title:    "分享「" + file.Name + "」",
		Icon:     walk.IconInformation(),
		MinSize:  Size{Width: 420, Height: 240},
		Layout:   VBox{},
		Children: []Widget{
			Composite{
				Layout: Grid{Columns: 2},
				Children: []Widget{
					Label{Text: "有效期"},
					ComboBox{AssignTo: &ttlBox, Model: ttlLabels, CurrentIndex: 1},
					Label{Text: "提取码"},
					LineEdit{AssignTo: &pwEdit, MaxLength: 4},
					Label{Text: ""},
					Label{
						Text: "可选，4 位数字；留空则拿到链接就能下",
						Font: Font{PointSize: 8},
					},
				},
			},
			Composite{
				Layout: HBox{},
				Children: []Widget{
					HSpacer{},
					PushButton{AssignTo: &okPB, Text: "生成链接", OnClicked: func() { dlg.Accept() }},
					PushButton{AssignTo: &cancelPB, Text: "取消", OnClicked: func() { dlg.Cancel() }},
				},
			},
		},
	}.Create(g.mw)
	if err != nil {
		walk.MsgBox(g.mw, "打开对话框失败", err.Error(), walk.MsgBoxIconError)
		return
	}
	dlg.SetDefaultButton(okPB)
	dlg.SetCancelButton(cancelPB)

	if dlg.Run() != walk.DlgCmdOK {
		return
	}

	pw := strings.TrimSpace(pwEdit.Text())
	if pw != "" && (len(pw) != 4 || !isDigits(pw)) {
		walk.MsgBox(g.mw, "提取码无效", "提取码必须是 4 位数字。", walk.MsgBoxIconWarning)
		return
	}

	idx := ttlBox.CurrentIndex()
	if idx < 0 || idx >= len(ttlHours) {
		idx = 1
	}
	share, err := g.srv.store.AddShare(file.ID, pw, time.Duration(ttlHours[idx])*time.Hour)
	if err != nil {
		walk.MsgBox(g.mw, "生成失败", err.Error(), walk.MsgBoxIconError)
		return
	}

	link := g.srv.shareURL(share.Code)
	_ = walk.Clipboard().SetText(link)

	msg := link + "\r\n\r\n有效期至 " + share.ExpiresAt.Format("01-02 15:04")
	if pw != "" {
		msg += "\r\n提取码：" + pw
	}
	msg += "\r\n\r\n链接已复制到剪贴板，发给对方即可。"
	walk.MsgBox(g.mw, "分享链接已生成", msg, walk.MsgBoxIconInformation)
	_ = g.clipLbl.SetText("已分享：" + share.Code)
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
