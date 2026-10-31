package protocol

//func TestNewClientWithCookie(t *testing.T) {
//	baseUrl := ""
//	config := config2.Data{
//		BaseURL:   "https://jwglxt.ycit.edu.cn/",
//		Account:   "account",
//		Passwd:    "password",
//		CasPasswd: "cas2password",
//		UserAgent: baseCfg.FireFoxUA,
//		CasLogin:  false,
//	}
//
//	cookie := &http.Cookie{ // 直连教务系统只有这个
//		Name:  "JSESSIONID",
//		Value: "",
//	}
//	cookie2 := &http.Cookie{ // 过 nginx有这个
//		Name:  "route",
//		Value: "",
//	}
//	cookies := []*http.Cookie{cookie, cookie2}
//	jar, err := cookiejar.New(nil)
//	if err != nil {
//		log.Fatal(err)
//	}
//	u, _ := url.Parse(baseUrl)
//	jar.SetCookies(u, cookies)
//	NewClientWithCookieJar(&config, 15*time.Second, jar)
//}
