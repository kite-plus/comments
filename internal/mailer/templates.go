package mailer

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"net/url"
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

//go:embed mail.html
var mailHTML string

var page = template.Must(template.New("mail").Parse(mailHTML))

// view is what mail.html lays out in the Kite Plus paper look, shared with
// the account mail of id.kite.plus.
type view struct {
	Lang, Subject, Preview, Eyebrow, Heading string
	Paragraphs                               []string
	Cards                                    []card
	More                                     string
	// Button links to URL; MuteURL, when set, sits under it.
	Button, URL, MuteURL, MuteLabel string
	// FallbackLabel, when set, prints URL under the button for mail apps
	// that drop it.
	FallbackLabel                   string
	Note                            string
	Service, Site, SiteURL, Tagline string
	Unsubscribe, UnsubscribeLabel   string
}

type card struct {
	Author, Meta, Excerpt                  string
	ViewURL, ViewLabel, MuteURL, MuteLabel string
}

// finish renders v into m. base is the service's public URL.
func finish(m *Message, base, locale string, v view) {
	v.Lang = pick(locale, "zh-CN", "en")
	v.Subject = m.Subject
	v.Service = pick(locale, "评论", "Comments")
	v.Tagline = pick(locale, "一个账号，通行 Explore、Kite 评论和 Kite 应用", "One account for Explore, Kite Comments and the Kite app")
	v.SiteURL = strings.TrimRight(base, "/")
	if u, err := url.Parse(v.SiteURL); err == nil {
		v.Site = u.Host
	}
	v.Unsubscribe = m.Unsubscribe
	if v.Preview == "" && len(v.Paragraphs) > 0 {
		v.Preview = v.Paragraphs[0]
	}
	m.Text = text(v)
	var h bytes.Buffer
	// On a template error the mail goes out as plain text only.
	if err := page.Execute(&h, v); err == nil {
		m.HTML = h.String()
	}
}

func text(v view) string {
	var b strings.Builder
	add := func(s string) {
		if s != "" {
			b.WriteString(s + "\n\n")
		}
	}
	add(v.Heading)
	for _, p := range v.Paragraphs {
		add(p)
	}
	for _, c := range v.Cards {
		head := c.Author
		if c.Meta != "" {
			head += " · " + c.Meta
		}
		b.WriteString("— " + head + "\n" + c.Excerpt + "\n")
		if c.ViewURL != "" {
			b.WriteString(c.ViewURL + "\n")
		}
		if c.MuteURL != "" {
			b.WriteString(c.MuteLabel + ": " + c.MuteURL + "\n")
		}
		b.WriteString("\n")
	}
	add(v.More)
	if v.URL != "" {
		add(v.Button + ": " + v.URL)
	}
	if v.MuteURL != "" {
		add(v.MuteLabel + ": " + v.MuteURL)
	}
	add(v.Note)
	b.WriteString("--\nKite Plus " + v.Service + " · " + v.Site + "\n")
	if v.Unsubscribe != "" {
		b.WriteString(v.UnsubscribeLabel + ": " + v.Unsubscribe + "\n")
	}
	return b.String()
}

func muteLabel(locale string) string {
	return pick(locale, "不再接收这个讨论页的提醒", "Stop mail from this page")
}

func fallbackLabel(locale string) string {
	return pick(locale, "按钮打不开？把这个链接复制到浏览器：", "Button not working? Copy this link into your browser:")
}

func ownerNote(locale, siteName string) string {
	return pick(locale, fmt.Sprintf("你收到这封邮件，是因为你是「%s」的站长。", siteName), fmt.Sprintf("You get this because you own %s.", siteName))
}

