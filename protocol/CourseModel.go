package protocol

import (
	"time"
)

// APIConfig 是一次选课会话的可变状态。
//
// 它以前是一个 60 多个字段的 god struct，字段按"谁写、谁读"混在一起。现在按
// 生命周期拆成五块，每块都是值类型（string/bool/切片），可以整体拷贝或按值传给
// 下层函数：
//
//	Index  —— 选课首页解析出来的学生信息与查询参数（Initialize/SelectTab 重建）
//	Window —— 选课时间窗与学分统计（只读展示）
//	Tabs   —— 页签列表与当前模式（SelectTab 维护）
//	Wish   —— 愿望清单（由 CLI 从 Excel 读入）
//	State  —— 请求过程中的开关：needInit 标记需要重新抓首页，listDump/detailDump
//	          只是"打印过一次"的幂等标记，yl/xztk 是查询/退课的行为开关
//
// 之所以仍保留一层 APIConfig 而不是让各函数直接收五块参数：请求构造需要同时读
// Index/Window/State，拆参数会把 15+ 个实参铺到每个协议函数上。后续可以把
// 某一块单独传给不需要全量的函数（例如 GetCourseList 只依赖 Index+State）。
type APIConfig struct {
	Index  IndexInfo
	Window SelectionWindow // 存储页签参数
	Tabs   TabState        // 切换页签用
	Wish   WishList
	State  RuntimeState
}

