package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
)

const (
	defaultRoot = "data_kiddo" // default PDF root
	outSubDir   = "_merged"    // output dir under root
	listenAddr  = ":8080"      // http server address
	maxFileScan = 100000       // safety cap
)

// FileItem represents a discovered PDF file.
type FileItem struct {
	Path string // absolute path
	Rel  string // relative path from root
	Size int64
	Mod  time.Time
}

// ---- template helpers ----

var funcMap = template.FuncMap{
	// convert interface{} (int64/float/int) to float64
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
	"div": func(a, b float64) float64 { return a / b },
	"mul": func(a, b float64) float64 { return a * b },
}

var page = template.Must(template.New("index").Funcs(funcMap).Parse(`
<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <title>PDF Merger</title>
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <style>
    :root { --bg:#fff; --fg:#111; --muted:#666; --border:#eee; }
    * { box-sizing: border-box; }
    body { font-family: system-ui, -apple-system, Segoe UI, Roboto, sans-serif; margin: 24px; color: var(--fg); background: var(--bg); }
    h1 { margin-top: 0; font-size: 22px; }
    .wrap { display: grid; grid-template-columns: 1fr 360px; gap: 24px; }
    table { width: 100%; border-collapse: collapse; }
    th, td { padding: 8px 10px; border-bottom: 1px solid var(--border); vertical-align: middle; }
    tr:hover { background: #fafafa; }
    .side { position: sticky; top: 20px; height: fit-content; }
    .btn { padding: 10px 14px; border: 0; background: #111; color: #fff; border-radius: 8px; cursor: pointer; }
    .btn:disabled { opacity: .5; cursor: not-allowed; }
    .row { display:flex; gap:8px; align-items:center; }
    input[type="text"], input[type="number"] { padding: 10px; border: 1px solid #ddd; border-radius: 8px; }
    input[type="number"] { width: 90px; }
    .muted { color: var(--muted); font-size: 12px; }
    .toolbar { display:flex; gap:10px; align-items:center; margin-bottom:10px; }
    .search { flex: 1; }
    .small { font-size: 12px; }
    .fileRow { cursor: default; }
    .badge { background:#f2f2f2; border:1px solid #e6e6e6; border-radius:999px; padding:2px 8px; font-size:11px; color:#444; }
    code { background:#f6f6f6; padding:2px 6px; border-radius:6px; }
    @media (max-width: 900px) {
      .wrap { grid-template-columns: 1fr; }
      .side { position: static; }
    }
  </style>
</head>
<body>
  <h1>PDF Merger</h1>

  <div class="toolbar">
    <input id="q" class="search" type="text" placeholder="Filter by name...">
    <span class="small">Root: <code>{{.Root}}</code></span>
    <span class="badge" id="countBadge"></span>
  </div>

  <div class="wrap">
    <div>
      <table id="tbl">
        <thead>
          <tr>
            <th style="width:56px;">Pick</th>
            <th>PDF</th>
            <th style="width:210px;">Order & Preview</th>
          </tr>
        </thead>
        <tbody id="tbody">
          {{range $i, $f := .Files}}
          <tr class="fileRow" data-rel="{{ $f.Rel }}" data-name="{{ $f.Rel }}">
            <td><input type="checkbox" class="pick" data-rel="{{ $f.Rel }}"></td>
            <td>
              <div><strong>{{$f.Rel}}</strong></div>
              <div class="muted">{{printf "%.2f" (div (f64 $f.Size) 1048576.0)}} MB · {{$f.Mod.Format "2006-01-02 15:04"}}</div>
            </td>
            <td>
              <div class="row">
                <input type="number" class="order" data-rel="{{ $f.Rel }}" min="1" step="1" placeholder="#">
                <button type="button" class="btn small previewBtn">Preview</button>
              </div>
            </td>
          </tr>
          {{end}}
        </tbody>
      </table>
    </div>

    <div class="side">
      <h3>Output</h3>
      <div style="margin-bottom:12px;">
        <label>Output filename (no spaces/ext):</label>
        <input id="outname" type="text" placeholder="merged_kiddo">
        <div class="muted">Lưu tại <code>{{.OutDir}}</code></div>
      </div>

      <button id="mergeBtn" class="btn">Merge selected PDFs</button>
      <div id="status" style="margin-top:12px;"></div>

      <hr>
      <h3>Preview</h3>
      <div id="previewBox" style="height:420px;border:1px solid #eee;border-radius:8px;overflow:hidden;">
        <iframe id="previewFrame" src="" style="width:100%;height:100%;border:0;" title="Preview"></iframe>
      </div>
      <div class="muted">Chọn một file (tick hoặc nút Preview) để xem trước.</div>
    </div>
  </div>

<script>
  const tbody = document.getElementById('tbody');
  const badge = document.getElementById('countBadge');

  function setPreview(rel) {
    if (!rel) return;
    document.getElementById('previewFrame').src = '/file?rel=' + encodeURIComponent(rel) + '#page=1&zoom=page-width';
  }

  function currentPicks() {
    return Array.from(document.querySelectorAll('.pick:checked')).map(cb => cb.getAttribute('data-rel'));
  }

  function collectSelection() {
    const picks = currentPicks();
    // map rel -> order (number)
    const orders = {};
    document.querySelectorAll('.order').forEach(inp => {
      const rel = inp.getAttribute('data-rel');
      const v = parseInt(inp.value || "0", 10);
      if (!isNaN(v) && v > 0) orders[rel] = v;
    });

    // sort by order then by name
    const ordered = picks.slice().sort((a, b) => {
      const oa = (orders[a] || 1e9);
      const ob = (orders[b] || 1e9);
      if (oa !== ob) return oa - ob;
      return a.localeCompare(b);
    });
    return ordered;
  }

  // Filter by query
  function filterRows(q) {
    const rows = tbody.querySelectorAll('tr.fileRow');
    let shown = 0;
    rows.forEach(tr => {
      const name = (tr.getAttribute('data-name') || '').toLowerCase();
      const match = !q || name.includes(q);
      tr.style.display = match ? '' : 'none';
      if (match) shown++;
    });
    badge.textContent = shown + ' files';
  }

  // Initialize counts
  filterRows('');

  // Search input
  document.getElementById('q').addEventListener('input', (e) => {
    filterRows((e.target.value || '').toLowerCase());
  });

  // Row click handlers
  tbody.addEventListener('click', (e) => {
    const row = e.target.closest('tr.fileRow');
    if (!row) return;

    if (e.target.classList.contains('previewBtn')) {
      setPreview(row.getAttribute('data-rel'));
    }
    if (e.target.classList.contains('pick')) {
      const rel = e.target.getAttribute('data-rel');
      // Preview the 1st picked
      if (document.querySelectorAll('.pick:checked').length === 1) setPreview(rel);
    }
  });

  // Merge
  document.getElementById('mergeBtn').addEventListener('click', async () => {
    const list = collectSelection();
    const name = (document.getElementById('outname').value || 'merged_kiddo').replace(/\s+/g, '_');

    if (list.length < 2) {
      document.getElementById('status').innerHTML = '<span style="color:#c00">Chọn ít nhất 2 file.</span>';
      return;
    }

    document.getElementById('mergeBtn').disabled = true;
    document.getElementById('status').textContent = 'Merging...';

    try {
      const resp = await fetch('/merge', {
        method: 'POST',
        headers: {'Content-Type':'application/json'},
        body: JSON.stringify({ files: list, out: name })
      });
      const data = await resp.json();
      if (resp.ok) {
        document.getElementById('status').innerHTML = '✅ Done: <a href="' + data.download + '" target="_blank" rel="noreferrer">' + data.download + '</a>';
      } else {
        document.getElementById('status').innerHTML = '❌ '+ (data.error || 'Merge error');
      }
    } catch (e) {
      document.getElementById('status').textContent = '❌ ' + e;
    } finally {
      document.getElementById('mergeBtn').disabled = false;
    }
  });
</script>
</body>
</html>
`))

