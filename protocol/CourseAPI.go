package protocol

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log"
	"regexp"
	"school_sdk/check_code"
	baseCfg "school_sdk/protocol/config"
	"school_sdk/utils"
	"strings"
	"time"

	"github.com/antchfx/htmlquery"
)

// IndexOptions 控制一次 index 参数抓取的行为。
type IndexOptions struct {
	// Save 为 true 时把原始页面写到工作目录，供调试/对比。
	Save bool

	// WaitForOpen 为 true 时，当前不在选课阶段就每秒轮询等待；
	// 为 false 时立刻返回 StageError，把原因交给调用方。
	// Web 的 /init 需要尽快把"还没到选课时间"反馈给前端，而不是挂住请求。
	WaitForOpen bool
}

// GetIndexParams 抓取选课首页参数，成功返回 nil。
func (a *APIClient) GetIndexParams(ctx context.Context, cfg *APIConfig, opts IndexOptions) error {
	log.Println("============GetIndexParams()============")
	//fmt.Println("Ctrl+C 退出")
	needEnter := false
	i := 0
	// 阶段上报：WebUI 不必等 hook 拿 URL 去猜（见 init_stage.go）。
	a.resetInitStage()
	for {
		if i > 50 {
			return stagef("index", "请求次数达到设定阈值")
		}
		// ★ 每次循环先看 ctx
		select {
		case <-ctx.Done():
			log.Println("GetIndexParams: ctx 已取消，退出")
			return &StageError{Op: "index", Message: "ctx 已取消，退出", Err: ctx.Err()}
		default:
		}

		i++
		// 等待开放期间不再重复报 index，否则会与 index-wait 每秒交替刷屏。
		if !needEnter {
			a.reportInitStage(InitPhaseIndex, "")
		}
		reqAt := time.Now()
		resp, err := a.hedgeC.R().
			SetContext(ctx).
			SetQueryParam("gnmkdm", baseCfg.Gndmmk_xsxk).
			SetFormData(map[string]string{
				"gnmkdm":  baseCfg.Gndmmk_xsxk, // 这个接口能post
				"czdmKey": "00",
			}).
			Get(baseCfg.ChooseCourseIndex)

		if err != nil {
			// ctx 已经结束时重试没有意义：直接返回，别再空转 50 轮。
			if ctxErr := ctx.Err(); ctxErr != nil {
				log.Println("index 请求已中止:", ctxErr)
				return &StageError{Op: "index", Message: "index 请求已中止", Err: ctxErr}
			}
			// 请求级超时/取消时 resp 可能为 nil，取 Duration 会 panic。
			if errors.Is(err, context.DeadlineExceeded) {
				if resp != nil {
					fmt.Println("index 请求超时:", resp.Duration(), err)
					log.Println("index 请求超时:", resp.Duration(), err)
				} else {
					fmt.Println("index 请求超时:", err)
					log.Println("index 请求超时:", err)
				}
			} else {
				fmt.Println("index请求出错:", err)
				log.Println("index请求出错:", err)
				time.Sleep(time.Millisecond * 1500)
			}
			continue
		}

		if !a.CheckLogout302(resp) && utils.UserIsLogin(a.cfg.Account, resp.String()) {
		} else {
			a.markSessionExpired(resp)
			return &StageError{Op: "index", Message: "登录已失效", Err: ErrUnauthorized}
		}
		if resp.IsStatusFailure() {
			fmt.Println("GetIndexParams:", resp.Status())
			continue
		}
		if len(resp.Bytes()) == 0 {
			log.Println("GetIndexParams len(resp.Bytes()) == 0", resp.Status())
			fmt.Println("GetIndexParams len(resp.Bytes()) == 0", resp.Status())
			time.Sleep(time.Millisecond * 1500)
			continue
		}
		docNode, err1 := htmlquery.Parse(bytes.NewReader(resp.Bytes()))
		//docNode, err1 := htmlquery.Parse(resp.Body)
		if err1 != nil {
			log.Println(err1)
			fmt.Println("\r完成请求然后解析出错？")
			log.Println("完成请求然后解析出错？", err1)
			continue
		}
		if getXpathValue(docNode, "iskxk") == "0" {
			// 当前不属于选课阶段
			statNode := htmlquery.Find(docNode, `//div[@class="nodata"]/span/text()`)
			if len(statNode) != 0 {
				jdStr := strings.TrimSpace(htmlquery.InnerText(statNode[0]))
				// Sorry, it is not in the elective stage at present. If necessary, please contact the administrator.
				// 对不起，当前不属于选课阶段，如有需要，请与管理员联系！
				re := regexp.MustCompile(`ver=(\d+)`)
				match := re.FindStringSubmatch(resp.String())
				ver := ""
				if len(match) > 1 {
					ver = match[1]
				} else {
					log.Println("未找到 ver=数字")
				}
				if !opts.WaitForOpen {
					return stagef("index", "%d %s ver=%s", i, jdStr, ver)
				}
				a.reportInitStage(InitPhaseIndexWait, jdStr)
				fmt.Printf("\r%d %s ver=%s", i, jdStr, ver)
				log.Printf("%d %s ver=%s", i, jdStr, ver)
				needEnter = true
				// ★ 关键：把 time.Sleep 换成可被 ctx 打断的睡眠
				timer := time.NewTimer(time.Second)
				select {
				case <-ctx.Done():
					timer.Stop()
					log.Println("GetIndexParams: 等待选课阶段中被取消")
					return &StageError{Op: "index", Message: "等待选课阶段中被取消", Err: ctx.Err()}
				case <-timer.C:
				}
			}
			continue
		}

		if needEnter {
			fmt.Println()
			needEnter = false
		}
		a.reportInitStage(InitPhaseIndexParse, "")
		if parseYzbIndexHtml(cfg, docNode) {
			log.Println("GetIndexParams:", resp.Status(), resp.String())
			continue
		}
		CheckTime(cfg.Index.currentsj, reqAt)
		htmlContent := utils.RemoveEmptyLines(resp.String())
		if !opts.Save {
			log.Println(htmlContent)
		}
		a.Name = getXpathValue(docNode, "xm")
		if opts.Save {
			check_code.SaveFile("./", "zzxkyzb_cxZzxkYzbIndex.html", resp.Bytes())
		}
		return nil
	}
}

