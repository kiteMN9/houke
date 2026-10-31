package web

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"

	"school_sdk/check_code/kaptcha"
)

// kaptchaStatus 返回模型配置与加载状态。
func (s *Server) kaptchaStatus(c *gin.Context) {
	mgr := s.store.Kaptcha()
	if mgr == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "验证码配置未初始化"})
		return
	}
	c.JSON(http.StatusOK, mgr.Status())
}

// updateKaptcha 保存配置并立即热加载。
//
// 只提交需要改的字段即可（指针字段区分“没传”和“传了零值”）；配置页目前
// 每次提交完整表单，但这样以后加“只切换启用开关”的按钮不用改后端。
func (s *Server) updateKaptcha(c *gin.Context) {
	mgr := s.store.Kaptcha()
	if mgr == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "验证码配置未初始化"})
		return
	}
	var in struct {
		Enabled   *bool    `json:"enabled"`
		ModelPath *string  `json:"modelPath"`
		Threshold *float64 `json:"threshold"`
		Workers   *int     `json:"workers"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误: " + err.Error()})
		return
	}
	cur := mgr.Settings()
	if in.Enabled != nil {
		cur.Enabled = *in.Enabled
	}
	if in.ModelPath != nil {
		cur.ModelPath = *in.ModelPath
	}
	if in.Threshold != nil {
		cur.Threshold = *in.Threshold
	}
	if in.Workers != nil {
		cur.Workers = *in.Workers
	}
	if err := mgr.Apply(cur); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "status": mgr.Status()})
		return
	}
	c.JSON(http.StatusOK, mgr.Status())
}

// reloadKaptcha 按已保存的配置重新读一次模型文件。
func (s *Server) reloadKaptcha(c *gin.Context) {
	mgr := s.store.Kaptcha()
	if mgr == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "验证码配置未初始化"})
		return
	}
	if err := mgr.Reload(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "status": mgr.Status()})
		return
	}
	c.JSON(http.StatusOK, mgr.Status())
}

// uploadKaptchaModel 接收一个 .onnx 文件，存到配置文件旁边的 models/ 下，
// 然后把 ModelPath 指向它并立即加载。
func (s *Server) uploadKaptchaModel(c *gin.Context) {
	mgr := s.store.Kaptcha()
	if mgr == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "验证码配置未初始化"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, kaptcha.MaxModelBytes+(1<<20))
	fh, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取上传文件失败: " + err.Error()})
		return
	}
	if fh.Size <= 0 || fh.Size > kaptcha.MaxModelBytes {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("模型大小必须在 1 字节到 %d MB 之间", kaptcha.MaxModelBytes>>20),
		})
		return
	}
	name := sanitizeModelName(fh.Filename)
	if name == "" || !strings.EqualFold(filepath.Ext(name), ".onnx") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "只支持 .onnx 模型文件"})
		return
	}
	dir := filepath.Join(filepath.Dir(mgr.File()), "models")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "创建模型目录失败: " + err.Error()})
		return
	}
	dst := filepath.Join(dir, name)
	if err := saveUploadedModel(fh, dst); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存模型失败: " + err.Error()})
		return
	}

	cur := mgr.Settings()
	cur.Enabled = true
	cur.ModelPath = dst
	if err := mgr.Apply(cur); err != nil {
		// 文件已经落盘，保留它：用户改改路径或重新上传即可，不必再传一遍。
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "模型已保存到 " + dst + "，但加载失败: " + err.Error(),
			"status": mgr.Status(),
		})
		return
	}
	c.JSON(http.StatusOK, mgr.Status())
}

// testKaptcha 用当前模型识别一张上传的验证码图片，用于配置页的“测试识别”。
func (s *Server) testKaptcha(c *gin.Context) {
	mgr := s.store.Kaptcha()
	if mgr == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "验证码配置未初始化"})
		return
	}
	const maxImageBytes = 4 << 20
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxImageBytes+(1<<20))
	fh, err := c.FormFile("image")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取图片失败: " + err.Error()})
		return
	}
	if fh.Size <= 0 || fh.Size > maxImageBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "图片大小必须在 1 字节到 4 MB 之间"})
		return
	}
	f, err := fh.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "打开图片失败: " + err.Error()})
		return
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxImageBytes+1))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取图片失败: " + err.Error()})
		return
	}
	res, err := mgr.TestRecognize(c.Request.Context(), raw)
	if err != nil {
		msg := err.Error()
		if errors.Is(err, kaptcha.ErrDisabled) {
			msg = "自动识别未启用或模型尚未加载，请先保存配置"
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": msg})
		return
	}
	c.JSON(http.StatusOK, res)
}

// sanitizeModelName 只保留文件名本身和字母 / 数字 / ._-，避免路径穿越。
func sanitizeModelName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '.', r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), ".")
	if out == "" || out == "." || out == ".." {
		return ""
	}
	return out
}

// saveUploadedModel 先写临时文件再改名，避免上传中断留下半个模型。
func saveUploadedModel(fh *multipart.FileHeader, dst string) error {
	src, err := fh.Open()
	if err != nil {
		return err
	}
	defer src.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".kaptcha-*.onnx")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()

	n, err := io.Copy(tmp, io.LimitReader(src, kaptcha.MaxModelBytes+1))
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if n > kaptcha.MaxModelBytes {
		return fmt.Errorf("模型超过 %d MB", kaptcha.MaxModelBytes>>20)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return err
	}
	tmpName = "" // 已改名，defer 不要再删
	return nil
}
