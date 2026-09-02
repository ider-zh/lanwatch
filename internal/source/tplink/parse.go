package tplink

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/ider-zh/lanwatch/internal/source"
)

// anonHost 是路由器对“匿名主机”的占位主机名，应视为无主机名。
const anonHost = "匿名主机"

// linkTypeFor 将 TP-Link type 字段映射为统一 LinkType。
// 与 scripts/tplink_probe.py:LINK_TYPES 一致（"0"=wired, "1"=wifi, "2"=wifi-guest）。
func linkTypeFor(t string) source.LinkType {
	switch t {
	case "0":
		return source.LinkWired
	case "1":
		return source.LinkWifi
	case "2":
		return source.LinkWifiGuest
	default:
		return source.LinkUnknown
	}
}

// clean 递归 URL-decode 所有字符串值（响应字符串是 URL 编码的 UTF-8）。
// 对数字/布尔/空值原样返回。decode 是幂等的：已解码串再次 decode 不变。
func clean(v interface{}) interface{} {
	switch x := v.(type) {
	case string:
		if d, err := url.QueryUnescape(x); err == nil {
			return d
		}
		return x
	case map[string]interface{}:
		m := make(map[string]interface{}, len(x))
		for k, val := range x {
			m[k] = clean(val)
		}
		return m
	case []interface{}:
		s := make([]interface{}, len(x))
		for i, val := range x {
			s[i] = clean(val)
		}
		return s
	default:
		return v
	}
}

// getMap 沿路径取嵌套 map。
func getMap(m map[string]interface{}, path ...string) map[string]interface{} {
	cur := interface{}(m)
	for _, p := range path {
		mm, ok := cur.(map[string]interface{})
		if !ok {
			return nil
		}
		cur = mm[p]
	}
	if out, ok := cur.(map[string]interface{}); ok {
		return out
	}
	return nil
}

// rows 取形如 [{"x_1":{...}}, {"x_2":{...}}] 的表，剥掉外层序号键，返回内层行。
func rows(m map[string]interface{}, path ...string) []map[string]interface{} {
	arr := table(m, path...)
	var out []map[string]interface{}
	for _, it := range arr {
		im, ok := it.(map[string]interface{})
		if !ok {
			continue
		}
		for _, v := range im {
			if rm, ok := v.(map[string]interface{}); ok {
				out = append(out, rm)
			}
		}
	}
	return out
}

// table 沿路径取最终的 []interface{} 数组节点。
func table(m map[string]interface{}, path ...string) []interface{} {
	cur := interface{}(m)
	for _, p := range path {
		mm, ok := cur.(map[string]interface{})
		if !ok {
			return nil
		}
		cur = mm[p]
	}
	if arr, ok := cur.([]interface{}); ok {
		return arr
	}
	return nil
}

// str 取字符串字段（非字符串返回 ""）。
func str(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// parse 把 /ds 的原始响应解析为统一观测列表。
// host_info 为权威在线列表；sys_arp 陈旧，仅用来补齐 host_info 中没有的条目，且不当在线判据。
func (a *Adapter) parse(raw []byte) ([]source.Observation, error) {
	var top map[string]interface{}
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, err
	}
	dec, _ := clean(top).(map[string]interface{})

	lan := getMap(dec, "network", "lan")
	lanIP := str(lan, "ipaddr")
	prefix := ""
	if lanIP != "" {
		if idx := strings.LastIndex(lanIP, "."); idx >= 0 {
			prefix = lanIP[:idx+1]
		}
	}

	// aliases：hosts_info.device 里的自定义名称，优先于 host_info.hostname
	aliases := map[string]string{}
	for _, row := range rows(dec, "hosts_info", "device") {
		mac := source.NormalizeMAC(str(row, "mac"))
		if mac == "" {
			continue
		}
		if name := str(row, "name"); name != "" {
			aliases[mac] = name
		}
	}

	devices := map[string]source.Observation{}
	addDevice := func(o source.Observation) {
		if _, ok := devices[o.MAC]; !ok {
			devices[o.MAC] = o
		}
	}

	// 1) host_info：权威在线列表
	for _, row := range rows(dec, "hosts_info", "host_info") {
		mac := source.NormalizeMAC(str(row, "mac"))
		if mac == "" {
			continue
		}
		ip := str(row, "ip")
		host := str(row, "hostname")
		if a, ok := aliases[mac]; ok && a != "" {
			host = a
		} else if host == anonHost {
			host = ""
		}
		onLAN := prefix != "" && strings.HasPrefix(ip, prefix)
		addDevice(source.Observation{
			MAC:        mac,
			IP:         ip,
			Hostname:   host,
			LinkType:   linkTypeFor(str(row, "type")),
			SSID:       str(row, "ssid"),
			Present:    onLAN,
			Confidence: pick(onLAN, 85, 20),
			SourceID:   a.id,
		})
	}

	// 2) sys_arp：仅补齐 host_info 没有的条目，且不当在线判据
	for _, row := range rows(dec, "ip_mac_bind", "sys_arp") {
		mac := source.NormalizeMAC(str(row, "mac"))
		if mac == "" {
			continue
		}
		if _, ok := devices[mac]; ok {
			continue
		}
		ip := str(row, "ip")
		host := str(row, "hostname")
		if host == anonHost {
			host = ""
		}
		onLAN := prefix != "" && strings.HasPrefix(ip, prefix)
		addDevice(source.Observation{
			MAC:        mac,
			IP:         ip,
			Hostname:   host,
			LinkType:   source.LinkUnknown,
			SSID:       "",
			Present:    onLAN,
			Confidence: pick(onLAN, 60, 25),
			SourceID:   a.id,
		})
	}

	out := make([]source.Observation, 0, len(devices))
	for _, d := range devices {
		out = append(out, d)
	}
	return out, nil
}

func pick(cond bool, a, b int) int {
	if cond {
		return a
	}
	return b
}
