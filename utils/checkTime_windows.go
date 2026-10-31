//go:build windows

package utils

import (
	//"math/rand"
	"crypto/rand"
	"encoding/binary"
	"log"
	"time"

	"github.com/beevik/ntp"
)

// 当构建为32位程序时（只有学校机房会用）一般电脑时间是不准的
func getNTPTime() (time.Time, error) {
	strings := []string{"ntp.aliyun.com", "ntp.tencent.com", "cn.pool.ntp.org", "time.windows.com", "asia.pool.ntp.org", "ntp.ntsc.ac.cn"}

	var n uint32
	err := binary.Read(rand.Reader, binary.LittleEndian, &n)
	if err != nil {
		log.Fatal(err)
	}
	randomIndex := int(n) % len(strings)
	//randomIndex := rand.Intn(len(strings))
	return ntp.Time(strings[randomIndex])
}