// GetCourseTabParams 页签获取
func (a *APIClient) GetCourseTabParams(ctx context.Context, cfg *APIConfig, save bool) error {
	// 补齐搜索课程需要的发包参数
	log.Println("===============getCourseList_pre()=================")
	formData := map[string]string{
		"xszxzt": cfg.Index.xszxzt, // 1
		"kspage": "0",
		"jspage": "0",
	}
	if cfg.Index.xkkz_id != "" {
		formData["xkkz_id"] = cfg.Index.xkkz_id
	}
	if cfg.Index.xkkz_xh != "" {
		formData["xkkz_xh"] = cfg.Index.xkkz_xh
	}
	for {
		select {
		case <-ctx.Done():
			log.Println("getCourseListPre: ctx 已取消，退出")
			return ctx.Err()
		default:
		}
		a.reportInitStage(InitPhaseListPre, "")

		resp, err := a.hedgeC.R().
			SetContext(ctx).
			SetQueryParam("gnmkdm", baseCfg.Gndmmk_xsxk).
			SetFormData(formData).
			Post(baseCfg.ChooseCourseListPre)

		if err != nil {
			// 判断是否因Context取消导致的错误
			if errors.Is(err, context.Canceled) {
				fmt.Println("YzbDisplay 请求已取消")
				return ctx.Err()
			}
			if errors.Is(err, context.DeadlineExceeded) {
				if resp != nil {
					fmt.Println("YzbDisplay 请求超时:", resp.Duration())
					log.Println("YzbDisplay 请求超时:", resp.Duration(), err)
				}
			} else {
				fmt.Println("YzbDisplay 请求出错:", err)
				log.Println("YzbDisplay 请求出错:", err)
				time.Sleep(time.Millisecond * 500)
			}
			continue
		}
		if resp.IsStatusFailure() {
			// 这里容易有 404 问题和
			// 200 错误提示 系统运行异常，请稍后再试 问题
			fmt.Println("ListPre:", resp.Status(), resp.String())
			continue
		}
		if a.LoginCheck(resp) {
		} else {
			return ErrUnauthorized
		}
		if resp.IsStatusSuccess() {
			docNode, err1 := htmlquery.Parse(bytes.NewReader(resp.Bytes()))
			if err1 != nil {
				log.Println("htmlquery:", err1)
				fmt.Println("完成请求然后解析出错？")
				continue
			}
			a.reportInitStage(InitPhaseListPreParse, "")
			parseListPreHtml(cfg, docNode)
			htmlContent := utils.RemoveEmptyLines(resp.String())
			if !save {
				log.Println(htmlContent)
			}
			if save {
				check_code.SaveFile("./", "zzxkyzb_cxZzxkYzbDisplay.html", resp.Bytes())
			}
			return nil
		}
		fmt.Println("ListPre:", resp.Status(), resp.String())
	}
	// zdzys //"一门课程最多可选"+zdzys+"个志愿！"
	// sfqzxk //"一门课程只能选一个教学班！"
	// self.lnzgxkxf # 历学期选课最高学分要求为
	// bxqzgxkxf //基本选课规则设置中设置的最高选课学分
	// 本学期本类型课程选课最高学分要求为"+bxqzgxkxf+"，当前本学期本类型课程选课总学分为("+kklxzxfs+"+"+$("#xf_"+kch_id).text()+"="+c_kklxzxfs+")，超出选课最高学分要求，不可选！"
	// 本学期本类型课程选课最高门次要求为 bxqzgxkmc ，不可选
}

