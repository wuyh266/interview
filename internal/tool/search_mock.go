package tool

import (
	"fmt"
	"strings"
)

type searchDocument struct {
	result     SearchResult
	title      string
	searchable string
}

var mockSearchDocuments = makeMockSearchDocuments()

func makeMockSearchDocuments() []searchDocument {
	docs := make([]searchDocument, 0, 600)
	add := func(title, snippet, keywords string) {
		id := fmt.Sprintf("mock-%03d", len(docs)+1)
		docs = append(docs, searchDocument{
			result:     SearchResult{ID: id, Title: title, Snippet: snippet, URL: "https://search.example/" + id},
			title:      strings.ToLower(title),
			searchable: strings.ToLower(strings.Join([]string{id, title, snippet, keywords}, " ")),
		})
	}

	cities := []struct{ name, keyword string }{
		{"北京", "Beijing"}, {"上海", "Shanghai"}, {"广州", "Guangzhou"}, {"深圳", "Shenzhen"},
		{"杭州", "Hangzhou"}, {"成都", "Chengdu"}, {"西安", "Xi'an"}, {"武汉", "Wuhan"},
		{"南京", "Nanjing"}, {"重庆", "Chongqing"},
	}
	conditions := []string{"晴", "多云", "阴", "小雨", "阵雨"}
	for index, city := range cities {
		for day := 1; day <= 30; day++ {
			date := fmt.Sprintf("2026-04-%02d", day)
			condition := conditions[(index+day)%len(conditions)]
			low := 12 + (index*3+day)%15
			title := fmt.Sprintf("%s天气 %s", city.name, date)
			snippet := fmt.Sprintf("%s的模拟天气为%s，气温 %d 到 %d °C。", city.name, condition, low, low+6)
			add(title, snippet, city.keyword+" 天气 预报 weather forecast")
		}
	}

	topics := []struct{ title, keywords, snippet string }{
		{"Go Context", "golang cancel timeout", "通过 context 传递超时和取消信号。"},
		{"Go 单元测试", "golang test table-driven", "用表驱动测试覆盖正常值、边界值和错误输入。"},
		{"Go JSON", "golang encoding schema", "用 JSON 标签定义字段名称，并处理缺失字段和空值。"},
		{"Go 并发", "golang goroutine channel", "根据并发任务的生命周期选择 channel 和互斥锁。"},
		{"Go 大数运算", "golang calculator math/big", "用任意精度整数或有理数处理大数和小数。"},
		{"Go HTTP", "golang request client", "配置 HTTP 客户端超时并及时关闭响应体。"},
		{"Go 错误处理", "golang errors wrap", "保留错误原因并为调用层补充上下文。"},
		{"Agent 工具调用", "agent tool calling calculator", "把工具 Schema、调用参数和执行结果接入对话循环。"},
		{"Agent 会话管理", "agent session context", "通过会话键恢复独立的对话和工具状态。"},
		{"Agent 日志追踪", "agent trace slog", "按调用 ID 关联参数、输出、耗时和错误。"},
		{"项目周报", "weekly report 进度", "列出本周产出、待解决事项和下一周安排。"},
		{"待办管理", "todo task list", "将任务写成可执行的待办并按会话查询。"},
		{"会议纪要", "meeting notes 记录", "提炼会议决议、负责人和截止日期。"},
		{"面试准备", "interview question 复习", "准备项目讲解、知识点和限时练习。"},
		{"代码评审", "code review 检查", "核对行为变化、可读性与可验证性。"},
	}
	formats := []string{
		"概览", "上手指南", "参考手册", "常见问题", "检查清单", "入门练习", "回归测试", "最佳实践", "应用示例", "实战笔记",
		"问题排查", "复习提纲", "进阶阅读", "对照笔记", "配置说明", "术语表", "方法比较", "常见误区", "经验总结", "快速查阅",
	}
	for _, topic := range topics {
		for _, format := range formats {
			add(topic.title+" "+format, format+"。"+topic.snippet, topic.keywords)
		}
	}
	return docs
}
