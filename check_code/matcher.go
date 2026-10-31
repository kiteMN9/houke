package check_code

import (
	"bytes"
	"embed"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"path"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/disintegration/imaging"
)

// 缓存结构体
type cachedImage struct {
	img image.Image
	//maskPixels []pixelInfo
	//dx         int
	dy int
	//name       string
}

// 全局缓存变量
var (
	imageCache     []cachedImage
	cacheInitOnce  sync.Once
	cacheInitError error
	cacheMutex     sync.RWMutex
	cacheReady     = make(chan struct{}) // 用于通知缓存已准备好
	isCacheReady   atomic.Bool           // 缓存就绪标志（初始化 goroutine 与识别并发读写）
)

// FindBestMatch 公开的识别函数，会检查缓存是否就绪
func FindBestMatch(data []byte) int {
	img, err := decodeImage(data)
	if err != nil {
		fmt.Println("err:", err)
		return 0
	}
	// 检查缓存是否就绪，如果没有就绪则等待
	if !isCacheReady.Load() {
		select {
		case <-cacheReady:
			// 缓存已就绪，继续执行
		case <-time.After(10 * time.Second):
			// 超时，返回默认值
			fmt.Println("等待预缓存超时")
			return 0
		}
	}
	return getMatchedX(img)
}

// 图像解码统一处理
func decodeImage(data []byte) (image.Image, error) {
	img, err := imaging.Decode(bytes.NewReader(data))
	//img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	data = nil
	return img, nil
}

type result struct {
	x    int
	diff int32
}

// FindBestMatchWithImages 直接接收图片对象
func FindBestMatchWithImages(largeImg image.Image, maskPixels []pixelInfo, dx int) (int, int32, error) {
	// // 确保 largeImg 是宽度更大的图片
	// if smallImg.Bounds().Dx() > largeImg.Bounds().Dx() {
	// 	largeImg, smallImg = smallImg, largeImg
	// }

	// 并行计算参数
	bounds := largeImg.Bounds()
	maxX := bounds.Dx() - dx
	var wg sync.WaitGroup

	// 启动多个 worker（按 CPU 核心数）
	numWorkers := runtime.NumCPU()
	xStep := maxX / numWorkers
	resultCh := make(chan result, numWorkers+1)

	// 如果已经是NRGBA类型，直接返回
	largeNRGBA, ok := largeImg.(*image.NRGBA)
	if !ok {
		// 将大图转换为 NRGBA 提升访问速度
		largeNRGBA = image.NewNRGBA(bounds)
		draw.Draw(largeNRGBA, bounds, largeImg, image.Point{}, draw.Src)
	}

	for workerID := range numWorkers {
		wg.Add(1)
		startX := workerID * xStep
		endX := startX + xStep
		if workerID == numWorkers-1 {
			endX = maxX
		}

		go func(start, end int) {
			defer wg.Done()

			var currentDiff int32 = 0
			bestXR := 0
			var minDiffR int32 = math.MaxInt32

			for x := start; x <= end; x++ {
				currentDiff = 0
				for _, p := range maskPixels {
					// 直接访问像素数组
					offset := largeNRGBA.PixOffset(x+p.X, p.Y)

					// 计算差异
					dr := int32(largeNRGBA.Pix[offset]) - int32(p.Color.R)
					if dr < 0 {
						dr = -dr
					}
					dg := int32(largeNRGBA.Pix[offset+1]) - int32(p.Color.G)
					if dg < 0 {
						dg = -dg
					}
					db := int32(largeNRGBA.Pix[offset+2]) - int32(p.Color.B)
					if db < 0 {
						db = -db
					}

					currentDiff += dr + dg + db
				}

				if currentDiff < minDiffR {
					minDiffR = currentDiff
					bestXR = x
				}
			}
			resultCh <- result{x: bestXR, diff: minDiffR}
		}(startX, endX)
	}

	// 等待并收集结果
	go func() {
		wg.Wait()
		close(resultCh)
	}()
	var minDiff int32 = math.MaxInt32
	bestX := 0

	for res := range resultCh {
		if res.diff < minDiff {
			minDiff = res.diff
			bestX = res.x
		}
	}
	//largeImg = nil
	//largeNRGBA = nil

	return bestX, minDiff, nil
}

//go:embed merged/*.png
var embeddedFS embed.FS

// initImageCache 并发初始化图片缓存
func initImageCache() {
	// log.Println("开始预加载图片缓存...")
	//startTime := time.Now()

	entries, err := embeddedFS.ReadDir("merged")
	if err != nil {
		cacheMutex.Lock()
		cacheInitError = fmt.Errorf("读取嵌入目录失败: %v", err)
		cacheMutex.Unlock()

		isCacheReady.Store(true)
		close(cacheReady)
		return
	}

	var wg sync.WaitGroup
	tempCache := make([]cachedImage, 0, len(entries))
	var tempMutex sync.Mutex

	// 限制并发数，避免过多占用资源
	maxConcurrency := runtime.NumCPU() * 2
	semaphore := make(chan struct{}, maxConcurrency)

	for _, entry := range entries {
		if entry.IsDir() || path.Ext(entry.Name()) != ".png" {
			continue
		}

		wg.Add(1)
		go func(filename string) {
			defer wg.Done()

			// 获取信号量
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			filePath := path.Join("merged", filename)
			data, readErr := embeddedFS.ReadFile(filePath)
			if readErr != nil {
				fmt.Printf("读取失败 %s: %v", filePath, readErr)
				return
			}

			reader := bytes.NewReader(data)
			img, decErr := png.Decode(reader)
			if decErr != nil {
				fmt.Printf("解码失败 %s: %v", filePath, decErr)
				return
			}

			// 预处理图片的像素信息
			//maskPixels, _, dy := lowerWhiteGetNonTransParentPixels(img)

			tempMutex.Lock()
			tempCache = append(tempCache, cachedImage{
				img: img,
				//maskPixels: maskPixels,
				//dx:         dx,
				dy: img.Bounds().Dy(),
				//name:       filename,
			})
			tempMutex.Unlock()
		}(entry.Name())
	}

	wg.Wait()
	close(semaphore)

	cacheMutex.Lock()
	imageCache = tempCache
	if len(tempCache) == 0 {
		cacheInitError = fmt.Errorf("没有成功加载任何图片")
	}
	cacheMutex.Unlock()

	if len(tempCache) == 0 {
		fmt.Println("警告: 没有成功加载任何图片")
	} else if len(tempCache) != 10 {
		fmt.Printf("警告: 共加载 %d 张图片", len(tempCache))
	}
	//fmt.Printf("共加载 %d 张图片, 耗时 %v", len(imageCache), time.Since(startTime))

	isCacheReady.Store(true)
	close(cacheReady)
}

