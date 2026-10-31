package check_code

import (
	"bytes"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsExist(t *testing.T) {
	dir := t.TempDir()
	require.True(t, IsExist(dir))
	require.False(t, IsExist(filepath.Join(dir, "missing")))

	file := filepath.Join(dir, "a.txt")
	require.NoError(t, os.WriteFile(file, []byte("hi"), 0o644))
	require.True(t, IsExist(file))
}

func TestSaveFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	data := []byte{0x00, 0x01, 0xff, 'a'}

	SaveFile(dir, "x.bin", data)

	got, err := os.ReadFile(filepath.Join(dir, "x.bin"))
	require.NoError(t, err)
	require.Equal(t, data, got)

	// 目标目录已存在时应当直接覆盖写。
	SaveFile(dir, "x.bin", []byte("second"))
	got, err = os.ReadFile(filepath.Join(dir, "x.bin"))
	require.NoError(t, err)
	require.Equal(t, []byte("second"), got)
}

func TestSaveImg(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "imgs")
	img := colorImage(5, 4)

	SavePngImg(img, dir, "puzzle")

	file := filepath.Join(dir, "puzzle.png")
	require.True(t, IsExist(file))

	data, err := os.ReadFile(file)
	require.NoError(t, err)
	decoded, err := png.Decode(bytes.NewReader(data))
	require.NoError(t, err)
	require.Equal(t, 5, decoded.Bounds().Dx())
	require.Equal(t, 4, decoded.Bounds().Dy())
	require.Equal(t, img.NRGBAAt(3, 2), color.NRGBAModel.Convert(decoded.At(3, 2)))
}

// TestSaveImg_DefaultName msg 为空时用毫秒时间戳命名。
func TestSaveImg_DefaultName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "imgs")

	SavePngImg(colorImage(2, 2), dir, "")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Regexp(t, `^\d+\.png$`, entries[0].Name())
}

func TestSaveImgStream(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "imgs")
	SaveImgStream(mustPNG(t, colorImage(3, 2)), dir, "stream")
	require.True(t, IsExist(filepath.Join(dir, "stream.png")))

	// 空数据或无法解码的数据不应创建目录/文件。
	badDir := filepath.Join(t.TempDir(), "bad")
	SaveImgStream(nil, badDir, "empty")
	SaveImgStream([]byte("not an image"), badDir, "bad")
	require.False(t, IsExist(badDir))
}

func TestDisplayCaptcha(t *testing.T) {
	t.Chdir(t.TempDir())

	require.NoError(t, DisplayCaptcha(mustPNG(t, colorImage(4, 4))))
	require.True(t, IsExist("kaptcha.png"))
}