// ---- flags ----
var (
	rootDirFlag = flag.String("root", defaultRoot, "root directory to scan PDFs")
	addrFlag    = flag.String("addr", listenAddr, "http listen address (e.g. :8080)")
)

func main() {
	flag.Parse()
	root := *rootDirFlag

	// Ensure output dir exists
	if err := os.MkdirAll(filepath.Join(root, outSubDir), 0o755); err != nil {
		log.Fatal(err)
	}

	// Handlers using closure over 'root'
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		files, err := scanPDFs(root)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		log.Printf("[scan] found %d PDFs under %s", len(files), root)
		sort.Slice(files, func(i, j int) bool { return files[i].Mod.After(files[j].Mod) })
		data := struct {
			Files        []FileItem
			OutDir, Root string
		}{files, filepath.Join(root, outSubDir), root}
		_ = page.Execute(w, data)
	})

	http.HandleFunc("/merge", func(w http.ResponseWriter, r *http.Request) {
		type req struct {
			Files []string `json:"files"` // relative paths
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
		outName = sanitize(outName) + ".pdf"

		// Build absolute input paths and validate they are inside root
		var absInputs []string
		for _, rel := range in.Files {
			ap := filepath.Join(root, filepath.FromSlash(rel))
			if !strings.HasPrefix(abs(ap)+string(os.PathSeparator), abs(root)+string(os.PathSeparator)) {
				http.Error(w, `{"error":"invalid path"}`, http.StatusBadRequest)
				return
			}
			if st, err := os.Stat(ap); err != nil || !st.Mode().IsRegular() {
				http.Error(w, `{"error":"file not found: `+escape(rel)+`"}`, http.StatusNotFound)
				return
			}
			absInputs = append(absInputs, ap)
		}

		outPath := filepath.Join(root, outSubDir, outName)

		// Merge via pdfcpu API
		if err := pdfapi.MergeCreateFile(absInputs, outPath, false, nil); err != nil {
			// fallback: try append mode (older pdfcpu versions)
			if e2 := pdfapi.MergeAppendFile(absInputs, outPath, false, nil); e2 != nil {
				http.Error(w, `{"error":"merge failed: `+escape(err.Error())+`"}`, http.StatusInternalServerError)
				return
			}
		}

		resp := map[string]string{
			"ok":       "1",
			"download": "/download/" + urlPath(outName),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	// Serve merged files for download
	http.Handle("/download/", http.StripPrefix("/download/",
		http.FileServer(http.Dir(filepath.Join(root, outSubDir))),
	))

	// Serve original PDF for preview (inline)
	http.HandleFunc("/file", func(w http.ResponseWriter, r *http.Request) {
		rel := r.URL.Query().Get("rel")
		if rel == "" {
			http.Error(w, "missing rel", http.StatusBadRequest)
			return
		}
		ap := filepath.Join(root, filepath.FromSlash(rel))
		// prevent traversal
		if !strings.HasPrefix(abs(ap)+string(os.PathSeparator), abs(root)+string(os.PathSeparator)) {
			http.Error(w, "invalid path", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		http.ServeFile(w, r, ap)
	})

	log.Printf("PDF Merger UI at http://localhost%v  (root: %s)", *addrFlag, root)
	log.Fatal(http.ListenAndServe(*addrFlag, nil))
}

// ---- utilities ----

func scanPDFs(root string) ([]FileItem, error) {
	var files []FileItem
	count := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := strings.ToLower(d.Name())
		if strings.HasSuffix(name, ".pdf") {
			info, _ := d.Info()
			rel, _ := filepath.Rel(root, path)
			files = append(files, FileItem{
				Path: path,
				Rel:  filepath.ToSlash(rel),
				Size: info.Size(),
				Mod:  info.ModTime(),
			})
			count++
			if count >= maxFileScan {
				return filepath.SkipAll
			}
		}
		return nil
	})
	return files, err
}

func abs(p string) string {
	a, _ := filepath.Abs(p)
	return a
}

func sanitize(s string) string {
	// keep letters, numbers, dash, underscore and dots (for optional sub-name)
	var b strings.Builder
	for _, r := range s {
		if r == '-' || r == '_' || r == '.' ||
			(r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "merged"
	}
	// ensure no double extension
	out := b.String()
	if strings.HasSuffix(strings.ToLower(out), ".pdf") {
		out = strings.TrimSuffix(out, ".pdf")
	}
	return out
}

func escape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, `"`, `'`), "\n", " ")
}

func urlPath(name string) string {
	return strings.ReplaceAll(name, " ", "_")
}

// optional helper: parse int safely (unused here)
func atoiSafe(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
