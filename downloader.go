package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// downloadPDF:
// - Nếu URL trả PDF (content-type hoặc đuôi .pdf) -> ghi file.
// - Nếu trả HTML -> parse để tìm link .pdf / link "Download", rồi tải tiếp.
// - Hỗ trợ "application/octet-stream" (nhiều site dùng khi tải file).
func downloadPDF(u, outPath string) error {
	client := &http.Client{Timeout: 60 * time.Second}

	// 1) Try GET u
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "WorksheetMerger/1.1 (+https://example.local)")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return errors.New("http " + strconv.Itoa(resp.StatusCode))
	}

	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ct, "pdf") || strings.HasSuffix(strings.ToLower(u), ".pdf") || ct == "application/octet-stream" {
		f, err := os.Create(outPath)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(f, resp.Body)
		return err
	}

	// 2) HTML -> cố gắng tìm link PDF
	if strings.Contains(ct, "text/html") {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		pdfURL := findPDFLinkInHTML(string(body), u)
		if pdfURL == "" {
			return fmt.Errorf("no direct PDF link found in HTML page: %s", u)
		}
		return downloadPDF(pdfURL, outPath)
	}

	return fmt.Errorf("unsupported content-type %s for %s", ct, u)
}

// Parse HTML để tìm <a href="...pdf"> hoặc anchor text có "download"/"pdf".
func findPDFLinkInHTML(html, base string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return ""
	}
	var candidates []string

	doc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		txt := strings.ToLower(strings.TrimSpace(a.Text()))
		abs := mustAbsURL(base, href)
		l := strings.ToLower(abs)
		switch {
		case strings.HasSuffix(l, ".pdf"):
			candidates = append(candidates, abs)
		case strings.Contains(txt, "download") || strings.Contains(txt, "pdf"):
			candidates = append(candidates, abs)
		}
	})

	// prefer .pdf endings
	for _, c := range candidates {
		if strings.HasSuffix(strings.ToLower(c), ".pdf") {
			return c
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return ""
}

func mustAbsURL(baseStr, href string) string {
	bu, err := url.Parse(baseStr)
	if err != nil {
		return href
	}
	hu, err := url.Parse(href)
	if err != nil {
		return href
	}
	return bu.ResolveReference(hu).String()
}
