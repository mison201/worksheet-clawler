package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

const (
	wsfBaseURL   = "https://www.worksheetfun.com"
	wsfUserAgent = "WSFunCrawler-Items/1.0 (+https://example.local)"
)

type WSFCheckpoint struct {
	LastPage int `json:"last_page"`
}

type WSFCrawlConfig struct {
	DataPath        string
	CPPath          string
	StartPage       int
	EndPage         int // 0 = auto detect; với BaseCategoryURL + LazyExhaust => bỏ qua, cào đến khi dừng
	DelayMs         int
	MaxItems        int    // 0 = unlimited
	BaseCategoryURL string // nếu set, cào theo category này
	LazyExhaust     bool   // true: tăng /page/N/ tới khi lỗi/không còn bài
	EmptyPageLimit  int    // số trang trống liên tiếp để dừng (mặc định 3 nếu =0)
	Retry           int    // số lần retry tải trang list (mặc định 2 nếu =0)
}

// ---------- Public runner ----------

func RunWSFunCrawlerWithCheckpoint(cfg WSFCrawlConfig) error {
	client := &http.Client{Timeout: 30 * time.Second}

	// defaults
	if cfg.EmptyPageLimit <= 0 {
		cfg.EmptyPageLimit = 3
	}
	if cfg.Retry <= 0 {
		cfg.Retry = 2
	}

	// checkpoint -> start
	cp, _ := wsfReadCheckpoint(cfg.CPPath)
	start := cfg.StartPage
	if cp.LastPage > 0 {
		start = cp.LastPage + 1
	}
	if start < 1 {
		start = 1
	}

	// ensure data dir
	if err := osMkdirAll(filepath.Dir(absPath(cfg.DataPath)), 0o755); err != nil && !os.IsExist(err) {
		return err
	}

	// nạp dữ liệu cũ để dedup theo pdf_url
	existing, _ := loadItems(cfg.DataPath)
	seen := make(map[string]struct{}, len(existing))
	for _, it := range existing {
		if it.PDFURL != "" {
			seen[strings.TrimSpace(it.PDFURL)] = struct{}{}
		}
	}

	// Quyết định chiến lược phân trang
	useLazy := cfg.LazyExhaust || (strings.TrimSpace(cfg.BaseCategoryURL) != "" && cfg.EndPage == 0)

	// Tính end nếu không dùng lazy
	end := cfg.EndPage
	if !useLazy && end == 0 {
		var err error
		if strings.TrimSpace(cfg.BaseCategoryURL) != "" {
			end, err = wsfFindMaxPagesForCategory(client, cfg.BaseCategoryURL)
		} else {
			end, err = wsfFindMaxPages(client)
		}
		if err != nil {
			log.Printf("[wsfun] cannot detect max pages: %v, fallback 100", err)
			end = 100
		}
	}

	log.Printf("[wsfun] crawl start=%d strategy=%s end=%d data=%s cp=%s",
		start, tern(useLazy, "lazy-exhaust", "bounded"), end, cfg.DataPath, cfg.CPPath)

	var batch []Item
	collected := 0

	if useLazy {
		// -------- Lazy exhaust mode: tăng /page/N/ đến khi dừng --------
		emptyRun := 0
		for p := start; ; p++ {
			listURL := wsfCatOrRootURL(cfg.BaseCategoryURL, p)
			log.Printf("[wsfun] page %d: %s", p, listURL)

			doc, err := wsfFetchDocWithRetry(client, listURL, cfg.Retry)
			if err != nil {
				log.Printf("[wsfun] stop on error page %d: %v", p, err)
				_ = wsfWriteCheckpoint(cfg.CPPath, p)
				break
			}

			detailLinks, thumbMap := wsfExtractDetailLinks(doc)
			if len(detailLinks) == 0 {
				emptyRun++
				log.Printf("[wsfun] page %d: no detail links (emptyRun=%d)", p, emptyRun)
				_ = wsfWriteCheckpoint(cfg.CPPath, p)
				if emptyRun >= cfg.EmptyPageLimit {
					log.Printf("[wsfun] stop on empty pages (limit=%d)", cfg.EmptyPageLimit)
					break
				}
				// nghỉ một chút rồi tiếp
				time.Sleep(time.Duration(cfg.DelayMs) * time.Millisecond)
				continue
			}
			emptyRun = 0 // reset vì có bài

			for _, durl := range detailLinks {
				if cfg.MaxItems > 0 && collected >= cfg.MaxItems {
					break
				}
				time.Sleep(time.Duration(cfg.DelayMs) * time.Millisecond)

				it, err := wsfParseDetail(client, durl)
				if err != nil {
					log.Printf("  [wsfun/detail] %s -> %v", durl, err)
					continue
				}
				// thumbnail fallback từ listing
				if it.IMGURL == "" {
					if tb, ok := thumbMap[durl]; ok {
						it.IMGURL = tb
					}
				}
				// đủ dữ liệu + chưa trùng pdf_url
				if it.Title == "" || it.PDFURL == "" {
					continue
				}
				key := strings.TrimSpace(it.PDFURL)
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				batch = append(batch, it)
				collected++
			}

			// ghi batch định kỳ
			if len(batch) > 0 {
				if err := wsfAppendItems(cfg.DataPath, batch); err != nil {
					return err
				}
				batch = batch[:0]
			}

			// checkpoint sau mỗi trang
			if err := wsfWriteCheckpoint(cfg.CPPath, p); err != nil {
				log.Printf("[wsfun] warn write checkpoint: %v", err)
			}

			if cfg.MaxItems > 0 && collected >= cfg.MaxItems {
				break
			}
		}

	} else {
		// -------- Bounded mode: p in [start..end] --------
		for p := start; p <= end; p++ {
			listURL := wsfCatOrRootURL(cfg.BaseCategoryURL, p)
			log.Printf("[wsfun] page %d: %s", p, listURL)

			doc, err := wsfFetchDocWithRetry(client, listURL, cfg.Retry)
			if err != nil {
				log.Printf("[wsfun] skip page %d: %v", p, err)
				_ = wsfWriteCheckpoint(cfg.CPPath, p)
				continue
			}

			detailLinks, thumbMap := wsfExtractDetailLinks(doc)
			if len(detailLinks) == 0 {
				log.Printf("[wsfun] page %d: no detail links", p)
				_ = wsfWriteCheckpoint(cfg.CPPath, p)
				continue
			}

			for _, durl := range detailLinks {
				if cfg.MaxItems > 0 && collected >= cfg.MaxItems {
					break
				}
				time.Sleep(time.Duration(cfg.DelayMs) * time.Millisecond)

				it, err := wsfParseDetail(client, durl)
				if err != nil {
					log.Printf("  [wsfun/detail] %s -> %v", durl, err)
					continue
				}
				if it.IMGURL == "" {
					if tb, ok := thumbMap[durl]; ok {
						it.IMGURL = tb
					}
				}
				if it.Title == "" || it.PDFURL == "" {
					continue
				}
				key := strings.TrimSpace(it.PDFURL)
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				batch = append(batch, it)
				collected++
			}

			if len(batch) > 0 {
				if err := wsfAppendItems(cfg.DataPath, batch); err != nil {
					return err
				}
				batch = batch[:0]
			}

			if err := wsfWriteCheckpoint(cfg.CPPath, p); err != nil {
				log.Printf("[wsfun] warn write checkpoint: %v", err)
			}

			if cfg.MaxItems > 0 && collected >= cfg.MaxItems {
				break
			}
		}
	}

	log.Printf("[wsfun] collected %d new items", collected)
	return nil
}

