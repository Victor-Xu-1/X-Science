package server

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

const sessionRunnerOutputActions = `output(?:s|ted|ting)?|deliver(?:ed|ing)?|publish(?:ed|ing)?|sav(?:e|ed|ing)|writ(?:e|ing)|creat(?:e|ed|ing)|generat(?:e|ed|ing)|export(?:ed|ing)?|attach(?:ed|ing)?|retain(?:ed|ing)?|preserv(?:e|ed|ing)|keep(?:ing)?|provid(?:e|ed|ing)`
const sessionRunnerInputActions = `read(?:ing)?|inspect(?:ed|ing)?|download(?:ed|ing)?|load(?:ed|ing)?|pars(?:e|ed|ing)|open(?:ed|ing)?|use|using`
const sessionRunnerChineseOutputActions = `输出|交付|发布|保存|生成|产出|创建|导出|附加|保留|留存|整理|写入|放入|给出`
const sessionRunnerChineseInputActions = `读取|阅读|检查|下载|加载|解析|打开|使用`

var sessionRunnerDeliverableActionPattern = regexp.MustCompile(`(?i)\b(?:` + sessionRunnerOutputActions + `|` + sessionRunnerInputActions + `)\b|` + sessionRunnerChineseOutputActions + `|` + sessionRunnerChineseInputActions)
var sessionRunnerOutputActionPattern = regexp.MustCompile(`(?i)^(?:` + sessionRunnerOutputActions + `|` + sessionRunnerChineseOutputActions + `)$`)
var sessionRunnerDeliverableNegationPattern = regexp.MustCompile(`(?i)\b(?:not|never|without|avoid|don't|dont|shouldn't|needn't|no\s+(?:need|extra)|not\s+required)\b|不要|不需要|不必|不用|不得|不能|禁止|避免|无需|无须|不额外|不再|不(?:应(?:该)?|再|额外)?(?:` + sessionRunnerChineseOutputActions + `)`)
var sessionRunnerDeliverableContrastPattern = regexp.MustCompile(`(?i)\b(?:but|however|instead)\b|但是|但|而是|改为`)
var sessionRunnerDeliverableReminderPattern = regexp.MustCompile(`(?i)\b(?:don't|do\s+not)\s+forget\s+to\s+|不要忘记|别忘记`)

// sessionRunnerAffirmativeDeliverableIntent qualifies the existing filename,
// format and persistence recognizers with one shared polarity rule. Mentioning
// a format in a prohibited output or a later input action cannot create a hard
// completion requirement. This is conservative intent extraction, not a second
// scientific-quality gate or an attempt to rewrite the user's instructions.
func sessionRunnerAffirmativeDeliverableIntent(prefix string, implicitPersistence bool) bool {
	prefix = prefix[sessionRunnerDeliverableClauseStart(prefix):]
	prefix = sessionRunnerDeliverableReminderPattern.ReplaceAllString(prefix, "")
	actions := sessionRunnerDeliverableActionPattern.FindAllStringIndex(prefix, -1)
	// "可直接使用的 Markdown" describes usability, not a later input action.
	// Keep the earlier generation verb instead of reversing that requirement.
	for len(actions) > 0 {
		action := actions[len(actions)-1]
		before := prefix[:action[0]]
		if prefix[action[0]:action[1]] != "使用" || !(strings.HasSuffix(before, "可直接") || strings.HasSuffix(before, "可供") || strings.HasSuffix(before, "可以") || strings.HasSuffix(before, "可")) {
			break
		}
		actions = actions[:len(actions)-1]
	}
	if len(actions) == 0 {
		return implicitPersistence && !sessionRunnerDeliverableNegationPattern.MatchString(prefix)
	}
	action := actions[len(actions)-1]
	if !implicitPersistence && !sessionRunnerOutputActionPattern.MatchString(prefix[action[0]:action[1]]) {
		return false
	}
	// A comma or contrast before the latest action opens a new polarity scope.
	// Lists after that action stay in its scope, so "do not generate PDF, DOCX"
	// excludes both formats instead of negating only the first one.
	before := prefix[:action[0]]
	scopeStart := 0
	if boundary := strings.LastIndexAny(before, ",，"); boundary >= 0 {
		_, size := utf8.DecodeRuneInString(before[boundary:])
		scopeStart = boundary + size
	}
	if contrasts := sessionRunnerDeliverableContrastPattern.FindAllStringIndex(before, -1); len(contrasts) > 0 {
		if end := contrasts[len(contrasts)-1][1]; end > scopeStart {
			scopeStart = end
		}
	}
	return !sessionRunnerDeliverableNegationPattern.MatchString(prefix[scopeStart:])
}