// GetCourseList 拿到课程号全集
func (a *APIClient) GetCourseList(ctx context.Context, cfg *APIConfig) ([]CourseListDic, error) {
	// 搜索课程，主页面的查询
	if !cfg.State.listDump {
		log.Println("========搜索课程 getCourseList()========")
		fmt.Println("搜索课程")
	}
	var result CourseListResult
	for {
		formData := map[string]string{ // 25
			"bbhzxjxb":     cfg.Window.bbhzxjxb,
			"bh_id":        cfg.Index.bh_id,
			"bklx_id":      cfg.Window.bklx_id,
			"rwlx":         cfg.Window.rwlx, // 校选是2 专选是1 没有这两个会蹦出来选不了的课，主修课：✓ 选修课：✗
			"xkly":         cfg.Window.xkly, // 1 选择无限制是0，主修课：✓ 选修课：✗
			"sfkkjyxdxnxq": cfg.Window.sfkkjyxdxnxq,
			"xqh_id":       cfg.Index.xqh_id,
			"jg_id":        cfg.Index.jg_id,
			"zyh_id":       cfg.Index.zyh_id,
			//"zyh_id_xs":    cfg.Index.zyh_id,
			"njdm_id": cfg.Index.njdm_id,
			//"njdm_id_xs":   cfg.Index.njdm_id,
			// bjgkczxbbjwcx: 0
			"xbm": cfg.Index.xbm,
			//"xslbdm":   cfg.Index.xslbdm,
			"mzm":      cfg.Index.mzm,
			"xz":       cfg.Index.xz,
			"ccdm":     cfg.Index.ccdm,
			"xsbj":     cfg.Index.xsbj,
			"sfkknj":   cfg.Window.sfkknj,
			"gnjkxdnj": cfg.Window.gnjkxdnj,
			"sfkkzy":   cfg.Window.sfkkzy,
			"kzybkxy":  cfg.Window.kzybkxy,
			"sfznkx":   cfg.Window.sfznkx,
			"zdkxms":   cfg.Window.zdkxms,
			"sfkxq":    cfg.Window.sfkxq,
			"sfkcfx":   cfg.Window.sfkcfx,
			"kkbk":     cfg.Window.kkbk,
			"kkbkdj":   cfg.Window.kkbkdj,
			// 	sfkgbcx: 0
			// 	sfrxtgkcxd: 0
			// 	tykczgxdcs: 0
			"xkxnm":  cfg.Index.xkxnm,  // 当前学期年份, 如2021-2022 即2021，必须
			"xkxqm":  cfg.Index.xkxqm,  // 3 12 16
			"kklxdm": cfg.Index.kklxdm, // 01为主修课 10为选修课，校选10 专选01，英语进阶06，必须
			"rlkz":   cfg.Window.rlkz,
			// 	xkzgbj: 0
			"kspage": "1",
			"jspage": "222", // 页号，一页显示的数量，必须
			//"jxbzb":  "",
		}
		if cfg.Index.xkkz_xh != "" {
			formData["xkkz_xh"] = cfg.Index.xkkz_xh // 有xh可以没有id
		}
		if cfg.Index.xkkz_id != "" {
			formData["xkkz_id"] = cfg.Index.xkkz_id
		}
		if cfg.Index.njdm_id_list0 != "" {
			formData["njdm_id_list[0]"] = cfg.Index.njdm_id_list0 // 这个就是选课的时候筛选的条件，建议只填个年级就好了
		}
		if cfg.State.yl {
			formData["yl_list[0]"] = "1"
		}
		requ := a.hedgeC.R().
			SetContext(ctx).
			SetQueryParams(map[string]string{
				"gnmkdm": baseCfg.Gndmmk_xsxk,
			}).
			SetFormData(formData)

		resp, err := requ.Post(baseCfg.ChooseCourseCourseList)
		if err != nil {
			// 判断是否因Context取消导致的错误
			if errors.Is(err, context.Canceled) {
				log.Println("PartD 请求已取消")
				return nil, ctx.Err()
			}
			if errors.Is(err, context.DeadlineExceeded) {
				if resp != nil {
					fmt.Println("PartD 请求超时", resp.Duration())
					log.Println("PartD 请求超时", resp.Duration())
				}
				continue
			}

			fmt.Println("PartD 请求发生错误:", err)
			log.Println("PartD 请求发生错误:", err, resp.String())
			time.Sleep(370 * time.Millisecond)
			continue
		}
		if resp.IsStatusFailure() {
			log.Println(resp.Status(), resp.String())
			time.Sleep(1 * time.Second)
			continue
		}
		if a.LoginCheck(resp) {
		} else {
			return nil, ErrUnauthorized
		}
		if resp.IsStatusSuccess() {
			if err := json.Unmarshal(resp.Bytes(), &result); err != nil {
				if resp.String() == `"0"` || resp.String() == `"1"` {
					fmt.Println("搜索课程失败 msg:", resp.String())
					log.Println("搜索课程失败 msg:", resp.String())
					cfg.State.needInit = true
					a.MarkParamRefresh()
					return nil, nil
				}
				fmt.Println(err)
				log.Println(err, resp.String())
			}
			if !cfg.State.listDump {
				log.Println(resp.String())
				cfg.State.listDump = true
			}
			if result.TmpList != nil {
				return result.TmpList, nil
			}
			// {"msg":"加密串错误，可以清除浏览器缓存后刷新网页重试！","flag":"0"}
			if result.Msg != "" || result.Flag != "" {
				log.Println(resp.String())
				fmt.Println(resp.String())
				cfg.State.needInit = true
				// msg 可能是“加密串错误”这类参数过期，也可能只是“还没开始选课”；
				// 两种情况重抓一次首页参数都不亏，代价由调用方的冷却兜住。
				a.MarkParamRefresh()
				return result.TmpList, nil
			}
			fmt.Println("课程列表为空", len(result.TmpList))
			time.Sleep(1 * time.Second)
			continue
		}

	}
}

