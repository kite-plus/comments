package mailer

import (
	"fmt"
	"html"
	"strings"
)

// Item is one comment in a notification.
type Item struct {
	SiteName    string
	ThreadTitle string
	Permalink   string
	Author      string
	Excerpt     string
	Pending     bool
	MuteURL     string
}

func zh(locale string) bool { return locale != "en" }

func pick(locale, zhText, enText string) string {
	if zh(locale) {
		return zhText
	}
	return enText
}

// layout wraps text and HTML bodies with the common footer.
type layout struct {
	locale string
	text   strings.Builder
	html   strings.Builder
}

func newLayout(locale string) *layout {
	l := &layout{locale: locale}
	l.html.WriteString(`<div style="font:15px/1.6 system-ui,-apple-system,'PingFang SC','Microsoft YaHei',sans-serif;color:#1f2328;max-width:36rem">`)
	return l
}

func (l *layout) para(s string) {
	l.text.WriteString(s + "\n\n")
	l.html.WriteString("<p>" + html.EscapeString(s) + "</p>")
}

func (l *layout) link(label, href string) {
	l.text.WriteString(label + ": " + href + "\n\n")
	l.html.WriteString(`<p><a href="` + html.EscapeString(href) + `">` + html.EscapeString(label) + `</a></p>`)
}

func (l *layout) item(it Item) {
	head := it.Author
	if it.ThreadTitle != "" {
		head += pick(l.locale, " · 《"+it.ThreadTitle+"》", " · “"+it.ThreadTitle+"”")
	}
	if it.Pending {
		head += pick(l.locale, "（待审核）", " (awaiting review)")
	}
	l.text.WriteString("— " + head + "\n" + it.Excerpt + "\n" + it.Permalink + "\n")
	l.html.WriteString(`<div style="border-left:3px solid #d1d9e0;padding:.25rem .75rem;margin:1rem 0"><div style="color:#59636e">` +
		html.EscapeString(head) + `</div><div>` + html.EscapeString(it.Excerpt) + `</div><div><a href="` +
		html.EscapeString(it.Permalink) + `">` + html.EscapeString(pick(l.locale, "查看", "View")) + `</a>`)
	if it.MuteURL != "" {
		l.text.WriteString(pick(l.locale, "关闭这个讨论页的通知：", "Stop mail from this page: ") + it.MuteURL + "\n")
		l.html.WriteString(` · <a href="` + html.EscapeString(it.MuteURL) + `" style="color:#59636e">` +
			html.EscapeString(pick(l.locale, "关闭这个讨论页的通知", "Stop mail from this page")) + `</a>`)
	}
	l.text.WriteString("\n")
	l.html.WriteString(`</div></div>`)
}

func (l *layout) done(m *Message, footer string) {
	l.text.WriteString("--\n" + footer + "\n")
	l.html.WriteString(`<p style="color:#59636e;font-size:13px;margin-top:2rem">` + html.EscapeString(footer))
	if m.Unsubscribe != "" {
		l.text.WriteString(pick(l.locale, "退订：", "Unsubscribe: ") + m.Unsubscribe + "\n")
		l.html.WriteString(` · <a href="` + html.EscapeString(m.Unsubscribe) + `" style="color:#59636e">` +
			html.EscapeString(pick(l.locale, "退订", "Unsubscribe")) + `</a>`)
	}
	l.html.WriteString(`</p></div>`)
	m.Text, m.HTML = l.text.String(), l.html.String()
}

// Replies is the reply notification (moderation.md section 6.1).
func Replies(locale, to string, items []Item, unsubscribe string) Message {
	m := Message{To: to, Unsubscribe: unsubscribe}
	site := items[0].SiteName
	if len(items) == 1 {
		m.Subject = pick(locale, fmt.Sprintf("%s 回复了你在「%s」的评论", items[0].Author, site),
			fmt.Sprintf("%s replied to your comment on %s", items[0].Author, site))
	} else {
		m.Subject = pick(locale, fmt.Sprintf("你的评论收到了 %d 条回复", len(items)), fmt.Sprintf("%d replies to your comments", len(items)))
	}
	l := newLayout(locale)
	l.para(pick(locale, "有人回复了你的评论：", "Someone replied to your comment:"))
	for _, it := range items {
		l.item(it)
	}
	l.done(&m, pick(locale, "Kite Plus 评论 · 不能直接回复这封邮件。", "Kite Plus Comments · Replies to this mail are not read."))
	return m
}

// Owner is the owner's notification of new comments, instant or the daily
// digest (moderation.md section 6.2).
func Owner(locale, to, siteName string, items []Item, total int, consoleURL, unsubscribe string, digest bool) Message {
	m := Message{To: to, Unsubscribe: unsubscribe}
	switch {
	case digest:
		m.Subject = pick(locale, fmt.Sprintf("「%s」今天有 %d 条新评论", siteName, total), fmt.Sprintf("%d new comments on %s today", total, siteName))
	default:
		m.Subject = pick(locale, fmt.Sprintf("「%s」有 %d 条新评论", siteName, total), fmt.Sprintf("%d new comments on %s", total, siteName))
	}
	l := newLayout(locale)
	for _, it := range items {
		l.item(it)
	}
	if total > len(items) {
		l.para(pick(locale, fmt.Sprintf("另有 %d 条未列出。", total-len(items)), fmt.Sprintf("%d more not shown.", total-len(items))))
	}
	l.link(pick(locale, "在控制台处理", "Open the console"), consoleURL)
	l.done(&m, pick(locale, "Kite Plus 评论 · 站长通知可以在站点设置里调整。", "Kite Plus Comments · Change owner mail in the site settings."))
	return m
}

