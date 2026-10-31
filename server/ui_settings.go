package main

import (
	"fmt"
	"image"
	"image/color"
	"strings"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// phoneRow 保存「已配对手机」每一行的移除按钮。
type phoneRow struct {
	remove widget.Clickable
}

// openSettings 打开设置面板。每次打开都重新读一遍当前配置，
// 这样外面（比如手机端）改过的值也能看到。
func (u *ui) openSettings() {
	u.relayURL.SetText(u.srv.relayURL())
	u.relayKey.SetText(u.srv.relayKey())
	u.phones = u.srv.phones()
	u.phoneRows = make([]phoneRow, len(u.phones))
	u.showSettings = true
}

// settingsPanel 设置面板：配对码、网络地址、公网中转、已配对手机。
func (u *ui) settingsPanel(gtx layout.Context) {
	paintOverlay(gtx)

	layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		// 窗口被拖小时面板跟着缩，别超出窗口被裁掉
		w, h := gtx.Dp(unit.Dp(600)), gtx.Dp(unit.Dp(602))
		if m := gtx.Constraints.Max; w > m.X {
			w = m.X
		}
		if m := gtx.Constraints.Max; h > m.Y {
			h = m.Y
		}
		gtx.Constraints = layout.Exact(image.Pt(w, h))
		return u.card(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							l := material.H6(u.th, "设置")
							l.Color = uiInk
							return l.Layout(gtx)
						}),
						layout.Flexed(1, spacerX),
						layout.Rigid(u.linkButton(&u.btnCloseSettings, "关闭")),
					)
				}),
				layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),

				// 中间整块放进 List，窗口小时可以滚
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return u.settingsScroll.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				// ---------- 配对 ----------
				layout.Rigid(u.sectionTitle("配对")),
				layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							l := material.Body2(u.th, "配对码")
							l.Color = uiMuted
							return l.Layout(gtx)
						}),
						layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							l := material.Body1(u.th, shortToken(u.srv.token()))
							l.Color = uiInk
							return l.Layout(gtx)
						}),
						layout.Rigid(layout.Spacer{Width: unit.Dp(14)}.Layout),
						layout.Rigid(u.pillButton(&u.btnCopy, "复制", false)),
						layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
						layout.Rigid(u.pillButton(&u.btnReset, "重置", false)),
						layout.Flexed(1, spacerX),
					)
				}),
				layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),

				// ---------- 网络地址 ----------
				layout.Rigid(u.sectionTitle("网络地址")),
				layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return u.netChoices(gtx)
				}),
				layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),

				// ---------- 文件保存位置 ----------
				layout.Rigid(u.sectionTitle("文件保存位置")),
				layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							l := material.Body2(u.th, u.srv.store.FilesDir())
							l.Color = uiInk
							l.MaxLines = 1
							return l.Layout(gtx)
						}),
						layout.Rigid(u.pillButton(&u.btnPickDir, "更改", false)),
						layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
						layout.Rigid(u.pillButton(&u.btnResetDir, "恢复默认", false)),
					)
				}),
				layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),

				// ---------- 最近记录 ----------
				layout.Rigid(u.sectionTitle("最近记录")),
				layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					children := []layout.FlexChild{
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							l := material.Body2(u.th, "显示条数")
							l.Color = uiMuted
							return l.Layout(gtx)
						}),
						layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
					}
					for i, n := range clipLimitOptions {
						if i > 0 {
							children = append(children, layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout))
						}
						children = append(children, layout.Rigid(u.limitChip(i, n)))
					}
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, children...)
				}),
				layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),

				// ---------- 公网中转 ----------
				layout.Rigid(u.sectionTitle("公网中转")),
				layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(u.fieldLabel("地址")),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return u.inputBox(gtx, &u.relayURL, "https://drop.example.com")
						}),
					)
				}),
				layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(u.fieldLabel("密钥")),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return u.inputBox(gtx, &u.relayKey, "中转服务给的上传密钥")
						}),
					)
				}),
				layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),

				// ---------- 已配对手机 ----------
				layout.Rigid(u.sectionTitle("已配对的手机")),
				layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if len(u.phones) == 0 {
						return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							l := material.Body2(u.th, "还没有手机配对过")
							l.Color = uiMuted
							return l.Layout(gtx)
						})
					}
					return u.phoneList.Layout(gtx, len(u.phones), func(gtx layout.Context, i int) layout.Dimensions {
						return u.phoneRowWidget(gtx, i)
					})
				}),
						)
					})
				}),
				layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),

				// ---------- 底部 ----------
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							if u.settingsError == "" {
								return layout.Dimensions{}
							}
							l := material.Body2(u.th, u.settingsError)
							l.Color = color.NRGBA{R: 0xD1, G: 0x4B, B: 0x4B, A: 0xFF}
							return l.Layout(gtx)
						}),
						layout.Rigid(u.pillButton(&u.btnCancelSettings, "取消", false)),
						layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
						layout.Rigid(u.pillButton(&u.btnSaveSettings, "保存", true)),
					)
				}),
			)
		})
	})
}

// clipLimitOptions 设置里可选的显示条数
var clipLimitOptions = [5]int{10, 20, 30, 50, 100}