// GetCourseDetail 根据课程号查询该课程号下的所有教学班补齐剩余参数
func (a *APIClient) GetCourseDetail(ctx context.Context, cfg *APIConfig, kch_id string) ([]CourseDetail, error) {
	// 	查询课程具体信息
	// 	获得do_jxb_id（选课的必要参数）以及很重要的容量信息等等
	if !cfg.State.detailDump {
		fmt.Println("\r正在获取详细信息")
		log.Println("========查询课程具体信息 GetCourseDetail()========")
	}
	type MsgFlag struct {
		Msg  string `json:"msg"`
		Flag string `json:"flag"`
	}
	var result []CourseDetail
	for {
		formData := map[string]string{
			"rwlx":         cfg.Window.rwlx, // 校选是2 专选是1 没有这两个会蹦出来选不了的课，主修课：✓ 选修课：✗
			"xkly":         cfg.Window.xkly, // 1 选择无限制是0，主修课：✓ 选修课：✗
			"bklx_id":      cfg.Window.bklx_id,
			"sfkkjyxdxnxq": cfg.Window.sfkkjyxdxnxq,

			"xqh_id": cfg.Index.xqh_id,
			"jg_id":  cfg.Index.jg_id,
			"zyh_id": cfg.Index.zyh_id,
			//"zyh_id_xs":  cfg.Index.zyh_id,
			"njdm_id": cfg.Index.njdm_id,
			//"njdm_id_xs": cfg.Index.njdm_id,
			"bh_id": cfg.Index.bh_id,
			// bjgkczxbbjwcx: 0
			"xbm": cfg.Index.xbm,
			//"xslbdm":   cfg.Index.xslbdm,
			"mzm":      cfg.Index.mzm,
			"xz":       cfg.Index.xz,
			"ccdm":     cfg.Index.ccdm,
			"xsbj":     cfg.Index.xsbj,
			"sfkknj":   cfg.Window.sfkknj,
			"gnjkxdnj": cfg.Window.gnjkxdnj,
			"sfkkzy":   cfg.Window.sfkkzy,
			"kzybkxy":  cfg.Window.kzybkxy,
			"sfznkx":   cfg.Window.sfznkx,
			"zdkxms":   cfg.Window.zdkxms,
			"sfkxq":    cfg.Window.sfkxq,
			"sfkcfx":   cfg.Window.sfkcfx,
			"bbhzxjxb": cfg.Window.bbhzxjxb,
			"kkbk":     cfg.Window.kkbk,
			"kkbkdj":   cfg.Window.kkbkdj,

			// 	sfkgbcx: 0
			// 	sfrxtgkcxd: 0
			// 	tykczgxdcs: 0
			"xkxnm": cfg.Index.xkxnm, // 当前学期年份, 如2021-2022 即2021，必须
			"xkxqm": cfg.Index.xkxqm, // 3 12 16

			"rlkz":   cfg.Window.rlkz,
			"kklxdm": cfg.Index.kklxdm, // 01为主修课 10为选修课，校选10 专选01，英语进阶06，必须

			// 	xkzgbj: 0
			"kch_id": kch_id, // 课程号，必须
			"xklc":   cfg.Window.xklc,
		}
		if cfg.Index.xkkz_id != "" {
			formData["xkkz_id"] = cfg.Index.xkkz_id
		} else {
			formData["xkkz_xh"] = cfg.Index.xkkz_xh // 有id可以没有xh
		}
		//if cfg.Index.xkkz_xh != "" {
		//	formData["xkkz_xh"] = cfg.Index.xkkz_xh
		//}
		if cfg.Index.njdm_id_list0 != "" {
			formData["njdm_id_list[0]"] = cfg.Index.njdm_id_list0 // 这个就是选课的时候筛选的条件，建议只填个年级就好了
		}
		if cfg.State.yl {
			formData["yl_list[0]"] = "1"
		}
		requ := a.hedgeC.R().
			SetContext(ctx).
			SetQueryParams(map[string]string{
				"gnmkdm": baseCfg.Gndmmk_xsxk,
			}).
			SetFormData(formData)

		if !a.waitLogin(ctx) {
			return result, ctx.Err()
		}
		resp, err := requ.Post(baseCfg.ChooseCourseCourseDetail)
		if err != nil {
			// 判断是否因Context取消导致的错误
			if errors.Is(err, context.Canceled) {
				fmt.Println("WithKch 请求已取消")
				return result, ctx.Err()
			}
			if errors.Is(err, context.DeadlineExceeded) {
				if resp != nil {
					fmt.Println("WithKch 请求超时", resp.Duration())
				}
				continue
			}
			fmt.Println("WithKch 请求发生错误:", err)
			log.Println("WithKch 请求发生错误:", err, resp.String())
			time.Sleep(370 * time.Millisecond)
			continue
		}
		if resp.IsStatusFailure() {
			log.Println(resp.Status(), resp.String())
			time.Sleep(1 * time.Second)
			continue
		}
		if a.LoginCheck(resp) {
			if !cfg.State.detailDump {
				log.Println(resp.String())
				cfg.State.detailDump = true
			}
			if result != nil {
				return result, nil
			}
		} else {
			return result, ErrUnauthorized
		}
		if resp.IsStatusSuccess() {
			if err := json.Unmarshal(resp.Bytes(), &result); err != nil {
				if resp.String() == `"0"` {
					fmt.Println(`"0"，未查询到信息，可能没到选课时间，可能程序编写错误，也可能教务系统临时调整了选课`)
					log.Println(`"0"，未查询到信息，可能没到选课时间，可能程序编写错误，也可能教务系统临时调整了选课`)
					cfg.State.needInit = true
					a.MarkParamRefresh()
					return nil, nil
				}
				fmt.Println(err, resp.String())
				log.Println(err, resp.String())
				var mf MsgFlag
				if err := json.Unmarshal(resp.Bytes(), &mf); err != nil {
					cfg.State.needInit = true
					a.MarkParamRefresh()
					log.Println("GetCourseDetail:", resp.String())
					fmt.Println(resp.String())
					return nil, nil
				}
				// 详情接口的“加密串错误”能解析成 MsgFlag，不会走上面的 needInit 分支；
				// 这里补一刀，否则参数一过期，自动选课的详情刷新会一直拿到空结果。
				if NeedsParamRefresh(mf.Msg) {
					a.MarkParamRefresh()
				}
				log.Println("GetCourseDetail?:", resp.String())
				fmt.Println(resp.String())
				return nil, nil
			}
		}

	}
}

