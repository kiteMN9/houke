package check_code

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	// testPieceWidth 拼图块宽度：生产环境里服务端返回的是一根窄竖条
	testPieceWidth = 60
	// testPieceHeight 内置背景图高度（拼图块高度必须与之一致才会参与匹配）
	testPieceHeight = 200
	// testBgWidth 内置背景图宽度（原图 304 裁掉左边 70）
	testBgWidth = 234
)

// waitCacheReady 等待包 init 里异步预加载的图片缓存就绪。
func waitCacheReady(t *testing.T) {
	t.Helper()
	select {
	case <-cacheReady:
	case <-time.After(10 * time.Second):
		t.Fatal("等待图片缓存就绪超时")
	}
}

// mustCachedImages 返回已经加载好的内置背景图模板。
func mustCachedImages(t *testing.T) []cachedImage {
	t.Helper()
	waitCacheReady(t)
	cached, err := getCachedImages()
	require.NoError(t, err)
	require.NotEmpty(t, cached)
	return cached
}

// mustPNG 把图片编码成 PNG 字节，模拟服务端下发的验证码图片。
func mustPNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// colorImage 生成一张每像素都不同的不透明小图，便于校验落盘/解码结果。
func colorImage(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8((x*37 + y*11) % 251),
				G: uint8((x*13 + y*59) % 251),
				B: uint8((x*7 + y*23) % 251),
				A: 255,
			})
		}
	}
	return img
}

// syntheticBackground 生成一张确定性噪点背景，用来替代内置模板测试匹配算法。
func syntheticBackground(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			hash := uint32(x)*73856093 ^ uint32(y)*19349663
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(hash >> 8),
				G: uint8(hash >> 16),
				B: uint8(hash >> 24),
				A: 255,
			})
		}
	}
	return img
}

// cropPiece 从背景图中裁出一块不透明竖条，等价于「满不透明的拼图块」。
func cropPiece(t *testing.T, src image.Image, x0, w int) *image.NRGBA {
	t.Helper()
	b := src.Bounds()
	piece := image.NewNRGBA(image.Rect(0, 0, w, b.Dy()))
	draw.Draw(piece, piece.Bounds(), src, image.Pt(b.Min.X+x0, b.Min.Y), draw.Src)
	return piece
}

// transparentPiece 让竖条右侧大部分透明，模拟真实拼图块「缺口处不透明、其余透明」的形态。
func transparentPiece(t *testing.T, src image.Image, x0, w, opaqueWidth int) *image.NRGBA {
	t.Helper()
	piece := cropPiece(t, src, x0, w)
	for y := range piece.Bounds().Dy() {
		for x := opaqueWidth; x < w; x++ {
			piece.SetNRGBA(x, y, color.NRGBA{})
		}
	}
	return piece
}

func TestDecodeImage(t *testing.T) {
	img := colorImage(4, 3)
	got, err := decodeImage(mustPNG(t, img))
	require.NoError(t, err)
	require.Equal(t, 4, got.Bounds().Dx())
	require.Equal(t, 3, got.Bounds().Dy())

	_, err = decodeImage([]byte("not an image"))
	require.Error(t, err)
}

