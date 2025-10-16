package main

import (
	"bufio"
	"encoding/json"
	"log"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
)

func handleIndex(items []Item, outDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// sort by Title asc
		sort.Slice(items, func(i, j int) bool {
			return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title)
		})
		data := struct{ Items []Item }{Items: items}
		_ = page.Execute(w, data)
	}
}

func handleMerge(outDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in MergeRequest
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
		outPath := filepath.Join(outDir, outName)

		tmpDir, err := osMkdirTemp("", "merge_dl_*")
		if err != nil {
			http.Error(w, `{"error":"cannot create temp dir"}`, http.StatusInternalServerError)
			return
		}
		defer osRemoveAll(tmpDir)

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

		// pdfcpu Merge
		if err := pdfapi.MergeCreateFile(localFiles, outPath, false, nil); err != nil {
			// fallback for older versions
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
	}
}