// ---------- List & pagination ----------

func wsfCatOrRootURL(baseCat string, p int) string {
	if strings.TrimSpace(baseCat) != "" {
		return wsfCatPageURL(baseCat, p)
	}
	return wsfListURL(p)
}

// Ở WorksheetFun, trang chủ phân trang dạng /page/N/
func wsfListURL(p int) string {
	if p <= 1 {
		return wsfBaseURL + "/"
	}
	return fmt.Sprintf("%s/page/%d/", wsfBaseURL, p)
}

func wsfFetchDoc(client *http.Client, u string) (*goquery.Document, error) {
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", wsfUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	return goquery.NewDocumentFromReader(resp.Body)
}

// retry đơn giản cho trang list
func wsfFetchDocWithRetry(client *http.Client, u string, retry int) (*goquery.Document, error) {
	var lastErr error
	for i := 0; i <= retry; i++ {
		doc, err := wsfFetchDoc(client, u)
		if err == nil {
			return doc, nil
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}
	return nil, lastErr
}

func wsfFindMaxPages(client *http.Client) (int, error) {
	doc, err := wsfFetchDoc(client, wsfListURL(1))
	if err != nil {
		return 0, err
	}
	max := 1
	re := regexp.MustCompile(`/page/(\d+)/?`)
	doc.Find(`a[href*="/page/"]`).Each(func(_ int, s *goquery.Selection) {
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

// Trên trang list, mỗi post có link vào detail; ta lấy tất cả link bài
func wsfExtractDetailLinks(doc *goquery.Document) ([]string, map[string]string) {
	var links []string
	thumbByDetail := map[string]string{}

	// 1) Lấy <a> trong article/post (như cũ)
	doc.Find("article a[href]").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		abs := wsfAbs(href)
		if wsfIsDetailURL(abs) {
			links = append(links, abs)
			if img := a.Find("img"); img.Length() > 0 {
				if src, ok := img.Attr("src"); ok && src != "" {
					thumbByDetail[abs] = wsfAbs(src)
				}
			}
		}
	})

	// 2) Bắt thêm anchor trực tiếp ngay dưới container id=post-XXXX
	doc.Find(`[id^="post-"] > a[href]`).Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		abs := wsfAbs(href)
		if wsfIsDetailURL(abs) {
			links = append(links, abs)
			// thumbnail nếu có <img> bên trong anchor
			if img := a.Find("img"); img.Length() > 0 {
				if src, ok := img.Attr("src"); ok && src != "" {
					thumbByDetail[abs] = wsfAbs(src)
				}
			}
		}
	})

	// 3) Fallback: tiêu đề bài
	doc.Find(".entry-title a[href]").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		abs := wsfAbs(href)
		if wsfIsDetailURL(abs) {
			links = append(links, abs)
		}
	})

	return uniq(links), thumbByDetail
}

