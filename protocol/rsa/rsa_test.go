package rsa

//func Test_RsaEncrypt(t *testing.T) {
//	// Base64编码的公钥模数和指数
//	modulusBase64 := "AKaDZR0GA7V7IokWdV+r7J3QgV8ovozuHgFrShFVbQQJYf1+FjIlcxgHP1BFOv4efIgJ0yOm7e1rX0MqaA2974rbAojpXOWKdbt7OGP53wOhryeOkd9GYB6EmbUYT4bnWR94ALyYNTecE73rJrCTgd3hMI4FMxsyp2k0TuU6OYMN"
//	exponentBase64 := "AQAB"
//	secret := "12345678"
//	result, err := EncryptRsa(modulusBase64, exponentBase64, secret)
//	require.NoError(t, err)
//	fmt.Println(result)
//}

//func Test_RsaPEMEncrypt(*testing.T) {
//	pubKey := `-----BEGIN PUBLIC KEY-----
//MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEArwU+ygY1ESANVTgMQ0VFFaPqmqgDB84iBNqAuCPAFczr3fmTM179XQyoLDLSPPHeNLaLbc2u2JezURm7wDK4QVEoqpVrRdEgptXhYKhWTfQnyl7Q8px3G2EtC0+zBrU51HVMzUFIW4dstNvbdiAhKldt3tEvqmAuGhlBeDG8nBwuLQBpkt0LvXO9Gkv61L7Ksy2JUBZTlwe3cisl96Zl1uYexvlUvcIb3jPJ8pan/Ku17W/7w6lYFT8tyOGRaMG75pr9RrBLKcy79hkYEx2c/kvWF0AX3ENRiJNq4bnXd/uLbLc78JZ1nqgu6InQFJZvHK7/OrKW50og5QoOLtKblwIDAQAB
//-----END PUBLIC KEY-----`
//
//	// 5. 准备要加密的数据
//	secretMessage := []byte("这是需要加密的敏感信息")
//	encrypt, err := PEMEncrypt(pubKey, secretMessage)
//	if err != nil {
//		return
//	}
//	fmt.Printf("加密结果: %x\n", encrypt)
//	//fmt.Println(string(encrypt))
//}
