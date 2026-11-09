package jwmock

import "fmt"

// loginPage 是账号密码登录页：没有字符验证码，也没有滑块标记。
func loginPage(base string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>登录</title></head>
<body>
<h3>统一身份认证（模拟）</h3>
<form method="post" action="%s/xtgl/login_slogin.html">
  <input type="hidden" id="csrftoken" name="csrftoken" value="mock-csrf">
  <label>用户名 <input id="yhm" name="yhm" autocomplete="username"></label>
  <label>密码 <input id="mm" name="mm" type="password" autocomplete="current-password"></label>
  <button type="submit">登录</button>
</form>
</body></html>`, base)
}

func tipsPage(msg string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>登录</title></head>
<body><div id="tips">%s</div></body></html>`, msg)
}

func menuPage() string {
	return `<!DOCTYPE html><html><head><meta charset="utf-8"><title>首页</title></head>
<body><div id="menu">模拟教务首页</div></body></html>`
}

// indexPage 是选课首页，包含 GetIndexParams 需要的全部隐藏字段和页签。
func indexPage(name, now string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>学生选课</title></head>
<body>
<h2>学生选课</h2>
<input type="hidden" id="firstXkkzId" name="firstXkkzId" value="mock-xkkz-id">
<input type="hidden" id="firstXkkzXh" name="firstXkkzXh" value="">
<input type="hidden" id="firstKklxdm" name="firstKklxdm" value="10">
<input type="hidden" id="firstKklxmc" name="firstKklxmc" value="主修">
<input type="hidden" id="firstNjdmId" name="firstNjdmId" value="2024">
<input type="hidden" id="firstZyhId" name="firstZyhId" value="zy-001">
<input type="hidden" id="bh_id" name="bh_id" value="20240001">
<input type="hidden" id="xkxnm" name="xkxnm" value="2025">
<input type="hidden" id="xkxqm" name="xkxqm" value="3">
<input type="hidden" id="xqh_id" name="xqh_id" value="1">
<input type="hidden" id="jg_id_1" name="jg_id_1" value="01">
<input type="hidden" id="xz" name="xz" value="4">
<input type="hidden" id="zyfx_id" name="zyfx_id" value="">
<input type="hidden" id="ccdm" name="ccdm" value="01">
<input type="hidden" id="xbm" name="xbm" value="1">
<input type="hidden" id="mzm" name="mzm" value="01">
<input type="hidden" id="xsbj" name="xsbj" value="01">
<input type="hidden" id="xslbdm" name="xslbdm" value="01">
<input type="hidden" id="xszxzt" name="xszxzt" value="1">
<input type="hidden" id="xxdm" name="xxdm" value="mock">
<input type="hidden" id="zxfs" name="zxfs" value="">
<input type="hidden" id="tkzgcs_qt" name="tkzgcs_qt" value="">
<input type="hidden" id="currentsj" name="currentsj" value="%s">
<input type="hidden" id="iskxk" name="iskxk" value="1">
<input type="hidden" id="xm" name="xm" value="%s">
<h5>总学分最低 <font color="red">2.0</font><font color="red">10.0</font></h5>
<font id="yxxfs">0.0</font>
<ul>
  <li><a href="javascript:void(0)" onclick="queryCourse(this,'10','mock-xkkz-id','2024','zy-001')">主修</a></li>
</ul>
</body></html>`, now, name)
}

// tabPage 是页签参数页，GetCourseTabParams 从这里读取选课限制参数。
func tabPage() string {
	return `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>选课参数</title></head>
<body>
<input type="hidden" id="bklx_id" name="bklx_id" value="1">
<input type="hidden" id="rwlx" name="rwlx" value="1">
<input type="hidden" id="xkly" name="xkly" value="1">
<input type="hidden" id="xklc" name="xklc" value="1">
<input type="hidden" id="xklcmc" name="xklcmc" value="第一轮">
<input type="hidden" id="sfkknj" name="sfkknj" value="0">
<input type="hidden" id="rlkz" name="rlkz" value="1">
<input type="hidden" id="cdrlkz" name="cdrlkz" value="1">
<input type="hidden" id="kkbk" name="kkbk" value="0">
<input type="hidden" id="kkbkdj" name="kkbkdj" value="">
<input type="hidden" id="jxbzcxskg" name="jxbzcxskg" value="0">
<input type="hidden" id="xkxskcgskg" name="xkxskcgskg" value="0">
<input type="hidden" id="sfkcfx" name="sfkcfx" value="0">
<input type="hidden" id="sfkkjyxdxnxq" name="sfkkjyxdxnxq" value="0">
<input type="hidden" id="gnjkxdnj" name="gnjkxdnj" value="">
<input type="hidden" id="sfkkzy" name="sfkkzy" value="0">
<input type="hidden" id="kzybkxy" name="kzybkxy" value="0">
<input type="hidden" id="sfznkx" name="sfznkx" value="0">
<input type="hidden" id="zdkxms" name="zdkxms" value="">
<input type="hidden" id="sfkxq" name="sfkxq" value="0">
<input type="hidden" id="bbhzxjxb" name="bbhzxjxb" value="0">
<input type="hidden" id="rlzlkz" name="rlzlkz" value="0">
<input type="hidden" id="sfkxk" name="sfkxk" value="1">
<input type="hidden" id="sfktk" name="sfktk" value="0">
<input type="hidden" id="jdlx" name="jdlx" value="1">
<input type="hidden" id="syts" name="syts" value="2">
<input type="hidden" id="syxs" name="syxs" value="2">
<input type="hidden" id="xkkssj" name="xkkssj" value="2025-09-01 08:00:00">
<input type="hidden" id="xkjssj" name="xkjssj" value="2025-09-30 23:59:59">
</body></html>`
}
