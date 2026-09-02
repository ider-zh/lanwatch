package tplink

// orgAuthPwd 的两个常量，硬编码在路由器 /web-static/lib/slp-ajax.js。
const (
	encKey = "RDpbLfCPsJZ7fiv"
	encDic = "yLwVl0zKqws7LgKPRQ84Mdt708T1qQ3Ha7xv3H7NyU84p21BriUWBU43odz3iP4rBL3cD02KZciX" +
		"TysVXiV8ngg6vL48rPJyAUw0HurW20xqxv9aYb4M9wK1Ae0wlro510qXeU07kV57fQMc8L6aLgML" +
		"wygtc0F10a0Dg70TOoouyFhdysuRMO51yY5ZlOZZLEal1h0t9YQW0Ko7oBwmCAHoic4HYbUyVeU3" +
		"sfQ1xtXcPcf1aT303wAQhv66qzW"
)

// EncodePassword 实现 orgAuthPwd：DIC[(key[i] ^ pwd[i]) % len(DIC)]，较短一侧补 0xBB。
// 与 scripts/tplink_probe.py:encode_password 完全一致。
// 真实路由器密码只经环境变量注入，不在源码/文档中留存（例：DevPass123 -> t7zU2drULeefbwK）。
func EncodePassword(pwd string) string {
	n := len(encDic)
	maxLen := len(encKey)
	if len(pwd) > maxLen {
		maxLen = len(pwd)
	}
	out := make([]byte, 0, maxLen)
	for i := 0; i < maxLen; i++ {
		var left, right byte = 0xBB, 0xBB
		if i < len(encKey) {
			left = encKey[i]
		}
		if i < len(pwd) {
			right = pwd[i]
		}
		out = append(out, encDic[(int(left)^int(right))%n])
	}
	return string(out)
}
