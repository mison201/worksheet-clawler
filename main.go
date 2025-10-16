package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
)

// ======================= CONFIG =======================

const (
	defaultAddr = ":8080"
	defaultOut  = "merged_output" // nơi lưu file merge đã tạo
)

// ======================= DATA TYPES ===================

type Item struct {
	Title  string `json:"title"`
	PDFURL string `json:"pdf_url"`
	IMGURL string `json:"img_url"`              // thumbnail/cover để hiển thị
	URL    string `json:"detail_url,omitempty"` // optional để debug
}

type MergeRequest struct {
	Files []string `json:"files"` // danh sách PDF URL
	Out   string   `json:"out"`   // tên file output (không có .pdf)
}

// ======================= FLAGS ========================

var (
	dataPath = flag.String("data", "items.jsonl", "path to data file (JSON array or JSONL) with fields: title,pdf_url,img_url")
	addrFlag = flag.String("addr", defaultAddr, "http listen address, e.g. :8080")
	outDir   = flag.String("out", defaultOut, "output directory for merged PDFs")
)

// ======================= TEMPLATE =====================

var funcMap = template.FuncMap{
	"f64": func(n any) float64 {
		switch v := n.(type) {
		case int64:
			return float64(v)
		case int:
			return float64(v)
		case float64:
			return v
		default:
			return 0
		}
	},
}

