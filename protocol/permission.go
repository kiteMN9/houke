package protocol

import "strings"

// isNoPermission 判断响应体是不是教务系统的“无功能权限”提示页。
//
// 学校的权限拒绝是 HTTP 200 + 一张 HTML 错误页（标题为“无功能权限”），
// 既不是 JSON 数据，也不是登录页，所以只能从正文里认这个提示语。
// user_info.go 里早就有同样的判断，这里收成一个函数，成绩、已选等
// 接口共用。
func isNoPermission(body string) bool {
	return strings.Contains(body, "无功能权限")
}
