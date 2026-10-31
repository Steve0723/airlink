package main

import (
	"fmt"
	"image"
	"os"
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
		Top: unit.Dp(7), Bottom: unit.Dp(7),
		Left: unit.Dp(12), Right: unit.Dp(12),
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
		// 小窗口下几乎贴满，只留 8dp 让阴影能看出来
		m := gtx.Constraints.Max
		gap := gtx.Dp(unit.Dp(8))
		w, h := m.X-gap, m.Y-gap
		// 大窗口时别铺满整个屏幕
		if maxW := gtx.Dp(unit.Dp(760)); w > maxW {
			w = maxW
		}
		if maxH := gtx.Dp(unit.Dp(580)); h > maxH {
			h = maxH
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
							return u.inputBox(gtx, &u.textEditor, "")
						}),
						layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
						layout.Rigid(u.pillButton(&u.btnSend, "发送", true)),
					)
				}),
				layout.Rigid(layout.Spacer{Height: unit.Dp(9)}.Layout),

				// 分类标签：文字互传 / 剪贴板
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(u.tabButton(&u.tabText, "文字互传", 0)),
						layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
						layout.Rigid(u.tabButton(&u.tabClip, "剪贴板", 1)),
					)
				}),
				layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),

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
					if debugLayout {
						fmt.Fprintf(os.Stderr, "[flexed] 收到约束 %v\n", gtx.Constraints)
					}
					u.refreshClips()
					if len(u.clips) == 0 {
						return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							l := material.Body2(u.th, "还没有记录")
							l.Color = uiMuted
							return l.Layout(gtx)
						})
					}
					if debugLayout {
						fmt.Fprintf(os.Stderr, "[list] 收到约束 %v\n", gtx.Constraints)
					}
					return u.clipList.Layout(gtx, len(u.clips), func(gtx layout.Context, i int) layout.Dimensions {
						d := u.clipRowWidget(gtx, i)
						if debugLayout {
							fmt.Fprintf(os.Stderr, "[clipRow] i=%d size=%v\n", i, d.Size)
						}
						return d
					})
				}),
			)
		})
	})
}

// tabButton 画一个标签页按钮，点中就切到对应分类
func (u *ui) tabButton(c *widget.Clickable, label string, idx int) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if c.Clicked(gtx) {
			u.clipTab = idx
		}
		active := u.clipTab == idx

		return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			m := op.Record(gtx.Ops)
			dims := layout.Inset{
				Top: unit.Dp(5), Bottom: unit.Dp(5),
				Left: unit.Dp(12), Right: unit.Dp(12),
			}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(u.th, label)
				l.TextSize = unit.Sp(12)
				if active {
					l.Color = color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
				} else {
					l.Color = uiMuted
				}
				return l.Layout(gtx)
			})
			call := m.Stop()

			bg := color.NRGBA{R: 0xEC, G: 0xEF, B: 0xED, A: 0xFF}
			if active {
				bg = uiGreen
			}
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

func (u *ui) clipRowWidget(gtx layout.Context, i int) layout.Dimensions {
	c := u.clips[i]
	row := &u.clipRows[i]

	if row.copy.Clicked(gtx) {
		_ = setClipboard(c.Content)
	}
	if row.del.Clicked(gtx) {
		u.srv.store.DeleteClip(c.ID)
	}

	// 宽度自己算，不用 Flexed —— 列表给的约束在异常情况下会是
	// layout.Inf(1000000)，Flexed 照单全收，一行就撑到天边，
	// 后面的记录全被挤出去（这就是「只显示一条」的成因）。
	maxX := gtx.Constraints.Max.X
	if maxX <= 0 || maxX > 10000 {
		maxX = gtx.Dp(unit.Dp(520)) // 兜底
	}
	btnW := gtx.Dp(unit.Dp(84)) // 复制 + 删除
	contentW := maxX - btnW - gtx.Dp(unit.Dp(16))
	if contentW < gtx.Dp(unit.Dp(80)) {
		contentW = gtx.Dp(unit.Dp(80))
	}

	return layout.Inset{Top: unit.Dp(2), Bottom: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		m := op.Record(gtx.Ops)
		dims := layout.Inset{
			Top: unit.Dp(7), Bottom: unit.Dp(7),
			Left: unit.Dp(10), Right: unit.Dp(6),
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					// 只限宽度，高度让它按内容自适应 ——
					// 之前写死 38dp，内容占两行就把下面的「来源·时间」挤没了
					gtx.Constraints.Min.X = contentW
					gtx.Constraints.Max.X = contentW
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							l := material.Body2(u.th, c.Content)
							l.Color = uiInk
							l.TextSize = unit.Sp(13)
							l.MaxLines = 2
							return l.Layout(gtx)
						}),
						layout.Rigid(layout.Spacer{Height: unit.Dp(3)}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							l := material.Body2(u.th, c.From+" · "+c.At.Format("01-02 15:04"))
							l.Color = uiMuted
							l.TextSize = unit.Sp(11)
							return l.Layout(gtx)
						}),
					)
				}),
				layout.Rigid(u.linkButton(&row.copy, "复制")),
				layout.Rigid(u.linkButtonColored(&row.del, "删除", uiDanger)),
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
	kind := KindText
	if u.clipTab == 1 {
		kind = KindClip
	}
	all := u.srv.store.Clips(kind)
	clips := all

	// 只显示最近 N 条，N 可以在设置里改
	if limit := u.srv.clipLimit(); limit > 0 && len(clips) > limit {
		clips = clips[:limit]
	}

	// 之前用 key 缓存避免每帧重建，但那个逻辑在不同步时会让列表卡在旧数据上
	// （清空、删除、换条数都踩过）。记录本来就不多，直接重建最省心。
	u.clips = clips
	if len(u.clipRows) != len(clips) {
		u.clipRows = make([]clipRow, len(clips))
	}
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
