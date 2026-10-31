package protocol

import "strings"

// =========================================================================
// 选课参数过期信号（正方叫“加密串错误”）
//
// 列表/详情接口依赖首页隐藏域里的 xkkz_id / xkkz_xh（选课控制号）。它被轮换
// 或过期后，教务会回：
//
//	{"msg":"加密串错误，可以清除浏览器缓存后刷新网页重试！","flag":"0"}
//
// 服务端给的解法就是“刷新网页”——重新 GET 选课首页，把隐藏域换成新值，
// 也就是再走一遍 GetIndexParams + GetCourseListPre。
//
// 这个信号以前只写进 cfg.State.needInit，而 engine 每个请求拿的是 cfg 值拷贝
// （见 engine/session.go 的 snapshot），写进去就丢了；CLI 的 Controller 因为
// 独占同一份 cfg 才没暴露这个问题。这里在 client 上再记一份：client 是每个
// 账号唯一、跨请求共享的对象，谁持有它谁就能看见。
//
// 约定与 loginState.go 一致：谁发现状态变化，谁在这里记账；上层只读/取用，
// 不要再各存一份。
// =========================================================================

// MarkParamRefresh 标记“下一次请求前需要重新抓选课参数”。
// 并发安全；重复标记无副作用。
func (a *APIClient) MarkParamRefresh() {
	if a == nil {
		return
	}
	a.paramRefresh.Store(true)
}

// TakeParamRefresh 取走并清除标记，返回之前是否被标记过。
//
// 用 Swap 而不是 Load：多个并发请求同时撞上参数过期时，只让第一个去重抓首页，
// 其余的拿到 false 直接返回，等下一次请求自然用上新参数。
func (a *APIClient) TakeParamRefresh() bool {
	if a == nil {
		return false
	}
	return a.paramRefresh.Swap(false)
}

// paramRefreshHints 是教务提示“参数过期、请刷新页面”时出现过的文案。
// 只放确定属于这一类问题的：像“人数已满”“选课频率过高”都有别的处理方式，
// 混进来会把正常失败也当成参数过期，白白多抓一次首页。
var paramRefreshHints = []string{
	"加密串错误",  // 正方 9.0 原文：“加密串错误，可以清除浏览器缓存后刷新网页重试！”
	"刷新网页重试", // 同一句的后半段，不同版本前缀不一样
}

// NeedsParamRefresh 判断一段接口文案是不是“参数过期、请刷新页面”。
//
// 选课接口没有 needInit 那样的隐式通道，只能看返回的 msg；调用方拿它决定
// 要不要重抓参数后重试。空串一律返回 false。
func NeedsParamRefresh(msg string) bool {
	if msg == "" {
		return false
	}
	for _, hint := range paramRefreshHints {
		if strings.Contains(msg, hint) {
			return true
		}
	}
	return false
}