// Report tells owners about an illegal or privacy report at once.
func Report(locale, to, siteName, reason string, it Item, consoleURL string) Message {
	m := Message{To: to}
	m.Subject = pick(locale, fmt.Sprintf("「%s」有一条评论被举报：%s", siteName, reasonText(locale, reason)),
		fmt.Sprintf("A comment on %s was reported: %s", siteName, reasonText(locale, reason)))
	l := newLayout(locale)
	l.para(pick(locale, "这类举报需要尽快处理：", "Reports like this need attention soon:"))
	l.item(it)
	l.link(pick(locale, "在控制台处理", "Open the console"), consoleURL)
	l.done(&m, pick(locale, "Kite Plus 评论", "Kite Plus Comments"))
	return m
}

func reasonText(locale, reason string) string {
	zhText := map[string]string{"illegal": "违法", "privacy": "泄露个人信息", "spam": "垃圾广告", "abuse": "人身攻击或骚扰", "other": "其他"}
	enText := map[string]string{"illegal": "illegal", "privacy": "personal information", "spam": "spam", "abuse": "abuse", "other": "other"}
	return pick(locale, zhText[reason], enText[reason])
}

// Claim is the verification mail of a claim (sites.md section 6).
func Claim(locale, to, link string) Message {
	m := Message{To: to, Subject: pick(locale, "认领你的访客评论", "Claim your guest comments")}
	l := newLayout(locale)
	l.para(pick(locale, "有人在 Kite Plus 评论里请求把用这个邮箱留下的访客评论认领到自己的账号下。",
		"Someone asked Kite Plus Comments to move guest comments left with this address to their account."))
	l.link(pick(locale, "是我本人：打开读者中心完成验证（1 小时内有效）", "It was me: verify in the reader center (valid for one hour)"), link)
	l.para(pick(locale, "如果不是你本人发起的，忽略这封邮件即可，什么都不会发生。", "If it was not you, ignore this mail; nothing will happen."))
	l.done(&m, pick(locale, "Kite Plus 评论", "Kite Plus Comments"))
	return m
}

// GuestConfirm is the one mail a guest address gets before reply mail.
func GuestConfirm(locale, to, siteName, link, unsubscribe string) Message {
	m := Message{To: to, Unsubscribe: unsubscribe, Subject: pick(locale, "确认接收评论回复通知", "Confirm reply notifications")}
	l := newLayout(locale)
	l.para(pick(locale, fmt.Sprintf("你在「%s」留言时勾选了「有人回复时通知我」。确认之后，有人回复你时才会收到邮件。", siteName),
		fmt.Sprintf("You asked to hear about replies to your comment on %s. Confirm, and only then will mail follow.", siteName)))
	l.link(pick(locale, "确认", "Confirm"), link)
	l.para(pick(locale, "如果不是你本人留的言，忽略这封邮件即可。", "If you did not leave that comment, ignore this mail."))
	l.done(&m, pick(locale, "Kite Plus 评论", "Kite Plus Comments"))
	return m
}

// Recheck tells owners that an origin no longer proves control: first as a
// warning, then when it leaves the verified list (sites.md section 2).
func Recheck(locale, to, siteName, origin, detail string, dropped bool, consoleURL string) Message {
	m := Message{To: to}
	l := newLayout(locale)
	if dropped {
		m.Subject = pick(locale, fmt.Sprintf("「%s」的 %s 已停止显示评论", siteName, origin), fmt.Sprintf("Comments stopped on %s for %s", origin, siteName))
		l.para(pick(locale, "这个地址连续 14 天没有通过验证复查，评论组件不会再在那里显示。重新放好验证记录后，在控制台点「验证」即可恢复。",
			"This address failed its verification recheck for 14 days, so the widget no longer shows there. Put the record back and verify again in the console."))
	} else {
		m.Subject = pick(locale, fmt.Sprintf("「%s」的验证记录找不到了", siteName), fmt.Sprintf("The verification record for %s is missing", siteName))
		l.para(pick(locale, fmt.Sprintf("定期复查没有在 %s 找到验证记录。14 天内恢复记录即可，否则评论组件会在那里停止显示。", origin),
			fmt.Sprintf("The periodic recheck found no verification record at %s. Restore it within 14 days, or the widget stops showing there.", origin)))
	}
	if detail != "" {
		l.para(detail)
	}
	l.link(pick(locale, "打开控制台", "Open the console"), consoleURL)
	l.done(&m, pick(locale, "Kite Plus 评论", "Kite Plus Comments"))
	return m
}
