package config

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Duration 让配置里继续写 "43s"，但代码里拿到的是 time.Duration。
//
// 以前 Timeout / HedgingDelay 是 string，每个用到的地方各写一遍
// time.ParseDuration，错误处理也各不相同（有的被 `_` 吞掉，有的回落硬编码值）。
// 现在解析只发生在两个地方：读文件（UnmarshalJSON）和 web 的表单归一化，
// 其余代码拿到的一定是合法正数。
type Duration time.Duration

// Std 返回标准库的 time.Duration。
func (d Duration) Std() time.Duration { return time.Duration(d) }

// String 输出带单位的字符串（"43s"、"10m0s"），可以直接给 time.ParseDuration。
func (d Duration) String() string { return time.Duration(d).String() }

// MarshalJSON 写成字符串，保持和旧配置文件一致。
func (d Duration) MarshalJSON() ([]byte, error) {
	return []byte(strconv.Quote(time.Duration(d).String())), nil
}

// UnmarshalJSON 接受 `"43s"` 这类字符串；数字或非法时长都报错。
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return errors.New(`时长必须是字符串，如 "43s"`)
	}
	v, err := ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// ParseDuration 解析用户输入里的时长字符串（如 "43s"、"10m"）。
//
// 空串、非法格式、非正数都返回 error；回落默认值还是向上报错由调用方决定。
// 集中在这里的目的是让所有调用点对"什么算合法时长"有同一个答案。
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("时长为空")
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("非法时长 %q: %w", s, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("时长必须大于 0: %q", s)
	}
	return d, nil
}