func (a *APIClient) ChooseCourse(ctx context.Context, idx IndexInfo, win SelectionWindow, co *CustomCourseDic) (ChooseCourseResult, error) {
	// 	选课
	// 	若flag==1则表示选课成功
	var sxbj = "0"
	if win.rlkz == "1" || win.rlzlkz == "1" || win.cdrlkz == "1" {
		sxbj = "1"
	}
	var result = ChooseCourseResult{Flag: "-5"}
	for range 5 {
		formData := map[string]string{
			// "bklx_id": idx.bklx_id,  // 英语进阶，这一个能顶掉很多个
			// 选课第一阶段 不允许跨年级跨专业选课 选课第二阶段 允许跨年级跨专业选课 不带下面的参数也可以
			"jxb_ids": co.Do_jxb_id,
			"kch_id":  co.Kch_id,
			// "kcmc":    co.kcmc,
			"rwlx":   win.rwlx,
			"rlkz":   win.rlkz,
			"cdrlkz": win.cdrlkz,
			"rlzlkz": win.rlzlkz,
			"sxbj":   sxbj,
			"xxkbj":  co.Xxkbj,
			"qz":     "0",
			"cxbj":   co.Cxbj,
			//"xkkz_id":    idx.xkkz_id,
			"njdm_id": idx.njdm_id,
			//"njdm_id_xs": idx.njdm_id,
			"zyh_id": idx.zyh_id,
			//"zyh_id_xs":  idx.zyh_id,
			"kklxdm": idx.kklxdm, // 校选10 专选01 英语06
			"xklc":   win.xklc,   // 选课轮次
			"xkxnm":  idx.xkxnm,
			"xkxqm":  idx.xkxqm,
			//"jcxx_id":    "[]jcxx_arr",
		}
		if idx.xkkz_id != "" {
			formData["xkkz_id"] = idx.xkkz_id
		} else {
			formData["xkkz_xh"] = idx.xkkz_xh
		}
		//if idx.xkkz_xh != "" {
		//	formData["xkkz_xh"] = idx.xkkz_xh
		//}
		if !a.waitLogin(ctx) {
			return result, ctx.Err()
		}
		resp, err := a.hedgeC.R().
			SetQueryParams(map[string]string{
				"gnmkdm": baseCfg.Gndmmk_xsxk,
				//"su":     a.cfg.Account,
			}).SetContext(ctx).
			SetFormData(formData).
			//SetResult(&result).
			Post(baseCfg.ChooseCourse)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				fmt.Println("选课请求已取消")
				return result, ctx.Err()
			}
			if errors.Is(err, context.DeadlineExceeded) {
				if resp != nil {
					fmt.Println("选课请求超时", resp.Duration())
					log.Println("选课请求超时", resp.Duration())
				}
			} else {
				fmt.Println("选课请求发生错误", err)
				time.Sleep(100 * time.Millisecond)
			}
			continue
		}
		if resp.IsStatusFailure() {
			log.Println("chooseCourse", resp.Status())
			log.Println("chooseCourse 状态码:", resp.Status(), resp.String())
			time.Sleep(1 * time.Second)
			continue
		}
		if a.LoginCheck(resp) {
		} else {
			return result, ErrUnauthorized
		}
		if err := json.Unmarshal(resp.Bytes(), &result); err != nil {
			fmt.Println(err, resp.String())
			log.Println(err, resp.String())
			continue
		}
		return result, nil
	}
	return result, nil
}

