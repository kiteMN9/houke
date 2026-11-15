package protocol

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// StaticAsset 是一次静态资源抓取的结果（Body 是解压后的原始字节）。
type StaticAsset struct {
	Path        string
	Status      int
	ContentType string
	Body        []byte
}

// WebFetchStatic 用当前会话 GET 教务站点上的前端脚本，供 WebUI 诊断用。
//
// 用途是"算哈希，看学校有没有换过 JS"：正方一改选课页的脚本，本工具的选择器
// 与参数推断就可能失效，拿到一串和本地快照不一样的哈希，比对着页面猜快得多。
// 因此只发一次请求——不重试、不重登、不缓存，重试会把"版本刚切换"这件事掩盖掉。
// path 必须是 config 里的站内端点常量，不接受上层随手拼的绝对 URL。
func (a *APIClient) WebFetchStatic(ctx context.Context, path string) (StaticAsset, error) {
	asset := StaticAsset{Path: path}
	if err := ctx.Err(); err != nil {
		return asset, err
	}
	if !strings.HasPrefix(path, "/") {
		return asset, fmt.Errorf("静态资源路径必须是站内绝对路径: %q", path)
	}
	resp, err := a.Http.R().SetContext(ctx).SetTimeout(10 * time.Second).SetRetryCount(0).Get(path)
	if err != nil {
		return asset, fmt.Errorf("抓取 %s: %w", path, err)
	}
	asset.Status = resp.StatusCode()
	asset.ContentType = strings.TrimSpace(resp.Header().Get("Content-Type"))
	body := resp.Bytes()

	if looksLikeScript(asset.ContentType, body) {
		asset.Body = body
		return asset, nil
	}
	if resp.IsStatusFailure() {
		return asset, fmt.Errorf("抓取 %s 失败: %s", path, resp.Status())
	}
	// 未登录时正方会把这个请求 302 到登录页（resty 默认跟随跳转），最终拿到的
	// 是一张 HTML。先嗅探内容再判登录态，避免把登录页的哈希当成脚本哈希展示。
	if !a.LoginCheck(resp) {
		return asset, ErrUnauthorized
	}
	return asset, fmt.Errorf("%s 返回的不是脚本（%s，%d 字节）", path, asset.ContentType, len(body))
}

// looksLikeScript 用 Content-Type 加正文前缀判断响应是不是一份 JS。
// 有的教务站点把 .js 当 text/plain 甚至 application/octet-stream 发，
// 所以不能只看类型。
func looksLikeScript(contentType string, body []byte) bool {
	ct := strings.ToLower(contentType)
	if strings.Contains(ct, "javascript") || strings.Contains(ct, "ecmascript") {
		return true
	}
	trimmed := strings.TrimLeft(string(body), " \t\r\n\ufeff")
	for _, prefix := range []string{
		"//", "/*", "\"use strict\"", "'use strict'",
		"var ", "let ", "const ", "function", "(function", "!function", "define(",
	} {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}