// IndexInfo 是选课首页解析出来的学校/学生信息与查询参数。
// 字段名保持正方接口的原始拼写，方便对照抓包与接口文档。
type IndexInfo struct {
	xkkz_id string
	xkkz_xh string
	kklxdm  string // 课程类型代码

	bh_id   string // 班号
	xqh_id  string // 校区号
	zyh_id  string // 专业号
	njdm_id string // 年级代码
	xkxqm   string // 学期 3 12 16
	xkxnm   string // 学年

	xz   string // 学制4年
	xxdm string // 学校代码

	mzm  string
	ccdm string // 层次代码
	xbm  string // 性别码 男1 女2

	xszxzt  string
	zyfx_id string // wfx
	xslbdm  string // wlb
	xsbj    string // 4294967296 or 1 ，学生标记,
	jg_id   string // 学院, 机构ID

	tkzgcs_qt string
	currentsj string

	njdm_id_list0 string // 过滤

	//<input type="hidden" name="jgh_id" id="jgh_id"/>
	//<input type="hidden" name="to_kch" id="to_kch" value=""/>
	//<input type="hidden" name="jzxkf" id="jzxkf" value="0"/>
	//<input type="hidden" name="tkzgcs_qt" id="tkzgcs_qt" value="-1"/>
	//<input type="hidden" name="xkzgmc" id="xkzgmc" value="100"/>
	//<input type="hidden" name="xkzgxf" id="xkzgxf" value="100"/>
	//<input type="hidden" name="zkcs" id="zkcs" value="13"/>
	//<input type="hidden" name="zxfs" id="zxfs" value="26.5"/>
	//<input type="hidden" name="bdzcbj" id="bdzcbj" value="0"/>
	//<input type="hidden" name="xkxnm" id="xkxnm" value="2026"/>
	//<input type="hidden" name="xkxqm" id="xkxqm" value="3"/>
	//<input type="hidden" name="xkxnmc" id="xkxnmc" value="2026-2027"/>
	//<input type="hidden" name="xkxqmc" id="xkxqmc" value="1"/>
	//<input type="hidden" name="xh_id" id="xh_id" value="250707014016"/>
	//<input type="hidden" name="xqh_id" id="xqh_id" value="1"/>
	//<input type="hidden" name="jg_id_1" id="jg_id_1" value="007"/>
	//<input type="hidden" name="zyh_id" id="zyh_id" value="07201"/>
	//<input type="hidden" name="zyfx_id" id="zyfx_id" value="wfx"/>
	//<input type="hidden" name="njdm_id" id="njdm_id" value="2025"/>
	//<input type="hidden" name="xkkz_xh" id="xkkz_xh" value=""/>
	//<input type="hidden" name="bh_id" id="bh_id" value="20250385"/>
	//<input type="hidden" name="xbm" id="xbm" value="2"/>
	//<input type="hidden" name="zh" id="zh" value=""/>
	//<input type="hidden" name="qzz" id="qzz" value="100"/>
	//<input type="hidden" name="xslbdm" id="xslbdm" value="01"/>
	//<input type="hidden" name="mzm" id="mzm" value="17"/>
	//<input type="hidden" name="xz" id="xz" value="4"/>
	//<input type="hidden" name="xh" id="xh" value="250707014016"/>
	//<input type="hidden" name="xm" id="xm" value="热阿依·杰恩斯汉"/>
	//<input type="hidden" name="currentsj" id="currentsj" value="2026-09-22 19:36:54"/>
	//<input type="hidden" name="ccdm" id="ccdm" value="3"/>
	//<input type="hidden" name="xsbj" id="xsbj" value="66"/>
	//<input type="hidden" name="sjhm" id="sjhm" value=""/>
	//<input type="hidden" name="xszxzt" id="xszxzt" value="1"/>
	//<input type="hidden" name="njdm_id_1" id="njdm_id_1" value="2025"/>
	//<input type="hidden" name="zyh_id_1" id="zyh_id_1" value="07201"/>
	//<input type="hidden" name="sfxsxkbz" id="sfxsxkbz" value="0"/>
	//<input type="hidden" name="sfxskssj" id="sfxskssj" value="0"/>
	//<input type="hidden" name="wrljxbbhkg" id="wrljxbbhkg" value="0"/>
	//<input type="hidden" name="jxbzbkg" id="jxbzbkg" value="1"/>
	//<input type="hidden" name="tykpzykg" id="tykpzykg" value="0"/>
	//<input type="hidden" name="tkdxyzms" id="tkdxyzms" value="0"/>
	//<input type="hidden" name="jxbzhkg" id="jxbzhkg" value="0"/>
	//<input type="hidden" name="xxdm" id="xxdm" value="10305"/>
	//<input type="hidden" name="xkgwckg" id="xkgwckg" value="0"/>
	//<input type="hidden" name="cxkctskg" id="cxkctskg" value="0"/>
	//<input type="hidden" name="kxqxktskg" id="kxqxktskg" value="0"/>
	//<input type="hidden" name="tbtkxqxktskg" id=tbtkxqxktskg value="0"/>
	//<input type="hidden" name="xkwkxkcqjtsqkg" id=xkwkxkcqjtsqkg value="0"/>
	//<input type="hidden" name="xkkczdsqkg" id="xkkczdsqkg" value="1"/>
	//<input type="hidden" name="xkmcjzxskcs" id="xkmcjzxskcs" value="10"/>
	//<input type="hidden" name="xkckctkckg" id="xkckctkckg" value=""/>
	//<input type="hidden" name="xkbzsyljkg" id="xkbzsyljkg" value="0"/>
	//<input type="hidden" name="xyjskljdz" id="xyjskljdz" value="http://"/>
	//<input type="hidden" name="dsfrlxskg" id="dsfrlxskg" value="0"/>
	//<input type="hidden" name="xsckxkgzkg" id="xsckxkgzkg" value="0"/>
	//<input type="hidden" name="xxkbkztskg" id="xxkbkztskg" value="0"/>
	//<input type="hidden" name="xkxfqzfs" id="xkxfqzfs" value="0"/>
	//<input type="hidden" name="zzxkxsrwxfkg" id="zzxkxsrwxfkg" value="0"/>
	//<input type="hidden" name="zzxkxsxkrsmxkg" id="zzxkxsxkrsmxkg" value="0"/>
	//<input type="hidden" name="zzxkxfmcjckg" id="zzxkxfmcjckg" value="0"/>
	//<input type="hidden" name="zzxkwylksqxkyxkg" id="zzxkwylksqxkyxkg" value="0"/>
	//<input type="hidden" name="rlmsbsfrlkg" id="rlmsbsfrlkg" value="0"/>
	//<input type="hidden" name="xkkzrlsrlmxsfs" id="xkkzrlsrlmxsfs" value="0"/>
	//<input type="hidden" name="xksjjfsjxskg" id="xksjjfsjxskg" value="0"/>
	//<input type="hidden" name="xsckgrkbkg" id="xsckgrkbkg" value="0"/>
	//<input type="hidden" name="sfxsjxdd" id="sfxsjxdd" value="1"/>
	//<input type="hidden" name="xkjsxxxsfs" id="xkjsxxxsfs" value="0"/>
	//<input type="hidden" name="xkjxbxskcxf" id="xkjxbxskcxf" value="0"/>
	//<input type="hidden" name="xkpksjctqrkg" id="xkpksjctqrkg" value="0"/>
	//<input type="hidden" name="zzxkgjcxkg_xf" id="zzxkgjcxkg_xf" value="0"/>
	//<input type="hidden" name="zzxkgjcxkg_fatj" id="zzxkgjcxkg_fatj" value="0"/>
	//<input type="hidden" name="zzxkgjcxkg_zyfx" id="zzxkgjcxkg_zyfx" value="0"/>
	//<input type="hidden" name="zzxkgjcxkg_bj" id="zzxkgjcxkg_bj" value="0"/>
	//<input type="hidden" name="zzxkgjcxkg_kcz" id="zzxkgjcxkg_kcz" value="0"/>
	//<input type="hidden" name="zzxkgjcxkg_nj" id="zzxkgjcxkg_nj" value="1"/>
	//<input type="hidden" name="zzxkgjcxkg_xy" id="zzxkgjcxkg_xy" value="1"/>
	//<input type="hidden" name="zzxkgjcxkg_zy" id="zzxkgjcxkg_zy" value="1"/>
	//<input type="hidden" name="zzxkgjcxkg_kkxy" id="zzxkgjcxkg_kkxy" value="1"/>
	//<input type="hidden" name="zzxkgjcxkg_xqu" id="zzxkgjcxkg_xqu" value="0"/>
	//<input type="hidden" name="zzxkgjcxkg_yqu" id="zzxkgjcxkg_yqu" value="0"/>
	//<input type="hidden" name="zzxkgjcxkg_tjbj" id="zzxkgjcxkg_tjbj" value="0"/>
	//<input type="hidden" name="zzxkgjcxkg_kclb" id="zzxkgjcxkg_kclb" value="1"/>
	//<input type="hidden" name="zzxkgjcxkg_kcxz" id="zzxkgjcxkg_kcxz" value="1"/>
	//<input type="hidden" name="zzxkgjcxkg_jxms" id="zzxkgjcxkg_jxms" value="1"/>
	//<input type="hidden" name="zzxkgjcxkg_kcgs" id="zzxkgjcxkg_kcgs" value="1"/>
	//<input type="hidden" name="zzxkgjcxkg_skxq" id="zzxkgjcxkg_skxq" value="1"/>
	//<input type="hidden" name="zzxkgjcxkg_skjc" id="zzxkgjcxkg_skjc" value="1"/>
	//<input type="hidden" name="zzxkgjcxkg_jxb" id="zzxkgjcxkg_jxb" value="1"/>
	//<input type="hidden" name="zzxkgjcxkg_sfcx" id="zzxkgjcxkg_sfcx" value="1"/>
	//<input type="hidden" name="zzxkgjcxkg_ywyl" id="zzxkgjcxkg_ywyl" value="1"/>
	//<input type="hidden" name="zzxkgjcxkg_sksjct" id="zzxkgjcxkg_sksjct" value="0"/>
	//<input type="hidden" name="xksdxjckg" id="xksdxjckg" value="0"/>
	//<input type="hidden" name="xkxskclxkg" id="xkxskclxkg" value="0"/>
}

