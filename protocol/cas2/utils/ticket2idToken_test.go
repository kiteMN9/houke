package utils

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_extractIDToken(t *testing.T) {
	exp := time.Date(2030, time.January, 2, 3, 4, 5, 0, time.FixedZone("CST", 8*60*60))
	// 票据全是编的：学号、姓名、学校都不指向真人。
	inner := makeJWT(t, map[string]any{
		"sub":                   "20230001",
		"ATTR_name":             "张三",
		"ATTR_identityTypeName": "本科生",
		"ATTR_organizationName": "示例大学",
	})
	outer := makeJWT(t, map[string]any{
		"sub":              "20230001",
		"organizationCode": "99999",
		"idToken":          inner,
		"exp":              exp.Unix(),
	})

	idToken, expTime, account, err := ExtractIDToken(outer)
	require.NoError(t, err)
	assert.Equal(t, inner, idToken)
	// 别拿 time.Local 比墙上时间：CI 跑在 UTC 机器上，比时刻本身才对。
	assert.True(t, exp.Equal(expTime), "exp = %s, want %s", expTime.In(exp.Location()), exp)
	assert.Equal(t, "20230001", account)

	// 内层 idToken 用 base64url，解开后才有姓名 / 学校这些展示信息。
	tk, err := ParseTicket(outer)
	require.NoError(t, err)
	assert.Equal(t, "20230001", tk.Identity.Sub)
	assert.Equal(t, "99999", tk.Identity.OrgCode)
	assert.Equal(t, "张三", tk.Identity.Name)
	assert.Equal(t, "本科生", tk.Identity.Type)
	assert.Equal(t, "示例大学", tk.Identity.Org)
}

// 票据段可能是无填充的 base64url（JWT 规范）、带 padding 的 base64url，
// 也可能混用标准字母表，解码都得认。旧实现先补 '=' 再交给 RawStdEncoding，
// 这种票直接解坏——真实内层 idToken 就中过招。
func TestDecodeJWTPart(t *testing.T) {
	payloads := []struct {
		name string
		raw  []byte
	}{
		{"json", []byte(`{"ATTR_name":"张三"}`)},
		{"url-alphabet", []byte{0xfb, 0xff}}, // base64url 是 "-_8"，且长度 3%4 != 0
	}
	encodings := []struct {
		name string
		enc  *base64.Encoding
	}{
		{"rawurl", base64.RawURLEncoding},
		{"url", base64.URLEncoding},
		{"rawstd", base64.RawStdEncoding},
		{"std", base64.StdEncoding},
	}

	for _, p := range payloads {
		for _, e := range encodings {
			t.Run(p.name+"/"+e.name, func(t *testing.T) {
				got, err := decodeJWTPart(e.enc.EncodeToString(p.raw))
				require.NoError(t, err)
				assert.Equal(t, p.raw, got)
			})
		}
	}
}
