//go:build windows

package check_code

import "fmt"

//func run(img image.Image) {
//	cfg := pixelgl.WindowConfig{
//		Title:  "Captcha Viewer",
//		Bounds: pixel.R(0, 0, float64(img.Bounds().Dx()), float64(img.Bounds().Dy())),
//		VSync:  true,
//	}
//
//	win, err := pixelgl.NewWindow(cfg)
//	if err != nil {
//		panic(err)
//	}
//
//	picData := pixel.PictureDataFromImage(img)
//	sprite := pixel.NewSprite(picData, picData.Bounds())
//
//	for !win.Closed() {
//		win.Clear(colornames.White)
//		sprite.Draw(win, pixel.IM.Moved(win.Bounds().Center()))
//		win.Update()
//	}
//}

//func DisplayCaptcha(data []byte) error {
//	img, err := decodeImage(data)
//	if err != nil {
//		return err
//	}
//	pixelgl.Run(func() {
//		run(img)
//	})
//	return nil
//}

func DisplayCaptcha(data []byte) error {
	SaveImgStream(data, "./", "kaptcha")
	fmt.Println("图片已保存")
	return nil
}