// SelectionWindow 选课页签
//
// 是选课时间窗与学分统计，用于展示与"距离结束还有多久"。
type SelectionWindow struct {
	bklx_id string
	rwlx    string
	xkly    string // 选课来源
	xklc    string // 轮次
	xklcmc  string // 轮次名称
	sfkknj  string

	rlkz   string // 容量控制
	rlzlkz string // 容量总量控制
	cdrlkz string
	kkbk   string
	kkbkdj string

	sfkcfx     string // 未知参数
	bbhzxjxb   string
	xkxskcgskg string
	jxbzcxskg  string

	jdlx         string
	sfznkx       string
	kzybkxy      string
	sfkkzy       string
	zdkxms       string
	gnjkxdnj     string
	sfkkjyxdxnxq string

	sfktk string // 是否可退课
	sfkxk string // 是否可选课
	sfkxq string // 是否开学前

	minCredit      string // 本学期选课要求总学分最低
	maxCredit      string // 总学分最高
	selectedCredit string // 本学期已选学分
	syxs           string // 距选课结束的小时数
	syts           string // 距选课结束的天数
	zxfs           string // 已修分数？
	xkkssj         string // 例: 2026-09-09 12:30:00
	xkjssj         string // 选课结束时间 2026-09-10 11:00:0

	startTimeStamp time.Time // 开始选课时间戳

	//<input type="hidden" name="rwlx" id="rwlx" value="2"/>
	//<input type="hidden" name="kczxskg" id="kczxskg" value="0"/>
	//<input type="hidden" name="kklxpx" id="kklxpx" value="4510"/>
	//<input type="hidden" name="xklc" id="xklc" value="2"/>
	//<input type="hidden" name="xklcmc" id="xklcmc" value="第2轮"/>
	//<input type="hidden" name="bklx_id" id="bklx_id" value="0"/>
	//<input type="hidden" name="zckz" id="zckz" value="0"/>
	//<input type="hidden" name="sfzyxk" id="sfzyxk" value="0"/>
	//<input type="hidden" name="sfyjxk" id="sfyjxk" value="0"/>
	//<input type="hidden" name="zdzys" id="zdzys" value="1"/>
	//<input type="hidden" name="sfqzxk" id="sfqzxk" value="0"/>
	//<input type="hidden" name="sfkknj" id="sfkknj" value="0"/>
	//<input type="hidden" name="gnjkxdnj" id="gnjkxdnj" value="0"/>
	//<input type="hidden" name="sfkkzy" id="sfkkzy" value="0"/>
	//<input type="hidden" name="kzybkxy" id="kzybkxy" value="0"/>
	//<input type="hidden" name="sfznkx" id="sfznkx" value="0"/>
	//<input type="hidden" name="zdkxms" id="zdkxms" value="0"/>
	//<input type="hidden" name="sfkxq" id="sfkxq" value="0"/>
	//<input type="hidden" name="bhbcyxkjxb" id="bhbcyxkjxb" value="0"/>
	//<input type="hidden" name="sfkcfx" id="sfkcfx" value="0"/>
	//<input type="hidden" name="kkbk" id="kkbk" value="0"/>
	//<input type="hidden" name="kkbkdj" id="kkbkdj" value="0"/>
	//<input type="hidden" name="bklbkcj" id="bklbkcj" value="0"/>
	//<input type="hidden" name="sfkxk" id="sfkxk" value="1"/>
	//<input type="hidden" name="sfktk" id="sfktk" value="1"/>
	//<input type="hidden" name="txbsfrl" id="txbsfrl" value="0"/>
	//<input type="hidden" name="jsfrlsj" id="jsfrlsj" value="0"/>
	//<input type="hidden" name="txbsfrl_1" id="txbsfrl_1" value="0"/>
	//<input type="hidden" name="tktjrs" id="tktjrs" value="0"/>
	//<input type="hidden" name="rlzlkz" id="rlzlkz" value="1"/>
	//<input type="hidden" name="cdrlkz" id="cdrlkz" value="0"/>
	//<input type="hidden" name="rlkz" id="rlkz" value="0"/>
	//<input type="hidden" name="xkly" id="xkly" value="0"/>
	//<input type="hidden" name="sfyxsksjct" id="sfyxsksjct" value="0"/>
	//<input type="hidden" name="ddkzbj" id="ddkzbj" value="0"/>
	//<input type="hidden" name="cxddkzbj" id="cxddkzbj" value="0"/>
	//<input type="hidden" name="sfkkjyxdxnxq" id="sfkkjyxdxnxq" value="0"/>
	//<input type="hidden" name="sfkgbcx" id="sfkgbcx" value="0"/>
	//<input type="hidden" name="xkxskcgskg" id="xkxskcgskg" value="1"/>
	//<input type="hidden" name="tkzgcs_jb" id="tkzgcs_jb" value="-1"/>
	//<input type="hidden" name="jxbzcxskg" id="jxbzcxskg" value="0"/>
	//<input type="hidden" name="xkzgbj" id="xkzgbj" value="0"/>
	//<input type="hidden" name="cxcykclxxskg" id="cxcykclxxskg" value="0"/>
	//<input type="hidden" name="sfrxtgkcxd" id="sfrxtgkcxd" value="0"/>
	//<input type="hidden" name="tykczgxdcs" id="tykczgxdcs" value="0"/>
	//<input type="hidden" name="sxrlkzlx" id="sxrlkzlx" value=""/>
	//<input type="hidden" name="isinxksj" id="isinxksj" value="1"/>
	//<input type="hidden" name="isInylsj" id="isInylsj" value="0"/>
	//<input type="hidden" name="xksjxskz" id="xksjxskz" value="0"/>
	//<input type="hidden" name="lnzkcs" id="lnzkcs" value=""/>
	//<input type="hidden" name="lnzxfs" id="lnzxfs" value=""/>
	//<input type="hidden" name="bxqzdxkxf" id="bxqzdxkxf" value="0"/>
	//<input type="hidden" name="bxqzgxkxf" id="bxqzgxkxf" value="2.5"/>
	//<input type="hidden" name="bxqzdxkmc" id="bxqzdxkmc" value="0"/>
	//<input type="hidden" name="bxqzgxkmc" id="bxqzgxkmc" value="2"/>
	//<input type="hidden" name="lnzdxkxf" id="lnzdxkxf" value="0"/>
	//<input type="hidden" name="lnzgxkxf" id="lnzgxkxf" value="0"/>
	//<input type="hidden" name="lnzdyhxf" id="lnzdyhxf" value="-1"/>
	//<input type="hidden" name="lnzgyhxf" id="lnzgyhxf" value="-1"/>
	//<input type="hidden" name="lnzdxkmc" id="lnzdxkmc" value="0"/>
	//<input type="hidden" name="lnzgxkmc" id="lnzgxkmc" value="0"/>
	//<input type="hidden" name="bbhzxjxb" id="bbhzxjxb" value="0"/>
	//<input type="hidden" name="zxgbxkkg" id="zxgbxkkg" value="0"/>
	//<input type="hidden" name="syts" id="syts" value="0"/>
	//<input type="hidden" name="syxs" id="syxs" value="15"/>
	//<input type="hidden" name="jdlx" id="jdlx" value="0"/>
	//<input type="hidden" name="bjgkczxbbjwcx" id="bjgkczxbbjwcx" value="0"/>
	//<input type="hidden" name="jspage" id="jspage" value="0"/>
	//<input type="hidden" name="xkydjc" id="xkydjc" value="0"/>
	//<input type="hidden" name="lnzgxkxs" id="lnzgxkxs" value="0"/>
	//<input type="hidden" name="bxqzgxkxs" id="bxqzgxkxs" value="0"/>
	//<input type="hidden" name="kzkcgs" id="kzkcgs" value="0"/>
	//<input type="hidden" name="hdmc" id="hdmc" value=""/>
	//<input type="hidden" name="xkkssj" id="xkkssj" value="2026-09-22 12:30:00"/>
	//<input type="hidden" name="xkjssj" id="xkjssj" value="2026-09-23 11:00:00"/>
}

