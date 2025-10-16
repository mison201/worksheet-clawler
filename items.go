package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

const (
	baseURL   = "https://www.kiddoworksheets.com"
	listPath  = "/all-downloads/"
	userAgent = "KiddoCrawler-Items/1.0 (+https://example.local)"
)

var (
	outPath     = flag.String("out", "items.jsonl", "output file: .jsonl (default) or .json")
	startPage   = flag.Int("start", 1, "start page number")
	endPage     = flag.Int("end", 0, "end page number (0 = auto detect)")
	maxItems    = flag.Int("max", 0, "max items to collect (0 = unlimited)")
	delayMs     = flag.Int("delay", 1200, "delay between requests in milliseconds")
	debugLog    = flag.Bool("debug", false, "verbose logging")
	httpTimeout = flag.Int("timeout", 30, "HTTP timeout seconds")
)

func itemMain() {
	flag.Parse()

	client := &http.Client{Timeout: time.Duration(*httpTimeout) * time.Second}

	// Mở file output
	if err := os.MkdirAll(filepath.Dir(absPath(*outPath)), 0o755); err != nil && !os.IsExist(err) {
		log.Fatal(err)
	}

	// Thu thập items
	var items []Item

	// Tìm số trang tối đa nếu endPage=0
	pages := *endPage
	if pages == 0 {
		n, err := findMaxPages(client)
		if err != nil {
			log.Printf("[warn] cannot detect max pages: %v -> fallback 100", err)
			pages = 100
		} else {
			pages = n
		}
	}
	if *debugLog {
		log.Printf("[info] scanning pages %d..%d", *startPage, pages)
	}

	count := 0
	for p := *startPage; p <= pages; p++ {
		listURL := resolveListURL(p)
		if *debugLog {
			log.Printf("[list] %s", listURL)
		}
		doc, err := fetchDoc(client, listURL)
		if err != nil {
			log.Printf("[list] skip page %d: %v", p, err)
			continue
		}

		// Dò link vào detail
		detailLinks := extractDetailLinks(doc, baseURL)
		if *debugLog {
			log.Printf("  found %d detail links", len(detailLinks))
		}

		// Lấy fallback thumbnail (từ listing) cho từng detail nếu có thể
		thumbByDetail := map[string]string{}
		doc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
			href, _ := a.Attr("href")
			href = toAbs(href)
			if !isDetailURL(href) {
				return
			}
			// thử lấy ảnh con trong a > img
			if img := a.Find("img"); img.Length() > 0 {
				if src, ok := img.Attr("src"); ok && src != "" {
					thumbByDetail[href] = toAbs(src)
				}
			}
		})

		for _, durl := range detailLinks {
			if *maxItems > 0 && count >= *maxItems {
				break
			}
			// tôn trọng delay
			time.Sleep(time.Duration(*delayMs) * time.Millisecond)

			item, err := parseDetail(client, durl)
			if err != nil {
				if *debugLog {
					log.Printf("  [detail] %s -> %v", durl, err)
				}
				continue
			}
			// nếu thiếu ảnh thì dùng fallback từ listing
			if item.IMGURL == "" {
				if tb, ok := thumbByDetail[durl]; ok {
					item.IMGURL = tb
				}
			}
			// đủ dữ liệu tối thiểu?
			if item.Title == "" || item.PDFURL == "" {
				if *debugLog {
					log.Printf("  [skip] missing title/pdf for %s", durl)
				}
				continue
			}

			items = append(items, item)
			count++
			if *debugLog {
				log.Printf("  [+] %s", item.Title)
			}
		}
		if *maxItems > 0 && count >= *maxItems {
			break
		}
	}

	// Ghi ra file
	if strings.HasSuffix(strings.ToLower(*outPath), ".jsonl") {
		if err := writeJSONL(*outPath, items); err != nil {
			log.Fatal(err)
		}
	} else {
		if err := writeJSONArray(*outPath, items); err != nil {
			log.Fatal(err)
		}
	}
	log.Printf("Done. Wrote %d items -> %s", len(items), *outPath)
}

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

func fetchDoc(client *http.Client, u string) (*goquery.Document, error) {
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", userAgent)
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
	title := strings.TrimSpace(doc.Find("h1").First().Text())
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
	if !strings.HasSuffix(strings.ToLower(pdf), ".pdf") {
		// để nguyên; một số trang có redirect download
	}

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

	return Item{
		Title:  title,
		PDFURL: pdf,
		IMGURL: img,
		URL:    detailURL,
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

func absPath(p string) string {
	if p == "" {
		return "."
	}
	ap, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return ap
}