func wsfIsDetailURL(href string) bool {
	h := strings.ToLower(href)
	if !strings.HasPrefix(h, "http") {
		return false
	}
	u, err := url.Parse(h)
	if err != nil {
		return false
	}
	if !strings.Contains(u.Host, "worksheetfun.com") {
		return false
	}
	l := strings.ToLower(u.Path)
	// loại file media
	if strings.HasSuffix(l, ".jpg") || strings.HasSuffix(l, ".jpeg") || strings.HasSuffix(l, ".png") || strings.HasSuffix(l, ".zip") {
		return false
	}
	// chấp nhận đa số URL bài viết
	return true
}

func wsfAbs(href string) string {
	u, err := url.Parse(href)
	if err != nil {
		return href
	}
	if u.IsAbs() {
		return href
	}
	base, _ := url.Parse(wsfBaseURL + "/")
	return base.ResolveReference(u).String()
}

// ---------- Detail parsing ----------

func wsfParseDetail(client *http.Client, detailURL string) (Item, error) {
	// NEW: nếu detailURL là PDF trực tiếp (trường hợp #post-XXXX > a trỏ thẳng .pdf)
	if strings.HasSuffix(strings.ToLower(detailURL), ".pdf") {
		return Item{
			Title:   fallbackTitle(detailURL), // dùng tên file làm title fallback
			PDFURL:  detailURL,
			IMGURL:  "", // có thể được gán từ thumbMap ở caller
			URL:     detailURL,
			Subject: "", // thường không rõ ở list; có thể suy từ category nếu cần
		}, nil
	}

	// (phần còn lại giữ nguyên như cũ)
	doc, err := wsfFetchDoc(client, detailURL)
	if err != nil {
		return Item{}, err
	}

	// Title: ưu tiên h1.entry-title; fallback title tag
	title := strings.TrimSpace(doc.Find("h1.entry-title").First().Text())
	if title == "" {
		title = strings.TrimSpace(doc.Find("title").First().Text())
	}

	// Subject: lấy từ categories/tags
	subject := ""
	var subjects []string
	doc.Find(`a[rel="category tag"], a[href*="/category/"], a[rel="tag"], a[href*="/tag/"]`).Each(func(_ int, s *goquery.Selection) {
		t := cleanText(s.Text())
		if t != "" {
			subjects = append(subjects, t)
		}
	})
	if s := pickLikelySubject(subjects); s != "" {
		subject = s
	} else if len(subjects) > 0 {
		subject = subjects[0]
	}

	// PDF: .pdf hoặc anchor có chữ download/pdf
	var pdf string
	doc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		h, _ := a.Attr("href")
		abs := wsfAbs(h)
		txt := strings.ToLower(strings.TrimSpace(a.Text()))
		l := strings.ToLower(abs)
		switch {
		case strings.HasSuffix(l, ".pdf"):
			if pdf == "" {
				pdf = abs
			}
		case strings.Contains(txt, "download") || strings.Contains(txt, "pdf"):
			if pdf == "" {
				pdf = abs
			}
		}
	})

	// Image/thumbnail: prefer og:image; else ảnh đầu trong nội dung
	var img string
	if og := doc.Find(`meta[property="og:image"]`).First(); og.Length() > 0 {
		if c, ok := og.Attr("content"); ok && c != "" {
			img = wsfAbs(c)
		}
	}
	if img == "" {
		if im := doc.Find("article img, .entry-content img").First(); im.Length() > 0 {
			if src, ok := im.Attr("src"); ok && src != "" {
				img = wsfAbs(src)
			}
		}
	}

	return Item{
		Title:   cleanText(title),
		PDFURL:  pdf,
		IMGURL:  img,
		URL:     detailURL,
		Subject: subject,
	}, nil
}