// TestLowerWhiteGetNonTransParentPixels 覆盖掩码提取的三条规则：
// 纯白不透明跳过、完全透明跳过、按行序每第 3 个像素跳过。
func TestLowerWhiteGetNonTransParentPixels(t *testing.T) {
	t.Run("跳过白底/透明像素与每第三个像素", func(t *testing.T) {
		img := image.NewNRGBA(image.Rect(0, 0, 3, 3))
		img.SetNRGBA(0, 0, color.NRGBA{R: 255, G: 255, B: 255, A: 255}) // 第 1 个：纯白，跳过
		img.SetNRGBA(1, 0, color.NRGBA{R: 90, G: 10, B: 20, A: 0})      // 第 2 个：透明，跳过
		img.SetNRGBA(2, 0, color.NRGBA{R: 10, G: 20, B: 30, A: 255})    // 第 3 个：按规则跳过
		img.SetNRGBA(0, 1, color.NRGBA{R: 11, G: 21, B: 31, A: 255})
		img.SetNRGBA(1, 1, color.NRGBA{R: 12, G: 22, B: 32, A: 255})
		img.SetNRGBA(2, 1, color.NRGBA{R: 13, G: 23, B: 33, A: 255}) // 第 6 个：跳过
		img.SetNRGBA(0, 2, color.NRGBA{R: 14, G: 24, B: 34, A: 255})
		img.SetNRGBA(1, 2, color.NRGBA{R: 15, G: 25, B: 35, A: 255})
		img.SetNRGBA(2, 2, color.NRGBA{R: 16, G: 26, B: 36, A: 255}) // 第 9 个：跳过

		pixels, dx, dy := lowerWhiteGetNonTransParentPixels(img)
		require.Equal(t, 3, dx)
		require.Equal(t, 3, dy)
		require.Equal(t, []pixelInfo{
			{X: 0, Y: 1, Color: color.NRGBA{R: 11, G: 21, B: 31}},
			{X: 1, Y: 1, Color: color.NRGBA{R: 12, G: 22, B: 32}},
			{X: 0, Y: 2, Color: color.NRGBA{R: 14, G: 24, B: 34}},
			{X: 1, Y: 2, Color: color.NRGBA{R: 15, G: 25, B: 35}},
		}, pixels)
	})

	t.Run("坐标相对于图像起点", func(t *testing.T) {
		img := image.NewNRGBA(image.Rect(10, 20, 13, 23))
		for y := 20; y < 23; y++ {
			for x := 10; x < 13; x++ {
				img.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 7, A: 255})
			}
		}

		pixels, dx, dy := lowerWhiteGetNonTransParentPixels(img)
		require.Equal(t, 3, dx)
		require.Equal(t, 3, dy)
		require.Equal(t, []pixelInfo{
			{X: 0, Y: 0, Color: color.NRGBA{R: 10, G: 20, B: 7}},
			{X: 1, Y: 0, Color: color.NRGBA{R: 11, G: 20, B: 7}},
			{X: 0, Y: 1, Color: color.NRGBA{R: 10, G: 21, B: 7}},
			{X: 1, Y: 1, Color: color.NRGBA{R: 11, G: 21, B: 7}},
			{X: 0, Y: 2, Color: color.NRGBA{R: 10, G: 22, B: 7}},
			{X: 1, Y: 2, Color: color.NRGBA{R: 11, G: 22, B: 7}},
		}, pixels)
	})

	t.Run("RGBA 输入按 NRGBA 转换后取像素", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 2, 2))
		img.SetRGBA(0, 0, color.RGBA{R: 1, G: 2, B: 3, A: 0})         // 第 1 个：透明，跳过
		img.SetRGBA(1, 0, color.RGBA{R: 4, G: 5, B: 6, A: 255})       // 第 2 个：保留
		img.SetRGBA(0, 1, color.RGBA{R: 255, G: 255, B: 255, A: 255}) // 第 3 个：跳过
		img.SetRGBA(1, 1, color.RGBA{R: 7, G: 8, B: 9, A: 255})       // 第 4 个：保留

		pixels, dx, dy := lowerWhiteGetNonTransParentPixels(img)
		require.Equal(t, 2, dx)
		require.Equal(t, 2, dy)
		require.Equal(t, []pixelInfo{
			{X: 1, Y: 0, Color: color.NRGBA{R: 4, G: 5, B: 6}},
			{X: 1, Y: 1, Color: color.NRGBA{R: 7, G: 8, B: 9}},
		}, pixels)
	})
}

// TestFindBestMatchWithImages 用确定性噪点背景验证滑窗匹配的命中位置与差异值，
// 覆盖最左/中间/最右三个边界以及非 NRGBA 背景、透明拼图块。
func TestFindBestMatchWithImages(t *testing.T) {
	bg := syntheticBackground(testBgWidth, testPieceHeight)
	rgbaBg := image.NewRGBA(bg.Bounds())
	draw.Draw(rgbaBg, rgbaBg.Bounds(), bg, image.Point{}, draw.Src)

	cases := []struct {
		name  string
		bg    image.Image
		piece image.Image
		wantX int
	}{
		{"命中左边界", bg, cropPiece(t, bg, 0, testPieceWidth), 0},
		{"命中中间", bg, cropPiece(t, bg, 57, testPieceWidth), 57},
		{"命中右边界", bg, cropPiece(t, bg, testBgWidth-testPieceWidth, testPieceWidth), testBgWidth - testPieceWidth},
		{"透明拼图块", bg, transparentPiece(t, bg, 87, testPieceWidth, 20), 87},
		{"RGBA 背景自动转换", rgbaBg, cropPiece(t, bg, 33, testPieceWidth), 33},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			maskPixels, dx, dy := lowerWhiteGetNonTransParentPixels(c.piece)
			require.Equal(t, testPieceWidth, dx)
			require.Equal(t, testPieceHeight, dy)
			require.NotEmpty(t, maskPixels, "拼图块应当含有可用像素")

			gotX, diff, err := FindBestMatchWithImages(c.bg, maskPixels, dx)
			require.NoError(t, err)
			require.Equal(t, c.wantX, gotX)
			require.Zero(t, diff, "命中自身的像素差异应当为 0")
		})
	}
}

