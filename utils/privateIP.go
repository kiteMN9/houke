package utils

import (
	"net"
)

var tenNet *net.IPNet

func init() {
	_, tenNet, _ = net.ParseCIDR("10.0.0.0/8")
}

func Is10PrivateIP(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	return tenNet.Contains(ip)
}

//func main() {
//	fmt.Println(Is10PrivateIP("10.0.0.1"))       // true
//	fmt.Println(Is10PrivateIP("10.255.255.255")) // true
//	fmt.Println(Is10PrivateIP("192.168.1.1"))    // false
//	fmt.Println(Is10PrivateIP("11.0.0.1"))       // false
//}