// limitChip 画一个「显示条数」选项
func (u *ui) limitChip(idx, n int) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		c := &u.limitChips[idx]
		if c.Clicked(gtx) {
			u.srv.setClipLimit(n)
			u.clipKey = "" // 强制重建列表
		}
		selected := u.srv.clipLimit() == n
		return u.chip(c, selected, fmt.Sprintf("%d", n))(gtx)
	}
}

// netChoices 网络地址用一排可点的标签，比下拉框直观：
// 多网卡机器上一眼能看到每张网卡的名字。
func (u *ui) netChoices(gtx layout.Context) layout.Dimensions {
	eps := Candidates(u.srv.port)
	if len(eps) == 0 {
		l := material.Body2(u.th, "没有找到可用网络")
		l.Color = uiMuted
		return l.Layout(gtx)
	}

	// 网卡数量可能变，按需扩容
	for len(u.netChips) < len(eps) {
		u.netChips = append(u.netChips, widget.Clickable{})
	}

	pref := u.srv.preferredIP()

	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		func() []layout.FlexChild {
			children := make([]layout.FlexChild, 0, len(eps)*2)
			for i := range eps {
				ep := eps[i]
				chip := &u.netChips[i]
				selected := ep.IP == pref || (pref == "" && i == 0)

				if chip.Clicked(gtx) {
					u.srv.setPreferredIP(ep.IP)
					u.qrKey = "" // 地址变了，二维码要重画
				}

				if i > 0 {
					children = append(children, layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout))
				}
				children = append(children, layout.Rigid(u.chip(chip, selected, ep.IP)))
			}
			return children
		}()...,
	)
}

func (u *ui) phoneRowWidget(gtx layout.Context, i int) layout.Dimensions {
	p := u.phones[i]
	row := &u.phoneRows[i]

	if row.remove.Clicked(gtx) {
		u.srv.removePhone(p.ID)
		u.phones = append(u.phones[:i], u.phones[i+1:]...)
		u.phoneRows = append(u.phoneRows[:i], u.phoneRows[i+1:]...)
		return layout.Dimensions{}
	}

	return layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(u.th, p.Name)
				l.Color = uiInk
				return l.Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(u.th, "上次 "+p.LastSeen.Format("01-02 15:04"))
				l.Color = uiMuted
				return l.Layout(gtx)
			}),
			layout.Flexed(1, spacerX),
			layout.Rigid(u.linkButton(&row.remove, "移除")),
		)
	})
}

// saveSettings 校验并写入配置。
func (u *ui) saveSettings() {
	url := strings.TrimSpace(u.relayURL.Text())
	key := strings.TrimSpace(u.relayKey.Text())

	if (url == "") != (key == "") {
		u.settingsError = "地址和密钥要么都填，要么都留空"
		return
	}
	if url != "" && !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		u.settingsError = "地址要以 http:// 或 https:// 开头"
		return
	}

	u.srv.setRelay(url, key)
	u.qrKey = ""
	u.settingsError = ""
	u.showSettings = false
}

// ---------- 小工具 ----------

// spacerX 占满剩余宽度，用来把右侧内容顶到边上。
func spacerX(gtx layout.Context) layout.Dimensions {
	return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, 0)}
}

func paintOverlay(gtx layout.Context) {
	paint.Fill(gtx.Ops, color.NRGBA{R: 0x0F, G: 0x14, B: 0x12, A: 0x70})
}

// pillStyled 按指定配色画一个胶囊按钮。
func (u *ui) pillStyled(gtx layout.Context, c *widget.Clickable, label string, bg, fg color.NRGBA) layout.Dimensions {
	return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		m := op.Record(gtx.Ops)
		dims := layout.Inset{
			Top: unit.Dp(7), Bottom: unit.Dp(7),
			Left: unit.Dp(14), Right: unit.Dp(14),
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			l := material.Body2(u.th, label)
			l.Color = fg
			return l.Layout(gtx)
		})
		call := m.Stop()

		r := dims.Size.Y / 2
		rr := clip.RRect{
			Rect: image.Rect(0, 0, dims.Size.X, dims.Size.Y),
			NW:   r, NE: r, SW: r, SE: r,
		}
		paint.FillShape(gtx.Ops, bg, rr.Op(gtx.Ops))
		call.Add(gtx.Ops)
		return dims
	})
}

func (u *ui) sectionTitle(text string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		l := material.Body2(u.th, text)
		l.Color = uiMuted
		l.TextSize = unit.Sp(12)
		return l.Layout(gtx)
	}
}

func (u *ui) fieldLabel(text string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(unit.Dp(46)), gtx.Dp(unit.Dp(20))))
		l := material.Body2(u.th, text)
		l.Color = uiMuted
		return l.Layout(gtx)
	}
}

// chip 选中态的圆角标签。
func (u *ui) chip(c *widget.Clickable, selected bool, label string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		bg, fg := uiTile, uiInk
		if selected {
			bg, fg = uiGreen, color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
		} else if c.Pressed() {
			bg = color.NRGBA{R: 0xD4, G: 0xDA, B: 0xD6, A: 0xFF}
		} else if c.Hovered() {
			bg = color.NRGBA{R: 0xE2, G: 0xE6, B: 0xE3, A: 0xFF}
		}
		return u.pillStyled(gtx, c, label, bg, fg)
	}
}