// getCachedImages 获取缓存的图片
func getCachedImages() ([]cachedImage, error) {
	if !isCacheReady.Load() {
		return nil, fmt.Errorf("图片缓存尚未就绪")
	}

	cacheMutex.RLock()
	defer cacheMutex.RUnlock()

	if cacheInitError != nil {
		return nil, cacheInitError
	}

	// 返回副本，调用方在锁外遍历时不会再碰到内部切片被替换
	cached := make([]cachedImage, len(imageCache))
	copy(cached, imageCache)
	return cached, nil
}

// PreloadCache 预加载缓存 - 应该在程序启动时调用
func PreloadCache() {
	cacheInitOnce.Do(func() {
		go initImageCache()
	})
}

// IsCacheReady 检查缓存是否就绪
//func IsCacheReady() bool {
//	return isCacheReady
//}

// WaitForCacheReady 等待缓存就绪（带超时）
//func WaitForCacheReady(timeout time.Duration) bool {
//	if isCacheReady {
//		return true
//	}
//
//	select {
//	case <-cacheReady:
//		return true
//	case <-time.After(timeout):
//		return false
//	}
//}

func getMatchedX(img image.Image) int {
	// 获取输入图片的非透明像素坐标和颜色
	maskPixels, dx, dy := lowerWhiteGetNonTransParentPixels(img)

	// 获取缓存的图片
	cachedImages, err := getCachedImages()
	if err != nil {
		fmt.Printf("获取缓存图片失败: %v", err)
		return 0
	}

	resultCh := make(chan result, len(cachedImages)+1)
	var wg sync.WaitGroup
	//now := time.Now()
	for _, cached := range cachedImages {
		// 验证高度一致
		if cached.dy != dy {
			continue
		}

		wg.Add(1)
		go func(cachedImg cachedImage) {
			defer wg.Done()

			// 使用缓存的图片直接进行匹配
			currentX, currentDiff, matchErr := FindBestMatchWithImages(cachedImg.img, maskPixels, dx)
			if matchErr != nil {
				fmt.Println("匹配错误:", matchErr)
				return
			}
			resultCh <- result{x: currentX, diff: currentDiff}
		}(cached)
	}

	// 等待并收集结果
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	var minDiff int32 = math.MaxInt32
	bestX := 0

	for res := range resultCh {
		if res.diff < minDiff {
			minDiff = res.diff
			bestX = res.x
		}
		if minDiff < 75 {
			return bestX + 70
		}
	}
	//fmt.Println(minDiff, bestX, time.Since(now))
	return bestX + 70
}

// 像素信息结构体
type pixelInfo struct {
	X, Y  int
	Color color.NRGBA
}

func lowerWhiteGetNonTransParentPixels(img image.Image) ([]pixelInfo, int, int) {
	// 创建新图片容器（使用NRGBA以保留透明度）
	bounds := img.Bounds()
	// dstImg := image.NewNRGBA(bounds)
	//var pixels []pixelInfo
	pixels := make([]pixelInfo, 0, 2680)

	rgbaImg, ok := img.(*image.NRGBA)
	if !ok {
		rgbaImg = image.NewNRGBA(bounds)
		draw.Draw(rgbaImg, bounds, img, image.Point{}, draw.Src)
	}

	count := 0
	// 遍历每个像素进行调整
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			count++
			// 选择性跳过一些像素
			if count%3 == 0 {
				continue
			}
			// 获取原始颜色和透明度
			// 尝试直接访问 *image.RGBA
			idx := rgbaImg.PixOffset(x, y)
			r8, g8, b8, a8 := rgbaImg.Pix[idx], rgbaImg.Pix[idx+1], rgbaImg.Pix[idx+2], rgbaImg.Pix[idx+3]

			// 判断是否为纯白色且不透明
			if r8 == 255 && g8 == 255 && b8 == 255 && a8 == 255 {
				// 设置Alpha为0（完全透明）
				// dstImg.Set(x, y, color.NRGBA{R: newR, G: newG, B: newB, A: 0})
				continue
			} else if a8 > 0 {
				pixels = append(pixels, pixelInfo{
					X:     x - bounds.Min.X, // 转换为相对坐标
					Y:     y - bounds.Min.Y,
					Color: color.NRGBA{R: r8, G: g8, B: b8},
				})
			}
		}
	}

	return pixels, bounds.Dx(), bounds.Dy()
}

// 包初始化时自动开始预加载
func init() {
	PreloadCache()
}
