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

// contextMenu 记录右键菜单的状态。
// Gio 没有内置的右键菜单，得自己处理 pointer 的次键事件 + 画浮层。
type contextMenu struct {
	visible bool
	row     int        // 目标行，-1 表示没有
	pos     image.Point // 弹出的位置（窗口客户区坐标，dp）
}

// menuItem 画一个菜单项。
// action 为 nil 时画一条分隔线。
func (u *ui) menuItem(c *widget.Clickable, label string, danger bool, action func()) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		// Clicked 必须在 Layout 之前 —— 它内部会调 update，和 Layout 抢事件
		if c.Clicked(gtx) {
			if action != nil {
				action()
			}
			u.ctxMenu.visible = false
		}

		return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			m := op.Record(gtx.Ops)
			dims := layout.Inset{
				Top: unit.Dp(7), Bottom: unit.Dp(7),
				Left: unit.Dp(14), Right: unit.Dp(28),
			}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(u.th, label)
				l.TextSize = unit.Sp(13)
				if danger {
					l.Color = color.NRGBA{R: 0xD1, G: 0x4B, B: 0x4B, A: 0xFF}
				} else {
					l.Color = uiInk
				}
				return l.Layout(gtx)
			})
			call := m.Stop()

			if c.Hovered() {
				bg := color.NRGBA{R: 0xF2, G: 0xF7, B: 0xF4, A: 0xFF}
				if danger {
					bg = color.NRGBA{R: 0xFC, G: 0xEF, B: 0xEF, A: 0xFF}
				}
				r := gtx.Dp(unit.Dp(6))
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
}

// menuSeparator 菜单里的分隔线。
func (u *ui) menuSeparator(gtx layout.Context) layout.Dimensions {
	return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		h := gtx.Dp(1)
		w := gtx.Constraints.Max.X
		rect := clip.Rect{Max: image.Pt(w, h)}
		defer rect.Push(gtx.Ops).Pop()
		paint.Fill(gtx.Ops, color.NRGBA{R: 0xEC, G: 0xEF, B: 0xED, A: 0xFF})
		return layout.Dimensions{Size: image.Pt(w, h)}
	})
}

// drawContextMenu 按鼠标位置把菜单画出来。
// 坐标是窗口客户区，所以直接在根坐标系用 op.Offset 定位。
func (u *ui) drawContextMenu(gtx layout.Context) {
	if !u.ctxMenu.visible {
		return
	}

	mw := gtx.Dp(unit.Dp(176))
	maxX := gtx.Constraints.Max.X
	maxY := gtx.Constraints.Max.Y

	pos := u.ctxMenu.pos
	// 靠近右边或下边时往回缩，别跑出窗口
	if pos.X+mw > maxX {
		pos.X = maxX - mw
	}
	if pos.X < 0 {
		pos.X = 0
	}
	if pos.Y < 0 {
		pos.Y = 0
	}

	stack := op.Offset(pos).Push(gtx.Ops)
	defer stack.Pop()

	gtx.Constraints = layout.Constraints{
		Min: image.Pt(mw, 0),
		Max: image.Pt(mw, maxY-pos.Y),
	}
	u.contextMenuWidget(gtx)
}

// contextMenuWidget 画右键菜单的浮层。
func (u *ui) contextMenuWidget(gtx layout.Context) layout.Dimensions {
	if !u.ctxMenu.visible {
		return layout.Dimensions{}
	}
	// 目标行可能已经被删掉了
	if u.ctxMenu.row < 0 || u.ctxMenu.row >= len(u.files) {
		u.ctxMenu.visible = false
		return layout.Dimensions{}
	}

	items := []layout.Widget{
		u.menuItem(&u.ctxOpen, "打开", false, u.openSelected),
		u.menuItem(&u.ctxReveal, "在文件夹中显示", false, u.revealSelected),
		u.menuItem(&u.ctxCopyName, "复制文件名", false, u.copySelectedName),
		u.menuSeparator,
		u.menuItem(&u.ctxDelete, "删除", true, u.deleteSelected),
	}

	m := op.Record(gtx.Ops)
	dims := layout.UniformInset(unit.Dp(5)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		children := make([]layout.FlexChild, 0, len(items))
		for _, it := range items {
			item := it
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return item(gtx)
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
	call := m.Stop()

	// 白色圆角底 + 1dp 浅灰描边，模拟浮层
	r := gtx.Dp(unit.Dp(10))
	outer := clip.RRect{
		Rect: image.Rect(0, 0, dims.Size.X, dims.Size.Y),
		NW:   r, NE: r, SW: r, SE: r,
	}
	paint.FillShape(gtx.Ops, color.NRGBA{R: 0xDF, G: 0xE4, B: 0xE1, A: 0xFF}, outer.Op(gtx.Ops))

	inset := gtx.Dp(1)
	inner := clip.RRect{
		Rect: image.Rect(inset, inset, dims.Size.X-inset, dims.Size.Y-inset),
		NW:   r, NE: r, SW: r, SE: r,
	}
	paint.FillShape(gtx.Ops, color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}, inner.Op(gtx.Ops))

	call.Add(gtx.Ops)
	return dims
}

// copySelectedName 复制文件名（不是路径，文件名才是常被粘贴的）。
func (u *ui) copySelectedName() {
	f, _, ok := u.selectedPath()
	if !ok {
		return
	}
	_ = setClipboard(f.Name)
}
