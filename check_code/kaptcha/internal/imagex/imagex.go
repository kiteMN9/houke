// Package imagex 把任意格式的验证码图片转成模型需要的灰度张量。
//
// 预处理必须和训练时保持一致：训练用的是 torchvision 的 Grayscale()+ToTensor()，
// 即按 ITU-R 601-2 亮度公式在 [0,1] 上做加权平均，数据布局是 (C,H,W) 单通道。
package imagex

import (
	"bytes"
	"fmt"
	"image"
	"io"

	// 注册常见解码器。kaptcha 默认输出 jpg/png，gif/bmp 属于顺手支持。
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

// Decode 解码图片字节。支持 png/jpeg/gif。
func Decode(raw []byte) (image.Image, error) {
	img, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("imagex: 无法解码图片: %w", err)
	}
	_ = format
	return img, nil
}

// DecodeReader 从 reader 解码图片。
func DecodeReader(r io.Reader) (image.Image, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return Decode(raw)
}

// luma 复刻 PIL 的 convert("L")：L = (19595R + 38470G + 7471B + 0x8000) >> 16。
// 训练数据就是 prepare.py 用 PIL 转灰度后存下来的，所以这里必须一致。
func luma(r, g, b uint8) float32 {
	v := (19595*uint32(r) + 38470*uint32(g) + 7471*uint32(b) + 0x8000) >> 16
	return float32(v) / 255
}

// Gray 把图片转成 size 为 h×w 的灰度浮点切片，取值域 [0,1]，行主序。
//
// 已经是目标尺寸时不做缩放，避免引入重采样误差——kaptcha 生成的图正好是 200×50。
func Gray(img image.Image, w, h int) ([]float32, error) {
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw <= 0 || sh <= 0 {
		return nil, fmt.Errorf("imagex: 图片尺寸非法 %dx%d", sw, sh)
	}
	gray := make([]float32, sw*sh)
	switch src := img.(type) {
	case *image.Gray:
		for y := 0; y < sh; y++ {
			row := src.Pix[(y+b.Min.Y-src.Rect.Min.Y)*src.Stride:][:sw]
			dst := gray[y*sw : (y+1)*sw]
			for x, v := range row {
				dst[x] = float32(v) / 255
			}
		}
	case *image.RGBA:
		for y := 0; y < sh; y++ {
			row := src.Pix[(y+b.Min.Y-src.Rect.Min.Y)*src.Stride:][:sw*4]
			dst := gray[y*sw : (y+1)*sw]
			for x := 0; x < sw; x++ {
				p := row[x*4:]
				dst[x] = luma(p[0], p[1], p[2])
			}
		}
	case *image.NRGBA:
		for y := 0; y < sh; y++ {
			row := src.Pix[(y+b.Min.Y-src.Rect.Min.Y)*src.Stride:][:sw*4]
			dst := gray[y*sw : (y+1)*sw]
			for x := 0; x < sw; x++ {
				p := row[x*4:]
				dst[x] = luma(p[0], p[1], p[2])
			}
		}
	default:
		// YCbCr/CMYK/调色板等交给标准库转换，再按同样的公式取亮度。
		for y := 0; y < sh; y++ {
			dst := gray[y*sw : (y+1)*sw]
			for x := 0; x < sw; x++ {
				r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
				dst[x] = luma(uint8(r>>8), uint8(g>>8), uint8(bl>>8))
			}
		}
	}
	if sw == w && sh == h {
		return gray, nil
	}
	return resizeBilinear(gray, sw, sh, w, h), nil
}

// resizeBilinear 双线性缩放灰度图。验证码图不要求像素级一致，双线性足够。
func resizeBilinear(src []float32, sw, sh, dw, dh int) []float32 {
	dst := make([]float32, dw*dh)
	for y := 0; y < dh; y++ {
		// 用像素中心对齐，避免整体偏移半个像素。
		sy := (float64(y)+0.5)*float64(sh)/float64(dh) - 0.5
		y0 := int(sy)
		if sy < 0 {
			y0 = 0
		}
		if y0 > sh-1 {
			y0 = sh - 1
		}
		y1 := y0 + 1
		if y1 > sh-1 {
			y1 = sh - 1
		}
		fy := float32(sy - float64(y0))
		if fy < 0 {
			fy = 0
		}
		for x := 0; x < dw; x++ {
			sx := (float64(x)+0.5)*float64(sw)/float64(dw) - 0.5
			x0 := int(sx)
			if sx < 0 {
				x0 = 0
			}
			if x0 > sw-1 {
				x0 = sw - 1
			}
			x1 := x0 + 1
			if x1 > sw-1 {
				x1 = sw - 1
			}
			fx := float32(sx - float64(x0))
			if fx < 0 {
				fx = 0
			}
			v00 := src[y0*sw+x0]
			v01 := src[y0*sw+x1]
			v10 := src[y1*sw+x0]
			v11 := src[y1*sw+x1]
			top := v00 + (v01-v00)*fx
			bot := v10 + (v11-v10)*fx
			dst[y*dw+x] = top + (bot-top)*fy
		}
	}
	return dst
}
