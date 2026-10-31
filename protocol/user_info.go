package protocol

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"log"
	"net/http"
	baseCfg "school_sdk/protocol/config"
	"strings"
	"time"
)

func (a *APIClient) GetJsonInfo() (UserInfo, error) {
	var result UserInfo
	resp, err := a.hedgeC.R().
		SetTimeout(12 * time.Second).
		//SetResult(&result).
		SetQueryParams(map[string]string{
			"gnmkdm": "N100801",
			//"su":     a.cfg.Account,
		}).Get(baseCfg.InfoJson)
	if err != nil {
		log.Println(err)
		return result, err
	}
	if resp.IsStatusFailure() {
		log.Println("GetJsonInfo HTTP 状态码错误:", resp.Status())
		return result, nil
	}
	if resp.ResultError() != nil {
		log.Println(resp.ResultError(), resp.String())
		return result, nil
	}
	if !a.LoginCheck(resp) {
		log.Println("获取个人信息登录态失效:", resp.Status())
		return result, ErrUnauthorized
	}

	if err := json.Unmarshal(resp.Bytes(), &result); err != nil {
		log.Println("获取个人信息失败 msg:", err, resp.String())
		if isNoPermission(resp.String()) {
			log.Println("获取个人信息无功能权限")
			return result, nil
		}
		log.Println(err)
	}

	a.Name = result.Xm
	return result, nil
}

// WebGetUserInfo 查询当前登录账号的个人信息，供 WebUI 使用。
//
// 与 GetJsonInfo 不同，它接受 context，并把所有失败如实返回：权限页不含
// 当前账号，LoginCheck 会误判成掉线，因此必须先判断“无功能权限”。
func (a *APIClient) WebGetUserInfo(ctx context.Context) (UserInfo, error) {
	var result UserInfo
	if err := ctx.Err(); err != nil {
		return result, err
	}
	resp, err := a.hedgeC.R().
		SetContext(ctx).
		SetQueryParams(map[string]string{
			"gnmkdm": "N100801",
		}).Get(baseCfg.InfoJson)
	if err != nil {
		return result, err
	}
	if resp.IsStatusFailure() {
		return result, fmt.Errorf("获取个人信息失败（%s）", resp.Status())
	}
	if isNoPermission(resp.String()) {
		return result, ErrNoPermission
	}
	if !a.LoginCheck(resp) {
		return result, ErrUnauthorized
	}
	if err := json.Unmarshal(resp.Bytes(), &result); err != nil {
		return result, fmt.Errorf("解析个人信息失败: %w", err)
	}
	if result.Xm == "" && result.Xh == "" && result.XhId == "" {
		return result, fmt.Errorf("学校返回的个人信息为空")
	}
	a.Name = result.Xm
	return result, nil
}

// WebGetUserPhoto 下载当前账号的学生大头照（原图），供 WebUI 使用。
//
// 返回图片字节和 MIME 类型。先嗅探内容再判断登录态：学校未上传照片时会
// 返回 HTML 页面，若先调用 LoginCheck 可能被误判为登录失效。
func (a *APIClient) WebGetUserPhoto(ctx context.Context) ([]byte, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	resp, err := a.Http.R().
		SetContext(ctx).
		SetQueryParams(map[string]string{
			"xh_id": a.cfg.Account,
			"zplx":  "rxqzp",
		}).
		Get(baseCfg.StudentPhoto)
	if err != nil {
		return nil, "", err
	}
	if resp.IsStatusFailure() {
		return nil, "", fmt.Errorf("获取大头照失败（%s），学校可能尚未上传照片", resp.Status())
	}
	body := resp.Bytes()
	if isNoPermission(string(body)) {
		return nil, "", ErrNoPermission
	}

	headerCT := strings.TrimSpace(resp.Header().Get("Content-Type"))
	if len(body) > 0 {
		sniffed := http.DetectContentType(body)
		if strings.HasPrefix(sniffed, "image/") {
			return body, sniffed, nil
		}
		if strings.HasPrefix(headerCT, "image/") {
			return body, headerCT, nil
		}
	}
	if !a.LoginCheck(resp) {
		return nil, "", ErrUnauthorized
	}
	shown := headerCT
	if shown == "" {
		shown = http.DetectContentType(body)
	}
	return nil, "", fmt.Errorf("学校未返回图片（%s），可能尚未上传照片", shown)
}