// TabState 是页签与当前模式，由 SelectTab 维护。
type TabState struct {
	Kklxmc    string      // 当前课程类型名称
	modeName  string      // 当前模式名称: 特殊课程、通识选修课...
	modeStore []ModeStore // 模式存储，用于模式切换
}

// WishList 是愿望清单，字段来自 CLI 的 Excel 输入，engine/web 不设置。
type WishList struct {
	WantClassList   []string
	WantTeacherList []string
	WantTypeList    []string
}

// RuntimeState 是请求过程中的开关；needInit 决定是否重新抓首页，
// listDump/detailDump 是打印幂等标记，yl/xztk 是查询/退课行为开关。
type RuntimeState struct {
	listDump   bool
	detailDump bool
	needInit   bool
	yl         bool // 余量查询参数
	xztk       bool // 限制退课
}

// ModeStore zzxkYzb.js
// function queryCourse(a_element,kklxdm,xkkz_id,njdm_id,zyh_id,xkkz_xh){}
type ModeStore struct {
	Kklxmc  string
	Kklxdm  string
	Xkkz_id string
	Njdm_id string
	Zyh_id  string
	Xkkz_xh string
}

type ChosenDic struct {
	Bdzcbj             string `json:"bdzcbj"`
	Cxbj               string `json:"cxbj"`
	Date               string `json:"date"`
	DateDigit          string `json:"dateDigit"`
	DateDigitSeparator string `json:"dateDigitSeparator"`

	Day       string `json:"day"`
	Ddkzbj    string `json:"ddkzbj"`
	Do_jxb_id string `json:"do_jxb_id"` //
	Xkkz_id   string `json:"xkkz_id"`   // 'xkkz_id': '0',
	Rwlx      string `json:"rwlx"`      // 'rwlx': '1',
	IsInxksj  string `json:"isInxksj"`
	Jdlx      string `json:"jdlx"`
	Jgpxzd    string `json:"jgpxzd"`
	Jsxx      string `json:"jsxx"`   // "320057/陈爱华/副教授"
	JxbId     string `json:"jxb_id"` //
	Jxbmc     string `json:"jxbmc"`  // 教学班名称 '化工原理（上）-0002',

	Jxbxf     string `json:"jxbxf"`
	Jxbzls    string `json:"jxbzls"`
	Jxdd      string `json:"jxdd"`
	Kcmc      string `json:"kcmc"` // 课程名称 "化工原理（上）"
	Kklxdm    string `json:"kklxdm"`
	Kklxmc    string `json:"kklxmc"`
	Xf        string `json:"xf"`     // 学分 'xf': '3'
	Kch       string `json:"kch"`    // 'kch': '0003021031',
	Kch_id    string `json:"kch_id"` // 课程号ID 'kch_id': '0003021031',
	Kklxpx    string `json:"kklxpx"`
	Krrl      string `json:"krrl"`
	Listnav   string `json:"listnav"`
	LocaleKey string `json:"localeKey"`
	Month     string `json:"month"`
	PageTotal int    `json:"pageTotal"`
	Pageable  bool   `json:"pageable"`
	Sfktk     string `json:"sfktk"`  // 是否可退课
	Sfxkbj    string `json:"sfxkbj"` // 是否处于选课
	JxbRS     string `json:"jxbrs"`  // 'jxbrs': '68'
	YXzRS     string `json:"yxzrs"`  // 'yxzrs': '68'

	Qz          string `json:"qz"`
	Rangeable   bool   `json:"rangeable"`
	Sksj        string `json:"sksj"`
	Sxbj        string `json:"sxbj"`
	TKchId      string `json:"t_kch_id"`
	TotalResult string `json:"totalResult"`
	Xxkbj       string `json:"xxkbj"` // 选修课标记?
	Year        string `json:"year"`
	Zixf        string `json:"zixf"`
	Zy          string `json:"zy"`

	QueryModel QueryModel `json:"queryModel"`
	UserModel  UserModel  `json:"userModel"`
}

