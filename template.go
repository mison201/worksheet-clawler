package main

import "html/template"

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
    input[type="text"], select { padding:10px; border:1px solid #ddd; border-radius:8px; }
    .toolbar { display:flex; gap:10px; margin-bottom:12px; align-items:center; flex-wrap:wrap; }
    .search { flex:1; min-width:240px; }
    .small { font-size:12px; }
    .chips { display:flex; gap:6px; flex-wrap:wrap; margin-top:6px; }
    .chip { background:#f5f5f5; border:1px solid #e9e9e9; border-radius:999px; padding:2px 8px; font-size:11px; color:#444; }
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
    <select id="subject" title="Filter by subject">
      <option value="">All subjects</option>
    </select>
    <span class="small" id="countLabel"></span>
  </div>

  <div class="wrap">
    <div>
      <div id="list" class="list">
        {{range .Items}}
        <div class="card" data-title="{{.Title}}" data-pdf="{{.PDFURL}}" data-subject="{{.Subject}}">
          <img class="thumb" loading="lazy" src="{{.IMGURL}}" alt="thumb" onerror="this.style.display='none'">
          <div class="meta">
            <input type="checkbox" class="pick" title="Select" />
            <div style="flex:1; min-width:0">
              <div class="title" title="{{.Title}}">{{.Title}}</div>
              <div class="chips">
                {{if .Subject}}<span class="chip" title="Subject">{{.Subject}}</span>{{end}}
              </div>
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
          <input id="outname" type="text" placeholder="merged_kiddo" style="flex:1;">
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
  const subjectSel = document.getElementById('subject');
  const label = document.getElementById('countLabel');

  // Build subject dropdown from items on page
  (function initSubjects(){
    const subjects = new Set();
    Array.from(list.children).forEach(c => {
      const s = (c.getAttribute('data-subject') || '').trim();
      if (s) subjects.add(s);
    });
    const opts = Array.from(subjects).sort((a,b)=>a.localeCompare(b));
    opts.forEach(s => {
      const opt = document.createElement('option');
      opt.value = s.toLowerCase();
      opt.textContent = s;
      subjectSel.appendChild(opt);
    });
  })();

  function applyFilters() {
    const term = (q.value || '').toLowerCase();
    const subj = (subjectSel.value || '').toLowerCase();

    let shown = 0;
    Array.from(list.children).forEach(c => {
      const title = (c.getAttribute('data-title')||'').toLowerCase();
      const s = (c.getAttribute('data-subject')||'').toLowerCase();
      const matchTitle = !term || title.includes(term);
      const matchSubject = !subj || (s && (s === subj || s.includes(subj)));
      const visible = matchTitle && matchSubject;
      c.style.display = visible ? '' : 'none';
      if (visible) shown++;
    });
    label.textContent = shown + ' items';
  }

  function countShown() {
    let shown = 0;
    Array.from(list.children).forEach(c => { if (c.style.display !== 'none') shown++; });
    label.textContent = shown + ' items';
  }

  // initial count
  countShown();

  q.addEventListener('input', applyFilters);
  subjectSel.addEventListener('change', applyFilters);

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
      status.innerHTML = '✅ Done: <a href="'+data.download+'" target="_blank" rel="noreferrer">'+data.download+'</a>'
        + (data.skipped && data.skipped.length ? '<div class="muted">Skipped: '+data.skipped.length+'</div>' : '');
    } else {
      status.innerHTML = '❌ ' + (data.error || 'Merge failed');
    }
  });
</script>
</body>
</html>
`))