type UserInfo struct {
	Bdzcbj string `json:"bdzcbj"` // 已注册, 未报到
	BhId   string `json:"bh_id"`  // 班级名称
	Byzx   string `json:"byzx"`   // 毕业中学
	Bz     string `json:"bz"`     // 备注: 普本, 普本艺术, 江苏普本, 贵州普本
	Csrq   string `json:"csrq"`   // 出生日期
	CyNum  int    `json:"cyNum"`

	Year               string `json:"year"`               // 查询日期年
	Date               string `json:"date"`               // 二○二六年七月十三日
	DateDigit          string `json:"dateDigit"`          // 查询时间 2026年7月13日
	DateDigitSeparator string `json:"dateDigitSeparator"` // 2026-7-13
	Day                string `json:"day"`

	Gddh       string `json:"gddh"`     // 固定电话
	Fdyjgh     string `json:"fdyjgh"`   // 辅导员姓名
	HasXszp    string `json:"has_xszp"` // 是否拥有学生照片？
	JdNum      int    `json:"jdNum"`
	Jg         string `json:"jg"`    // 生员所在地区？
	JgId       string `json:"jg_id"` // 学院
	Jgpxzd     string `json:"jgpxzd"`
	JlNum      int    `json:"jlNum"`
	Jtdh       string `json:"jtdh"` // 家庭电话
	Jtdz       string `json:"jtdz"` // 家庭地址
	Ksh        string `json:"ksh"`  // 考生号
	Listnav    string `json:"listnav"`
	LocaleKey  string `json:"localeKey"` // zh_CN
	Month      string `json:"month"`
	Mzm        string `json:"mzm"`     // 民族: 汉族
	NjdmId     string `json:"njdm_id"` // 年级代码入学 2023
	PageTotal  int    `json:"pageTotal"`
	Pageable   bool   `json:"pageable"`
	Pyccdm     string `json:"pyccdm"`    // 培养层次代码: 本科
	PyfaxxId   string `json:"pyfaxx_id"` // AAAAAAAAAFFFFFFEEEEEECCCCCBBBB11
	Qqhm       string `json:"qqhm"`      // QQ号码
	QueryModel struct {
		CurrentPage   int  `json:"currentPage"`
		CurrentResult int  `json:"currentResult"`
		EntityOrField bool `json:"entityOrField"`
		Limit         int  `json:"limit"`
		Offset        int  `json:"offset"`
		PageNo        int  `json:"pageNo"`
		PageSize      int  `json:"pageSize"`
		ShowCount     int  `json:"showCount"`
		//Sorts         []interface{} `json:"sorts"`
		TotalCount  int `json:"totalCount"`
		TotalPage   int `json:"totalPage"`
		TotalResult int `json:"totalResult"`
	} `json:"queryModel"`
	Rangeable   bool   `json:"rangeable"`
	Rxrq        string `json:"rxrq"` // 入学日期 202?-09-0? YYYY-MM-DD
	Rxzf        string `json:"rxzf"` // 入学分数
	Sfzx        string `json:"sfzx"` // 是否？: 是
	Syd         string `json:"syd"`  // 生源地
	Sjhm        string `json:"sjhm"` // 手机号码
	TotalResult string `json:"totalResult"`
	Txdz        string `json:"txdz"`
	UserModel   struct {
		Monitor    bool   `json:"monitor"`
		RoleCount  int    `json:"roleCount"`
		RoleKeys   string `json:"roleKeys"`
		RoleValues string `json:"roleValues"`
		Status     int    `json:"status"`
		Usable     bool   `json:"usable"`
	} `json:"userModel"`
	Xbm    string `json:"xbm"`    // 性别
	Xh     string `json:"xh"`     // 学号
	XhId   string `json:"xh_id"`  // 学号
	Xjztdm string `json:"xjztdm"` // 学籍状态代码: 在读
	Xm     string `json:"xm"`     // 姓名
	Xlccdm string `json:"xlccdm"` // 学历层次代码: 本科
	Xmpy   string `json:"xmpy"`   // 姓名拼音
	Ywxm   string `json:"ywxm"`   // ？拼音姓名
	Xnm    string `json:"xnm"`    // 学年码 2026
	Xnmc   string `json:"xnmc"`   // 学年名称 2026-2027
	Xqm    string `json:"xqm"`    // 学期码 3
	Xqmc   string `json:"xqmc"`   // 学期名称 1
	Xz     string `json:"xz"`     // 学制 4 年

	Ylzd1 string `json:"ylzd1"` // 分数1 语文
	Ylzd2 string `json:"ylzd2"` // 分数2 数学
	Ylzd3 string `json:"ylzd3"` // 分数3 英语

	Zjhm  string `json:"zjhm"`   // 证件号码
	Zjlxm string `json:"zjlxm"`  // 证件类型
	ZyhId string `json:"zyh_id"` // 专业名称
	Zzmmm string `json:"zzmmm"`  // 政治面貌

	AdditionalData map[string]any `json:",unknown"`
}

func (a *APIClient) GetRawInfo() ([]byte, error) {
	resp, err := a.Http.R().
		SetQueryParams(map[string]string{
			"gnmkdm": "N100801",
			//"layout": "default",
			//"su": a.cfg.Account,
		}).
		Get(baseCfg.InfoHtm)
	//Get(baseCfg.PersonalInfo)

	if err != nil {
		log.Println(err)
		return nil, err
	}
	if !a.LoginCheck(resp) {
		log.Println("登录态失效:", resp.Status())
		return nil, ErrUnauthorized
	}
	return resp.Bytes(), nil
}

func (a *APIClient) GetPhoto() ([]byte, error) {
	resp, err := a.Http.R().
		SetQueryParams(map[string]string{
			"xh_id": a.cfg.Account,
			"zplx":  "rxqzp",
		}).
		Get(baseCfg.StudentPhoto)

	if err != nil {
		log.Println(err)
		return nil, err
	}
	if !a.LoginCheck(resp) {
		log.Println("登录态失效:", resp.Status())
		return nil, ErrUnauthorized
	}
	return resp.Bytes(), nil
}
