//go:build !windows

package check_code

import "fmt"

func DisplayCaptcha(data []byte) error {
	SaveImgStream(data, "./", "kaptcha")
	fmt.Println("图片已保存")
	return nil
}