type ChooseCourseResult struct {
	Flag string `json:"flag"`
	Msg  string `json:"msg"`
}

type UserModel struct {
	Monitor    bool   `json:"monitor"`
	RoleCount  int    `json:"roleCount"`
	RoleKeys   string `json:"roleKeys"`
	RoleValues string `json:"roleValues"`
	Status     int    `json:"status"`
	Usable     bool   `json:"usable"`
}

type QueryModel struct {
	CurrentPage   int   `json:"currentPage"`
	CurrentResult int   `json:"currentResult"`
	EntityOrField bool  `json:"entityOrField"`
	Limit         int   `json:"limit"`
	Offset        int   `json:"offset"`
	PageNo        int   `json:"pageNo"`
	PageSize      int   `json:"pageSize"`
	ShowCount     int   `json:"showCount"`
	Sorts         []any `json:"sorts"`
	TotalCount    int   `json:"totalCount"`
	TotalPage     int   `json:"totalPage"`
	TotalResult   int   `json:"totalResult"`
}

type CourseListDic struct {
	Blyxrs             string `json:"blyxrs"` // 本轮已选人数
	Blzyl              string `json:"blzyl"`
	Cxbj               string `json:"cxbj"`               // 重修标记 0
	Date               string `json:"date"`               // '二○二五年二月二十六日'
	DateDigit          string `json:"dateDigit"`          // '2025年2月26日'
	DateDigitSeparator string `json:"dateDigitSeparator"` // '2025-2-26'

	Day       string `json:"day"`
	Fxbj      string `json:"fxbj"`
	Jgpxzd    string `json:"jgpxzd"`
	JxbId     string `json:"jxb_id"` // 教学班id，用于连接List和Detail
	Jxbmc     string `json:"jxbmc"`  // 教学班名称  "艺术哲学：美是如何诞生的(艺术类)-0001"
	Jxbxf     string `json:"jxbxf"`
	Jxbzls    string `json:"jxbzls"`           // 'jxbzls': '1'
	Kch       string `json:"kch"`              // 课程号  '9000000398'
	KchId     string `json:"kch_id"`           // 课程号 id
	Kclxmc    string `json:"kclxmc,omitempty"` // 公共选修课
	Kcmc      string `json:"kcmc"`             // 课程名称  "艺术哲学：美是如何诞生的(艺术类)"
	Kcrow     string `json:"kcrow"`            // RowNum int 行号
	Kklxdm    string `json:"kklxdm"`           // 课程类型代码，区分不同类型选课  '10'
	Kzmc      string `json:"kzmc"`             // 课程性质  "艺术类"
	Listnav   string `json:"listnav"`
	LocaleKey string `json:"localeKey"`
	Month     string `json:"month"`
	PageTotal int    `json:"pageTotal"`
	Pageable  bool   `json:"pageable"`

	Rangeable   bool   `json:"rangeable"`
	Rwzxs       string `json:"rwzxs"`
	Sftj        string `json:"sftj"`
	TotalResult string `json:"totalResult"`
	Xf          string `json:"xf"`    // 学分  "1.5"
	Xxkbj       string `json:"xxkbj"` // 选修课标记?
	Year        string `json:"year"`  // '2025'
	Yxzrs       string `json:"yxzrs"` // 已选总人数  "70"
	Zcongbj     string `json:"zcongbj"`

	QueryModel QueryModel `json:"queryModel"`
	UserModel  UserModel  `json:"userModel"`
}

