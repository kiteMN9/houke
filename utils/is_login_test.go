package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_User_is_login(t *testing.T) {
	account := "1111111"
	text := `
<html>
<body>
	<input type="hidden" name="rwlx" id="rwlx" value="2"/>
	<input type="hidden" name="kklxpx" id="kklxpx" value="4510"/>
	<input type="hidden" name="xklc" id="xklc" value="1"/>
	<input type="hidden" name="xklcmc" id="xklcmc" value="第1轮"/>
	<input type="hidden" name="bklx_id" id="bklx_id" value="0"/>
	<input type="hidden" name="zckz" id="zckz" value="0"/>
	<input type="hidden" name="sfkxq" id="sfkxq" value="0"/>
	<input type="hidden" name="sfkcfx" id="sfkcfx" value="0"/>
	<input type="hidden" name="kkbk" id="kkbk" value="0"/>
	<input type="hidden" name="kkbkdj" id="kkbkdj" value="0"/>
	<input type="hidden" name="sfkxk" id="sfkxk" value="1"/>
	<input type="hidden" name="sfktk" id="sfktk" value="1"/>
	<input type="hidden" name="txbsfrl" id="txbsfrl" value="0"/>
	<input type="hidden" name="tktjrs" id="tktjrs" value="0"/>
	<input type="hidden" name="rlzlkz" id="rlzlkz" value="1"/>
	<input type="hidden" name="rlkz" id="rlkz" value="0"/>
	<input type="hidden" name="xkly" id="xkly" value="0"/>
	<input type="hidden" name="sfyxsksjct" id="sfyxsksjct" value="0"/>
	<input type="hidden" name="bjgkczxbbjwcx" id="bjgkczxbbjwcx" value="0"/>
	<input type="hidden" name="jspage" id="jspage" value="0"/>
	<input type="hidden" name="globJsPage" id="globJsPage" value="0"/>
	<input type="hidden" name="isEnd" id="isEnd" value="false"/>
</body>
</html>`
	assert.Equal(t, true, UserIsLogin(account, text))
}
