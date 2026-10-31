package protocol

import (
	"log"
	"strings"
	"time"

	"github.com/antchfx/htmlquery"
	"golang.org/x/net/html"
)

// CheckTime 对比教务系统返回的时间与本地时间，偏差过大时记一条日志。
// 时钟偏差会影响选课定时，但不值得中断一次已经成功的请求。
func CheckTime(timeStr string, reqAt time.Time) {
	//时间检测
	if timeStr == "" || len(timeStr) < 6 {
		return
	}
	systemTime := time.Now()
	// 日期格式
	layout := "2006-01-02 15:04:05"
	// 解析日期字符串为time.Time对象
	loc, _ := time.LoadLocation("Local")
	timeObj, err := time.ParseInLocation(layout, timeStr, loc)
	if err != nil {
		return
	}
	diff := timeObj.Sub(reqAt)
	deviation := reqAt.Sub(systemTime).Abs()
	if diff.Abs() > 5*time.Second && diff.Abs() < 24*time.Hour {
		log.Println("当前时间和教务系统时间差:", diff, "误差:", deviation)
	}
}

// parseKklxdmXkkz__ 解析 zzxkYzb.js 里的选课控制菜单（queryCourse(...)），
// 把每个菜单项的 kklxdm / xkkz_id / njdm_id / zyh_id / xkkz_xh 存进 cfg.Tabs.modeStore。
func parseKklxdmXkkz__(cfg *APIConfig, docNode *html.Node) {
	nodes := htmlquery.Find(docNode, `*//ul/li/a`)
	if len(nodes) != 0 {
		cfg.Tabs.modeStore = nil
	}
	for _, item := range nodes {
		// 提取名称
		nameNode := htmlquery.FindOne(item, "./text()")
		if nameNode == nil {
			continue
		}

		// 提取 onclick 属性
		onclick := htmlquery.SelectAttr(item, "onclick")
		if onclick == "" {
			continue
		}

		// 处理 onclick 字符串
		tmp := strings.TrimPrefix(onclick, "queryCourse(this,'")
		parts := strings.Split(tmp, "','")
		if len(parts) < 3 {
			continue
		}
		var store ModeStore
		store.Kklxmc = nameNode.Data
		store.Kklxdm = parts[0]
		store.Xkkz_id = parts[1]
		store.Njdm_id = parts[2]
		if len(parts) == 4 {
			store.Zyh_id = strings.TrimSuffix(parts[3], "')")
		}
		if len(parts) == 5 {
			store.Zyh_id = parts[3]
			store.Xkkz_xh = strings.TrimSuffix(parts[4], "')")
		}
		if len(parts) >= 6 {
			log.Println("⚠️ xkkz unsport")
		}
		cfg.Tabs.modeStore = append(cfg.Tabs.modeStore, store)
	}
}

// removePrefix 去掉单个字符串中的 "数字/" 前缀
func removePrefix(s string) string {
	_, after, ok := strings.Cut(s, "/")
	if !ok {
		// 没有斜杠，返回原字符串（或根据需求处理）
		return s
	}
	// 返回第一个斜杠之后的部分
	return after
}

// processLine 处理可能包含多个条目的行（分号分隔）
func processLine(line string) string {
	parts := strings.Split(line, ";")
	for i, part := range parts {
		parts[i] = removePrefix(part)
	}
	return strings.Join(parts, ";")
}