// ---------- Append JSON & helpers ----------

func wsfAppendItems(path string, items []Item) error {
	if len(items) == 0 {
		return nil
	}
	// JSONL
	if strings.HasSuffix(strings.ToLower(path), ".jsonl") {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
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

	// JSON array
	existing, _ := loadItems(path)
	existing = append(existing, items...)
	sort.Slice(existing, func(i, j int) bool {
		return strings.ToLower(existing[i].Title) < strings.ToLower(existing[j].Title)
	})
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(existing); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func wsfReadCheckpoint(path string) (WSFCheckpoint, error) {
	var cp WSFCheckpoint
	f, err := os.Open(path)
	if err != nil {
		return cp, err
	}
	defer f.Close()
	if err := json.NewDecoder(f).Decode(&cp); err != nil {
		return cp, err
	}
	return cp, nil
}

func wsfWriteCheckpoint(path string, page int) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	cp := WSFCheckpoint{LastPage: page}
	if err := json.NewEncoder(f).Encode(&cp); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Chuẩn hoá URL category: đảm bảo có /page/1 hoặc có trailing slash
func wsfNormalizeCatURL(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return ""
	}
	if !strings.HasSuffix(u, "/") {
		u += "/"
	}
	// nếu đã có /page/N/ thì giữ nguyên; nếu không có thì thêm page/1/
	re := regexp.MustCompile(`/page/\d+/`)
	if re.MatchString(u) {
		return u
	}
	if !strings.Contains(u, "/page/") {
		if !strings.HasSuffix(u, "/") {
			u += "/"
		}
		u = strings.TrimSuffix(u, "/") + "/page/1/"
	}
	return u
}

// Tạo URL cho trang N dựa trên category base
func wsfCatPageURL(baseCat string, n int) string {
	baseCat = wsfNormalizeCatURL(baseCat)
	re := regexp.MustCompile(`(/page/)(\d+)(/)`)
	return re.ReplaceAllString(baseCat, fmt.Sprintf("${1}%d${3}", n))
}

func wsfFindMaxPagesForCategory(client *http.Client, baseCat string) (int, error) {
	baseCat = wsfNormalizeCatURL(baseCat)
	doc, err := wsfFetchDoc(client, wsfCatPageURL(baseCat, 1))
	if err != nil {
		return 0, err
	}
	max := 1
	re := regexp.MustCompile(`/page/(\d+)/?`)
	doc.Find(`a[href*="/page/"]`).Each(func(_ int, s *goquery.Selection) {
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

// ---- small utils ----

func tern[T any](cond bool, a, b T) T {
	if cond {
		return a
	}
	return b
}
