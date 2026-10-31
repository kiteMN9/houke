package config

import "slices"

// Settings 是"用户配置"的运行时形态：由 web 从 Account 构造，client 只读。
//
// 它是值类型：client 拿到的是拷贝，改不到调用方那份；需要可变副本时先 Clone。
// 会话产物（JWT、学到的 route、修正过的地址）不在这里，见 Session。
//
// JSON tag 保留旧的字段名：Settings 仍然可以按旧 config.json 的形状
// 序列化 / 反序列化（迁移脚本、测试、以及"把配置贴给作者"这种场景）。
// 代码内部一律用 Go 字段名，不再有"字段名写错就静默丢值"的 JSON 往返。
type Settings struct {
	BaseURL      string   `json:"url"`
	Account      string   `json:"account"`
	Password     string   `json:"password,omitempty"`
	CasPassword  string   `json:"casPassword,omitempty"`
	UserAgent    string   `json:"ua"`
	Timeout      Duration `json:"timeout"`
	Want         string   `json:"want,omitempty"`
	Hedging      bool     `json:"hedging,omitempty"`
	HedgingDelay Duration `json:"hedgingDelay"`
	Routes       []string `json:"routes"` // 用户指定；不 omitempty，保证 [] 与 nil 可区分
}

// Clone 返回深拷贝：Routes 是唯一有引用语义的字段。
func (s Settings) Clone() Settings {
	s.Routes = slices.Clone(s.Routes)
	return s
}
