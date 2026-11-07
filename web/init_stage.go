package web

import (
	"school_sdk/protocol"
)

// initStageText 把 protocol 的初始化阶段翻译成给用户看的一句话。
//
// 和 loginStageText 一样：未知阶段返回空串，不覆盖已有文案。
func initStageText(st protocol.InitStage) string {
	switch st.Phase {
	case protocol.InitPhaseIndex:
		return "正在获取选课首页参数"
	case protocol.InitPhaseIndexWait:
		return "等待选课阶段开放"
	case protocol.InitPhaseIndexParse:
		return "正在解析选课页签"
	case protocol.InitPhaseTabParams:
		return "正在获取课程列表参数"
	case protocol.InitPhaseTabParamsParse:
		return "正在解析选课规则"
	default:
		return ""
	}
}