// TestGetCachedImages 校验内置模板的加载结果。
func TestGetCachedImages(t *testing.T) {
	cached := mustCachedImages(t)
	require.Len(t, cached, 10, "内置背景模板数量变了，请同步更新断言")
	for i, c := range cached {
		require.NotNil(t, c.img, "第 %d 张缓存图为空", i)
		require.Equal(t, c.img.Bounds().Dy(), c.dy, "第 %d 张缓存图的 dy 与图片高度不一致", i)
	}
}

// TestFindBestMatch 走完整生产路径：PNG 解码 → 掩码提取 → 与全部内置背景匹配 → +70 补偿。
func TestFindBestMatch(t *testing.T) {
	cached := mustCachedImages(t)
	bg := cached[0].img
	bounds := bg.Bounds()
	require.Greater(t, bounds.Dx(), testPieceWidth)

	for _, x0 := range []int{0, 57, bounds.Dx() - testPieceWidth} {
		piece := cropPiece(t, bg, x0, testPieceWidth)
		// 内置背景被裁掉了左边 70px，所以识别结果要补回 70。
		require.Equal(t, x0+70, FindBestMatch(mustPNG(t, piece)),
			"拼图块在背景中的位置 x0=%d", x0)
	}

	t.Run("非图片数据返回 0", func(t *testing.T) {
		require.Zero(t, FindBestMatch([]byte("not an image")))
	})
}

// TestFindBestMatchConcurrent 让多个 goroutine 同时识别，配合 -race 覆盖缓存只读路径。
func TestFindBestMatchConcurrent(t *testing.T) {
	cached := mustCachedImages(t)
	piece := cropPiece(t, cached[0].img, 57, testPieceWidth)
	data := mustPNG(t, piece)

	const (
		workers    = 8
		iterations = 5
	)
	got := make([]int, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range iterations {
				got[i] = FindBestMatch(data)
			}
		}()
	}
	wg.Wait()

	for i, x := range got {
		require.Equal(t, 127, x, "第 %d 个 worker 的识别结果不一致", i)
	}
}

// TestCacheInitConcurrent 重置缓存后重新异步初始化，让 FindBestMatch 与 initImageCache
// 真正并发。这条用例是给 -race 用的：它覆盖 isCacheReady / cacheInitError / imageCache
// 在初始化期间的所有读写。
func TestCacheInitConcurrent(t *testing.T) {
	// 先等第一次（包 init 触发的）预加载结束，避免和重置动作交叉。
	waitCacheReady(t)
	resetCacheForTest(t)

	_, err := getCachedImages()
	require.Error(t, err, "缓存尚未就绪时应返回错误")

	// 缓存已清空，直接从内嵌资源里取一张背景图造拼图块。
	embedded, err := embeddedFS.ReadFile("merged/0.png")
	require.NoError(t, err)
	bg, err := decodeImage(embedded)
	require.NoError(t, err)
	piece := cropPiece(t, bg, 57, testPieceWidth)
	data := mustPNG(t, piece)

	// 重新触发异步初始化，随后立刻并发调用识别与 PreloadCache。
	PreloadCache()

	const recognizers = 8
	var wg sync.WaitGroup
	for range recognizers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 3 {
				// 初始化完成前会阻塞在 cacheReady 上，这里不校验返回值。
				_ = FindBestMatch(data)
			}
		}()
	}
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			PreloadCache()
		}()
	}
	wg.Wait()

	waitCacheReady(t)
	cached, err := getCachedImages()
	require.NoError(t, err)
	require.Len(t, cached, 10)
	require.Equal(t, 127, FindBestMatch(data))
}

// resetCacheForTest 把图片缓存恢复成「尚未初始化」，用于覆盖并发初始化路径。
// 仅限同包测试、且没有其它测试并行执行（不使用 t.Parallel）时调用。
func resetCacheForTest(t *testing.T) {
	t.Helper()
	cacheMutex.Lock()
	imageCache = nil
	cacheInitError = nil
	cacheMutex.Unlock()

	isCacheReady.Store(false)
	cacheReady = make(chan struct{})
	cacheInitOnce = sync.Once{}
}
