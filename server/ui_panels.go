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
		if !u.interactive {
			return u.pillBody(gtx, label, filled, false, false)
		}
		return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return u.pillBody(gtx, label, filled, c.Hovered(), c.Pressed())
		})
	}
}

func (u *ui) pillBody(gtx layout.Context, label string, filled, hovered, pressed bool) layout.Dimensions {
	bg, fg := uiTile, uiGreen
	if filled {
		bg, fg = uiGreen, color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
	}
	if !filled {
		if hovered {
			bg = color.NRGBA{R: 0xE2, G: 0xE6, B: 0xE3, A: 0xFF}
		}
		if pressed {
			bg = color.NRGBA{R: 0xD4, G: 0xDA, B: 0xD6, A: 0xFF}
		}
	} else if pressed {
		bg = color.NRGBA{R: 0x17, G: 0x86, B: 0x63, A: 0xFF}
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
		w, h := gtx.Dp(unit.Dp(640)), gtx.Dp(unit.Dp(500))
		if m := gtx.Constraints.Max; w > m.X {
			w = m.X
		}
		if m := gtx.Constraints.Max; h > m.Y {
			h = m.Y
		}
		gtx.Constraints = layout.Exact(image.Pt(w, h))
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

	if row.copy.Clicked(gtx) {
		_ = setClipboard(c.Content)
	}
	if row.del.Clicked(gtx) {
		u.srv.store.DeleteClip(c.ID)
		u.clipKey = "" // 强制下一帧重建列表
	}

	// 每条做成一张浅灰圆角卡片，和手机端 .texts li 一致：
	// 内容在上、来源·时间在下、按钮靠右
	return layout.Inset{Top: unit.Dp(3), Bottom: unit.Dp(3)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		m := op.Record(gtx.Ops)
		dims := layout.Inset{
			Top: unit.Dp(9), Bottom: unit.Dp(9),
			Left: unit.Dp(12), Right: unit.Dp(8),
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							l := material.Body2(u.th, c.Content)
							l.Color = uiInk
							l.TextSize = unit.Sp(13)
							l.MaxLines = 3
							return l.Layout(gtx)
						}),
						layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							l := material.Body2(u.th, c.From+" · "+c.At.Format("01-02 15:04"))
							l.Color = uiMuted
							l.TextSize = unit.Sp(11)
							return l.Layout(gtx)
						}),
					)
				}),
				layout.Rigid(u.linkButton(&row.copy, "复制")),
				layout.Rigid(u.linkButton(&row.del, "删除")),
			)
		})
		call := m.Stop()

		r := gtx.Dp(unit.Dp(10))
		rr := clip.RRect{
			Rect: image.Rect(0, 0, dims.Size.X, dims.Size.Y),
			NW:   r, NE: r, SW: r, SE: r,
		}
		paint.FillShape(gtx.Ops, color.NRGBA{R: 0xF4, G: 0xF6, B: 0xF5, A: 0xFF}, rr.Op(gtx.Ops))

		call.Add(gtx.Ops)
		return dims
	})
}

// refreshClips 只在记录变化时重建切片，按钮状态跟着一起扩容。
func (u *ui) refreshClips() {
	clips := u.srv.store.Clips()

	// 只显示最近 N 条，N 可以在设置里改
	if limit := u.srv.clipLimit(); limit > 0 && len(clips) > limit {
		clips = clips[:limit]
	}

	key := ""
	for _, c := range clips {
		key += c.ID
	}
	// 清空之后 clips 为空、key 也是空串，只比 key 会误判成"没变化"，
	// 列表就一直停在旧数据上。加上长度比较才兜得住。
	if key == u.clipKey && len(clips) == len(u.clips) {
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
