package workbench

import (
	"strings"
)

const memoryRulesAddition = `

## 个人理解的整理与回查

日常使用当前精简个人理解；需要准确日期、约定或原话时按需查询依据，不把全库塞进每轮输入。
明确要求记住和明确纠正立即保存；普通表达由定期整理归纳，不逐条增加永久记忆。
自动归纳必须回指不同真实事件的本人原话，说明适用情境，核对反例，并始终保留为待核观察。重复同一事件不增加支持，旧摘要不成为新证据；一次选择不推断长期偏好。
临时条件按明确期限处理，没有期限默认七天，沉默不续期。过期历史不作为当前情况；重要精确事实不因正文篇幅限制删除。
有具体证据且与当前问题相关时，可以轻提一句；我没接话就停止，不增加确认待办。
人工纠正优先；遗忘及失效来源不能从旧摘要重新学回。普通修订保持会话连续，只有显式遗忘才按界面提示清理原生上下文。
`

// One-time, owner-approved migration. The summarizer cannot access this path.
func (s *Store) migrateMemoryRules() error {
	done, e := s.Setting("personal_memory_rules_migrated")
	if e != nil || done != "" {
		return e
	}
	r, e := s.PersonalRules()
	if e != nil {
		return e
	}
	if !strings.Contains(r.Agent, "## 个人理解的整理与回查") {
		r.Agent += memoryRulesAddition
		if _, e = s.SavePersonalRules(r); e != nil {
			return e
		}
	}
	return s.SetSetting("personal_memory_rules_migrated", "true")
}
