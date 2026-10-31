package protocol

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHasSliderCaptcha(t *testing.T) {
	htm := `
<!doctype html>
<html lang="zh-CN">
<head>
</head>
<body style="background:#fafafa; display: none;">
		<div class="row sl_log_bor4">
			<div class="col-sm-4 sl_log_rt">
					<form class="form-horizontal" role="form" action="/xtgl/login_slogin.html" method="post">
					<input type="hidden" id="csrftoken" name="csrftoken" value="96175c4b-bbeb-4969-a06a-2dc0b64af4d0,96175c4bbbeb4969a06a2dc0b64af4d0"/>
					<input type="hidden" id="language" name="language" value="zh_CN"/>
					<div>
						<div class="tab-content">
							<div id="home" class="tab-pane in active">
								<div class="form-group">
									<div class="input-group">
										<div class="input-group-addon"><img src="/zftal-ui-v5-1.0.2/assets/images/log_ic01.png" width="16" height="16" /></div>
										<input type="text" class="form-control" name="yhm" id="yhm" value="" placeholder="用户名" onblur="" autocomplete="off">
									</div>
								</div>
								<div class="form-group">
									<div class="input-group">
										<div class="input-group-addon"><img src="/zftal-ui-v5-1.0.2/assets/images/log_ic02.png" width="16" height="16" /></div>
										<input type="password" name="mm" id="hidMm" style="display:none" autocomplete="off">
										<input type="text" class="form-control" name="mm" id="mm" value="" placeholder="密码" autocomplete="off"  onfocus="this.type='password'">
										<input type="password" style="display:none;" autocomplete="off">
									</div>
								</div>
								<div class="form-group">
									<button type="button" class="btn btn-primary btn-block" id="dl">登 录</button>
								</div>

								<!-- 登录口提示信息 -->
								 	<div class="form-group">
										<div class="alert alert-danger" role="alert" id="dlktsxx">
										</div>
								    </div>
									<div class="sl_log_ewm hidden-xs"><img src="/zftal-ui-v5-1.0.2/assets/images/login_ewm.gif?t=1791187429088" width="90" height="90" />&nbsp;&nbsp;<span>用手机扫一扫,<br />安全、便捷登录</span></div>
							</div>
							<div id="profile" class="tab-pane" align="center">
									<img id="qrcode" src="" class="img-thumbnail" alt="Cinque Terre" style="weight:220px;height:220px;">
									<img id="qrcodeerror" src="/css/assets/images/erweima.png" class="img-thumbnail" alt="Cinque Terre" style="weight:220px;height:220px;display:none;cursor:pointer;">
									<p class="tipsqrcode"><img id="wxbs" src="/css/assets/images/timg.png" style="weight:15px;height:15px;">微信扫码</p>
									<p class="tipsqrcode">扫码关注公众号进行登录</p>
							</div>
							<div class="qcode" id="qcodepc" style="display:none;">
								<p>统一身份认证</p>
								<div id="qrcode1">
									<div id="qrcode-img" title=""><canvas width="179" height="179" style="display: none;"></canvas>
									<img alt="Scan me!"  src="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAM0AAADNCAYAAAAbvPRpAAAFXElEQVR42u3dQW6EMAwFUO5/6ekFWqkawP52HhI72jAhL4uvxLmu6/qk3L9dXe3eeZf//u3TbVRcSf3XeEMDDTTQQAMNNNBAAw000OxCk9T5Fe/3dBsVAz9pEkvH+kI/QwMNNNBAAw000EADDTTQ7EeT3vlduCb2XzrqivTx5v+DBhpooIEGGmiggQYaaKCBZkonpGOYmEh2jQNooIEGGmiggQYaaKCBBhpoDk7PkpaaJMGseOeud4EGGmiggQYaaKCBBhpooIHmRTRdSU/6MpqJaVfXu3T97QsTLzTQQAMNNNBAAw000EADzS406RU2Pec5ZWkNDs9BA43noIHGc9BA4zlovrw/4VfScpaK/zfxd0xMxW71PTTQQAMNNNBAAw000EADzWQ0FWdBphfleHpgOQajZp9MRRslm9CggQYaaKCBBhpooIEGGmgmo0kaqBXpVNJvSz/6JGlSvPm30EADDTTQQAMNNNBAAw00c9F0Le9YNOscdZZmehpXghoaaKCBBhpooIEGGmiggWYbmvRKkulFG5LSuIrnugC3pZnQQAMNNNBAAw000EADDTTb0CSlZyVpyMDjI9LP/5yICxpooIEGGmiggQYaaKCB5gg0XS/dldolFcKo6NPN36PiggYaaKCBBhpooIEGGmigOQJNRSq2eb/KRHCb233h+0IDDTTQQAMNNNBAAw000OxCk7TPY+JenK60K2mQp5+LCg000EADDTTQQAMNNNBAcyyaRq3RZ1UmpXvpZ5E+DTMJOjTQQAMNNNBAAw000EADzRFo0pOPiQlY1+BNh5S+jObX56CBBhpooIEGGmiggQYaaCaj2bI3ZUuylTTYNlcevdkGNNBAAw000EADDTTQQAPNLjRdg7xrwLSd3Rg+IVS0O3GygwYaaKCBBhpooIEGGmigWYdmy/KOift4tqR2SZVHoYEGGmiggQYaaKCBBhpooPkiPUtPnbqW+XSldgNKt8akhSVlaaGBBhpooIEGGmiggQYaaLah2YIrKbVLTxrTlzpVjNM/noMGGmiggQYaaKCBBhpooNmFJn1ZycS0ayKQpD4NmxCggQYaaKCBBhpooIEGGmjmovk8fKUX4Jj40btSoonfvOL9oIEGGmiggQYaaKCBBhpojkAzsYBE0gdJGvhb9rpAAw000EADDTTQQAMNNNBA89W9N9lKSmGSBtvEpT9d4woaaKCBBhpooIEGGmiggWYdmqSOST+vM2n5SVIKeFwb0EADDTTQQAMNNNBAAw00s9vISYTSB356BcsuXEkTQtFyL2iggQYaaKCBBhpooIEGmrlokj5IxYfrSsoGzJ7R52Z2jQNooIEGGmiggQYaaKCBBpp1aNILG3SB6/ptXeljeoGQsP6DBhpooIEGGmiggQYaaKCZiyY96Uk/r7NriVBSwhlW/fL9GxpooIEGGmiggQYaaKCBZhua9CIa6Xs6kgZ5ehpXcT8+/qCBBhpooIEGGmiggQYaaLahSbqSOjVpGUhXAjZgr8v7+4eggQYaaKCBBhpooIEGGmgmo9mScqTvOUlakpI+sTVieL8sLTTQQAMNNNBAAw000EADTSKarlSsIl1JWmoyMY3bkp7dfGdooIEGGmiggQYaaKCBBpr9aJKqMnZ1VtfgSC/8kZSOFr0fNNBAAw000EADDTTQQAMNNFOKY2yBngRuS7VPaKCBBhpooIEGGmiggQYaaAah6bomnq+ZPomFAYEGGmiggQYaaKCBBhpooDkTTXrKkTTwJ+JK7/skhNBAAw000EADDTTQQAMNNOvQbDkbEZC+o0qSJs8Xvi800EADDTTQQAMNNNBAA81YND9MXR4t28yoBgAAAABJRU5ErkJggg==">
									</div>
								</div>
								<div id="qrcode-content" style="display:none;">
									<div class="qrcode-bg"></div>
									<p class="refresh-text">
										<i class="fa fa-refresh"></i>
										<a href="javascript:void(0);" id="qrcode_refresh">刷新 </a>
									</p>
								</div>
								<span>打开教务app<br>在【首页】页面上打开扫一扫</span>
							</div>
						</div>
					</div>
				</form>
			</div>
		</div>
	</div>

<div id="footerID" class="footer"  style="background-color: " >
	<p>版权所有&#169; Copyright 1999-2023 合肥城市学院&nbsp;&nbsp;&nbsp;版本V-8.0.0</p>
</div>
</body>
</html>`

	htm2 := `

<!doctype html>
<!-- <html lang="zh-CN" class="tb-allpage-filter">  -->
<html lang="zh-CN">
<head>
</head>
<body style="background:#fafafa; display: none;">
<div class="container container_1170">
		</div>
		<div class="row sl_log_bor4">
			<div class="col-sm-4 sl_log_rt">
					<form class="form-horizontal" role="form" action="/xtgl/login_slogin.html" method="post">
					<input type="hidden" id="csrftoken" name="csrftoken" value="3ea84510-ac16-4e70-9ae0-81f178a582ce,3ea84510ac164e709ae081f178a582ce"/>
					<input type="hidden" id="language" name="language" value="zh_CN"/>
					<input type="hidden" name="ydType" id="ydType" value= >
					<div>
						<div class="tab-content">
							<div id="home" class="tab-pane in active">
								<div class="form-group">
									<div class="input-group">
										<div class="input-group-addon"><img src="/zftal-ui-v5-1.0.2/assets/images/log_ic01.png" width="16" height="16" /></div>
										<input type="text" class="form-control" name="yhm" id="yhm" value="" placeholder="用户名" onblur="" autocomplete="off">
									</div>
								</div>
								<div class="form-group">
									<div class="input-group">
										<div class="input-group-addon"><img src="/zftal-ui-v5-1.0.2/assets/images/log_ic02.png" width="16" height="16" /></div>
										<input type="password" name="mm" id="hidMm" style="display:none" autocomplete="off">
										<input type="text" class="form-control" name="mm" id="mm" value="" placeholder="密码" autocomplete="off"  onfocus="this.type='password'">
										<input type="password" style="display:none;" autocomplete="off">
									</div>
								</div>
										<script type='text/javascript' src='/zfcaptchaLogin?type=resource&instanceId=zfcaptchaLogin&name=zfJQuery-1.11.1-min.js'></script>
<div class='captcha_wrapper'>
</div>
<link rel='stylesheet' href='/zfcaptchaLogin?type=resource&instanceId=zfcaptchaLogin&name=zfdun_captcha.css' />
<script type='text/javascript' src='/zfcaptchaLogin?type=resource&instanceId=zfcaptchaLogin&name=zfdun_captcha.js'></script>
								 <div class="form-group" id = "myDiv">
									<!--<a href="/xtgl/init_fkIndex.html" class="checkbox pull-left" target="_blank">访客登录</a> -->
									   <a href="/pwdmgr/retake/index.zf" id="wjmm" class="checkbox pull-right " target="_blank">忘记密码？</a>
								</div>
								<div class="form-group">
									<button type="button" class="btn btn-primary btn-block" id="dl">登 录</button>
								</div>
										<div class = "form-group">
											   <a href="https://jwglxt.ycit.edu.cn/sso/hnyyxyiotlogin" id="jtLoginUrl" class="checkbox pull-right">跳转到统一身份认证平台登录</a>
										</div>
							</div>
							<div id="profile" class="tab-pane" align="center">
									<img id="qrcode" src="" class="img-thumbnail" alt="Cinque Terre" style="weight:220px;height:220px;">
									<img id="qrcodeerror" src="/css/assets/images/erweima.png" class="img-thumbnail" alt="Cinque Terre" style="weight:220px;height:220px;display:none;cursor:pointer;">
									<p class="tipsqrcode"><img id="wxbs" src="/css/assets/images/timg.png" style="weight:15px;height:15px;">微信扫码</p>
									<p class="tipsqrcode">扫码关注公众号进行登录</p>
							</div>
							<div class="qcode" id="qcodepc" style="display:none;">
								<p>统一身份认证</p>
								<div id="qrcode1">
									<div id="qrcode-img" title=""><canvas width="179" height="179" style="display: none;"></canvas>
									<img alt="Scan me!"  src="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAM0AAADNCAYAAAAbvPRpAAAFXElEQVR42u3dQW6EMAwFUO5/6ekFWqkawP52HhI72jAhL4uvxLmu6/qk3L9dXe3eeZf//u3TbVRcSf3XeEMDDTTQQAMNNNBAAw000OxCk9T5Fe/3dBsVAz9pEkvH+kI/QwMNNNBAAw000EADDTTQ7EeT3vlduCb2XzrqivTx5v+DBhpooIEGGmiggQYaaKCBZkonpGOYmEh2jQNooIEGGmiggQYaaKCBBhpoDk7PkpaaJMGseOeud4EGGmiggQYaaKCBBhpooIHmRTRdSU/6MpqJaVfXu3T97QsTLzTQQAMNNNBAAw000EADzS406RU2Pec5ZWkNDs9BA43noIHGc9BA4zlovrw/4VfScpaK/zfxd0xMxW71PTTQQAMNNNBAAw000EADzWQ0FWdBphfleHpgOQajZp9MRRslm9CggQYaaKCBBhpooIEGGmgmo0kaqBXpVNJvSz/6JGlSvPm30EADDTTQQAMNNNBAAw00c9F0Le9YNOscdZZmehpXghoaaKCBBhpooIEGGmiggWYbmvRKkulFG5LSuIrnugC3pZnQQAMNNNBAAw000EADDTTb0CSlZyVpyMDjI9LP/5yICxpooIEGGmiggQYaaKCB5gg0XS/dldolFcKo6NPN36PiggYaaKCBBhpooIEGGmigOQJNRSq2eb/KRHCb233h+0IDDTTQQAMNNNBAAw000OxCk7TPY+JenK60K2mQp5+LCg000EADDTTQQAMNNNBAcyyaRq3RZ1UmpXvpZ5E+DTMJOjTQQAMNNNBAAw000EADzRFo0pOPiQlY1+BNh5S+jObX56CBBhpooIEGGmiggQYaaCaj2bI3ZUuylTTYNlcevdkGNNBAAw000EADDTTQQAPNLjRdg7xrwLSd3Rg+IVS0O3GygwYaaKCBBhpooIEGGmigWYdmy/KOift4tqR2SZVHoYEGGmiggQYaaKCBBhpooPkiPUtPnbqW+XSldgNKt8akhSVlaaGBBhpooIEGGmiggQYaaLah2YIrKbVLTxrTlzpVjNM/noMGGmiggQYaaKCBBhpooNmFJn1ZycS0ayKQpD4NmxCggQYaaKCBBhpooIEGGmjmovk8fKUX4Jj40btSoonfvOL9oIEGGmiggQYaaKCBBhpojkAzsYBE0gdJGvhb9rpAAw000EADDTTQQAMNNNBA89W9N9lKSmGSBtvEpT9d4woaaKCBBhpooIEGGmiggWYdmqSOST+vM2n5SVIKeFwb0EADDTTQQAMNNNBAAw00s9vISYTSB356BcsuXEkTQtFyL2iggQYaaKCBBhpooIEGmrlokj5IxYfrSsoGzJ7R52Z2jQNooIEGGmiggQYaaKCBBpp1aNILG3SB6/ptXeljeoGQsP6DBhpooIEGGmiggQYaaKCZiyY96Uk/r7NriVBSwhlW/fL9GxpooIEGGmiggQYaaKCBZhua9CIa6Xs6kgZ5ehpXcT8+/qCBBhpooIEGGmiggQYaaLahSbqSOjVpGUhXAjZgr8v7+4eggQYaaKCBBhpooIEGGmgmo9mScqTvOUlakpI+sTVieL8sLTTQQAMNNNBAAw000EADTSKarlSsIl1JWmoyMY3bkp7dfGdooIEGGmiggQYaaKCBBpr9aJKqMnZ1VtfgSC/8kZSOFr0fNNBAAw000EADDTTQQAMNNFOKY2yBngRuS7VPaKCBBhpooIEGGmiggQYaaAah6bomnq+ZPomFAYEGGmiggQYaaKCBBhpooDkTTXrKkTTwJ+JK7/skhNBAAw000EADDTTQQAMNNOvQbDkbEZC+o0qSJs8Xvi800EADDTTQQAMNNNBAA81YND9MXR4t28yoBgAAAABJRU5ErkJggg==">
									</div>
								</div>
								<div id="qrcode-content" style="display:none;">
									<div class="qrcode-bg"></div>
									<p class="refresh-text">
										<i class="fa fa-refresh"></i>
										<a href="javascript:void(0);" id="qrcode_refresh">刷新 </a>
									</p>
								</div>
								<span>打开教务app<br>在【首页】页面上打开扫一扫</span>
							</div>
						</div>
					</div>
				</form>
			</div>
		</div>
	</div>
<div id="footerID" class="footer"  style="background-color: " >
	<p>版权所有&#169; Copyright 盐城工学院&nbsp;&nbsp;&nbsp;版本V-9.0</p>
</div>
</html>`

	htm3 := `

<!doctype html>
<!-- <html lang="zh-CN" class="tb-allpage-filter">  -->
<html lang="zh-CN">
<head>


<title>教学管理信息服务平台</title>

<![endif]-->

</head>
<body style="background:#fafafa; display: none;">
<input type="hidden" name="dlmmsfknt" id="dlmmsfknt" value= 1>
<input type="hidden" name="ydappsfbldlzt" id="ydappsfbldlzt" value= 0>
<input type="hidden" name="yzcskz" id="yzcskz" value= -1>
<input type="hidden" name="mmsfjm" id="mmsfjm" value= 1>
<input type="hidden" name="mmsrddcshkzfs" id="mmsrddcshkzfs" value= 0>
<input type="hidden" name="dlsbsdsj" id="dlsbsdsj" value= 3>
<input type="hidden" name="sfxswechatlogin" id="sfxswechatlogin" value= 0>
<input type="hidden" name="scene_id" id="scene_id" value= >
<input type="hidden" name="authJwglxtLoginURL" id="authJwglxtLoginURL" value= https://jwxt.stbu.edu.cn/sso/mhtloginc>
<input type="hidden" name="rzzxxs" id="rzzxxs" value= 1>
<input type="hidden" name="rzys" id="rzys" value=  #880000>
<input type="hidden" name="sfzywqh" id="sfzywqh" value= 0>
<input type="hidden" name="xxdm" id="xxdm" value= 13672>
<input type="hidden" name="userPolicyOn" id="userPolicyOn" value= 0>
<input type="hidden" name="dxsyrz" id="dxsyrz" value= 0>

<div class="container container_1170">
	<div class="row sl_log_top">
		<div class="col-sm-8 logo_1"><img src="/zftal-ui-v5-1.0.2/assets/images/logo/logo_jw_d.png?t=1791301506472" style="margin-top:-3px"/>
				<span id="xtmc">四川工商学院教学综合信息管理平台</span>
		</div>
		<div class="col-sm-4 text-right">

			</div>
		</div>
		<div class="row sl_log_bor4">

			<div class="col-sm-8 hidden-xs sl_log_lf slideShow">
				<!-- 0不轮换图片-->
				<img class="img-responsive" src="/xtgl/dlycssz_cxDlycsszZp.html?zplx=3&t=1791301506472"/>

			</div>
			<div class="col-sm-4 sl_log_rt">
					<form class="form-horizontal" role="form" action="/xtgl/login_slogin.html" method="post">
					<input type="hidden" id="csrftoken" name="csrftoken" value="10fd7962-d286-4fb7-9d9e-b15bfc6163fd,10fd7962d2864fb79d9eb15bfc6163fd"/>
					<input type="hidden" id="language" name="language" value="zh_CN"/>
					<input type="hidden" name="ydType" id="ydType" value= >
					<div>
						
						<div class="tab-content">
							
							<div id="home" class="tab-pane in active">
								
									<h5>用户登录</h5>
								
								<!-- 防止浏览器自动填充密码 -->
								<input type="text" style="display: none;" autocomplete="off"/>
								<input type="password" style="display: none;" autocomplete="off"/>
								<!-- 防止浏览器自动填充密码 end -->
									<p style="display: none;" id="tips" class="bg_danger sl_danger">
									</p>
								
								<div class="form-group">
									<div class="input-group">
										<div class="input-group-addon"><img src="/zftal-ui-v5-1.0.2/assets/images/log_ic01.png" width="16" height="16" /></div>
										<input type="text" class="form-control" name="yhm" id="yhm" value="" placeholder="用户名" onblur="" autocomplete="off">
									</div>
								</div>
								<div class="form-group">
									<div class="input-group">
										<div class="input-group-addon"><img src="/zftal-ui-v5-1.0.2/assets/images/log_ic02.png" width="16" height="16" /></div>
										<input type="password" name="mm" id="hidMm" style="display:none" autocomplete="off">
										<input type="text" class="form-control" name="mm" id="mm" value="" placeholder="密码" autocomplete="off"  onfocus="this.type='password'">
										<input type="password" style="display:none;" autocomplete="off">
									</div>
								</div>

								<!-- 0-弹出验证码-->
									
										<div id="yzmDiv" class="form-group">
											<div class="input-group col-xs-4 pull-left">
												<input name="yzm" type="text" id="yzm" class="form-control" placeholder="验证码">
											</div>
											<div class="input-group col-xs-4 col-xs-offset-5">
												<img border="0" align="absmiddle" id="yzmPic" style="cursor: hand;" onclick="javascript:refreshCode();" name="yzmPic" width="108" height="34" />
											</div>
										</div>

								<div class="form-group">
									<button type="button" class="btn btn-primary btn-block" id="dl">登 录</button>
								</div>

										<div class = "form-group">
												<button type="button" class="btn btn-primary btn-block rzdl" id="tysfyzdl">统一身份认证登录</button>
												<a href="https://jwxt.stbu.edu.cn/sso/mhtloginc" id="authJwglxtLoginURL" class="checkbox pull-right"></a>
											
										</div>

								<!-- 登录口提示信息 -->
								 	<div class="form-group">
										<div class="alert alert-danger" role="alert" id="dlktsxx">
										</div>
								    </div>
							</div>
							<div id="profile" class="tab-pane" align="center">
									<img id="qrcode" src="" class="img-thumbnail" alt="Cinque Terre" style="weight:220px;height:220px;">
									<img id="qrcodeerror" src="/css/assets/images/erweima.png" class="img-thumbnail" alt="Cinque Terre" style="weight:220px;height:220px;display:none;cursor:pointer;">
									<p class="tipsqrcode"><img id="wxbs" src="/css/assets/images/timg.png" style="weight:15px;height:15px;">微信扫码</p>
									<p class="tipsqrcode">扫码关注公众号进行登录</p>
							</div>
							<div class="qcode" id="qcodepc" style="display:none;">
								<p>统一身份认证</p>
								<div id="qrcode1">
									<div id="qrcode-img" title=""><canvas width="179" height="179" style="display: none;"></canvas>
									<img alt="Scan me!"  src="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAM0AAADNCAYAAAAbvPRpAAAFXElEQVR42u3dQW6EMAwFUO5/6ekFWqkawP52HhI72jAhL4uvxLmu6/qk3L9dXe3eeZf//u3TbVRcSf3XeEMDDTTQQAMNNNBAAw000OxCk9T5Fe/3dBsVAz9pEkvH+kI/QwMNNNBAAw000EADDTTQ7EeT3vlduCb2XzrqivTx5v+DBhpooIEGGmiggQYaaKCBZkonpGOYmEh2jQNooIEGGmiggQYaaKCBBhpoDk7PkpaaJMGseOeud4EGGmiggQYaaKCBBhpooIHmRTRdSU/6MpqJaVfXu3T97QsTLzTQQAMNNNBAAw000EADzS406RU2Pec5ZWkNDs9BA43noIHGc9BA4zlovrw/4VfScpaK/zfxd0xMxW71PTTQQAMNNNBAAw000EADzWQ0FWdBphfleHpgOQajZp9MRRslm9CggQYaaKCBBhpooIEGGmgmo0kaqBXpVNJvSz/6JGlSvPm30EADDTTQQAMNNNBAAw00c9F0Le9YNOscdZZmehpXghoaaKCBBhpooIEGGmiggWYbmvRKkulFG5LSuIrnugC3pZnQQAMNNNBAAw000EADDTTb0CSlZyVpyMDjI9LP/5yICxpooIEGGmiggQYaaKCB5gg0XS/dldolFcKo6NPN36PiggYaaKCBBhpooIEGGmigOQJNRSq2eb/KRHCb233h+0IDDTTQQAMNNNBAAw000OxCk7TPY+JenK60K2mQp5+LCg000EADDTTQQAMNNNBAcyyaRq3RZ1UmpXvpZ5E+DTMJOjTQQAMNNNBAAw000EADzRFo0pOPiQlY1+BNh5S+jObX56CBBhpooIEGGmiggQYaaCaj2bI3ZUuylTTYNlcevdkGNNBAAw000EADDTTQQAPNLjRdg7xrwLSd3Rg+IVS0O3GygwYaaKCBBhpooIEGGmigWYdmy/KOift4tqR2SZVHoYEGGmiggQYaaKCBBhpooPkiPUtPnbqW+XSldgNKt8akhSVlaaGBBhpooIEGGmiggQYaaLah2YIrKbVLTxrTlzpVjNM/noMGGmiggQYaaKCBBhpooNmFJn1ZycS0ayKQpD4NmxCggQYaaKCBBhpooIEGGmjmovk8fKUX4Jj40btSoonfvOL9oIEGGmiggQYaaKCBBhpojkAzsYBE0gdJGvhb9rpAAw000EADDTTQQAMNNNBA89W9N9lKSmGSBtvEpT9d4woaaKCBBhpooIEGGmiggWYdmqSOST+vM2n5SVIKeFwb0EADDTTQQAMNNNBAAw00s9vISYTSB356BcsuXEkTQtFyL2iggQYaaKCBBhpooIEGmrlokj5IxYfrSsoGzJ7R52Z2jQNooIEGGmiggQYaaKCBBpp1aNILG3SB6/ptXeljeoGQsP6DBhpooIEGGmiggQYaaKCZiyY96Uk/r7NriVBSwhlW/fL9GxpooIEGGmiggQYaaKCBZhua9CIa6Xs6kgZ5ehpXcT8+/qCBBhpooIEGGmiggQYaaLahSbqSOjVpGUhXAjZgr8v7+4eggQYaaKCBBhpooIEGGmgmo9mScqTvOUlakpI+sTVieL8sLTTQQAMNNNBAAw000EADTSKarlSsIl1JWmoyMY3bkp7dfGdooIEGGmiggQYaaKCBBpr9aJKqMnZ1VtfgSC/8kZSOFr0fNNBAAw000EADDTTQQAMNNFOKY2yBngRuS7VPaKCBBhpooIEGGmiggQYaaAah6bomnq+ZPomFAYEGGmiggQYaaKCBBhpooDkTTXrKkTTwJ+JK7/skhNBAAw000EADDTTQQAMNNOvQbDkbEZC+o0qSJs8Xvi800EADDTTQQAMNNNBAA81YND9MXR4t28yoBgAAAABJRU5ErkJggg==">
									</div>
								</div>
								<div id="qrcode-content" style="display:none;">
									<div class="qrcode-bg"></div>
									<p class="refresh-text">
										<i class="fa fa-refresh"></i>
										<a href="javascript:void(0);" id="qrcode_refresh">刷新 </a>
									</p>
								</div>
								<span>打开教务app<br>在【首页】页面上打开扫一扫</span>
							</div>
						</div>
					</div>
				</form>
			</div>
		</div>
		
	</div>

<div id="footerID" class="footer"  style="background-color: " >

	<p>版权所有&#169; Copyright 1999-2026 正方软件股份有限公司　　中国·杭州西湖区三墩镇数治路99号901室&nbsp;&nbsp;&nbsp;版本V-9.0</p>
</div>

<!-- footer-end -->
</body>

</html>`

	cases := []struct {
		name string
		html string
		want bool
	}{
		{
			name: "passwordLogin",
			html: htm,
			want: false,
		},
		{
			name: "sliderCaptcha",
			html: htm2,
			want: true,
		},
		{
			name: "charCaptcha",
			html: htm3,
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc, err := goquery.NewDocumentFromReader(strings.NewReader(c.html))
			require.NoError(t, err)
			assert.Equal(t, c.want, hasSliderCap(doc))
		})
	}
}