type CourseListResult struct {
	TmpList []CourseListDic `json:"tmpList"` // 搜索课程返回的清单
	Sfxsjc  string          `json:"sfxsjc"`
	Msg     string          `json:"msg"`
	Flag    string          `json:"flag"` // 成功 flag=1
}

// CourseDetail zzxkyzb_cxJxbWithKchZzxkYzb.html
type CourseDetail struct {
	Bxbj               string `json:"bxbj"`
	Date               string `json:"date"`               //'date': '二○二五年二月二十日'
	DateDigit          string `json:"dateDigit"`          //'dateDigit': '2025年2月20日'
	DateDigitSeparator string `json:"dateDigitSeparator"` //'dateDigitSeparator': '2025-2-20'
	Day                string `json:"day"`                //'day': '20'

	Dsfrl string `json:"dsfrl"`
	Fxbj  string `json:"fxbj"`

	DoJxbId   string `json:"do_jxb_id"`
	FjxbId    string `json:"fjxb_id"`
	Jgpxzd    string `json:"jgpxzd"`
	Jsxx      string `json:"jsxx"`   // 教师信息 440015/裴如意/副教授
	JxbId     string `json:"jxb_id"` // 教学班id，用于连接List和Detial 以 jxb_id 为课程唯一标识符
	Jxbmc     string `json:"jxbmc"`
	Jxbrs     string `json:"jxbrs"` // 教学班人数
	Jxdd      string `json:"jxdd"`  // 教学地点 知行楼108
	Listnav   string `json:"listnav"`
	LocaleKey string `json:"localeKey"`
	Month     string `json:"month"`
	PageTotal int    `json:"pageTotal"`
	Pageable  bool   `json:"pageable"`

	Rangeable   bool   `json:"rangeable"`
	Sjsfsj      string `json:"sjsfsj"`
	Sksj        string `json:"sksj"` // 上课时间 星期二第1-2节{2-13周}
	TotalResult string `json:"totalResult"`

	Xsdm string `json:"xsdm"`
	Xsmc string `json:"xsmc"`
	Year string `json:"year"` // year: 2025

	Jxbrl  string `json:"jxbrl"`  // 教学班容量
	Xqumc  string `json:"xqumc"`  // 校区名称 北校区 南校区
	Xqh_id string `json:"xqh_id"` // 校区号 3
	Kcxzmc string `json:"kcxzmc"` // 课程性质名称: 必修
	Kkxymc string `json:"kkxymc"` // kkxymc: 外国语学院
	Jxms   string `json:"jxms"`   // jxms: 理论
	Kclbmc string `json:"kclbmc"` // kclbmc: 公共必修课
	Xf     string `json:"xf"`
	Yqmc   string `json:"yqmc"` //'yqmc': '--'

	Yxzrs string `json:"yxzrs"` // 已选总人数 9.0正方

	QueryModel QueryModel `json:"queryModel"`
	UserModel  UserModel  `json:"userModel"`

	//Jsm       string `json:"jsm"`    // Teacher 参考自 GCCTool
	//Jsmc      string `json:"jsmc"`   // Room 参考自 GCCTool
	//Zrs         string `json:"zrs"` // 总人数 参考自 GCCTool 盐工无
}

