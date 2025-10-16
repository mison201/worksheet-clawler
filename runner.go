package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

type CrawlConfig struct {
	DataPath  string
	CPPath    string
	StartPage int
	EndPage   int // 0 = auto detect
	DelayMs   int
	MaxItems  int // 0 = unlimited
	UserAgent string
}

type checkpoint struct {
	LastPage int `json:"last_page"` // đã crawl xong tới trang này
}

func RunCrawlerWithCheckpoint(cfg CrawlConfig) error {
	// chuẩn bị client
	client := &http.Client{Timeout: 30 * time.Second}

	// lấy last_page từ checkpoint (nếu có)
	cp, _ := readCheckpoint(cfg.CPPath)
	start := cfg.StartPage
	if cp.LastPage > 0 {
		start = cp.LastPage + 1
	}
	if start < 1 {
		start = 1
	}

	// tìm end nếu = 0
	end := cfg.EndPage
	if end == 0 {
		n, err := findMaxPages(client)
		if err != nil {
			log.Printf("[crawl] cannot detect max pages: %v, fallback 100", err)
			end = 100
		} else {
			end = n
		}
	}

	// ensure data dir
	if err := osMkdirAll(filepath.Dir(absPath(cfg.DataPath)), 0o755); err != nil && !os.IsExist(err) {
		return err
	}

	log.Printf("[crawl] start=%d end=%d data=%s cp=%s", start, end, cfg.DataPath, cfg.CPPath)

	// pre-load để dedup
	existing, _ := loadItems(cfg.DataPath)
	seen := make(map[string]struct{}, len(existing))
	for _, it := range existing {
		if it.PDFURL != "" {
			seen[strings.TrimSpace(it.PDFURL)] = struct{}{}
		}
	}

	var batch []Item
	collected := 0

	for p := start; p <= end; p++ {
		listURL := resolveListURL(p)
		log.Printf("[crawl] page %d: %s", p, listURL)

		// tải trang list
		doc, err := fetchDoc(client, listURL)
		if err != nil {
			log.Printf("[crawl] skip page %d: %v", p, err)
			// vẫn cập nhật checkpoint để không kẹt ở trang lỗi mãi
			_ = writeCheckpoint(cfg.CPPath, p)
			continue
		}

		// lấy link chi tiết
		detailLinks := extractDetailLinks(doc, baseURL)
		if len(detailLinks) == 0 {
			log.Printf("[crawl] page %d: no detail links", p)
			_ = writeCheckpoint(cfg.CPPath, p)
			continue
		}

		// fallback thumbnail map từ trang list (nếu cần)
		thumbByDetail := make(map[string]string)
		doc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
			href, _ := a.Attr("href")
			href = toAbs(href)
			if !isDetailURL(href) {
				return
			}
			if img := a.Find("img"); img.Length() > 0 {
				if src, ok := img.Attr("src"); ok && src != "" {
					thumbByDetail[href] = toAbs(src)
				}
			}
		})

		// duyệt chi tiết
		for _, durl := range detailLinks {
			if cfg.MaxItems > 0 && collected >= cfg.MaxItems {
				break
			}
			time.Sleep(time.Duration(cfg.DelayMs) * time.Millisecond)

			it, err := parseDetail(client, durl)
			if err != nil {
				log.Printf("  [detail] %s -> %v", durl, err)
				continue
			}
			if it.IMGURL == "" {
				if tb, ok := thumbByDetail[durl]; ok {
					it.IMGURL = tb
				}
			}
			// đủ dữ liệu và chưa trùng pdf_url?
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

		// ghi batch ra file định kỳ (tránh mất dữ liệu nếu crash)
		if len(batch) > 0 {
			if err := appendItems(cfg.DataPath, batch); err != nil {
				return err
			}
			batch = batch[:0]
		}

		// cập nhật checkpoint sau mỗi trang
		if err := writeCheckpoint(cfg.CPPath, p); err != nil {
			log.Printf("[crawl] warn write checkpoint: %v", err)
		}

		if cfg.MaxItems > 0 && collected >= cfg.MaxItems {
			break
		}
	}

	log.Printf("[crawl] collected %d new items", collected)
	return nil
}

// ---- checkpoint I/O ----

func readCheckpoint(path string) (checkpoint, error) {
	var cp checkpoint
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

func writeCheckpoint(path string, page int) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	cp := checkpoint{LastPage: page}
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

// ---- append & dedup ----

func appendItems(path string, items []Item) error {
	if len(items) == 0 {
		return nil
	}

	// JSONL: append từng dòng
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

	// JSON array: nạp cũ -> thêm -> sort -> ghi lại
	existing, _ := loadItems(path)
	existing = append(existing, items...)

	// optional: sort by Title để file ổn định
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

// ---- dùng lại fetchDoc nhưng đảm bảo User-Agent ----

func fetchDoc(client *http.Client, u string) (*goquery.Document, error) {
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", userAgent) // hoặc cfg.UserAgent nếu bạn muốn
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

// absPath nhỏ để lấy dir output
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