func (a *APIClient) GetSelected(ctx context.Context, xkxnm, xkxqm string) ([]ChosenDic, error) {
	// 查询已选课程
	fmt.Println("查询已选课程")
	var result []ChosenDic
	for range 2 {
		resp, err := a.hedgeC.R().
			SetContext(ctx).
			SetTimeout(time.Second*23).
			SetQueryParam("gnmkdm", baseCfg.Gndmmk_xsxk).
			//SetQueryParam("su", a.cfg.Account).
			SetFormData(map[string]string{
				"xkxnm": xkxnm,
				"xkxqm": xkxqm,
			}).
			//SetResult(&result).
			Post(baseCfg.CourseSelectedList)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil, ctx.Err()
			}
			if errors.Is(err, context.DeadlineExceeded) {
				if resp != nil {
					fmt.Println("已选课程请求超时", resp.Duration())
				}
				return nil, err
			}

			fmt.Println("已选课程请求错误:", err)
			log.Println("已选课程请求错误:", err)
			time.Sleep(150 * time.Millisecond)
			continue
		}
		if resp.IsStatusFailure() {
			log.Println("Choosed:", resp.Status())
			log.Println("getHaveChoosedList 状态码:", resp.Status())
			continue
		}
		// 权限拒绝的页面不带当前账号，LoginCheck 可能误判成“掉线”，
		// 所以先认“无功能权限”，再判断登录态。
		if isNoPermission(resp.String()) {
			fmt.Println("已选课程无功能权限")
			log.Println("已选课程无功能权限")
			return nil, ErrNoPermission
		}

		if a.LoginCheck(resp) {
		} else {
			return result, ErrUnauthorized
		}

		if err := json.Unmarshal(resp.Bytes(), &result); err != nil {
			fmt.Println(err, resp.String())
			log.Println(err, resp.String())
			continue
		}
		log.Printf("已选课程查询: \n%s", resp.String())
		return result, nil
	}
	return result, nil
}

