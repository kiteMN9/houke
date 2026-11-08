package config

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// 默认值集中在这里。
//
// 以前 "43s" / "21s" / 默认 UA 分别写死在 config.initConfig、
// config.ReadConfigFromBytes、config.ReadConfig、web.Account.normalize、
// web.buildRuntime、web.createAccount 和 client.NewBasicClient 里，
// 改一个默认值要全局 grep；漏掉一处，同一份配置从不同入口读进来结果就不同。
//
// 约定：非测试代码里不允许再出现字面量 "43s" / "21s"，一律引用这里。
const (
	DefaultBaseURL      = "https://jwglxt.ycit.edu.cn/"
	DefaultTimeout      = 43 * time.Second
	DefaultHedgingDelay = 21 * time.Second
)

// Default 返回一份可以直接使用的配置（不含任何凭据）。
//
// 新建账号、测试、以及从稀疏 JSON 里恢复都从它出发：文件里缺哪个字段，
// 哪个字段就保持默认值——不需要第二份默认表，也不会出现"稀疏文件读到零值"。
func Default() Settings {
	return Settings{
		BaseURL:      DefaultBaseURL,
		UserAgent:    DefaultUA,
		Timeout:      Duration(DefaultTimeout),
		HedgingDelay: Duration(DefaultHedgingDelay),
	}
}

// Validate 检查配置有没有明显到"不该发起请求"的问题。
//
// web 的 buildRuntime 在构造 client 前会调用它，把配置错误变成一个
// 可以直接展示给用户的 500，而不是等请求失败后猜原因。
func (s Settings) Validate() error {
	if strings.TrimSpace(s.Account) == "" {
		return errors.New("account 不能为空")
	}
	if err := ValidateBaseURL(s.BaseURL); err != nil {
		return err
	}
	if !CheckUALegal(s.UserAgent) {
		return fmt.Errorf("ua 不合法: %q", s.UserAgent)
	}
	if s.Timeout <= 0 {
		return errors.New("timeout 必须大于 0")
	}
	// hedgingDelay 只在开启对冲时才会被用到，所以也只在开启时要求。
	if s.Hedging && s.HedgingDelay <= 0 {
		return errors.New("hedgingDelay 必须大于 0")
	}
	return nil
}
