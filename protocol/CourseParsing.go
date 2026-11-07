package protocol

import (
	"fmt"
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

func getXpathValue(docNode *html.Node, name string) string {
	if docNode == nil || name == "" {
		return ""
	}
	escapedName := html.EscapeString(name)
	// 使用单个XPath同时匹配name或id
	xpath := `//*[@id="` + escapedName + `" or @name="` + escapedName + `"]`
	if node := htmlquery.FindOne(docNode, xpath); node != nil {
		return htmlquery.SelectAttr(node, "value")
	}
	return ""
}

func parseYzbIndexHtml(cfg *APIConfig, docNode *html.Node) bool {
	cfg.Index.xkkz_id = getXpathValue(docNode, "firstXkkzId")
	cfg.Index.xkkz_xh = getXpathValue(docNode, "firstXkkzXh")
	cfg.Index.kklxdm = getXpathValue(docNode, "firstKklxdm")
	cfg.Tabs.Kklxmc = getXpathValue(docNode, "firstKklxmc")
	cfg.Index.njdm_id = getXpathValue(docNode, "firstNjdmId")
	cfg.Index.zyh_id = getXpathValue(docNode, "firstZyhId")
	//cfg.Index.njdm_id_list0 = cfg.Index.njdm_id
	cfg.Index.bh_id = getXpathValue(docNode, "bh_id")   // 班号
	cfg.Index.xkxnm = getXpathValue(docNode, "xkxnm")   // 学年
	cfg.Index.xkxqm = getXpathValue(docNode, "xkxqm")   // 学期
	cfg.Index.xqh_id = getXpathValue(docNode, "xqh_id") // 校区号
	cfg.Index.jg_id = getXpathValue(docNode, "jg_id_1")
	cfg.Index.xz = getXpathValue(docNode, "xz") // 学制
	cfg.Index.zyfx_id = getXpathValue(docNode, "zyfx_id")
	cfg.Index.ccdm = getXpathValue(docNode, "ccdm")
	cfg.Index.xbm = getXpathValue(docNode, "xbm")   // 性别码 男1 女2
	cfg.Index.mzm = getXpathValue(docNode, "mzm")   // 民族码
	cfg.Index.xsbj = getXpathValue(docNode, "xsbj") // 学生标记
	cfg.Index.xslbdm = getXpathValue(docNode, "xslbdm")
	cfg.Index.xszxzt = getXpathValue(docNode, "xszxzt")
	cfg.Index.xxdm = getXpathValue(docNode, "xxdm") // 学校代码
	cfg.Window.zxfs = getXpathValue(docNode, "zxfs")
	cfg.Index.tkzgcs_qt = getXpathValue(docNode, "tkzgcs_qt")
	cfg.Index.currentsj = getXpathValue(docNode, "currentsj") // 当前时间

	// 解析总学分最低
	minCreditNode := htmlquery.FindOne(docNode, `//h5[contains(., "总学分最低")]/font[@color='red'][1]`)
	if minCreditNode != nil {
		cfg.Window.minCredit = htmlquery.InnerText(minCreditNode)
	}
	// 解析总学分最高
	maxCreditNode := htmlquery.FindOne(docNode, `//h5[contains(., "总学分最低")]/font[@color='red'][2]`)
	if maxCreditNode != nil {
		cfg.Window.maxCredit = htmlquery.InnerText(maxCreditNode)
	}
	// 解析本学期已选学分
	selectedCreditNode := htmlquery.FindOne(docNode, `//font[@id='yxxfs']`)
	if selectedCreditNode != nil {
		cfg.Window.selectedCredit = htmlquery.InnerText(selectedCreditNode)
	}
	if cfg.Index.xkkz_id == "" && cfg.Index.kklxdm == "" && cfg.Index.xkkz_xh == "" && cfg.Index.xkxnm == "" {
		fmt.Println("❌ Step 1 index failed")
		return true
	}
	if cfg.Index.xkkz_xh != "" {
		fmt.Println("正方 V9")
	}
	fmt.Println("Step 1 index finished")
	fmt.Println("\n\r将要选 \033[1;36m", cfg.Tabs.Kklxmc, "\033[0m !!")
	log.Println("将要选", cfg.Tabs.Kklxmc, "!!")
	parseKklxdmXkkz__(cfg, docNode)
	cfg.Tabs.modeName = cfg.Tabs.Kklxmc
	return false
}

// 处理页签页
func parseTabParamsHtml(cfg *APIConfig, docNode *html.Node) {
	cfg.Window.bklx_id = getXpathValue(docNode, "bklx_id")
	cfg.Window.rwlx = getXpathValue(docNode, "rwlx")
	cfg.Window.xkly = getXpathValue(docNode, "xkly")
	cfg.Window.xklc = getXpathValue(docNode, "xklc")     // 选课轮次
	cfg.Window.xklcmc = getXpathValue(docNode, "xklcmc") // 选课轮次
	cfg.Window.sfkknj = getXpathValue(docNode, "sfkknj")
	cfg.Window.rlkz = getXpathValue(docNode, "rlkz")
	cfg.Window.cdrlkz = getXpathValue(docNode, "cdrlkz")
	cfg.Window.kkbk = getXpathValue(docNode, "kkbk")
	cfg.Window.kkbkdj = getXpathValue(docNode, "kkbkdj")
	cfg.Window.jxbzcxskg = getXpathValue(docNode, "jxbzcxskg")
	cfg.Window.xkxskcgskg = getXpathValue(docNode, "xkxskcgskg")
	cfg.Window.sfkcfx = getXpathValue(docNode, "sfkcfx")
	cfg.Window.sfkkjyxdxnxq = getXpathValue(docNode, "sfkkjyxdxnxq")
	cfg.Window.gnjkxdnj = getXpathValue(docNode, "gnjkxdnj")
	cfg.Window.sfkkzy = getXpathValue(docNode, "sfkkzy")
	cfg.Window.kzybkxy = getXpathValue(docNode, "kzybkxy")
	cfg.Window.sfznkx = getXpathValue(docNode, "sfznkx")
	cfg.Window.zdkxms = getXpathValue(docNode, "zdkxms")
	cfg.Window.sfkxq = getXpathValue(docNode, "sfkxq")
	cfg.Window.bbhzxjxb = getXpathValue(docNode, "bbhzxjxb")
	cfg.Window.rlzlkz = getXpathValue(docNode, "rlzlkz")
	cfg.Window.sfkxk = getXpathValue(docNode, "sfkxk")   // 是否可选课
	cfg.Window.sfktk = getXpathValue(docNode, "sfktk")   // 是否可退课
	cfg.Window.jdlx = getXpathValue(docNode, "jdlx")     // 体育课多志愿开关
	cfg.Window.syts = getXpathValue(docNode, "syts")     // 距选课结束还剩{0}天
	cfg.Window.syxs = getXpathValue(docNode, "syxs")     // 距选课结束还剩{0}小时
	cfg.Window.xkkssj = getXpathValue(docNode, "xkkssj") // 选课开始时间
	cfg.Window.xkjssj = getXpathValue(docNode, "xkjssj") // 选课结束时间
	if cfg.Window.bklx_id == "" && cfg.Window.rwlx == "" && cfg.Window.xkly == "" && cfg.Window.syxs == "" {
		fmt.Println("❌ Step 2 tab params failed")
		return
	}
	fmt.Println("Step 2 tab params finished")
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