func (a *APIClient) QuitCourse(ctx context.Context, idx IndexInfo, jxb_ids, kch_id string) (bool, string, error) {
	// 退课
	log.Println("========quitCourse()========")
	for range 3 {
		resp, err := a.hedgeC.R().
			SetContext(ctx).
			SetQueryParam("gnmkdm", baseCfg.Gndmmk_xsxk).
			//SetQueryParam("su", a.cfg.Account).
			SetFormData(map[string]string{
				"kch_id":  kch_id,
				"jxb_ids": jxb_ids,
				"xkxnm":   idx.xkxnm,
				"xkxqm":   idx.xkxqm,
				"txbsfrl": "0",
			}).
			Post(baseCfg.QuitCourse)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return false, "", ctx.Err()
			}
			fmt.Println("退课请求发生错误")
			log.Println(err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if resp.IsStatusFailure() {
			log.Println("quitCourse", resp.Status())
			fmt.Println("quitCourse", resp.Status())
		}
		if a.LoginCheck(resp) {
			log.Println(resp.String()) // "1"
			if resp.String() == `"1"` {
				//fmt.Println("退课成功")
				return true, resp.String(), nil
			} else if resp.String() == `"2"` {
				// 服务器繁忙
				return false, resp.String(), nil
			} else if resp.String() == `"3"` {
				// 未知错误
				return false, resp.String(), nil
			} else if resp.String() == `"4"` {
				// 非法访问
				return false, resp.String(), nil
			} else if resp.String() == `"5"` {
				// 验证失败
				return false, resp.String(), nil
			}
			return false, resp.String(), nil
		}

		return false, resp.String(), ErrUnauthorized
	}
	return false, "??", nil
}