type CustomCourseDic struct {
	Jxb_id string `json:"jxb_id"` // 教学班id，用于连接 List 和 Detail
	Jxbmc  string `json:"jxbmc"`  // 教学班名称  艺术哲学：美是如何诞生的(艺术类)-0001
	Jxbzls string `json:"jxbzls"` // 'jxbzls': '1'
	Kch    string `json:"kch"`    // 课程号
	Kch_id string `json:"kch_id"` // 课程号id
	Kcmc   string `json:"kcmc"`   // 课程名称  艺术哲学：美是如何诞生的(艺术类)
	Kklxdm string `json:"kklxdm"` // 关键参数，区分不同类型选课  '10'
	Kclxmc string `json:"kclxmc"` // 课程类别名称  'kclxmc': '公共选修课'
	Kzmc   string `json:"kzmc"`   // 课程性质  'kzmc': '艺术类'
	XF     string `json:"xf"`     // 学分  1.5
	Xxkbj  string `json:"xxkbj"`  // 选修课标记?
	Year   string `json:"year"`   // 'year': '2025'
	Yxzrs  string `json:"yxzrs"`  // 已选人数  'yxzrs': '70'
	Cxbj   string `json:"cxbj"`   // 重修标记 '0'

	Do_jxb_id string `json:"do_jxb_id"` //
	Jxbrl     string `json:"jxbrl"`     // 'jxbrl': '66'
	Sksj      string `json:"sksj"`      //'sksj': '星期二第1-2节{2-13周}
	Jxdd      string `json:"jxdd"`      // 'jxdd': '知行楼108',
	Jsxx      string `json:"jsxx"`      //jsxx': '440015/裴如意/副教授'
	Xqumc     string `json:"xqumc"`     //'xqumc': '北校区'
	Xqh_id    string `json:"xqh_id"`    //'xqh_id': '3'
	Kcxzmc    string `json:"kcxzmc"`    // 'kcxzmc': '必修'
	Kkxymc    string `json:"kkxymc"`    // 'kkxymc': '外国语学院'
	Jxms      string `json:"jxms"`      //'jxms': '理论'
	Kclbmc    string `json:"kclbmc"`    //'kclbmc': '公共必修课'
	Day       string `json:"day"`       // 'day': '26'
	// 'date': '二○二五年二月二十六日', 'dateDigit': '2025年2月26日', 'dateDigitSeparator': '2025-2-26'
	Date               string `json:"date"`
	DateDigit          string `json:"dateDigit"` //'dateDigit': '2025年2月20日'
	DateDigitSeparator string `json:"dateDigitSeparator"`
}
