package jwmock

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math/rand"
)

// makeKaptchaPNG 生成一张字符验证码图片。
//
// 模拟端不校验图片内容（客户端要么用识别器、要么通过 Ask 回调把测试里约定
// 的答案填回来），所以这里只需要一张能被 image/png 解码的图。
func makeKaptchaPNG() []byte {
	const w, h = 120, 40
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	// 浅色底 + 噪点，视觉上像一张验证码。
	for y := range h {
		for x := range w {
			c := uint8(235 + rand.Intn(20))
			img.SetNRGBA(x, y, color.NRGBA{R: c, G: c, B: c, A: 255})
		}
	}
	for range 60 {
		x := rand.Intn(w)
		y := rand.Intn(h)
		r := uint8(rand.Intn(120))
		img.SetNRGBA(x, y, color.NRGBA{R: r, G: uint8(rand.Intn(120)), B: uint8(rand.Intn(120)), A: 255})
	}
	return encodePNG(img)
}

// makeSliderPNG 生成一张滑块验证码的“拼图”图片。
//
// 客户端会调用 check_code.FindBestMatch：它按高度匹配内置背景，再把非纯白
// 像素当作 mask 做模板匹配，最终返回 bestX+70（恒 >= 70）。因此这里给一张
// 60x200、大部分透明、带一小块不透明色块的图即可；即使高度不匹配，识别
// 结果也只是退化成 70，不会失败。
func makeSliderPNG() []byte {
	const w, h = 60, 200
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	// 顶部一条不透明色块，模拟缺口位置。
	for y := 30; y < 60; y++ {
		for x := 10; x < 40; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 180, G: 160, B: 120, A: 230})
		}
	}
	for range 400 {
		x := rand.Intn(w)
		y := rand.Intn(h)
		if x >= 10 && x < 40 && y >= 30 && y < 60 {
			continue
		}
		img.SetNRGBA(x, y, color.NRGBA{R: 90, G: 90, B: 90, A: 255})
	}
	return encodePNG(img)
}

// encodePNG 把图片编码成 PNG；编码失败时返回 nil（调用方会写空响应）。
func encodePNG(img image.Image) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}
