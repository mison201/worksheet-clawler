package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

const (
	baseURL   = "https://www.kiddoworksheets.com"
	listPath  = "/all-downloads/"
	userAgent = "KiddoCrawler-Items/1.0 (+https://example.local)"
)

func findMaxPages(client *http.Client) (int, error) {
	doc, err := fetchDoc(client, baseURL+listPath)
	if err != nil {
		return 0, err
	}
	max := 1
	re := regexp.MustCompile(`/all-downloads/page/(\d+)/?`)
	doc.Find(`a[href*="/all-downloads/page/"]`).Each(func(_ int, s *goquery.Selection) {
		if href, ok := s.Attr("href"); ok {
			m := re.FindStringSubmatch(href)
			if len(m) == 2 {
				n := atoiSafe(m[1])
				if n > max {
					max = n
				}
			}
		}
	})
	return max, nil
}

func resolveListURL(p int) string {
	if p <= 1 {
		return baseURL + listPath
	}
	return fmt.Sprintf("%s/all-downloads/page/%d/", baseURL, p)
}

func extractDetailLinks(doc *goquery.Document, base string) []string {
	var links []string
	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		h := toAbs(href)
		if isDetailURL(h) {
			links = append(links, h)
		}
	})
	return uniq(links)
}

func isDetailURL(href string) bool {
	h := strings.ToLower(href)
	if strings.HasPrefix(h, "mailto:") || strings.HasPrefix(h, "javascript:") {
		return false
	}
	for _, seg := range []string{
		"/worksheet/",
		"/worksheets/",
		"/find-",
		"/tracing-",
		"/sight-words/",
		"/vocabulary/",
		"/shapes/",
	} {
		if strings.Contains(h, seg) {
			return true
		}
	}
	return false
}

func parseDetail(client *http.Client, detailURL string) (Item, error) {
	doc, err := fetchDoc(client, detailURL)
	if err != nil {
		return Item{}, err
	}

	// title
	title := cleanText(doc.Find("h1").First().Text())
	if title == "" {
		title = fallbackTitle(detailURL)
	}

	// pdf candidates
	var pdf string
	doc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		h, _ := a.Attr("href")
		h = toAbs(h)
		txt := strings.ToLower(strings.TrimSpace(a.Text()))
		if strings.HasSuffix(strings.ToLower(h), ".pdf") {
			if pdf == "" {
				pdf = h
			}
		} else if strings.Contains(txt, "download") || strings.Contains(txt, "pdf") {
			if pdf == "" {
				pdf = h
			}
		}
	})
	// prefer direct .pdf
	// (nếu không đuôi .pdf, để nguyên — có thể là link tải có redirect)

	// image: prefer OG image
	var img string
	if og := doc.Find(`meta[property="og:image"]`).First(); og.Length() > 0 {
		if c, ok := og.Attr("content"); ok && c != "" {
			img = toAbs(c)
		}
	}
	if img == "" {
		if im := doc.Find("img").First(); im.Length() > 0 {
			if src, ok := im.Attr("src"); ok && src != "" {
				img = toAbs(src)
			}
		}
	}

	// subject: chiến lược nhiều lớp

	// 1) Dò các dòng có "Subject:" trong li/p/div
	subject := ""
	doc.Find("li, p, div").Each(func(_ int, s *goquery.Selection) {
		if subject != "" {
			return
		}
		t := strings.ToLower(strings.TrimSpace(s.Text()))
		if strings.HasPrefix(t, "subject") {
			raw := cleanText(s.Text())
			if val := strings.TrimSpace(afterColon(raw)); val != "" {
				subject = val
			}
		}
	})

	// 2) Nếu chưa có: tìm label đậm/strong "Subject:" + sibling
	if subject == "" {
		doc.Find("strong,b").Each(func(_ int, s *goquery.Selection) {
			if subject != "" {
				return
			}
			txt := strings.ToLower(strings.TrimSpace(s.Text()))
			if strings.HasPrefix(txt, "subject") {
				// lấy nguyên node text cha
				raw := cleanText(s.Parent().Text())
				val := strings.TrimSpace(afterColon(raw))
				if val == "" {
					// thử next sibling text
					if sib := s.Parent().Next(); sib.Length() > 0 {
						val = cleanText(sib.Text())
					}
				}
				if val != "" {
					subject = val
				}
			}
		})
	}

	// 3) Nếu vẫn chưa có: thử lấy từ category/tag
	if subject == "" {
		var candidates []string
		doc.Find(`a[rel="category tag"], a[href*="/category/"], a[href*="/tags/"], a[href*="/tag/"]`).Each(func(_ int, a *goquery.Selection) {
			t := cleanText(a.Text())
			if t != "" {
				candidates = append(candidates, t)
			}
		})
		if s := pickLikelySubject(candidates); s != "" {
			subject = s
		} else if len(candidates) > 0 {
			subject = candidates[0]
		}
	}

	return Item{
		Title:   title,
		PDFURL:  pdf,
		IMGURL:  img,
		URL:     detailURL,
		Subject: subject,
	}, nil
}

// ---- utils ----

func uniq(ss []string) []string {
	m := make(map[string]struct{}, len(ss))
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if _, ok := m[s]; !ok {
			m[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

func toAbs(href string) string {
	u, err := url.Parse(href)
	if err != nil {
		return href
	}
	if u.IsAbs() {
		return href
	}
	base, _ := url.Parse(baseURL)
	return base.ResolveReference(u).String()
}

func fallbackTitle(u string) string {
	pu, err := url.Parse(u)
	if err != nil {
		return u
	}
	parts := strings.Split(strings.Trim(pu.Path, "/"), "/")
	if len(parts) == 0 {
		return u
	}
	return strings.ReplaceAll(parts[len(parts)-1], "-", " ")
}

func writeJSONL(path string, items []Item) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, it := range items {
		if err := enc.Encode(it); err != nil {
			return err
		}
	}
	return w.Flush()
}

func writeJSONArray(path string, items []Item) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(items)
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n = n*10 + int(r-'0')
		}
	}
	return n
}

// cleanText: collapse whitespace
func cleanText(s string) string {
	s = strings.TrimSpace(s)
	s = regexp.MustCompile(`\s+`).ReplaceAllString(s, " ")
	return s
}

func afterColon(s string) string {
	i := strings.IndexRune(s, ':')
	if i < 0 {
		return s
	}
	return s[i+1:]
}

// pickLikelySubject: ưu tiên các subject phổ biến
func pickLikelySubject(cands []string) string {
	if len(cands) == 0 {
		return ""
	}
	// map từ khóa -> ưu tiên
	keywords := []string{
		"math", "english", "phonics", "reading", "writing",
		"science", "social studies", "colors", "shapes",
		"numbers", "counting", "tracing", "alphabet", "letters",
		"grammar", "vocabulary",
	}
	lc := make([]string, len(cands))
	for i, c := range cands {
		lc[i] = strings.ToLower(c)
	}
	for _, kw := range keywords {
		for i, v := range lc {
			if strings.Contains(v, kw) {
				return cands[i]
			}
		}
	}
	return ""
}