var page = template.Must(template.New("index").Funcs(funcMap).Parse(`
<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <title>Worksheet PDF Picker</title>
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <style>
    :root { --border:#eee; --muted:#666; }
    * { box-sizing:border-box; }
    body { font-family: system-ui, -apple-system, Segoe UI, Roboto, sans-serif; margin: 24px; }
    h1 { margin:0 0 16px 0; font-size:22px; }
    .wrap { display:grid; grid-template-columns: 1.2fr 380px; gap:24px; }
    .list { display:grid; grid-template-columns: repeat(auto-fill, minmax(260px, 1fr)); gap:14px; }
    .card { border:1px solid var(--border); border-radius:12px; overflow:hidden; display:flex; flex-direction:column; }
    .thumb { width:100%; aspect-ratio:4/3; object-fit:cover; background:#fafafa; display:block; }
    .meta { padding:10px 12px; display:flex; gap:8px; align-items:center; }
    .title { font-weight:600; font-size:14px; line-height:1.4; }
    .muted { color:var(--muted); font-size:12px; }
    .pick { width:18px; height:18px; }
    .order { width:70px; padding:8px; border:1px solid #ddd; border-radius:8px; }
    .btn { padding:10px 14px; border:0; background:#111; color:#fff; border-radius:10px; cursor:pointer; }
    .side { position:sticky; top:20px; height:fit-content; }
    .box { border:1px solid var(--border); border-radius:12px; padding:12px; }
    .row { display:flex; gap:8px; align-items:center; }
    input[type="text"] { width:100%; padding:10px; border:1px solid #ddd; border-radius:8px; }
    .toolbar { display:flex; gap:10px; margin-bottom:12px; align-items:center; }
    .search { flex:1; }
    .small { font-size:12px; }
    iframe { width:100%; height:420px; border:0; border-radius:8px; }
    @media (max-width: 980px) {
      .wrap { grid-template-columns: 1fr; }
      .side { position: static; }
    }
  </style>
</head>
<body>
  <h1>Worksheet Picker (from links)</h1>

  <div class="toolbar">
    <input id="q" class="search" type="text" placeholder="Filter by title...">
    <span class="small" id="countLabel"></span>
  </div>

  <div class="wrap">
    <div>
      <div id="list" class="list">
        {{range .Items}}
        <div class="card" data-title="{{.Title}}" data-pdf="{{.PDFURL}}">
          <img class="thumb" loading="lazy" src="{{.IMGURL}}" alt="thumb" onerror="this.style.display='none'">
          <div class="meta">
            <input type="checkbox" class="pick" title="Select" />
            <div style="flex:1; min-width:0">
              <div class="title" title="{{.Title}}">{{.Title}}</div>
              <div class="muted" style="word-break:break-all">{{.PDFURL}}</div>
            </div>
          </div>
          <div style="display:flex; gap:8px; padding:0 12px 12px 12px;">
            <input type="number" class="order" min="1" step="1" placeholder="# order">
            <button type="button" class="btn small" onclick="preview(this)">Preview</button>
          </div>
        </div>
        {{end}}
      </div>
    </div>

    <div class="side">
      <div class="box" style="margin-bottom:12px;">
        <h3 style="margin:0 0 8px 0">Output</h3>
        <div class="row">
          <input id="outname" type="text" placeholder="merged_kiddo">
          <button id="mergeBtn" class="btn">Merge</button>
        </div>
        <div id="status" style="margin-top:10px;"></div>
      </div>

      <div class="box">
        <h3 style="margin:0 0 8px 0">Preview</h3>
        <iframe id="pv" src="" title="Preview"></iframe>
        <div class="muted">Xem trực tiếp PDF qua <code>pdf_url</code> (nếu site không chặn iFrame).</div>
      </div>
    </div>
  </div>

<script>
  const list = document.getElementById('list');
  const q = document.getElementById('q');
  const label = document.getElementById('countLabel');

  function countShown() {
    const cards = Array.from(list.children);
    const shown = cards.filter(c => c.style.display !== 'none').length;
    label.textContent = shown + ' items';
  }
  countShown();

  q.addEventListener('input', () => {
    const term = (q.value || '').toLowerCase();
    Array.from(list.children).forEach(c => {
      const title = (c.getAttribute('data-title')||'').toLowerCase();
      c.style.display = title.includes(term) ? '' : 'none';
    });
    countShown();
  });

  function preview(btn) {
    const card = btn.closest('.card');
    const pdf = card.getAttribute('data-pdf');
    document.getElementById('pv').src = pdf + '#page=1&zoom=page-width';
  }

  function selection() {
    const entries = [];
    Array.from(list.children).forEach(c => {
      if (c.style.display === 'none') return;
      const pick = c.querySelector('.pick');
      if (!pick.checked) return;
      const order = parseInt(c.querySelector('.order').value || '0', 10);
      entries.push({pdf: c.getAttribute('data-pdf'), ord: order > 0 ? order : 1e9, title: c.getAttribute('data-title')});
    });
    entries.sort((a,b) => a.ord - b.ord || a.title.localeCompare(b.title));
    return entries.map(e => e.pdf);
  }

  document.getElementById('mergeBtn').addEventListener('click', async () => {
    const files = selection();
    const out = (document.getElementById('outname').value || 'merged_kiddo').replace(/\s+/g, '_');
    const status = document.getElementById('status');
    if (files.length < 2) {
      status.innerHTML = '<span style="color:#c00">Chọn ít nhất 2 item.</span>';
      return;
    }
    status.textContent = 'Downloading & merging...';
    const resp = await fetch('/merge', {
      method:'POST',
      headers:{'Content-Type':'application/json'},
      body: JSON.stringify({files, out})
    });
    const data = await resp.json();
    if (resp.ok) {
      status.innerHTML = '✅ Done: <a href="'+data.download+'" target="_blank" rel="noreferrer">'+data.download+'</a>';
    } else {
      status.innerHTML = '❌ ' + (data.error || 'Merge failed');
    }
  });
</script>
</body>
</html>
`))

// ======================= MAIN ========================

