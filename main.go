package main

import (
	"flag"
	"log"
	"net/http"
)

var (
	// UI flags
	dataPath = flag.String("data", defaultData, "path to data file (JSON array or JSONL) with fields: title,pdf_url,img_url")
	addrFlag = flag.String("addr", defaultAddr, "http listen address, e.g. :8080")
	outDir   = flag.String("out", defaultOut, "output directory for merged PDFs")

	// Crawl-on-start flags
	autoCrawl   = flag.Bool("crawl", true, "run crawler before starting UI")
	cpPath      = flag.String("cp", "kiddo.checkpoint.json", "checkpoint file to resume crawl")
	startPage   = flag.Int("start", 1, "start page number (used if no checkpoint yet)")
	endPage     = flag.Int("end", 0, "end page number (0 = auto detect)")
	delayMs     = flag.Int("delay", 1200, "delay between requests in milliseconds")
	maxItemsRun = flag.Int("max", 0, "max items to collect this run (0 = unlimited)")
)

func main() {
	flag.Parse()

	// 1) (Optional) Run crawler with checkpoint
	if *autoCrawl {
		if err := RunCrawlerWithCheckpoint(CrawlConfig{
			DataPath:  *dataPath,
			CPPath:    *cpPath,
			StartPage: *startPage,
			EndPage:   *endPage,
			DelayMs:   *delayMs,
			MaxItems:  *maxItemsRun,
			UserAgent: userAgent, // từ crawler
		}); err != nil {
			log.Printf("[crawl] warning: %v", err)
		}
	}

	// 2) Load items for UI
	items, err := loadItems(*dataPath)
	if err != nil {
		log.Fatalf("load items: %v", err)
	}
	if len(items) == 0 {
		log.Printf("Warning: no items found in %s", *dataPath)
	}

	// 3) Ensure output dir
	if err := osMkdirAll(*outDir, 0o755); err != nil {
		log.Fatal(err)
	}

	// 4) Routes (UI)
	http.HandleFunc("/", handleIndex(items, *outDir))
	http.HandleFunc("/merge", handleMerge(*outDir))
	http.Handle("/download/", http.StripPrefix("/download/", http.FileServer(http.Dir(*outDir))))

	log.Printf("UI: http://localhost%s  | data=%s  | out=%s  | crawl=%v", *addrFlag, *dataPath, *outDir, *autoCrawl)
	log.Fatal(http.ListenAndServe(*addrFlag, nil))
}
