package utils

import (
	"fmt"
	"math/rand"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// 允许的命名
var NamePattern = "[a-zA-Z][a-zA-Z0-9_]*"

// 列名大小写要求的正则（规则 limit_col_name_case 的 name_case 参数），
// 与 NamePattern 的字符集约定一致（字母开头 + 字母/数字/下划线），仅收紧大小写
var (
	LowerNamePattern = "[a-z][a-z0-9_]*"
	UpperNamePattern = "[A-Z][A-Z0-9_]*"
)

const KeyJoinChar = "+"

// IsSubKey
func IsSubKey(keyA, keyB string) bool {
	var short, long string
	if len(keyA) < len(keyB) {
		short, long = keyA, keyB
	} else {
		short, long = keyB, keyA
	}
	shortSet := strings.Split(short, KeyJoinChar)
	longSet := strings.Split(long, KeyJoinChar)

	for i := 0; i < len(shortSet); i++ {
		if shortSet[i] != longSet[i] {
			return false
		}
	}
	return true
}

// IsByteContain
func IsByteContain(items []byte, item byte) bool {
	for _, eachItem := range items {
		if eachItem == item {
			return true
		}
	}
	return false
}

// HasPrefix 不区分大小写
func HasPrefix(s, prefix string, caseSensitive bool) bool {
	if caseSensitive {
		//  区分大小写
		return strings.HasPrefix(s, prefix)
	}
	return strings.HasPrefix(strings.ToLower(s), strings.ToLower(prefix))
}

// IsContain等值比较，忽略大小写
func IsContain(items []string, item string) bool {
	for _, eachItem := range items {
		if strings.EqualFold(eachItem, item) {
			return true
		}
	}
	return false
}

func ErrsJoin(str string, err []error) string {
	if len(err) < 1 {
		return ""
	}
	result := ""
	for i, v := range err {
		if v == nil {
			continue
		}

		if i == 0 {
			result += v.Error()
			continue
		}
		result += v.Error() + str
	}
	return result
}

// 去重
func RemoveDuplicate(s []string) []string {
	result := []string{}
	temp := map[string]struct{}{}
	for _, item := range s {
		if _, ok := temp[item]; !ok {
			temp[item] = struct{}{}
			result = append(result, item)
		}
	}
	return result
}

// 查重
func IsRepeat(items []string) (bool, []string) {
	repeat := make(map[string]bool)
	var repeatValue []string
	for _, item := range items {
		itemLower := strings.ToLower(item)
		if !repeat[itemLower] {
			repeat[itemLower] = true
		} else {
			repeatValue = append(repeatValue, item)
		}
	}
	if len(repeatValue) > 0 {
		return true, repeatValue
	}
	return false, repeatValue
}

// 获取最大值
func MaxInt(items []int) int {
	maxVal := items[0]
	for i := 1; i < len(items); i++ {
		//从第二个 元素开始循环比较，如果发现有更大的，则交换
		if maxVal < items[i] {
			maxVal = items[i]
		}
	}
	return maxVal
}

func RemoveElements(arr []string, toRemove []string) []string {
	// 创建一个map，用于快速查找要删除的元素
	removeMap := make(map[string]bool)
	for _, val := range toRemove {
		removeMap[val] = true
	}

	// 创建一个新的切片，用于存储不包含要删除元素的值
	result := []string{}

	// 遍历原始切片，将不在要删除元素中的值添加到新的切片中
	for _, val := range arr {
		if !removeMap[val] {
			result = append(result, val)
		}
	}

	return result
}

// 判断字符串是否匹配正则
func IsMatchPattern(pattern string, str string) bool {
	regCom := regexp.MustCompile(pattern)
	indices := regCom.FindAllStringIndex(str, -1)
	// 完全无匹配（如大写要求下的小写列名、纯中文名）视为不匹配，
	// 不能因循环未执行而误返回 true
	if len(indices) == 0 {
		return false
	}
	for _, indice := range indices {
		start, end := indice[0], indice[1]
		if unicode.IsDigit(rune(str[0])) {
			// 不能以数字开头
			return false
		} else {
			// 发现异常的字符
			if start != 0 || end != len(str) {
				return false
			}
		}
	}
	return true
}

// ---------- 索引/对象命名要求（MySQL 与 PG 引擎共用同一判定口径） ----------

// MatchIndexName 判定索引名是否满足规范要求，MySQL/PG 两套引擎共用，保证口径一致。
//
// 取值优先级：
//  1. 配置了正则表达式（regex 非空）=> 以正则为准，且要求「完整匹配」
//     （等价于 ^(?:regex)$）。不能直接用 regexp.MatchString，它是子串命中：
//     uk_\w+ 会把 xuk_abc 误判为合规，造成漏检。
//  2. 未配置正则 => 回退前缀匹配（不区分大小写）。
//  3. 两者都为空 => 视为不限制，直接通过。
//
// 正则非法（无法编译）时按「不合规」处理，避免静默放行。
func MatchIndexName(name, regex, prefix string) bool {
	if r := strings.TrimSpace(regex); r != "" {
		re, err := regexp.Compile(fullMatchPattern(r))
		if err != nil {
			return false
		}
		return re.MatchString(name)
	}
	if p := strings.TrimSpace(prefix); p != "" {
		return HasPrefix(name, p, false)
	}
	return true
}

// fullMatchPattern 把用户配置的正则包装为「完整匹配」表达式。
// 用户已自带 ^/$ 时，嵌套锚点仍是合法且等价的 RE2 表达式。
func fullMatchPattern(pattern string) string {
	return "^(?:" + pattern + ")$"
}

// IndexNameRequirementText 生成索引名要求的人类可读描述，用于告警文案回显。
// 让用户能直接看出「当前到底按什么要求判定」（正则优先、其次前缀），
// 避免出现「配置了 A 却按 B 判定」的观感。
func IndexNameRequirementText(regex, prefix string) string {
	if r := strings.TrimSpace(regex); r != "" {
		return fmt.Sprintf("完整匹配正则`%s`", r)
	}
	if p := strings.TrimSpace(prefix); p != "" {
		return fmt.Sprintf("以`%s`为前缀（不区分大小写）", p)
	}
	return "满足命名要求"
}

func GenerateSimpleRandomString(length int) string {
	const charset = "0123456789ABCDEF"
	source := rand.NewSource(time.Now().UnixNano())
	rng := rand.New(source)

	result := make([]byte, length)
	for i := range result {
		result[i] = charset[rng.Intn(len(charset))]
	}

	return string(result)
}

func GenerateRandomString(length int) string {
	const charset = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ!@#$%^&*()-_=+[]{};:,.<>?`~"
	source := rand.NewSource(time.Now().UnixNano())
	rng := rand.New(source)

	result := make([]byte, length)
	for i := range result {
		result[i] = charset[rng.Intn(len(charset))]
	}

	return string(result)
}
