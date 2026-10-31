package web

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"school_sdk/check_code/kaptcha"
)

func TestKaptchaConfigOverHTTP(t *testing.T) {
	store := newTestStore(t)
	srv := httptest.NewServer(mustNewServer(t, store).router)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/kaptcha") //nolint:noctx // 测试里不关心 ctx
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET status = %d (%s), want 200", resp.StatusCode, body)
	}
	var st kaptcha.Status
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		t.Fatalf("解析状态: %v", err)
	}
	if !st.Settings.Enabled || st.Settings.Threshold != kaptcha.DefaultThreshold {
		t.Errorf("默认设置 = %+v", st.Settings)
	}
	if st.SettingsFile == "" || st.NumChars != kaptcha.NumChars {
		t.Errorf("状态缺少配置路径或模型规格: %+v", st)
	}

	// 只提交要改的字段：enabled=false 后模型应被卸载。
	body := strings.NewReader(`{"enabled":false,"threshold":0.95,"workers":2}`)
	req, err := http.NewRequest(http.MethodPut, srv.URL+"/api/kaptcha", body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	putResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer putResp.Body.Close()
	if putResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(putResp.Body)
		t.Fatalf("PUT status = %d (%s), want 200", putResp.StatusCode, raw)
	}
	var updated kaptcha.Status
	if err := json.NewDecoder(putResp.Body).Decode(&updated); err != nil {
		t.Fatalf("解析 PUT 响应: %v", err)
	}
	if updated.Settings.Enabled || updated.Settings.Threshold != 0.95 || updated.Settings.Workers != 2 {
		t.Errorf("保存后的设置 = %+v", updated.Settings)
	}
	if updated.Enabled || updated.Loaded {
		t.Errorf("关闭后不应仍处于已加载状态: %+v", updated)
	}
	if _, err := os.Stat(store.Kaptcha().File()); err != nil {
		t.Errorf("kaptcha.json 未落盘: %v", err)
	}
}

func TestKaptchaConfigRejectsBadThreshold(t *testing.T) {
	store := newTestStore(t)
	srv := httptest.NewServer(mustNewServer(t, store).router)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPut, srv.URL+"/api/kaptcha",
		strings.NewReader(`{"threshold":2}`))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("PUT status = %d, want 400", resp.StatusCode)
	}
	if got := store.Kaptcha().Settings(); got.Threshold != kaptcha.DefaultThreshold {
		t.Errorf("非法阈值不应改设置，实际 %+v", got)
	}
}

func TestKaptchaModelUploadSanitizesName(t *testing.T) {
	store := newTestStore(t)
	srv := httptest.NewServer(mustNewServer(t, store).router)
	defer srv.Close()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "../../evil.onnx")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write([]byte("this is not an onnx model")); err != nil {
		t.Fatalf("写上传内容: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/kaptcha/model", &buf)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	// 内容不是合法 ONNX，加载会失败；但文件已按清洗后的名字落盘，方便排查/替换。
	if resp.StatusCode != http.StatusBadRequest {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST status = %d (%s), want 400", resp.StatusCode, raw)
	}
	dst := filepath.Join(filepath.Dir(store.Kaptcha().File()), "models", "evil.onnx")
	if _, err := os.Stat(dst); err != nil {
		t.Errorf("上传文件未按清洗后的名字保存到 %s: %v", dst, err)
	}
}

func TestKaptchaModelUploadRejectsNonOnnx(t *testing.T) {
	store := newTestStore(t)
	srv := httptest.NewServer(mustNewServer(t, store).router)
	defer srv.Close()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "evil.exe")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write([]byte("MZ")); err != nil {
		t.Fatalf("写上传内容: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/kaptcha/model", &buf)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST status = %d (%s), want 400", resp.StatusCode, raw)
	}
	dst := filepath.Join(filepath.Dir(store.Kaptcha().File()), "models", "model.onnx")
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Errorf("非 .onnx 文件不应落盘为 %s", dst)
	}
}

func TestKaptchaTestImageWhenDisabled(t *testing.T) {
	store := newTestStore(t)
	srv := httptest.NewServer(mustNewServer(t, store).router)
	defer srv.Close()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("image", "captcha.png")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write([]byte("not a real image")); err != nil {
		t.Fatalf("写图片: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/kaptcha/test", &buf)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST status = %d (%s), want 400", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "未启用") {
		t.Errorf("错误文案 = %s, want 含「未启用」", raw)
	}
}
