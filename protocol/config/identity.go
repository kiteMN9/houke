package config

import "strings"

// Identity 是一次登录所证明的身份。
//
// 它既不是用户配置，也不是会话产物。密码登录时身份就是配置里的 Account，
// 没什么可记的；微信扫码登录时顺序是反的——用户在扫之前什么都不用填，
// 唯一能知道"登的是谁"的时刻，是 ticket 到手之后。所以身份由 ticket 解析
// 出来，登录成功后交给上层落到账号上。
//
// 它必须比 Session 活得久：JWT 会过期，人不会。Session 被 Clear 掉之后，
// 身份仍然是这个账号之所以是"这个人的账号"的依据。
type Identity struct {
	// Sub 是学号 / 登录账号（ticket 外层 JWT 的 sub）。
	// 它也是绑定账号的键：同一个 sub 就是同一个人。
	Sub string `json:"sub"`
	// Name 是姓名（内层 idToken 的 ATTR_name），只用于展示。
	Name string `json:"name,omitempty"`
	// Type 是身份类型，如"学生""教师"（ATTR_identityTypeName）。
	Type string `json:"type,omitempty"`
	// Org 是学校名（ATTR_organizationName），只用于展示。
	Org string `json:"org,omitempty"`
	// OrgCode 是学校编码（organizationCode）。一个账号列表里可能同时有
	// 多所学校的账号，裸学号会撞，所以完整身份是 (OrgCode, Sub)。
	OrgCode string `json:"orgCode,omitempty"`
}

// Empty 报告身份是否未知。nil 与空身份都算未知：
// 调用方不需要先判空指针再判字段。
func (i *Identity) Empty() bool {
	return i == nil || strings.TrimSpace(i.Sub) == ""
}

// Clone 返回一份拷贝，避免调用方之间共享同一个可写指针。
func (i *Identity) Clone() *Identity {
	if i == nil {
		return nil
	}
	c := *i
	return &c
}

// Display 返回界面上的称呼：姓名优先，没有姓名时回落学号。
func (i *Identity) Display() string {
	if i == nil {
		return ""
	}
	if i.Name != "" {
		return i.Name
	}
	return i.Sub
}

// SamePerson 报告两个身份是否属于同一所学校里的同一个人。
// 缺学校编码的一侧视为"不知道"而不是"不同"：老账号没有记录编码时也得能认出来。
func (i *Identity) SamePerson(o *Identity) bool {
	if i.Empty() || o.Empty() {
		return false
	}
	if i.Sub != o.Sub {
		return false
	}
	return i.OrgCode == "" || o.OrgCode == "" || i.OrgCode == o.OrgCode
}