func main() {
	flag.Parse()

	// Load items from data file
	items, err := loadItems(*dataPath)
	if err != nil {
		log.Fatalf("load items: %v", err)
	}
	if len(items) == 0 {
		log.Printf("Warning: no items found in %s", *dataPath)
	}

	// Ensure output dir
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatal(err)
	}

	// Index
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// sort by Title asc
		sort.Slice(items, func(i, j int) bool { return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title) })
		data := struct {
			Items []Item
		}{Items: items}
		_ = page.Execute(w, data)
	})

	// Merge endpoint: download then merge
	http.HandleFunc("/merge", func(w http.ResponseWriter, r *http.Request) {
		type req struct {
			Files []string `json:"files"`
			Out   string   `json:"out"`
		}
		var in req
		if err := json.NewDecoder(bufio.NewReader(r.Body)).Decode(&in); err != nil {
			http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
			return
		}
		if len(in.Files) < 2 {
			http.Error(w, `{"error":"select at least 2 files"}`, http.StatusBadRequest)
			return
		}
		outName := strings.TrimSpace(in.Out)
		if outName == "" {
			outName = "merged_kiddo"
		}
		outName = sanitizeNoExt(outName) + ".pdf"
		outPath := filepath.Join(*outDir, outName)

		tmpDir, err := os.MkdirTemp("", "merge_dl_*")
		if err != nil {
			http.Error(w, `{"error":"cannot create temp dir"}`, http.StatusInternalServerError)
			return
		}
		defer os.RemoveAll(tmpDir)

		var localFiles []string
		var skipped []string

		for i, u := range in.Files {
			lp := filepath.Join(tmpDir, "f_"+strconv.Itoa(i)+".pdf")
			if err := downloadPDF(u, lp); err != nil {
				log.Printf("[skip] %s -> %v", u, err)
				skipped = append(skipped, u)
				continue
			}
			localFiles = append(localFiles, lp)
		}

		if len(localFiles) < 2 {
			http.Error(w, `{"error":"not enough valid PDFs to merge","skipped":"`+escape(strings.Join(skipped, ","))+`"}`, http.StatusBadRequest)
			return
		}

		if err := pdfapi.MergeCreateFile(localFiles, outPath, false, nil); err != nil {
			if e2 := pdfapi.MergeAppendFile(localFiles, outPath, false, nil); e2 != nil {
				http.Error(w, `{"error":"merge failed"}`, http.StatusInternalServerError)
				return
			}
		}

		resp := map[string]any{
			"ok":       1,
			"download": "/download/" + urlPath(outName),
			"skipped":  skipped,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	// Serve merged outputs
	http.Handle("/download/", http.StripPrefix("/download/", http.FileServer(http.Dir(*outDir))))

	log.Printf("UI: http://localhost%s  | data=%s  | out=%s", *addrFlag, *dataPath, *outDir)
	log.Fatal(http.ListenAndServe(*addrFlag, nil))
}

// ======================= HELPERS ======================

func loadItems(path string) ([]Item, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// detect jsonl vs json array
	if strings.HasSuffix(strings.ToLower(path), ".jsonl") {
		var items []Item
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			var it Item
			if err := json.Unmarshal([]byte(line), &it); err != nil {
				return nil, err
			}
			// basic validation
			if it.PDFURL == "" {
				continue
			}
			items = append(items, it)
		}
		return items, sc.Err()
	}

	// else treat as JSON array
	var items []Item
	if err := json.NewDecoder(f).Decode(&items); err != nil {
		return nil, err
	}
	return items, nil
}

// replace the existing downloadPDF with this smarter version:

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

	// handle redirects already followed by http.Client
	if resp.StatusCode >= 400 {
		return errors.New("http " + strconv.Itoa(resp.StatusCode))
	}

	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ct, "pdf") || strings.HasSuffix(strings.ToLower(u), ".pdf") || ct == "application/octet-stream" {
		// looks like a PDF (some servers use octet-stream)
		f, err := os.Create(outPath)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(f, resp.Body)
		return err
	}

	// 2) If HTML, try to extract a direct .pdf from the page
	if strings.Contains(ct, "text/html") {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		pdfURL := findPDFLinkInHTML(string(body), u)
		if pdfURL == "" {
			// as a last chance: sometimes anchor text has 'download'
			return fmt.Errorf("no direct PDF link found in HTML page: %s", u)
		}
		// recursive call to download the real PDF
		return downloadPDF(pdfURL, outPath)
	}

	// 3) Unknown type
	return fmt.Errorf("unsupported content-type %s for %s", ct, u)
}

// Parse HTML to find a <a href="...pdf"> or an anchor whose text contains Download/PDF.
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

func sanitizeNoExt(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '-' || r == '_' ||
			(r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "merged"
	}
	return b.String()
}

func escape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, `"`, `'`), "\n", " ")
}

func urlPath(name string) string {
	return strings.ReplaceAll(name, " ", "_")
}
