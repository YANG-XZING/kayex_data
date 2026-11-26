package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"regexp"
	"strconv"
	"strings"

	"kx-opc-client/internal/model"
)

const defaultOPCPort = 4840

var (
	ipRangePattern = regexp.MustCompile(`^((?:\d{1,3}\.){3})(\d{1,3})-(\d{1,3})$`)
)

// Area 按明确的 IPv4 起止范围划分区号，范围包含两个端点。
// 仅在上位机没有配置设备名时，用区号和 IP 最后一段生成备用炉台号。
type Area struct {
	From string `json:"from"`
	To   string `json:"to"`
	Area string `json:"area"`
}

// Furnaces 是炉台清单解析结果。
type Furnaces struct {
	Fixed  []model.Furnace // 单独写出的 IP：一定采集，连不上就一直重连
	Ranged []model.Furnace // IP 范围展开后的 IP：OPC 端口能连通才采集
}

// furnacesFile 对应 furnaces.json。
type furnacesFile struct {
	Port     int      `json:"port"`     // 所有炉台的 OPC 端口，默认 4840
	Areas    []Area   `json:"areas"`    // IP 范围分区规则（可选）
	Furnaces []string `json:"furnaces"` // 单个 IP（如 10.65.108.12）或 IP 范围（如 10.65.108.1-254）
	Exclude  []string `json:"exclude"`  // 不采集的 IP
}

// LoadFurnaces 读取并校验炉台清单。
func LoadFurnaces(path, tablePrefix string) (Furnaces, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Furnaces{}, fmt.Errorf("读取炉台清单: %w", err)
	}
	furnaces, err := ParseFurnaces(raw, tablePrefix)
	if err != nil {
		return Furnaces{}, fmt.Errorf("炉台清单 %s:\n%w", path, err)
	}
	return furnaces, nil
}

// ParseFurnaces 解析炉台清单。格式错误、单个 IP 重复、单个 IP 同时被排除都会报错；
// 范围之间重叠或范围包含单独写出的 IP 时自动去重（单独写出的优先）。
func ParseFurnaces(raw []byte, tablePrefix string) (Furnaces, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var file furnacesFile
	if err := decoder.Decode(&file); err != nil {
		return Furnaces{}, fmt.Errorf("JSON 格式错误: %w", err)
	}
	if file.Port == 0 {
		file.Port = defaultOPCPort
	}

	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if file.Port < 1 || file.Port > 65535 {
		fail("port 不合法: %d", file.Port)
	}
	areas := file.Areas
	for i, a := range areas {
		from, to, ok := areaBounds(a)
		if !ok || a.Area == "" {
			fail("areas[%d]: 必须填写合法的 IPv4 起止范围 from、to 和非空 area，且 from 不能大于 to", i)
			continue
		}
		for j := 0; j < i; j++ {
			otherFrom, otherTo, valid := areaBounds(areas[j])
			if valid && from.Compare(otherTo) <= 0 && otherFrom.Compare(to) <= 0 {
				fail("areas[%d] 与 areas[%d] 的 IP 范围重叠", i, j)
			}
		}
	}

	excluded := map[string]bool{}
	for _, ip := range file.Exclude {
		if !isIPv4(ip) {
			fail("exclude: %q 不是合法的 IPv4 地址", ip)
		}
		excluded[ip] = true
	}

	var (
		result Furnaces
		fixed  = map[string]bool{}
		ranged = map[string]bool{}
	)
	for _, entry := range file.Furnaces {
		if isIPv4(entry) {
			switch {
			case fixed[entry]:
				fail("furnaces: %s 重复", entry)
			case excluded[entry]:
				fail("furnaces: %s 同时出现在 exclude 中", entry)
			default:
				fixed[entry] = true
				result.Fixed = append(result.Fixed, NewFurnace(entry, file.Port, areas, tablePrefix))
			}
			continue
		}
		ips, err := expandRange(entry)
		if err != nil {
			fail("furnaces: %v", err)
			continue
		}
		for _, ip := range ips {
			if ranged[ip] || excluded[ip] {
				continue
			}
			ranged[ip] = true
			result.Ranged = append(result.Ranged, NewFurnace(ip, file.Port, areas, tablePrefix))
		}
	}
	// 范围里与单独写出的 IP 重复的去掉，单独写出的优先
	result.Ranged = removeIPs(result.Ranged, fixed)

	if len(file.Furnaces) == 0 {
		fail("furnaces 为空，至少需要一台炉台")
	}
	if err := errors.Join(errs...); err != nil {
		return Furnaces{}, err
	}
	return result, nil
}

// expandRange 展开 IP 范围，如 10.65.108.1-254；只支持最后一段为范围。
func expandRange(entry string) ([]string, error) {
	m := ipRangePattern.FindStringSubmatch(entry)
	if m == nil || !validOctets(m[1]) {
		return nil, fmt.Errorf("%q 既不是 IPv4 地址，也不是 IP 范围（如 10.65.108.1-254）", entry)
	}
	from, _ := strconv.Atoi(m[2])
	to, _ := strconv.Atoi(m[3])
	if from < 1 || to > 254 || from > to {
		return nil, fmt.Errorf("%q 的范围必须在 1~254 之间，且起始不大于结束", entry)
	}
	ips := make([]string, 0, to-from+1)
	for host := from; host <= to; host++ {
		ips = append(ips, m[1]+strconv.Itoa(host))
	}
	return ips, nil
}

func removeIPs(furnaces []model.Furnace, ips map[string]bool) []model.Furnace {
	kept := furnaces[:0]
	for _, f := range furnaces {
		if !ips[f.IP] {
			kept = append(kept, f)
		}
	}
	return kept
}

// NewFurnace 生成子表名和备用炉台号，如 172.30.3.61 → A061。
func NewFurnace(ip string, port int, areas []Area, tablePrefix string) model.Furnace {
	f := model.Furnace{
		IP:    ip,
		Port:  port,
		Table: strings.ToLower(tablePrefix + strings.ReplaceAll(ip, ".", "_")),
	}
	address, _ := netip.ParseAddr(ip)
	for _, area := range areas {
		from, to, ok := areaBounds(area)
		if ok && address.Is4() && address.Compare(from) >= 0 && address.Compare(to) <= 0 {
			host, _ := strconv.Atoi(ip[strings.LastIndexByte(ip, '.')+1:])
			f.AreaDeviceID = fmt.Sprintf("%s%03d", area.Area, host)
			break
		}
	}
	return f
}

func areaBounds(a Area) (netip.Addr, netip.Addr, bool) {
	from, errFrom := netip.ParseAddr(a.From)
	to, errTo := netip.ParseAddr(a.To)
	return from, to, errFrom == nil && errTo == nil && from.Is4() && to.Is4() && from.Compare(to) <= 0
}

func validOctets(prefix string) bool {
	for _, part := range strings.Split(strings.TrimSuffix(prefix, "."), ".") {
		if n, err := strconv.Atoi(part); err != nil || n > 255 {
			return false
		}
	}
	return true
}

func isIPv4(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && ip.To4() != nil && !strings.Contains(s, ":")
}
