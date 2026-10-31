package main

import (
	"image"
	"image/color"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// pillButton 圆角按钮。filled=true 是实心，false 是浅底。
func (u *ui) pillButton(c *widget.Clickable, label string, filled bool) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return material.Clickable(gtx, c, func(gtx layout.Context) layout.Dimensions {
			bg, fg := uiTile, uiGreen
			if filled {
				bg, fg = uiGreen, color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
			}
			if c.Hovered() && !filled {
				bg = color.NRGBA{R: 0xE2, G: 0xE6, B: 0xE3, A: 0xFF}
			}

			m := op.Record(gtx.Ops)
			dims := layout.Inset{
				Top: unit.Dp(7), Bottom: unit.Dp(7),
				Left: unit.Dp(16), Right: unit.Dp(16),
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
}

// inputBox 圆角输入框，内部包一个 Editor。
func (u *ui) inputBox(gtx layout.Context, ed *widget.Editor, hint string) layout.Dimensions {
	m := op.Record(gtx.Ops)
	dims := layout.Inset{
		Top: unit.Dp(9), Bottom: unit.Dp(9),
		Left: unit.Dp(14), Right: unit.Dp(14),
	}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		e := material.Editor(u.th, ed, hint)
		e.Color = uiInk
		e.HintColor = uiMuted
		e.TextSize = unit.Sp(14)
		return e.Layout(gtx)
	})
	call := m.Stop()

	r := gtx.Dp(unit.Dp(11))
	rr := clip.RRect{
		Rect: image.Rect(0, 0, dims.Size.X, dims.Size.Y),
		NW:   r, NE: r, SW: r, SE: r,
	}
	paint.FillShape(gtx.Ops, color.NRGBA{R: 0xF4, G: 0xF6, B: 0xF5, A: 0xFF}, rr.Op(gtx.Ops))

	call.Add(gtx.Ops)
	return dims
}

// textPanel 文字互传面板：发剪贴板、手输文字、查看和删除记录。
func (u *ui) textPanel(gtx layout.Context) {
	// 半透明遮罩，把主界面压暗
	paint.Fill(gtx.Ops, color.NRGBA{R: 0x0F, G: 0x14, B: 0x12, A: 0x70})

	layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(unit.Dp(560)), gtx.Dp(unit.Dp(392))))
		return u.card(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				// 标题
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							l := material.H6(u.th, "文字互传")
							l.Color = uiInk
							return l.Layout(gtx)
						}),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, 0)}
						}),
						layout.Rigid(u.linkButton(&u.btnCloseText, "关闭")),
					)
				}),
				layout.Rigid(layout.Spacer{Height: unit.Dp(14)}.Layout),

				// 输入 + 发送
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return u.inputBox(gtx, &u.textEditor, "输入要发送给手机的文字")
						}),
						layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
						layout.Rigid(u.pillButton(&u.btnSend, "发送", true)),
					)
				}),
				layout.Rigid(layout.Spacer{Height: unit.Dp(14)}.Layout),

				// 记录标题行
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							l := material.Body2(u.th, "最近记录")
							l.Color = uiMuted
							return l.Layout(gtx)
						}),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, 0)}
						}),
						layout.Rigid(u.linkButton(&u.btnClear, "清空")),
					)
				}),
				layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),

				// 记录列表
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					u.refreshClips()
					if len(u.clips) == 0 {
						return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							l := material.Body2(u.th, "还没有记录")
							l.Color = uiMuted
							return l.Layout(gtx)
						})
					}
					return u.clipList.Layout(gtx, len(u.clips), func(gtx layout.Context, i int) layout.Dimensions {
						return u.clipRowWidget(gtx, i)
					})
				}),
			)
		})
	})
}

func (u *ui) clipRowWidget(gtx layout.Context, i int) layout.Dimensions {
	c := u.clips[i]
	row := &u.clipRows[i]

	// 先处理这一行的按钮点击
	if row.copy.Clicked(gtx) {
		_ = setClipboard(c.Content)
	}
	if row.del.Clicked(gtx) {
		u.srv.store.DeleteClip(c.ID)
		u.clipKey = "" // 强制下一帧重建列表
	}

	// 行间分隔线，让每条记录分得开
	defer func() {
		line := clip.Rect{Max: image.Pt(gtx.Constraints.Max.X, gtx.Dp(1))}
		defer line.Push(gtx.Ops).Pop()
		paint.Fill(gtx.Ops, color.NRGBA{R: 0xEF, G: 0xF2, B: 0xF0, A: 0xFF})
	}()

	return layout.Inset{Top: unit.Dp(7), Bottom: unit.Dp(7)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			// 方向标记
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				arrow := "→"
				if c.From == "手机" {
					arrow = "←"
				}
				gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(unit.Dp(20)), gtx.Dp(unit.Dp(20))))
				l := material.Body2(u.th, arrow)
				l.Color = uiGreen
				return l.Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
			// 内容
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(u.th, clipPreview(c.Content))
				l.Color = uiInk
				l.MaxLines = 1
				return l.Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
			// 时间
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(unit.Dp(80)), gtx.Dp(unit.Dp(20))))
				l := material.Body2(u.th, c.At.Format("01-02 15:04"))
				l.Color = uiMuted
				return l.Layout(gtx)
			}),
			layout.Rigid(u.linkButton(&row.copy, "复制")),
			layout.Rigid(u.linkButton(&row.del, "删除")),
		)
	})
}

// refreshClips 只在记录变化时重建切片，按钮状态跟着一起扩容。
func (u *ui) refreshClips() {
	clips := u.srv.store.Clips()
	key := ""
	for _, c := range clips {
		key += c.ID
	}
	if key == u.clipKey {
		return
	}
	u.clipKey = key
	u.clips = clips
	u.clipRows = make([]clipRow, len(clips))
}

// clipPreview 把多行文本压成一行，列表里显示用。
func clipPreview(s string) string {
	out := make([]rune, 0, 48)
	for _, r := range s {
		if r == '\n' || r == '\r' {
			r = ' '
		}
		out = append(out, r)
		if len(out) >= 44 {
			return string(out) + "…"
		}
	}
	return string(out)
}
