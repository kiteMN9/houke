package cas2

import (
	"testing"
)

//func Test_hex2Base64(*testing.T) {
//	// 测试示例
//	hexStr := "9d7e1d0ad292e45fec8241750ad58374460159dee689b3aa30886224f296a124cbe6da3a46885b1b30f0ad8456de99709c2c9ab1926c618eaf8dd0a3a0bb84e860c19afcfb6cee9110ada490a4ab80254415f39d5f7ce254dde3556207bb2ccb5a8f984d582245220650d83a3714fe2db7ba1266083c7f372f071a38f795608f6e89ca3852321d16b3cf60883e0bfec12c2dee75ae6e3bcb63e3dbd9f42374fcc0d18f47abe0e1e824a1b9e59d41c59e2d860cf83ac20dcf3086ce2cd671161980c07ce201a91d5e73887cd70784f74cbc9fa624209dc6ed5e4e37a1983058139c2828333ec726ad2abacc3229863434bd6eb335e96adbc40483a65076bb43bc"
//	// nX4dCtKS5F/sgkF1CtWDdEYBWd7mibOqMIhiJPKWoSTL5to6RohbGzDwrYRW3plwnCyasZJsYY6vjdCjoLuE6GDBmvz7bO6REK2kkKSrgCVEFfOdX3ziVN3jVWIHuyzLWo+YTVgiRSIGUNg6NxT+Lbe6EmYIPH83LwcaOPeVYI9uico4UjIdFrPPYIg+C/7BLC3uda5uO8tj49vZ9CN0/MDRj0er4OHoJKG55Z1BxZ4thgz4OsINzzCGzizWcRYZgMB84gGpHV5ziHzXB4T3TLyfpiQgncbtXk43oZgwWBOcKCgzPscmrSq6zDIphjQ0vW6zNelq28QEg6ZQdrtDvA==
//	// nX4dCtKS5F/sgkF1CtWDdEYBWd7mibOqMIhiJPKWoSTL5to6RohbGzDwrYRW3plwnCyasZJsYY6vjdCjoLuE6GDBmvz7bO6REK2kkKSrgCVEFfOdX3ziVN3jVWIHuyzLWo+YTVgiRSIGUNg6NxT+Lbe6EmYIPH83LwcaOPeVYI9uico4UjIdFrPPYIg+C/7BLC3uda5uO8tj49vZ9CN0/MDRj0er4OHoJKG55Z1BxZ4thgz4OsINzzCGzizWcRYZgMB84gGpHV5ziHzXB4T3TLyfpiQgncbtXk43oZgwWBOcKCgzPscmrSq6zDIphjQ0vW6zNelq28QEg6ZQdrtDvA==
//	fmt.Printf("完整示例:\n%s\n__RSA__%s\n", hexStr, hexToBase64(hexStr))
//}

func Test_rsa(t *testing.T) {
	// 示例 PEM 公钥（这里需要替换为实际的 PEM 公钥）
	//	publicKeyPEM := `-----BEGIN PUBLIC KEY-----
	//MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEArwU+ygY1ESANVTgMQ0VFFaPqmqgDB84iBNqAuCPAFczr3fmTM179XQyoLDLSPPHeNLaLbc2u2JezURm7wDK4QVEoqpVrRdEgptXhYKhWTfQnyl7Q8px3G2EtC0+zBrU51HVMzUFIW4dstNvbdiAhKldt3tEvqmAuGhlBeDG8nBwuLQBpkt0LvXO9Gkv61L7Ksy2JUBZTlwe3cisl96Zl1uYexvlUvcIb3jPJ8pan/Ku17W/7w6lYFT8tyOGRaMG75pr9RrBLKcy79hkYEx2c/kvWF0AX3ENRiJNq4bnXd/uLbLc78JZ1nqgu6InQFJZvHK7/OrKW50og5QoOLtKblwIDAQAB
	//-----END PUBLIC KEY-----`

	// 从 PEM 创建加密器
	//encryptor, err := NewRSAEncryptorFromPEM(publicKeyPEM)
	//require.NoError(t, err)

	//password := "myPassword123"

	//fmt.Println("=== RSA 加密测试 ===")
	//fmt.Printf("原始密码: %s\n", password)

	// 加密得到 16 进制结果
	//hexResult, err := encryptor.Encrypt(password)
	//require.NoError(t, err)
	//fmt.Printf("16进制结果: %s\n", hexResult)

	// 加密得到 Base64 结果
	//base64Result, err := encryptor.EncryptWithBase64(password)
	//require.NoError(t, err)
	//fmt.Printf("Base64结果: %s\n", base64Result)

	// 测试自定义 Base64 编码
	//customBase64 := hexToBase64Custom(hexResult)
	//fmt.Printf("自定义Base64: %s\n", customBase64)
}