// isCourseRegistered 实验性
func (a *APIClient) isCourseRegistered(ctx context.Context, cfg *APIConfig, co *CustomCourseDic) (bool, error) {
	log.Println("====isCourseRegistered====")
	for {
		formData := map[string]string{
			"jxb_id": co.Do_jxb_id,
			"xnm":    cfg.Index.xkxnm,
			"xqm":    cfg.Index.xkxqm,
		}
		if cfg.Index.xkkz_id != "" {
			formData["xkkz_id"] = cfg.Index.xkkz_id
		}
		if cfg.Index.xkkz_xh != "" {
			formData["xkkz_xh"] = cfg.Index.xkkz_xh
		}
		resp, err := a.hedgeC.R().
			SetContext(ctx).
			SetTimeout(time.Second*19).
			SetQueryParam("gnmkdm", baseCfg.Gndmmk_xsxk).
			//SetQueryParam("su", a.cfg.Account).
			SetFormData(formData).
			Post(baseCfg.CourseRegistered)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return false, ctx.Err()
			}
			if errors.Is(err, context.DeadlineExceeded) {
				if resp != nil {
					fmt.Println("Reg请求超时", resp.Duration())
				}
				continue
			} else {
				fmt.Println("Reg请求发生错误")
				log.Println(err)
				time.Sleep(150 * time.Millisecond)
			}
			continue
		}
		if resp.IsStatusFailure() {
			log.Println("isCourseRegistered:", resp.Status())
			log.Println("isCourseRegistered 状态码:", resp.Status())
			continue
		}
		if resp.ResultError() != nil {
			log.Println(resp.ResultError(), resp.String())
			continue
		}
		if a.LoginCheck(resp) {
			return resp.String() == "1", nil
		}

		return false, ErrUnauthorized
	}
}

// getCourseDoJxb 实验性
func (a *APIClient) getCourseDoJxb(ctx context.Context, cfg *APIConfig, jxb_ids []string) ([]CourseDetail, error) {
	// 貌似只有实验课能用这个
	var result []CourseDetail
	for {
		formData := map[string]string{
			"jxb_ids": strings.Join(jxb_ids, ","),
			"bklx_id": cfg.Window.bklx_id,
			"kklxdm":  cfg.Index.kklxdm,
			"rlkz":    cfg.Window.rlkz,
			"xklc":    cfg.Window.xklc,
			"zyh_id":  cfg.Index.zyh_id,
			"njdm_id": cfg.Index.njdm_id,
		}
		if cfg.Index.xkkz_id != "" {
			formData["xkkz_id"] = cfg.Index.xkkz_id
		} else {
			formData["xkkz_xh"] = cfg.Index.xkkz_xh
		}
		//if cfg.Index.xkkz_xh != "" {
		//	formData["xkkz_xh"] = cfg.Index.xkkz_xh
		//}
		requ := a.hedgeC.R().
			SetContext(ctx).
			SetQueryParams(map[string]string{
				"gnmkdm": baseCfg.Gndmmk_xsxk,
				//"su":     a.cfg.Account,
			}).
			SetFormData(formData)

		if !a.waitLogin(ctx) {
			return result, ctx.Err()
		}
		resp, err := requ.Post(baseCfg.ChooseSimpleDoJxb)
		if err != nil {
			// 判断是否因Context取消导致的错误
			if errors.Is(err, context.Canceled) {
				fmt.Println("请求已取消")
				return result, ctx.Err()
			}
			if errors.Is(err, context.DeadlineExceeded) {
				if resp != nil {
					fmt.Println("请求超时", resp.Duration())
				}
				continue
			}
			fmt.Println("请求发生错误:", err)
			log.Println("请求发生错误:", err, resp.String())
			time.Sleep(370 * time.Millisecond)
			continue
		}

		if resp.IsStatusFailure() {
			log.Println(resp.Status(), resp.String())
			time.Sleep(1 * time.Second)
			continue
		}
		if a.LoginCheck(resp) {
			if !cfg.State.detailDump {
				log.Println(resp.String())
				cfg.State.detailDump = true
			}
			if result != nil {
				return result, nil
			}
		} else {
			return result, ErrUnauthorized
		}
		if err := json.Unmarshal(resp.Bytes(), &result); err != nil {
			if resp.String() == `"0"` {
				fmt.Println(`"0"，未查询到信息，可能没到选课时间，可能程序编写错误，也可能教务系统临时调整了选课`)
				log.Println(`"0"，未查询到信息，可能没到选课时间，可能程序编写错误，也可能教务系统临时调整了选课`)
				cfg.State.needInit = true
				a.MarkParamRefresh()
				return nil, nil
			}
			fmt.Println(err)
			log.Println(err, resp.String())
		}
	}
}
