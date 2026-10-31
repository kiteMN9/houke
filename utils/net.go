package utils

import (
	"fmt"
	"log"
	"net"
	"runtime"
	"strings"
)

func NetCheck() {
	if runtime.GOOS == "android" {
		return
	}
	// 获取所有网络接口
	interfaces, err := net.Interfaces()
	if err != nil {
		//panic(err)
		log.Println(err)
		return
	}

	for _, iface := range interfaces {
		// 跳过回环接口和未启用的接口
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}

		// 获取接口地址列表
		addresses, addrErr := iface.Addrs()
		if addrErr != nil {
			continue
		}

		for _, addr := range addresses {
			// 处理IPNet类型地址（包含掩码）
			switch v := addr.(type) {
			case *net.IPNet:
				ipv4 := v.IP.To4()
				if ipv4 == nil {
					continue // 跳过IPv6地址
				}

				mask := net.IP(v.Mask).To4()
				if mask == nil {
					continue
				}

				// 计算网络地址
				network := net.IP(make([]byte, 4))
				for i := range ipv4 {
					network[i] = ipv4[i] & mask[i]
				}

				// 计算CIDR表示法
				ones, _ := v.Mask.Size()
				cidr := fmt.Sprintf("%s/%d", network.String(), ones)

				// 输出结果
				if strings.Contains(cidr, "10.0.2.") {
					fmt.Printf("接口: %s\n", iface.Name)
					fmt.Printf("所属网段: %s\n\n", cidr)
					log.Printf("接口: %s\n", iface.Name)
					log.Printf("IP地址: %s\n", ipv4)
					log.Printf("子网掩码: %s\n", mask)
					log.Printf("所属网段: %s\n\n", cidr)
					continue
				}
				if !strings.Contains(cidr, "10.0.5.") && !strings.Contains(cidr, "10.0.6.") && !strings.Contains(cidr, "10.0.4.") {
					continue
				}
				//if !strings.Contains(cidr, "10.0.") && !strings.Contains(cidr, "192.168.") && !strings.Contains(cidr, "172.16.") {
				//	continue
				//}
				fmt.Printf("接口: %s\n", iface.Name)
				//fmt.Printf("IP地址: %s\n", ipv4)
				//fmt.Printf("子网掩码: %s\n", mask)
				fmt.Printf("所属网段: %s\n\n", cidr)

				log.Printf("接口: %s\n", iface.Name)
				log.Printf("IP地址: %s\n", ipv4)
				log.Printf("子网掩码: %s\n", mask)
				log.Printf("所属网段: %s\n\n", cidr)
			}
		}
	}
}