// ownerCard lists a comment for the site owner; a pending one is not on
// the page yet, so it has no link.
func ownerCard(locale string, it Item) card {
	c := card{Author: it.Author, Excerpt: it.Excerpt}
	if it.ThreadTitle != "" {
		c.Meta = pick(locale, "《"+it.ThreadTitle+"》", "“"+it.ThreadTitle+"”")
	}
	if it.Pending {
		c.Meta = strings.TrimSpace(c.Meta + pick(locale, "（待审核）", " (awaiting review)"))
	} else {
		c.ViewURL, c.ViewLabel = it.Permalink, pick(locale, "查看", "View")
	}
	return c
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// Replies is the reply notification (moderation.md section 6.1).
func Replies(base, locale, to string, items []Item, unsubscribe string) Message {
	return replies(base, locale, to, items, unsubscribe, "")
}

// GuestReplies is reply mail to a guest address that confirmed it before
// guest comments ended; it also says how to claim the comments.
func GuestReplies(base, locale, to string, items []Item, unsubscribe, claimURL string) Message {
	return replies(base, locale, to, items, unsubscribe, claimURL)
}

func replies(base, locale, to string, items []Item, unsubscribe, claimURL string) Message {
	m := Message{To: to, Unsubscribe: unsubscribe}
	site := items[0].SiteName
	v := view{
		Eyebrow:          pick(locale, "新回复", "New reply"),
		Note:             pick(locale, "你收到这封邮件，是因为你留了言并开启了回复提醒。要回复请回到原文，直接回复这封邮件对方收不到。", "You get this because you commented and turned on reply mail. To answer, go back to the page; replies to this mail are not read."),
		UnsubscribeLabel: pick(locale, "退订所有回复提醒", "Unsubscribe from all reply mail"),
	}
	if len(items) == 1 {
		it := items[0]
		m.Subject = pick(locale, fmt.Sprintf("%s 回复了你在「%s」的评论", it.Author, site),
			fmt.Sprintf("%s replied to your comment on %s", it.Author, site))
		v.Heading = pick(locale, it.Author+" 回复了你的评论", it.Author+" replied to your comment")
		where := pick(locale, "「"+site+"」", site)
		if it.ThreadTitle != "" {
			where = pick(locale, where+"的《"+it.ThreadTitle+"》", "“"+it.ThreadTitle+"” on "+site)
		}
		v.Paragraphs = []string{pick(locale, "你在"+where+"下的评论有了新回复：", "Your comment under "+where+" has a new reply:")}
		v.Cards = []card{{Author: it.Author, Excerpt: it.Excerpt}}
		v.Button, v.URL = pick(locale, "查看回复", "View the reply"), it.Permalink
		if it.MuteURL != "" {
			v.MuteURL, v.MuteLabel = it.MuteURL, muteLabel(locale)
		}
		v.Preview = it.Author + pick(locale, "：", ": ") + it.Excerpt
	} else {
		m.Subject = pick(locale, fmt.Sprintf("你的评论收到了 %d 条回复", len(items)), fmt.Sprintf("%d replies to your comments", len(items)))
		v.Heading = m.Subject
		v.Paragraphs = []string{pick(locale, "这段时间你的评论收到了这些回复：", "These replies to your comments came in:")}
		for _, it := range items {
			c := card{Author: it.Author, Excerpt: it.Excerpt, ViewURL: it.Permalink, ViewLabel: pick(locale, "查看", "View")}
			c.Meta = pick(locale, "「"+it.SiteName+"」", it.SiteName)
			if it.ThreadTitle != "" {
				c.Meta = pick(locale, c.Meta+"《"+it.ThreadTitle+"》", c.Meta+", “"+it.ThreadTitle+"”")
			}
			if it.MuteURL != "" {
				c.MuteURL, c.MuteLabel = it.MuteURL, muteLabel(locale)
			}
			v.Cards = append(v.Cards, c)
		}
	}
	if claimURL != "" {
		v.More = pick(locale, "Kite Plus 评论已不再接受访客评论。用 Kite Plus 账号登录，在读者中心认领用这个邮箱留下的评论，以后的回复都在那里："+claimURL,
			"Kite Plus Comments no longer takes guest comments. Sign in with a Kite Plus account and claim the comments left with this address in the reader center to follow replies there: "+claimURL)
	}
	finish(&m, base, locale, v)
	return m
}

// Owner is the owner's notification of new comments, instant or the daily
// digest (moderation.md section 6.2).
func Owner(base, locale, to, siteName string, items []Item, total int, consoleURL, unsubscribe string, digest bool) Message {
	m := Message{To: to, Unsubscribe: unsubscribe}
	v := view{
		Eyebrow:          pick(locale, "新评论", "New comments"),
		Button:           pick(locale, "在控制台处理", "Open the console"),
		URL:              consoleURL,
		Note:             pick(locale, fmt.Sprintf("你收到这封邮件，是因为你是「%s」的站长。站长通知可以在站点设置里改成即时、每日汇总或关闭。", siteName), fmt.Sprintf("You get this because you own %s. Owner mail can be instant, daily or off in the site settings.", siteName)),
		UnsubscribeLabel: pick(locale, "不再接收这个站点的站长通知", "Stop owner mail for this site"),
	}
	comments := plural(total, "comment", "comments")
	if digest {
		v.Eyebrow = pick(locale, "每日汇总", "Daily digest")
		m.Subject = pick(locale, fmt.Sprintf("「%s」今天有 %d 条新评论", siteName, total), fmt.Sprintf("%d new %s on %s today", total, comments, siteName))
	} else {
		m.Subject = pick(locale, fmt.Sprintf("「%s」有 %d 条新评论", siteName, total), fmt.Sprintf("%d new %s on %s", total, comments, siteName))
	}
	v.Heading = m.Subject
	for _, it := range items {
		v.Cards = append(v.Cards, ownerCard(locale, it))
	}
	if total > len(items) {
		v.More = pick(locale, fmt.Sprintf("另有 %d 条未列出，在控制台里可以看到全部。", total-len(items)), fmt.Sprintf("%d more not shown; the console has them all.", total-len(items)))
	}
	if len(items) > 0 {
		v.Preview = items[0].Author + pick(locale, "：", ": ") + items[0].Excerpt
	}
	finish(&m, base, locale, v)
	return m
}

// Report tells owners about an illegal or privacy report at once.
func Report(base, locale, to, siteName, reason string, it Item, consoleURL string) Message {
	m := Message{To: to}
	r := reasonText(locale, reason)
	m.Subject = pick(locale, fmt.Sprintf("「%s」有一条评论被举报：%s", siteName, r),
		fmt.Sprintf("A comment on %s was reported: %s", siteName, r))
	finish(&m, base, locale, view{
		Eyebrow:    pick(locale, "举报", "Report"),
		Heading:    pick(locale, "一条评论被举报："+r, "A comment was reported: "+r),
		Paragraphs: []string{pick(locale, fmt.Sprintf("「%s」上的这条评论被举报为「%s」，这类举报需要尽快处理：", siteName, r), fmt.Sprintf("This comment on %s was reported as %s. Reports like this need attention soon:", siteName, r))},
		Cards:      []card{ownerCard(locale, it)},
		Button:     pick(locale, "在控制台处理", "Open the console"),
		URL:        consoleURL,
		Note:       ownerNote(locale, siteName),
	})
	return m
}

func reasonText(locale, reason string) string {
	zhText := map[string]string{"illegal": "违法", "privacy": "泄露个人信息", "spam": "垃圾广告", "abuse": "人身攻击或骚扰", "other": "其他"}
	enText := map[string]string{"illegal": "illegal", "privacy": "personal information", "spam": "spam", "abuse": "abuse", "other": "other"}
	return pick(locale, zhText[reason], enText[reason])
}

// Claim is the verification mail of a claim (sites.md section 6).
func Claim(base, locale, to, link string) Message {
	m := Message{To: to, Subject: pick(locale, "认领你的访客评论", "Claim your guest comments")}
	finish(&m, base, locale, view{
		Eyebrow: pick(locale, "认领评论", "Claim comments"),
		Heading: m.Subject,
		Paragraphs: []string{
			pick(locale, "有人在 Kite Plus 评论里请求把用这个邮箱留下的访客评论认领到自己的账号下。", "Someone asked Kite Plus Comments to move guest comments left with this address to their account."),
			pick(locale, "如果是你本人，打开读者中心完成验证。", "If it was you, finish in the reader center."),
		},
		Button:        pick(locale, "是我本人，去验证", "It was me, verify"),
		URL:           link,
		FallbackLabel: fallbackLabel(locale),
		Note:          pick(locale, "链接 1 小时内有效。如果不是你本人发起的，忽略这封邮件即可，什么都不会发生。", "The link works for one hour. If it was not you, ignore this mail; nothing will happen."),
	})
	return m
}

// GuestConfirm is the one mail a guest address gets before reply mail.
func GuestConfirm(base, locale, to, siteName, link, unsubscribe string) Message {
	m := Message{To: to, Unsubscribe: unsubscribe, Subject: pick(locale, "确认接收评论回复通知", "Confirm reply notifications")}
	where := pick(locale, "你留言时", "You asked")
	if siteName != "" {
		where = pick(locale, "你在「"+siteName+"」留言时", "On "+siteName+", you asked")
	}
	finish(&m, base, locale, view{
		Eyebrow:          pick(locale, "回复提醒", "Reply mail"),
		Heading:          pick(locale, "确认接收回复提醒", "Confirm reply mail"),
		Paragraphs:       []string{pick(locale, where+"勾选了「有人回复时通知我」。确认之后，有人回复你时才会收到邮件。", where+" to hear about replies to your comment. Confirm, and only then will mail follow.")},
		Button:           pick(locale, "确认接收", "Confirm"),
		URL:              link,
		FallbackLabel:    fallbackLabel(locale),
		Note:             pick(locale, "如果不是你本人留的言，忽略这封邮件即可，不会再收到后续邮件。", "If you did not leave that comment, ignore this mail and no more will follow."),
		UnsubscribeLabel: pick(locale, "不再给这个邮箱发任何评论邮件", "Stop all comment mail to this address"),
	})
	return m
}

// Recheck tells owners that an origin no longer proves control: first as a
// warning, then when it leaves the verified list (sites.md section 2).
func Recheck(base, locale, to, siteName, origin, detail string, dropped bool, consoleURL string) Message {
	m := Message{To: to}
	v := view{
		Eyebrow: pick(locale, "站点验证", "Site verification"),
		Button:  pick(locale, "打开控制台", "Open the console"),
		URL:     consoleURL,
		Note:    ownerNote(locale, siteName),
	}
	if dropped {
		m.Subject = pick(locale, fmt.Sprintf("「%s」的 %s 已停止显示评论", siteName, origin), fmt.Sprintf("Comments stopped on %s for %s", origin, siteName))
		v.Heading = pick(locale, origin+" 已停止显示评论", "Comments stopped on "+origin)
		v.Paragraphs = []string{pick(locale, "这个地址连续 14 天没有通过验证复查，评论组件不会再在那里显示。重新放好验证记录后，在控制台点「验证」即可恢复。",
			"This address failed its verification recheck for 14 days, so the widget no longer shows there. Put the record back and verify again in the console.")}
	} else {
		m.Subject = pick(locale, fmt.Sprintf("「%s」的验证记录找不到了", siteName), fmt.Sprintf("The verification record for %s is missing", siteName))
		v.Heading = pick(locale, "验证记录找不到了", "The verification record is missing")
		v.Paragraphs = []string{pick(locale, fmt.Sprintf("定期复查没有在 %s 找到验证记录。14 天内恢复记录即可，否则评论组件会在那里停止显示。", origin),
			fmt.Sprintf("The periodic recheck found no verification record at %s. Restore it within 14 days, or the widget stops showing there.", origin))}
	}
	if detail != "" {
		v.Paragraphs = append(v.Paragraphs, detail)
	}
	finish(&m, base, locale, v)
	return m
}