// 302 回到登录页时用于修正 baseURL：只取 LoginIndex 之前的部分，
// /jwglxt 这类子路径要保留，否则重试会打到站点根上。
func TestCorrectedBaseURL(t *testing.T) {
	cases := []struct {
		name     string
		current  string
		location string
		want     string
	}{
		{"相对地址带子路径", "https://jwglxt.ycit.edu.cn/", "/jwglxt/xtgl/login_slogin.html?time=1", "https://jwglxt.ycit.edu.cn/jwglxt/"},
		{"绝对地址带子路径", "https://jwglxt.ycit.edu.cn/", "https://jwglxt.ycit.edu.cn/jwglxt/xtgl/login_slogin.html", "https://jwglxt.ycit.edu.cn/jwglxt/"},
		{"已在子路径下", "https://jwglxt.ycit.edu.cn/jwglxt/", "/jwglxt/xtgl/login_slogin.html", "https://jwglxt.ycit.edu.cn/jwglxt/"},
		{"根路径", "https://jwglxt.ycit.edu.cn/jwglxt/", "/xtgl/login_slogin.html", "https://jwglxt.ycit.edu.cn/"},
		{"不是登录页", "https://jwglxt.ycit.edu.cn/", "/xtgl/index_initMenu.html", ""},
		{"无法解析", "://bad", "://bad", ""},
	}
	for _, c := range cases {
		if got := correctedBaseURL(c.current, c.location); got != c.want {
			t.Errorf("%s: correctedBaseURL(%q, %q) = %q, want %q", c.name, c.current, c.location, got, c.want)
		}
	}
}
